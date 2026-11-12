package project

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// MasterExportGainPoint is the rule both renderers follow for where an
// export's loudness gain enters the master stage: ahead of the last limiter,
// after the inserts before it; after a chain without a limiter; and at the
// master input when nothing in the chain runs ahead of it.
func TestMasterExportGainPointForEachChain(t *testing.T) {
	declarations := map[string]string{
		"tone":    "fx tone eq { type = highpass frequency = 25Hz }",
		"glue":    "fx glue comp { threshold = -18dB }",
		"stereo":  "fx stereo width { amount = 1.1 }",
		"guard":   "fx guard limiter { ceiling = -6dBTP }",
		"ceiling": "fx ceiling limiter { ceiling = -1dBTP }",
	}
	for _, tc := range []struct {
		name    string
		master  string // the master block, or "" for none
		inserts []string
		want    ExportGainPoint
	}{
		{"no master block", "", nil, GainAtMasterInput},
		{"master level only", "master { level = 3dB }", nil, GainAtMasterInput},
		{"the limiter alone", "", []string{"ceiling"}, GainAtMasterInput},
		{"the last limiter first", "", []string{"ceiling", "stereo"}, GainAtMasterInput},
		{"inserts ahead of the last limiter", "", []string{"tone", "glue", "ceiling"}, GainBeforeLastLimiter},
		{"inserts on both sides of the last limiter", "", []string{"tone", "ceiling", "stereo"}, GainBeforeLastLimiter},
		{"a limiter ahead of the last one", "", []string{"guard", "tone", "ceiling"}, GainBeforeLastLimiter},
		{"no limiter", "", []string{"tone", "glue", "stereo"}, GainAfterChain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "cicada 2\ntempo 120\ntrack a acid {}\npattern p { 1 5 1 3 }\nscene s { a = p }\nsong { s }\n"
			master := tc.master
			if len(tc.inserts) > 0 {
				for _, name := range tc.inserts {
					source += declarations[name] + "\n"
				}
				master = "master { insert = " + strings.Join(tc.inserts, " -> ") + " }"
			}
			score, diagnostics := notation.Parse([]byte(source + master + "\n"))
			for _, d := range diagnostics {
				if d.Severity == "error" {
					t.Fatal(d)
				}
			}
			p, diagnostics := FromScore(score)
			for _, d := range diagnostics {
				if d.Severity == "error" {
					t.Fatal(d)
				}
			}
			if got := MasterExportGainPoint(p); got != tc.want {
				t.Fatalf("gain point %d, want %d", got, tc.want)
			}
		})
	}
}
