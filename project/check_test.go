package project

import (
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestRejectsPatternWithTrackDependentPitch(t *testing.T) {
	source := []byte(`cicada 1
tempo 120
key c minor
seed 1
track low acid { octave = 2 }
track high acid { octave = 3 }
pattern shared acid steps = 4 { 1 . 3 . }
scene main { low = shared high = shared }
song { main }
`)
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	_, diagnostics = Check(score)
	found := false
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "CICADA-USE" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected track-dependent pattern error, got %+v", diagnostics)
	}
}

func TestSourceAndProjectAgreeAtVoiceCeiling(t *testing.T) {
	for _, acidTracks := range []int{2, 3} {
		var source strings.Builder
		source.WriteString("cicada 1\n")
		for i := 0; i < 5; i++ {
			fmt.Fprintf(&source, "track d%d drums {}\n", i)
		}
		for i := 0; i < acidTracks; i++ {
			fmt.Fprintf(&source, "track a%d acid {}\n", i)
		}
		source.WriteString("pattern beat drums steps=1 { bd: x; sd: x; ch: x; oh: x; cp: x; rs: x; }\n")
		source.WriteString("pattern note acid steps=1 { 1 }\nscene all {")
		for i := 0; i < 5; i++ {
			fmt.Fprintf(&source, " d%d=beat", i)
		}
		for i := 0; i < acidTracks; i++ {
			fmt.Fprintf(&source, " a%d=note", i)
		}
		source.WriteString(" }\nsong { all }\n")
		score, diagnostics := notation.Parse([]byte(source.String()))
		if len(diagnostics) != 0 {
			t.Fatalf("%d acid tracks parse: %+v", acidTracks, diagnostics)
		}
		p, diagnostics := FromScore(score)
		if acidTracks == 3 {
			if p != nil || len(diagnostics) != 1 || diagnostics[0].Code != "CICADA-LIMIT" || diagnostics[0].Position.Line == 0 {
				t.Fatalf("33 voices were not rejected at the song entry: %+v", diagnostics)
			}
			continue
		}
		if p == nil || len(diagnostics) != 0 {
			t.Fatalf("32 voices were rejected: %+v", diagnostics)
		}
		if _, err := CompileEngine(p, 48_000, 128); err != nil {
			t.Fatalf("32-voice project could not load: %v", err)
		}
	}
}

func TestCheckRejectsBadInstrumentOverride(t *testing.T) {
	src := []byte(`cicada 1
instrument tone { param cutoff: hz = 200hz; voice mono { out = sine(cutoff); } }
track lead tone { level = -3db cutoff = 50ms }
pattern p notes steps=1 { 1 }
scene main { lead=p }
song { main }
`)
	score, parseDiagnostics := notation.Parse(src)
	if len(parseDiagnostics) != 0 {
		t.Fatalf("parse diagnostics: %+v", parseDiagnostics)
	}
	_, diagnostics := Check(score)
	if len(diagnostics) != 1 || diagnostics[0].Code != "CICADA-UNIT" || diagnostics[0].Position != (notation.Position{Line: 3, Column: 41}) {
		t.Fatalf("want unit error, got %+v", diagnostics)
	}
}

func TestCheckPointsToInvalidTrackParameterValue(t *testing.T) {
	for _, tc := range []struct {
		name, track string
		column      int
	}{
		{"acid", "track bass acid {\n  cutoff = 50ms\n}", 12},
		{"drum", "track bass drums {\n  bd_tune = 2ms\n}", 13},
		{"mixer", "track bass acid {\n  pan = 2\n}", 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patternKind := "acid"
			pattern := "1"
			if tc.name == "drum" {
				patternKind, pattern = "drums", "bd: x;"
			}
			source := "cicada 1\n" + tc.track + "\npattern p " + patternKind + " steps=1 { " + pattern + " }\nscene main { bass=p }\nsong { main }\n"
			score, parseDiagnostics := notation.Parse([]byte(source))
			if len(parseDiagnostics) != 0 {
				t.Fatalf("parse: %+v", parseDiagnostics)
			}
			_, diagnostics := Check(score)
			if len(diagnostics) != 1 || diagnostics[0].Code != "CICADA-PARAM" || diagnostics[0].Position != (notation.Position{Line: 3, Column: tc.column}) {
				t.Fatalf("want parameter value at 3:%d, got %+v", tc.column, diagnostics)
			}
		})
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

func TestCheckRejectsUnrenderableUnusedPattern(t *testing.T) {
	src := []byte(`cicada 1
track bass acid {}
pattern used acid steps=1 { 1 }
pattern unused acid steps=1 { 1*9 }
scene main { bass=used }
song { main }
`)
	score, parseDiagnostics := notation.Parse(src)
	if len(parseDiagnostics) != 0 {
		t.Fatalf("parse diagnostics: %+v", parseDiagnostics)
	}
	_, diagnostics := Check(score)
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "CICADA-PARAM" && diagnostic.Position.Line == 4 && diagnostic.Position.Column == 31 {
			return
		}
	}
	t.Fatalf("unused invalid pattern was accepted: %+v", diagnostics)
}
