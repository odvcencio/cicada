# Cicada source edition 1

This reference describes the notation accepted by the current validator. The complete grammar is in the [EBNF appendix](appendix.ebnf). Numeric limits and defaults below describe edition 1 on main.

## Notation conventions

**Status:** Implemented.

**Syntax (EBNF):** A source file is `source_file`: an optional `cicada 1` header followed by zero or more declarations. Declarations use braces; whitespace and newlines separate tokens. See `source_file` and `_declaration` in the appendix.

**Meaning:** Declaration order does not affect name resolution. `//` begins a comment through the end of its line. Semicolons are optional on instrument statements, kit bindings, and drum rows. A vertical bar in a note pattern is a visual group separator; it does not add a step.

**Types and units:** Identifiers are ASCII and case-sensitive. They begin with a lowercase letter or underscore, then contain lowercase letters, digits, underscores, or hyphens. An identifier is at most 64 bytes.

**Defaults:** A score without a header is edition 1. The parser accepts spaces, tabs, CR, and LF between tokens.

**Errors:** Invalid tokens or declaration forms report `CICADA-SYNTAX`. Duplicate declarations report `CICADA-DUPLICATE`; overlong names report `CICADA-LIMIT`.

**Example:**

```cicada
cicada 1
title "Night circuit"
tempo 138
key a minor
seed 4242

track bass acid { cutoff = 620Hz }
pattern pulse acid {
  1^ . 1~ 5
}
scene main { bass = pulse }
song { main*8 }
```

**Edition history:** Edition 1 accepts both headered legacy scores and headerless scores. `cicada fix` can move a standalone legacy header to `cicada.mod`.

## Lexical elements

**Status:** Implemented.

**Syntax (EBNF):** `identifier`, `string`, `integer`, `number`, `fraction`, and `comment` define the tokens. Keywords are lowercase literals. The full token rules are in the appendix.

**Meaning:** A string is double-quoted and may contain escaped characters. A fraction token represents a musical division such as `1/8`, `1/8T`, or `1/8.`. A fraction is accepted as a value; its interpretation depends on the field.

**Types and units:** Integers are decimal digits. Numbers may be signed and may carry `Hz`, `kHz`, `ms`, `s`, `dB`, or `%`. Unit spellings are case-sensitive as listed in the grammar.

**Defaults:** No separators are required between declarations beyond whitespace and braces. Formatting adds readable line breaks.

**Errors:** A malformed token or unterminated string reports `CICADA-SYNTAX`. A syntactically valid number with a wrong unit or range reports `CICADA-UNIT` or `CICADA-PARAM`.

