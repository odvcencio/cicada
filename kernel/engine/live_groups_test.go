package engine

import (
	"fmt"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
)

func liveSharedCommand(id uint16, pressure float32) cmd.Command {
	return cmd.Command{Op: cmd.OpSetLiveGroup, Index: id, Arg1: math.Float32bits(pressure), Pad: math.Float32bits(.5)}
}

func TestLiveSharedControlsRetainReleasedVoicesAndRejectStaleBindings(t *testing.T) {
	for _, voices := range []int{1, 4, 8} {
		t.Run(fmt.Sprint(voices), func(t *testing.T) {
			cfg := expressionConfig()
			if voices != 1 {
				cfg = liveIdentityConfig(voices)
			}
			cfg.Track[0].Graph = graph.Program{Len: 7, Output: 6, Nodes: [graph.MaxNodes]graph.Node{
				{Op: graph.Pressure}, {Op: graph.Gate}, {Op: graph.Constant, Value: 1}, {Op: graph.Constant}, {Op: graph.Constant, Value: 1000},
				{Op: graph.ADSR, A: 1, B: 3, C: 3, D: 2, E: 4}, {Op: graph.Multiply, A: 0, B: 5},
			}}
			e, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			e.meterRate = 0
			on := cmd.Command{Op: cmd.OpNoteOn, Index: 7, Arg0: 60 | 100<<8}
			if voices != 1 {
				on.Arg1 = cmd.LiveIdentity
			}
			push := func(commands ...cmd.Command) {
				t.Helper()
				if !e.PushBatch(commands) {
					t.Fatal("commands rejected")
				}
			}
			var left, right [128]float32
			render := func() {
				e.Render(left[:], right[:])
				var m cmd.Message
				for e.Poll(&m) {
					if m.Kind == cmd.Fault {
						t.Fatal(m)
					}
				}
			}
			// Deliberately reverse arrival order: binding and controls follow onset.
			push(liveSharedCommand(1, .2), cmd.Command{Op: cmd.OpBindNoteGroup, Index: 7, Arg0: 1}, on, expressionCommand(7, .3))
			render()
			if !e.LiveGroupActive(0, 1) {
				t.Fatal("group did not bind")
			}
			off := cmd.Command{Op: cmd.OpNoteOff, Index: 7, Arg1: on.Arg1}
			push(off)
			render()
			before := left[127]
			push(liveSharedCommand(1, .8), expressionCommand(7, 1)) // member movement is frozen after release
			render()
			if before <= 0 || left[127] < before*2 || left[127] > before*3 {
				t.Fatal("shared tail movement or frozen member controls failed", before, left[127])
			}
			// Replacing/stealing every slot must discard its previous binding.
			for id := uint16(20); id < 28; id++ {
				on.Index = id
				push(on)
				render()
			}
			push(cmd.Command{Op: cmd.OpBindNoteGroup, Index: 7, Arg0: 2}, liveSharedCommand(2, 1))
			render()
			if e.LiveGroupActive(0, 2) || left[127] != 0 {
				t.Fatal("stale binding addressed a replacement voice", left[127])
			}
			if allocs := testing.AllocsPerRun(100, func() {
				push(liveSharedCommand(1, .7))
				render()
			}); allocs != 0 {
				t.Fatal("live shared callback allocated", allocs)
			}
		})
	}
}

func TestLiveSharedAcidBendShapesReleasedAudio(t *testing.T) {
	render := func(cents float32) []float32 {
		cfg := expressionConfig()
		cfg.Track[0].Kind = VoiceAcid
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.meterRate = 0
		if !e.PushBatch([]cmd.Command{{Op: cmd.OpNoteOn, Index: 7, Arg0: 60 | 100<<8}, {Op: cmd.OpBindNoteGroup, Index: 7, Arg0: 1}, liveSharedCommand(1, 0)}) {
			t.Fatal("acid group commands rejected")
		}
		result := make([]float32, 128*4)
		var right [128]float32
		for block := range 4 {
			if block == 1 {
				e.Push(cmd.Command{Op: cmd.OpNoteOff, Index: 7})
			}
			if block == 2 {
				c := liveSharedCommand(1, 0)
				c.Arg0 = math.Float32bits(cents)
				e.Push(c)
			}
			e.Render(result[block*128:(block+1)*128], right[:])
		}
		return result
	}
	a, b := render(0), render(600)
	first, peak := -1, float64(0)
	for i := range a {
		delta := math.Abs(float64(a[i] - b[i]))
		if delta > 0 && first < 0 {
			first = i
		}
		peak = max(peak, delta)
	}
	if first < 256 || first >= 384 || peak < 1e-5 {
		t.Fatal("released acid voice lost its shared bend", first, peak)
	}
}

