package drum

import (
	"fmt"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/graph"
)

func TestDrumDecayRetainsAudibleTail(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		for _, tc := range []struct {
			lane      Lane
			decay     float64
			snappy    float64
			observeAt float64
		}{
			{BD, 1.5, 0, 4.05},
			{OH, 1.2, 0, 4.05},
			{LT, 1, 0, 4.05},
			{SD, .04, .4, .55},
		} {
			t.Run(fmt.Sprintf("%s/%d", Names[tc.lane], rate), func(t *testing.T) {
				kit, err := New(rate, 4242)
				if err != nil {
					t.Fatal(err)
				}
				p := kit.Params(tc.lane)
				p.Decay = tc.decay
				if tc.snappy != 0 {
					p.Snappy, p.Mix = tc.snappy, 1
				}
				if err := kit.SetParams(tc.lane, p); err != nil {
					t.Fatal(err)
				}
				kit.Hit(tc.lane, 127, false)
				for range int(tc.observeAt * float64(rate)) {
					kit.NextStereo()
				}
				var energy float64
				for range rate / 20 {
					l, r := kit.NextStereo()
					energy += float64(l*l + r*r)
				}
				rms := math.Sqrt(energy / float64(rate/20) / 2)
				if rms < 1e-4 || !kit.lanes[tc.lane].current.active || kit.Fault() {
					t.Fatalf("audible decay truncated: RMS %.8f, active=%v, fault=%v", rms, kit.lanes[tc.lane].current.active, kit.Fault())
				}
				// Retirement stays bounded once every audible component has decayed.
				for range int(13 * max(p.Decay, p.Snappy) * float64(rate)) {
					kit.NextStereo()
				}
				if kit.lanes[tc.lane].current.active {
					t.Fatal("decayed voice did not retire")
				}
			})
		}
	}
}

func qualityDrumProgram() graph.Program {
	return graph.Program{Len: 13, Output: 12, Nodes: [graph.MaxNodes]graph.Node{
		{Op: graph.Constant, Value: 710},
		{Op: graph.Constant, Value: .31},
		{Op: graph.Pulse, A: 0, B: 1},
		{Op: graph.Gate},
		{Op: graph.Constant, Value: .5},
		{Op: graph.Constant, Value: 120},
		{Op: graph.Constant, Value: .25},
		{Op: graph.Constant, Value: 20},
		{Op: graph.ADSR, A: 3, B: 4, C: 5, D: 6, E: 7},
		{Op: graph.Multiply, A: 2, B: 8},
		{Op: graph.Velocity},
		{Op: graph.Multiply, A: 9, B: 10},
		{Op: graph.Tanh, A: 11},
	}}
}

func TestAuthoredQualityDrumRetriggerKeepsIndependentState(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			program := qualityDrumProgram()
			kit, _ := New(rate, 4242)
			if err := kit.SetGraph(BD, program); err != nil {
				t.Fatal(err)
			}
			current, _ := graph.NewVoice(program, rate)
			previous, _ := graph.NewVoice(program, rate)
			kit.Hit(BD, 98, false)
			current.NoteOn(MIDINotes[BD], 98, false)
			previous.NoteOn(MIDINotes[BD], 98, false)
			for range 173 {
				kit.NextStereo()
				current.Next()
				previous.Next()
			}
			kit.Hit(BD, 117, false)
			current.NoteOn(MIDINotes[BD], 117, false)
			v := &kit.lanes[BD]
			fade := max(1, int(float64(rate)/1000))
			for sample := range 512 {
				want := float64(current.Next())
				if sample < fade {
					want += float64(previous.Next()) * float64(fade-sample) / max(1, float64(rate)/1000)
				}
				left, right := kit.NextStereo()
				if left != float32(want*v.level*v.panL) || right != float32(want*v.level*v.panR) {
					t.Fatalf("retrigger altered independent quality voice at sample %d: left=%g right=%g want=%g", sample, left, right, want*v.level*v.panL)
				}
			}
			if allocs := testing.AllocsPerRun(100, func() {
				kit.Hit(BD, 98, false)
				for range 64 {
					kit.NextStereo()
				}
			}); allocs != 0 {
				t.Fatalf("quality drum retrigger allocated %g objects", allocs)
			}
		})
	}
}
