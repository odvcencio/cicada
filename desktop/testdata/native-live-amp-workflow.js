(() => {
 if (window.__liveAmpSmoke) return; window.__liveAmpSmoke = true;
 const checks=[], errors=[]; addEventListener('error',e=>errors.push(e.message)); addEventListener('unhandledrejection',e=>errors.push(String(e.reason)));
 const wait=async(fn,label)=>{const until=Date.now()+20000; while(Date.now()<until){if(await fn())return; await new Promise(r=>setTimeout(r,100));} throw Error('Timed out: '+label)};
 const state=async(path='state')=>{const r=await fetch('/api/'+path,{cache:'no-store'}); if(!r.ok)throw Error(path+' '+r.status); return r.json()};
 const click=(root,label)=>{const b=[...root.querySelectorAll('button')].find(x=>x.textContent.trim()===label); if(!b)throw Error('Missing '+label); b.click()};
 const fill=(e,v)=>{if(!e)throw Error('missing input'); e.value=v; e.dispatchEvent(new Event('input',{bubbles:true})); e.dispatchEvent(new Event('change',{bubbles:true}))};
 const fixture=`cicada 2\ntitle "Live amp smoke"\ntempo 96\nkey c minor\ntrack bass acid {}\ntrack guitar audio {}\npattern pulse acid steps=4 { 1 . 5 . }\nscene main { bass=pulse }\nsong { main*4 }\n`;
 (async()=>{
  await wait(()=>window.__gosx?.ready===true && document.querySelector('#studio-shell[data-shell-actions=ready]'),'Studio ready');
  const initial=await state('audio/config'); if(initial.options.inputEnabled || !initial.options.monitorMuted) throw Error('input/monitor unexpectedly enabled on startup'); checks.push('fresh startup input disabled and monitoring muted');
  const editor=document.querySelector('#score textarea'); fill(editor,fixture); editor.focus(); editor.dispatchEvent(new KeyboardEvent('keydown',{key:'s',code:'KeyS',metaKey:true,bubbles:true,cancelable:true}));
  await wait(async()=>{const s=await state();return s.valid&&s.source.includes('track guitar audio')},'fixture saved'); checks.push('disposable audio track saved');
  click(document,'Audio'); await wait(()=>document.querySelector('#audio-dialog [name=ampEnabled]'),'Audio controls');
  const dialog=document.querySelector('#audio-dialog');
  const section=[...dialog.querySelectorAll('details')].find(x=>x.querySelector('summary')?.textContent.trim()==='Guitar amp and cabinet'); if(!section)throw Error('guitar section missing'); section.open=true;
  const form=section.closest('form'); const enabled=form.querySelector('[name=ampEnabled]'); enabled.checked=true; enabled.dispatchEvent(new Event('change',{bubbles:true})); fill(form.querySelector('[name=ampDrive]'),'0.7'); fill(form.querySelector('[name=ampTone]'),'0.6'); fill(form.querySelector('[name=ampOutput]'),'-3'); fill(form.querySelector('[name=ampCabinet]'),'2');
  click(form,'Apply audio settings');
  await wait(async()=>{const a=await state('audio/config');return a.options.ampEnabled&&a.options.ampCabinet===2&&Math.abs(a.options.ampDrive-.7)<.001},'monitor settings saved'); checks.push('amp controls persist while null input stays disabled');
  try { await wait(()=>{const f=document.querySelector('#vocals [name=action][value=amp-chain]')?.closest('form'); return f && ![...f.querySelectorAll('button')].find(x=>x.textContent.trim()==='Apply saved amp to track')?.disabled && Math.abs(Number(f.querySelector('[name=ampDrive]')?.value)-.7)<.001}, 'updated track controls'); } catch(e) {const f=document.querySelector('#vocals [name=action][value=amp-chain]')?.closest('form'); throw Error('stale projection '+JSON.stringify({url:location.href, disabled:f?.querySelector('button')?.disabled, drive:f?.querySelector('[name=ampDrive]')?.value, feedback:document.querySelector('[data-workspace-status]')?.innerText, status:f?.getAttribute('data-gosx-form-state'), amp: (await state('audio/config')).options.ampDrive}));}
  checks.push('monitor changes refresh the saved track form without a reload');
  const trackForm=document.querySelector('#vocals [name=action][value=amp-chain]').closest('form'); fill(trackForm.querySelector('[name=track]'),'guitar'); click(trackForm,'Apply saved amp to track');
  await wait(async()=>{const s=await state();return s.source.includes('ampcab')&&s.source.includes('guitar_live_amp')},'track effect saved');
  const saved=await state(); if(!/drive\s*=\s*0\.7/.test(saved.source)||!/tone\s*=\s*0\.6/.test(saved.source))throw Error('track controls did not match monitor'); checks.push('apply saved sound creates editable guitar track amp');
  await wait(()=>document.querySelector('#vocals details form button') && [...document.querySelectorAll('#vocals button')].some(x=>x.textContent.trim()==='Save playback amp'), 'playback editor');
  const saveButton=[...document.querySelectorAll('#vocals button')].find(x=>x.textContent.trim()==='Save playback amp'); const editorForm=saveButton.closest('form'); fill(editorForm.querySelector('[name=ampDrive]'),'0.2'); await new Promise(r=>setTimeout(r,1600)); if(editorForm.querySelector('[name=ampDrive]').value!=='0.2')throw Error('publisher overwrote unsaved playback amp edit'); checks.push('monitor refresh preserves an unsaved playback amp edit');
  click(editorForm,'Save playback amp'); await wait(async()=>/drive\s*=\s*0\.2/.test((await state()).source),'saved playback amp edit'); checks.push('playback amp edit saved separately from monitoring');
  await wait(async()=>{const f=[...document.querySelectorAll('#vocals button')].find(x=>x.textContent.trim()==='Set playback bypass')?.closest('form');return f&&f.querySelector('[name=revision]')?.value===(await state()).revision},'fresh playback bypass form');
  const bypassForm=[...document.querySelectorAll('#vocals button')].find(x=>x.textContent.trim()==='Set playback bypass')?.closest('form'); if(!bypassForm)throw Error('playback bypass missing'); fill(bypassForm.querySelector('[name=value]'),'true'); click(bypassForm,'Set playback bypass'); await wait(async()=>/bypass\s*=\s*(true|on)/.test((await state()).source),'playback bypass saved');
  await wait(async()=>{const f=[...document.querySelectorAll('#vocals button')].find(x=>x.textContent.trim()==='Set playback bypass')?.closest('form');return f&&f.querySelector('[name=revision]')?.value===(await state()).revision},'fresh restored bypass form');
  const restored=[...document.querySelectorAll('#vocals button')].find(x=>x.textContent.trim()==='Set playback bypass')?.closest('form'); fill(restored.querySelector('[name=value]'),'false'); click(restored,'Set playback bypass'); await wait(async()=>/bypass\s*=\s*(false|off)/.test((await state()).source),'playback bypass restored'); checks.push('saved playback amp bypass and restore');
  if(errors.length)throw Error(errors.join('; ')); return {type:'cicada-native-smoke',ok:true,checks,source:saved.source};
 })().catch(e=>({type:'cicada-native-smoke',ok:false,checks,error:String(e)+'\n'+e.stack,errors})).then(r=>window.chrome.webview.postMessage(JSON.stringify(r)));
})();
