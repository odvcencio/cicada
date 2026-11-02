package notation

import (
	"bytes"
	"strings"
	"testing"
)

const modulatorHeader = "tempo 120\ntrack pad acid {}\ntrack drums acid {}\npattern p { 1 . 3 . }\nscene s { pad = p }\nsong { s*2 }\n"

func modulatorCodes(t *testing.T, source string) []string {
	t.Helper()
	_, ds := Parse([]byte(modulatorHeader + source))
	var codes []string
	for _, d := range ds {
		if d.Severity == "error" {
			codes = append(codes, d.Code+": "+d.Message)
		}
	}
	return codes
}

func TestModulatorsParseIntoScore(t *testing.T) {
	score, _ := Parse([]byte(modulatorHeader + `
lfo drift { wave = sine rate = 8bars phase = 0 }
envelope duck { attack = 5ms release = 120ms source = drums }
sway wander { rate = 2bars octaves = 2 }
contour swell { period = 4bars @0 -1 @1/4 1 smooth @1 -1 }
mod drift -> pad.pan depth = 0.15 add
mod wander -> pad.level depth = 1.5dB
`))
	if score == nil || len(score.Modulators) != 4 || len(score.Routes) != 2 {
		t.Fatalf("score = %+v", score)
	}
	kinds := []string{}
	for _, m := range score.Modulators {
		kinds = append(kinds, m.Kind)
	}
	if got := strings.Join(kinds, ","); got != "lfo,envelope,sway,contour" {
		t.Fatalf("kinds = %s", got)
	}
	swell := score.Modulators[3]
	if len(swell.Points) != 3 || swell.Points[1].Shape != "smooth" || swell.Points[1].At != "@1/4" {
		t.Fatalf("contour = %+v", swell)
	}
	r := score.Routes[0]
	if r.Source != "drift" || r.Target != "pad.pan" || r.Mode != "add" || r.Params[0].Value != "0.15" {
		t.Fatalf("route = %+v", r)
	}
	if score.Routes[1].Mode != "" {
		t.Fatalf("a route without a mode word has mode %q", score.Routes[1].Mode)
	}
}

// Until a renderer plays them the forms fail with one CICADA-UNSUPPORTED and
// nothing else, so the gate is the only thing standing between source and sound.
func TestModulatorsAreGatedUntilPlayable(t *testing.T) {
	codes := modulatorCodes(t, "lfo drift { rate = 1/4 }\nmod drift -> pad.pan depth = 0.1\n")
	if len(codes) != 1 || !strings.HasPrefix(codes[0], "CICADA-UNSUPPORTED") {
		t.Fatalf("codes = %v", codes)
	}
}

func TestModulatorDiagnostics(t *testing.T) {
	cases := []struct{ name, source, want string }{
		{"unknown setting", "lfo a { rate = 1/4 shape = sine }", "CICADA-PARAM: lfo a has no setting shape"},
		{"missing rate", "lfo a { wave = sine }", "CICADA-PARAM: lfo a needs rate"},
		{"bad wave", "lfo a { rate = 1/4 wave = saw }", "CICADA-PARAM: wave must be"},
		{"rate without unit", "lfo a { rate = 4 }", "CICADA-PARAM: rate: expected a note length"},
		{"rate below 0.01 Hz", "lfo a { rate = 0.001Hz }", "CICADA-PARAM: rate is below 0.01 Hz"},
		{"non-integer ticks", "lfo a { rate = 1/7 }", "CICADA-PARAM: rate: the length 1/7 is not a whole number of ticks"},
		{"bad loop", "lfo a { rate = 1/4 loop = hold }", "CICADA-PARAM: loop must be wrap or continue"},
		{"phase out of range", "lfo a { rate = 1/4 phase = 1 }", "CICADA-PARAM: phase must be"},
		{"phase NaN", "lfo a { rate = 1/4 phase = nan }", "CICADA-PARAM: phase must be"},
		{"octaves", "sway a { rate = 1bar octaves = 5 }", "CICADA-PARAM: octaves must be"},
		{"envelope needs source", "envelope e { attack = 5ms }", "CICADA-PARAM: envelope e needs source"},
		{"envelope source is a track", "envelope e { source = ghost }", "CICADA-REFERENCE: envelope source ghost"},
		{"envelope time", "envelope e { source = drums attack = 20s }", "CICADA-PARAM: attack must be"},
		{"duplicate setting", "lfo a { rate = 1/4 rate = 1/8 }", "CICADA-DUPLICATE: duplicate setting rate"},
		{"duplicate name", "lfo a { rate = 1/4 }\nsway a { rate = 1/4 }", "CICADA-DUPLICATE"},
		{"contour one point", "contour c { period = 1bar @0 0 }", "CICADA-PARAM: contour c needs at least two points"},
		{"contour start", "contour c { period = 1bar @1/4 0 @1 0 }", "CICADA-POSITION: the first contour point must be at @0"},
		{"contour end", "contour c { period = 1bar @0 0 @1/2 0 }", "CICADA-POSITION: the last contour point must be at @1"},
		{"contour order", "contour c { period = 1bar @0 0 @1/2 0 @1/2 0 @1 0 }", "CICADA-POSITION: contour points must increase"},
		{"contour range", "contour c { period = 1bar @0 0 @1 2 }", "CICADA-PARAM: a contour value"},
		{"contour curve shape", "contour c { period = 1bar @0 0 @1 1 curve 2 }", "CICADA-UNSUPPORTED: a contour does not accept the curve shape"},
		{"contour first shape", "contour c { period = 1bar @0 0 smooth @1 1 }", "CICADA-PARAM: the first contour point"},
		{"contour period", "contour c { @0 0 @1 1 }", "CICADA-PARAM: contour c needs period"},
		{"route source", "mod ghost -> pad.pan depth = 0.1", "CICADA-REFERENCE: route source ghost"},
		{"route mode", "lfo a { rate = 1/4 }\nmod a -> pad.pan depth = 0.1 mix", "CICADA-PARAM: route mode must be"},
		{"route depth", "lfo a { rate = 1/4 }\nmod a -> pad.pan", "CICADA-PARAM: route needs depth"},
		{"route depth text", "lfo a { rate = 1/4 }\nmod a -> pad.pan depth = fast", "CICADA-PARAM: depth must be"},
		{"route setting", "lfo a { rate = 1/4 }\nmod a -> pad.pan depth = 0.1 slew = 2", "CICADA-PARAM: route has no setting slew"},
		{"route repeated", "lfo a { rate = 1/4 }\nmod a -> pad.pan depth = 0.1\nmod a -> pad.pan depth = 0.2", "CICADA-DUPLICATE: duplicate route"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			codes := modulatorCodes(t, c.source+"\n")
			for _, code := range codes {
				if strings.HasPrefix(code, c.want) {
					return
				}
			}
			t.Fatalf("want %q in %v", c.want, codes)
		})
	}
}

