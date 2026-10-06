//go:build browser

package main

import (
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"
)

// This sends fixture PCM through a MediaStream and the real capture worklet,
// journal worker, multipart admission, native sample voice and browser output.
func TestBrowserInstrumentRecording(t *testing.T) {
	// Parallel roadmap lanes must not share DevTools or Studio ports.
	if os.Getenv("CICADA_BROWSER") != "windows" {
		oldStudio, oldDebug := browserStudioAddress, browserDebugAddress
		t.Cleanup(func() { browserStudioAddress, browserDebugAddress = oldStudio, oldDebug })
		freeAddress := func() string {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			listener.Close()
			return address
		}
		browserStudioAddress, browserDebugAddress = freeAddress(), freeAddress()
	}
	wav, err := os.ReadFile("../../host/recording/testdata/pencil-taps.wav")
	if err != nil {
		t.Fatal(err)
	}
	server := startBrowserStudio(t, []byte(studioScore), wav)
	chrome := startBrowserChrome(t, server)
	chrome.setViewport(1440, 1000)
	chrome.navigate("http://" + browserStudioAddress + "/")
	chrome.click("[data-panel-tab=record]")
	chrome.eval(`(() => { const mode=document.getElementById('audio-mode');mode.value='browser';mode.dispatchEvent(new Event('change',{bubbles:true}));return true; })()`)
	chrome.eval(`(() => {
      window.instrumentTestStart=performance.now();
      navigator.mediaDevices.getUserMedia=async constraints=>{
        if(constraints.audio.echoCancellation!==false || constraints.audio.noiseSuppression!==false || constraints.audio.autoGainControl!==false) throw new Error('microphone processing must be off');
        const context=window.cicadaBrowserAudio.context;
        const buffer=await context.decodeAudioData(await (await fetch('/__test/reference')).arrayBuffer());
        const destination=context.createMediaStreamDestination(), source=context.createBufferSource();
        destination.channelCount=1;
        source.buffer=buffer;source.connect(destination);window.instrumentFixtureSource=source;
        destination.stream.getAudioTracks()[0].getSettings=()=>({channelCount:1,sampleRate:context.sampleRate,echoCancellation:false,noiseSuppression:false,autoGainControl:false});
        return destination.stream;
      };
      return true;
    })()`)
	chrome.click("#instrument-mic")
	chrome.waitFor(`document.getElementById('instrument-status').textContent.startsWith('Recording ·')`, 30*time.Second)
	chrome.eval(`(() => { const source=window.instrumentFixtureSource; window.instrumentFixtureEnded=false;source.onended=()=>window.instrumentFixtureEnded=true;source.start();return true; })()`)
	chrome.waitFor(`window.instrumentFixtureEnded===true`, 15*time.Second)
	chrome.click("#instrument-stop")
	chrome.waitFor(`!document.getElementById('instrument-mic').disabled`, 30*time.Second)
	if string(chrome.eval(`window.cicadaRecordedInstrument?.hits.length || 0`)) != "15" {
		t.Fatalf("browser slicing: %s", chrome.eval(`({hits:window.cicadaRecordedInstrument?.hits.length,status:document.getElementById('instrument-status').textContent})`))
	}
	var result struct {
		Milliseconds float64 `json:"milliseconds"`
		Hits, Layers int
		Takes        []int
		Low, High    float64
		Overflow     bool
	}
	data := chrome.eval(`(async()=>{
      const p=window.cicadaRecordedInstrument, takes=[];
      let low=0,high=0;
      for(let cycle=0;cycle<6;cycle++) {
        const response=await fetch('/api/instrument-audition',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({sha256:p.sha256,note:60,velocity:80,cycle})});
        if(!response.ok) throw new Error('audition failed');takes.push(Number(response.headers.get('X-Cicada-Take')));
      }
      for(const velocity of [25,120]) {
        const response=await fetch('/api/instrument-audition',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({sha256:p.sha256,note:60,velocity,cycle:0})});
        const buffer=await window.cicadaBrowserAudio.context.decodeAudioData(await response.arrayBuffer());
        const pcm=buffer.getChannelData(0);let power=0;for(const x of pcm)power+=x*x;
        if(velocity===25)low=power;else high=power;
      }
      await window.cicadaPlayRecordedInstrument(60,80);await window.cicadaPlayRecordedInstrument(72,120);
      return {milliseconds:performance.now()-window.instrumentTestStart,Hits:p.hits.length,Layers:new Set(p.manifest.zones.map(z=>z.Layer)).size,Takes:takes,Low:low,High:high,Overflow:document.documentElement.scrollWidth>window.innerWidth};
    })()`)
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Milliseconds >= 120000 || result.Hits != 15 || result.Layers != 3 || result.Low >= result.High || len(result.Takes) != 6 || result.Takes[0] != result.Takes[5] || result.Takes[0] == result.Takes[1] || result.Overflow {
		t.Fatalf("recording result: %s", data)
	}
	t.Logf("15 browser-recorded fixture taps ready and played in %.0f ms; soft/hard energy %.4f/%.4f; cycle %v", result.Milliseconds, result.Low, result.High, result.Takes)
	chrome.click("#instrument-fit")
	chrome.waitFor(`window.cicadaModeledInstrument && !document.getElementById('instrument-fit').disabled`, 30*time.Second)
	var modeled struct {
		Milliseconds float64
		Modes        int
		Sampled      bool
	}
	data = chrome.eval(`(async()=>{const p=window.cicadaModeledInstrument;await window.cicadaPlayRecordedInstrument(p.model.rootMIDI,80);await window.cicadaPlayRecordedInstrument(p.model.rootMIDI+12,120);return {Milliseconds:performance.now()-window.instrumentTestStart,Modes:p.model.modes.length,Sampled:window.cicadaRecordedInstrument.sha256!==p.sha256};})()`)
	if err = json.Unmarshal(data, &modeled); err != nil {
		t.Fatal(err)
	}
	if modeled.Milliseconds >= 120000 || modeled.Modes == 0 || modeled.Sampled {
		t.Fatal("modeled browser playback", string(data))
	}
	t.Logf("sampled and modeled versions played in %.0f ms; %d fitted modes", modeled.Milliseconds, modeled.Modes)
	chrome.screenshot("modeled-instrument-1440.png")
	chrome.click("#instrument-mode")
	if string(chrome.eval(`window.cicadaRecordedInstrument.model===undefined`)) != "true" {
		t.Fatal("cannot return to sampled instrument")
	}
	chrome.click("#instrument-mode")
	chrome.setViewport(390, 900)
	chrome.eval(`new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve(true))))`)
	if string(chrome.eval(`document.documentElement.scrollWidth<=window.innerWidth`)) != "true" {
		t.Fatal("mobile page overflow")
	}
	if string(chrome.eval(`document.getElementById('instrument-save-score').getBoundingClientRect().bottom <= document.querySelector('.workspace-tabs').getBoundingClientRect().top`)) != "true" {
		t.Fatal("mobile instrument controls are clipped")
	}
	chrome.screenshot("modeled-instrument-390.png")
}
