import unittest
from types import SimpleNamespace

import numpy as np

from audio_bench import runner, timeline

SR = 16000


class FakeModel:
    def __init__(self, finish_reason="stop"):
        self.calls = []
        self.max_tokens = []
        self.finish_reason = finish_reason

    def generate(self, inputs, sampling_params=None, use_tqdm=False):
        audio = inputs[0]["multi_modal_data"]["audio"][0]
        self.calls.append(audio.shape[0])
        self.max_tokens.append(sampling_params.max_tokens)
        text = f"w{len(self.calls)}"
        return [SimpleNamespace(outputs=[SimpleNamespace(text=text, finish_reason=self.finish_reason)])]


class FakeASR:
    """Mirrors qwen_asr 0.0.6 chunking: one generate per full chunk, finish only on a non-empty tail."""

    def __init__(self, model):
        self.model = model
        self.sampling_params = SimpleNamespace(max_tokens=256)
        self.states = []
        self.offline = []

    def init_streaming_state(self, context="", language=None, unfixed_chunk_num=2, unfixed_token_num=5, chunk_size_sec=2.0):
        state = SimpleNamespace(
            context=context,
            chunk_size_samples=int(round(chunk_size_sec * SR)),
            buffer=np.zeros(0, dtype=np.float32),
            audio_accum=np.zeros(0, dtype=np.float32),
            chunk_id=0,
            text="",
            language="",
        )
        self.states.append(state)
        return state

    def _decode(self, state, audio):
        state.audio_accum = np.concatenate([state.audio_accum, audio])
        out = self.model.generate([{"prompt": "", "multi_modal_data": {"audio": [state.audio_accum]}}], sampling_params=self.sampling_params)
        state.text = out[0].outputs[0].text
        state.language = "Russian"
        state.chunk_id += 1

    def streaming_transcribe(self, pcm, state):
        state.buffer = np.concatenate([state.buffer, pcm])
        while state.buffer.shape[0] >= state.chunk_size_samples:
            chunk, state.buffer = state.buffer[: state.chunk_size_samples], state.buffer[state.chunk_size_samples :]
            self._decode(state, chunk)
        return state

    def finish_streaming_transcribe(self, state):
        if state.buffer.shape[0] == 0:
            return state
        tail, state.buffer = state.buffer, np.zeros(0, dtype=np.float32)
        self._decode(state, tail)
        return state

    def transcribe(self, audio, context="", language=None, return_time_stamps=False):
        pcm, rate = audio
        self.offline.append((pcm.shape[0], rate, context, language))
        out = self.model.generate([{"prompt": "", "multi_modal_data": {"audio": [pcm]}}], sampling_params=self.sampling_params)
        return [SimpleNamespace(text="offline " + out[0].outputs[0].text, language="Russian")]


def run(seconds, finish_reason="stop", **config):
    asr = FakeASR(FakeModel(finish_reason))
    probe = runner.GenerateProbe.install(asr)
    cfg = runner.RunConfig(**{"chunk_sec": 1.0, **config})
    result = runner.transcribe_sample(asr, probe, np.zeros(int(seconds * SR), dtype=np.float32), cfg)
    return asr, result


class RunnerTest(unittest.TestCase):
    def test_transport_frames_aggregate_to_model_chunk(self):
        _, result = run(2.5)
        utt = result["utterances"][0]
        self.assertEqual([s["audio_end"] for s in utt["steps"]], [1.0, 2.0])
        self.assertTrue(utt["finish"]["called"])
        self.assertEqual(utt["streaming_final"], "w3")

    def test_finish_with_empty_tail_makes_no_model_call(self):
        asr, result = run(2.0)
        utt = result["utterances"][0]
        self.assertFalse(utt["finish"]["called"])
        self.assertEqual(utt["finish"]["compute"], 0.0)
        self.assertEqual(utt["streaming_final"], "w2")
        self.assertEqual(asr.model.calls, [SR, 2 * SR, 2 * SR])  # two steps + one offline pass

    def test_forced_boundary_resets_state_and_offline_uses_same_pcm(self):
        asr, result = run(45.0, context="ctx", language="Russian")
        bounds = [(u["start"], u["end"]) for u in result["utterances"]]
        self.assertEqual(bounds, [(0.0, 20.0), (20.0, 40.0), (40.0, 45.0)])
        self.assertEqual(len(asr.states), 3)
        self.assertEqual(max(s.audio_accum.shape[0] for s in asr.states), 20 * SR)
        self.assertEqual(asr.offline, [(20 * SR, SR, "ctx", "Russian"), (20 * SR, SR, "ctx", "Russian"), (5 * SR, SR, "ctx", "Russian")])
        self.assertEqual(result["offline"], " ".join(u["offline"]["text"] for u in result["utterances"]))

    def test_streaming_steps_use_their_own_token_cap(self):
        asr, _ = run(2.5, step_max_tokens=32, offline_max_tokens=256)
        self.assertEqual(asr.model.max_tokens, [32, 32, 32, 256])

    def test_truncation_is_reported(self):
        _, result = run(1.5, finish_reason="length")
        utt = result["utterances"][0]
        self.assertTrue(utt["steps"][0]["truncated"])
        self.assertTrue(utt["finish"]["truncated"])
        self.assertTrue(utt["offline"]["truncated"])
        self.assertTrue(result["truncated"])


class ContextTest(unittest.TestCase):
    def test_empty_dictionary_gives_empty_context(self):
        self.assertEqual(runner.build_context(()), "")

    def test_list_template_is_only_the_terms(self):
        self.assertEqual(runner.build_context(("NestJS", "JWT"), template="list"), "NestJS, JWT")
        with self.assertRaisesRegex(ValueError, "template"):
            runner.build_context(("NestJS",), template="prose")

    def test_context_is_deterministic_and_bounded(self):
        text = runner.build_context(("NestJS", "PostgreSQL"))
        self.assertEqual(text, runner.build_context(("NestJS", "PostgreSQL")))
        self.assertIn("NestJS\nPostgreSQL", text)
        with self.assertRaisesRegex(ValueError, "4096"):
            runner.build_context(tuple(f"term-{i:03d}-" + "x" * 100 for i in range(60)))


class TimelineTest(unittest.TestCase):
    def test_single_track_without_backlog(self):
        jobs = [timeline.Job(1.0, 0.5, 0), timeline.Job(2.0, 0.5, 0)]
        self.assertEqual(timeline.latencies(jobs), [0.5, 0.5])

    def test_backlog_grows_when_compute_exceeds_chunk(self):
        jobs = [timeline.Job(1.0, 1.5, 0), timeline.Job(2.0, 1.5, 0)]
        self.assertEqual(timeline.latencies(jobs), [1.5, 2.0])

    def test_two_tracks_share_one_serial_model(self):
        jobs = [timeline.Job(1.0, 0.6, 0), timeline.Job(1.0, 0.6, 1)]
        self.assertEqual([round(x, 6) for x in timeline.latencies(jobs)], [0.6, 1.2])


if __name__ == "__main__":
    unittest.main()
