#!/usr/bin/env python3
"""Compare root-note sampler renders with their licensed original recordings.
Requires NumPy/FFmpeg and source_render.go output. Reports conversion/SRC and
post-attack envelope error separately from recorded source tail noise.
"""

import argparse
import hashlib
import json
import pathlib
import re
import subprocess

import numpy as np


def sha(b):
    return hashlib.sha256(b).hexdigest()


def main(args):
    selections = {}
    for line in pathlib.Path(args.selections).read_text().splitlines():
        match = re.fullmatch(r"(\w+) asset=(\w+) frames=(\d+) velocity=(\d+)", line)
        if match:
            selections[match[1]] = (match[2], int(match[3]), int(match[4]))
    if set(selections) != {"grand", "nylon", "bass", "kit", "violin", "trumpet"}:
        raise ValueError("reference selections must cover all six catalog instruments")
    result = []
    for name, (ident, frames, velocity) in selections.items():
        m = json.loads((pathlib.Path(args.packs) / name / "manifest.json").read_text())
        a = next(a for a in m["assets"] if a["id"] == ident)
        suffix = pathlib.Path(a["source_url"]).suffix
        source = pathlib.Path(args.sources) / (sha(a["source_url"].encode()) + suffix)
        if sha(source.read_bytes()) != a["source_sha256"]:
            raise ValueError("source hash mismatch")
        raw = subprocess.check_output(
            [
                "ffmpeg",
                "-v",
                "error",
                "-nostdin",
                "-i",
                str(source),
                "-map_metadata",
                "-1",
                "-ar",
                "48000",
                "-ac",
                str(a["channels"]),
                "-f",
                "f32le",
                "-threads",
                "1",
                "pipe:1",
            ]
        )
        original = (
            np.frombuffer(raw, dtype="<f4")
            .reshape(-1, a["channels"])[:frames]
            .astype(np.float64)
        )
        if a["channels"] == 1:
            original = np.repeat(original, 2, axis=1)
        rendered = np.fromfile(
            pathlib.Path(args.renders) / (name + ".f32"), dtype="<f4"
        ).reshape(-1, 2).astype(np.float64) / (velocity / 127)
        if len(rendered) != frames or len(original) != frames or frames < 1024:
            raise ValueError(
                "reference render is truncated or has unexpected dimensions"
            )
        # Exclude the intentional 2 ms onset ramp, compare the remaining envelope.
        original = original[128:]
        rendered = rendered[128:]
        length = min(len(original), len(rendered))
        original = original[:length]
        rendered = rendered[:length]

        def rms(x):
            return float(np.sqrt(np.mean(x * x)))

        residual = rms(rendered - original)
        signal = rms(original)
        n = min(32768, length)
        window = np.hanning(n)[:, None]
        ref = np.abs(np.fft.rfft(original[:n] * window, axis=0))
        got = np.abs(np.fft.rfft(rendered[:n] * window, axis=0))
        mask = ref > np.max(ref) * 0.001
        spectrum = float(
            np.max(np.abs(20 * np.log10(np.maximum(got[mask], 1e-30) / ref[mask])))
        )
        block = 480
        whole = length // block
        refenv = np.sqrt(
            np.mean(
                original[: whole * block].reshape(whole, block, 2) ** 2, axis=(1, 2)
            )
        )
        env = np.sqrt(
            np.mean(
                rendered[: whole * block].reshape(whole, block, 2) ** 2, axis=(1, 2)
            )
        )
        valid = refenv > signal * 0.01
        envelope = float(
            np.max(np.abs(20 * np.log10(np.maximum(env[valid], 1e-30) / refenv[valid])))
        )
        tail = original[-min(4800, length) :]
        item = dict(
            instrument=name,
            source_sha256=a["source_sha256"],
            source_url=a["source_url"],
            frames_compared=length,
            source_relative_residual_db=20
            * np.log10(max(residual, 1e-30) / max(signal, 1e-30)),
            max_spectrum_error_db_above_minus60=spectrum,
            max_10ms_envelope_error_db_above_minus40=envelope,
            recorded_excerpt_tail_rms_dbfs=20 * np.log10(max(rms(tail), 1e-30)),
            source_dc_dbfs=20 * np.log10(max(abs(float(np.mean(original))), 1e-30)),
        )
        item["status"] = (
            "pass"
            if item["source_relative_residual_db"] <= -75
            and spectrum <= 0.1
            and envelope <= 0.01
            else "miss"
        )
        result.append(item)
    pathlib.Path(args.out).write_text(
        json.dumps(
            dict(
                reference="licensed original recording decoded to 48 kHz stereo float32; selected root/layer; first 128 frames excluded; significant spectral bins and 10 ms envelope frames",
                metrics=result,
            ),
            indent=2,
        )
        + "\n"
    )
    print(json.dumps(result), flush=True)
    if any(x["status"] == "miss" for x in result):
        raise SystemExit("licensed-source comparison has misses; see JSON report")


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--packs", default="build/pro-packs")
    p.add_argument("--sources", required=True)
    p.add_argument("--renders", default="build/source-reference")
    p.add_argument("--selections", required=True)
    p.add_argument("--out", default="build/source-reference/metrics.json")
    main(p.parse_args())
