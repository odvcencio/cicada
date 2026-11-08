package project

import (
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func compileLive(t *testing.T, source string) (*Project, []notation.Diagnostic) {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("parse: %+v", ds)
	}
	return FromScore(score)
}

const liveInstrument = "cicada 2\ntempo 120\ninstrument tone {\n  param cutoff = 300Hz\n  param attack = 5ms\n  voice mono { out = lowpass(saw(pitch), cutoff) * env(gate, attack) }\n}\ntrack lead tone {}\npattern line { c3 . . . }\n"

// A scene setting on an authored parameter compiles to a live binding, and the
// parameter's unit and range from its declaration are checked.
func TestSceneSettingOnAnAuthoredParameter(t *testing.T) {
	p, ds := compileLive(t, liveInstrument+"scene main { lead = line lead.cutoff = 2000Hz lead.attack = 40ms }\nsong { main }\n")
	if p == nil {
		t.Fatalf("compile: %+v", ds)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.LiveParams) != 2 {
		t.Fatalf("bindings %+v, want cutoff and attack", cfg.LiveParams)
	}
	for _, bad := range []struct{ setting, want string }{
		{"lead.cutoff = 30000Hz", "CICADA-UNIT"},
		{"lead.cutoff = 40ms", "CICADA-UNIT"},
	} {
		_, ds := compileLive(t, liveInstrument+"scene main { lead = line "+bad.setting+" }\nsong { main }\n")
		if !strings.Contains(fmt.Sprint(ds), bad.want) {
			t.Errorf("%s: want %s, got %+v", bad.setting, bad.want, ds)
		}
	}
}

// Only the first eight declared parameters can be live; the ninth is refused
// with the limit named.
func TestOnlyTheFirstEightAuthoredParametersAreLive(t *testing.T) {
	var params, uses strings.Builder
	for i := 1; i <= 9; i++ {
		fmt.Fprintf(&params, "  param p%d = %d\n", i, i)
		fmt.Fprintf(&uses, " + p%d", i)
	}
	source := "cicada 2\ntempo 120\ninstrument tone {\n" + params.String() + "  voice mono { out = sine(pitch) * env(gate, 100ms) * (0" + uses.String() + ") * 0.01 }\n}\ntrack lead tone {}\npattern line { c3 . . . }\n"
	if p, ds := compileLive(t, source+"scene main { lead = line lead.p8 = 2 }\nsong { main }\n"); p == nil {
		t.Fatalf("the eighth parameter was refused: %+v", ds)
	}
	_, ds := compileLive(t, source+"scene main { lead = line lead.p9 = 2 }\nsong { main }\n")
	if !strings.Contains(fmt.Sprint(ds), "only the first 8 parameters") {
		t.Fatalf("want the eight-parameter limit, got %+v", ds)
	}
}
