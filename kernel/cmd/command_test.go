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
	invalidTie := uint32(1<<9 | 1<<10 | 1<<11 | 100<<14)
	if _, err := EncodeCommand(Command{Op: OpSetStep, Track: 0, Arg0: invalidTie}, 1); err == nil {
		t.Fatal("accepted ratcheted tie")
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
