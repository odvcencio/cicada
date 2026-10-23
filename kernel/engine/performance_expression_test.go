package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
)

func TestPerformedExpressionOwnsControllerHistoryAndRetriggers(t *testing.T) {
	p := &seq.Performance{LengthTicks: 1920, Notes: []seq.PerformedNote{
		{Tick: 0, EndTick: 480, Note: 60, Velocity: 90, Expressions: []seq.PerformedExpression{
			{Tick: 0, Params: seq.Expression{Set: true, Pressure: .2, Timbre: .5}},
			{Tick: 120, Params: seq.Expression{Set: true, Pressure: .7, Timbre: .5}},
			{Tick: 480, Params: seq.Expression{Set: true, Pressure: .9, Timbre: .5}},
		}},
		{Tick: 480, EndTick: 960, Note: 60, Velocity: 90, Expressions: []seq.PerformedExpression{
			{Tick: 0, Params: seq.Expression{Set: true, Pressure: .3, Timbre: .5}},
		}},
	}}
	e, err := New(performanceConfig(expressionConfig(), p))
	if err != nil {
		t.Fatal(err)
	}
	p.Notes[0].Expressions[1].Params.Pressure = .99
	if e.patterns[0].slots[0].Performance.Notes[0].Expressions[1].Params.Pressure != .7 {
		t.Fatal("prepared gesture aliases caller data")
	}
	e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0})
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var left, right [128]float32
	render := func(n int) {
		e.Render(left[:n], right[:n])
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatal("gesture playback fault", message)
			}
		}
	}
	clock, _ := seq.NewClock(48000, 120000)
	var frame int64
	for _, check := range []struct {
		tick     int64
		pressure float32
	}{{0, .2}, {120, .7}, {480, .3}} {
		end := clock.SampleAtTick(check.tick) + 1
		for frame < end {
			n := min(128, int(end-frame))
			render(n)
			frame += int64(n)
		}
		if got := e.voices[0].graph.Next(); got != check.pressure {
			t.Fatalf("tick=%d: pressure=%g want %g", check.tick, got, check.pressure)
		}
	}
	e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg1: 240})
	render(1)
	if got := e.voices[0].graph.Next(); got != .7 {
		t.Fatal("seek lost the last held controller value", got)
	}
}

func TestPerformedPolyExpressionIsIndependentAndAllocationFree(t *testing.T) {
	p := &seq.Performance{LengthTicks: 960, Notes: []seq.PerformedNote{
		{Tick: 0, EndTick: 600, Note: 60, Velocity: 90, Expressions: []seq.PerformedExpression{{Params: seq.Expression{Set: true, Pressure: .2, Timbre: .5}}, {Tick: 120, Params: seq.Expression{Set: true, Pressure: .8, Timbre: .5}}}},
		{Tick: 0, EndTick: 480, Note: 64, Velocity: 90, Expressions: []seq.PerformedExpression{{Params: seq.Expression{Set: true, Pressure: .3, Timbre: .5}}, {Tick: 240, Params: seq.Expression{Set: true, Pressure: .4, Timbre: .5}}}},
	}}
	cfg := performanceConfig(polyConfig(), p)
	cfg.Track[0].Graph = graph.Program{Len: 1, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Pressure}}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0})
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var left, right [128]float32
	clock, _ := seq.NewClock(48000, 120000)
	frame := int64(0)
	for _, check := range []struct {
		tick int64
		want float32
	}{{0, .5}, {120, 1.1}, {240, 1.2}} {
		end := clock.SampleAtTick(check.tick) + 1
		for frame < end {
			n := min(128, int(end-frame))
			e.Render(left[:n], right[:n])
			frame += int64(n)
			var m cmd.Message
			for e.Poll(&m) {
				if m.Kind == cmd.Fault {
					t.Fatal(m)
				}
			}
		}
		if got := e.voices[0].poly.Next(); got != check.want {
			t.Fatal("controller crossed note ownership", check.tick, got, check.want)
		}
	}
	if allocations := testing.AllocsPerRun(1000, func() {
		e.Render(left[:], right[:])
		var m cmd.Message
		for e.Poll(&m) {
		}
	}); allocations != 0 {
		t.Fatal("gesture callback allocated", allocations)
	}
	e.Push(cmd.Command{Op: cmd.OpStop, Track: 255})
	e.Render(left[:], right[:])
	for _, gate := range e.patterns[0].performed.gates {
		if gate.id != 0 {
			t.Fatal("stop kept gesture ownership")
		}
	}
}

func TestPerformedControllersDoNotAddressLiveMonoOverlay(t *testing.T) {
	p := &seq.Performance{LengthTicks: 960, Notes: []seq.PerformedNote{{Tick: 0, EndTick: 480, Note: 60, Velocity: 90, Expressions: []seq.PerformedExpression{{Params: seq.Expression{Set: true, Pressure: .2, Timbre: .5}}, {Tick: 120, Params: seq.Expression{Set: true, Pressure: .9, Timbre: .5}}}}}}
	e, err := New(performanceConfig(expressionConfig(), p))
	if err != nil {
		t.Fatal(err)
	}
	e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0})
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var left, right [128]float32
	e.Render(left[:], right[:])
	e.PushBatch([]cmd.Command{{Op: cmd.OpNoteOn, Track: 0, Index: 7, Arg0: 69 | 127<<8}, expressionCommand(7, .6)})
	for frame := 128; frame < 3100; frame += 128 {
		e.Render(left[:], right[:])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatal(m)
			}
		}
	}
	if got := e.voices[0].graph.Next(); got != .6 {
		t.Fatal("score gesture changed live overlay", got)
	}
}

func TestPerformedControllerBurstRefusedBeforePlayback(t *testing.T) {
	p := &seq.Performance{LengthTicks: 3840}
	for pitch := uint8(60); pitch < 63; pitch++ {
		note := seq.PerformedNote{Tick: 0, EndTick: 1000, Note: pitch, Velocity: 100}
		for tick := int64(0); tick < 200; tick++ {
			note.Expressions = append(note.Expressions, seq.PerformedExpression{Tick: tick, Params: seq.Expression{Set: true, Pressure: .5, Timbre: .5}})
		}
		p.Notes = append(p.Notes, note)
	}
	if player, err := New(performanceConfig(expressionConfig(), p)); err == nil || player != nil {
		t.Fatal("controller burst admitted into the callback")
	}
}
