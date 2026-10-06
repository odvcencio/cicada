# Accepted source syntax and implementation status

These owner-accepted designs extend Cicada's source language. Each section says what is available in the current build. A `cicada-accepted` example records syntax that the validator does not yet accept; the documentation test skips those examples until the feature lands.

Runnable examples in this file use source edition 2. Examples marked `cicada-accepted` are designs for later support.

## Authored graph phase modulation

**Status:** Implemented. The bell and feedback-free PM bass are experimental pending owner listening acceptance; scores select them through authored instruments.

**Syntax and meaning:** `pm(hz, audio, unit)` returns a sine carrier with an audio phase offset scaled by index in radians. An index envelope changes brightness independently of amplitude. Stored phase stays bounded; the graph remains acyclic and keeps its 128-node and 32-stateful-node limits. PM needs no delay storage. Wrong units and arity report `CICADA-UNIT` and `CICADA-PARAM`.

**Aliasing and compatibility:** PM is not antialiased. The [edition-2 reference](edition-2.md#graph-phase-modulation) records measurements and the example's register/index limit. Opcode 27 requires image-version-13 capability bit 2 without changing node records; older images still load. Additive in editions 1 and 2, with shared language-server hover and `cicada explain graph.pm` descriptions.

**Example:** [fm-bell.cicada](../../examples/fm-bell.cicada) authors a two-operator FM bell with a decaying index and a feedback-free PM bass.

## Authored graph delay and comb

**Status:** Implemented. Authored pluck and slapback voices are experimental pending owner listening acceptance.

**Syntax and meaning:** `delay(audio, ms)` provides a linearly interpolated tap. `comb(audio, ms, unit, unit)` provides allpass interpolation, feedback, and one-pole damping, with the complete loop period tuned at its fundamental. `1 / pitch` has type ms. See [graph delays and plucked strings](edition-2.md#graph-delays-and-plucked-strings) for units, limits, diagnostics, interpolation, and image compatibility.

**Core budget:** 4,096 float32 samples per delay or comb, at most 8,192 per voice (32,768 bytes of rings). Storage is allocated before rendering. A third node reports `CICADA-LIMIT`; invalid units and constant controls report `CICADA-UNIT` and `CICADA-PARAM`.

**Edition history:** Additive in editions 1 and 2. No new declarations or expression syntax are required. [pluck.cicada](../../examples/pluck.cicada) demonstrates a graph-only Karplus–Strong voice and slapback.

## Parameter paths and scene settings

**Status:** Implemented. Scene parameter paths use the shared registry for type, unit, range, and engine validation.

**Syntax (EBNF):**

```ebnf
parameter_path ::= identifier , { "." , identifier } ;
scene_setting ::= parameter_path , "=" , scene_value ;
```

**Meaning:** A scene can set a registered track or effect parameter alongside its pattern bindings. The value takes effect when the scene lands, uses the registered smoothing, and carries into later scenes until another scene changes that path. `cicada explain` reports the effective value at a bar.

**Types and units:** Values use the parameter registry's type, unit, and range. Track paths use the track ID, such as `bass.cutoff`; effect paths use the effect ID, such as `room.feedback`.

**Defaults:** Registry defaults apply first, then track or effect settings, then the latest scene setting. An omitted path carries its previous scene value forward.

**Errors:** Unknown owners report `CICADA-REFERENCE`; unknown paths and out-of-range values report `CICADA-PARAM`; wrong units report `CICADA-UNIT`. Registered values without an engine representation report `CICADA-UNSUPPORTED`.

**Example:** The chorus keeps the cutoff set by the verse:

```cicada
cicada 2
fx room delay { time = 1/8 feedback = 0.3 }
track bass acid {}
pattern pulse acid { 1 . 1 . }
scene verse { bass = pulse bass.cutoff = 900Hz room.feedback = 0.4 }
scene chorus { bass = pulse }
song { verse*4 chorus*4 }
```

**Edition history:** Registry-backed paths and scene parameter settings are implemented. Studio, the language server, and `cicada explain` use the same parameter registry.

## Named mixer forms

**Status:** Implemented for the supported routes in source editions 1 and 2.

**Syntax (EBNF):** Edition 2 uses named effects, sends, inserts, built-in buses, and `master` settings; see [edition 2](edition-2.md#named-mixer-forms). Track and master mixer settings include `level`, `mute`, and `solo`.

**Meaning:** Tracks, built-in buses, effect returns, and the master use named routes. Mute and solo are saved with the score, so a mixer click can preview immediately and remain part of the project. A rendered solo emits `CICADA-SOLO`.

**Types and units:** Levels and send amounts use dB or linear gain as documented in [track mixer settings](edition-1.md#track-mixer-settings). `mute` and `solo` are `on` or `off`. Effect and bus names are identifiers.

**Defaults:** Track level is -6 dB; mute and solo are off. The music bus has a fixed -3 dB trim, the SFX bus is at unity, and the master safety limiter remains last.

**Errors:** Unknown mixer names report `CICADA-REFERENCE`. Unsupported routes, a non-empty master insert, or more than one insert on a route report `CICADA-UNSUPPORTED`. A saved solo reports `CICADA-SOLO` during render and check.

**Example:** Mute and solo are ordinary saved mixer settings:

```cicada
cicada 2
fx room delay {}
track bass acid {
  level = -6dB
  mute = off
  solo = off
  send room = -12dB pre
  out = music
}
pattern pulse acid { 1 . 1 . }
scene main { bass = pulse }
song { main }
```

**Edition history:** The named mixer was accepted as additive edition-1 syntax and is implemented in edition 2. Edition 2 requires named mixer spellings; `cicada fix` migrates the edition-1 aliases.

## Audio assets, clips and samplers

**Status:** Implemented as edition-2 score and project data. `cicada check` verifies assets. Playback, capture, retained-take recovery and Studio controls belong to the other Phase 1 lanes.

**Syntax (EBNF):**

```ebnf
asset_decl ::= "asset" , identifier , string , "{" , { param_decl } , "}" ;
clip_decl ::= "clip" , identifier , identifier , "{" , { param_decl } , "}" ;
sampler_decl ::= "sampler" , identifier , "{" , { param_decl } , "}" ;
```

**Meaning:** An asset identifies immutable file bytes by a project-relative path and SHA-256. A clip defines a half-open source region `[start, end)` with gain and fades. A scene binds a clip to an `audio` track. That binding denotes one start on scene entry; playback scheduling is implemented by the engine lane. A sampler names one whole-asset region and accepts note patterns. `loop` means the whole region; loop bounds and crossfades are later work.

**Types and units:** Required asset fields are `sha256` (64 lowercase hex digits in a quoted string), `format = wav`, positive integer `frames`, integer `rate` from 8000 to 384000Hz, and `channels` from 1 to 2. Optional `source` is `recorded`, `imported`, or `generated`. WAV validation supports PCM 16/24/32-bit and IEEE float32, including ancillary RIFF chunks. It verifies rate, channels and frame count against the declaration without decoding samples. Paths and symlinks must stay inside the project directory.

Clip `start`, `end`, `fade_in` and `fade_out` accept `frames`, `s`, or `ms`. They must resolve exactly to integer source frames. Gain is -60 to +24dB. Each fade is at most the region length. A sampler requires `asset`, an absolute `root` with octave 0–6 and resolved MIDI note 12–95, `mode = oneshot` or `loop`, and integer `voices` from 1 to 32. Audio and sampler tracks accept mixer settings only. Clip names cannot collide with patterns or the scene actions `off`, `keep`, and `stop`.

**Defaults:** Clips start at frame 0, end at the asset frame count, use 0dB gain, and have no fades. Sampler fields are explicit. A project manifest carries edition 2; the legacy source header remains optional.

**Errors:** `CICADA-ASSET-MISSING` reports missing or unreadable files; `CICADA-ASSET-HASH` reports malformed or mismatched hashes; `CICADA-ASSET-FORMAT` reports unsupported containers, WAV encodings, or dimension mismatches; `CICADA-ASSET-PATH` reports path escapes; `CICADA-CLIP-RANGE` reports invalid regions or units; `CICADA-SAMPLER-PARAM` reports invalid sampler fields. Unknown references report `CICADA-REFERENCE` and close names receive a spelling suggestion. Diagnostics include expected and actual values and source positions. Different supported source rates are accepted as data for the sample engine; no resampling runs during checking.

**Example:** Generate the 9,644-byte WAV with the helper in [the manual](../manual/writing-music.md#audio-assets-and-samplers), then use:

```cicada
cicada 2
asset vocal "audio/example.wav" {
  sha256 = "528059ad119b79f269c9580505858c14d3636f77ade0e908942653e4c708f6ee"
  format = wav
  frames = 4800
  rate = 48000Hz
  channels = 1
  source = generated
}
clip vocal-a vocal { start = 10ms end = 4800frames gain = -2dB fade_in = 1ms fade_out = 2ms }
sampler vocal-hit { asset = vocal root = c3 mode = oneshot voices = 8 }
track chops vocal-hit { level = -8dB }
track vox audio { level = -6dB }
pattern hits { c3 . c3 . }
scene verse { chops = hits vox = vocal-a }
song { verse*4 }
```

**Edition history:** The September 30 workstation scope supersedes the earlier additive edition-1 proposal. These declarations require edition 2. Semantic assets, clips and sampler instruments extend `cicada.project/2`; the three arrays are omitted for scores without assets. Older strict readers reject the new fields. The future unified plan ABI and proposed project format 3 remain separate work.

## Mastering targets and the master insert chain

**Status:** Accepted; the mastering view and non-empty master insert chain are not available in the current build. Named export profiles and loudness-targeted WAV export are available; see [export profiles](edition-1.md#export-profiles).

**Syntax (EBNF):**

```ebnf
master_insert ::= "master" , "{" , "insert" , "=" , insert_chain , "}" ;
insert_chain ::= identifier , { "->" , identifier } ;
mastering_target ::= "export" , identifier , "{" , { target_setting } , "}" ;
target_setting ::= "loudness" , "=" , number , ( "LUFS" | "LU" )
                 | "true_peak" , "=" , number , "dBTP"
                 | "normalize" , "=" , switch ;
```

**Meaning:** The mastering view will edit an ordered effect chain before the built-in safety limiter and save named delivery targets in the score. It will offer loudness-matched A/B bypass and Streaming (-14 LUFS, -1 dBTP), Apple Music (-16 LUFS, -1 dBTP), and EBU R128 broadcast (-23 LUFS, -1 dBTP) targets. Reference tracks stay in session settings, not in the score. Existing export profiles already support rate, bit depth, tail, and the implemented render target options.

**Types and units:** Loudness uses LUFS or LU, true peak uses dBTP, and normalize is a switch. Target presets use the values above. An insert chain is an ordered list of declared effect names; the built-in safety limiter stays last.

**Defaults:** Streaming, Apple Music, and EBU R128 targets use the values above. An export profile field that is omitted keeps the renderer's default. The master chain is empty unless declared.

**Errors:** Unknown profiles or effects report `CICADA-REFERENCE`. Invalid units and ranges report `CICADA-PARAM`. A true-peak ceiling without loudness targeting, normalization combined with loudness targeting, and a non-empty master insert currently report `CICADA-UNSUPPORTED`.

**Example:** This records a target and an ordered master chain. The chain and mastering-view behavior remain unavailable:

```cicada-accepted
cicada 2
fx glue comp { threshold = -18dB }
master { insert = glue }
export streaming {
  loudness = -14LUFS
  true_peak = -1dBTP
  normalize = off
}
```

**Edition history:** Named export profiles landed with edition 2. The expanded mastering controls and master insert chain are accepted follow-up work; they do not change the current render limits.

## Live settings and MIDI mappings

**Status:** Built for macro, layers and phrase; land stored; midi and tempo still accepted-only. Record quantization, count-in, and parameter-mapped macros remain accepted-only. Studio already supports live MIDI performance and note takes.

**Syntax (EBNF):**

```ebnf
live_decl ::= "live" , "{" , { live_setting } , "}" ;
live_setting ::= "land" , "=" , ( identifier | bar_count )
               | "phrase" , "=" , bar_count
               | "macro" , identifier , "=" , number , [ "smooth" , duration ]
               | "layers" , identifier , "{" , { layer_setting } , "}"
               | "record" , "=" , fraction
               | "count_in" , "=" , duration ;
layer_setting ::= identifier , ">=" , number
                | "attack" , bar_count
                | "release" , bar_count ;
midi_decl ::= "midi" , "{" , { midi_port | midi_mapping } , "}" ;
midi_port ::= "port" , identifier ;
midi_mapping ::= identifier , ( "ch" , integer | "cc" , integer ) , "->" , parameter_path
               | identifier , "note" , pitch , "->" , ( identifier | action ) ;
```

**Meaning:** In edition 2, one `live` block after all tracks declares ordered host macros, track layers, and phrase length. The compiler emits initial macro values, layer thresholds and masks, and phrase length as global kernel commands at tick 0. Smoothing is a host default for later changes; initialization emits no ramp. `land` is stored for future launch scheduling. Input quantization and count-in remain accepted follow-up work. A scene may save its own launch timing. `midi` maps logical ports to tracks, parameter paths, or launch actions. `cicada.local` maps those logical ports to device names on one machine and is not committed. Session view and armed-track state stay in `.cicada/studio.json`.

**Types and units:** Launch timing is `now`, `beat`, `bar`, `2bars`, `4bars`, or `phrase`. Phrase length is 1–64 bars. At most 16 macros have unitless values in 0–1 and nonnegative smoothing durations in ms or s. Layers reference a declared macro and track IDs, with at most 3 distinct thresholds in 0–1. Only `attack 1bar` is built; release is 1–16 bars. Thresholds round to `value * 255` for the kernel: equal packed values share a level, and packed zero joins level 0. Tracks without a rule are on at every level. Record quantization is a musical fraction. Count-in is a bar count. MIDI channels are 1–16, controller numbers are 0–127, and note mappings use MIDI pitches. A controller mapping may specify a range in the target parameter's unit.

**Defaults:** Macro smoothing is 0 ms, layer attack is 1 bar, and release is 3 bars. Omitted phrase length emits no phrase command. Launches are planned to land on a bar; record quantization is 1/16; count-in is one bar. Omitting a MIDI channel accepts any channel. Device names are resolved from `cicada.local`, not the shared score.

**Errors:** Unknown ports, tracks, actions, parameter paths, channels, or controller numbers must be rejected. Device names unavailable on the current machine and invalid mapping ranges must produce a clear diagnostic. Built live diagnostics use `CICADA-LIVE-MACRO` for unknown macros and range or unit errors, `CICADA-LIVE-TRACK` for unknown or repeated tracks, and `CICADA-LIVE-LIMIT` for macro and threshold limits. Block placement and duplicate settings use `CICADA-LIVE-BLOCK`; landing, phrase, attack, and release errors use `CICADA-LIVE-LAND`, `CICADA-LIVE-PHRASE`, `CICADA-LIVE-ATTACK`, and `CICADA-LIVE-RELEASE`. Each source diagnostic includes a file, line, and column. MIDI diagnostics remain accepted-only.

**Example:** The score stores logical mappings; the local file supplies machine-specific device names:

```cicada-accepted
cicada 2
live {
  land = bar
  record = 1/16
  count_in = 1bar
}

midi {
  port keys
  keys ch 1 -> lead
  keys cc 74 -> bass.cutoff 200Hz..4kHz
}
```

```text
# cicada.local (kept on this machine; do not commit)
port keys = "KeyStep 37 MIDI 1"
```

**Edition history:** The built macro, layers, and phrase forms require edition 2. Expanded live and MIDI settings were accepted additions to edition 1. Device names and Studio view state remain local settings.

## Automation blocks

**Status:** Accepted; not available in the current build.

**Syntax (EBNF):**

```ebnf
automate_decl ::= "automate" , parameter_path , "{" , { automation_point } , "}" ;
automation_point ::= position , value , [ shape ] ;
position ::= "@" , integer , "." , integer , "." , integer ;
shape ::= "step" | "linear" | "smooth" | "exponential" | "curve" , number ;
```

**Meaning:** A lane changes a registered parameter over song time. Lanes may live at song, scene, or pattern scope. Interpolation uses the parameter's control space: for frequency and time parameters that space is logarithmic, so `exponential` has the same meaning as `linear` there. `linear_period` is not a supported shape.

**Types and units:** Positions are one-based bar.beat.step values. Point values use the addressed parameter's registered type and unit. Shapes are step, linear, smooth, exponential-as-linear-in-control-space, or a curve with numeric tension.

**Defaults:** `linear` is the default shape. An omitted lane contributes no automation.

**Errors:** Invalid positions, unresolved paths, incompatible units, and out-of-order points must be rejected. `linear_period` reports `CICADA-UNSUPPORTED`; stable diagnostics and boundary behavior for the other cases have not landed.

**Example:** `exponential` uses the existing linear interpolation rule in control space:

```cicada-accepted
cicada 2
automate bass.cutoff {
  @1.1.1 400Hz
  @5.1.1 2400Hz exponential
}
```

**Edition history:** Accepted as additive edition-1 syntax and semantic JSON version 2 work. The current build has no automation record or Studio lane.

## Continuous pitch settings and rows (P7)

**Status:** Accepted; the `glide`, `vibrato`, `bend:`, and `vibrato:` source settings and rows are not available in the current build. Custom graph voices already glide for 60 ms on `~` notes, as implemented in #65.

**Syntax (EBNF):**

```ebnf
pitch_setting ::= "glide" , "=" , duration
                | "vibrato" , "=" , number , "ct"
                | "vibrato_rate" , "=" , number , "Hz"
                | "vibrato_delay" , "=" , duration ;
bend_row ::= "bend" , ":" , { signed_cents | "." } ;
vibrato_row ::= "vibrato" , ":" , { number , "ct" | "." } ;
signed_cents ::= ( "+" | "-" ) , number , "ct" ;
```

**Meaning:** Instrument settings define continuous pitch behavior and tracks may override them. `bend:` gives a signed pitch offset per step. `vibrato:` gives vibrato depth per step. A dot holds the preceding row value; `0ct` resets it. A tie keeps its pitch state. A `~` note connects to the following note.

**Types and units:** Glide and vibrato delay use ms or seconds. Vibrato depth and bend use cents (`ct`); vibrato rate uses Hz. Bend values are signed cents per step, such as `+50ct` or `-1200ct`.

**Defaults:** Custom graph voices use a 60 ms glide on notes written with `~`; notes without `~` keep their existing onset behavior. The standard theremin library keeps its 70 ms glide. No vibrato is added unless a depth is set; the accepted design does not assign default vibrato rate or delay values.

**Errors:** Negative durations, invalid pitch ranges, wrong units, or a row whose length differs from its pattern must be rejected. A dot before any row value has no value to hold and must be rejected. Diagnostics and the new row semantics have not landed.

**Example:** The settings belong to the instrument or its track override; the rows align with pattern steps:

```cicada-accepted
cicada 2
instrument glassbass {
  param glide = 60ms
  param vibrato = 12ct
  param vibrato_rate = 5.5Hz
  param vibrato_delay = 200ms
  voice mono {
    let osc = saw(pitch)
    out = osc
  }
}

track lead glassbass { glide = 60ms }
pattern glide-line {
  1~ 3 . 5
  bend: +50ct . -1200ct 0ct
  vibrato: 4ct . 12ct 0ct
}
scene main { lead = glide-line }
song { main }
```

**Edition history:** Accepted P7 settings and rows add typed control to edition 1. The 60 ms custom-voice default is implemented in the engine; source overrides and per-step rows remain unavailable.

## Flexible grid and pattern chains

**Status:** Accepted; not available in the current build.

**Syntax (EBNF):**

```ebnf
grid_setting ::= "step" , "=" , fraction ;
tuplet_group ::= "[" , { acid_step } , "]" ;
parameter_row ::= identifier , ":" , { value | "." } ;
chain_decl ::= "chain" , "=" , identifier , { identifier } ;
```

**Meaning:** A pattern-level step duration selects its grid resolution. A bracketed group subdivides one cell evenly; a following tie extends the group. Labeled rows attach per-step values such as cutoff, velocity, nudge, vibrato depth, or pitch bend. A source chain plays its patterns in order and loops.

**Types and units:** Step duration is a note division. At 960 pulses per quarter note (PPQ), accepted values must occupy a whole number of ticks. `nudge` is a percentage of one step; `bend:` values are signed cents, `vibrato:` values are cents of depth, and other rows use their parameter type or the MIDI velocity range. Chains contain at most 32 pattern names.

**Defaults:** `step = 1/16` preserves the current grid. A missing row value (`.`) holds its previous value; `0ct` resets a pitch row. A track without a chain keeps its current pattern behavior.

**Errors:** Divisions that do not produce whole ticks, groups without valid cells, row lengths that differ from the pattern, and invalid chain references must be rejected. For example, `1/16t` and `1/20` fit the 960-PPQ grid; `1/28` does not.

**Example:** The grid and labeled rows are planned for each pattern:

```cicada-accepted
cicada 2
pattern triplet {
  step = 1/8t
  [1 3 5] -
  cutoff: 600Hz . 900Hz
  bend: +50ct . -1200ct 0ct
}
track bass acid { chain = intro triplet chorus }
```

**Edition history:** Accepted as an additive edition-1 form. The current source grammar still uses a fixed sixteenth-note grid and has no parameter rows or source chain declaration.

## Multi-file projects and manifest metadata

**Status:** Multi-file loading and manifest metadata are implemented. Imports, qualified library names, private library declarations, `require`, `cicada.sum`, and bundle provenance remain accepted-only.

**Syntax (EBNF):**

```ebnf
import_decl ::= "import" , string ;
manifest_metadata ::= "entry" , string
                    | "source" , string
                    | "license" , ( spdx_identifier | string )
                    | "author" , string ;
sum_file ::= { generated_sum_record } ;
```

**Meaning:** All listed `.cicada` files under one `cicada.mod` share one namespace, like files in one Go package. The manifest names the entry file and explicit source list and stores an SPDX license identifier and author. Imports are for libraries; a qualified name selects a library declaration. Library declarations whose names begin with underscore are private. `require` directives pin library versions. The generated `cicada.sum` records source hashes and dependency locks. `cicada bundle` writes provenance; the manifest does not.

**Types and units:** Entry and source values are project-relative file paths; the source list is explicit, not a glob. License is an SPDX identifier. Author is a string. Sum records contain SHA-256 hashes for source files and locked dependencies; the generated file is tool-owned.

**Defaults:** Files in one project need no import to reference one another. A single-file project remains valid without changes. The entry is loaded first; remaining source files are loaded in sorted path order.

**Errors:** Duplicate declarations report `CICADA-DUPLICATE` with both file:line:column locations. Unresolved references report `CICADA-REFERENCE` at the reference. `CICADA-SOURCE-PATH` rejects absolute paths, traversal components, glob patterns, unlisted scores, and symlinks that escape the project. `CICADA-SOURCE-MISSING` reports missing or unreadable listed files at their manifest directives. Invalid or repeated manifest directives report `CICADA-MANIFEST`. Dependency and hash diagnostics remain accepted-only.

**Built example:** [Shared circuit](../../examples/multifile/main.cicada) separates its song, patterns, and voices into three files:

```text
project multifile
cicada 2
entry "main.cicada"
source "main.cicada"
source "parts/patterns.cicada"
source "parts/voices.cicada"
license "MIT"
author "Cicada contributors"
```

The entry is included automatically, even if it has no `source` line. Repeating it once in the source list is allowed. Repeated `source` directives for the same path are rejected. An explicit source list requires an entry. Existing manifests without `entry` or `source` keep loading each requested score independently. Source headers are optional and must match the manifest when present. Names retain their existing declaration-kind rules across all files; a track and pattern may share a spelling.

`check`, `fmt`, `fix --all`, `explain`, `play`, and `render` load the project from any listed score. Project-wide `check` compiles it once; `fmt` formats each listed file separately. The language server resolves diagnostics, definitions, renames, parameter hover, completion, and notation fixes across files, including unsaved buffers. Studio refuses projects with more than one source file before opening recovery, editing, or undo history. Save As copies every listed source and updates the manifest if the requested score is renamed.

**Accepted-only example:** Imports and dependency locking are planned follow-up work:

```cicada-accepted
cicada 2
// cicada.mod
project night-circuit
cicada 2
entry "main.cicada"
source "main.cicada"
source "parts/bass.cicada"
license "MIT"
author "Cicada user"

// parts/bass.cicada
pattern bass-a { 1^ . 1~ 5 }

// main.cicada
import "std/theremin"
track lead theremin.classic {}
scene main {
  bass = bass-a
  lead = lead-a
}
song { main*8 }
```

**Edition history:** Multi-file projects and manifest metadata work in editions 1 and 2 without changing source grammar, semantic JSON, or the kernel image format. Imports, `require`, and `cicada.sum` remain accepted follow-up work.
