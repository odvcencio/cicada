package main

import (
	"fmt"
	"os"
	"testing"

	"m31labs.dev/cicada/internal/soaktiming"
)

const browserSoakCPUBudgetMs = soaktiming.CPUBudgetMs
const browserSoakHostSampleInterval = soaktiming.HostSampleInterval

type browserSoakHostSample = soaktiming.HostSample

var parseBrowserSoakLoad = soaktiming.ParseLoad
var startBrowserSoakHostSampling = soaktiming.StartHostSampling
var browserSoakHostBusy = soaktiming.HostBusy
var browserSoakCPUClockResolutionMs = soaktiming.ClockResolutionMs

type browserSoakChecks struct {
	EditsComplete     bool
	Faults            int
	TransportAdvanced bool
	MemoryStable      bool
	Underruns         int
	CPUUsedMs         float64
}

func browserSoakVerdict(checks browserSoakChecks, samples []browserSoakHostSample, cpuClockResolutionMs float64) soaktiming.Result {
	return soaktiming.Evaluate(
		checks.Underruns == 0 && checks.CPUUsedMs <= browserSoakCPUBudgetMs,
		checks.EditsComplete && checks.Faults == 0 && checks.TransportAdvanced && checks.MemoryStable,
		samples, cpuClockResolutionMs, browserSoakCPUBudgetMs,
	)
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
	resolution := browserSoakCPUClockResolutionMs(engine, clock)
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
