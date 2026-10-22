import json
import tempfile
import threading
import unittest
from email.parser import BytesParser
from email.policy import default
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

from audio_bench import run_grok


class FakeXAI(BaseHTTPRequestHandler):
    requests = []

    def do_POST(self):
        body = self.rfile.read(int(self.headers["Content-Length"]))
        message = BytesParser(policy=default).parsebytes(
            b"Content-Type: " + self.headers["Content-Type"].encode() + b"\r\n\r\n" + body
        )
        fields = [(part.get_param("name", header="content-disposition"), part.get_content()) for part in message.iter_parts()]
        FakeXAI.requests.append({"auth": self.headers["Authorization"], "path": self.path, "fields": fields})
        if self.headers["Authorization"] != "Bearer good-key":
            self.send_response(401)
            self.end_headers()
            return
        data = json.dumps({"text": "Почему context cancellation не остановил goroutine?", "language": "ru", "duration": 2.5}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *args):
        pass


class GrokRunnerTest(unittest.TestCase):
    def setUp(self):
        FakeXAI.requests = []
        self.server = HTTPServer(("127.0.0.1", 0), FakeXAI)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.server.server_port}/v1/stt"
        self.dir = tempfile.TemporaryDirectory()
        root = Path(self.dir.name)
        (root / "a.wav").write_bytes(b"RIFF....WAVEfake")
        import hashlib

        digest = hashlib.sha256(b"RIFF....WAVEfake").hexdigest()
        manifest = {
            "version": 1,
            "context_phrases": ["context cancellation", "goroutine"],
            "samples": [{"id": "q1", "audio": "a.wav", "sha256": digest, "speaker": "s", "conversation": "c", "split": "dev",
                         "categories": ["mixed"], "reference": "Почему context cancellation не остановил goroutine?", "terms": [], "critical": []}],
        }
        self.manifest = root / "manifest.json"
        self.manifest.write_text(json.dumps(manifest, ensure_ascii=False), encoding="utf-8")
        self.out = root / "grok.jsonl"

    def tearDown(self):
        self.server.shutdown()
        self.dir.cleanup()

    def run_main(self, key, *extra):
        return run_grok.main(["--manifest", str(self.manifest), "--out", str(self.out), "--url", self.url, *extra], env={"XAI_API_KEY": key})

    def test_sends_terms_language_and_file_last(self):
        self.assertEqual(self.run_main("good-key", "--language", "ru"), 0)
        req = FakeXAI.requests[0]
        self.assertEqual(req["auth"], "Bearer good-key")
        names = [n for n, _ in req["fields"]]
        self.assertEqual(names[-1], "file")
        self.assertEqual([v for n, v in req["fields"] if n == "keyterm"], ["context cancellation", "goroutine"])
        self.assertIn(("language", "ru"), req["fields"])
        self.assertIn(("model", "grok-voice-transcribe-2.0"), req["fields"])

    def test_writes_report_compatible_results(self):
        self.run_main("good-key")
        lines = [json.loads(l) for l in self.out.read_text(encoding="utf-8").splitlines()]
        self.assertEqual(lines[0]["type"], "run")
        self.assertEqual(lines[0]["engine"], "grok-voice-transcribe-2.0/batch")
        self.assertNotIn("good-key", self.out.read_text(encoding="utf-8"))
        sample = lines[1]
        self.assertEqual(sample["id"], "q1")
        self.assertEqual(sample["offline"], "Почему context cancellation не остановил goroutine?")
        self.assertEqual(sample["streaming_final"], sample["offline"])
        self.assertEqual(lines[-1]["type"], "end")

    def test_rejected_key_fails_without_printing_it(self):
        self.assertEqual(self.run_main("bad-key"), 1)

    def test_missing_key_is_an_error(self):
        self.assertEqual(self.run_main(""), 2)

    def test_terms_over_xai_limits_are_rejected(self):
        doc = json.loads(self.manifest.read_text(encoding="utf-8"))
        doc["context_phrases"] = ["x" * 51]
        self.manifest.write_text(json.dumps(doc), encoding="utf-8")
        self.assertEqual(self.run_main("good-key"), 2)
        self.assertEqual(FakeXAI.requests, [])


if __name__ == "__main__":
    unittest.main()
