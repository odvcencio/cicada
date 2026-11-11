# Opt-in mix chain

The new `kernel/fx/pro` package prepares a stereo chain in this order: four-band parametric EQ, linked compression, transient shaping, stereo width with optional bass centering, early reflections plus an eight-line FDN tail, and a true-peak lookahead limiter. Controls are fixed when the chain is prepared; `Process` and `Reset` allocate no memory. Zero parameters are an exact zero-latency bypass.

Native hosts assign a prepared chain to `engine.Config.MasterProcessor` before `engine.New`. The engine runs it after master gain and before the existing master safety limiter. `MasterProcessorLatencyFrames` reports its additional delay. Project image encoding rejects a prepared processor, because it contains host-owned state that cannot be silently omitted.

Browser hosts can load the optional `cicada-mix.wasm` companion module separately from the core. `host/promix/processor.mjs` prepares it before the AudioWorklet starts and copies stereo blocks of up to 128 frames without making new typed-array views during processing. Preparation, compilation, fetches and controls belong outside the callback. The companion adds download bytes and memory; its size is reported separately and does not change the default core's size gate of 322 KiB raw and 120 KiB Brotli. The core retains these size gates; saved track effects use a separately negotiated project-image capability.

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
track bass fm_bass {}
pattern pulse { 1 . 3 . 5 . 3 . }
scene main { bass = pulse }
song { main*16 }
fx tone eq { type = highpass frequency = 25Hz q = 0.707 }
fx glue comp { threshold = -18dB ratio = 2 makeup = 1dB }
fx punch transient { attack = 2dB sustain = -1dB }
fx stereo width { amount = 1.08 bass_mono = 100Hz }
fx ceiling limiter { ceiling = -1dBTP lookahead = 3ms release = 100ms }
master { insert = tone -> glue -> punch -> stereo -> ceiling }
export broadcast { loudness = -23LUFS true_peak = -1dBTP normalize = off }
```

Run `cicada render score.cicada --export broadcast --bars 16 -o broadcast.wav`.
The export target uses the existing iterative BS.1770 loudness renderer and
measures the delivered WAV. Live playback does not apply programme normalization.
An unreachable target returns an error instead of claiming success. This
one-track bass cannot reach a −14 LUFS streaming target, because the renderer
adds at most 12 dB of input gain, so the example asks for the −23 LUFS
broadcast level.

### Export loudness targets

A loudness target (`--loudness`, or an `export` block with `loudness`) renders
the score in up to six passes. Each pass measures the delivered WAV and adjusts
one static gain. Where that gain goes depends on the master chain.

| Master chain | Where the gain goes | What holds the true-peak ceiling |
| --- | --- | --- |
| No limiter | Direct renderer: after the master chain. Scheduled renderer (arrangements, audio clips, serial track effects, sample packs): at the master input, as in earlier releases, because the engine applies its master gain before the chain. Output is unchanged from earlier releases. | The gain stops at the ceiling. It keeps crest factor, so an ambitious target can miss and returns a shortfall error. |
| Contains a `limiter` | At the master input: after the music and SFX buses sum and the master `level`, before the chain. | Your limiter. The extra level reaches it, so it reduces peaks and a punchy mix can get louder than its natural crest allows. |

Without a limiter, a target that the render misses at the true-peak ceiling
ends its error with the fix: add a limiter at the export's true-peak ceiling
as the last master insert, such as `fx ceiling limiter { ceiling = -1dBTP }`
with `master { insert = ceiling }`. `cicada render --json` reports it as the
code `loudness_shortfall_at_true_peak_ceiling` with a `remedy` that holds the
exact score text for that score and ceiling, and the `addeffect` intent that
makes the same change with `cicada apply`
([agent formats](../manual/agents.md#render---json)).

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

Only the first pass synthesizes the mix, on both renderers and with or without
a master chain. It stores the signal that enters the master stage (the summed
buses, before the master `level` and the export gain) in a temporary file next
to the output, and later passes play that signal back through the master
gain, the chain and the safety limiter with the new gain. A pass whose gain
differs from an earlier pass's by less than the renderer's gain can show is not
rendered at all: it reuses that pass's audio and measurement. The result
matches rendering every pass in full, byte for byte. The render summary counts
the full syntheses, the cached replays and the repeated passes, and lists each
pass's gain, loudness and true peak. `CICADA_RENDER_NO_REUSE=1` turns the reuse
off, so every pass synthesizes the song (see the [CLI reference](../cli.md)).

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
Supported kinds are `drive`, `eq`, `comp`, `deesser`, `ampcab`, and `chorus`. Each track owns separate
filter and detector state. The chain runs before the fader and both send taps,
so vocal reverb and delay receive the processed voice. All processing and resets
allocate no memory. The engine aligns tracks to the longest drive-insert delay;
EQ, compression, and de-essing add no latency.

```cicada
cicada 2
fx cleanup eq { type = highpass frequency = 80Hz q = 0.707 }
fx leveler comp { threshold = -18dB ratio = 3 makeup = 0dB }
fx sibilance deesser { frequency = 6000Hz threshold = -24dB ratio = 4 range = 9dB }
track lead piano { insert = cleanup -> leveler -> sibilance }
pattern phrase { 1 . 3 . 5 . 3 . }
scene main { lead = phrase }
song { main*4 }
```

The same chain can be inserted on recorded `audio` tracks. Add one EQ declaration
per band; track EQ frequency is 10–19845 Hz, with the same type, Q, and gain
controls as master EQ. Track compressors detect their own stereo input and do
not accept an external sidechain. Saved chain edits rebuild independent state;
scene and live effect automation on a serial track chain are currently rejected.
The legacy single-drive insert retains its live controls. A track with exactly
one chorus also exposes `chorus_rate`, `chorus_depth`, `chorus_spread`, and
`chorus_mix` for scene settings, automation, and modulation routes.

The stereo chorus uses one to three voices. `rate` is 0.05–5 Hz (default
0.6 Hz), `depth` is 0–8 ms (default 3 ms), and `spread` and `mix` are 0–1
(defaults 1 and 0.5). `voices` is an integer from 1 to 3 (default 3). The
four continuous controls use a 10 ms smoother. Settled `mix = 0` passes the
dry signal exactly while maintaining the delay history. `mix` crossfades the
dry signal with the average of the delayed voices (16 to 19 ms behind it), so
at `mix = 0.5` a part plays about 4 to 6 dB quieter in RMS: measured on the
grand-piano pack, an A major chord lost 4.1 dB and a low F#1 and F#2 octave
about 6 dB, its 92 Hz fundamental 12.6 dB, because the delays cancel part of
it. Raise the track `level` to compensate.

```cicada
cicada 2
fx wide chorus { voices = 3 rate = 0.6Hz depth = 3ms spread = 1 mix = 0.5 }
track lead acid { insert = wide }
pattern phrase { 1 . 3 . }
lfo drift { wave = triangle rate = 2bars }
mod drift -> lead.chorus_mix depth = 0.25
scene main { lead = phrase }
song { main*4 }
```

Browser and WAM playback select the optional modular kernel for chorus. Portable
chorus projects have a conservative 8 MiB effect-working-storage limit;
externally prepared master processors cannot yet be included in that budget.

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
Brotli bytes are reported separately; the default core retains the 322 KiB raw
and 120 KiB Brotli gates. WAM2 export automatically packages the required variant
from beside the configured core file. `make build-wam2` prepares both the core
and vocal modules; modeled keys require `make build-keys-wasm`.

## Tape echo

`tape` is a delay return that behaves like a tape echo. Declare it where you
would declare a `delay`, send tracks to it the same way, and a score has one
delay return: either `delay` or `tape`.

```cicada
cicada 2
tempo 74
fx echo tape { time = 3/16 feedback = 0.6 wow = 0.5 flutter = 0.2 saturation = 0.4 damp = 3200Hz }
track rim drums { send echo = 0.8 pre }
pattern hit drums { rs: x... .... .... .... }
automate echo.time { @1.1.1 608ms @3.1.1 300ms smooth }
scene main { rim = hit }
song { main*4 }
```

In this score the repeats start at 608 ms and, over two bars, slow their
spacing down to 300 ms. The pitch of the repeats rises while the time shortens
and falls while it lengthens, as it does when you turn the delay knob on a tape
machine.

| Control | Unit | Range | Default |
| --- | --- | --- | --- |
| `time` | note division or ms | 1/32 up to 2 s, 1 ms to 2000 ms | 1/8 |
| `feedback` | unitless | 0–0.95 | 0.4 |
| `wow` | unitless | 0–1 | 0.4 |
| `flutter` | unitless | 0–1 | 0.2 |
| `saturation` | unitless | 0–1 | 0.3 |
| `damp` | Hz | 1000–16000 | 5000 |

All six controls take scene settings (`echo.feedback = 0.3`) and `automate`
lanes (`echo.time`, `echo.wow`, `echo.flutter`, `echo.feedback`,
`echo.saturation`, `echo.damp`). Each moves through a 5 ms smoother, except
`time`, which glides as described below.

**What it models.**

- One read head over a 2-second stereo buffer. A change of `time`, from
  automation, a scene, or a tempo change on a synced time, moves the head at up
  to half a sample per sample, so the repeats play between 0.5x and 1.5x speed
  during the move. Nothing crossfades. A jump of 300 ms takes 0.6 s.
- Wow: slow drift of the head (two sines near 0.37 and 1.3 Hz), up to 3 ms of
  excursion at `wow = 1`. Flutter: fast ripple (near 7 and 11 Hz), up to 0.25 ms
  at `flutter = 1`.
- A one-pole `damp` filter and a `tanh` limiter inside the feedback loop.
  `saturation` drives that limiter harder and divides the drive back out, so
  quiet repeats keep the `feedback` gain and loud repeats compress.
- With `wow`, `flutter` and `saturation` at 0, `tape` matches a stereo `delay`
  that has the same time, feedback and `damp`, no ping-pong, width 1 and mix 1.
  The match is byte for byte when the time is a whole number of frames. For
  other times it differs by float rounding, under -120 dB.

**What it does not model.** There is no tape-head frequency response, no
hiss, no bias or print-through, and no motor inertia beyond the fixed glide
rate. There is no ping-pong, width or mix control: the return is always
wet-only and stereo-preserving. It cannot be a track insert or a master stage.
Modulation routes to its controls are not supported yet. Portable browser kernel
images refuse a score that declares `tape`, with a clear error; native playback
and offline `render` and `stems` support it.

## Same bits on every machine

**The rule.** A score renders to the same bits on amd64 (any CPU and any
`GOAMD64` level), arm64 and WebAssembly, from voice to exported file. Two
things in Go can break it. Fusion is ruled out everywhere on that path; the
math library is not yet, so part of it matches in the tests without being
guaranteed.

- **Fused multiply-add.** Go may compute `x*y + z` with one rounding instead
  of two on arm64 and with `GOAMD64=v3`. What is guaranteed: the compiler
  reports no product it would fuse in `kernel/`, `render/` and the packages
  `render` imports, on arm64 and with `GOAMD64=v3` (CI builds them with
  `-d=fmahash=vy` and fails on any report). A product is rounded with an
  explicit `float32(...)` or `float64(...)`, for example `a + float64(b*c)`.
  Go may also fuse across statements, so a conversion of a plain value is
  needed where that value is itself a product: a variable set from a product
  in an earlier statement (`s1 := state*inv` then `float64(g*u) + float64(s1)`),
  a parameter of a function the compiler inlines into a caller that passes a
  product, and `v` in `2*v`, which the compiler rewrites as `v + v`. Elsewhere
  such a conversion prevents nothing and is left out; the compiler's report
  decides which are needed.
  Not yet covered: `cmd/cicada` (live monitor mixing in
  `studio_audio_devices.go`) and the `host/` packages that `render` does not
  import; a later pull request in this series extends the check to every
  package that produces audio a user hears, records or exports.
- **Math library functions.** `math.Exp`, `math.Log`, `math.Pow`, `math.Sin`
  and the others can differ in the last bit by target: `math.Exp` even uses an
  FMA instruction only on amd64 CPUs that have one. The master EQ design, the
  pro limiter, transient shaper and normalizer, the convolution FFT, the
  loudness meter and the export gain use `kernel/dsp/detmath` instead, whose
  functions return the same bits everywhere and are within 1 ULP of the exact
  result. Still on the math library: the master compressor and room (they
  build on the track compressor and reverb), the track effects in `kernel/fx`,
  the amp and cabinet, the mix in `kernel/mix`, and the voices.

The follow-up removes the rest: no architecture-dependent `math` call
between voice and exported file, and a digest per processor and voice on all
three targets.

**How to check it.**

```
go test -count=1 ./kernel/dsp/detmath ./kernel/fx/... ./kernel/mix ./kernel/loudness
```

pins each processor's output as a SHA-256 digest; CI runs the same tests on
arm64 and with `GOAMD64=v3`. To list fusable expressions, build for arm64 with
the compiler's FMA report:

```
GOARCH=arm64 go build -gcflags='m31labs.dev/cicada/...=-d=fmahash=vy' ./kernel/... ./render/...
```

Each `fmahash triggered FILE:LINE` line is a product that arm64 fuses; CI fails
on any. `TestRenderPathAvoidsMathLibrary` in
`kernel/dsp/detmath` fails when the master chain or the export code calls a
math function that `detmath` replaces.

The exported file itself is compared too. `TestExampleWAVParity` in
`cmd/cicada` runs `cicada render` on example scores four ways (plain,
`--normalize`, -14 LUFS / -1 dBTP, -9 LUFS / -1.5 dBTP) and hashes each WAV:

```
CICADA_WAV_PARITY_OUT=$PWD/build/wav-hashes.txt go test ./cmd/cicada -run '^TestExampleWAVParity$' -count=1 -v
CICADA_WAV_PARITY_REFERENCE=$PWD/build/wav-hashes.txt go test ./cmd/cicada -run '^TestExampleWAVParity$' -count=1 -v
```

The first command writes the hashes (CI runs it on amd64), the second compares
another machine's renders with them (CI runs it on arm64). Pull requests
render a subset; the nightly run sets `CICADA_WAV_PARITY_ALL=1` and renders
every score under `examples/`. A render known to differ is listed, with the
date and the cause, in `knownWAVDifferences`; the list can only shrink.
