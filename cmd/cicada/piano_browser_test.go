//go:build browser

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBrowserPianoCPUReport measures the shipping worklet with eight sustained
// keys and repeated hammer strikes. Renderer counters use the existing coarse
// AudioWorklet clock fallback; the 0.67 ms callback budget is unchanged.
func TestBrowserPianoCPUReport(t *testing.T) {
	if os.Getenv("CICADA_BROWSER") != "windows" {
		t.Skip("set CICADA_BROWSER=windows to measure Windows Chrome AudioWorklet CPU")
	}
	// Use a separate Chrome profile so cleanup cannot stop another test's
	// browser processes.
	priorProfile, priorWSL := windowsBrowserProfile, windowsBrowserProfileWSL
	profileName := fmt.Sprintf("cicada-piano-cpu-%d", os.Getpid())
	windowsBrowserProfile = priorProfile[:strings.LastIndex(priorProfile, `\`)+1] + profileName
	windowsBrowserProfileWSL = filepath.Join(filepath.Dir(priorWSL), profileName)
	t.Cleanup(func() { windowsBrowserProfile, windowsBrowserProfileWSL = priorProfile, priorWSL })
	source := []byte(`cicada 2
title "Modeled piano CPU fixture"
tempo 120
track grand piano {
  level = -18db
  sustain = 1
}
pattern silence notes steps=16 {
  . . . . | . . . . | . . . . | . . . .
}
scene sustained {
  grand = silence
}
song { sustained*999 }
`)
	server := startBrowserStudio(t, source, nil)
	chrome := startBrowserChrome(t, server)
	chrome.setViewport(1440, 1000)
	chrome.navigate("http://" + browserStudioAddress + "/")
	chrome.eval(`(()=>{const select=document.getElementById('audio-mode');select.value='browser';select.dispatchEvent(new Event('change',{bubbles:true}));return true})()`)
	chrome.waitFor("!document.getElementById('start-audio').hidden", 5*time.Second)
	chrome.click("#start-audio")
	chrome.waitFor("window.cicadaBrowserAudio.context?.state==='running' && document.getElementById('studio-status').textContent.includes('Browser audio ready')", 20*time.Second)
	chrome.eval(`(()=>{
  window.__pianoCPU={faults:0,overloads:0,noteOns:0,keys:new Set(),chordStrikes:0};
  window.cicadaBrowserAudio.onMessage(message=>{
    const state=window.__pianoCPU;
    if(message.kind===7)state.faults++;
    if(message.kind===6)state.overloads++;
    if(message.kind===3){state.noteOns++;state.keys.add(message.a)}
  });
  return true;
})()`)
	chrome.click("#transport-button")
	chrome.waitFor("window.cicadaBrowserAudio.playing===true", 5*time.Second)
	chrome.eval(`(()=>{
  const strike=()=>{
    window.cicadaBrowserAudio.sendCommands([24,36,48,55,60,64,67,72].map(note=>({op:12,track:0,arg0:note|(120<<8)})));
    window.__pianoCPU.chordStrikes++;
  };
  strike();window.__pianoCPU.timer=setInterval(strike,250);return true;
})()`)
	t.Cleanup(func() {
		chrome.eval("clearInterval(window.__pianoCPU?.timer);window.cicadaBrowserAudio.stop(true);true")
	})
	// A fresh profile starts short-lived renderer processes. Let startup and
	// V8 compilation settle before requiring a stable CPU-counter window.
	time.Sleep(30 * time.Second)
	attempt := 0
sampleWindow:
	attempt++
	beforeCPU := windowsChromeRendererCPU(t)
	chrome.eval("window.cicadaBrowserAudio.node.port.postMessage({t:'p'});true")
	beforeValue := chrome.eval(`(async()=>{const m=await window.cicadaBrowserAudio.requestMetrics();return {samples:m.callbackSamples,strikes:window.__pianoCPU.chordStrikes}})()`)
	var before struct {
		Samples int `json:"samples"`
		Strikes int `json:"strikes"`
	}
	if err := json.Unmarshal(beforeValue, &before); err != nil {
		t.Fatalf("decode initial piano CPU counters %s: %v", beforeValue, err)
	}
	time.Sleep(10 * time.Second)
	value := chrome.eval(`(async()=>{
  const m=await window.cicadaBrowserAudio.requestMetrics(),s=window.__pianoCPU;
  return {clock:m.clock,p99:m.callbackP99Ms,max:m.maxCallbackDurationMs,samples:m.callbackSamples,
    quantumMs:m.q,durationExceedances:m.callbackDurationExceedances,gapExceedances:m.callbackGapExceedances,
    underruns:m.underruns,memoryBytes:m.memoryBytes,faults:s.faults+(window.cicadaBrowserAudio.playing?0:1),
    overloads:s.overloads,noteOns:s.noteOns,distinctKeys:s.keys.size,strikes:s.chordStrikes};
})()`)
	afterCPU := windowsChromeRendererCPU(t)
	var metrics struct {
		Clock               bool    `json:"clock"`
		P99                 float64 `json:"p99"`
		Max                 float64 `json:"max"`
		Samples             int     `json:"samples"`
		QuantumMs           float64 `json:"quantumMs"`
		DurationExceedances int     `json:"durationExceedances"`
		GapExceedances      int     `json:"gapExceedances"`
		Underruns           int     `json:"underruns"`
		Memory              int     `json:"memoryBytes"`
		Faults              int     `json:"faults"`
		Overloads           int     `json:"overloads"`
		NoteOns             int     `json:"noteOns"`
		DistinctKeys        int     `json:"distinctKeys"`
		Strikes             int     `json:"strikes"`
	}
	if err := json.Unmarshal(value, &metrics); err != nil {
		t.Fatalf("decode piano worklet metrics %s: %v", value, err)
	}
	if metrics.Samples <= before.Samples || metrics.DistinctKeys != 8 || metrics.NoteOns < 8 || metrics.Strikes-before.Strikes < 16 {
		t.Fatalf("piano CPU workload did not run: before=%+v after=%+v", before, metrics)
	}
	cpuMs, totalCPUMs, maxPID, intervalMs, stable := windowsChromeRendererCPUMsPerCallback(beforeCPU, afterCPU, metrics.QuantumMs)
	if !stable || maxPID == 0 {
		if attempt < 3 {
			t.Logf("discarding unstable CPU-counter window %d: renderers=%d..%d", attempt, beforeCPU.Processes, afterCPU.Processes)
			goto sampleWindow
		}
		t.Fatalf("piano CPU window lacks a stable renderer process set: before=%d after=%d", beforeCPU.Processes, afterCPU.Processes)
	}
	budgetMs, budgetMetric := cpuMs, "maximum Windows Chrome renderer CPU milliseconds per AudioWorklet callback"
	clock := "Date.now()"
	if metrics.Clock {
		budgetMs, budgetMetric, clock = metrics.P99, "AudioWorklet callback p99", "AudioWorklet performance.now()"
	}
	report := map[string]any{
		"engine": "Windows Chrome AudioWorklet", "voice": "modeled grand piano", "expectedVoices": 8,
		"sampleRate": 48000, "blockFrames": 128, "sustain": 1, "strikeIntervalMs": 250, "velocity": 120,
		"clock": clock, "p99": metrics.P99, "maxCallbackDurationMs": metrics.Max,
		"cpuBudgetMetric": budgetMetric, "cpuBudgetMs": budgetMs, "cpuLimitMsPerCallback": .67,
		"cpuMsPerCallback": cpuMs, "totalRendererCPUMsPerCallback": totalCPUMs,
		"rendererCPUMsPerExpectedVoicePerBlock": cpuMs / 8,
		"processCounterIntervalMs":              intervalMs, "maxRendererProcessId": maxPID,
		"rendererProcessesBefore": beforeCPU.Processes, "rendererProcessesAfter": afterCPU.Processes,
		"counterWindowAttempts": attempt,
		"callbackSamples":       metrics.Samples, "workletSamplesInWindow": metrics.Samples - before.Samples,
		"workletExpectedSamplesInCounterInterval": intervalMs / metrics.QuantumMs,
		"chordStrikesInWindow":                    metrics.Strikes - before.Strikes, "receivedNoteOns": metrics.NoteOns,
		"receivedDistinctKeys": metrics.DistinctKeys, "faults": metrics.Faults, "overloads": metrics.Overloads,
		"underruns": metrics.Underruns, "durationExceedances": metrics.DurationExceedances,
		"gapExceedances": metrics.GapExceedances, "memoryBytes": metrics.Memory,
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "build", "piano-browser-cpu-report.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("Windows Chrome piano: expected_voices=8 metric=%s used=%.4fms/128-frame-block p99=%.4fms max=%.2fms renderer_cpu=%.4fms/block renderer_cpu_per_expected_voice=%.4fms/block strikes=%d callbacks=%d faults=%d overloads=%d underruns=%d limit=0.67ms report=%s", budgetMetric, budgetMs, metrics.P99, metrics.Max, cpuMs, cpuMs/8, metrics.Strikes-before.Strikes, metrics.Samples-before.Samples, metrics.Faults, metrics.Overloads, metrics.Underruns, path)
	if metrics.Faults != 0 || metrics.Overloads != 0 || budgetMs > .67 {
		t.Fatalf("piano CPU gate failed: faults=%d overloads=%d %s=%.4fms/block limit=0.67ms", metrics.Faults, metrics.Overloads, budgetMetric, budgetMs)
	}
}
