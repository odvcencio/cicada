package notation

import (
	"math/big"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	"m31labs.dev/cicada/kernel/seq"
)

// Modulator limits (spec 24, lane 1). The image table, the engine and the
// Studio list share these.
const (
	MaxModulators     = 32
	MaxRoutes         = 64
	MaxContourPoints  = 64
	MaxSwayOctaves    = 4
	MinFreeRateMicro  = 10_000 // 0.01 Hz
	maxModulatorTicks = 1 << 28
)

// Modulator kinds, as written in source.
const (
	ModLFO      = "lfo"
	ModEnvelope = "envelope"
	ModSway     = "sway"
	ModContour  = "contour"
)

// Modulator is one declared control source: an lfo, envelope, sway or
// contour. Settings keep their source spelling until project compilation.
type Modulator struct {
	Name     string
	Kind     string
	Params   []Param
	Points   []ContourPoint // contour only
	Position Position
}

// ContourPoint is one drawn breakpoint. At is a fraction of the period, Shape
// the interpolation that arrives at the point.
type ContourPoint struct {
	At, Value, Shape, Curve string
	Position                Position
}

// Route sends one modulator to one registered parameter path.
type Route struct {
	Source, Target, Mode string
	Params               []Param
	Position             Position
	SourcePosition       Position
	TargetPosition       Position
}

// Param returns the first setting with a name.
func (m Modulator) Param(name string) (Param, bool) { return findParam(m.Params, name) }

// Param returns the first setting with a name.
func (r Route) Param(name string) (Param, bool) { return findParam(r.Params, name) }

func findParam(params []Param, name string) (Param, bool) {
	for _, p := range params {
		if p.Name == name {
			return p, true
		}
	}
	return Param{}, false
}

func parseModulator(w *loweringWalker, n *gts.Node, kind string) Modulator {
	m := Modulator{Name: w.declaration(w.Field(n, "name")), Kind: kind, Position: w.position(n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch w.Type(c) {
		case "param_decl":
			m.Params = append(m.Params, parseParam(w, c))
		case "contour_point":
			m.Points = append(m.Points, ContourPoint{
				At: w.Text(w.Field(c, "at")), Value: w.Text(w.Field(c, "value")),
				Shape: w.Text(w.Field(c, "shape")), Curve: w.Text(w.Field(c, "curve")), Position: w.position(c),
			})
		}
	}
	return m
}

func parseRoute(w *loweringWalker, n *gts.Node) Route {
	source, target := w.Field(n, "source"), w.Field(n, "target")
	r := Route{
		Source: strings.Join(strings.Fields(w.Text(source)), ""), Target: w.parameterPath(w.Text(target), w.position(target)),
		Mode: w.Text(w.Field(n, "mode")), Position: w.position(n), SourcePosition: w.position(source), TargetPosition: w.position(target),
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); w.Type(c) == "param_decl" {
			r.Params = append(r.Params, parseParam(w, c))
		}
	}
	return r
}

// ModRate is a source rate or period. A synced rate is a whole number of
// ticks (960 per quarter note, 3840 per bar); a free rate is micro-hertz.
type ModRate struct {
	Free       bool
	Ticks      int64 // synced
	MicroHertz int64 // free
}

