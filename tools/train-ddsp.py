#!/usr/bin/env python3
"""Train the CC0 synthetic reed control model; inference needs no Python.

Run with OPENBLAS_NUM_THREADS=1 and numpy==2.3.5. The corpus, targets and
weights created by this script are dedicated to CC0-1.0 (see MODEL-LICENSE).
The script itself is covered by the repository's MIT license.
"""
import argparse
import hashlib
import json
from pathlib import Path
import wave

import numpy as np

SEED = 20261006
HIDDEN = 8
HARMONICS = 8
STEPS = 3500


def teacher(x):
    """Synthetic reed: loudness opens upper partials; pitch changes the body."""
    f, loud = x[:, 0:1], x[:, 1:2]
    h = np.arange(1, HARMONICS + 1)[None, :]
    body = 1 + 0.12 * np.cos(f * np.pi * 2 + h * 0.7)
    harmonic = loud * 0.48 * body / h ** (1.5 - 0.45 * loud)
    noise = 0.025 * loud * (0.3 + f)
    return np.concatenate((harmonic, noise), axis=1)


def forward(x, w1, b1, w2, b2):
    z = x @ w1 + b1
    a = np.clip(z, 0, 1)
    return a @ w2 + b2, a, z


def quantized(x, w1, b1, w2, b2):
    # Mirror Go arithmetic, including arithmetic right shifts and saturation.
    f0 = np.rint(x[:, 0] * 2048000).astype(np.int64)
    loudness = np.rint(x[:, 1] * 32767).astype(np.int64)
    qx = np.column_stack((f0 * 32767 // 2048000, loudness))
    a = np.clip((qx @ w1 + (b1 << 15)) >> 13, 0, 32767)
    out = np.clip((a @ w2 + (b2 << 15)) >> 13, 0, 32767)
    out[(f0 == 0) | (loudness == 0)] = 0
    return out / 32768


def audio(path, x, y):
    """Optional corpus audition: 32 two-second examples, generated locally."""
    sr = 48000
    t = np.arange(sr * 2) / sr
    rng = np.random.default_rng(SEED)
    path.mkdir(parents=True, exist_ok=True)
    for i, (controls, target) in enumerate(zip(x[:32], y[:32])):
        pcm = sum(target[k] * np.sin(2 * np.pi * (k + 1) * controls[0] * 2048 * t)
                  for k in range(HARMONICS))
        noise = rng.uniform(-1, 1, len(t))
        for j in range(1, len(noise)):
            noise[j] = noise[j - 1] + (noise[j] - noise[j - 1]) / 4
        pcm += target[-1] * noise
        with wave.open(str(path / f"reed-{i:02d}.wav"), "wb") as out:
            out.setparams((1, 2, sr, 0, "NONE", "not compressed"))
            out.writeframes(np.rint(np.clip(pcm, -1, 1) * 32767).astype("<i2").tobytes())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=Path("kernel/voice/ddsp"))
    parser.add_argument("--audio-out", type=Path)
    args = parser.parse_args()
    rng = np.random.default_rng(SEED)
    x = rng.uniform([80 / 2048, 0], [1600 / 2048, 1], (512, 2))
    y = teacher(x)
    # Independent interleaved pitch/loudness grid, never used by the optimizer.
    test = np.array([(f / 2048, l) for f in np.linspace(93, 1587, 13)
                     for l in np.linspace(0, 1, 11)])
    target = teacher(test)
    params = [rng.normal(0, 0.4, (2, HIDDEN)), np.full(HIDDEN, 0.2),
              rng.normal(0, 0.1, (HIDDEN, HARMONICS + 1)), np.full(HARMONICS + 1, 0.03)]
    m = [np.zeros_like(p) for p in params]
    v = [np.zeros_like(p) for p in params]
    for step in range(1, STEPS + 1):
        w1, b1, w2, b2 = params
        pred, a, z = forward(x, *params)
        d = 2 * (pred - y) / y.size
        da = (d @ w2.T) * ((z > 0) & (z < 1))
        grads = [x.T @ da, da.sum(axis=0), a.T @ d, d.sum(axis=0)]
        for p, g, mi, vi in zip(params, grads, m, v):
            mi *= 0.9
            mi += 0.1 * g
            vi *= 0.999
            vi += 0.001 * g * g
            p -= 0.01 * (mi / (1 - 0.9 ** step)) / (np.sqrt(vi / (1 - 0.999 ** step)) + 1e-8)
    q = [np.rint(p * 8192).astype(np.int64) for p in params]
    if any(np.max(np.abs(p)) > 32767 for p in q):
        raise ValueError("weights exceed int16 range")
    packed = b"".join(p.astype("<i2").tobytes() for p in q)
    digest = hashlib.sha256(packed).hexdigest()
    qt = quantized(test, *q)
    report = {"model": "synthetic-reed-v1", "license": "CC0-1.0", "seed": SEED,
              "numpy": np.__version__, "steps": STEPS, "train_examples": len(x),
              "heldout_examples": len(test), "weight_fraction_bits": 13,
              "weight_bytes": len(packed), "sha256": digest,
              "training_mse": float(np.mean((forward(x, *params)[0] - y) ** 2)),
              "heldout_quantized_mse": float(np.mean((qt - target) ** 2)),
              "heldout_quantized_max_error": float(np.max(np.abs(qt - target))),
              "corpus": "Locally generated harmonic reed and filtered noise; no recordings or downloaded weights"}
    if report["heldout_quantized_mse"] > 0.0001:
        raise ValueError("held-out quality gate failed")
    args.out.mkdir(parents=True, exist_ok=True)
    lines = ["// Pinned synthetic-reed-v1 parameters; train with tools/train-ddsp.py.",
             "// Model parameters are CC0-1.0; see MODEL-LICENSE. Q13 int16.",
             "package ddsp", "", f'const ModelSHA256 = "{digest}"', ""]
    for name, p in zip(("inputWeights", "hiddenBias", "outputWeights", "outputBias"), q):
        # Runtime loops use neuron-major arrays; hash retains training layout.
        p = p.T if p.ndim == 2 else p
        shape = "".join(f"[{n}]" for n in p.shape)
        lines.append(f"var {name} = {shape}int16{{")
        for row in (p if p.ndim == 2 else [p]):
            text = ", ".join(str(int(n)) for n in row)
            lines.append("\t" + ("{" + text + "}," if p.ndim == 2 else text + ","))
        lines.append("}\n")
    sine = np.rint(np.sin(np.arange(257) * (2 * np.pi / 256)) * 32767).astype(int)
    lines.append("var sineTable = [257]int16{")
    for start in range(0, len(sine), 16):
        lines.append("\t" + ", ".join(map(str, sine[start:start + 16])) + ",")
    lines.append("}\n")
    (args.out / "model.go").write_text("\n".join(lines))
    (args.out / "model.json").write_text(json.dumps(report, indent=2) + "\n")
    # Go tests validate the independently evaluated integer network and quality.
    rows = ["f0_millihz,loudness," + ",".join(f"target{k}" for k in range(9)) +
            "," + ",".join(f"q15_{k}" for k in range(9))]
    for controls, expected, inferred in zip(test, target, qt):
        rows.append(f"{round(controls[0]*2048000)},{round(controls[1]*32767)}," +
                    ",".join(f"{n:.9f}" for n in expected) + "," +
                    ",".join(str(round(n * 32768)) for n in inferred))
    (args.out / "testdata").mkdir(exist_ok=True)
    (args.out / "testdata" / "heldout.csv").write_text("\n".join(rows) + "\n")
    if args.audio_out:
        audio(args.audio_out, x, y)
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
