#!/usr/bin/env python3
"""Verify source-aware performed edits against independently authored audio."""

import argparse
import importlib.util
import json
import math
from pathlib import Path
import subprocess
import sys
import tempfile
import wave


FINAL = """pattern phrase performance length=7680ticks slot=3 transpose=12 seed=99 {
 gesture-group swell {
  gesture at=0ticks bend=0ct pressure=0 timbre=0.5;
  gesture at=1200ticks bend=150ct pressure=0.7 timbre=0.6;
  gesture at=3839ticks bend=-50ct pressure=0.3 timbre=0.4;
 }
 gesture-group lift {
  gesture at=0ticks bend=0ct pressure=0 timbre=0.5;
  gesture at=960ticks bend=-100ct pressure=0.9 timbre=0.8;
 }
 note 60 at=17ticks length=703ticks velocity=103 group=swell {
  gesture at=0ticks bend=75ct pressure=0.6 timbre=0.4 vibrato=14ct rate=6Hz;
  gesture at=703ticks bend=100ct pressure=0.5 timbre=0.6;
 }
 note 65 at=1001ticks length=341ticks velocity=95 group=lift;
 note 67 at=1943ticks length=677ticks velocity=111 group=swell;
 note 72 at=1943ticks length=677ticks velocity=78 group=swell;
}
"""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cicada", type=Path, required=True)
    parser.add_argument("--proof", type=Path, required=True)
    args = parser.parse_args()
    proof = args.proof.resolve()
    report = json.loads((proof / "report.json").read_text())["result"]
    reopened = json.loads((proof / "reopen-report.json").read_text())["result"]
    assert report["ok"] and reopened["ok"]
    assert report["exported"] == reopened["reopened"]
    spec = importlib.util.spec_from_file_location("pitch_reference", Path(__file__).with_name("performance-pitch-audio.py"))
    reference = importlib.util.module_from_spec(spec)
    sys.dont_write_bytecode = True
    spec.loader.exec_module(reference)
    sources = {"baseline": report["original"], "edited": report["final"], "reference-baseline": reference.reference([60, 64, 67, 72]), "reference-edited": FINAL}
    output = Path(tempfile.mkdtemp(prefix="audio-reference-", dir=proof))
    scores = {}
    for name, source in sources.items():
        folder = output / name
        folder.mkdir()
        (folder / "main.cicada").write_text(report["initialEntry"])
        (folder / "phrase.cicada").write_text(source)
        (folder / "cicada.mod").write_text('project performance-sources\ncicada 2\nentry "main.cicada"\nsource "phrase.cicada"\n')
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
        assert pcm["baseline"] == pcm["reference-baseline"], (rate, "baseline articulation differs")
        assert pcm["edited"] == pcm["reference-edited"], (rate, "performed edits differ from authored reference")
        assert frames["baseline"] == frames["edited"]
        samples = [int.from_bytes(pcm["edited"][i:i+3], "little", signed=True) / 8388608 for i in range(0, len(pcm["edited"]), 3)]
        delta = [(int.from_bytes(pcm["edited"][i:i+3], "little", signed=True) - int.from_bytes(pcm["baseline"][i:i+3], "little", signed=True)) / 8388608 for i in range(0, len(pcm["edited"]), 3)]
        peak = max(map(abs, samples))
        rms = math.sqrt(sum(x*x for x in samples) / len(samples))
        difference = math.sqrt(sum(x*x for x in delta) / len(delta))
        assert 0 < peak < 1 and rms > 1e-6 and difference > 1e-6
        checks.append({"rate": rate, "frames": frames["edited"], "baselinePCMIdentical": True, "editedPCMIdentical": True, "peak": peak, "rms": rms, "differenceRMS": difference})
    (proof / "audio.json").write_text(json.dumps({"output": str(output), "freshProcessReopenMatches": True, "checks": checks}, indent=2) + "\n")
    print("Baseline and edited performances match their authored references at 44.1 and 48 kHz.")


if __name__ == "__main__":
    main()
