# Audio fingerprint gate

Run `make test-golden` from the repository root. It compiles `examples/first-acid.cicada`, renders eight bars through the native float32 engine at 48 kHz, and checks `testdata/golden/first-acid.fp`. The fixture is an acoustic regression reference, not a listening approval.

Each spectral frame spans 100 ms. A Hann window is zero-padded to 8192 samples at 44.1 or 48 kHz, or 16384 at 96 kHz, transformed with a fixed radix-2 FFT, and summarized into 64 logarithmically spaced bands from 40 Hz to 16 kHz. Stereo channel powers are averaged. Band energies are stored as signed int16 tenths of a decibel, with a −120 dB floor. A comparison requires the sample count and frame dimensions to match, mean absolute band drift at most 0.5 dB, and maximum band drift at most 2 dB.

The binary header is 56 bytes: `CIFP`, version 1, 64 bands, sample rate, audio sample count, samples per spectral window, spectral frame count, and a SHA-256 hash of the first 4096 interleaved stereo float32 frames. Fields are little-endian. On Linux amd64, the hash must match byte for byte; other platforms use the spectral budget.

To inspect a deliberate sound change, render and listen to the updated WAV, then run `go run ./cmd/cicada golden --update`. That command prints the drift from the previous fixture before replacing it. Record the listening note alongside the change. The PCM24 WAV export has its own duration, peak, and DC verifier; this fingerprint checks the live float32 engine shared with WASM.

## Decision 0002 calibration record (2026-09-24)

The owner listened to the baseline and calibrated First acid previews, described them as sounding mostly the same, called the result nice, and instructed us to proceed. The baseline PCM24 WAV has SHA-256 `b156cacba9a5cdf5fec68b493eb0c4091cd06773567e8e838cec90be069e0987`; the calibrated WAV has SHA-256 `1db486bb8722bae66b8246836de6649f359bb8d1d950526a3bfb9c076a50de0c`. These full WAVs are generated review artifacts, not repository fixtures.

The updated eight-bar fingerprint differs from the old reference by a mean 0.261 dB and maximum 23.700 dB across bands; its first-sample hash also changes. The calibrated 16-bar WAV has 1,479,653 frames at 48 kHz, peak -10.26 dBFS, DC -61.15 dBFS, and no clipped samples. The optimization in the calibration branch reproduces that calibrated WAV byte for byte. `make test-golden` passes with the new reference. The native acid performance budget and the separate alias-rejection gate remain open.
