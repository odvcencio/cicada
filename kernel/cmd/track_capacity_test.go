//go:build !tinygo && !js

package cmd

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
)

func TestNativeCommandsRouteBeyondPortableTracks(t *testing.T) {
	for _, command := range []Command{
		{Op: OpNoteOn, Track: 63, Arg0: 60 | 127<<8},
		{Op: OpSetParam, Track: 63, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(.5)},
		{Op: OpSetLayerMask, Track: 255, Arg1: 1 << 31},
		{Op: OpSetLayerMasks, Track: 255, Index: 3<<4 | 15, Arg0: 1 << 31, Arg1: 1 << 31},
	} {
		var bytes [CommandSize]byte
		encoded, err := EncodeCommand(command, 64)
		if err != nil {
			t.Fatalf("native command %+v: %v", command, err)
		}
		copy(bytes[:], encoded[:])
		got, err := DecodeCommand(bytes[:], 64)
		if err != nil || got != command {
			t.Fatalf("native command roundtrip %+v: %+v %v", command, got, err)
		}
	}
	if err := (Command{Op: OpNoteOn, Track: 64}).Validate(64); err == nil {
		t.Fatal("out-of-range track accepted")
	}
	if err := (Command{Op: OpSetLayerMasks, Track: 255, Index: 3 << 4, Arg0: 1 << 31}).Validate(63); err == nil {
		t.Fatal("mask references track63 in63-track project")
	}
}
