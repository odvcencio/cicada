# Electronic keyboards

Cicada has 21 original keyboard patches. Their modeled versions retain
velocity, sustain and release behavior. All prepare coefficients before audio
starts, allocate nothing during playback and support MIDI 21–108 at 44.1,
48, 96 and 192 kHz. `voices` reserves 1–8 notes per track within the unchanged
32-voice project budget.

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
uses the same live MIDI pedal path as the grand. Other score controls prepare
new coefficients when the score is applied. The Organ Go API also supports
smoothed live drawbars and rotary speed changes.

Declared samplers, instruments and kits retain their own names: a declaration
named `tine_ep` overrides that built-in patch in its score.

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
registrations and velocities limit exact comparisons; owner listening is the
acceptance check.

## Tine and Reed EP controls

| Parameter | Range | Unit |
| --- | --- | --- |
| `sustain` | 0–1 | normalized |
| `voices` | 1–8 | integer |
| `pickup_position` | 0–1 | normalized |
| `pickup_distance` | 0.2–2 | normalized |
| `hammer_felt` | 0–1 | normalized |
| `decay` | 0.3–20 | s |
| `release` | 0.02–2 | s |
| `drive` | 0–1 | normalized |
| `tremolo` | 0–1 | normalized |
| `tremolo_rate` | 0.1–12 | hz |
| `auto_pan` | 0–1 | normalized |
| `gain` | 0–2 | normalized |
| `oversample` | 2–4 | integer |

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
controls require `hz`, detuning requires `cents`, and `output` requires `dB`.
Other controls are numbers; integer selectors reject fractional values.
`sustain` also accepts `off`/`on`.
