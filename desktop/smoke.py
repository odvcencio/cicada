#!/usr/bin/env python3
"""Check a packaged macOS app without an audio device or a user's project."""

import argparse
from contextlib import contextmanager
import json
import os
from pathlib import Path
import plistlib
import re
import shlex
import shutil
import signal
import subprocess
import tempfile
import time
import urllib.request
import uuid


@contextmanager
def isolated_app(app, output):
    # Darwin ignores Options.UserDataDir for WKWebView. Its default store uses
    # the bundle identity, so a second production bundle would share that store.
    bundle_id = "dev.cicada.studio.smoke-" + uuid.uuid4().hex
    website_data = [
        Path.home() / "Library/WebKit" / bundle_id,
        Path.home() / "Library/Caches" / bundle_id,
    ]
    if any(path.exists() or path.is_symlink() for path in website_data):
        raise RuntimeError("smoke bundle identity already has website data")
    with tempfile.TemporaryDirectory(prefix="cicada-desktop-smoke-") as temp:
        copied_app = Path(temp) / "Cicada Studio Smoke.app"
        shutil.copytree(app, copied_app, symlinks=True)
        plist = copied_app / "Contents/Info.plist"
        info = plistlib.loads(plist.read_bytes())
        info["CFBundleIdentifier"] = bundle_id
        info["CFBundleName"] = info["CFBundleDisplayName"] = "Cicada Studio Smoke"
        # A temporary test app must never become Finder's default score editor.
        info.pop("CFBundleDocumentTypes", None)
        info.pop("UTExportedTypeDeclarations", None)
        plist.write_bytes(plistlib.dumps(info))
        with (output / "signing.log").open("w") as log:
            for args in [
                ["-s", "-", "--force", "--timestamp=none", "--deep"],
                ["--verify", "--deep", "--strict"],
            ]:
                subprocess.run(["codesign", *args, str(copied_app)],
                               stdout=log, stderr=log, check=True)
        isolation = {
            "originalApp": str(app), "temporaryApp": str(copied_app),
            "bundleID": bundle_id, "websiteData": [str(path) for path in website_data],
        }
        report = output / "isolation.json"
        report.write_text(json.dumps(isolation, indent=2) + "\n")
        try:
            yield copied_app / "Contents/MacOS/cicada-studio"
        finally:
            # Only these fresh, randomly named stores belong to this smoke run.
            for path in website_data:
                shutil.rmtree(path, ignore_errors=True)
            isolation["remainingWebsiteData"] = [
                str(path) for path in website_data if path.exists() or path.is_symlink()
            ]
            report.write_text(json.dumps(isolation, indent=2) + "\n")


def alive(pid):
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False


def descendants(pid):
    rows = subprocess.check_output(["ps", "-axo", "pid=,ppid="], text=True)
    pairs = [tuple(map(int, row.split())) for row in rows.splitlines()]
    found = {pid}
    while True:
        children = {child for child, parent in pairs if parent in found} - found
        if not children:
            return found
        found |= children


