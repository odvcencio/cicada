"""Exercise the host fixture's real HTTP/download contract without macOS."""

import hashlib
import io
import json
from pathlib import Path
import select
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import urllib.error
import urllib.request
import zipfile

import host_smoke


def stop_fixture(process):
    if process.poll() is None:
        process.terminate()
    try:
        return process.communicate(timeout=3)
    except subprocess.TimeoutExpired:
        process.kill()
        return process.communicate(timeout=3)


class HostFixtureTests(unittest.TestCase):
    def test_loopback_fixture_binds_without_dns(self):
        with patch("socket.getfqdn", side_effect=AssertionError("fixture must not resolve DNS")):
            with host_smoke.FixtureHTTPServer(("127.0.0.1", 0), host_smoke.fixture_handler()) as server:
                self.assertEqual(server.server_name, "localhost")
                self.assertEqual(server.server_address[0], "127.0.0.1")
                self.assertGreater(server.server_port, 0)

    def test_private_http_fixture_matches_saved_zip_contract(self):
        with tempfile.TemporaryDirectory() as temporary:
            score = Path(temporary) / "main.cicada"
            score.write_text(host_smoke.SCORE)
            process = subprocess.Popen([
                sys.executable, str(Path(host_smoke.__file__).resolve()), "studio", str(score),
                "--listen", "127.0.0.1:0", "--audio", "null",
            ], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                if not select.select([process.stdout], [], [], 15)[0]:
                    status = process.poll()
                    _, error = stop_fixture(process)
                    self.fail(f"fixture readiness timed out: status={status}; stderr={error[-2000:]!r}")
                ready = process.stdout.readline().strip()
                if not ready:
                    status = process.poll()
                    _, error = stop_fixture(process)
                    self.fail(f"fixture did not start: status={status}; stderr={error[-2000:]!r}")
                self.assertRegex(ready, r"^Cicada Studio: http://127\.0\.0\.1:\d+/$")
                origin = ready.removeprefix("Cicada Studio: ").rstrip("/")
                with urllib.request.urlopen(origin + "/studio", timeout=2) as response:
                    self.assertIn(b'data-native-host-fixture="1"', response.read())
                with urllib.request.urlopen(origin + "/fixture.json", timeout=2) as response:
                    metadata = json.load(response)
                with urllib.request.urlopen(origin + host_smoke.EXPORT_PATH, timeout=2) as response:
                    payload = response.read()
                    self.assertEqual(response.headers["Content-Type"], "application/zip")
                    self.assertEqual(response.headers["Cache-Control"], "no-store")
                    self.assertEqual(response.headers["Content-Disposition"], 'attachment; filename="host-fixture.zip"')
                    self.assertEqual(int(response.headers["Content-Length"]), len(payload))
                self.assertEqual(metadata["bytes"], len(payload))
                self.assertEqual(metadata["sha256"], hashlib.sha256(payload).hexdigest())
                with zipfile.ZipFile(io.BytesIO(payload)) as archive:
                    self.assertEqual(archive.namelist(), ["fixture.txt"])
                    self.assertEqual(archive.read("fixture.txt"), b"Cicada native host save fixture.\n" * 64)
                with self.assertRaises(urllib.error.HTTPError) as error:
                    urllib.request.urlopen(origin + "/../../etc/passwd", timeout=2)
                self.assertEqual(error.exception.code, 404)
                self.assertEqual(score.read_text(), host_smoke.SCORE)
            finally:
                stop_fixture(process)

    def test_fixture_rejects_non_private_launches(self):
        with tempfile.TemporaryDirectory() as temporary:
            score = Path(temporary) / "main.cicada"
            score.write_text(host_smoke.SCORE)
            for listen, audio in [("0.0.0.0:0", "null"), ("127.0.0.1:0", "tymbal")]:
                with self.subTest(listen=listen, audio=audio):
                    result = subprocess.run([
                        sys.executable, str(Path(host_smoke.__file__).resolve()), "studio", str(score),
                        "--listen", listen, "--audio", audio,
                    ], capture_output=True, text=True, timeout=10)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("loopback ephemeral port and null audio", result.stderr)


if __name__ == "__main__":
    unittest.main()
