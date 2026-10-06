package kernelimage_test

import (
	"encoding/binary"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"os"
	"reflect"
	"testing"
)

func TestDDSPCapabilityImageAndAllocationFree(t *testing.T) {
	source, err := os.ReadFile("../../examples/neural-reed.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(data[30:32]) != kernelimage.DDSPCapability {
		t.Fatal("missing DDSP capability")
	}
	decoded, err := kernelimage.Decode(data, 48000, 128)
	if err != nil || !reflect.DeepEqual(cfg, decoded) {
		t.Fatalf("roundtrip: %v", err)
	}
	e, err := engine.New(decoded)
	if err != nil {
		t.Fatal(err)
	}
	var left, right [128]float32
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var sounded bool
	for range 100 {
		e.Render(left[:], right[:])
		for _, x := range left {
			sounded = sounded || x != 0
		}
		var msg cmd.Message
		for e.Poll(&msg) {
		}
	}
	if !sounded {
		t.Fatal("silent demo")
	}
	if n := testing.AllocsPerRun(100, func() {
		e.Render(left[:], right[:])
		var msg cmd.Message
		for e.Poll(&msg) {
		}
	}); n != 0 {
		t.Fatalf("engine render allocated=%g", n)
	}
	data[30] = 0
	if _, err := kernelimage.Decode(data, 48000, 128); err == nil {
		t.Fatal("DDSP image accepted without capability")
	}
}
