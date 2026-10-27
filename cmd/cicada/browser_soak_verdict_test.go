package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/soaktiming"
)

func TestBrowserSoakHostBusy(t *testing.T) {
	for _, test := range []struct {
		name    string
		samples []browserSoakHostSample
		busy    bool
	}{
		{"quiet", []browserSoakHostSample{{Load1: 1, CPUCount: 8}, {Load1: 3, CPUCount: 8}}, false},
		{"at threshold", []browserSoakHostSample{{Load1: 4, CPUCount: 8}}, false},
		{"busy before soak", []browserSoakHostSample{{Load1: 4.01, CPUCount: 8}, {Load1: 1, CPUCount: 8}}, true},
		{"busy during soak", []browserSoakHostSample{{Load1: 1, CPUCount: 8}, {Load1: 4.01, CPUCount: 8}}, true},
		{"single CPU busy", []browserSoakHostSample{{Load1: 0.51, CPUCount: 1}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := browserSoakHostBusy(test.samples); got != test.busy {
				t.Errorf("host busy = %v, want %v", got, test.busy)
			}
		})
	}
}

func TestBrowserSoakVerdict(t *testing.T) {
	for _, test := range []struct {
		name         string
		load         float64
		resolutionMs float64
		underruns    int
		cpuMs        float64
		verdict      string
		reason       string
		pass         bool
	}{
		{"quiet fine passes at budget", 1, 0.1, 0, 0.67, "pass", "", true},
		{"quiet fine underruns fail", 1, 0.1, 1, 0.2, "fail", "", false},
		{"quiet fine CPU fails", 1, 0.1, 0, 0.671, "fail", "", false},
		{"busy fine timing inconclusive", 5, 0.1, 316, 1.231, "inconclusive", "host busy", true},
		{"quiet fine Node CPU fails", 1, 0.001, 0, 1.231, "fail", "", false},
		{"quiet coarse CPU still fails underruns", 1, 1, 316, 1.231, "fail", "clock resolution", false},
		{"quiet coarse CPU inconclusive", 1, 1, 0, 1.231, "inconclusive", "clock resolution", true},
		{"coarse passing metric still inconclusive", 1, 1, 0, 0.2, "inconclusive", "clock resolution", true},
		{"busy coarse timing inconclusive", 5, 1, 316, 1.231, "inconclusive", "host busy", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			checks := browserSoakChecks{
				EditsComplete: true, TransportAdvanced: true, MemoryStable: true,
				Underruns: test.underruns, CPUUsedMs: test.cpuMs,
				HighResolutionClock: true, QuantumMs: 128000.0 / 48000,
			}
			samples := []browserSoakHostSample{{Load1: test.load, CPUCount: 8}}
			result := browserSoakVerdict(checks, samples, samples, test.resolutionMs)
			if result.TimingVerdict != test.verdict || result.GatePass != test.pass {
				t.Errorf("verdict = %+v, want timing=%s pass=%v", result, test.verdict, test.pass)
			}
			if !strings.Contains(result.TimingReason, test.reason) {
				t.Errorf("reason = %q, want %q", result.TimingReason, test.reason)
			}
			if test.load > 4 && test.resolutionMs > 0.67 && !strings.Contains(result.TimingReason, "clock resolution") {
				t.Errorf("both reasons must be reported: %q", result.TimingReason)
			}
		})
	}
}

func TestBrowserSoakCorrectnessFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*browserSoakChecks)
	}{
		{"kernel faults", func(c *browserSoakChecks) { c.Faults = 1 }},
		{"edit failures", func(c *browserSoakChecks) { c.EditsComplete = false }},
		{"transport stalled", func(c *browserSoakChecks) { c.TransportAdvanced = false }},
		{"memory unstable", func(c *browserSoakChecks) { c.MemoryStable = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			checks := browserSoakChecks{
				EditsComplete: true, TransportAdvanced: true, MemoryStable: true,
				CPUUsedMs:           1.231,
				HighResolutionClock: true, QuantumMs: 128000.0 / 48000,
			}
			test.change(&checks)
			for _, environment := range []struct{ load, resolutionMs float64 }{{5, 0.1}, {1, 1}} {
				samples := []browserSoakHostSample{{Load1: environment.load, CPUCount: 8}}
				result := browserSoakVerdict(checks, samples, samples, environment.resolutionMs)
				if result.GatePass || result.TimingVerdict != "inconclusive" {
					t.Errorf("correctness failure must fail despite inconclusive timing: %+v", result)
				}
			}
		})
	}
}

