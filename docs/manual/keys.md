# Electronic keyboards

Cicada has 21 original keyboard patches. Their modeled versions retain
velocity, sustain and release behavior. All prepare coefficients before audio
starts, allocate nothing during playback and support MIDI 21–108 at 44.1,
48, 96 and 192 kHz. `voices` reserves 1–8 notes per track within the native
256-voice project budget. Browser and WAM images support up to 32 voices
across 16 tracks; native playback supports up to 64 tracks.

| Family | Score instrument names |
| --- | --- |
| Tine EP | `tine_ep`, `tine_bell`, `tine_bark`, `tine_tremolo` |
| Reed EP | `reed_ep`, `reed_tremolo` |
| Clav | `clav`, `clav_muted`, `clav_hollow` |
| Tonewheel Organ | `tonewheel_organ`, `organ_jazz`, `organ_full`, `organ_soft` |
| Six-operator FM | `fm_ep`, `bell_keys`, `fm_bass` |
| Analog poly | `brass_stab`, `soft_pad`, `poly_keys`, `sync_lead` |
| Divide-down strings | `string_machine` |

```cicada
cicada 2
tempo 108
track keys tine_ep {
  voices = 4
  pickup_distance = 0.7
  hammer_felt = 0.6
  release = 120ms
  tremolo = 0.35
  tremolo_rate = 4.8hz
}
pattern comp notes { [c4 e4 g4 b4] - - . [d4 f4 a4 c5] - - . }
scene dry { keys = comp }
scene held { keys = comp keys.sustain = 1 }
song { dry held }
```

Complete comping and melodic examples for each patch are in `examples/keys/`.
Give pads enough held time for their attack. Note velocity changes the modeled
pickup or modulation depth; `^` accents a note or chord. The sustain control
uses the same live MIDI pedal path as the grand. Most other score controls
prepare new coefficients when the score is applied; the next section lists the
ones a scene or an `automate` lane can change during playback.

Declared samplers, instruments and kits retain their own names: a declaration
named `tine_ep` overrides that built-in patch in its score.

## Changing controls during playback

A scene setting such as `keys.tremolo = 0.6` or an `automate keys.tremolo`
lane changes these controls while the song plays. Every other keyboard control
is fixed until the score is applied again. A path from another family fails
with `CICADA-PARAM`: `keys.tremolo` on a `clav` track is an error.

| Family | Scene setting | `automate` | Range and unit |
| --- | --- | --- | --- |
| Every keyboard | `sustain` | yes | 0–1, normalized |
| Tine and Reed EP | `tremolo`, `auto_pan`, `drive` | yes | 0–1, normalized |
| Tine and Reed EP | `tremolo_rate` | yes | 0.1–12 hz |
| Tine and Reed EP | `gain` | yes | 0–2, normalized |
| Clav | `drive` | yes | 0–1, normalized |
| Clav | `gain` | yes | 0–2, normalized |
| Tonewheel Organ | `drive`, `rotary_mix` | yes | 0–1, normalized |
| Tonewheel Organ | `gain` | yes | 0–2, normalized |
| Tonewheel Organ | `rotary_fast` | no | `slow` or `fast` (also `off` or `on`) |
| Tonewheel Organ | `drawbar1` to `drawbar9` | no | whole numbers 0–8 |
| FM keys | `gain` | yes | 0.001–2, normalized |
| Analog poly | `drive` | yes | 0–1, normalized |
| Analog poly | `output` | yes | -60–6 dB |
| Divide-down strings | `output` | yes | -60–6 dB |

Continuous controls glide to a new value over about 20 ms, so a step in a scene
or an automation lane does not click. Drawbars and rotary speed use the organ's
own smoothing: a drawbar fades over about 4 ms, and the rotary horn and drum
speed up or slow down over one to four seconds. Drawbar 9 stays silent while the organ's
`percussion` is on, as on the hardware it models.

