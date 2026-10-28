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
// browser_cpu_node.js reads twice per block. By default V8 compiles a kernel function
// with Liftoff the first time it runs. While the timed loop runs, it also tiers hot
// functions up with TurboFan, its optimizing compiler, on background threads, and Linux
// credits those threads to the process counter at scheduler ticks. With all three flags
// V8 compiles the whole kernel with TurboFan before the loop, and Node's worker threads
// use no measurable CPU time in the loop. Use all three.
//
// What each flag set does, measured on Node 22.23.4 (V8 12.4.254.21-node.57) on one host
// with the kernel built at main 28715939. Six timed runs per flag set give the CPU times
// and the excess-block counts, and four traced runs (--trace-wasm-compilation-times) give
// the compiles:
//
//   - --single-threaded alone leaves liftoff, wasm-lazy-compilation and
//     wasm-dynamic-tiering on (node --single-threaded --print-flag-values). V8 compiles a
//     function with Liftoff on the calling thread, which is the timed thread, the first
//     time it runs, and tiers hot functions up with TurboFan on one worker thread. In
//     every traced run, 76 Liftoff and 48 TurboFan compiles finished inside the timed
//     loop. Worker threads used 105 to 156 ms of CPU in it, and 14 to 25 measured blocks
//     were excess blocks.
//   - The two compile flags alone compile all 311 kernel functions with TurboFan before
//     the loop, and no compile finishes inside it. Worker threads still used 3.5 to
//     3.8 ms of CPU in the loop, for a reason the compile trace does not show, and 1 or 2
//     measured blocks were excess blocks.
//   - With all three flags no compile finishes inside the loop, worker threads use 0.00 ms
//     of CPU in it, and no measured block is an excess block.
//
// With --single-threaded, V8 also compiles the script's own JavaScript on the timed
// thread, and a compile there adds to a block. The script reads its clock before the loop
// for that reason (warmCPUClock).
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
