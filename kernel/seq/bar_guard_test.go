package seq

import (
	"bytes"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A bar's length comes from a MeterMap. TestNoFixedBarLength fails when the
// removed constant comes back, when a fixed bar length appears outside the
// places listed in barAllowlist, or when a zero MeterMap appears outside the
// places listed in zeroMeterMapAllowlist. Test files, testdata and generated
// minified assets are not scanned.
//
// Go is type-checked, one package at a time, so strings and comments do not
// count and the spelling of a number does not matter. It flags:
//   - every constant expression whose value is 3840, 7680 or 15360 (one, two
//     and four 4/4 bars), however it is written: 3840, 0xF00, 3840.0, 4*PPQ,
//     PPQ<<2, int64(PPQ)*4, 16*TicksPerStep, an aliased seq.TicksPerWhole;
//   - a product chain such as bars*4*PPQ whose constant factors multiply to one
//     of those values;
//   - a constant or variable named like ticksPerBar that is set without a call;
//   - seq.TicksPerBar, and TicksPerBar declared in kernel/seq;
//   - outside kernel/seq, a zero MeterMap: seq.MeterMap{}, new(seq.MeterMap), or
//     a seq.MeterMap variable declared without a value.
//
// Imports from outside this repository are not loaded; an expression that
// needs one is not a constant to the checker, so it is not flagged (no such
// constant is a bar length today). Files with conflicting build tags are
// checked together, which only adds duplicate-declaration errors that the
// guard ignores.
//
// JavaScript, TypeScript, HTML and .gsx sources stay a text scan: it finds
// 3840, 7680, 15360 (decimal or hex) and the products 4*960 and 4*PPQ. In
// JavaScript and TypeScript, and in HTML script elements, comments are removed
// by a small lexer that knows strings, template literals and regular
// expressions, so a "/*" or "//" inside a string cannot hide code. HTML outside
// scripts loses only <!-- --> comments; .gsx is scanned whole.
//
// Each allowed occurrence is pinned by file and by where it sits: the enclosing
// top-level declaration in Go, the trimmed line elsewhere. The count is exact:
// a new occurrence fails, a moved one fails, and a removed one asks for its
// entry to be removed.
type barAllowance struct {
	count  int
	reason string
}

const (
	wholeNote   = "a whole note: note values and the longest pattern cell are fractions of it"
	seekUnit    = "the seek wire encoding: tick = Arg0*3840 + Arg1 (cmd.SeekUnit)"
	frameCount  = "an audio buffer size in frames, not ticks"
	gosxHost    = "the GoSX worklet's bar length: it keeps its own bar clock until the descriptor carries the meter map (time-signatures phase 6)"
	phase5Pages = "named phase 5 exception: page JavaScript counts 4/4 bars until Studio sends pages the meter map as display data"
	zeroMap     = "the zero map's fast path: one 4/4 segment, whose bar is a whole note"
)

var barAllowlist = map[[2]string]barAllowance{
	// Go: whole notes, the seek unit, frames and the GoSX descriptor.
	{"kernel/seq/meter.go", "const TicksPerWhole"}:                              {1, "defines the whole note, PPQ * 4"},
	{"kernel/seq/meter.go", "const MaxBarTicks"}:                                {1, "the longest bar, 32/4: eight whole notes"},
	{"kernel/seq/meter.go", "NewMeterMap"}:                                      {2, "a beat is a whole note divided by the meter's unit; one 4/4 change is the zero map"},
	{"kernel/seq/meter.go", "MeterMap.BarAt"}:                                   {2, zeroMap},
	{"kernel/seq/meter.go", "MeterMap.BarStart"}:                                {1, zeroMap},
	{"kernel/seq/meter.go", "MeterMap.BarTicks"}:                                {1, zeroMap},
	{"kernel/seq/meter.go", "MeterMap.BarsTicks"}:                               {1, zeroMap},
	{"kernel/seq/meter.go", "MeterMap.BarsUntil"}:                               {2, zeroMap},
	{"kernel/seq/meter.go", "MeterMap.BeatsInBar"}:                              {1, zeroMap},
	{"kernel/seq/meter.go", "MeterMap.NextBarLine"}:                             {2, zeroMap},
	{"kernel/seq/meter.go", "MeterMap.Position"}:                                {2, zeroMap},
	{"kernel/seq/meter.go", "MeterMap.PositionTick"}:                            {2, zeroMap},
	{"kernel/seq/meter.go", "MeterMap.QuantizeBars"}:                            {1, zeroMap},
	{"kernel/seq/meter.go", "MeterMap.WholeBars"}:                               {2, zeroMap},
	{"kernel/seq/step.go", "Pattern.Validate"}:                                  {1, wholeNote},
	{"kernel/cmd/command.go", "const SeekUnit"}:                                 {1, seekUnit},
	{"kernel/cmd/command.go", "Command.Validate"}:                               {2, seekUnit + " (Arg1's limit); and " + wholeNote + " (the quantize grid limit; kernel/cmd cannot import seq)"},
	{"kernel/cmd/command.go", "SeekArgs"}:                                       {2, seekUnit},
	{"kernel/engine/engine.go", "Engine.apply"}:                                 {1, seekUnit},
	{"host/liveplay/range.go", "PlaybackRange.Validate"}:                        {1, seekUnit + ": the furthest tick a seek can reach"},
	{"musictime/time.go", "MusicalTicks"}:                                       {1, wholeNote},
	{"notation/grid.go", "GridTicks"}:                                           {2, wholeNote},
	{"notation/modulators.go", "ParseModRate"}:                                  {1, wholeNote},
	{"project/modulators.go", "rateSource"}:                                     {2, wholeNote},
	{"project/validate.go", "validateProject"}:                                  {1, wholeNote},
	{"smf/project.go", "fromProject"}:                                           {1, "a whole note divided by the beat gives the time-signature denominator"},
	{"workstation/modulation.go", "modulationRate"}:                             {2, wholeNote},
	{"workstation/pattern_grid.go", "patternGridName"}:                          {1, wholeNote},
	{"internal/audiobackend/tymbal_period_native.go", "tymbalPeriodCandidates"}: {1, frameCount},
	{"sdk/gosxhost/backend.go", "const TicksPerBar"}:                            {2, gosxHost},
	{"sdk/gosxhost/backend.go", "DefaultBackend"}:                               {1, gosxHost},

	// JavaScript: the seek wire encoding.
	{"host/web/client.js", "this.sendCommands([{ op: 3, arg0: Math.floor(tick / 3840), arg1: tick % 3840 }, ...(playing ? [{ op: 1 }] : [])]);"}: {2, seekUnit},
	{"host/web/client.js", "if (node === this.node && record.op === 3) this.anchorTick((record.arg0 || 0) * 3840 + (record.arg1 || 0));"}:        {1, seekUnit},
	{"host/web/processor.js", "seekTick = bar * 3840 + tick;"}:                                                                                   {1, seekUnit},
	{"host/web/processor.js", "swapView.setUint32(4, tick / 3840, true);"}:                                                                       {1, seekUnit},
	{"host/web/processor.js", "putTick(swap, 8, tick % 3840);"}:                                                                                  {1, seekUnit},

	// JavaScript: named phase 5 exceptions (page display and the count-in).
	{"cmd/cicada/studio-live.js", "const map = {1: 960, 2: 3840, 5: 3840, 6: 7680, 8: 15360};"}:                                                            {4, phase5Pages + ": live pad quantize lengths"},
	{"cmd/cicada/studio-live.js", "return Math.max(0, (Number(state.bar || 1) - 1) * 3840 + (Number(state.step || 1) - 1) * 240);"}:                        {1, phase5Pages + ": live pad position"},
	{"cmd/cicada/studio-live.js", "return Math.max(1, Math.ceil((target - current) / 3840));"}:                                                             {1, phase5Pages + ": bars until a queued launch"},
	{"cmd/cicada/view.html", "const bar = Math.floor(message.tick / 3840) + 1;"}:                                                                           {1, phase5Pages + ": score view transport bar"},
	{"cmd/cicada/view.html", "const step = Math.floor(message.tick % 3840 / 240) + 1;"}:                                                                    {1, phase5Pages + ": score view transport step"},
	{"host/demobrowser/page.js", "if (kind === 1) { state.playhead = tick; $('transport-position').textContent = `Bar ${Math.floor(tick / 3840) + 1}`; }"}: {1, phase5Pages + ": demo transport label"},
	{"host/web/capture-client.js", "const countInFrames=Math.ceil(bars*3840*60000*this.audio.context.sampleRate/(bpm*960));"}:                              {1, phase5Pages + ": recording count-in length"},
}

const (
	meterHook    = "the hook that returns the song's meter map: time-signatures phase 2 builds it from the meter declaration"
	noMeterYet   = "phase 2 must thread a real map here: "
	silentPhase2 = "phase 2 must thread a real map here, and it fails silently (wrong positions, no error) until it does: "
)

// zeroMeterMapAllowlist is the exact list of places that use the zero (4/4)
// map because their data carries no meter yet. Every other caller passes the
// song's map.
var zeroMeterMapAllowlist = map[[2]string]barAllowance{
	{"notation/meter.go", "Score.MeterMap"}:                          {1, meterHook},
	{"project/meter.go", "Project.MeterMap"}:                         {1, meterHook},
	{"edit/apply.go", "Context.MeterMap"}:                            {1, meterHook},
	{"edit/notes.go", "EditNotes.Validate"}:                          {1, noMeterYet + "an interface method with no context; phase 2 raises the grid limit to 30,720 ticks so it needs no map"},
	{"host/kernelimage/unified.go", "validateUnifiedFields"}:         {1, noMeterYet + "a kernel image carries no meter table until header flag bit 7"},
	{"host/takejournal/journal.go", "Timeline.MeterMap"}:             {1, silentPhase2 + "a take timeline records tempo steps by bar but no meter, so its clock and its count-in sit on 4/4 bars"},
	{"render/wav_timing.go", "wavTiming.frames"}:                     {1, noMeterYet + "timing chunk versions 1 and 2 record no meter (version 3 does)"},
	{"smf/import.go", "ImportSource"}:                                {1, noMeterYet + "the importer accepts 4/4 files only (spec phase 6)"},
	{"smf/import.go", "writeImport"}:                                 {1, noMeterYet + "the importer writes a 4/4 song (spec phase 6)"},
	{"cmd/cicada/studio_transport.go", "studioTransport.meterMap"}:   {1, noMeterYet + "with no player there is no song yet"},
	{"cmd/cicada-kernel-wasm/main.go", "nextBar"}:                    {1, noMeterYet + "a fallback before the engine exists"},
	{"workstation/control/grid.go", "GridMeterMap"}:                  {1, silentPhase2 + "the browser engine's one map: it reads server-written positions as 4/4 until phase 5 sends it the song's"},
	{"workstation/transcription.go", "studioApp.transcriptionAudio"}: {1, noMeterYet + "the transcriber writes a 4/4 score (spec phase 6)"},
}

var (
	textBarNumber  = regexp.MustCompile(`(?i)\b(3_?840(\.0*)?|7_?680|15_?360|0x0*f00|0x0*1e00|0x0*3c00)\b|\b(960|PPQ)\s*\*\s*4\b|\b4\s*\*\s*(960|PPQ)\b`)
	barNamePattern = regexp.MustCompile(`(?i)^(ticks_?per_?bar|bar_?ticks|[a-z]*ticksperbar)$`)
	htmlComment    = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlScript     = regexp.MustCompile(`(?is)(<script\b[^>]*>)(.*?)(</script\s*>)`)
	moduleLine     = regexp.MustCompile(`(?m)^module\s+(\S+)`)
)

const seqImportPath = "m31labs.dev/cicada/kernel/seq"

func barGuardRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root not found from %s: %v", root, err)
	}
	return root
}

