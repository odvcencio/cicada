package project

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// MaxRouteHz is the fastest a source may move a registered parameter. The
// control grid is 375 Hz at 48 kHz; faster movement belongs in graph ops.
const MaxRouteHz = 20

// Modulator is one resolved control source (spec 24, lane 1). A synced source
// carries RateTicks, a free one RateMicroHz; exactly one is set. Phase, At and
// Value stay decimal numbers; the image builder converts them to the kernel's
// fixed point.
type Modulator struct {
	ID          string         `cicada:"Modulator identifier" json:"id" introduced:"cicada.project/2"`
	Kind        string         `cicada:"Source kind" json:"kind" introduced:"cicada.project/2"`
	Wave        string         `cicada:"LFO waveform" json:"wave,omitempty" introduced:"cicada.project/2"`
	RateTicks   int64          `cicada:"Cycle length of a tempo-synced source" unit:"tick" range:"1..268435456" json:"rate_ticks,omitempty" introduced:"cicada.project/2"`
	RateMicroHz int64          `cicada:"Cycle rate of a free-rate source" unit:"micro-Hz" range:"10000.." json:"rate_micro_hz,omitempty" introduced:"cicada.project/2"`
	Phase       float64        `cicada:"LFO start phase in cycles" range:"0..1" json:"phase,omitempty" introduced:"cicada.project/2"`
	Loop        string         `cicada:"Song loop behaviour" json:"loop,omitempty" introduced:"cicada.project/2"`
	Octaves     int            `cicada:"Sway layers" range:"1..4" json:"octaves,omitempty" introduced:"cicada.project/2"`
	Source      string         `cicada:"Envelope source track" json:"source,omitempty" introduced:"cicada.project/2"`
	AttackMS    float64        `cicada:"Envelope attack" unit:"ms" range:"0..10000" json:"attack_ms,omitempty" introduced:"cicada.project/2"`
	ReleaseMS   float64        `cicada:"Envelope release" unit:"ms" range:"0..10000" json:"release_ms,omitempty" introduced:"cicada.project/2"`
	Points      []ContourPoint `cicada:"Drawn breakpoints" json:"points,omitempty" introduced:"cicada.project/2"`
}

// ContourPoint is one breakpoint of a contour. At is a fraction of the period.
type ContourPoint struct {
	At    float64 `cicada:"Position as a fraction of the period" range:"0..1" json:"at" introduced:"cicada.project/2"`
	Value float64 `cicada:"Level" range:"-1..1" json:"value" introduced:"cicada.project/2"`
	Shape string  `cicada:"Incoming segment shape" json:"shape" introduced:"cicada.project/2"`
}

// ModRoute sends a source to a registered parameter. Depth is in the target's
// base units for add, and a plain factor for multiply. Routes apply in
// declaration order, after automation and macros and before per-step locks.
type ModRoute struct {
	Source string     `cicada:"Modulator identifier" json:"source" introduced:"cicada.project/2"`
	Target string     `cicada:"Registered parameter path" json:"target" introduced:"cicada.project/2"`
	Mode   string     `cicada:"add or multiply" json:"mode" introduced:"cicada.project/2"`
	Depth  SceneValue `cicada:"Depth in the target's base units, or a factor for multiply" json:"depth" introduced:"cicada.project/2"`
}

type routeFault struct{ code, text string }

func (f *routeFault) Error() string { return f.code + ": " + f.text }

