package notation

import (
	"bytes"
	_ "embed"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
)

// cicada.bin is generated from the grammargen DSL in language/grammar.
//
//go:generate go run ../cmd/cicada-grammar -bin cicada.bin
//go:embed cicada.bin
var grammarBlob []byte

// Language returns the Cicada grammar loaded from the generated parser blob.
func Language() (*gts.Language, error) {
	return walk.LanguageFromBlob("cicada", grammarBlob)
}

// ParseTree returns the lossless gotreesitter CST for editor and query tools.
// It returns a partial tree alongside syntax errors where possible.
func ParseTree(src []byte) (*gts.Node, *walk.Walker, error) {
	return walk.ParseFromBlob("cicada", grammarBlob, src)
}

// Parse lowers the CST to a typed score and then validates musical references.
func Parse(src []byte) (*Score, []Diagnostic) {
	return parseEdition(src, 0)
}

// ParseEdition parses a score using the edition inherited from its project
// manifest. An explicit source header must match that edition.
func ParseEdition(src []byte, edition int) (*Score, []Diagnostic) {
	if edition != 1 && edition != 2 {
		return nil, []Diagnostic{{Code: "CICADA-VERSION", Severity: "error", Message: "only cicada 1 and 2 are supported", Position: Position{Line: 1, Column: 1}}}
	}
	return parseEdition(src, edition)
}

// ParseSource parses a named loose file, resolving its optional source header.
func ParseSource(file SourceFile) (*Score, []Diagnostic) {
	return parseFiles([]SourceFile{file}, 0)
}

// SourceFile keeps a source's identity and exact bytes for project tooling.
type SourceFile struct {
	Path         string
	Source       []byte
	Library      string
	Bindings     map[string]string
	Root         string
	Edition      int
	Declarations map[string]bool
}

// ParseFiles lowers files in the supplied order into one score. References and
// phrase expansion run only after every declaration has been loaded.
func ParseFiles(files []SourceFile, edition int) (*Score, []Diagnostic) {
	if edition != 1 && edition != 2 {
		return nil, []Diagnostic{{Code: "CICADA-VERSION", Severity: "error", Message: "only cicada 1 and 2 are supported", Position: Position{Line: 1, Column: 1}}}
	}
	return parseFiles(files, edition)
}

func parseEdition(src []byte, edition int) (*Score, []Diagnostic) {
	return parseFiles([]SourceFile{{Source: src}}, edition)
}

type loweringWalker struct {
	*walk.Walker
	file                string
	library             string
	edition             int
	bindings            map[string]string
	origins             map[string]Origin
	diagnostics         *[]Diagnostic
	declarations        map[string]bool
	libraryDeclarations map[string]map[string]bool
}

func (w *loweringWalker) position(n *gts.Node) Position {
	p := pos(w.Walker, n)
	p.File = w.file
	return p
}

