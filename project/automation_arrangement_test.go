package project

import (
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

const arrangementAutomationScore = `cicada 2
track bass acid {}
pattern pulse { 1 . 5 . }
arrange {
  place later bass pulse { at = @5.1.1 length = 4bars }
  place earlier bass pulse { at = @1.1.1 length = 4bars }
  marker beyond-audio { at = @17.1.1 }
}
`

func TestArrangementAutomationUsesPlacementExtent(t *testing.T) {
	for _, tc := range []struct {
		name, arrangement, point string
		wantPositionError        bool
	}{
		{"inside", arrangementAutomationScore, "@5.1.1", false},
		{"exclusive playback end", arrangementAutomationScore, "@9.1.1", false},
		{"beyond last placement", arrangementAutomationScore, "@9.1.2", true},
		{"marker does not extend duration", arrangementAutomationScore, "@17.1.1", true},
		{"partial bar end", "cicada 2\ntrack bass acid {}\npattern pulse { 1 }\narrange { place short bass pulse { at = @1.1.1 length = 1.125bars } }\n", "@2.1.3", false},
		{"beyond partial bar", "cicada 2\ntrack bass acid {}\npattern pulse { 1 }\narrange { place short bass pulse { at = @1.1.1 length = 1.125bars } }\n", "@2.1.4", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.arrangement + fmt.Sprintf("automate bass.cutoff { @1.1.1 400Hz %s 1200Hz smooth }\n", tc.point)
			score, ds := notation.Parse([]byte(source))
			if hasErrors(ds) {
				t.Fatal(ds)
			}
			p, ds := FromScore(score)
			if tc.wantPositionError {
				for _, d := range ds {
					if d.Code == "CICADA-POSITION" {
						return
					}
				}
				t.Fatalf("expected arrangement boundary diagnostic, got %v", ds)
			}
			if p == nil || hasErrors(ds) {
				t.Fatalf("valid arrangement automation rejected: %v", ds)
			}
		})
	}
}

func TestArrangementAutomationCompilesAndRoundTrips(t *testing.T) {
	score, ds := notation.Parse([]byte(arrangementAutomationScore + "automate bass.cutoff { @1.1.1 400Hz @5.1.1 1200Hz smooth @9.1.1 800Hz step }\n"))
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	data, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(data)
	if err != nil || !SemanticEqual(p, decoded) {
		t.Fatalf("JSON round trip: %v", err)
	}
	source, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	score, ds = notation.Parse(source)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	restored, ds := FromScore(score)
	if restored == nil || hasErrors(ds) || !SemanticEqual(p, restored) {
		t.Fatalf("source round trip: %v", ds)
	}
	controls, err := CompileAutomation(restored)
	if err != nil || len(controls) == 0 || controls[len(controls)-1].Tick != 8*3840 {
		t.Fatalf("automation did not compile through the arrangement end: %v", err)
	}
	if _, err := CompileSchedule(restored); err != nil {
		t.Fatal(err)
	}
	// Placement-based duration must not relax the existing sixteenth grid.
	restored.Automation[0].Points[1].Tick++
	if err := ValidateProject(restored); err == nil || !strings.Contains(err.Error(), "CICADA-POSITION") {
		t.Fatalf("off-grid point accepted: %v", err)
	}
}

func TestArrangementAutomationRetainsParameterRestrictions(t *testing.T) {
	for _, tc := range []struct{ source, code string }{
		{"master { level = -3dB }\nautomate master.level { @1.1.1 -3dB @5.1.1 -6dB }", "CICADA-UNSUPPORTED"},
		{"automate bass.cutoff { @1.1.1 400Hz @5.1.1 200ms }", "CICADA-UNIT"},
		{"automate bass.cutoff { @1.1.1 400Hz @5.1.1 100000Hz }", "CICADA-UNIT"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			score, ds := notation.Parse([]byte(arrangementAutomationScore + tc.source))
			if !hasErrors(ds) {
				_, ds = FromScore(score)
			}
			for _, d := range ds {
				if d.Code == tc.code {
					return
				}
			}
			t.Fatalf("expected %s, got %v", tc.code, ds)
		})
	}
}
