//go:build browser_soak

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

func TestBrowserSoak(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	// Keep the transport running beyond the 30-minute measurement window by
	// extending two existing song entries within the notation's 999-bar limit.
	sourceText := strings.Replace(string(source), "  main*8\n  outro*8", "  main*999\n  main*999", 1)
	if sourceText == string(source) {
		t.Fatal("could not extend the existing song entries for the real-time soak")
	}
	source = []byte(sourceText)
	server := startBrowserStudio(t, source, nil)
	chrome := startBrowserChrome(t, server)
	chrome.setViewport(1440, 1000)
	chrome.navigate("http://" + browserStudioAddress + "/")
	chrome.eval(`(()=>{const select=document.getElementById('audio-mode');select.value='browser';select.dispatchEvent(new Event('change',{bubbles:true}));return true})()`)
	chrome.waitFor("!document.getElementById('start-audio').hidden", 5*time.Second)
	chrome.eval(`window.__soakBrowserErrors=[];window.addEventListener('error',event=>window.__soakBrowserErrors.push(event.message));window.addEventListener('unhandledrejection',event=>window.__soakBrowserErrors.push(String(event.reason)));true`)
	chrome.click("#start-audio")
	waitFor := func(expression string, timeout time.Duration) bool {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			var ready bool
			if json.Unmarshal(chrome.eval(expression), &ready) == nil && ready {
				return true
			}
			time.Sleep(100 * time.Millisecond)
		}
		return false
	}
	if !waitFor("window.cicadaBrowserAudio.context?.state==='running'", 20*time.Second) {
		detail := chrome.eval(`JSON.stringify({state:window.cicadaBrowserAudio.context?.state,status:document.getElementById('studio-status').textContent,node:!!window.cicadaBrowserAudio.node,revision:window.cicadaBrowserAudio.revision,errors:window.__soakBrowserErrors})`)
		t.Fatalf("Browser AudioContext did not start: %s", detail)
	}
	if !waitFor("document.getElementById('studio-status').textContent.includes('Browser audio ready')", 15*time.Second) {
		detail := chrome.eval(`JSON.stringify({state:window.cicadaBrowserAudio.context?.state,status:document.getElementById('studio-status').textContent,startText:document.getElementById('start-audio').textContent,startDisabled:document.getElementById('start-audio').disabled,node:!!window.cicadaBrowserAudio.node,revision:window.cicadaBrowserAudio.revision,readyPromise:!!window.cicadaBrowserAudio.readyPromise,errors:window.__soakBrowserErrors})`)
		t.Fatalf("Browser audio did not become ready: %s", detail)
	}
	chrome.eval(`(()=>{window.__soakFaults=0;window.__soakPlayhead=0;window.__soakMessages=0;window.__soakPortMessages=0;window.__soakGCCount=0;window.__soakGCDurationMs=0;window.__soakGCObserverAvailable=false;window.__soakLongTaskCount=0;window.__soakLongTaskMaxMs=0;window.__soakLongTaskObserverAvailable=false;const port=window.cicadaBrowserAudio.node.port,receive=port.onmessage;port.onmessage=event=>{window.__soakPortMessages++;receive.call(port,event)};window.cicadaBrowserAudio.onMessage(message=>{window.__soakMessages++;if(message.kind===7)window.__soakFaults++;if(message.kind===1)window.__soakPlayhead=message.tick});try{new PerformanceObserver(list=>{for(const entry of list.getEntries()){window.__soakGCCount++;window.__soakGCDurationMs+=entry.duration}}).observe({entryTypes:['gc']});window.__soakGCObserverAvailable=true}catch(_){}try{new PerformanceObserver(list=>{for(const entry of list.getEntries()){window.__soakLongTaskCount++;window.__soakLongTaskMaxMs=Math.max(window.__soakLongTaskMaxMs,entry.duration)}}).observe({entryTypes:['longtask']});window.__soakLongTaskObserverAvailable=true}catch(_){}return true})()`)
	chrome.click("#transport-button")
	chrome.waitFor("window.cicadaBrowserAudio.playing===true", 5*time.Second)

	cpuData, err := os.ReadFile(filepath.Join("..", "..", "build", "browser-cpu-report.json"))
	if err != nil {
		t.Fatal("run budget-browser before the soak to provide the Node CPU fallback report:", err)
	}
	var cpuReport struct {
		Engine           string  `json:"engine"`
		P99              float64 `json:"p99"`
		CPUMsPerCallback float64 `json:"cpuMsPerCallback"`
		BudgetMs         float64 `json:"cpuBudgetMs"`
		BudgetMetric     string  `json:"cpuBudgetMetric"`
	}
	expectedCPUEngine := "Node V8 WebAssembly"
	if os.Getenv("CICADA_BROWSER") == "windows" {
		expectedCPUEngine = "Windows Chrome AudioWorklet"
	}
	if err := json.Unmarshal(cpuData, &cpuReport); err != nil || cpuReport.Engine != expectedCPUEngine {
		t.Fatalf("invalid browser CPU report: expected %q, got %s (%v)", expectedCPUEngine, cpuData, err)
	}

	type snapshot struct {
		CallbackSamples            int     `json:"callbackSamples"`
		DurationExceedances        int     `json:"callbackDurationExceedances"`
		GapExceedances             int     `json:"callbackGapExceedances"`
		Underruns                  int     `json:"underruns"`
		MaxDurationMs              float64 `json:"maxCallbackDurationMs"`
		CallbackP99Ms              float64 `json:"callbackP99Ms"`
		QuantumMs                  float64 `json:"q"`
		ReportedLatencyMs          float64 `json:"l"`
		ClockResolutionAllowanceMs float64 `json:"cp"`
		CallbackDurationLimitMs    float64 `json:"dl"`
		CallbackGapLimitMs         float64 `json:"gl"`
		MemoryBytes                int     `json:"memoryBytes"`
		PeakMemoryBytes            int     `json:"peakMemoryBytes"`
		InstanceCount              int     `json:"instanceCount"`
		MessageCount               int     `json:"workletPortMessages"`
		DecodedMessageCount        int     `json:"decodedMessages"`
		GCCount                    int     `json:"gcCount"`
		GCDurationMs               float64 `json:"gcDurationMs"`
		GCObserverAvailable        bool    `json:"gcObserverAvailable"`
		LongTaskCount              int     `json:"longTaskCount"`
		LongTaskMaxMs              float64 `json:"longTaskMaxMs"`
		LongTaskObserverAvailable  bool    `json:"longTaskObserverAvailable"`
		JSHeapUsedBytes            int64   `json:"jsHeapUsedBytes"`
		Faults                     int     `json:"faults"`
		Clock                      bool    `json:"clock"`
		Playing                    bool    `json:"playing"`
		Playhead                   int64   `json:"playhead"`
		OutputTimeline             struct {
			Available   bool           `json:"available"`
			Samples     int            `json:"samples"`
			Misses      int            `json:"misses"`
			MaxLagMs    float64        `json:"maxLagMs"`
			MaxExcessMs float64        `json:"maxExcessMs"`
			LastMiss    map[string]any `json:"lastMiss"`
			LatencyMs   float64        `json:"latencyMs"`
			QuantumMs   float64        `json:"quantumMs"`
			LimitMs     float64        `json:"limitMs"`
		} `json:"outputTimeline"`
	}
	readMetrics := func() snapshot {
		t.Helper()
		value := chrome.eval(`(async()=>{const metrics=await window.cicadaBrowserAudio.requestMetrics();return {...metrics,faults:window.__soakFaults,playing:window.cicadaBrowserAudio.playing,playhead:window.__soakPlayhead,workletPortMessages:window.__soakPortMessages,decodedMessages:window.__soakMessages,gcCount:window.__soakGCCount,gcDurationMs:window.__soakGCDurationMs,gcObserverAvailable:window.__soakGCObserverAvailable,longTaskCount:window.__soakLongTaskCount,longTaskMaxMs:window.__soakLongTaskMaxMs,longTaskObserverAvailable:window.__soakLongTaskObserverAvailable,jsHeapUsedBytes:performance.memory?.usedJSHeapSize||0}})()`)
		var result snapshot
		if err := json.Unmarshal(value, &result); err != nil {
			t.Fatalf("decode browser soak metrics %s: %v", value, err)
		}
		return result
	}
	const soakWarmup = 60 * time.Second
	time.Sleep(soakWarmup)
	warmup := readMetrics()
	if warmup.CallbackSamples < 1000 {
		t.Fatalf("AudioWorklet timing histogram has too few warmup callbacks: %d", warmup.CallbackSamples)
	}
	playbackStats := chrome.eval("window.cicadaBrowserAudio.context.playbackStats?.toJSON?.()??null")
	t.Logf("browser soak 60-second warmup: callback underruns=%d; output timeline exceedances=%d/%d samples; callback p99=%.2f ms; Chrome playbackStats diagnostic=%s", warmup.Underruns, warmup.OutputTimeline.Misses, warmup.OutputTimeline.Samples, warmup.CallbackP99Ms, playbackStats)
	afterWarmup := warmup
	memoryPeak := warmup.MemoryBytes
	chrome.eval(`window.cicadaBrowserAudio.node.port.postMessage({t:'p'});true`)
	measurementBase := readMetrics()
	started := time.Now()
	const editCount = 180
	stepEdits, sourceEdits := 0, 0
	stepEditFailures, sourceEditFailures := 0, 0
	stepEditFailureDetails := make([]map[string]any, 0)
	var current snapshot
	for edit := 1; edit <= editCount; edit++ {
		due := started.Add(time.Duration(edit) * 10 * time.Second)
		if delay := time.Until(due); delay > 0 {
			time.Sleep(delay)
		}
		step := (edit - 1) % 16
		selector := fmt.Sprintf(`.drum-cell.step-edit[data-pattern="beat-a"][data-lane="bd"][data-step="%d"]`, step)
		if !waitFor(`!!document.querySelector(`+strconvQuote(selector)+`)`, 5*time.Second) {
			stepEditFailures++
			stepEditFailureDetails = append(stepEditFailureDetails, map[string]any{"edit": edit, "step": step, "reason": "grid cell unavailable", "selector": selector})
		} else {
			before := chrome.eval(`(()=>{const cell=document.querySelector(` + strconvQuote(selector) + `);return cell?{revision:document.body.dataset.revision,filled:!!cell.querySelector('.fill')}:null})()`)
			var beforeState struct {
				Revision string `json:"revision"`
				Filled   bool   `json:"filled"`
			}
			if err := json.Unmarshal(before, &beforeState); err != nil || beforeState.Revision == "" {
				stepEditFailures++
				stepEditFailureDetails = append(stepEditFailureDetails, map[string]any{"edit": edit, "step": step, "reason": "could not read pre-edit grid state", "state": string(before), "error": fmt.Sprint(err)})
			} else {
				chrome.eval(`(()=>{const cell=document.querySelector(` + strconvQuote(selector) + `);if(!cell)return false;window.__soakRevision=document.body.dataset.revision;cell.click();return true})()`)
				postcondition := "document.body.dataset.revision!==window.__soakRevision && !document.getElementById('save-source').disabled && !!document.querySelector(" + strconvQuote(selector+" .fill") + ") !== " + fmt.Sprint(beforeState.Filled)
				if waitFor(postcondition, 10*time.Second) {
					stepEdits++
				} else {
					stepEditFailures++
					after := chrome.eval(`(()=>{const cell=document.querySelector(` + strconvQuote(selector) + `);return {revision:document.body.dataset.revision,filled:!!cell?.querySelector('.fill'),saveDisabled:document.getElementById('save-source').disabled,status:document.getElementById('studio-status').textContent}})()`)
					stepEditFailureDetails = append(stepEditFailureDetails, map[string]any{"edit": edit, "step": step, "reason": "revision and grid state did not both settle within 10 seconds", "before": beforeState, "after": string(after)})
					t.Logf("soak step edit %d (step %d) failed: before=%s after=%s", edit, step, before, after)
				}
			}
		}

		if edit%6 == 0 {
			contents, err := os.ReadFile(server.score)
			sourceReady, failure := err == nil, ""
			if err != nil {
				failure = "read score: " + err.Error()
			}
			lines := strings.Split(string(contents), "\n")
			foundTitle := false
			for i, line := range lines {
				if strings.HasPrefix(line, "title ") {
					lines[i] = fmt.Sprintf("title \"First acid soak %02d\"", edit/6)
					foundTitle = true
					break
				}
			}
			if sourceReady && !foundTitle {
				sourceReady, failure = false, "score source has no title"
			}
			updated, marshalErr := json.Marshal(strings.Join(lines, "\n"))
			if sourceReady && marshalErr != nil {
				sourceReady, failure = false, "encode source: "+marshalErr.Error()
			}
			if sourceReady && !waitFor("!document.getElementById('save-source').disabled", 5*time.Second) {
				sourceReady, failure = false, "save button did not become available"
			}
			if sourceReady {
				chrome.eval(`(()=>{window.__soakRevision=document.body.dataset.revision;document.getElementById('edit-source').click();const editor=document.getElementById('source-editor');editor.value=` + string(updated) + `;editor.dispatchEvent(new Event('input',{bubbles:true}));document.getElementById('save-source').click();return true})()`)
				if !waitFor("document.body.dataset.revision!==window.__soakRevision && document.getElementById('studio-status').textContent.includes('queued') && !document.getElementById('save-source').disabled", 15*time.Second) {
					sourceReady, failure = false, "source save did not stage and queue"
				}
			}
			if sourceReady {
				sourceEdits++
			} else {
				sourceEditFailures++
				ui := chrome.eval(`({status:document.getElementById('studio-status').textContent,revision:document.body.dataset.revision,saveDisabled:document.getElementById('save-source').disabled,editing:!document.getElementById('editor-shell').hidden})()`)
				t.Logf("soak source edit %d failed: %s; UI=%s", edit/6, failure, ui)
				chrome.eval(`(()=>{const discard=document.getElementById('discard-source');if(discard&&!discard.hidden)discard.click();return true})()`)
			}
		}
		if edit%6 == 0 || edit == editCount {
			current = readMetrics()
			if current.PeakMemoryBytes > memoryPeak {
				memoryPeak = current.PeakMemoryBytes
			}
			t.Logf("browser soak progress: %d/30 min, step edits=%d (failures=%d), source edits=%d (failures=%d), worklet underruns=%d, faults=%d, memory=%d bytes", edit/6, stepEdits, stepEditFailures, sourceEdits, sourceEditFailures, current.Underruns, current.Faults, current.MemoryBytes)
			soFarUnderruns := current.Underruns - warmup.Underruns
			soFarTimelineMisses := current.OutputTimeline.Misses - warmup.OutputTimeline.Misses
			t.Logf("soak measurement so far: worklet underruns=%d, output timeline exceedances=%d", soFarUnderruns, soFarTimelineMisses)
		}
	}
	// A prepared edit and a short crossfade may retain extra instances. Wait for
	// the last swap, then require that only the active instance remains.
	if !waitFor(`(async()=> (await window.cicadaBrowserAudio.requestMetrics()).instanceCount===1)()`, 5*time.Second) {
		t.Error("staged or fading WASM instance was retained after the last edit")
	}
	current = readMetrics()
	if current.PeakMemoryBytes > memoryPeak {
		memoryPeak = current.PeakMemoryBytes
	}
	chrome.eval(`window.cicadaBrowserAudio.stop();true`)
	editsComplete := stepEdits == 180 && sourceEdits == 30
	soakUnderruns := current.Underruns - warmup.Underruns
	soakTimelineMisses := current.OutputTimeline.Misses - warmup.OutputTimeline.Misses
	transportAdvanced := current.Playing && current.Playhead > warmup.Playhead
	// At most four instances overlap: active, fading, prepared and loading.
	memoryLimit := 4 * afterWarmup.MemoryBytes
	memoryStable := afterWarmup.MemoryBytes != 0 && current.InstanceCount == 1 && current.MemoryBytes <= afterWarmup.MemoryBytes && memoryPeak <= memoryLimit
	cpuBudgetMs := cpuReport.P99
	cpuBudgetMetric := "Node V8 WebAssembly p99"
	if os.Getenv("CICADA_BROWSER") == "windows" {
		cpuBudgetMs = cpuReport.BudgetMs
		cpuBudgetMetric = cpuReport.BudgetMetric
	}
	cpuWithinLimit := cpuBudgetMs <= 0.67
	// The output-timeline lag is reported but does not gate: in headless Windows Chrome the
	// reported latency sits at the median of the measured lag, so a healthy silent worklet
	// exceeds the limit about half of the time (see the rowan-timeline evidence).
	soakPass := editsComplete && soakUnderruns == 0 && current.Faults == 0 && transportAdvanced && memoryStable && cpuWithinLimit
	clockUsed := "Date.now()"
	if current.Clock {
		clockUsed = "AudioWorklet performance.now()"
	}

	report := map[string]any{
		"wasmInstanceCount":              current.InstanceCount,
		"wasmTransientMemoryLimitBytes":  memoryLimit,
		"gatePass":                       soakPass,
		"requestedDurationSeconds":       1800,
		"actualDurationSeconds":          time.Since(started).Seconds(),
		"stepEdits":                      stepEdits,
		"stepEditFailures":               stepEditFailures,
		"stepEditFailureDetails":         stepEditFailureDetails,
		"sourceEdits":                    sourceEdits,
		"sourceEditFailures":             sourceEditFailures,
		"warmupSeconds":                  int(soakWarmup.Seconds()),
		"warmupUnderruns":                warmup.Underruns,
		"underruns":                      soakUnderruns,
		"underrunSignal":                 "One underrun per callback when duration exceeds one 128-frame quantum plus a 0.1 ms performance.now allowance (1 ms only when falling back to Date.now), or the callback-start gap exceeds reported AudioContext baseLatency plus outputLatency, one quantum, and the same clock allowance. Output timeline lag is measured as currentTime minus getOutputTimestamp().contextTime; an exceedance is lag above the reported latency plus one quantum.",
		"quantumMs":                      current.QuantumMs,
		"reportedContextLatencyMs":       current.ReportedLatencyMs,
		"clockResolutionAllowanceMs":     current.ClockResolutionAllowanceMs,
		"workletDurationThresholdMs":     current.CallbackDurationLimitMs,
		"workletGapThresholdMs":          current.CallbackGapLimitMs,
		"outputTimelineThresholdMs":      current.OutputTimeline.LimitMs,
		"outputTimelineMisses":           soakTimelineMisses,
		"outputTimelineSamples":          current.OutputTimeline.Samples - warmup.OutputTimeline.Samples,
		"outputTimelineAvailable":        current.OutputTimeline.Available,
		"outputTimelineMaxLagMs":         current.OutputTimeline.MaxLagMs,
		"outputTimelineMaxExcessMs":      current.OutputTimeline.MaxExcessMs,
		"outputTimelineLastMiss":         current.OutputTimeline.LastMiss,
		"thresholdDerivation":            "At 48 kHz, one 128-frame quantum is 128/48000 seconds = 2.667 ms. Worklet duration is limited to 2.667 ms plus a 0.1 ms performance.now allowance (1 ms only for the Date.now fallback). Callback-start gap is limited to reported baseLatency + outputLatency + 2.667 ms plus that same clock allowance. Output-timeline lag is currentTime minus getOutputTimestamp().contextTime and is limited to reported baseLatency + outputLatency + 2.667 ms.",
		"callbackP99Ms":                  current.CallbackP99Ms,
		"callbackSamples":                current.CallbackSamples - measurementBase.CallbackSamples,
		"callbackDurationExceedances":    current.DurationExceedances - measurementBase.DurationExceedances,
		"callbackGapExceedances":         current.GapExceedances - measurementBase.GapExceedances,
		"maxCallbackDurationMs":          current.MaxDurationMs,
		"playbackStatsDiagnostic":        string(playbackStats),
		"faults":                         current.Faults,
		"playheadTickAfterWarmup":        warmup.Playhead,
		"playheadTickFinal":              current.Playhead,
		"memoryAfterWarmupBytes":         afterWarmup.MemoryBytes,
		"memoryFinalBytes":               current.MemoryBytes,
		"memoryPeakAfterWarmupBytes":     memoryPeak,
		"memoryGrowthAfterWarmup":        memoryPeak - afterWarmup.MemoryBytes,
		"audioWorkletHighResClock":       current.Clock,
		"clockUsed":                      clockUsed,
		"workletPortMessagesAfterWarmup": current.MessageCount - warmup.MessageCount,
		"decodedMessagesAfterWarmup":     current.DecodedMessageCount - warmup.DecodedMessageCount,
		"gcEventsAfterWarmup":            current.GCCount - warmup.GCCount,
		"gcDurationMsAfterWarmup":        current.GCDurationMs - warmup.GCDurationMs,
		"gcObserverAvailable":            current.GCObserverAvailable,
		"longTasksAfterWarmup":           current.LongTaskCount - warmup.LongTaskCount,
		"longTaskMaxMsAfterWarmup":       current.LongTaskMaxMs,
		"longTaskObserverAvailable":      current.LongTaskObserverAvailable,
		"jsHeapUsedAfterWarmupBytes":     afterWarmup.JSHeapUsedBytes,
		"jsHeapUsedFinalBytes":           current.JSHeapUsedBytes,
		"processorTimingEngine":          cpuReport.Engine,
		"processorP99Ms":                 cpuReport.P99,
		"browserCPUUsedMsPerCallback":    cpuBudgetMs,
		"browserCPUBudgetMsPerCallback":  0.67,
		"browserCPUBudgetMetric":         cpuBudgetMetric,
	}
	bytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "build", "browser-soak-report.json")
	if err := os.WriteFile(path, append(bytes, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("browser soak complete: pass=%v duration=%.1fs step_edits=%d source_edits=%d worklet_underruns=%d duration_exceedances=%d gap_exceedances=%d output_timeline_exceedances=%d faults=%d memory=%d->%d bytes callbacks=%d callback_p99=%.2fms max_callback=%.2fms clock_high_res=%v CPU=%s metric=%s used=%.4fms/callback limit=0.67ms report=%s", soakPass, report["actualDurationSeconds"], stepEdits, sourceEdits, soakUnderruns, report["callbackDurationExceedances"], report["callbackGapExceedances"], soakTimelineMisses, current.Faults, afterWarmup.MemoryBytes, current.MemoryBytes, report["callbackSamples"], current.CallbackP99Ms, current.MaxDurationMs, current.Clock, cpuReport.Engine, cpuBudgetMetric, cpuBudgetMs, path)
	if soakUnderruns != 0 {
		t.Errorf("browser soak underruns: worklet=%d (output-timeline exceedances=%d, informational)", soakUnderruns, soakTimelineMisses)
	}
	if !editsComplete {
		t.Errorf("browser soak completed with edit failures: steps=%d/%d source=%d/%d step failures=%d source failures=%d", stepEdits, editCount, sourceEdits, editCount/6, stepEditFailures, sourceEditFailures)
	}
	if current.Faults != 0 {
		t.Errorf("browser soak recorded %d kernel faults", current.Faults)
	}
	if !transportAdvanced {
		t.Errorf("browser transport did not keep advancing through the soak: warmup playhead=%d final=%d playing=%v", warmup.Playhead, current.Playhead, current.Playing)
	}
	if !memoryStable {
		t.Errorf("WASM instances did not settle or exceeded the transient bound: warmup=%d peak=%d final=%d instances=%d limit=%d", afterWarmup.MemoryBytes, memoryPeak, current.MemoryBytes, current.InstanceCount, memoryLimit)
	}
	if !cpuWithinLimit {
		t.Errorf("browser CPU metric %.4f ms/callback exceeds 0.67 ms budget", cpuBudgetMs)
	}
}
