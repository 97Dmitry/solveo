"""Container entrypoint: load Qwen3-ASR once (vLLM, BF16) and measure corpus samples.

Writes JSONL: one {"type": "run"} header, then one {"type": "sample"} record per
sample. Never downloads anything; run the container with --network none.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
import time
from importlib import metadata
from pathlib import Path

import numpy as np

from . import manifest as manifest_mod
from .runner import CONTEXT_TEMPLATES, SAMPLE_RATE, GenerateProbe, RunConfig, build_context, transcribe_sample


def read_pcm16k(path: Path) -> np.ndarray:
    import soundfile as sf
    import soxr

    data, rate = sf.read(str(path), dtype="float32", always_2d=True)
    mono = data.mean(axis=1)
    if rate != SAMPLE_RATE:
        mono = soxr.resample(mono, rate, SAMPLE_RATE, quality="HQ")
    return np.ascontiguousarray(mono, dtype=np.float32)


def weights_digest(model_dir: Path) -> str:
    digest = hashlib.sha256()
    for path in sorted(model_dir.glob("*.safetensors")):
        file_digest = hashlib.sha256()
        with path.open("rb") as f:
            for block in iter(lambda: f.read(1 << 24), b""):
                file_digest.update(block)
        digest.update(f"{path.name} {file_digest.hexdigest()}\n".encode())
    return digest.hexdigest()


def versions() -> dict:
    out = {}
    for name in ("qwen-asr", "vllm", "torch", "transformers", "soxr"):
        try:
            out[name] = metadata.version(name)
        except metadata.PackageNotFoundError:
            out[name] = None
    return out


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", required=True, type=Path, help="local model directory (read-only mount)")
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--manifest", type=Path)
    source.add_argument("--audio", type=Path, nargs="+", help="ad-hoc smoke files, no scoring")
    parser.add_argument("--split", default="dev", choices=("dev", "holdout", "all"))
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--chunk-sec", type=float, default=1.0)
    parser.add_argument("--unfixed-chunk-num", type=int, default=2)
    parser.add_argument("--unfixed-token-num", type=int, default=5)
    parser.add_argument("--language", default=None, help="forced language name, e.g. Russian; default auto")
    parser.add_argument("--context", choices=("none", "manifest"), default="manifest")
    parser.add_argument("--context-template", choices=CONTEXT_TEMPLATES, default="instruction")
    parser.add_argument("--pronunciations", type=Path, help="optional canonical-term to pronunciation JSON; use the worker's pronunciations.json to reproduce its context")
    parser.add_argument("--max-new-tokens", type=int, default=256, help="offline transcribe() token cap")
    parser.add_argument("--step-max-tokens", type=int, default=32, help="streaming decode step token cap")
    parser.add_argument("--max-model-len", type=int, default=2048)
    parser.add_argument("--gpu-memory-utilization", type=float, default=0.6)
    parser.add_argument("--max-num-seqs", type=int, default=2)
    # vLLM defaults (8192 batched tokens, encoder profiled with 20 audio items) exceed 8 GiB VRAM.
    parser.add_argument("--max-num-batched-tokens", type=int, default=2048)
    args = parser.parse_args(argv)

    samples, phrases = [], ()
    if args.manifest:
        m = manifest_mod.load(args.manifest)
        manifest_mod.verify_audio(m)
        leaks = manifest_mod.split_leaks(m)
        if leaks:
            print("split leakage: " + "; ".join(leaks), file=sys.stderr)
            return 1
        samples = [(s.id, m.audio_path(s)) for s in m.select(args.split)]
        phrases = m.context_phrases if args.context == "manifest" else ()
    else:
        samples = [(p.name, p) for p in args.audio]
    pronunciations = None
    pronunciation_digest = None
    if args.pronunciations:
        raw = args.pronunciations.read_bytes()
        pronunciations = json.loads(raw)
        if not isinstance(pronunciations, dict) or any(not isinstance(k, str) or not isinstance(v, str) for k, v in pronunciations.items()):
            parser.error("pronunciations must be a JSON object of string keys and values")
        pronunciation_digest = hashlib.sha256(raw).hexdigest()
    context = build_context(phrases, args.context_template)
    final_context = build_context(phrases, args.context_template, pronunciations=pronunciations)
    cfg = RunConfig(
        chunk_sec=args.chunk_sec,
        unfixed_chunk_num=args.unfixed_chunk_num,
        unfixed_token_num=args.unfixed_token_num,
        language=args.language,
        context=context,
        final_context=final_context,
        step_max_tokens=args.step_max_tokens,
        offline_max_tokens=args.max_new_tokens,
    )

    from qwen_asr import Qwen3ASRModel
    import torch
    from vllm import ModelRegistry

    if "Qwen3ASRForConditionalGeneration" not in ModelRegistry.get_supported_archs():
        # qwen-asr registers its vLLM model under a bare except; re-import to surface the cause.
        from qwen_asr.core.vllm_backend import Qwen3ASRForConditionalGeneration  # noqa: F401

        raise RuntimeError("qwen-asr did not register Qwen3ASRForConditionalGeneration with vLLM")

    started = time.perf_counter()
    asr = Qwen3ASRModel.LLM(
        model=str(args.model),
        max_new_tokens=args.max_new_tokens,
        dtype="bfloat16",
        gpu_memory_utilization=args.gpu_memory_utilization,
        max_model_len=args.max_model_len,
        max_num_seqs=args.max_num_seqs,
        max_num_batched_tokens=args.max_num_batched_tokens,
        limit_mm_per_prompt={"audio": 1},
    )
    cold_load = time.perf_counter() - started
    probe = GenerateProbe.install(asr)
    # The first requests compile/capture kernels (seconds); keep them out of the measured steps.
    started = time.perf_counter()
    transcribe_sample(asr, probe, np.zeros(int(2.5 * SAMPLE_RATE), dtype=np.float32), cfg)
    warmup = time.perf_counter() - started

    args.out.parent.mkdir(parents=True, exist_ok=True)
    with args.out.open("w", encoding="utf-8") as out:
        header = {
            "type": "run",
            "model_dir": str(args.model),
            "model_revision": (args.model / "REVISION").read_text().strip() if (args.model / "REVISION").exists() else None,
            "weights_sha256": weights_digest(args.model),
            "versions": versions(),
            "gpu": torch.cuda.get_device_name(0),
            "dtype": "bfloat16",
            "cold_load_sec": cold_load,
            "warmup_sec": warmup,
            "context_sha256": hashlib.sha256(context.encode()).hexdigest(),
            "final_context_sha256": hashlib.sha256(final_context.encode()).hexdigest(),
            "context_phrases": len(phrases),
            "config": {
                "chunk_sec": cfg.chunk_sec,
                "unfixed_chunk_num": cfg.unfixed_chunk_num,
                "unfixed_token_num": cfg.unfixed_token_num,
                "language": cfg.language or "auto",
                "max_utterance_sec": cfg.max_utterance_sec,
                "max_new_tokens": args.max_new_tokens,
                "step_max_tokens": args.step_max_tokens,
                "context_template": args.context_template,
                "pronunciations_sha256": pronunciation_digest,
                "max_model_len": args.max_model_len,
                "gpu_memory_utilization": args.gpu_memory_utilization,
                "max_num_seqs": args.max_num_seqs,
                "max_num_batched_tokens": args.max_num_batched_tokens,
                "split": args.split if args.manifest else None,
            },
        }
        out.write(json.dumps(header, ensure_ascii=False) + "\n")
        for sid, path in samples:
            result = transcribe_sample(asr, probe, read_pcm16k(path), cfg)
            out.write(json.dumps({"type": "sample", "id": sid, **result}, ensure_ascii=False) + "\n")
            out.flush()
            print(f"{sid}: {result['streaming_final']!r} | offline {result['offline']!r}", file=sys.stderr)
        out.write(json.dumps({"type": "end"}) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
