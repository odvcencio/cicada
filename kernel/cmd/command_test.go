package cmd

import (
	"math"
	"reflect"
	"testing"
)

func TestCommandWireLayout(t *testing.T) {
	command := Command{Op: OpSelectPattern, Track: 2, Index: 15, Arg0: 20, Tick: 0x0102030405060708}
	encoded, err := EncodeCommand(command, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := [CommandSize]byte{9, 2, 15, 0, 20, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 8, 7, 6, 5, 4, 3, 2, 1}
	if encoded != want {
		t.Fatalf("wire bytes: %x, want %x", encoded, want)
	}
	decoded, err := DecodeCommand(encoded[:], 3)
	if err != nil || decoded != command {
		t.Fatalf("round trip: %+v, %v", decoded, err)
	}
	if count := testing.AllocsPerRun(1000, func() {
		EncodeCommand(command, 3)
		DecodeCommand(encoded[:], 3)
	}); count != 0 {
		t.Fatalf("codec allocated %v times", count)
	}
}

func TestMessageWireLayout(t *testing.T) {
	message := Message{Kind: Switched, Track: 3, A: 0x1234, B: 0x01020304, Tick: 0x0102030405060708}
	encoded := EncodeMessage(message)
	want := [MessageSize]byte{5, 3, 0x34, 0x12, 4, 3, 2, 1, 8, 7, 6, 5, 4, 3, 2, 1}
	if encoded != want {
		t.Fatalf("message bytes: %x, want %x", encoded, want)
	}
	decoded, err := DecodeMessage(encoded[:])
	if err != nil || decoded != message {
		t.Fatalf("round trip: %+v, %v", decoded, err)
	}
}

func TestConductorMessageKindsRoundTrip(t *testing.T) {
	messages := []Message{
		{Kind: Bar, B: 16, Tick: 3840},
		{Kind: PhraseEnd, B: 2, Tick: 7680},
		{Kind: LayerChanged, A: 3, B: 0xffff, Tick: 3840},
		{Kind: MacroReached, Track: 15, Tick: 123},
	}
	for _, message := range messages {
		encoded := EncodeMessage(message)
		decoded, err := DecodeMessage(encoded[:])
		if err != nil || decoded != message {
			t.Fatalf("message round trip %+v: got %+v, %v", message, decoded, err)
		}
	}
	invalid := EncodeMessage(Message{Kind: MacroReached + 1})
	if _, err := DecodeMessage(invalid[:]); err == nil {
		t.Fatal("accepted unknown conductor message kind")
	}
}

func TestCommandRejectsMalformedRecords(t *testing.T) {
	valid, _ := EncodeCommand(Command{Op: OpPlay, Track: 255}, 1)
	for _, mutate := range []func(*[CommandSize]byte){
		func(b *[CommandSize]byte) { b[0] = 0 },
		func(b *[CommandSize]byte) { b[1] = 0 },
		func(b *[CommandSize]byte) { b[12] = 1 },
		func(b *[CommandSize]byte) { b[23] = 0x80 },
	} {
		invalid := valid
		mutate(&invalid)
		if _, err := DecodeCommand(invalid[:], 1); err == nil {
			t.Fatalf("accepted malformed %x", invalid)
		}
	}
	if _, err := DecodeCommand(valid[:23], 1); err == nil {
		t.Fatal("accepted partial record")
	}
	if _, err := EncodeCommand(Command{Op: OpSetParam, Track: 0, Arg0: math.Float32bits(float32(math.NaN()))}, 1); err == nil {
		t.Fatal("accepted nonfinite parameter")
	}
	if _, err := EncodeCommand(Command{Op: OpSelectPattern, Track: 0, Arg0: 4}, 1); err == nil {
		t.Fatal("accepted reserved quantize")
	}
	if _, err := EncodeCommand(Command{Op: OpSelectPattern, Track: 0, Arg1: 2}, 1); err == nil {
		t.Fatal("accepted invalid restart flag")
	}
	for _, chain := range []Command{
		{Op: OpSetChain, Track: 0, Index: 32, Arg0: 1 << 8},
		{Op: OpSetChain, Track: 0, Index: 0, Arg0: 0},
		{Op: OpSetChain, Track: 0, Index: 0, Arg0: 1 << 8, Arg1: 1},
	} {
		if _, err := EncodeCommand(chain, 1); err == nil {
			t.Fatalf("accepted invalid chain record: %+v", chain)
		}
	}
	invalidTie := uint32(1<<9 | 1<<10 | 1<<11 | 100<<14)
	if _, err := EncodeCommand(Command{Op: OpSetStep, Track: 0, Arg0: invalidTie}, 1); err == nil {
		t.Fatal("accepted ratcheted tie")
	}
}

func TestMacroCommandsRoundTripAndRejectInvalidPayloads(t *testing.T) {
	commands := []Command{
		{Op: OpDefineMacro, Track: 255, Index: 15, Arg0: math.Float32bits(0.25)},
		{Op: OpSetMacro, Track: 255, Index: 0, Arg0: math.Float32bits(1), Arg1: 4800},
	}
	for _, command := range commands {
		encoded, err := EncodeCommand(command, 1)
		if err != nil {
			t.Fatalf("encode %+v: %v", command, err)
		}
		decoded, err := DecodeCommand(encoded[:], 1)
		if err != nil || decoded != command {
			t.Fatalf("round trip %+v: got %+v, %v", command, decoded, err)
		}
	}

	invalid := []Command{
		{Op: OpDefineMacro, Track: 255, Index: 16, Arg0: math.Float32bits(0.5)},
		{Op: OpSetMacro, Track: 255, Index: 16, Arg0: math.Float32bits(0.5)},
		{Op: OpDefineMacro, Track: 255, Index: 0, Arg0: math.Float32bits(1.5)},
		{Op: OpSetMacro, Track: 255, Index: 0, Arg0: math.Float32bits(1.5)},
		{Op: OpDefineMacro, Track: 255, Index: 0, Arg0: math.Float32bits(float32(math.NaN()))},
		{Op: OpSetMacro, Track: 255, Index: 0, Arg0: math.Float32bits(float32(math.NaN()))},
		{Op: OpDefineMacro, Track: 255, Index: 0, Arg0: math.Float32bits(float32(math.Inf(1)))},
		{Op: OpSetMacro, Track: 255, Index: 0, Arg0: math.Float32bits(float32(math.Inf(-1)))},
		{Op: OpDefineMacro, Track: 0, Index: 0, Arg0: math.Float32bits(0.5)},
		{Op: OpSetMacro, Track: 0, Index: 0, Arg0: math.Float32bits(0.5)},
		{Op: OpDefineMacro, Track: 255, Index: 0, Arg0: math.Float32bits(0.5), Arg1: 1},
	}
	for _, command := range invalid {
		if _, err := EncodeCommand(command, 1); err == nil {
			t.Errorf("accepted invalid macro command: %+v", command)
		}
	}
}

func TestBatchDecodeIsAllOrNone(t *testing.T) {
	one, _ := EncodeCommand(Command{Op: OpPlay, Track: 255}, 1)
	two, _ := EncodeCommand(Command{Op: OpStop, Track: 255}, 1)
	data := append(one[:], two[:]...)
	dst := []Command{{Op: OpCue}, {Op: OpCue}}
	data[CommandSize+12] = 1
	if count, err := DecodeCommands(data, 1, dst); err == nil || count != 0 || !reflect.DeepEqual(dst, []Command{{Op: OpCue}, {Op: OpCue}}) {
		t.Fatalf("partial batch committed: count=%d err=%v dst=%+v", count, err, dst)
	}
	data[CommandSize+12] = 0
	if count, err := DecodeCommands(data, 1, dst); err != nil || count != 2 || dst[0].Op != OpPlay || dst[1].Op != OpStop {
		t.Fatalf("valid batch failed: count=%d err=%v dst=%+v", count, err, dst)
	}
	if count, err := DecodeCommands(data[:len(data)-1], 1, dst); err == nil || count != 0 {
		t.Fatal("accepted partial batch")
	}
}

func TestConductorCommandsValidate(t *testing.T) {
	tests := []struct {
		name   string
		tracks uint8
		cmd    Command
		valid  bool
	}{
		{"layers minimum threshold and default release", 4, Command{Op: OpSetLayers, Track: 255, Index: 0, Arg0: 0x00030201}, true},
		{"layers all thresholds and release max", 16, Command{Op: OpSetLayers, Track: 255, Index: 15, Arg0: 0xffc08040, Arg1: 16}, true},
		{"layers macro out of range", 4, Command{Op: OpSetLayers, Track: 255, Index: 16, Arg0: 0x00030201}, false},
		{"layers thresholds not ascending", 4, Command{Op: OpSetLayers, Track: 255, Arg0: 0x00030202}, false},
		{"layers release out of range", 4, Command{Op: OpSetLayers, Track: 255, Arg0: 0x00030201, Arg1: 17}, false},
		{"layers requires global track", 4, Command{Op: OpSetLayers, Track: 0, Arg0: 0x00030201}, false},
		{"masks fit track count", 4, Command{Op: OpSetLayerMasks, Track: 255, Index: 0, Arg0: 0x00030001, Arg1: 0x000f0004}, true},
		{"mask above track count in first pair", 3, Command{Op: OpSetLayerMasks, Track: 255, Index: 0, Arg0: 0x00080001}, false},
		{"mask above track count in second pair", 3, Command{Op: OpSetLayerMasks, Track: 255, Index: 0, Arg1: 0x00080001}, false},
		{"masks macro out of range", 4, Command{Op: OpSetLayerMasks, Track: 255, Index: 16}, false},
		{"phrase minimum", 1, Command{Op: OpSetPhraseBars, Track: 255, Arg0: 1}, true},
		{"phrase maximum", 1, Command{Op: OpSetPhraseBars, Track: 255, Arg0: 64}, true},
		{"phrase zero", 1, Command{Op: OpSetPhraseBars, Track: 255}, false},
		{"phrase above maximum", 1, Command{Op: OpSetPhraseBars, Track: 255, Arg0: 65}, false},
		{"phrase index must be zero", 1, Command{Op: OpSetPhraseBars, Track: 255, Index: 1, Arg0: 8}, false},
		{"phrase reserved payload", 1, Command{Op: OpSetPhraseBars, Track: 255, Arg0: 8, Arg1: 1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := EncodeCommand(tt.cmd, tt.tracks)
			if (err == nil) != tt.valid {
				t.Fatalf("EncodeCommand(%+v) error = %v, valid = %v", tt.cmd, err, tt.valid)
			}
		})
	}
}
