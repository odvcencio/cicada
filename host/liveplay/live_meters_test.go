package liveplay

import (
	"io"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/graph"
)

// loudScore plays a full-scale sine, optionally through the music-bus
// compressor. The track gain sets how far the master sum is above the limiter.
func loudScore(t *testing.T, gainDB, pan float64, compress bool) Score {
	t.Helper()
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000}
	program := graph.Program{}
	program.Nodes[0] = graph.Node{Op: graph.Constant, Value: 375}
	program.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
	program.Len, program.Output = 2, 1
	cfg.Track[0].Kind, cfg.Track[0].Graph = engine.VoiceGraph, program
	cfg.Track[0].GainDB, cfg.Track[0].GainSet, cfg.Track[0].Pan = gainDB, true, pan
	if compress {
		comp := fx.DefaultCompParams()
		cfg.CompMusic = &comp
	}
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Score{
		Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Name: "loud",
		Tracks:     []TrackSlots{{ID: "bass"}},
		Parameters: []ParameterValue{{Track: 0, ID: kernel.ParamMixGain, Value: float32(gainDB)}},
	}
}

func lastMeterFrame(t *testing.T, p *Player, bytes int64) MeterFrame {
	t.Helper()
	var last MeterFrame
	var seen bool
	for read := int64(0); read < bytes; read += blockFrames * 8 {
		if _, err := io.CopyN(io.Discard, p, blockFrames*8); err != nil {
			t.Fatal(err)
		}
		select {
		case last = <-p.Meters():
			seen = true
		default:
		}
	}
	if !seen {
		t.Fatal("render thread delivered no meter frame")
	}
	return last
}

func TestStereoMeterFramesFollowPanThroughThePlayer(t *testing.T) {
	p, err := New(meterScore(t, "pan", -6), 48_000) // pan -1: hard left
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	frame := lastMeterFrame(t, p, 48_000*8/4)
	track := frame.Tracks[0]
	if track.PeakL < .3 || track.PeakR != 0 || track.RMSL <= 0 || track.RMSR != 0 {
		t.Fatalf("hard-left track meter: %+v", track)
	}
	if track.Peak != track.PeakL || track.RMS <= 0 {
		t.Fatalf("mono quantities lost their meaning: %+v", track)
	}
}

func TestMeterFrameCarriesGainReductionAndPreLimiterOver(t *testing.T) {
	quiet, err := New(loudScore(t, -30, 0, true), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer quiet.Close()
	frame := lastMeterFrame(t, quiet, 48_000*8/2)
	if frame.PreLimiterOver() || frame.LimiterGR != 0 {
		t.Fatalf("a quiet mix reported the limiter or an over: %+v", frame)
	}
	squeezed, err := New(loudScore(t, -6, 0, true), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer squeezed.Close()
	if frame = lastMeterFrame(t, squeezed, 48_000*8/2); frame.CompGR <= 0 {
		t.Fatalf("a signal above the compressor threshold reported no gain reduction: %+v", frame.CompGR)
	}
	loud, err := New(loudScore(t, 6, 0, false), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer loud.Close()
	frame = lastMeterFrame(t, loud, 48_000*8/2)
	if !frame.PreLimiterOver() || frame.LimiterGR <= 0 {
		t.Fatalf("a hot mix must report an over and gain reduction: over=%v limiter=%g pre=%g", frame.PreLimiterOver(), frame.LimiterGR, frame.MasterPre.Peak)
	}
	pub := NewPublisher()
	loud.SetPublisher(pub)
	loud.PublishTelemetry()
	decoded, err := DecodeMetersFrame(pub.Latest()[FrameMeters])
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.Master.PreLimiterOver || decoded.Master.LimiterGRDB <= 0 || decoded.Tracks[0].PeakL <= 0 || decoded.Tracks[0].PeakR != decoded.Tracks[0].PeakL {
		t.Fatalf("published meters lost dynamics or stereo values: %+v", decoded.Master)
	}
}

func TestMetersFrameDynamicsTailRoundTripsAndLegacyFramesDecode(t *testing.T) {
	frame := MetersFrame{TrackCount: 1}
	frame.Tracks[0] = TrackMeter{PeakL: .5, PeakR: .25, RMSL: .1, RMSR: .05}
	frame.Master = MasterMeter{MomentaryLUFS: -14, ShortTermLUFS: -15, TruePeakDBTP: -1, CompGRDB: 3.5, LimiterGRDB: 1.25, PreLimiterOver: true}
	var buf [MetersFrameSize]byte
	if n := frame.Encode(buf[:]); n != MetersFrameSize {
		t.Fatalf("encoded %d bytes, want %d", n, MetersFrameSize)
	}
	got, err := DecodeMetersFrame(buf[:])
	if err != nil || got != frame {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	legacy, err := DecodeMetersFrame(buf[:MetersFrameSize-metersDynamicsSize])
	if err != nil || legacy.Master.CompGRDB != 0 || legacy.Master.LimiterGRDB != 0 || legacy.Master.PreLimiterOver || legacy.Master.TruePeakDBTP != -1 || legacy.Tracks[0] != frame.Tracks[0] {
		t.Fatalf("a frame without the dynamics tail must decode with zeros: %+v %v", legacy, err)
	}
	var wide MetersFrame
	wide.TrackCount = 40
	wide.Tracks[39] = TrackMeter{PeakL: 1}
	wide.Master.PreLimiterOver = true
	var wideBuf [MaxMetersFrameSize]byte
	if n := wide.Encode(wideBuf[:]); n != MaxMetersFrameSize {
		t.Fatalf("wide frame encoded %d bytes", n)
	}
	if got, err := DecodeMetersFrame(wideBuf[:]); err != nil || got != wide {
		t.Fatalf("wide round trip: %v", err)
	}
}

func TestCPULoadEMAIsReportedAndRenderDoesNotAllocate(t *testing.T) {
	p, err := New(meterScore(t, "load", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.CPULoad() != 0 {
		t.Fatalf("load before any block: %g", p.CPULoad())
	}
	if _, err := io.CopyN(io.Discard, p, blockFrames*8*50); err != nil {
		t.Fatal(err)
	}
	load := p.CPULoad()
	if !(load > 0) || math.IsNaN(load) || load > 5 {
		t.Fatalf("CPU load EMA %g is not a plausible fraction of the block budget", load)
	}
	var frame [blockFrames * 8]byte
	if allocs := testing.AllocsPerRun(100, func() {
		if _, err := p.Read(frame[:]); err != nil {
			t.Fatal(err)
		}
	}); allocs != 0 {
		t.Fatalf("render allocated %g times per block", allocs)
	}
}
