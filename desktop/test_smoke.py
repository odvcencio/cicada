"""Exercise the native host/supervisor timeout protocol without a GUI process."""

import contextlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import smoke


class Clock:
    def __init__(self):
        self.now = 0

    def monotonic(self):
        value = self.now
        self.now += 1
        return value


class HostProcess:
    pid = 987654321

    def __init__(self, args, finish):
        self.args, self.finish, self.returncode = args, finish, None
        self.polls = 0
        self.terminated = False
        budget = int(args[args.index("--smoke-timeout") + 1].removesuffix("s"))
        data = Path(args[args.index("--data-dir") + 1]) / "logs"
        data.mkdir(parents=True)
        (data / "studio.log").write_text("Cicada Studio: http://127.0.0.1:12345/\n")
        report = Path(args[args.index("--smoke-out") + 1])
        report.write_text(json.dumps({"studioLoadedMs": 1, "frontend": {"ok": True}, "timeoutSeconds": budget}))

    def poll(self):
        self.polls += 1
        if self.finish and self.polls > 1:
            self.returncode = 0
        return self.returncode

    def wait(self, timeout):
        if self.returncode is None:
            raise subprocess.TimeoutExpired(self.args, timeout)
        return self.returncode

    def terminate(self):
        self.terminated = True
        self.returncode = 0

    def kill(self):
        self.returncode = -9


class SmokeTimeoutTests(unittest.TestCase):
    def run_host(self, folder, finish=True, **kwargs):
        clock = Clock()
        created = []

        def popen(args, **_):
            process = HostProcess(args, finish)
            created.append(process)
            return process

        with patch.object(smoke.subprocess, "Popen", side_effect=popen), \
                patch.object(smoke, "descendants", side_effect=lambda pid: {pid}), \
                patch.object(smoke, "alive", return_value=False), \
                patch.object(smoke.os, "kill"), \
                patch.object(smoke.time, "monotonic", side_effect=clock.monotonic), \
                patch.object(smoke.time, "sleep"):
            try:
                result = smoke.check_launch(Path("test-host"), folder, "normal", Path("test-workflow.js"), **kwargs)
                return result, created[0], clock
            except subprocess.TimeoutExpired:
                return None, created[0], clock

    def test_default_and_explicit_budgets_reach_the_host(self):
        for options, seconds in [({}, 60), ({"timeout": 120}, 120)]:
            with self.subTest(seconds=seconds), tempfile.TemporaryDirectory() as temp:
                result, process, _ = self.run_host(Path(temp), **options)
                self.assertIsNotNone(result)
                self.assertEqual(process.args[process.args.index("--smoke-timeout") + 1], f"{seconds}s")
                self.assertEqual(result["startup"]["timeoutSeconds"], seconds)

    def test_supervisor_uses_the_same_budget_and_cleans_up_a_stuck_host(self):
        for seconds in [60, 120]:
            with self.subTest(seconds=seconds), tempfile.TemporaryDirectory() as temp:
                result, process, clock = self.run_host(Path(temp), finish=False, timeout=seconds)
                self.assertIsNone(result)
                self.assertTrue(process.terminated)
                self.assertGreaterEqual(clock.now, seconds + 10)
                self.assertLess(clock.now, seconds + 12)

    def test_nonpositive_timeout_is_rejected_before_creating_state(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / "not-created"
            stderr = io.StringIO()
            with patch("sys.argv", ["smoke.py", "--app", "missing.app", "--output", str(output), "--timeout", "0"]), \
                    contextlib.redirect_stderr(stderr), self.assertRaises(SystemExit) as error:
                smoke.main()
            self.assertEqual(error.exception.code, 2)
            self.assertIn("--timeout must be positive", stderr.getvalue())
            self.assertFalse(output.exists())


if __name__ == "__main__":
    unittest.main()
