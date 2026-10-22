// Disposable multi-file score, null audio and synthetic gestures only.
(() => {
 if(location.hostname!=='127.0.0.1'||window.__cicadaGridSources)return;
 window.__cicadaGridSources=true;
 const key='cicada.grid-sources-proof',saved=JSON.parse(sessionStorage.getItem(key)||'null'),checks=saved?.checks||[];
 const wait=async(check,label)=>{const end=performance.now()+45000;while(performance.now()<end){if(await check())return;await new Promise(r=>setTimeout(r,50));}throw new Error('Timed out: '+label);};
 const api=async(name)=>{const r=await fetch('/api/'+name,{cache:'no-store'});if(!r.ok)throw new Error(name+': '+r.status);return r.json();};
 const read=async()=>{const v=await api('state'),projections=await Promise.all(['main.cicada','lead.cicada','keys.cicada','drums.cicada','phrases.cicada'].map(async file=>{const r=await fetch('/__workspace?panel=code&file='+file,{cache:'no-store'});if(!r.ok)throw new Error('source projection '+r.status);const p=await r.json();return {file,source:new DOMParser().parseFromString(p.html,'text/html').querySelector('textarea').value,revision:p.sourceRevision};}));return {...v,source:v.source.replaceAll('\r\n','\n'),sourceRevision:projections[0].revision,files:{...Object.fromEntries(projections.map(p=>[p.file,p.source])),'main.cicada':v.source.replaceAll('\r\n','\n')}};};
 const ready=()=>wait(()=>window.__gosx?.ready&&document.documentElement.getAttribute('data-gosx-navigation-state')==='idle'&&(location.pathname==='/studio'?document.querySelector('#studio-shell[data-shell-actions=ready]'):document.querySelector('#cicada-workspace[data-workspace-reactive=ready]'))&&!document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'),'Studio ready');
 const same=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
 const fill=(node,value)=>{node.value=String(value);node.dispatchEvent(new Event('input',{bubbles:true}));node.dispatchEvent(new Event('change',{bubbles:true}));};
 const exact=files=>wait(async()=>same((await read()).files,files),'exact project sources');
 const forms=operation=>[...document.querySelectorAll('#patterns form')].find(f=>f.querySelector('[name=action]')?.value===operation);
 async function render(label){document.querySelector('[data-gosx-disclosure-target="#export-dialog"]').click();const previous=(await api('export')).id;document.querySelector('#export button[type=submit]').click();let job;await wait(async()=>{job=await api('export');if(job.state==='failed')throw new Error(job.error);return job.id!==previous&&['succeeded','shortfall'].includes(job.state);},'WAV export');const r=await fetch(job.downloadURL),bytes=new Uint8Array(await r.arrayBuffer());if(!r.ok||new TextDecoder().decode(bytes.slice(0,4))!=='RIFF')throw new Error('Invalid WAV');const hash=[...new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))].map(x=>x.toString(16).padStart(2,'0')).join('');if(typeof window.__cicadaGridAudio==='function'){let binary='';for(const b of bytes)binary+=String.fromCharCode(b);await window.__cicadaGridAudio(label,btoa(binary));}document.querySelector('#export-dialog [data-gosx-disclosure-close]').click();return {hash,bytes:bytes.length};}
 async function choose(pattern,step=1,lane=''){
  if(document.querySelector('.piano-roll')?.dataset.patternId!==pattern){document.querySelector('#patterns .pattern-choice[href*="pattern='+pattern+'"]').click();await wait(()=>document.querySelector('.piano-roll')?.dataset.patternId===pattern,'pattern selected');await ready();}
  if(document.querySelector('#step-inspector [name=step]')?.value!==String(step)){const ruler=[...document.querySelectorAll('.piano-roll .step-number')].find(a=>a.textContent===String(step));ruler.click();await wait(()=>document.querySelector('#step-inspector [name=step]')?.value===String(step),'step selected');await ready();}
  if(lane&&document.querySelector('#step-inspector [name=lane]')?.value!==lane){document.querySelector('.drum-row a[href*="lane='+lane+'"]').click();await wait(()=>document.querySelector('#step-inspector [name=lane]')?.value===lane,'drum lane selected');await ready();}
 }
 async function edit(operation,values,owner,check,label){const before=(await read()).files,f=forms(operation);if(!f)throw new Error('Missing form '+operation);const disclosure=f.closest('details');if(disclosure)disclosure.open=true;for(const [key,value] of Object.entries(values)){const field=f.querySelector('[name='+key+']');if(field.type==='checkbox'){field.checked=value;field.dispatchEvent(new Event('change',{bubbles:true}));}else fill(field,value);}f.querySelector('button[type=submit]').click();await wait(async()=>check((await read()).files[owner]),label);await ready();const after=(await read()).files;for(const name of Object.keys(before))if(name!==owner&&after[name]!==before[name])throw new Error(label+' changed '+name);document.querySelector('form[action$="/__actions/undo"] button').click();await exact(before);await ready();document.querySelector('form[action$="/__actions/redo"] button').click();await exact(after);await ready();checks.push(label+' saves only its owner with exact project Undo/Redo');}
 (async()=>{
  await ready();const initial=await read(),audio=await api('audio/config');if(!initial.source.includes('cicada-grid-sources-test: disposable fixture')||audio.options.inputEnabled||String(audio.inventory.backendName||audio.inventory.backend).toLowerCase()!=='null')throw new Error('Use the disposable null-audio grid-sources fixture');
  if(saved?.phase==='reopen'){
   if(location.pathname!=='/studio'){location.href='/studio?dock=clip&pattern=beat&lane=sd&step=4&region=detail';return null;}
   await exact(saved.final);const reopened=await render('reopened');if(reopened.hash!==saved.exported.hash)throw new Error('Reopened WAV changed');checks.push('reopening Studio retains all grid sources and the final WAV');sessionStorage.removeItem(key);return {type:'cicada-native-smoke',ok:true,...saved,phase:'done',checks,reopened};
  }
  if(!saved){
   if(location.pathname!=='/studio'){location.href='/studio?dock=clip&pattern=line&region=detail';return null;}
   const baseline=await render('baseline');sessionStorage.setItem(key,JSON.stringify({phase:'grid',checks,original:initial.files,baseline,initialEntry:initial.source}));location.href='/?panel=patterns&pattern=line&step=1&octave=3';return null;
  }
  if(saved.phase==='grid'){
   if(location.pathname!=='/'){location.href='/?panel=patterns&pattern=line&step=1&octave=3';return null;}
   await choose('line');document.querySelector('[data-workspace-tool=draw]').click();const before=(await read()).files;
   for(const [step,note] of [[1,'D4'],[3,'F4'],[6,'A4']])document.querySelector('.roll-cell[aria-label="line step '+step+' '+note+'"]').click();
   await wait(async()=>{const s=(await read()).files['lead.cicada'];return /d4 . f4 . c5 a4 e5/.test(s);},'rapid owned-source drawing');await ready();const drawn=(await read()).files;
   if(drawn['main.cicada']!==saved.initialEntry||drawn['phrases.cicada']!==before['phrases.cicada']||drawn['keys.cicada']!==before['keys.cicada'])throw new Error('Drawing changed a shared phrase or another source');
   for(let i=0;i<3;i++){document.querySelector('form[action$="/__actions/undo"] button').click();await ready();}await exact(before);for(let i=0;i<3;i++){document.querySelector('form[action$="/__actions/redo"] button').click();await ready();}await exact(drawn);checks.push('three queued drawing gestures advance their own project versions and retain shared phrases');
   if(typeof window.__cicadaGridArmStaleProjection==='function'){
    await window.__cicadaGridArmStaleProjection();for(const [step,note]of [[2,'C4'],[4,'G4'],[8,'B4']])document.querySelector('.roll-cell[aria-label="line step '+step+' '+note+'"]').click();
    await wait(()=>/Saved\. Project files changed/.test(document.querySelector('[data-workspace-status]')?.textContent),'stale acknowledged projection refuses queued drawing');
    const landed=(await read()).files;if(!/d4 c4 f4 . c5 a4 e5 ./.test(landed['lead.cicada']))throw new Error('A queued gesture crossed an unreviewed project version');
    document.querySelector('[data-workspace-retry]').click();await ready();await wait(()=>document.querySelector('[data-workspace-retry]').hidden,'explicit workspace refresh');
    document.querySelector('form[action$="/__actions/undo"] button').click();await exact({...drawn,'phrases.cicada':landed['phrases.cicada']});await ready();checks.push('the client refuses a synthetic stale projection after a real outside save, cancels later gestures and refreshes explicitly');
   }
   await choose('line',1);await edit('step',{mode:'note',pitch:62,accent:true,slide:true,ratchet:2,chance:75},'lead.cicada',s=>/d4\^~\*2\?75/.test(s),'note articulation');
   await edit('range',{operation:'transpose',first:1,last:4,target:5,amount:12},'lead.cicada',s=>/d5\^~\*2\?75 . f5/.test(s),'range transposition');
   await edit('resize',{length:12},'lead.cicada',s=>/steps=12/.test(s),'pattern resize');
   await edit('settings',{swing:55,gate:65,transpose:-12},'lead.cicada',s=>/transpose\s*=\s*-12/.test(s),'pattern timing');
   await choose('harmony',1);await edit('chord',{chord_root:2,chord_octave:4,chord_shape:'minor',chord_inversion:0,hold:2},'keys.cicada',s=>/\[d4 f4 a4\] -/.test(s),'chord voicing');
   await choose('beat',4,'sd');const rowNames=[...document.querySelectorAll('.drum-row .roll-label')].map(n=>n.textContent);if(!same(rowNames,['SD','BD','CH']))throw new Error('Secondary drum lanes lost authored order: '+rowNames);
   document.querySelector('.roll-cell[aria-label="beat sd step 4"]').click();await wait(async()=>/sd: ..xx/.test((await read()).files['drums.cicada']),'drum hit');await ready();
   await edit('step',{mode:'hit',velocity:4,ratchet:2,chance:90},'drums.cicada',s=>/x4\*2\?90/.test(s),'drum articulation');
   sessionStorage.setItem(key,JSON.stringify({...saved,phase:'shell',checks,edited:(await read()).files}));location.href='/studio?dock=clip&pattern=beat&lane=sd&step=4&region=detail';return null;
  }
  if(saved.phase==='export'){
   const discard=document.querySelector('#score button[name=intent][value=discard]');if(discard){discard.click();await wait(()=>!document.querySelector('#score button[name=intent][value=discard]'),'discard recovered test draft');await ready();}
   await exact(saved.final);const exported=await render('edited');if(exported.hash===saved.baseline.hash)throw new Error('Grid edits did not change the audio');sessionStorage.setItem(key,JSON.stringify({...saved,phase:'reopen',checks,exported}));location.href='/studio?dock=clip&pattern=beat&lane=sd&step=4&region=detail';return null;
  }
  await exact(saved.edited);let final=saved.edited;
  if(typeof window.__cicadaGridOutsideSave==='function'){
   const form=forms('step'),velocity=form.querySelector('[name=velocity]'),code=document.querySelector('#score textarea'),originalCode=code.value;fill(velocity,7);fill(code,originalCode+'\n// retain this private grid-conflict draft\n');const outside=await window.__cicadaGridOutsideSave();form.querySelector('button[type=submit]').click();
   await wait(()=>/Refresh Studio before saving/.test(document.querySelector('.pattern-workspace .form-status')?.textContent),'inline grid source conflict');
   final=(await read()).files;if(final['phrases.cicada']!==outside||final['drums.cicada']!==saved.edited['drums.cicada']||document.querySelector('#step-inspector [name=velocity]').value!=='7'||document.querySelector('#score textarea')!==code||!code.value.includes('private grid-conflict draft'))throw new Error('Conflict lost external bytes, source draft or drum articulation draft');
   checks.push('an outside phrase save refuses stale drum editing while retaining the external source and both local drafts');if(typeof window.__cicadaGridViews==='function')await window.__cicadaGridViews();fill(document.querySelector('#score textarea'),originalCode);await wait(()=>/No unsaved Code edits/.test(document.querySelector('[data-source-recovery-status]')?.textContent),'discard disposable Code recovery draft');
  }else if(typeof window.__cicadaGridViews==='function')await window.__cicadaGridViews();
  // Review the current saved project before exporting after an outside save.
  sessionStorage.setItem(key,JSON.stringify({...saved,phase:'export',checks,final}));location.href='/studio?dock=clip&pattern=beat&lane=sd&step=4&region=detail';return null;
 })().catch(error=>({type:'cicada-native-smoke',ok:false,checks,error:String(error)+'\n'+String(error.stack||'')+'\nSTATUS '+document.querySelector('[data-workspace-status]')?.textContent})).then(result=>{if(!result)return;if(typeof window.__cicadaGridResult==='function')window.__cicadaGridResult(result);else window.chrome?.webview?.postMessage(JSON.stringify(result));});
})();
