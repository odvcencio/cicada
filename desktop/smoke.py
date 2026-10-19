#!/usr/bin/env python3
"""Check a packaged macOS app without an audio device or a user's project."""

import argparse
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import tempfile
import time
import urllib.request


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
    if mode == "midi-term":
        args.append("--smoke-midi")
    marker = folder / "helper.pid"
    if mode.startswith("early-"):
        helper = folder / "slow-cicada"
        helper.write_text(
            '#!/bin/sh\nprintf "%s\\n" "$$" > ' + shlex.quote(str(marker)) + "\nexec sleep 120\n"
        )
        helper.chmod(0o755)
        args += ["--cicada", str(helper)]
    args.append(str(score))
    seen = set()
    with (folder / "stderr.log").open("w") as stderr:
        proc = subprocess.Popen(args, stdout=stderr, stderr=stderr)
        seen.add(proc.pid)
        deadline = time.monotonic() + 70
        checked = False
        try:
            while proc.poll() is None and time.monotonic() < deadline:
                seen |= descendants(proc.pid)
                log = folder / "data/logs/studio.log"
                text = log.read_text() if log.exists() else ""
                match = re.search(r"^Cicada Studio: (http://127\.0\.0\.1:\d+/)(?:__local/[A-Za-z0-9_-]{20,128})?\s*$", text, re.MULTILINE)
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
            return {"mode": mode, "pids": sorted(seen), "startup": startup, "nativeMIDIConnected": mode == "midi-term", "artifacts": str(folder)}
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
    binary = args.app.resolve() / "Contents/MacOS/cicada-studio"
    if not binary.is_file():
        parser.error(f"desktop host is missing: {binary}")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    results = []
    for mode in ["normal", "term", "int", "early-term", "early-int", "midi-term"]:
        result = check_launch(binary, output, mode)
        results.append(result)
        (output / "report.json").write_text(json.dumps(results, indent=2) + "\n")
        print(f"{mode}: passed", flush=True)


if __name__ == "__main__":
    main()
