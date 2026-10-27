package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/soaktiming"
)

// browserCPUNodeFlags keep other threads' CPU time out of process.cpuUsage(), which
// browser_cpu_node.js reads twice per block. By default V8 compiles the kernel with
// Liftoff and tiers it up on background threads while the timed loop runs, and Linux
// credits those threads to the process counter at scheduler ticks. These flags
// compile the whole kernel with the optimizing compiler before the loop and start no
// background thread. Use all three: --single-threaded alone disables tier-up and leaves
// slow Liftoff code in the loop, and the two compile flags alone leave background
// threads running. With no background thread, V8 also compiles the script's own
// JavaScript on the timed thread, and a compile there adds to a block. The script reads
// its clock before the loop for that reason (warmCPUClock).
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
	CPUExcessBlockShare float64 // share of excess blocks in the CPU report, 0 if the engine reports none
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

// browserSoakCPUExcessBlockShare returns the share of excess blocks in a cached CPU report.
// Only the Node fallback measures it. A Node report without it came from an older
// browser_cpu_node.js, which timed with V8's background threads running, so it is refused.
func browserSoakCPUExcessBlockShare(report browserSoakCPUReport) (float64, error) {
	if report.Engine != "Node V8 WebAssembly" {
		return 0, nil
	}
	if report.ExcessBlockShare == nil {
		return 0, errors.New("browser CPU report has no excessBlockShare; rerun budget-browser with the current browser_cpu_node.js")
	}
	return *report.ExcessBlockShare, nil
}

// browserSoakCPUBudgetError is the failure message for a CPU metric over the budget. A
// failed CPU verdict carries the note about excess blocks, and the message repeats it.
func browserSoakCPUBudgetError(usedMs float64, cpu soaktiming.Result) string {
	message := fmt.Sprintf("browser CPU metric %.4f ms/callback exceeds %.2f ms budget", usedMs, browserSoakCPUBudgetMs)
	if cpu.TimingReason != "" {
		message += "; " + cpu.TimingReason
	}
	return message
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
	// The same report's share of excess blocks only explains a failure; it never excuses one.
	cpu := soaktiming.EvaluateCPU(checks.CPUUsedMs <= browserSoakCPUBudgetMs, correctnessPass, cpuSamples, cpuClockResolutionMs, browserSoakCPUBudgetMs, checks.CPUExcessBlockShare)
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
		}
		if metric.verdict.TimingReason != "" {
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
	excessShare, _ := report["excessBlockShare"].(float64)
	verdict := soaktiming.EvaluateCPU(timingPass, correctnessPass, samples, resolution, browserSoakCPUBudgetMs, excessShare)
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
