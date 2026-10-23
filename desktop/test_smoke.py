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
        data.mkdir(parents=True, exist_ok=True)
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

    def test_recovery_launches_share_the_score_and_private_state(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            score = folder / "draft.cicada"
            score.write_text("// unchanged disposable score\n")
            state = folder / "private-state"
            for _ in range(2):
                result, process, _ = self.run_host(folder, score=score, state=state)
                self.assertEqual(process.args[-1], str(score))
                self.assertEqual(result["stateDir"], str(state))
                self.assertEqual(process.args[process.args.index("--data-dir") + 1], str(state / "data"))
                self.assertEqual(score.read_text(), "// unchanged disposable score\n")

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

    def test_portable_workflow_changes_only_a_disposable_copy(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            binary = root / "Test.app/Contents/MacOS/cicada-studio"
            binary.parent.mkdir(parents=True)
            binary.touch()
            project = root / "fixture"
            project.mkdir()
            source = project / "song.cicada"
            source.write_text("// original fixture\n")
            fixture = root / "fixture.json"
            fixture.write_text(json.dumps({"score": str(source), "originalRemoved": True, "userLibrariesRemoved": True}))
            output = root / "results"

            def launch(*_, score, **__):
                self.assertNotEqual(score, source)
                self.assertTrue(score.is_relative_to(output.resolve()))
                score.write_text("// native workflow edit\n")
                return {"startup": {"frontend": {"ok": True}}}

            with patch("sys.argv", ["smoke.py", "--app", str(root / "Test.app"), "--output", str(output), "--mode", "normal", "--portable-fixture", str(fixture)]), \
                    patch.object(smoke, "isolated_app", return_value=contextlib.nullcontext(binary)), \
                    patch.object(smoke, "check_launch", side_effect=launch), contextlib.redirect_stdout(io.StringIO()):
                smoke.main()
            self.assertEqual(source.read_text(), "// original fixture\n")

    def test_project_workflow_copies_entry_and_secondary_sources(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            binary = root / "Test.app/Contents/MacOS/cicada-studio"
            binary.parent.mkdir(parents=True)
            binary.touch()
            project = root / "fixture"
            project.mkdir()
            source = project / "main.cicada"
            source.write_text("// entry\n")
            part = project / "arrangement.cicada"
            part.write_text("// arrangement\n")
            workflow = root / "workflow.js"
            workflow.write_text("// synthetic workflow\n")
            output = root / "results"

            def launch(*_, score, **__):
                self.assertNotEqual(score, source)
                self.assertTrue(score.is_relative_to(output.resolve()))
                score.write_text("// edited copy\n")
                (score.parent / part.name).write_text("// edited arrangement copy\n")
                return {"startup": {"frontend": {"ok": True}}}

            with patch("sys.argv", ["smoke.py", "--app", str(root / "Test.app"), "--output", str(output), "--mode", "normal", "--project-fixture", str(source), "--workflow", str(workflow)]), \
                    patch.object(smoke, "isolated_app", return_value=contextlib.nullcontext(binary)), \
                    patch.object(smoke, "check_launch", side_effect=launch), contextlib.redirect_stdout(io.StringIO()):
                smoke.main()
            self.assertEqual(source.read_text(), "// entry\n")
            self.assertEqual(part.read_text(), "// arrangement\n")


if __name__ == "__main__":
    unittest.main()
