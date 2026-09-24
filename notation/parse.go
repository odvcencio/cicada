package notation

import (
	_ "embed"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
)

//go:embed cicada.bin
var grammarBlob []byte

// ParseTree returns the lossless gotreesitter CST for editor and query tools.
// It returns a partial tree alongside syntax errors where possible.
func ParseTree(src []byte) (*gts.Node, *walk.Walker, error) {
	return walk.ParseFromBlob("cicada", grammarBlob, src)
}

// Parse lowers the CST to a typed score and then validates musical references.
func Parse(src []byte) (*Score, []Diagnostic) {
	root, w, err := ParseTree(src)
	if err != nil {
		return nil, []Diagnostic{syntaxDiagnostic(err)}
	}
	s := &Score{TempoMilli: 130_000, KeyRoot: "a", Scale: "minor"}
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch w.Type(n) {
		case "integer":
			s.Version, _ = strconv.Atoi(w.Text(n))
		case "title_decl":
			v := childText(w, n, "string")
			s.Title, _ = strconv.Unquote(v)
		case "tempo_decl":
			value := childText(w, n, "number")
			s.TempoMilli = parseMilli(value)
		case "key_decl":
			s.KeyRoot = childText(w, n, "key_root")
			s.Scale = childText(w, n, "identifier")
		case "seed_decl":
			s.Seed, _ = strconv.ParseUint(childText(w, n, "integer"), 10, 64)
		case "instrument_decl":
			s.Instruments = append(s.Instruments, parseInstrument(w, n))
		case "track_decl":
			s.Tracks = append(s.Tracks, parseTrack(w, n))
		case "phrase_decl":
			s.Phrases = append(s.Phrases, parsePhrase(w, n))
		case "acid_pattern", "note_pattern", "drum_pattern":
			s.Patterns = append(s.Patterns, parsePattern(w, n))
		case "scene_decl":
			s.Scenes = append(s.Scenes, parseScene(w, n))
		case "song_decl":
			for j := 0; j < n.NamedChildCount(); j++ {
				entry := n.NamedChild(j)
				if w.Type(entry) == "song_entry" {
					s.Song = append(s.Song, parseSongEntry(w, entry))
				}
			}
		case "fx_decl":
			s.Effects = append(s.Effects, parseEffect(w, n))
		}
	}
	diagnostics := expandPhrases(s)
	diagnostics = append(diagnostics, Validate(s)...)
	return s, diagnostics
}

