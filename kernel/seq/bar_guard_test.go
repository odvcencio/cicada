package seq

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A bar's length comes from a MeterMap. TestNoFixedBarLength fails when the
// removed constant comes back, or when a fixed bar length appears outside the
// places listed below. In Go it finds, by parsing (so strings and comments do
// not count): the integer literals 3840, 7680 and 15360 (one, two and four 4/4
// bars), the float 3840.0, a product of 4 and 960 or of 4 and PPQ, a use of
// TicksPerWhole outside this package, and a constant or variable named like
// ticksPerBar that is set from literals. In JavaScript, HTML, TypeScript and
// .gsx sources it finds the same numbers and products in the text, with
// comments removed. Test files, testdata and generated minified assets are not
// scanned.
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
)

var barAllowlist = map[[2]string]barAllowance{
	// Go: whole notes, the seek unit, frames and the GoSX descriptor.
	{"kernel/seq/meter.go", "const TicksPerWhole"}:                              {1, "defines the whole note, PPQ * 4"},
	{"kernel/seq/step.go", "Pattern.Validate"}:                                  {1, wholeNote},
	{"kernel/cmd/command.go", "const SeekUnit"}:                                 {1, seekUnit},
	{"kernel/cmd/command.go", "Command.Validate"}:                               {1, wholeNote + "; kernel/cmd cannot import seq"},
	{"musictime/time.go", "MusicalTicks"}:                                       {1, wholeNote},
	{"notation/grid.go", "GridTicks"}:                                           {2, wholeNote},
	{"notation/modulators.go", "ParseModRate"}:                                  {1, wholeNote},
	{"project/modulators.go", "rateSource"}:                                     {2, wholeNote},
	{"project/validate.go", "ValidateProject"}:                                  {1, wholeNote},
	{"smf/project.go", "fromProject"}:                                           {1, "a whole note divided by the beat gives the time-signature denominator"},
	{"workstation/modulation.go", "modulationRate"}:                             {2, wholeNote},
	{"workstation/pattern_grid.go", "patternGridName"}:                          {1, wholeNote},
	{"internal/audiobackend/tymbal_period_native.go", "tymbalPeriodCandidates"}: {1, frameCount},
	{"sdk/gosxhost/backend.go", "const TicksPerBar"}:                            {2, gosxHost},

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

var (
	textBarNumber  = regexp.MustCompile(`\b(3_?840(\.0*)?|7680|15360)\b|\b960\s*\*\s*4\b|\b4\s*\*\s*960\b`)
	barNamePattern = regexp.MustCompile(`(?i)^(ticks_?per_?bar|bar_?ticks|[a-z]*ticksperbar)$`)
	jsLineComment  = regexp.MustCompile(`(^|[^:'"\\])//.*$`)
)

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

// isFour, isQuarter and barLike classify the operands of a product.
func isIntLit(e ast.Expr, value string) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && strings.ReplaceAll(lit.Value, "_", "") == value
}

func isQuarter(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name == "PPQ"
	case *ast.SelectorExpr:
		return v.Sel.Name == "PPQ"
	case *ast.ParenExpr:
		return isQuarter(v.X)
	}
	return isIntLit(e, "960")
}

func literalOnly(e ast.Expr) bool {
	only := true
	ast.Inspect(e, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.CallExpr, *ast.IndexExpr:
			only = false
		}
		return only
	})
	return only
}

