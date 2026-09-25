// Package grammar defines the Cicada grammar in the grammargen Go DSL. It is
// the source of notation/cicada.bin: run `go generate ./notation` after a
// change, and `make grammar-check` to verify the blob and the editor queries.
//
// Only the generator and tests import this package. The parser loads the
// generated blob, so programs that parse scores do not link grammargen.
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
		sym("scene_decl"), sym("song_decl"), sym("fx_decl"),
	))

	// Header: `title "Night circuit"`, `tempo 138`, `key a minor`, `seed 4242`.
	g.Define("title_decl", seq(str("title"), sym("string")))
	g.Define("tempo_decl", seq(str("tempo"), sym("number")))
	g.Define("key_decl", seq(str("key"), field("root", sym("key_root")), field("scale", sym("identifier"))))
	g.Define("seed_decl", seq(str("seed"), sym("integer")))

	// `track <name> <voice> { key = value }` binds a name to the built-in acid
	// or drums voice, or to a declared instrument.
	g.Define("track_decl", seq(
		str("track"), field("name", sym("identifier")), field("kind", sym("identifier")),
		str("{"), repeat(sym("param_decl")), str("}"),
	))

	// An instrument declares typed parameters and one voice. The voice binds
	// ordered lets and ends with the audio it outputs.
	g.Define("instrument_decl", seq(
		str("instrument"), field("name", sym("identifier")),
		str("{"), repeat(sym("instrument_param")), sym("voice_decl"), str("}"),
	))
	// A kit binds drum lanes to instruments or built-in drum voices.
	g.Define("kit_decl", seq(
		str("kit"), field("name", sym("identifier")),
		str("{"), repeat(sym("kit_binding")), str("}"),
	))
	g.Define("kit_binding", seq(
		field("lane", sym("identifier")), str("="), field("target", sym("kit_target")), str(";"),
	))
	g.Define("kit_target", choice(
		field("instrument", sym("identifier")),
		seq(str("builtin"), str("."), field("voice", sym("identifier"))),
	))
	g.Define("instrument_param", seq(
		str("param"), field("name", sym("identifier")), str(":"), field("unit", sym("identifier")),
		str("="), field("default", sym("number")), str(";"),
	))
	g.Define("voice_decl", seq(
		str("voice"), field("mode", sym("identifier")),
		str("{"), repeat(sym("let_stmt")), sym("out_stmt"), str("}"),
	))
	g.Define("let_stmt", seq(str("let"), field("name", sym("identifier")), str("="), field("value", sym("expression")), str(";")))
	g.Define("out_stmt", seq(str("out"), str("="), field("value", sym("expression")), str(";")))
	g.Define("expression", choice(
		precLeft(1, seq(field("left", sym("expression")), choice(str("+"), str("-")), field("right", sym("expression")))),
		precLeft(2, seq(field("left", sym("expression")), choice(str("*"), str("/")), field("right", sym("expression")))),
		sym("call_expr"),
		sym("number"),
		sym("identifier"),
		seq(str("("), sym("expression"), str(")")),
	))
	g.Define("call_expr", seq(field("function", sym("identifier")), str("("), commaSep(sym("expression")), str(")")))

	// Parameters of tracks and effects. Effects parse but do not render yet.
	g.Define("param_decl", seq(field("name", sym("identifier")), str("="), field("value", sym("value"))))
	g.Define("fx_decl", seq(str("fx"), field("name", sym("identifier")), str("{"), repeat(sym("param_decl")), str("}")))

	// A phrase is a named run of steps that `use` splices into a pattern
	// before scheduling, optionally repeated and transposed.
	g.Define("phrase_decl", seq(
		str("phrase"), field("name", sym("identifier")), optional(str("acid")),
		str("{"), repeat(sym("acid_step")), str("}"),
	))
	g.Define("acid_pattern", seq(
		str("pattern"), field("name", sym("identifier")), str("acid"), repeat(sym("pattern_attr")),
		str("{"), repeat(choice(sym("acid_step"), sym("phrase_use"))), str("}"),
	))
	g.Define("note_pattern", seq(
		str("pattern"), field("name", sym("identifier")), optional(str("notes")), repeat(sym("pattern_attr")),
		str("{"), repeat(choice(sym("acid_step"), sym("phrase_use"))), str("}"),
	))
	g.Define("phrase_use", seq(
		str("use"), field("name", sym("identifier")),
		optional(seq(str("*"), field("repeat", sym("integer")))),
		optional(seq(str("transpose"), str("="), field("transpose", sym("number")))),
	))
	g.Define("drum_pattern", seq(
		str("pattern"), field("name", sym("identifier")), str("drums"), repeat(sym("pattern_attr")),
		str("{"), repeat(sym("drum_lane")), str("}"),
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

	// A drum lane is one row of a grid: `bd: X...x...;`.
	g.Define("drum_lane", seq(field("name", sym("identifier")), str(":"), repeat(sym("drum_hit")), str(";")))
	g.Define("drum_hit", seq(choice(str("."), sym("hit"), sym("accent_hit"), sym("velocity_hit")), optional(sym("ratchet")), optional(sym("probability"))))
	// A hit also admits x1-x9 so keyword extraction never claims it. A keyword
	// in a drum row would bring the identifier word token with it and swallow
	// rows like xx.. whole. velocity_hit wins x1-x9 by lexical precedence.
	g.Define("hit", token(prec(-1, pat(`x[1-9]?`))))
	g.Define("accent_hit", token(pat(`X`)))
	g.Define("velocity_hit", token(pat(`x[1-9]`)))
	g.Define("ratchet", seq(str("*"), sym("integer")))
	g.Define("probability", seq(choice(str("?"), str("%")), sym("integer")))

	// Scenes bind patterns to tracks (off stops a track, keep holds it), and
	// the song plays scenes for a number of bars.
	g.Define("scene_decl", seq(str("scene"), field("name", sym("identifier")), str("{"), repeat(sym("scene_assignment")), str("}")))
	g.Define("scene_assignment", seq(field("track", sym("identifier")), str("="), field("pattern", sym("identifier"))))
	g.Define("song_decl", seq(str("song"), str("{"), repeat(sym("song_entry")), str("}")))
	g.Define("song_entry", seq(field("scene", sym("identifier")), optional(seq(str("*"), field("bars", sym("integer"))))))

	// Literals. A number carries its unit; a fraction is a note division such
	// as 1/8, 1/8T (triplet), or 1/8. (dotted), lexed as one token so the
	// longest match beats a plain number.
	g.Define("value", choice(sym("number"), sym("identifier"), sym("string"), sym("fraction")))
	g.Define("fraction", token(pat(`[0-9]+\/[0-9]+[tT.]?`)))
	g.Define("number", token(pat(`-?[0-9]+(\.[0-9]+)?(hz|khz|ms|s|db|%)?`)))
	g.Define("integer", token(pat(`[0-9]+`)))
	g.Define("key_root", token(pat(`[a-g][#b]?`)))
	g.Define("string", token(pat(`"([^"\\]|\\.)*"`)))
	g.Define("identifier", token(pat(`[a-z_][a-z0-9_-]*`)))
	g.Define("comment", token(pat(`\/\/[^\n]*`)))

	g.SetExtras(pat(`[ \t\r\n]+`), sym("comment"))
	g.SetWord("identifier")

	g.Test("dense drum rows", "cicada 1 pattern beat drums { bd: Xx..x1x9*2%50; }",
		"(source_file (integer) (drum_pattern (identifier) (drum_lane (identifier) (drum_hit (accent_hit)) (drum_hit (hit)) (drum_hit) (drum_hit) (drum_hit (velocity_hit)) (drum_hit (velocity_hit) (ratchet (integer)) (probability (integer))))))")
	g.Test("note divisions", "cicada 1 fx echo { time = 1/8. swing = 1/16t div = 3/4 }",
		"(source_file (integer) (fx_decl (identifier) (param_decl (identifier) (value (fraction))) (param_decl (identifier) (value (fraction))) (param_decl (identifier) (value (fraction)))))")
	g.Test("uppercase triplet", "cicada 1 fx delay { time = 1/16T }", "")
	g.Test("steps", "cicada 1 pattern p notes { 1^.5,~*2%70 - | c#3' use hook*2 transpose = -12 }",
		"(source_file (integer) (note_pattern (identifier) (acid_step (acid_note (pitch (degree)) (modifier))) (acid_step) (acid_step (acid_note (pitch (degree)) (octave_shift) (modifier) (modifier (ratchet (integer))) (modifier (probability (integer))))) (acid_step) (acid_step) (acid_step (acid_note (pitch (letter_pitch)) (octave_shift))) (phrase_use (identifier) (integer) (number))))")
	g.Test("instrument", "cicada 1 instrument i { param c: hz = 1hz; voice mono { let s = env(gate, 9ms); out = saw(pitch - c) * (s * 2); } }", "")
	g.Test("chance spelling", "pattern p acid { 1?70 } pattern beat drums { bd: x?50; }", "")
	g.Test("default notes", "phrase hook { 1 . } pattern p { use hook 5 . }", "")
	g.Test("authored kit", "cicada 1 kit steel { bd=kick; ch=builtin.ch; }", "")

	return g
}