func TestModulatorFormatQualifiedNamesWithComments(t *testing.T) {
	source := []byte(modulatorHeader + `
lfo ns. // source declaration
  a { rate = 1/4 }
mod ns. // source reference
  a -> pad.pan depth = 0.1
`)
	doc, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	once, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, comment := range []string{"// source declaration", "// source reference"} {
		if strings.Count(string(once), comment) != 1 {
			t.Errorf("comment %q not preserved exactly once:\n%s", comment, once)
		}
	}
	doc, err = ParseDocument(once)
	if err != nil {
		t.Fatalf("formatted source cannot be parsed: %v\n%s", err, once)
	}
	twice, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("format not idempotent:\n%s\n---\n%s", once, twice)
	}
}

func TestModulatorLimits(t *testing.T) {
	var b strings.Builder
	for i := 0; i <= MaxModulators; i++ {
		b.WriteString("lfo l" + strings.Repeat("x", 0) + string(rune('a'+i/26)) + string(rune('a'+i%26)) + " { rate = 1/4 }\n")
	}
	found := false
	for _, code := range modulatorCodes(t, b.String()) {
		found = found || strings.HasPrefix(code, "CICADA-LIMIT: a project has at most 32 modulators")
	}
	if !found {
		t.Fatal("33 modulators were accepted")
	}
	b.Reset()
	b.WriteString("lfo a { rate = 1/4 }\n")
	for i := 0; i <= MaxRoutes; i++ {
		b.WriteString("mod a -> pad.pan depth = 0." + string(rune('0'+i/10)) + string(rune('0'+i%10)) + "\n")
	}
	found = false
	for _, code := range modulatorCodes(t, b.String()) {
		found = found || strings.HasPrefix(code, "CICADA-LIMIT: a project has at most 64 routes")
	}
	if !found {
		t.Fatal("65 routes were accepted")
	}
}

func TestParseModRate(t *testing.T) {
	cases := []struct {
		text  string
		ticks int64
		micro int64
		err   bool
	}{
		{"1/4", 960, 0, false}, {"1/4.", 1440, 0, false}, {"1/8T", 320, 0, false}, {"1/1", 3840, 0, false},
		{"8bars", 30720, 0, false}, {"1bar", 3840, 0, false}, {"0.5bars", 1920, 0, false},
		{"0.07Hz", 0, 70_000, false}, {"20Hz", 0, 20_000_000, false}, {"1kHz", 0, 1_000_000_000, false}, {"0.000001Hz", 0, 1, false},
		{"0.0000001Hz", 0, 0, true}, {"1/7", 0, 0, true}, {"0bars", 0, 0, true}, {"-1/4", 0, 0, true}, {"4", 0, 0, true}, {"Hz", 0, 0, true},
	}
	for _, c := range cases {
		got, msg := ParseModRate(c.text)
		if (msg != "") != c.err || got.Ticks != c.ticks || got.MicroHertz != c.micro || got.Free != (c.micro != 0) {
			t.Errorf("ParseModRate(%q) = %+v, %q", c.text, got, msg)
		}
	}
}

