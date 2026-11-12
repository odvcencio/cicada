package render

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// Peak normalization is skipped for a range whose output peak is below one
// 24-bit step (2^-23): the WAV is written at gain 1, byte for byte the plain
// render, and Report.NormalizeSkipped says why and names the range. That is
// how a silent range (rests, or a silent track's stem) always rendered. A
// range that holds only the end of a decaying tail is skipped too: before the
// flush of subnormal state its peak was about 2e-44 and the gain overflowed
// ("invalid WAV output gain"); with the flush the peak is exactly zero. A
// range above the floor still normalizes, however quiet: the string
// machine's tail from bar 5 peaks at about 1.7e-6 and is raised to -1 dBFS.
func TestNormalizeSkipsASilentRange(t *testing.T) {
	score := func(voice, notes string) *notation.Score {
		t.Helper()
		source := fmt.Sprintf("cicada 2\ntempo 240\nkey c major\nseed 1\ntrack t %s {}\npattern p notes {\n  step = 1/1\n  gate = 100%%\n  %s\n}\nscene a { t = p }\nsong { a*24 }\n", voice, notes)
		parsed, diagnostics := notation.Parse([]byte(source))
		for _, d := range diagnostics {
			if d.Severity == "error" {
				t.Fatal(d)
			}
		}
		return parsed
	}
	chord := "[c4 e4 g4 b4] -" + strings.Repeat(" .", 22)
	rests := strings.TrimPrefix(strings.Repeat(" .", 24), " ")
	for _, c := range []struct {
		name, voice, notes string
		from               int // zero-based
		skipped            bool
	}{
		{"tine_ep chord", "tine_ep", chord, 0, false},
		{"tine_ep tail from bar 5", "tine_ep", chord, 4, true},
		{"tine_ep tail from bar 21", "tine_ep", chord, 20, true},
		{"string_machine tail from bar 5", "string_machine", chord, 4, false},
		{"rests only", "tine_ep", rests, 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var normalized, plain bytes.Buffer
			report, err := WAV(score(c.voice, c.notes), Options{SampleRate: 48000, Bits: 24, From: c.from, Normalize: true}, &normalized)
			if err != nil {
				t.Fatal(err)
			}
			if !c.skipped {
				if report.NormalizeSkipped != "" {
					t.Fatalf("normalization skipped: %s", report.NormalizeSkipped)
				}
				if report.OutputPeak < .89 || report.OutputPeak > .9 || report.NormalizeGain <= 1 {
					t.Fatalf("normalized peak %g with gain %g, want 10^(-1/20)", report.OutputPeak, report.NormalizeGain)
				}
				return
			}
			if want := fmt.Sprintf("bars %d to 24", c.from+1); !strings.Contains(report.NormalizeSkipped, want) {
				t.Fatalf("NormalizeSkipped %q does not name the range (%q)", report.NormalizeSkipped, want)
			}
			if report.NormalizeGain != 1 {
				t.Fatalf("gain %g, want 1", report.NormalizeGain)
			}
			if _, err := WAV(score(c.voice, c.notes), Options{SampleRate: 48000, Bits: 24, From: c.from}, &plain); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(normalized.Bytes(), plain.Bytes()) {
				t.Fatal("the skipped normalization did not write the plain render's bytes")
			}
		})
	}
}
