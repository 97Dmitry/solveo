"""Per-track utterance state machine: VAD, pre-roll, coalesced streaming partials, hybrid final.

Pure logic over 16 kHz mono float32; the model and the VAD are injected. The
server feeds audio as it arrives and runs work() on its single model thread.
"""

from __future__ import annotations

from dataclasses import asdict, dataclass
from typing import Literal, Protocol

import numpy as np

SAMPLE_RATE = 16000
WINDOW = 512  # Silero VAD window at 16 kHz
CHUNK = SAMPLE_RATE  # 1 s of new audio before a streaming step
PREROLL = 4800  # 300 ms kept before speech onset
MAX_UTTERANCE = 20 * SAMPLE_RATE  # forced final, then a new utterance continues


class Model(Protocol):
    def init_state(self, context: str) -> object: ...

    def step(self, state: object, pcm: np.ndarray) -> tuple[str, bool]: ...

    def transcribe(self, pcm: np.ndarray, context: str) -> str: ...


class Vad(Protocol):
    def window(self, samples: np.ndarray) -> Literal["start", "end"] | None: ...

    def reset(self) -> None: ...


@dataclass(frozen=True)
class Update:
    stream_id: str
    utterance_id: str
    revision: int
    kind: Literal["started", "partial", "final"]
    start_sample: int
    end_sample: int
    text: str

    def to_json(self) -> dict:
        return {"type": "update", **asdict(self)}


@dataclass
class _Utterance:
    id: str
    start: int
    audio: list[np.ndarray]
    pending: list[np.ndarray]
    length: int = 0
    pending_length: int = 0
    revision: int = 0
    end: int = 0
    state: object = None
    stalled: bool = False

    def append(self, samples: np.ndarray) -> None:
        self.audio.append(samples)
        self.pending.append(samples)
        self.length += samples.shape[0]
        self.pending_length += samples.shape[0]


def _concat(parts: list[np.ndarray]) -> np.ndarray:
    return np.concatenate(parts) if parts else np.zeros(0, dtype=np.float32)


class Track:
    def __init__(self, stream_id: str, context: str, model: Model, vad: Vad):
        self.stream_id = stream_id
        self.context = context
        self.model = model
        self.vad = vad
        self._rest = np.zeros(0, dtype=np.float32)
        self._position = 0  # samples consumed by the VAD
        self._ring = np.zeros(0, dtype=np.float32)
        self._utterance: _Utterance | None = None
        self._finals: list[_Utterance] = []
        self._counter = 0
        self._busy = False

    def _start(self, start: int, audio: np.ndarray) -> Update:
        self._counter += 1
        u = _Utterance(id=f"{self.stream_id}:{self._counter}", start=start, audio=[], pending=[])
        u.append(audio)
        self._utterance = u
        return Update(self.stream_id, u.id, 0, "started", start, start, "")

    def _end(self, end: int) -> None:
        u = self._utterance
        u.end = end
        u.pending, u.pending_length = [], 0
        self._finals.append(u)
        self._utterance = None

    def feed(self, pcm: np.ndarray) -> list[Update]:
        updates: list[Update] = []
        data = np.concatenate([self._rest, np.asarray(pcm, dtype=np.float32)])
        whole = data.shape[0] - data.shape[0] % WINDOW
        for offset in range(0, whole, WINDOW):
            w = data[offset : offset + WINDOW]
            event = self.vad.window(w)
            if self._utterance is None:
                if event == "start":
                    updates.append(self._start(self._position - self._ring.shape[0], np.concatenate([self._ring, w])))
                    self._ring = np.zeros(0, dtype=np.float32)
                else:
                    self._ring = np.concatenate([self._ring, w])[-PREROLL:]
            else:
                self._utterance.append(w)
                if event == "end":
                    self._end(self._position + WINDOW)
                elif self._utterance.length >= MAX_UTTERANCE:
                    end = self._position + WINDOW
                    self._end(end)
                    updates.append(self._start(end, np.zeros(0, dtype=np.float32)))
            self._position += WINDOW
        self._rest = data[whole:].copy()
        return updates

    def flush(self) -> None:
        """Barrier: end the active utterance with every sample received so far."""
        if self._utterance is None:
            return
        if self._rest.shape[0]:
            self._utterance.append(self._rest)
            self._position += self._rest.shape[0]
            self._rest = np.zeros(0, dtype=np.float32)
        self._end(self._position)
        self.vad.reset()

    def _can_step(self) -> bool:
        u = self._utterance
        return u is not None and not u.stalled and u.pending_length >= CHUNK

    @property
    def busy(self) -> bool:
        return self._busy

    def has_final(self) -> bool:
        return not self._busy and bool(self._finals)

    def has_work(self) -> bool:
        return not self._busy and (bool(self._finals) or self._can_step())

    def take_job(self) -> "Job | None":
        """Next model call for this track; at most one is outstanding. Finals go first."""
        if self._busy:
            return None
        if self._finals:
            u = self._finals.pop(0)
            job = Job("final", u, _concat(u.audio))
        elif self._can_step():
            u = self._utterance
            job = Job("step", u, _concat(u.pending))
            u.pending, u.pending_length = [], 0
        else:
            return None
        self._busy = True
        return job

    def run_job(self, job: "Job"):
        """The model call; safe to run on another thread while feed() continues."""
        if job.kind == "final":
            return self.model.transcribe(job.pcm, self.context), False
        if job.utterance.state is None:
            job.utterance.state = self.model.init_state(self.context)
        return self.model.step(job.utterance.state, job.pcm)

    def finish_job(self, job: "Job", result) -> list[Update]:
        self._busy = False
        text, truncated = result
        u = job.utterance
        if job.kind == "final":
            u.revision += 1
            return [Update(self.stream_id, u.id, u.revision, "final", u.start, u.end, text)]
        if truncated:
            # The decode ran into the token cap: later steps would repeat it. The final still comes offline.
            u.stalled = True
            return []
        u.revision += 1
        return [Update(self.stream_id, u.id, u.revision, "partial", u.start, u.start + u.length, text)]

    def work(self) -> list[Update]:
        job = self.take_job()
        return self.finish_job(job, self.run_job(job)) if job else []


@dataclass
class Job:
    kind: Literal["final", "step"]
    utterance: _Utterance
    pcm: np.ndarray
