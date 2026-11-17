package control

import (
	"encoding/base64"
	"m31labs.dev/cicada/kernel/seq"
	"testing"
	"time"

	"m31labs.dev/cicada/host/liveplay"
)

func TestPlayheadReadsNativeFrameAndWrapsAudition(t *testing.T) {
	frame := liveplay.TransportFrame{Bar: 3, Beat: 4, Tick: 720, TempoMilli: 120000, Playing: true}
	var raw [liveplay.TransportFrameSize]byte
	frame.Encode(raw[:])
	s := PlaybackState{Playing: true, Bar: 3, Step: 16, Range: &PlaybackRange{Start: 7680, End: 11520, Loop: true}}
	s.Frames.Transport = base64.StdEncoding.EncodeToString(raw[:])
	p := s.Playhead(seq.MeterMap{})
	if p.Tick != 11280 || p.Tempo != 120000 || p.At(250*time.Millisecond) != 7920 {
		t.Fatalf("native loop position %+v -> %f", p, p.At(250*time.Millisecond))
	}
	s.Playing = false // The last binary frame can still say playing after Stop.
	if got := s.Playhead(seq.MeterMap{}).At(time.Second); got != 11280 {
		t.Fatalf("stopped cursor advanced: %f", got)
	}
	p.Range.Loop = false
	p.Playing = true
	if got := p.At(time.Second); got != 11520 {
		t.Fatalf("audition ran past its exclusive end: %f", got)
	}
	p.Range = nil
	if got := p.At(time.Hour); got != 11952 {
		t.Fatalf("disconnected cursor extrapolated too far: %f", got)
	}
	s.Frames.Transport = "bad frame"
	if got := s.Playhead(seq.MeterMap{}); got.Tick != 11280 || got.Tempo != 0 {
		t.Fatalf("invalid telemetry did not fall back: %+v", got)
	}
}

func TestPlayheadSongTicks(t *testing.T) {
	cases := []struct {
		name            string
		bar, beat, tick int32
		value           *int64
		want, tempo     int64
	}{
		{"3/4", 3, 2, 100, ptrTick(6820), 6820, 120000},
		{"6/8", 2, 5, 100, ptrTick(4900), 4900, 120000},
		{"7/4", 2, 7, 100, ptrTick(12580), 12580, 120000},
		{"4/4", 3, 2, 100, ptrTick(8740), 8740, 120000},
		{"legacy", 3, 2, 100, nil, 8740, 120000},
		{"negative", 3, 2, 100, ptrTick(-1), 240, 0},
		{"absurd", 3, 2, 100, ptrTick(1 << 53), 240, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := liveplay.TransportFrame{Bar: tc.bar, Beat: tc.beat, Tick: tc.tick, TempoMilli: 120000}
			var raw [liveplay.TransportFrameSize]byte
			frame.Encode(raw[:])
			s := PlaybackState{Bar: 1, Step: 2, FrameTick: tc.value}
			s.Frames.Transport = base64.StdEncoding.EncodeToString(raw[:])
			p := s.Playhead(seq.MeterMap{})
			if p.Tick != tc.want || p.Tempo != tc.tempo {
				t.Fatalf("got %+v", p)
			}
		})
	}
	for _, kind := range []string{"short", "version"} {
		t.Run(kind, func(t *testing.T) {
			var raw [liveplay.TransportFrameSize]byte
			(liveplay.TransportFrame{Bar: 3, Beat: 2, TempoMilli: 120000}).Encode(raw[:])
			data := raw[:]
			if kind == "short" {
				data = data[:20]
			} else {
				data[0] = 2
			}
			s := PlaybackState{Bar: 3, Step: 2, Tick: ptrTick(6000), FrameTick: ptrTick(6820)}
			s.Frames.Transport = base64.StdEncoding.EncodeToString(data)
			p := s.Playhead(seq.MeterMap{})
			if p.Tick != 6000 || p.Tempo != 0 {
				t.Fatalf("got %+v", p)
			}
		})
	}
}

func ptrTick(v int64) *int64 { return &v }
