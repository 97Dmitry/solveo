"""Per-sample Qwen3-ASR measurement: streaming final, offline pass, decode costs.

Works against the qwen_asr 0.0.6 wrapper (or a test fake with the same
methods). PCM is pushed in 100 ms transport frames; the wrapper aggregates them
into model chunks. Each utterance is capped at max_utterance_sec with a fresh
streaming state, and the offline pass sees exactly the same PCM.
"""

from __future__ import annotations

import copy
import time
from dataclasses import dataclass

import numpy as np

SAMPLE_RATE = 16000
TRANSPORT_FRAME = SAMPLE_RATE // 10
MAX_CONTEXT_BYTES = 4096
# Frozen before holdout; changing it invalidates earlier dev results.
CONTEXT_TEMPLATE = (
    "Technical interview in Russian with English IT terms. "
    "Write these terms exactly as listed when they are spoken:\n"
)


@dataclass(frozen=True)
class RunConfig:
    chunk_sec: float = 1.0
    unfixed_chunk_num: int = 2
    unfixed_token_num: int = 5
    language: str | None = None
    context: str = ""
    final_context: str | None = None
    max_utterance_sec: float = 20.0
    # qwen-asr shares one SamplingParams between streaming steps and offline transcribe(); a step only
    # needs the rolled-back tokens plus one chunk, so a small cap bounds a runaway decode. None keeps it.
    step_max_tokens: int | None = None
    offline_max_tokens: int | None = None


CONTEXT_TEMPLATES = ("instruction", "list")


def build_context(phrases: tuple[str, ...], template: str = "instruction", *, pronunciations: dict[str, str] | None = None) -> str:
    """'instruction': CONTEXT_TEMPLATE + one term per line; 'list': the terms alone, comma-separated."""
    if template not in CONTEXT_TEMPLATES:
        raise ValueError(f"unknown context template {template!r}")
    if not phrases:
        return ""
    text = CONTEXT_TEMPLATE + "\n".join(phrases) if template == "instruction" else ", ".join(phrases)
    size = len(text.encode("utf-8"))
    if size > MAX_CONTEXT_BYTES:
        raise ValueError(f"context is {size} bytes, limit {MAX_CONTEXT_BYTES}")
    if pronunciations:
        if template != "instruction":
            raise ValueError("pronunciations require the instruction template")
        hints = {term.casefold(): hint for term, hint in pronunciations.items()}
        remaining = MAX_CONTEXT_BYTES - size
        enriched = []
        for phrase in phrases:
            hint = hints.get(phrase.casefold())
            note = f" (pronunciation: {hint})" if hint else ""
            cost = len(note.encode("utf-8"))
            if cost <= remaining:
                enriched.append(phrase + note)
                remaining -= cost
            else:
                enriched.append(phrase)
        return CONTEXT_TEMPLATE + "\n".join(enriched)
    return text


class GenerateProbe:
    """Wraps the model's generate to time each call and catch length-truncated output."""

    def __init__(self, model):
        self._model = model
        self._calls: list[tuple[float, bool]] = []

    @classmethod
    def install(cls, asr) -> "GenerateProbe":
        probe = cls(asr.model)
        asr.model = probe
        return probe

    def generate(self, *args, **kwargs):
        started = time.perf_counter()
        outputs = self._model.generate(*args, **kwargs)
        elapsed = time.perf_counter() - started
        truncated = any(o.outputs[0].finish_reason == "length" for o in outputs)
        self._calls.append((elapsed, truncated))
        return outputs

    def __getattr__(self, name):
        return getattr(self._model, name)

    def take(self) -> list[tuple[float, bool]]:
        calls, self._calls = self._calls, []
        return calls


def _summary(calls: list[tuple[float, bool]]) -> dict:
    return {
        "called": bool(calls),
        "compute": sum(c for c, _ in calls),
        "truncated": any(t for _, t in calls),
    }


def _set_max_tokens(asr, limit: int | None) -> None:
    if limit is None:
        return
    params = asr.sampling_params
    params = params.clone() if hasattr(params, "clone") else copy.copy(params)
    params.max_tokens = limit
    asr.sampling_params = params


def transcribe_utterance(asr, probe: GenerateProbe, pcm: np.ndarray, cfg: RunConfig) -> dict:
    probe.take()
    _set_max_tokens(asr, cfg.step_max_tokens)
    state = asr.init_streaming_state(
        context=cfg.context,
        language=cfg.language,
        unfixed_chunk_num=cfg.unfixed_chunk_num,
        unfixed_token_num=cfg.unfixed_token_num,
        chunk_size_sec=cfg.chunk_sec,
    )
    chunk = int(round(cfg.chunk_sec * SAMPLE_RATE))
    steps = []
    for start in range(0, pcm.shape[0], TRANSPORT_FRAME):
        before = state.chunk_id
        asr.streaming_transcribe(pcm[start : start + TRANSPORT_FRAME], state)
        calls = probe.take()
        for n, (compute, truncated) in enumerate(calls, before + 1):
            steps.append({"audio_end": n * chunk / SAMPLE_RATE, "compute": compute, "truncated": truncated, "text": state.text})
    asr.finish_streaming_transcribe(state)
    finish = _summary(probe.take())
    streaming_final = state.text

    _set_max_tokens(asr, cfg.offline_max_tokens)
    final_context = cfg.context if cfg.final_context is None else cfg.final_context
    result = asr.transcribe(audio=(pcm, SAMPLE_RATE), context=final_context, language=cfg.language)[0]
    offline = {"text": result.text, "language": result.language, **_summary(probe.take())}
    del offline["called"]
    return {
        "steps": steps,
        "finish": finish,
        "streaming_final": streaming_final,
        "streaming_language": state.language,
        "offline": offline,
    }


def transcribe_sample(asr, probe: GenerateProbe, pcm: np.ndarray, cfg: RunConfig) -> dict:
    return transcribe_pieces(pcm, cfg.max_utterance_sec, lambda piece: transcribe_utterance(asr, probe, piece, cfg))


def transcribe_pieces(pcm: np.ndarray, max_utterance_sec: float, transcribe) -> dict:
    """Cut forced utterance boundaries and merge per-utterance results; shared by all engines."""
    limit = int(round(max_utterance_sec * SAMPLE_RATE))
    utterances = []
    for start in range(0, max(pcm.shape[0], 1), limit):
        piece = pcm[start : start + limit]
        utt = transcribe(piece)
        utt["start"] = start / SAMPLE_RATE
        utt["end"] = (start + piece.shape[0]) / SAMPLE_RATE
        utterances.append(utt)
    truncated = any(
        u["finish"]["truncated"] or u["offline"]["truncated"] or any(s["truncated"] for s in u["steps"])
        for u in utterances
    )
    return {
        "audio_sec": pcm.shape[0] / SAMPLE_RATE,
        "utterances": utterances,
        "streaming_final": " ".join(u["streaming_final"] for u in utterances if u["streaming_final"]),
        "offline": " ".join(u["offline"]["text"] for u in utterances if u["offline"]["text"]),
        "truncated": truncated,
    }
