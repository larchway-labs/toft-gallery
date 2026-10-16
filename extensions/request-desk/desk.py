"""The desk itself: an embedded SQLite database and the rule that reads it.

This module knows nothing about Toft and imports nothing from it. It takes
a :class:`Caller` — an address, a name, a role and a list of directory group
names — and answers what that person may do here. That separation is the point
of the app rather than a tidiness preference: the platform is the authority on
*who is looking*, and this file is the authority on *what that person may do*,
and neither can quietly become the other.

It is also what makes the rule testable without a running install. `test_desk.py`
constructs callers by hand.

**Everything is keyed on the email address**, lowercased and stripped. An
editor adds a person by address and name; the platform later says an address;
the two meet here. A person whose address is not in the directory is not
refused rudely — they are simply nobody this desk knows, which is a state the
page can explain and an editor can fix in one field.
"""

from __future__ import annotations

import sqlite3
import threading
from contextlib import contextmanager
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path

# The four states a request can be in. `withdrawn` is the requester's own exit
# and is deliberately not a decision: it carries no decider and no note, so the
# queue can say "nobody refused this, they took it back".
STATUSES = ("pending", "approved", "rejected", "withdrawn")
OPEN_STATUS = "pending"

SCHEMA = """
CREATE TABLE IF NOT EXISTS people (
    email      TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    active     INTEGER NOT NULL DEFAULT 1,
    added_by   TEXT NOT NULL DEFAULT '',
    added_at   TEXT NOT NULL
);

-- `groups` is a keyword in modern SQLite (window frames), so the table is not
-- called that. The word the app uses on screen is still "group".
CREATE TABLE IF NOT EXISTS desk_groups (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,
    description  TEXT NOT NULL DEFAULT '',
    can_submit   INTEGER NOT NULL DEFAULT 0,
    can_view_all INTEGER NOT NULL DEFAULT 0,
    can_decide   INTEGER NOT NULL DEFAULT 0,
    -- Optional: the name of a Toft group. Anyone the platform says is in
    -- it lands in this desk group without an editor adding them by hand.
    lp_group     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS memberships (
    email    TEXT NOT NULL REFERENCES people(email) ON DELETE CASCADE,
    group_id INTEGER NOT NULL REFERENCES desk_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (email, group_id)
);

CREATE TABLE IF NOT EXISTS requests (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    title           TEXT NOT NULL,
    category        TEXT NOT NULL DEFAULT '',
    details         TEXT NOT NULL DEFAULT '',
    amount          TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending',
    requester_email TEXT NOT NULL,
    requester_name  TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    decided_at      TEXT,
    decided_by      TEXT,
    decision_note   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS requests_by_requester ON requests(requester_email, id DESC);
CREATE INDEX IF NOT EXISTS requests_by_status    ON requests(status, id DESC);

-- Who did what, and when. A decision that cannot be accounted for is worth
-- less than no decision at all, and the row above only remembers the last one.
CREATE TABLE IF NOT EXISTS events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id INTEGER NOT NULL,
    at         TEXT NOT NULL,
    actor      TEXT NOT NULL,
    action     TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS events_by_request ON events(request_id, id);
"""

# What a fresh database starts with: the two groups the desk is designed
# around. They are ordinary rows — an editor can rename them, change what they
# grant, add a third, or delete both — and seeding them only means nobody has
# to invent the shape of the thing before using it.
SEED_GROUPS = (
    ("Approvers", "Sees every request and decides on it.", 0, 1, 1),
    ("Requesters", "Submits requests and sees their own.", 1, 0, 0),
)


def now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def normalize_email(value: str) -> str:
    return (value or "").strip().lower()


