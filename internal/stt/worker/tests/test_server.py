import asyncio
import json
import struct
import time
import unittest
import urllib.error
import urllib.request

import numpy as np
from websockets.asyncio.client import connect
from websockets.exceptions import ConnectionClosed, InvalidStatus

from solveo_stt import server
from tests.test_engine import FakeModel, FakeVad

TOKEN = "t" * 64


def frame(stream_id, seq, samples, rate=48000, channels=2):
    header = json.dumps({"stream_id": stream_id, "seq": seq, "rate": rate, "channels": channels}).encode()
    return struct.pack("<I", len(header)) + header + np.asarray(samples, dtype="<f4").tobytes()


def tone(seconds, rate=48000, channels=2, value=0.5):
    return np.full(int(seconds * rate) * channels, value, dtype=np.float32)


class Harness:
    def __init__(self, ready=True, lease=30.0, model=None):
        self.loaded = asyncio.Event()
        if ready:
            self.loaded.set()
        self.model = model or FakeModel()
        self.exited = asyncio.Event()

        async def load():
            await self.loaded.wait()
            return self.model

        self.server = server.Server(load, lambda: FakeVad(end_after_windows=25), TOKEN, lease_sec=lease,
                                    on_exit=self.exited.set)

    async def __aenter__(self):
        self.port = await self.server.start("127.0.0.1", 0)
        return self

    async def __aexit__(self, *exc):
        await self.server.stop()

    def connect(self, token=TOKEN):
        return connect(f"ws://127.0.0.1:{self.port}/v1", additional_headers={"Authorization": f"Bearer {token}"})

    async def health(self, token=TOKEN):
        def get():
            req = urllib.request.Request(f"http://127.0.0.1:{self.port}/health", headers={"Authorization": f"Bearer {token}"})
            try:
                with urllib.request.urlopen(req, timeout=5) as resp:
                    return resp.status, json.loads(resp.read())
            except urllib.error.HTTPError as e:
                return e.code, None

        return await asyncio.to_thread(get)


async def until(ws, predicate, timeout=5.0):
    seen = []
    deadline = time.monotonic() + timeout
    while True:
        msg = json.loads(await asyncio.wait_for(ws.recv(), deadline - time.monotonic()))
        seen.append(msg)
        if predicate(msg):
            return seen


async def open_stream(ws, stream_id, context=()):
    await ws.send(json.dumps({"type": "open", "stream_id": stream_id, "track": "system", "context": list(context)}))
    return (await until(ws, lambda m: m.get("stream_id") == stream_id and m["type"] in ("opened", "error")))[-1]


class ServerTest(unittest.IsolatedAsyncioTestCase):
    async def test_rejects_bad_token(self):
        async with Harness() as h:
            with self.assertRaises(InvalidStatus) as ctx:
                async with h.connect(token="bad"):
                    pass
            self.assertEqual(ctx.exception.response.status_code, 401)
            self.assertEqual((await h.health(token="bad"))[0], 401)

    async def test_health_before_and_after_ready(self):
        async with Harness(ready=False) as h:
            self.assertEqual((await h.health())[0], 503)
            with self.assertRaises(InvalidStatus):
                async with h.connect():
                    pass
            h.loaded.set()
            for _ in range(50):
                status, body = await h.health()
                if status == 200:
                    break
                await asyncio.sleep(0.02)
            self.assertEqual(status, 200)
            self.assertEqual(body["model"], "Qwen/Qwen3-ASR-1.7B")
            self.assertTrue(body["ready"])

    async def test_open_push_flush_yields_final(self):
        async with Harness() as h, h.connect() as ws:
            self.assertEqual((await open_stream(ws, "a"))["type"], "opened")
            samples = np.concatenate([tone(2), tone(1, value=0.0)])
            step = 4800 * 2
            for seq, start in enumerate(range(0, samples.shape[0], step), 1):
                await ws.send(frame("a", seq, samples[start : start + step]))
            await ws.send(json.dumps({"type": "flush", "stream_id": "a", "request_id": "r1"}))
            seen = await until(ws, lambda m: m["type"] == "flushed")
            kinds = [m["kind"] for m in seen if m["type"] == "update"]
            self.assertEqual(kinds[0], "started")
            self.assertIn("final", kinds)
            self.assertEqual(seen[-1], {"type": "flushed", "stream_id": "a", "request_id": "r1"})

    async def test_idle_flush_answers_immediately(self):
        async with Harness() as h, h.connect() as ws:
            await open_stream(ws, "a")
            await ws.send(json.dumps({"type": "flush", "stream_id": "a", "request_id": "r"}))
            self.assertEqual((await until(ws, lambda m: True))[-1]["type"], "flushed")

    async def test_sequence_gap_closes_stream(self):
        async with Harness() as h, h.connect() as ws:
            await open_stream(ws, "a")
            await ws.send(frame("a", 1, tone(0.1)))
            await ws.send(frame("a", 3, tone(0.1)))
            msg = (await until(ws, lambda m: m["type"] == "error"))[-1]
            self.assertEqual(msg["stream_id"], "a")
            self.assertEqual((await open_stream(ws, "b"))["type"], "opened")

    async def test_oversized_frame_is_fatal(self):
        async with Harness() as h, h.connect() as ws:
            await open_stream(ws, "a")
            await ws.send(frame("a", 1, np.zeros(20000, dtype=np.float32)))
            with self.assertRaises(ConnectionClosed) as ctx:
                await asyncio.wait_for(ws.recv(), 5)
            self.assertEqual(ctx.exception.rcvd.code, 1009)

    async def test_context_over_limit_is_stream_error(self):
        async with Harness() as h, h.connect() as ws:
            msg = await open_stream(ws, "a", context=["x" * 100] * 60)
            self.assertEqual(msg["type"], "error")

    async def test_model_load_failure_exits(self):
        exited = asyncio.Event()

        async def load():
            raise RuntimeError("no gpu")

        srv = server.Server(load, FakeVad, TOKEN, on_exit=exited.set)
        await srv.start("127.0.0.1", 0)
        try:
            await asyncio.wait_for(exited.wait(), 2)
            self.assertIsInstance(srv.load_error, RuntimeError)
            self.assertFalse(srv.ready)
        finally:
            await srv.stop()

    async def test_lease_exits_without_session(self):
        async with Harness(lease=0.2) as h:
            async with h.connect():
                await asyncio.sleep(0.4)
                self.assertFalse(h.exited.is_set())
            await asyncio.wait_for(h.exited.wait(), 2)


class FakeEntry:
    def __init__(self, final=False, step=False):
        self.final, self.step = final, step

    def has_final(self):
        return self.final

    def has_work(self):
        return self.final or self.step


class PickTest(unittest.TestCase):
    def test_round_robin_after_last_served(self):
        entries = [FakeEntry(step=True), FakeEntry(step=True), FakeEntry(step=True)]
        self.assertEqual(server.pick_next(entries, last=0), 1)
        self.assertEqual(server.pick_next(entries, last=2), 0)

    def test_finals_first(self):
        entries = [FakeEntry(step=True), FakeEntry(step=True), FakeEntry(final=True)]
        self.assertEqual(server.pick_next(entries, last=0), 2)

    def test_nothing_to_do(self):
        self.assertIsNone(server.pick_next([FakeEntry(), FakeEntry()], last=-1))


if __name__ == "__main__":
    unittest.main()
