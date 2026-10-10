// The demo's Node runner uses the same host sampling and timing policy as the
// Go browser tests. Start sampling before warmup, then send the final report
// on stdin to receive the verdict and host evidence on stdout.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"m31labs.dev/cicada/internal/soaktiming"
)

type demoSoakReport struct {
	Underruns                int      `json:"underruns"`
	Faults                   int      `json:"faults"`
	MemoryAfterWarmupBytes   int      `json:"memoryAfterWarmupBytes"`
	MemoryPeakBytes          int      `json:"memoryPeakBytes"`
	PlayheadAdvanced         bool     `json:"playheadAdvanced"`
	CallbackSamples          int      `json:"callbackSamples"`
	MessagesDrained          int      `json:"messagesDrained"`
	Errors                   []string `json:"errors"`
	BrowserErrors            []string `json:"browserErrors"`
	AudioWorkletHighResClock bool     `json:"audioWorkletHighResClock"`
	QuantumMs                float64  `json:"quantumMs"`
}

func (r demoSoakReport) verdict(samples []soaktiming.HostSample) soaktiming.Result {
	correctnessPass := r.Faults == 0 && r.MemoryPeakBytes == r.MemoryAfterWarmupBytes && r.PlayheadAdvanced && r.CallbackSamples > 1000 && r.MessagesDrained > 0 && len(r.Errors) == 0 && len(r.BrowserErrors) == 0
	// The demo gates callback underruns against a quantum, not the separate
	// 0.67 ms CPU budget. A 1 ms clock can still resolve a 128-frame quantum.
	resolution := soaktiming.CallbackClockResolutionMs(r.AudioWorkletHighResClock)
	return soaktiming.Evaluate(r.Underruns == 0, correctnessPass, samples, resolution, r.QuantumMs)
}

func run(input io.Reader, output io.Writer, start func() func() []soaktiming.HostSample) error {
	stop := start()
	defer stop()
	encoder := json.NewEncoder(output)
	if err := encoder.Encode(map[string]bool{"ready": true}); err != nil {
		return err
	}
	var report demoSoakReport
	if err := json.NewDecoder(input).Decode(&report); err != nil {
		if err == io.EOF {
			// The runner may stop sampling after an earlier correctness failure.
			return nil
		}
		return err
	}
	samples := stop()
	return encoder.Encode(struct {
		soaktiming.Result
		HostLoadSamples               []soaktiming.HostSample `json:"hostLoadSamples"`
		HostBusy                      bool                    `json:"hostBusy"`
		HostLoadSampleIntervalSeconds int                     `json:"hostLoadSampleIntervalSeconds"`
		ClockResolutionMs             float64                 `json:"clockResolutionMs"`
	}{report.verdict(samples), samples, soaktiming.HostBusy(samples), int(soaktiming.HostSampleInterval.Seconds()), soaktiming.CallbackClockResolutionMs(report.AudioWorkletHighResClock)})
}

func main() {
	if err := run(os.Stdin, os.Stdout, soaktiming.StartHostSampling); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
