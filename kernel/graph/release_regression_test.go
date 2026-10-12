package graph

import (
	"fmt"
	"math"
	"testing"
)

func authoredReleaseProgram() Program {
	return Program{Len: 12, Output: 11, Nodes: [MaxNodes]Node{
		{Op: Pitch}, {Op: Gate}, {Op: Constant, Value: 10},
		{Op: Constant, Value: 80}, {Op: Constant, Value: .7},
		{Op: Constant, Value: 850}, {Op: ADSR, A: 1, B: 2, C: 3, D: 4, E: 5},
		{Op: Constant, Value: .3}, {Op: Pulse, A: 0, B: 7},
		{Op: Constant, Value: 2500}, {Op: SVF, A: 8, B: 9, C: 7},
		{Op: Multiply, A: 6, B: 10},
	}}
}

func TestCohortPoolHonorsAuthoredADSRRelease(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		for _, release := range []string{"handle", "cohort", "all"} {
			t.Run(fmt.Sprintf("%d/%s", rate, release), func(t *testing.T) {
				program := authoredReleaseProgram()
				pool, err := NewPool(program, rate)
				if err != nil {
					t.Fatal(err)
				}
				voice, err := NewVoice(program, rate)
				if err != nil {
					t.Fatal(err)
				}
				handles, err := pool.NoteOn([4]uint8{60}, 1, 100, false, Cohort{1, 1})
				if err != nil {
					t.Fatal(err)
				}
				voice.NoteOn(60, 100, false)
				for range rate / 5 {
					pool.Next()
					voice.Next()
				}
				switch release {
				case "handle":
					pool.NoteOff(handles[0])
				case "cohort":
					pool.Release(Cohort{1, 1})
				case "all":
					pool.ReleaseAll()
				}
				voice.NoteOff()
				var tailEnergy float64
				for frame := range rate {
					got, want := pool.Next(), voice.Next()
					if math.Abs(float64(got-want)) > 1e-7 {
						t.Fatalf("authored release changed at frame %d: pool=%g voice=%g", frame, got, want)
					}
					if frame > rate/20 && frame < rate/2 {
						tailEnergy += float64(got) * float64(got)
					}
				}
				if tailEnergy < 1 {
					t.Fatalf("850ms chord release lost its body: tail energy %g", tailEnergy)
				}
				if pool.ActiveVoices() != 0 {
					t.Fatal("finished ADSR release did not retire")
				}
			})
		}
	}
}

func TestAuthoredADSRChordStealingBoundedAndAllocationFree(t *testing.T) {
	pool, err := NewPool(authoredReleaseProgram(), 48000)
	if err != nil {
		t.Fatal(err)
	}
	id := int64(0)
	if got := testing.AllocsPerRun(20, func() {
		id++
		pool.NoteOn([4]uint8{60, 64, 67, 71}, 4, 100, false, Cohort{id, 1})
		for range 2048 {
			pool.Next()
		}
		pool.ReleaseAll()
		for range 2048 {
			pool.Next()
		}
	}); got != 0 {
		t.Fatalf("ADSR chord rendering allocated %g", got)
	}
	if pool.ActiveVoices() > 4 {
		t.Fatal("steals exceeded the four-voice limit")
	}
	pool.ReleaseAll()
	for range 48000 {
		pool.Next()
	}
	if pool.ActiveVoices() != 0 {
		t.Fatal("repeated chord releases left active voices")
	}
}

func TestQualityStateCopyIndependentAndAllocationFree(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		program := authoredReleaseProgram()
		a, err := NewVoice(program, rate)
		if err != nil {
			t.Fatal(err)
		}
		b, err := NewVoice(program, rate)
		if err != nil {
			t.Fatal(err)
		}
		c, err := NewVoice(program, rate)
		if err != nil {
			t.Fatal(err)
		}
		a.NoteOn(60, 100, false)
		for range 997 {
			a.Next()
		}
		b.CopyStateFrom(a)
		for frame := range 512 {
			if a.Next() != b.Next() {
				t.Fatalf("copied quality voice shares or loses state: rate%d frame%d", rate, frame)
			}
		}
		c.CopyStateFrom(a)
		b.Reset()
		b.NoteOn(84, 127, false)
		for frame := range 512 {
			b.Next()
			if a.Next() != c.Next() {
				t.Fatalf("reset/retrigger of copied voice changed source: rate%d frame%d", rate, frame)
			}
		}
		if got := testing.AllocsPerRun(100, func() { b.CopyStateFrom(a); b.NoteOff(); b.Next(); b.Reset() }); got != 0 {
			t.Fatalf("quality state copy allocated %g", got)
		}
	}
}

func TestAuthoredReleaseRetiresWhenGateDoesNotFollowNoteOff(t *testing.T) {
	program := authoredReleaseProgram()
	program.Nodes[1] = Node{Op: Constant, Value: 1}
	program.Len, program.Output = 7, 6
	pool, err := NewPool(program, 48000)
	if err != nil {
		t.Fatal(err)
	}
	pool.NoteOn([4]uint8{60}, 1, 100, false, Cohort{1, 1})
	for range 4800 {
		pool.Next()
	}
	pool.ReleaseAll()
	var last float32
	for range pool.releaseLimitFrames + 64 {
		last = pool.Next()
	}
	if pool.ActiveVoices() != 0 || last != 0 {
		t.Fatal("a constant ADSR gate kept its released slot indefinitely")
	}
}

func TestAuthoredReleasePreservesDelayedTailAfterSilentGap(t *testing.T) {
	for _, count := range []int{1, 2} {
		program := authoredReleaseProgram()
		program.Nodes[2].Value, program.Nodes[3].Value = 1, 1
		program.Nodes[4].Value, program.Nodes[5].Value = 1, 1
		program.Nodes[12] = Node{Op: Constant, Value: 30}
		program.Nodes[13] = Node{Op: Delay, A: 11, B: 12}
		program.Len, program.Output = 14, 13
		if count == 2 {
			program.Nodes[14] = Node{Op: Delay, A: 13, B: 12}
			program.Len, program.Output = 15, 14
		}
		pool, err := NewPool(program, 48000)
		if err != nil {
			t.Fatal(err)
		}
		voice, err := NewVoice(program, 48000)
		if err != nil {
			t.Fatal(err)
		}
		pool.NoteOn([4]uint8{60}, 1, 100, false, Cohort{1, 1})
		voice.NoteOn(60, 100, false)
		for range 96 {
			pool.Next()
			voice.Next()
		}
		pool.ReleaseAll()
		voice.NoteOff()
		var energy float64
		for frame := range 12000 {
			got, want := pool.Next(), voice.Next()
			if math.Abs(float64(got-want)) > 1e-7 {
				t.Fatalf("%d delays: tail lost at frame%d pool=%g voice=%g", count, frame, got, want)
			}
			energy += float64(got) * float64(got)
		}
		if energy < .01 {
			t.Fatal("delayed pulse was silent")
		}
		if pool.ActiveVoices() != 0 {
			t.Fatal("finished delayed tail did not retire")
		}
	}
}

func TestPoolRejectsInvalidProgramBeforeInspectingNodes(t *testing.T) {
	for _, program := range []*Program{nil, {}, {Len: 129, Output: 0}} {
		if _, err := NewPoolFromProgram(program, 48000); err == nil {
			t.Fatal("invalid pool program accepted")
		}
	}
}
