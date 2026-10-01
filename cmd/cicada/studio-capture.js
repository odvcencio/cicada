(() => {
  'use strict';
  const byId=id=>document.getElementById(id), api=window.CicadaBrowserCapture;
  const arm=byId('pcm-arm'),record=byId('pcm-record'),stop=byId('pcm-stop'),recover=byId('pcm-recover');
  if(!arm || !api) return;
  const capture=new api.BrowserCapture(window.cicadaBrowserAudio);
  window.cicadaPCM=capture;
  const status=byId('pcm-status'),settings=byId('pcm-settings'),receiptKey='cicada-project-take';
  let sampler=null,working=false,native=false,target=null;
  let nativeStatus={state:'idle',storage:'project journal',timing:'device reported',calibration:'uncalibrated'};
  const revision=()=>window.cicadaStudio?.revision() || document.body.dataset.revision;
  const state=()=>native?nativeStatus:capture.status;
  function update(s=state()) {
    const idle=['idle','stopped'].includes(s.state);
    arm.disabled=working || !idle;
    record.disabled=working || s.state!=='armed';
    stop.disabled=working || !['armed','recording'].includes(s.state);
    recover.disabled=working || !idle;
    for(const id of ['pcm-channels','pcm-track','pcm-scene','audio-mode']) byId(id).disabled=!idle || working;
    arm.setAttribute('aria-pressed',String(!idle));
    status.textContent=`${s.state} · storage ${s.storage} · timing ${s.timing} · ${s.calibration} · ${native?'device monitor settings':'monitor off'}${s.incomplete?' · take incomplete':''}${s.error?' · '+s.error:''}`;
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
  async function published(result,browserId) {
    receipt({takeId:result.take,browserId,track:target?.track,scene:target?.scene});
    sampler?.stop();
    await window.cicadaBrowserAudio.startAudio(true);
    sampler=new api.PublishedSampler(window.cicadaBrowserAudio.context,result.take,result.revision);
    byId('sampler-play').disabled=false;byId('sampler-stop').disabled=false;
    byId('sampler-status').textContent=`Project take ${result.take} · shared sampler voice · browser PCM retained`;
    await window.cicadaRefreshProjection?.({revision:result.revision,source:result.source});
  }
  async function publishBrowser() {
    const take=await capture.recover();
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
    if(window.cicadaStudio?.dirty()) throw new Error('Save source edits before recording');
    target={track:byId('pcm-track').value,scene:byId('pcm-scene').value,revision:revision()};
    if(!target.track || !target.scene) throw new Error('Add an audio track and scene before recording');
    sampler?.stop();native=byId('audio-mode').value==='native';
    if(native) {
      const result=await command('arm');
      nativeStatus={...nativeStatus,state:'armed',error:'',incomplete:false};
      receipt({takeId:result.activeCapture,track:target.track,scene:target.scene});
      settings.textContent='Native capture uses the configured duplex device layout.';
    } else {
      await capture.arm(Number(byId('pcm-channels').value));
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
    } else { await capture.stop();await publishBrowser(); }
  });
  action(recover,async()=>{
    sampler?.stop();
    const saved=receipt();
    if(saved?.takeId) {
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
      await publishBrowser();
    }
  });
  action(byId('sampler-play'),async()=>{await window.cicadaBrowserAudio.context.resume();await sampler.play(Number(byId('sampler-note').value),Number(byId('sampler-root').value),byId('sampler-loop').checked);});
  action(byId('sampler-stop'),()=>sampler?.stop());
})();
