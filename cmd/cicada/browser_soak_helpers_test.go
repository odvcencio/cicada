package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/soaktiming"
)

const browserSoakCPUBudgetMs = soaktiming.CPUBudgetMs
const browserSoakHostSampleInterval = soaktiming.HostSampleInterval

type browserSoakHostSample = soaktiming.HostSample

var parseBrowserSoakLoad = soaktiming.ParseLoad
var startBrowserSoakHostSampling = soaktiming.StartHostSampling
var browserSoakHostBusy = soaktiming.HostBusy
var browserSoakCPUClockResolutionMs = soaktiming.CPUClockResolutionMs

type browserSoakChecks struct {
	EditsComplete       bool
	Faults              int
	TransportAdvanced   bool
	MemoryStable        bool
	Underruns           int
	CPUUsedMs           float64
	HighResolutionClock bool
	QuantumMs           float64
}

type browserSoakResult struct {
	soaktiming.Result
	UnderrunTiming soaktiming.Result
	CPUTiming      soaktiming.Result
}

func browserSoakVerdict(checks browserSoakChecks, samples []browserSoakHostSample, cpuClockResolutionMs float64) browserSoakResult {
	correctnessPass := checks.EditsComplete && checks.Faults == 0 && checks.TransportAdvanced && checks.MemoryStable
	// Underrun detection measures callback duration and gaps against a quantum.
	// Its worklet clock is independent of the CPU report's calibrated clock.
	underruns := soaktiming.Evaluate(checks.Underruns == 0, correctnessPass, samples, soaktiming.CallbackClockResolutionMs(checks.HighResolutionClock), checks.QuantumMs)
	cpu := soaktiming.Evaluate(checks.CPUUsedMs <= browserSoakCPUBudgetMs, correctnessPass, samples, cpuClockResolutionMs, browserSoakCPUBudgetMs)
	result := browserSoakResult{UnderrunTiming: underruns, CPUTiming: cpu}
	result.TimingVerdict = "pass"
	result.GatePass = underruns.GatePass && cpu.GatePass
	var reasons []string
	for _, metric := range []struct {
		name    string
		verdict soaktiming.Result
	}{{"underruns", underruns}, {"CPU budget", cpu}} {
		switch metric.verdict.TimingVerdict {
		case "fail":
			result.TimingVerdict = "fail"
		case "inconclusive":
			if result.TimingVerdict != "fail" {
				result.TimingVerdict = "inconclusive"
			}
			reasons = append(reasons, metric.name+": "+metric.verdict.TimingReason)
		}
	}
	result.TimingReason = strings.Join(reasons, "; ")
	return result
}

func startBrowserCPUSoakSampling(t *testing.T) func() []browserSoakHostSample {
	if os.Getenv("CICADA_BROWSER_SOAK") != "1" {
		return nil
	}
	stop := startBrowserSoakHostSampling()
	t.Cleanup(func() { stop() })
	return stop
}

// budget-browser is also a prerequisite of the nightly soak. Apply the same
// timing policy there only when invoked by the soak target.
func browserCPUSoakTiming(t *testing.T, stop func() []browserSoakHostSample, report map[string]any, timingPass, correctnessPass bool, engine, clock string) bool {
	t.Helper()
	if stop == nil {
		return true
	}
	samples := stop()
	observedMs, _ := report["cpuClockResolutionMs"].(float64)
	resolution := browserSoakCPUClockResolutionMs(engine, clock, observedMs)
	verdict := soaktiming.Evaluate(timingPass, correctnessPass, samples, resolution, browserSoakCPUBudgetMs)
	report["timingVerdict"], report["timingReason"], report["gatePass"] = verdict.TimingVerdict, verdict.TimingReason, verdict.GatePass
	report["hostLoadSamples"], report["hostBusy"] = samples, browserSoakHostBusy(samples)
	report["hostLoadSampleIntervalSeconds"] = int(browserSoakHostSampleInterval.Seconds())
	report["browserCPUClockResolutionMs"] = resolution
	if verdict.TimingVerdict == "inconclusive" {
		t.Logf("browser CPU timing inconclusive: %s", verdict.TimingReason)
		fmt.Printf("::warning::Browser CPU timing inconclusive: %s\n", verdict.TimingReason)
		return false
	}
	return true
}
