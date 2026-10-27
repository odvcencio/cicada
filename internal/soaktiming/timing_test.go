package soaktiming

import (
	"math"
	"strings"
	"testing"
)

func TestExcessBlockNote(t *testing.T) {
	for _, test := range []struct {
		name  string
		share float64
		note  bool
	}{
		{"no excess blocks", 0, false},
		{"below the limit", 0.004, false},
		{"at the limit", ExcessBlockShareLimit, false},
		{"just above the limit", 0.0051, true},
		{"one block in twenty", 0.05, true},
		{"not a number", math.NaN(), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ExcessBlockNote(test.share); (got != "") != test.note {
				t.Errorf("ExcessBlockNote(%g) = %q, want a note: %v", test.share, got, test.note)
			}
		})
	}
	const start = "5.00% of measured blocks ran more than 0.9 ms above the median, over the 0.50% limit for a clean counter;"
	got := ExcessBlockNote(0.05)
	if !strings.HasPrefix(got, start) || !strings.Contains(got, "firstExcessBlocks") {
		t.Errorf("note = %q, want it to start %q and name firstExcessBlocks", got, start)
	}
}

// The share of excess blocks explains a failed budget. It must never excuse one, and it
// must never move a pass or an inconclusive result, whatever the share is.
func TestEvaluateCPUExcessBlocksNeverChangeTheVerdict(t *testing.T) {
	quiet := []HostSample{{Load1: 1, CPUCount: 8}}
	busy := []HostSample{{Load1: 5, CPUCount: 8}}
	for _, test := range []struct {
		name         string
		timingPass   bool
		correctness  bool
		samples      []HostSample
		resolutionMs float64
		verdict      string
		gatePass     bool
	}{
		{"within budget passes", true, true, quiet, 0.001, "pass", true},
		{"over budget fails", false, true, quiet, 0.001, "fail", false},
		{"over budget and a correctness failure fails", false, false, quiet, 0.001, "fail", false},
		{"a busy host is still inconclusive", false, true, busy, 0.001, "inconclusive", true},
		{"a coarse clock is still inconclusive", false, true, quiet, 1, "inconclusive", true},
		{"a correctness failure still fails the gate when inconclusive", false, false, busy, 0.001, "inconclusive", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			plain := Evaluate(test.timingPass, test.correctness, test.samples, test.resolutionMs, CPUBudgetMs)
			if plain.TimingVerdict != test.verdict || plain.GatePass != test.gatePass {
				t.Fatalf("Evaluate = %+v, want %s pass=%v", plain, test.verdict, test.gatePass)
			}
			for _, share := range []float64{0, ExcessBlockShareLimit, 0.0051, 0.02, 1, math.NaN()} {
				got := EvaluateCPU(test.timingPass, test.correctness, test.samples, test.resolutionMs, CPUBudgetMs, share)
				if got.TimingVerdict != plain.TimingVerdict || got.GatePass != plain.GatePass {
					t.Errorf("share %g changed the result from %+v to %+v", share, plain, got)
				}
				noted := strings.Contains(got.TimingReason, "firstExcessBlocks")
				if want := plain.TimingVerdict == "fail" && share > ExcessBlockShareLimit; noted != want {
					t.Errorf("share %g: note present = %v, want %v (reason %q)", share, noted, want, got.TimingReason)
				}
				if !noted && got.TimingReason != plain.TimingReason {
					t.Errorf("share %g changed the reason from %q to %q", share, plain.TimingReason, got.TimingReason)
				}
			}
		})
	}
}

func TestEvaluateCPUExplainsAFailedBudget(t *testing.T) {
	quiet := []HostSample{{Load1: 1, CPUCount: 8}}
	got := EvaluateCPU(false, true, quiet, 0.001, CPUBudgetMs, 0.0231)
	if got.TimingVerdict != "fail" || got.GatePass || !strings.HasPrefix(got.TimingReason, "2.31% of measured blocks ran more than 0.9 ms above the median") {
		t.Errorf("a failed budget with 2.31%% excess blocks = %+v, want a failure that names the share", got)
	}
	if got := EvaluateCPU(false, true, quiet, 0.001, CPUBudgetMs, 0.0049); got.TimingVerdict != "fail" || got.TimingReason != "" {
		t.Errorf("a failed budget under the share limit = %+v, want a failure with no reason", got)
	}
}
