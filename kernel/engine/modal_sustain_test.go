package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/modal"
)

// The 50 percent gate of a 1/16 step at 120 BPM ends 62.5 ms, 3000 frames,
// into the note. The vibraphone damper takes 70 ms to fall 60 dB.
const modalGateFrames = 3000

// modalPedalConfig plays middle C on the first step of a one-bar pattern.
func modalPedalConfig(sustain float32) Config {
	cfg := Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 4, BPMMilli: 120000}
	cfg.Track[0] = TrackConfig{Kind: VoiceModal, Modal: modal.Vibraphone, PianoSustain: sustain}
	pattern := seq.Pattern{Len: 16, GatePercent: 50}
	pattern.Steps[0], _ = seq.PackStep(seq.Step{Note: 60, Gate: true, Velocity: 100, Ratchet: 1, Probability: 100})
	cfg.Patterns = []PatternBank{{}}
	cfg.Patterns[0].Slots[0] = pattern
	return cfg
}

// modalPlacementConfig places that pattern once, over the first bar, as an
// arrange block does.
func modalPlacementConfig(sustain float32) Config {
	cfg := modalPedalConfig(sustain)
	cfg.Schedule = []ScheduleEvent{
		{Kind: SchedulePattern, Track: 0, Index: 0, Tick: 0, EndTick: seq.TicksPerBar, ID: 1},
		{Kind: SchedulePatternEnd, Track: 0, Tick: seq.TicksPerBar, EndTick: seq.TicksPerBar, ID: 1},
	}
	return cfg
}

func newModalPedalEngine(t *testing.T, cfg Config, selectPattern bool) *Engine {
	t.Helper()
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if selectPattern {
		e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0})
	}
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	return e
}

func renderModalLeft(t *testing.T, e *Engine, frames int) []float32 {
	t.Helper()
	var left, right [128]float32
	out := make([]float32, 0, frames+128)
	for len(out) < frames {
		e.Render(left[:], right[:])
		out = append(out, left[:]...)
	}
	if e.faulted {
		t.Fatal("engine faulted")
	}
	return out
}

func modalMeanSquare(x []float32) float64 {
	var sum float64
	for _, v := range x {
		sum += float64(v) * float64(v)
	}
	return sum / float64(len(x))
}

// modalSpan is the mean square of the output from one time to another, in seconds.
func modalSpan(out []float32, from, to float64) float64 {
	return modalMeanSquare(out[int(from*48000):int(to*48000)])
}

func modalSetPedal(value float32, tick int64) cmd.Command {
	return cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamPianoSustain), Arg0: math.Float32bits(value), Tick: tick}
}

func TestModalPedalKeepsAGatedNoteSoundingAfterItsGate(t *testing.T) {
	// Energy 100 to 300 ms after the gate ends, once the damper has finished.
	after := func(out []float32) float64 {
		return modalMeanSquare(out[modalGateFrames+4800 : modalGateFrames+4800+9600])
	}
	pedal := renderModalLeft(t, newModalPedalEngine(t, modalPedalConfig(1), true), 48000)
	plain := renderModalLeft(t, newModalPedalEngine(t, modalPedalConfig(0), true), 48000)
	t.Logf("METRIC modal engine mean_square_100_to_300ms_after_gate pedal=%.3e damped=%.3e", after(pedal), after(plain))
	if after(plain) > 1e-9 {
		t.Fatalf("without the pedal the note kept %.3e after its gate", after(plain))
	}
	if after(pedal) < 1e-4 {
		t.Fatalf("with the pedal down the note kept only %.3e after its gate", after(pedal))
	}
}

func TestModalPedalAutomationLiftDampsTheNote(t *testing.T) {
	// A placement, as an arrange block schedules it, with the pedal down from
	// the first tick and lifted at one second.
	cfg := modalPlacementConfig(0)
	cfg.Automation = []cmd.Command{modalSetPedal(1, 0), modalSetPedal(0, 1920)}
	out := renderModalLeft(t, newModalPedalEngine(t, cfg, false), 3*48000)
	held, lifted := modalSpan(out, .2, .9), modalSpan(out, 1.1, 1.5)
	t.Logf("METRIC modal engine mean_square held_to_0.9s=%.3e after_lift_1.1_to_1.5s=%.3e", held, lifted)
	if held < 1e-4 {
		t.Fatalf("the note stopped sounding with the pedal down: %.3e", held)
	}
	if lifted > held*1e-6 {
		t.Fatalf("lifting the pedal left %.3e, from %.3e", lifted, held)
	}
	// The note still rings just before the lift, so its gate did not damp it.
	if modalSpan(out, .9, 1.0) < held*1e-3 {
		t.Fatal("the note was damped before the lift")
	}
}

func TestModalPedalSurvivesThePlacementEnd(t *testing.T) {
	// A placement ends with a key-up. With the pedal down the note must ring
	// through it exactly as it rings when the placement runs on. A four-bar
	// pattern with one note makes the long placement free of any key-up after
	// the gate's, so the two renders differ only by the end of the short one,
	// which falls at two seconds.
	placed := func(bars int64) Config {
		cfg := modalPedalConfig(1)
		cfg.Patterns[0].Slots[0].Len = 64
		end := bars * seq.TicksPerBar
		cfg.Schedule = []ScheduleEvent{
			{Kind: SchedulePattern, Track: 0, Index: 0, Tick: 0, EndTick: end, ID: 1},
			{Kind: SchedulePatternEnd, Track: 0, Tick: end, EndTick: end, ID: 1},
		}
		return cfg
	}
	short := renderModalLeft(t, newModalPedalEngine(t, placed(1), false), 4*48000)
	long := renderModalLeft(t, newModalPedalEngine(t, placed(4), false), 4*48000)
	// Without the pedal the 50 percent gate has damped the note long before
	// the placement ends, and a test of its end would pass on silence.
	if inside := modalSpan(short, 1.0, 1.9); inside < 1e-6 {
		t.Fatalf("the note is silent with the pedal down, %.3e one second in", inside)
	}
	for n := range short {
		if short[n] != long[n] {
			t.Fatalf("the placement end changed the note at frame %d (%.3f s): %g, want %g", n, float64(n)/48000, short[n], long[n])
		}
	}
}

