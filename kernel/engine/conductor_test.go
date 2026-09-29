package engine

import (
	"math"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

func conductorTestEngine(t *testing.T, tracks int, phraseBars uint32) *Engine {
	t.Helper()
	e, err := New(Config{SampleRate: 48_000, MaxBlock: 256, Tracks: tracks, MaxVoices: tracks, BPMMilli: 120_000})
	if err != nil {
		t.Fatal(err)
	}
	pushMacroCommand(t, e, cmd.Command{Op: cmd.OpDefineMacro, Track: 255, Index: 0})
	pushMacroCommand(t, e, cmd.Command{Op: cmd.OpSetLayers, Track: 255, Index: 0, Arg0: 0x00c08040})
	pushMacroCommand(t, e, cmd.Command{Op: cmd.OpSetLayerMasks, Track: 255, Index: 0, Arg0: 0x00020001, Arg1: 0x00030000})
	if phraseBars != 0 {
		pushMacroCommand(t, e, cmd.Command{Op: cmd.OpSetPhraseBars, Track: 255, Arg0: phraseBars})
	}
	pushMacroCommand(t, e, cmd.Command{Op: cmd.OpPlay, Track: 255})
	return e
}

func primeConductor(t *testing.T, e *Engine) []cmd.Message {
	t.Helper()
	if err := e.transport.SeekTick(0); err != nil {
		t.Fatal(err)
	}
	var left, right [1]float32
	e.Render(left[:], right[:])
	var messages []cmd.Message
	var message cmd.Message
	for e.Poll(&message) {
		messages = append(messages, message)
	}
	return messages
}

func conductorAt(t *testing.T, e *Engine, tick int64, value float32) (uint32, []cmd.Message) {
	t.Helper()
	pushMacroCommand(t, e, cmd.Command{Op: cmd.OpSetMacro, Track: 255, Index: 0, Arg0: math.Float32bits(value), Tick: tick})
	if err := e.transport.SeekTick(tick); err != nil {
		t.Fatal(err)
	}
	var left, right [1]float32
	e.Render(left[:], right[:])
	var messages []cmd.Message
	var message cmd.Message
	for e.Poll(&message) {
		messages = append(messages, message)
	}
	return e.layerMask, messages
}

func TestConductorAttackReleaseAndThresholdChatter(t *testing.T) {
	cases := []struct {
		name   string
		values []float32
		levels []uint16
	}{
		{"attack one level per bar", []float32{1, 1, 1}, []uint16{1, 2, 3}},
		{"release after three quiet bars", []float32{1, 1, 1, 0, 0, 0}, []uint16{1, 2, 3, 3, 3, 2}},
		{"threshold chatter", []float32{1, 1, 1, 0.6, 0.8, 0.6, 0.8, 0.6, 0.8}, []uint16{1, 2, 3, 3, 3, 3, 3, 3, 3}},
		{"immediate jump up", []float32{1, 1, 1}, []uint16{1, 2, 3}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			e := conductorTestEngine(t, 2, 0)
			primeConductor(t, e)
			level := uint16(0)
			for i, value := range tt.values {
				_, messages := conductorAt(t, e, int64(i+1)*seq.TicksPerBar, value)
				for _, message := range messages {
					if message.Kind == cmd.LayerChanged {
						level = message.A
					}
				}
				if level != tt.levels[i] {
					t.Fatalf("bar %d value %v: level=%d want %d", i+1, value, level, tt.levels[i])
				}
			}
		})
	}
}

func TestLayerMaskAppliesOnBarBoundaryOnly(t *testing.T) {
	e := conductorTestEngine(t, 2, 0)
	primeConductor(t, e)
	if e.layerMask != 1 {
		t.Fatalf("initial level-0 mask=%b, want 1", e.layerMask)
	}
	pushMacroCommand(t, e, cmd.Command{Op: cmd.OpSetMacro, Track: 255, Index: 0, Arg0: math.Float32bits(1), Tick: seq.TicksPerBar / 2})
	if err := e.transport.SeekTick(seq.TicksPerBar / 2); err != nil {
		t.Fatal(err)
	}
	var left, right [1]float32
	e.Render(left[:], right[:])
	if e.layerMask != 1 {
		t.Fatalf("mid-bar macro change changed mask to %b", e.layerMask)
	}
	if mask, _ := conductorAt(t, e, seq.TicksPerBar, 1); mask != 2 {
		t.Fatalf("bar boundary mask=%b, want 10", mask)
	}
}

