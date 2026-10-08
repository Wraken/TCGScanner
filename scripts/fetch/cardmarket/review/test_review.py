"""Tests for the review tool's pure parts: python -m unittest (from this dir)."""

import json
import tempfile
import unittest
from pathlib import Path

import review

REPORT = """onepiece  catalog of 2026-10-07
ambiguous  OP05-119_p1      candidates [1 2 3 4]
ambiguous  OP05-119_p2      candidates [1 2 3 4]
unresolved OP05-120
ambiguous  sfd-001-221      candidates [7 8]
"""

EXPS = [
    {"id": 100, "game_id": 15, "code": "op05", "name": "Awakening of the New Era"},
    {"id": 101, "game_id": 15, "code": "promo", "name": "One Piece Promos"},
]


def bp(id, exp, number, version, *cm, image="https://x/{}.jpg"):
    return {
        "id": id,
        "expansion_id": exp,
        "version": version,
        "name": "Monkey.D.Luffy",
        "image_url": image.format(id),
        "card_market_ids": list(cm),
        "fixed_properties": {"collector_number": number},
    }


BLUEPRINTS = {
    100: [bp(1, 100, "OP05-119", None, 1, 2), bp(2, 100, "OP05-119", "Alternate Art", 3, 4)],
    101: [bp(3, 101, "OP05-119", "Manga", 5, 6)],
}

CARDS = {
    "OP05-119": {"name": "Monkey.D.Luffy", "set": "OP05", "number": "119"},
    "OP05-119_p1": {"name": "Monkey.D.Luffy", "set": "OP05", "number": "119"},
    "OP05-119_p2": {"name": "Monkey.D.Luffy", "set": "OP05", "number": "119"},
    "OP05-120": {"name": "Nami", "set": "OP05", "number": "120"},
}


class ReportTest(unittest.TestCase):
    def test_read_ambiguous_keeps_the_games_cards_in_order(self):
        got = review.read_ambiguous(REPORT, review.GAME_PREFIXES["onepiece"])
        self.assertEqual(got, {"OP05-119_p1": [1, 2, 3, 4], "OP05-119_p2": [1, 2, 3, 4]})


class IndexTest(unittest.TestCase):
    def setUp(self):
        self.ix = review.index_blueprints(EXPS, BLUEPRINTS)

    def test_one_tile_per_blueprint_with_every_product_of_the_printing(self):
        tiles = review.group_candidates([1, 2, 3, 4], self.ix)
        self.assertEqual([t["blueprint"] for t in tiles], [1, 2])
        self.assertEqual(tiles[1]["products"], [3, 4])
        self.assertEqual(tiles[1]["version"], "Alternate Art")
        self.assertEqual(tiles[1]["expansion"], "Awakening of the New Era")
        self.assertEqual(tiles[1]["preview"], "/preview/2")
        self.assertEqual(tiles[0]["version"], "")

    def test_a_product_on_no_blueprint_is_still_shown_without_a_preview(self):
        tiles = review.group_candidates([3, 99], self.ix)
        self.assertEqual([t["blueprint"] for t in tiles], [2, None])
        self.assertEqual(tiles[1]["products"], [99])
        self.assertIsNone(tiles[1]["preview"])

    def test_candidates_for_a_plain_card_are_every_printing_of_its_number(self):
        tiles = review.candidates_for_number("OP05-119", CARDS, self.ix)
        self.assertEqual([t["blueprint"] for t in tiles], [1, 2, 3])

    def test_base_number_strips_the_variant_letter(self):
        self.assertEqual(review.base_number("OP01-025a"), "OP01-025")
        self.assertEqual(review.base_number("OP01-025"), "OP01-025")
        self.assertEqual(review.base_number("P-001"), "P-001")


class QueueTest(unittest.TestCase):
    def test_choice_queue_skips_cards_already_settled_by_hand(self):
        ambiguous = {"OP05-119_p1": [1, 2], "OP05-119_p2": [1, 2], "OP05-119_p3": [1, 2]}
        manual = {"picks": {"OP05-119_p1": 3}, "none": ["OP05-119_p3"]}
        self.assertEqual(review.choice_queue(ambiguous, manual), ["OP05-119_p2"])

    def test_check_rows_are_the_games_plain_mapped_cards(self):
        ix = review.index_blueprints(EXPS, BLUEPRINTS)
        mapping = {"OP05-119": 1, "OP05-119_p1": 3, "OP05-120": 77, "1-1": 5}
        rows = review.check_rows(mapping, CARDS, ix, review.GAME_PREFIXES["onepiece"])
        self.assertEqual([r["id"] for r in rows], ["OP05-119", "OP05-120"])
        self.assertEqual(rows[0]["preview"], "/preview/1")
        self.assertEqual(rows[0]["name"], "Monkey.D.Luffy")
        self.assertIsNone(rows[1]["preview"])  # product 77 is on no cached blueprint


class ManualFileTest(unittest.TestCase):
    def test_picks_are_written_at_once_and_none_moves_between_lists(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "manual.json"
            review.save_pick(path, "OP05-119_p1", 4)
            review.save_pick(path, "OP05-119_p2", None)
            self.assertEqual(json.loads(path.read_text("utf-8")), {"picks": {"OP05-119_p1": 4}, "none": ["OP05-119_p2"]})
            # A second verdict replaces the first, whichever list it was in.
            review.save_pick(path, "OP05-119_p1", None)
            review.save_pick(path, "OP05-119_p2", 5)
            self.assertEqual(json.loads(path.read_text("utf-8")), {"picks": {"OP05-119_p2": 5}, "none": ["OP05-119_p1"]})
            # Clearing forgets the card so the queue shows it again.
            review.save_pick(path, "OP05-119_p1", review.CLEAR)
            self.assertEqual(review.load_manual(path), {"picks": {"OP05-119_p2": 5}, "none": []})

    def test_a_missing_file_is_an_empty_verdict_list(self):
        self.assertEqual(review.load_manual(Path("nope/manual.json")), {"picks": {}, "none": []})


if __name__ == "__main__":
    unittest.main()


class ImageUrlTest(unittest.TestCase):
    def test_the_original_image_is_preferred_over_the_180px_preview(self):
        b = {"image_url": "https://cardtrader.com/uploads/blueprints/image/1/preview_x.jpg",
             "image": {"url": "/uploads/blueprints/image/1/x.jpg", "preview": {"url": "/uploads/blueprints/image/1/preview_x.jpg"}}}
        self.assertEqual(review.image_url(b), "https://cardtrader.com/uploads/blueprints/image/1/x.jpg")

    def test_an_absolute_original_url_is_kept_and_the_preview_is_the_fallback(self):
        self.assertEqual(review.image_url({"image": {"url": "https://cdn/x.jpg"}}), "https://cdn/x.jpg")
        self.assertEqual(review.image_url({"image_url": "https://cardtrader.com/p.jpg", "image": {}}), "https://cardtrader.com/p.jpg")
        self.assertIsNone(review.image_url({}))
