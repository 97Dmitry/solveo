"""Container entrypoint for the Nemotron 3.5 ASR comparison; same JSONL schema as run_qwen.

Never downloads anything; run the container with --network none.
"""

from __future__ import annotations

import argparse
import json
import sys
import time
from pathlib import Path

import numpy as np

from . import manifest as manifest_mod
from .nemotron import Engine
from .run_qwen import read_pcm16k, versions, weights_digest
from .runner import SAMPLE_RATE, transcribe_pieces


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", required=True, type=Path)
    parser.add_argument("--manifest", required=True, type=Path)
    parser.add_argument("--split", default="dev", choices=("dev", "holdout", "all"))
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--language", default="auto", help="auto or a locale such as ru-RU")
    parser.add_argument("--lookahead", type=int, default=13, help="right context frames: 0, 3, 6 or 13 (80..1120 ms)")
    parser.add_argument("--max-utterance-sec", type=float, default=20.0)
    args = parser.parse_args(argv)

    m = manifest_mod.load(args.manifest)
    manifest_mod.verify_audio(m)
    leaks = manifest_mod.split_leaks(m)
    if leaks:
        print("split leakage: " + "; ".join(leaks), file=sys.stderr)
        return 1

    import torch

    started = time.perf_counter()
    engine = Engine(str(args.model), args.language, args.lookahead)
    cold_load = time.perf_counter() - started
    started = time.perf_counter()
    engine.transcribe_utterance(np.zeros(int(2.5 * SAMPLE_RATE), dtype=np.float32))
    warmup = time.perf_counter() - started
    torch.cuda.reset_peak_memory_stats()

    args.out.parent.mkdir(parents=True, exist_ok=True)
    with args.out.open("w", encoding="utf-8") as out:
        header = {
            "type": "run",
            "engine": "nemotron-3.5-asr-streaming-0.6b/transformers",
            "model_revision": (args.model / "REVISION").read_text().strip() if (args.model / "REVISION").exists() else None,
            "weights_sha256": weights_digest(args.model),
            "versions": versions(),
            "gpu": torch.cuda.get_device_name(0),
            "dtype": "bfloat16",
            "cold_load_sec": cold_load,
            "warmup_sec": warmup,
            "context_phrases": 0,
            "config": {
                # Report treats chunk_sec as the wait before a word can be decoded: the streaming latency here.
                "chunk_sec": engine.latency_sec,
                "lookahead": args.lookahead,
                "language": args.language,
                "max_utterance_sec": args.max_utterance_sec,
                "split": args.split,
            },
        }
        out.write(json.dumps(header, ensure_ascii=False) + "\n")
        for sample in m.select(args.split):
            result = transcribe_pieces(read_pcm16k(m.audio_path(sample)), args.max_utterance_sec, engine.transcribe_utterance)
            out.write(json.dumps({"type": "sample", "id": sample.id, **result}, ensure_ascii=False) + "\n")
            out.flush()
            print(f"{sample.id}: {result['streaming_final']!r} | offline {result['offline']!r}", file=sys.stderr)
        out.write(json.dumps({"type": "end", "torch_peak_allocated_mib": torch.cuda.max_memory_allocated() >> 20,
                              "torch_peak_reserved_mib": torch.cuda.max_memory_reserved() >> 20}) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
