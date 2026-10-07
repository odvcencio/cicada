# Electric piano models

Tine EP and Reed EP are original modal instruments with velocity-dependent
pickup coloration. The six patches are `tine_ep`, `tine_bell`, `tine_bark`,
`tine_tremolo`, `reed_ep` and `reed_tremolo`. The package documentation describes
the physical controls and ranges. Playback uses a fixed 1–8 voice limit.

## Recorded reference measurements

Reference audio was used for analysis only. No recording, factory patch or
reference asset is included in this package. The source sets are a recorded
[tine instrument](https://github.com/sfzinstruments/jlearman.jRhodes3d)
(CC BY-NC 4.0 sample license) and a recorded
[reed instrument](https://github.com/sfzinstruments/GregSullivan.E-Pianos)
(CC BY 3.0). The tine recording has documented EQ; these are comparisons to
those recordings, not calibrated measurements of an unprocessed instrument.

Measurements use mono PCM at 48 kHz with four internal substeps. Centroid
and 1.5–4 kHz energy use a Hann-windowed first 500 ms power spectrum,
normalized to power from 40 Hz through 12 kHz. T20 is the first crossing
20 dB below the maximum 5 ms RMS in the first 500 ms, searched after that
window. These are whole-note decay times, distinct from bending-mode T20.
Tine reference comparisons use D4; Reed uses D-flat4. Reference layer strike
velocities are unknown, so rows compare ordinal layers, not measured force.

| Tine layer, soft to hard | Reference centroid, Hz | Model centroid, Hz | Reference T20, s | Model T20, s |
| --- | ---: | ---: | ---: | ---: |
| 1 / model velocity 25 | 296.5 | 294.4 | 6.290 | 5.760 |
| 2 / model velocity 51 | 303.5 | 298.6 | 5.895 | 5.260 |
| 3 / model velocity 76 | 341.3 | 310.8 | 5.510 | 4.715 |
| 4 / model velocity 102 | 487.4 | 330.4 | 4.580 | 3.850 |
| 5 / model velocity 127 | 648.5 | 345.3 | 3.595 | 3.305 |

The slower tonebar beat and weaker, shorter bending modes leave the pickup
harmonics as the main source of strike brightness. The default endward
pickup adds second-harmonic body. Centroid therefore changes less with
velocity than in the recorded tine layers; this calibration does not claim
a match to those recordings.

| Reed layer | Reference centroid, Hz | Model velocity / centroid, Hz |
| --- | ---: | ---: |
| pp | 287.2 | 25 / 277.4 |
| mp | 306.9 | 51 / 278.6 |
| f | 451.1 | 102 / 295.0 |
| ff | 637.7 | 127 / 318.7 |

Reed clips are trimmed too early to establish comparable recorded T20.
Release noise is designed rather than fitted to an isolated release recording.

## Partial balance across the keyboard

At velocity 100, partial levels use exact-frequency Fourier measurements
with identical 10–70 ms and 160–220 ms Hann windows. The bending mode is
6.267 times the fundamental; it is inharmonic. Second-harmonic levels are
relative to the fundamental in the first window. Drop is the absolute loss
of bending-mode amplitude over 150 ms, including preset tremolo. Rising
tremolo gain can mask some modal decay in this measure.

| Patch | Note | Bend, dB re f0 | Drop in 150 ms, dB | Second, dB re f0 | 1.5–4 kHz power, % |
| --- | --- | ---: | ---: | ---: | ---: |
| `tine_ep` | A3 | -17.7 | 14.4 | -8.2 | 0.009 |
| `tine_ep` | D4 | -18.1 | 15.4 | -8.2 | 0.055 |
| `tine_ep` | E4 | -18.2 | 15.8 | -8.2 | 0.052 |
| `tine_ep` | C#5 | -19.1 | 17.9 | -8.3 | 0.114 |
| `tine_bark` | A3 | -19.0 | 13.8 | -8.7 | 0.012 |
| `tine_bark` | D4 | -19.4 | 14.8 | -8.7 | 0.051 |
| `tine_bark` | E4 | -19.5 | 15.1 | -8.7 | 0.092 |
| `tine_bark` | C#5 | -20.4 | 17.0 | -8.8 | 0.512 |
| `tine_tremolo` | A3 | -17.7 | 9.6 | -8.2 | 0.011 |
| `tine_tremolo` | D4 | -18.0 | 10.7 | -8.2 | 0.063 |
| `tine_tremolo` | E4 | -18.2 | 11.1 | -8.2 | 0.059 |
| `tine_tremolo` | C#5 | -19.1 | 13.2 | -8.3 | 0.118 |
| `tine_bell` | A3 | -16.8 | 14.4 | -16.7 | 0.003 |
| `tine_bell` | D4 | -17.1 | 15.4 | -16.8 | 0.063 |
| `tine_bell` | E4 | -17.3 | 15.8 | -16.8 | 0.057 |
| `tine_bell` | C#5 | -18.2 | 17.9 | -16.8 | 0.081 |
| `reed_ep` | A3 | -25.2 | 17.1 | -9.6 | 0.002 |
| `reed_ep` | D4 | -25.5 | 18.3 | -9.6 | 0.012 |
| `reed_ep` | E4 | -25.6 | 18.9 | -9.7 | 0.014 |
| `reed_ep` | C#5 | -26.5 | 21.4 | -9.8 | 0.219 |
| `reed_tremolo` | A3 | -26.7 | 12.2 | -10.0 | 0.002 |
| `reed_tremolo` | D4 | -27.0 | 13.4 | -10.0 | 0.010 |
| `reed_tremolo` | E4 | -27.2 | 13.9 | -10.1 | 0.015 |
| `reed_tremolo` | C#5 | -28.0 | 16.5 | -10.2 | 0.284 |

The 1.5–4 kHz band includes the D4 bending mode at about 1.84 kHz, as
well as E4 and C#5. A3's mode sits below this band, so its per-partial
measurement is also needed. A threshold above 2 kHz would miss the D4 mode.
`TestTineSpectralBalance` checks A3, D4, E4 and C#5 at velocities 64, 100
and 120. It removes known tremolo gain for modal decay and fits out
fundamental decay for tonebar ripple. D4 bending-mode T20 stays below
0.3 s. The note-scaled cap and pickup saturation give minimum calibrated
150 ms drops of 13 dB at A3 and 14.4 dB for `tine_bark` at D4/E4; other
tested notes retain a 15 dB minimum. Second-harmonic bark grows by at least
3 dB from velocity 64 to 120. Reed second-harmonic preservation is checked
within 1 dB at velocity 100. Third-harmonic levels depend on the window and
are excluded from the gate.

These objective checks do not establish listening equivalence to a commercial
instrument. Listen at matched loudness to assess warmth, bark and ping.

## Cost and determinism

At 48 kHz, 128-frame blocks, eight active voices: Tine EP measured
61.0 ns/voice/frame (62.4 µs/block); Reed EP 73.3 ns/voice/frame
(75.0 µs/block). Single-voice figures were 71.5 and 134.7 ns/frame respectively.
These are native measurements on a loaded machine, not browser CPU promises.
Note events, pedal changes, reset and rendering allocate zero times.

`go test -tags ep_wasm ./kernel/voice/ep` compares 2,359,296 float32 samples
bit-for-bit between native Go and TinyGo at 44.1, 48, 96 and 192 kHz with
64, 128 and 256-frame blocks, and checks WASM allocation telemetry. Golden
tests pin attack, retrigger and release PCM. The engine is unlinked from the
core WASM build; its core size delta is zero.
