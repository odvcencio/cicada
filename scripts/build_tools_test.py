"""Exercise build tool discovery, argument preservation, and process cleanup."""

import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import tempfile
import time
import unittest

SCRIPTS = Path(__file__).resolve().parent


class BuildToolsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="cicada-build-tools-test-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.bundle = self.root / "TinyGo bundle"
        (self.bundle / "bin").mkdir(parents=True)
        tinygo = self.bin / "tinygo"
        tinygo.write_text("#!/bin/sh\nprintf '%s\\n' " + shlex.quote(str(self.bundle)) + "\n")
        tinygo.chmod(0o755)
        self.env = {**os.environ, "PATH": str(self.bin), "WASMOPT": ""}
        self.args = ["-Oz", "input with 'quotes' and $(false).wasm", "-o", "output\nname.wasm"]

    def optimizer(self, path, name):
        path.write_text("#!/bin/sh\nprintf '%s\\000' " + shlex.quote(name) + ' "$@"\n')
        path.chmod(0o755)

    def optimize(self):
        return subprocess.run(["/bin/sh", str(SCRIPTS / "wasm-opt.sh"), *self.args], env=self.env, capture_output=True, timeout=3)

    def assert_optimizer(self, name):
        result = self.optimize()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.decode().split("\0"), [name, *self.args, ""])

    def test_explicit_optimizer_preserves_path_and_arguments(self):
        self.optimizer(self.bundle / "bin/wasm-opt", "bundled")
        self.optimizer(self.bin / "wasm-opt", "path")
        custom = self.root / "custom optimizer's executable"
        self.optimizer(custom, "explicit")
        self.env["WASMOPT"] = str(custom)
        self.assert_optimizer("explicit")

    def test_bundled_optimizer_remains_preferred(self):
        self.optimizer(self.bundle / "bin/wasm-opt", "bundled")
        self.optimizer(self.bin / "wasm-opt", "path")
        self.assert_optimizer("bundled")

    def test_separate_binaryen_on_path(self):
        self.optimizer(self.bin / "wasm-opt", "path")
        self.assert_optimizer("path")

    def test_invalid_explicit_optimizer_does_not_silently_switch(self):
        self.optimizer(self.bundle / "bin/wasm-opt", "bundled")
        self.env["WASMOPT"] = str(self.root / "absent")
        result = self.optimize()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, b"")
        self.assertIn(b"absent", result.stderr)

    def test_missing_optimizer_reports_actionable_error(self):
        result = self.optimize()
        self.assertEqual(result.returncode, 127)
        self.assertIn(b"install Binaryen on PATH or set WASMOPT", result.stderr)

    def timed(self, *args):
        return subprocess.run([sys.executable, str(SCRIPTS / "run-with-timeout.py"), *args], capture_output=True, timeout=5)

    def test_deadline_preserves_arguments_and_command_status(self):
        for duration in ["1s", "0"]:
            result = self.timed(duration, sys.executable, "-c", "import sys; print(repr(sys.argv[1:])); sys.exit(17)", *self.args)
            self.assertEqual(result.returncode, 17, result.stderr)
            self.assertEqual(result.stdout.decode().strip(), repr(self.args))

    def test_invalid_deadline_never_starts_command(self):
        marker = self.root / "must-not-start"
        for duration in ["-1", "nan", "inf", "1e999s"]:
            result = self.timed(duration, sys.executable, "-c", f"open({str(marker)!r}, 'w').close()")
            self.assertEqual(result.returncode, 2, result.stderr)
        self.assertFalse(marker.exists())

    def test_deadline_terminates_build(self):
        result = self.timed("--kill-after=.1s", ".2s", sys.executable, "-c", "import time; time.sleep(10)")
        self.assertEqual(result.returncode, 124, result.stderr)

    def test_uncooperative_build_is_killed_after_grace(self):
        result = self.timed("--kill-after=.1s", ".2s", "/bin/sh", "-c", "trap '' TERM; exec /bin/sleep 10")
        self.assertEqual(result.returncode, 137, result.stderr)

    def test_deadline_removes_child_when_parent_exits_first(self):
        marker = self.root / "child.pid"
        child = "import signal,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); time.sleep(10)"
        parent = f"import subprocess,time; from pathlib import Path; p=subprocess.Popen([{sys.executable!r},'-c',{child!r}]); Path({str(marker)!r}).write_text(str(p.pid)); time.sleep(10)"
        result = self.timed("--kill-after=.1s", ".3s", sys.executable, "-c", parent)
        self.assertEqual(result.returncode, 124, result.stderr)
        pid = int(marker.read_text())
        deadline = time.monotonic() + 2
        while time.monotonic() < deadline:
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                return
            # Container init may retain a dead orphan as a zombie.
            stat = Path(f"/proc/{pid}/stat")
            if stat.exists():
                try:
                    if stat.read_text().split(") ", 1)[1].startswith("Z "):
                        return
                except FileNotFoundError:
                    return
            time.sleep(.01)
        os.kill(pid, signal.SIGKILL)
        self.fail("compiler child survived the build deadline")

    def test_cancellation_signals_reach_build_group(self):
        for signum in [signal.SIGINT, signal.SIGTERM]:
            marker = self.root / f"ready-{signum}"
            command = f"from pathlib import Path; import os,time; Path({str(marker)!r}).write_text(str(os.getpid())); time.sleep(10)"
            process = subprocess.Popen([sys.executable, str(SCRIPTS / "run-with-timeout.py"), "--kill-after=.1s", "10s", sys.executable, "-c", command], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            try:
                deadline = time.monotonic() + 3
                while not marker.exists() and time.monotonic() < deadline:
                    time.sleep(.01)
                self.assertTrue(marker.exists(), "build did not start")
                process.send_signal(signum)
                self.assertEqual(process.wait(timeout=3), 128 + signum)
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait()
                    if marker.exists():
                        try:
                            os.killpg(int(marker.read_text()), signal.SIGKILL)
                        except ProcessLookupError:
                            pass


if __name__ == "__main__":
    unittest.main()
