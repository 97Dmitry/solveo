"""Exercise publication decisions without GitHub access or mutations."""
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("publish-release.sh").resolve()
FAKE_GH = r'''#!/usr/bin/env python3
import os
from pathlib import Path
import sys
args = sys.argv[1:]
with open(os.environ["GH_CALLS"], "a") as log:
    log.write(" ".join(args) + "\n")
if args[0] == "api":
    if "branches/master" in args[1]:
        print(os.environ.get("HEAD_SHA", "abc123"))
    elif "releases?" in args[1]:
        if os.environ.get("QUERY_FAIL"):
            sys.exit(1)
        print(os.environ.get("EXISTING", ""))
    elif "commits/" in args[1]:
        print(os.environ.get("RELEASE_SHA", "abc123"))
    else:
        sys.exit(2)
elif args[:2] == ["release", "upload"] and os.environ.get("UPLOAD_FAIL"):
    sys.exit(1)
'''


class PublishReleaseTest(unittest.TestCase):
    def run_release(self, *, corrupt=False, **overrides):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            binary_dir = root / "build/bin"
            binary_dir.mkdir(parents=True)
            data = b"test executable"
            (binary_dir / "Solveo.exe").write_bytes(data)
            digest = hashlib.sha256(data).hexdigest()
            (binary_dir / "SHA256SUMS.txt").write_text(f"{digest}  Solveo.exe\n")
            if corrupt:
                (binary_dir / "Solveo.exe").write_bytes(b"changed")
            fake = root / "gh"
            fake.write_text(FAKE_GH)
            fake.chmod(0o755)
            calls = root / "calls"
            env = dict(os.environ, PATH=f"{root}:{os.environ['PATH']}",
                       GH_REPO="owner/repo", GITHUB_SHA="abc123",
                       GITHUB_RUN_NUMBER="42", GH_CALLS=str(calls), **overrides)
            result = subprocess.run(["bash", str(SCRIPT)], cwd=root, env=env,
                                    text=True, capture_output=True, check=False)
            return result, calls.read_text() if calls.exists() else ""

    def test_new_release_uploads_before_publication(self):
        result, calls = self.run_release()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--target abc123", calls)
        self.assertIn("--draft\n", calls)
        self.assertIn("Solveo.exe SHA256SUMS.txt --clobber", calls)
        self.assertLess(calls.index("release upload"), calls.index("release edit"))

    def test_stale_commit_skips_publication(self):
        result, calls = self.run_release(HEAD_SHA="newer")
        self.assertEqual(result.returncode, 0)
        self.assertNotIn("release ", calls)

    def test_corrupt_artifact_stops_before_github(self):
        result, calls = self.run_release(corrupt=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, "")

    def test_query_failure_stops_publication(self):
        result, calls = self.run_release(QUERY_FAIL="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("release create", calls)

    def test_upload_failure_keeps_draft(self):
        result, calls = self.run_release(UPLOAD_FAIL="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("release edit", calls)

    def test_retry_continues_matching_draft(self):
        result, calls = self.run_release(EXISTING="true\tabc123")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("release create", calls)
        self.assertIn("release edit", calls)

    def test_retry_preserves_published_assets(self):
        result, calls = self.run_release(EXISTING="false\tabc123")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("release upload", calls)
        self.assertNotIn("release edit", calls)

    def test_conflicting_release_or_draft_stops(self):
        for values in ({"EXISTING": "true\tother"},
                       {"EXISTING": "false\tabc123", "RELEASE_SHA": "other"}):
            with self.subTest(values=values):
                result, calls = self.run_release(**values)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn("release upload", calls)
                self.assertNotIn("release edit", calls)


if __name__ == "__main__":
    unittest.main()