func TestBarAndPhraseEndMessages(t *testing.T) {
	e := conductorTestEngine(t, 2, 2)
	initial := primeConductor(t, e)
	var bars, phrases []cmd.Message
	for _, message := range initial {
		if message.Kind == cmd.Bar {
			bars = append(bars, message)
		}
	}
	for boundary := int64(1); boundary <= 4; boundary++ {
		if err := e.transport.SeekTick(boundary * seq.TicksPerBar); err != nil {
			t.Fatal(err)
		}
		var left, right [1]float32
		e.Render(left[:], right[:])
		var message cmd.Message
		for e.Poll(&message) {
			switch message.Kind {
			case cmd.Bar:
				bars = append(bars, message)
			case cmd.PhraseEnd:
				phrases = append(phrases, message)
			}
		}
	}
	if len(bars) != 5 {
		t.Fatalf("got %d bar events, want 5: %+v initial=%+v", len(bars), bars, initial)
	}
	for i, message := range bars {
		if message.B != uint32(i+1) || message.Tick != int64(i)*seq.TicksPerBar {
			t.Fatalf("bar event %d = %+v", i, message)
		}
	}
	wantPhrases := []cmd.Message{
		{Kind: cmd.PhraseEnd, B: 1, Tick: 2 * seq.TicksPerBar},
		{Kind: cmd.PhraseEnd, B: 2, Tick: 4 * seq.TicksPerBar},
	}
	if !reflect.DeepEqual(phrases, wantPhrases) {
		t.Fatalf("phrase events = %+v, want %+v", phrases, wantPhrases)
	}
}

func TestMacroReachedMessage(t *testing.T) {
	e := conductorTestEngine(t, 2, 0)
	primeConductor(t, e)
	pushMacroCommand(t, e, cmd.Command{Op: cmd.OpSetMacro, Track: 255, Index: 0, Arg0: math.Float32bits(1), Arg1: 3, Tick: e.transport.Tick()})
	var left, right [1]float32
	for i := 0; i < 3; i++ {
		e.Render(left[:], right[:])
		var message cmd.Message
		found := false
		for e.Poll(&message) {
			if message.Kind == cmd.MacroReached {
				found = true
				if message.Track != 0 {
					t.Fatalf("MacroReached track=%d, want macro 0", message.Track)
				}
			}
		}
		if found != (i == 2) {
			t.Fatalf("frame %d MacroReached=%v", i+1, found)
		}
	}
	e.Render(left[:], right[:])
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.MacroReached {
			t.Fatal("MacroReached emitted more than once")
		}
	}
}

func TestConductorIsDeterministic(t *testing.T) {
	run := func(t *testing.T, blockSize int) []cmd.Message {
		t.Helper()
		e := conductorTestEngine(t, 2, 0)
		for i, value := range []float32{1, 0, 1, 0} {
			pushMacroCommand(t, e, cmd.Command{Op: cmd.OpSetMacro, Track: 255, Index: 0, Arg0: math.Float32bits(value), Tick: int64(i) * seq.TicksPerBar})
		}
		var events []cmd.Message
		var left, right [256]float32
		for rendered := 0; rendered < 4*96_000; rendered += blockSize {
			e.Render(left[:blockSize], right[:blockSize])
			var message cmd.Message
			for e.Poll(&message) {
				if message.Kind == cmd.LayerChanged {
					events = append(events, message)
				}
			}
		}
		return events
	}
	large := run(t, 256)
	small := run(t, 64)
	if !reflect.DeepEqual(large, small) {
		t.Fatalf("layer sequences differ by block size: 256=%+v 64=%+v", large, small)
	}
}

// A host that never uses the live-control ops and never drains messages (an offline
// render, for example) must not see its message ring fill with Bar events and fault.
func TestBarEventsStayOffUntilLiveControlIsUsed(t *testing.T) {
	e, err := New(Config{SampleRate: 48_000, MaxBlock: 256, Tracks: 1, MaxVoices: 1, BPMMilli: 240_000})
	if err != nil {
		t.Fatal(err)
	}
	pushMacroCommand(t, e, cmd.Command{Op: cmd.OpPlay, Track: 255})
	left, right := make([]float32, 256), make([]float32, 256)
	// 240 BPM: one bar is 0.25 s, so 64 bars is 16 s of frames, none of them drained.
	for frame := 0; frame < 48_000*16; frame += 256 {
		e.Render(left, right)
	}
	if e.faulted {
		t.Fatal("the engine faulted because unread Bar messages filled the ring")
	}
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.Bar || message.Kind == cmd.PhraseEnd || message.Kind == cmd.MacroReached {
			t.Fatalf("live-control message %d was emitted without a live-control op", message.Kind)
		}
	}
}
