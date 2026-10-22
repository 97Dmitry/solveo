"""Grok STT (xAI) batch measurement on the corpus; same JSONL schema as run_qwen so report.py compares them.

Audio is uploaded to xAI. Usage (key from the environment, never on the command line):
  XAI_API_KEY=... python3 -m audio_bench.run_grok --manifest M --out R.jsonl [--language ru]
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

from . import manifest as manifest_mod

DEFAULT_URL = "https://api.x.ai/v1/stt"
MODEL = "grok-voice-transcribe-2.0"
MAX_TERMS = 100
MAX_TERM_CHARS = 50


def multipart(fields: list[tuple[str, str]], file_name: str, data: bytes) -> tuple[bytes, str]:
    """Option fields first and the file last, as xAI requires."""
    boundary = uuid.uuid4().hex
    out = bytearray()
    for name, value in fields:
        out += f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode()
    out += f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{file_name}"\r\nContent-Type: application/octet-stream\r\n\r\n'.encode()
    out += data + f"\r\n--{boundary}--\r\n".encode()
    return bytes(out), f"multipart/form-data; boundary={boundary}"


def transcribe(url: str, key: str, path: Path, terms: list[str], language: str | None) -> tuple[dict, float]:
    fields = [("model", MODEL)] + [("keyterm", t) for t in terms]
    if language:
        fields.append(("language", language))
    body, content_type = multipart(fields, path.name, path.read_bytes())
    req = urllib.request.Request(url, data=body, method="POST",
                                 headers={"Authorization": f"Bearer {key}", "Content-Type": content_type})
    started = time.perf_counter()
    with urllib.request.urlopen(req, timeout=120) as resp:
        result = json.loads(resp.read())
    return result, time.perf_counter() - started


def main(argv: list[str] | None = None, env=os.environ) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--split", default="dev", choices=("dev", "holdout", "all"))
    parser.add_argument("--language", default=None, help="language hint, e.g. ru; default auto")
    parser.add_argument("--context", choices=("none", "manifest"), default="manifest")
    parser.add_argument("--url", default=DEFAULT_URL)
    args = parser.parse_args(argv)

    key = env.get("XAI_API_KEY", "")
    if not key:
        print("XAI_API_KEY is not set", file=sys.stderr)
        return 2
    m = manifest_mod.load(args.manifest)
    manifest_mod.verify_audio(m)
    terms = list(m.context_phrases) if args.context == "manifest" else []
    bad = [t for t in terms if len(t) > MAX_TERM_CHARS]
    if len(terms) > MAX_TERMS or bad:
        print(f"dictionary exceeds xAI limits ({MAX_TERMS} terms, {MAX_TERM_CHARS} characters): {bad}", file=sys.stderr)
        return 2

    args.out.parent.mkdir(parents=True, exist_ok=True)
    with args.out.open("w", encoding="utf-8") as out:
        header = {"type": "run", "engine": f"{MODEL}/batch", "language": args.language or "auto",
                  "context_phrases": len(terms), "config": {"chunk_sec": 0, "split": args.split, "mode": "batch upload"}}
        out.write(json.dumps(header, ensure_ascii=False) + "\n")
        for sample in m.select(args.split):
            try:
                result, compute = transcribe(args.url, key, m.audio_path(sample), terms, args.language)
            except urllib.error.HTTPError as e:
                print(f"{sample.id}: xAI answered {e.code}", file=sys.stderr)
                return 1
            text = result.get("text", "").strip()
            duration = float(result.get("duration", 0))
            utterance = {"start": 0.0, "end": duration, "steps": [], "finish": {"called": False, "compute": 0.0, "truncated": False},
                         "streaming_final": text, "streaming_language": result.get("language"),
                         "offline": {"text": text, "language": result.get("language"), "compute": compute, "truncated": False}}
            record = {"type": "sample", "id": sample.id, "audio_sec": duration, "utterances": [utterance],
                      "streaming_final": text, "offline": text, "truncated": False}
            out.write(json.dumps(record, ensure_ascii=False) + "\n")
            out.flush()
            print(f"{sample.id}: {text!r}", file=sys.stderr)
        out.write(json.dumps({"type": "end"}) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
