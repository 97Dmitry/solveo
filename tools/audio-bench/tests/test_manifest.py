import hashlib
import json
import tempfile
import unittest
from pathlib import Path

from audio_bench import manifest

DIGEST = hashlib.sha256(b"RIFF fake").hexdigest()


def sample(**overrides):
    value = {
        "id": "s1",
        "audio": "s1.wav",
        "sha256": DIGEST,
        "speaker": "spk1",
        "conversation": "c1",
        "split": "dev",
        "categories": ["mixed"],
        "reference": "почему context cancellation не остановил goroutine",
        "terms": [{"canonical": "context", "variants": []}],
        "critical": [{"kind": "negation", "canonical": "не остановил", "variants": []}],
    }
    value.update(overrides)
    return value


class ManifestTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.root = Path(self.dir.name)
        (self.root / "s1.wav").write_bytes(b"RIFF fake")

    def tearDown(self):
        self.dir.cleanup()

    def write(self, samples, **extra):
        doc = {"version": 1, "context_phrases": ["goroutine"], "samples": samples}
        doc.update(extra)
        path = self.root / "manifest.json"
        path.write_text(json.dumps(doc, ensure_ascii=False), encoding="utf-8")
        return path

    def test_loads_valid_manifest_and_verifies_hash(self):
        m = manifest.load(self.write([sample()]))
        self.assertEqual(m.samples[0].terms[0].canonical, "context")
        self.assertEqual(m.context_phrases, ("goroutine",))
        manifest.verify_audio(m)

    def test_hash_mismatch_fails(self):
        m = manifest.load(self.write([sample(sha256="0" * 64)]))
        with self.assertRaisesRegex(manifest.ManifestError, "sha256 mismatch"):
            manifest.verify_audio(m)

    def test_rejects_duplicate_ids_bad_split_and_escaping_paths(self):
        cases = [
            ([sample(), sample()], "duplicate"),
            ([sample(split="train")], "split"),
            ([sample(audio="../s1.wav")], "audio"),
            ([sample(reference=None)], "reference"),
            ([sample(categories=["silence"], reference="текст")], "silence"),
            ([sample(categories=["mixd"])], "category"),
            ([sample(sha256="")], "sha256"),
            ([sample(terms=[{"canonical": "JWT", "variants": []}])], "not found in reference"),
            ([sample(critical=[{"kind": "date", "canonical": "не остановил", "variants": []}])], "kind"),
        ]
        for samples, message in cases:
            with self.subTest(message=message):
                with self.assertRaisesRegex(manifest.ManifestError, message):
                    manifest.load(self.write(samples))

    def test_rejects_unknown_version(self):
        path = self.write([sample()], version=2)
        with self.assertRaisesRegex(manifest.ManifestError, "version"):
            manifest.load(path)

    def test_detects_speaker_and_conversation_leakage(self):
        m = manifest.load(
            self.write(
                [
                    sample(id="a", speaker="spk1", conversation="c1"),
                    sample(id="b", speaker="spk1", conversation="c2", split="holdout"),
                    sample(id="c", speaker="spk2", conversation="c1", split="holdout"),
                ]
            )
        )
        self.assertEqual(
            manifest.split_leaks(m),
            ["conversation 'c1' in dev and holdout", "speaker 'spk1' in dev and holdout"],
        )

    def test_select_by_split(self):
        m = manifest.load(self.write([sample(id="a"), sample(id="b", speaker="x", conversation="y", split="holdout")]))
        self.assertEqual([s.id for s in m.select("holdout")], ["b"])
        self.assertEqual([s.id for s in m.select("all")], ["a", "b"])


if __name__ == "__main__":
    unittest.main()
