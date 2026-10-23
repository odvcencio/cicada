// Disposable pinned ideas and null audio; no physical input or output tests.
(() => {
  if(location.hostname!=='127.0.0.1'||window.__cicadaDirectVariationSmoke)return;
  window.__cicadaDirectVariationSmoke=true;
  const key='cicada.direct-variation-proof',retained=JSON.parse(sessionStorage.getItem(key)||'null'),checks=retained?.checks||[];
  const read=async(name='state')=>{const r=await fetch('/api/'+name,{cache:'no-store'});if(!r.ok)throw new Error(name+': '+r.status);return r.json();};
  const wait=async(check,label, timeout = 45000)=>{const end=performance.now()+timeout;while(performance.now()<end){if(await check())return;await new Promise(r=>setTimeout(r,50));}throw new Error('Timed out: '+label);};
  const ready=()=>wait(()=>window.__gosx?.ready&&document.querySelector('#studio-shell[data-shell-actions=ready]')&&!document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'),'Studio ready');
  const saved=source=>wait(async()=>(await read()).source===source&&document.querySelector('#undo-form [name=revision]')?.value===(await read()).revision,'saved source and projection');
  const selected=()=>new URL(location.href).searchParams.get('pattern');
  const fill=(field,value)=>{field.value=value;field.dispatchEvent(new Event('input',{bubbles:true}));field.dispatchEvent(new Event('change',{bubbles:true}));};
  const variation=id=>document.querySelector('[data-pattern-variation="'+id+'"]');
  async function chooseUpper(id){
    await ready();await wait(()=>document.querySelector('#cicada-pattern-preview[data-sounds-ready=true]'),'sound selector ready');const select=document.querySelector('#cicada-pattern-preview [data-pattern-preview-track]');fill(select,'upper');
    await wait(()=>variation(id)?.querySelector('[name=track]').value==='upper','chosen sound reaches variation');
  }
  async function audition(name){const button=document.querySelector('#cicada-pattern-preview [data-sound-preview]');await wait(()=>!button.disabled,'preview ready');button.click();await wait(()=>button.getAttribute('aria-pressed')==='true'&&button.getAttribute('aria-busy')==='false','preview '+name);button.click();await wait(()=>button.getAttribute('aria-pressed')==='false'&&button.getAttribute('aria-busy')==='false','preview stopped');}
  async function copy(id,name){
    await chooseUpper(id);fill(variation(id).querySelector('[name=newName]'),name);
    if(typeof window.__cicadaDirectSubmit==='function')await window.__cicadaDirectSubmit(id);else variation(id).querySelector('button[type=submit]').click();
    await wait(()=>selected()===name&&new URL(location.href).searchParams.get('track')==='upper','local variation opens with its sound');await ready();
    if(document.querySelector('#cicada-pattern-preview [data-pattern-preview-track]').value!=='upper')throw new Error('Variation changed the chosen sound');
    return (await read()).source;
  }
  async function bind(name){
    const details=document.querySelector('[data-pattern-management="'+name+'"]');if(!details.open)details.querySelector('summary').click();
    const form=[...details.querySelectorAll('form')].find(f=>f.querySelector('[name=action]')?.value==='bind');
    if(form.querySelector('[name=track]').value!=='upper')throw new Error('Scene assignment lost the chosen sound');
    form.querySelector('button[name=binding][value=play]').click();await wait(async()=>new RegExp('upper\\s*=\\s*'+name).test((await read()).source),'variation assigned to scene');await ready();return (await read()).source;
  }
  async function render(label){
    document.querySelector('[data-gosx-disclosure-target="#export-dialog"]').click();const previous=(await read('export')).id;document.querySelector('#export button[type=submit]').click();let job;
    await wait(async()=>{job=await read('export');if(job.state==='failed')throw new Error(job.error);return job.id!==previous&&['succeeded','shortfall'].includes(job.state);},'WAV export', 90000);
    const r=await fetch(job.downloadURL),bytes=new Uint8Array(await r.arrayBuffer());if(!r.ok||new TextDecoder().decode(bytes.slice(0,4))!=='RIFF')throw new Error('Invalid WAV');
    const hash=[...new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))].map(x=>x.toString(16).padStart(2,'0')).join('');
    if(typeof window.__cicadaDirectAudio==='function'){let binary='';for(const byte of bytes)binary+=String.fromCharCode(byte);await window.__cicadaDirectAudio(label,btoa(binary));}
    document.querySelector('#export-dialog [data-gosx-disclosure-close]').click();return {hash,bytes:bytes.length};
  }
  (async()=>{
    await ready();const initial=(await read()).source,audio=await read('audio/config');
    if(!initial.includes('cicada-direct-variation-test: disposable fixture')||audio.options.inputEnabled||String(audio.inventory.backendName||audio.inventory.backend).toLowerCase()!=='null')throw new Error('Use the disposable null-audio library fixture');
    const target=retained?.phase==='reopen'?'answer':'motif.idea';if(selected()!==target){location.href='/studio?dock=clip&pattern='+target+'&track='+(retained?'upper':'keys')+'&scene=main&region=detail';return null;}
    if(retained?.phase==='reopen'){
      await saved(retained.final);if(document.querySelector('.performance-inspector [name=tick]')?.value!=='34'||document.querySelector('.performance-inspector [name=pitch]')?.value!=='67'||document.querySelector('#cicada-pattern-preview [data-pattern-preview-track]').value!=='upper')throw new Error('Reopened copy lost its sound, notes or timing');
      const reopened=await render('reopened');if(reopened.hash!==retained.performed.hash)throw new Error('Reopened WAV changed');sessionStorage.removeItem(key);checks.push('reopened local variations retain their sound selection, edited performance and WAV');
      return {type:'cicada-native-smoke',ok:true,checks,...retained,phase:'done',reopened};
    }
    const baseline=await render('baseline');await chooseUpper('motif.idea');await audition('unused library idea');
    if(!/^[a-z_][a-z0-9_-]*$/.test(variation('motif.idea').querySelector('[name=newName]').value))throw new Error('Library variation proposed an invalid name');
    if(typeof window.__cicadaDirectViews==='function')await window.__cicadaDirectViews();
    fill(variation('motif.idea').querySelector('[name=newName]'),'off');variation('motif.idea').querySelector('button[type=submit]').click();await wait(()=>variation('motif.idea')?.querySelector('form').dataset.gosxFormState==='error','reserved name refused');await ready();
    if((await read()).source!==initial||!variation('motif.idea').closest('details').open)throw new Error('Failed copy changed music or hid retry controls');
    const copied=await copy('motif.idea','chorus');if(!copied.startsWith(initial))throw new Error('Copy rewrote original entry declarations');
    document.querySelector('#undo-form button').click();await saved(initial);document.querySelector('#redo-form button').click();await saved(copied);await ready();
    const unchanged=await render('copied');if(unchanged.hash!==baseline.hash)throw new Error('Unused variation changed the song');await audition('local chorus');
    checks.push('an unused library grid becomes a local variation in the chosen register with one Undo and unchanged song audio');
    const gridSource=await bind('chorus'),grid=await render('grid');if(grid.hash===baseline.hash)throw new Error('Assigned variation did not sound');
    [...document.querySelectorAll('nav.pattern-library a')].find(a=>new URL(a.href).searchParams.get('pattern')==='motif.played').click();await wait(()=>selected()==='motif.played','unused performance selected');await ready();
    const performedCopy=await copy('motif.played','answer');if(!performedCopy.includes('gesture-group touch'))throw new Error('Copy lost shared gestures');
    const note=document.querySelector('.performance-inspector form');fill(note.querySelector('[name=pitch]'),'67');note.querySelector('button[type=submit]').click();await wait(async()=>/note 67 at=17ticks/.test((await read()).source),'local performance edited');await ready();
    if(new URL(location.href).searchParams.get('track')!=='upper')throw new Error('Note edit lost sound context');
    const beforeScale=(await read()).source;document.querySelector('[data-performance-time=answer] button[value="2/1"]').click();await wait(async()=>/pattern answer performance length=7680ticks/.test((await read()).source),'copy shaped in time');await ready();
    if(new URL(location.href).searchParams.get('track')!=='upper')throw new Error('Time edit lost sound context');
    const scaled=(await read()).source;document.querySelector('#undo-form button').click();await saved(beforeScale);document.querySelector('#redo-form button').click();await saved(scaled);await ready();
    const final=await bind('answer'),performed=await render('performed');if(performed.hash===grid.hash)throw new Error('New performance did not replace the scene part');
    checks.push('a library performance becomes editable locally, keeps shared gestures and its chosen sound through note/time edits, then replaces the scene part');
    sessionStorage.setItem(key,JSON.stringify({phase:'reopen',checks,initial,original:initial,copied,gridSource,performedCopy,beforeScale,final,baseline,unchanged,grid,performed}));location.href='/studio?dock=clip&pattern=answer&track=upper&scene=main&region=detail';return null;
  })().catch(error=>({type:'cicada-native-smoke',ok:false,checks,error:String(error)+'\n'+String(error.stack||'')})).then(result=>{if(!result)return;if(typeof window.__cicadaDirectResult==='function')window.__cicadaDirectResult(result);else window.chrome?.webview?.postMessage(JSON.stringify(result));});
})();
