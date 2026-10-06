# Language extensions

These sections describe supported Cicada syntax, behavior, diagnostics, and limits. Runnable examples declare source edition 2.

## Authored graph phase modulation

**Syntax and meaning:** `pm(hz, audio, unit)` returns a sine carrier with an audio phase offset scaled by index in radians. An index envelope changes brightness independently of amplitude. Stored phase stays bounded; the graph remains acyclic and keeps its 128-node and 32-stateful-node limits. PM needs no delay storage. Wrong units and arity report `CICADA-UNIT` and `CICADA-PARAM`.

**Aliasing and compatibility:** PM is not antialiased. The [edition-2 reference](edition-2.md#graph-phase-modulation) describes the example's register/index limit. Opcode 32 requires capability bit 5 without changing node records; older images still load. Additive in editions 1 and 2, with shared language-server hover and `cicada explain graph.pm` descriptions.

**Example:** [fm-bell.cicada](../../examples/fm-bell.cicada) authors a two-operator FM bell with a decaying index and a feedback-free PM bass.

## Authored graph delay and comb

**Syntax and meaning:** `delay(audio, ms)` provides a linearly interpolated tap. `comb(audio, ms, unit, unit)` provides allpass interpolation, feedback, and one-pole damping, with the complete loop period tuned at its fundamental. `1 / pitch` has type ms. See [graph delays and plucked strings](edition-2.md#graph-delays-and-plucked-strings) for units, limits, diagnostics, interpolation, and image compatibility.

**Core budget:** 4,096 float32 samples per delay or comb, at most 8,192 per voice (32,768 bytes of rings). Storage is allocated before rendering. A third node reports `CICADA-LIMIT`; invalid units and constant controls report `CICADA-UNIT` and `CICADA-PARAM`.

**Edition history:** Additive in editions 1 and 2. No new declarations or expression syntax are required. [pluck.cicada](../../examples/pluck.cicada) demonstrates a graph-only Karplus–Strong voice and slapback.

## Parameter paths and scene settings

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

## Experimental guitar voice

Use `track lead guitar { experimental = on }` with an ordinary note pattern.
Sequenced notes and gates drive a single physical string and its built-in amp;
slides change pitch without replucking. The shared registry exposes `bend`,
`vibrato`, `brightness`, `damping`, `pickup` and `drive` with 8 ms smoothing.
Native playback, the TinyGo AudioWorklet and offline rendering support it
within the existing core voice and size limits. Guitar images use version 15;
legacy encoding and versions 8..13 remain supported. See the
[edition-2 reference](edition-2.md#experimental-guitar-voice) for opt-in,
units, ranges, model limits and image compatibility.

## Named mixer forms

**Syntax (EBNF):** Edition 2 uses named effects, sends, inserts, built-in buses, and `master` settings; see [edition 2](edition-2.md#named-mixer-forms). Track and master mixer settings include `level`, `mute`, and `solo`.

**Meaning:** Tracks, built-in buses, effect returns, and the master use named routes. Mute and solo are saved with the score, so a mixer click can preview immediately and remain part of the project. A rendered solo emits `CICADA-SOLO`.

**Types and units:** Levels and send amounts use dB or linear gain as documented in [track mixer settings](edition-1.md#track-mixer-settings). `mute` and `solo` are `on` or `off`. Effect and bus names are identifiers.

**Defaults:** Track level is -6 dB; mute and solo are off. The music bus has a fixed -3 dB trim, the SFX bus is at unity, and the master safety limiter remains last.

**Errors:** Unknown mixer names report `CICADA-REFERENCE`. Unsupported routes or more than one insert on a track or bus report `CICADA-UNSUPPORTED`. A saved solo reports `CICADA-SOLO` during render and check.

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

**Edition history:** Named mixer forms are implemented in edition 2. Edition 2 requires named mixer spellings; `cicada fix` migrates the edition-1 aliases.

## Audio assets, clips and samplers

**Syntax (EBNF):**

```ebnf
asset_decl ::= "asset" , identifier , string , "{" , { param_decl } , "}" ;
clip_decl ::= "clip" , identifier , identifier , "{" , { param_decl } , "}" ;
sampler_decl ::= "sampler" , identifier , "{" , { param_decl } , "}" ;
```

**Meaning:** An asset identifies immutable file bytes by a project-relative path and SHA-256. A clip defines a half-open source region `[start, end)` with gain and fades. A scene binds a clip to an `audio` track. That binding denotes one start on scene entry; the engine schedules playback. A sampler names one whole-asset region and accepts note patterns. `loop` means the whole region; source loop bounds and crossfades are not supported.

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

**Edition history:** These declarations require edition 2. Semantic assets, clips and sampler instruments extend `cicada.project/2`; the three arrays are omitted for scores without assets. Older strict readers reject the new fields.

## Mastering targets and the master insert chain

**Syntax (EBNF):**

```ebnf
master_insert ::= "master" , "{" , "insert" , "=" , insert_chain , "}" ;
insert_chain ::= identifier , { "->" , identifier } ;
mastering_target ::= "export" , identifier , "{" , { target_setting } , "}" ;
target_setting ::= "loudness" , "=" , number , ( "LUFS" | "LU" )
                 | "true_peak" , "=" , number , "dBTP"
                 | "normalize" , "=" , switch ;
```

**Meaning:** The master insert runs an ordered effect chain before the built-in safety limiter. Studio shows its saved order, settings, and named delivery targets. Loudness-matched A/B bypass is not supported. Streaming (-14 LUFS, -1 dBTP), Apple Music (-16 LUFS, -1 dBTP), and EBU R128 broadcast (-23 LUFS, -1 dBTP) targets can be authored as export profiles. Reference tracks stay in session settings, not in the score. Existing export profiles already support rate, bit depth, tail, and the implemented render target options.

**Types and units:** Loudness uses LUFS or LU, true peak uses dBTP, and normalize is a switch. Target presets use the values above. An insert chain contains at most 16 distinct declared effect names. Supported master kinds are `eq`, `comp`, `transient`, `width`, `limiter`, and `convolution`; see [master effect controls](../audio/mix-chain.md#score-master-effects). The built-in safety limiter stays last. Master effect controls are fixed until recompilation; scene automation and external compressor sidechains are unavailable.

**Defaults:** Streaming, Apple Music, and EBU R128 targets use the values above. An export profile field that is omitted keeps the renderer's default. The master chain is empty unless declared.

**Errors:** Unknown profiles or effects report `CICADA-REFERENCE`. Invalid units and ranges report `CICADA-PARAM`. A true-peak ceiling without loudness targeting and normalization combined with loudness targeting report `CICADA-UNSUPPORTED`. Repeated inserts report `CICADA-DUPLICATE`; chains longer than 16 report `CICADA-LIMIT`.

**Example:** Check this score, then render it with `cicada render score.cicada --export streaming --bars 16 -o streaming.wav` and verify with `cicada verify-wav streaming.wav --bars 16 --lufs -14 --true-peak-max -1`:

```cicada
cicada 2
track bass acid { level = 0dB }
pattern pulse acid { 1 . 3 . 5 . 3 . }
scene main { bass = pulse }
song { main*16 }
fx glue comp { threshold = -18dB }
master { insert = glue }
export streaming {
  loudness = -14LUFS
  true_peak = -1dBTP
  normalize = off
}
```

**Edition history:** Named export profiles landed with edition 2. The master insert chain is additive in editions 1 and 2 and uses existing semantic insert arrays. It does not change the core kernel ABI or budgets. Prepared processors cannot be serialized into a core WASM project image; browser score playback with these inserts requires companion-host wiring.

## Host macros and track layers

A source-edition-2 `live` block after the tracks declares host macros, track layers, and phrase length. Macro values are unitless, from 0 to 1; smoothing uses ms or seconds. At most 16 macros and three distinct layer thresholds are supported. Layer attack is one bar and release is 1–16 bars. Thresholds are quantized to eight bits; tracks without a rule stay active.

Initialization applies macro values immediately. Hosts submit `OpSetMacro` changes with the declared smoothing. `land` stores a launch preference; saved MIDI mappings and record quantization declarations are unavailable. Studio provides live MIDI performance and note recording through its controls. See [host macros and track layers](../manual/next-level.md#host-macros-and-track-layers) for a complete score and host commands.

## Expression rows

**Syntax (EBNF):**

```ebnf
bend_row ::= "bend" , ":" , { signed_cents | "." } ;
vibrato_row ::= "vibrato" , ":" , { number , "ct" | "." } ;
pressure_row ::= "pressure" , ":" , { number | "." } ;
timbre_row ::= "timbre" , ":" , { number | "." } ;
signed_cents ::= [ "+" | "-" ] , number , "ct" ;
```

**Meaning:** Rows follow the melodic cells and have one value per step. `bend:` gives a signed pitch offset; `vibrato:` gives vibrato depth; `pressure:` and `timbre:` provide normalized graph inputs. A dot holds the preceding row value; `0ct` resets pitch or depth. A tie keeps its note and applies that step's expression, so held dots preserve pitch while explicit values can move it. A `~` note connects to the following note.

**Types and units:** Bend is -9600 to 9600 cents (`ct`); vibrato depth is 0 to 9600 cents. Pressure and timbre are numbers from 0 to 1. Bend accepts `+50ct`, `-1200ct`, and `0ct`.

**Defaults:** Pitch, pressure, and vibrato depth start at zero; timbre starts at 0.5. A leading dot holds these defaults. Vibrato rows use 5 Hz with no delay; zero depth disables vibrato. No expression is added to a pattern without rows. Custom graph voices use a 60 ms glide on `~` notes, and the standard theremin library keeps its 70 ms glide.

**Errors:** Duplicate rows, values outside the stated ranges, wrong units, and row lengths that differ from the melodic pattern report errors. Pitch-derived delay and comb times must stay within their ring bounds across bend and vibrato extrema, including tied steps. Drum patterns do not accept expression rows. Assigned sampler and modeled piano tracks reject expression rows because those voices do not yet implement note expression.

**Example:** Rows change a held note without retriggering its envelope:

```cicada
cicada 2
instrument glassbass {
  voice mono {
    let osc = saw(pitch)
    out = osc * env(gate, 300ms) * velocity
  }
}

track lead glassbass {}
pattern glide-line {
  1 - - 5
  bend: +50ct . -1200ct 0ct
  vibrato: 4ct . 12ct 0ct
  pressure: 0 . 0.7 0
  timbre: . 0.8 . 0.5
}
scene main { lead = glide-line }
song { main }
```

**Edition history:** Expression rows are additive in source editions 1 and 2 and semantic formats /1 and /2. The optional `patterns[].expression` array stores resolved pitch cents, pressure, timbre, and vibrato depth. Instrument-level glide and vibrato settings are not supported.

## Multi-file projects and manifest metadata

**Syntax (EBNF):**

```ebnf
import_decl ::= "import" , string ;
manifest_metadata ::= "entry" , string
                    | "source" , string
                    | "license" , ( spdx_identifier | string )
                    | "author" , string ;
sum_file ::= { generated_sum_record } ;
```

**Meaning:** All listed `.cicada` files under one `cicada.mod` share one namespace, like files in one Go package. The manifest names the entry file and explicit source list and stores an SPDX license identifier and author. Imports are for libraries; a qualified name selects a library declaration. Library declarations whose names begin with underscore are private. Version requirements are not supported. The generated `cicada.sum` pins each imported library by path, resolution kind, and content hash.

**Types and units:** Entry and source values are project-relative file paths; the source list is explicit, not a glob. License is an SPDX identifier. Author is a string. Sum records use `PATH std|project|user sha256:HASH`, sorted by path. Each hash covers the library manifest, sorted sources, and audio assets; transitive imports have their own records. The generated file is tool-owned.

**Defaults:** Files in one project need no import to reference one another. A single-file project remains valid without changes. The entry is loaded first; remaining source files are loaded in sorted path order.

**Errors:** Duplicate declarations report `CICADA-DUPLICATE` with both file:line:column locations. Unresolved references report `CICADA-REFERENCE` at the reference. `CICADA-SOURCE-PATH` rejects absolute paths, traversal components, glob patterns, unlisted scores, and symlinks that escape the project. `CICADA-SOURCE-MISSING` reports missing or unreadable listed files at their manifest directives. Invalid or repeated manifest directives report `CICADA-MANIFEST`. `CICADA-LIB-HASH` rejects missing or changed pins at the import. `CICADA-LIB-SHADOW`, `CICADA-LIB-PRIVATE`, `CICADA-LIB-CYCLE`, `CICADA-LIB-DECL`, and `CICADA-LIB-CAPABILITY` diagnose ambiguous resolution, private access, cycles, score-only declarations, and unsupported engine requirements.

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

**Library imports:** [Library circuit](../../examples/libraries/main.cicada) imports a project library. Library manifests declare `library PATH`, `cicada`, `source`, `license`, and `author`, with optional `engine` minimum edition and `capabilities` bit mask. `std/` is an embedded namespace; project `lib/` and the per-OS user config directory plus `cicada/lib` are also searched. `$CICADA_LIBRARY` overrides the user location. Duplicate paths across locations are errors. Direct user imports are hash-pinned. See [the library manual](../manual/writing-music.md#import-a-library) for rules and update commands.

**Edition history:** Multi-file projects and manifest metadata work in editions 1 and 2 without changing source grammar, semantic JSON, or the kernel image format. Imports and hash pinning work in editions 1 and 2 without changing the kernel image format.

## Presets

**Syntax:**

```cicada
cicada 2

instrument glassbass {
  octave = 2
  param cutoff = 540Hz
  param bite = 0.58
  voice mono {
    out = lowpass(saw(pitch), cutoff) * bite * env(gate, 330ms)
  }
}

preset glassbass.bright {
  instrument = glassbass
  cutoff = 900Hz
  bite = 0.7
}
track lead glassbass.bright { bite = 0.65 }
pattern melody { 1 . 3 . }
scene verse { lead = melody }
song { verse }
```

**Meaning:** A preset names an existing target and replaces parameter values without changing its DSP expressions, routing, or asset binding. A track binds a voice preset in its instrument position. Registry default, instrument default, preset, track setting, and scene setting apply in that order. `cicada explain` reports explicit layers and the computed value. Authored parameters have instrument defaults and no separate registry default.

**Targets:** Authored and library-qualified instruments; `acid`; `drums` with lane-prefixed values; a built-in drum lane such as `builtin.bd`; an authored kit's mixer values; `audio` mixer values; declared samplers' root, mode, voice count, and mixer values; and declared or built-in effects. Bind an effect preset with `fx NAME PRESET { ... }`. An unreferenced effect used as its target supplies defaults without creating an extra runtime instance; routed or scene-addressed targets remain active. Built-in effect targets are `builtin.drive`, `builtin.delay`, `builtin.reverb`, and `builtin.comp`. An authored kit lane can bind an authored instrument preset. The experimental guitar is not a score voice in this edition.

**Types and units:** Built-in values use the shared parameter registry's type, unit, and bounds. Authored numeric parameters add host descriptors with their declared or inferred unit and the finite float32 range; the language has no authored parameter bounds syntax. Sampler settings use host descriptors. These descriptors do not extend the kernel parameter ABI. A preset cannot bind another preset or replace an asset, insert chain, or bus. Scene settings retain the engine's existing registry path support; arbitrary authored DSP parameters and sampler configuration are not scene parameters.

**Diagnostics:** `CICADA-PRESET-PARAM` reports unknown or structural parameters; `CICADA-PRESET-TYPE`, `CICADA-PRESET-UNIT`, and `CICADA-PRESET-RANGE` report invalid values; `CICADA-PRESET-TARGET` reports missing or incompatible targets. Each diagnostic carries file, line, and column. Duplicate declarations carry both locations. Library presets follow the same privacy, qualification, and hash checks as other library declarations.

**Example:** [Preset circuit](../../examples/presets/main.cicada) binds authored, acid, and imported presets and overrides a value on the track and in a scene. The imported library is vendored from the [library example](../../examples/libraries/main.cicada), with an added mixer preset.

**Limits:** An authored kit lane cannot apply numeric settings to a built-in recipe through the kit binding; use a `drums` track's lane values or a `builtin.bd` preset instead. Existing limits on effect instances still apply. Studio continues to refuse multi-file editing and has no preset save action in this version. Semantic JSON and generated source contain resolved values; formatting the original source retains preset declarations.
