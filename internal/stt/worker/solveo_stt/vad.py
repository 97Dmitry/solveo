"""Silero VAD adapter; the model ships inside the silero-vad package, no download."""

from __future__ import annotations

import numpy as np

from .engine import SAMPLE_RATE

THRESHOLD = 0.5
ENDPOINT_SILENCE_MS = 800


class SileroVad:
    def __init__(self):
        import torch
        from silero_vad import VADIterator, load_silero_vad

        self._torch = torch
        self._iterator = VADIterator(
            load_silero_vad(),
            threshold=THRESHOLD,
            sampling_rate=SAMPLE_RATE,
            min_silence_duration_ms=ENDPOINT_SILENCE_MS,
            speech_pad_ms=0,
        )

    def window(self, samples: np.ndarray):
        event = self._iterator(self._torch.from_numpy(np.ascontiguousarray(samples, dtype=np.float32)), return_seconds=False)
        if not event:
            return None
        return "start" if "start" in event else "end"

    def reset(self) -> None:
        self._iterator.reset_states()
