package kernelimage_test

import (
	"encoding/binary"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestQualityInstrumentImagePreservesFiveInputsAndPolyBudget(t *testing.T) {
	patch, _ := instrument.FindPatch("poly-brass")
	declaration, err := patch.Source("brass")
	if err != nil {
		t.Fatal(err)
	}
	source := []byte("cicada 1\n" + declaration + "track keys brass {}\npattern p notes { c4 . e4 . }\nscene main { keys=p }\nsong { main }\n")
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
	if cfg.Track[0].Kind != engine.VoiceGraphPoly {
		t.Fatal("poly voice lowered to mono")
	}
	wire, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(wire, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, decoded) {
		t.Fatal("quality instrument lost graph inputs in the project image")
	}
	if _, err = engine.New(decoded); err != nil {
		t.Fatal(err)
	}
	decoded.MaxVoices = 7
	if _, err = engine.New(decoded); err == nil {
		t.Fatal("eight-voice graph exceeded the host budget silently")
	}
}

func TestVersion13MasterSoloRemainsDecodable(t *testing.T) {
	cfg := firstAcidConfig(t)
	cfg.MasterSolo = true
	wire, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Legacy graphs use the same eight-byte node representation; version 14
	// adds two argument indices only to the new ADSR operation.
	binary.LittleEndian.PutUint16(wire[4:6], 13)
	decoded, err := kernelimage.Decode(wire, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, decoded) {
		t.Fatal("version 13 changed existing bus/master state")
	}
}
