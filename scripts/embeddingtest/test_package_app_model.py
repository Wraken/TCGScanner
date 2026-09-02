import tempfile
import unittest
from pathlib import Path

from package_app_model import (
    fill_variant_fallbacks,
    image_source_problems,
    metadata_coverage,
    metadata_problems,
)


class MetadataCoverageTest(unittest.TestCase):
    def test_missing_labels_per_game(self):
        labels = ["OP01-025", "OP01-026", "sv01-020", "1-1", "weird-1"]
        manifest = {
            "OP01-025": "onepiece/OP01-025.webp",
            "OP01-026": "onepiece/OP01-026.webp",
            "sv01-020": "pokemon/sv01-020.webp",
            "1-1": "lorcana/1-1.webp",
        }
        metadata = {
            "onepiece": {"OP01-025": {"name": "Zoro"}},
            "pokemon": {},
        }
        got = metadata_coverage(labels, manifest, metadata)
        # Only games WITH a metadata file are reported; weird-1 (not in the
        # manifest) and lorcana (no file) are ignored.
        self.assertEqual(got, {"onepiece": ["OP01-026"], "pokemon": ["sv01-020"]})

    def test_variant_fallback_copies_base_entry(self):
        labels = ["OP01-025", "OP01-025_p2", "OP01-025_r1", "OP01-030_p1"]
        manifest = {l: f"onepiece/{l}.webp" for l in labels}
        metadata = {"onepiece": {"OP01-025": {"name": "Zoro", "rarity": "SR"}}}
        added = fill_variant_fallbacks(labels, manifest, metadata)
        self.assertEqual(added, {"onepiece": 2})
        self.assertEqual(metadata["onepiece"]["OP01-025_p2"], {"name": "Zoro", "rarity": "SR"})
        self.assertEqual(metadata["onepiece"]["OP01-025_r1"], {"name": "Zoro", "rarity": "SR"})
        # No base card -> still missing, reported by the coverage check.
        self.assertEqual(metadata_coverage(labels, manifest, metadata), {"onepiece": ["OP01-030_p1"]})

    def test_variant_already_present_is_not_overwritten(self):
        labels = ["OP01-025", "OP01-025_p2"]
        manifest = {l: f"onepiece/{l}.webp" for l in labels}
        metadata = {
            "onepiece": {
                "OP01-025": {"name": "Zoro"},
                "OP01-025_p2": {"name": "Zoro (parallel)"},
            }
        }
        added = fill_variant_fallbacks(labels, manifest, metadata)
        self.assertEqual(added, {})
        self.assertEqual(metadata["onepiece"]["OP01-025_p2"], {"name": "Zoro (parallel)"})

    def test_variant_fallback_does_not_require_base_in_labels(self):
        # The base card (EB01-001) need not be in `labels` itself -- only in
        # the game's metadata -- for its variant to be filled.
        labels = ["EB01-001_p1"]
        manifest = {"EB01-001_p1": "onepiece/EB01-001_p1.webp"}
        metadata = {"onepiece": {"EB01-001": {"name": "Base Card"}}}
        added = fill_variant_fallbacks(labels, manifest, metadata)
        self.assertEqual(added, {"onepiece": 1})
        self.assertEqual(metadata["onepiece"]["EB01-001_p1"], {"name": "Base Card"})

    def test_variant_fallback_skips_unmapped_and_unknown_game_labels(self):
        # "1-1" is lorcana, which has no metadata file; "weird-1" isn't in the
        # manifest at all (game resolves to ""). Neither should raise.
        labels = ["1-1", "weird-1", "OP01-025_p1"]
        manifest = {"1-1": "lorcana/1-1.webp", "OP01-025_p1": "onepiece/OP01-025_p1.webp"}
        metadata = {"onepiece": {"OP01-025": {"name": "Zoro"}}}
        added = fill_variant_fallbacks(labels, manifest, metadata)
        self.assertEqual(added, {"onepiece": 1})


class ImageSourceProblemsTest(unittest.TestCase):
    def test_missing_dir_is_a_problem(self):
        with tempfile.TemporaryDirectory() as d:
            missing = Path(d) / "does-not-exist"
            image_dirs, problems = image_source_problems([f"onepiece={missing}"])
        self.assertEqual(image_dirs, {})
        self.assertEqual(len(problems), 1)
        self.assertIn("onepiece", problems[0])

    def test_empty_dir_is_a_problem(self):
        with tempfile.TemporaryDirectory() as d:
            image_dirs, problems = image_source_problems([f"onepiece={d}"])
        self.assertEqual(image_dirs, {})
        self.assertEqual(len(problems), 1)

    def test_dir_with_only_cover_art_is_a_problem(self):
        with tempfile.TemporaryDirectory() as d:
            (Path(d) / "_cover.png").write_bytes(b"")
            image_dirs, problems = image_source_problems([f"onepiece={d}"])
        self.assertEqual(image_dirs, {})
        self.assertEqual(len(problems), 1)

    def test_dir_with_one_card_image_is_ok(self):
        with tempfile.TemporaryDirectory() as d:
            (Path(d) / "x.png").write_bytes(b"")
            image_dirs, problems = image_source_problems([f"onepiece={d}"])
        self.assertEqual(problems, [])
        self.assertEqual(image_dirs, {"onepiece": Path(d)})

    def test_malformed_spec_is_a_problem(self):
        image_dirs, problems = image_source_problems(["onepiece"])
        self.assertEqual(image_dirs, {})
        self.assertEqual(len(problems), 1)

    def test_duplicate_game_is_a_problem(self):
        with tempfile.TemporaryDirectory() as d1, tempfile.TemporaryDirectory() as d2:
            (Path(d1) / "x.png").write_bytes(b"")
            (Path(d2) / "y.png").write_bytes(b"")
            image_dirs, problems = image_source_problems([f"onepiece={d1}", f"onepiece={d2}"])
        self.assertEqual(len(problems), 1)
        self.assertIn("given twice", problems[0])


class MetadataProblemsTest(unittest.TestCase):
    def test_list_shaped_input_is_a_problem(self):
        problems = metadata_problems(["OP01-025"])
        self.assertEqual(len(problems), 1)

    def test_entry_not_a_dict_is_a_problem(self):
        problems = metadata_problems({"OP01-025": "Zoro"})
        self.assertEqual(len(problems), 1)

    def test_string_colors_is_a_problem(self):
        problems = metadata_problems({"OP01-025": {"name": "Zoro", "colors": "Red"}})
        self.assertEqual(len(problems), 1)

    def test_valid_input_has_no_problems(self):
        problems = metadata_problems({
            "OP01-025": {"name": "Zoro", "colors": ["Red"]},
            "OP01-026": {"name": "Sanji"},  # no colors key at all -- also fine
        })
        self.assertEqual(problems, [])

    def test_non_string_field_is_a_problem(self):
        problems = metadata_problems({"OP01-025": {"name": "Zoro", "number": 25}})
        self.assertEqual(len(problems), 1)

    def test_non_string_color_element_is_a_problem(self):
        problems = metadata_problems({"OP01-025": {"name": "Zoro", "colors": ["Red", 1]}})
        self.assertEqual(len(problems), 1)


if __name__ == "__main__":
    unittest.main()
