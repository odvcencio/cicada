#!/usr/bin/env python3
"""Compare UI-shaped pitches with independently authored musical references."""

import argparse
import hashlib
import json
import math
from pathlib import Path
import subprocess
import tempfile
import wave


PITCHES = {
    "up": [72, 76, 79, 84],
    "down": [53, 57, 60, 65],
    "mirror-first": [60, 56, 53, 48],
    "mirror-second": [68, 64, 61, 56],
}


def reference(pitches):
    # Fixed gates, dynamics, master/member expression and vibrato are authored
    # here, independently of the editor's pitch and source-writing code.
    a, b, c, d = pitches
    return f"""pattern phrase performance length=3840ticks slot=3 transpose=12 seed=99 {{
 gesture-group touch {{
  gesture at=0ticks bend=0ct pressure=0 timbre=0.5;
  gesture at=960ticks bend=200ct pressure=0.8 timbre=0.9;
  gesture at=3839ticks bend=-50ct pressure=0.3 timbre=0.4;
 }}
 note {a} at=17ticks length=703ticks velocity=103 group=touch {{
  gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7 vibrato=12ct rate=7Hz;
  gesture at=703ticks bend=100ct pressure=0.5 timbre=0.6;
 }}
 note {b} at=979ticks length=341ticks velocity=87 group=touch;
 note {c} at=1943ticks length=677ticks velocity=111 group=touch;
 note {d} at=1943ticks length=677ticks velocity=78 group=touch;
}}
"""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cicada", type=Path, required=True)
    parser.add_argument("--proof", type=Path, required=True)
    parser.add_argument("--native-export", type=Path)
    args = parser.parse_args()
    proof = args.proof.resolve()
    report = json.loads((proof / "report.json").read_text())["result"]
    reopened = json.loads((proof / "reopen-report.json").read_text())["result"]
    assert report["ok"] and reopened["ok"]
    assert report["shapes"]["mirror-second"]["audio"] == reopened["reopened"]
    if args.native_export:
        assert hashlib.sha256(args.native_export.read_bytes()).hexdigest() == reopened["reopened"]["hash"]
    output = Path(tempfile.mkdtemp(prefix="audio-reference-", dir=proof))
    sources = {"baseline": report["original"]}
    sources.update({name: shape["source"] for name, shape in report["shapes"].items()})
    sources.update({"reference-" + name: reference(notes) for name, notes in PITCHES.items()})
    scores = {}
    for name, source in sources.items():
        folder = output / name
        folder.mkdir()
        (folder / "main.cicada").write_text(report["initialEntry"])
        (folder / "phrase.cicada").write_text(source)
        (folder / "cicada.mod").write_text('project performance-pitch\ncicada 2\nentry "main.cicada"\nsource "phrase.cicada"\n')
        scores[name] = folder / "main.cicada"
    checks = []
    for rate in [44100, 48000]:
        pcm, frames = {}, {}
        for name, score in scores.items():
            wav = output / (name + f"-{rate}.wav")
            result = subprocess.run([str(args.cicada.resolve()), "render", str(score), "-o", str(wav), f"--rate={rate}", "--bits=24", "--dither=false"], capture_output=True, text=True)
            wav.with_suffix(".log").write_text(result.stdout + result.stderr)
            assert result.returncode == 0, result.stderr
            with wave.open(str(wav), "rb") as stream:
                assert stream.getframerate() == rate and stream.getnchannels() == 2 and stream.getsampwidth() == 3
                frames[name] = stream.getnframes()
                pcm[name] = stream.readframes(frames[name])
        for name in PITCHES:
            assert pcm[name] == pcm["reference-" + name], (rate, name, "differs from authored pitches or articulation")
            assert frames[name] == frames["baseline"]
            samples = [int.from_bytes(pcm[name][i:i+3], "little", signed=True) / 8388608 for i in range(0, len(pcm[name]), 3)]
            delta = [(int.from_bytes(pcm[name][i:i+3], "little", signed=True) - int.from_bytes(pcm["baseline"][i:i+3], "little", signed=True)) / 8388608 for i in range(0, len(pcm[name]), 3)]
            peak = max(map(abs, samples))
            rms = math.sqrt(sum(x*x for x in samples) / len(samples))
            difference = math.sqrt(sum(x*x for x in delta) / len(delta))
            assert 0 < peak < 1 and rms > 1e-6 and difference > 1e-6
            checks.append({"rate": rate, "shape": name, "frames": frames[name], "referencePCMIdentical": True, "peak": peak, "rms": rms, "differenceRMS": difference})
    result = {"output": str(output), "nativeExportCompared": bool(args.native_export), "freshProcessReopenMatches": True, "checks": checks}
    (proof / "audio.json").write_text(json.dumps(result, indent=2) + "\n")
    print(f"All {len(checks)} phrase-pitch renders match their authored references.")


if __name__ == "__main__":
    main()
