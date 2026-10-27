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

// Excess blocks. The Node fallback times each block with process.cpuUsage(), which adds
// up the CPU time of every thread in the process. Linux credits a thread other than the
// caller only at a scheduler tick, so a tick inside a block can add a whole tick (1 ms at
// 1000 Hz) of another thread's time. cmd/cicada/browser_cpu_node.js counts the excess
// blocks, the measured blocks that ran more than ExcessBlockMs above the median (a Go test
// keeps the two constants equal). Another thread's time and slow render work both make
// them, so the count cannot tell the causes apart. It explains a failed budget
// (EvaluateCPU) and never excuses one.
const ExcessBlockMs = 0.9
const ExcessBlockShareLimit = 0.005

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

func Evaluate(timingPass, correctnessPass bool, samples []HostSample, clockResolutionMs, budgetMs float64) Result {
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
	result := Result{TimingVerdict: "pass", TimingReason: strings.Join(reasons, "; ")}
	if len(reasons) != 0 {
		result.TimingVerdict = "inconclusive"
	} else if !timingPass {
		result.TimingVerdict = "fail"
	}
	result.GatePass = correctnessPass && (timingPass || result.TimingVerdict == "inconclusive")
	return result
}

// ExcessBlockNote returns the sentence to add to a failed CPU timing when the share of
// excess blocks is over ExcessBlockShareLimit, and "" when it is not.
func ExcessBlockNote(share float64) string {
	if !(share > ExcessBlockShareLimit) {
		return ""
	}
	return fmt.Sprintf("%.2f%% of measured blocks ran more than %.1f ms above the median, over the %.2f%% limit for a clean counter; firstExcessBlocks in the CPU report lists them: evenly spaced indices suggest another thread's CPU time in the counter, indices at note or bar starts suggest render work", 100*share, ExcessBlockMs, 100*ExcessBlockShareLimit)
}

// EvaluateCPU is Evaluate for the CPU budget of the Node fallback. excessBlockShare is the
// share of excess blocks in the same report (0 when the engine reports none). It fails
// closed: over ExcessBlockShareLimit it adds ExcessBlockNote to a failed verdict and
// changes nothing else. Another thread's time can only add to a block, so it can turn a
// pass into a failure but not the reverse, and a pass stays a pass. The count cannot show
// that the excess blocks were not render work, so a failure stays a failure.
func EvaluateCPU(timingPass, correctnessPass bool, samples []HostSample, clockResolutionMs, budgetMs, excessBlockShare float64) Result {
	result := Evaluate(timingPass, correctnessPass, samples, clockResolutionMs, budgetMs)
	if note := ExcessBlockNote(excessBlockShare); note != "" && result.TimingVerdict == "fail" {
		if result.TimingReason != "" {
			result.TimingReason += "; "
		}
		result.TimingReason += note
	}
	return result
}
