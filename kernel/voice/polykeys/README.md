# Analog poly keys

`polykeys` provides eight voices with saw, pulse/PWM and sub oscillators, oscillator hard sync, a four-pole ladder or two-pole state-variable low-pass filter, slow pitch drift, and a stereo ensemble chorus. The original patches are `poly_keys`, `brass_stab`, `soft_pad`, and `sync_lead`.

```go
params, err := polykeys.Patch("soft_pad")
if err != nil { return err }
keys, err := polykeys.New(48000, params)
if err != nil { return err }
if err := keys.NoteOn(60, 100); err != nil { return err }
left, right := keys.NextStereo()
_, _ = left, right
keys.NoteOff(60)
```

`New` accepts 44100, 48000, 96000, and 192000 Hz. Notes cover MIDI 21–108; velocities cover 0–127, with zero acting as note-off. `SetVoiceLimit` sets 1–8 voices, defaults to eight, and clears voices outside a reduced limit; `Reset` preserves that limit. `SetSustain` takes 0–1, with 0.5 as the pedal threshold. `AllNotesOff` releases every voice and clears the pedal; `Reset` immediately clears notes, filters, modulation phases, and delay tails. `Active` reports live voice envelopes; short chorus tails can remain after it becomes false.

Parameters are prepared in `New` and cannot change during playback. The public fields use `float64`; oscillator, filter, envelope and delay state use explicit `float32` rounding. Note events, pedal changes, reset, and `NextStereo` allocate no memory. Voice stealing prefers released voices and then the oldest voice. Retriggers and steals use a 64-frame transition.

| Parameter | Range | Default | Meaning |
| --- | --- | --- | --- |
| `Saw`, `Pulse` | 0–1 each | 0.7, 0.3 | Oscillator mix; at least one must be nonzero. |
| `PulseWidth` | 0.1–0.9 | 0.45 | Pulse duty cycle. |
| `PWM` | 0–0.35 | 0.12 | Pulse-width modulation depth; the resulting width stays within 0.08–0.92. |
| `PWMRate` | 0.01–10 Hz | 0.41 | Shared pulse-width LFO rate. |
| `Sub` | 0–1 | 0.16 | Half-frequency square wave level. |
| `Detune` | 0–30 cents | 7 | Second oscillator offset. |
| `Drift` | 0–10 cents | 2 | Seeded per-voice offset and slow independent pitch drift. |
| `Sync` | 1–8 | 1 | Slave/master ratio; values above one enable hard sync. |
| `Filter` | `Ladder`, `StateVariable` | `Ladder` | Four-pole or two-pole low-pass. |
| `Cutoff` | 20–18000 Hz | 2400 | Base cutoff; the complete sweep clamps to the smaller of 18000 Hz and 20% of the host rate. |
| `Resonance` | 0–0.95 | 0.18 | Feedback amount; filters do not intentionally self-oscillate. |
| `KeyTrack` | 0–1 | 0.45 | Cutoff tracking, centered on MIDI 60. |
| `FilterEnv` | 0–6 octaves | 2.4 | Velocity-sensitive cutoff sweep from the amplitude envelope. |
| `Drive` | 0–1 | 0.12 | Filter-input saturation. |
| `Attack` | 0.001–10 s | 0.008 | Linear rise to full amplitude. |
| `Decay` | 0.01–10 s | 0.7 | Time for the difference from sustain to fall by 40 dB. |
| `Sustain` | 0–1 | 0.55 | Held-note envelope level; distinct from the sustain pedal. |
| `Release` | 0.01–10 s | 0.3 | Time for released amplitude to fall by 40 dB. |
| `Velocity` | 0–1 | 0.75 | MIDI velocity influence on amplitude and cutoff envelope. |
| `Chorus` | 0–1 | 0.28 | Three-tap stereo ensemble amount. |
| `LevelDB` | −60–6 dB | −9 | Output gain, with fixed polyphony headroom. |

Oscillators use PolyBLEP discontinuity correction at twice the host rate. Sync resets the slave at the fractional master wrap and corrects that discontinuity; the slave rate clamps to 45% of the internal rate on extreme high notes. Filters use prepared coefficient tables and trapezoidal integration. The chorus uses a fixed circular buffer, three asynchronous LFOs, fractional delay interpolation, and bandwidth limiting inspired by bucket-brigade delays. It does not simulate clock pulses or component noise. The output decimator averages the two internal phases; this is an economical model, rather than a circuit reconstruction.

Focused verification:

```sh
GOWORK=off go test ./kernel/voice/polykeys
GOWORK=off go test -tags keys_wasm ./kernel/voice/polykeys -run TestNativeWASMParity
GOWORK=off go test ./kernel/voice/polykeys -run '^$' -bench BenchmarkPolyKeys
```

Tests cover validation, four-rate stability, releases, pedal behavior, velocity response, stealing, reset, circular-buffer boundaries, zero allocations, fixed PCM hashes, fractional hard sync, filter response, and alias energy. The synthetic saw test measures 15.44 dB less aliased energy than an uncorrected saw at 8976.6 Hz. Low-level filter measurements place the ladder cutoff at −3.01 dB and the state-variable cutoff at −3.06 dB relative to 100 Hz with a nominal 1000 Hz cutoff. These are DSP checks; they do not establish a match to a recorded commercial instrument. The optional parity test compares all four patches at all supported rates and block sizes 64, 128, and 256, and checks WASM allocations.

Native measurements on an Intel Core Ultra 9 285, 2026-10-06, with 48000 Hz output, 128-frame blocks, `GOMAXPROCS=2`, and one-second sustained benchmarks:

| Patch | One voice, ns/frame | Eight voices, ns/frame total | Eight voices, ns/frame/voice | Eight voices, one CPU core |
| --- | ---: | ---: | ---: | ---: |
| `poly_keys` | 108.39 | 512.82 | 64.10 | 2.46% |
| `brass_stab` | 89.73 | 495.79 | 61.97 | 2.38% |
| `soft_pad` | 147.02 | 552.04 | 69.00 | 2.65% |
| `sync_lead` | 90.16 | 455.43 | 56.93 | 2.19% |

All cases measured 0 B/op and 0 allocs/op. CPU figures include the shared chorus and are estimates from elapsed native render time on a loaded machine; browser runtime cost is not measured by this benchmark. The TinyGo parity fixture was 24394 raw bytes and matched 1572864 stereo samples bit for bit, including voice-limit changes, with zero WASM trigger/render/reset allocations. The package is not imported by the default core kernel in this capability change, so its core-kernel size delta is zero. The fixture size includes construction and test exports and is not an incremental kernel-size measurement.
