; Cicada highlight query.
;
; Every leaf in a Cicada CST receives exactly one capture. Where one token
; means different things in different places ("-" is a tie in a pattern and
; subtraction in an expression; "," lowers an octave or separates arguments),
; the parent node decides. Predicates split built-in names from user names, so
; no two patterns claim the same node. The query therefore gives the same
; result in editors that prefer the first matching pattern and in editors that
; prefer the last.
;
; Captures follow the Neovim naming scheme. Musical meaning rides on dotted
; suffixes (@constant.pitch.degree, @number.frequency, ...). An editor that
; does not know a suffix falls back to its base capture. Three enclosing-node
; captures layer style over whole notes: an accent is @markup.strong, a slide
; is @markup.italic, and a ratchet is @markup.underline.

; ---------------------------------------------------------------------------
; Layers: whole-note styling. These come first so leaf captures, which carry
; the colors, take precedence in last-match editors.

(acid_note (modifier "^")) @markup.strong
(chord_note (modifier "^")) @markup.strong
(acid_note (modifier "~")) @markup.italic
(acid_note (modifier (ratchet))) @markup.underline
(drum_hit (accent_hit)) @markup.strong
(drum_hit (ratchet)) @markup.underline

; ---------------------------------------------------------------------------
; Source header: edition is selected by the project manifest; the optional source
; marker is highlighted when present. Title, tempo, key, and seed are declarations.

"cicada" @keyword.directive
(source_file (integer) @number.version)

[
  "title"
  "tempo"
  "key"
  "seed"
] @keyword

(title_decl (string) @markup.heading)

