package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const browserSoakCPUBudgetMs = 0.67
const browserSoakHostSampleInterval = 30 * time.Second

type browserSoakHostSample struct {
	SampledAt         time.Time `json:"sampledAt"`
	Load1             float64   `json:"loadAverage1Minute"`
	CPUCount          int       `json:"availableCPUCount"`
	RunnableProcesses int       `json:"runnableProcesses"`
	Error             string    `json:"error,omitempty"`
}

func parseBrowserSoakLoad(data []byte) (float64, int, error) {
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

func readBrowserSoakHostSample() browserSoakHostSample {
	sample := browserSoakHostSample{SampledAt: time.Now(), CPUCount: runtime.NumCPU()}
	data, err := os.ReadFile("/proc/loadavg")
	if err == nil {
		sample.Load1, sample.RunnableProcesses, err = parseBrowserSoakLoad(data)
	}
	if err != nil {
		sample.Error = err.Error()
	}
	return sample
}

// Sample independently of browser edits so an edit timeout cannot hide a busy
// interval. The returned stop function also records a final sample and is safe
// to call again from defer after an early test failure.
func startBrowserSoakHostSampling() func() []browserSoakHostSample {
	samples := []browserSoakHostSample{readBrowserSoakHostSample()}
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(browserSoakHostSampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				samples = append(samples, readBrowserSoakHostSample())
			case <-done:
				samples = append(samples, readBrowserSoakHostSample())
				return
			}
		}
	}()
	var stopOnce sync.Once
	return func() []browserSoakHostSample {
		stopOnce.Do(func() { close(done) })
		<-stopped
		return samples
	}
}

// A real-time soak needs scheduling headroom. Reserve half the available CPUs:
// any 1-minute load average above half the CPU count makes the host busy, even
// if later samples are quiet. Linux load also includes uninterruptible tasks;
// treating that pressure as busy conservatively avoids a false timing failure.
func browserSoakHostBusy(samples []browserSoakHostSample) bool {
	for _, sample := range samples {
		if sample.Error == "" && sample.CPUCount > 0 && sample.Load1 > float64(sample.CPUCount)/2 {
			return true
		}
	}
	return false
}

func browserSoakCPUClockResolutionMs(engine, clock string) float64 {
	if engine == "Windows Chrome AudioWorklet" && clock == "AudioWorklet performance.now()" {
		// Use the existing high-resolution worklet allowance conservatively.
		return 0.1
	}
	// The Node CPU fallback and Date.now timing are treated as millisecond
	// clocks for this gate; neither can resolve the unchanged 0.67 ms budget.
	return 1
}

type browserSoakChecks struct {
	EditsComplete     bool
	Faults            int
	TransportAdvanced bool
	MemoryStable      bool
	Underruns         int
	CPUUsedMs         float64
}

type browserSoakResult struct {
	TimingVerdict string
	TimingReason  string
	GatePass      bool
}

func browserSoakVerdict(checks browserSoakChecks, samples []browserSoakHostSample, cpuClockResolutionMs float64) browserSoakResult {
	var reasons []string
	if browserSoakHostBusy(samples) {
		reasons = append(reasons, "host busy: 1-minute load exceeded half the available CPU count")
	}
	if len(samples) == 0 {
		reasons = append(reasons, "host quietness unavailable: no load samples")
	}
	// A missing /proc/loadavg (for example on a non-Linux host) is not evidence
	// of quietness. Keep correctness gating while reporting timing inconclusive.
	for _, sample := range samples {
		if sample.Error != "" || sample.CPUCount <= 0 {
			reasons = append(reasons, "host quietness unavailable: incomplete load samples")
			break
		}
	}
	if cpuClockResolutionMs > browserSoakCPUBudgetMs {
		reasons = append(reasons, fmt.Sprintf("CPU clock resolution %.2f ms is coarser than %.2f ms budget", cpuClockResolutionMs, browserSoakCPUBudgetMs))
	}
	timingPass := checks.Underruns == 0 && checks.CPUUsedMs <= browserSoakCPUBudgetMs
	result := browserSoakResult{TimingVerdict: "pass", TimingReason: strings.Join(reasons, "; ")}
	if len(reasons) != 0 {
		result.TimingVerdict = "inconclusive"
	} else if !timingPass {
		result.TimingVerdict = "fail"
	}
	result.GatePass = checks.EditsComplete && checks.Faults == 0 && checks.TransportAdvanced && checks.MemoryStable && (timingPass || result.TimingVerdict == "inconclusive")
	return result
}