def check_launch(binary, output, mode, workflow, timeout=60, score=None, state=None):
    folder = Path(tempfile.mkdtemp(prefix=mode + "-", dir=output))
    if score is None:
        score = folder / "main.cicada"
        score.write_text(
            'title "Desktop CI"\ntempo 96\nkey c minor\n'
            "track bass acid { level = -24dB }\n"
            "pattern pulse { 1 . 5 . }\nscene main { bass = pulse }\nsong { main*4 }\n"
        )
    report = folder / "startup.json"
    data_dir = (state or folder) / "data"
    args = [
        str(binary), "--audio", "null", "--data-dir", str(data_dir),
        "--smoke-out", str(report), "--smoke-timeout", f"{timeout}s", "--smoke-hold", "5s" if mode == "normal" else "60s",
    ]
    if mode == "midi-term":
        args.append("--smoke-midi")
    if mode == "normal":
        args += ["--smoke-script", str(workflow)]
    marker = folder / "helper.pid"
    if mode.startswith("early-"):
        helper = folder / "slow-cicada"
        helper.write_text(
            '#!/bin/sh\nprintf "%s\\n" "$$" > ' + shlex.quote(str(marker)) + "\nexec sleep 120\n"
        )
        helper.chmod(0o755)
        args += ["--cicada", str(helper)]
    args.append(str(score))
    env = os.environ.copy()
    env["CICADA_STATE_DIR"] = str(state or folder / "state")
    env["CICADA_AUDIO_SETTINGS_DIR"] = str((state or folder) / "audio-settings")
    seen = set()
    with (folder / "stderr.log").open("w") as stderr:
        proc = subprocess.Popen(args, stdout=stderr, stderr=stderr, env=env)
        seen.add(proc.pid)
        deadline = time.monotonic() + timeout + 10
        checked = False
        try:
            while proc.poll() is None and time.monotonic() < deadline:
                seen |= descendants(proc.pid)
                log = data_dir / "logs/studio.log"
                text = log.read_text() if log.exists() else ""
                match = re.search(r"^Cicada Studio: (http://127\.0\.0\.1:\d+/)(?:__local/[A-Za-z0-9_-]{20,128})?\s*$", text, re.MULTILINE)
                helper_pid = marker.read_text().strip() if marker.exists() else ""
                if not checked and mode.startswith("early-") and helper_pid:
                    seen.add(int(helper_pid))
                    proc.send_signal(signal.SIGTERM if mode.endswith("term") else signal.SIGINT)
                    checked = True
                elif not checked and match:
                    if mode == "normal":
                        # Let WKWebView own the first authenticated session.
                        # Its workflow checks runtime and mutations itself.
                        checked = True
                        continue
                    url = match[1]
                    with urllib.request.urlopen(url, timeout=5) as response:
                        page = response.read().decode()
                    scripts = re.findall(r'<script[^>]+src="([^"]+)"', page)
                    assets = [path for path in scripts if path.startswith("/")]
                    if not assets:
                        raise RuntimeError("workstation page did not include runtime scripts")
                    for path in assets:
                        with urllib.request.urlopen(url.rstrip("/") + path, timeout=5) as response:
                            response.read()
                    if mode == "midi-term":
                        startup = json.loads(report.read_text()) if report.exists() else {}
                        if startup.get("nativeMIDIError"):
                            raise RuntimeError(f"native MIDI did not connect: {startup['nativeMIDIError']}")
                        if "Native MIDI input disconnected" in text:
                            raise RuntimeError("native MIDI disconnected before the shutdown signal")
                        if "Native MIDI input connected" not in text:
                            time.sleep(0.1)
                            continue
                    if mode != "normal":
                        proc.send_signal(signal.SIGTERM if mode in {"term", "midi-term"} else signal.SIGINT)
                    checked = True
                time.sleep(0.1)
            proc.wait(timeout=5)
            if proc.returncode != 0 or not checked:
                raise RuntimeError(f"{mode}: exit={proc.returncode}, ready={checked}; see {folder}")
            deadline = time.monotonic() + 3
            while any(alive(pid) for pid in seen) and time.monotonic() < deadline:
                time.sleep(0.1)
            remaining = [pid for pid in seen if alive(pid)]
            if remaining:
                raise RuntimeError(f"{mode}: helper processes survived shutdown: {remaining}")
            startup = json.loads(report.read_text())
            if startup.get("timeout") or startup.get("sidecarError"):
                raise RuntimeError(f"{mode}: failed startup: {startup}")
            if mode == "normal" and not startup.get("studioLoadedMs"):
                raise RuntimeError(f"native window never loaded Studio: {startup}")
            if mode == "normal" and not startup.get("frontend", {}).get("ok"):
                raise RuntimeError(f"native workflow failed: {startup.get('frontend')}; see {folder}")
            return {
                "mode": mode, "pids": sorted(seen), "startup": startup,
                "artifacts": str(folder), "stateDir": env["CICADA_STATE_DIR"], "dataDir": str(data_dir),
            }
        finally:
            if proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()
            # Only terminate processes observed as descendants of this test app.
            for pid in seen:
                try:
                    os.kill(pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--workflow", type=Path)
    parser.add_argument("--portable-fixture", type=Path, help="copy a generated portable-song fixture before running the normal workflow")
    parser.add_argument("--project-fixture", type=Path, help="copy a disposable score's containing folder before running a supplied normal workflow")
    parser.add_argument("--mode", choices=["all", "normal", "term", "int", "early-term", "early-int", "midi-term", "code-recovery"], default="all")
    parser.add_argument("--timeout", type=int, default=60, help="total workflow timeout in seconds (default: 60)")
    args = parser.parse_args()
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    if args.portable_fixture and args.mode != "normal":
        parser.error("--portable-fixture requires --mode normal")
    if args.project_fixture and (args.mode != "normal" or args.portable_fixture or not args.workflow):
        parser.error("--project-fixture requires --mode normal and --workflow, without --portable-fixture")
    app = args.app.resolve()
    binary = app / "Contents/MacOS/cicada-studio"
    if not binary.is_file():
        parser.error(f"desktop host is missing: {binary}")
    workflow = (args.workflow or Path(__file__).parent / "testdata" / ("native-portable-song-workflow.js" if args.portable_fixture else "native-workflow.js")).resolve()
    if not workflow.is_file():
        parser.error(f"native workflow is missing: {workflow}")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    results = []
    with isolated_app(app, output) as binary:
        if args.portable_fixture or args.project_fixture:
            if args.portable_fixture:
                fixture = json.loads(args.portable_fixture.read_text())
                if not fixture.get("originalRemoved") or not fixture.get("userLibrariesRemoved"):
                    raise RuntimeError("use only a generated portable-song fixture")
                source = Path(fixture["score"]).resolve()
            else:
                source = args.project_fixture.resolve()
            folder = Path(tempfile.mkdtemp(prefix="portable-", dir=output))
            shutil.copytree(source.parent, folder / "project")
            result = check_launch(binary, output, "normal", workflow, args.timeout,
                                  score=folder / "project" / source.name, state=folder / "state")
            (output / "report.json").write_text(json.dumps([result], indent=2) + "\n")
            print("project-fixture: passed" if args.project_fixture else "portable-song: passed", flush=True)
            return
        if args.mode == "code-recovery":
            # One disposable score and private profile across three app exits.
            folder = Path(tempfile.mkdtemp(prefix="recovery-", dir=output))
            shutil.copytree(Path(__file__).resolve().parent.parent / "examples/studio-project", folder / "project")
            workflow = Path(__file__).resolve().parent / "testdata/native-code-recovery-workflow.js"
            for phase in ["capture", "recover", "verify"]:
                result = check_launch(binary, output, "normal", workflow, args.timeout,
                                      score=folder / "project/main.cicada", state=folder / "state")
                checks = result["startup"]["frontend"].get("checks", [])
                if not checks or not all(check.startswith(phase + ": ") for check in checks):
                    raise RuntimeError(f"native recovery skipped {phase}: {result}")
                result["phase"] = phase
                results.append(result)
                (output / "report.json").write_text(json.dumps(results, indent=2) + "\n")
                print(f"code-recovery {phase}: passed", flush=True)
            return
        modes = ["normal", "term", "int", "early-term", "early-int", "midi-term"] if args.mode == "all" else [args.mode]
        for mode in modes:
            result = check_launch(binary, output, mode, workflow, args.timeout)
            results.append(result)
            (output / "report.json").write_text(json.dumps(results, indent=2) + "\n")
            print(f"{mode}: passed", flush=True)


if __name__ == "__main__":
    main()