func TestModulatorFormatIsIdempotentAndKeepsComments(t *testing.T) {
	source := []byte(modulatorHeader + `
// slow drift
lfo   drift{wave=sine   rate=8bars phase=0 // start at zero
}
envelope duck {attack=5ms release=120ms source=drums}
sway wander { rate = 2bars
  octaves = 2 }
contour swell { period=4bars @0 -1 @1/4 1 smooth @1/2 0 curve 2 @1 -1 // end
}
mod drift -> pad.pan depth=0.15 add
mod wander -> pad.level depth = 1.5db // quiet
lfo empty {}
`)
	document, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	once, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"// slow drift\nlfo drift {\n  wave = sine\n  rate = 8bars\n  phase = 0\n  // start at zero\n}",
		"envelope duck {\n  attack = 5ms\n  release = 120ms\n  source = drums\n}",
		"  @1/4 1 smooth\n  @1/2 0 curve 2\n  @1 -1\n  // end\n}",
		"mod drift -> pad.pan depth = 0.15 add",
		"mod wander -> pad.level depth = 1.5dB\n\n// quiet\nlfo empty {}",
	} {
		if !strings.Contains(string(once), want) {
			t.Errorf("formatted source lacks %q:\n%s", want, once)
		}
	}
	again, err := ParseDocument(once)
	if err != nil {
		t.Fatalf("formatted source does not parse: %v\n%s", err, once)
	}
	twice, err := Format(again)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("not idempotent:\n%s\n---\n%s", once, twice)
	}
	before, _ := Parse(source)
	after, _ := Parse(once)
	if len(before.Modulators) != len(after.Modulators) || len(before.Routes) != len(after.Routes) {
		t.Fatal("formatting changed the declarations")
	}
}

// The new words open a declaration only where a declaration may start; every
// other place that spells them keeps its meaning.
func TestModulatorWordsStayIdentifiersElsewhere(t *testing.T) {
	score, ds := Parse([]byte(`tempo 120
track mod acid { level = -3dB }
track lfo acid {}
instrument tone { param sway = 1 voice mono { out = saw(220Hz) * mod(contour) } }
pattern p { 1 . }
scene s { mod = p lfo = p }
song { s }
`))
	for _, d := range ds {
		if strings.Contains(d.Message, "unexpected") || d.Code == "CICADA-SYNTAX" {
			t.Fatalf("syntax diagnostic: %v", d)
		}
	}
	if score == nil || len(score.Tracks) != 2 || len(score.Modulators) != 0 {
		t.Fatalf("score = %+v", score)
	}
}

// Every comment inside a declaration survives `fmt`, including those inside a
// setting, a contour point and a route, and the second pass changes nothing.
func TestModulatorFormatKeepsEveryComment(t *testing.T) {
	source := []byte(modulatorHeader + `
lfo a {
  rate = // why this rate
    8bars
  wave = sine // inline trailing
}
contour c {
  period = 1bar
  @0 // start
    0
  @1/2 // mid
    1 smooth
  @1 0 // end
}
mod a -> // to the pad
  pad.pan depth = // gentle
  0.1 add
mod c -> pad.level // second
  depth = 1dB
  // before mode
  multiply
`)
	var once, twice []byte
	for pass, in := range []*[]byte{&once, &twice} {
		src := source
		if pass == 1 {
			src = once
		}
		document, err := ParseDocument(src)
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if *in, err = Format(document); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"// why this rate", "// inline trailing", "// start", "// mid", "// end", "// to the pad", "// gentle", "// second", "// before mode"} {
		if !strings.Contains(string(once), want) {
			t.Errorf("comment %q was dropped:\n%s", want, once)
		}
	}
	if strings.Count(string(source), "//") != strings.Count(string(once), "//") {
		t.Errorf("comment count %d became %d", strings.Count(string(source), "//"), strings.Count(string(once), "//"))
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("not idempotent:\n%s\n---\n%s", once, twice)
	}
}

func TestRouteWithoutModeDuplicatesAdd(t *testing.T) {
	codes := modulatorCodes(t, "lfo a { rate = 1/4 }\nmod a -> pad.pan depth = 0.1\nmod a -> pad.pan depth = 0.2 add\n")
	found := false
	for _, c := range codes {
		found = found || strings.HasPrefix(c, "CICADA-DUPLICATE: duplicate route")
	}
	if !found {
		t.Fatalf("codes = %v", codes)
	}
}

func TestContourPeriodRefusesHz(t *testing.T) {
	codes := modulatorCodes(t, "contour c { period = 0.5Hz @0 0 @1 1 }\n")
	if len(codes) == 0 || !strings.Contains(strings.Join(codes, "\n"), "not a rate in Hz") {
		t.Fatalf("codes = %v", codes)
	}
}
