# Six-operator FM keys

`fm` provides three original keyboard patches with independent operator
envelopes, velocity-sensitive modulation, feedback, stereo carrier placement
and eight fixed voices. It ships no recorded audio or factory patch data.

```go
params, err := fm.Patch("fm_ep") // also bell_keys and fm_bass
if err != nil { return err }
keys, err := fm.New(48000, params)
if err != nil { return err }
_ = keys.NoteOn(60, 100)
left, right := keys.NextStereo()
_, _ = left, right
keys.NoteOff(60)
```

The network evaluates operator 5 first and operator 0 last.
`Routing[destination][source]` accepts a source only when its index is greater
than the destination. `Output` selects carriers. Operator level is linear
amplitude at an output and radians at a modulation input. Each operator can
also feed its own two-sample averaged feedback path.

| Control | Range | Meaning |
| --- | --- | --- |
| Ratio | 0.125–32 | Multiple of the played note frequency |
| Detune | −50–50 cents | Pitch offset per operator |
| Level | 0–8 | Peak carrier amplitude or modulation depth |
| Velocity | 0–1 | Blend between fixed level and squared MIDI velocity |
| KeyTracking | 0–1 | Shorter decay and release toward the treble |
| Pan | −1–1 | Carrier position; scaled by StereoSpread |
| Feedback | 0–2 | Self-modulation depth |
| Attack | 0–5 s | Linear rise to peak |
| Decay | 0–30 s | Time for a 60 dB fall toward sustain |
| Sustain | 0–1 | Held level as a fraction of peak |
| Release | 0.005–30 s | Time for a 60 dB fall after key release |
| Gain | 0.001–2 | Whole-instrument output level |
| StereoSpread | 0–1 | Carrier and keyboard pan spread |

Notes are MIDI 21–108. Velocity zero releases the note. `SetSustain` accepts
0–1; values of at least 0.5 hold released keys. `AllNotesOff` releases keys and
respects the pedal. `Reset` clears keys, pedal, filter history and steal tails.
One audio owner must serialize these methods.
`SetVoiceLimit(1..8)` selects the hard polyphony limit before playback; the
default is eight slots, and `Reset` preserves the configured limit.

The voice runs at twice the requested rate, then uses a shared 128-tap FIR
decimator. It supports 44.1, 48, 96 and 192 kHz. The decimator delays output by
31.75 frames. High-ratio operators and modulation depth taper near the output
Nyquist frequency. FM has infinitely many mathematical sidebands; the guard
and filter suppress aliasing rather than claiming every sideband is absent.

Measurements from the native tests and TinyGo fixture on 2026-10-06:

| Check | Result |
| --- | --- |
| Native/TinyGo PCM parity | 1,572,864 float32 samples, bit exact; 3 patches, 4 rates, 3 block sizes, voice limits 1/2/4/8 |
| Note, pedal, reset and render allocations | Zero native and WASM allocations |
| Decimator stop band at/above output Nyquist | Maximum −86.57 dB |
| Clean high carrier at 16,744 Hz | Total fitted-sine residual −129.06 dB |
| Hard/soft normalized first-difference energy | EP 2.00×; bells 6.63×; bass 1.76× |
| One voice, native 48 kHz | 451.6 ns/output frame, including shared decimator |
| Eight voices, native 48 kHz | 2,525 ns/output frame; 315.6 ns/voice amortized |
| Eight voices, native 128-frame block | 0.323 ms |

CPU measurements used an x86-64 processor during concurrent builds and tests
and include no browser p99 claim.
These are numerical qualification results, not listening acceptance or a fit
against recordings of a particular instrument. Score registration and the
optional keyboard module are integrated in a separate capability.

The package is not imported by the core kernel in this capability, so its
core raw and Brotli size deltas are zero. No budget changes are required.

Reproduce checks with `GOWORK=off GOMAXPROCS=2`:

```sh
go test -p 2 ./kernel/voice/fm -count=1 -v -bench BenchmarkStereo
go test -p 2 -tags fm_wasm ./kernel/voice/fm -run TestFMNativeWASMDeterminism -count=1 -v
go vet -p 2 ./kernel/voice/fm
```
