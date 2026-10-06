# String machine

`strings` provides eight divide-down string voices and a stereo triple-chorus ensemble. Its original patch is `string_machine`.

```go
params, err := strings.Patch("string_machine")
if err != nil { return err }
keys, err := strings.New(48000, params)
if err != nil { return err }
if err := keys.NoteOn(60, 100); err != nil { return err }
left, right := keys.NextStereo()
_, _ = left, right
keys.NoteOff(60)
```

`New` accepts 44100, 48000, 96000, and 192000 Hz. Notes cover MIDI 21–108; velocities cover 0–127, with zero acting as note-off. `SetVoiceLimit` sets 1–8 voices, defaults to eight, and clears voices outside a reduced limit; `Reset` preserves that limit. `SetSustain` takes 0–1, with 0.5 as the pedal threshold. `AllNotesOff` releases every voice and clears the pedal; `Reset` clears envelopes, dividers, filters, and chorus tails. `Active` reports live envelopes; the shared filters and chorus can retain a short tail after it becomes false.

Each pitch class has a shared integer top-octave generator. Lower octaves read divided phases from that generator; triggering a key does not reset it. This preserves octave coherence and avoids independent oscillator beating. PolyBLEP saw waves supply 16-foot, 8-foot, and 4-foot registers. A high-pass and low-pass shape the mixed tone before three delay taps, each with independent slow and fast LFOs. The ensemble uses fractional delay interpolation and a bandwidth limit inspired by bucket-brigade delays. It does not simulate physical clock pulses or component noise. The 4-foot register drops out if its frequency would approach Nyquist.

| Parameter | Range | Default | Meaning |
| --- | --- | --- | --- |
| `Octave16` | 0–1 | 0.18 | Octave-below register level. |
| `Octave8` | 0–1 | 0.7 | Fundamental register level. |
| `Octave4` | 0–1 | 0.3 | Octave-above register level. |
| `Attack` | 0.001–10 s | 0.12 | Linear attack duration. |
| `Release` | 0.01–10 s | 0.85 | Time for released amplitude to fall by 40 dB. |
| `Cutoff` | 200–16000 Hz | 4700 | Shared tone low-pass; a fixed 65 Hz high-pass removes DC and excess low-end energy. |
| `Velocity` | 0–1 | 0.35 | Velocity influence on amplitude. |
| `Ensemble` | 0–1 | 0.78 | Triple-chorus amount. |
| `EnsembleRate` | 0.1–3 | 1 | Multiplier for the independent slow and fast LFO rates. |
| `LevelDB` | −60–6 dB | −10 | Output gain, with fixed polyphony headroom. |

At least one register level must be nonzero. Register levels are normalized together. All parameters are finite and prepared in `New`; they cannot change during playback. Public fields use `float64`; render state and coefficients use explicit `float32` rounding. Note events, pedal changes, reset, and rendering allocate no memory. Stealing prefers released voices and then the oldest voice, with a 64-frame transition.

Focused verification:

```sh
GOWORK=off go test ./kernel/voice/strings
GOWORK=off go test -tags keys_wasm ./kernel/voice/strings -run TestNativeWASMParity
GOWORK=off go test ./kernel/voice/strings -run '^$' -bench BenchmarkStringMachine
```

Tests cover validation, four-rate stability, releases, stereo ensemble output, velocity, pedal behavior, stealing, reset, circular-buffer boundaries, zero allocations, fixed PCM hashes, and coherent octave division. The pitch check measures less than 0.001 cent of error at 48000 Hz. The optional parity test compares all supported rates and block sizes 64, 128, and 256 and checks WASM allocations. These are synthetic DSP checks; no recorded instrument comparison is claimed here.

Native measurements on an Intel Core Ultra 9 285, 2026-10-06, with 48000 Hz output, 128-frame blocks, `GOMAXPROCS=2`, and one-second sustained benchmarks:

| Voices | ns/frame total | ns/frame/voice | One CPU core at 48000 Hz |
| --- | ---: | ---: | ---: |
| 1 | 51.38 | 51.38 | 0.25% |
| 8 | 107.48 | 13.44 | 0.52% |

Both cases measured 0 B/op and 0 allocs/op. These CPU estimates include the shared dividers, filters, and ensemble, and use elapsed native render time on a loaded machine; browser runtime cost is not measured. The TinyGo parity fixture was 18483 raw bytes and matched 393216 stereo samples bit for bit, including voice-limit changes, with zero WASM trigger/render/reset allocations. The package is not imported by the default core kernel in this capability change, so its core-kernel size delta is zero. The fixture size includes construction and test exports and is not an incremental kernel-size measurement.