func TestBrowserSoakCPUClockResolution(t *testing.T) {
	for _, test := range []struct {
		engine   string
		clock    string
		observed float64
		want     float64
	}{
		{"Node V8 WebAssembly", "process.cpuUsage() user milliseconds", 0.001, 0.001},
		{"Node V8 WebAssembly", "process.cpuUsage() user milliseconds", 1, 1},
		{"Node V8 WebAssembly", "process.cpuUsage() user milliseconds", 0, 0},
		{"Windows Chrome AudioWorklet", "Date.now()", 0.001, 1},
		{"Windows Chrome AudioWorklet", "AudioWorklet performance.now()", 0, 0.1},
	} {
		if got := browserSoakCPUClockResolutionMs(test.engine, test.clock, test.observed); got != test.want {
			t.Errorf("%s / %s resolution = %g ms, want %g ms", test.engine, test.clock, got, test.want)
		}
	}
}

func TestBrowserSoakHostSamplingUnavailable(t *testing.T) {
	for _, samples := range [][]browserSoakHostSample{
		nil,
		{{CPUCount: 8, Error: "load average unavailable"}},
		{{Load1: 1, CPUCount: 0}},
	} {
		checks := browserSoakChecks{EditsComplete: true, TransportAdvanced: true, MemoryStable: true, HighResolutionClock: true, QuantumMs: 128000.0 / 48000}
		result := browserSoakVerdict(checks, samples, samples, 0.1)
		if result.TimingVerdict != "inconclusive" || !result.GatePass || !strings.Contains(result.TimingReason, "host quietness unavailable") {
			t.Errorf("missing host evidence must not be treated as quiet: %+v", result)
		}
	}
}

func TestBrowserSoakParseLoad(t *testing.T) {
	load, runnable, err := parseBrowserSoakLoad([]byte("1.25 0.90 0.85 3/700 12345\n"))
	if err != nil || load != 1.25 || runnable != 3 {
		t.Fatalf("load sample = %g, %d, %v; want 1.25, 3, nil", load, runnable, err)
	}
	for _, data := range []string{"", "invalid 0 0 1/2", "1.25 0 0 invalid/2"} {
		if _, _, err := parseBrowserSoakLoad([]byte(data)); err == nil {
			t.Errorf("invalid load sample %q was accepted", data)
		}
	}
}

func TestBrowserSoakHostSampling(t *testing.T) {
	stop := startBrowserSoakHostSampling()
	defer stop()
	samples := stop()
	if len(samples) != 2 || samples[0].CPUCount <= 0 || samples[0].SampledAt.IsZero() || samples[1].SampledAt.Before(samples[0].SampledAt) {
		t.Fatalf("expected initial and final host samples: %+v", samples)
	}
	if again := stop(); len(again) != len(samples) {
		t.Fatalf("stopping twice changed the samples: %+v", again)
	}
}

func TestBrowserCPUSoakSamplingScope(t *testing.T) {
	t.Setenv("CICADA_BROWSER_SOAK", "0")
	if stop := startBrowserCPUSoakSampling(t); stop != nil {
		t.Fatal("ordinary CPU budgets must retain their timing gates")
	}
	t.Setenv("CICADA_BROWSER_SOAK", "1")
	stop := startBrowserCPUSoakSampling(t)
	if stop == nil || len(stop()) != 2 {
		t.Fatal("nightly CPU prerequisite must sample host quietness")
	}
}