// ParseModRate reads a note length (`1/4`, `1/8.`, `1/8T`, `8bars`) or a
// free rate in hertz (`0.07Hz`, `1kHz`). Free rates keep exact micro-hertz.
func ParseModRate(text string) (ModRate, string) {
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)
	switch {
	case strings.HasSuffix(lower, "khz"):
		return parseFreeRate(text[:len(text)-3], 1_000_000_000)
	case strings.HasSuffix(lower, "hz"):
		return parseFreeRate(text[:len(text)-2], 1_000_000)
	case strings.HasSuffix(text, "bars"), strings.HasSuffix(text, "bar"):
		number := strings.TrimSuffix(strings.TrimSuffix(text, "bars"), "bar")
		r, ok := new(big.Rat).SetString(number)
		if !ok || r.Sign() <= 0 {
			return ModRate{}, "expected a positive number of bars"
		}
		return syncedRate(r.Mul(r, big.NewRat(seq.TicksPerBar, 1)), text)
	case strings.Contains(text, "/"):
		body := text
		num, den := int64(1), int64(1)
		switch {
		case strings.HasSuffix(body, "."):
			body, num, den = body[:len(body)-1], 3, 2
		case strings.HasSuffix(body, "t"), strings.HasSuffix(body, "T"):
			body, num, den = body[:len(body)-1], 2, 3
		}
		r, ok := new(big.Rat).SetString(body)
		if !ok || r.Sign() <= 0 {
			return ModRate{}, "expected a note length such as 1/4"
		}
		r.Mul(r, big.NewRat(seq.TicksPerBar*num, den))
		return syncedRate(r, text)
	}
	return ModRate{}, "expected a note length such as 1/4, a bar count such as 8bars, or a rate in Hz"
}

func syncedRate(ticks *big.Rat, text string) (ModRate, string) {
	if !ticks.IsInt() || !ticks.Num().IsInt64() || ticks.Num().Int64() < 1 {
		return ModRate{}, "the length " + text + " is not a whole number of ticks"
	}
	if ticks.Num().Int64() > maxModulatorTicks {
		return ModRate{}, "the length " + text + " is too long"
	}
	return ModRate{Ticks: ticks.Num().Int64()}, ""
}

