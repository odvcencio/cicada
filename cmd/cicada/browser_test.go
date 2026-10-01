//go:build browser

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowserParity(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	reference := nativeReference(t, source, 16, 48_000)
	server := startBrowserStudio(t, source, reference)
	chrome := startBrowserChrome(t, server)
	chrome.setViewport(1440, 1000)
	chrome.navigate("http://" + browserStudioAddress + "/")

	result := chrome.eval(`(async()=>{
  const ref = await (await fetch('/__test/reference')).arrayBuffer();
  const frames = new DataView(ref).getUint32(0, true);
  const logBytes = new DataView(ref).getUint32(4, true);
  const rate = 48000;
  const context = new OfflineAudioContext(2, frames, rate);
  await context.audioWorklet.addModule('/audio/cicada-processor.js');
  const module = await WebAssembly.compile(await (await fetch('/api/kernel.wasm')).arrayBuffer());
  const imageResponse = await fetch('/api/kernel-image?rate=48000');
  const image = await imageResponse.arrayBuffer();
  let readyResolve, readyReject;
  const ready = new Promise((resolve,reject)=>{readyResolve=resolve;readyReject=reject});
  const batches=[]; let returnedMessages=0;
  const node = new AudioWorkletNode(context,'cicada',{numberOfInputs:0,numberOfOutputs:1,outputChannelCount:[2],processorOptions:{m:module,i:image,r:imageResponse.headers.get('X-Cicada-Revision')}});
  node.port.onmessage = event => {
    const data=event.data;
    if(data.t==='r') readyResolve(data);
    else if(data.t==='e') readyReject(new Error(data.e));
    else if(data.t==='m') {
      batches.push(new Uint8Array(data.bytes,0,data.n).slice());
      node.port.postMessage({t:'b',bytes:data.bytes},[data.bytes]);
      returnedMessages++;
    }
  };
  node.connect(context.destination);
  const info=await ready;
  const command=new Uint8Array(24); command[0]=1; command[1]=255;
  node.port.postMessage({t:'c',bytes:command},[command.buffer]);
  const suspends=[];
  for(let callback=8;callback*128<frames;callback+=8)suspends.push(context.suspend(callback*128/rate));
  const rendering=context.startRendering();
  for(let index=0;index<suspends.length;index++){
    await suspends[index];
    while(returnedMessages<index+1)await new Promise(resolve=>setTimeout(resolve,0));
    await context.resume();
  }
  const rendered=await rendering;
  await new Promise(resolve=>setTimeout(resolve,100));
  const timingLength=batches.reduce((sum,batch)=>sum+batch.length,0);
  const log=new Uint8Array(timingLength); let at=0;
  for(const batch of batches){log.set(batch,at);at+=batch.length;}
  const expectedLog=new Uint8Array(ref,8+frames*8,logBytes);
  let firstDiff=-1;
  for(let i=0;i<Math.min(log.length,expectedLog.length);i++)if(log[i]!==expectedLog[i]){firstDiff=i;break;}
  if(firstDiff<0&&log.length!==expectedLog.length)firstDiff=Math.min(log.length,expectedLog.length);
  const rec=(bytes,i)=>Array.from(bytes.slice(Math.floor(i/16)*16,Math.floor(i/16)*16+16));
  const logSame=firstDiff<0;
  const referenceView=new DataView(ref); let peak=0,peakAt=-1;
  for(let frame=0;frame<frames;frame++) for(let channel=0;channel<2;channel++){
    const expected=referenceView.getFloat32(8+frame*8+channel*4,true);
    const actual=rendered.getChannelData(channel)[frame];
    const difference=Math.abs(expected-actual);
    if(difference>peak){peak=difference;peakAt=frame*2+channel;}
  }
  return {frames,blocks:frames/128,maxDifference:peak,peakAt,logSame,firstDiff,actualRecord:firstDiff>=0?rec(log,firstDiff):[],expectedRecord:firstDiff>=0?rec(expectedLog,firstDiff):[],logBytes:log.length,expectedLogBytes:logBytes,memoryBytes:info.mem,clock:info.c};
})()`)
	var report struct {
		Frames           int     `json:"frames"`
		Blocks           int     `json:"blocks"`
		MaxDifference    float64 `json:"maxDifference"`
		PeakAt           int     `json:"peakAt"`
		LogSame          bool    `json:"logSame"`
		LogBytes         int     `json:"logBytes"`
		ExpectedLogBytes int     `json:"expectedLogBytes"`
		FirstDiff        int     `json:"firstDiff"`
		ActualRecord     []byte  `json:"actualRecord"`
		ExpectedRecord   []byte  `json:"expectedRecord"`
		MemoryBytes      int     `json:"memoryBytes"`
		Clock            bool    `json:"clock"`
	}
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("decode browser parity report %s: %v", result, err)
	}
	parityReport := map[string]any{
		"fixture":             "examples/first-acid.cicada",
		"bars":                16,
		"sampleRate":          48_000,
		"frames":              report.Frames,
		"blocks":              report.Blocks,
		"maxSampleDifference": report.MaxDifference,
		"messageLogsEqual":    report.LogSame,
		"messageBytes":        report.LogBytes,
		"wasmMemoryBytes":     report.MemoryBytes,
		"audioWorkletClock":   report.Clock,
	}
	parityBytes, err := json.MarshalIndent(parityReport, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	parityPath := filepath.Join("..", "..", "build", "browser-parity-report.json")
	if err := os.WriteFile(parityPath, append(parityBytes, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("OfflineAudioContext parity: frames=%d blocks=%d max_sample_difference=%.9g at=%d messages_equal=%v message_bytes=%d wasm_memory=%d clock=%v", report.Frames, report.Blocks, report.MaxDifference, report.PeakAt, report.LogSame, report.LogBytes, report.MemoryBytes, report.Clock)
	if !report.LogSame || report.ExpectedLogBytes != report.LogBytes {
		t.Fatalf("browser/kernel message log differs from native: %+v", report)
	}
	if report.MaxDifference > 1e-6 {
		t.Fatalf("browser/native sample difference %.9g at sample %d exceeds 1e-6", report.MaxDifference, report.PeakAt)
	}
	if report.Frames == 0 || report.Blocks == 0 {
		t.Fatal("browser rendered no audio frames")
	}
	_ = server
}

func TestBrowserStudioFlow(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	server := startBrowserStudio(t, source, nil)
	chrome := startBrowserChrome(t, server)
	chrome.setViewport(390, 900)
	chrome.navigate("http://" + browserStudioAddress + "/")
	chrome.eval(`(()=>{const select=document.getElementById('audio-mode');select.value='browser';select.dispatchEvent(new Event('change',{bubbles:true}));return true})()`)
	chrome.waitFor("document.getElementById('audio-mode').value==='browser' && !document.getElementById('start-audio').hidden", 5*time.Second)
	chrome.screenshot("studio-browser-390-before-audio.png")
	chrome.setViewport(1440, 1000)
	chrome.waitFor("window.innerWidth===1440", 5*time.Second)
	chrome.screenshot("studio-browser-1440-before-audio.png")

	chrome.setViewportMode(390, 900, false)
	chrome.waitFor("window.innerWidth===390", 5*time.Second)
	chrome.eval(`new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve(true))))`)
	chrome.click("#start-audio")
	chrome.waitFor("window.cicadaBrowserAudio.context && window.cicadaBrowserAudio.context.state==='running' && document.getElementById('studio-status').textContent.includes('Browser audio ready')", 20*time.Second)
	chrome.click("#transport-button")
	chrome.waitFor("window.cicadaBrowserAudio.playing===true", 5*time.Second)
	chrome.waitFor("document.getElementById('audio-engine-status').textContent==='browser · AudioWorklet · 48k'", 5*time.Second)
	chrome.waitFor("(async()=>{const response=await fetch('/api/transport',{cache:'no-store'});return (await response.json()).activeBackend==='browser · AudioWorklet · 48k'})()", 5*time.Second)
	chrome.waitFor("document.getElementById('transport-position').textContent.startsWith('Bar 1')", 5*time.Second)
	chrome.screenshot("studio-browser-390-playing.png")
	chrome.setViewport(1440, 1000)
	chrome.waitFor("window.innerWidth===1440", 5*time.Second)
	chrome.eval(`new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve(true))))`)
	chrome.screenshot("studio-browser-1440-playing.png")

	chrome.eval(`(()=>{window.__m2messages=[];window.cicadaBrowserAudio.onMessage(message=>window.__m2messages.push(message));const port=window.cicadaBrowserAudio.node.port,post=port.postMessage.bind(port);window.__m2commands=[];port.postMessage=(data,transfer)=>{if(data?.t==='c'){const b=data.bytes;window.__m2commands.push({op:b[0],track:b[1],index:b[2]|b[3]<<8,arg0:b[4]|b[5]<<8|b[6]<<16|b[7]<<24,tick:Array.from(b.slice(16,24))})}return post(data,transfer)};return true})()`)
	chrome.eval(`document.querySelector('.scene-pad[data-scene="outro"]').click();true`)
	sceneDeadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(sceneDeadline) {
		var switched bool
		if json.Unmarshal(chrome.eval("window.__m2messages.some(message=>message.kind===5)"), &switched) == nil && switched {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var switched bool
	_ = json.Unmarshal(chrome.eval("window.__m2messages.some(message=>message.kind===5)"), &switched)
	if !switched {
		t.Fatalf("scene launch did not switch: %s", chrome.eval("({messages:window.__m2messages.slice(-12),commands:window.__m2commands,status:document.getElementById('studio-status').textContent,playing:window.cicadaBrowserAudio.playing,sceneIndex:document.querySelector('.scene-pad[data-scene=\"outro\"]').dataset.sceneIndex,tick:window.__m2messages.filter(m=>m.kind===1).slice(-1)[0]?.tick,currentTime:window.cicadaBrowserAudio.context.currentTime})"))
	}
	chrome.eval(`document.querySelector('.scene-slot[data-track="bass"][data-pattern="bass-a"]').click();true`)
	chrome.waitFor("window.__m2messages.some(message=>message.kind===5 && message.track===0)", 45*time.Second)
	chrome.eval(`document.querySelector('.song-block[data-scene="outro"] .song-play').click();true`)
	chrome.waitFor("window.__m2messages.some(message=>message.kind===1 && message.tick>=8*3840)", 5*time.Second)

	chrome.eval(`(()=>{const original=window.fetch.bind(window);let release;const gate=new Promise(resolve=>release=resolve);window.__releaseFirstToggle=release;window.__toggleRequests=0;window.__firstToggleHeld=false;window.fetch=(input,init)=>{if(String(input)==='/api/toggle'){window.__toggleRequests++;if(window.__toggleRequests===1){window.__firstToggleHeld=true;return gate.then(()=>original(input,init))}}return original(input,init)};window.__m2revision=document.body.dataset.revision;document.querySelector('.drum-cell.step-edit[data-pattern="beat-a"][data-lane="bd"][data-step="1"]').click();document.querySelector('.drum-cell.step-edit[data-pattern="beat-a"][data-lane="bd"][data-step="2"]').click();return true})()`)
	chrome.waitFor("window.__firstToggleHeld && window.__toggleRequests===1", 5*time.Second)
	chrome.eval(`window.__releaseFirstToggle();true`)
	chrome.waitFor("window.__toggleRequests===2 && document.body.dataset.revision!==window.__m2revision && document.getElementById('studio-status').textContent.includes('browser score queued') && !document.getElementById('save-source').disabled", 30*time.Second)
	chrome.waitFor("document.body.dataset.revision===window.cicadaBrowserAudio.revision", 5*time.Second)
	gridStageTime := chrome.eval("window.cicadaBrowserAudio.lastStageDurationMs")
	chrome.waitFor("window.__m2messages.some(message=>message.kind===3 && message.track===1 && message.tick%3840===240)", 8*time.Second)
	chrome.waitFor("!document.getElementById('save-source').disabled", 5*time.Second)
	chrome.eval(`window.__m2revision=document.body.dataset.revision;document.getElementById('edit-source').click();const editor=document.getElementById('source-editor');editor.value=editor.value.replace(/^title "First acid"$/m,'title "First acid browser test"');editor.dispatchEvent(new Event('input',{bubbles:true}));document.getElementById('save-source').click();true`)
	chrome.waitFor("document.body.dataset.revision!==window.__m2revision && document.getElementById('studio-status').textContent.includes('queued') && !document.getElementById('save-source').disabled", 10*time.Second)
	sourceStageTime := chrome.eval("window.cicadaBrowserAudio.lastStageDurationMs")
	playbackStats := chrome.eval("window.cicadaBrowserAudio.context.playbackStats?.toJSON?.()??null")
	workletMetrics := chrome.eval("window.cicadaBrowserAudio.requestMetrics()")
	t.Logf("Browser source staging: grid %.3f ms, text %.3f ms; playbackStats=%s; workletMetrics=%s", rawFloat(t, gridStageTime), rawFloat(t, sourceStageTime), playbackStats, workletMetrics)

	content, err := os.ReadFile(server.score)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "bd: Xxx.") {
		t.Fatalf("grid edit was not saved in the score: %s", content)
	}
	if !strings.Contains(string(content), `title "First acid browser test"`) {
		t.Fatalf("source edit was not saved in the score: %s", content)
	}
	chrome.mustCall("Page.reload", map[string]any{"ignoreCache": true})
	chrome.waitFor("document.readyState==='complete'", 10*time.Second)
	chrome.waitFor("document.getElementById('audio-mode').value==='native'", 5*time.Second)
	visibleStep := chrome.eval(`(()=>{return [1,2].every(step=>{const cell=document.querySelector('.drum-cell.step-edit[data-pattern="beat-a"][data-lane="bd"][data-step="'+step+'"]');return !!cell&&!!cell.querySelector('.fill')})})()`)
	var persisted bool
	if err := json.Unmarshal(visibleStep, &persisted); err != nil || !persisted {
		t.Fatalf("edited step is not visible after reload: %s (%v)", visibleStep, err)
	}
	t.Logf("Browser Studio flow: play, scene, slot, play-from-block, next-bar grid note, source persistence, and reload verified; score=%s", server.score)
}

func TestBrowserStepEditQueueRegression(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	server := startBrowserStudio(t, source, nil)
	chrome := startBrowserChrome(t, server)
	chrome.navigate("http://" + browserStudioAddress + "/")
	chrome.eval(`(()=>{const select=document.getElementById('audio-mode');select.value='browser';select.dispatchEvent(new Event('change',{bubbles:true}));return true})()`)
	chrome.waitFor("document.getElementById('audio-mode').value==='browser'", 5*time.Second)
	baseline := chrome.eval(`(()=>({revision:document.body.dataset.revision,one:!!document.querySelector('.drum-cell.step-edit[data-pattern="beat-a"][data-lane="bd"][data-step="1"] .fill'),two:!!document.querySelector('.drum-cell.step-edit[data-pattern="beat-a"][data-lane="bd"][data-step="2"] .fill')}))()`)
	chrome.eval("window.__stepBaseline=" + string(baseline) + ";true")
	chrome.eval(`(()=>{const original=window.fetch.bind(window);let release;const gate=new Promise(resolve=>release=resolve);window.__releaseFirstToggle=release;window.__toggleRequests=0;window.__firstToggleHeld=false;window.fetch=(input,init)=>{if(String(input)==='/api/toggle'){window.__toggleRequests++;if(window.__toggleRequests===1){window.__firstToggleHeld=true;return gate.then(()=>original(input,init))}}return original(input,init)};document.querySelector('.drum-cell.step-edit[data-pattern="beat-a"][data-lane="bd"][data-step="1"]').click();document.querySelector('.drum-cell.step-edit[data-pattern="beat-a"][data-lane="bd"][data-step="2"]').click();return true})()`)
	chrome.waitFor("window.__firstToggleHeld && window.__toggleRequests===1", 5*time.Second)
	time.Sleep(250 * time.Millisecond)
	queued := chrome.eval("window.__toggleRequests")
	if string(queued) != "1" {
		t.Fatalf("second step edit was sent before the first save completed: requests=%s", queued)
	}
	chrome.eval(`window.__releaseFirstToggle();true`)
	chrome.waitFor("window.__toggleRequests===2 && document.body.dataset.revision!==window.__stepBaseline.revision && !document.getElementById('save-source').disabled && !!document.querySelector('.drum-cell.step-edit[data-pattern=\"beat-a\"][data-lane=\"bd\"][data-step=\"1\"] .fill')!==window.__stepBaseline.one && !!document.querySelector('.drum-cell.step-edit[data-pattern=\"beat-a\"][data-lane=\"bd\"][data-step=\"2\"] .fill')!==window.__stepBaseline.two", 30*time.Second)
	content, err := os.ReadFile(server.score)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "bd: Xxx.") {
		t.Fatalf("serialized step edits were not both persisted: %s", content)
	}
	t.Log("two overlapping grid clicks waited for separate saves and both step changes persisted")
}

func rawFloat(t *testing.T, value json.RawMessage) float64 {
	t.Helper()
	var result float64
	if err := json.Unmarshal(value, &result); err != nil {
		t.Fatalf("decode browser timing %s: %v", value, err)
	}
	return result
}

func TestBrowserUnderrunDetector(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	server := startBrowserStudio(t, source, nil)
	chrome := startBrowserChrome(t, server)
	chrome.setViewport(1440, 1000)
	chrome.navigate("http://" + browserStudioAddress + "/")
	chrome.eval(`(()=>{const select=document.getElementById('audio-mode');select.value='browser';select.dispatchEvent(new Event('change',{bubbles:true}));return true})()`)
	chrome.waitFor("!document.getElementById('start-audio').hidden", 5*time.Second)
	chrome.click("#start-audio")
	chrome.waitFor("window.cicadaBrowserAudio.context?.state==='running'", 20*time.Second)
	chrome.waitFor("document.getElementById('studio-status').textContent.includes('Browser audio ready')", 5*time.Second)
	time.Sleep(2 * time.Second)
	chrome.eval(`window.__detectorMessages=[];window.cicadaBrowserAudio.onMessage(message=>window.__detectorMessages.push(message));true`)
	chrome.click("#transport-button")
	playDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(playDeadline) {
		var playing bool
		if json.Unmarshal(chrome.eval("window.cicadaBrowserAudio.playing===true"), &playing) == nil && playing {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var playing bool
	_ = json.Unmarshal(chrome.eval("window.cicadaBrowserAudio.playing===true"), &playing)
	if !playing {
		t.Fatalf("Browser Play did not reach the worklet: %s", chrome.eval("({mode:document.getElementById('audio-mode').value,status:document.getElementById('studio-status').textContent,context:window.cicadaBrowserAudio.context?.state,playing:window.cicadaBrowserAudio.playing,messages:window.__detectorMessages.slice(-8)})"))
	}
	before := chrome.eval(`(()=>{const c=window.cicadaBrowserAudio.context;return {playbackStats:c.playbackStats?.toJSON?.()??null,outputTimestamp:c.getOutputTimestamp?.()??null,currentTime:c.currentTime,baseLatency:c.baseLatency,outputLatency:c.outputLatency}})()`)
	baselineResult := chrome.eval(`(async()=>{const m=await window.cicadaBrowserAudio.requestMetrics();return {underruns:m.underruns,maxDurationMs:m.maxCallbackDurationMs,clockPrecisionMs:m.cp,callbackDurationLimitMs:m.dl,callbackGapLimitMs:m.gl,outputTimeline:m.outputTimeline}})()`)
	var baseline struct {
		Underruns               int     `json:"underruns"`
		MaxDurationMs           float64 `json:"maxDurationMs"`
		ClockPrecisionMs        float64 `json:"clockPrecisionMs"`
		CallbackDurationLimitMs float64 `json:"callbackDurationLimitMs"`
		CallbackGapLimitMs      float64 `json:"callbackGapLimitMs"`
		OutputTimeline          struct {
			Available bool    `json:"available"`
			Misses    int     `json:"misses"`
			LatencyMs float64 `json:"latencyMs"`
			QuantumMs float64 `json:"quantumMs"`
			LimitMs   float64 `json:"limitMs"`
		} `json:"outputTimeline"`
	}
	if err := json.Unmarshal(baselineResult, &baseline); err != nil {
		t.Fatalf("decode baseline underrun metrics %s: %v", baselineResult, err)
	}
	timestampCheck := chrome.eval(`(()=>{const audio=window.cicadaBrowserAudio,context=audio.context,original=context.getOutputTimestamp.bind(context),ageMs=20,latencyMs=((context.baseLatency||0)+(context.outputLatency||0))*1000;audio.outputTimeline={available:false,samples:0,misses:0,maxLagMs:0,maxExcessMs:0};context.getOutputTimestamp=()=>({contextTime:Math.max(0,context.currentTime-latencyMs/1000-ageMs/1000),performanceTime:performance.now()-ageMs});audio.sampleOutputTimeline();const result={...audio.outputTimeline,latencyMs,ageMs};context.getOutputTimestamp=original;return result})()`)
	var timestampAge struct {
		Samples   int     `json:"samples"`
		Misses    int     `json:"misses"`
		MaxLagMs  float64 `json:"maxLagMs"`
		LatencyMs float64 `json:"latencyMs"`
		AgeMs     float64 `json:"ageMs"`
	}
	if err := json.Unmarshal(timestampCheck, &timestampAge); err != nil {
		t.Fatalf("decode output timestamp age check %s: %v", timestampCheck, err)
	}
	deltaMs := timestampAge.MaxLagMs - timestampAge.LatencyMs
	if timestampAge.Samples != 1 || timestampAge.Misses != 0 || deltaMs < -1 || deltaMs > 2 {
		t.Fatalf("output timeline did not compensate for a stale 20 ms output timestamp: metrics=%s lag-minus-latency=%.3f ms", timestampCheck, deltaMs)
	}
	t.Logf("output timeline age check: a %g ms-old timestamp measured %.3f ms lag at %.3f ms reported latency without an exceedance", timestampAge.AgeMs, timestampAge.MaxLagMs, timestampAge.LatencyMs)
	chrome.eval(`window.__stallAt=performance.now();window.cicadaBrowserAudio.injectStall();true`)
	result := chrome.eval(`(async()=>{await new Promise(resolve=>setTimeout(resolve,1500));const m=await window.cicadaBrowserAudio.requestMetrics();return {clock:m.clock,underruns:m.underruns,clockPrecisionMs:m.cp,maxDurationMs:m.maxCallbackDurationMs,callbackP99Ms:m.callbackP99Ms,callbackDurationLimitMs:m.dl,callbackGapLimitMs:m.gl,quantumMs:m.q,reportedLatencyMs:m.l,memoryBytes:m.memoryBytes,outputTimeline:m.outputTimeline}})()`)
	var metrics struct {
		Clock                   bool    `json:"clock"`
		Underruns               int     `json:"underruns"`
		ClockPrecisionMs        float64 `json:"clockPrecisionMs"`
		MaxDurationMs           float64 `json:"maxDurationMs"`
		CallbackP99Ms           float64 `json:"callbackP99Ms"`
		CallbackDurationLimitMs float64 `json:"callbackDurationLimitMs"`
		CallbackGapLimitMs      float64 `json:"callbackGapLimitMs"`
		QuantumMs               float64 `json:"quantumMs"`
		ReportedLatencyMs       float64 `json:"reportedLatencyMs"`
		MemoryBytes             int     `json:"memoryBytes"`
		OutputTimeline          struct {
			Available   bool    `json:"available"`
			Samples     int     `json:"samples"`
			Misses      int     `json:"misses"`
			MaxLagMs    float64 `json:"maxLagMs"`
			MaxExcessMs float64 `json:"maxExcessMs"`
			LatencyMs   float64 `json:"latencyMs"`
			QuantumMs   float64 `json:"quantumMs"`
			LimitMs     float64 `json:"limitMs"`
		} `json:"outputTimeline"`
	}
	if err := json.Unmarshal(result, &metrics); err != nil {
		t.Fatalf("decode injected stall report %s: %v", result, err)
	}
	if metrics.CallbackDurationLimitMs < metrics.QuantumMs+0.99 || metrics.CallbackDurationLimitMs > metrics.QuantumMs+1.01 || metrics.CallbackGapLimitMs < metrics.ReportedLatencyMs+metrics.QuantumMs+0.99 || metrics.CallbackGapLimitMs > metrics.ReportedLatencyMs+metrics.QuantumMs+1.01 {
		t.Fatalf("worklet underrun thresholds must retain the one-millisecond allowance: metrics=%s", result)
	}
	after := chrome.eval(`(()=>{const c=window.cicadaBrowserAudio.context;return {playbackStats:c.playbackStats?.toJSON?.()??null,outputTimestamp:c.getOutputTimestamp?.()??null,currentTime:c.currentTime,baseLatency:c.baseLatency,outputLatency:c.outputLatency}})()`)
	t.Logf("underrun detector proof: quantum=%.3f ms; reported latency=%.3f ms; clock allowance=%.3f ms; duration limit=%.3f ms; callback-gap limit=%.3f ms; output-lag limit=%.3f ms; high-resolution worklet clock=%v; baseline worklet=%d (max duration=%.2f), after 20 ms stall worklet=%d (max duration=%.2f); output-timeline samples=%d exceedances=%d; before=%s after=%s", metrics.QuantumMs, metrics.ReportedLatencyMs, metrics.ClockPrecisionMs, metrics.CallbackDurationLimitMs, metrics.CallbackGapLimitMs, metrics.OutputTimeline.LimitMs, metrics.Clock, baseline.Underruns, baseline.MaxDurationMs, metrics.Underruns, metrics.MaxDurationMs, metrics.OutputTimeline.Samples, metrics.OutputTimeline.Misses, before, after)
	if metrics.Underruns <= baseline.Underruns || metrics.MaxDurationMs < 20 {
		t.Fatalf("one deliberate processor stall was not counted by the underrun detector: before=%+v after=%+v", baseline, metrics)
	}
	chrome.eval(`(()=>{const bytes=new Uint8Array(24);bytes[0]=250;bytes[1]=255;window.cicadaBrowserAudio.node.port.postMessage({t:'c',bytes},[bytes.buffer]);return true})()`)
	chrome.waitFor("document.getElementById('studio-status').textContent.startsWith('Audio fault') && window.cicadaBrowserAudio.playing===false && document.getElementById('transport-button').textContent==='Play'", 5*time.Second)
	t.Log("kernel Fault message stopped Browser transport and appeared in Studio status")
}

func TestBrowserProcessorAllocations(t *testing.T) {
	t.Setenv("PULSE_SERVER", "unix:/nonexistent")
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	startBrowserStudio(t, source, nil)

	temp := t.TempDir()
	for _, artifact := range []struct{ endpoint, name string }{
		{endpoint: "/api/kernel.wasm", name: "kernel.wasm"},
		{endpoint: "/api/kernel-image?rate=48000", name: "kernel.image"},
	} {
		response, err := http.Get("http://" + browserStudioAddress + artifact.endpoint)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("download %s: status=%d err=%v", artifact.endpoint, response.StatusCode, readErr)
		}
		if err := os.WriteFile(filepath.Join(temp, artifact.name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("node", "--expose-gc", "../../host/web/processor_alloc_test.js", filepath.Join(temp, "kernel.wasm"), filepath.Join(temp, "kernel.image"))
	output, err := command.CombinedOutput()
	t.Logf("AudioWorklet allocation test output:\n%s", output)
	if err != nil {
		t.Fatalf("Node AudioWorklet allocation test: %v", err)
	}
	capture := exec.Command("node", "--expose-gc", "../../host/web/processor_alloc_test.js", filepath.Join(temp, "kernel.wasm"), filepath.Join(temp, "kernel.image"), "capture")
	captureOutput, captureErr := capture.CombinedOutput()
	t.Logf("Capture worklet allocation test output:\n%s", captureOutput)
	if captureErr != nil {
		t.Fatalf("Node capture worklet allocation test: %v", captureErr)
	}
	stop := exec.Command("node", "../../host/web/processor_stop_test.js", filepath.Join(temp, "kernel.wasm"), filepath.Join(temp, "kernel.image"))
	stopOutput, stopErr := stop.CombinedOutput()
	t.Logf("AudioWorklet stop-after-edit test output:\n%s", stopOutput)
	if stopErr != nil {
		t.Fatalf("Node AudioWorklet stop-after-edit test: %v", stopErr)
	}
}

func TestBrowserCPUReport(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	server := startBrowserStudio(t, source, nil)
	if os.Getenv("CICADA_BROWSER") == "windows" {
		chrome := startBrowserChrome(t, server)
		chrome.setViewport(1440, 1000)
		chrome.navigate("http://" + browserStudioAddress + "/")
		chrome.eval(`(()=>{const select=document.getElementById('audio-mode');select.value='browser';select.dispatchEvent(new Event('change',{bubbles:true}));return true})()`)
		chrome.waitFor("!document.getElementById('start-audio').hidden", 5*time.Second)
		chrome.click("#start-audio")
		chrome.waitFor("window.cicadaBrowserAudio.context?.state==='running' && document.getElementById('studio-status').textContent.includes('Browser audio ready')", 20*time.Second)
		chrome.eval(`(()=>{window.__budgetPortMessages=0;window.__budgetDecodedMessages=0;window.__budgetGCCount=0;window.__budgetGCDurationMs=0;window.__budgetGCObserverAvailable=false;window.__budgetLongTaskCount=0;window.__budgetLongTaskMaxMs=0;window.__budgetLongTaskObserverAvailable=false;const port=window.cicadaBrowserAudio.node.port,receive=port.onmessage;port.onmessage=event=>{window.__budgetPortMessages++;if(event.data?.t==='m')window.__budgetDecodedMessages+=event.data.n/16;receive.call(port,event)};try{new PerformanceObserver(list=>{for(const entry of list.getEntries()){window.__budgetGCCount++;window.__budgetGCDurationMs+=entry.duration}}).observe({entryTypes:['gc']});window.__budgetGCObserverAvailable=true}catch(_){}try{new PerformanceObserver(list=>{for(const entry of list.getEntries()){window.__budgetLongTaskCount++;window.__budgetLongTaskMaxMs=Math.max(window.__budgetLongTaskMaxMs,entry.duration)}}).observe({entryTypes:['longtask']});window.__budgetLongTaskObserverAvailable=true}catch(_){}return true})()`)
		chrome.click("#transport-button")
		chrome.waitFor("window.cicadaBrowserAudio.playing===true", 5*time.Second)
		time.Sleep(2 * time.Second)
		beforeCPU := windowsChromeRendererCPU(t)
		beforeValue := chrome.eval(`(async()=>{const metrics=await window.cicadaBrowserAudio.requestMetrics();return {samples:metrics.callbackSamples}})()`)
		var beforeMetrics struct {
			Samples int `json:"samples"`
		}
		if err := json.Unmarshal(beforeValue, &beforeMetrics); err != nil {
			t.Fatalf("decode initial Windows Chrome CPU sample count %s: %v", beforeValue, err)
		}
		time.Sleep(10 * time.Second)
		value := chrome.eval(`(async()=>{const metrics=await window.cicadaBrowserAudio.requestMetrics();return {clock:metrics.clock,p99:metrics.callbackP99Ms,max:metrics.maxCallbackDurationMs,samples:metrics.callbackSamples,quantumMs:metrics.q,durationExceedances:metrics.callbackDurationExceedances,gapExceedances:metrics.callbackGapExceedances,underruns:metrics.underruns,memoryBytes:metrics.memoryBytes,faults:window.cicadaBrowserAudio.playing?0:1}})()`)
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
		}
		if err := json.Unmarshal(value, &metrics); err != nil {
			t.Fatalf("decode Windows Chrome CPU metrics %s: %v", value, err)
		}
		diagnostics := chrome.eval(`({portMessages:window.__budgetPortMessages,decodedMessages:window.__budgetDecodedMessages,gcCount:window.__budgetGCCount,gcDurationMs:window.__budgetGCDurationMs,gcObserverAvailable:window.__budgetGCObserverAvailable,longTaskCount:window.__budgetLongTaskCount,longTaskMaxMs:window.__budgetLongTaskMaxMs,longTaskObserverAvailable:window.__budgetLongTaskObserverAvailable})`)
		clock := "Date.now()"
		if metrics.Clock {
			clock = "AudioWorklet performance.now()"
		}
		if metrics.Samples <= beforeMetrics.Samples || afterCPU.Processes == 0 {
			t.Fatalf("Windows Chrome CPU sample window is invalid: callbacks=%d..%d renderer_processes=%d..%d", beforeMetrics.Samples, metrics.Samples, beforeCPU.Processes, afterCPU.Processes)
		}
		measuredCallbacks := metrics.Samples - beforeMetrics.Samples
		cpuMsPerCallback, totalRendererCPUMs, maxRendererPID, counterIntervalMs, stableRendererSet := windowsChromeRendererCPUMsPerCallback(beforeCPU, afterCPU, metrics.QuantumMs)
		if !stableRendererSet || maxRendererPID == 0 {
			t.Fatalf("Windows Chrome CPU sample window had no stable renderer process set: before=%d after=%d", beforeCPU.Processes, afterCPU.Processes)
		}
		cpuBudgetMs, cpuBudgetMetric := cpuMsPerCallback, "maximum Windows Chrome renderer CPU milliseconds per AudioWorklet callback"
		if metrics.Clock {
			cpuBudgetMs, cpuBudgetMetric = metrics.P99, "AudioWorklet callback p99"
		}
		report := map[string]any{
			"engine": "Windows Chrome AudioWorklet", "clock": clock, "p99": metrics.P99,
			"maxCallbackDurationMs": metrics.Max, "underruns": metrics.Underruns,
			"callbackSamples": metrics.Samples, "cpuBudgetMetric": cpuBudgetMetric, "cpuBudgetMs": cpuBudgetMs,
			"cpuMsPerCallback": cpuMsPerCallback, "totalRendererCPUMsPerCallback": totalRendererCPUMs,
			"processCounterIntervalMs": counterIntervalMs, "workletSamplesInWindow": measuredCallbacks,
			"workletExpectedSamplesInCounterInterval": counterIntervalMs / metrics.QuantumMs,
			"maxRendererProcessId":                    maxRendererPID, "cpuLimitMsPerCallback": 0.67,
			"rendererProcessesBefore": beforeCPU.Processes, "rendererProcessesAfter": afterCPU.Processes,
			"durationExceedances": metrics.DurationExceedances, "gapExceedances": metrics.GapExceedances,
			"pageDiagnostics": json.RawMessage(diagnostics),
			"faults":          metrics.Faults, "memoryBytes": metrics.Memory,
		}
		if metrics.Faults == 0 {
			chrome.eval("window.cicadaBrowserAudio.stop();true")
			profile := chrome.eval(`(async()=>{try{const [wasmResponse,imageResponse]=await Promise.all([fetch('/api/kernel.wasm'),fetch('/api/kernel-image?rate=48000')]);if(!wasmResponse.ok||!imageResponse.ok)throw Error('kernel diagnostic asset fetch failed');const wasmBytes=await wasmResponse.arrayBuffer(),imageBytes=new Uint8Array(await imageResponse.arrayBuffer()),{instance}=await WebAssembly.instantiate(wasmBytes),x=instance.exports;x._initialize();const project=x.gosx_audio_project_alloc(imageBytes.length);if(!project)throw Error('kernel diagnostic project allocation failed');new Uint8Array(x.memory.buffer,project,imageBytes.length).set(imageBytes);if(x.gosx_audio_init(48000,128,2)!==0)throw Error('kernel diagnostic project initialization failed');const commands=new Uint8Array(x.memory.buffer,x.gosx_audio_cmd_ptr(),48);commands[0]=3;commands[1]=255;commands[24]=1;commands[25]=255;x.gosx_audio_cmd_commit(2);const all=[],warmup=256,measured=11264;let messageDrains=0;for(let block=0;block<warmup+measured;block++){const start=performance.now();x.gosx_audio_render(128);if((block&7)===7){x.gosx_audio_msg_drain();messageDrains++}const elapsed=performance.now()-start;if(block>=warmup)all.push(elapsed)}all.sort((a,b)=>a-b);const at=p=>all[Math.min(all.length-1,Math.ceil(all.length*p)-1)];return {engine:'Windows Chrome WebAssembly kernel diagnostic',clock:'page performance.now()',warmupCallbacks:warmup,measuredCallbacks:all.length,p50Ms:at(.5),p95Ms:at(.95),p99Ms:at(.99),maxMs:all[all.length-1],meanMs:all.reduce((a,b)=>a+b,0)/all.length,messageDrainEveryCallbacks:8,messageDrains:messageDrains,memoryBytes:x.memory.buffer.byteLength}}catch(error){return {error:String(error&&error.stack||error)}}})()`)
			report["wasmCallbackProfile"] = json.RawMessage(profile)
			t.Logf("Windows Chrome per-callback WASM profile: %s; worklet message traffic: %s", profile, diagnostics)
		}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("..", "..", "build", "browser-cpu-report.json")
		if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		t.Logf("Windows Chrome CPU budget: metric=%s used=%.4f ms/callback clock=%s callback_p99=%.4f ms max=%.2f ms samples=%d measured_samples=%d expected_samples_in_counter_window=%.0f counter_window=%.0f ms quantum=%.3f ms duration_exceedances=%d gap_exceedances=%d renderer_max_pid=%d renderer_cpu_max=%.4f ms/callback renderer_cpu_total=%.4f ms/callback limit=0.67 ms underruns=%d memory=%d bytes renderer_processes=%d..%d page_diagnostics=%s", cpuBudgetMetric, cpuBudgetMs, clock, metrics.P99, metrics.Max, metrics.Samples, measuredCallbacks, counterIntervalMs/metrics.QuantumMs, counterIntervalMs, metrics.QuantumMs, metrics.DurationExceedances, metrics.GapExceedances, maxRendererPID, cpuMsPerCallback, totalRendererCPUMs, metrics.Underruns, metrics.Memory, beforeCPU.Processes, afterCPU.Processes, diagnostics)
		if metrics.Faults != 0 || cpuBudgetMs > 0.67 {
			t.Fatalf("Windows Chrome CPU gate failed: faults=%d underruns=%d %s=%.4f ms/callback limit=0.67 ms", metrics.Faults, metrics.Underruns, cpuBudgetMetric, cpuBudgetMs)
		}
		return
	}
	temp := t.TempDir()
	for _, artifact := range []struct{ endpoint, name string }{
		{endpoint: "/api/kernel.wasm", name: "kernel.wasm"},
		{endpoint: "/api/kernel-image?rate=48000", name: "kernel.image"},
	} {
		response, err := http.Get("http://" + browserStudioAddress + artifact.endpoint)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("fetch %s: status=%d err=%v body=%s", artifact.endpoint, response.StatusCode, readErr, data)
		}
		if err := os.WriteFile(filepath.Join(temp, artifact.name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("node", "browser_cpu_node.js", filepath.Join(temp, "kernel.wasm"), filepath.Join(temp, "kernel.image"), "11264")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Node V8 kernel timing failed: %v\n%s", err, output)
	}
	var report struct {
		Engine       string  `json:"engine"`
		Clock        string  `json:"clock"`
		Blocks       int     `json:"blocks"`
		Measured     int     `json:"measuredBlocks"`
		P50          float64 `json:"p50"`
		P95          float64 `json:"p95"`
		P99          float64 `json:"p99"`
		Max          float64 `json:"max"`
		Faults       int     `json:"faults"`
		MemoryBytes  int     `json:"finalMemoryBytes"`
		MemoryGrowth int     `json:"memoryGrowthAfterWarmupBytes"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode Node CPU report %s: %v", output, err)
	}
	if report.Engine != "Node V8 WebAssembly" || report.Measured != report.Blocks-256 || report.MemoryGrowth != 0 || report.Faults != 0 {
		t.Fatalf("incomplete Node CPU timing or kernel soak: %+v", report)
	}
	var fullReport map[string]any
	if err := json.Unmarshal(output, &fullReport); err != nil {
		t.Fatal(err)
	}
	reportBytes, err := json.MarshalIndent(fullReport, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "build", "browser-cpu-report.json")
	if err := os.WriteFile(path, append(reportBytes, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("Browser AudioWorklet clock unavailable; Node V8 WASM fallback (%s), milliseconds per 128-frame block: blocks=%d measured=%d p50=%.4f p95=%.4f p99=%.4f max=%.4f kernel_faults=%d memory=%d growth=%d report=%s", report.Clock, report.Blocks, report.Measured, report.P50, report.P95, report.P99, report.Max, report.Faults, report.MemoryBytes, report.MemoryGrowth, path)
	// The Node fallback runs on shared CI runners, where one block in a hundred can take
	// several times longer from OS scheduling. It counts user CPU time only, and the 0.67 ms
	// budget gates p95; p99 and max are logged, and a p99 above three times the budget
	// (2.01 ms) still fails, so a pathological tail is caught.
	// The real p99 check is the Windows Chrome CPU report (p99 against 0.67 ms, the real
	// page clock). Record it with `make release-cpu-report` before any release that changes
	// the kernel or the AudioWorklet processor.
	if report.P95 > 0.67 || report.P99 > 3*0.67 {
		t.Fatalf("Node V8 WASM p95 %.4f ms (budget 0.67 ms) or p99 %.4f ms (ceiling 2.01 ms) exceeded; max %.4f ms", report.P95, report.P99, report.Max)
	}
}

func TestBrowserCaptureTargets(t *testing.T) {
	server := startBrowserStudio(t, []byte(audioTakeScore), nil)
	chrome := startBrowserChrome(t, server)
	chrome.navigate("http://" + browserStudioAddress + "/")
	chrome.waitFor("!document.getElementById('pcm-arm').disabled", 5*time.Second)
	save := func(source, condition string) {
		t.Helper()
		text, _ := json.Marshal(source)
		chrome.eval(`(()=>{window.__targetRevision=document.body.dataset.revision;document.getElementById('edit-source').click();const editor=document.getElementById('source-editor');editor.value=` + string(text) + `;editor.dispatchEvent(new Event('input',{bubbles:true}));document.getElementById('save-source').click();return true})()`)
		chrome.waitFor("document.body.dataset.revision!==window.__targetRevision && !document.getElementById('save-source').disabled && ("+condition+")", 10*time.Second)
	}
	added := strings.Replace(audioTakeScore, "track vox audio {}", "track vox audio {}\ntrack guitar audio {}", 1)
	save(added, "document.getElementById('pcm-track').options.length===2")
	chrome.eval(`document.getElementById('pcm-track').value='guitar';true`)
	renamed := strings.Replace(added, "track guitar audio {}", "track lead audio {}", 1)
	renamed = strings.ReplaceAll(renamed, "main", "verse")
	save(renamed, "Array.from(document.getElementById('pcm-track').options,o=>o.value).join(',')==='vox,lead' && document.getElementById('pcm-track').value==='vox' && document.getElementById('pcm-scene').value==='verse'")
	removed := strings.ReplaceAll(renamed, "track vox audio {}", "")
	removed = strings.ReplaceAll(removed, "track lead audio {}", "")
	removed = strings.ReplaceAll(removed, "vox = off", "")
	save(removed, "document.getElementById('pcm-track').options.length===0 && document.getElementById('pcm-arm').disabled")
	t.Log("capture selectors follow added, renamed and removed targets through actual source saves")
}
