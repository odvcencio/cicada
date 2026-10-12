package control

import (
	"encoding/base64"
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
	p := s.Playhead()
	if p.Tick != 11280 || p.Tempo != 120000 || p.At(250*time.Millisecond) != 7920 {
		t.Fatalf("native loop position %+v -> %f", p, p.At(250*time.Millisecond))
	}
	s.Playing = false // The last binary frame can still say playing after Stop.
	if got := s.Playhead().At(time.Second); got != 11280 {
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
	if got := s.Playhead(); got.Tick != 11280 || got.Tempo != 0 {
		t.Fatalf("invalid telemetry did not fall back: %+v", got)
	}
}
