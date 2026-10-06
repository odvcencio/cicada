(function(root) {
  'use strict';
  const MAX_BYTES=32*1024*1024;
  function recordingWAV(take) {
    const {sampleRate:rate,channels}=take.metadata, frames=take.rawFrames;
    if(take.incomplete) throw new Error('Recording has missing audio. Record the melody again.');
    if(!Number.isSafeInteger(frames)||frames<1||frames>rate*60||44+frames*4>MAX_BYTES) throw new Error('Record a melody of at most 60 seconds.');
    const wav=new ArrayBuffer(44+frames*4), view=new DataView(wav), pcm=new Float32Array(take.pcm);
    const tag=(offset,value)=>[...value].forEach((c,i)=>view.setUint8(offset+i,c.charCodeAt(0)));
    tag(0,'RIFF');view.setUint32(4,wav.byteLength-8,true);tag(8,'WAVEfmt ');view.setUint32(16,16,true);
    view.setUint16(20,3,true);view.setUint16(22,1,true);view.setUint32(24,rate,true);view.setUint32(28,rate*4,true);
    view.setUint16(32,4,true);view.setUint16(34,32,true);tag(36,'data');view.setUint32(40,frames*4,true);
    for(const block of take.blocks) for(let f=0;f<block.timing.Frames;f++) {
      let sum=0;for(let c=0;c<channels;c++) sum+=pcm[block.offset/4+f*channels+c];
      view.setFloat32(44+(block.rawFrame+f)*4,sum/channels,true);
    }
    return wav;
  }
  async function discardTake(capture,env=root) {
    const take=capture.take;
    if(!take) return;
    capture.worker?.terminate();capture.worker=null;capture.take=null;
    if(take.mode==='opfs') {
      const directory=await env.navigator.storage.getDirectory();
      const takes=await directory.getDirectoryHandle('cicada-takes');
      await takes.removeEntry(take.id,{recursive:true});
    } else if(take.mode==='indexeddb') {
      await new Promise((resolve,reject)=>{
        const request=env.indexedDB.open('cicada-pcm',1);
        request.onerror=()=>reject(request.error);
        request.onsuccess=()=>{
          const db=request.result,tx=db.transaction('takes','readwrite'),store=tx.objectStore('takes');
          const cursor=store.openKeyCursor(env.IDBKeyRange.bound(take.id+':',take.id+':\uffff'));
          cursor.onsuccess=()=>{const item=cursor.result;if(item){store.delete(item.primaryKey);item.continue();}};
          tx.oncomplete=()=>{db.close();resolve();};tx.onerror=tx.onabort=()=>{db.close();reject(tx.error);};
        };
      });
    }
    try {
      if(JSON.parse(env.localStorage.getItem('cicada-last-take')||'null')?.id===take.id) env.localStorage.removeItem('cicada-last-take');
      env.localStorage.removeItem('cicada-transcription-take');
    } catch(_) {}
  }
  function mount(document,env=root) {
    const byId=id=>document.getElementById(id),panel=byId('melody-transcribe');
    if(!panel) return;
    const status=byId('melody-status'),capture=new env.CicadaBrowserCapture.BrowserCapture(env.cicadaBrowserAudio);
    let working=false,result=null,original=null,revision='',controller=null,timer=null,epoch=0,voices=new Set(),previousIndex=null,previousID='';
    const active=()=>['arming','armed','recording','saving','recovering'].includes(capture.status.state);
    function update() {
      byId('melody-mic').disabled=working||active();byId('melody-stop').disabled=working||capture.status.state!=='recording';
      byId('melody-cancel').disabled=!working&&!active()&&!result&&!original;
      for(const id of ['melody-file','melody-tempo','melody-key','melody-grid']) byId(id).disabled=working||active();
      for(const id of ['melody-apply','melody-download','melody-play']) byId(id).disabled=working||active()||!result;
      byId('melody-mic').setAttribute('aria-pressed',String(capture.status.state==='recording'));
    }
    function failed(error) { status.textContent=error.message||String(error); }
    function stopVoices() { for(const voice of voices){voice.stop();voice.disconnect();}voices.clear(); }
    function clearOriginal() {if(original)env.URL.revokeObjectURL(original);original=null;byId('melody-original').removeAttribute('src');}
    async function discardRecording(take=capture.take) {
      if(!take||take.id===previousID)return;
      await discardTake({take,worker:capture.worker},env);capture.worker=null;capture.take=null;
      if(previousIndex&&!env.localStorage.getItem('cicada-last-take'))env.localStorage.setItem('cicada-last-take',previousIndex);
    }
    async function busy(fn) {
      if(working) return;
      working=true;update();const ticket=epoch;
      try { await fn(ticket); } catch(error) { if(ticket===epoch&&error.name!=='AbortError')failed(error); }
      finally {working=false;update();}
    }
    async function analyze(file,ticket) {
      if(file.size>MAX_BYTES) throw new Error('Choose one WAV file of at most 32 MiB.');
      result=null;byId('melody-result').hidden=true;clearOriginal();
      original=env.URL.createObjectURL(file);byId('melody-original').src=original;
      revision=env.cicadaStudio.revision();
      const form=new env.FormData();form.append('wav',file,file.name||'melody.wav');
      form.append('tempo',byId('melody-tempo').value);form.append('key',byId('melody-key').value);form.append('grid',byId('melody-grid').value);
      controller=new env.AbortController();status.textContent='Finding pitches, notes and timing…';
      const response=await env.fetch('/api/transcribe',{method:'POST',body:form,signal:controller.signal});
      const decoded=await response.json();if(!response.ok)throw new Error(decoded.error||'Cannot transcribe this recording.');
      if(ticket!==epoch) return;
      result=decoded;controller=null;byId('melody-score').textContent=result.source;
      const confidence=result.notes.reduce((sum,n)=>sum+n.confidence,0)/result.notes.length;
      byId('melody-summary').textContent=`${result.notes.length} notes · ${result.tempo.toFixed(1)} BPM (${Math.round(result.tempoConfidence*100)}%) · ${result.meter} (${Math.round(result.meterConfidence*100)}%) · ${result.key} (${Math.round(result.keyConfidence*100)}%) · pitch confidence ${Math.round(confidence*100)}% · grid moved boundaries ${(result.quantizationMS||0).toFixed(1)} ms on average`;
      byId('melody-notes').textContent=result.notes.map((n,i)=>`${i+1}: ${n.start.toFixed(3)}–${n.end.toFixed(3)} s · ${n.pitchHz.toFixed(1)} Hz · MIDI ${n.midi} · ${n.cents.toFixed(1)} cents · ${Math.round(n.confidence*100)}% confidence`).join('\n');
      byId('melody-warnings').textContent=(result.warnings||[]).join(' ');
      byId('melody-result').hidden=false;status.textContent='Melody ready. Review and listen before replacing the score.';
    }
    byId('melody-file').addEventListener('change',event=>busy(ticket=>{const files=event.target.files;if(files.length!==1)throw new Error('Choose one WAV file.');return analyze(files[0],ticket);}));
    byId('melody-mic').addEventListener('click',()=>busy(async ticket=>{
      stopVoices();status.textContent='Opening microphone…';
      try{previousIndex=env.localStorage.getItem('cicada-last-take');previousID=JSON.parse(previousIndex||'null')?.id||'';}catch(_){}
      await capture.arm(1,{instrument:true});
      try{env.localStorage.setItem('cicada-transcription-take',JSON.stringify({...capture.take,previousIndex}));}catch(_){}
      if(ticket!==epoch){await capture.stop();await discardRecording();return;}
      await capture.recordInstrument();status.textContent='Recording a single melody · microphone monitoring off · maximum 60 seconds';
      timer=env.setTimeout(()=>byId('melody-stop').click(),60000);
    }));
    byId('melody-stop').addEventListener('click',()=>busy(async ticket=>{
      env.clearTimeout(timer);timer=null;
      try {
        await capture.stop();const take=await capture.recover();
        const file=new env.File([recordingWAV(take)],'melody.wav',{type:'audio/wav'});
        if(ticket===epoch)await analyze(file,ticket);
      } finally {await discardRecording();}
    }));
    async function cancel() {
      epoch++;controller?.abort();controller=null;env.clearTimeout(timer);timer=null;
      stopVoices();result=null;byId('melody-result').hidden=true;clearOriginal();
      const ownTake=capture.take;
      try{await capture.stop();await discardRecording(ownTake);}catch(error){failed(error);update();return;}
      status.textContent='Preview cleared. The score is unchanged.';update();
    }
    byId('melody-cancel').addEventListener('click',cancel);
    byId('melody-apply').addEventListener('click',()=>busy(async()=>{
      if(env.cicadaStudio.dirty()||env.cicadaStudio.busy())throw new Error('Save or discard source edits before replacing the score.');
      const response=await env.fetch('/api/source',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({revision,source:result.source})});
      const saved=await response.json();if(!response.ok)throw new Error(saved.error||'Cannot replace the score.');
      revision=saved.revision;env.dispatchEvent(new env.CustomEvent('cicada:sourcewritten',{detail:saved}));
      status.textContent='Score replaced. Undo is available in History.';
    }));
    byId('melody-download').addEventListener('click',()=>{
      const url=env.URL.createObjectURL(new env.Blob([result.source],{type:'text/plain'})),link=document.createElement('a');
      link.href=url;link.download='melody.cicada';link.click();env.setTimeout(()=>env.URL.revokeObjectURL(url),1000);
    });
    byId('melody-play').addEventListener('click',()=>busy(async()=>{
      stopVoices();const ticket=epoch,context=env.cicadaBrowserAudio.context||new env.AudioContext();
      await context.resume();if(ticket!==epoch)return;
      const first=result.notes[0]?.start||0,base=context.currentTime+0.03;
      for(const note of result.notes) {
        const source=context.createOscillator(),gain=context.createGain();source.type='triangle';source.frequency.value=note.pitchHz;
        const start=base+note.start-first,end=base+note.end-first;
        gain.gain.setValueAtTime(0,start);gain.gain.linearRampToValueAtTime(0.12,start+0.01);gain.gain.setValueAtTime(0.12,Math.max(start+0.01,end-0.01));gain.gain.linearRampToValueAtTime(0,end);
        source.connect(gain);gain.connect(context.destination);voices.add(source);
        source.onended=()=>{voices.delete(source);source.disconnect();gain.disconnect();};source.start(start);source.stop(end);
      }
    }));
    byId('melody-silence').addEventListener('click',()=>{epoch++;stopVoices();byId('melody-original').pause();});
    capture.onStatus(state=>{if(state.error)failed(new Error(state.error));update();});
    env.addEventListener('pagehide',cancel);
    try {
      const retained=JSON.parse(env.localStorage.getItem('cicada-transcription-take')||'null');
      if(retained){previousIndex=retained.previousIndex;previousID=JSON.parse(previousIndex||'null')?.id||'';discardRecording(retained).catch(failed);}
    } catch(_) {}
    update();return {analyze,cancel,capture};
  }
  const api={recordingWAV,discardTake,mount};
  if(typeof module!=='undefined')module.exports=api;
  root.CicadaStudioTranscribe=api;
  if(root.document)mount(root.document,root);
})(globalThis);
