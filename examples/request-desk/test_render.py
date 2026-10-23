"""Tests for the two path rules, which are the ones easy to get backwards.

A link on a page carries the mount prefix; a `Location` header must not,
because Toft's proxy puts the prefix back on every redirect it forwards.
Getting that the wrong way round sends the browser to
`/apps/request-desk/apps/request-desk/people`, which is how this test came to
exist.
"""

from __future__ import annotations

import unittest

import render
from desk import Abilities, Caller


class RedirectTargetTest(unittest.TestCase):
    def test_a_bare_redirect_is_the_app_root(self):
        self.assertEqual(render.redirect_target(), "/")

    def test_a_target_is_never_prefixed(self):
        self.assertEqual(render.redirect_target("people"), "/people")
        self.assertEqual(render.redirect_target("/people"), "/people")

    def test_the_sentence_rides_on_the_query_string(self):
        self.assertEqual(
            render.redirect_target("people", "rosa@example.com added to the directory.", "good"),
            "/people?msg=rosa%40example.com+added+to+the+directory.&kind=good",
        )

    def test_no_message_means_no_query_string(self):
        self.assertEqual(render.redirect_target("groups"), "/groups")


class LinkTest(unittest.TestCase):
    """The other half of the rule: everything rendered into HTML *is* prefixed."""

    BASE = "/apps/request-desk"

    def test_navigation_links_carry_the_prefix(self):
        html = render.page(self.BASE, "Desk", "Desk", "", "<p>hi</p>",
                           abilities=Abilities(manage=True, view_all=True))
        self.assertIn(f'href="{self.BASE}/people"', html)
        self.assertIn(f'href="{self.BASE}/queue"', html)

    def test_form_actions_carry_the_prefix(self):
        self.assertIn(f'action="{self.BASE}/requests"', render.submit_form(self.BASE))
        self.assertIn(f'action="{self.BASE}/people"', render.people_page(self.BASE, [], []))
        self.assertIn(f'action="{self.BASE}/groups"', render.groups_page(self.BASE, []))

    def test_the_navigation_hides_what_the_caller_may_not_do(self):
        html = render.page(self.BASE, "Desk", "Desk", "", "", abilities=Abilities())
        self.assertNotIn("/people", html.split("<nav>")[1].split("</nav>")[0])
        self.assertNotIn("/queue", html.split("<nav>")[1].split("</nav>")[0])
        self.assertIn("/access", html)

    def test_a_name_with_markup_in_it_is_escaped(self):
        card = render.identity_card(
            Caller(email="x@example.com", name="<script>alert(1)</script>"),
            Abilities(why=("<b>because</b>",)), source="src",
        )
        self.assertNotIn("<script>", card)
        self.assertIn("&lt;script&gt;", card)
        self.assertNotIn("<b>because</b>", card)


if __name__ == "__main__":
    unittest.main()
