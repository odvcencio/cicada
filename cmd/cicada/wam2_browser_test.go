//go:build browser

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/project"
)

func TestBrowserWAM2(t *testing.T) {
	root := filepath.Join("..", "..")
	sdk := filepath.Join(root, "build", "wam2-sdk", "node_modules", "@webaudiomodules", "sdk")
	output := filepath.Join(t.TempDir(), "plugin")
	if err := wam2Command([]string{filepath.Join(root, "examples", "live-intensity.cicada"), "-o", output, "--kernel", filepath.Join(root, "build", "cicada-kernel.wasm"), "--sdk", sdk, "--midi-track", "bass"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	// Run the official SDK reference host with its audio-input connection
	// removed for an instrument, and expose its instance to the test.
	refScript, err := os.ReadFile(filepath.Join(sdk, "host", "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	refScript = bytes.ReplaceAll(refScript, []byte("mediaElementSource.connect(wamInstance.audioNode);"), []byte("globalThis.referenceWam = wamInstance;"))
	source, err := os.ReadFile(filepath.Join(root, "examples", "live-intensity.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := compileStudioSource(filepath.Join(root, "examples", "live-intensity.cicada"), source)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := schedule.Compile(p, filepath.Join(root, "examples"), 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	native, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range project.LiveCommands(p) {
		if !native.Push(c) {
			t.Fatal("native setup rejected")
		}
	}
	if !native.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
		t.Fatal("native play rejected")
	}
	reference := make([]byte, 4096*8)
	left, right := make([]float32, 128), make([]float32, 128)
	for at := 0; at < 4096; at += 128 {
		native.Render(left, right)
		for i := 0; i < 128; i++ {
			binary.LittleEndian.PutUint32(reference[(at+i)*8:], math.Float32bits(left[i]))
			binary.LittleEndian.PutUint32(reference[(at+i)*8+4:], math.Float32bits(right[i]))
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/native", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(reference) })
	mux.HandleFunc("/reference/index.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write(refScript)
	})
	mux.HandleFunc("/reference/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(sdk, "host", "index.html"))
	})
	mux.Handle("/src/", http.StripPrefix("/src/", http.FileServer(http.Dir(filepath.Join(sdk, "src")))))
	mux.Handle("/", http.FileServer(http.Dir(output)))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	previousDebug := browserDebugAddress
	browserDebugAddress = listener.Addr().String()
	_ = listener.Close()
	t.Cleanup(func() { browserDebugAddress = previousDebug })
	chrome := startBrowserChrome(t, nil)
	chrome.setViewport(1100, 900)
	chrome.navigate(server.URL + "/reference/")
	chrome.waitFor(`!!globalThis.referenceWam?.initialized && !!document.querySelector('#mount').firstElementChild`, 30*time.Second)
	metadata := chrome.eval(`(async()=>{
  const p=referenceWam; const info=await p.audioNode.getParameterInfo();
  if(!p.descriptor.isInstrument || !p.descriptor.hasMidiInput) throw Error('Not an instrument');
  if(info.intensity.minValue!==0 || info.intensity.maxValue!==1 || info.intensity.defaultValue!==.3) throw Error('Incorrect WamParameterInfo');
  return {referenceHost:true, parameterInfo:info};
})()`)
	t.Logf("reference host: %s", metadata)
	chrome.screenshot("wam2-reference-host.png")
	chrome.navigate(server.URL + "/host.html")
	chrome.waitFor(`!!globalThis.cicadaWam?.initialized`, 30*time.Second)
	result := chrome.eval(`(async()=>{
  const {initializeWamHost}=await import('/sdk.js');
  const {default:Plugin}=await import('/index.js');
  const midiContext=new OfflineAudioContext(2,48000,48000);
  const [group]=await initializeWamHost(midiContext);
  const p=await Plugin.createInstance(group,midiContext);
  const node=p.audioNode; node.connect(midiContext.destination);
  const state=await node.getState();
  await node.setParameterValues({intensity:{id:'intensity',value:.8,normalized:true}});
  const saved=JSON.parse(JSON.stringify(await node.getState()));
  await node.setParameterValues({intensity:{id:'intensity',value:.1,normalized:false}});
  await node.setState(saved);
  if((await node.getParameterValues(false)).intensity.value!==.8)throw Error('State restore failed');
  let rejected=false;
  try{await node.setState({...saved,scoreId:'different'})}catch{rejected=true}
  if(!rejected)throw Error('Accepted incompatible state');
  const clone=await Plugin.createInstance(group,midiContext,saved);
  if((await clone.audioNode.getState()).parameterValues.intensity.value!==.8)throw Error('Initial state lost');
  clone.audioNode.destroy();
  let automationApplied=false;
  node.addEventListener('wam-automation',()=>{automationApplied=true});
  node.scheduleEvents(
    {type:'wam-midi',time:256/48000,data:{bytes:[0x90,60,100]}},
    {type:'wam-automation',time:512/48000,data:{id:'intensity',value:.6,normalized:false}},
    {type:'wam-midi',time:.5,data:{bytes:[0x90,60,0]}}
  );
  await node.getState();
  const buffer=await midiContext.startRendering();
  let before=0,peak=0,late=0;
  for(let i=0;i<48000;i++){
    const value=Math.abs(buffer.getChannelData(0)[i]);
    if(i<256)before=Math.max(before,value);
    peak=Math.max(peak,value);if(i>47000)late=Math.max(late,value);
  }
  if(before!==0||peak<.001)throw Error('MIDI timing/output failed: '+JSON.stringify({before,peak,late}));
  await new Promise(resolve=>setTimeout(resolve,0));
  if(!automationApplied)throw Error('Automation was not processed');
  node.destroy();
  const c=new OfflineAudioContext(2,4096,48000);
  const [g]=await initializeWamHost(c);
  const score=await Plugin.createInstance(g,c);
  score.audioNode.connect(c.destination);
  score.audioNode.scheduleEvents({type:'wam-transport',time:0,data:{playing:true,tempo:138}});
  await score.audioNode.getState();
  const rendered=await c.startRendering();let scorePeak=0,maxDifference=0;
  const expected=new DataView(await (await fetch('/native')).arrayBuffer());
  for(let i=0;i<4096;i++)for(let channel=0;channel<2;channel++){
    const v=rendered.getChannelData(channel)[i];scorePeak=Math.max(scorePeak,Math.abs(v));
    maxDifference=Math.max(maxDifference,Math.abs(v-expected.getFloat32(i*8+channel*4,true)));
  }
  if(scorePeak<.001)throw Error('Score playback is silent');
  if(maxDifference>1e-6)throw Error('Native/WAM2 PCM differs: '+maxDifference);
  score.audioNode.destroy();
  return {midiPeak:peak,midiBeforeScheduledSample:before,midiTail:late,scorePeak,maxNativeDifference:maxDifference,stateRestored:true,initialStateRestored:true,automation:true};
})()`)
	var report map[string]any
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatal(err)
	}
	t.Logf("WAM2 output/state: %s", result)
	chrome.eval(`(async()=>{
  const root=document.querySelector('#mount').firstElementChild.shadowRoot;
  root.querySelector('input').value='.75';root.querySelector('input').dispatchEvent(new Event('input'));
  await new Promise(resolve=>setTimeout(resolve,300));
  if(root.querySelector('.value').textContent!=='0.750')throw Error('GUI macro control failed');
  root.querySelector('input').focus();
  await cicadaWam.audioNode.setParameterValues({intensity:{id:'intensity',value:.55,normalized:false}});
  await new Promise(resolve=>setTimeout(resolve,300));
  if(root.querySelector('.value').textContent!=='0.550')throw Error('Focused GUI did not follow host automation');
  const state=await cicadaWam.audioNode.getState();
  state.parameterValues.intensity.value=.75;
  await cicadaWam.audioNode.setState(state);
  await new Promise(resolve=>setTimeout(resolve,300));
  if(root.querySelector('.value').textContent!=='0.750')throw Error('GUI did not follow state restoration');
})()`)
	chrome.mustCall("Runtime.evaluate", map[string]any{"expression": `document.querySelector('#mount').firstElementChild.shadowRoot.querySelector('button').click()`, "userGesture": true})
	chrome.waitFor(`document.querySelector('#mount').firstElementChild.shadowRoot.querySelector('.status').textContent==='Playing'`, 10*time.Second)
	chrome.screenshot("wam2-desktop-playing.png")
	chrome.setViewport(360, 900)
	chrome.screenshot("wam2-mobile-playing.png")
	chrome.eval(`cicadaWam.destroyGui(document.querySelector('#mount').firstElementChild);cicadaWam.audioNode.destroy();cicadaWam.audioContext.close();`)
	if strings.Contains(string(metadata), "error") {
		t.Fatalf("reference host: %s", metadata)
	}
}