func lowerModulators(p *Project, score *notation.Score) []notation.Diagnostic {
	if len(score.Modulators) == 0 && len(score.Routes) == 0 {
		return nil
	}
	var ds []notation.Diagnostic
	fail := func(code, message string, at notation.Position) {
		ds = append(ds, notation.Diagnostic{Code: code, Message: message, Severity: "error", Position: at})
	}
	for _, source := range score.Modulators {
		m := Modulator{ID: source.Name, Kind: source.Kind}
		get := func(name string) string { v, _ := source.Param(name); return v.Value }
		switch source.Kind {
		case notation.ModLFO:
			m.Wave = get("wave")
			if m.Wave == "" {
				m.Wave = "sine"
			}
			m.setRate(get("rate"))
			m.Phase, _ = strconv.ParseFloat(strings.TrimSuffix(get("phase"), "%"), 64)
			if strings.HasSuffix(get("phase"), "%") {
				m.Phase /= 100
			}
			m.Loop = loopOrWrap(get("loop"))
		case notation.ModSway:
			m.setRate(get("rate"))
			m.Octaves = 1
			if v := get("octaves"); v != "" {
				m.Octaves, _ = strconv.Atoi(v)
			}
			m.Loop = loopOrWrap(get("loop"))
		case notation.ModContour:
			m.setRate(get("period"))
			m.Loop = loopOrWrap(get("loop"))
			for _, pt := range source.Points {
				at, _ := notation.ParseContourPosition(pt.At)
				a, _ := at.Float64()
				v, _ := strconv.ParseFloat(pt.Value, 64)
				shape := pt.Shape
				if shape == "" {
					shape = "linear"
				}
				m.Points = append(m.Points, ContourPoint{At: a, Value: v, Shape: shape})
			}
		case notation.ModEnvelope:
			m.Source = get("source")
			m.AttackMS, m.ReleaseMS = 10, 100
			if v := get("attack"); v != "" {
				m.AttackMS = durationMS(v)
			}
			if v := get("release"); v != "" {
				m.ReleaseMS = durationMS(v)
			}
		}
		p.Modulators = append(p.Modulators, m)
	}
	for _, source := range score.Routes {
		depth, _ := source.Param("depth")
		mode := source.Mode
		if mode == "" {
			mode = "add"
		}
		value, unit, err := paramdefs.ParseBase(depth.Value)
		if err != nil {
			fail("CICADA-PARAM", "depth must be a number with an optional unit; actual "+depth.Value, depth.ValuePosition)
			continue
		}
		route := ModRoute{Source: source.Source, Target: source.Target, Mode: mode, Depth: SceneValue{Unit: unit, Number: &value}}
		if fault := checkRoute(p, route); fault != nil {
			fail(fault.code, fault.text, source.Position)
			continue
		}
		p.Routes = append(p.Routes, route)
	}
	if len(ds) > 0 {
		return ds
	}
	p.Format, p.Version = FormatID2, 2
	return fittedRateWarnings(p, score)
}

// FitTolerance is how far a `loop = continue` source's fitted rate may differ
// from the authored one before the compiler warns (a fraction of the rate).
const FitTolerance = 0.10

// loopFit is the whole number of cycles a `loop = continue` source plays in one
// pass of the song, and the rate that gives, in hertz. The song is the loop.
// N rounds to the nearest whole cycle and is at least 1.
func loopFit(p *Project, m Modulator) (cycles int64, authoredHz, fittedHz float64, ok bool) {
	ticks := p.SongEndTick()
	if p.Arrange != nil {
		ticks = 0
		for _, placement := range p.Arrange.Placements {
			ticks = max(ticks, placement.AtTick+placement.LengthTicks)
		}
	}
	if ticks == 0 || m.Loop != "continue" {
		return 0, 0, 0, false
	}
	tempo, err := NewTempoMap(p.MeterMap(), 48000, int64(p.TempoMilli), SongTempoChanges(p))
	if err != nil {
		return 0, 0, 0, false
	}
	seconds := float64(tempo.SampleAtTick(ticks)) / 48000
	switch {
	case m.RateTicks > 0:
		cycles = int64(float64(ticks)/float64(m.RateTicks) + .5)
		authoredHz = float64(ticks) / float64(m.RateTicks) / seconds
	case m.RateMicroHz > 0:
		authoredHz = float64(m.RateMicroHz) / 1e6
		cycles = int64(float64(authoredHz*seconds) + .5)
	default:
		return 0, 0, 0, false
	}
	cycles = max(1, cycles)
	return cycles, authoredHz, float64(cycles) / seconds, true
}

// fittedRateWarnings reports each continue-looped source whose fitted rate is
// more than FitTolerance away from the authored rate. It is CICADA-RATE at
// warning severity: the score still plays, at the fitted rate.
func fittedRateWarnings(p *Project, score *notation.Score) []notation.Diagnostic {
	var ds []notation.Diagnostic
	for i, m := range p.Modulators {
		cycles, authored, fitted, ok := loopFit(p, m)
		if ok && (fitted < authored*(1-FitTolerance) || fitted > authored*(1+FitTolerance)) {
			ds = append(ds, notation.Diagnostic{Code: "CICADA-RATE", Severity: "warning", Position: score.Modulators[i].Position,
				Message: fmt.Sprintf("%s %s: loop = continue fits %d whole cycles to the song, so it runs at %.4g Hz, not the authored %.4g Hz", m.Kind, m.ID, cycles, fitted, authored)})
		}
	}
	return ds
}

