import unittest
from types import SimpleNamespace

import numpy as np

from solveo_stt import qwen

SR = 16000


class FakeLLM:
    def __init__(self, finish_reason="stop"):
        self.audio_lengths, self.max_tokens, self.finish_reason = [], [], finish_reason

    def generate(self, inputs, sampling_params=None, use_tqdm=False):
        self.audio_lengths.append(inputs[0]["multi_modal_data"]["audio"][0].shape[0])
        self.max_tokens.append(sampling_params.max_tokens)
        out = SimpleNamespace(text=f"t{len(self.audio_lengths)}", finish_reason=self.finish_reason)
        return [SimpleNamespace(outputs=[out])]


class FakeParams(SimpleNamespace):
    def clone(self):
        return FakeParams(**vars(self))


class FakeASR:
    """qwen-asr 0.0.6 semantics: one generate per full chunk of state.buffer, re-feeding audio_accum."""

    def __init__(self, llm):
        self.model = llm
        self.sampling_params = FakeParams(max_tokens=256, temperature=0.0)
        self.inits = []

    def init_streaming_state(self, context="", language=None, unfixed_chunk_num=2, unfixed_token_num=5, chunk_size_sec=2.0):
        self.inits.append((context, language, unfixed_chunk_num, unfixed_token_num, chunk_size_sec))
        return SimpleNamespace(chunk_size_samples=int(chunk_size_sec * SR), buffer=np.zeros(0, np.float32),
                               audio_accum=np.zeros(0, np.float32), chunk_id=0, text="")

    def streaming_transcribe(self, pcm, state):
        state.buffer = np.concatenate([state.buffer, pcm])
        while state.buffer.shape[0] >= state.chunk_size_samples:
            chunk, state.buffer = state.buffer[: state.chunk_size_samples], state.buffer[state.chunk_size_samples :]
            state.audio_accum = np.concatenate([state.audio_accum, chunk])
            out = self.model.generate([{"multi_modal_data": {"audio": [state.audio_accum]}}], sampling_params=self.sampling_params)
            state.text = out[0].outputs[0].text
            state.chunk_id += 1
        return state

    def transcribe(self, audio, context="", language=None):
        pcm, rate = audio
        out = self.model.generate([{"multi_modal_data": {"audio": [pcm]}}], sampling_params=self.sampling_params)
        return [SimpleNamespace(text="offline " + out[0].outputs[0].text, language="Russian")]


def make(finish_reason="stop"):
    llm = FakeLLM(finish_reason)
    asr = FakeASR(llm)
    return qwen.QwenModel(asr), asr, llm


class QwenModelTest(unittest.TestCase):
    def test_state_uses_pinned_streaming_settings(self):
        model, asr, _ = make()
        model.init_state("ctx")
        self.assertEqual(asr.inits, [("ctx", None, 2, 5, 1.0)])

    def test_step_puts_backlog_into_one_decode(self):
        model, _, llm = make()
        state = model.init_state("")
        text, truncated = model.step(state, np.ones(int(3.4 * SR), np.float32))
        self.assertEqual(llm.audio_lengths, [3 * SR])
        self.assertEqual((text, truncated), ("t1", False))
        self.assertEqual(state.buffer.shape[0], int(0.4 * SR))

    def test_step_and_final_use_own_token_caps(self):
        model, _, llm = make()
        state = model.init_state("")
        model.step(state, np.ones(SR, np.float32))
        model.transcribe(np.ones(SR, np.float32), "")
        self.assertEqual(llm.max_tokens, [32, 256])

    def test_step_reports_truncation(self):
        model, _, _ = make(finish_reason="length")
        state = model.init_state("")
        self.assertTrue(model.step(state, np.ones(SR, np.float32))[1])

    def test_transcribe_returns_text(self):
        model, _, llm = make()
        self.assertEqual(model.transcribe(np.ones(2 * SR, np.float32), "ctx"), "offline t1")
        self.assertEqual(llm.audio_lengths, [2 * SR])


class ContextTest(unittest.TestCase):
    def test_empty_dictionary_gives_empty_context(self):
        self.assertEqual(qwen.build_context([]), "")

    def test_context_template_and_order(self):
        text = qwen.build_context(["NestJS", "JWT"])
        self.assertTrue(text.startswith("Technical interview in Russian with English IT terms."))
        self.assertTrue(text.endswith(":\nNestJS\nJWT"))

    def test_context_over_limit_rejected(self):
        with self.assertRaisesRegex(ValueError, "4096"):
            qwen.build_context(["x" * 100] * 60)


if __name__ == "__main__":
    unittest.main()
