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
