import unittest

from audio_bench import nemotron


class ChunkSpansTest(unittest.TestCase):
    def test_spans_follow_processor_geometry_and_cover_the_tail(self):
        spans = nemotron.chunk_spans(
            n_samples=10000, first_samples=3000, first_mel=20, mel_per_chunk=10, hop=160, n_fft=512, samples_per_chunk=1856
        )
        self.assertEqual(spans, [(0, 3000), (2944, 4800), (4544, 6400), (6144, 8000), (7744, 9600), (9344, 11200)])

    def test_short_audio_is_one_padded_first_chunk(self):
        spans = nemotron.chunk_spans(1000, 3000, 20, 10, 160, 512, 1856)
        self.assertEqual(spans, [(0, 3000)])


class StepsTest(unittest.TestCase):
    def test_tokens_are_assigned_to_the_chunk_whose_processing_emitted_them(self):
        chunks = [(16000, 10.0, 10.2), (32000, 10.3, 10.6)]
        tokens = [(10.1, 2), (10.15, 1), (10.5, 3), (10.7, 9)]
        steps = nemotron.steps_from_events(chunks, tokens)
        self.assertEqual([s["audio_end"] for s in steps], [1.0, 2.0])
        self.assertEqual([round(s["compute"], 6) for s in steps], [0.2, 0.3])
        self.assertEqual([s["tokens"] for s in steps], [3, 15])

    def test_tokens_after_the_last_chunk_belong_to_it(self):
        steps = nemotron.steps_from_events([(16000, 1.0, 1.5)], [(1.6, 4)])
        self.assertEqual(steps[0]["tokens"], 4)


if __name__ == "__main__":
    unittest.main()
