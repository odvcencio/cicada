package project

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

func TestSharedDelayValidationChecksReleasedAndCombinedBends(t *testing.T) {
	program := graph.Program{Len: 4, Output: 3, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Constant, Value: 1}, {Op: graph.Pitch}, {Op: graph.Period, A: 0, B: 1}, {Op: graph.Delay, A: 0, B: 2}}}
	p := seq.Pattern{Len: 1, Performance: &seq.Performance{LengthTicks: 3840, Groups: []seq.ControlGroup{{Name: "touch", Points: []seq.GroupPoint{{Value: expression.Controls{Timbre: .5}}, {Tick: 960, Value: expression.Controls{PitchCents: -9600, Timbre: .5}}}}}, Notes: []seq.PerformedNote{{Note: 60, Velocity: 100, EndTick: 120, Group: 1}}}}
	if err := ValidateGraphDelayPattern(program, 48000, p); err == nil {
		t.Fatal("unsafe master bend after gate release bypassed delay bounds")
	}
	p.Performance.Groups[0].Points[1].Value.PitchCents = -2400
	p.Performance.Notes[0].Expressions = []seq.PerformedExpression{{Tick: 120, Params: seq.Expression{Set: true, PitchCents: -7200, Timbre: .5}}}
	if err := ValidateGraphDelayPattern(program, 48000, p); err == nil {
		t.Fatal("frozen release member and shared bend bypassed combined bounds")
	}
	p.Performance.Notes[0].Expressions = nil
	if err := ValidateGraphDelayPattern(program, 48000, p); err != nil {
		t.Fatal("safe shared release was refused", err)
	}
}

const groupScore = `cicada 2
tempo 120
instrument glass { voice poly { out=sine(pitch)*env(gate,300ms)*(0.1+pressure*0.05+timbre*0.05) } }
track keys glass {}
pattern phrase performance length=3840ticks {
 gesture-group touch {
  gesture at=0ticks bend=0ct pressure=0 timbre=0.5;
  gesture at=960ticks bend=200ct pressure=0.8 timbre=0.9;
 }
 note 60 at=17ticks length=703ticks velocity=103 group=touch {
  gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7;
 }
 note 64 at=19ticks length=1301ticks velocity=87 group=touch;
}
scene main { keys=phrase }
song { main }`

func TestSharedGestureSourceSemanticAndKernelRoundTrip(t *testing.T) {
	score, ds := notation.Parse([]byte(groupScore))
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || len(ds) > 0 {
		t.Fatal(ds)
	}
	if len(p.Patterns[0].Performance.Groups) != 1 || p.Patterns[0].Performance.Notes[0].Group != 1 {
		t.Fatal("group binding lost in semantic model")
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var copy Project
	if err := json.Unmarshal(data, &copy); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Patterns[0].Performance.Kernel(), copy.Patterns[0].Performance.Kernel()) {
		t.Fatal("semantic JSON changed shared gestures")
	}
	text := performanceSource(p.Patterns[0], 0, false, p.Seed)
	start, end := strings.Index(groupScore, "pattern phrase"), strings.Index(groupScore, "scene main")
	parsed, ds := notation.Parse([]byte(groupScore[:start] + text + groupScore[end:]))
	if len(ds) > 0 {
		t.Fatal(ds, text)
	}
	if !reflect.DeepEqual(parsed.Patterns[0].Performance, p.Patterns[0].Performance.Kernel()) {
		t.Fatal("source export changed shared lane", text)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Track[0].Kind != engine.VoiceGraphPoly {
		t.Fatal("recording a shared gesture reduced the live instrument's eight voices")
	}
	if _, err := engine.New(cfg); err != nil {
		t.Fatal(err)
	}
}
