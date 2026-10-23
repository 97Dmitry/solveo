"""WebSocket protocol v1: authenticated sessions, per-stream tracks, one serialized model thread, lease.

Binary frames: uint32 LE header length, JSON {stream_id, seq, rate, channels},
float32 LE interleaved samples. Audio is downmixed and resampled to 16 kHz here.
"""

from __future__ import annotations

import asyncio
import json
import struct
import time
import traceback
from concurrent.futures import ThreadPoolExecutor
from http import HTTPStatus

import numpy as np
import soxr
from websockets.asyncio.server import serve
from websockets.exceptions import ConnectionClosed

from . import qwen
from .engine import SAMPLE_RATE, Track

MAX_MESSAGE = 64 * 1024
MODEL_ID = "Qwen/Qwen3-ASR-1.7B"
MODEL_REVISION = "7278e1e70fe206f11671096ffdd38061171dd6e5"


def pick_next(entries, last: int):
    """Round-robin after the last served entry; any pending final beats any streaming step."""
    n = len(entries)
    order = [(last + 1 + i) % n for i in range(n)]
    for i in order:
        if entries[i].has_final():
            return i
    for i in order:
        if entries[i].has_work():
            return i
    return None


class _Stream:
    def __init__(self, ws, track: Track):
        self.ws = ws
        self.track = track
        self.seq = 0
        self.rate = 0
        self.channels = 0
        self.resampler = None
        self.flushes: list[str] = []

    def has_final(self) -> bool:
        return self.track.has_final()

    def has_work(self) -> bool:
        return self.track.has_work()