func TestModalPedalFollowsLiveCommands(t *testing.T) {
	e, err := New(modalPedalConfig(0))
	if err != nil {
		t.Fatal(err)
	}
	var left, right [128]float32
	render := func(frames int) []float32 {
		out := make([]float32, 0, frames)
		for len(out) < frames {
			e.Render(left[:], right[:])
			out = append(out, left[:]...)
		}
		return out
	}
	e.Push(modalSetPedal(1, 0))
	e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 60 | 100<<8})
	render(2048)
	e.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0})
	render(8192)
	ringing := modalMeanSquare(render(8192))
	e.Push(modalSetPedal(0, 0))
	render(8192)
	lifted := modalMeanSquare(render(8192))
	if e.faulted {
		t.Fatal("pedal commands faulted the engine")
	}
	t.Logf("METRIC modal engine live mean_square pedal_down=%.3e after_lift=%.3e", ringing, lifted)
	if ringing < 1e-4 || lifted > ringing*1e-6 {
		t.Fatalf("live pedal: %.3e with the pedal down, %.3e after the lift", ringing, lifted)
	}
}

func TestModalPedalFollowsADirectorState(t *testing.T) {
	// A Game Director state is a scene: its pedal setting applies when the state
	// is entered, and the next state's setting lifts the pedal.
	cfg := modalPedalConfig(0)
	pedal := func(value float32) Scene {
		return Scene{Settings: []SceneSetting{{Track: 0, ID: kernel.ParamPianoSustain, Value: value}}}
	}
	cfg.Scenes = []Scene{pedal(1), pedal(0)}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var left, right [128]float32
	render := func(frames int) []float32 {
		out := make([]float32, 0, frames)
		for len(out) < frames {
			e.Render(left[:], right[:])
			out = append(out, left[:]...)
		}
		return out
	}
	state := func(scene uint32) {
		e.Push(cmd.Command{Op: cmd.OpSetState, Track: 0xff, Arg0: scene})
	}
	state(0)
	e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 60 | 100<<8})
	render(2048)
	e.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0})
	render(8192)
	ringing := modalMeanSquare(render(8192))
	state(1)
	render(8192)
	lifted := modalMeanSquare(render(8192))
	if e.faulted {
		t.Fatal("state commands faulted the engine")
	}
	t.Logf("METRIC modal engine director mean_square state_with_pedal=%.3e state_without=%.3e", ringing, lifted)
	if ringing < 1e-4 || lifted > ringing*1e-6 {
		t.Fatalf("director states: %.3e in the state that holds the pedal, %.3e in the next", ringing, lifted)
	}
}

func TestModalPedalRefusesAnOutOfRangeValue(t *testing.T) {
	e, err := New(modalPedalConfig(0))
	if err != nil {
		t.Fatal(err)
	}
	e.apply(modalSetPedal(1.5, 0))
	if !e.faulted {
		t.Fatal("a pedal above 1 was accepted")
	}
	cfg := modalPedalConfig(2)
	if _, err := New(cfg); err == nil {
		t.Fatal("a configured pedal above 1 was accepted")
	}
}

func TestModalSceneSeekRestoresSustain(t *testing.T) {
	cfg := modalPedalConfig(.25)
	cfg.Scenes = []Scene{{}, {Settings: []SceneSetting{{Track: 0, ID: kernel.ParamPianoSustain, Value: 1}}}}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// A key-up is silent while the second scene holds the pedal down, and
	// damps once a seek back to the first scene restores the authored pedal.
	rings := func() bool {
		e.apply(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 60 | 100<<8})
		var left, right [128]float32
		for range 16 {
			e.Render(left[:], right[:])
		}
		e.apply(cmd.Command{Op: cmd.OpNoteOff, Track: 0})
		for range 64 {
			e.Render(left[:], right[:])
		}
		var tail float64
		for range 8 {
			e.Render(left[:], right[:])
			tail += modalMeanSquare(left[:])
		}
		return tail > 1e-6
	}
	e.apply(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 1})
	if e.faulted || e.voices[0].pianoSustain != 1 || !rings() {
		t.Fatal("forward seek did not lift the dampers")
	}
	e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff})
	if e.faulted || e.voices[0].pianoSustain != .25 || rings() {
		t.Fatal("backward seek kept the later pedal")
	}
}

func TestModalResetKeepsTheAuthoredPedal(t *testing.T) {
	e, err := New(modalPedalConfig(1))
	if err != nil {
		t.Fatal(err)
	}
	e.Reset()
	e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 60 | 100<<8})
	var left, right [128]float32
	for range 16 {
		e.Render(left[:], right[:])
	}
	e.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0})
	for range 64 {
		e.Render(left[:], right[:])
	}
	var tail float64
	for range 8 {
		e.Render(left[:], right[:])
		tail += modalMeanSquare(left[:])
	}
	if e.faulted || tail < 1e-6 {
		t.Fatalf("reset dropped the authored pedal: %.3e after the key-up", tail)
	}
}