func scannedSource(d fs.DirEntry) (skipDir, scan bool) {
	name := d.Name()
	if d.IsDir() {
		return name == "build" || name == "testdata" || name == "node_modules" || strings.HasPrefix(name, "."), false
	}
	if strings.HasSuffix(name, "_test.go") || strings.Contains(name, ".test.") || strings.HasSuffix(name, "_test.cjs") || name == "test.cjs" || strings.HasSuffix(name, ".min.js") || strings.HasSuffix(name, ".d.ts") {
		return false, false
	}
	switch filepath.Ext(name) {
	case ".go", ".js", ".cjs", ".mjs", ".html", ".ts", ".gsx":
		return false, true
	}
	return false, false
}

// isBarLength reports whether a constant is one, two or four 4/4 bars.
func isBarLength(v constant.Value) bool {
	if v == nil || v.Kind() != constant.Int && v.Kind() != constant.Float {
		return false
	}
	n, exact := constant.Int64Val(constant.ToInt(v))
	return exact && (n == 3840 || n == 7680 || n == 15360)
}

// mulFactors flattens a chain of * through parentheses.
func mulFactors(e ast.Expr, out []ast.Expr) []ast.Expr {
	switch v := e.(type) {
	case *ast.ParenExpr:
		return mulFactors(v.X, out)
	case *ast.BinaryExpr:
		if v.Op == token.MUL {
			return mulFactors(v.Y, mulFactors(v.X, out))
		}
	}
	return append(out, e)
}

