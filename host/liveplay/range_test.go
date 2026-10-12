package liveplay

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
)

func TestListeningRangeStopsAtExactExclusiveEndAndLoopsAtPickup(t *testing.T) {
	for _, start := range []int64{0, 320, seq.TicksPerBar + seq.PPQ} {
		for _, loop := range []bool{false, true} {
			p, err := New(meterScore(t, "range", -12), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			r := PlaybackRange{StartTick: start, EndTick: start + 320, Loop: loop}
			if err := p.PlayRange(r); err != nil {
				t.Fatal(err)
			}
			clock, _ := seq.NewClock(48_000, 120_000)
			frames := clock.SampleAtTick(r.EndTick) - clock.SampleAtTick(r.StartTick)
			pcm := make([]byte, frames*8)
			if _, err := io.ReadFull(p, pcm); err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(pcm, make([]byte, len(pcm))) {
				t.Fatal("range was silent")
			}
			last := pcm[len(pcm)-8:]
			if math.Float32frombits(binary.LittleEndian.Uint32(last)) != 0 || math.Float32frombits(binary.LittleEndian.Uint32(last[4:])) != 0 {
				t.Fatal("range did not fade to zero at its final frame")
			}
			var frame [8]byte
			if _, err := io.ReadFull(p, frame[:]); err != nil {
				t.Fatal(err)
			}
			if loop {
				want := Position{Bar: start/seq.TicksPerBar + 1, Step: start%seq.TicksPerBar/seq.TicksPerStep + 1}
				if p.RangeFinished() || p.RangeCycles() != 1 || p.Position() != want {
					t.Fatalf("pickup loop: finished=%v cycles=%d position=%+v want=%+v", p.RangeFinished(), p.RangeCycles(), p.Position(), want)
				}
			} else {
				if !p.RangeFinished() || frame != [8]byte{} {
					t.Fatal("once-through audition rendered past its end")
				}
				if _, err := io.ReadFull(p, pcm); err != nil || !bytes.Equal(pcm, make([]byte, len(pcm))) {
					t.Fatalf("completed audition was not silent: %v", err)
				}
			}
			p.Close()
		}
	}
}

func TestListeningJumpWaitsForStereoFrameAndClearContinuesSong(t *testing.T) {
	p, _ := New(testScore(t, "range", 120_000), 48_000)
	defer p.Close()
	var part [8]byte
	_, _ = io.ReadFull(p, part[:3])
	r := PlaybackRange{StartTick: 4*seq.TicksPerBar + seq.PPQ, EndTick: 5 * seq.TicksPerBar, Loop: true}
	if err := p.PlayRange(r); err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadFull(p, part[:5])
	if p.Position().Bar != 1 {
		t.Fatal("jump split a stereo frame")
	}
	_, _ = io.ReadFull(p, part[:])
	if p.Position() != (Position{Bar: 5, Step: 5}) {
		t.Fatalf("pickup jump: %+v", p.Position())
	}
	p.ClearRange()
	_, _ = io.CopyN(io.Discard, p, 96_000*8)
	if p.RangeFinished() || p.PlaybackRange() != nil || p.RangeCycles() != 0 || p.Position().Bar < 6 {
		t.Fatalf("cleared loop did not continue the song: %+v", p.Position())
	}
}

func TestShortLoopTakesLatestEditAndNewBoundsWithoutAudioAllocations(t *testing.T) {
	p, _ := New(testScore(t, "initial", 120_000), 48_000)
	defer p.Close()
	r := PlaybackRange{StartTick: 960, EndTick: 1200, Loop: true}
	if err := p.PlayRange(r); err != nil {
		t.Fatal(err)
	}
	var pcm [blockFrames * 8]byte
	_, _ = io.ReadFull(p, pcm[:])
	next := testScore(t, "updated", 150_000)
	r = PlaybackRange{StartTick: 3*seq.TicksPerBar + 320, EndTick: 3*seq.TicksPerBar + 640, Loop: true}
	next.Range = &r
	if err := p.Offer(next); err != nil {
		t.Fatal(err)
	}
	if n := testing.AllocsPerRun(200, func() { _, _ = io.ReadFull(p, pcm[:]) }); n != 0 {
		t.Fatalf("range callback allocated: %g", n)
	}
	if p.current.Name != "updated" || p.BPMMilli() != 150_000 || *p.PlaybackRange() != r || p.RangeCycles() < 1 || p.Position().Bar != 4 {
		t.Fatalf("short loop did not take its edit: %s %+v", p.current.Name, p.Position())
	}
	for _, bad := range []PlaybackRange{{StartTick: -1, EndTick: 1}, {StartTick: 2, EndTick: 2}, {StartTick: 2, EndTick: 1}} {
		if err := p.PlayRange(bad); err == nil || *p.PlaybackRange() != r {
			t.Fatal("invalid range replaced a valid loop")
		}
	}
}
