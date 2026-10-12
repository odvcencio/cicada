//go:build !tinygo && !js

package liveplay

import (
	"fmt"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
)

func TestNativeLastTrackLiveNotesOverridesAndMeters(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: blockFrames, Tracks: 64, MaxVoices: 1, BPMMilli: 120000}
	program := graph.Program{}
	program.Nodes[0] = graph.Node{Op: graph.Constant, Value: 375}
	program.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
	program.Len, program.Output = 2, 1
	cfg.Track[63].Kind, cfg.Track[63].Graph = engine.VoiceGraph, program
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	score := Score{Engine: e, SampleRate: 48000, BPMMilli: 120000, Tracks: make([]TrackSlots, 64)}
	for i := range score.Tracks {
		score.Tracks[i].ID = fmt.Sprintf("t%d", i)
	}
	score.Tracks[63].Kind, score.Tracks[63].Pitched = "instrument", true
	p, err := New(score, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.SetParam(63, kernel.ParamMixGain, -12); err != nil {
		t.Fatal(err)
	}
	if err := p.NoteWithID("t63", 60, 127, true, 40); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, blockFrames*8)
	for block := 0; block < 40; block++ {
		if _, err := p.Read(buf); err != nil {
			t.Fatal(err)
		}
	}
	var last MeterFrame
	for len(p.meters) > 0 {
		last = <-p.meters
	}
	if last.TrackCount != 64 || last.TrackIDs[63] != "t63" || last.Tracks[63].Peak <= 0 || math.IsNaN(float64(last.Tracks[63].Peak)) {
		t.Fatalf("last track meters: %+v", last)
	}
	identity := p.trackNames.Load().overrideTrack(63)
	overrides := p.overrides.Load().tracks[identity]
	if overrides == nil || identity.id != "t63" || identity.incarnation == 0 {
		t.Fatalf("last track override identity lost: %+v", identity)
	}
	if override := overrides.values[kernel.ParamMixGain]; !override.active {
		t.Fatalf("last track override lost: %+v", override)
	}
}

func TestNativeExpandedMeterWireRetainsLastTrackAndLegacyLayout(t *testing.T) {
	frame := MetersFrame{TrackCount: 64}
	frame.Tracks[63] = TrackMeter{PeakL: .75, RMSR: .25}
	frame.Master = MasterMeter{MomentaryLUFS: -14, ShortTermLUFS: -15, TruePeakDBTP: -1}
	var buf [MaxMetersFrameSize]byte
	if n := frame.Encode(buf[:]); n != MaxMetersFrameSize || buf[0] != 2 {
		t.Fatalf("expanded size=%d version=%d", n, buf[0])
	}
	got, err := DecodeMetersFrame(buf[:])
	if err != nil || got != frame {
		t.Fatalf("expanded roundtrip: %+v %v", got, err)
	}
	if allocs := testing.AllocsPerRun(1000, func() { frame.Encode(buf[:]) }); allocs != 0 {
		t.Fatalf("expanded encoding allocated %g", allocs)
	}
	frame = MetersFrame{TrackCount: 16}
	if n := frame.Encode(buf[:]); n != MetersFrameSize || buf[0] != FrameVersion {
		t.Fatalf("legacy size=%d version=%d", n, buf[0])
	}
	frame.TrackCount = 65
	if frame.Encode(buf[:]) != 0 {
		t.Fatal("overflow meter frame encoded")
	}
}