func isMeterMap(t types.Type) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Name() == "MeterMap" && obj.Pkg() != nil && obj.Pkg().Path() == seqImportPath
}

func callFree(e ast.Expr) bool {
	free := true
	ast.Inspect(e, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.CallExpr, *ast.IndexExpr:
			free = false
		}
		return free
	})
	return free
}

// declPlaces names each top-level place in a Go file: a function or method by
// its name (Type.Method), each constant, variable or type by its keyword and
// first name.
func declPlaces(file *ast.File) (nodes []ast.Node, names []string) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				name := d.Tok.String()
				switch s := spec.(type) {
				case *ast.ValueSpec:
					name += " " + s.Names[0].Name
				case *ast.TypeSpec:
					name += " " + s.Name.Name
				case *ast.ImportSpec:
					continue
				}
				nodes, names = append(nodes, spec), append(names, name)
			}
		case *ast.FuncDecl:
			name := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) == 1 {
				recv := d.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				if index, ok := recv.(*ast.IndexExpr); ok {
					recv = index.X
				}
				if id, ok := recv.(*ast.Ident); ok {
					name = id.Name + "." + name
				}
			}
			nodes, names = append(nodes, d), append(names, name)
		}
	}
	return nodes, names
}

// goFindings counts, for one type-checked Go file, the fixed bar lengths and
// the zero meter maps per top-level place.
func goFindings(t *testing.T, rel string, file *ast.File, info *types.Info) (bars, zeros map[string]int) {
	bars, zeros = map[string]int{}, map[string]int{}
	imports := map[string]string{}
	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		name := path.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = importPath
	}
	checkZeros := !strings.HasPrefix(rel, "kernel/seq/")
	nodes, names := declPlaces(file)
	for i, decl := range nodes {
		where := names[i]
		ast.Inspect(decl, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.SelectorExpr:
				if x, ok := v.X.(*ast.Ident); ok && v.Sel.Name == "TicksPerBar" && imports[x.Name] == seqImportPath {
					t.Errorf("%s uses seq.TicksPerBar; ask a MeterMap for bar lengths", rel)
				}
			case *ast.CompositeLit:
				if checkZeros && isMeterMap(info.TypeOf(v)) {
					zeros[where]++
				}
			case *ast.CallExpr:
				if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "new" && len(v.Args) == 1 {
					if _, builtin := info.Uses[id].(*types.Builtin); builtin && checkZeros && isMeterMap(info.TypeOf(v.Args[0])) {
						zeros[where]++
					}
				}
			case *ast.ValueSpec:
				if checkZeros && len(v.Values) == 0 && v.Type != nil && isMeterMap(info.TypeOf(v.Type)) {
					zeros[where] += len(v.Names)
				}
				for j, name := range v.Names {
					if strings.HasPrefix(rel, "kernel/seq/") && name.Name == "TicksPerBar" {
						t.Errorf("%s declares TicksPerBar again; a bar's length comes from a MeterMap", rel)
					}
					if barNamePattern.MatchString(name.Name) && j < len(v.Values) && callFree(v.Values[j]) {
						bars[where]++
					}
				}
			case *ast.AssignStmt:
				if v.Tok == token.DEFINE {
					for j, lhs := range v.Lhs {
						if id, ok := lhs.(*ast.Ident); ok && barNamePattern.MatchString(id.Name) && j < len(v.Rhs) && callFree(v.Rhs[j]) {
							bars[where]++
						}
					}
				}
			}
			e, ok := n.(ast.Expr)
			if !ok {
				return true
			}
			if tv, ok := info.Types[e]; ok && tv.Value != nil {
				if isBarLength(tv.Value) {
					bars[where]++
					return false
				}
				return true
			}
			if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.MUL {
				product, constants := constant.MakeInt64(1), 0
				for _, f := range mulFactors(b, nil) {
					if tv, ok := info.Types[f]; ok && tv.Value != nil && (tv.Value.Kind() == constant.Int || tv.Value.Kind() == constant.Float) {
						product, constants = constant.BinaryOp(product, token.MUL, tv.Value), constants+1
					}
				}
				if constants >= 2 && isBarLength(product) {
					bars[where]++
					return false
				}
			}
			return true
		})
	}
	return bars, zeros
}

