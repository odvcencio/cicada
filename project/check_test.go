package project

import (
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestCheckRejectsBadInstrumentOverride(t *testing.T) {
	src := []byte(`cicada 1
instrument tone { param cutoff: hz = 200hz; voice mono { out = sine(cutoff); } }
track lead tone { cutoff = 50ms }
pattern p notes steps=1 { 1 }
scene main { lead=p }
song { main }
`)
	score, parseDiagnostics := notation.Parse(src)
	if len(parseDiagnostics) != 0 {
		t.Fatalf("parse diagnostics: %+v", parseDiagnostics)
	}
	_, diagnostics := Check(score)
	if len(diagnostics) != 1 || diagnostics[0].Code != "CICADA-UNIT" {
		t.Fatalf("want unit error, got %+v", diagnostics)
	}
}

func TestCheckRejectsUnrenderableStep(t *testing.T) {
	src := []byte(`cicada 1
track bass acid {}
pattern p acid steps=1 { 1*9 }
scene main { bass=p }
song { main }
`)
	score, parseDiagnostics := notation.Parse(src)
	if len(parseDiagnostics) != 0 {
		t.Fatalf("parse diagnostics: %+v", parseDiagnostics)
	}
	_, diagnostics := Check(score)
	if len(diagnostics) != 1 || diagnostics[0].Code != "CICADA-PARAM" {
		t.Fatalf("want ratchet compile error, got %+v", diagnostics)
	}
}