**Example:** The source in [Notation conventions](#notation-conventions) uses identifiers, a string, integers, and a frequency literal. Add comments with `//`.

**Edition history:** Edition 1 accepts lowercase legacy unit spellings as well as the SI spellings shown in this reference. `cicada fix` normalizes recognized unit spelling.

## Source files and projects

**Status:** Implemented.

**Syntax (EBNF):** A `.cicada` file contains one `source_file`. A project manifest is a separate `cicada.mod` file with one `project <name>` line and one `cicada 1` line; blank lines and `#` comments are allowed.

**Meaning:** The nearest `cicada.mod` supplies the project name and source edition. A loose score defaults to edition 1. Project commands such as `check` and `fmt` walk score files under the manifest. Each score is parsed and compiled independently; declarations in one file are not visible in another.

**Types and units:** Manifest project names begin with an ASCII letter or digit and then contain letters, digits, hyphens, or underscores. Source identifiers follow the stricter lowercase rule above.

**Defaults:** `cicada new <name>` creates a project directory containing `cicada.mod` and `main.cicada`.

**Errors:** A manifest must contain exactly one valid project name and one supported `cicada` edition. Unknown or duplicate directives are rejected. Cross-file references report `CICADA-REFERENCE` because there is no shared namespace in the current implementation.

**Example:** The manifest form is `project night-circuit` followed by `cicada 1`; a matching score uses the first example on this page.

**Edition history:** Edition 1 adds the manifest while keeping loose scores and the legacy source header usable. Multi-file shared namespaces are accepted for later implementation; see [accepted syntax](accepted.md#multi-file-projects).

## Edition directive

**Status:** Implemented.

**Syntax (EBNF):** The optional source header is `"cicada" , integer`; the manifest directive is `cicada 1`.

**Meaning:** The source edition controls notation interpretation. It is separate from the semantic JSON format version.

**Types and units:** The only supported edition number is integer `1`.

**Defaults:** A loose source file without a header uses edition 1. A manifest must state its edition.

**Errors:** Any source or manifest edition other than 1 reports `CICADA-VERSION`.

**Example:** `cicada 1` may appear as the first declaration in a legacy score; new project files can rely on `cicada.mod`.

**Edition history:** No prior source edition is supported by this build. Edition 2 has not been implemented.

## Title declaration

**Status:** Implemented.

**Syntax (EBNF):** `title_decl ::= "title" , string`.

**Meaning:** Sets the human-readable project title.

**Types and units:** A Unicode string.

**Defaults:** Empty when omitted.

**Errors:** More than 120 Unicode characters reports `CICADA-LIMIT`.

**Example:** `title "Night circuit"`.

**Edition history:** Available in edition 1.

## Tempo declaration

**Status:** Implemented.

**Syntax (EBNF):** `tempo_decl ::= "tempo" , number`.

**Meaning:** Sets the quarter-note tempo for transport, sequencer, and tempo-synchronized effects.

**Types and units:** Beats per minute. The source number has no unit suffix and may have up to three decimal places.

**Defaults:** `130` BPM.

**Errors:** Values outside 20–300 BPM or with more than three decimal places report `CICADA-PARAM`.

**Example:** `tempo 138`.

**Edition history:** Available in edition 1.

## Key declaration

**Status:** Implemented.

**Syntax (EBNF):** `key_decl ::= "key" , key_root , identifier`.

**Meaning:** Sets the root and scale used to resolve numbered scale degrees.

**Types and units:** Root is A–G with an optional sharp or flat. The scale is one of `minor`, `major`, `dorian`, `phrygian`, `harmonic`, `pent`, `mixo`, or `blues`.

**Defaults:** `key a minor`.

**Errors:** An unknown root or scale reports `CICADA-KEY`. Degrees 2 and 6 are invalid in `pent` and `blues` scales and report `CICADA-SCALE-DEGREE`.

**Example:** `key c# dorian`.

**Edition history:** Available in edition 1.

## Seed declaration

**Status:** Implemented.

**Syntax (EBNF):** `seed_decl ::= "seed" , integer`.

**Meaning:** Sets the deterministic seed inherited by patterns without an explicit seed. It also identifies the random stream used for chance.

**Types and units:** Unsigned 32-bit integer.

**Defaults:** `0`.

**Errors:** A value above 4,294,967,295 reports `CICADA-SEED`.

**Example:** `seed 4242`.

**Edition history:** Available in edition 1.

## Instruments and voices

**Status:** Implemented.

**Syntax (EBNF):** `instrument_decl` contains an optional `instrument_octave`, zero or more `instrument_param` declarations, and one `voice_decl`. A voice contains zero or more ordered `let_stmt` bindings and one `out_stmt`.

**Meaning:** An instrument compiles a bounded expression graph into a mono sound source. A binding may use declared parameters, built-in inputs, and earlier bindings. The output expression must produce audio.

**Types and units:** Parameter units are `unit`, `hz`, `ms`, and `db`. Built-in inputs are `pitch` (Hz), `gate` (gate signal), `velocity` (unit value), and `sample_rate` (Hz). The graph functions are `saw`, `square`, and `sine` (Hz to audio); `noise` (no inputs to audio); `env` (gate and ms to unit); `ladder` and `diode` (audio, Hz, unit to audio); `lowpass` and `highpass` (audio and Hz to audio); `mix` (two audio inputs and unit to audio); `tanh` (audio to audio); `exp2` (unit to unit); and `clamp` (three unit inputs to unit). Expressions are type-checked before playback. The implemented voice mode is `mono`.

**Defaults:** Home octave is 2. A parameter's unit may be inferred from its default literal. `voice mono` is required. A track may override home octave from 0–6 unless its instrument declares a synthesis parameter named `octave`; in that case the track value sets the synthesis parameter and the instrument's home octave remains in effect.

**Errors:** Duplicate or reserved names report `CICADA-DUPLICATE`; unknown symbols report `CICADA-REFERENCE`; incompatible types or units report `CICADA-UNIT`. Graphs above 128 nodes or 32 stateful nodes report `CICADA-LIMIT`. `voice poly` parses but reports `CICADA-UNSUPPORTED`.

**Example:**

```cicada
instrument glassbass {
  octave = 2
  param cutoff: hz = 720Hz
  param bite = 0.65
  voice mono {
    let osc = saw(pitch)
    let shape = env(gate, 380ms)
    out = ladder(osc, cutoff, bite) * shape
  }
}
track lead glassbass { cutoff = 900Hz }
pattern lead-a { 5 . 3 . }
scene main { lead = lead-a }
song { main }
```

**Edition history:** Typed instruments and mono graphs are available in edition 1. Polyphonic voices, arbitrary DSP code, plugins, and sample assets are not implemented.

## Authored kits

**Status:** Implemented.

**Syntax (EBNF):** `kit_decl` contains `kit_binding` entries. A target is an instrument name or `builtin.<lane>`.

**Meaning:** A kit maps drum lanes to compiled mono instruments or built-in drum recipes. An unbound lane is silent.

**Types and units:** Lane names are `bd`, `sd`, `ch`, `oh`, `cp`, `rs`, `lt`, `mt`, `ht`, `cb`, and `cy`. Each instrument binding receives that lane's trigger pitch and hit velocity.

**Defaults:** There are no implicit authored bindings.

**Errors:** Unknown lanes or instrument targets report `CICADA-REFERENCE`; duplicate lane bindings report `CICADA-DUPLICATE`.

**Example:**

```cicada
instrument kick {
  param decay = 250ms
  voice mono {
    out = sine(pitch) * env(gate, decay)
  }
}
kit steel { bd = kick; ch = builtin.ch; }
track kit steel {}
pattern beat drums { bd: X... ch: x.x. }
scene main { kit = beat }
song { main*4 }
```

**Edition history:** Authored kits and the eleven built-in lane names are available in edition 1.

## Tracks and patterns

**Status:** Implemented.

**Syntax (EBNF):** `track_decl` names a built-in voice (`acid` or `drums`), an instrument, or a kit. `acid_pattern`, `note_pattern`, and `drum_pattern` define reusable patterns. A pattern can place attributes before or inside its braces.

**Meaning:** A track owns voice and mixer settings. A scene connects a compatible pattern to a track. Note patterns may omit the `notes` kind word; the compiler infers note patterns for custom instruments. An acid track can play acid or note patterns. A drum or kit track plays drum patterns.

**Types and units:** A pattern has 1–64 expanded steps. Swing is 50–75%; gate is 10–100%; note transpose is −24 to +24 semitones. Each track can use at most 16 patterns. Optional `slot = N` selects a slot from 0 through 15.

**Defaults:** Note kind is inferred when omitted. Pattern defaults are 50% swing, 55% gate, zero transpose, and the project seed. `steps = N` is an optional assertion and must equal the expanded length.

**Errors:** Missing or excess steps report `CICADA-LIMIT`; a mismatched explicit `steps` value or incompatible pattern kind reports `CICADA-PARAM`; unknown attributes and invalid slot values report `CICADA-PARAM`.

**Example:** `pattern pulse acid { swing = 54% 1^ . 1~ 5 }` defines four steps; the main source example shows it bound to a track.

**Edition history:** Edition 1 accepts legacy kind words and header attributes. `cicada fix` removes redundant `acid` or `notes` words and moves settings inside pattern braces. See the [built-in track parameter catalog](built-in-track-parameters.md) for voice-specific names, units, ranges, and defaults.

Unknown track parameters fail semantic validation. This score has exactly one error:

```cicada-invalid CICADA-PARAM
track bass acid { mystery = 1 }
pattern pulse acid { 1 }
scene main { bass = pulse }
song { main }
```

## Pitch, octaves, and degrees

**Status:** Implemented.

**Syntax (EBNF):** `pitch` is a `degree` or `letter_pitch`, optionally followed by octave shifts and modifiers.

**Meaning:** A degree resolves against the score key. A letter pitch is absolute. Each apostrophe raises a note by one octave; each comma lowers it by one octave.

**Types and units:** Letter pitches use A–G, optional `#` or `b`, and optional octave 0–6. Numbered pitches use degree 1–7 with an optional chromatic accidental. MIDI note 60 is C4. Final pitches must remain in MIDI range 0–127.

**Defaults:** An unnumbered letter pitch uses the instrument's home octave, default 2. A built-in acid track also defaults to octave 2. An explicit letter octave is absolute and is not changed by the home octave.

**Errors:** An unsupported degree in a pentatonic or blues scale reports `CICADA-SCALE-DEGREE`; a resolved pitch outside MIDI range reports `CICADA-PARAM`.

**Example:** The note sequence `c#3 1' 5,` combines an absolute pitch, a raised scale degree, and a lowered degree.

**Edition history:** Available in edition 1. Source spelling is retained by the concrete syntax tree; JSON stores resolved MIDI note numbers.

## Drum rows and beats

**Status:** Implemented.

**Syntax (EBNF):** A `drum_pattern` contains `drum_lane` rows. Each row begins with a lane and colon, then contains one hit per step. A trailing semicolon is optional.

**Meaning:** `.` is silence. `x` is a normal hit, `X` is an accented hit, and `x1`–`x9` select stepped velocities. Rows in a pattern have equal length. Unwritten lanes are silent.

**Types and units:** Hits compile to MIDI velocity values from 0–127. The built-in set has eleven lanes listed under [Authored kits](#authored-kits).

**Defaults:** `x` uses velocity 100; `X` uses velocity 127; `x1` through `x9` use 14 through 126 in increments of 14. The pattern's swing and seed defaults also apply to drum patterns.

**Errors:** Unknown lane names report `CICADA-REFERENCE`; row lengths that differ report `CICADA-PARAM`. A nonzero transpose on a drum pattern reports `CICADA-UNSUPPORTED`.

**Example:**

```cicada
track kit drums {}
pattern beat drums {
  bd: X...x...
  sd: ....X...
  ch: x.x.x.x.
}
scene main { kit = beat }
song { main*4 }
```

**Edition history:** Edition 1 accepts rows with or without semicolons and cells either joined or separated by whitespace. The formatter groups long rows in four-step beats.

## Accents, ties, slides, ratchets, and chance

**Status:** Implemented.

**Syntax (EBNF):** `acid_step` accepts rest `.`, tie `-`, separator `|`, or `acid_note`. A note may be followed by `^`, `~`, `*N`, and `?N`; legacy `%N` is also parsed for chance.

**Meaning:** `^` accents a note. `-` extends the preceding note. `~` slides to the next playable note without retriggering the outgoing note. `*N` divides one step into N retriggers. `?N` gives an event an N-percent chance on each seeded pass.

**Types and units:** Ratchet count is 2–8. Written chance is 1–99%; an unmarked event has probability 100%. Modifiers apply to one note or drum hit.

**Defaults:** No accent, slide, or ratchet; probability is 100%. Ties are not hits.

**Errors:** Ratchets outside 2–8 and chances outside 1–99 report `CICADA-PARAM`. A slide followed by a rest has no effect and produces the `CICADA-SLIDE-REST` warning unless a pattern or scene switch supplies the target.

**Example:** `1^ . 3~*2?70 5` accents the first note and gives the third note a two-hit ratchet, slide, and 70% chance.

**Edition history:** `?N` is the current spelling. `%N` remains accepted for edition-1 compatibility; `cicada fix` rewrites `%N` to `?N`.

## SI literals and musical units

**Status:** Implemented.

**Syntax (EBNF):** `number` accepts a number with an optional unit suffix; `fraction` accepts a note division.

**Meaning:** Frequency literals accept Hz or kHz; time literals accept ms or s; level literals accept dB. Percent literals express swing, gate, and unit values where specified. Fractions are used for tempo-synced delay divisions.

**Types and units:** The semantic layer stores frequency in Hz, time in milliseconds where required by source parameters, dB in decibels, ratios as unitless values, tempo in milli-BPM, and note divisions as named rhythmic values.

**Defaults:** Unitless fields remain unitless; a suffix does not change a field's range.

**Errors:** A recognized value with an incompatible unit reports `CICADA-UNIT` or `CICADA-PARAM`. A delay division must fit the fixed four-second delay buffer at the score tempo.

**Example:** `cutoff = 2kHz`, `decay = 0.3s`, `level = -6dB`, and `swing = 56%`.

**Edition history:** The formatter emits `Hz`, `kHz`, and `dB` SI capitalization. Lowercase legacy suffixes remain accepted in edition 1.

## Scenes and actions

**Status:** Implemented.

**Syntax (EBNF):** A `scene_decl` contains zero or more `scene_assignment` entries of the form `track = pattern`.

**Meaning:** A named pattern starts or replaces the pattern on that track. `off` stops the track. `keep` leaves that track unchanged when a scene launches. An omitted track entry also keeps its current state.

**Types and units:** The left side names a declared track. The right side names a compatible pattern or the `off`/`keep` action.

**Defaults:** An omitted track is kept. A newly started transport begins at the first song entry.

**Errors:** Duplicate assignments report `CICADA-DUPLICATE`; missing track or pattern names report `CICADA-REFERENCE`.

**Example:** `scene drop { bass = pulse drums = off }`.

**Edition history:** `stop` is accepted as the canonical stop word when no pattern named `stop` exists. `off` remains valid in edition 1; `cicada fix` rewrites it to `stop` when that spelling is unambiguous.

## Parameter paths

**Status:** Implemented.

**Syntax (EBNF):** A `parameter_path` has the form `owner.setting` and is accepted as a scene target; see `parameter_path`, `scene_target`, and `scene_assignment` in the [EBNF appendix](appendix.ebnf). Its first part names the owner. The remaining part names a registered setting and may contain another dot, as in `bass.send.delay`.

**Meaning:** A path addresses a registered setting on a declared track or effect. Track and effect names share the owner namespace, so an owner must resolve to exactly one of them. Effect paths use the declared effect name, such as `delay.feedback`. Track paths use the track name, such as `bass.cutoff` or `beat.bd_tune`. The send paths `bass.send.delay` and `bass.send.reverb` address the existing delay and reverb sends, which the track block spells `send_a` and `send_b`. Drum lane controls keep their flat suffixes, such as `bd_tune`.

**Types and units:** The parameter registry sets each path's type, unit, range, default, and whether it can change live. Numeric scene values use the registered unit and range; toggles and enumerations use their registered words. Track voice parameters are listed in the [built-in track parameter catalog](built-in-track-parameters.md); effect values are listed under [Effects](#effects). Scene settings can target only live parameters.

**Defaults:** Each path starts with its registry default. A track or effect block value overrides that default. A scene setting overrides the block value when that scene lands, and remains active until another scene sets the same path. For example, `bass.cutoff` defaults to 600 Hz, while the acid track block may set it to 700 Hz.

**Errors:** An unknown owner reports `CICADA-REFERENCE`; if a track and effect have the same name, the ambiguous owner also reports `CICADA-REFERENCE` with a rename hint. An unknown setting reports `CICADA-PARAM`. A duplicate path in one scene reports `CICADA-DUPLICATE`. A wrong unit, value type, or range reports `CICADA-UNIT`. A registered but non-live setting, or any path owned by reserved `master`, reports `CICADA-UNSUPPORTED`.

**Example:** Set the existing sends through their paths:

```cicada
fx delay {}
fx reverb {}
track bass acid { send_a = 0.2 send_b = 0.1 }
pattern pulse acid { 1 . 1 . }
scene main {
  bass = pulse
  bass.send.delay = 0.45
  bass.send.reverb = 0.3
}
song { main*4 }
```

Drum lane parameters use the named drum track:

```cicada
track beat drums {}
pattern drum-loop drums { bd: X... }
scene main { beat = drum-loop beat.bd_tune = 48Hz }
song { main }
```

Use `cicada explain score.cicada bass.cutoff @3.2.4` to inspect the registry default, block value, active scene value, and computed value at a song location.

**Edition history:** Parameter paths and live scene settings are additive edition-1 features. They use `cicada.project/2` when a semantic project contains scene settings. The `master` owner remains reserved; see [accepted syntax](accepted.md#named-mixer-pieces).

## Scene parameter settings

**Status:** Implemented.

**Syntax (EBNF):** A scene assignment is `scene_target = scene_value`. The target is either a track identifier for a pattern binding or a `parameter_path` for a setting. The appendix defines the accepted number, identifier, string, and fraction value tokens.

**Meaning:** A scene applies its settings when it lands at the scene switch, together with its pattern bindings. A setting takes effect at that boundary and uses the registered live smoothing. Later scenes carry the last value forward when they omit that path; a scene changes it only by setting the same path again. `level = off` disables a track through the engine's layer mask. It is not a numeric level value.

**Types and units:** A setting must resolve to a live registry entry for its track or effect. Numeric values use that entry's unit and range. Because the engine applies scene values as float32, a non-boundary value that rounds outside the registered range reports `CICADA-PARAM`. An exact minimum or maximum that rounds outside the range moves inward by one float32 step. Toggle and enumeration settings use the registered values, and `off` is available where the entry permits it. A `delay.time` division stays synced to tempo when a scene applies it; the engine crossfades to the new delay time. `drums.level = off` turns off the whole drum track; drum lane level paths such as `drums.bd_level` address one lane.

**Defaults:** The precedence is registry default, then the track or effect block, then a scene setting. Once a scene changes a path, that value carries through following scenes until another scene sets it. An omitted setting does not restore the block value or registry default.

**Errors:** Duplicate settings for one path in a scene report `CICADA-DUPLICATE`. Unknown owners report `CICADA-REFERENCE`; ambiguous track/effect owners report `CICADA-REFERENCE` and a rename hint. Unknown settings report `CICADA-PARAM`; wrong values or units report `CICADA-UNIT`. A non-boundary numeric value that rounds outside its float32 range, or a synced delay division that exceeds the four-second buffer at the score tempo, reports `CICADA-PARAM`. A registered value with no engine representation reports `CICADA-UNSUPPORTED` and names its path and value. Non-live paths and reserved `master` paths report `CICADA-UNSUPPORTED`.

**Example:** The second scene leaves `bass.cutoff`, `drums.level`, and the synced delay division at the values set by the first scene:

```cicada
fx delay {}
track bass acid { cutoff = 700Hz send_a = 0.2 }
track drums drums { level = -4dB }
pattern pulse acid { 1 . 1 . }
pattern beat drums { bd: X... }
scene verse {
  bass = pulse
  drums = beat
  bass.cutoff = 900Hz
  drums.level = off
  delay.time = 1/8
}
scene chorus {
  bass = pulse
  drums = beat
}
song { verse*4 chorus*4 }
```

This invalid scene uses an owner shared by a track and an effect:

```cicada-invalid CICADA-REFERENCE
fx delay { feedback = 0.2 }
track delay acid {}
pattern riff acid { 1 }
scene main { delay = riff delay.feedback = 0.3 }
song { main }
```

This invalid scene targets an effect setting that is registered but not live:

```cicada-invalid CICADA-UNSUPPORTED
fx drive { shape = soft }
track bass acid {}
pattern riff acid { 1 }
scene main { bass = riff drive.shape = hard }
song { main }
```

This in-range value is rejected because it rounds below the drum tune's float32 minimum:

```cicada-invalid CICADA-PARAM
track beat drums {}
pattern riff drums steps=1 { sd: X }
scene main { beat=riff beat.sd_tune=0.7000000000000001 }
song { main }
```

**Edition history:** Scene parameter settings were added to edition 1 without changing pattern binding syntax. At a song scene switch they land with the scene and carry forward until overridden.

## Songs

**Status:** Implemented.

**Syntax (EBNF):** `song_decl` is a sequence of `song_entry` values. Each entry is a scene name with optional `*N` bar count.

**Meaning:** Entries play in order. The song repeats according to the transport's loop behavior.

**Types and units:** Each entry lasts 1–999 bars.

**Defaults:** An entry without `*N` lasts one bar.

**Errors:** Empty songs, unknown scene names, and durations outside 1–999 report `CICADA-LIMIT` or `CICADA-REFERENCE`.

**Example:** `song { intro*4 verse*8 chorus*8 }`.

**Edition history:** Available in edition 1. Editing a song's order or duration in Studio patches only the `song` declaration.

## Pattern chains

**Status:** Accepted; not available in the current build.

**Syntax (EBNF):** See `chain_decl` in [accepted syntax](accepted.md#flexible-grid-and-pattern-chains).

**Meaning:** A track chain plays a listed sequence of patterns, repeating the chain after the last entry. The runtime engine supports chain playback; edition 1 source does not yet declare chains.

**Types and units:** Each item names a pattern assigned to that track. The accepted source design caps a chain at 32 entries.

**Defaults:** No source chain is configured.

**Errors:** The accepted design requires valid patterns and a chain within the runtime limit. Source diagnostics are not implemented.

**Example:** `chain = intro verse chorus`.

**Edition history:** Chain playback exists in the engine; source syntax is accepted for a later additive edition-1 change and has not merged.

## Phrases and the generator

**Status:** Implemented.

**Syntax (EBNF):** `phrase_decl` names a sequence of acid steps. `phrase_use` inserts it into a note pattern with optional `*N` repetition and optional `transpose=N` or `+N`.

**Meaning:** Phrase uses expand before scheduling. The source model retains the phrase definition and use; the semantic project stores expanded notes.

**Types and units:** Repeat count is 1–64. Transpose is an integer number of semitones from −24 to +24. The expanded pattern remains within 64 steps.

**Defaults:** A phrase without the optional `acid` word is an acid phrase. A use repeats once and does not transpose. `cicada gen` defaults to 16 steps, key C, minor scale, seed 0, swing 54%, and an AABA structure.

**Errors:** Unknown phrase names report `CICADA-REFERENCE`; invalid repeat or transpose reports `CICADA-USE`; expansion above 64 steps reports `CICADA-EXPANSION`.

**Example:**

```cicada
track bass acid {}
phrase hook { 1^ . 1~ 5 }
pattern pulse { use hook*2 }
scene main { bass = pulse }
song { main*8 }
```

**Edition history:** Edition 1 accepts optional phrase kind and transpose spellings. `cicada fix` removes redundant `acid` and normalizes positive transposes to `+N`.

## Phrase generator command

**Status:** Implemented.

**Syntax:** `cicada gen` takes flags: `--seed N --key ROOT --scale SCALE`, with optional `--root-octave`, `--steps`, density controls, `--swing`, `--gate`, `--structure`, `--rest-downbeat`, `-o FILE`, and `--trace`.

**Meaning:** The generator creates a deterministic, parseable acid score from the seed and music settings. It writes source to stdout unless `-o` names a new output file. A trace writes the random draw log as JSON to stdout.

**Types and units:** The generator seed is unsigned 64-bit. Key roots are C–B with sharps; scales are the eight edition-1 scales. Effective step counts are 8, 16, 32, or 64. Density, accent density, slide density, and octave-jump density are 0–1. Swing is 50–75% to two decimal places; gate is 10–100%. Structures are A, AABA, ABAB, ABAC, and AAAB. Root octave is 1–4.

**Defaults:** Seed 0, key C minor, root octave 2, 16 steps, density 0.6, accent density 0.5, slide density 0.4, octave-jump density 0.3, swing 54%, gate 55%, and AABA structure. The emitted score's project seed is stored as a 32-bit value.

**Errors:** Invalid keys, scales, structures, steps, ranges, or non-finite density values fail. `--trace` requires `-o` so the source and JSON trace have separate outputs.

**Example:**

```sh
cicada gen --seed 4242 --key a --scale minor --trace -o generated.cicada
```

**Edition history:** The generator emits edition-1 source and runs before the audio callback. Its output is parsed and compiled by the same project pipeline as authored scores.

## Effects

**Status:** Implemented.

**Syntax (EBNF):** `fx_decl` contains named parameter assignments. The supported effect names are `drive`, `delay`, `reverb`, and `comp`.

**Meaning:** `drive` is a track insert; `delay` and `reverb` are fixed send returns; `comp` processes the music bus after returns and before the master limiter.

**Types and units:** Drive uses shape (`soft`, `hard`, `fold`, `diode`), gain (0–36 dB), tone (1–20 kHz), and mix (0–1). Delay uses free time (1–2000 ms) or one of `1/32`, `1/16`, `1/16T`, `1/16.`, `1/8`, `1/8T`, `1/8.`, `3/16`, `1/4`, `1/4.`, `1/2`; feedback (0–0.95), damp (1–16 kHz), width and mix (0–1), and a boolean ping-pong setting. A synced division must fit the four-second delay buffer at the score tempo. Reverb uses size (0.5–1.5), decay (0.3–12 s), damp (2–16 kHz), highpass (40–400 Hz), predelay (0–200 ms), and mix (0–1). Compressor uses peak/RMS detection, threshold (−40–0 dB), ratio (1–20), knee (0–12 dB), attack (0.1–100 ms), release (10–1000 ms), auto or −24–24 dB makeup, optional sidechain track, and mix (0–1).

**Defaults:** Drive: soft, 0 dB, 12 kHz, fully wet. Delay: 1/8, feedback 0.35, damp 6 kHz, ping-pong off, width 1, fully wet. Reverb: size 1, decay 2.4 s, damp 8 kHz, highpass 120 Hz, no predelay, fully wet. Compressor: peak, −18 dB, 4:1, 6 dB knee, 10 ms attack, 100 ms release, auto makeup, fully wet.

**Errors:** Unknown effects report `CICADA-UNSUPPORTED`. Unknown parameter names, wrong units, out-of-range values, or unsupported tempo divisions report `CICADA-PARAM` or `CICADA-UNIT`.

**Example:** The [FX bus example](../../examples/fx-bus.cicada) declares drive, delay, and reverb and routes tracks through the current fixed sends.

**Edition history:** These four built-in effects are available in edition 1. Arbitrary effect graphs and per-track compressor inserts are not implemented.

## Current mixer

**Status:** Implemented.

**Syntax (EBNF):** Mixer values use `param_decl` inside a `track_decl`; the current source keys are `level`, `pan`, `insert`, `send_a`, `send_b`, `send_pre`, and `bus`.

**Meaning:** Track level and pan control the dry path. `insert` selects `none` or the declared `drive` effect. `send_a` routes to delay and `send_b` to reverb. `send_pre = true` taps both sends before level and pan. `bus` selects the music or SFX bus. The SFX bus joins after music compression; its tracks can feed the compressor sidechain.

**Types and units:** Level is dB or `off`; pan is unitless; send amounts are unit values; `send_pre` is boolean; bus is `music` or `sfx`.

**Defaults:** Level −6 dB, pan 0, no insert, both sends 0, post-fader sends, output bus `music`. The master path has a linked stereo limiter with a −0.3 dBFS ceiling and 1.5 ms lookahead.

**Errors:** Level must be −60 to +6 dB; pan −1 to +1; sends 0–1. `send_pre` requires a nonzero send. `mute` and `solo` source fields are reserved and report an error in this edition.

**Example:** `track bass acid { level = -3dB pan = -0.2 insert = drive send_a = 0.3 bus = music }`.

**Edition history:** Edition 1 has two fixed send returns, one drive insert, music/SFX buses, and a master limiter. Named mixer pieces and a master insert chain are accepted but not yet available; see [accepted syntax](accepted.md#named-mixer-pieces).
