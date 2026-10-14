package project

import (
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
)

// rangeScore wraps device blocks in a minimal valid score. The device text
// starts on line 3.
func rangeScore(devices string) []byte {
	return []byte("cicada 1\ntempo 120\n" + devices + "\npattern beat drums steps=1 { bd: x }\nscene main { d = beat }\nsong { main }\n")
}

func scoreErrors(t *testing.T, source []byte) []notation.Diagnostic {
	t.Helper()
	score, diagnostics := notation.Parse(source)
	if !hasErrors(diagnostics) {
		_, more := FromScore(score)
		diagnostics = append(diagnostics, more...)
	}
	var errors []notation.Diagnostic
	for _, d := range diagnostics {
		if d.Severity == "error" {
			errors = append(errors, d)
		}
	}
	return errors
}

type wantRange struct {
	message      string
	line, column int
}

func checkRangeErrors(t *testing.T, source []byte, want ...wantRange) {
	t.Helper()
	got := scoreErrors(t, source)
	if len(got) != len(want) {
		t.Fatalf("want %d diagnostics, got %d: %+v", len(want), len(got), got)
	}
	for i, w := range want {
		d := got[i]
		if d.Message != w.message || d.Code != "CICADA-PARAM" || d.Position.Line != w.line || d.Position.Column != w.column {
			t.Errorf("diagnostic %d = %s %q at %d:%d\nwant CICADA-PARAM %q at %d:%d", i, d.Code, d.Message, d.Position.Line, d.Position.Column, w.message, w.line, w.column)
		}
	}
}

func TestDrumParameterRangeNamesParameterValueAndRange(t *testing.T) {
	checkRangeErrors(t, rangeScore("track d drums {\n  oh_decay = 50ms\n}"),
		wantRange{"drums oh_decay 50ms is below the minimum 150ms (range 150ms to 1.2s)", 4, 14})
}

func TestReverbParameterRange(t *testing.T) {
	checkRangeErrors(t, rangeScore("fx room reverb {\n  size = 0.2\n}\ntrack d drums {}"),
		wantRange{"reverb size 0.2 is below the minimum 0.5 (range 0.5 to 1.5)", 4, 10})
}

func TestDelayParameterRange(t *testing.T) {
	checkRangeErrors(t, rangeScore("fx echo delay {\n  time = 3/16\n  feedback = 1.4\n}\ntrack d drums {}"),
		wantRange{"delay feedback 1.4 is above the maximum 0.95 (range 0 to 0.95)", 5, 14})
}

func TestAcidParameterRangeReportsEveryError(t *testing.T) {
	source := []byte("cicada 1\ntempo 120\ntrack bass acid {\n  cutoff = 30000Hz\n  reso = 1.7\n}\npattern riff acid steps=1 { 1 }\nscene main { bass = riff }\nsong { main }\n")
	checkRangeErrors(t, source,
		wantRange{"acid cutoff 30000Hz is above the maximum 8000Hz (range 20Hz to 8000Hz)", 4, 12},
		wantRange{"acid reso 1.7 is above the maximum 1 (range 0 to 1)", 5, 10})
}

func TestEveryParameterErrorAcrossDevicesIsReportedOnce(t *testing.T) {
	devices := "fx echo delay {\n  feedback = 1.4\n  damp = 100Hz\n}\nfx room reverb {\n  size = 0.2\n}\ntrack d drums {}"
	got := scoreErrors(t, rangeScore(devices))
	if len(got) != 3 {
		t.Fatalf("want 3 diagnostics, got %+v", got)
	}
	for _, want := range []string{"delay feedback 1.4 is above", "delay damp 100Hz is below the minimum 1000Hz", "reverb size 0.2 is below"} {
		found := false
		for _, d := range got {
			found = found || strings.HasPrefix(d.Message, want)
		}
		if !found {
			t.Errorf("no diagnostic starts with %q: %+v", want, got)
		}
	}
}

func TestEnumerationErrorListsAllowedValues(t *testing.T) {
	source := []byte("cicada 1\ntempo 120\ntrack bass acid {\n  filter = moog\n}\npattern riff acid steps=1 { 1 }\nscene main { bass = riff }\nsong { main }\n")
	checkRangeErrors(t, source, wantRange{"acid filter moog is not one of diode, ladder", 4, 12})
}

