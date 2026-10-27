package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/soaktiming"
)

// browserCPUNodeFlags keep other threads' CPU time out of process.cpuUsage(), which
// browser_cpu_node.js reads once per block. By default V8 compiles the kernel with
// Liftoff and tiers it up on background threads while the timed loop runs, and Linux
// credits those threads to the process counter at scheduler ticks. These flags
// compile the whole kernel with the optimizing compiler before the loop and start no
// background task. Use all three: --single-threaded alone disables tier-up and leaves
// slow Liftoff code in the loop, and the two compile flags alone leave background
// threads running.
var browserCPUNodeFlags = []string{"--single-threaded", "--no-liftoff", "--no-wasm-lazy-compilation"}

// browserCPUNodeCommand times the kernel in Node. Run it from cmd/cicada. Node reads
// its own flags only before the script name.
func browserCPUNodeCommand(wasmPath, imagePath string, blocks int) *exec.Cmd {
	args := append(append([]string{}, browserCPUNodeFlags...), "browser_cpu_node.js", wasmPath, imagePath, strconv.Itoa(blocks))
	return exec.Command("node", args...)
}

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
	CPUForeignShare     float64 // share of blocks well above the median, 0 if the engine lacks it
	HighResolutionClock bool
	QuantumMs           float64
}

type browserSoakCPUReport struct {
	Engine            string                  `json:"engine"`
	Clock             string                  `json:"clock"`
	ClockResolutionMs float64                 `json:"cpuClockResolutionMs"`
	P99               float64                 `json:"p99"`
	CPUMsPerCallback  float64                 `json:"cpuMsPerCallback"`
	BudgetMs          float64                 `json:"cpuBudgetMs"`
	BudgetMetric      string                  `json:"cpuBudgetMetric"`
	ExcessBlockShare  *float64                `json:"excessBlockShare"` // nil unless the Node fallback wrote it
	HostLoadSamples   []browserSoakHostSample `json:"hostLoadSamples"`
}

type browserSoakResult struct {
	soaktiming.Result
	UnderrunTiming soaktiming.Result
	CPUTiming      soaktiming.Result
}

func browserSoakVerdict(checks browserSoakChecks, soakSamples, cpuSamples []browserSoakHostSample, cpuClockResolutionMs float64) browserSoakResult {
	correctnessPass := checks.EditsComplete && checks.Faults == 0 && checks.TransportAdvanced && checks.MemoryStable
	// Underrun detection measures callback duration and gaps against a quantum.
	// Its worklet clock is independent of the CPU report's calibrated clock.
	underruns := soaktiming.Evaluate(checks.Underruns == 0, correctnessPass, soakSamples, soaktiming.CallbackClockResolutionMs(checks.HighResolutionClock), checks.QuantumMs)
	// The CPU value is cached from budget-browser. Only that report's load
	// samples describe its measurement window; later soak samples cannot
	// establish whether that earlier CPU measurement was quiet.
	// The same report's share of blocks above the median says whether other threads'
	// CPU time reached the counter that produced the cached value.
	cpu := soaktiming.Evaluate(checks.CPUUsedMs <= browserSoakCPUBudgetMs, correctnessPass, cpuSamples, cpuClockResolutionMs, browserSoakCPUBudgetMs, soaktiming.ForeignCPUReason(checks.CPUForeignShare))
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
	foreignShare, _ := report["excessBlockShare"].(float64)
	verdict := soaktiming.Evaluate(timingPass, correctnessPass, samples, resolution, browserSoakCPUBudgetMs, soaktiming.ForeignCPUReason(foreignShare))
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
