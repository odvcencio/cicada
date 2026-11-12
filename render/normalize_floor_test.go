package render

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// Peak normalization refuses a range whose output peak is below one 24-bit
// step (2^-23) with ErrSilentRange and names the range. Such a range holds
// only the end of a decaying tail: before the flush of subnormal state its
// peak was about 2e-44 and the gain overflowed ("invalid WAV output gain");
// with the flush the peak is exactly zero. A range above the floor still
// normalizes, however quiet: the string machine's tail from bar 5 peaks at
// about 1.7e-6 and is raised to -1 dBFS.
func TestNormalizeRefusesASilentRange(t *testing.T) {
	score := func(voice string) *notation.Score {
		t.Helper()
		source := fmt.Sprintf("cicada 2\ntempo 240\nkey c major\nseed 1\ntrack t %s {}\npattern p notes {\n  step = 1/1\n  gate = 100%%\n  [c4 e4 g4 b4] -%s\n}\nscene a { t = p }\nsong { a*24 }\n", voice, strings.Repeat(" .", 22))
		parsed, diagnostics := notation.Parse([]byte(source))
		for _, d := range diagnostics {
			if d.Severity == "error" {
				t.Fatal(d)
			}
		}
		return parsed
	}
	for _, c := range []struct {
		voice  string
		from   int // zero-based
		silent bool
	}{
		{"tine_ep", 0, false},
		{"tine_ep", 4, true},
		{"tine_ep", 20, true},
		{"string_machine", 4, false},
	} {
		t.Run(fmt.Sprintf("%s from bar %d", c.voice, c.from+1), func(t *testing.T) {
			var out bytes.Buffer
			report, err := WAV(score(c.voice), Options{SampleRate: 48000, Bits: 24, From: c.from, Normalize: true}, &out)
			if !c.silent {
				if err != nil {
					t.Fatal(err)
				}
				if report.OutputPeak < .89 || report.OutputPeak > .9 {
					t.Fatalf("normalized peak %g, want 10^(-1/20)", report.OutputPeak)
				}
				return
			}
			if !errors.Is(err, ErrSilentRange) {
				t.Fatalf("got %v, want ErrSilentRange", err)
			}
			if want := fmt.Sprintf("bars %d to 24", c.from+1); !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not name the range (%q)", err, want)
			}
			// Without normalization the same range renders.
			if _, err := WAV(score(c.voice), Options{SampleRate: 48000, Bits: 24, From: c.from}, io.Discard); err != nil {
				t.Fatal(err)
			}
		})
	}
}