// LoopFit describes the fit of a source for explain: ok is false when the
// source does not use loop = continue.
func LoopFit(p *Project, id string) (cycles int64, authoredHz, fittedHz float64, ok bool) {
	for _, m := range p.Modulators {
		if m.ID == id {
			return loopFit(p, m)
		}
	}
	return 0, 0, 0, false
}

func loopOrWrap(v string) string {
	if v == "" {
		return "wrap"
	}
	return v
}

func durationMS(v string) float64 {
	n, _, _ := paramdefs.ParseBase(v) // ms or s, both returned in ms
	return n
}

func (m *Modulator) setRate(text string) {
	if rate, msg := notation.ParseModRate(text); msg == "" {
		if rate.Free {
			m.RateMicroHz = rate.MicroHertz
		} else {
			m.RateTicks = rate.Ticks
		}
	}
}

// maxTempoMilli is the fastest tempo the song reaches: the header, every scene
// or arrangement tempo step.
func maxTempoMilli(p *Project) int64 {
	top := int64(p.TempoMilli)
	for _, t := range SongTempoChanges(p) {
		top = max(top, int64(t.BPMMilli))
	}
	if p.Arrange != nil {
		for _, t := range p.Arrange.Tempos {
			top = max(top, int64(t.BPMMilli))
		}
	}
	return top
}

// fastestFactor is how many times faster the fastest layer runs.
func (m Modulator) fastestFactor() int64 {
	if m.Kind == notation.ModSway && m.Octaves > 1 {
		return 1 << (m.Octaves - 1)
	}
	return 1
}

// exceedsRate reports whether a source moves faster than MaxRouteHz at the
// song's fastest tempo.
func (m Modulator) exceedsRate(p *Project) bool {
	f := m.fastestFactor()
	if m.RateMicroHz > 0 {
		return m.RateMicroHz*f > MaxRouteHz*1_000_000
	}
	if m.RateTicks > 0 {
		// Hz = bpmMilli/1000 * 960 / 60 / ticks, against 20.
		return maxTempoMilli(p)*960*f > MaxRouteHz*60_000*m.RateTicks
	}
	return false
}

