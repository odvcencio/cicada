package engine

import (
	"fmt"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
)

func TestPerformedSharedControlsReachReleasedVoiceAndStayImmutable(t *testing.T) {
	for _, voices := range []int{4, 8} {
		t.Run(fmt.Sprint(voices), func(t *testing.T) { testSharedRelease(t, voices) })
	}
}

func testSharedRelease(t *testing.T, voices int) {
	p := &seq.Performance{LengthTicks: 960, Groups: []seq.ControlGroup{{Name: "touch", Points: []seq.GroupPoint{{Value: expression.Controls{Pressure: .2, Timbre: .5}}, {Tick: 130, Value: expression.Controls{Pressure: .8, Timbre: .5}}}}}, Notes: []seq.PerformedNote{{Note: 60, Velocity: 100, EndTick: 120, Group: 1}}}
	cfg := liveIdentityConfig(voices)
	cfg.Track[0].Graph = graph.Program{Len: 7, Output: 6, Nodes: [graph.MaxNodes]graph.Node{
		{Op: graph.Pressure}, {Op: graph.Gate}, {Op: graph.Constant, Value: 1}, {Op: graph.Constant}, {Op: graph.Constant, Value: 100},
		{Op: graph.ADSR, A: 1, B: 3, C: 3, D: 2, E: 4}, {Op: graph.Multiply, A: 0, B: 5},
	}}
	e, err := New(performanceConfig(cfg, p))
	if err != nil {
		t.Fatal(err)
	}
	p.Groups[0].Points[1].Value.Pressure = 0 // caller storage must not mutate the prepared lane
	e.meterRate = 0
	e.PushBatch([]cmd.Command{{Op: cmd.OpSelectPattern}, {Op: cmd.OpPlay, Track: 255}})
	var left, right [128]float32
	render := func(n int) {
		t.Helper()
		e.Render(left[:n], right[:n])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatal(m)
			}
		}
	}
	for e.transport.Tick() < 125 {
		render(1)
	}
	if e.patterns[0].performed.gates[60].id != 0 {
		t.Fatal("note gate did not release")
	}
	next := func() float32 {
		if pool := e.voices[0].poly; pool != nil {
			return pool.Next()
		}
		v, _ := e.voices[0].legacyPoly.NextStereo()
		return v
	}
	before := next()
	for e.transport.Tick() < 132 {
		render(1)
	}
	after := next()
	if before <= 0 || after < before*2 {
		t.Fatal("shared pressure did not shape the releasing voice", before, after)
	}
	if allocs := testing.AllocsPerRun(100, func() { render(128) }); allocs != 0 {
		t.Fatal("shared performance callback allocated", allocs)
	}
}
