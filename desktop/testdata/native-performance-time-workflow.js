// Disposable secondary score and null audio only; no device qualification.
(() => {
  if(location.hostname!=='127.0.0.1' || window.__cicadaPerformanceTimeSmoke)return;
  window.__cicadaPerformanceTimeSmoke=true;
  const key='cicada.performance-time-proof',retained=JSON.parse(sessionStorage.getItem(key)||'null'),checks=retained?.checks||[];
  const read=async(name='workspace')=>{
    if(name!=='workspace'){const r=await fetch('/api/'+name,{cache:'no-store'});if(!r.ok)throw new Error(name+': '+r.status);return r.json();}
    const [state,r]=await Promise.all([read('state'),fetch('/__workspace?panel=code&file=phrase.cicada',{cache:'no-store'})]);
    if(!r.ok)throw new Error('source projection: '+r.status);const projection=await r.json(),doc=new DOMParser().parseFromString(projection.html,'text/html'),editor=doc.querySelector('textarea');
    if(!editor)throw new Error('source projection has no editor');return {...state,peer:editor.value,source_revision:projection.sourceRevision};
  };
  const wait=async(check,label)=>{const end=performance.now()+45000;while(performance.now()<end){if(await check())return;await new Promise(r=>setTimeout(r,50));}throw new Error('Timed out: '+label);};
  const ready=()=>wait(()=>window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]') && !document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'),'Studio ready');
  const peer=view=>view.peer;
  // Read the saved fixture through the public code projection. The note
  // inspector and exported PCM separately check the compiled performance.
  const phrase=view=>{const source=peer(view),length=source.match(/performance\s+length\s*=\s*(\d+)ticks/),note=source.match(/note\s+60\s+at\s*=\s*(\d+)ticks\s+length\s*=\s*(\d+)ticks\s+velocity\s*=\s*(\d+)\s+group\s*=\s*touch/),group=source.match(/gesture-group\s+touch\s*\{([\s\S]*?)\}/),point=group?.[1].match(/gesture\s+at\s*=\s*(\d+)ticks\s+bend\s*=\s*200ct/);
    if(!length||!note||!point)throw new Error('saved fixture lost its notes or shared lane');return {length_ticks:Number(length[1]),notes:[{tick:Number(note[1]),end_tick:Number(note[1])+Number(note[2]),note:60,velocity:Number(note[3]),group:1}],sharedTick:Number(point[1])};};
  const saved=source=>wait(async()=>{const v=await read();return peer(v)===source && document.querySelector('[data-performance-time=phrase] [name=source_revision]')?.value===v.source_revision;},'secondary source and displayed receipt');
  async function render(label){
    document.querySelector('[data-gosx-disclosure-target="#export-dialog"]').click();const previous=(await read('export')).id;
    document.querySelector('#export button[type=submit]').click();let job;
    await wait(async()=>{job=await read('export');if(job.state==='failed')throw new Error(job.error);return job.id!==previous && ['succeeded','shortfall'].includes(job.state);},'WAV export');
    const r=await fetch(job.downloadURL),bytes=new Uint8Array(await r.arrayBuffer());if(!r.ok || new TextDecoder().decode(bytes.slice(0,4))!=='RIFF')throw new Error('Invalid WAV');
    const hash=[...new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))].map(x=>x.toString(16).padStart(2,'0')).join('');
    if(typeof window.__cicadaTimeAudio==='function'){let binary='';for(const b of bytes)binary+=String.fromCharCode(b);await window.__cicadaTimeAudio(label,btoa(binary));}
    document.querySelector('#export-dialog [data-gosx-disclosure-close]').click();return {hash,bytes:bytes.length};
  }
  (async()=>{
    await ready();const initial=await read(),audio=await read('audio/config');
    if(!initial.source.includes('cicada-performance-time-test: disposable fixture') || audio.options.inputEnabled || String(audio.inventory.backendName||audio.inventory.backend).toLowerCase()!=='null')throw new Error('Use the disposable null-audio secondary score');
    if(!document.querySelector('[data-performance-time=phrase]')){location.href='/studio?dock=clip&pattern=phrase&note=0&region=detail';return null;}
    if(retained?.phase==='reopen'){
      await saved(retained.final);const p=phrase(await read());
      if(p.length_ticks!==7680 || p.notes[0].tick!==34 || p.sharedTick!==1920)throw new Error('Reopened scaling lost its timing or gesture lane');
      const reopened=await render('reopened');if(reopened.hash!==retained.scales['2/1'].audio.hash)throw new Error('Reopened WAV changed');
      sessionStorage.removeItem(key);checks.push('reopened secondary performance retains its timing, shared gestures and exported WAV');
      return {type:'cicada-native-smoke',ok:true,checks,original:retained.original,initialEntry:retained.initialEntry,final:retained.final,baseline:retained.baseline,scales:retained.scales,reopened};
    }
    const original=peer(initial),initialEntry=initial.source,baseline=await render('baseline'),scales={};
    if(typeof window.__cicadaTimeViews==='function')await window.__cicadaTimeViews();
    for(const [ratio,length,start,end,gesture] of [['1/2',1920,9,360,480],['2/3',2560,11,480,640],['3/2',5760,26,1080,1440],['2/1',7680,34,1440,1920]]){
      const button=document.querySelector('[data-performance-time=phrase] [name=scale][value="'+ratio+'"]');
      if(typeof window.__cicadaTimeSubmit==='function')await window.__cicadaTimeSubmit(ratio);else button.click();
      await wait(async()=>phrase(await read()).length_ticks===length && document.querySelector('.performance-inspector [name=tick]')?.value===String(start),'scaled phrase and note selection');
      const view=await read(),source=peer(view),p=phrase(view);await saved(source);await ready();
      if(view.source!==initialEntry || p.notes[0].end_tick!==end || p.notes[0].note!==60 || p.notes[0].velocity!==103 || p.notes[0].group!==1 || p.sharedTick!==gesture)throw new Error('Scaling changed pitch, source ownership, gestures or block length');
      const preview=document.querySelector('[data-sound-preview=phrase]');await wait(()=>!preview.disabled,'part preview ready');const beforeTransport=await read('transport');preview.click();
      await wait(()=>preview.getAttribute('aria-pressed')==='true' && preview.getAttribute('aria-busy')==='false' && /Looping phrase with keys/.test(document.querySelector('#cicada-pattern-preview [data-sound-status]').textContent),'scaled part preview acknowledged');
      const during=await read('transport');if(during.playing || during.bar!==beforeTransport.bar || during.step!==beforeTransport.step)throw new Error('Part audition moved the song transport');
      preview.click();await wait(()=>preview.getAttribute('aria-pressed')==='false' && preview.getAttribute('aria-busy')==='false','stop part preview');
      document.querySelector('#undo-form button').click();await saved(original);await ready();
      document.querySelector('#redo-form button').click();await saved(source);await ready();
      const output=await render(ratio.replace('/','-'));if(output.hash===baseline.hash)throw new Error('Time scaling did not change rendered audio');
      scales[ratio]={source,timing:p,audio:output};checks.push(ratio+' scales the secondary phrase, auditions without moving the song, and has source Undo/Redo');
      if(ratio!=='2/1'){document.querySelector('#undo-form button').click();await saved(original);await ready();}
    }
    const final=peer(await read());sessionStorage.setItem(key,JSON.stringify({phase:'reopen',checks,original,initialEntry,final,baseline,scales}));location.href='/studio?dock=clip&pattern=phrase&note=0&region=detail';return null;
  })().catch(error=>({type:'cicada-native-smoke',ok:false,checks,error:String(error)+'\n'+String(error.stack||'')})).then(result=>{if(!result)return;if(typeof window.__cicadaTimeResult==='function')window.__cicadaTimeResult(result);else window.chrome?.webview?.postMessage(JSON.stringify(result));});
})();
