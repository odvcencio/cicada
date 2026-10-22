// Disposable synthetic phrase and null audio only.
(() => {
  if(location.hostname!=='127.0.0.1'||window.__cicadaPerformanceSourcesSmoke)return;
  window.__cicadaPerformanceSourcesSmoke=true;
  const key='cicada.performance-sources-proof',retained=JSON.parse(sessionStorage.getItem(key)||'null'),checks=retained?.checks||[];
  const read=async(name='workspace')=>{
    if(name!=='workspace'){const r=await fetch('/api/'+name,{cache:'no-store'});if(!r.ok)throw new Error(name+': '+r.status);return r.json();}
    const [state,r]=await Promise.all([read('state'),fetch('/__workspace?panel=code&file=phrase.cicada',{cache:'no-store'})]);
    if(!r.ok)throw new Error('source projection: '+r.status);const projection=await r.json(),doc=new DOMParser().parseFromString(projection.html,'text/html');
    return {...state,peer:doc.querySelector('textarea').value,source_revision:projection.sourceRevision};
  };
  const wait=async(check,label)=>{const end=performance.now()+45000;while(performance.now()<end){if(await check())return;await new Promise(r=>setTimeout(r,50));}throw new Error('Timed out: '+label);};
  const ready=()=>wait(()=>window.__gosx?.ready&&document.querySelector('#studio-shell[data-shell-actions=ready]')&&!document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'),'Studio ready');
  const fill=(field,value)=>{field.value=value;field.dispatchEvent(new Event('input',{bubbles:true}));field.dispatchEvent(new Event('change',{bubbles:true}));};
  const section=()=>document.querySelector('#patterns .performance-workspace');
  const saved=source=>wait(async()=>{const v=await read();return v.peer===source&&section()?.querySelector('[name=source_revision]')?.value===v.source_revision;},'secondary source and receipt');
  async function render(label){
    document.querySelector('[data-gosx-disclosure-target="#export-dialog"]').click();const previous=(await read('export')).id;document.querySelector('#export button[type=submit]').click();let job;
    await wait(async()=>{job=await read('export');if(job.state==='failed')throw new Error(job.error);return job.id!==previous&&['succeeded','shortfall'].includes(job.state);},'WAV export');
    const r=await fetch(job.downloadURL),bytes=new Uint8Array(await r.arrayBuffer());if(!r.ok||new TextDecoder().decode(bytes.slice(0,4))!=='RIFF')throw new Error('Invalid WAV');
    const hash=[...new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))].map(x=>x.toString(16).padStart(2,'0')).join('');
    if(typeof window.__cicadaSourcesAudio==='function'){let binary='';for(const b of bytes)binary+=String.fromCharCode(b);await window.__cicadaSourcesAudio(label,btoa(binary));}
    document.querySelector('#export-dialog [data-gosx-disclosure-close]').click();return {hash,bytes:bytes.length};
  }
  async function audition(){
    const preview=document.querySelector('[data-sound-preview=phrase]');await wait(()=>!preview.disabled,'preview ready');const before=await read('transport');preview.click();
    await wait(()=>preview.getAttribute('aria-pressed')==='true'&&preview.getAttribute('aria-busy')==='false'&&/with keys/.test(document.querySelector('#cicada-pattern-preview [data-sound-status]').textContent),'shaped part preview');
    const during=await read('transport');if(during.playing!==before.playing||during.bar!==before.bar||during.step!==before.step)throw new Error('Preview moved the song');
    preview.click();await wait(()=>preview.getAttribute('aria-pressed')==='false'&&preview.getAttribute('aria-busy')==='false','preview stopped');
  }
  const form=operation=>[...document.querySelectorAll('#patterns form')].find(f=>f.querySelector('[name=action]')?.value===operation);
  async function selectNote(index){if(new URL(location.href).searchParams.get('note')!==String(index)){document.querySelectorAll('.performance-content tbody th a')[index].click();await wait(()=>new URL(location.href).searchParams.get('note')===String(index)&&form('performed-note')?.querySelector('[name=index]')?.value===String(index),'note selected');}await ready();}
  async function editStep(operation,values,check,label){
    const before=(await read()).peer,f=form(operation);for(const [key,value] of Object.entries(values))fill(f.querySelector('[name='+key+']'),String(value));
    if(typeof window.__cicadaSourcesSubmit==='function')await window.__cicadaSourcesSubmit(operation);else f.querySelector('button[type=submit]').click();
    await wait(async()=>check((await read()).peer),label);await ready();const view=await read(),source=view.peer;await saved(source);
    if(view.source!==window.__sourcesInitialEntry||document.querySelector('[data-pattern-preview-track]').value!=='keys')throw new Error(label+' changed source ownership or sound context');
    document.querySelector('#undo-form button').click();await saved(before);await ready();document.querySelector('#redo-form button').click();await saved(source);await ready();checks.push(label+' saves in the owning file with exact Undo/Redo');
  }
  (async()=>{
    await ready();const initial=await read(),audio=await read('audio/config');
    if(!initial.source.includes('cicada-performance-sources-test: disposable fixture')||audio.options.inputEnabled||String(audio.inventory.backendName||audio.inventory.backend).toLowerCase()!=='null')throw new Error('Use the disposable null-audio sources fixture');
    const query=new URL(location.href).searchParams;
    if(!section()||query.get('pattern')!=='phrase'||query.get('dock')!=='clip'||query.get('track')!=='keys'||query.get('region')!=='detail'){location.href='/studio?dock=clip&pattern=phrase&note=0&track=keys&region=detail';return null;}
    window.__sourcesInitialEntry=initial.source;
    if(retained?.phase==='reopen'){
      await saved(retained.final);const reopened=await render('reopened');if(reopened.hash!==retained.exported.hash)throw new Error('Reopened WAV changed');sessionStorage.removeItem(key);checks.push('reopened owned performance retains notes, both gesture layers, loop and WAV');return {type:'cicada-native-smoke',ok:true,...retained,phase:'done',checks,reopened};
    }
    const original=initial.peer,initialEntry=initial.source,baseline=await render('baseline');
    await selectNote(1);await editStep('performed-note',{pitch:65,tick:1001,end_tick:1342,velocity:95},s=>/note 65 at=1001ticks length=341ticks velocity=95 group=touch/.test(s),'note edit');
    await selectNote(0);await editStep('performed-gesture',{tick:0,pitch_cents:75,pressure:.6,timbre:.4,vibrato_depth_cents:14,vibrato_rate_hz:6},s=>/bend=75ct pressure=0.6 timbre=0.4 vibrato=14ct rate=6Hz/.test(s),'member gesture edit');
    document.querySelectorAll('#shared-gestures tbody th a')[1].click();await wait(()=>form('shared-gesture')?.querySelector('[name=index]')?.value==='1','shared point selected');await ready();
    await editStep('shared-gesture',{tick:1200,pitch_cents:150,pressure:.7,timbre:.6},s=>/gesture at=1200ticks bend=150ct pressure=0.7 timbre=0.6/.test(s),'shared gesture edit');
    await editStep('performance-group',{name:'swell'},s=>s.includes('gesture-group swell')&&!s.includes('group=touch'),'lane rename');
    const newLane=[...document.querySelectorAll('#shared-gestures nav a')].find(a=>a.textContent==='New shared lane');newLane.click();await wait(()=>form('performance-group')?.querySelector('button')?.textContent==='Create shared lane','new lane form');await ready();
    await editStep('performance-group',{name:'lift'},s=>s.includes('gesture-group lift'),'lane creation');
    await selectNote(1);await editStep('performance-binding',{group:2},s=>/note 65 at=1001ticks length=341ticks velocity=95 group=lift/.test(s),'note lane binding');
    const add=[...document.querySelectorAll('#shared-gestures nav a')].find(a=>a.textContent==='Add a shared point');add.click();await wait(()=>form('shared-gesture')?.querySelector('button')?.textContent==='Add shared point','new point form');await ready();
    await editStep('shared-gesture',{tick:960,pitch_cents:-100,pressure:.9,timbre:.8},s=>/gesture at=960ticks bend=-100ct pressure=0.9 timbre=0.8/.test(s),'new shared point');
    const beforeRefusal=(await read()).peer;form('performance-group').querySelector('button[value=remove]').click();await wait(()=>/unbind this lane/.test(form('performance-group').textContent),'bound lane refusal');if((await read()).peer!==beforeRefusal)throw new Error('Refused lane removal changed the source');checks.push('bound lane removal refuses locally without discarding its notes or gestures');
    const length=form('performance-length');length.closest('details').open=true;await editStep('performance-length',{length_ticks:7680},s=>/performance length=7680ticks/.test(s),'loop length edit');
    await audition();if(typeof window.__cicadaSourcesViews==='function')await window.__cicadaSourcesViews();
    const exported=await render('edited');if(exported.hash===baseline.hash)throw new Error('Performed edits did not change audio');
    if(typeof window.__cicadaSourcesOutsideSave==='function'){
      const code=document.querySelector('#score textarea'),originalCode=code.value,note=form('performed-note').querySelector('[name=velocity]');fill(code,originalCode+'\n// unsaved source-conflict draft\n');fill(note,'101');const outside=await window.__cicadaSourcesOutsideSave();form('performed-note').querySelector('button[value=save]').click();
      await wait(()=>/changed/.test(form('performed-note').querySelector('.form-status').textContent),'outside source conflict');
      if((await read()).peer!==outside||code!==document.querySelector('#score textarea')||!code.value.includes('unsaved source-conflict draft')||form('performed-note').querySelector('[name=velocity]').value!=='101')throw new Error('Conflict lost external bytes or local draft');
      if(typeof window.__cicadaSourcesConflictViews==='function')await window.__cicadaSourcesConflictViews();
      fill(code,originalCode);checks.push('an outside secondary save refuses a stale note edit while retaining external bytes and local code/note drafts');
    }
    const final=(await read()).peer;sessionStorage.setItem(key,JSON.stringify({phase:'reopen',checks,original,initialEntry,final,baseline,exported}));location.href='/studio?dock=clip&pattern=phrase&note=1&track=keys&region=detail';return null;
  })().catch(error=>({type:'cicada-native-smoke',ok:false,checks,error:String(error)+'\n'+String(error.stack||'')})).then(result=>{if(!result)return;if(typeof window.__cicadaSourcesResult==='function')window.__cicadaSourcesResult(result);else window.chrome?.webview?.postMessage(JSON.stringify(result));});
})();