class Server:
    def __init__(self, load_model, make_vad, token: str, lease_sec: float = 30.0, on_exit=None):
        self._load_model = load_model
        self._make_vad = make_vad
        self._auth = f"Bearer {token}"
        self._lease_sec = lease_sec
        self._on_exit = on_exit
        self._entries: list[_Stream] = []
        self._sessions = 0
        self._idle_since = time.monotonic()
        self._wake = asyncio.Event()
        self._executor = ThreadPoolExecutor(max_workers=1)
        self._tasks: list[asyncio.Task] = []
        self.model = None
        self.ready = False
        self.load_error: BaseException | None = None

    async def start(self, host: str, port: int) -> int:
        self._server = await serve(self._session, host, port, process_request=self._process_request, max_size=MAX_MESSAGE)
        self._tasks = [asyncio.create_task(t) for t in (self._load(), self._schedule(), self._lease())]
        return self._server.sockets[0].getsockname()[1]

    async def stop(self) -> None:
        for task in self._tasks:
            task.cancel()
        self._server.close()
        await self._server.wait_closed()
        self._executor.shutdown(wait=False, cancel_futures=True)

    async def _load(self) -> None:
        try:
            self.model = await self._load_model()
        except Exception as e:  # the process exits; Solveo reads the container log
            traceback.print_exc()
            self.load_error = e
            if self._on_exit:
                self._on_exit()
            return
        self.ready = True
        self._idle_since = time.monotonic()

    def _process_request(self, connection, request):
        authorized = request.headers.get("Authorization") == self._auth
        if request.path == "/health":
            if not authorized:
                return connection.respond(HTTPStatus.UNAUTHORIZED, "unauthorized\n")
            if not self.ready:
                return connection.respond(HTTPStatus.SERVICE_UNAVAILABLE, "loading\n")
            body = {"ready": True, "model": MODEL_ID, "revision": MODEL_REVISION, "dtype": "bfloat16"}
            return connection.respond(HTTPStatus.OK, json.dumps(body))
        if request.path != "/v1":
            return connection.respond(HTTPStatus.NOT_FOUND, "not found\n")
        if not authorized:
            return connection.respond(HTTPStatus.UNAUTHORIZED, "unauthorized\n")
        if not self.ready:
            return connection.respond(HTTPStatus.SERVICE_UNAVAILABLE, "loading\n")
        return None

    async def _session(self, ws) -> None:
        self._sessions += 1
        streams: dict[str, _Stream] = {}
        try:
            async for message in ws:
                if isinstance(message, bytes):
                    await self._frame(ws, streams, message)
                else:
                    await self._control(ws, streams, json.loads(message))
        except ConnectionClosed:
            pass
        finally:
            for entry in streams.values():
                self._drop(entry)
            self._sessions -= 1
            if self._sessions == 0:
                self._idle_since = time.monotonic()

    def _drop(self, entry: _Stream) -> None:
        if entry in self._entries:
            self._entries.remove(entry)

    async def _send(self, ws, message: dict) -> None:
        try:
            await ws.send(json.dumps(message, ensure_ascii=False))
        except ConnectionClosed:
            pass

    async def _fail(self, ws, streams, stream_id: str, message: str) -> None:
        entry = streams.pop(stream_id, None)
        if entry:
            self._drop(entry)
        await self._send(ws, {"type": "error", "stream_id": stream_id, "message": message})

    async def _control(self, ws, streams, msg: dict) -> None:
        kind, sid = msg.get("type"), str(msg.get("stream_id", ""))
        if kind == "open":
            if sid == "" or sid in streams:
                return await self._fail(ws, streams, sid, "поток уже открыт или без идентификатора")
            try:
                phrases = [str(p) for p in msg.get("context", [])]
                context = qwen.build_context(phrases)
                partial_context = qwen.build_context(phrases, pronunciations=False)
            except ValueError as e:
                return await self._send(ws, {"type": "error", "stream_id": sid, "message": str(e)})
            entry = _Stream(ws, Track(sid, context, self.model, self._make_vad(), partial_context=partial_context))
            streams[sid] = entry
            self._entries.append(entry)
            return await self._send(ws, {"type": "opened", "stream_id": sid})
        entry = streams.get(sid)
        if entry is None:
            return await self._send(ws, {"type": "error", "stream_id": sid, "message": "поток не открыт"})
        if kind == "flush":
            entry.track.flush()
            entry.flushes.append(str(msg.get("request_id", "")))
            self._wake.set()
            await self._check_flush(entry)
        elif kind == "close":
            streams.pop(sid, None)
            self._drop(entry)

    async def _frame(self, ws, streams, message: bytes) -> None:
        (size,) = struct.unpack_from("<I", message)
        header = json.loads(message[4 : 4 + size])
        sid = str(header.get("stream_id", ""))
        entry = streams.get(sid)
        if entry is None:
            return await self._send(ws, {"type": "error", "stream_id": sid, "message": "поток не открыт"})
        seq, rate, channels = int(header["seq"]), int(header["rate"]), int(header["channels"])
        if seq != entry.seq + 1:
            return await self._fail(ws, streams, sid, f"пропуск в последовательности кадров: {entry.seq + 1} вместо {seq}")
        if not 8000 <= rate <= 192000 or not 1 <= channels <= 8 or (entry.rate and (rate, channels) != (entry.rate, entry.channels)):
            return await self._fail(ws, streams, sid, "неподдерживаемый или изменившийся формат звука")
        data = np.frombuffer(message[4 + size :], dtype="<f4")
        if data.shape[0] % channels:
            return await self._fail(ws, streams, sid, "неполный кадр звука")
        entry.seq, entry.rate, entry.channels = seq, rate, channels
        mono = data.reshape(-1, channels).mean(axis=1, dtype=np.float32)
        if rate != SAMPLE_RATE:
            if entry.resampler is None:
                entry.resampler = soxr.ResampleStream(rate, SAMPLE_RATE, 1, dtype="float32", quality="HQ")
            mono = entry.resampler.resample_chunk(mono)
        for update in entry.track.feed(mono):
            await self._send(ws, update.to_json())
        self._wake.set()

    async def _check_flush(self, entry: _Stream) -> None:
        if entry.flushes and not entry.track.busy and not entry.track.has_work():
            for request_id in entry.flushes:
                await self._send(entry.ws, {"type": "flushed", "stream_id": entry.track.stream_id, "request_id": request_id})
            entry.flushes = []

    async def _schedule(self) -> None:
        loop = asyncio.get_running_loop()
        last = -1
        while True:
            await self._wake.wait()
            self._wake.clear()
            while (i := pick_next(self._entries, last)) is not None:
                last, entry = i, self._entries[i]
                job = entry.track.take_job()
                try:
                    result = await loop.run_in_executor(self._executor, entry.track.run_job, job)
                except Exception as e:  # a failed decode ends this stream, not the session
                    entry.track.finish_job(job, ("", True))
                    self._drop(entry)
                    await self._send(entry.ws, {"type": "error", "stream_id": entry.track.stream_id, "message": f"ошибка распознавания: {e}"})
                    continue
                updates = entry.track.finish_job(job, result)
                if entry in self._entries:
                    for update in updates:
                        await self._send(entry.ws, update.to_json())
                    await self._check_flush(entry)

    async def _lease(self) -> None:
        while True:
            await asyncio.sleep(min(1.0, self._lease_sec / 4))
            if self.ready and self._sessions == 0 and time.monotonic() - self._idle_since >= self._lease_sec:
                if self._on_exit:
                    self._on_exit()
                return
