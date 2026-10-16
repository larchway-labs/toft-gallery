# Request Desk

An internal request desk — somebody asks for a monitor, a conference ticket, a
license; somebody else approves or refuses it — and a demonstration of the thing
that makes it possible: **an app deciding what you may do from the identity
Toft hands it.**

FastAPI, and the standard library's `sqlite3` for an embedded database. No
JavaScript framework, no build step, no external asset, and no dependency
beyond the two lines in `requirements.txt`.

## The seam

Toft answers exactly one question, and answers it in a way the app cannot
forge. A signed-in person's request arrives carrying `X-Toft-Act-For`, a
blob the platform signed over `(user, app, session)`; the app relays it back to
`GET /api/v1/app/viewer` with its own token, and the platform resolves it
against the real principal. What comes back is an address, a name, the person's
role *on this deployment*, whether they administer the install, and every
Toft group they are in.

Everything after that is this app's own business:

```
Toft said                          the desk decided
──────────────                          ────────────────
email  rosa@example.com        ──▶  a row in the directory
name   Rosa Iqbal                   ──▶  the desk groups she is in
role   viewer                            ──▶  submit / see everything / decide
groups finance, support
```

The `Access` page renders both halves and the sentences that connect them, for
whoever is looking at it. `GET /access.json` is the same thing as JSON. That
page is the app: everything else is a request desk built on top of it.

## Three sources of authority, deliberately not one

**The platform's role on the app** — `owner`, `editor`, or an administrator of
the install — is used for exactly one thing: administering the desk. Somebody
has to be able to add the first person to an empty directory, and that somebody
is whoever Toft already trusts with the deployment. It is never used to
approve a request, because *may redeploy this app* is not *may approve an
expense*, and an app that conflates them has invented an authority nobody
granted.

**A desk group an editor put you in** is the ordinary path. A group is a name
and three switches — submit, see every request, decide — and it is an ordinary
row: rename it, change what it grants, add a fifth, delete both of the ones
that ship.

**A desk group linked to a Toft group** is the same thing with the platform
doing the bookkeeping. Put `finance` in the link field on the Approvers group,
and everybody Toft puts in `finance` decides here, with nobody added by
hand.

A fresh database starts with two groups, which is the shape the app is designed
around:

| Group | submit | see every request | decide |
|---|---|---|---|
| **Requesters** | ✓ | | |
| **Approvers** | | ✓ | ✓ |

**Submitting requires a directory row.** A group link can make somebody an
approver without an editor touching the directory, but a request is filed
*against a person this desk knows*, so the address has to be in the directory
and active. That is the one asymmetry in the model and it is on purpose.

## What each person sees

| | My desk | Queue | People | Groups |
|---|---|---|---|---|
| Not in the directory, no group | identity, and what would grant something | — | — | — |
| Requesters | their own requests, and the form | — | — | — |
| Approvers | their own | everything, with Approve and Reject | — | — |
| Toft `owner`/`editor`/admin | their own | everything | ✓ | ✓ |

The navigation hides what you may not do. Every route also **refuses** it, and
every mutation re-resolves the identity before it writes — the hiding is
courtesy, the refusal is the enforcement.

## Requirements

**Viewer identity must be switched on for this app.** It is off by default: a
`system_admin` turns it on, and until they do this app is told nothing and says
so on the page rather than looking broken.

**The app must not be public.** Toft withholds the email address from an
app whose visibility is `public` — a public app's visitors did not consent to
being on a list by visiting — and the address is how this desk recognises
people. With no address the app explains that, and changing the visibility is
the whole fix.

**A volume, if the data is meant to survive.** The SQLite file goes wherever
`DB_PATH` says; failing that, on the first writable volume mapped to the app;
failing that, beside the app on the workload's own filesystem, where a restart
loses it. The page footer always says which of the three it is — beside the
version this tree is, since an extension is updated in place and "which release
am I looking at" is the first question after a redeploy. Storage means
isolated mode — shared mode refuses a storage mapping at deploy time, by design.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `DESK_TITLE` | no | `Request Desk` | Heading on every page. |
| `DB_PATH` | no | *(a mapped volume, else beside the app)* | Where the SQLite file lives. |
| `DEV_IDENTITY` | no | *(unset)* | Local development only — see below. |

`PORT`, `HOST`, `BASE_PATH` and `TOFT_APP_TOKEN` come from the platform.

## Routes

| Path | What |
|---|---|
| `GET /` | My desk: the identity, what it grants, your requests, the form |
| `GET /queue` | Every request, filterable by status — needs *see every request* |
| `GET /people` | The directory — needs *administers the desk* |
| `GET /groups` | The groups and what they grant — needs *administers the desk* |
| `GET /access` · `GET /access.json` | The identity and the decision, rendered and as JSON |
| `GET /healthz` | Opens the database and returns `ok`. No identity needed |
| `POST /requests` · `/requests/{id}/decide` · `/requests/{id}/withdraw` | Submit, decide, withdraw |
| `POST /people` · `/people/{email}/active` · `/people/{email}/delete` · `/people/{email}/groups` | The directory |
| `POST /groups` · `/groups/{id}` · `/groups/{id}/delete` | The groups |

## The files

| File | What is in it |
|---|---|
| `desk.py` | The SQLite schema, every query, and **the rule**. Imports nothing from Toft |
| `main.py` | The routes, and the one place the platform is asked who is looking |
| `render.py` | Every page, as strings |
| `test_desk.py` | 31 tests over the rule, constructing callers by hand |
| `test_render.py` | 8 more over the two path rules below, which are easy to get backwards |

### Links carry the prefix; redirects must not

Toft starts uvicorn with `--root-path /apps/{slug}` and its proxy *strips*
that prefix before forwarding, so the app is mounted at `/` and builds its own
links back up from `root_path`. The same proxy puts the prefix back on any
`Location` header coming the other way — which is what makes an ordinary
framework redirect work without the framework knowing where it lives.

So the two are spelled differently, and a page that spells them the same sends
the browser to `/apps/request-desk/apps/request-desk/people`:

| | Carries the prefix |
|---|---|
| `href=` and `action=` in the HTML | **yes** — `render` builds them from `base` |
| the `Location` of a 303 after a POST | **no** — `render.redirect_target` never adds it |

`desk.py` knowing nothing about Toft is the point rather than a tidiness
preference: the platform is the authority on *who is looking*, this file is the
authority on *what that person may do*, and neither can quietly become the
other. It is also what lets the rule be tested without a running install.

```
python3 -m unittest -v
```

## Running it locally

The Toft SDK is installed into the app by the platform's build, so it is
never declared as a dependency — and off-platform the import simply fails,
which the app renders as a state rather than a crash. To work on the pages:

```bash
pip install -r requirements.txt
DEV_IDENTITY="rosa@example.com|Rosa Iqbal|editor||finance,support" python main.py
```

The fields are `email|name|role|admin|groups`. It is **read only when there is
no app token**, so setting it on a deployed app does nothing: an app that could
be told who its viewer is by an environment variable would have no identity
model at all, only a habit.

## What it deliberately does not do

- **No notifications.** An approval could email the requester through
  `toft.notify.email` — the desk does not, because that would need an
  administrator to attach email to the app and this app is about one mechanism.
- **No attachments and no per-request comments.** A decision carries one note.
- **No pagination.** A desk with ten thousand requests is a different app.
- **No caching of its own.** The SDK holds a resolved identity for 60 seconds,
  which means a group change takes effect here within a minute. Where the
  *platform* enforces something that window does not exist; where this app
  enforces, this app is the enforcer and the window is stated rather than
  hidden.