```cicada
cicada 2
track keys tine_ep { }
pattern comp notes { [c4 e4 g4 b4] - - . [d4 f4 a4 c5] - - . }
automate keys.tremolo { @1.1.1 0 @3.1.1 0.8 }
scene calm { keys = comp }
scene wobble { keys = comp keys.tremolo_rate = 6hz keys.auto_pan = 0.7 }
song { calm wobble }
```

Scene settings follow the same rule as every other scene parameter. A later
scene that omits a setting keeps the earlier value, and starting the song again
or seeking back to the start restores the track's authored value at once.

## Native and browser playback

Native Cicada contains the family. Studio loads `cicada-keys.wasm` only when an
image advertises capability bit 9. Build it with `make build-keys-wasm` and
place it beside the CLI, or set `CICADA_KEYS_WASM`. The core kernel and worklet
keep their existing budgets; the heavier keyboard DSP is separately loaded.
Switching between core and keyboard modules retains the AudioContext and
estimated transport position, resets held notes/effect tails, and finishes
and disarms active capture with a message to arm it again.

`make test-keys-wasm` checks core rejection of keys images, all 21 score
patches, native/TinyGo PCM and musical messages, score golden hashes and zero
WASM render allocations. Per-engine fixtures additionally cover four rates
and three block sizes. Reference and cost evidence is in the engine READMEs
and [the reference report](keys-reference-measurements.md). Unknown reference
registrations and velocities limit exact comparisons; listening is needed to
assess the sound.

## Levels

Every modeled keyboard patch has one fixed calibration trim, so the same music
at the same fader comes out at about the same loudness. The reference for each
patch is its `examples/keys/<patch>.cicada` file at `level = 0dB`; its first
four bars measure -24 LUFS integrated (BS.1770, plus or minus 0.5 LU). The trim
sits at the voice output, in `LevelTrimDB` in `host/keyboard`. It is separate
from `gain`, `output`, `level` and velocity, which keep their meaning and
defaults: a score with `output = 6dB` gets the trim plus its 6 dB. Native
playback, offline render and the keys WASM module apply the same trim.
`TestKeysReferenceLevels` in `render` measures all 21 patches.

Scores written before this change play their keyboard parts at a different
loudness: trims range from -5.7 dB (`clav`) to +15.1 dB (`poly_keys`). Re-check
the balance of existing mixes.

The grand piano (`piano`) has its own fixed trim of +12.97 dB
(`PianoLevelTrimDB` in `kernel/voice/keyboard`), applied at the voice output in
the engine and in offline render. Its reference is `examples/modeled-piano.cicada`
with the fader moved from -6 dB to 0 dB, rendered over its four bars; that
measures -24 LUFS. `level`, velocity and `sustain` keep their meaning.
`TestPianoReferenceLevel` in `render` checks it. Existing piano scores play
about 13 dB louder than before.

## Tine and Reed EP controls

| Parameter | Range | Unit | Effect when increased |
| --- | --- | --- | --- |
| `sustain` | 0–1 | normalized | Values above 0.5 hold released notes until the pedal is lifted. |
| `voices` | 1–8 | integer | Allows more simultaneous notes before the oldest voice is replaced. |
| `pickup_position` | 0–1 | normalized | On Tine, moving away from 0.5 toward either end adds the second harmonic; the center gives odd pickup harmonics. Reed's pickup field does not use this control. |
| `pickup_distance` | 0.2–2 | normalized | Gives a cleaner, quieter tone with less nonlinear bark. Moving the pickup closer adds harmonic body and saturation. |
| `hammer_felt` | 0–1 | normalized | Softens the strike and reduces bending-mode ping and contact noise. Lower values add ping. |
| `decay` | 0.3–20 | s | Extends the held fundamental's decay. Bending modes retain their shorter decay caps. |
| `release` | 0.02–2 | s | Slows damping after key-off or pedal release. |
| `drive` | 0–1 | normalized | Increases pickup-stage saturation and harmonic bite. |
| `tremolo` | 0–1 | normalized | Deepens the periodic volume change. |
| `tremolo_rate` | 0.1–12 | hz | Speeds up tremolo and auto-pan. |
| `auto_pan` | 0–1 | normalized | Widens left/right motion when tremolo is enabled. |
| `gain` | 0–2 | normalized | Raises output level after pickup shaping. |
| `oversample` | 2 or 4 | integer | Four internal substeps reduce aliasing at greater CPU cost. |

