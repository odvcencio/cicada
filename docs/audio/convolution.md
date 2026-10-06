# Convolution with licensed rooms

`kernel/fx/convolution` prepares a stereo FFT convolver before audio rendering. `New(rate, leftIR, rightIR, partitionFrames)` owns all storage; `Process` and `Reset` allocate no memory. A mono IR applies independently to both input channels. This is independent-channel stereo convolution, not a four-channel true-stereo IR matrix.

For long impulses with head partitions up to 512 frames, a small head handles early arrivals while a 2048-frame FFT tail runs as a bounded state machine across sixteen jobs. The tail starts at `4096 - headFrames` in the IR and has 4096 frames of internal delay; this preserves the requested head latency and exact convolution timing. No goroutine or asynchronous worker races the callback. Short IRs and larger partitions use the uniform path. Prefer 128 frames for live 48 kHz use: latency is 2.667 ms. Large uniform partitions can produce costly boundary bursts even when average CPU is low.

`host/irasset` verifies source dimensions, finite PCM, byte bounds and SHA-256 before preparing an IR. Its windowed-sinc resampler removes frequencies above the destination bandwidth and preserves transfer-function gain. Preparation can take seconds for a 192 kHz plate; perform it in a host worker, then pass owned state to the audio thread. The optional `Impulse.Condition(20, 1)` removes DC and limits stereo-linked impulse energy with attenuation only. Raw decode preserves source coefficients; conditioning creates new storage and is explicit.

The fetchable pack contains a treated bedroom, ballroom, swept physical plate, and outdoor gazebo. Every WAV is pinned to a source revision with license, URL, dimensions and SHA-256 in `assets/ir/manifest.json`. The source [IR-Library](https://github.com/itsmusician/IR-Library) licenses its recorded impulses under MIT. Preserve the fetcher's COPYING notice whenever redistributing the audio. No large audio is embedded in the repository or core WASM.

```sh
python3 assets/ir/fetch.py --help
go run ./cmd/cicada-convolve -score examples/convolution.cicada -asset room -ir room.wav -partition 128 -mix .15 -o room-audition.wav
make -f tools/audio/convolution.mk convolution-wasm convolution-wasm-test
```

The CLI conditions the impulse with a 20 Hz highpass and linked unit-energy ceiling, then compensates both wet and dry latency for its offline audition. All four IRs can use the same score. Source-file hashes refer to unmodified WAVs; preparation is a reproducible transformation.

Browser hosts lazily load and verify selected audio with `host/irasset/loader.mjs`, optionally call `conditionImpulse`, and prepare a separate `cicada-convolution.wasm` module through `processor.mjs`. Module compilation and IR transfer happen before the AudioWorklet connects. Its callback wrapper reuses typed-array views and clears output on a processing fault. Companion download bytes and resident IR/FFT memory are additional to the default core; the core's 300 KiB budget and worklet bytes remain unchanged.

## Score impulses

Declare a project-relative WAV as an `asset` with its SHA-256, frame count,
sample rate and channel count, then reference it with
`fx room convolution { asset = room-ir mix = 0.15 partition = 128frames }`
and `master { insert = room }`. The asset uses the existing edition-2 asset
syntax; include the impulse's redistribution license with your project.

Preparation confines paths and symlinks to the project directory, checks exact
file bytes and dimensions, rejects non-finite PCM, resamples to the render rate,
and applies the same 20 Hz DC removal and unit-energy ceiling as the convolution
audition CLI. Impulses are bounded to 12 seconds, the resident frame limit, and
16 MiB of source bytes. Wet and dry paths share the declared partition latency;
offline export removes it. Asset loading and all FFT storage allocation finish
before rendering. A score containing only impulse assets can use the ordinary
synth engine; sampler and clip playback still use their own host path.
