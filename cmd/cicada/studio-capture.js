(() => {
  'use strict';
  const byId = id => document.getElementById(id);
  const arm=byId('pcm-arm'), record=byId('pcm-record'), stop=byId('pcm-stop'), recover=byId('pcm-recover');
  if(!arm || !window.CicadaBrowserCapture) return;
  const capture=new window.CicadaBrowserCapture.BrowserCapture(window.cicadaBrowserAudio);
  window.cicadaPCM=capture;
  const status=byId('pcm-status'), settings=byId('pcm-settings');
  let sampler=null, working=false;
  function update(state) {
    const idle=['idle','stopped'].includes(state.state);
    arm.disabled=working || !idle;
    record.disabled=working || state.state!=='armed';
    stop.disabled=working || !['armed','recording'].includes(state.state);
    recover.disabled=working || !idle;
    byId('pcm-channels').disabled=!idle || working;
    arm.setAttribute('aria-pressed',String(!idle));
    status.textContent=`${state.state} · storage ${state.storage} · timing ${state.timing} · ${state.calibration} · monitor off${state.incomplete?' · take incomplete':''}${state.error?' · '+state.error:''}`;
    if(state.settings) settings.textContent=['echoCancellation','noiseSuppression','autoGainControl','sampleRate','channelCount','latency'].map(key=>`${key}: ${state.settings[key] ?? 'unreported'}`).join(' · ');
  }
  capture.onStatus(update);
  function action(button, fn) {
    button.addEventListener('click',async()=>{
      if(working) return;
      working=true;update(capture.status);
      try { await fn(); }
      catch(error) { status.textContent=error.message; }
      finally { working=false;update({...capture.status,error:capture.status.error || (status.textContent.includes(' · ')?'':status.textContent)}); }
    });
  }
  action(arm,async()=>{
    if(byId('audio-mode').value!=='browser') throw new Error('Select Browser audio before arming PCM capture');
    sampler?.stop();
    await capture.arm(Number(byId('pcm-channels').value));
  });
  action(record,()=>capture.record());
  action(stop,()=>capture.stop());
  action(recover,async()=>{
    const take=await capture.recover();
    await window.cicadaBrowserAudio.startAudio();
    sampler=new window.CicadaBrowserCapture.TakeSampler(window.cicadaBrowserAudio.context);
    sampler.load(take);
    byId('sampler-play').disabled=false;
    byId('sampler-stop').disabled=false;
    byId('sampler-status').textContent=`Recovered ${take.rawFrames} raw frames · ${take.incomplete?'incomplete take; known gaps retained':'finalized take'} · one audition voice`;
  });
  action(byId('sampler-play'),async()=>{await window.cicadaBrowserAudio.context.resume();sampler.play(Number(byId('sampler-note').value),Number(byId('sampler-root').value),byId('sampler-loop').checked);});
  action(byId('sampler-stop'),()=>sampler?.stop());
})();
