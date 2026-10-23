"""Build a manifest from a scripted phrase list and per-phrase annotations.

phrases.txt: `id | source recording | spoken text`; `#` starts a comment, ` / `
marks a spoken pause and is not part of the reference. Recordings are expected
as `<audio-dir>/<id>.wav` (decoded from the source by ffmpeg, see README).

Usage: python3 -m audio_bench.phrases --phrases P --annotations A --audio-dir D --out M
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from dataclasses import dataclass
from pathlib import Path, PurePath

from . import manifest


@dataclass(frozen=True)
class Phrase:
    id: str
    source: str
    reference: str


def parse(path: Path) -> list[Phrase]:
    out: list[Phrase] = []
    for number, line in enumerate(Path(path).read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        parts = [p.strip() for p in line.split("|")]
        if len(parts) != 3 or not parts[0] or not parts[1]:
            raise ValueError(f"line {number}: expected 'id | recording | text'")
        sid, source, text = parts
        if any(p.id == sid for p in out):
            raise ValueError(f"line {number}: duplicate id {sid}")
        out.append(Phrase(sid, source, " ".join(text.replace(" / ", " ").split())))
    return out


def build(phrases: list[Phrase], annotations: dict, audio_dir: Path, out: Path) -> None:
    notes = annotations["samples"]
    missing = sorted({p.id for p in phrases} ^ set(notes))
    if missing:
        raise ValueError(f"phrases and annotations differ for {missing}")
    samples = []
    for p in phrases:
        audio = Path(audio_dir) / f"{p.id}.wav"
        note = notes[p.id]
        samples.append(
            {
                "id": p.id,
                "source": p.source,
                "audio": PurePath(os.path.relpath(audio, Path(out).parent)).as_posix(),
                "sha256": hashlib.sha256(audio.read_bytes()).hexdigest(),
                "speaker": annotations["speaker"],
                "conversation": annotations["conversation"],
                "split": annotations["split"],
                "categories": note["categories"],
                "reference": p.reference,
                "terms": [{"variants": [], **t} for t in note.get("terms", [])],
                "critical": [{"variants": [], **c} for c in note.get("critical", [])],
            }
        )
    doc = {"version": 1, "context_phrases": annotations.get("context_phrases", []), "samples": samples}
    Path(out).write_text(json.dumps(doc, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    manifest.load(Path(out))


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--phrases", required=True, type=Path)
    parser.add_argument("--annotations", required=True, type=Path)
    parser.add_argument("--audio-dir", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args(argv)
    annotations = json.loads(args.annotations.read_text(encoding="utf-8"))
    build(parse(args.phrases), annotations, args.audio_dir, args.out)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
