package project

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
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

// A route on a parameter that a scene also sets: a seek back to the start
// sounds like a fresh start, because the route takes the authored value as its
// base again.
func TestSeekBackRestoresARoutedLiveParameter(t *testing.T) {
	p, ds := compileLive(t, "cicada 2\ntempo 120\ninstrument tone {\n  param cutoff = 300Hz\n  voice mono { out = lowpass(saw(pitch), cutoff) * env(gate, 400ms) * 0.4 }\n}\ntrack lead tone {}\npattern line { c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 }\nlfo wob { wave = sine rate = 1bars }\nmod wob -> lead.cutoff depth = 100Hz\nscene main { lead = line }\nscene open { lead = line lead.cutoff = 5000Hz }\nsong { main open }\n")
	if p == nil {
		t.Fatalf("compile: %+v", ds)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	firstBar := func(e *engine.Engine, seek bool) []float32 {
		var l, r [128]float32
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		if seek {
			for i := 0; i < 2*96000/128; i++ { // play into the open scene
				e.Render(l[:], r[:])
			}
			e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff})
		}
		var out []float32
		for i := 0; i < 96000/128; i++ {
			e.Render(l[:], r[:])
			out = append(out, l[:]...)
		}
		return out
	}
	fresh, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	seeked, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, b := firstBar(fresh, false), firstBar(seeked, true)
	worst := 0.0
	for i := range a {
		worst = max(worst, math.Abs(float64(a[i]-b[i])))
	}
	if worst > 1e-6 {
		t.Fatalf("after a seek back the first bar differs from a fresh start by %g", worst)
	}
}

// A live delay time is checked at every value it can reach, as a static
// setting is, instead of being clamped while it plays.
func TestLiveDelayTimeIsCheckedAtTheValuesItReaches(t *testing.T) {
	source := "cicada 2\ntempo 120\ninstrument echoey {\n  param time = 20ms\n  voice mono { out = delay(saw(pitch), time) * env(gate, 300ms) * 0.3 }\n}\ntrack lead echoey {}\npattern line { c3 . . . }\n"
	for _, test := range []struct {
		setting string
		refused bool
	}{{"lead.time = 40ms", false}, {"lead.time = 200ms", true}} {
		p, ds := compileLive(t, source+"scene main { lead = line "+test.setting+" }\nsong { main }\n")
		if p == nil {
			t.Fatalf("%s: compile: %+v", test.setting, ds)
		}
		_, err := CompileEngine(p, 48000, 128)
		if test.refused != (err != nil) || test.refused && !strings.Contains(err.Error(), "can move time to 200ms") {
			t.Fatalf("%s: got %v", test.setting, err)
		}
	}
}

// A multiply route scales its base by 1-depth to 1+depth: 60ms with depth 0.5
// reaches 90ms, past the 85.3ms a delay line holds at 48 kHz.
func TestMultiplyRouteReachIsChecked(t *testing.T) {
	source := "cicada 2\ntempo 120\ninstrument echoey {\n  param time = 60ms\n  voice mono { out = delay(saw(pitch), time) * env(gate, 300ms) * 0.3 }\n}\ntrack lead echoey {}\npattern line { c3 . . . }\nlfo wob { wave = sine rate = 1bars }\n"
	for _, test := range []struct {
		depth   string
		refused bool
	}{{"0.25", false}, {"0.5", true}} {
		p, ds := compileLive(t, source+"mod wob -> lead.time depth = "+test.depth+" multiply\nscene main { lead = line }\nsong { main }\n")
		if p == nil {
			t.Fatalf("depth %s: compile: %+v", test.depth, ds)
		}
		_, err := CompileEngine(p, 48000, 128)
		if test.refused != (err != nil) || test.refused && !strings.Contains(err.Error(), "can move time to 90ms") {
			t.Fatalf("depth %s: got %v", test.depth, err)
		}
	}
}

// MIDI controls cannot reach an authored parameter yet, and say so.
func TestMIDIControlOnAnAuthoredParameterIsRefused(t *testing.T) {
	_, ds := compileLive(t, liveInstrument+"midi { port keys\n  keys ch 1 cc 74 -> lead.cutoff\n}\nscene main { lead = line }\nsong { main }\n")
	if !strings.Contains(fmt.Sprint(ds), "cannot reach an authored instrument parameter") {
		t.Fatalf("want a MIDI refusal, got %+v", ds)
	}
}
