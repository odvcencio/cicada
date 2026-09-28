package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/mix"
)

func sineProgram(frequency, amplitude float32) graph.Program {
	var p graph.Program
	p.Nodes[0] = graph.Node{Op: graph.Constant, Value: frequency}
	p.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
	p.Nodes[2] = graph.Node{Op: graph.Constant, Value: amplitude}
	p.Nodes[3] = graph.Node{Op: graph.Multiply, A: 1, B: 2}
	p.Len, p.Output = 4, 3
	return p
}

func liveMeterEngine(t *testing.T, tracks int) *Engine {
	t.Helper()
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: tracks, MaxVoices: tracks, BPMMilli: 120_000}
	for i := 0; i < tracks; i++ {
		cfg.Track[i].Kind = VoiceGraph
		cfg.Track[i].Graph = sineProgram(375, 1)
		cfg.Track[i].GainDB, cfg.Track[i].GainSet = 0, true
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < tracks; i++ {
		e.voices[i].graph.NoteOn(69, 127, false)
	}
	return e
}

func pushParam(t *testing.T, e *Engine, track uint8, id kernel.ParamID, value float32) {
	t.Helper()
	if !e.Push(cmd.Command{Op: cmd.OpSetParam, Track: track, Index: uint16(id), Arg0: math.Float32bits(value)}) {
		t.Fatal("parameter command was rejected")
	}
}

func renderBlocks(e *Engine, blocks int) {
	var left, right [128]float32
	for range blocks {
		e.Render(left[:], right[:])
	}
}

type collectedMeters struct {
	values [256][3]float64
	found  [256][3]bool
}

func collectMeters(e *Engine) collectedMeters {
	var meters collectedMeters
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.Meter && message.A < 3 {
			meters.values[message.Track][message.A] = float64(math.Float32frombits(message.B))
			meters.found[message.Track][message.A] = true
		}
	}
	return meters
}

func (meters collectedMeters) value(t *testing.T, track uint8, quantity uint16) float64 {
	t.Helper()
	if !meters.found[track][quantity] {
		t.Fatalf("missing meter track=%#x quantity=%d", track, quantity)
	}
	return meters.values[track][quantity]
}

func dbFS(value float64) float64 { return 20 * math.Log10(value) }

func TestTrackGainUsesRegistrySmoothingRamp(t *testing.T) {
	cfg := Config{SampleRate: 48_000, MaxBlock: 1, Tracks: 1, MaxVoices: 1}
	cfg.Track[0].Kind = VoiceOff
	cfg.Track[0].GainDB, cfg.Track[0].GainSet = -60, true
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	start := e.voices[0].mix.Left
	target := float32(1 / math.Sqrt2)
	pushParam(t, e, 0, kernel.ParamMixGain, 0)
	alpha := float64(smoothingAlpha(kernel.ParamMixGain, cfg.SampleRate))
	previous := float64(start)
	var left, right [1]float32
	for frame := 0; frame < 240; frame++ {
		e.Render(left[:], right[:])
		current := float64(e.voices[0].mix.Left)
		if current < previous {
			t.Fatalf("gain ramp reversed at frame %d: %g < %g", frame, current, previous)
		}
		if current-previous > alpha*float64(target-start)+1e-8 {
			t.Fatalf("gain jump %g exceeds registry ramp bound %g", current-previous, alpha*float64(target-start))
		}
		previous = current
	}
	// A one-pole with the registry's 5 ms time constant reaches
	// 1-(1-alpha)^240 of the step after 5 ms at 48 kHz.
	want := float64(start) + (float64(target)-float64(start))*(1-math.Pow(1-alpha, 240))
	if math.Abs(previous-want) > 2e-6 {
		t.Fatalf("gain after 5 ms %.8f, want %.8f", previous, want)
	}
}

