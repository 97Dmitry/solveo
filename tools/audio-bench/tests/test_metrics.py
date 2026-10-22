import unittest

from audio_bench import metrics


class NormalizeTest(unittest.TestCase):
    def test_casefold_yo_and_punctuation(self):
        self.assertEqual(
            metrics.normalize("Ещё раз: Read-Committed, ПОЖАЛУЙСТА!"),
            ["еще", "раз", "read", "committed", "пожалуйста"],
        )

    def test_keeps_in_token_dots_plus_and_hash(self):
        self.assertEqual(
            metrics.normalize("Next.js, C++ и C# версии 3.5."),
            ["next.js", "c++", "и", "c#", "версии", "3.5"],
        )

    def test_nfkc_and_whitespace(self):
        self.assertEqual(metrics.normalize("ﬁle   O(n log n)"), ["file", "o", "n", "log", "n"])


class ErrorRateTest(unittest.TestCase):
    def test_wer_denominator_is_reference_words(self):
        counts = metrics.word_errors("a b c d", "a x c d e")
        self.assertEqual((counts.errors, counts.reference), (2, 4))

    def test_wer_of_empty_reference_counts_insertions(self):
        counts = metrics.word_errors("", "лишний текст")
        self.assertEqual((counts.errors, counts.reference), (2, 0))

    def test_cer_ignores_spaces_and_punctuation(self):
        self.assertEqual(metrics.char_errors("нет, не так", "Нет не так.").errors, 0)
        counts = metrics.char_errors("нет, не так", "нет не таг")
        self.assertEqual((counts.errors, counts.reference), (1, 8))

    def test_aggregate_rate_is_pooled_not_averaged(self):
        pooled = metrics.ErrorCounts(1, 10) + metrics.ErrorCounts(1, 2)
        self.assertAlmostEqual(pooled.rate, 2 / 12)


class TermTest(unittest.TestCase):
    def test_term_matches_canonical_or_variant_as_token_sequence(self):
        term = metrics.Term("NestJS", ("Nest JS",))
        self.assertEqual(metrics.term_hits(term, "почему NestJS", "почему nest js"), (1, 1))
        self.assertEqual(metrics.term_hits(term, "почему NestJS", "почему нест"), (0, 1))

    def test_term_is_not_matched_inside_longer_token(self):
        term = metrics.Term("JWT", ())
        self.assertEqual(metrics.term_hits(term, "JWT токен", "jwts токен"), (0, 1))

    def test_repeated_term_counts_each_occurrence(self):
        term = metrics.Term("goroutine", ())
        self.assertEqual(
            metrics.term_hits(term, "goroutine и ещё goroutine", "goroutine и ещё горутина"),
            (1, 2),
        )

    def test_critical_item_requires_any_form(self):
        item = metrics.Term("150", ("сто пятьдесят",))
        self.assertTrue(metrics.preserved(item, "не меньше сто пятьдесят миллисекунд"))
        self.assertFalse(metrics.preserved(item, "не меньше пятьдесят миллисекунд"))


class StatsTest(unittest.TestCase):
    def test_p95_nearest_rank(self):
        values = list(range(1, 21))
        self.assertEqual(metrics.percentile(values, 95), 19)
        self.assertEqual(metrics.percentile([3.0], 95), 3.0)

    def test_percentile_of_empty_is_none(self):
        self.assertIsNone(metrics.percentile([], 95))

    def test_rtf(self):
        self.assertAlmostEqual(metrics.rtf(compute_seconds=2.0, audio_seconds=8.0), 0.25)

    def test_silence_output_is_any_token(self):
        self.assertFalse(metrics.is_hallucination(" . , "))
        self.assertTrue(metrics.is_hallucination("Спасибо за просмотр"))


if __name__ == "__main__":
    unittest.main()
