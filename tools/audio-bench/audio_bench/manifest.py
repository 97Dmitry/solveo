"""Benchmark manifest: corpus samples, references, terms and dev/holdout split."""

from __future__ import annotations

import hashlib
import json
import re
from dataclasses import dataclass
from pathlib import Path, PurePosixPath

from .metrics import Term, count_term

SPLITS = ("dev", "holdout")
CATEGORIES = frozenset(
    {"mixed", "ru", "en", "silence", "noise", "numbers", "accent", "long", "pause", "interruption", "call"}
)
CRITICAL_KINDS = frozenset({"number", "negation", "constraint"})
MAX_PHRASES = 256
MAX_PHRASE_CHARS = 128
_SHA256 = re.compile(r"^[0-9a-f]{64}$")


class ManifestError(ValueError):
    pass


@dataclass(frozen=True)
class CriticalItem:
    kind: str
    term: Term


@dataclass(frozen=True)
class Sample:
    id: str
    audio: str
    sha256: str
    speaker: str
    conversation: str
    split: str
    categories: tuple[str, ...]
    reference: str
    terms: tuple[Term, ...]
    critical: tuple[CriticalItem, ...]


@dataclass(frozen=True)
class Manifest:
    root: Path
    context_phrases: tuple[str, ...]
    samples: tuple[Sample, ...]

    def select(self, split: str) -> list[Sample]:
        if split == "all":
            return list(self.samples)
        if split not in SPLITS:
            raise ManifestError(f"unknown split {split!r}")
        return [s for s in self.samples if s.split == split]

    def audio_path(self, sample: Sample) -> Path:
        return self.root / PurePosixPath(sample.audio)


def _text(raw: dict, key: str, sid: str) -> str:
    value = raw.get(key)
    if not isinstance(value, str):
        raise ManifestError(f"sample {sid}: {key} must be a string")
    return value


def _term(raw: dict, sid: str) -> Term:
    canonical = raw.get("canonical")
    variants = raw.get("variants", [])
    if not isinstance(canonical, str) or not canonical.strip():
        raise ManifestError(f"sample {sid}: term canonical must be a non-empty string")
    if not isinstance(variants, list) or not all(isinstance(v, str) and v.strip() for v in variants):
        raise ManifestError(f"sample {sid}: term variants must be non-empty strings")
    return Term(canonical, tuple(variants))


def _sample(raw: dict) -> Sample:
    if not isinstance(raw, dict):
        raise ManifestError("sample must be an object")
    sid = raw.get("id")
    if not isinstance(sid, str) or not sid:
        raise ManifestError("sample id must be a non-empty string")
    audio = _text(raw, "audio", sid)
    path = PurePosixPath(audio)
    if not audio or path.is_absolute() or ".." in path.parts or "\\" in audio:
        raise ManifestError(f"sample {sid}: audio must be a relative path inside the corpus")
    sha = _text(raw, "sha256", sid)
    if not _SHA256.match(sha):
        raise ManifestError(f"sample {sid}: sha256 must be 64 lowercase hex characters")
    split = _text(raw, "split", sid)
    if split not in SPLITS:
        raise ManifestError(f"sample {sid}: split must be one of {SPLITS}")
    categories = raw.get("categories")
    if not isinstance(categories, list) or not categories:
        raise ManifestError(f"sample {sid}: categories must be a non-empty list")
    unknown = sorted(set(categories) - CATEGORIES)
    if unknown:
        raise ManifestError(f"sample {sid}: unknown category {unknown}")
    reference = _text(raw, "reference", sid)
    if "silence" in categories and reference.strip():
        raise ManifestError(f"sample {sid}: silence sample must have an empty reference")

    terms = tuple(_term(t, sid) for t in raw.get("terms", []))
    critical = []
    for item in raw.get("critical", []):
        kind = item.get("kind") if isinstance(item, dict) else None
        if kind not in CRITICAL_KINDS:
            raise ManifestError(f"sample {sid}: critical kind must be one of {sorted(CRITICAL_KINDS)}")
        critical.append(CriticalItem(kind, _term(item, sid)))
    for term in (*terms, *(c.term for c in critical)):
        if count_term(term, reference) == 0:
            raise ManifestError(f"sample {sid}: {term.canonical!r} not found in reference")

    return Sample(
        id=sid,
        audio=audio,
        sha256=sha,
        speaker=_text(raw, "speaker", sid),
        conversation=_text(raw, "conversation", sid),
        split=split,
        categories=tuple(categories),
        reference=reference,
        terms=terms,
        critical=tuple(critical),
    )


def _phrases(raw) -> tuple[str, ...]:
    if not isinstance(raw, list):
        raise ManifestError("context_phrases must be a list")
    out: list[str] = []
    for phrase in raw:
        if not isinstance(phrase, str) or "\x00" in phrase:
            raise ManifestError("context phrase must be a string without NUL")
        phrase = phrase.strip()
        if not phrase or phrase in out:
            continue
        if len(phrase) > MAX_PHRASE_CHARS:
            raise ManifestError(f"context phrase longer than {MAX_PHRASE_CHARS} characters")
        out.append(phrase)
    if len(out) > MAX_PHRASES:
        raise ManifestError(f"more than {MAX_PHRASES} context phrases")
    return tuple(out)


def load(path: Path) -> Manifest:
    path = Path(path)
    doc = json.loads(path.read_text(encoding="utf-8"))
    if doc.get("version") != 1:
        raise ManifestError("unsupported manifest version")
    raw_samples = doc.get("samples")
    if not isinstance(raw_samples, list) or not raw_samples:
        raise ManifestError("samples must be a non-empty list")
    samples = [_sample(s) for s in raw_samples]
    ids = [s.id for s in samples]
    duplicates = sorted({i for i in ids if ids.count(i) > 1})
    if duplicates:
        raise ManifestError(f"duplicate sample ids {duplicates}")
    return Manifest(path.parent, _phrases(doc.get("context_phrases", [])), tuple(samples))


def verify_audio(manifest: Manifest) -> None:
    for sample in manifest.samples:
        path = manifest.audio_path(sample)
        if not path.is_file():
            raise ManifestError(f"sample {sample.id}: missing audio {sample.audio}")
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        if digest != sample.sha256:
            raise ManifestError(f"sample {sample.id}: sha256 mismatch")


def split_leaks(manifest: Manifest) -> list[str]:
    """Speakers or conversations present in both dev and holdout."""
    leaks = []
    for field in ("speaker", "conversation"):
        by_split = {split: {getattr(s, field) for s in manifest.samples if s.split == split} for split in SPLITS}
        for value in sorted(by_split["dev"] & by_split["holdout"]):
            leaks.append(f"{field} {value!r} in dev and holdout")
    return sorted(leaks)
