package soaktiming

import (
	"math"
	"strings"
	"testing"
)

func TestForeignCPUReason(t *testing.T) {
	for _, test := range []struct {
		name   string
		share  float64
		reason bool
	}{
		{"no slow blocks", 0, false},
		{"below the limit", 0.004, false},
		{"at the limit", ForeignCPUShareLimit, false},
		{"just above the limit", 0.0051, true},
		{"a tick in one block of twenty", 0.05, true},
		{"not a number", math.NaN(), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ForeignCPUReason(test.share); (got != "") != test.reason {
				t.Errorf("ForeignCPUReason(%g) = %q, want a reason: %v", test.share, got, test.reason)
			}
		})
	}
	const want = "foreign CPU in counter: 5.00% of measured blocks ran more than 0.9 ms above the median (limit 0.50%)"
	if got := ForeignCPUReason(0.05); got != want {
		t.Errorf("reason = %q, want %q", got, want)
	}
}

func TestEvaluateExtraReasons(t *testing.T) {
	quiet := []HostSample{{Load1: 1, CPUCount: 8}}
	busy := []HostSample{{Load1: 5, CPUCount: 8}}
	foreign := ForeignCPUReason(0.02)
	for _, test := range []struct {
		name        string
		timingPass  bool
		correct     bool
		samples     []HostSample
		extra       []string
		verdict     string
		pass        bool
		reasonParts []string
	}{
		{"no reasons keeps a failing budget failed", false, true, quiet, nil, "fail", false, nil},
		{"an empty reason changes nothing", false, true, quiet, []string{""}, "fail", false, nil},
		{"a reason makes a failing budget inconclusive", false, true, quiet, []string{foreign}, "inconclusive", true, []string{"foreign CPU in counter"}},
		{"a reason also makes a passing budget inconclusive", true, true, quiet, []string{foreign}, "inconclusive", true, []string{"foreign CPU in counter"}},
		{"a reason does not excuse a correctness failure", false, false, quiet, []string{foreign}, "inconclusive", false, []string{"foreign CPU in counter"}},
		{"a reason joins the host reasons", false, true, busy, []string{foreign}, "inconclusive", true, []string{"host busy", "foreign CPU in counter"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := Evaluate(test.timingPass, test.correct, test.samples, 0.001, CPUBudgetMs, test.extra...)
			if got.TimingVerdict != test.verdict || got.GatePass != test.pass {
				t.Errorf("verdict = %+v, want %s pass=%v", got, test.verdict, test.pass)
			}
			for _, part := range test.reasonParts {
				if !strings.Contains(got.TimingReason, part) {
					t.Errorf("reason %q lacks %q", got.TimingReason, part)
				}
			}
			if len(test.reasonParts) == 0 && got.TimingReason != "" {
				t.Errorf("unexpected reason %q", got.TimingReason)
			}
		})
	}
}