func TestBrowserCPUSoakTiming(t *testing.T) {
	for _, test := range []struct {
		name     string
		nightly  bool
		load     float64
		engine   string
		clock    string
		correct  bool
		enforced bool
		gatePass bool
	}{
		{"ordinary budget stays strict", false, 5, "Node V8 WebAssembly", "process.cpuUsage() user milliseconds", true, true, false},
		{"nightly busy high resolution", true, 5, "Windows Chrome AudioWorklet", "AudioWorklet performance.now()", true, false, true},
		{"nightly quiet high resolution", true, 1, "Windows Chrome AudioWorklet", "AudioWorklet performance.now()", true, true, false},
		{"nightly quiet missing calibration", true, 1, "Node V8 WebAssembly", "process.cpuUsage() user milliseconds", true, false, true},
		{"nightly correctness failure", true, 5, "Node V8 WebAssembly", "process.cpuUsage() user milliseconds", false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stop func() []browserSoakHostSample
			if test.nightly {
				stop = func() []browserSoakHostSample { return []browserSoakHostSample{{Load1: test.load, CPUCount: 8}} }
			}
			report := make(map[string]any)
			if enforced := browserCPUSoakTiming(t, stop, report, false, test.correct, test.engine, test.clock); enforced != test.enforced {
				t.Errorf("CPU timing enforced = %v, want %v", enforced, test.enforced)
			}
			if test.nightly && report["gatePass"] != test.gatePass {
				t.Errorf("CPU report gatePass = %v, want %v", report["gatePass"], test.gatePass)
			}
			if !test.nightly && len(report) != 0 {
				t.Errorf("ordinary CPU report unexpectedly changed: %v", report)
			}
		})
	}
}

func TestBrowserSoakForeignCPUVerdict(t *testing.T) {
	for _, test := range []struct {
		name    string
		share   float64
		cpuMs   float64
		verdict string
		pass    bool
	}{
		{"clean counter over budget fails", 0, 0.8, "fail", false},
		{"share at the limit is clean", soaktiming.ForeignCPUShareLimit, 0.8, "fail", false},
		{"clean counter within budget passes", 0.001, 0.3, "pass", true},
		{"contaminated counter over budget is inconclusive", 0.0051, 1.231, "inconclusive", true},
		{"contaminated counter within budget is inconclusive too", 0.02, 0.3, "inconclusive", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			checks := browserSoakChecks{
				EditsComplete: true, TransportAdvanced: true, MemoryStable: true,
				CPUUsedMs: test.cpuMs, CPUForeignShare: test.share,
				HighResolutionClock: true, QuantumMs: 128000.0 / 48000,
			}
			samples := []browserSoakHostSample{{Load1: 1, CPUCount: 8}}
			result := browserSoakVerdict(checks, samples, samples, 0.001)
			if result.CPUTiming.TimingVerdict != test.verdict || result.GatePass != test.pass {
				t.Errorf("CPU timing = %+v, want %s pass=%v", result.CPUTiming, test.verdict, test.pass)
			}
			if got := strings.Contains(result.CPUTiming.TimingReason, "foreign CPU in counter"); got != (test.share > soaktiming.ForeignCPUShareLimit) {
				t.Errorf("foreign CPU reason present = %v for share %g: %q", got, test.share, result.CPUTiming.TimingReason)
			}
			if result.UnderrunTiming.TimingVerdict != "pass" {
				t.Errorf("the CPU counter must not change the underrun verdict: %+v", result.UnderrunTiming)
			}
		})
	}
}