func TestMasterChainAndTrackEQParameterRanges(t *testing.T) {
	devices := "fx limit limiter {\n  ceiling = 3dBTP\n}\nfx wide width {\n  amount = 3\n}\nfx cut eq {\n  frequency = 5Hz\n}\nmaster { insert = limit -> wide }\ntrack d drums { insert = cut }"
	got := scoreErrors(t, rangeScore(devices))
	for _, want := range []string{"limiter ceiling 3dBTP is above the maximum 0dBTP (range -12dBTP to 0dBTP)", "width amount 3 is above the maximum 2 (range 0 to 2)", "eq frequency 5Hz is below the minimum 10Hz"} {
		found := false
		for _, d := range got {
			found = found || strings.HasPrefix(d.Message, want)
		}
		if !found {
			t.Errorf("no diagnostic starts with %q: %+v", want, got)
		}
	}
}

func TestValidParametersAtTheLimitsPass(t *testing.T) {
	source := []byte("cicada 1\ntempo 120\ntrack bass acid {\n  cutoff = 8000Hz\n  reso = 0\n  decay = 0.1s\n}\npattern riff acid steps=1 { 1 }\nscene main { bass = riff }\nsong { main }\n")
	if got := scoreErrors(t, source); len(got) != 0 {
		t.Fatalf("limit values were rejected: %+v", got)
	}
}

func TestSceneSettingRangeUsesSharedMessage(t *testing.T) {
	source := []byte("cicada 1\nfx room reverb {}\ntrack bass acid {}\npattern riff acid steps=1 { 1 }\nscene main {\n  bass = riff\n  room.size = 0.2\n  bass.cutoff = 9000Hz\n}\nsong { main }\n")
	_, got := notation.Parse(source)
	if len(got) != 2 {
		t.Fatalf("want 2 diagnostics, got %+v", got)
	}
	want := []string{"room.size 0.2 is below the minimum 0.5 (range 0.5 to 1.5)", "bass.cutoff 9000Hz is above the maximum 8000Hz (range 20Hz to 8000Hz)"}
	for i := range want {
		if got[i].Message != want[i] || got[i].Position.Line != 7+i {
			t.Errorf("diagnostic %d = %q at line %d, want %q at line %d", i, got[i].Message, got[i].Position.Line, want[i], 7+i)
		}
	}
}

func TestAutomationRangeNamesThePointAndReportsEveryOne(t *testing.T) {
	source := []byte("cicada 1\ntrack bass acid {}\npattern a acid steps=1 { 1 }\nautomate bass.cutoff { @1.1.1 400Hz @2.1.1 90000Hz @3.1.1 5Hz }\nautomate bass.reso { @1.1.1 2 }\nscene main { bass=a }\nsong { main*4 }\n")
	score, diagnostics := notation.Parse(source)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	_, got := FromScore(score)
	want := []string{
		"bass.cutoff @2.1.1 90000Hz is above the maximum 8000Hz (range 20Hz to 8000Hz)",
		"bass.cutoff @3.1.1 5Hz is below the minimum 20Hz (range 20Hz to 8000Hz)",
		"bass.reso @1.1.1 2 is above the maximum 1 (range 0 to 1)",
	}
	if len(got) != len(want) {
		t.Fatalf("want %d diagnostics, got %+v", len(want), got)
	}
	for i := range want {
		if got[i].Message != want[i] || got[i].Position.Line != 4+i/2 && i < 2 {
			t.Errorf("diagnostic %d = %q line %d, want %q", i, got[i].Message, got[i].Position.Line, want[i])
		}
	}
}

func boundLiteral(d paramdefs.Descriptor, v float64) string {
	text := strconv.FormatFloat(v, 'f', -1, 64)
	switch strings.ToLower(d.Unit) {
	case "hz":
		return text + "Hz"
	case "ms":
		return text + "ms"
	case "db":
		return text + "dB"
	case "dbtp":
		return text + "dBTP"
	}
	return text
}

func TestParameterRangesDocIsCurrent(t *testing.T) {
	want, err := paramdefs.GenerateRangesDoc()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../docs/manual/parameter-ranges.md")
	if err != nil || string(got) != string(want) {
		t.Fatalf("docs/manual/parameter-ranges.md is stale; run go run ./cmd/paramgen (%v)", err)
	}
	index, err := os.ReadFile("../docs/manual/README.md")
	if err != nil || !strings.Contains(string(index), "(parameter-ranges.md)") {
		t.Fatalf("the manual index does not link parameter-ranges.md (%v)", err)
	}
}

