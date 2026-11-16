package render

import (
	"bytes"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func swingSong(meter string, cells int) string {
	notes := strings.TrimSpace(strings.Repeat("c3 ", cells))
	return "cicada 2\ntempo 120\nmeter " + meter + "\ntrack lead acid {}\npattern lilt { step = 1/8 swing = 60% " + notes + " }\nscene a { lead = lilt }\nsong { a*4 }\n"
}

// Swing delays odd cells counted from the song's start (spec 2.3, rule 7). A
// 7/8 or 5/8 bar holds an odd number of eighths, so the swung cells would flip
// every bar (bar 2's downbeat came 96 ticks late in 7/8, #358 review): check
// refuses it and neither render path plays it. In 6/8 the six eighths pair up,
// and both render paths agree.
func TestSwingNeedsAnEvenNumberOfCellsPerBar(t *testing.T) {
	for _, c := range []struct {
		meter string
		cells int
	}{{"7/8", 7}, {"5/8", 5}} {
		score, ds := notation.Parse([]byte(swingSong(c.meter, c.cells)))
		if score == nil || hasRenderErrors(ds) {
			t.Fatal(ds)
		}
		_, ds = project.FromScore(score)
		refused := false
		for _, d := range ds {
			refused = refused || d.Severity == "error" && d.Code == "CICADA-METER" && strings.Contains(d.Message, "an odd number")
		}
		if !refused {
			t.Fatalf("%s swing was not refused: %v", c.meter, ds)
		}
		for _, scheduled := range []bool{false, true} {
			var out bytes.Buffer
			if _, _, err := pathsRender(score, scheduled, "", 2, 44100, &out); err == nil {
				t.Fatalf("%s swing rendered (scheduled %v)", c.meter, scheduled)
			}
		}
	}
	score, ds := notation.Parse([]byte(swingSong("6/8", 6)))
	if score == nil || hasRenderErrors(ds) {
		t.Fatal(ds)
	}
	if _, ds := project.FromScore(score); hasRenderErrors(ds) {
		t.Fatalf("6/8 swing refused: %v", ds)
	}
	var direct, scheduled bytes.Buffer
	if _, _, err := pathsRender(score, false, "", 2, 44100, &direct); err != nil {
		t.Fatal(err)
	}
	if _, _, err := pathsRender(score, true, "", 2, 44100, &scheduled); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(direct.Bytes(), scheduled.Bytes()) {
		t.Fatal("6/8 swing differs between the render paths")
	}
}
