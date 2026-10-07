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

Measurements use mono PCM, a Hann-windowed first 500 ms power spectrum, and
non-overlapping 5 ms RMS windows. T20 is the first crossing 20 dB below the
maximum RMS in the first 500 ms, searched after that window. Model notes are
D4 for Tine EP and D-flat4 for Reed EP, at 48 kHz with four internal substeps.
All measured attacks reach 90% of peak RMS within the first 15 ms; the 5 ms
window does not resolve contact details within a window.

| Tine layer, soft to hard | Reference centroid, Hz | Model centroid, Hz | Reference T20, s | Model T20, s |
| --- | ---: | ---: | ---: | ---: |
| 1 / model velocity 25 | 296.5 | 306.1 | 6.290 | 5.410 |
| 2 / 51 | 303.5 | 327.7 | 5.895 | 5.195 |
| 3 / 76 | 341.3 | 380.5 | 5.510 | 4.475 |
| 4 / 102 | 487.4 | 481.8 | 4.580 | 3.710 |
| 5 / 127 | 648.5 | 618.6 | 3.595 | 3.540 |

The model's endpoint centroids differ by +3.2% and −4.6%; endpoint T20 differs
by −14.0% and −1.5%. Reference layer strike velocities are not known, so rows
compare ordinal layers rather than equal measured force. Power above 2 kHz
rises from 0.044% to 1.333% in the model and from 0.001% to 0.285% in the
reference. The model therefore retains more upper transient energy. The
velocity-dependent fundamental damping and shorter bending-mode decay were
tuned after the first comparison exposed an overly short body and long bell.

| Reed layer | Reference centroid, Hz | Model velocity / centroid, Hz |
| --- | ---: | ---: |
| pp | 287.2 | 25 / 285.9 |
| mp | 306.9 | 51 / 299.9 |
| f | 451.1 | 102 / 422.1 |
| ff | 637.7 | 127 / 569.7 |

Reed centroid endpoint differences are −0.4% and −10.7%. The model's hard
strike has 3.477% power above 2 kHz versus 0.734% in the reference. Reed clips
are trimmed too early to establish comparable T20; modeled T20 is 1.03–1.10 s.
Release noise is designed rather than fitted to an isolated release recording.
These objective checks do not establish listening equivalence to a commercial
instrument. The listening examples are the acceptance material.

## Cost and determinism

At 48 kHz, 128-frame blocks, eight active voices: Tine EP measured
61.0 ns/voice/frame (62.4 µs/block); Reed EP 73.3 ns/voice/frame
(75.0 µs/block). Single-voice figures were 71.5 and 134.7 ns/frame respectively.
These are native measurements on a loaded machine, not browser CPU promises.
Note events, pedal changes, reset and rendering allocate zero times.

`go test -tags ep_wasm ./kernel/voice/ep` compares 786,432 float32 samples
bit-for-bit between native Go and TinyGo at 44.1, 48, 96 and 192 kHz with
64, 128 and 256-frame blocks, and checks WASM allocation telemetry. Golden
tests pin attack, retrigger and release PCM. The engine is unlinked from the
core WASM build; its core size delta is zero.
