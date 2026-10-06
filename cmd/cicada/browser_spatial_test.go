//go:build browser

package main

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestBrowserSpatialParity(t *testing.T) {
	source := []byte(`tempo 120
key c major
track bass acid { cutoff = 900Hz }
pattern pulse { 1~ 1~ 1~ 1 }
scene loop { bass = pulse }
song { loop*1 }
`)
	score, diagnostics := notation.Parse(source)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatal(diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	native, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	commands := []cmd.Command{
		{Op: cmd.OpSeek, Track: 255}, {Op: cmd.OpPlay, Track: 255}, cmd.TrackPosition(0, 1, 0, 0, 0),
		cmd.ListenerRotation(math.Pi/2, 0, 0, 7), cmd.ListenerPosition(.125, -.25, .375, 14),
		cmd.ListenerRotation(.731, -.325, 1.51, 21), cmd.TrackPosition(0, -.5, -.75, 1.25, 28),
	}
	if !native.PushBatch(commands) {
		t.Fatal("spatial commands rejected")
	}
	reference := make([]byte, 4+len(commands)*24+8192*8)
	binary.LittleEndian.PutUint32(reference, uint32(len(commands)*24))
	for i, c := range commands {
		record, err := cmd.EncodeCommand(c, uint8(cfg.Tracks))
		if err != nil {
			t.Fatal(err)
		}
		copy(reference[4+i*24:], record[:])
	}
	var left, right [128]float32
	for block := 0; block < 64; block++ {
		native.Render(left[:], right[:])
		for i := range left {
			at := 4 + len(commands)*24 + (block*128+i)*8
			binary.LittleEndian.PutUint32(reference[at:], math.Float32bits(left[i]))
			binary.LittleEndian.PutUint32(reference[at+4:], math.Float32bits(right[i]))
		}
	}
	server := startBrowserStudio(t, source, reference)
	chrome := startBrowserChrome(t, server)
	chrome.navigate("http://" + browserStudioAddress + "/")
	result := chrome.eval(`(async()=>{
  const ref=await (await fetch('/__test/reference')).arrayBuffer(),view=new DataView(ref);
  const commandBytes=view.getUint32(0,true),commands=new Uint8Array(ref,4,commandBytes).slice();
  const context=new OfflineAudioContext(2,8192,48000);
  await context.audioWorklet.addModule('/audio/cicada-processor.js');
  const module=await WebAssembly.compile(await (await fetch('/api/kernel.wasm')).arrayBuffer());
  const image=await (await fetch('/api/kernel-image?rate=48000')).arrayBuffer();
  let resolveReady,rejectReady,faults=0;
  const ready=new Promise((resolve,reject)=>{resolveReady=resolve;rejectReady=reject});
  const node=new AudioWorkletNode(context,'cicada',{numberOfInputs:0,numberOfOutputs:1,outputChannelCount:[2],processorOptions:{m:module,i:image}});
  node.port.onmessage=event=>{
    const data=event.data;
    if(data.t==='r')resolveReady(data);
    if(['e','x','f'].includes(data.t)){faults++;rejectReady(Error(data.e||'spatial fault'))}
    if(data.t==='m'){
      const v=new DataView(data.bytes);
      for(let at=0;at<data.n;at+=16)if(v.getUint8(at)===7)faults++;
      node.port.postMessage({t:'b',bytes:data.bytes},[data.bytes]);
    }
  };
  node.connect(context.destination);const info=await ready;
  if(!(info.p&131072))throw Error('Missing spatial capability');
  node.port.postMessage({t:'c',bytes:commands});
  const rendered=await context.startRendering();
  let mismatches=0,peak=0;
  for(let c=0;c<2;c++)for(let i=0;i<8192;i++){
    const expected=view.getFloat32(4+commandBytes+i*8+c*4,true);
    const faded=Math.fround(expected*(i<240?(i+1)/240:1));
    const actual=rendered.getChannelData(c)[i];
    if(actual!==faded)mismatches++;
    peak=Math.max(peak,Math.abs(actual));
  }
  const {exports:x}=await WebAssembly.instantiate(module);x._initialize();
  const ptr=x.gosx_audio_project_alloc(image.byteLength);new Uint8Array(x.memory.buffer,ptr,image.byteLength).set(new Uint8Array(image));
  if(x.gosx_audio_init(48000,128,2)!==0)throw Error('Diagnostic kernel initialization failed');
  const before=x.gosx_audio_allocation_count(),beforeBytes=x.gosx_audio_alloc_bytes(),beforeMemory=x.memory.buffer.byteLength;
  new Uint8Array(x.memory.buffer,x.gosx_audio_cmd_ptr(),commands.length).set(commands);x.gosx_audio_cmd_commit(commands.length/24);
  for(let block=0;block<64;block++){x.gosx_audio_render(128);x.gosx_audio_msg_drain()}
  return {frames:8192,mismatches,peak,faults,allocations:Number(x.gosx_audio_allocation_count()-before),allocatedBytes:Number(x.gosx_audio_alloc_bytes()-beforeBytes),memoryGrowth:x.memory.buffer.byteLength-beforeMemory};
})()`)
	var report struct {
		Frames, Mismatches, Faults, Allocations, AllocatedBytes, MemoryGrowth int
		Peak                                                                  float64
	}
	if err := json.Unmarshal(result, &report); err != nil {
		t.Fatalf("browser spatial result: %s: %v", result, err)
	}
	if report.Frames != 8192 || report.Mismatches != 0 || report.Faults != 0 || report.Allocations != 0 || report.AllocatedBytes != 0 || report.MemoryGrowth != 0 || report.Peak <= 0 {
		t.Fatalf("browser spatial parity or allocation gate failed: %s", result)
	}
	t.Logf("Chrome AudioWorklet spatial parity and kernel allocations: %s", result)
}
