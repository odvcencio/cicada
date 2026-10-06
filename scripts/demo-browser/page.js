'use strict';
const status = document.querySelector('#status');
const call = (name, ...args) => {
  const error = globalThis.cicadaQualification[name](...args);
  if (error) throw new Error(error);
};
globalThis.qualificationCommands = [];
globalThis.qualificationResetPromise = Promise.resolve();
globalThis.qualificationPlaying = false;
globalThis.qualificationMIDI = new EventTarget(); // Simulated disconnect, not hardware acceptance.
globalThis.qualificationSamples = () => {
  const samples = new Float32Array(globalThis.qualificationAnalyser.fftSize);
  globalThis.qualificationAnalyser.getFloatTimeDomainData(samples);
  return { energy: samples.reduce((sum, x) => sum + x * x, 0), finite: samples.every(Number.isFinite) };
};
(async () => {
  const go = new Go();
  const {instance} = await WebAssembly.instantiate(await (await fetch('/assets/bridge.wasm')).arrayBuffer(), go.importObject);
  go.run(instance).catch(error => { globalThis.qualificationError = error.message; status.textContent = error.message; });
  document.querySelector('#start').disabled = false;
  status.textContent = 'Bridge ready. Audio requires a user gesture.';
})().catch(error => { status.textContent = error.message; });
document.querySelector('#start').onclick = async () => {
  try {
    const context = new AudioContext({sampleRate:48000, latencyHint:'interactive'});
    const module = await WebAssembly.compile(await (await fetch('/assets/kernel.wasm')).arrayBuffer());
    const image = globalThis.cicadaQualification.image(context.sampleRate).buffer;
    await context.audioWorklet.addModule('/audio/processor.js');
    const node = new AudioWorkletNode(context,'cicada',{numberOfInputs:0,numberOfOutputs:1,outputChannelCount:[2],processorOptions:{m:module,i:image,r:'qualification',l:0}});
    let resetResolve;
    await new Promise((resolve,reject) => {
      const timeout=setTimeout(()=>reject(new Error('kernel initialization timeout')),15000);
      node.port.onmessage=event=>{
        const data=event.data;
        if(data.t==='r'){clearTimeout(timeout);resolve();}
        if(data.t==='t'){
          call('resetReady');
          if(resetResolve){resetResolve();resetResolve=null;}
        }
        if(data.t==='m')node.port.postMessage({t:'b',bytes:data.bytes},[data.bytes]);
        if(data.t==='e'||data.t==='f'){globalThis.qualificationError=JSON.stringify(data);reject(new Error(globalThis.qualificationError));}
      };
    });
    const post=node.port.postMessage.bind(node.port);
    node.port.postMessage=(data,transfer)=>{
      const stopping=data.t==='c'&&data.bytes[0]===2;
      if(data.t==='c')globalThis.qualificationCommands.push(Array.from(data.bytes));
      post(data,transfer);
      if(stopping){
        globalThis.qualificationPlaying=false;
        if(resetResolve)return;
        globalThis.qualificationResetPromise=new Promise(resolve=>resetResolve=resolve);
        const fresh=globalThis.cicadaQualification.image(context.sampleRate).buffer;
        post({t:'i',i:fresh,r:'reset'},[fresh]);
      }
    };
    const gain=context.createGain();gain.gain.value=.15;
    const analyser=context.createAnalyser();analyser.fftSize=2048;
    node.connect(gain);gain.connect(analyser);analyser.connect(context.destination);
    await context.resume();
    globalThis.qualificationContext=context;
    globalThis.qualificationAnalyser=analyser;
    call('bind',node,context,globalThis.qualificationMIDI);
    document.querySelector('#play').disabled=document.querySelector('#stop').disabled=false;
    document.querySelector('#start').disabled=true;
    status.textContent=`Actual AudioWorklet ready · ${context.sampleRate} Hz`;
  } catch(error) { globalThis.qualificationError=error.message;status.textContent=error.message; }
};
document.querySelector('#play').onclick=async()=>{await qualificationResetPromise;call('play');qualificationPlaying=true;};
document.querySelector('#stop').onclick=()=>call('stop');
const keys={a:['bass',45],s:['lead',48],d:['drums',36]};
window.addEventListener('keydown',event=>{
  if(event.ctrlKey||event.altKey||event.metaKey||!keys[event.key.toLowerCase()])return;
  const [track,note]=keys[event.key.toLowerCase()];
  event.preventDefault();call('down','key:'+event.code,track,note,110,event.repeat);
});
window.addEventListener('keyup',event=>{if(keys[event.key.toLowerCase()])call('up','key:'+event.code);});