// jsWithoutComments replaces JavaScript comments with spaces, keeping line
// breaks. It follows strings, template literals (with nested ${} code) and
// regular expression literals, so their contents never start a comment. A
// slash starts a regular expression when the previous token cannot end an
// operand, the usual rule for scanning JavaScript without parsing it.
func jsWithoutComments(src []byte) []byte {
	out := bytes.Clone(src)
	blank := func(from, to int) {
		for k := from; k < to && k < len(out); k++ {
			if out[k] != '\n' {
				out[k] = ' '
			}
		}
	}
	isWord := func(c byte) bool {
		return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
	}
	regexAfterWord := map[string]bool{"return": true, "typeof": true, "case": true, "do": true, "else": true, "in": true, "of": true, "new": true, "delete": true, "void": true, "throw": true, "instanceof": true, "yield": true, "await": true}
	var code func(i int, nested bool) int
	// quoted skips a string or template body from just after its opening quote.
	quoted := func(i int, quote byte) int {
		for i < len(src) {
			switch c := src[i]; {
			case c == '\\':
				i += 2
			case c == quote:
				return i + 1
			case quote != '`' && c == '\n':
				return i // an unterminated string ends at the line
			case quote == '`' && c == '$' && i+1 < len(src) && src[i+1] == '{':
				i = code(i+2, true)
			default:
				i++
			}
		}
		return i
	}
	code = func(i int, nested bool) int {
		depth := 0
		operand := false // the previous token can end an operand: a slash divides
		for i < len(src) {
			c := src[i]
			switch {
			case c == '/' && i+1 < len(src) && src[i+1] == '/':
				end := bytes.IndexByte(src[i:], '\n')
				if end < 0 {
					end = len(src) - i
				}
				blank(i, i+end)
				i += end
			case c == '/' && i+1 < len(src) && src[i+1] == '*':
				end := bytes.Index(src[i+2:], []byte("*/"))
				if end < 0 {
					blank(i, len(src))
					return len(src)
				}
				blank(i, i+2+end+2)
				i += 2 + end + 2
			case c == '/' && !operand:
				i++
				for inClass := false; i < len(src) && src[i] != '\n'; i++ {
					if src[i] == '\\' {
						i++
					} else if src[i] == '[' {
						inClass = true
					} else if src[i] == ']' {
						inClass = false
					} else if src[i] == '/' && !inClass {
						i++
						break
					}
				}
				for i < len(src) && isWord(src[i]) {
					i++
				}
				operand = true
			case c == '\'' || c == '"' || c == '`':
				i = quoted(i+1, c)
				operand = true
			case isWord(c):
				start := i
				for i < len(src) && isWord(src[i]) {
					i++
				}
				operand = !regexAfterWord[string(src[start:i])]
			case c == ')' || c == ']':
				i++
				operand = true
			case c == '{':
				depth++
				i++
				operand = false
			case c == '}':
				if nested && depth == 0 {
					return i + 1
				}
				depth--
				i++
				operand = true // an object literal or block; a slash after it divides
			case c == ' ' || c == '\t' || c == '\r' || c == '\n':
				i++
			default:
				i++
				operand = false
			}
		}
		return i
	}
	code(0, false)
	return out
}

