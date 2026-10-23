import json
import tempfile
import unittest
from pathlib import Path

from audio_bench import manifest, phrases

PHRASES = """# comment
q01 | Recording (1).m4a | Почему context cancellation не остановил goroutine?

q02 | Recording (2).m4a | Первая часть. / Вторая часть после паузы.
q03 | Recording (3).m4a |
"""

ANNOTATIONS = {
    "speaker": "user1",
    "conversation": "quick",
    "split": "dev",
    "context_phrases": ["goroutine"],
    "samples": {
        "q01": {"categories": ["mixed"], "terms": [{"canonical": "goroutine", "variants": ["горутина"]}],
                "critical": [{"kind": "negation", "canonical": "не остановил"}]},
        "q02": {"categories": ["ru", "pause"]},
        "q03": {"categories": ["silence"]},
    },
}


class PhrasesTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.root = Path(self.dir.name)
        (self.root / "phrases.txt").write_text(PHRASES, encoding="utf-8")
        (self.root / "wav").mkdir()
        for sid in ("q01", "q02", "q03"):
            (self.root / "wav" / f"{sid}.wav").write_bytes(sid.encode())

    def tearDown(self):
        self.dir.cleanup()

    def test_parse_drops_comments_and_pause_markers(self):
        parsed = phrases.parse(self.root / "phrases.txt")
        self.assertEqual([p.id for p in parsed], ["q01", "q02", "q03"])
        self.assertEqual(parsed[0].source, "Recording (1).m4a")
        self.assertEqual(parsed[1].reference, "Первая часть. Вторая часть после паузы.")
        self.assertEqual(parsed[2].reference, "")

    def test_parse_rejects_malformed_and_duplicate_lines(self):
        for text, message in (("q01 | only two fields\n", "line 1"), ("q01 | a.m4a | x\nq01 | b.m4a | y\n", "duplicate")):
            with self.subTest(message=message):
                (self.root / "bad.txt").write_text(text, encoding="utf-8")
                with self.assertRaisesRegex(ValueError, message):
                    phrases.parse(self.root / "bad.txt")

    def test_build_writes_a_loadable_manifest_with_hashes(self):
        out = self.root / "manifest.json"
        phrases.build(phrases.parse(self.root / "phrases.txt"), ANNOTATIONS, self.root / "wav", out)
        m = manifest.load(out)
        manifest.verify_audio(m)
        self.assertEqual([s.audio for s in m.samples], ["wav/q01.wav", "wav/q02.wav", "wav/q03.wav"])
        self.assertEqual(m.samples[0].terms[0].variants, ("горутина",))
        self.assertEqual(m.samples[0].speaker, "user1")
        self.assertEqual(json.loads(out.read_text(encoding="utf-8"))["samples"][1]["source"], "Recording (2).m4a")

    def test_build_requires_annotation_for_every_phrase(self):
        annotations = {**ANNOTATIONS, "samples": {k: v for k, v in ANNOTATIONS["samples"].items() if k != "q02"}}
        with self.assertRaisesRegex(ValueError, "q02"):
            phrases.build(phrases.parse(self.root / "phrases.txt"), annotations, self.root / "wav", self.root / "m.json")


if __name__ == "__main__":
    unittest.main()
