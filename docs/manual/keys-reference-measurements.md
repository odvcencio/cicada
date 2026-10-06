# Electronic keys: recorded-reference measurements

Measured on 2026-10-06. These comparisons expose several remaining timbre
differences; they do not establish equivalence to a commercial instrument. The
models use original patches. Reference recordings are used for analysis only.

## Recordings and scope

| Reference | Recording and license | What can be compared |
| --- | --- | --- |
| String, clean | [Real 1976 string machine, exposed 4-foot register](https://commons.wikimedia.org/wiki/File:ARP-Solina_Violin_clean_Stair.ogg), public domain, granted by the copyright holder | Isolated first tone; harmonic balance and attack |
| String, ensemble | [Same string machine and register, modulation enabled](https://commons.wikimedia.org/wiki/File:ARP-Solina_Violin_modulated_Stair.ogg), public domain | Ensemble spectrum and attack |
| Analog, isolated | [Analog paraphonic keyboard, three sequentially overlapping notes](https://commons.wikimedia.org/wiki/File:Korg_Volca_Keys_paraphonic_behaviour.flac), [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/) | First isolated C3 tone; attack, spectrum and held-note decay |
| Analog, polyphonic | [Two real analog polyphonic instruments, lead and arpeggio](https://commons.wikimedia.org/wiki/File:Juno-60-jp4.ogg), [CC0](https://creativecommons.org/publicdomain/zero/1.0/) | Broad spectral range of a musical performance; individual voices cannot be separated |
| FM hardware | [Six-operator FM hardware, EP, bells and bass demonstrations](https://commons.wikimedia.org/wiki/File:Korg_Volca_fm_2_-_Demo_using_Yamaha_DX7_presets.flac), [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/) | Isolated first tones, bright bell chord and repeated bass strikes |

The FM source describes a line recording with chorus disabled, a small amount
of reverb and live keyboard velocity. It plays existing hardware patches; only
the recording was analyzed. The model patches use their own operator settings.
Attribution and recording metadata remain available on the linked source pages.

Reference MIDI messages, exact velocities, envelope settings, pickup or filter
controls and recording gains are unavailable. The string sources are lossy
Vorbis recordings. The analog duet contains overlapping instruments. The FM
recording contains reverb and some overlapping notes. These facts limit any
comparison of envelopes or velocity response.

## Method

All recordings were decoded to mono floating-point audio at 48 kHz. Each model
was rendered directly from its package at 192 kHz and reduced to 48 kHz with a
polyphase Kaiser filter, using SciPy 1.18.1 `resample_poly(up=1, down=4)` with
its default window. Stereo model channels were averaged before reduction.
Each controlled model tone starts at time zero, stays held for two seconds,
and has three seconds of release. All parameters retain their patch defaults
unless a setting is explicitly listed below.

Spectra use a symmetric Hann window and squared FFT magnitude. DC is removed.
Reported centroid and 95% rolloff include power from 40 Hz through 12 kHz;
centroid is the power-weighted mean frequency, and rolloff is the frequency
below which 95% of that power falls. These figures describe spectral balance,
not loudness. Different note registers, voicings and unknown velocities can
change them substantially.

Attack is the first 10% to 90% rise relative to the highest RMS within the
first 300 ms, using nonoverlapping 5 ms RMS blocks. The slow pad uses a 1.1 s
search interval. Values have at least 5 ms resolution and are sensitive to
oscillator phase and chorus interference. A measured zero rise is reported as
at most 5 ms. Post-peak T20 is the first observed 20 dB amplitude drop before
the next reference note; it is not an inferred key-release time. Model release
T20 uses the final 100 ms before the known two-second key-off as its baseline.

The analog duet's range uses 4,096-sample Hann frames with a 2,048-sample hop.
Frames more than 30 dB below the loudest frame are excluded. Its 10th, median
and 90th percentile centroids are **311 / 618 / 1,208 Hz**; corresponding 95%
rolloffs are **691 / 1,699 / 2,965 Hz**. This is a musical reference range,
not a calibration target for an isolated model note.

## String machine

The first recorded tone has an approximately 130.8 Hz fundamental. Reference
spectra use 0.20–0.80 s. Controlled model spectra use the same interval and
velocity 80. The single-register model comparison sets `Octave16=0`,
`Octave8=0`, `Octave4=1`, `Ensemble=0` and uses note 36, so its sounding
fundamental also matches 130.8 Hz. Its envelope and filter retain defaults.
The default patch uses note 48 and also includes its lower 16-foot component.

| Sound | Centroid, Hz | 95% rolloff, Hz | RMS attack, ms |
| --- | ---: | ---: | ---: |
| Recorded clean register | 618 | 1,440 | 45 |
| Modeled single 4-foot register, ensemble off | 363 | 1,178 | 105 |
| Recorded ensemble register | 501 | 1,315 | 70 |
| Default `string_machine` | 453 | 1,438 | 100 |

The recorded clean tone has second and third harmonics **+4.0 and +3.4 dB**
relative to its fundamental. The modeled isolated register has **−5.3 and
−8.7 dB**. The reference therefore has much stronger low-order upper partials.
The default model's 262 and 392 Hz partials are +4.1 and −7.3 dB relative to
its 130.8 Hz component; it also includes a lower 65.4 Hz component. Register
mixing alone does not recreate the recorded shaping. The default ensemble has
a broadly similar centroid to the
recorded ensemble, with different registration and harmonic structure.

The model's measured release T20 is **413 ms** with default ensemble and
**382 ms** for the isolated register. The first recorded note remains sounding
until the next stair-step note; its key-off time cannot be established, so a
reference release measurement is unavailable.

Follow-up: measure more registrations and key ranges, then add register-specific
spectral shaping and calibrate the fast attack setting. The current single
tone filter and saw mixture do not capture the exposed register's harmonic
balance. The three asynchronous ensemble delays create stereo movement, but
the available reference is insufficient to validate an exact modulation law.

## Analog keys and pads

The isolated analog reference begins with a held C3 tone, before further notes
at about 2.72 and 5.56 s retrigger its shared envelope. The first tone's spectrum
uses 0.04–0.25 s. Model comparisons use note 48, velocity 80 and the same
interval; the slow pad uses 0.70–1.30 s after its attack.

| Sound | Centroid, Hz | 95% rolloff, Hz | RMS attack, ms | Model release T20, ms |
| --- | ---: | ---: | ---: | ---: |
| Recorded isolated analog tone | 343 | 1,057 | ≤5 | unavailable |
| `poly_keys` | 364 | 1,310 | 10 | 138 |
| `brass_stab` | 471 | 1,710 | 20 | 63 |
| `soft_pad` | 216 | 788 | 645 | 713 |
| `sync_lead` | 634 | 2,095 | 5 | 88 |

The reference's second harmonic is −6.1 dB relative to its fundamental;
`poly_keys` measures −4.6 dB for the same 262 / 131 Hz ratio, with an additional
sub oscillator below those frequencies. The reference falls 20 dB from its initial peak
in **1.15 s while the first note is still held**. All four model patches retain
a nonzero sustain and do not fall 20 dB before their controlled key-off.
Their envelope designs intentionally cover different musical uses, and this
reference's unknown controls cannot establish a match for them.

The source's shared-envelope behavior also differs from the model's independent
per-voice envelopes. The polyphonic duet is a stronger example of performance
texture, but contains no isolated voice suitable for an envelope or resonance
calibration. Follow-up: obtain isolated polyphonic hardware notes across known
filter settings and velocities; calibrate resonant and hard-sync spectra as
well as static harmonic balance. The current 2× oscillator/filter path uses
an economical decimator, so the existing high-note alias tests remain relevant
alongside these recordings.

Musical listening also exposed pulse-width DC passing through the nonlinear
filter and chorus. The final engine includes a prepared 12 Hz stereo output
high-pass. Across all supported rates, sustained asymmetric pulse tests measure
at most **−86.8 dB DC relative to RMS**, and changing pad chords with released
tails measure at most **−145.0 dB**. The string engine's existing high-pass
measures at most **−114.1 dB** with the same changing-chord protocol. These
figures describe integrated offset, not low-frequency musical energy.

## FM keys

The EP comparison selects the first approximately C4 tone at 0.94–1.12 s.
The low bell selects the first approximately C3 tone at 40.59–40.89 s. The
high bell window at 75.60–75.90 s contains several tones. Spectral components
near 1,046, 1,318 and 1,568 Hz suggest a C6/E6/G6 voicing; this is an inference
from the recording. The controlled high-bell model uses notes 84, 88 and 91
together. The bass selects the first strike at 100.14–100.28 s, with audible
components near 65 and 131 Hz; model note 48 produces these through its
half-ratio and unit-ratio carriers.

Model velocity is 80. Model spectral windows start 50 ms after the trigger for
EP and bells, or 15 ms for bass, and have the same duration as each reference
window. The three high model bell tones start together; the exact source
voicing, trigger offsets and velocities are unknown.

| Sound | Recorded centroid, Hz | Model centroid, Hz | Recorded 95% rolloff, Hz | Model 95% rolloff, Hz | Recorded / model RMS attack, ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| EP / `fm_ep` | 278 | 267 | 522 | 267 | 5 / 10 |
| Low bell / `bell_keys` | 367 | 358 | 787 | 840 | 10 / ≤5 |
| High bell chord / `bell_keys` | 4,314 | 3,415 | 9,397 | 8,443 | 5 / 5 |
| Bass / `fm_bass` | 137 | 153 | 257 | 264 | 5 / 10 |

The EP second harmonic is **−11.9 dB** relative to its fundamental in the
recording and **−17.1 dB** in the model at velocity 80. The bass is **+5.4 dB**
and **+2.9 dB**, respectively. These are useful coarse tonal checks, with
unknown source velocities and controls.

The original bell patch was retuned after the first comparison exposed weak
upper partials. The final low-note spectrum is near the selected recording's
centroid and rolloff at velocity 80. The high voicing reaches **5,061 Hz centroid
and 10,037 Hz rolloff** at velocity 127, above the selected recording's
4,314 / 9,397 Hz; at velocity 104 its centroid is 4,189 Hz. Unknown reference
velocities prevent treating those cases as matched strikes. Its low-note
centroid reaches 546 Hz at velocity 127. The recorded low bell falls 20 dB in
**790 ms** after its first RMS peak, before the following strike; the model
measures **845 ms**. Its controlled release T20 is **368 ms**. The high model
chord reaches post-peak T20 in **515 ms**, and has **203 ms** release T20.
The EP and bass releases are **93 and 43 ms**.
Reference key-release times are unavailable.

Sixty-three repeated bass onset windows from 100.12–115.9 s were measured
10–90 ms after each onset. Their centroid 10th / median / 90th percentiles are
**132 / 150 / 770 Hz**, while RMS spans only **1.59 dB**. Detected onsets are
at least 120 ms apart and require a rise above 12 dB in a 5 ms block. Exact MIDI
velocity values are unavailable, and recording level is not a velocity proxy;
these observations cannot be fitted as a controlled velocity curve.

The bass patch was also retuned with stronger velocity-dependent modulation.
Its controlled velocity sweep now spans 117–717 Hz centroid, approaching the
recording's wide brightness range. Its model level still changes by 7.65 dB
across that sweep; the recording's exact strikes cannot establish the intended
level curve. Follow-up: measure known MIDI velocities and registers, and assess
both patches in matched dry musical phrases. The source's reverb, voicing and
unknown settings prevent a commercial-equivalence claim from these figures.

## Controlled velocity response

Each model was rendered at velocities **32, 56, 80, 104 and 127**, with the
same notes and windows used above. This table gives the endpoint level increase
and centroid change. Intermediate level values are monotonic, as are centroid
values for the analog and FM examples. String velocity changes level; its
centroid varies by less than 0.001 Hz across the five levels.

| Patch | RMS increase, v32 → v127, dB | Centroid, v32 → v127, Hz |
| --- | ---: | ---: |
| `poly_keys` | 7.16 | 340 → 382 |
| `brass_stab` | 7.13 | 420 → 520 |
| `soft_pad` | 2.64 | 213 → 220 |
| `sync_lead` | 7.06 | 583 → 683 |
| `string_machine` | 2.64 | 453 → 453 |
| `fm_ep` | 8.38 | 262 → 291 |
| `bell_keys`, C3 | 8.59 | 263 → 546 |
| `bell_keys`, inferred high chord | 8.45 | 2,541 → 5,061 |
| `fm_bass` | 7.65 | 117 → 717 |

The recordings do not supply controlled MIDI velocity sweeps, so these are
model curves rather than measured errors against a hardware velocity curve.
Matched-loudness owner listening remains a separate acceptance step.

## Original-file verification

These SHA-256 hashes identify the analyzed originals; reference audio is kept
outside the repository.

| Reference | SHA-256 |
| --- | --- |
| String, clean | `9926adf91e4a0cf4a44451247e93c188283035dcad0884352b8194f1aaf13fa3` |
| String, ensemble | `223194bf66606b0c525691c52112e65d6a2a2eac72d94d2474ffd7e8feaeed66` |
| Analog, isolated | `ca3edb395e2ec091eb1ae800ee6e923cb7baf448d2243600d5c3dc054a12a085` |
| Analog, polyphonic | `033b2393d56aed67a6f85416d2e6bb64e4598385bdb5b1f9c390ed987ba65bee` |
| FM hardware | `72e9039c01bf2b6c3f1e8c03037bdc6d6574335c9a0dd4607129315e50f06579` |

The analysis script SHA-256 is
`0b4ccd137830beba2ab1b1f4a2e6657f0461d0060d54918b4d298a791f627a1d`.
