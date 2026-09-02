import unittest

from package_app_model import fill_variant_fallbacks, metadata_coverage


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


if __name__ == "__main__":
    unittest.main()