// textCode returns the scanned text of a non-Go source, with the same line
// breaks as the source.
func textCode(rel string, data []byte) []byte {
	switch filepath.Ext(rel) {
	case ".js", ".cjs", ".mjs", ".ts":
		return jsWithoutComments(data)
	case ".html":
		out := htmlComment.ReplaceAllFunc(data, func(m []byte) []byte {
			return bytes.Map(func(r rune) rune {
				if r == '\n' {
					return r
				}
				return ' '
			}, m)
		})
		for _, m := range htmlScript.FindAllSubmatchIndex(data, -1) {
			copy(out[m[4]:m[5]], jsWithoutComments(data[m[4]:m[5]]))
		}
		return out
	}
	return data
}

// textFindings counts, for one text source, the fixed bar lengths per trimmed
// source line.
func textFindings(rel string, data []byte) map[string]int {
	found := map[string]int{}
	lines := strings.Split(string(data), "\n")
	codeLines := strings.Split(string(textCode(rel, data)), "\n")
	for i, line := range codeLines {
		if n := len(textBarNumber.FindAllString(line, -1)); n > 0 {
			found[strings.TrimSpace(lines[i])] += n
		}
	}
	return found
}

// goTree loads and type-checks every scanned Go package in the repository.
// Packages of this repository are imported from source; other imports are
// empty packages.
type goTree struct {
	fset     *token.FileSet
	module   string
	packages map[string][]*goPackage // by import path: one per package name in a directory
	external map[string]*types.Package
	checking map[*goPackage]bool
}