func TestLiveSharedMatchesRecordedPerformance(t *testing.T) {
	for _, voices := range []int{1, 4, 8} {
		t.Run(fmt.Sprint(voices), func(t *testing.T) {
			cfg := expressionConfig()
			if voices != 1 {
				cfg = liveIdentityConfig(voices)
			}
			cfg.Track[0].Graph = graph.Program{Len: 10, Output: 9, Nodes: [graph.MaxNodes]graph.Node{
				{Op: graph.Pressure}, {Op: graph.Gate}, {Op: graph.Constant, Value: 1}, {Op: graph.Constant}, {Op: graph.Constant, Value: 1000},
				{Op: graph.ADSR, A: 1, B: 3, C: 3, D: 2, E: 4}, {Op: graph.Multiply, A: 0, B: 5},
				{Op: graph.Pitch}, {Op: graph.Sine, A: 7}, {Op: graph.Multiply, A: 6, B: 8},
			}}
			member := seq.Expression{Set: true, Pressure: .3, Timbre: .5}
			bent := member
			bent.PitchCents = 100
			shared := expression.Controls{Pressure: .2, Timbre: .5}
			late := expression.Controls{PitchCents: -200, Pressure: .8, Timbre: .75}
			performance := &seq.Performance{LengthTicks: 960,
				Groups: []seq.ControlGroup{{Name: "master", Points: []seq.GroupPoint{{Value: shared}, {Tick: 130, Value: late}}}},
				Notes:  []seq.PerformedNote{{Note: 60, Velocity: 100, EndTick: 120, Group: 1, Expressions: []seq.PerformedExpression{{Params: member}, {Tick: 60, Params: bent}}}},
			}
			recorded, err := New(performanceConfig(cfg, performance))
			if err != nil {
				t.Fatal(err)
			}
			live, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			recorded.meterRate, live.meterRate = 0, 0
			recorded.PushBatch([]cmd.Command{{Op: cmd.OpPlay, Track: 255}, {Op: cmd.OpSelectPattern}})
			identityMode := uint32(0)
			if voices != 1 {
				identityMode = cmd.LiveIdentity
			}
			controls := func(op cmd.Op, id uint16, tick int64, pitch, pressure, timbre float32) cmd.Command {
				return cmd.Command{Op: op, Index: id, Tick: tick, Arg0: math.Float32bits(pitch), Arg1: math.Float32bits(pressure), Pad: math.Float32bits(timbre)}
			}
			if !live.PushBatch([]cmd.Command{
				{Op: cmd.OpPlay, Track: 255}, {Op: cmd.OpNoteOn, Index: 7, Arg0: 60 | 100<<8, Arg1: identityMode},
				{Op: cmd.OpBindNoteGroup, Index: 7, Arg0: 1},
				controls(cmd.OpNoteExpression, 7, 0, 0, .3, .5), controls(cmd.OpSetLiveGroup, 1, 0, 0, .2, .5),
				controls(cmd.OpNoteExpression, 7, 60, 100, .3, .5), {Op: cmd.OpNoteOff, Index: 7, Arg1: identityMode, Tick: 120},
				controls(cmd.OpSetLiveGroup, 1, 130, -200, .8, .75),
				controls(cmd.OpNoteExpression, 7, 150, 9600, 1, 1), // released member controls stay frozen
			}) {
				t.Fatal("live performance rejected")
			}
			var al, ar, bl, br [128]float32
			peak := float64(0)
			for frame, block := 0, 0; frame < 8192; block++ {
				n := min([]int{1, 127, 128}[block%3], 8192-frame)
				recorded.Render(al[:n], ar[:n])
				live.Render(bl[:n], br[:n])
				for i := range n {
					peak = max(peak, math.Abs(float64(al[i])))
					if math.Abs(float64(al[i]-bl[i])) > 1e-6 || math.Abs(float64(ar[i]-br[i])) > 1e-6 {
						t.Fatalf("live/replay differ at frame %d: %g/%g, %g/%g", frame+i, bl[i], al[i], br[i], ar[i])
					}
				}
				frame += n
			}
			if peak == 0 {
				t.Fatal("comparison was silent")
			}
		})
	}
}
