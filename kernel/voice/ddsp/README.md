# Quantized neural reed

`ddsp(pitch, loudness)` produces eight harmonics and filtered noise from a
pinned neural network. It runs inside the native and TinyGo audio kernels.
The 210-byte model was trained for this repository on original procedural
data; the corpus and weights are redistributable under [CC0-1.0](MODEL-LICENSE).
The implementation and training script use the repository's MIT license.

```cicada
instrument neural_reed {
  octave = 4
  voice mono {
    let loudness = env(gate, 620ms) * velocity
    out = ddsp(pitch, loudness)
  }
}
```

Render the complete example from the repository root:

```sh
GOWORK=off go run ./cmd/cicada check examples/neural-reed.cicada
GOWORK=off go run ./cmd/cicada render examples/neural-reed.cicada -o build/neural-reed.wav --block 128
```

The model takes fundamental frequency in millihertz and linear loudness in
Q15 (0..32767). A 2-input, 8-hidden-unit clipped-ReLU network predicts eight
harmonic amplitudes and one noise amplitude. Weights and biases are signed
Q13 int16; bounded int32 accumulators, arithmetic shifts and saturation define every
rounding step. The output is signed Q15 PCM, converted to float32 by an exact
power-of-two scale at the graph boundary. `model.json` records the training
seed, held-out error and SHA-256 of the 210-byte parameter stream.

Oscillator and noise products round to Q15 before their bounded int32 sum;
the final PCM sample saturates to int16. Tests prove the pinned network's
accumulator bounds. Unchanged controls reuse the last prediction and gains.
Graph controls explicitly round the float32 product before the integer
conversion, so native FMA contraction cannot move half-integer boundaries.

The eight partials use a pinned integer sine table with linear interpolation.
Partials at or above 0.49 times the sample rate are excluded. Seeded xorshift
noise passes through an integer one-pole filter. Network predictions occur
every 64 samples, with 64-sample linear interpolation between controls.
The integer phase increment follows pitch immediately. Zero frequency or
zero loudness mutes immediately. `Reset` restores the phase, filter and noise
seed; changing the host's block partition does not change the output.

Training covers 80..1600 Hz and loudness 0..1, at 48 kHz. Runtime supports
44.1, 48 and 96 kHz. Network frequency controls saturate at 2048 Hz; oscillator
pitch remains bounded to 0.49 times the render rate. Values beyond the training
range are synthesis controls rather than a validated timbre match. Noise
color follows the render rate. This is a compact DDSP-style control model,
inspired by the [DDSP paper](https://arxiv.org/abs/2001.04643); it is a synthetic
reed demonstration, with no recorded-instrument or human-listening claim.

## Reproduce the model

The script generates all training examples locally from its documented reed
equations. Harmonic amplitudes and filtered-noise gain are supervised targets;
no pitch extractor, recordings, downloads or pretrained models are involved.
Training uses 512 seeded examples and Adam for 3500 steps; an independent
143-point pitch/loudness grid measures quantized error. Optional WAVs audition
the generated corpus; they are not needed for inference or training.

```sh
python3 -m venv build/ddsp-venv
build/ddsp-venv/bin/pip install -r tools/ddsp-requirements.txt
OPENBLAS_NUM_THREADS=1 build/ddsp-venv/bin/python tools/train-ddsp.py --audio-out build/ddsp-corpus
gofmt -w kernel/voice/ddsp/model.go
GOWORK=off go test ./kernel/voice/ddsp -count=1 -v
```

The Go tests hash the pinned weights, compare integer predictions against the
Python reference, and require held-out amplitude MSE below 0.00001 and maximum
error below 0.03. Training is outside the kernel. Neither Python nor NumPy is a
runtime dependency. The array file, provenance JSON, and held-out CSV are the
only required generated files. Reproduction uses NumPy 2.3.5 and one BLAS thread;
the shipped pins, rather than floating-point training, define runtime parity.

## Validate runtime behavior

```sh
GOWORK=off go test ./kernel/voice/ddsp ./kernel/graph ./host/kernelimage ./instrument -count=1
GOWORK=off go test -tags ddsp_wasm ./kernel/voice/ddsp -run TestDDSPNativeWASMParityAndBudget -count=1 -v
GOWORK=off make test-kernel-wasm budget-size test-golden
```

Project images retain their layout and set required capability bit 7. Older
kernels reject the operation before rendering.