type goPackage struct {
	path  string
	files []*ast.File
	rels  []string
	pkg   *types.Package
	info  *types.Info
}

func (g *goTree) importPath(dir string) string {
	if dir == "." {
		return g.module
	}
	return g.module + "/" + dir
}

// Import implements types.Importer.
func (g *goTree) Import(importPath string) (*types.Package, error) {
	if groups := g.packages[importPath]; len(groups) > 0 {
		// A directory's importable package is its largest; the others are
		// ignored generators and the like.
		best := groups[0]
		for _, p := range groups[1:] {
			if len(p.files) > len(best.files) {
				best = p
			}
		}
		g.check(best)
		return best.pkg, nil
	}
	if p := g.external[importPath]; p != nil {
		return p, nil
	}
	p := types.NewPackage(importPath, path.Base(importPath))
	p.MarkComplete()
	g.external[importPath] = p
	return p, nil
}

func (g *goTree) check(p *goPackage) {
	if p.pkg != nil || g.checking[p] {
		return
	}
	g.checking[p] = true
	p.info = &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: g, Error: func(error) {}, FakeImportC: true}
	p.pkg, _ = conf.Check(p.path, g.fset, p.files, p.info)
	delete(g.checking, p)
}

func checkModulePaths(t *testing.T, root, module string, goMods []string) {
	t.Helper()
	for _, rel := range goMods {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		m := moduleLine.FindSubmatch(data)
		dir := filepath.ToSlash(filepath.Dir(rel))
		want := module
		if dir != "." {
			want += "/" + dir
		}
		if m == nil || string(m[1]) != want {
			t.Fatalf("%s: module path is not %s; the guard maps import paths to directories", rel, want)
		}
	}
}

