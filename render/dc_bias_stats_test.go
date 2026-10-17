package render

import (
	"fmt"
	"io"
	"math"
	"testing"

	"m31labs.dev/cicada/notation"
)

// The export DC correction lands after the master chain, just before the
// limiter. Both renderers must count it in the pre-limiter peak and overs.
func TestMasterBiasCountsInPreLimiterStatistics(t *testing.T) {
	for _, chain := range []bool{false, true} {
		t.Run(fmt.Sprintf("chain=%v", chain), func(t *testing.T) {
			masterInsert := ""
			if chain {
				masterInsert = "insert = flat"
			}
			stats := func(scheduled bool, bias float32) Report {
				trackInsert := ""
				if scheduled {
					trackInsert = "insert = flat"
				}
				source := fmt.Sprintf(`cicada 2
tempo 138
key a minor
seed 4242
instrument dcsat {
  voice mono {
    let osc = pulse(pitch, 0.9)
    let shape = env(gate, 380ms)
    out = tanh(osc * 4) * shape * 0.3
  }
}
fx flat eq { type = peak frequency = 1000Hz q = 1 gain = 0dB }
track lead dcsat { %s }
pattern lead-a { 5 . . . 3 . . . | 1' . . . 7 . . . }
scene main { lead = lead-a }
song { main*2 }
master { level = 6dB %s }
`, trackInsert, masterInsert)
				score, ds := notation.Parse([]byte(source))
				if score == nil {
					t.Fatalf("parse: %+v", ds)
				}
				if UsesScheduledEngine(score, Options{}) != scheduled {
					t.Fatalf("scheduled = %v not honored", scheduled)
				}
				report, err := WAV(score, Options{SampleRate: 48000, Bits: 24, Bars: 2, TailSec: 0.5, MasterBiasL: bias, MasterBiasR: bias}, io.Discard)
				if err != nil {
					t.Fatal(err)
				}
				return report
			}
			const bias = 1.5
			direct0, direct1 := stats(false, 0), stats(false, bias)
			sched0, sched1 := stats(true, 0), stats(true, bias)
			t.Logf("peak direct %.4f->%.4f scheduled %.4f->%.4f; overs direct %d scheduled %d", direct0.Peak, direct1.Peak, sched0.Peak, sched1.Peak, direct1.PreLimiterOvers, sched1.PreLimiterOvers)
			for name, pair := range map[string][2]Report{"direct": {direct0, direct1}, "scheduled": {sched0, sched1}} {
				if pair[1].Peak < pair[0].Peak+bias/2 {
					t.Errorf("%s peak %.4f ignores the bias (unbiased %.4f)", name, pair[1].Peak, pair[0].Peak)
				}
				if pair[1].PreLimiterOvers == 0 {
					t.Errorf("%s overs exclude the bias", name)
				}
			}
			if math.Abs(float64(direct1.Peak-sched1.Peak)) > 0.02*float64(direct1.Peak) {
				t.Errorf("peak differs: direct %.4f scheduled %.4f", direct1.Peak, sched1.Peak)
			}
		})
	}
}