func TestMuteRampReachesExactZeroAndReturnsInTenMilliseconds(t *testing.T) {
	cfg := Config{SampleRate: 48_000, MaxBlock: 1, Tracks: 1, MaxVoices: 1}
	cfg.Track[0].Kind = VoiceOff
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var left, right [1]float32
	pushParam(t, e, 0, kernel.ParamMixMute, 1)
	for frame := 0; frame < 480; frame++ {
		e.Render(left[:], right[:])
	}
	if e.voices[0].muteGain != 0 {
		t.Fatalf("mute reached %g after 480 frames", e.voices[0].muteGain)
	}
	pushParam(t, e, 0, kernel.ParamMixMute, 0)
	for frame := 0; frame < 480; frame++ {
		e.Render(left[:], right[:])
	}
	if e.voices[0].muteGain != 1 {
		t.Fatalf("unmute reached %g after 480 frames", e.voices[0].muteGain)
	}
}

func TestSoloSilencesTrackAndSendButKeepsReturnTail(t *testing.T) {
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 2, MaxVoices: 2}
	cfg.Track[0].Kind, cfg.Track[1].Kind = VoiceGraph, VoiceGraph
	cfg.Track[0].Graph, cfg.Track[1].Graph = sineProgram(375, .2), sineProgram(375, .2)
	cfg.Track[0].GainDB, cfg.Track[1].GainDB = 0, 0
	cfg.Track[0].GainSet, cfg.Track[1].GainSet = true, true
	cfg.Track[1].SendA, cfg.Track[1].SendPre = 1, true
	delay := fx.DefaultDelayParams()
	delay.Division, delay.TimeMs, delay.Feedback, delay.DampHz = fx.FreeDelay, 1, .8, 16_000
	cfg.DelayA = &delay
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.voices[0].graph.NoteOn(69, 127, false)
	e.voices[1].graph.NoteOn(69, 127, false)
	renderBlocks(e, 2)
	pushParam(t, e, 0, kernel.ParamMixSolo, 1)
	renderBlocks(e, 10)
	if e.voices[1].muteGain != 0 || e.voices[1].muteTarget != 0 || e.voices[0].muteGain != 1 {
		t.Fatalf("solo ramp state: solo=%g other=%g target=%g", e.voices[0].muteGain, e.voices[1].muteGain, e.voices[1].muteTarget)
	}
	// The complete meter window after the 10 ms ramp proves the other track is
	// silent. The return remains active because solo does not mute effect tails.
	renderBlocks(e, 4)
	meters := collectMeters(e)
	if got := meters.value(t, 1, 0); got != 0 {
		t.Fatalf("non-solo track peak is %g after solo ramp", got)
	}
	if got := meters.value(t, 0xf0, 0); got == 0 {
		t.Fatal("solo cleared the delay return tail")
	}
}

func TestNamedSendTapFlagsStayIndependent(t *testing.T) {
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 1}
	cfg.Track[0].Kind = VoiceOff
	cfg.Track[0].SendA, cfg.Track[0].SendB = 0.25, 0.5
	cfg.Track[0].SendAPre = true
	delay, reverb := fx.DefaultDelayParams(), fx.DefaultReverbParams()
	cfg.DelayA, cfg.ReverbB = &delay, &reverb
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.voices[0].sendAPre || e.voices[0].sendBPre {
		t.Fatalf("per-send taps collapsed to one flag: delay=%t reverb=%t", e.voices[0].sendAPre, e.voices[0].sendBPre)
	}
}

func TestMasterSoloStateIsLoaded(t *testing.T) {
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, MasterSolo: true}
	cfg.Track[0].Kind = VoiceOff
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.masterSolo {
		t.Fatal("master solo state was dropped during engine load")
	}
}

func TestTrackMeterMatchesEqualPowerSineFormula(t *testing.T) {
	e := liveMeterEngine(t, 1)
	e.voices[0].gainDB, e.voices[0].pan = -6, 0
	e.voices[0].mix = newMix(-6, 0)
	e.voices[0].targetMix = e.voices[0].mix
	renderBlocks(e, 4) // four 128-frame blocks, each one exact 375 Hz cycle
	meters := collectMeters(e)
	peak := meters.value(t, 0, 0)
	rms := meters.value(t, 0, 1)
	// Spec 05 defines a -6 dB track gain and equal-power pan. A unit sine has
	// peak 1 and RMS 1/sqrt(2); pan 0 gives sqrt(1/2) on both channels. RMS is
	// averaged across both channels: sqrt((L^2+R^2)/2) per sample.
	gain := math.Pow(10, -6.0/20)
	panGain := math.Cos(math.Pi / 4)
	wantPeak := gain * panGain
	wantRMS := (1 / math.Sqrt2) * gain * panGain
	if math.Abs(dbFS(peak)-dbFS(wantPeak)) > .05 {
		t.Fatalf("track peak %.3f dBFS, want %.3f dBFS", dbFS(peak), dbFS(wantPeak))
	}
	if math.Abs(dbFS(rms)-dbFS(wantRMS)) > .05 {
		t.Fatalf("track RMS %.3f dBFS, want %.3f dBFS", dbFS(rms), dbFS(wantRMS))
	}
}