// Foreign CPU time belongs to the CPU measurement only. Underruns stay a hard gate.
func TestBrowserSoakUnderrunsFailWithContaminatedCPU(t *testing.T) {
	checks := browserSoakChecks{
		EditsComplete: true, TransportAdvanced: true, MemoryStable: true,
		Underruns: 3, CPUUsedMs: 1.231, CPUForeignShare: 0.05,
		HighResolutionClock: true, QuantumMs: 128000.0 / 48000,
	}
	samples := []browserSoakHostSample{{Load1: 1, CPUCount: 8}}
	result := browserSoakVerdict(checks, samples, samples, 0.001)
	if result.UnderrunTiming.TimingVerdict != "fail" || result.CPUTiming.TimingVerdict != "inconclusive" || result.GatePass {
		t.Errorf("underruns must fail whatever the CPU counter held: %+v", result)
	}
}

func TestBrowserCPUSoakTimingForeignCPU(t *testing.T) {
	quiet := func() []browserSoakHostSample { return []browserSoakHostSample{{Load1: 1, CPUCount: 8}} }
	const engine, clock = "Node V8 WebAssembly", "process.cpuUsage() user milliseconds"
	for _, test := range []struct {
		name     string
		report   map[string]any
		verdict  string
		enforced bool
		gatePass bool
	}{
		{"clean counter keeps its budget", map[string]any{"cpuClockResolutionMs": 0.001, "excessBlockShare": 0.0}, "fail", true, false},
		{"contaminated counter is inconclusive", map[string]any{"cpuClockResolutionMs": 0.001, "excessBlockShare": 0.02}, "inconclusive", false, true},
		{"a report without a share is not treated as contaminated", map[string]any{"cpuClockResolutionMs": 0.001}, "fail", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if enforced := browserCPUSoakTiming(t, quiet, test.report, false, true, engine, clock); enforced != test.enforced {
				t.Errorf("CPU timing enforced = %v, want %v", enforced, test.enforced)
			}
			if test.report["timingVerdict"] != test.verdict || test.report["gatePass"] != test.gatePass {
				t.Errorf("report verdict = %v gatePass = %v, want %s %v", test.report["timingVerdict"], test.report["gatePass"], test.verdict, test.gatePass)
			}
			reason, _ := test.report["timingReason"].(string)
			if strings.Contains(reason, "foreign CPU in counter") != (test.verdict == "inconclusive") {
				t.Errorf("timing reason = %q", reason)
			}
		})
	}
}

func TestBrowserSoakCPUReportShare(t *testing.T) {
	for _, test := range []struct {
		name string
		json string
		want *float64
	}{
		{"absent", `{"engine":"Node V8 WebAssembly"}`, nil},
		{"zero is a measurement", `{"engine":"Node V8 WebAssembly","excessBlockShare":0}`, new(float64)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var report browserSoakCPUReport
			if err := json.Unmarshal([]byte(test.json), &report); err != nil {
				t.Fatal(err)
			}
			if (report.ExcessBlockShare == nil) != (test.want == nil) || (test.want != nil && *report.ExcessBlockShare != *test.want) {
				t.Errorf("share = %v, want %v", report.ExcessBlockShare, test.want)
			}
		})
	}
}

// Node reads its own flags only before the script name, and it needs all three
// (see browserCPUNodeFlags), so pin the whole command line.
func TestBrowserCPUNodeCommand(t *testing.T) {
	command := browserCPUNodeCommand("kernel.wasm", "kernel.image", 11264)
	want := []string{"node", "--single-threaded", "--no-liftoff", "--no-wasm-lazy-compilation", "browser_cpu_node.js", "kernel.wasm", "kernel.image", "11264"}
	if !slices.Equal(command.Args, want) {
		t.Errorf("Node command = %q, want %q", command.Args, want)
	}
}

// The reporter and the verdict must agree on how far above the median a block is
// counted. The reporter is a script, so compare the source.
func TestNodeCPUReporterUsesTheForeignCPUExcess(t *testing.T) {
	source, err := os.ReadFile("browser_cpu_node.js")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("const EXCESS_BLOCK_MS = %v;", soaktiming.ForeignCPUExcessMs)
	if !strings.Contains(string(source), want) {
		t.Errorf("browser_cpu_node.js must declare %q to match soaktiming.ForeignCPUExcessMs", want)
	}
}