func TestNoFixedBarLength(t *testing.T) {
	root := barGuardRoot(t)
	rootMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	m := moduleLine.FindSubmatch(rootMod)
	if m == nil {
		t.Fatal("go.mod has no module line")
	}
	tree := &goTree{fset: token.NewFileSet(), module: string(m[1]), packages: map[string][]*goPackage{}, external: map[string]*types.Package{}, checking: map[*goPackage]bool{}}
	bars, zeros := map[[2]string]int{}, map[[2]string]int{}
	var goMods []string
	byDir := map[[2]string]*goPackage{}
	err = filepath.WalkDir(root, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		skipDir, scan := scannedSource(d)
		if skipDir && file != root {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(root, file)
		rel = filepath.ToSlash(rel)
		if d.Name() == "go.mod" {
			goMods = append(goMods, rel)
		}
		if !scan {
			return nil
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if !strings.HasSuffix(rel, ".go") {
			for where, n := range textFindings(rel, data) {
				bars[[2]string{rel, where}] = n
			}
			return nil
		}
		parsed, err := parser.ParseFile(tree.fset, file, data, parser.SkipObjectResolution)
		if err != nil {
			return nil // a file that does not parse fails its own build
		}
		dir := path.Dir(rel)
		key := [2]string{dir, parsed.Name.Name}
		p := byDir[key]
		if p == nil {
			p = &goPackage{path: tree.importPath(dir)}
			byDir[key] = p
			tree.packages[p.path] = append(tree.packages[p.path], p)
		}
		p.files, p.rels = append(p.files, parsed), append(p.rels, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	checkModulePaths(t, root, tree.module, goMods)
	keys := make([][2]string, 0, len(byDir))
	for key := range byDir {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+" "+keys[i][1] < keys[j][0]+" "+keys[j][1] })
	for _, key := range keys {
		p := byDir[key]
		tree.check(p)
		for i, file := range p.files {
			b, z := goFindings(t, p.rels[i], file, p.info)
			for where, n := range b {
				bars[[2]string{p.rels[i], where}] += n
			}
			for where, n := range z {
				zeros[[2]string{p.rels[i], where}] += n
			}
		}
	}
	compareAllowlist(t, bars, barAllowlist, "fixed bar or whole-note lengths; a bar's length comes from a MeterMap")
	compareAllowlist(t, zeros, zeroMeterMapAllowlist, "zero MeterMaps; pass the song's map, or list the place for phase 2")
}

func compareAllowlist(t *testing.T, found map[[2]string]int, allowlist map[[2]string]barAllowance, what string) {
	t.Helper()
	keys := make([][2]string, 0, len(found))
	for key := range found {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	for _, key := range keys {
		allowed, ok := allowlist[key]
		switch {
		case !ok:
			t.Errorf("%s (%s) has %d %s", key[0], key[1], found[key], what)
		case found[key] != allowed.count:
			t.Errorf("%s (%s) has %d, %d allowed (%s)", key[0], key[1], found[key], allowed.count, allowed.reason)
		}
	}
	for key, allowed := range allowlist {
		if _, ok := found[key]; !ok {
			t.Errorf("%s (%s) no longer has its %d allowed occurrences; remove the entry", key[0], key[1], allowed.count)
		}
	}
}

// TestBarGuardCatchesEverySpelling runs the guard's Go and text checks on
// sources that hide a bar length in the ways a reviewer found, and on sources
// whose strings contain comment markers.
func TestBarGuardCatchesEverySpelling(t *testing.T) {
	goSource := `package sneak

import s "m31labs.dev/cicada/kernel/seq"

const PPQ = 960
const TicksPerStep = 240

func A() int64 { return 16 * TicksPerStep }
func B() int64 { return int64(PPQ) * 4 }
func C() int64 { return 0xF00 }
func D() int64 { return PPQ << 2 }
func E() int64 { return s.TicksPerWhole }
func F() int64 { return PPQ * 2 * 2 }
func G() int64 { return 3840 }
func H(bars int64) int64 { return bars * 4 * PPQ }
func I() float64 { return 3840.0 }
func J() int64 { const local = 2 * 1920; return local } // the constant and its use
func K() s.MeterMap { var m s.MeterMap; return m }
func L() s.MeterMap { return s.MeterMap{} }
func M() *s.MeterMap { return new(s.MeterMap) }
func N() int64 { return TicksPerStep * 4 }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sneak.go", goSource, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	tree := &goTree{fset: fset, module: "m31labs.dev/cicada", packages: map[string][]*goPackage{}, external: map[string]*types.Package{}, checking: map[*goPackage]bool{}}
	seqPackage := types.NewPackage(seqImportPath, "seq")
	seqPackage.Scope().Insert(types.NewConst(token.NoPos, seqPackage, "TicksPerWhole", types.Typ[types.UntypedInt], constant.MakeInt64(TicksPerWhole)))
	meterMap := types.NewNamed(types.NewTypeName(token.NoPos, seqPackage, "MeterMap", nil), types.NewStruct(nil, nil), nil)
	seqPackage.Scope().Insert(meterMap.Obj())
	seqPackage.MarkComplete()
	tree.external[seqImportPath] = seqPackage
	p := &goPackage{path: "m31labs.dev/cicada/sneak", files: []*ast.File{file}, rels: []string{"sneak/sneak.go"}}
	tree.check(p)
	bars, zeros := goFindings(t, "sneak/sneak.go", file, p.info)
	for name, want := range map[string]int{"A": 1, "B": 1, "C": 1, "D": 1, "E": 1, "F": 1, "G": 1, "H": 1, "I": 1, "J": 2} {
		if bars[name] != want {
			t.Errorf("%s: %d findings, want %d", name, bars[name], want)
		}
	}
	if bars["N"] != 0 || bars["const PPQ"] != 0 || bars["const TicksPerStep"] != 0 {
		t.Errorf("flagged a beat or a step: %v", bars)
	}
	for _, name := range []string{"K", "L", "M"} {
		if zeros[name] != 1 {
			t.Errorf("%s: %d zero maps, want 1", name, zeros[name])
		}
	}

	for _, c := range []struct {
		rel, source string
		want        int
	}{
		{"a.js", "const glob = \"lib/*.js\";\nconst bar = 3840;\nconst x = 1;\n", 1},
		{"b.js", "const sep = `//`; const bar = 3840;\n", 1},
		{"c.js", "const sep = '//'; const bar = 3840;\n", 1},
		{"d.js", "const re = /\\/\\*/; const bar = 3840;\n", 1},
		{"e.js", "const t = `${a /* x */ + `//`}`; const bar = 0xF00;\n", 1},
		{"f.js", "// const bar = 3840;\n/* const bar = 3840; */\nconst q = a / 2 / 3; // 3840\n", 0},
		{"g.js", "const ticks = 4 * PPQ;\n", 1},
		{"h.ts", "const s = \"it's\"; const bar = 7680; // 3840\n", 1},
		{"i.html", "<p>don't</p>\n<!-- 3840 -->\n<script>const s = '/*'; const bar = 3840;</script>\n<div data-bar=\"15360\"></div>\n", 2},
		{"j.gsx", "<p>{3840}</p>\n", 1},
	} {
		total := 0
		for _, n := range textFindings(c.rel, []byte(c.source)) {
			total += n
		}
		if total != c.want {
			t.Errorf("%s: %d findings, want %d", c.rel, total, c.want)
		}
	}
}
