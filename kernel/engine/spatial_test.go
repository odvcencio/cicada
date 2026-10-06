package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/spatial"
)

func spatialConfig(rate int) Config {
	cfg := Config{SampleRate: rate, MaxBlock: 128, Tracks: 2, MaxVoices: 2, BPMMilli: 120000}
	for i := 0; i < 2; i++ {
		cfg.Track[i] = TrackConfig{Kind: VoiceGraph, GainSet: true, Pan: -1, Graph: graph.Program{Len: 1, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Constant, Value: .1}}}}
	}
	cfg.Track[1].BusSFX = true
	return cfg
}

func TestSpatialBusListenerRotationTranslationAndStereoRestore(t *testing.T) {
	e, err := New(spatialConfig(48000))
	if err != nil {
		t.Fatal(err)
	}
	var left, right [128]float32
	var buses [128]spatial.Frame
	if !e.PushBatch([]cmd.Command{cmd.TrackPosition(0, 1, 0, 0, 0), cmd.TrackPosition(1, 0, 0, 2, 0)}) {
		t.Fatal("position commands rejected")
	}
	e.RenderWithBFormat(left[:], right[:], buses[:])
	if buses[127].Music != (spatial.BFormat{W: .1, X: .1}) || buses[127].SFX != (spatial.BFormat{W: .05, Z: .05}) {
		t.Fatalf("wrong world buses: %+v", buses[127])
	}
	e.Push(cmd.ListenerRotation(math.Pi/2, 0, 0, 0))
	e.RenderWithBFormat(left[:], right[:], buses[:])
	if b := buses[0].Music; b.W != .1 || math.Abs(float64(b.Y+.1)) > 1e-7 || math.Abs(float64(b.X)) > 1e-7 {
		t.Fatalf("listener rotation did not rotate the bus: %+v", b)
	}
	e.Push(cmd.ListenerPosition(1, -1, 0, 0))
	e.RenderWithBFormat(left[:], right[:], buses[:])
	if b := buses[0].Music; math.Abs(float64(b.X-.1)) > 1e-7 || math.Abs(float64(b.Y)) > 1e-7 {
		t.Fatalf("listener translation did not re-encode: %+v", b)
	}
	e.Push(cmd.TrackStereo(0, 0))
	e.RenderWithBFormat(left[:], right[:], buses[:])
	if buses[0].Music != (spatial.BFormat{}) {
		t.Fatal("stereo track remained on spatial bus")
	}
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.Fault {
			t.Fatal(message)
		}
	}
}

func TestSpatialCommandsScheduledAndBlockInvariant(t *testing.T) {
	commands := []cmd.Command{
		{Op: cmd.OpPlay, Track: 255}, cmd.TrackPosition(0, 1, 0, 0, 0), cmd.TrackPosition(1, 0, 1, 1, 0),
		cmd.ListenerRotation(math.Pi/2, 0, 0, 7), cmd.ListenerPosition(.125, .25, .375, 14),
		cmd.ListenerRotation(.25, -.5, .75, 21), cmd.TrackStereo(1, 28),
	}
	render := func(block int) ([2048]float32, [2048]float32, [2048]spatial.Frame) {
		e, err := New(spatialConfig(48000))
		if err != nil {
			t.Fatal(err)
		}
		if !e.PushBatch(commands) {
			t.Fatal("scheduled spatial commands rejected")
		}
		var l, r [2048]float32
		var bus [2048]spatial.Frame
		for at := 0; at < len(l); at += block {
			end := min(len(l), at+block)
			e.RenderWithBFormat(l[at:end], r[at:end], bus[at:end])
		}
		return l, r, bus
	}
	l, r, buses := render(128)
	for _, block := range []int{1, 17, 64} {
		otherL, otherR, otherBus := render(block)
		if l != otherL || r != otherR || buses != otherBus {
			t.Fatalf("spatial output changes with block size %d", block)
		}
	}
	// At 120 BPM/48 kHz the command at tick 7 falls at frame 175.
	if buses[174].Music.X != .1 || math.Abs(float64(buses[175].Music.Y+.1)) > 1e-7 {
		t.Fatal("rotation missed its sample tick")
	}
}

func TestSpatialRenderCommandsAllocationFreeAndReset(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		e, err := New(spatialConfig(rate))
		if err != nil {
			t.Fatal(err)
		}
		var left, right [128]float32
		var buses [128]spatial.Frame
		if allocations := testing.AllocsPerRun(100, func() {
			e.Reset()
			e.Push(cmd.TrackPosition(0, 1, 2, 3, 0))
			e.Push(cmd.TrackPosition(1, -1, -2, -3, 0))
			e.Push(cmd.ListenerRotation(.25, -.5, .75, 0))
			e.Push(cmd.ListenerPosition(.1, .2, .3, 0))
			e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixGain), Arg0: math.Float32bits(-12)})
			for range 8 {
				e.RenderWithBFormat(left[:], right[:], buses[:])
			}
			e.Push(cmd.TrackStereo(0, 0))
			e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255})
			e.Render(left[:], right[:])
		}); allocations != 0 {
			t.Fatalf("rate %d spatial Render allocated %g objects", rate, allocations)
		}
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatal(message)
			}
		}
		t.Logf("rate=%d spatial Render allocations=0", rate)
	}
}

func TestSpatialGainPanMuteAndBusRouting(t *testing.T) {
	cfg := spatialConfig(48000)
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var l, r [128]float32
	var buses [128]spatial.Frame
	e.Push(cmd.TrackPosition(0, 1, 0, 0, 0))
	e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixGain), Arg0: math.Float32bits(-6)})
	e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(1)})
	for range 100 {
		e.RenderWithBFormat(l[:], r[:], buses[:])
	}
	if math.Abs(float64(buses[127].Music.W)-.1*math.Pow(10, -.3)) > 1e-5 {
		t.Fatalf("spatial track gain/pan: %+v", buses[127].Music)
	}
	e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixMute), Arg0: math.Float32bits(1)})
	for range 8 {
		e.RenderWithBFormat(l[:], r[:], buses[:])
	}
	if buses[127].Music != (spatial.BFormat{}) {
		t.Fatal("mute left the positioned track audible")
	}
	for _, route := range []string{"music", "sfx"} {
		cfg := spatialConfig(48000)
		cfg.Tracks, cfg.MaxVoices = 1, 1
		cfg.Track[0].BusSFX = route == "sfx"
		cfg.MusicBusMute, cfg.SFXBusMute = route == "music", route == "sfx"
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.Push(cmd.TrackPosition(0, 0, 1, 0, 0))
		for range 4 {
			e.Render(l[:], r[:])
			for i := range l {
				if l[i] != 0 || r[i] != 0 {
					t.Fatalf("muted %s spatial bus was audible", route)
				}
			}
		}
	}
}

func BenchmarkSpatialRender16Tracks128(b *testing.B) {
	cfg := spatialConfig(48000)
	cfg.Tracks, cfg.MaxVoices = 16, 16
	for i := 2; i < 16; i++ {
		cfg.Track[i] = cfg.Track[0]
	}
	e, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		e.Push(cmd.TrackPosition(uint8(i), 1, float32(i), .5, 0))
	}
	var l, r [128]float32
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		e.Render(l[:], r[:])
	}
}
