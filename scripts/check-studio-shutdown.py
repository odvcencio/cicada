#!/usr/bin/env python3
"""Check real Studio shutdown using disposable scores and null audio only."""

import argparse
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import tempfile
import time
from urllib.parse import urlsplit


def child_process(parent):
    output = subprocess.check_output(["ps", "-axo", "pid=,ppid=,command="], text=True)
    for line in output.splitlines():
        fields = line.strip().split(None, 2)
        if len(fields) == 3 and fields[1] == str(parent):
            backend = re.search(r"--backend (http://\S+)", fields[2])
            if backend:
                return int(fields[0]), backend[1]
    raise RuntimeError("Studio did not start its workstation child")


def running(pid):
    result = subprocess.run(["ps", "-p", str(pid), "-o", "stat="], capture_output=True, text=True)
    return result.returncode == 0 and not result.stdout.strip().startswith("Z")


def check(binary, output, mode):
    folder = Path(tempfile.mkdtemp(prefix=mode + "-", dir=output))
    source = ('cicada 2\ntempo 120\ntrack keys piano {}\n'
              'pattern tune notes { c4 . e4 . }\n'
              'arrange { place opening keys tune { at=@1.1.1 length=1bar } }\n')
    score = folder / "main.cicada"
    score.write_text(source)
    env = dict(os.environ, CICADA_STATE_DIR=str(folder / "state"),
               CICADA_AUDIO_SETTINGS_DIR=str(folder / "settings"))
    log_path = folder / "server.log"
    child, connections = None, []
    with log_path.open("w") as log:
        process = subprocess.Popen([str(binary), "studio", str(score), "--audio", "null",
                                    "--listen", "127.0.0.1:0"], env=env, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                match = re.search(r"Cicada Studio: (http://\S+)", log_path.read_text())
                if match:
                    break
                if process.poll() is not None:
                    raise RuntimeError("Studio exited before readiness")
                time.sleep(0.025)
            else:
                raise RuntimeError("Studio startup timed out")
            child, backend = child_process(process.pid)
            targets = []
            if mode.startswith("frontend") or mode == "both-partial":
                targets.append(match[1])
            if mode.startswith("domain") or mode == "both-partial":
                targets.append(backend)
            for target in targets:
                url = urlsplit(target)
                if url.hostname != "127.0.0.1":
                    raise RuntimeError("Shutdown checks require loopback services")
                conn = socket.create_connection((url.hostname, url.port), timeout=2)
                connections.append(conn)
                if mode.endswith("partial"):
                    conn.sendall(b"GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n")
            # Let both HTTP servers accept the deliberately incomplete clients.
            time.sleep(0.05)
            started = time.monotonic()
            process.send_signal(signal.SIGTERM)
            code = process.wait(timeout=4)
            while running(child) and time.monotonic() - started < 4:
                time.sleep(0.025)
            elapsed = time.monotonic() - started
            if running(child):
                raise RuntimeError("Workstation child survived Studio shutdown")
            if code != 0:
                raise RuntimeError(f"Studio shutdown returned {code}")
            if "context deadline exceeded" in log_path.read_text():
                raise RuntimeError("A Studio service exhausted its shutdown deadline")
            if score.read_text() != source:
                raise RuntimeError("Shutdown changed the disposable score")
            return {"mode": mode, "parentPID": process.pid, "childPID": child,
                    "parentExit": code, "childStopped": True, "scoreUnchanged": True,
                    "shutdownSeconds": elapsed, "log": str(log_path)}
        finally:
            for conn in connections:
                conn.close()
            if process.poll() is None:
                process.kill()
                process.wait()
            if child is not None and running(child):
                os.kill(child, signal.SIGTERM)
                deadline = time.monotonic() + 6
                while running(child) and time.monotonic() < deadline:
                    time.sleep(0.025)
                if running(child):
                    os.kill(child, signal.SIGKILL)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cicada", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    modes = ["quiet", "frontend-preconnect", "frontend-partial",
             "domain-preconnect", "domain-partial", "both-partial"]
    results = [check(args.cicada.resolve(), output, mode) for mode in modes]
    (output / "report.json").write_text(json.dumps(results, indent=2) + "\n")
    print(f"Studio shutdown passed for {len(results)} connection scenarios.")


if __name__ == "__main__":
    main()
