//go:build browser

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBrowserRenderSizeHint(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	server := startBrowserStudio(t, source, nil)
	chrome := startBrowserChrome(t, server)
	chrome.navigate("http://" + browserStudioAddress + "/")
	chrome.eval(`(()=>{const select=document.getElementById('audio-mode');select.value='browser';select.dispatchEvent(new Event('change',{bubbles:true}));return true})()`)
	chrome.waitFor("!document.getElementById('start-audio').hidden", 5*time.Second)
	chrome.click("#start-audio")
	chrome.waitFor("window.cicadaBrowserAudio.context?.state==='running'", 20*time.Second)
	value := chrome.eval(`(async()=>{
  const audio=window.cicadaBrowserAudio;
  const hardwareQuantum=audio.context.renderQuantumSize||128;
  const [wasmResponse,imageResponse]=await Promise.all([fetch('/api/kernel.wasm'),fetch('/api/kernel-image?rate=48000')]);
  const module=await WebAssembly.compile(await wasmResponse.arrayBuffer()),image=await imageResponse.arrayBuffer();
  const results=[];let reference;
  for(const hint of [128,64,256,512]){
    const context=new OfflineAudioContext({numberOfChannels:2,length:8192,sampleRate:48000,renderSizeHint:hint});
    await context.audioWorklet.addModule('/audio/cicada-processor.js');
    let readyResolve,readyReject;const ready=new Promise((resolve,reject)=>{readyResolve=resolve;readyReject=reject});
    const node=new AudioWorkletNode(context,'cicada',{numberOfInputs:0,numberOfOutputs:1,outputChannelCount:[2],processorOptions:{m:module,i:image}});
    let faults=0;
    node.port.onmessage=event=>{
      const data=event.data;
      if(data.t==='r')readyResolve();
      if(data.t==='e'){faults++;readyReject(Error(data.e))}
      if(data.t==='f')faults++;
      if(data.t==='m')node.port.postMessage({t:'b',bytes:data.bytes},[data.bytes]);
    };
    node.connect(context.destination);await ready;
    const play=new Uint8Array(24);play[0]=1;play[1]=255;
    node.port.postMessage({t:'c',bytes:play});
    const rendered=await context.startRendering();
    const channels=[rendered.getChannelData(0),rendered.getChannelData(1)];
    let maxDifference=0,peak=0;
    for(let c=0;c<2;c++)for(let i=0;i<channels[c].length;i++){
      const sample=channels[c][i];
      if(!Number.isFinite(sample))throw Error('non-finite worklet output');
      peak=Math.max(peak,Math.abs(sample));
      if(reference)maxDifference=Math.max(maxDifference,Math.abs(sample-reference[c][i]));
    }
    reference ||= channels;
    results.push({hint,actual:context.renderQuantumSize||128,maxDifference,peak,faults});
    node.port.close();
  }
  return {hardwareQuantum,supported:'renderQuantumSize' in audio.context,results};
})()`)
	var report struct {
		HardwareQuantum int  `json:"hardwareQuantum"`
		Supported       bool `json:"supported"`
		Results         []struct {
			Hint, Actual, Faults int
			MaxDifference, Peak  float64
		} `json:"results"`
	}
	if err := json.Unmarshal(value, &report); err != nil {
		t.Fatalf("decode render quantum results %s: %v", value, err)
	}
	if len(report.Results) != 4 || report.HardwareQuantum < 1 {
		t.Fatalf("incomplete render quantum results: %s", value)
	}
	for _, result := range report.Results {
		if result.MaxDifference != 0 || result.Peak == 0 || result.Faults != 0 || result.Actual < 1 {
			t.Fatalf("render quantum parity or negotiation failed: %s", value)
		}
	}
	t.Logf("render quantum negotiation and exact worklet PCM: %s", value)
}
