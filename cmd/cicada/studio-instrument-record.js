(() => {
  'use strict';
  const byId=id=>document.getElementById(id), panel=byId('instrument-record');
  if(!panel) return;
  const status=byId('instrument-status'), record=byId('instrument-mic'), stop=byId('instrument-stop');
  const capture=new window.CicadaBrowserCapture.BrowserCapture(window.cicadaBrowserAudio);
  let working=false, pack=null, sampledPack=null, modeledPack=null, timer=null, generation=0, sources=new Set(), cycle=0;
  const cache=new Map();
  function update() {
    const recording=['armed','recording','saving'].includes(capture.status.state);
    record.disabled=working||recording; stop.disabled=working||!recording;
    for(const id of ['instrument-name','instrument-root','instrument-layers','instrument-pitch','instrument-files']) byId(id).disabled=working||recording;
    byId('instrument-save-score').disabled=!pack||working;
    byId('instrument-fit').disabled=!sampledPack||working||recording;
    byId('instrument-fit-hit').disabled=!sampledPack||working||recording;
    byId('instrument-mode').disabled=!modeledPack||working||recording;
    for(const key of panel.querySelectorAll('[data-instrument-note]')) key.disabled=!pack||working;
    record.setAttribute('aria-pressed',String(recording));
  }
  function stopVoices() { generation++;for(const source of sources) { source.stop();source.disconnect(); }sources.clear(); }
  function failed(error) { status.textContent=error.message||String(error); }
  async function busy(fn) {
    if(working) return;
    working=true;update();
    try { await fn(); } catch(error) { failed(error); }
    finally { working=false;update(); }
  }
  async function build(files) {
    if(!files.length || files.length>32) throw new Error('Choose 1–32 WAV files');
    if(files.reduce((size,file)=>size+file.size,0)>64*1024*1024) throw new Error('Choose WAV files totalling at most 64 MiB');
    const form=new FormData();
    form.append('name',byId('instrument-name').value);form.append('root',byId('instrument-root').value);
    form.append('layers',byId('instrument-layers').value);form.append('autoPitch',String(byId('instrument-pitch').checked));
    for(const file of files) form.append('wav',file,file.name||'microphone.wav');
    status.textContent='Finding hits, estimating pitch and building the pack…';
    const response=await fetch('/api/instrument-record',{method:'POST',body:form});
    const result=await response.json();if(!response.ok) throw new Error(result.error||'Cannot build instrument');
    result.playRoot=Number(byId('instrument-root').value);sampledPack=result;modeledPack=null;byId('instrument-fit-hit').max=result.hits.length;
    byId('instrument-fit-hit').value=1;selectPack(result);
  }
  function selectPack(result) {
    stopVoices();cache.clear();pack=result;cycle=0;
    window.cicadaRecordedInstrument=pack;
    byId('instrument-mode').textContent=pack.model?'Play sampled':'Play modeled';
    const layers=new Set(pack.manifest.zones.map(z=>z.Layer)), counts=new Set(pack.manifest.zones.map(z=>z.Count));
    status.textContent=pack.model?`${pack.model.modes.length} fitted modes · root ${pack.model.rootHz.toFixed(1)} Hz · modeled instrument ready to play`:`${pack.hits.length} hits · ${layers.size} velocity layers · ${[...counts].join('/')} round robins · user recording · ready to play`;
    byId('instrument-root').value=pack.model?pack.model.rootMIDI:pack.playRoot;
    byId('instrument-map').textContent=pack.model?pack.model.modes.map((mode,index)=>`Mode ${index+1}: ${mode.frequencyHz.toFixed(1)} Hz · ratio ${mode.ratio.toFixed(3)} · decay T60 ${mode.t60.toFixed(3)} s (${Math.round(mode.decayConfidence*100)}% fit confidence)`).join('\n'):pack.hits.map((hit,index)=>`Take ${index+1}: ${(hit.start/hit.rate).toFixed(3)}–${(hit.end/hit.rate).toFixed(3)} s · ${hit.loudnessDB.toFixed(1)} dB · ${hit.pitchHz?hit.pitchHz.toFixed(1)+' Hz':'unpitched'} (${Math.round(hit.confidence*100)}% confidence) · root ${hit.root}`).join('\n');
    byId('instrument-declaration').textContent=pack.declaration;
    byId('instrument-score-path').textContent=`Score saved: ${pack.scorePath}`;
    byId('instrument-result').hidden=false;
    update();
  }
  byId('instrument-fit').addEventListener('click',()=>busy(async()=>{
    status.textContent='Fitting resonances and decay times…';
    const response=await fetch('/api/instrument-fit',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({sha256:sampledPack.sha256,hit:Number(byId('instrument-fit-hit').value)})});
    const result=await response.json();if(!response.ok) throw new Error(result.error||'Cannot fit model');
    modeledPack=result;window.cicadaModeledInstrument=result;selectPack(result);
  }));
  byId('instrument-mode').addEventListener('click',()=>selectPack(pack.model?sampledPack:modeledPack));
  // Preserve raw frame gaps while converting committed browser PCM to WAV.
  function recordingWAV(take) {
    const rate=take.metadata.sampleRate, channels=take.metadata.channels, frames=take.rawFrames;
    if(!Number.isSafeInteger(frames)||frames<1||frames>8388608) throw new Error('Recording exceeds 8,388,608 frames');
    if(take.incomplete) {
      const gaps=take.blocks.reduce((count,block)=>count+(block.timing.GapFrames||0),0);
      throw new Error(`Microphone recording is incomplete (${Math.round(gaps/rate*1000)} ms of missing audio); record again or recover the PCM take`);
    }
    const wav=new ArrayBuffer(44+frames*4), view=new DataView(wav), pcm=new Float32Array(take.pcm);
    const tag=(offset,value)=>[...value].forEach((c,i)=>view.setUint8(offset+i,c.charCodeAt(0)));
    tag(0,'RIFF');view.setUint32(4,wav.byteLength-8,true);tag(8,'WAVEfmt ');view.setUint32(16,16,true);
    view.setUint16(20,3,true);view.setUint16(22,1,true);view.setUint32(24,rate,true);view.setUint32(28,rate*4,true);
    view.setUint16(32,4,true);view.setUint16(34,32,true);tag(36,'data');view.setUint32(40,frames*4,true);
    for(const block of take.blocks) for(let f=0;f<block.timing.Frames;f++) {
      let sum=0;for(let ch=0;ch<channels;ch++) sum+=pcm[block.offset/4+f*channels+ch];
      view.setFloat32(44+(block.rawFrame+f)*4,sum/channels,true);
    }
    return new File([wav],'microphone.wav',{type:'audio/wav'});
  }
  record.addEventListener('click',()=>busy(async()=>{
    stopVoices();status.textContent='Opening microphone…';
    await capture.arm(1,{instrument:true});await capture.recordInstrument();
    status.textContent='Recording · tap 15 times, from soft to hard, then stop · monitor off';
    timer=setTimeout(()=>stop.click(),60000);
  }));
  stop.addEventListener('click',()=>busy(async()=>{
    clearTimeout(timer);timer=null;
    await capture.stop();const take=await capture.recover();
    await build([recordingWAV(take)]);
  }));
  capture.onStatus(s=>{ if(s.error) failed(new Error(s.error));update(); });
  byId('instrument-files').addEventListener('change',event=>busy(()=>build([...event.target.files])));
  panel.addEventListener('dragover',event=>{event.preventDefault();});
  panel.addEventListener('drop',event=>{event.preventDefault();if(['armed','recording','saving'].includes(capture.status.state))return;busy(()=>build([...event.dataTransfer.files]));});
  async function play(note,velocity) {
    if(!pack) return;
    const selected=pack, epoch=generation, takeCycle=cycle++;
    const context=window.cicadaBrowserAudio.context || window.cicadaInstrumentContext || new AudioContext({sampleRate:48000});
    window.cicadaInstrumentContext=context;
    await context.resume();
    const key=`${note}:${velocity}:${takeCycle%256}`;
    let decoded=cache.get(key);
    if(!decoded) {
      const response=await fetch('/api/instrument-audition',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({sha256:selected.sha256,note,velocity,cycle:takeCycle})});
      if(!response.ok) throw new Error((await response.json()).error||'Cannot play instrument');
      decoded=await context.decodeAudioData(await response.arrayBuffer());
      if(cache.size>=128) cache.delete(cache.keys().next().value);cache.set(key,decoded);
    }
    if(epoch!==generation || selected!==pack) return;
    const source=context.createBufferSource();source.buffer=decoded;source.connect(context.destination);sources.add(source);
    source.onended=()=>{sources.delete(source);source.disconnect();};source.start();
  }
  for(const key of panel.querySelectorAll('[data-instrument-note]')) key.addEventListener('click',()=>{
    const note=Number(byId('instrument-root').value)+Number(key.dataset.instrumentNote);
    play(note,Number(byId('instrument-velocity').value)).catch(failed);
  });
  byId('instrument-silence').addEventListener('click',stopVoices);
  byId('instrument-velocity').addEventListener('input',event=>{byId('instrument-velocity-value').value=event.target.value;});
  byId('instrument-save-score').addEventListener('click',()=>{
    const name=pack.manifest.id, root=Number(byId('instrument-root').value);
    const pitch=['c','db','d','eb','e','f','gb','g','ab','a','bb','b'][root%12]+(Math.floor(root/12)-1);
    const score=`cicada 2\n${pack.declaration}\ntrack recorded ${name} { level = -6dB }\npattern taps { ${pitch} . ${pitch}^ . ${pitch} . ${pitch}^ . }\nscene main { recorded = taps }\nsong { main*4 }\n`;
    const url=URL.createObjectURL(new Blob([score],{type:'text/plain'})),link=document.createElement('a');link.href=url;link.download=name+'.cicada';link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
  });
  window.addEventListener('pagehide',()=>{clearTimeout(timer);stopVoices();capture.stop().catch(()=>{});});
  // Expose playback for MIDI hosts and the browser fixture contract.
  window.cicadaPlayRecordedInstrument=play;
  update();
})();