Harder strikes increase the pickup's harmonic bark as well as level. The Tine
presets use a short bending transient over the longer fundamental. `tine_ep`
and `tine_tremolo` place the pickup near an end for harmonic body. `tine_bark`
adds a closer pickup and more drive; `tine_bell` keeps a cleaner pickup and
firmer strike for a distinct ping. Reed's bending transient also fades early.

Track overrides replace preset controls. Raising `pickup_distance` on
`tine_bark` reduces its defining bark; raising `hammer_felt` on `tine_bell`
softens its ping. Start with the preset defaults, then change one control at a
time to keep the intended character audible.

## Clav controls

| Parameter | Range | Unit |
| --- | --- | --- |
| `sustain` | 0–1 | normalized |
| `voices` | 1–8 | integer |
| `pickup` | 0–3 | integer |
| `string_mute` | 0–1 | normalized |
| `tangent` | 0.05–0.45 | normalized |
| `click` | 0–1 | normalized |
| `drive` | 0–1 | normalized |
| `gain` | 0–2 | normalized |

## Tonewheel Organ controls

| Parameter | Range | Unit |
| --- | --- | --- |
| `sustain` | 0–1 | normalized |
| `voices` | 1–8 | integer |
| `drawbar1` | 0–8 | integer |
| `drawbar2` | 0–8 | integer |
| `drawbar3` | 0–8 | integer |
| `drawbar4` | 0–8 | integer |
| `drawbar5` | 0–8 | integer |
| `drawbar6` | 0–8 | integer |
| `drawbar7` | 0–8 | integer |
| `drawbar8` | 0–8 | integer |
| `drawbar9` | 0–8 | integer |
| `percussion` | 0–2 | integer |
| `percussion_fast` | 0–1 | integer |
| `percussion_soft` | 0–1 | integer |
| `scanner` | 0–6 | integer |
| `click` | 0–1 | normalized |
| `leakage` | 0–1 | normalized |
| `crosstalk` | 0–1 | normalized |
| `drive` | 0–1 | normalized |
| `rotary_mix` | 0–1 | normalized |
| `mic_spread` | 0–1 | normalized |
| `rotary_fast` | 0–1 | integer |
| `gain` | 0–2 | normalized |

## FM keys controls

| Parameter | Range | Unit |
| --- | --- | --- |
| `sustain` | 0–1 | normalized |
| `voices` | 1–8 | integer |
| `gain` | 0.001–2 | normalized |
| `stereo_spread` | 0–1 | normalized |
| `op1_ratio` | 0.125–32 | normalized |
| `op1_detune` | -50–50 | cents |
| `op1_level` | 0–8 | normalized |
| `op1_velocity` | 0–1 | normalized |
| `op1_key_tracking` | 0–1 | normalized |
| `op1_pan` | -1–1 | normalized |
| `op1_feedback` | 0–2 | normalized |
| `op1_attack` | 0–5 | s |
| `op1_decay` | 0–30 | s |
| `op1_amp_sustain` | 0–1 | normalized |
| `op1_release` | 0.005–30 | s |
| `op1_output` | 0–1 | normalized |

## Analog poly keys controls

| Parameter | Range | Unit |
| --- | --- | --- |
| `sustain` | 0–1 | normalized |
| `voices` | 1–8 | integer |
| `saw` | 0–1 | normalized |
| `pulse` | 0–1 | normalized |
| `pulse_width` | 0.1–0.9 | normalized |
| `pwm` | 0–0.35 | normalized |
| `pwm_rate` | 0.01–10 | hz |
| `sub` | 0–1 | normalized |
| `detune` | 0–30 | cents |
| `drift` | 0–10 | cents |
| `sync` | 1–8 | normalized |
| `cutoff` | 20–18000 | hz |
| `resonance` | 0–0.95 | normalized |
| `key_track` | 0–1 | normalized |
| `filter_env` | 0–6 | normalized |
| `drive` | 0–1 | normalized |
| `attack` | 0.001–10 | s |
| `decay` | 0.01–10 | s |
| `amp_sustain` | 0–1 | normalized |
| `release` | 0.01–10 | s |
| `velocity` | 0–1 | normalized |
| `chorus` | 0–1 | normalized |
| `output` | -60–6 | db |
| `filter` | 0–1 | integer |

