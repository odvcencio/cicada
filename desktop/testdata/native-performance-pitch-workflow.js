// Disposable synthetic phrase and null audio only.
(() => {
  if(location.hostname!=='127.0.0.1'||window.__cicadaPerformancePitchSmoke)return;
  window.__cicadaPerformancePitchSmoke=true;
  const key='cicada.performance-pitch-proof',retained=JSON.parse(sessionStorage.getItem(key)||'null'),checks=retained?.checks||[];
  const read=async(name='workspace')=>{
    if(name!=='workspace'){const r=await fetch('/api/'+name,{cache:'no-store'});if(!r.ok)throw new Error(name+': '+r.status);return r.json();}
    const [state,r]=await Promise.all([read('state'),fetch('/__workspace?panel=code&file=phrase.cicada',{cache:'no-store'})]);
    if(!r.ok)throw new Error('source projection: '+r.status);const projection=await r.json(),doc=new DOMParser().parseFromString(projection.html,'text/html');
    return {...state,peer:doc.querySelector('textarea').value,source_revision:projection.sourceRevision};
  };
  const wait=async(check,label)=>{const end=performance.now()+45000;while(performance.now()<end){if(await check())return;await new Promise(r=>setTimeout(r,50));}throw new Error('Timed out: '+label);};
  const ready=()=>wait(()=>window.__gosx?.ready&&document.querySelector('#studio-shell[data-shell-actions=ready]')&&!document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'),'Studio ready');
  const fill=(field,value)=>{field.value=value;field.dispatchEvent(new Event('input',{bubbles:true}));field.dispatchEvent(new Event('change',{bubbles:true}));};
  const section=()=>document.querySelector('[data-performance-pitch=phrase]');
  const saved=source=>wait(async()=>{const v=await read();return v.peer===source&&section()?.querySelector('[name=source_revision]')?.value===v.source_revision;},'secondary source and receipt');
  const pitches=source=>[...source.matchAll(/note (\d+) at=/g)].map(m=>Number(m[1]));
  async function render(label){
    document.querySelector('[data-gosx-disclosure-target="#export-dialog"]').click();const previous=(await read('export')).id;document.querySelector('#export button[type=submit]').click();let job;
    await wait(async()=>{job=await read('export');if(job.state==='failed')throw new Error(job.error);return job.id!==previous&&['succeeded','shortfall'].includes(job.state);},'WAV export');
    const r=await fetch(job.downloadURL),bytes=new Uint8Array(await r.arrayBuffer());if(!r.ok||new TextDecoder().decode(bytes.slice(0,4))!=='RIFF')throw new Error('Invalid WAV');
    const hash=[...new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))].map(x=>x.toString(16).padStart(2,'0')).join('');
    if(typeof window.__cicadaPitchAudio==='function'){let binary='';for(const b of bytes)binary+=String.fromCharCode(b);await window.__cicadaPitchAudio(label,btoa(binary));}
    document.querySelector('#export-dialog [data-gosx-disclosure-close]').click();return {hash,bytes:bytes.length};
  }
  async function audition(){
    const preview=document.querySelector('[data-sound-preview=phrase]');await wait(()=>!preview.disabled,'preview ready');const before=await read('transport');preview.click();
    await wait(()=>preview.getAttribute('aria-pressed')==='true'&&preview.getAttribute('aria-busy')==='false'&&/with keys/.test(document.querySelector('#cicada-pattern-preview [data-sound-status]').textContent),'shaped part preview');
    const during=await read('transport');if(during.playing!==before.playing||during.bar!==before.bar||during.step!==before.step)throw new Error('Preview moved the song');
    preview.click();await wait(()=>preview.getAttribute('aria-pressed')==='false'&&preview.getAttribute('aria-busy')==='false','preview stopped');
  }
  (async()=>{
    await ready();const initial=await read(),audio=await read('audio/config');
    if(!initial.source.includes('cicada-performance-pitch-test: disposable fixture')||audio.options.inputEnabled||String(audio.inventory.backendName||audio.inventory.backend).toLowerCase()!=='null')throw new Error('Use the disposable null-audio pitch fixture');
    const query=new URL(location.href).searchParams;
    if(!section()||query.get('pattern')!=='phrase'||query.get('dock')!=='clip'||query.get('track')!=='keys'||query.get('region')!=='detail'){location.href='/studio?dock=clip&pattern=phrase&note=0&track=keys&region=detail';return null;}
    if(retained?.phase==='reopen'){
      await saved(retained.final);if(document.querySelector('[data-pattern-preview-track]').value!=='keys')throw new Error('Reopen lost audition context');
      const reopened=await render('reopened');if(reopened.hash!==retained.shapes['mirror-second'].audio.hash)throw new Error('Reopened WAV changed');
      sessionStorage.removeItem(key);checks.push('reopened secondary phrase keeps shaped pitches, articulation and identical WAV');
      return {type:'cicada-native-smoke',ok:true,...retained,checks,phase:'done',reopened};
    }
    const original=initial.peer,initialEntry=initial.source,baseline=await render('baseline'),shapes={};
    const shift=section().querySelector('[name=semitones]');fill(shift,'48');section().querySelector('button[type=submit]').click();
    await wait(()=>/outside MIDI/.test(section().textContent),'whole-phrase pitch limit feedback');await ready();
    if((await read()).peer!==original||section().querySelector('[name=semitones]').value!=='48')throw new Error('Refused pitch shaping changed the score or lost retry input');
    checks.push('out-of-range sounding pitches refuse without clipping notes or losing the requested shift');
    for(const [name,operation,amount,note,expected] of [['up','transpose',12,0,[72,76,79,84]],['down','transpose',-7,0,[53,57,60,65]],['mirror-first','reflect',0,0,[60,56,53,48]],['mirror-second','reflect',0,1,[68,64,61,56]]]){
      if(new URL(location.href).searchParams.get('note')!==String(note)){document.querySelectorAll('.performance-content tbody th a')[note].click();await wait(()=>new URL(location.href).searchParams.get('note')===String(note),'selected mirror center');}await ready();
      if(operation==='transpose')fill(section().querySelector('[name=semitones]'),String(amount));
      const form=[...section().querySelectorAll('form')].find(f=>f.querySelector('[name=pitch_operation]')?.value===operation);
      if(typeof window.__cicadaPitchSubmit==='function')await window.__cicadaPitchSubmit(operation);else form.querySelector('button[type=submit]').click();
      await wait(async()=>JSON.stringify(pitches((await read()).peer))===JSON.stringify(expected),'shaped phrase pitches');
      const view=await read(),source=view.peer;await saved(source);await ready();if(view.source!==initialEntry||document.querySelector('[data-pattern-preview-track]').value!=='keys')throw new Error('Shaping changed source ownership or audition context');
      await audition();document.querySelector('#undo-form button').click();await saved(original);await ready();document.querySelector('#redo-form button').click();await saved(source);await ready();
      const output=await render(name);if(output.hash===baseline.hash)throw new Error('Pitch shaping did not change audio');shapes[name]={source,pitches:expected,audio:output};
      checks.push(name+' retains the secondary phrase articulation, auditions independently and has exact Undo/Redo');
      if(name!=='mirror-second'){document.querySelector('#undo-form button').click();await saved(original);await ready();}
    }
    if(typeof window.__cicadaPitchViews==='function')await window.__cicadaPitchViews();
    const final=(await read()).peer;sessionStorage.setItem(key,JSON.stringify({phase:'reopen',checks,original,initialEntry,final,baseline,shapes}));location.href='/studio?dock=clip&pattern=phrase&note=1&track=keys&region=detail';return null;
  })().catch(error=>({type:'cicada-native-smoke',ok:false,checks,error:String(error)+'\n'+String(error.stack||'')})).then(result=>{if(!result)return;if(typeof window.__cicadaPitchResult==='function')window.__cicadaPitchResult(result);else window.chrome?.webview?.postMessage(JSON.stringify(result));});
})();