func parseTrack(w *walk.Walker, n *gts.Node) Track {
	t := Track{Name: w.Text(w.Field(n, "name")), Kind: w.Text(w.Field(n, "kind")), Position: pos(w, n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if w.Type(c) == "param_decl" {
			t.Params = append(t.Params, parseParam(w, c))
		}
	}
	return t
}

func parseEffect(w *walk.Walker, n *gts.Node) Effect {
	e := Effect{Name: w.Text(w.Field(n, "name")), Position: pos(w, n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if w.Type(c) == "param_decl" {
			e.Params = append(e.Params, parseParam(w, c))
		}
	}
	return e
}

func parseInstrument(w *walk.Walker, n *gts.Node) Instrument {
	inst := Instrument{Name: w.Text(w.Field(n, "name")), Position: pos(w, n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch w.Type(c) {
		case "instrument_param":
			inst.Params = append(inst.Params, InstrumentParam{
				Name: w.Text(w.Field(c, "name")), Unit: w.Text(w.Field(c, "unit")),
				Default: w.Text(w.Field(c, "default")), Position: pos(w, c),
			})
		case "voice_decl":
			inst.Mode = w.Text(w.Field(c, "mode"))
			for j := 0; j < c.NamedChildCount(); j++ {
				stmt := c.NamedChild(j)
				switch w.Type(stmt) {
				case "let_stmt":
					inst.Lets = append(inst.Lets, Let{
						Name:  w.Text(w.Field(stmt, "name")),
						Value: parseExpr(w, w.Field(stmt, "value")), Position: pos(w, stmt),
					})
				case "out_stmt":
					inst.Output = parseExpr(w, w.Field(stmt, "value"))
				}
			}
		}
	}
	return inst
}

func parseExpr(w *walk.Walker, n *gts.Node) *Expr {
	if n == nil {
		return nil
	}
	e := &Expr{Position: pos(w, n)}
	if left, right := w.Field(n, "left"), w.Field(n, "right"); left != nil && right != nil {
		e.Kind = "binary"
		e.Left, e.Right = parseExpr(w, left), parseExpr(w, right)
		e.Text = strings.TrimSpace(string(w.Src[left.EndByte():right.StartByte()]))
		return e
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch w.Type(c) {
		case "call_expr":
			e.Kind, e.Text = "call", w.Text(w.Field(c, "function"))
			for j := 0; j < c.NamedChildCount(); j++ {
				arg := c.NamedChild(j)
				if w.Type(arg) == "expression" {
					e.Args = append(e.Args, parseExpr(w, arg))
				}
			}
			return e
		case "number":
			e.Kind, e.Text = "number", w.Text(c)
			return e
		case "identifier":
			e.Kind, e.Text = "name", w.Text(c)
			return e
		case "expression":
			return parseExpr(w, c) // parenthesized expression
		}
	}
	return e
}

func parseParam(w *walk.Walker, n *gts.Node) Param {
	return Param{Name: w.Text(w.Field(n, "name")), Value: w.Text(w.Field(n, "value")), Position: pos(w, n)}
}

func parsePattern(w *walk.Walker, n *gts.Node) Pattern {
	p := Pattern{Name: w.Text(w.Field(n, "name")), Position: pos(w, n)}
	if w.Type(n) == "acid_pattern" {
		p.Kind = "acid"
	} else if w.Type(n) == "note_pattern" {
		p.Kind = "notes"
	} else {
		p.Kind = "drums"
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch w.Type(c) {
		case "pattern_attr":
			p.Attrs = append(p.Attrs, parseParam(w, c))
		case "acid_step":
			if w.Text(c) != "|" {
				step := StepToken{Text: w.Text(c), Position: pos(w, c)}
				p.Parts = append(p.Parts, PatternPart{Step: &step})
			}
		case "phrase_use":
			use := parsePhraseUse(w, c)
			p.Parts = append(p.Parts, PatternPart{Use: &use})
		case "drum_lane":
			lane := Lane{Name: w.Text(w.Field(c, "name")), Position: pos(w, c)}
			for j := 0; j < c.NamedChildCount(); j++ {
				hit := c.NamedChild(j)
				if w.Type(hit) == "drum_hit" {
					lane.Hits = append(lane.Hits, StepToken{Text: w.Text(hit), Position: pos(w, hit)})
				}
			}
			p.Lanes = append(p.Lanes, lane)
		}
	}
	return p
}

func parsePhrase(w *walk.Walker, n *gts.Node) Phrase {
	p := Phrase{Name: w.Text(w.Field(n, "name")), Position: pos(w, n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if w.Type(c) == "acid_step" && w.Text(c) != "|" {
			p.Steps = append(p.Steps, StepToken{Text: w.Text(c), Position: pos(w, c)})
		}
	}
	return p
}

func parsePhraseUse(w *walk.Walker, n *gts.Node) PhraseUse {
	u := PhraseUse{Name: w.Text(w.Field(n, "name")), Repeat: 1, Position: pos(w, n)}
	if count := childText(w, n, "integer"); count != "" {
		u.Repeat, _ = strconv.Atoi(count)
	}
	if value := childText(w, n, "number"); value != "" {
		u.Transpose, _ = strconv.Atoi(value)
	}
	return u
}

func parseScene(w *walk.Walker, n *gts.Node) Scene {
	s := Scene{Name: w.Text(w.Field(n, "name")), Position: pos(w, n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if w.Type(c) == "scene_assignment" {
			s.Bindings = append(s.Bindings, Binding{
				Track: w.Text(w.Field(c, "track")), Pattern: w.Text(w.Field(c, "pattern")), Position: pos(w, c),
			})
		}
	}
	return s
}

func parseSongEntry(w *walk.Walker, n *gts.Node) SongEntry {
	e := SongEntry{Scene: w.Text(w.Field(n, "scene")), Bars: 1, Position: pos(w, n)}
	if bars := childText(w, n, "integer"); bars != "" {
		e.Bars, _ = strconv.Atoi(bars)
	}
	return e
}

func childText(w *walk.Walker, n *gts.Node, typ string) string {
	if c := w.ChildByType(n, typ); c != nil {
		return w.Text(c)
	}
	return ""
}

func pos(w *walk.Walker, n *gts.Node) Position {
	line, col := w.Pos(n)
	return Position{Line: line, Column: col}
}

func syntaxDiagnostic(err error) Diagnostic {
	d := Diagnostic{Code: "CICADA-SYNTAX", Severity: "error", Message: err.Error()}
	parts := strings.SplitN(err.Error(), ":", 3)
	if len(parts) == 3 {
		d.Position.Line, _ = strconv.Atoi(parts[0])
		d.Position.Column, _ = strconv.Atoi(parts[1])
	}
	return d
}

// parseMilli accepts up to three BPM decimal places without floating point.
func parseMilli(s string) int64 {
	parts := strings.SplitN(s, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole < 0 {
		return -1
	}
	frac := int64(0)
	if len(parts) == 2 {
		if len(parts[1]) > 3 || len(parts[1]) == 0 {
			return -1
		}
		v, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return -1
		}
		for i := len(parts[1]); i < 3; i++ {
			v *= 10
		}
		frac = v
	}
	return whole*1000 + frac
}
