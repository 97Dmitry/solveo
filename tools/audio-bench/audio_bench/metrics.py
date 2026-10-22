"""Scoring primitives for the STT benchmark. Standard library only.

Normalization (documented in README): NFKC, casefold, ё→е, every Unicode
punctuation/symbol becomes a word break except "+" and "#" (C++, C#) and a
"." between two letters/digits (Next.js, 3.5). Punctuation therefore never
counts as a recognition error; spelling, numbers and word order do.
"""

from __future__ import annotations

import math
import unicodedata
from dataclasses import dataclass
from typing import Iterable, Sequence

_KEPT_SYMBOLS = {"+", "#"}


def normalize(text: str) -> list[str]:
    text = unicodedata.normalize("NFKC", text).casefold().replace("ё", "е")
    out = []
    for i, ch in enumerate(text):
        category = unicodedata.category(ch)
        if ch.isspace():
            out.append(" ")
        elif category[0] in "PS" and ch not in _KEPT_SYMBOLS:
            inner_dot = (
                ch == "."
                and 0 < i < len(text) - 1
                and text[i - 1].isalnum()
                and text[i + 1].isalnum()
            )
            out.append(ch if inner_dot else " ")
        else:
            out.append(ch)
    return "".join(out).split()


@dataclass(frozen=True)
class ErrorCounts:
    errors: int
    reference: int

    def __add__(self, other: "ErrorCounts") -> "ErrorCounts":
        return ErrorCounts(self.errors + other.errors, self.reference + other.reference)

    @property
    def rate(self) -> float | None:
        """Pooled error rate; None when the reference is empty."""
        if self.reference == 0:
            return None
        return self.errors / self.reference


def _edit_distance(ref: Sequence[str], hyp: Sequence[str]) -> int:
    prev = list(range(len(hyp) + 1))
    for i, r in enumerate(ref, 1):
        cur = [i]
        for j, h in enumerate(hyp, 1):
            cur.append(min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (r != h)))
        prev = cur
    return prev[-1]


def word_errors(reference: str, hypothesis: str) -> ErrorCounts:
    ref = normalize(reference)
    return ErrorCounts(_edit_distance(ref, normalize(hypothesis)), len(ref))


def char_errors(reference: str, hypothesis: str) -> ErrorCounts:
    """Character errors over normalized tokens, spaces excluded."""
    ref = "".join(normalize(reference))
    return ErrorCounts(_edit_distance(ref, "".join(normalize(hypothesis))), len(ref))


@dataclass(frozen=True)
class Term:
    canonical: str
    variants: tuple[str, ...] = ()

    def forms(self) -> list[list[str]]:
        seen, out = set(), []
        for form in (self.canonical, *self.variants):
            tokens = normalize(form)
            if tokens and tuple(tokens) not in seen:
                seen.add(tuple(tokens))
                out.append(tokens)
        return out


def _occurrences(needle: list[str], tokens: list[str]) -> int:
    count, i, n = 0, 0, len(needle)
    while i + n <= len(tokens):
        if tokens[i : i + n] == needle:
            count += 1
            i += n
        else:
            i += 1
    return count


def count_term(term: Term, text: str) -> int:
    tokens = normalize(text)
    return sum(_occurrences(form, tokens) for form in term.forms())


def term_hits(term: Term, reference: str, hypothesis: str) -> tuple[int, int]:
    """(recognized occurrences, expected occurrences) of a term."""
    expected = count_term(term, reference)
    return min(expected, count_term(term, hypothesis)), expected


def preserved(item: Term, hypothesis: str) -> bool:
    return count_term(item, hypothesis) > 0


def is_hallucination(hypothesis: str) -> bool:
    """Any normalized token in the output of a silence/noise sample."""
    return bool(normalize(hypothesis))


def percentile(values: Iterable[float], p: float) -> float | None:
    """Nearest-rank percentile; None for no values."""
    ordered = sorted(values)
    if not ordered:
        return None
    rank = max(1, math.ceil(p / 100 * len(ordered)))
    return ordered[rank - 1]


def rtf(compute_seconds: float, audio_seconds: float) -> float | None:
    if audio_seconds <= 0:
        return None
    return compute_seconds / audio_seconds