// checkRoute applies every rule a route must meet: its source exists, its
// target is a registered row on the smoothed path, it forms no cycle, the
// source is slow enough, and the depth fits the target's unit and range.
func checkRoute(p *Project, r ModRoute) *routeFault {
	var source *Modulator
	for i := range p.Modulators {
		if p.Modulators[i].ID == r.Source {
			source = &p.Modulators[i]
		}
	}
	if source == nil {
		return &routeFault{"CICADA-REFERENCE", "route source " + r.Source + " is not a declared modulator"}
	}
	if r.Mode != "add" && r.Mode != "multiply" {
		return &routeFault{"CICADA-PARAM", "route mode must be add or multiply; actual " + r.Mode}
	}
	owner, _, _ := strings.Cut(r.Target, ".")
	for _, effect := range p.Effects {
		if effect.ID == owner || strings.HasPrefix(r.Target, effect.ID+".") {
			if kind := semanticEffectKind(effect); kind == "reverb" || kind == "delay" {
				return &routeFault{"CICADA-ROUTE", r.Target + " is a legacy " + kind + " setting; every change rebuilds the effect, so a route to it is refused"}
			}
		}
	}
	resolved, err := ResolveParameterPath(p, r.Target)
	if err != nil {
		code := "CICADA-REFERENCE"
		if pe, ok := err.(*PathError); ok {
			code = pe.Code
		}
		return &routeFault{code, err.Error()}
	}
	d := resolved.Descriptor
	switch {
	case resolved.OwnerKind != "track":
		return &routeFault{"CICADA-ROUTE", r.Target + " is not a track row; routes reach track mixer and voice parameters"}
	case !d.Live || !d.Automatable || d.Curve == "toggle" || d.Curve == "enum" || d.Unit == "enum":
		return &routeFault{"CICADA-ROUTE", r.Target + " is not a continuous live row"}
	case routableRow(resolved.ID) != "":
		return &routeFault{"CICADA-ROUTE", routableRow(resolved.ID)}
	case d.SmoothingMS <= 0:
		return &routeFault{"CICADA-ROUTE", r.Target + " does not pass through the one-pole smoother"}
	}
	if source.Kind == notation.ModEnvelope && source.Source == owner {
		return &routeFault{"CICADA-ROUTE", "envelope " + source.ID + " follows " + source.Source + " and would modulate its own input"}
	}
	if source.Kind != notation.ModEnvelope && source.exceedsRate(p) {
		return &routeFault{"CICADA-RATE", "modulator " + source.ID + " runs above 20 Hz at the song's fastest tempo; a registered row moves at most 20 Hz"}
	}
	if r.Depth.Number == nil || !finite(*r.Depth.Number) {
		return &routeFault{"CICADA-PARAM", "depth must be a finite number"}
	}
	depth := *r.Depth.Number
	if r.Mode == "multiply" {
		if r.Depth.Unit != "unit" {
			return &routeFault{"CICADA-UNIT", "a multiply route takes a plain factor; actual " + r.Depth.Unit}
		}
		if math.Abs(depth) > 4 {
			return &routeFault{"CICADA-PARAM", "a multiply depth is at most 4 in size"}
		}
		return nil
	}
	want := map[string]string{"": "unit", "ratio": "unit", "semitone": "unit", "cent": "unit", "dB": "db", "Hz": "hz", "ms": "ms"}[d.Unit]
	if want == "" {
		want = strings.ToLower(d.Unit)
	}
	if r.Depth.Unit != want {
		return &routeFault{"CICADA-UNIT", fmt.Sprintf("%s takes a depth in %s; actual unit %s", r.Target, unitWord(d.Unit), unitWord(r.Depth.Unit))}
	}
	if math.Abs(depth) > d.Max-d.Min {
		return &routeFault{"CICADA-PARAM", fmt.Sprintf("depth %g is wider than the range of %s", depth, r.Target)}
	}
	return nil
}

func unitWord(u string) string {
	if u == "" || u == "unit" {
		return "plain numbers"
	}
	return u
}

func validateModulators(p *Project) error {
	if len(p.Modulators) == 0 && len(p.Routes) == 0 {
		return nil
	}
	if p.Format != FormatID2 {
		return fmt.Errorf("CICADA-VERSION: modulators require cicada.project/2")
	}
	if len(p.Modulators) > notation.MaxModulators {
		return fmt.Errorf("CICADA-LIMIT: at most %d modulators", notation.MaxModulators)
	}
	if len(p.Routes) > notation.MaxRoutes {
		return fmt.Errorf("CICADA-LIMIT: at most %d routes", notation.MaxRoutes)
	}
	tracks := map[string]bool{}
	for _, t := range p.Tracks {
		tracks[t.ID] = true
	}
	seen := map[string]bool{}
	for _, m := range p.Modulators {
		if !validID(m.ID) || seen[m.ID] {
			return fmt.Errorf("CICADA-DUPLICATE: modulator %q is invalid or repeated", m.ID)
		}
		seen[m.ID] = true
		synced, free := m.RateTicks != 0, m.RateMicroHz != 0
		if m.Kind != notation.ModEnvelope {
			if synced == free || m.RateTicks < 0 || m.RateTicks > 1<<28 || free && m.RateMicroHz < notation.MinFreeRateMicro {
				return fmt.Errorf("CICADA-PARAM: modulator %s needs one valid rate", m.ID)
			}
			if m.Loop != "wrap" && m.Loop != "continue" {
				return fmt.Errorf("CICADA-PARAM: modulator %s loop must be wrap or continue", m.ID)
			}
		}
		switch m.Kind {
		case notation.ModLFO:
			if m.Wave != "sine" && m.Wave != "triangle" && m.Wave != "square" && m.Wave != "sample-hold" || !(m.Phase >= 0 && m.Phase < 1) {
				return fmt.Errorf("CICADA-PARAM: lfo %s has an invalid wave or phase", m.ID)
			}
		case notation.ModSway:
			if m.Octaves < 1 || m.Octaves > notation.MaxSwayOctaves {
				return fmt.Errorf("CICADA-PARAM: sway %s octaves must be 1..%d", m.ID, notation.MaxSwayOctaves)
			}
		case notation.ModEnvelope:
			if !tracks[m.Source] || !(m.AttackMS >= 0 && m.AttackMS <= 10000 && m.ReleaseMS >= 0 && m.ReleaseMS <= 10000) {
				return fmt.Errorf("CICADA-REFERENCE: envelope %s needs a declared source track and times up to 10 s", m.ID)
			}
		case notation.ModContour:
			if free {
				return fmt.Errorf("CICADA-PARAM: contour %s period must use a note length or bar count", m.ID)
			}
			if len(m.Points) < 2 || len(m.Points) > notation.MaxContourPoints || m.Points[0].At != 0 || m.Points[len(m.Points)-1].At != 1 {
				return fmt.Errorf("CICADA-POSITION: contour %s runs from @0 to @1 with 2..%d points", m.ID, notation.MaxContourPoints)
			}
			for i, pt := range m.Points {
				if !(pt.Value >= -1 && pt.Value <= 1) || !notation.ModulatorShapes[pt.Shape] || i > 0 && pt.At <= m.Points[i-1].At {
					return fmt.Errorf("CICADA-POSITION: contour %s point %d is out of order, out of range or has an unknown shape", m.ID, i+1)
				}
			}
		default:
			return fmt.Errorf("CICADA-PARAM: unknown modulator kind %q", m.Kind)
		}
	}
	seenRoutes := make(map[struct{ source, target, mode string }]bool, len(p.Routes))
	for _, r := range p.Routes {
		if fault := checkRoute(p, r); fault != nil {
			return fault
		}
		key := struct{ source, target, mode string }{r.Source, r.Target, r.Mode}
		if seenRoutes[key] {
			return fmt.Errorf("CICADA-DUPLICATE: duplicate route %s -> %s", r.Source, r.Target)
		}
		seenRoutes[key] = true
	}
	return nil
}

