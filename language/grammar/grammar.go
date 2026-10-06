// Package grammar defines the Cicada grammar in the grammargen Go DSL. It is
// the source of notation/cicada.bin: run `go generate ./notation` after a
// change, and `make grammar-check` to verify the blob and the editor queries.
//
// Only the generator and tests import this package. The parser loads the
// generated blob, so programs that parse scores do not link grammargen.
//
//go:generate go run ../../cmd/cicada-ebnf -o ../../docs/spec/appendix.ebnf
package grammar

import "github.com/odvcencio/gotreesitter/grammargen"

// The DSL reads like tree-sitter's grammar.js.
var (
	seq      = grammargen.Seq
	choice   = grammargen.Choice
	repeat   = grammargen.Repeat
	optional = grammargen.Optional
	str      = grammargen.Str
	sym      = grammargen.Sym
	pat      = grammargen.Pat
	field    = grammargen.Field
	token    = grammargen.Token
	prec     = grammargen.Prec
	precLeft = grammargen.PrecLeft
	commaSep = grammargen.CommaSep
)

// Cicada returns the grammar for Cicada 1 scores. Rule order fixes symbol
// ids, so reordering rules changes the generated blob.
func Cicada() *grammargen.Grammar {
	g := grammargen.NewGrammar("cicada")

	// The legacy version directive is optional; a project manifest can own the edition.
	g.Define("source_file", seq(optional(seq(str("cicada"), sym("integer"))), repeat(sym("_declaration"))))
	g.Define("_declaration", choice(
		sym("title_decl"), sym("tempo_decl"), sym("key_decl"), sym("seed_decl"),
		sym("instrument_decl"), sym("kit_decl"), sym("track_decl"), sym("phrase_decl"),
		sym("acid_pattern"), sym("note_pattern"), sym("drum_pattern"),
		sym("scene_decl"), sym("song_decl"), sym("fx_decl"), sym("bus_decl"),
		sym("master_decl"), sym("export_decl"), sym("asset_decl"), sym("clip_decl"), sym("sampler_decl"), sym("live_decl"),
	))

	// Edition-2 audio declarations share typed key/value bodies.
	g.Define("asset_decl", seq(str("asset"), field("name", sym("identifier")), field("path", sym("string")), str("{"), repeat(sym("param_decl")), str("}")))
	g.Define("clip_decl", seq(str("clip"), field("name", sym("identifier")), field("asset", sym("identifier")), str("{"), repeat(sym("param_decl")), str("}")))
	g.Define("sampler_decl", seq(str("sampler"), field("name", sym("identifier")), str("{"), repeat(sym("param_decl")), str("}")))

	// Header: `title "Night circuit"`, `tempo 138`, `key a minor`, `seed 4242`.
	g.Define("title_decl", seq(str("title"), sym("string")))
	g.Define("tempo_decl", seq(str("tempo"), sym("number")))
	g.Define("key_decl", seq(str("key"), field("root", sym("key_root")), field("scale", sym("identifier"))))
	g.Define("seed_decl", seq(str("seed"), sym("integer")))

	// `track <name> <voice> { key = value }` binds a name to the built-in acid
	// or drums voice, or to a declared instrument.
	g.Define("track_decl", seq(
		str("track"), field("name", sym("identifier")), field("kind", sym("identifier")),
		str("{"), repeat(sym("mix_setting")), str("}"),
	))

	// An instrument declares typed parameters and one voice. The voice binds
	// ordered lets and ends with the audio it outputs.
	g.Define("instrument_decl", seq(
		str("instrument"), field("name", sym("identifier")),
		str("{"), optional(sym("instrument_octave")), repeat(sym("instrument_param")), sym("voice_decl"), str("}"),
	))
	g.Define("instrument_octave", seq(str("octave"), str("="), field("value", sym("integer")), optional(str(";"))))
	// A kit binds drum lanes to instruments, built-in voices, or modeled pieces.
	g.Define("kit_decl", seq(
		str("kit"), field("name", sym("identifier")),
		str("{"), repeat(sym("kit_binding")), str("}"),
	))
	g.Define("kit_binding", seq(
		field("lane", sym("identifier")), str("="), field("target", sym("kit_target")), optional(str(";")),
	))
	g.Define("kit_target", choice(
		field("instrument", sym("identifier")),
		seq(str("builtin"), str("."), field("voice", sym("identifier"))),
		seq(str("model"), str("."), field("voice", sym("identifier"))),
	))
	g.Define("instrument_param", seq(
		str("param"), field("name", sym("identifier")), optional(seq(str(":"), field("unit", sym("identifier")))),
		str("="), field("default", sym("number")), optional(str(";")),
	))
	g.Define("voice_decl", seq(
		str("voice"), field("mode", sym("identifier")),
		str("{"), repeat(sym("let_stmt")), sym("out_stmt"), str("}"),
	))
	g.Define("let_stmt", seq(str("let"), field("name", sym("identifier")), str("="), field("value", sym("expression")), optional(str(";"))))
	g.Define("out_stmt", seq(str("out"), str("="), field("value", sym("expression")), optional(str(";"))))
	g.Define("expression", choice(
		precLeft(1, seq(field("left", sym("expression")), choice(str("+"), str("-")), field("right", sym("expression")))),
		precLeft(2, seq(field("left", sym("expression")), choice(str("*"), str("/")), field("right", sym("expression")))),
		sym("call_expr"),
		sym("number"),
		sym("identifier"),
		seq(str("("), sym("expression"), str(")")),
	))
	g.Define("call_expr", seq(field("function", sym("identifier")), str("("), commaSep(sym("expression")), str(")")))

	// Parameters of tracks and effects. Mixer routing has its own typed source
	// forms; unsupported routes still parse so validation can name the exact
	// construct instead of reporting a syntax error.
	g.Define("param_decl", seq(field("name", sym("identifier")), str("="), field("value", sym("value"))))
	g.Define("send_decl", seq(str("send"), field("to", sym("identifier")), str("="), field("level", sym("number")), optional(field("tap", str("pre")))))
	g.Define("mix_setting", choice(sym("send_decl"), sym("param_decl")))
	g.Define("fx_decl", seq(str("fx"), field("name", sym("identifier")), optional(field("kind", sym("identifier"))), str("{"), repeat(sym("param_decl")), str("}")))
	g.Define("bus_decl", seq(str("bus"), field("name", sym("identifier")), str("{"), repeat(sym("mix_setting")), str("}")))
	g.Define("master_decl", seq(str("master"), str("{"), repeat(sym("mix_setting")), str("}")))
	g.Define("export_decl", seq(str("export"), field("name", sym("identifier")), str("{"), repeat(sym("param_decl")), str("}")))

	// A phrase is a named run of steps that `use` splices into a pattern
	// before scheduling, optionally repeated and transposed.
	g.Define("phrase_decl", seq(
		str("phrase"), field("name", sym("identifier")), optional(str("acid")),
		str("{"), repeat(sym("acid_step")), str("}"),
	))
	g.Define("acid_pattern", seq(
		str("pattern"), field("name", sym("identifier")), str("acid"), repeat(sym("pattern_attr")),
		str("{"), repeat(sym("pattern_attr")), repeat(choice(sym("acid_step"), sym("phrase_use"))), str("}"),
	))
	g.Define("note_pattern", seq(
		str("pattern"), field("name", sym("identifier")), optional(str("notes")), repeat(sym("pattern_attr")),
		str("{"), repeat(sym("pattern_attr")), repeat(choice(sym("acid_step"), sym("phrase_use"))), str("}"),
	))
	g.Define("phrase_use", seq(
		str("use"), field("name", sym("identifier")),
		optional(seq(str("*"), field("repeat", sym("integer")))),
		optional(choice(
			seq(str("transpose"), str("="), field("transpose", sym("number"))),
			seq(str("+"), field("transpose", sym("number"))),
		)),
	))
	g.Define("drum_pattern", seq(
		str("pattern"), field("name", sym("identifier")), str("drums"), repeat(sym("pattern_attr")),
		str("{"), repeat(sym("pattern_attr")), repeat(sym("drum_lane")), str("}"),
	))
	g.Define("pattern_attr", seq(field("name", sym("identifier")), str("="), field("value", sym("number"))))

	// A step is a rest, a tie, a bar line (which takes no time), or a note.
	// A note is a scale degree or a letter pitch, then octave marks, then
	// modifiers: accent ^, slide ~, ratchet *n, and chance ?n (%n is legacy).
	g.Define("acid_step", choice(str("."), str("-"), str("|"), sym("acid_note")))
	g.Define("acid_note", seq(field("pitch", sym("pitch")), repeat(sym("octave_shift")), repeat(sym("modifier"))))
	g.Define("pitch", choice(sym("degree"), sym("letter_pitch")))
	g.Define("degree", token(pat(`[1-7][#b]?`)))
	g.Define("letter_pitch", token(pat(`[a-g][#b]?[0-6]?`)))
	g.Define("octave_shift", choice(str("'"), str(",")))
	g.Define("modifier", choice(str("^"), str("~"), sym("ratchet"), sym("probability")))

	// Joined labels outrank adjacent hit tokens when semicolons are omitted.
	// The identifier form keeps legacy whitespace and comments before the colon.
	g.Define("drum_lane", seq(
		choice(field("name", sym("drum_lane_label")), seq(field("name", sym("identifier")), str(":"))),
		repeat(sym("drum_hit")), optional(str(";")),
	))
	g.Define("drum_lane_label", token(prec(3, pat(`[a-z][a-z0-9_-]*:`))))
	g.Define("drum_hit", seq(choice(str("."), sym("hit"), sym("accent_hit"), sym("velocity_hit")), optional(sym("ratchet")), optional(sym("probability"))))
	// A hit also admits x1-x9 so keyword extraction never claims it. A keyword
	// in a drum row would bring the identifier word token with it and swallow
	// rows like xx.. whole. Hits outrank identifiers, and velocity_hit wins
	// x1-x9 by lexical precedence. Joined labels outrank both forms.
	g.Define("hit", token(prec(1, pat(`x[1-9]?`))))
	g.Define("accent_hit", token(pat(`X`)))
	g.Define("velocity_hit", token(prec(2, pat(`x[1-9]`))))
	g.Define("ratchet", seq(str("*"), sym("integer")))
	g.Define("probability", seq(choice(str("?"), str("%")), sym("integer")))

	// Scenes bind patterns to tracks, and also allow dotted parameter paths.
	// Undotted identifiers keep their edition-1 binding meaning.
	g.Define("scene_decl", seq(str("scene"), field("name", sym("identifier")), str("{"), repeat(sym("scene_assignment")), str("}")))
	g.Define("scene_assignment", seq(field("target", sym("scene_target")), str("="), field("value", sym("scene_value"))))
	g.Define("scene_target", choice(sym("parameter_path"), sym("identifier")))
	g.Define("scene_value", choice(sym("number"), sym("identifier"), sym("string"), sym("fraction")))
	g.Define("parameter_path", token(prec(3, pat(`[a-z_][a-z0-9_-]*(\.[a-z_][a-z0-9_-]*)+`))))
	g.Define("song_decl", seq(str("song"), str("{"), repeat(sym("song_entry")), str("}")))
	g.Define("song_entry", seq(field("scene", sym("identifier")), optional(seq(str("*"), field("bars", sym("integer"))))))

	// Literals. A number carries its unit; a fraction is a note division such
	// as 1/8, 1/8T (triplet), or 1/8. (dotted), lexed as one token so the
	// longest match beats a plain number.
	g.Define("value", choice(sym("number"), sym("insert_chain"), sym("identifier"), sym("string"), sym("fraction")))
	g.Define("insert_chain", seq(field("first", sym("identifier")), repeat(seq(str("->"), field("next", sym("identifier"))))))
	g.Define("fraction", token(pat(`[0-9]+\/[0-9]+[tT.]?`)))
	g.Define("number", token(pat(`-?[0-9]+(\.[0-9]+)?(frames|LUFS|dBTP|LU|khz|kHz|hz|Hz|ms|s|db|dB|%)?`)))
	g.Define("integer", token(pat(`[0-9]+`)))
	g.Define("key_root", token(pat(`[a-g][#b]?`)))
	g.Define("string", token(pat(`"([^"\\]|\\.)*"`)))
	g.Define("identifier", token(pat(`[a-z_][a-z0-9_-]*`)))
	g.Define("comment", token(pat(`\/\/[^\n]*`)))

	// Live controls use ordinary numbers so unit and range errors get semantic diagnostics.
	g.Define("live_decl", seq(str("live"), str("{"), repeat(choice(sym("live_land"), sym("live_phrase"), sym("live_macro"), sym("live_layers"))), str("}")))
	g.Define("live_land", seq(str("land"), str("="), field("value", choice(sym("identifier"), sym("bar_count"))), optional(str(";"))))
	g.Define("live_phrase", seq(str("phrase"), str("="), field("value", sym("bar_count")), optional(str(";"))))
	g.Define("live_macro", seq(str("macro"), field("name", sym("identifier")), str("="), field("value", sym("number")), optional(seq(str("smooth"), field("smooth", sym("number")))), optional(str(";"))))
	g.Define("live_layers", seq(str("layers"), field("macro", sym("identifier")), str("{"), repeat(choice(sym("live_layer"), sym("live_attack"), sym("live_release"))), str("}")))
	g.Define("live_layer", seq(field("track", sym("identifier")), str(">="), field("value", sym("number")), optional(str(";"))))
	g.Define("live_attack", seq(str("attack"), field("value", sym("bar_count")), optional(str(";"))))
	g.Define("live_release", seq(str("release"), field("value", sym("bar_count")), optional(str(";"))))
	g.Define("bar_count", token(pat(`-?[0-9]+(\.[0-9]+)?bars?`)))

	g.SetExtras(pat(`[ \t\r\n]+`), sym("comment"))
	g.SetWord("identifier")

	g.Test("dense drum rows", "cicada 1 pattern beat drums { bd: Xx..x1x9*2%50; }",
		"(source_file (integer) (drum_pattern (identifier) (drum_lane (drum_lane_label) (drum_hit (accent_hit)) (drum_hit (hit)) (drum_hit) (drum_hit) (drum_hit (velocity_hit)) (drum_hit (velocity_hit) (ratchet (integer)) (probability (integer))))))")
	g.Test("note divisions", "cicada 1 fx echo { time = 1/8. swing = 1/16t div = 3/4 }",
		"(source_file (integer) (fx_decl (identifier) (param_decl (identifier) (value (fraction))) (param_decl (identifier) (value (fraction))) (param_decl (identifier) (value (fraction)))))")
	g.Test("named mixer pieces", "fx hall reverb {} bus music { insert = comp } track bass acid { send hall = -9dB pre insert = grit -> drive out = sfx mute = on solo = off } master { insert = none } export release { loudness = -14LUFS true_peak = -1dBTP normalize = off }",
		"")
	g.Test("uppercase triplet", "cicada 1 fx delay { time = 1/16T }", "")
	g.Test("steps", "cicada 1 pattern p notes { 1^.5,~*2%70 - | c#3' use hook*2 transpose = -12 }",
		"(source_file (integer) (note_pattern (identifier) (acid_step (acid_note (pitch (degree)) (modifier))) (acid_step) (acid_step (acid_note (pitch (degree)) (octave_shift) (modifier) (modifier (ratchet (integer))) (modifier (probability (integer))))) (acid_step) (acid_step) (acid_step (acid_note (pitch (letter_pitch)) (octave_shift))) (phrase_use (identifier) (integer) (number))))")
	g.Test("instrument", "cicada 1 instrument i { param c: hz = 1hz; voice mono { let s = env(gate, 9ms); out = saw(pitch - c) * (s * 2); } }", "")
	g.Test("inferred instrument units", "instrument i { param cutoff = 720Hz param decay = 0.3s param level = -6dB param amount = 50% voice mono { out = saw(cutoff) * amount } }", "")
	g.Test("scene parameter paths", "scene drop { bass = bass-b bass.cutoff = 900Hz drums.bd_level = off }", "")
	g.Test("modeled kit targets", "kit acoustic { bd = model.kick ch = model.hat_closed cp = model.cross_stick }", "")
	g.Test("chance spelling", "pattern p acid { 1?70 } pattern beat drums { bd: x?50; }", "")
	g.Test("default notes", "phrase hook { 1 . } pattern p { use hook 5 . }", "")
	g.Test("SI units", "track bass acid { cutoff = 2kHz level = -6dB } instrument i { param cutoff: hz = 720Hz; voice mono { out = saw(440Hz); } }", "")
	g.Test("pattern settings inside braces", "pattern p { swing = 56% gate = 60% seed = 7 1 . } pattern beat drums { swing = 54% bd: x.; }", "")
	g.Test("line-based statements", "instrument i { param cutoff: hz = 720Hz voice mono { let osc = saw(pitch) out = osc } } kit k { bd = i ch = builtin.ch } pattern b drums { bd: x... sd: .x.. }", "")
	g.Test("short transpose", "phrase hook { 1 . } pattern p { use hook +7 }", "")
	g.Test("live controls", "live { land = bar phrase = 8bars macro intensity = 0.3 smooth 400ms layers intensity { drums >= 0.25 attack 1bar release 3bars } }", "")
	g.Test("authored kit", "cicada 1 kit steel { bd=kick; ch=builtin.ch; }", "")

	return g
}
