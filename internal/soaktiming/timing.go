package soaktiming

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const CPUBudgetMs = 0.67
const HostSampleInterval = 30 * time.Second

// The Node fallback times each block with process.cpuUsage(), which adds up the CPU
// time of every thread in the process. Linux credits a thread other than the caller
// only at a scheduler tick, so a tick inside a block adds a whole tick (1 ms at
// 1000 Hz) of another thread's time. cmd/cicada/browser_cpu_node.js counts the
// measured blocks that ran more than ForeignCPUExcessMs above the median (a Go test
// keeps the two constants equal). A share above ForeignCPUShareLimit means the p99
// may be another thread's time, so the budget cannot be judged from it.
const ForeignCPUExcessMs = 0.9
const ForeignCPUShareLimit = 0.005

type HostSample struct {
	SampledAt         time.Time `json:"sampledAt"`
	Load1             float64   `json:"loadAverage1Minute"`
	CPUCount          int       `json:"availableCPUCount"`
	RunnableProcesses int       `json:"runnableProcesses"`
	Error             string    `json:"error,omitempty"`
}

func ParseLoad(data []byte) (float64, int, error) {
	fields := strings.Fields(string(data))
	if len(fields) < 4 {
		return 0, 0, fmt.Errorf("incomplete /proc/loadavg")
	}
	load, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, 0, err
	}
	runnableText, _, _ := strings.Cut(fields[3], "/")
	runnable, err := strconv.Atoi(runnableText)
	return load, runnable, err
}

func readHostSample() HostSample {
	sample := HostSample{SampledAt: time.Now(), CPUCount: runtime.NumCPU()}
	data, err := os.ReadFile("/proc/loadavg")
	if err == nil {
		sample.Load1, sample.RunnableProcesses, err = ParseLoad(data)
	}
	if err != nil {
		sample.Error = err.Error()
	}
	return sample
}

// Sample independently of browser edits so an edit timeout cannot hide a busy
// interval. The returned stop function also records a final sample and is safe
// to call again from defer after an early test failure.
func StartHostSampling() func() []HostSample {
	samples := []HostSample{readHostSample()}
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(HostSampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				samples = append(samples, readHostSample())
			case <-done:
				samples = append(samples, readHostSample())
				return
			}
		}
	}()
	var stopOnce sync.Once
	return func() []HostSample {
		stopOnce.Do(func() { close(done) })
		<-stopped
		return samples
	}
}

// A real-time soak needs scheduling headroom. Reserve half the available CPUs:
// any 1-minute load average above half the CPU count makes the host busy, even
// if later samples are quiet. Linux load also includes uninterruptible tasks;
// treating that pressure as busy conservatively avoids a false timing failure.
func HostBusy(samples []HostSample) bool {
	for _, sample := range samples {
		if sample.Error == "" && sample.CPUCount > 0 && sample.Load1 > float64(sample.CPUCount)/2 {
			return true
		}
	}
	return false
}

func CallbackClockResolutionMs(highResolution bool) float64 {
	if highResolution {
		// Use the existing high-resolution worklet allowance conservatively.
		return 0.1
	}
	// Date.now measures callback duration and gaps in whole milliseconds.
	return 1
}

func CPUClockResolutionMs(engine, clock string, observedMs float64) float64 {
	if engine == "Node V8 WebAssembly" {
		// The producer calibrates process.cpuUsage's user CPU clock. The label
		// names the reported unit; it does not imply millisecond resolution.
		return observedMs
	}
	if engine == "Windows Chrome AudioWorklet" {
		switch clock {
		case "AudioWorklet performance.now()":
			return CallbackClockResolutionMs(true)
		case "Date.now()":
			return CallbackClockResolutionMs(false)
		}
	}
	return 0 // No measurement evidence for an unknown clock.
}

type Result struct {
	TimingVerdict string `json:"timingVerdict"`
	TimingReason  string `json:"timingReason"`
	GatePass      bool   `json:"gatePass"`
}

func ClockCanResolveBudget(resolutionMs, budgetMs float64) bool {
	return resolutionMs > 0 && budgetMs > 0 && resolutionMs <= budgetMs
}

// ForeignCPUReason returns the reason to pass to Evaluate when the share of
// measured blocks above the median by ForeignCPUExcessMs is over the limit, and
// "" when the counter is clean enough to judge.
func ForeignCPUReason(share float64) string {
	if !(share > ForeignCPUShareLimit) {
		return ""
	}
	return fmt.Sprintf("foreign CPU in counter: %.2f%% of measured blocks ran more than %.1f ms above the median (limit %.2f%%)", 100*share, ForeignCPUExcessMs, 100*ForeignCPUShareLimit)
}

// Evaluate returns the timing verdict. Each non-empty extraReasons entry makes the
// timing inconclusive, like a busy host or a clock too coarse for the budget.
func Evaluate(timingPass, correctnessPass bool, samples []HostSample, clockResolutionMs, budgetMs float64, extraReasons ...string) Result {
	var reasons []string
	if HostBusy(samples) {
		reasons = append(reasons, "host busy: 1-minute load exceeded half the available CPU count")
	}
	if len(samples) == 0 {
		reasons = append(reasons, "host quietness unavailable: no load samples")
	}
	// Missing load evidence is not evidence of quietness.
	for _, sample := range samples {
		if sample.Error != "" || sample.CPUCount <= 0 {
			reasons = append(reasons, "host quietness unavailable: incomplete load samples")
			break
		}
	}
	if !ClockCanResolveBudget(clockResolutionMs, budgetMs) {
		reasons = append(reasons, fmt.Sprintf("clock resolution %.2f ms cannot resolve %.2f ms budget", clockResolutionMs, budgetMs))
	}
	for _, reason := range extraReasons {
		if reason != "" {
			reasons = append(reasons, reason)
		}
	}
	result := Result{TimingVerdict: "pass", TimingReason: strings.Join(reasons, "; ")}
	if len(reasons) != 0 {
		result.TimingVerdict = "inconclusive"
	} else if !timingPass {
		result.TimingVerdict = "fail"
	}
	result.GatePass = correctnessPass && (timingPass || result.TimingVerdict == "inconclusive")
	return result
}
