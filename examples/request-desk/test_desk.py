"""Tests for the rule, which is the part worth testing.

`desk.py` imports nothing from Toft, so every one of these constructs a
caller by hand — an address, a name, a platform role, a list of platform group
names — and asserts what the desk decides from it. That is the same shape the
app is in at runtime; the only thing missing is the HTTP call that produced the
caller.

    python3 -m unittest -v
"""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from desk import Caller, Store, normalize_email


def caller(email="", name="", role="", is_admin=False, groups=()) -> Caller:
    return Caller(email=email, name=name, role=role, is_admin=is_admin, lp_groups=tuple(groups))


class DeskTest(unittest.TestCase):
    def setUp(self) -> None:
        self.dir = tempfile.TemporaryDirectory()
        self.store = Store(Path(self.dir.name) / "desk.sqlite")
        self.store.initialize()
        self.groups = {g["name"]: g["id"] for g in self.store.groups()}

    def tearDown(self) -> None:
        self.dir.cleanup()

    # -- the shape a fresh desk starts in ----------------------------------

    def test_seeds_two_groups(self):
        self.assertEqual(sorted(self.groups), ["Approvers", "Requesters"])
        approvers = [g for g in self.store.groups() if g["name"] == "Approvers"][0]
        self.assertTrue(approvers["can_view_all"] and approvers["can_decide"])
        self.assertFalse(approvers["can_submit"])

    def test_initialize_is_idempotent(self):
        second = Store(self.store.path)
        second.initialize()
        self.assertEqual(len(second.groups()), 2)

    # -- who the platform names, and what that is worth --------------------

    def test_a_stranger_may_do_nothing(self):
        a = self.store.abilities(caller("nobody@example.com", role="viewer"))
        self.assertFalse(a.anything)
        self.assertFalse(a.known)
        self.assertIn("not in this desk's directory", " ".join(a.why))

    def test_platform_editor_administers_the_desk(self):
        a = self.store.abilities(caller("ed@example.com", role="editor"))
        self.assertTrue(a.manage and a.view_all and a.decide)
        # Never submit: filing a request is being in the directory, and an
        # administrator who wants to file one adds themselves like anybody else.
        self.assertFalse(a.submit)

    def test_install_administrator_administers_the_desk(self):
        a = self.store.abilities(caller("root@example.com", role="viewer", is_admin=True))
        self.assertTrue(a.manage)
        self.assertIn("administer this Toft install", " ".join(a.why))

    def test_platform_viewer_alone_carries_nothing(self):
        a = self.store.abilities(caller("v@example.com", role="viewer"))
        self.assertFalse(a.manage)
        self.assertFalse(a.anything)

    # -- the directory and its groups --------------------------------------

    def test_requester_may_submit_and_nothing_else(self):
        self.store.add_person("rosa@example.com", "Rosa Iqbal", "ed@example.com")
        self.store.set_membership("rosa@example.com", self.groups["Requesters"], True)
        a = self.store.abilities(caller("rosa@example.com", role="viewer"))
        self.assertTrue(a.submit)
        self.assertFalse(a.view_all or a.decide or a.manage)
        self.assertEqual(a.groups, ("Requesters",))

    def test_approver_sees_everything_and_decides(self):
        self.store.add_person("ann@example.com", "Ann Osei", "ed@example.com")
        self.store.set_membership("ann@example.com", self.groups["Approvers"], True)
        a = self.store.abilities(caller("ann@example.com", role="viewer"))
        self.assertTrue(a.view_all and a.decide)
        self.assertFalse(a.submit or a.manage)

    def test_directory_membership_alone_grants_nothing(self):
        self.store.add_person("new@example.com", "New Person", "ed@example.com")
        a = self.store.abilities(caller("new@example.com", role="viewer"))
        self.assertTrue(a.known)
        self.assertFalse(a.anything)
        self.assertIn("in no group", " ".join(a.why))

    def test_deactivating_ends_access_without_removing_anything(self):
        self.store.add_person("rosa@example.com", "Rosa", "ed@example.com")
        self.store.set_membership("rosa@example.com", self.groups["Requesters"], True)
        self.store.set_person_active("rosa@example.com", False)
        a = self.store.abilities(caller("rosa@example.com"))
        self.assertFalse(a.submit)
        self.assertTrue(a.known)
        self.assertEqual(self.store.person("rosa@example.com")["groups"], ("Requesters",))

    def test_the_address_is_matched_case_insensitively(self):
        self.store.add_person("Rosa@Example.COM ", "Rosa", "ed@example.com")
        self.store.set_membership("rosa@example.com", self.groups["Requesters"], True)
        self.assertTrue(self.store.abilities(caller("ROSA@example.com")).submit)
        self.assertEqual(normalize_email(" A@B.COM "), "a@b.com")

    def test_an_address_that_is_not_one_is_refused(self):
        with self.assertRaises(ValueError):
            self.store.add_person("rosa", "Rosa", "ed@example.com")

    def test_adding_somebody_twice_reactivates_rather_than_duplicating(self):
        self.store.add_person("rosa@example.com", "Rosa", "ed@example.com")
        self.store.set_person_active("rosa@example.com", False)
        msg = self.store.add_person("rosa@example.com", "Rosa Iqbal", "ed@example.com")
        self.assertIn("already here", msg)
        self.assertEqual(len(self.store.people()), 1)
        self.assertTrue(self.store.person("rosa@example.com")["active"])

    # -- a Toft group standing in for the directory -------------------

    def test_a_linked_toft_group_grants_membership(self):
        self.store.update_group(self.groups["Approvers"], "Finance approves.", False, True, True, "finance")
        a = self.store.abilities(caller("cfo@example.com", role="viewer", groups=("finance", "ops")))
        self.assertTrue(a.view_all and a.decide)
        self.assertIn("Toft puts you in the group 'finance'", " ".join(a.why))

    def test_a_link_is_matched_by_name_not_by_case(self):
        self.store.update_group(self.groups["Approvers"], "", False, True, True, "Finance")
        self.assertTrue(self.store.abilities(caller("cfo@example.com", groups=("FINANCE",))).decide)

    def test_a_link_does_not_let_a_stranger_submit(self):
        # Submitting is being somebody this desk knows: a request is filed
        # against a person in the directory, so the link is not a way around it.
        self.store.update_group(self.groups["Requesters"], "", True, False, False, "everyone")
        a = self.store.abilities(caller("drive-by@example.com", groups=("everyone",)))
        self.assertFalse(a.submit)
        self.assertEqual(a.groups, ("Requesters",))

    def test_an_unlinked_group_never_matches_an_empty_platform_group_list(self):
        a = self.store.abilities(caller("nobody@example.com", groups=()))
        self.assertEqual(a.groups, ())

    # -- no address at all --------------------------------------------------

    def test_without_an_address_nothing_can_be_matched(self):
        a = self.store.abilities(caller("", name="Anon", role="viewer"))
        self.assertFalse(a.known or a.submit)
        self.assertIn("did not give this app an email address", " ".join(a.why))

    def test_an_administrator_without_an_address_still_administers(self):
        a = self.store.abilities(caller("", role="owner"))
        self.assertTrue(a.manage and a.view_all and a.decide)

    # -- requests -----------------------------------------------------------

    def _requester(self) -> Caller:
        self.store.add_person("rosa@example.com", "Rosa Iqbal", "ed@example.com")
        self.store.set_membership("rosa@example.com", self.groups["Requesters"], True)
        return caller("rosa@example.com", "Rosa Iqbal", role="viewer")

    def _approver(self) -> Caller:
        self.store.add_person("ann@example.com", "Ann Osei", "ed@example.com")
        self.store.set_membership("ann@example.com", self.groups["Approvers"], True)
        return caller("ann@example.com", "Ann Osei", role="viewer")

    def test_a_request_carries_its_requester_and_an_event(self):
        rosa = self._requester()
        rid = self.store.submit(rosa, "A second monitor", "Equipment", "For the support desk", "240")
        row = self.store.request(rid)
        self.assertEqual(row["status"], "pending")
        self.assertEqual(row["requester_email"], "rosa@example.com")
        self.assertEqual([e["action"] for e in row["events"]], ["submitted"])

    def test_a_request_needs_a_title(self):
        with self.assertRaises(ValueError):
            self.store.submit(self._requester(), "  ", "", "", "")

    def test_deciding_twice_is_refused(self):
        rid = self.store.submit(self._requester(), "Monitor", "", "", "")
        ann = self._approver()
        self.store.decide(ann, rid, "approved", "Fine.")
        with self.assertRaises(ValueError):
            self.store.decide(ann, rid, "rejected", "Changed my mind.")
        row = self.store.request(rid)
        self.assertEqual(row["status"], "approved")
        self.assertEqual(row["decided_by"], "ann@example.com")
        self.assertEqual([e["action"] for e in row["events"]], ["submitted", "approved"])

    def test_a_verdict_that_is_not_one_is_refused(self):
        rid = self.store.submit(self._requester(), "Monitor", "", "", "")
        with self.assertRaises(ValueError):
            self.store.decide(self._approver(), rid, "maybe", "")

    def test_only_the_requester_withdraws_and_only_while_pending(self):
        rosa = self._requester()
        ann = self._approver()
        rid = self.store.submit(rosa, "Monitor", "", "", "")
        with self.assertRaises(ValueError):
            self.store.withdraw(ann, rid)
        self.store.withdraw(rosa, rid)
        self.assertEqual(self.store.request(rid)["status"], "withdrawn")
        with self.assertRaises(ValueError):
            self.store.withdraw(rosa, rid)

    def test_a_withdrawal_names_no_decider(self):
        rosa = self._requester()
        rid = self.store.submit(rosa, "Monitor", "", "", "")
        self.store.withdraw(rosa, rid)
        self.assertIsNone(self.store.request(rid)["decided_by"])

    def test_listing_is_scoped_by_requester_and_by_status(self):
        rosa = self._requester()
        ann = self._approver()
        first = self.store.submit(rosa, "Monitor", "", "", "")
        self.store.submit(ann, "Conference", "", "", "")
        self.store.decide(ann, first, "approved", "")
        self.assertEqual([r["id"] for r in self.store.requests(requester="rosa@example.com")], [first])
        self.assertEqual(len(self.store.requests(status="pending")), 1)
        self.assertEqual(self.store.counts()["approved"], 1)

    def test_pending_sorts_above_everything_decided(self):
        rosa = self._requester()
        ann = self._approver()
        old = self.store.submit(rosa, "Old", "", "", "")
        self.store.decide(ann, old, "approved", "")
        fresh = self.store.submit(rosa, "Fresh", "", "", "")
        self.assertEqual([r["id"] for r in self.store.requests()][0], fresh)

    def test_removing_a_person_keeps_what_they_filed(self):
        rosa = self._requester()
        rid = self.store.submit(rosa, "Monitor", "", "", "")
        self.store.remove_person("rosa@example.com")
        self.assertIsNone(self.store.person("rosa@example.com"))
        self.assertEqual(self.store.request(rid)["requester_email"], "rosa@example.com")
        self.assertFalse(self.store.abilities(rosa).anything)

    def test_deleting_a_group_takes_its_grant_with_it(self):
        rosa = self._requester()
        self.store.delete_group(self.groups["Requesters"])
        self.assertFalse(self.store.abilities(rosa).submit)

    def test_a_group_name_is_unique(self):
        with self.assertRaises(ValueError):
            self.store.create_group("Approvers", "", False, True, True)

    def test_the_platform_name_updates_the_directory_row(self):
        self.store.add_person("rosa@example.com", "R.", "ed@example.com")
        self.store.update_name("rosa@example.com", "Rosa Iqbal")
        self.assertEqual(self.store.person("rosa@example.com")["name"], "Rosa Iqbal")
        self.store.update_name("rosa@example.com", "   ")
        self.assertEqual(self.store.person("rosa@example.com")["name"], "Rosa Iqbal")


if __name__ == "__main__":
    unittest.main()
