// A copied score and native null audio; all actions use the ordinary UI forms.
(() => {
 if(location.hostname!=='127.0.0.1'||window.__cicadaSoloClockSmoke)return;window.__cicadaSoloClockSmoke=true;
 const read=async name=>{const r=await fetch('/api/'+name,{cache:'no-store'});if(!r.ok)throw new Error(name+': '+r.status);return r.json()};
 const wait=async(check,label)=>{const end=performance.now()+60000;while(performance.now()<end){if(await check())return;await new Promise(r=>setTimeout(r,50))}throw new Error('Timed out: '+label)};
 const ready=()=>wait(()=>window.__gosx?.ready&&document.querySelector('#studio-shell[data-shell-actions=ready]')&&!document.querySelector('form[data-gosx-form-state=pending]'),'managed forms');
 const click=(label,scope=document)=>{const b=[...scope.querySelectorAll('button')].find(n=>n.textContent.trim()===label&&!n.disabled);if(!b)throw new Error('Missing '+label);b.click()};
 (async()=>{
  await ready();const original=await read('state'),audio=await read('audio/config'),history=(await read('history')).edits;
  if(!original.source.includes('cicada-solo-clock-test: disposable fixture')||audio.options.inputEnabled||String(audio.inventory.backendName||audio.inventory.backend).toLowerCase()!=='null')throw new Error('Use a copied solo-clock fixture with null audio');
  const inspector=document.querySelector('[data-placement-inspector=phrase]');if(!inspector.open)inspector.querySelector('summary').click();const checks=[];
  click('Loop solo',inspector);await wait(async()=>{const s=await read('transport');if(s.error)throw new Error(s.error);return s.listening?.name==='phrase'&&s.listening.mode==='solo'&&s.listening.cycles>=1},'solo pass');checks.push('native solo loops through a tempo change with later automation intact');
  click('Audition solo',inspector);await wait(async()=>{const s=await read('transport');if(s.error)throw new Error(s.error);return s.listening?.mode==='solo'&&!s.listening.loop&&s.listening.completed&&!s.playing},'finite solo');checks.push('native solo finishes at the phrase end');
  click('Play full song');await wait(async()=>{const s=await read('transport');return s.playing&&!s.listening&&!s.error},'full song restored');click('Stop listening');await wait(async()=>!(await read('transport')).playing,'stopped');
  if((await read('state')).source!==original.source||JSON.stringify((await read('history')).edits)!==JSON.stringify(history))throw new Error('Audition changed the score or Undo history');checks.push('full song returns without a source or Undo edit');return {type:'cicada-native-smoke',ok:true,checks};
 })().catch(error=>({type:'cicada-native-smoke',ok:false,checks:[],error:String(error)+'\n'+String(error.stack||'')+'\n'+document.body.innerText.slice(-3000)})).then(result=>window.chrome?.webview?.postMessage(JSON.stringify(result)));
})();
