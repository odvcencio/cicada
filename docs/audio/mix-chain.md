# Opt-in mix chain

The new `kernel/fx/pro` package prepares a stereo chain in this order: four-band parametric EQ, linked compression, transient shaping, stereo width with optional bass centering, early reflections plus an eight-line FDN tail, and a true-peak lookahead limiter. Controls are fixed when the chain is prepared; `Process` and `Reset` allocate no memory. Zero parameters are an exact zero-latency bypass.

Native hosts assign a prepared chain to `engine.Config.MasterProcessor` before `engine.New`. The engine runs it after master gain and before the existing master safety limiter. `MasterProcessorLatencyFrames` reports its additional delay. Project image encoding rejects a prepared processor, because it contains host-owned state that cannot be silently omitted.

Browser hosts can load the optional `cicada-mix.wasm` companion module separately from the core. `host/promix/processor.mjs` prepares it before the AudioWorklet starts and copies stereo blocks of up to 128 frames without making new typed-array views during processing. Preparation, compilation, fetches and controls belong outside the callback. The companion adds download bytes and memory; its size is reported separately and does not change the default core's 300 KiB gate. The core worklet and project image ABI are unchanged.

Build and exercise the actual TinyGo module:

```sh
make -f tools/audio/mix.mk mix-wasm mix-wasm-test
```

Audition every processor with the same score, `examples/pro-mix.cicada`:

```sh
go run ./cmd/cicada-mix -score examples/pro-mix.cicada -preset dry -o before.wav
go run ./cmd/cicada-mix -score examples/pro-mix.cicada -preset mastering -lufs -14 -o after.wav
```

Other presets are `eq`, `compressor`, `transient`, `width`, `reverb`, and `limiter`. `-gain-db` sets the input gain for a limiter stress audition. The offline adapter compensates processor latency and drains delayed final samples, so paired files remain aligned.

The EQ implements peaking, shelving, highpass and lowpass biquads using the [W3C Audio EQ Cookbook](https://www.w3.org/TR/audio-eq-cookbook/). Compression reuses Cicada's existing soft-knee stereo detector. Transient shaping has 1 ms of lookahead. Width preserves the mid signal and optionally removes low-frequency side energy. The reverb adds independently timed stereo early reflections and wet width to the existing modulated FDN. These are useful controls, not measured emulations of a named commercial processor.

The limiter reconstructs intersample peaks with a finite polyphase windowed-sinc detector, applies linked smoothed gain, and leaves a conservative reserve below the requested ceiling. Independent longer and denser reconstruction tests cover tones, clipped signals, noise, and boundaries. Finite reconstruction and a fixture corpus are not a proof of a universal brickwall ceiling. The default limiter adds interpolation and lookahead latency; the API returns the exact number of frames.

`NormalizeStereo` is a two-pass host operation. It measures BS.1770 integrated loudness, applies a static gain within the true-peak constraint, and reports the achieved result and whether the target is reachable. It preserves crest factor and can therefore miss an ambitious target. Live limiting does not normalize a programme to LUFS. Review the JSON report rather than assuming the requested LUFS was reached.

Tests measure frequency response, dynamics gain, stereo linking, attack/release, exact reset silence, tails, allocations, latency, THD, and a high-rate residual on selected steady tones. The high-rate comparison is not full transient or wideband alias qualification. Sound quality and realtime CPU usage depend on the source audio and target device. The new chain is opt-in; legacy score goldens and default processing retain their output.

The integrated loudness meter now waits for a complete 400 ms gate before adding it to the histogram. Previously it included 100/200/300 ms startup fragments, biasing short programmes. A startup regression and the existing EBU suite cover the fix. This follows the [EBU programme loudness guidance](https://tech.ebu.ch/docs/tech/tech3343.pdf); incomplete gating blocks are discarded. Programmes shorter than 400 ms remain unchanged and report no integrated loudness.

## Score master effects

A score can declare each processor and order it on the master. Native playback,
WAV export, and stems export prepare these effects before audio rendering.
Studio shows the saved chain, followed by the built-in safety limiter.

```cicada
cicada 2
track bass acid {}
pattern pulse acid { 1 . 3 . 5 . 3 . }
scene main { bass = pulse }
song { main*16 }
fx tone eq { type = highpass frequency = 25Hz q = 0.707 }
fx glue comp { threshold = -18dB ratio = 2 makeup = 1dB }
fx punch transient { attack = 2dB sustain = -1dB }
fx stereo width { amount = 1.08 bass_mono = 100Hz }
fx ceiling limiter { ceiling = -1dBTP lookahead = 3ms release = 100ms }
master { insert = tone -> glue -> punch -> stereo -> ceiling }
export streaming { loudness = -14LUFS true_peak = -1dBTP normalize = off }
```

Run `cicada render score.cicada --export streaming --bars 16 -o streaming.wav`.
The export target uses the existing iterative BS.1770 loudness renderer and
measures the delivered WAV. Live playback does not apply programme normalization.
An unreachable target returns an error instead of claiming success.

| Kind | Controls (defaults; ranges) |
| --- | --- |
| `eq` | `type = peak` (`peak`, `low_shelf`, `high_shelf`, `highpass`, `lowpass`); `frequency = 1000Hz` (10 Hz to 0.45 × render rate); `q = 1` (0.1–20); `gain = 0dB` (−24 to +24 dB). Declare separate EQ effects for multiple bands. |
| `comp` | Existing compressor controls: `threshold`, `ratio`, `knee`, `attack`, `release`, `makeup`, `detect`, `mix`. Master compression detects its own stereo input. |
| `transient` | `attack = 0dB`, `sustain = 0dB` (−18 to +18 dB); `fast = 1ms` (0.1–10 ms), `slow = 30ms` (10–100 ms, greater than fast), `release = 120ms` (20–1000 ms). |
| `width` | `amount = 1` (0–2); `bass_mono = 0Hz` (bypass, or 20–500 Hz). |
| `limiter` | `ceiling = -1dBTP` (−12 to 0 dBTP); `lookahead = 3ms` (1–10 ms); `release = 100ms` (20–1000 ms). |
| `convolution` | Required `asset` name; `mix = 0.15` (0–1); `partition = 128frames` (power of two, 64–2048). See [score impulses](convolution.md#score-impulses). |

The chain accepts at most 16 distinct effect names, in any order. Processing and
reset allocate no memory. Offline export removes the summed processor latency
and drains delayed final samples. Omission or `insert = none` preserves legacy
output. Track and music-bus insert limits are unchanged. Master effects have
fixed controls until recompilation; scene settings and live parameter paths for
them are rejected. A compressor cannot share state between music and master.

Prepared processors cannot be encoded into a core WASM project image. Browser
score playback needs the companion modules connected by its host; that wiring
is separate from native playback and Studio's saved-chain display. The core
kernel ABI, worklet and size gates remain unchanged.
