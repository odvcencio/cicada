package project

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// TestEchoHighpassValues checks the effect block's highpass on a delay and a
// tape: a frequency from 20 Hz to 2 kHz, or off; anything else is refused with
// a message that names the range.
func TestEchoHighpassValues(t *testing.T) {
	for _, kind := range []string{"delay", "tape"} {
		for value, want := range map[string]string{
			"300Hz":  "",
			"2kHz":   "",
			"20Hz":   "",
			"off":    "",
			"10Hz":   "20",
			"2.5kHz": "2000",
			"300":    "Hz",
			"40ms":   "Hz",
		} {
			source := "cicada 2\ntempo 120\nfx echo " + kind + " { highpass = " + value + " }\ntrack a acid { send echo = 0.5 }\npattern p { c3 }\nscene main { a = p }\nsong { main }\n"
			score, ds := notation.Parse([]byte(source))
			if !hasErrors(ds) {
				_, ds = FromScore(score)
			}
			if want == "" {
				if hasErrors(ds) {
					t.Errorf("%s highpass = %s: %v", kind, value, ds)
				}
				continue
			}
			if !hasErrors(ds) || !strings.Contains(ds[0].Message, want) {
				t.Errorf("%s highpass = %s: want an error naming %q, got %v", kind, value, want, ds)
			}
		}
	}
}
