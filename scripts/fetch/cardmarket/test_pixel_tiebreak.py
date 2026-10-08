# scripts/fetch/cardmarket/test_pixel_tiebreak.py
"""python -m unittest test_pixel_tiebreak (from scripts/fetch/cardmarket)."""
import unittest

import pixel_tiebreak as pt


class DecideTest(unittest.TestCase):
    # scored: [(correlation, blueprint id, [products]), ...] best first
    def test_a_clear_lead_settles(self):
        status, best = pt.decide([(0.88, 1, [10, 11]), (0.31, 2, [12])], 0.7, 0.15)
        self.assertEqual(status, "settled")
        self.assertEqual(best, (0.88, 1, [10, 11]))

    def test_a_close_second_is_tied(self):
        status, best = pt.decide([(0.87, 1, [10]), (0.80, 2, [12])], 0.7, 0.15)
        self.assertEqual(status, "tied")
        self.assertIsNone(best)

    def test_nothing_resembling_ours_is_low(self):
        status, best = pt.decide([(0.62, 1, [10]), (0.20, 2, [12])], 0.7, 0.15)
        self.assertEqual(status, "low")
        self.assertIsNone(best)

    def test_a_single_candidate_still_needs_the_floor(self):
        self.assertEqual(pt.decide([(0.95, 1, [10])], 0.7, 0.15)[0], "settled")
        self.assertEqual(pt.decide([(0.5, 1, [10])], 0.7, 0.15)[0], "low")
        self.assertEqual(pt.decide([], 0.7, 0.15)[0], "no image")


class TwinRuleTest(unittest.TestCase):
    def test_two_ids_of_one_pack_on_one_printing_are_both_dropped(self):
        picks = {"OP01-006_p3": 7, "OP01-006_p4": 7, "OP01-006_p5": 8, "OP02-001_p1": 7}
        pack_of = {"OP01-006_p3": "PRB-01", "OP01-006_p4": "PRB-01", "OP01-006_p5": "PRB-01", "OP02-001_p1": "OP-02"}
        kept, twins = pt.twin_rule(picks, pack_of)
        self.assertEqual(kept, {"OP01-006_p5": 8, "OP02-001_p1": 7})
        self.assertEqual(twins, ["OP01-006_p3", "OP01-006_p4"])

    def test_a_reprint_and_a_parallel_may_share_nothing_but_the_base(self):
        # Different kinds (_r vs _p) never collide: rule C told them apart.
        kept, twins = pt.twin_rule({"EB01-009_r1": 3, "EB01-009_p1": 3}, {"EB01-009_r1": "PRB-02", "EB01-009_p1": "PRB-02"})
        self.assertEqual(twins, [])
        self.assertEqual(len(kept), 2)


if __name__ == "__main__":
    unittest.main()
