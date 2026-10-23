"""python -m solveo_stt serve --model DIR [--port 8765] | download --out DIR"""

from __future__ import annotations

import argparse
import asyncio
import os
import sys
from pathlib import Path

import numpy as np

from .server import MODEL_ID, MODEL_REVISION

MODEL_FILES = [
    "chat_template.json", "config.json", "generation_config.json", "merges.txt",
    "model-00001-of-00002.safetensors", "model-00002-of-00002.safetensors", "model.safetensors.index.json",
    "preprocessor_config.json", "tokenizer_config.json", "vocab.json",
]


def _load_and_warm(model_dir: str):
    from . import qwen

    model = qwen.load(model_dir)
    # The first requests compile kernels for seconds; ready means the first real phrase is fast.
    model.transcribe(np.zeros(int(2.5 * 16000), dtype=np.float32), "")
    model.step(model.init_state(""), np.zeros(int(1.2 * 16000), dtype=np.float32))
    return model


def serve(model_dir: str, host: str, port: int) -> int:
    import torch

    from .server import Server
    from .vad import SileroVad

    token = os.environ.get("SOLVEO_STT_TOKEN", "")
    if len(token) < 32:
        print("SOLVEO_STT_TOKEN is missing or too short", file=sys.stderr)
        return 2
    torch.set_num_threads(1)

    async def run() -> int:
        stop = asyncio.Event()
        loop = asyncio.get_running_loop()

        async def load():
            return await loop.run_in_executor(None, _load_and_warm, model_dir)

        server = Server(load, SileroVad, token, on_exit=stop.set)
        await server.start(host, port)
        print(f"solveo-stt listening on {host}:{port}, loading {model_dir}", flush=True)
        await stop.wait()
        await server.stop()
        return 1 if server.load_error else 0

    return asyncio.run(run())


def download(out: str) -> int:
    os.environ["HF_HUB_OFFLINE"] = "0"
    from huggingface_hub import snapshot_download

    target = Path(out)
    (target / "REVISION").unlink(missing_ok=True)
    snapshot_download(repo_id=MODEL_ID, revision=MODEL_REVISION, local_dir=target, allow_patterns=MODEL_FILES)
    # Written last: its presence means the download completed.
    (target / "REVISION").write_text(MODEL_REVISION + "\n")
    print(f"downloaded {MODEL_ID}@{MODEL_REVISION}", flush=True)
    return 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(prog="solveo_stt")
    sub = parser.add_subparsers(dest="command", required=True)
    s = sub.add_parser("serve")
    s.add_argument("--model", required=True)
    s.add_argument("--host", default="0.0.0.0")
    s.add_argument("--port", type=int, default=8765)
    d = sub.add_parser("download")
    d.add_argument("--out", required=True)
    args = parser.parse_args(argv)
    if args.command == "serve":
        return serve(args.model, args.host, args.port)
    return download(args.out)


if __name__ == "__main__":
    raise SystemExit(main())
