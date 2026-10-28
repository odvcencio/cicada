# Opt-in mix chain

The new `kernel/fx/pro` package prepares a stereo chain in this order: four-band parametric EQ, linked compression, transient shaping, stereo width with optional bass centering, early reflections plus an eight-line FDN tail, and a true-peak lookahead limiter. Controls are fixed when the chain is prepared; `Process` and `Reset` allocate no memory. Zero parameters are an exact zero-latency bypass.

Native hosts assign a prepared chain to `engine.Config.MasterProcessor` before `engine.New`. The engine runs it after master gain and before the existing master safety limiter. `MasterProcessorLatencyFrames` reports its additional delay. Project image encoding rejects a prepared processor, because it contains host-owned state that cannot be silently omitted.

Browser hosts can load the optional `cicada-mix.wasm` companion module separately from the core. `host/promix/processor.mjs` prepares it before the AudioWorklet starts and copies stereo blocks of up to 128 frames without making new typed-array views during processing. Preparation, compilation, fetches and controls belong outside the callback. The companion adds download bytes and memory; its size is reported separately and does not change the default core's size gate of 320 KiB raw and 120 KiB Brotli. The core retains these size gates; saved track effects use a separately negotiated project-image capability.

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

The limiter reconstructs intersample peaks with a finite polyphase windowed-sinc detector, applies linked smoothed gain, and leaves a conservative reserve below the requested ceiling. Finite reconstruction cannot guarantee a universal brickwall ceiling. The default limiter adds interpolation and lookahead latency; the API returns the exact number of frames.

`NormalizeStereo` is a two-pass host operation. It measures BS.1770 integrated loudness, applies a static gain within the true-peak constraint, and reports the achieved result and whether the target is reachable. It preserves crest factor and can therefore miss an ambitious target. Live limiting does not normalize a programme to LUFS. Review the JSON report rather than assuming the requested LUFS was reached.

Integrated loudness requires a complete 400 ms gating block; shorter programmes report no integrated loudness.

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

### Export loudness targets

A loudness target (`--loudness`, or an `export` block with `loudness`) renders
the score in up to six passes. Each pass measures the delivered WAV and adjusts
one static gain. Where that gain goes depends on the master chain.

| Master chain | Where the gain goes | What holds the true-peak ceiling |
| --- | --- | --- |
| No limiter | Direct renderer: after the master chain. Scheduled renderer (arrangements, audio clips, serial track effects, sample packs): at the master input, as in earlier releases, because the engine applies its master gain before the chain. Output is unchanged from earlier releases. | The gain stops at the ceiling. It keeps crest factor, so an ambitious target can miss and returns a shortfall error. |
| Contains a `limiter` | At the master input: after the music and SFX buses sum and the master `level`, before the chain. | Your limiter. The extra level reaches it, so it reduces peaks and a punchy mix can get louder than its natural crest allows. |

With a limiter in the chain, the renderer keeps two guard rails:

- Input gain is capped at +12 dB.
- The chain limiter's gain reduction is held to 6 dB or less. Ordinary
  streaming masters use a few dB of limiting; 6 dB is the upper end.

A target that needs more than either limit stops short and returns a shortfall
error that names the limit that bound. The WAV from the best pass is still
written (Studio keeps it as a separate shortfall file). When even a render
that breaks a limit is too quiet for the target, and the best render is within
0.5 dB of the reduction maximum, the search stops instead of using all six
passes. The render summary reports the applied gain, the gain stage (`master_input` when the gain runs ahead of the master chain, `post_chain` when it runs after it or there is no chain), the
chain limiter's largest gain reduction, and the largest gain reduction of any
limiter. Review these numbers and run `cicada verify-wav` on the result.

When a master chain follows the direct renderer, only the first pass
synthesizes the mix. It stores the signal that enters the chain in a temporary
file next to the output, and later passes play that signal back through the
chain with the new gain. The result matches rendering every pass in full. The
scheduled renderer renders each pass in full.

The file takes about 23 MB per minute at 48 kHz, counted from the start of the
song to the end of the exported range plus its tail, so an export of the last
bars of a long song stores the whole song up to them. It stays until the export
ends, on the same disk as the export's two other temporary WAV files (one holds
the pass being rendered, the other the best pass so far). On a nearly full disk
it can take space that those files need, so the export frees it as follows:

- If the export cannot create the file, or a write to it fails, it renders every
  pass in full.
- If a pass, or the copy of a pass to the best-pass file, fails while the file
  exists, the export deletes the file, repeats that step without it, and renders
  every later pass in full. A pass fails when its render, its sync to disk or its
  close fails, or when the stored signal cannot be read back.
- If the repeated step fails too, the export stops with that error.

Passes rendered without the file take longer, but the WAV and the report are the
same.

The same renderer serves `cicada render --export`, `--loudness`, and Studio's
export. `cicada stems` does not apply loudness targets. The built-in safety
limiter (ceiling -0.3 dBFS) still follows the chain in both modes.

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
output. Tracks accept eight serial inserts; the music bus still accepts one compressor. Master effects have
fixed controls until recompilation; scene settings and live parameter paths for
them are rejected. A compressor cannot share state between music and master.

Prepared processors cannot be encoded into a core WASM project image. Browser
score playback needs the companion modules connected by its host; that wiring
is separate from native playback and Studio's saved-chain display. The master companion remains separate from core track processing.


## Track inserts

Track inserts accept up to eight distinct named stages in authored order.
Supported kinds are `drive`, `eq`, `comp`, and `deesser`. Each track owns separate
filter and detector state. The chain runs before the fader and both send taps,
so vocal reverb and delay receive the processed voice. All processing and resets
allocate no memory. The engine aligns tracks to the longest drive-insert delay;
EQ, compression, and de-essing add no latency.

```cicada
cicada 2
fx cleanup eq { type = highpass frequency = 80Hz q = 0.707 }
fx leveler comp { threshold = -18dB ratio = 3 makeup = 0dB }
fx sibilance deesser { frequency = 6000Hz threshold = -24dB ratio = 4 range = 9dB }
track lead acid { insert = cleanup -> leveler -> sibilance }
pattern phrase acid { 1 . 3 . 5 . 3 . }
scene main { lead = phrase }
song { main*4 }
```

The same chain can be inserted on recorded `audio` tracks. Add one EQ declaration
per band; track EQ frequency is 10–19845 Hz, with the same type, Q, and gain
controls as master EQ. Track compressors detect their own stereo input and do
not accept an external sidechain. Saved chain edits rebuild independent state;
scene and live effect automation on a serial track chain are currently rejected.
The legacy single-drive insert retains its live controls.

The stereo-linked de-esser uses a complementary first-order split: it attenuates
the high-frequency band while retaining the vocal body. `range` caps detector
gain reduction; it does not guarantee that every frequency is attenuated by
that amount. Set `mix = 0` for exact bypass.

| De-esser control | Unit | Range | Default |
| --- | --- | --- | --- |
| `frequency` | Hz | 2000–12000 | 6000 |
| `threshold` | dBFS (write `dB`) | −60–0 | −24 |
| `ratio` | unitless | 1–20 | 4 |
| `range` | dB | 0–24 | 9 |
| `attack` | ms | 0.1–20 | 1 |
| `release` | ms | 10–500 | 80 |
| `mix` | unitless | 0–1 | 1 |

Studio's **Add vocal chain** creates an 80 Hz highpass, a compressor at −18 dB
with ratio 3 and zero makeup, and the default de-esser in one undoable save. It
keeps existing inserts and leaves an existing EQ → compressor → de-esser trio
unchanged. Adjust the threshold to the recorded signal; these defaults are a
starting point, not automatic leveling. Finish an active take before saving
Mixer changes.

Native playback, export, and portable playback use the same saved track stages.
Core images require `TrackEffectsCapability` (bit 11). Studio automatically loads
`cicada-vocals.wasm` for those images; scores using modeled keyboard voices load
`cicada-keys.wasm`, which also includes track processing. The default portable
core rejects the track-processing requirement. Missing optional modules return
an error instead of silently playing without the effects.

Build the optional processing variant with `make build-vocals-wasm`. Its raw and
Brotli bytes are reported separately; the default core retains the 320 KiB raw
and 120 KiB Brotli gates. WAM2 export automatically packages the required variant
from beside the configured core file. `make build-wam2` prepares both the core
and vocal modules; modeled keys require `make build-keys-wasm`.
