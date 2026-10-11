package main

import (
	"encoding/json"
	"testing"
)

func TestBrowserSoakMeasurementWindows(t *testing.T) {
	for _, test := range []struct {
		name            string
		cpuLoad         float64
		soakLoad        float64
		cpuMs           float64
		underruns       int
		cpuVerdict      string
		underrunVerdict string
		pass            bool
	}{
		{"quiet CPU stage busy soak", 1, 5, 0.7, 8, "fail", "inconclusive", false},
		{"busy CPU stage quiet soak", 5, 1, 1.231, 8, "inconclusive", "fail", false},
		{"busy CPU stage quiet clean soak", 5, 1, 1.231, 0, "inconclusive", "pass", true},
		{"quiet passing CPU stage busy soak", 1, 5, 0.2, 8, "pass", "inconclusive", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			checks := browserSoakChecks{
				EditsComplete: true, TransportAdvanced: true, MemoryStable: true,
				Underruns: test.underruns, CPUUsedMs: test.cpuMs,
				HighResolutionClock: true, QuantumMs: 128000.0 / 48000,
			}
			soakSamples := []browserSoakHostSample{{Load1: test.soakLoad, CPUCount: 8}}
			cpuSamples := []browserSoakHostSample{{Load1: test.cpuLoad, CPUCount: 8}}
			result := browserSoakVerdict(checks, soakSamples, cpuSamples, 0.001)
			if result.CPUTiming.TimingVerdict != test.cpuVerdict || result.UnderrunTiming.TimingVerdict != test.underrunVerdict || result.GatePass != test.pass {
				t.Errorf("CPU load=%g and soak load=%g must be judged separately: %+v; want CPU=%s underruns=%s pass=%v", test.cpuLoad, test.soakLoad, result, test.cpuVerdict, test.underrunVerdict, test.pass)
			}
		})
	}
}

func TestBrowserSoakCPUReportMeasurementEvidence(t *testing.T) {
	for _, test := range []struct {
		name                        string
		cpuLoad, soakLoad           float64
		cpuVerdict, underrunVerdict string
	}{
		{"quiet CPU report busy soak", 1, 5, "fail", "inconclusive"},
		{"busy CPU report quiet soak", 5, 1, "inconclusive", "fail"},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := map[string]any{
				"engine": "Node V8 WebAssembly", "clock": "process.cpuUsage() user milliseconds",
				"cpuClockResolutionMs": 0.001, "p95": 0.2, "p99": 0.7,
			}
			stop := func() []browserSoakHostSample { return []browserSoakHostSample{{Load1: test.cpuLoad, CPUCount: 8}} }
			// p95=0.2 and p99=0.7 satisfy budget-browser's existing thresholds.
			// Studio must still apply its stricter 0.67 ms p99 limit independently.
			browserCPUSoakTiming(t, stop, report, true, true, report["engine"].(string), report["clock"].(string))
			data, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			var cached browserSoakCPUReport
			if err := json.Unmarshal(data, &cached); err != nil {
				t.Fatal(err)
			}
			checks := browserSoakChecks{
				EditsComplete: true, TransportAdvanced: true, MemoryStable: true,
				Underruns: 8, CPUUsedMs: cached.P99,
				HighResolutionClock: true, QuantumMs: 128000.0 / 48000,
			}
			resolution := browserSoakCPUClockResolutionMs(cached.Engine, cached.Clock, cached.ClockResolutionMs)
			result := browserSoakVerdict(checks, []browserSoakHostSample{{Load1: test.soakLoad, CPUCount: 8}}, cached.HostLoadSamples, resolution)
			if result.CPUTiming.TimingVerdict != test.cpuVerdict || result.UnderrunTiming.TimingVerdict != test.underrunVerdict || result.GatePass {
				t.Errorf("cached CPU evidence must survive report decoding: %+v", result)
			}
		})
	}
}

func TestBrowserSoakCPUHostSamplesUnavailable(t *testing.T) {
	checks := browserSoakChecks{
		EditsComplete: true, TransportAdvanced: true, MemoryStable: true,
		Underruns: 1, CPUUsedMs: 0.7,
		HighResolutionClock: true, QuantumMs: 128000.0 / 48000,
	}
	result := browserSoakVerdict(checks, []browserSoakHostSample{{Load1: 1, CPUCount: 8}}, nil, 0.001)
	if result.CPUTiming.TimingVerdict != "inconclusive" || result.UnderrunTiming.TimingVerdict != "fail" || result.GatePass {
		t.Fatalf("missing CPU samples must not borrow the soak's quietness: %+v", result)
	}
}

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
	samples := []browserSoakHostSample{{Load1: 1, CPUCount: 8}}
	result := browserSoakVerdict(checks, samples, samples, 1)
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
			samples := []browserSoakHostSample{{Load1: 1, CPUCount: 8}}
			result := browserSoakVerdict(checks, samples, samples, 0.001)
			if result.UnderrunTiming.TimingVerdict != test.verdict || result.CPUTiming.TimingVerdict != "pass" || result.GatePass != (test.verdict == "inconclusive") {
				t.Errorf("callback clock must be classified independently: %+v", result)
			}
			if test.verdict == "inconclusive" {
				checks.CPUUsedMs = 0.671
				if result := browserSoakVerdict(checks, samples, samples, 0.001); result.GatePass || result.CPUTiming.TimingVerdict != "fail" {
					t.Errorf("an inconclusive callback clock must not mask a CPU failure: %+v", result)
				}
			}
		})
	}
}
