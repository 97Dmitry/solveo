"""Nemotron 3.5 ASR (transformers >= 5.13) measurement with the same result schema as runner.py.

Cache-aware RNN-T streaming: each chunk is encoded once and never revisited, so
there is no finish decode; the final is the text after the last (zero-padded)
chunk. The offline pass runs the whole utterance in one non-streaming generate.
No context/word boosting: transformers does not expose it.
"""

from __future__ import annotations

import re
import time

import numpy as np

SAMPLE_RATE = 16000
_LANG_TAG = re.compile(r"<([a-z]{2}-[A-Z]{2})>")


def chunk_spans(n_samples, first_samples, first_mel, mel_per_chunk, hop, n_fft, samples_per_chunk):
    """Sample ranges of the streaming chunks, following the processor's chunk geometry.

    Unlike the upstream example, chunks continue until all audio is covered; the last
    one is zero-padded (silence after speech in a live stream).
    """
    spans = [(0, first_samples)]
    mel = first_mel
    while mel * hop < n_samples:
        start = mel * hop - n_fft // 2
        spans.append((start, start + samples_per_chunk))
        mel += mel_per_chunk
    return spans


def steps_from_events(chunks, tokens):
    """chunks: (audio_end_sample, t_start, t_done); tokens: (t, count). Cumulative tokens per step."""
    steps, total, i = [], 0, 0
    for n, (end, t_start, t_done) in enumerate(chunks):
        last = n == len(chunks) - 1
        while i < len(tokens) and (last or tokens[i][0] <= t_done):
            total += tokens[i][1]
            i += 1
        steps.append({"audio_end": end / SAMPLE_RATE, "compute": t_done - t_start, "tokens": total})
    return steps


class Engine:
    def __init__(self, model_dir: str, language: str, lookahead: int):
        import torch
        from transformers import AutoModelForRNNT, AutoProcessor

        self.torch = torch
        self.processor = AutoProcessor.from_pretrained(model_dir)
        self.processor.set_num_lookahead_tokens(lookahead)
        self.model = AutoModelForRNNT.from_pretrained(model_dir, dtype=torch.bfloat16, device_map="cuda")
        self.model.eval()
        self.language = language
        self.lookahead = lookahead
        self.latency_sec = self.processor.streaming_latency_ms / 1000

    def _features(self, pcm, first: bool):
        p = self.processor
        inputs = p(pcm, sampling_rate=SAMPLE_RATE, is_streaming=True, is_first_audio_chunk=first,
                   language=self.language, return_tensors="pt")
        return inputs.to(self.model.device, dtype=self.model.dtype)

    def _decode(self, ids, keep_special=False):
        return self.processor.tokenizer.decode(ids, skip_special_tokens=not keep_special).strip()

    def stream(self, pcm: np.ndarray):
        from transformers.generation.streamers import BaseStreamer

        p = self.processor
        fe = p.feature_extractor
        spans = chunk_spans(pcm.shape[0], p.num_samples_first_audio_chunk, p.num_mel_frames_first_audio_chunk,
                            p.num_mel_frames_per_audio_chunk, fe.hop_length, fe.n_fft, p.num_samples_per_audio_chunk)

        def segment(start, end):
            seg = pcm[start:end]
            return np.pad(seg, (0, end - start - seg.shape[0])) if seg.shape[0] < end - start else seg

        first = self._features(segment(*spans[0]), first=True)
        token_ids, token_events, chunk_events = [], [], []

        class Recorder(BaseStreamer):
            def put(self, value):
                ids = value.reshape(-1).tolist()
                token_ids.extend(ids)
                token_events.append((time.perf_counter(), len(ids)))

            def end(self):
                pass

        def features():
            for n, (start, end) in enumerate(spans):
                feats = (first.input_features[:, : p.num_mel_frames_first_audio_chunk, :] if n == 0
                         else self._features(segment(start, end), first=False).input_features)
                started = time.perf_counter()
                yield feats
                chunk_events.append((end, started, time.perf_counter()))

        with self.torch.inference_mode():
            self.model.generate(**{**first, "input_features": features()}, streamer=Recorder())
            if len(chunk_events) < len(spans):  # generate returned without asking past the last chunk
                end, started = spans[-1][1], chunk_events[-1][2] if chunk_events else time.perf_counter()
                chunk_events.append((end, started, time.perf_counter()))
        steps = steps_from_events(chunk_events, token_events)
        for s in steps:
            s["text"] = self._decode(token_ids[: s.pop("tokens")])
            s["truncated"] = False
        return steps, self._decode(token_ids), self._language(self._decode(token_ids, keep_special=True))

    def offline(self, pcm: np.ndarray):
        inputs = self.processor(pcm, sampling_rate=SAMPLE_RATE, language=self.language, return_tensors="pt")
        inputs = inputs.to(self.model.device, dtype=self.model.dtype)
        started = time.perf_counter()
        with self.torch.inference_mode():
            out = self.model.generate(**inputs, return_dict_in_generate=True)
        compute = time.perf_counter() - started
        ids = out.sequences[0].tolist()
        return {"text": self._decode(ids), "language": self._language(self._decode(ids, keep_special=True)),
                "compute": compute, "truncated": False}

    @staticmethod
    def _language(text: str):
        found = _LANG_TAG.findall(text)
        return found[-1] if found else None

    def transcribe_utterance(self, pcm: np.ndarray) -> dict:
        steps, final, language = self.stream(pcm)
        return {
            "steps": steps,
            "finish": {"called": False, "compute": 0.0, "truncated": False},
            "streaming_final": final,
            "streaming_language": language,
            "offline": self.offline(pcm),
        }