## String machine controls

| Parameter | Range | Unit |
| --- | --- | --- |
| `sustain` | 0–1 | normalized |
| `voices` | 1–8 | integer |
| `octave16` | 0–1 | normalized |
| `octave8` | 0–1 | normalized |
| `octave4` | 0–1 | normalized |
| `attack` | 0.001–10 | s |
| `release` | 0.01–10 | s |
| `cutoff` | 200–16000 | hz |
| `velocity` | 0–1 | normalized |
| `ensemble` | 0–1 | normalized |
| `ensemble_rate` | 0.1–3 | normalized |
| `output` | -60–6 | db |

## Selector values and FM routing

Clav `pickup` accepts `bridge`, `neck`, `both` or `difference`. `string_mute`
controls damping yarn; the track mixer’s `mute` controls the entire track.

Organ drawbars run from `drawbar1` (16-foot) through `drawbar9` (1-foot).
`percussion` accepts `off`, `second` or `third`; `percussion_fast` accepts
`slow`/`fast`, `percussion_soft` accepts `normal`/`soft`, and `scanner` accepts
`off`, `v1`–`v3` or `c1`–`c3`. `rotary_fast` accepts `slow`/`fast`.

FM exposes the listed `op1_*` fields for every operator, `op1`–`op6`.
`opN_amp_sustain` is its envelope level, separate from the shared pedal.
`routeS_D` sends operator S into lower-numbered operator D, with amounts
0–4; every S>D pair is available. Self-feedback uses `opN_feedback`.
Routing is an original acyclic network with six independent envelopes.
Analog `filter` accepts `ladder` or `state_variable`.

`decay`, `attack` and `release` accept seconds or milliseconds. Frequency
controls require `hz`, and `output` requires `dB`. Detuning values are cents.
Other controls are numbers; integer selectors reject fractional values.
`sustain` also accepts `off`/`on`.

Detune and drift controls use plain numeric values interpreted as cents, such as
`op1_detune=12` or `detune=9 drift=2`. Time controls accept seconds or `ms`;
frequency and output controls retain their documented `hz` and `db` units.

Standard-library keyboard presets are available through `import "std/keys"`.
Use qualified names such as `track part keys.fm_ep { voices = 4 }`. All 21
exports preserve the matching native patch defaults and accept the controls
listed above; preset defaults are applied before track overrides. Examples for
every export are in `examples/keys/std/`, with the exact embedded-library pin.

Presets resolve to the same patch names as direct tracks. A local instrument,
kit, or sampler declaration with that name takes precedence. The library
manifest requires keyboard capability bit 9 (512); native hosts prepare the
modeled voices and Studio loads the optional keyboard module for their images.

## Owned samples

`cmd/cicada-keys-pack` records these original engines at 192 kHz and their
highest available quality, then applies a sinc decimator to produce 48 kHz
PCM packs. It writes pinned manifests, five or more velocity layers, two or
more round robins, EP/Clav release takes and crossfaded sustain loops for
organ/pads/strings. No reference audio or factory data is used.

Pack dimensions must fit the sampler’s existing 256 MiB decoded PCM limit.
Default captures last 4 seconds for mono and 2 seconds for stereo; non-looped
voices have finite held tails. `--duration`, range and root spacing can be
chosen together within that limit. Sampling freezes nonlinear response at
layer centers and global modulation into each take. Pitch shifting, the
mandatory onset ramp and sustain loops also change PCM, so packs reproduce
these patch designs rather than arbitrary modeled performance bit-for-bit.
