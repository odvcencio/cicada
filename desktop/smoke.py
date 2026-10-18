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


def check_launch(binary, output, mode):
    folder = Path(tempfile.mkdtemp(prefix=mode + "-", dir=output))
    score = folder / "main.cicada"
    score.write_text(
        'title "Desktop CI"\ntempo 96\nkey c minor\n'
        "track bass acid { level = -24dB }\n"
        "pattern pulse { 1 . 5 . }\nscene main { bass = pulse }\nsong { main*4 }\n"
    )
    report = folder / "startup.json"
    args = [
        str(binary), "--audio", "null", "--data-dir", str(folder / "data"),
        "--smoke-out", str(report), "--smoke-hold", "5s" if mode == "normal" else "60s",
    ]
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
    env["CICADA_STATE_DIR"] = str(folder / "state")
    seen = set()
    with (folder / "stderr.log").open("w") as stderr:
        proc = subprocess.Popen(args, stdout=stderr, stderr=stderr, env=env)
        seen.add(proc.pid)
        deadline = time.monotonic() + 70
        checked = False
        try:
            while proc.poll() is None and time.monotonic() < deadline:
                seen |= descendants(proc.pid)
                log = folder / "data/logs/studio.log"
                text = log.read_text() if log.exists() else ""
                match = re.search(r"Cicada Studio: (http://127\.0\.0\.1:\d+/)", text)
                helper_pid = marker.read_text().strip() if marker.exists() else ""
                if not checked and mode.startswith("early-") and helper_pid:
                    seen.add(int(helper_pid))
                    proc.send_signal(signal.SIGTERM if mode.endswith("term") else signal.SIGINT)
                    checked = True
                elif not checked and match:
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
                    if mode != "normal":
                        proc.send_signal(signal.SIGTERM if mode == "term" else signal.SIGINT)
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
            return {
                "mode": mode, "pids": sorted(seen), "startup": startup,
                "artifacts": str(folder), "stateDir": env["CICADA_STATE_DIR"],
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
    args = parser.parse_args()
    app = args.app.resolve()
    binary = app / "Contents/MacOS/cicada-studio"
    if not binary.is_file():
        parser.error(f"desktop host is missing: {binary}")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    results = []
    with isolated_app(app, output) as binary:
        for mode in ["normal", "term", "int", "early-term", "early-int"]:
            result = check_launch(binary, output, mode)
            results.append(result)
            (output / "report.json").write_text(json.dumps(results, indent=2) + "\n")
            print(f"{mode}: passed", flush=True)


if __name__ == "__main__":
    main()
