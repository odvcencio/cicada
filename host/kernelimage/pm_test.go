package kernelimage_test

import (
	"encoding/binary"
	"os"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestPMCapabilityImage(t *testing.T) {
	source, err := os.ReadFile("../../examples/fm-bell.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if score == nil || len(ds) != 0 {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", ds)
	}
	for _, rate := range []int{44_100, 48_000} {
		cfg, err := project.CompileEngine(p, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		for _, kit := range []bool{false, true} {
			if kit {
				cfg.Track[0].Kit = &[drum.LaneCount]engine.KitLaneBinding{}
				cfg.Track[0].Kit[0] = engine.KitLaneBinding{Kind: engine.KitLaneGraph, Program: cfg.Track[0].Graph}
				cfg.Track[0].Kind = engine.VoiceDrums
				cfg.Track[0].Graph = graph.Program{}
				cfg.Patterns[0].Drums = new([16][drum.LaneCount]seq.Pattern)
				for slot := range cfg.Patterns[0].Slots {
					cfg.Patterns[0].Slots[slot].Steps = [64]uint32{}
					for lane := range cfg.Patterns[0].Drums[slot] {
						cfg.Patterns[0].Drums[slot][lane] = cfg.Patterns[0].Slots[slot]
					}
				}
				cfg.Track[1].Graph = graph.Program{Len: 1, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Noise}}}
			}
			data, err := kernelimage.Encode(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if binary.LittleEndian.Uint16(data[4:6]) != 13 || binary.LittleEndian.Uint16(data[30:32]) != kernelimage.PMCapability {
				t.Fatal("PM image header changed")
			}
			decoded, err := kernelimage.Decode(data, rate, 128)
			if err != nil || !reflect.DeepEqual(cfg, decoded) {
				t.Fatalf("PM image roundtrip: %v", err)
			}
			if _, err := engine.New(decoded); err != nil {
				t.Fatal(err)
			}
			for _, capability := range []uint16{0, kernelimage.DelayCapability, 8} {
				binary.LittleEndian.PutUint16(data[30:32], capability)
				if _, err := kernelimage.Decode(data, rate, 128); err == nil {
					t.Fatalf("PM accepted with capability %#x (kit=%v)", capability, kit)
				}
			}
		}
	}
}