// modulatorSources prints the declarations and routes in source order.
func modulatorSources(p *Project) ([]string, error) {
	var out []string
	num := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	for _, m := range p.Modulators {
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s {", m.Kind, m.ID)
		line := func(name, value string) { b.WriteString("\n  " + name + " = " + value) }
		switch m.Kind {
		case notation.ModLFO:
			line("wave", m.Wave)
			line("rate", rateSource(m))
			if m.Phase != 0 {
				line("phase", num(m.Phase))
			}
		case notation.ModSway:
			line("rate", rateSource(m))
			line("octaves", strconv.Itoa(m.Octaves))
		case notation.ModContour:
			line("period", rateSource(m))
		case notation.ModEnvelope:
			line("attack", num(m.AttackMS)+"ms")
			line("release", num(m.ReleaseMS)+"ms")
			line("source", m.Source)
		}
		if m.Kind != notation.ModEnvelope && m.Loop == "continue" {
			line("loop", "continue")
		}
		for _, pt := range m.Points {
			b.WriteString("\n  @" + num(pt.At) + " " + num(pt.Value))
			if pt.Shape != "linear" {
				b.WriteString(" " + pt.Shape)
			}
		}
		b.WriteString("\n}")
		out = append(out, b.String())
	}
	for _, r := range p.Routes {
		depth := ""
		if r.Mode == "multiply" {
			depth = num(*r.Depth.Number)
		} else {
			var err error
			if depth, err = sceneSettingSource(p, SceneSetting{Path: r.Target, Value: r.Depth}); err != nil {
				return nil, err
			}
		}
		out = append(out, "mod "+r.Source+" -> "+r.Target+" depth = "+depth+" "+r.Mode)
	}
	return out, nil
}

func rateSource(m Modulator) string {
	if m.RateMicroHz > 0 {
		return strconv.FormatFloat(float64(m.RateMicroHz)/1e6, 'f', -1, 64) + "Hz"
	}
	// A rate in bars counts 4/4 bars: a score declares no meter yet.
	if bars, whole := (seq.MeterMap{}).WholeBars(0, m.RateTicks); whole {
		return strconv.FormatInt(bars, 10) + "bars"
	}
	a, b := m.RateTicks, seq.TicksPerWhole
	for y := b; y != 0; a, y = y, a%y {
	}
	g := a
	return fmt.Sprintf("%d/%d", m.RateTicks/g, seq.TicksPerWhole/g)
}
