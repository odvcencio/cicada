(() => {
  'use strict';
  const byId=id=>document.getElementById(id), api=window.CicadaBrowserCapture;
  const arm=byId('pcm-arm'),record=byId('pcm-record'),stop=byId('pcm-stop'),recover=byId('pcm-recover');
  if(!arm || !api) return;
  let commit=byId('pcm-commit'), input=byId('pcm-browser-input'), refreshInputs=byId('pcm-refresh-inputs');
  if (document.createElement && arm.parentElement) {
    if (!commit) { commit=document.createElement('button'); commit.id='pcm-commit'; commit.type='button'; arm.parentElement.append(commit); }
    if (!input) {
      const label=document.createElement('label'); label.textContent='Browser microphone '; input=document.createElement('select'); input.id='pcm-browser-input'; label.append(input); arm.parentElement.prepend(label);
    }
    if (!refreshInputs) { refreshInputs=document.createElement('button'); refreshInputs.id='pcm-refresh-inputs'; refreshInputs.type='button'; refreshInputs.textContent='Refresh audio inputs'; arm.parentElement.append(refreshInputs); }
    arm.closest('.audio-io-panel')?.querySelector('p')?.replaceChildren(document.createTextNode('Arm a selected microphone and record with a count-in. Browser Stop retains recoverable PCM on this device; Recover loads it locally. Commit to project explicitly imports the selected take. Native Stop saves to the project journal. Microphone exposure depends on the OS and connection; gamepad buttons do not imply an audio input.'));
  }
  let captureMode=byId('pcm-backend');
  if (document.createElement && arm.parentElement && !captureMode) {
    const label=document.createElement('label'); label.textContent='Capture mode ';
    captureMode=document.createElement('select'); captureMode.id='pcm-backend';
    for (const [value,text] of [['browser','Browser · local microphone'],['native','Native · configured input']]) { const option=document.createElement('option'); option.value=value; option.textContent=text; captureMode.append(option); }
    captureMode.value=byId('audio-mode').value; label.append(captureMode); arm.parentElement.prepend(label);
    captureMode.addEventListener('change',()=>{ byId('audio-mode').value=captureMode.value; byId('audio-mode').dispatchEvent(new Event('change')); update(); });
  }
  if (commit) commit.textContent='Commit to project';
  const capture=new api.BrowserCapture(window.cicadaBrowserAudio);
  window.cicadaPCM=capture;
  const status=byId('pcm-status'),settings=byId('pcm-settings'),receiptKey='cicada-project-take';
  let sampler=null,working=true,native=false,target=null,auditionGeneration=0,retained=null;
  let nativeStatus={state:'idle',storage:'project journal',timing:'device reported',calibration:'uncalibrated'};
  const revision=()=>window.cicadaStudio?.revision() || document.body.dataset.revision;
  const state=()=>native?nativeStatus:capture.status;
  const hasOption=(id,value)=>[...byId(id).options].some(option=>option.value===value);
  const targetChanged=()=>target && (!hasOption('pcm-track',target.track) || !hasOption('pcm-scene',target.scene));
  function updateNative(snapshot) {
    if(!snapshot) return;
    nativeStatus={...nativeStatus,state:snapshot.recording?'recording':'armed',incomplete:snapshot.incomplete,error:snapshot.error || '',writtenFrames:snapshot.writtenFrames,countInRemainingFrames:snapshot.countInRemainingFrames};
  }
  function refreshTargets(next) {
    for(const id of ['pcm-track','pcm-scene']) {
      const select=byId(id),previous=select.value,options=next.querySelector('#'+id);
      if(!options) continue;
      select.replaceChildren(...[...options.childNodes].map(option=>option.cloneNode(true)));
      if(hasOption(id,previous)) select.value=previous;
    }
    update();
  }
  window.addEventListener('cicada:projectionrefreshed',event=>{
    if(event.detail?.document) refreshTargets(event.detail.document);
  });
  window.addEventListener('cicada:audiostate',event=>{
    if(native && ['armed','recording'].includes(nativeStatus.state)) {
      updateNative(event.detail?.runtime?.capture);update();
    }
  });
  function update(s=state()) {
    const idle=['idle','stopped'].includes(s.state);
    arm.disabled=working || !idle || !byId('pcm-track').value || !byId('pcm-scene').value;
    record.disabled=working || s.state!=='armed' || !!targetChanged();
    stop.disabled=working || !['armed','recording'].includes(s.state);
    recover.disabled=working || !idle;
    if (commit) { commit.disabled=working || !idle || native || !retained || !byId('pcm-track').value || !byId('pcm-scene').value; commit.hidden=native; }
    if (captureMode) { captureMode.disabled=working || !idle; captureMode.value=byId('audio-mode').value; }
    if (input) input.disabled=working || !idle || byId('audio-mode').value==='native';
    stop.textContent=byId('audio-mode').value==='native'?'Stop and save':'Stop and retain locally';
    for(const id of ['pcm-channels','pcm-track','pcm-scene','audio-mode']) byId(id).disabled=!idle || working;
    arm.setAttribute('aria-pressed',String(!idle));
    status.textContent=`${s.state} · storage ${s.storage} · timing ${s.timing} · ${s.calibration} · ${native?'device monitor settings':'monitor off'}${s.incomplete?' · take incomplete':''}${native && s.writtenFrames!==undefined?' · '+s.writtenFrames+' frames saved · count-in '+s.countInRemainingFrames+' frames remaining':''}${!idle && targetChanged()?' · recording target changed ('+target.track+' / '+target.scene+'); stop to retain the take':''}${s.error?' · '+s.error:''}`;
    if(s.settings) settings.textContent=['echoCancellation','noiseSuppression','autoGainControl','sampleRate','channelCount','latency'].map(key=>`${key}: ${s.settings[key] ?? 'unreported'}`).join(' · ');
  }
  capture.onStatus(()=>update());
  function receipt(value) {
    try {
      if(value) localStorage.setItem(receiptKey,JSON.stringify(value));
      return JSON.parse(localStorage.getItem(receiptKey) || 'null');
    } catch (_) { return value || null; }
  }
  async function command(action,extra={}) {
    const response=await fetch('/api/takes',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({...target,revision:revision(),action,...extra})});
    const result=await response.json();
    if(!response.ok) throw new Error(result.error || 'Take operation failed');
    return result;
  }
  function stopSampler() {
    auditionGeneration++;
    sampler?.stop();
  }
  async function published(result,browserId) {
    receipt({takeId:result.take,browserId,track:target?.track,scene:target?.scene});
    stopSampler();
    await window.cicadaBrowserAudio.startAudio(true);
    sampler=new api.PublishedSampler(window.cicadaBrowserAudio.context,result.take,result.revision);
    byId('sampler-play').disabled=false;byId('sampler-stop').disabled=false;
    byId('sampler-status').textContent=`Project take ${result.take} · shared sampler voice · browser PCM retained`;
    await window.cicadaRefreshProjection?.({revision:result.revision,source:result.source});
  }
  async function publishBrowser() {
    const take=retained || await capture.recover();
    try { await published(await api.publishTake(take,target),take.id); }
    catch(error) {
      if(error.takeId) receipt({takeId:error.takeId,browserId:take.id,track:target.track,scene:target.scene});
      throw error;
    }
  }
  function action(button,fn) {
    button.addEventListener('click',async()=>{
      if(working) return;
      working=true;update();
      try { state().error='';await fn(); }
      catch(error) { state().error=error.message; }
      finally { working=false;update(); }
    });
  }
  action(arm,async()=>{
    if(window.cicadaStudio?.dirty?.()) throw new Error('Save source edits before recording');
    target={track:byId('pcm-track').value,scene:byId('pcm-scene').value,revision:revision()};
    if(!target.track || !target.scene) throw new Error('Add an audio track and scene before recording');
    stopSampler();native=byId('audio-mode').value==='native';
    if(native) {
      const result=await command('arm');
      nativeStatus={...nativeStatus,state:'armed',error:'',incomplete:false,writtenFrames:0,countInRemainingFrames:0};
      receipt({takeId:result.activeCapture,track:target.track,scene:target.scene});
      settings.textContent='Native capture uses the configured duplex device layout.';
    } else {
      retained=null;
      await capture.arm(Number(byId('pcm-channels').value),input?.value || '');
      refreshInputList().catch(() => {});
      // A newly admitted take supersedes the previous publication receipt.
      // Until publication succeeds, recovery must use this browser take.
      try { localStorage.removeItem(receiptKey); } catch (_) {}
      // Keep the revision and target across a browser reload or capture fault.
      capture.take.target=target;
      try { localStorage.setItem('cicada-last-take',JSON.stringify(capture.take)); } catch (_) {}
    }
  });
  action(record,async()=>{
    if(native) { await command('start');nativeStatus.state='recording'; }
    else await capture.record();
  });
  action(stop,async()=>{
    if(native) {
      try { await published(await command('stop')); }
      finally { nativeStatus.state='stopped'; }
    } else { await capture.stop();await retainBrowser(); }
  });
  async function retainBrowser() {
    retained=await capture.recover();
    target=retained.metadata?.target || capture.take?.target || target;
    stopSampler();
    await window.cicadaBrowserAudio.startAudio(true);
    if (api.TakeSampler) { sampler=new api.TakeSampler(window.cicadaBrowserAudio.context); sampler.load(retained); }
    byId('sampler-play').disabled=!sampler; byId('sampler-stop').disabled=!sampler;
    byId('sampler-status').textContent='Local take retained on this device' + (retained.incomplete?' · incomplete':'') + ' · Commit to project to import';
    return retained;
  }
  if (commit) action(commit,async()=>{
    if (window.cicadaStudio?.dirty?.()) throw new Error('Save source edits before importing a take');
    target={track:byId('pcm-track').value,scene:byId('pcm-scene').value,revision:revision()};
    if (!target.track || !target.scene) throw new Error('Select an audio track and scene before committing');
    await publishBrowser();
  });
  action(recover,async()=>{
    stopSampler();
    const saved=receipt();
    if(byId('audio-mode').value==='native' && saved?.takeId) {
      target={track:saved.track,scene:saved.scene,revision:revision()};
      await published(await command('recover',{takeId:saved.takeId}),saved.browserId);
      return;
    }
    if(byId('audio-mode').value==='native') {
      const response=await fetch('/api/takes',{cache:'no-store'}),result=await response.json();
      if(!response.ok) throw new Error(result.error || 'Cannot read project takes');
      const take=(result.takes || []).filter(t=>t.frames>0).sort((a,b)=>a.created.localeCompare(b.created)).at(-1);
      if(!take) throw new Error('No retained project take to recover');
      await published(await command('recover',{takeId:take.id}));
    } else {
      let index=capture.take;
      if(!index) { try { index=JSON.parse(localStorage.getItem('cicada-last-take') || 'null'); } catch (_) {} }
      target=index?.target || {track:byId('pcm-track').value,scene:byId('pcm-scene').value,revision:revision()};
      // Recovery is an explicit selection against the current score revision.
      target={...target,revision:revision()};
      native=false; await retainBrowser();
    }
  });
  action(byId('sampler-play'),async()=>{
    const generation=++auditionGeneration,audition=sampler;
    await window.cicadaBrowserAudio.context.resume();
    if(generation!==auditionGeneration || audition!==sampler) return;
    await audition.play(Number(byId('sampler-note').value),Number(byId('sampler-root').value),byId('sampler-loop').checked);
  });
  for(const id of ['pcm-track','pcm-scene','audio-mode']) byId(id).addEventListener('change',()=>update());
  async function restoreNativeCapture() {
    try {
      const response=await fetch('/api/takes',{cache:'no-store'}),result=await response.json();
      if(!response.ok) throw new Error(result.error || 'Cannot read project takes');
      if(result.activeCapture) {
        const take=(result.takes || []).find(take=>take.id===result.activeCapture);
        if(!take) throw new Error('Active capture target is unavailable');
        native=true;target={track:take.track,scene:take.scene,revision:take.expected};
        byId('audio-mode').value='native';byId('pcm-channels').value=String(take.channels);
        if(hasOption('pcm-track',target.track)) byId('pcm-track').value=target.track;
        if(hasOption('pcm-scene',target.scene)) byId('pcm-scene').value=target.scene;
        nativeStatus.state='armed';updateNative(result.capture);
        receipt({takeId:take.id,track:take.track,scene:take.scene});
        settings.textContent='Native capture uses the configured duplex device layout.';
      }
    } catch(error) { state().error=error.message; }
    finally { working=false;update(); }
  }
  async function refreshInputList() {
    if (!input || !document.createElement) return;
    const selected=input.value;
    const devices=await capture.listInputs();
    const option=document.createElement('option'); option.value=''; option.textContent='System default microphone';
    input.replaceChildren(option);
    devices.forEach((device,index)=>{ const option=document.createElement('option'); option.value=device.deviceId; option.textContent=device.label || `Audio input ${index+1} (label after microphone permission)`; input.append(option); });
    // A disconnected explicit selection remains visible and must fail at Arm;
    // never substitute a different microphone without the user's choice.
    if (selected && !devices.some(d=>d.deviceId===selected)) { const missing=document.createElement('option'); missing.value=selected; missing.textContent='Selected input unavailable'; input.append(missing); }
    input.value=selected;
  }
  refreshInputs?.addEventListener('click',()=>refreshInputList().catch(error=>{state().error=error.message;update();}));
  window.navigator?.mediaDevices?.addEventListener?.('devicechange',()=>refreshInputList().catch(()=>{}));
  update();restoreNativeCapture(); refreshInputList().catch(()=>{});
  // Stop must remain available while Play is awaiting resume, fetch or decode.
  byId('sampler-stop').addEventListener('click',stopSampler);
})();
