package render

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// The piano, the electric piano, the poly keys, the string machine and the
// reverb sweep their state for subnormal numbers on a fixed cadence of their
// own samples. The output must therefore not depend on the render block size,
// on either renderer, in 32-bit float output where a flush can show. In these
// renders the sweeps of the electric piano, poly keys and string machine
// change the 32-bit output, so a sweep that followed the block size would fail
// here. The piano's and the reverb's sweeps change no output sample in them
// (the piano's removes a tenfold slowdown; the reverb's works in float64), so
// for those two the test shows only that the output stays the same.
func TestSubnormalSweepIsIndependentOfBlockSize(t *testing.T) {
	type variant struct {
		name, voice, fx, props string
		scheduled              int // 1 scheduled, 0 direct, -1 either
		held, bars             int
	}
	var variants []variant
	for _, voice := range []string{"piano", "tine_ep", "poly_keys", "string_machine"} {
		// A held piano chord reaches subnormal numbers after about 10 s.
		held, bars := 6, 12
		if voice == "piano" {
			held, bars = 14, 18
		}
		variants = append(variants,
			variant{voice + " direct", voice, "", "", 0, held, bars},
			// A serial EQ insert puts the score on the scheduled engine.
			variant{voice + " scheduled", voice, "fx tone eq { type = highpass frequency = 30Hz }\n", "insert = tone", 1, held, bars})
	}
	variants = append(variants, variant{"tine_ep into a reverb", "tine_ep", "fx room reverb { decay = 0.3s }\n", "send room = 0.5", -1, 6, 12})
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			// A chord held, then released, at one bar per second.
			steps := "[c4 e4 g4 b4]" + strings.Repeat(" -", v.held-1) + strings.Repeat(" .", v.bars-v.held)
			source := fmt.Sprintf("cicada 2\ntempo 240\nkey c major\nseed 1\n%strack t %s { %s }\npattern p notes {\n  step = 1/1\n  gate = 100%%\n  %s\n}\nscene a { t = p }\nsong { a*%d }\n", v.fx, v.voice, v.props, steps, v.bars)
			score, diagnostics := notation.Parse([]byte(source))
			for _, d := range diagnostics {
				if d.Severity == "error" {
					t.Fatal(d)
				}
			}
			if got := UsesScheduledEngine(score, Options{}); v.scheduled >= 0 && got != (v.scheduled == 1) {
				t.Fatalf("UsesScheduledEngine = %v, want %v", got, !got)
			}
			var first []byte
			for _, block := range []int{4096, 1000, 128} {
				var out bytes.Buffer
				if _, err := WAV(score, Options{SampleRate: 48000, Bits: 32, Block: block, TailSec: 1}, &out); err != nil {
					t.Fatal(err)
				}
				if first == nil {
					first = out.Bytes()
				} else if !bytes.Equal(out.Bytes(), first) {
					t.Fatalf("block %d writes different audio from block 4096", block)
				}
			}
		})
	}
}
