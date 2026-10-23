#!/usr/bin/env python3
"""Compare UI-scaled phrases with separately authored timing references."""

import argparse
import hashlib
import json
import math
from pathlib import Path
import subprocess
import tempfile
import wave


# Absolute positions are authored independently of the edit implementation.
# Each row is loop length, two (start, gate length) pairs, shared controller
# positions, and note-relative controller positions with their value ordinals.
REFERENCES = {
    "1/2": (1920, [(9, 351), (10, 650)], [(0, 0), (1, 2), (480, 3), (1919, 4)], [(0, 1), (1, 2), (351, 3)]),
    "2/3": (2560, [(11, 469), (13, 867)], [(0, 0), (1, 2), (640, 3), (2559, 4)], [(0, 0), (1, 1), (2, 2), (469, 3)]),
    "3/2": (5760, [(26, 1054), (29, 1951)], [(0, 0), (2, 1), (3, 2), (1440, 3), (5759, 4)], [(0, 0), (1, 1), (3, 2), (1054, 3)]),
    "2/1": (7680, [(34, 1406), (38, 2602)], [(0, 0), (2, 1), (4, 2), (1920, 3), (7678, 4)], [(0, 0), (2, 1), (4, 2), (1406, 3)]),
}
SHARED = [(0, 0, .5), (10, .1, .6), (20, .2, .7), (200, .8, .9), (-50, .3, .4)]
MEMBER = [(25, .2, .7), (50, .3, .8), (75, .4, .9), (100, .5, .6)]


def reference(ratio):
    length, notes, shared, member = REFERENCES[ratio]
    lines = [f"pattern phrase performance length={length}ticks slot=3 seed=99 {{", " gesture-group touch {"]
    for tick, ordinal in shared:
        bend, pressure, timbre = SHARED[ordinal]
        lines.append(f"  gesture at={tick}ticks bend={bend}ct pressure={pressure} timbre={timbre};")
    lines += [" }", f" note 60 at={notes[0][0]}ticks length={notes[0][1]}ticks velocity=103 group=touch {{"]
    for tick, ordinal in member:
        bend, pressure, timbre = MEMBER[ordinal]
        lines.append(f"  gesture at={tick}ticks bend={bend}ct pressure={pressure} timbre={timbre};")
    lines += [" }", f" note 64 at={notes[1][0]}ticks length={notes[1][1]}ticks velocity=87 group=touch;", "}"]
    return "\n".join(lines) + "\n"


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
    assert report["scales"]["2/1"]["audio"] == reopened["reopened"]
    if args.native_export:
        assert hashlib.sha256(args.native_export.read_bytes()).hexdigest() == reopened["reopened"]["hash"]
    output = Path(tempfile.mkdtemp(prefix="audio-reference-", dir=proof))
    scores = {}
    for name, source in [("baseline", report["original"])] + [(r.replace("/", "-"), v["source"]) for r, v in report["scales"].items()] + [("reference-" + r.replace("/", "-"), reference(r)) for r in REFERENCES]:
        folder = output / name
        folder.mkdir()
        (folder / "main.cicada").write_text(report["initialEntry"])
        (folder / "phrase.cicada").write_text(source)
        (folder / "cicada.mod").write_text('project performance-time\ncicada 2\nentry "main.cicada"\nsource "phrase.cicada"\n')
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
        for ratio in REFERENCES:
            name = ratio.replace("/", "-")
            assert pcm[name] == pcm["reference-" + name], (rate, ratio, "render differs from authored reference")
            assert frames[name] == frames["baseline"]
            samples = [int.from_bytes(pcm[name][i:i+3], "little", signed=True) / 8388608 for i in range(0, len(pcm[name]), 3)]
            delta = [(int.from_bytes(pcm[name][i:i+3], "little", signed=True) - int.from_bytes(pcm["baseline"][i:i+3], "little", signed=True)) / 8388608 for i in range(0, len(pcm[name]), 3)]
            peak, rms = max(map(abs, samples)), math.sqrt(sum(x*x for x in samples) / len(samples))
            difference = math.sqrt(sum(x*x for x in delta) / len(delta))
            assert 0 < peak < 1 and rms > 1e-6 and difference > 1e-6
            checks.append({"rate": rate, "ratio": ratio, "frames": frames[name], "referencePCMIdentical": True, "peak": peak, "rms": rms, "differenceRMS": difference})
    result = {"output": str(output), "nativeExportCompared": bool(args.native_export), "freshProcessReopenMatches": True, "checks": checks}
    (proof / "audio.json").write_text(json.dumps(result, indent=2) + "\n")
    print(f"All {len(checks)} phrase-time renders match their authored references.")


if __name__ == "__main__":
    main()