// The hand-written catalog lists the acid and drum ranges. Each listed range
// must match the table the diagnostics use.
func TestBuiltInTrackParameterCatalogMatchesTable(t *testing.T) {
	data, err := os.ReadFile("../docs/spec/built-in-track-parameters.md")
	if err != nil {
		t.Fatal(err)
	}
	number := func(s string) float64 {
		v, err := strconv.ParseFloat(strings.ReplaceAll(s, "−", "-"), 64)
		if err != nil {
			t.Fatalf("catalog number %q: %v", s, err)
		}
		return v
	}
	same := func(a, b float64) bool { return math.Abs(a-b) <= 1e-3*math.Max(1, math.Abs(b)) }
	compare := func(voice, name, low, high string) {
		d, ok := paramdefs.TrackParam(voice, name)
		if !ok {
			t.Errorf("catalog lists %s %s, which the table lacks", voice, name)
			return
		}
		if !same(number(low), d.Min) || !same(number(high), d.Max) {
			t.Errorf("catalog %s %s range %s to %s; table %g to %g", voice, name, low, high, d.Min, d.Max)
		}
	}
	acidRow := regexp.MustCompile(`^\| <code>(\w+)</code> \| [^|]+ \| (−?[0-9.]+)–(−?[0-9.]+) \|`)
	laneRow := regexp.MustCompile(`^\| ((?:<code>\w+</code>(?:, )?)+) \| (.*) \| [^|]* \|$`)
	field := regexp.MustCompile(`<code>(\w+)</code> (−?[0-9.]+)–(−?[0-9.]+)`)
	laneName := regexp.MustCompile(`<code>(\w+)</code>`)
	section, rows := "", 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			section = line
		}
		switch section {
		case "## Acid voice":
			if m := acidRow.FindStringSubmatch(line); m != nil {
				compare("acid", m[1], m[2], m[3])
				rows++
			}
		case "## Drum voice parameters":
			if m := laneRow.FindStringSubmatch(line); m != nil && !strings.Contains(line, "Lane") {
				for _, lane := range laneName.FindAllStringSubmatch(m[1], -1) {
					for _, f := range field.FindAllStringSubmatch(m[2], -1) {
						compare("drums", lane[1]+"_"+f[1], f[2], f[3])
						rows++
					}
				}
			}
		}
	}
	if rows < 50 {
		t.Fatalf("only %d catalog ranges were compared; the catalog format changed", rows)
	}
}

// Exact bounds must pass at full float64 precision on every path: source
// settings, scene settings, automation points, and the typed and JSON project.
func TestTomTuneExactBoundsPassOnEveryPath(t *testing.T) {
	max := strconv.FormatFloat(1.4983070768766815, 'f', -1, 64)
	for _, value := range []string{max, "1.498306", "1.498307", "0.6674199270850172", "0.66742"} {
		t.Run(value, func(t *testing.T) {
			source := rangeScore("track d drums { lt_tune = " + value + " }\nautomate d.lt_tune { @1.1.1 " + value + " @1.2.1 1 }")
			source = []byte(strings.Replace(string(source), "scene main { d = beat }", "scene main { d = beat d.mt_tune = "+value+" }", 1))
			score, diagnostics := notation.Parse(source)
			if hasErrors(diagnostics) {
				t.Fatalf("parse: %+v", diagnostics)
			}
			p, diagnostics := FromScore(score)
			if p == nil {
				t.Fatalf("FromScore: %+v", diagnostics)
			}
			data, err := CanonicalJSON(p)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeJSON(data)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateProject(decoded); err != nil {
				t.Fatalf("JSON project: %v", err)
			}
		})
	}
}

func TestOutsideTomBoundNeverContradictsItself(t *testing.T) {
	source := rangeScore("track d drums { lt_tune = 1.4983071 }")
	got := scoreErrors(t, source)
	if len(got) != 1 || !strings.Contains(got[0].Message, "lt_tune 1.4983071 is above the maximum 1.498") {
		t.Fatalf("got %+v", got)
	}
	d, _ := paramdefs.TrackParam("drums", "lt_tune")
	next := math.Nextafter(d.Max, 2)
	if m := paramdefs.RangeMessageValue(d, "x", next); !strings.Contains(m, strconv.FormatFloat(next, 'f', -1, 64)+" is above the maximum 1.498") {
		t.Fatalf("%s", m)
	}
}

func TestInvalidAcidGateIsReportedOnce(t *testing.T) {
	source := []byte("cicada 1\ntempo 120\ntrack bass acid {\n  gate = 150%\n}\npattern riff acid steps=1 { 1 }\nscene main { bass = riff }\nsong { main }\n")
	got := scoreErrors(t, source)
	if len(got) != 1 || got[0].Position.Line != 4 {
		t.Fatalf("want one diagnostic on line 4, got %+v", got)
	}
}
