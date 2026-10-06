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
| Hard/soft normalized first-difference energy, C4 | EP 2.00×; bells 3.83×; bass 113.21× |
| Block-independent PCM goldens | All three patches, blocks 1/64/128/256 |

Native CPU medians from three 300 ms runs at 48 kHz, with `GOMAXPROCS=1`:

| Patch | One voice, ns/frame | Eight voices, ns/frame | Eight voices, ns/voice | 128-frame block, ms |
| --- | ---: | ---: | ---: | ---: |
| `fm_ep` | 425.9 | 1,730 | 216.3 | 0.221 |
| `bell_keys` | 379.3 | 2,532 | 316.5 | 0.324 |
| `fm_bass` | 272.8 | 1,525 | 190.6 | 0.195 |

CPU measurements used an x86-64 processor during concurrent builds and tests.
The benchmark resets and strikes its fixed note slots every 4,800 frames to
keep decaying patches audible without accumulating voices. Figures include
the shared decimator and note preparation calls, with zero allocations. They
include no browser p99 claim. Score registration and the optional keyboard
module are integrated in a separate capability.

The original bell and bass settings were revised after spectral analysis of a
[six-operator hardware recording](https://commons.wikimedia.org/wiki/File:Korg_Volca_fm_2_-_Demo_using_Yamaha_DX7_presets.flac),
licensed [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/).
The recording stays outside the repository. Operator settings remain original;
the comparison uses recorded audio without importing hardware patch data.

Models were rendered at 192 kHz and reduced to mono 48 kHz with a polyphase
filter. Spectra remove DC and use a Hann window, squared FFT magnitude, and
power from 40 Hz to 12 kHz. Model velocity is 80. Low bells use C3; the upper
reference components suggest a C6/E6/G6 chord, which the model plays together.
That voicing is inferred, and source MIDI velocities and trigger offsets are
unknown. Model windows are 50–350 ms for bells and 15–155 ms for bass.

| Sound | Recorded centroid / 95% rolloff, Hz | Before, Hz | Revised, Hz |
| --- | ---: | ---: | ---: |
| C3 bell | 367 / 787 | 165 / 397 | 358 / 840 |
| Upper bell chord | 4,314 / 9,397 | 1,508 / 3,133 | 3,415 / 8,443 |
| Bass, 65/131 Hz components | 137 / 257 | 119 / 136 | 153 / 264 |

The revised low bell's observed post-peak T20 is 845 ms, compared with 790 ms
in the selected reference interval; its original carrier lasted beyond the
two-second held-note measurement. This is an amplitude-decay comparison,
with no inferred reference key-off. Five-millisecond RMS blocks set its time
resolution. The upper model chord reaches 5,061 Hz centroid and 10,037 Hz
rolloff at velocity 127.

Controlled model centroids at velocities 32 / 80 / 127 are **263 / 358 / 546 Hz**
for C3 bells, **2,541 / 3,415 / 5,061 Hz** for the upper chord and
**117 / 153 / 717 Hz** for bass. The recorded repeated bass onset centroids
have 10th / median / 90th percentiles of **132 / 150 / 770 Hz**, but those
strikes do not supply known MIDI velocities. This broader model response is
an improvement in expressive range, not a measured hardware velocity fit.
The remaining upper-bell spectral difference, recording reverb and uncertain
source voicing limit comparison. Matched-loudness listening remains required
before claiming commercial sound quality.

The package is not imported by the core kernel in this capability, so its
core raw and Brotli size deltas are zero. No budget changes are required.

Reproduce checks with `GOWORK=off GOMAXPROCS=2`:

```sh
go test -p 2 ./kernel/voice/fm -count=1 -v -bench BenchmarkStereo
go test -p 2 -tags fm_wasm ./kernel/voice/fm -run TestFMNativeWASMDeterminism -count=1 -v
go vet -p 2 ./kernel/voice/fm
```