func newMix(gainDB, pan float64) mix.Track { return mix.NewTrack(gainDB, pan, false) }

func TestLimiterMeterMatchesCeilingGainReduction(t *testing.T) {
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 1}
	cfg.Track[0].Kind, cfg.Track[0].Graph = VoiceGraph, sineProgram(375, 2)
	cfg.Track[0].GainDB, cfg.Track[0].GainSet, cfg.Track[0].Pan, cfg.Track[0].BusSFX = 0, true, -1, true
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.voices[0].graph.NoteOn(69, 127, false)
	e.meterRate = 1
	var left, right [128]float32
	for range 8 {
		e.Render(left[:], right[:])
	}
	got := collectMeters(e).value(t, 0xfb, 2)
	// Spec 05 sets the limiter ceiling to -0.3 dBFS. The SFX route avoids the
	// music bus trim; at pan -1 and 0 dB track gain its sustained sine peak is
	// 2, so static gain reduction is 20*log10(2/ceiling).
	ceiling := math.Pow(10, -.3/20)
	want := dbFS(2 / ceiling)
	if math.Abs(got-want) > .1 {
		t.Fatalf("limiter gain reduction %.3f dB, want %.3f dB", got, want)
	}
}

func TestMasterGainIsIncludedInPreLimiterMeter(t *testing.T) {
	readPreLimiterRMS := func(masterGainDB float64) float64 {
		cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, MasterGainDB: masterGainDB}
		cfg.Track[0].Kind, cfg.Track[0].Graph = VoiceGraph, sineProgram(375, .2)
		cfg.Track[0].GainDB, cfg.Track[0].GainSet = 0, true
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.voices[0].graph.NoteOn(69, 127, false)
		renderBlocks(e, 4) // One complete 512-frame meter window.
		return collectMeters(e).value(t, 0xfd, 1)
	}

	unityRMS := readPreLimiterRMS(0)
	boostedRMS := readPreLimiterRMS(6)
	gotDB := dbFS(boostedRMS) - dbFS(unityRMS)
	if math.Abs(gotDB-6) > .05 {
		t.Fatalf("0xFD pre-limiter meter gain delta %.3f dB, want 6 dB", gotDB)
	}
}

func TestCompressorMeterMatchesStaticGainComputer(t *testing.T) {
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 1}
	cfg.Track[0].Kind, cfg.Track[0].Graph = VoiceGraph, sineProgram(375, 1)
	cfg.Track[0].GainDB, cfg.Track[0].GainSet = -6, true
	params := fx.DefaultCompParams()
	params.Threshold, params.Ratio, params.Knee = -18, 4, 0
	params.AttackMs, params.ReleaseMs, params.MakeupAuto = .1, 100, false
	cfg.CompMusic = &params
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.voices[0].graph.NoteOn(69, 127, false)
	e.meterRate = 1
	var left, right [128]float32
	for range 12 {
		e.Render(left[:], right[:])
	}
	got := collectMeters(e).value(t, 0xfc, 2)
	// Spec 05's hard-knee static curve is (input dB - threshold)*(1-1/ratio).
	// The detector receives the track's -6 dB fader, equal-power pan at 0, and
	// the music bus's -3 dB trim, giving a peak of 10^(-12/20).
	inputDB := dbFS(math.Pow(10, -12.0/20))
	want := (inputDB - params.Threshold) * (1 - 1/params.Ratio)
	if math.Abs(got-want) > .2 {
		t.Fatalf("compressor gain reduction %.3f dB, want %.3f dB", got, want)
	}
}
