#!/usr/bin/env python3
"""Check local variations against the original library and an authored edit."""

import argparse
import hashlib
import json
import math
from pathlib import Path
import shutil
import subprocess
import tempfile
import wave


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cicada", type=Path, required=True)
    parser.add_argument("--proof", type=Path, required=True)
    parser.add_argument("--fixture", type=Path, required=True)
    parser.add_argument("--native-export", type=Path)
    args = parser.parse_args()
    proof = args.proof.resolve()
    result = json.loads((proof / "report.json").read_text())["result"]
    reopened = json.loads((proof / "reopen-report.json").read_text())["result"]
    fixture = json.loads(args.fixture.read_text())
    assert result["ok"] and reopened["ok"]
    assert result["baseline"] == result["unchanged"]
    assert result["performed"] == reopened["reopened"]
    if args.native_export:
        assert hashlib.sha256(args.native_export.read_bytes()).hexdigest() == result["performed"]["hash"]
    reference_grid = result["initial"].replace("upper=off", "upper=motif.idea")
    reference_played = result["initial"].replace("upper=off", "upper=reference-played") + '''
pattern reference-played performance length=7680ticks {
 gesture-group touch {
  gesture at=0ticks bend=0ct pressure=0 timbre=0.5;
  gesture at=1920ticks bend=200ct pressure=0.8 timbre=0.9;
 }
 note 67 at=34ticks length=1406ticks velocity=103 group=touch {
  gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7;
 }
 note 64 at=38ticks length=2602ticks velocity=87 group=touch;
}
'''
    assert reference_grid != result["initial"]
    folder = Path(tempfile.mkdtemp(prefix="audio-reference-", dir=proof))
    scores = {}
    for name, source in [("baseline", result["initial"]), ("copied", result["copied"]), ("grid", result["gridSource"]), ("performed", result["final"]), ("reference-grid", reference_grid), ("reference-performed", reference_played)]:
        target = folder / name
        shutil.copytree(Path(fixture["score"]).parent, target, ignore=shutil.ignore_patterns("exports", ".cicada"))
        (target / "main.cicada").write_text(source)
        scores[name] = target / "main.cicada"
    checks = []
    for rate in [44100, 48000]:
        pcm, frames = {}, {}
        for name, score in scores.items():
            output = folder / f"{name}-{rate}.wav"
            run = subprocess.run([str(args.cicada.resolve()), "render", str(score), "-o", str(output), f"--rate={rate}", "--bits=24", "--dither=false"], capture_output=True, text=True)
            output.with_suffix(".log").write_text(run.stdout + run.stderr)
            assert run.returncode == 0, run.stderr
            with wave.open(str(output), "rb") as stream:
                assert stream.getnchannels() == 2 and stream.getsampwidth() == 3 and stream.getframerate() == rate
                frames[name] = stream.getnframes()
                pcm[name] = stream.readframes(frames[name])
        assert pcm["baseline"] == pcm["copied"]
        for name in ["grid", "performed"]:
            assert pcm[name] == pcm["reference-" + name], (rate, name, "reference mismatch")
            assert frames[name] == frames["baseline"]
            samples = [int.from_bytes(pcm[name][i:i+3], "little", signed=True) / 8388608 for i in range(0, len(pcm[name]), 3)]
            delta = [(int.from_bytes(pcm[name][i:i+3], "little", signed=True) - int.from_bytes(pcm["baseline"][i:i+3], "little", signed=True)) / 8388608 for i in range(0, len(pcm[name]), 3)]
            peak, rms = max(map(abs, samples)), math.sqrt(sum(x*x for x in samples) / len(samples))
            difference = math.sqrt(sum(x*x for x in delta) / len(delta))
            assert 0 < peak < 1 and rms > 1e-6 and difference > 1e-6
            checks.append({"rate": rate, "material": name, "frames": frames[name], "originalSongPCMUnchangedByCopy": True, "referencePCMIdentical": True, "peak": peak, "rms": rms, "differenceRMS": difference})
    report = {"output": str(folder), "nativeExportCompared": bool(args.native_export), "freshProcessReopenMatches": True, "checks": checks}
    (proof / "audio.json").write_text(json.dumps(report, indent=2) + "\n")
    print(f"All {len(checks)} variation renders match their library or authored references.")


if __name__ == "__main__":
    main()