@dataclass(frozen=True)
class Caller:
    """A person as this app needs them, assembled from whatever the host said.

    ``role`` and ``is_admin`` are Toft's answer about *this deployment* —
    may this person redeploy the app, edit its jobs. That is not domain
    authority and the desk does not use it as one: it uses it for exactly one
    thing, which is deciding who administers the desk, because somebody has to
    be able to add the first person to an empty directory.

    ``lp_groups`` is where domain authority belongs, and a desk group can be
    linked to one.
    """

    email: str = ""
    name: str = ""
    role: str = ""
    is_admin: bool = False
    lp_groups: tuple[str, ...] = ()

    def normalized(self) -> "Caller":
        return Caller(
            email=normalize_email(self.email),
            name=(self.name or "").strip(),
            role=(self.role or "").strip().lower(),
            is_admin=bool(self.is_admin),
            lp_groups=tuple(g for g in self.lp_groups if g),
        )


@dataclass(frozen=True)
class Abilities:
    """What the caller may do, and the sentences that say why.

    ``why`` is not decoration. This app exists to demonstrate an authorization
    model, and a model whose verdict you cannot account for is one nobody will
    trust in their own app. Every ability that is granted names what granted
    it, and every one that is withheld names what would.
    """

    known: bool = False
    active: bool = False
    manage: bool = False
    submit: bool = False
    view_all: bool = False
    decide: bool = False
    groups: tuple[str, ...] = ()
    why: tuple[str, ...] = ()

    @property
    def anything(self) -> bool:
        return self.manage or self.submit or self.view_all or self.decide