func parseFiles(files []SourceFile, edition int) (*Score, []Diagnostic) {
	requestedEdition := edition
	if edition == 0 {
		edition = 1
	}
	s := &Score{Position: Position{Line: 1, Column: 1}, Version: edition, TempoMilli: 130_000, KeyRoot: "a", Scale: "minor"}
	if len(files) > 0 {
		s.Position.File = files[0].Path
	}
	var diagnostics []Diagnostic
	s.Origins = map[string]Origin{}
	if len(files) > 0 {
		s.LibraryAliases = files[0].Bindings
	}
	syntaxFailed := false
	seenDeclarations := map[string]Position{}
	seenNames := map[string]Position{}
	duplicateLocations := map[Position]Position{}
	// Collect names owned by each library before resolving any import reference.
	libraryFiles := map[string][]SourceFile{}
	for _, file := range files {
		if file.Library != "" {
			libraryFiles[file.Library] = append(libraryFiles[file.Library], file)
		}
	}
	libraryDeclarations := map[string]map[string]bool{}
	for library, sources := range libraryFiles {
		libraryDeclarations[strings.ReplaceAll(library, "/", ".")] = DeclarationNames(sources)
	}
	for _, file := range files {
		root, walker, err := ParseTree(file.Source)
		if err != nil {
			syntaxFailed = true
			d := syntaxDiagnostic(err, file.Source)
			d.Position.File = file.Path
			diagnostics = append(diagnostics, d)
			continue
		}
		w := &loweringWalker{Walker: walker, file: file.Path, library: file.Library, edition: file.Edition, bindings: file.Bindings, origins: s.Origins, diagnostics: &diagnostics, declarations: file.Declarations, libraryDeclarations: libraryDeclarations}
		if w.declarations == nil {
			w.declarations = DeclarationNames(files)
		}
		headerEdition := 0
		for i := 0; i < root.NamedChildCount(); i++ {
			n := root.NamedChild(i)
			kind := w.Type(n)
			switch kind {
			case "title_decl", "tempo_decl", "key_decl", "seed_decl", "song_decl", "master_decl", "live_decl", "arrange_decl":
				if first, exists := seenDeclarations[kind]; exists {
					diagnostics = append(diagnostics, Diagnostic{
						Code: "CICADA-DUPLICATE", Severity: "error",
						Message:  "duplicate " + strings.TrimSuffix(kind, "_decl") + " declaration",
						Position: w.position(n), Related: first,
					})
				} else {
					seenDeclarations[kind] = w.position(n)
				}
			}

			if name := w.Field(n, "name"); name != nil {
				namespace := kind
				switch kind {
				case "acid_pattern", "note_pattern", "drum_pattern", "clip_decl":
					namespace = "pattern"
				case "instrument_decl", "kit_decl", "sampler_decl", "preset_decl":
					namespace = "voice"
				case "track_decl", "fx_decl", "bus_decl":
					namespace = "mixer"
				}
				key := namespace + ":" + w.declaration(name)
				if first, exists := seenNames[key]; exists {
					duplicateLocations[w.position(n)] = first
					if first.File != file.Path {
						diagnostics = append(diagnostics, Diagnostic{Code: "CICADA-DUPLICATE", Severity: "error", Message: "duplicate declaration " + w.Text(name), Position: w.position(n), Related: first})
					}
				} else {
					seenNames[key] = w.position(n)
				}
			}
			switch kind {
			case "import_decl":
				if file.Bindings == nil {
					diagnostics = append(diagnostics, Diagnostic{Code: "CICADA-LIB-IMPORT", Severity: "error", Message: "imports require the project source loader", Position: w.position(n)})
				}
			case "integer":
				headerEdition, _ = strconv.Atoi(w.Text(n))
				if requestedEdition == 0 && file.Library == "" {
					s.Version = headerEdition
				}
			case "title_decl":
				v := childText(w, n, "string")
				s.TitlePosition = w.position(n)
				var unquoteErr error
				s.Title, unquoteErr = strconv.Unquote(v)
				if unquoteErr != nil {
					diagnostics = append(diagnostics, Diagnostic{
						Code: "CICADA-SYNTAX", Severity: "error",
						Message: "invalid title string", Position: s.TitlePosition,
					})
				}
			case "tempo_decl":
				value := childText(w, n, "number")
				s.TempoMilli = parseMilli(value)
				s.TempoPosition = w.position(n)
			case "key_decl":
				s.KeyRoot = childText(w, n, "key_root")
				s.KeyPosition = w.position(n)
				s.Scale = childText(w, n, "identifier")
			case "seed_decl":
				seed := w.ChildByType(n, "integer")
				s.SeedLiteral = w.Text(seed)
				s.SeedPosition = w.position(seed)
				s.Seed, _ = strconv.ParseUint(s.SeedLiteral, 10, 64)
			case "asset_decl":
				path, _ := strconv.Unquote(w.Text(w.Field(n, "path")))
				s.Assets = append(s.Assets, Asset{Root: file.Root, Name: w.declaration(w.Field(n, "name")), Path: path, Params: audioParams(w, n), Position: w.position(n)})
			case "clip_decl":
				s.Clips = append(s.Clips, Clip{Name: w.declaration(w.Field(n, "name")), Asset: w.reference(w.Field(n, "asset")), Params: audioParams(w, n), Position: w.position(n)})
			case "sampler_decl":
				s.Samplers = append(s.Samplers, Sampler{Name: w.declaration(w.Field(n, "name")), Params: audioParams(w, n), Position: w.position(n)})
			case "instrument_decl":
				s.Instruments = append(s.Instruments, parseInstrument(w, n))
			case "preset_decl":
				s.Presets = append(s.Presets, parsePreset(w, n))
			case "kit_decl":
				s.Kits = append(s.Kits, parseKit(w, n))
			case "live_decl":
				if s.Live != nil {
					diagnostics = append(diagnostics, Diagnostic{Code: "CICADA-LIVE-BLOCK", Severity: "error", Message: "only one live block is allowed", Position: w.position(n)})
				} else {
					s.Live, diagnostics = parseLive(w, n, diagnostics)
				}
			case "track_decl":
				if s.Live != nil {
					diagnostics = append(diagnostics, Diagnostic{Code: "CICADA-LIVE-BLOCK", Severity: "error", Message: "live block must appear after all tracks", Position: w.position(n)})
				}
				s.Tracks = append(s.Tracks, parseTrack(w, n))
			case "bus_decl":
				s.Buses = append(s.Buses, parseBus(w, n))
			case "master_decl":
				s.HasMaster = true
				s.Master = parseMixBlock(w, n)
			case "export_decl":
				s.Exports = append(s.Exports, parseExport(w, n))
			case "phrase_decl":
				s.Phrases = append(s.Phrases, parsePhrase(w, n))
			case "acid_pattern", "note_pattern", "drum_pattern":
				s.Patterns = append(s.Patterns, parsePattern(w, n))
			case "scene_decl":
				s.Scenes = append(s.Scenes, parseScene(w, n))
			case "arrange_decl":
				s.Arrange = parseArrangement(w, n)
			case "song_decl":
				s.SongPosition = w.position(n)
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
		expectedEdition := requestedEdition
		if file.Edition != 0 {
			expectedEdition = file.Edition
		}
		if headerEdition != 0 && expectedEdition != 0 && headerEdition != expectedEdition {
			diagnostics = append(diagnostics, Diagnostic{
				Code: "CICADA-VERSION", Severity: "error", Message: "source header does not match the project edition",
				Position: Position{File: file.Path, Line: 1, Column: 1},
			})
		}
	}
	if syntaxFailed {
		return nil, diagnostics
	}
	s.Assets, s.Clips, s.Samplers, _ = resolveAudio(s)
	diagnostics = append(diagnostics, expandPhrases(s)...)
	diagnostics = append(diagnostics, Validate(s)...)

	if len(files) > 0 {
		for i := range diagnostics {
			if diagnostics[i].Code == "CICADA-DUPLICATE" {
				if first, ok := duplicateLocations[diagnostics[i].Position]; ok {
					diagnostics[i].Related = first
				}
			}
			if diagnostics[i].Position.File == "" {
				diagnostics[i].Position.File = files[0].Path
			}
		}
	}
	return s, diagnostics
}

func parseTrack(w *loweringWalker, n *gts.Node) Track {
	t := Track{Name: w.declaration(w.Field(n, "name")), Kind: w.reference(w.Field(n, "kind")), Position: w.position(n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if w.Type(c) == "chain_decl" {
			if t.Chain != nil {
				*w.diagnostics = append(*w.diagnostics, Diagnostic{Code: "CICADA-DUPLICATE", Severity: "error", Message: "duplicate track chain", Position: w.position(c)})
			}
			t.Chain = []StepToken{}
			for j := 0; j < c.NamedChildCount(); j++ {
				name := c.NamedChild(j)
				if w.Type(name) == "identifier" || w.Type(name) == "qualified_name" {
					t.Chain = append(t.Chain, StepToken{Text: w.reference(name), Position: w.position(name)})
				}
			}
		}
		if w.Type(c) == "mix_setting" {
			t.Params = append(t.Params, parseMixSetting(w, c))
		}
	}
	return t
}

func parseKit(w *loweringWalker, n *gts.Node) Kit {
	k := Kit{Name: w.declaration(w.Field(n, "name")), Position: w.position(n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		binding := n.NamedChild(i)
		if w.Type(binding) != "kit_binding" {
			continue
		}
		k.Bindings = append(k.Bindings, KitBinding{
			Lane: w.Text(w.Field(binding, "lane")), Target: w.reference(w.Field(binding, "target")), Position: w.position(binding),
		})
	}
	return k
}

func parseEffect(w *loweringWalker, n *gts.Node) Effect {
	e := Effect{Name: w.declaration(w.Field(n, "name")), Kind: w.reference(w.Field(n, "kind")), Position: w.position(n)}
	if w.Field(n, "kind") != nil && !strings.Contains(w.Text(w.Field(n, "kind")), ".") && (w.Text(w.Field(n, "kind")) == "delay" || w.Text(w.Field(n, "kind")) == "reverb" || w.Text(w.Field(n, "kind")) == "drive" || w.Text(w.Field(n, "kind")) == "comp") {
		e.Kind = w.Text(w.Field(n, "kind"))
	}
	if e.Kind == "" { // edition-1 shorthand: fx delay { ... }
		e.Kind = w.Text(w.Field(n, "name"))
		// An edition-1 library exports a named effect after scoping. Keep its
		// original kind without applying the importing score's syntax edition.
		e.Legacy = w.library == "" || w.edition != 1
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if w.Type(c) == "param_decl" {
			e.Params = append(e.Params, parseParam(w, c))
		}
	}
	return e
}

func parseBus(w *loweringWalker, n *gts.Node) Bus {
	b := Bus{Name: w.declaration(w.Field(n, "name")), Position: w.position(n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		if w.Type(n.NamedChild(i)) == "mix_setting" {
			b.Params = append(b.Params, parseMixSetting(w, n.NamedChild(i)))
		}
	}
	return b
}

func parseMixBlock(w *loweringWalker, n *gts.Node) []Param {
	var params []Param
	for i := 0; i < n.NamedChildCount(); i++ {
		if w.Type(n.NamedChild(i)) == "mix_setting" {
			params = append(params, parseMixSetting(w, n.NamedChild(i)))
		}
	}
	return params
}

func parseExport(w *loweringWalker, n *gts.Node) Export {
	e := Export{Name: w.declaration(w.Field(n, "name")), Position: w.position(n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		if w.Type(n.NamedChild(i)) == "param_decl" {
			e.Params = append(e.Params, parseParam(w, n.NamedChild(i)))
		}
	}
	return e
}

func parseMixSetting(w *loweringWalker, n *gts.Node) Param {
	for i := 0; i < n.NamedChildCount(); i++ {
		child := n.NamedChild(i)
		switch w.Type(child) {
		case "send_decl":
			level := w.Field(child, "level")
			return Param{Name: "send", Target: w.reference(w.Field(child, "to")), Value: w.Text(level),
				Pre: w.Field(child, "tap") != nil, Position: w.position(child), ValuePosition: w.position(level)}
		case "param_decl":
			return parseParam(w, child)
		}
	}
	return Param{Position: w.position(n)}
}

func parseInstrument(w *loweringWalker, n *gts.Node) Instrument {
	inst := Instrument{Name: w.declaration(w.Field(n, "name")), Octave: 2, Position: w.position(n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch w.Type(c) {
		case "instrument_octave":
			var err error
			inst.Octave, err = strconv.Atoi(w.Text(w.Field(c, "value")))
			if err != nil {
				inst.Octave = -1
			}
			inst.OctaveSet = true
			inst.OctavePosition = w.position(c)
		case "instrument_param":
			inst.Params = append(inst.Params, InstrumentParam{
				Name: w.Text(w.Field(c, "name")), Unit: w.Text(w.Field(c, "unit")),
				Default: w.Text(w.Field(c, "default")), Position: w.position(c),
			})
		case "voice_decl":
			inst.Mode = w.Text(w.Field(c, "mode"))
			for j := 0; j < c.NamedChildCount(); j++ {
				stmt := c.NamedChild(j)
				switch w.Type(stmt) {
				case "let_stmt":
					inst.Lets = append(inst.Lets, Let{
						Name:  w.Text(w.Field(stmt, "name")),
						Value: parseExpr(w, w.Field(stmt, "value")), Position: w.position(stmt),
					})
				case "out_stmt":
					inst.Output = parseExpr(w, w.Field(stmt, "value"))
				}
			}
		}
	}
	return inst
}

func parseExpr(w *loweringWalker, n *gts.Node) *Expr {
	if n == nil {
		return nil
	}
	e := &Expr{Position: w.position(n)}
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

func parseParam(w *loweringWalker, n *gts.Node) Param {
	value := w.Field(n, "value")
	text := strings.Join(strings.Fields(w.Text(value)), " ")
	name := w.Text(w.Field(n, "name"))
	if name == "asset" || name == "insert" || name == "out" || name == "bus" {
		parts := strings.Split(text, "->")
		for i, part := range parts {
			parts[i] = w.referenceText(strings.TrimSpace(part), w.position(value))
		}
		text = strings.Join(parts, " -> ")
	}
	return Param{Name: name, Value: text, Position: w.position(n), ValuePosition: w.position(value)}
}

// compactStepText reads grammar leaves, excluding whitespace and comments.
func compactStepText(w *loweringWalker, n *gts.Node) string {
	if w.Type(n) == "comment" {
		return ""
	}
	if n.ChildCount() == 0 {
		return w.Text(n)
	}
	var text strings.Builder
	for i := 0; i < n.ChildCount(); i++ {
		text.WriteString(compactStepText(w, n.Child(i)))
	}
	return text.String()
}

func chordComments(w *loweringWalker, n *gts.Node) []string {
	if w.Type(n) == "comment" {
		return []string{w.Text(n)}
	}
	var comments []string
	for i := 0; i < n.NamedChildCount(); i++ {
		comments = append(comments, chordComments(w, n.NamedChild(i))...)
	}
	return comments
}

func parseStepToken(w *loweringWalker, n *gts.Node) StepToken {
	step := StepToken{Text: w.Text(n), Position: w.position(n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		chord := n.NamedChild(i)
		if w.Type(chord) != "chord_note" {
			continue
		}
		step.ChordComments = chordComments(w, chord)
		for j := 0; j < chord.NamedChildCount(); j++ {
			child := chord.NamedChild(j)
			switch w.Type(child) {
			case "chord_pitch":
				step.ChordPitches = append(step.ChordPitches, ChordPitch{Text: compactStepText(w, child), Start: int(child.StartByte() - n.StartByte()), End: int(child.EndByte() - n.StartByte())})
			case "modifier":
				step.ChordModifiers += compactStepText(w, child)
			}
		}
	}
	return step
}

func parsePattern(w *loweringWalker, n *gts.Node) Pattern {
	p := Pattern{Name: w.declaration(w.Field(n, "name")), Position: w.position(n)}
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
				step := parseStepToken(w, c)
				p.Parts = append(p.Parts, PatternPart{Step: &step})
			}
		case "phrase_use":
			use := parsePhraseUse(w, c)
			p.Parts = append(p.Parts, PatternPart{Use: &use})
		case "expression_row":
			row := ExpressionRow{Name: strings.TrimSuffix(w.Text(w.Field(c, "name")), ":"), Position: w.position(c)}
			for j := 0; j < c.NamedChildCount(); j++ {
				value := c.NamedChild(j)
				if w.Type(value) == "expression_cell" {
					row.Values = append(row.Values, StepToken{Text: w.Text(value), Position: w.position(value)})
				}
			}
			p.Expression = append(p.Expression, row)
		case "drum_lane":
			lane := Lane{Name: strings.TrimSuffix(w.Text(w.Field(c, "name")), ":"), Position: w.position(c)}
			for j := 0; j < c.NamedChildCount(); j++ {
				hit := c.NamedChild(j)
				if w.Type(hit) == "drum_hit" {
					lane.Hits = append(lane.Hits, StepToken{Text: w.Text(hit), Position: w.position(hit)})
				}
			}
			p.Lanes = append(p.Lanes, lane)
		}
	}
	return p
}

func parsePhrase(w *loweringWalker, n *gts.Node) Phrase {
	p := Phrase{Name: w.declaration(w.Field(n, "name")), Position: w.position(n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if w.Type(c) == "acid_step" && w.Text(c) != "|" {
			p.Steps = append(p.Steps, parseStepToken(w, c))
		}
	}
	return p
}

func parsePhraseUse(w *loweringWalker, n *gts.Node) PhraseUse {
	u := PhraseUse{Name: w.reference(w.Field(n, "name")), Repeat: 1, Position: w.position(n)}
	if count := childText(w, n, "integer"); count != "" {
		u.Repeat, _ = strconv.Atoi(count)
	}
	if value := childText(w, n, "number"); value != "" {
		u.Transpose, _ = strconv.Atoi(value)
	}
	return u
}

func parseScene(w *loweringWalker, n *gts.Node) Scene {
	s := Scene{Name: w.declaration(w.Field(n, "name")), Position: w.position(n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if w.Type(c) == "scene_assignment" {
			target := w.Text(w.Field(c, "target"))
			value := w.Field(c, "value")
			if strings.Contains(target, ".") {
				s.Settings = append(s.Settings, SceneSetting{
					Path: w.parameterPath(target, w.position(c)), Value: w.Text(value), Position: w.position(c), ValuePosition: w.position(value),
				})
			} else {
				s.Bindings = append(s.Bindings, Binding{Track: target, Pattern: w.reference(value), Position: w.position(c)})
			}
		}
	}
	return s
}

func parseSongEntry(w *loweringWalker, n *gts.Node) SongEntry {
	e := SongEntry{Scene: w.Text(w.Field(n, "scene")), Bars: 1, Position: w.position(n)}
	if bars := childText(w, n, "integer"); bars != "" {
		e.Bars, _ = strconv.Atoi(bars)
	}
	return e
}

func childText(w *loweringWalker, n *gts.Node, typ string) string {
	if c := w.ChildByType(n, typ); c != nil {
		return w.Text(c)
	}
	return ""
}

func pos(w *walk.Walker, n *gts.Node) Position {
	if w == nil || n == nil {
		return Position{Line: 1, Column: 1}
	}
	line, byteColumn := w.Pos(n)
	start := int(n.StartByte())
	lineStart := start - byteColumn + 1
	if lineStart < 0 || start > len(w.Src) {
		return Position{Line: line, Column: byteColumn}
	}
	return Position{Line: line, Column: utf8.RuneCount(w.Src[lineStart:start]) + 1}
}

func syntaxDiagnostic(err error, src []byte) Diagnostic {
	d := Diagnostic{Code: "CICADA-SYNTAX", Severity: "error", Message: err.Error(), Position: Position{Line: 1, Column: 1}}
	parts := strings.SplitN(err.Error(), ":", 3)
	if len(parts) == 3 {
		line, lineErr := strconv.Atoi(parts[0])
		byteColumn, columnErr := strconv.Atoi(parts[1])
		if lineErr == nil && columnErr == nil && line > 0 && byteColumn > 0 {
			d.Position = Position{Line: line, Column: scalarColumn(src, line, byteColumn)}
			d.Message = strings.TrimSpace(parts[2])
		}
	}
	return d
}

// gotreesitter columns are byte offsets; Cicada diagnostics use Unicode
// scalar columns. Syntax errors arrive as line:byte-column strings.
func scalarColumn(src []byte, line, byteColumn int) int {
	start := 0
	for row := 1; row < line; row++ {
		next := bytes.IndexByte(src[start:], '\n')
		if next < 0 {
			return byteColumn
		}
		start += next + 1
	}
	end := len(src)
	if next := bytes.IndexByte(src[start:], '\n'); next >= 0 {
		end = start + next
	}
	offset := start + byteColumn - 1
	if offset > end {
		offset = end
	}
	return utf8.RuneCount(src[start:offset]) + 1
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

func audioParams(w *loweringWalker, n *gts.Node) []Param {
	params := []Param{}
	for i := 0; i < n.NamedChildCount(); i++ {
		if child := n.NamedChild(i); w.Type(child) == "param_decl" {
			params = append(params, parseParam(w, child))
		}
	}
	return params
}

func parseLive(w *loweringWalker, n *gts.Node, ds []Diagnostic) (*Live, []Diagnostic) {
	live := &Live{Position: w.position(n)}
	seen := map[string]bool{}
	duplicate := func(kind string, node *gts.Node, settings map[string]bool) {
		if settings[kind] {
			ds = append(ds, Diagnostic{Code: "CICADA-LIVE-BLOCK", Severity: "error", Message: "duplicate " + strings.TrimPrefix(kind, "live_") + " setting", Position: w.position(node)})
		}
		settings[kind] = true
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		value := w.Field(c, "value")
		switch w.Type(c) {
		case "live_land":
			duplicate(w.Type(c), c, seen)
			live.Land, live.LandPosition = w.Text(value), w.position(value)
		case "live_phrase":
			duplicate(w.Type(c), c, seen)
			live.PhraseBars, live.PhrasePosition = parseBars(w.Text(value)), w.position(value)
		case "live_macro":
			smooth := w.Field(c, "smooth")
			ms := float64(0)
			if smooth != nil {
				ms = parseDurationMS(w.Text(smooth))
			}
			live.Macros = append(live.Macros, LiveMacro{Name: w.Text(w.Field(c, "name")), Value: parseLiveNumber(w.Text(value)), SmoothMS: ms, Position: w.position(c), ValuePosition: w.position(value), SmoothPosition: w.position(smooth)})
		case "live_state":
			live.States = append(live.States, LiveState{Name: w.Text(w.Field(c, "name")), Scene: w.Text(w.Field(c, "scene")), Position: w.position(c)})
		case "live_stinger", "live_transition":
			quantize := w.Text(w.Field(c, "quantize"))
			if quantize == "" {
				quantize = "bar"
			}
			crossfade := float64(0)
			if value := w.Field(c, "crossfade"); value != nil {
				crossfade = parseDurationMS(w.Text(value))
			}
			if w.Type(c) == "live_stinger" {
				live.Stingers = append(live.Stingers, LiveStinger{Name: w.Text(w.Field(c, "name")), Track: w.Text(w.Field(c, "track")), Pattern: w.Text(w.Field(c, "pattern")), Quantize: quantize, CrossfadeMS: crossfade, Position: w.position(c)})
			} else {
				live.Transitions = append(live.Transitions, LiveTransition{From: w.Text(w.Field(c, "from")), To: w.Text(w.Field(c, "to")), Quantize: quantize, CrossfadeMS: crossfade, Position: w.position(c)})
			}
		case "live_layers":
			macro := w.Field(c, "macro")
			layers := LiveLayers{Macro: w.Text(macro), AttackBars: 1, ReleaseBars: 3, Position: w.position(c), MacroPosition: w.position(macro)}
			settings := map[string]bool{}
			for j := 0; j < c.NamedChildCount(); j++ {
				entry := c.NamedChild(j)
				v := w.Field(entry, "value")
				switch w.Type(entry) {
				case "live_layer":
					layers.Rules = append(layers.Rules, LiveLayer{Track: w.Text(w.Field(entry, "track")), Value: parseLiveNumber(w.Text(v)), Position: w.position(entry), ValuePosition: w.position(v)})
				case "live_attack":
					duplicate(w.Type(entry), entry, settings)
					layers.AttackBars, layers.AttackPosition = parseBars(w.Text(v)), w.position(v)
				case "live_release":
					duplicate(w.Type(entry), entry, settings)
					layers.ReleaseBars, layers.ReleasePosition = parseBars(w.Text(v)), w.position(v)
				}
			}
			live.Layers = append(live.Layers, layers)
		}
	}
	return live, ds
}

func parseLiveNumber(text string) float64 {
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return math.NaN()
	}
	return value
}

func parseBars(text string) int {
	value, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(text, "s"), "bar"))
	if err != nil {
		return -1
	}
	return value
}

func parseDurationMS(text string) float64 {
	if strings.HasSuffix(text, "ms") {
		return parseLiveNumber(strings.TrimSuffix(text, "ms"))
	}
	if strings.HasSuffix(text, "s") {
		return parseLiveNumber(strings.TrimSuffix(text, "s")) * 1000
	}
	return math.NaN()
}

func (w *loweringWalker) parameterPath(value string, position Position) string {
	first, _, _ := strings.Cut(value, ".")
	if _, ok := w.bindings[first]; ok {
		_, rest, _ := strings.Cut(value, ".")
		owner, suffix, _ := strings.Cut(rest, ".")
		if strings.Contains(suffix, ".") {
			*w.diagnostics = append(*w.diagnostics, Diagnostic{Code: "CICADA-LIB-REFERENCE", Severity: "error", Message: "library parameter paths require a directly imported effect owner", Position: position})
			return value
		}
		resolved := w.referenceText(first+"."+owner, position)
		if suffix != "" {
			resolved += "." + suffix
		}
		return resolved
	}
	// Local owners have no dots in their names. A raw library namespace
	// cannot become an owner without a direct import binding.
	if w.bindings != nil && len(strings.Split(value, ".")) > 2 && !w.declarations[first] {
		return w.referenceText(value, position)
	}
	return value
}
