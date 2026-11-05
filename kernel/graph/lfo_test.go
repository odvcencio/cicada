package graph

import (
	"math"
	"testing"
)

func lfoTestVoice(t *testing.T, lock uint8) *Voice {
	t.Helper()
	p := Program{Len: 1, Output: 0}
	p.Nodes[0] = Node{Op: LFO, A: 0, B: lock, Value: 1}
	v, err := NewVoiceFromProgram(&p, 48000)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLFOSongClockAndNoteLock(t *testing.T) {
	song, note := lfoTestVoice(t, 0), lfoTestVoice(t, 1)
	clock := &LFOClock{Playing: true}
	song.BindLFOClock(clock)
	note.BindLFOClock(clock)
	note.NoteOn(60, 100, false)
	for _, tc := range []struct {
		sample uint64
		want   float32
	}{{0, 0}, {12000, 1}, {24000, 0}, {36000, -1}} {
		clock.Sample = tc.sample
		if got := song.Next(); math.Abs(float64(got-tc.want)) > 1e-6 {
			t.Fatalf("song sample %d: got %g, want %g", tc.sample, got, tc.want)
		}
	}
	for range 12000 {
		note.Next()
	}
	if got := note.Next(); math.Abs(float64(got-1)) > 1e-6 {
		t.Fatalf("note phase got %g", got)
	}
	note.NoteOn(62, 100, true)
	if got := note.Next(); got < .99 {
		t.Fatalf("slide restarted note LFO: %g", got)
	}
	note.NoteOn(64, 100, false)
	if got := note.Next(); got != 0 {
		t.Fatalf("fresh note did not restart LFO: %g", got)
	}
}

func TestLFOValidationAndCopyBinding(t *testing.T) {
	for _, n := range []Node{{Op: LFO, A: 4, Value: 1}, {Op: LFO, B: 2, Value: 1}, {Op: LFO, Value: float32(math.NaN())}, {Op: LFO, Value: 21}} {
		p := Program{Len: 1, Output: 0}
		p.Nodes[0] = n
		if err := ValidateProgram(&p, 48000); err == nil {
			t.Fatalf("accepted invalid node %+v", n)
		}
	}
	a, b := lfoTestVoice(t, 0), lfoTestVoice(t, 0)
	first, second := &LFOClock{Sample: 12000, Playing: true}, &LFOClock{Sample: 36000, Playing: true}
	a.BindLFOClock(first)
	b.BindLFOClock(second)
	b.CopyStateFrom(a)
	if got := b.Next(); got != -1 {
		t.Fatalf("copy lost destination clock: %g", got)
	}
}

func TestLFOQualitySubstepsAndNoRenderAllocations(t *testing.T) {
	p := Program{Len: 4, Output: 0}
	p.Nodes[0] = Node{Op: LFO, Value: 20}
	p.Nodes[1] = Node{Op: Constant, Value: 100}
	p.Nodes[2] = Node{Op: Constant, Value: .5}
	p.Nodes[3] = Node{Op: Pulse, A: 1, B: 2}
	v, err := NewVoiceFromProgram(&p, 48000)
	if err != nil {
		t.Fatal(err)
	}
	clock := &LFOClock{Sample: 1000}
	v.BindLFOClock(clock)
	first, second := v.nextSample(0), v.nextSample(1)
	if first != second {
		t.Fatalf("stopped song advanced inside a quality frame: %g vs %g", first, second)
	}
	clock.Playing = true
	first, second = v.nextSample(0), v.nextSample(1)
	if first == second {
		t.Fatal("playing song did not advance between quality substeps")
	}
	if got := testing.AllocsPerRun(1000, func() { v.Next() }); got != 0 {
		t.Fatalf("render allocated %g times per frame", got)
	}
}
