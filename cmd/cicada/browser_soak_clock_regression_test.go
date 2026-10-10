package main

import "testing"

func TestBrowserCPUNightlyMeasuredClock(t *testing.T) {
	for _, test := range []struct {
		name       string
		load       float64
		resolution float64
		enforced   bool
	}{
		{"quiet fine Node CPU fails", 1, 0.001, true},
		{"busy fine Node CPU inconclusive", 5, 0.001, false},
		{"quiet measured coarse CPU inconclusive", 1, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			stop := func() []browserSoakHostSample { return []browserSoakHostSample{{Load1: test.load, CPUCount: 8}} }
			report := map[string]any{"cpuClockResolutionMs": test.resolution}
			if enforced := browserCPUSoakTiming(t, stop, report, false, true, "Node V8 WebAssembly", "process.cpuUsage() user milliseconds"); enforced != test.enforced {
				t.Errorf("measured CPU timing enforced = %v, want %v; report=%v", enforced, test.enforced, report)
			}
			if report["gatePass"] != !test.enforced {
				t.Errorf("over-budget CPU gatePass = %v, want %v", report["gatePass"], !test.enforced)
			}
		})
	}
}

func TestBrowserSoakCPUClockDoesNotMaskUnderruns(t *testing.T) {
	checks := browserSoakChecks{
		EditsComplete: true, TransportAdvanced: true, MemoryStable: true,
		Underruns: 8, CPUUsedMs: 0.2,
		HighResolutionClock: true, QuantumMs: 128000.0 / 48000,
	}
	result := browserSoakVerdict(checks, []browserSoakHostSample{{Load1: 1, CPUCount: 8}}, 1)
	if result.GatePass || result.UnderrunTiming.TimingVerdict != "fail" || result.CPUTiming.TimingVerdict != "inconclusive" {
		t.Fatalf("a coarse CPU clock must not mask quiet-host underruns: %+v", result)
	}
}

func TestBrowserSoakCallbackClockValidity(t *testing.T) {
	for _, test := range []struct {
		name      string
		highRes   bool
		quantumMs float64
		verdict   string
	}{
		{"Date.now resolves the actual quantum", false, 128000.0 / 48000, "fail"},
		{"Date.now cannot resolve a submillisecond quantum", false, 0.5, "inconclusive"},
		{"fine callback clock resolves a submillisecond quantum", true, 0.5, "fail"},
	} {
		t.Run(test.name, func(t *testing.T) {
			checks := browserSoakChecks{
				EditsComplete: true, TransportAdvanced: true, MemoryStable: true,
				Underruns: 8, CPUUsedMs: 0.2,
				HighResolutionClock: test.highRes, QuantumMs: test.quantumMs,
			}
			result := browserSoakVerdict(checks, []browserSoakHostSample{{Load1: 1, CPUCount: 8}}, 0.001)
			if result.UnderrunTiming.TimingVerdict != test.verdict || result.CPUTiming.TimingVerdict != "pass" || result.GatePass != (test.verdict == "inconclusive") {
				t.Errorf("callback clock must be classified independently: %+v", result)
			}
			if test.verdict == "inconclusive" {
				checks.CPUUsedMs = 0.671
				if result := browserSoakVerdict(checks, []browserSoakHostSample{{Load1: 1, CPUCount: 8}}, 0.001); result.GatePass || result.CPUTiming.TimingVerdict != "fail" {
					t.Errorf("an inconclusive callback clock must not mask a CPU failure: %+v", result)
				}
			}
		})
	}
}