class Store:
    """The SQLite file, its schema, and every query the app makes.

    One connection per call, which is the boring and correct thing for a file
    database behind a web server: connections are cheap, WAL lets a reader run
    while a writer commits, and `busy_timeout` covers the rest. The lock below
    guards initialization only.
    """

    def __init__(self, path: str | Path) -> None:
        self.path = str(path)
        self._init_lock = threading.Lock()
        self._ready = False

    # ---- plumbing ---------------------------------------------------------

    @contextmanager
    def connect(self):
        conn = sqlite3.connect(self.path, timeout=10.0)
        conn.row_factory = sqlite3.Row
        try:
            conn.execute("PRAGMA foreign_keys = ON")
            conn.execute("PRAGMA busy_timeout = 10000")
            yield conn
            conn.commit()
        except Exception:
            conn.rollback()
            raise
        finally:
            conn.close()

    def initialize(self) -> None:
        """Create the schema and seed the two groups, once, idempotently."""
        with self._init_lock:
            if self._ready:
                return
            Path(self.path).parent.mkdir(parents=True, exist_ok=True)
            with self.connect() as conn:
                # WAL is what makes a reader and a writer coexist, and it is
                # worth having on a network filesystem in particular: a volume
                # is where this file belongs, and EFS charges for every fsync.
                conn.execute("PRAGMA journal_mode = WAL")
                conn.executescript(SCHEMA)
                seeded = conn.execute("SELECT COUNT(*) FROM desk_groups").fetchone()[0]
                if not seeded:
                    conn.executemany(
                        "INSERT INTO desk_groups (name, description, can_submit, can_view_all, can_decide)"
                        " VALUES (?, ?, ?, ?, ?)",
                        SEED_GROUPS,
                    )
            self._ready = True

    # ---- the directory ----------------------------------------------------

    def people(self) -> list[dict]:
        with self.connect() as conn:
            rows = conn.execute(
                "SELECT p.*, ("
                "  SELECT COUNT(*) FROM requests r WHERE r.requester_email = p.email"
                ") AS request_count FROM people p ORDER BY p.name = '', p.name, p.email"
            ).fetchall()
            groups = self._membership_map(conn)
        return [dict(r, groups=tuple(groups.get(r["email"], ()))) for r in rows]

    def person(self, email: str) -> dict | None:
        email = normalize_email(email)
        with self.connect() as conn:
            row = conn.execute("SELECT * FROM people WHERE email = ?", (email,)).fetchone()
            if row is None:
                return None
            groups = self._membership_map(conn).get(email, ())
        return dict(row, groups=tuple(groups))

    @staticmethod
    def _membership_map(conn) -> dict[str, list[str]]:
        out: dict[str, list[str]] = {}
        for row in conn.execute(
            "SELECT m.email, g.name FROM memberships m"
            " JOIN desk_groups g ON g.id = m.group_id ORDER BY g.name"
        ):
            out.setdefault(row["email"], []).append(row["name"])
        return out

    def add_person(self, email: str, name: str, added_by: str) -> str:
        """Add or re-activate one person. Returns the sentence for the toast."""
        email = normalize_email(email)
        name = (name or "").strip()
        if "@" not in email or email.startswith("@") or email.endswith("@"):
            raise ValueError(f"{email or 'that'} is not an email address, and the address is the identity here")
        with self.connect() as conn:
            existing = conn.execute("SELECT * FROM people WHERE email = ?", (email,)).fetchone()
            if existing is None:
                conn.execute(
                    "INSERT INTO people (email, name, active, added_by, added_at) VALUES (?, ?, 1, ?, ?)",
                    (email, name, added_by, now()),
                )
                return f"{email} added to the directory."
            conn.execute(
                "UPDATE people SET name = ?, active = 1 WHERE email = ?",
                (name or existing["name"], email),
            )
            return f"{email} was already here — updated and active."

    def set_person_active(self, email: str, active: bool) -> None:
        with self.connect() as conn:
            conn.execute(
                "UPDATE people SET active = ? WHERE email = ?", (1 if active else 0, normalize_email(email))
            )

    def remove_person(self, email: str) -> None:
        """Delete a person and their memberships. Their requests stay.

        The rows carry the address rather than a foreign key for exactly this
        reason: removing somebody from the directory ends their access, and it
        is not a license to rewrite the history of what was decided.
        """
        with self.connect() as conn:
            conn.execute("DELETE FROM people WHERE email = ?", (normalize_email(email),))

    def update_name(self, email: str, name: str) -> None:
        """Record the display name the platform gave for an address we know.

        The editor typed a name when they added the person; the platform knows
        the one that person actually signs in under. The second is better, and
        this is the only place the app writes without being asked to.
        """
        name = (name or "").strip()
        if not name:
            return
        with self.connect() as conn:
            conn.execute(
                "UPDATE people SET name = ? WHERE email = ? AND name <> ?",
                (name, normalize_email(email), name),
            )

    # ---- groups -----------------------------------------------------------

    def groups(self) -> list[dict]:
        with self.connect() as conn:
            rows = conn.execute(
                "SELECT g.*, (SELECT COUNT(*) FROM memberships m WHERE m.group_id = g.id) AS member_count"
                " FROM desk_groups g ORDER BY g.name"
            ).fetchall()
        return [dict(r) for r in rows]

    def create_group(self, name: str, description: str, can_submit: bool, can_view_all: bool,
                     can_decide: bool, lp_group: str = "") -> str:
        name = (name or "").strip()
        if not name:
            raise ValueError("a group needs a name")
        with self.connect() as conn:
            try:
                conn.execute(
                    "INSERT INTO desk_groups (name, description, can_submit, can_view_all, can_decide, lp_group)"
                    " VALUES (?, ?, ?, ?, ?, ?)",
                    (name, (description or "").strip(), int(can_submit), int(can_view_all),
                     int(can_decide), (lp_group or "").strip()),
                )
            except sqlite3.IntegrityError:
                raise ValueError(f"there is already a group called {name}") from None
        return f"Group {name} created."

    def update_group(self, group_id: int, description: str, can_submit: bool, can_view_all: bool,
                     can_decide: bool, lp_group: str) -> None:
        with self.connect() as conn:
            conn.execute(
                "UPDATE desk_groups SET description = ?, can_submit = ?, can_view_all = ?,"
                " can_decide = ?, lp_group = ? WHERE id = ?",
                ((description or "").strip(), int(can_submit), int(can_view_all), int(can_decide),
                 (lp_group or "").strip(), group_id),
            )

    def delete_group(self, group_id: int) -> None:
        with self.connect() as conn:
            conn.execute("DELETE FROM desk_groups WHERE id = ?", (group_id,))

    def set_membership(self, email: str, group_id: int, member: bool) -> None:
        email = normalize_email(email)
        with self.connect() as conn:
            if member:
                conn.execute(
                    "INSERT OR IGNORE INTO memberships (email, group_id) VALUES (?, ?)", (email, group_id)
                )
            else:
                conn.execute(
                    "DELETE FROM memberships WHERE email = ? AND group_id = ?", (email, group_id)
                )

    # ---- the rule ---------------------------------------------------------

    def abilities(self, caller: Caller) -> Abilities:
        """What this caller may do here, and why.

        Three sources, in the order they are considered:

        1. **Toft's role on the app** — owner, editor, or an administrator
           of the install. Those people administer the desk. Nothing else in
           this app uses the role, because "may redeploy this app" is not
           "may approve an expense".
        2. **A desk group an editor put them in.** The ordinary path.
        3. **A desk group linked to a Toft group they are in.** The same
           thing, with the directory doing the bookkeeping.
        """
        caller = caller.normalized()
        why: list[str] = []

        manage = caller.role in ("owner", "editor") or caller.is_admin
        if caller.is_admin and caller.role not in ("owner", "editor"):
            why.append("You administer this Toft install, so you administer this desk.")
        elif manage:
            why.append(f"Toft says your role on this app is {caller.role}, so you administer this desk.")

        if not caller.email:
            why.append("Toft did not give this app an email address, and the address is how this desk "
                       "recognises people. Nothing here can be matched to a person.")
            return Abilities(manage=manage, view_all=manage, decide=manage, why=tuple(why))

        with self.connect() as conn:
            person = conn.execute("SELECT * FROM people WHERE email = ?", (caller.email,)).fetchone()
            group_rows = conn.execute("SELECT * FROM desk_groups ORDER BY name").fetchall()
            explicit = {
                r["group_id"]
                for r in conn.execute("SELECT group_id FROM memberships WHERE email = ?", (caller.email,))
            }

        known = person is not None
        active = bool(person["active"]) if person is not None else False
        lp_names = {g.lower() for g in caller.lp_groups}

        in_groups: list[str] = []
        submit = view_all = decide = False
        for g in group_rows:
            by_membership = known and active and g["id"] in explicit
            linked = (g["lp_group"] or "").strip().lower()
            by_link = bool(linked) and linked in lp_names
            if not (by_membership or by_link):
                continue
            in_groups.append(g["name"])
            grants = [w for w, on in (("submit", g["can_submit"]), ("see every request", g["can_view_all"]),
                                      ("decide", g["can_decide"])) if on]
            granted = ", ".join(grants) if grants else "nothing yet"
            if by_membership:
                why.append(f"You are in the desk group {g['name']}, which grants {granted}.")
            else:
                why.append(
                    f"Toft puts you in the group '{g['lp_group']}', which the desk group "
                    f"{g['name']} is linked to. It grants {granted}."
                )
            submit = submit or bool(g["can_submit"])
            view_all = view_all or bool(g["can_view_all"])
            decide = decide or bool(g["can_decide"])

        if not known:
            why.append(f"{caller.email} is not in this desk's directory, so you cannot submit a request. "
                       "An editor adds an address on the People page.")
        elif not active:
            why.append(f"{caller.email} is in the directory but deactivated, so its group memberships "
                       "grant nothing.")
        elif not in_groups:
            why.append("You are in the directory but in no group, so there is nothing you may do yet.")

        # An administrator can always read the queue — otherwise the person who
        # sets the desk up cannot see whether they set it up correctly. They are
        # deliberately *not* given `submit`: submitting is being in the
        # directory, and an administrator who wants to file a request adds
        # themselves like anybody else.
        if manage:
            view_all = True
            decide = True

        return Abilities(
            known=known,
            active=active,
            manage=manage,
            submit=submit and known and active,
            view_all=view_all,
            decide=decide,
            groups=tuple(in_groups),
            why=tuple(why),
        )

    # ---- requests ---------------------------------------------------------

    def submit(self, caller: Caller, title: str, category: str, details: str, amount: str) -> int:
        caller = caller.normalized()
        title = (title or "").strip()
        if not title:
            raise ValueError("a request needs a title")
        with self.connect() as conn:
            cur = conn.execute(
                "INSERT INTO requests (title, category, details, amount, status, requester_email,"
                " requester_name, created_at) VALUES (?, ?, ?, ?, 'pending', ?, ?, ?)",
                (title[:200], (category or "").strip()[:80], (details or "").strip()[:4000],
                 (amount or "").strip()[:40], caller.email, caller.name, now()),
            )
            rid = int(cur.lastrowid)
            conn.execute(
                "INSERT INTO events (request_id, at, actor, action, note) VALUES (?, ?, ?, 'submitted', '')",
                (rid, now(), caller.email),
            )
        return rid

    def requests(self, requester: str = "", status: str = "") -> list[dict]:
        clauses, params = [], []
        if requester:
            clauses.append("requester_email = ?")
            params.append(normalize_email(requester))
        if status:
            clauses.append("status = ?")
            params.append(status)
        where = (" WHERE " + " AND ".join(clauses)) if clauses else ""
        with self.connect() as conn:
            rows = conn.execute(
                f"SELECT * FROM requests{where} ORDER BY status = 'pending' DESC, id DESC", params
            ).fetchall()
        return [dict(r) for r in rows]

    def request(self, request_id: int) -> dict | None:
        with self.connect() as conn:
            row = conn.execute("SELECT * FROM requests WHERE id = ?", (request_id,)).fetchone()
            if row is None:
                return None
            events = [dict(e) for e in conn.execute(
                "SELECT * FROM events WHERE request_id = ? ORDER BY id", (request_id,)
            )]
        return dict(row, events=events)

    def counts(self) -> dict:
        with self.connect() as conn:
            rows = conn.execute("SELECT status, COUNT(*) AS n FROM requests GROUP BY status").fetchall()
        out = {s: 0 for s in STATUSES}
        for r in rows:
            out[r["status"]] = r["n"]
        return out

    def decide(self, caller: Caller, request_id: int, verdict: str, note: str) -> str:
        """Approve or reject a pending request.

        The status check is inside the UPDATE rather than beside it, so two
        approvers clicking at once produce one decision and one of them is told
        the request had already moved.
        """
        if verdict not in ("approved", "rejected"):
            raise ValueError(f"{verdict} is not a decision")
        caller = caller.normalized()
        with self.connect() as conn:
            cur = conn.execute(
                "UPDATE requests SET status = ?, decided_at = ?, decided_by = ?, decision_note = ?"
                " WHERE id = ? AND status = 'pending'",
                (verdict, now(), caller.email, (note or "").strip()[:1000], request_id),
            )
            if cur.rowcount == 0:
                raise ValueError(f"request #{request_id} is not pending — somebody already decided it")
            conn.execute(
                "INSERT INTO events (request_id, at, actor, action, note) VALUES (?, ?, ?, ?, ?)",
                (request_id, now(), caller.email, verdict, (note or "").strip()[:1000]),
            )
        return f"Request #{request_id} {verdict}."

    def withdraw(self, caller: Caller, request_id: int) -> str:
        """The requester's own exit, and only from `pending`, and only theirs."""
        caller = caller.normalized()
        with self.connect() as conn:
            cur = conn.execute(
                "UPDATE requests SET status = 'withdrawn' WHERE id = ? AND status = 'pending'"
                " AND requester_email = ?",
                (request_id, caller.email),
            )
            if cur.rowcount == 0:
                raise ValueError(f"request #{request_id} is not yours to withdraw, or is no longer pending")
            conn.execute(
                "INSERT INTO events (request_id, at, actor, action, note) VALUES (?, ?, ?, 'withdrawn', '')",
                (request_id, now(), caller.email),
            )
        return f"Request #{request_id} withdrawn."
