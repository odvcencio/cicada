package cmd

import (
	"math"
	"testing"
)

func TestSpatialWireAndValidation(t *testing.T) {
	commands := []Command{TrackPosition(0, .123, -.456, .789, 960), TrackStereo(0, 0), ListenerPosition(1, 2, 3, 0), ListenerRotation(.4, -.3, .2, 0)}
	for _, command := range commands {
		data, err := EncodeCommand(command, 1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeCommand(data[:], 1)
		if err != nil || got != command {
			t.Fatalf("spatial wire changed: %+v, %v", got, err)
		}
		for word := range 3 {
			for _, value := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1)), 10001, -10001} {
				bad := command
				switch word {
				case 0:
					bad.Arg0 = math.Float32bits(value)
				case 1:
					bad.Arg1 = math.Float32bits(value)
				case 2:
					bad.Pad = math.Float32bits(value)
				}
				if bad.Validate(1) == nil {
					t.Fatalf("accepted spatial value %g", value)
				}
			}
		}
	}
	for _, bad := range []Command{
		TrackPosition(255, 1, 0, 0, 0), ListenerRotation(7, 0, 0, 0), TrackPosition(0, 1, 2, 3, -1),
		{Op: OpSetListenerPosition, Track: 0}, {Op: OpSetListenerRotation, Track: 255, Index: 1},
		{Op: OpSetTrackPosition, Track: 0, Index: 2}, {Op: OpSetTrackPosition, Track: 0, Pad: 1},
	} {
		if bad.Validate(1) == nil {
			t.Fatalf("accepted malformed spatial command %+v", bad)
		}
	}
	if allocations := testing.AllocsPerRun(100, func() { EncodeCommand(commands[0], 1) }); allocations != 0 {
		t.Fatalf("spatial ABI allocations: %g", allocations)
	}
}