func parseFreeRate(number string, scale int64) (ModRate, string) {
	r, ok := new(big.Rat).SetString(number)
	if !ok || r.Sign() <= 0 {
		return ModRate{}, "expected a positive rate in Hz"
	}
	r.Mul(r, big.NewRat(scale, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return ModRate{}, "a rate has at most six decimal places in Hz"
	}
	micro := r.Num().Int64()
	if micro < 1 || micro > 1_000_000_000_000 {
		return ModRate{}, "the rate is outside the supported range"
	}
	return ModRate{Free: true, MicroHertz: micro}, ""
}

// ParseContourPosition returns a contour point's position within its period
// as an exact fraction in [0, 1].
func ParseContourPosition(text string) (*big.Rat, bool) {
	r, ok := new(big.Rat).SetString(strings.TrimPrefix(text, "@"))
	if !ok || r.Sign() < 0 || r.Cmp(big.NewRat(1, 1)) > 0 {
		return nil, false
	}
	return r, true
}

// ModulatorShapes lists the point shapes a contour accepts. `curve` is
// refused so the drawn-source word and the tension shape never meet.
var ModulatorShapes = map[string]bool{"step": true, "linear": true, "smooth": true, "exponential": true}

var modulatorWaves = map[string]bool{"sine": true, "triangle": true, "square": true, "sample-hold": true}

var modulatorKeys = map[string]map[string]bool{
	ModLFO:      {"wave": true, "rate": true, "phase": true, "loop": true},
	ModEnvelope: {"attack": true, "release": true, "source": true},
	ModSway:     {"rate": true, "octaves": true, "loop": true},
	ModContour:  {"period": true, "loop": true},
}

// modulatorsPlayable keeps detail-layer forms from reaching a renderer before
// every surface can play them (spec 16, "Scope and syntax gate"). The change
// that makes the engine evaluate modulators sets it.
const modulatorsPlayable = false

// ValidateModulators checks the declared sources and routes of a score. It
// reports spelling, range and reference errors; target resolution and the
// rate cap against a registered row belong to project compilation.
func ValidateModulators(s *Score) []Diagnostic {
	if len(s.Modulators) == 0 && len(s.Routes) == 0 {
		return nil
	}
	var ds []Diagnostic
	add := func(code, message string, p Position) {
		ds = append(ds, Diagnostic{Code: code, Message: message, Severity: "error", Position: p})
	}
	if len(s.Modulators) > MaxModulators {
		add("CICADA-LIMIT", "a project has at most "+strconv.Itoa(MaxModulators)+" modulators", s.Modulators[MaxModulators].Position)
	}
	if len(s.Routes) > MaxRoutes {
		add("CICADA-LIMIT", "a project has at most "+strconv.Itoa(MaxRoutes)+" routes", s.Routes[MaxRoutes].Position)
	}
	tracks := map[string]bool{}
	for _, t := range s.Tracks {
		tracks[t.Name] = true
	}
	known := map[string]string{}
	firstDeclared := map[string]Position{}
	for _, m := range s.Modulators {
		if first, ok := firstDeclared[m.Name]; ok {
			ds = append(ds, Diagnostic{Code: "CICADA-DUPLICATE", Severity: "error", Message: "duplicate modulator " + m.Name, Position: m.Position, Related: first})
		} else {
			firstDeclared[m.Name] = m.Position
		}
		known[m.Name] = m.Kind
		keys := modulatorKeys[m.Kind]
		seen := map[string]bool{}
		for _, p := range m.Params {
			if !keys[p.Name] {
				add("CICADA-PARAM", m.Kind+" "+m.Name+" has no setting "+p.Name, p.Position)
				continue
			}
			if seen[p.Name] {
				add("CICADA-DUPLICATE", "duplicate setting "+p.Name+" in "+m.Kind+" "+m.Name, p.Position)
			}
			seen[p.Name] = true
			ds = append(ds, validateModulatorSetting(m, p, tracks)...)
		}
		required := map[string]string{ModLFO: "rate", ModSway: "rate", ModContour: "period", ModEnvelope: "source"}[m.Kind]
		if !seen[required] {
			add("CICADA-PARAM", m.Kind+" "+m.Name+" needs "+required, m.Position)
		}
		if m.Kind == ModContour {
			ds = append(ds, validateContour(m)...)
		}
	}
	seenRoutes := map[string]bool{}
	for _, r := range s.Routes {
		if _, ok := known[r.Source]; !ok {
			add("CICADA-REFERENCE", "route source "+r.Source+" is not a declared lfo, envelope, sway or contour", r.SourcePosition)
		}
		switch r.Mode {
		case "", "add", "multiply":
		default:
			add("CICADA-PARAM", "route mode must be add or multiply; actual "+r.Mode, r.Position)
		}
		depths := 0
		for _, p := range r.Params {
			if p.Name != "depth" {
				add("CICADA-PARAM", "route has no setting "+p.Name, p.Position)
				continue
			}
			depths++
			if depths > 1 {
				add("CICADA-DUPLICATE", "duplicate setting depth", p.Position)
			}
			if !validDepth(p.Value) {
				add("CICADA-PARAM", "depth must be a number with an optional unit; actual "+p.Value, p.ValuePosition)
			}
		}
		if depths == 0 {
			add("CICADA-PARAM", "route needs depth", r.Position)
		}
		mode := r.Mode
		if mode == "" {
			mode = "add"
		}
		key := r.Source + ">" + r.Target + ">" + mode
		if seenRoutes[key] {
			add("CICADA-DUPLICATE", "duplicate route "+r.Source+" -> "+r.Target, r.Position)
		}
		seenRoutes[key] = true
	}
	if !modulatorsPlayable {
		var first Position
		if len(s.Modulators) > 0 {
			first = s.Modulators[0].Position
		} else {
			first = s.Routes[0].Position
		}
		add("CICADA-UNSUPPORTED", "modulators and routes parse and format, but no renderer plays them yet", first)
	}
	return ds
}

func validDepth(value string) bool {
	v := strings.TrimSpace(value)
	for _, unit := range []string{"dB", "db", "kHz", "khz", "Hz", "hz", "ms", "s", "%"} {
		if strings.HasSuffix(v, unit) {
			v = strings.TrimSuffix(v, unit)
			break
		}
	}
	f, err := strconv.ParseFloat(v, 64)
	return err == nil && f == f && f < 1e9 && f > -1e9
}

func validateModulatorSetting(m Modulator, p Param, tracks map[string]bool) []Diagnostic {
	bad := func(code, message string) []Diagnostic {
		return []Diagnostic{{Code: code, Message: message, Severity: "error", Position: p.ValuePosition}}
	}
	switch p.Name {
	case "wave":
		if !modulatorWaves[p.Value] {
			return bad("CICADA-PARAM", "wave must be sine, triangle, square or sample-hold; actual "+p.Value)
		}
	case "rate", "period":
		if _, msg := ParseModRate(p.Value); msg != "" {
			return bad("CICADA-PARAM", p.Name+": "+msg)
		}
		r, _ := ParseModRate(p.Value)
		if r.Free && r.MicroHertz < MinFreeRateMicro {
			return bad("CICADA-PARAM", p.Name+" is below 0.01 Hz")
		}
		if r.Free && m.Kind == ModContour {
			return bad("CICADA-PARAM", "a contour period is a note length or a bar count, not a rate in Hz")
		}
	case "phase":
		f, err := strconv.ParseFloat(strings.TrimSuffix(p.Value, "%"), 64)
		if strings.HasSuffix(p.Value, "%") {
			f /= 100
		}
		if err != nil || !(f >= 0 && f < 1) {
			return bad("CICADA-PARAM", "phase must be at least 0 and below 1 cycle; actual "+p.Value)
		}
	case "loop":
		if p.Value != "wrap" && p.Value != "continue" {
			return bad("CICADA-PARAM", "loop must be wrap or continue; actual "+p.Value)
		}
	case "octaves":
		n, err := strconv.Atoi(p.Value)
		if err != nil || n < 1 || n > MaxSwayOctaves {
			return bad("CICADA-PARAM", "octaves must be an integer from 1 to "+strconv.Itoa(MaxSwayOctaves)+"; actual "+p.Value)
		}
	case "attack", "release":
		ms := parseDurationMS(p.Value)
		if !(ms >= 0 && ms <= 10_000) {
			return bad("CICADA-PARAM", p.Name+" must be a time from 0ms to 10s; actual "+p.Value)
		}
	case "source":
		if !tracks[p.Value] {
			return bad("CICADA-REFERENCE", "envelope source "+p.Value+" is not a declared track")
		}
	}
	return nil
}

func validateContour(m Modulator) []Diagnostic {
	var ds []Diagnostic
	add := func(code, message string, p Position) {
		ds = append(ds, Diagnostic{Code: code, Message: message, Severity: "error", Position: p})
	}
	if len(m.Points) < 2 {
		add("CICADA-PARAM", "contour "+m.Name+" needs at least two points", m.Position)
		return ds
	}
	if len(m.Points) > MaxContourPoints {
		add("CICADA-LIMIT", "a contour has at most "+strconv.Itoa(MaxContourPoints)+" points", m.Points[MaxContourPoints].Position)
	}
	var previous *big.Rat
	for i, pt := range m.Points {
		at, ok := ParseContourPosition(pt.At)
		if !ok {
			add("CICADA-POSITION", "a contour position is a fraction of the period from @0 to @1; actual "+pt.At, pt.Position)
			continue
		}
		if i == 0 && at.Sign() != 0 {
			add("CICADA-POSITION", "the first contour point must be at @0", pt.Position)
		}
		if i == len(m.Points)-1 && at.Cmp(big.NewRat(1, 1)) != 0 {
			add("CICADA-POSITION", "the last contour point must be at @1", pt.Position)
		}
		if previous != nil && at.Cmp(previous) <= 0 {
			add("CICADA-POSITION", "contour points must increase", pt.Position)
		}
		previous = at
		v, err := strconv.ParseFloat(pt.Value, 64)
		if err != nil || v < -1 || v > 1 {
			add("CICADA-PARAM", "a contour value is a plain number from -1 to 1; actual "+pt.Value, pt.Position)
		}
		switch {
		case pt.Shape == "":
		case pt.Shape == "curve":
			add("CICADA-UNSUPPORTED", "a contour does not accept the curve shape; use step, linear, smooth or exponential", pt.Position)
		case !ModulatorShapes[pt.Shape]:
			add("CICADA-PARAM", "contour shape must be step, linear, smooth or exponential; actual "+pt.Shape, pt.Position)
		case pt.Curve != "":
			add("CICADA-PARAM", "only the curve shape takes a number", pt.Position)
		}
		if i == 0 && pt.Shape != "" {
			add("CICADA-PARAM", "the first contour point has no incoming segment, so it takes no shape", pt.Position)
		}
	}
	return ds
}

// formatModulatorDeclaration prints a source or route from its CST: one
// setting per line, one contour point per line, a route on one line. Every
// comment inside the declaration stays, on its own line: those inside a
// setting, point or route go just before the line they were in, so a second
// pass reads them where the first put them.
func formatModulatorDeclaration(node *gts.Node, w *walk.Walker) string {
	text := func(n *gts.Node) string {
		raw := w.Text(n)
		// Comments are emitted separately. Remove their exact CST ranges before
		// normalizing whitespace so a line comment cannot swallow the tokens
		// that follow it, or be duplicated in a qualified name.
		var comments []*gts.Node
		var collect func(*gts.Node)
		collect = func(parent *gts.Node) {
			for i := 0; i < parent.ChildCount(); i++ {
				child := parent.Child(i)
				if w.Type(child) == "comment" {
					comments = append(comments, child)
				} else {
					collect(child)
				}
			}
		}
		collect(n)
		if len(comments) > 0 {
			var clean strings.Builder
			cursor := uint32(0)
			for _, comment := range comments {
				start, end := comment.StartByte()-n.StartByte(), comment.EndByte()-n.StartByte()
				clean.WriteString(raw[cursor:start])
				clean.WriteByte(' ')
				cursor = end
			}
			clean.WriteString(raw[cursor:])
			raw = clean.String()
		}
		if strings.Contains(raw, `"`) { // keep the whitespace inside string literals
			return raw
		}
		return canonicalNumericUnit(strings.Join(strings.Fields(raw), " "))
	}
	var inner func(n *gts.Node, out *[]string)
	inner = func(n *gts.Node, out *[]string) {
		for i := 0; i < n.ChildCount(); i++ {
			c := n.Child(i)
			if w.Type(c) == "comment" {
				*out = append(*out, strings.TrimSpace(w.Text(c)))
				continue
			}
			inner(c, out)
		}
	}
	if w.Type(node) == "mod_decl" {
		var comments []string
		inner(node, &comments)
		line := "mod " + text(w.Field(node, "source")) + " -> " + text(w.Field(node, "target"))
		for i := 0; i < node.NamedChildCount(); i++ {
			if c := node.NamedChild(i); w.Type(c) == "param_decl" {
				line += " " + w.Text(w.Field(c, "name")) + " = " + text(w.Field(c, "value"))
			}
		}
		if mode := w.Field(node, "mode"); mode != nil {
			line += " " + w.Text(mode)
		}
		return strings.Join(append(comments, line), "\n")
	}
	var headerComments []string
	inner(w.Field(node, "name"), &headerComments)
	lines := append(headerComments, w.Type(node)[:len(w.Type(node))-len("_decl")]+" "+text(w.Field(node, "name"))+" {")
	empty := true
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		var line string
		switch w.Type(c) {
		case "param_decl":
			line = "  " + w.Text(w.Field(c, "name")) + " = " + text(w.Field(c, "value"))
		case "contour_point":
			line = "  " + w.Text(w.Field(c, "at")) + " " + text(w.Field(c, "value"))
			if shape := w.Field(c, "shape"); shape != nil {
				line += " " + w.Text(shape)
				if curve := w.Field(c, "curve"); curve != nil {
					line += " " + text(curve)
				}
			}
		case "comment":
			lines = append(lines, "  "+strings.TrimSpace(w.Text(c)))
			empty = false
			continue
		default:
			continue
		}
		var comments []string
		inner(c, &comments)
		for _, comment := range comments {
			lines = append(lines, "  "+comment)
		}
		lines = append(lines, line)
		empty = false
	}
	if empty {
		lines[len(lines)-1] += "}"
		return strings.Join(lines, "\n")
	}
	return strings.Join(append(lines, "}"), "\n")
}
