import unittest

from audio_bench import report
from audio_bench.manifest import CriticalItem, Sample
from audio_bench.metrics import Term


def sample(sid, reference, categories=("mixed",), speaker="spk1", terms=(), critical=()):
    return Sample(sid, f"{sid}.wav", "0" * 64, speaker, "c", "dev", tuple(categories), reference, tuple(terms), tuple(critical))


def result(streaming, offline, audio=2.0, steps=((1.0, 0.2), (2.0, 0.2)), finish=0.0, offline_compute=0.4):
    return {
        "audio_sec": audio,
        "streaming_final": streaming,
        "offline": offline,
        "truncated": False,
        "utterances": [
            {
                "start": 0.0,
                "end": audio,
                "steps": [{"audio_end": e, "compute": c, "truncated": False, "text": ""} for e, c in steps],
                "finish": {"called": finish > 0, "compute": finish, "truncated": False},
                "streaming_final": streaming,
                "offline": {"text": offline, "compute": offline_compute, "truncated": False},
            }
        ],
    }


class ReportTest(unittest.TestCase):
    def setUp(self):
        jwt = Term("JWT")
        self.samples = [
            sample("a", "проверь JWT токен", terms=[jwt]),
            sample("b", "не меньше 150 ms", categories=("ru", "numbers"), speaker="spk2",
                   critical=[CriticalItem("number", Term("150", ("сто пятьдесят",)))]),
            sample("s", "", categories=("silence",)),
        ]
        self.results = {
            "a": result("проверь джей вэ тэ токен", "проверь JWT токен"),
            "b": result("не меньше сто пятьдесят ms", "не меньше 150 ms"),
            "s": result("Спасибо", ""),
        }
        self.summary = report.summarize(self.samples, self.results, chunk_sec=1.0, endpoint_silence_sec=0.8)

    def test_quality_per_hypothesis_kind(self):
        streaming = self.summary["kinds"]["streaming_final"]
        offline = self.summary["kinds"]["offline"]
        self.assertEqual(streaming["wer_mixed"], {"errors": 3, "reference": 3, "rate": 1.0})
        self.assertEqual(offline["wer"]["errors"], 0)
        self.assertEqual(streaming["terms"], {"hits": 0, "expected": 1, "rate": 0.0})
        self.assertEqual(streaming["critical"], {"preserved": 1, "samples": 1, "rate": 1.0})
        self.assertEqual(streaming["silence"]["hallucinated"], ["s"])
        self.assertEqual(offline["silence"]["hallucinated"], [])
        self.assertEqual(sorted(streaming["per_speaker"]), ["spk1", "spk2"])

    def test_latency_includes_chunk_wait_and_endpoint_silence(self):
        # Partial lag bound: chunk wait 1.0 + decode 0.2. Final work starts only after
        # 0.8 s of endpoint silence; the last 0.2 s step already finished by then.
        lat = self.summary["latency"]
        self.assertAlmostEqual(lat["partial_lag_p95"], 1.2)
        self.assertAlmostEqual(lat["streaming_final_p95"], 0.8)
        self.assertAlmostEqual(lat["hybrid_final_p95"], 0.8 + 0.4)
        self.assertAlmostEqual(lat["rtf_streaming"], 0.2)
        self.assertAlmostEqual(lat["rtf_hybrid"], 0.4)

    def test_two_tracks_share_one_model(self):
        two = self.summary["latency"]["two_tracks"]
        self.assertEqual(two["pairs"], 1)
        self.assertAlmostEqual(two["partial_lag_p95"], 1.4)
        self.assertAlmostEqual(two["hybrid_final_p95"], 0.8 + 0.4 + 0.4)

    def test_streaming_against_offline(self):
        cmp = self.summary["streaming_vs_offline"]
        self.assertEqual(cmp["offline_better"], ["a", "b", "s"])
        self.assertEqual(cmp["term_wins_offline"], ["a"])
        self.assertEqual(cmp["term_wins_streaming"], [])

    def test_markdown_lists_every_section(self):
        text = report.render_markdown(self.summary, {"model": "m"})
        for heading in ("Качество", "Задержка", "Streaming final против offline", "Худшие примеры"):
            self.assertIn(heading, text)


if __name__ == "__main__":
    unittest.main()
