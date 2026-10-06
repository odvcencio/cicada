package project

import (
	"m31labs.dev/cicada/notation"
	"reflect"
	"testing"
)

func TestCompressorPresetAllowsInstanceSidechain(t *testing.T) {
	score, ds := notation.Parse([]byte("cicada 2\npreset squeeze { instrument=builtin.comp threshold=-20dB }\nfx duck squeeze { sidechain=bass }\nbus music { insert=duck }\ntrack bass acid { out=music }\npattern melody { 1 . }\nscene main { bass=melody }\nsong { main }\n"))
	if score == nil || hasErrors(ds) {
		t.Fatalf("instance sidechain: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatalf("compile: %+v", ds)
	}
	if _, err := CompileEngine(p, 48000, 128); err != nil {
		t.Fatal(err)
	}
}

func TestDrumLanePresetPreservesLegacyRouting(t *testing.T) {
	song := "\npattern beat drums steps=4 { bd: X... }\nscene main { lead=beat }\nsong { main }\n"
	var compiled [2]*Project
	for i, source := range []string{
		"cicada 1\nfx delay {}\npreset kick { instrument=builtin.bd tune=52Hz }\ntrack lead kick { send_a=0.5 send_pre=true }",
		"cicada 1\nfx delay {}\ntrack lead drums { bd_tune=52Hz send_a=0.5 send_pre=true }",
	} {
		score, ds := notation.Parse([]byte(source + song))
		if score == nil || hasErrors(ds) {
			t.Fatalf("parse: %+v", ds)
		}
		compiled[i], ds = FromScore(score)
		if compiled[i] == nil || hasErrors(ds) {
			t.Fatalf("compile: %+v", ds)
		}
	}
	if !reflect.DeepEqual(compiled[0], compiled[1]) {
		t.Fatal("lane preset routing differs from inline drums")
	}
}
