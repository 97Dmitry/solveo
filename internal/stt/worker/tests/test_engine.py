import unittest

import numpy as np

from solveo_stt import engine
from solveo_stt.engine import CHUNK, MAX_UTTERANCE, PREROLL, WINDOW, Track


class FakeVad:
    """Speech is any window whose first sample is non-zero; reports transitions like Silero's iterator."""

    def __init__(self, end_after_windows=1):
        self.speaking = False
        self.silent = 0
        self.end_after = end_after_windows

    def window(self, samples):
        assert samples.shape[0] == WINDOW
        if samples[0] != 0:
            self.silent = 0
            if not self.speaking:
                self.speaking = True
                return "start"
            return None
        if self.speaking:
            self.silent += 1
            if self.silent >= self.end_after:
                self.speaking = False
                return "end"
        return None

    def reset(self):
        self.speaking, self.silent = False, 0


class FakeModel:
    def __init__(self, truncate=False):
        self.steps, self.finals, self.truncate = [], [], truncate

    def init_state(self, context):
        return {"context": context}

    def step(self, state, pcm):
        self.steps.append(pcm.shape[0])
        return f"s{len(self.steps)}", self.truncate

    def transcribe(self, pcm, context):
        self.finals.append(pcm.shape[0])
        return "final"


def speech(windows):
    return np.ones(windows * WINDOW, dtype=np.float32)


def silence(windows):
    return np.zeros(windows * WINDOW, dtype=np.float32)


def drain(track):
    out = []
    while track.has_work():
        out += track.work()
    return out


class TrackTest(unittest.TestCase):
    def setUp(self):
        self.model = FakeModel()
        self.track = Track("st", "ctx", self.model, FakeVad())

    def test_started_precedes_text(self):
        updates = self.track.feed(np.concatenate([silence(20), speech(40)]))
        self.assertEqual([u.kind for u in updates], ["started"])
        started = updates[0]
        self.assertEqual((started.utterance_id, started.revision), ("st:1", 0))
        self.assertEqual(started.start_sample, 20 * WINDOW - PREROLL)
        kinds = [u.kind for u in drain(self.track)]
        self.assertEqual(kinds, ["partial"])

    def test_preroll_is_clamped_at_stream_start(self):
        started = self.track.feed(np.concatenate([silence(2), speech(2)]))[0]
        self.assertEqual(started.start_sample, 0)

    def test_preroll_included_in_final(self):
        self.track.feed(np.concatenate([silence(20), speech(10), silence(1)]))
        finals = [u for u in drain(self.track) if u.kind == "final"]
        self.assertEqual(len(finals), 1)
        self.assertEqual(self.model.finals, [PREROLL + 11 * WINDOW])
        self.assertEqual(finals[0].end_sample - finals[0].start_sample, PREROLL + 11 * WINDOW)

    def test_streaming_steps_coalesce_backlog(self):
        self.track.feed(speech(int(3.5 * CHUNK / WINDOW)))
        updates = self.track.work()
        self.assertEqual(len(self.model.steps), 1)
        self.assertGreaterEqual(self.model.steps[0], 3 * CHUNK)
        self.assertEqual([(u.kind, u.revision, u.text) for u in updates], [("partial", 1, "s1")])
        self.assertFalse(self.track.has_work())

    def test_no_step_below_one_chunk(self):
        self.track.feed(speech(CHUNK // WINDOW - 2))
        self.assertFalse(self.track.has_work())

    def test_final_has_priority_over_steps(self):
        self.track.feed(np.concatenate([speech(int(2 * CHUNK / WINDOW)), silence(1)]))
        updates = self.track.work()
        self.assertEqual([u.kind for u in updates], ["final"])
        self.assertEqual(self.model.steps, [])
        self.assertEqual(drain(self.track), [])

    def test_final_revision_follows_partials(self):
        self.track.feed(speech(int(1.5 * CHUNK / WINDOW)))
        drain(self.track)
        self.track.feed(silence(1))
        final = drain(self.track)[0]
        self.assertEqual((final.kind, final.revision, final.text), ("final", 2, "final"))

    def test_truncated_step_stalls_partials(self):
        model = FakeModel(truncate=True)
        track = Track("st", "", model, FakeVad())
        track.feed(speech(int(1.5 * CHUNK / WINDOW)))
        self.assertEqual(drain(track), [])
        track.feed(speech(int(2 * CHUNK / WINDOW)))
        self.assertEqual(drain(track), [])
        self.assertEqual(len(model.steps), 1)
        track.feed(silence(1))
        self.assertEqual([u.kind for u in drain(track)], ["final"])

    def test_twenty_second_cap_forces_final(self):
        updates = self.track.feed(speech(int(21 * 16000 / WINDOW) + 1))
        updates += drain(self.track)
        finals = [u for u in updates if u.kind == "final"]
        self.assertEqual(len(finals), 1)
        self.assertEqual(finals[0].end_sample - finals[0].start_sample, MAX_UTTERANCE)
        started = [u for u in updates if u.kind == "started"]
        self.assertEqual([u.utterance_id for u in started], ["st:1", "st:2"])
        self.assertEqual(started[1].start_sample, finals[0].end_sample)

    def test_flush_finalizes_active_utterance(self):
        self.track.feed(np.concatenate([speech(10), np.ones(100, dtype=np.float32)]))
        self.track.flush()
        finals = [u for u in drain(self.track) if u.kind == "final"]
        self.assertEqual(len(finals), 1)
        self.assertEqual(self.model.finals, [10 * WINDOW + 100])

    def test_idle_flush_emits_nothing(self):
        self.track.feed(silence(10))
        self.track.flush()
        self.assertFalse(self.track.has_work())

    def test_silence_only_emits_nothing(self):
        self.assertEqual(self.track.feed(silence(500)), [])
        self.assertFalse(self.track.has_work())

    def test_job_split_runs_model_outside_track_state(self):
        self.track.feed(speech(int(1.5 * CHUNK / WINDOW)))
        job = self.track.take_job()
        self.assertEqual(job.kind, "step")
        self.assertIsNone(self.track.take_job())  # one job per track at a time
        self.track.feed(silence(1))  # the utterance ends while the step runs
        updates = self.track.finish_job(job, self.track.run_job(job))
        self.assertEqual([u.kind for u in updates], ["partial"])
        self.assertEqual([u.kind for u in drain(self.track)], ["final"])

    def test_has_final_and_busy_reflect_jobs(self):
        self.track.feed(np.concatenate([speech(10), silence(1)]))
        self.assertTrue(self.track.has_final())
        job = self.track.take_job()
        self.assertTrue(self.track.busy)
        self.assertFalse(self.track.has_final())
        self.track.finish_job(job, self.track.run_job(job))
        self.assertFalse(self.track.busy)

    def test_update_json_shape(self):
        update = engine.Update("st", "st:1", 3, "partial", 10, 20, "t")
        self.assertEqual(
            update.to_json(),
            {"type": "update", "stream_id": "st", "utterance_id": "st:1", "revision": 3, "kind": "partial",
             "start_sample": 10, "end_sample": 20, "text": "t"},
        )


if __name__ == "__main__":
    unittest.main()