; The key root is a pitch class. The scale decides what the degrees 1-7 mean,
; so it reads as the type of every degree in the score.
(key_decl root: (key_root) @constant.pitch.root)
((key_decl scale: (identifier) @type.builtin)
  (#any-of? @type.builtin "minor" "major" "dorian" "phrygian" "harmonic" "pent" "mixo" "blues"))
((key_decl scale: (identifier) @type)
  (#not-any-of? @type "minor" "major" "dorian" "phrygian" "harmonic" "pent" "mixo" "blues"))

(seed_decl (integer) @number.seed)

; ---------------------------------------------------------------------------
; Instruments: typed parameters, one voice, ordered lets, one out.

"instrument" @keyword.type
"octave" @keyword
"param" @keyword
"voice" @keyword.function
"let" @keyword
"out" @keyword.return

(instrument_decl name: (identifier) @type.definition)
(instrument_octave value: (integer) @number)
(instrument_param name: (identifier) @variable.parameter)
(instrument_param unit: (identifier) @type.builtin)
(voice_decl mode: (identifier) @keyword.modifier)
(let_stmt name: (identifier) @variable)

; Built-in voice inputs. Parameter and let references are refined by
; locals.scm, which gives each reference the capture of its definition.
((expression (identifier) @variable.builtin)
  (#any-of? @variable.builtin "pitch" "gate" "velocity" "sample_rate"))
((expression (identifier) @variable)
  (#not-any-of? @variable "pitch" "gate" "velocity" "sample_rate"))

; Graph primitives.
((call_expr function: (identifier) @function.builtin)
  (#any-of? @function.builtin
    "saw" "square" "sine" "noise" "env" "adsr" "pulse" "svf" "ladder" "diode"
    "lowpass" "highpass" "mix" "tanh" "exp2" "clamp"))
((call_expr function: (identifier) @function.call)
  (#not-any-of? @function.call
    "saw" "square" "sine" "noise" "env" "adsr" "pulse" "svf" "ladder" "diode"
    "lowpass" "highpass" "mix" "tanh" "exp2" "clamp"))

(expression ["+" "-" "*" "/"] @operator)
("->" @operator)
(call_expr "," @punctuation.delimiter)

; ---------------------------------------------------------------------------
; Tracks and effects: `track <name> <voice> { key = value }`.

"track" @keyword
"fx" @keyword.type
"bus" @keyword
"master" @keyword
"export" @keyword
"send" @keyword
"pre" @keyword.modifier
"insert" @keyword

(fx_decl kind: (identifier) @type.builtin)
(bus_decl name: (identifier) @type.definition)
(export_decl name: (identifier) @label)
(send_decl to: (identifier) @variable.member)
(insert_chain first: (identifier) @variable.member)
(insert_chain next: (identifier) @variable.member)

(track_decl name: (identifier) @variable.member)
((track_decl kind: (identifier) @type.builtin)
  (#any-of? @type.builtin "acid" "drums" "audio"))
((track_decl kind: (identifier) @type)
  (#not-any-of? @type "acid" "drums" "audio"))
(fx_decl name: (identifier) @type.definition)

; Authored kits bind lanes to instrument code, built-in voices, or modeled pieces.
"kit" @keyword.type
"builtin" @keyword.builtin
"model" @keyword.builtin
(kit_decl name: (identifier) @type.definition)
(kit_binding lane: (identifier) @tag.builtin)
(kit_target instrument: (identifier) @type)
(kit_target voice: (identifier) @tag.builtin)
(kit_target "." @punctuation.delimiter)

(param_decl name: (identifier) @property)
((value (identifier) @boolean)
  (#any-of? @boolean "on" "off" "true" "false"))
((value (identifier) @constant)
  (#not-any-of? @constant "on" "off" "true" "false"))
(value (string) @string)
(value (fraction) @number.fraction)

; ---------------------------------------------------------------------------
; Phrases and patterns. A phrase is a macro: `use` splices its steps into a
; pattern before scheduling.

"phrase" @keyword.directive.define
"pattern" @keyword.function
"use" @keyword.import
"transpose" @attribute.builtin
(phrase_use "+" @operator.transpose)

[
  "acid"
  "notes"
  "drums"
] @type.builtin

(phrase_decl name: (identifier) @function.macro)
(acid_pattern name: (identifier) @function)
(note_pattern name: (identifier) @function)
(drum_pattern name: (identifier) @function)
(pattern_attr name: (identifier) @attribute)

(phrase_use name: (identifier) @function.macro)
(phrase_use "*" @operator.repeat)
(phrase_use repeat: (integer) @number.repeat)

; ---------------------------------------------------------------------------
; Steps: rests, ties, bar lines, pitches, and their modifiers.

(acid_step "." @punctuation.special.rest)
(acid_step "-" @punctuation.special.tie)
(acid_step "|" @punctuation.delimiter.bar)

(degree) @constant.pitch.degree
(letter_pitch) @constant.pitch.letter
(octave_shift "'" @operator.octave.up)
(octave_shift "," @operator.octave.down)

(modifier "^" @operator.accent)
(modifier "~" @operator.slide)
(ratchet "*" @operator.ratchet)
(ratchet (integer) @number.ratchet)
(probability "%" @operator.probability)
(probability "?" @operator.probability)
(probability (integer) @number.probability)

; ---------------------------------------------------------------------------
; Drum grids: one `lane: hits` row per voice.

((drum_lane name: (drum_lane_label) @tag.builtin)
  (#any-of? @tag.builtin "bd:" "sd:" "ch:" "oh:" "cp:" "rs:" "lt:" "mt:" "ht:" "cb:" "cy:"))
((drum_lane name: (drum_lane_label) @tag)
  (#not-any-of? @tag "bd:" "sd:" "ch:" "oh:" "cp:" "rs:" "lt:" "mt:" "ht:" "cb:" "cy:"))

((drum_lane name: (identifier) @tag.builtin)
  (#any-of? @tag.builtin "bd" "sd" "ch" "oh" "cp" "rs" "lt" "mt" "ht" "cb" "cy"))
((drum_lane name: (identifier) @tag)
  (#not-any-of? @tag "bd" "sd" "ch" "oh" "cp" "rs" "lt" "mt" "ht" "cb" "cy"))

(drum_hit "." @punctuation.special.rest)
(hit) @constant.hit
(accent_hit) @constant.hit.accent
(velocity_hit) @constant.hit.velocity

; ---------------------------------------------------------------------------
; Arrangement: scenes bind patterns to tracks; the song plays scenes for bars.

"scene" @keyword
"song" @keyword

(scene_decl name: (identifier) @label)
(scene_assignment target: (scene_target (identifier) @variable.member))
(parameter_path) @property.parameter_path
((scene_assignment value: (scene_value (identifier) @constant.builtin))
  (#any-of? @constant.builtin "off" "keep"))
; Edition 1 can still have a pattern named `stop`. Use a neutral capture until
; semantic highlighting can distinguish that legacy reference from the verb.
((scene_assignment value: (scene_value (identifier) @constant))
  (#eq? @constant "stop"))
((scene_assignment value: (scene_value (identifier) @function))
  (#not-any-of? @function "off" "keep" "stop"))

(song_entry scene: (identifier) @label)
(song_entry "*" @operator.repeat)
(song_entry bars: (integer) @number.bars)

; ---------------------------------------------------------------------------
; Numbers carry their unit: 620Hz, 380ms, 3s, -6dB, 50%.

((number) @number.frequency (#match? @number.frequency "[Hh]z$"))
((number) @number.duration (#match? @number.duration "[0-9]m?s$"))
((number) @number.decibel (#match? @number.decibel "(db|dB)$"))
((number) @number.loudness (#match? @number.loudness "(LU|LUFS)$"))
((number) @number.true_peak (#match? @number.true_peak "dBTP$"))
((number) @number.percent (#match? @number.percent "%$"))
((number) @number.float (#match? @number.float "^-?[0-9]+\\.[0-9]+$"))
((number) @number (#match? @number "^-?[0-9]+$"))

; ---------------------------------------------------------------------------
; Punctuation and comments.

"=" @operator
[":" ";"] @punctuation.delimiter
["{" "}" "(" ")" "[" "]"] @punctuation.bracket

(comment) @comment

["asset" "clip" "sampler"] @keyword.type
(asset_decl name: (identifier) @type.definition)
(asset_decl path: (string) @string.special.path)
(clip_decl name: (identifier) @function)
(clip_decl asset: (identifier) @type)
(sampler_decl name: (identifier) @type.definition)
((number) @number.frames (#match? @number.frames "frames$"))

; Live host controls.
["live" "land" "macro" "smooth" "layers" "attack" "release" "state" "stinger" "transition" "quantize" "crossfade"] @keyword
(live_land value: (identifier) @constant.builtin)
(bar_count) @number.bars
(live_macro name: (identifier) @variable.parameter)
(live_layers macro: (identifier) @variable.parameter)
(live_layer track: (identifier) @variable.member)
">=" @operator

"import" @keyword.directive
(import_decl path: (string) @string)

(qualified_name (identifier) @property.parameter_path)
(qualified_name "." @punctuation.delimiter)

(live_state name: (identifier) @variable.parameter)
(live_state scene: (identifier) @label)
(live_stinger name: (identifier) @variable.parameter)
(live_stinger track: (identifier) @variable.member)
(live_stinger pattern: (identifier) @label)
(live_stinger quantize: (identifier) @constant.builtin)
(live_transition from: (identifier) @variable.parameter)
(live_transition to: (identifier) @variable.parameter)
(live_transition quantize: (identifier) @constant.builtin)
(live_stinger "." @punctuation.delimiter)
"preset" @keyword.type
(preset_decl name: (identifier) @type.definition)
["arrange" "place" "marker"] @keyword
(place_decl name: (identifier) @function)
(place_decl track: (identifier) @variable.member)
(place_decl content: (identifier) @function.call)
(marker_decl name: (identifier) @constant)
(musical_position) @number.position
(tick_count) @number.ticks
