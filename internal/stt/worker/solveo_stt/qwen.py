"""Adapter over qwen-asr 0.0.6 (vLLM backend): streaming steps with coalesced backlog and the offline final.

step() reaches into ASRStreamingState.buffer/audio_accum: those field names are
bound to the pinned qwen-asr version; re-check qwen_asr/inference/qwen3_asr.py
when the pin changes.
"""

from __future__ import annotations

import numpy as np

SAMPLE_RATE = 16000
CHUNK_SEC = 1.0
UNFIXED_CHUNK_NUM = 2
UNFIXED_TOKEN_NUM = 5
STEP_MAX_TOKENS = 32
FINAL_MAX_TOKENS = 256
MAX_CONTEXT_BYTES = 4096
CONTEXT_TEMPLATE = (
    "Technical interview in Russian with English IT terms. "
    "Write these terms exactly as listed when they are spoken:\n"
)
ARCHITECTURE = "Qwen3ASRForConditionalGeneration"


def build_context(phrases: list[str]) -> str:
    if not phrases:
        return ""
    text = CONTEXT_TEMPLATE + "\n".join(phrases)
    size = len(text.encode("utf-8"))
    if size > MAX_CONTEXT_BYTES:
        raise ValueError(f"словарь занимает {size} байт, допустимо {MAX_CONTEXT_BYTES}")
    return text


class _GenerateProbe:
    """Records whether the last generate call stopped at the token cap."""

    def __init__(self, model):
        self._model = model
        self.truncated = False

    def generate(self, *args, **kwargs):
        outputs = self._model.generate(*args, **kwargs)
        self.truncated = any(o.outputs[0].finish_reason == "length" for o in outputs)
        return outputs

    def __getattr__(self, name):
        return getattr(self._model, name)


class QwenModel:
    def __init__(self, asr):
        self._asr = asr
        self._probe = _GenerateProbe(asr.model)
        asr.model = self._probe
        self._params = {}

    def _cap(self, limit: int) -> None:
        params = self._params.get(limit)
        if params is None:
            params = self._asr.sampling_params.clone()
            params.max_tokens = limit
            self._params[limit] = params
        self._asr.sampling_params = params

    def init_state(self, context: str):
        return self._asr.init_streaming_state(
            context=context,
            language=None,
            unfixed_chunk_num=UNFIXED_CHUNK_NUM,
            unfixed_token_num=UNFIXED_TOKEN_NUM,
            chunk_size_sec=CHUNK_SEC,
        )

    def step(self, state, pcm: np.ndarray) -> tuple[str, bool]:
        buffer = np.concatenate([state.buffer, np.asarray(pcm, dtype=np.float32)])
        size = state.chunk_size_samples
        skip = max(0, buffer.shape[0] // size - 1) * size
        if skip:
            # Coalesce the backlog: earlier full chunks join the accumulated audio without their own decode.
            state.audio_accum = np.concatenate([state.audio_accum, buffer[:skip]])
            buffer = buffer[skip:]
        state.buffer = buffer
        self._cap(STEP_MAX_TOKENS)
        self._probe.truncated = False
        self._asr.streaming_transcribe(np.zeros(0, dtype=np.float32), state)
        return state.text, self._probe.truncated

    def transcribe(self, pcm: np.ndarray, context: str) -> str:
        self._cap(FINAL_MAX_TOKENS)
        result = self._asr.transcribe(audio=(np.asarray(pcm, dtype=np.float32), SAMPLE_RATE), context=context, language=None)
        return result[0].text


def check_registered() -> None:
    """qwen-asr registers its vLLM model under a bare except; fail loudly instead of 'architecture not supported'."""
    from vllm import ModelRegistry

    if ARCHITECTURE not in ModelRegistry.get_supported_archs():
        from qwen_asr.core.vllm_backend import Qwen3ASRForConditionalGeneration  # noqa: F401  surfaces the cause

        raise RuntimeError(f"qwen-asr не зарегистрировал {ARCHITECTURE} в vLLM")


def load(model_dir: str) -> QwenModel:
    from qwen_asr import Qwen3ASRModel

    check_registered()
    asr = Qwen3ASRModel.LLM(
        model=model_dir,
        max_new_tokens=FINAL_MAX_TOKENS,
        dtype="bfloat16",
        gpu_memory_utilization=0.6,
        max_model_len=2048,
        max_num_seqs=2,
        max_num_batched_tokens=2048,
        limit_mm_per_prompt={"audio": 1},
    )
    return QwenModel(asr)