// goBarFindings returns, for one Go file, the count of findings per enclosing
// top-level declaration.
func goBarFindings(t *testing.T, rel string, file *ast.File) map[string]int {
	found := map[string]int{}
	var nodes []ast.Node
	var names []string
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.GenDecl); ok {
			// Each constant, variable or type is its own place.
			for _, spec := range d.Specs {
				name := d.Tok.String()
				switch s := spec.(type) {
				case *ast.ValueSpec:
					name += " " + s.Names[0].Name
				case *ast.TypeSpec:
					name += " " + s.Name.Name
				}
				nodes, names = append(nodes, spec), append(names, name)
			}
			continue
		}
		nodes, names = append(nodes, decl), append(names, "?")
	}
	for i, decl := range nodes {
		where := names[i]
		switch d := decl.(type) {
		case *ast.FuncDecl:
			where = d.Name.Name
			if d.Recv != nil && len(d.Recv.List) == 1 {
				recv := d.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				if id, ok := recv.(*ast.Ident); ok {
					where = id.Name + "." + where
				}
			}
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.BasicLit:
				value := strings.ReplaceAll(v.Value, "_", "")
				if v.Kind == token.INT && (value == "3840" || value == "7680" || value == "15360") {
					found[where]++
				}
				if v.Kind == token.FLOAT {
					if f, err := strconv.ParseFloat(value, 64); err == nil && f == 3840 {
						found[where]++
					}
				}
			case *ast.BinaryExpr:
				if v.Op == token.MUL && (isIntLit(v.X, "4") && isQuarter(v.Y) || isQuarter(v.X) && isIntLit(v.Y, "4")) {
					found[where]++
				}
			case *ast.SelectorExpr:
				if x, ok := v.X.(*ast.Ident); ok && x.Name == "seq" {
					switch v.Sel.Name {
					case "TicksPerBar":
						t.Errorf("%s uses seq.TicksPerBar; ask a MeterMap for bar lengths", rel)
					case "TicksPerWhole":
						found[where]++
					}
				}
			case *ast.ValueSpec:
				for i, name := range v.Names {
					if strings.HasPrefix(rel, "kernel/seq/") && name.Name == "TicksPerBar" {
						t.Errorf("%s declares TicksPerBar again; a bar's length comes from a MeterMap", rel)
					}
					if barNamePattern.MatchString(name.Name) && i < len(v.Values) && literalOnly(v.Values[i]) {
						found[where]++
					}
				}
			case *ast.AssignStmt:
				if v.Tok == token.DEFINE {
					for i, lhs := range v.Lhs {
						if id, ok := lhs.(*ast.Ident); ok && barNamePattern.MatchString(id.Name) && i < len(v.Rhs) && literalOnly(v.Rhs[i]) {
							found[where]++
						}
					}
				}
			}
			return true
		})
	}
	return found
}

// textBarFindings returns, for one text source, the count of findings per
// trimmed line, with // and /* */ comments removed.
func textBarFindings(data []byte) map[string]int {
	found := map[string]int{}
	inBlock := false
	for _, line := range strings.Split(string(data), "\n") {
		code := line
		if inBlock {
			end := strings.Index(code, "*/")
			if end < 0 {
				continue
			}
			code, inBlock = code[end+2:], false
		}
		for {
			start := strings.Index(code, "/*")
			if start < 0 {
				break
			}
			end := strings.Index(code[start+2:], "*/")
			if end < 0 {
				code, inBlock = code[:start], true
				break
			}
			code = code[:start] + code[start+2+end+2:]
		}
		code = jsLineComment.ReplaceAllString(code, "$1")
		if n := len(textBarNumber.FindAllString(code, -1)); n > 0 {
			found[strings.TrimSpace(line)] += n
		}
	}
	return found
}

func TestNoFixedBarLength(t *testing.T) {
	root := barGuardRoot(t)
	found := map[[2]string]int{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		skipDir, scan := scannedSource(d)
		if skipDir && path != root {
			return filepath.SkipDir
		}
		if !scan {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var counts map[string]int
		if strings.HasSuffix(rel, ".go") {
			file, err := parser.ParseFile(fset, path, data, parser.SkipObjectResolution)
			if err != nil {
				return nil // a file that does not parse fails its own build
			}
			counts = goBarFindings(t, rel, file)
		} else {
			counts = textBarFindings(data)
		}
		for where, n := range counts {
			found[[2]string{rel, where}] = n
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var keys [][2]string
	for key := range found {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	for _, key := range keys {
		allowed, ok := barAllowlist[key]
		switch {
		case !ok:
			t.Errorf("%s (%s) has %d fixed bar or whole-note lengths; a bar's length comes from a MeterMap", key[0], key[1], found[key])
		case found[key] != allowed.count:
			t.Errorf("%s (%s) has %d, %d allowed (%s)", key[0], key[1], found[key], allowed.count, allowed.reason)
		}
	}
	for key, allowed := range barAllowlist {
		if _, ok := found[key]; !ok {
			t.Errorf("%s (%s) no longer has its %d allowed occurrences; remove the entry", key[0], key[1], allowed.count)
		}
	}
}
