#!/usr/bin/env python3
"""Run a build with a deadline, including its children, on macOS and Linux."""

import argparse
import math
import os
import re
import signal
import subprocess
import sys
import time


def duration(value):
    if not re.fullmatch(r"(?:\d+(?:\.\d*)?|\.\d+)[smhd]?", value):
        raise argparse.ArgumentTypeError("expected a duration such as 180s or 3m")
    suffix = value[-1] if value[-1].isalpha() else ""
    seconds = float(value[:-1] if suffix else value) * {"": 1, "s": 1, "m": 60, "h": 3600, "d": 86400}[suffix]
    if not math.isfinite(seconds):
        raise argparse.ArgumentTypeError("duration must be finite")
    return seconds


def signal_group(process, signum):
    try:
        os.killpg(process.pid, signum)
    except ProcessLookupError:
        pass


def stop(process, signum, grace):
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    signal_group(process, signum)
    forced = False
    try:
        process.wait(timeout=grace or None)
    except subprocess.TimeoutExpired:
        forced = True
    finally:
        # A shell may exit before a compiler child that ignores the signal.
        signal_group(process, signal.SIGKILL)
        process.wait()
    return forced


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kill-after", type=duration, default=5)
    parser.add_argument("duration", type=duration, help="deadline; 0 disables it")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if not args.command:
        parser.error("a build command is required")
    cancelled = 0

    def cancel(signum, _frame):
        nonlocal cancelled
        cancelled = signum

    signal.signal(signal.SIGINT, cancel)
    signal.signal(signal.SIGTERM, cancel)
    deadline = time.monotonic() + args.duration if args.duration else None
    try:
        process = subprocess.Popen(args.command, start_new_session=True)
    except OSError as error:
        print(f"cannot run {args.command[0]}: {error}", file=sys.stderr)
        return 127 if isinstance(error, FileNotFoundError) else 126

    while True:
        if cancelled:
            stop(process, cancelled, args.kill_after)
            return 128 + cancelled
        remaining = deadline - time.monotonic() if deadline is not None else .1
        if remaining <= 0:
            return 137 if stop(process, signal.SIGTERM, args.kill_after) else 124
        try:
            code = process.wait(timeout=min(.1, remaining))
            return code if code >= 0 else 128 - code
        except subprocess.TimeoutExpired:
            pass


if __name__ == "__main__":
    sys.exit(main())
