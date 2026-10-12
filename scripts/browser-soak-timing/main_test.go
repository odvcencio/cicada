package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/soaktiming"
)

func healthyDemoReport() demoSoakReport {
	return demoSoakReport{
		MemoryAfterWarmupBytes: 1024, MemoryPeakBytes: 1024,
		PlayheadAdvanced: true, CallbackSamples: 2000, MessagesDrained: 10,
		QuantumMs: 128000.0 / 48000, AudioWorkletHighResClock: true,
	}
}

func TestDemoSoakVerdict(t *testing.T) {
	for _, test := range []struct {
		name      string
		load      float64
		highRes   bool
		underruns int
		verdict   string
		pass      bool
	}{
		{"quiet clean", 1, true, 0, "pass", true},
		{"quiet underruns fail", 1, true, 8, "fail", false},
		{"busy underruns inconclusive", 5, true, 8, "inconclusive", true},
		{"quiet millisecond clock resolves quantum", 1, false, 8, "fail", false},
		{"busy millisecond clock", 5, false, 8, "inconclusive", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := healthyDemoReport()
			report.Underruns, report.AudioWorkletHighResClock = test.underruns, test.highRes
			result := report.verdict([]soaktiming.HostSample{{Load1: test.load, CPUCount: 8}})
			if result.TimingVerdict != test.verdict || result.GatePass != test.pass {
				t.Errorf("demo verdict = %+v, want %s pass=%v", result, test.verdict, test.pass)
			}
			if test.verdict == "inconclusive" && !strings.Contains(result.TimingReason, "host busy") {
				t.Errorf("missing busy-host reason: %+v", result)
			}
		})
	}
	report := healthyDemoReport()
	report.QuantumMs = 0.05
	if result := report.verdict([]soaktiming.HostSample{{Load1: 1, CPUCount: 8}}); result.TimingVerdict != "inconclusive" || !strings.Contains(result.TimingReason, "clock resolution") {
		t.Errorf("clock unable to resolve the quantum must be inconclusive: %+v", result)
	}
}

func TestDemoSoakCorrectnessFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*demoSoakReport)
	}{
		{"kernel faults", func(r *demoSoakReport) { r.Faults = 1 }},
		{"memory grows", func(r *demoSoakReport) { r.MemoryPeakBytes++ }},
		{"transport stalls", func(r *demoSoakReport) { r.PlayheadAdvanced = false }},
		{"too few callbacks", func(r *demoSoakReport) { r.CallbackSamples = 1000 }},
		{"no messages", func(r *demoSoakReport) { r.MessagesDrained = 0 }},
		{"page errors", func(r *demoSoakReport) { r.Errors = []string{"edit failed"} }},
		{"browser errors", func(r *demoSoakReport) { r.BrowserErrors = []string{"exception"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := healthyDemoReport()
			test.change(&report)
			result := report.verdict([]soaktiming.HostSample{{Load1: 5, CPUCount: 8}})
			if result.GatePass || result.TimingVerdict != "inconclusive" {
				t.Errorf("demo correctness must fail despite inconclusive timing: %+v", result)
			}
		})
	}
}

func TestDemoSoakProtocol(t *testing.T) {
	input, err := json.Marshal(healthyDemoReport())
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	start := func() func() []soaktiming.HostSample {
		return func() []soaktiming.HostSample { return []soaktiming.HostSample{{Load1: 5, CPUCount: 8}} }
	}
	if err := run(bytes.NewReader(input), &output, start); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var ready struct{ Ready bool }
	if err := decoder.Decode(&ready); err != nil || !ready.Ready {
		t.Fatalf("sampler readiness: %v, %v", ready, err)
	}
	var result struct {
		soaktiming.Result
		HostBusy        bool                    `json:"hostBusy"`
		HostLoadSamples []soaktiming.HostSample `json:"hostLoadSamples"`
	}
	if err := decoder.Decode(&result); err != nil || !result.HostBusy || len(result.HostLoadSamples) != 1 || result.TimingVerdict != "inconclusive" || !result.GatePass {
		t.Fatalf("demo report protocol: %+v, %v", result, err)
	}
	if err := run(strings.NewReader(""), &bytes.Buffer{}, start); err != nil {
		t.Fatalf("early runner failure must still stop sampling cleanly: %v", err)
	}
}

func TestDemoSoakMeasurementWindow(t *testing.T) {
	for _, test := range []struct {
		name               string
		ownLoad, otherLoad float64
		verdict            string
		pass               bool
	}{
		{"quiet demo ignores busy evidence from another window", 1, 5, "fail", false},
		{"busy demo ignores quiet evidence from another window", 5, 1, "inconclusive", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := healthyDemoReport()
			report.Underruns = 8
			data, err := json.Marshal(struct {
				demoSoakReport
				HostLoadSamples []soaktiming.HostSample `json:"hostLoadSamples"`
			}{report, []soaktiming.HostSample{{Load1: test.otherLoad, CPUCount: 8}}})
			if err != nil {
				t.Fatal(err)
			}
			ownSamples := []soaktiming.HostSample{{Load1: test.ownLoad, CPUCount: 8}}
			start := func() func() []soaktiming.HostSample {
				return func() []soaktiming.HostSample { return ownSamples }
			}
			var output bytes.Buffer
			if err := run(bytes.NewReader(data), &output, start); err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(&output)
			var ready struct{ Ready bool }
			if err := decoder.Decode(&ready); err != nil || !ready.Ready {
				t.Fatalf("sampler readiness: %v, %v", ready, err)
			}
			var result struct {
				soaktiming.Result
				HostLoadSamples []soaktiming.HostSample `json:"hostLoadSamples"`
			}
			if err := decoder.Decode(&result); err != nil {
				t.Fatal(err)
			}
			if result.TimingVerdict != test.verdict || result.GatePass != test.pass || len(result.HostLoadSamples) != 1 || result.HostLoadSamples[0].Load1 != test.ownLoad {
				t.Errorf("demo must use its own measurement window: %+v", result)
			}
		})
	}
}
