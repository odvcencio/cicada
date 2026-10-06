# Tonewheel Organ

This package provides an original tonewheel organ model with eight note slots, nine drawbars, single-trigger percussion, scanner vibrato and chorus, preamp drive, and an independently accelerating rotary horn and drum.

```go
params, _ := organ.Patch("tonewheel_organ")
keys, err := organ.New(48000, params)
if err != nil { return err }
_ = keys.NoteOn(60, 100)
left, right := keys.NextStereo()
_, _ = left, right
keys.NoteOff(60)
```

`New` accepts 44100, 48000, 96000, or 192000 Hz and prepares all coefficients. Notes span MIDI 21..108. Note events, controls, `Reset`, and stereo rendering allocate nothing. One audio owner must serialize these calls. `Reset` retains the registration and clears generator phases, notes, pedals, and effect tails.

`SetVoiceLimit` accepts 1..8 allocated note slots and defaults to eight. Lowering the limit clears excluded voices while preserving physical key state; `Reset` preserves the limit. Hosts can assign fewer slots per track to keep their overall voice budget.

## Controls

| Parameter | Range and meaning | Default |
| --- | --- | --- |
| `Drawbars` | Nine integers 0..8, ordered 16', 5 1/3', 8', 4', 2 2/3', 2', 1 3/5', 1 1/3', 1'. Each nonzero stop changes attenuation by 3 dB. | `[7,4,8,6,1,2,0,1,1]` |
| `Percussion` | `PercussionOff`, `PercussionSecond`, or `PercussionThird`. A shared envelope rearms after every physical key is released. Percussion cancels the 1' drawbar. | Third |
| `PercussionFast` | Fast or slow envelope; measured amplitude T60 is 0.7254 or 3.4541 s. | true |
| `PercussionSoft` | Soft or normal transient gain. Normal also attenuates the sustained drawbars slightly. | true |
| `Scanner` | `ScannerOff`, `V1`..`V3`, or `C1`..`C3`. Vibrato selects the scanned signal; chorus mixes dry and scanned signals equally. | C2 |
| `KeyClick` | 0..1 contact click amount; velocity changes this transient rather than the sustained tonewheel gain. | 0.32 |
| `Leakage` | 0..1 key circuit leakage amount. | 0.12 |
| `Crosstalk` | 0..1 adjacent pickup circuit crosstalk. | 0.08 |
| `Drive` | 0..1 preamp saturation amount. | 0.18 |
| `RotaryMix` | 0..1 direct-to-rotary crossfade. | 0.8 |
| `MicSpread` | 0..1 stereo microphone separation; zero gives identical channels. | 0.7 |
| `RotaryFast` | Slow or fast horn and drum target speeds. | false |
| `Gain` | Linear output multiplier, 0..2. | 0.8 |

`SetDrawbars` applies a validated registration with a prepared 4 ms smoothing time constant. `SetRotaryFast` changes motor targets without resetting their speed. `SetSustain` accepts 0..1 and holds gates at 0.5 or above; this optional modern hold control does not alter percussion's physical key rearm circuit.

The original patches are `tonewheel_organ`, `organ_jazz`, `organ_full`, and `organ_soft`. Their registrations and parameters are design choices, with no factory patch data or recorded assets.

## Measurements and checks

Measurements below were taken on 2026-10-06. Focused native tests cover every supported key and rate, drawbar sharing and foldback, velocity independence of sustained tone, percussion rearming and decay, voice stealing, sustain, finite output, exact silence after release, reset reproducibility, all six scanner settings, and rotary spread and inertia.

At 48 kHz, native sustained-note benchmarks measured 336.2 ns per stereo frame with one active voice and 1143 ns with eight. These correspond to 1.61% and 5.49% of one CPU core; the eight-voice case averages 142.9 ns per voice per frame. Both report zero bytes and allocations per operation. Results are host measurements, not browser CPU guarantees.

The TinyGo fixture compares 393216 float32 samples at 44.1, 48, 96, and 192 kHz in blocks of 64, 128, and 256. Every sample matches native PCM bit for bit. A separate WASM memory check reports zero allocation bytes during repeated note events and rendering. Run it with `go test -tags=organ_wasm ./kernel/voice/organ -run TestOrganNativeWASMDeterminism`.

The native stereo golden is SHA-256 `e4b010173396f3eaaf2f1cfed1caa8a3a33589c7779e768344a366c9ba33b42b`. A single 8' drawbar on MIDI 60 measures 261.625565 Hz with 0 cents error at 1-cent search resolution. Its RMS changes by less than 0.005 dB across the four sample rates. After one second of a slow-to-fast switch, the horn completes 67.08% of the speed change and the drum completes 26.14%, preserving their different inertias.

This isolated package does not enter the existing kernel entrypoint. The core kernel size remains 325651 bytes raw and 91981 bytes Brotli, a zero-byte delta; the unchanged 327680/122880-byte budgets pass. Score and browser module integration belong to the keyboard integration capability.

## Recorded reference analysis and limits

The [licensed tonewheel recording](https://freesound.org/people/MrAuralization/sounds/158628/) describes a real tonewheel organ and rotating cabinet, recorded at 16-bit/44.1 kHz, and uses CC BY 4.0. Only its high-quality preview was downloaded for analysis; no recording is included in this package or used to design a sample pack. The preview SHA-256 is `9c14a210d7779183cfc9e30ad70b6b7dafe30d11acb37fdfbcc1fb567eac9590`.

The recording identifies its first chord as Am7. The model comparison uses MIDI notes 45, 57, 60, 64, and 67, held at velocity 100. The table analyzes the 0.8..1.8 s window of each file after conversion to mono 48 kHz, DC removal, a Hann window, and exclusion of bins below 20 Hz. Spectral measures use power weighting and are independent of overall loudness.

| Sound | Power centroid, Hz | 95% power rolloff, Hz | Power above 2 kHz |
| --- | ---: | ---: | ---: |
| Recorded reference | 331.1 | 271 | 3.339% |
| Tonewheel Organ | 259.2 | 668 | 0.108% |
| Organ Jazz | 237.3 | 440 | 0.001% |
| Organ Full | 476.7 | 1568 | 2.407% |
| Organ Soft | 279.3 | 523 | 0.525% |

These are coarse comparisons: the reference registration, exact voicing, scanner setting, microphone response, and key velocities are unknown. The default model is darker by power centroid and has more upper-midrange energy by rolloff; the full registration supplies substantially more treble. No spectral error tolerance or exact sound match is claimed. The recording does not provide isolated attacks, releases, or controlled velocity layers, so those reference comparisons remain unvalidated. The modeled registrations' post-release RMS reaches -60 dB in 60..80 ms, measured in 5 ms windows; this includes effect and DC-filter tails.

The scanner is an original 18-stage all-pass circuit model rather than a measured historical circuit clone. The rotary speaker models two bands, directionality, Doppler shift, and motor inertia; it omits room convolution and mechanical cabinet resonances. Recorded calibration and listening tests are needed before calling the sound equivalent to a particular commercial instrument.
