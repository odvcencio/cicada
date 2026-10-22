// Synthetic imported performance, disposable project and null audio only.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__cicadaPlacementVariationSmoke) return;
  window.__cicadaPlacementVariationSmoke = true;
  const key = 'cicada.placement-variation-proof', retained = JSON.parse(sessionStorage.getItem(key) || 'null'), checks = retained?.checks || [];
  const read = async (name = 'state') => {
    const response = await fetch('/api/' + name, {cache:'no-store'});
    if (!response.ok) throw new Error(name + ': ' + response.status);
    return response.json();
  };
  async function wait(check, label) {
    const end = performance.now() + 45000;
    while (performance.now() < end) { if (await check()) return; await new Promise(resolve => setTimeout(resolve,50)); }
    throw new Error('Timed out: ' + label);
  }
  const ready = () => wait(() => window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]') && !document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'), 'Studio ready');
  const saved = source => wait(async () => (await read()).source === source && document.querySelector('#undo-form [name=revision]')?.value === (await read()).revision, 'saved source and projection');
  const block = id => document.querySelector('[data-placement-block='+id+']');
  const variation = () => document.querySelector('[data-placement-variation=return]');
  const fill = (input,value) => { input.value=value; input.dispatchEvent(new Event('input',{bubbles:true})); input.dispatchEvent(new Event('change',{bubbles:true})); };
  async function render(label) {
    document.querySelector('[data-gosx-disclosure-target="#export-dialog"]').click();
    const previous = (await read('export')).id;
    document.querySelector('#export button[type=submit]').click(); let job;
    await wait(async () => { job=await read('export'); if(job.state==='failed')throw new Error(job.error); return job.id!==previous && ['succeeded','shortfall'].includes(job.state); },'WAV export');
    const response=await fetch(job.downloadURL), bytes=new Uint8Array(await response.arrayBuffer());
    if(!response.ok || new TextDecoder().decode(bytes.slice(0,4))!=='RIFF')throw new Error('Invalid WAV');
    const hash=[...new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))].map(x=>x.toString(16).padStart(2,'0')).join('');
    if(typeof window.__cicadaVariationAudio==='function'){let binary='';for(const byte of bytes)binary+=String.fromCharCode(byte);await window.__cicadaVariationAudio(label,btoa(binary));}
    document.querySelector('#export-dialog [data-gosx-disclosure-close]').click(); return {hash,bytes:bytes.length};
  }
  (async () => {
    await ready(); const initial=(await read()).source,audio=await read('audio/config');
    if(!initial.includes('cicada-placement-variation-test: disposable fixture') || audio.options.inputEnabled || String(audio.inventory.backendName||audio.inventory.backend).toLowerCase()!=='null')throw new Error('Use the disposable null-audio imported performance');
    if(retained?.phase==='reopen'){
      await saved(retained.final);
      if(document.querySelector('.performance-inspector [name=pitch]')?.value!=='67' || !initial.includes('gesture-group touch'))throw new Error('Reopened independent phrase lost notes or gestures');
      const reopened=await render('reopened'); if(reopened.hash!==retained.edited.hash)throw new Error('Reopened WAV differs');
      sessionStorage.removeItem(key); checks.push('reopened local variation preserves its expression and exported WAV');
      return {type:'cicada-native-smoke',ok:true,checks,original:retained.original,copied:retained.copied,final:retained.final,baseline:retained.baseline,unchanged:retained.unchanged,edited:retained.edited,reopened};
    }
    if(!block('return')){location.href='/studio?tab=arrange&region=center&placement=return';return null;}
    const baseline=await render('baseline');
    block('return').querySelector('.placement-select').click();
    await wait(()=>document.querySelector('[data-placement-inspector=return]')?.open,'selected placement');await ready();
    const field=()=>variation().querySelector('[name=newName]'), button=()=>variation().querySelector('button[type=submit]');
    if(typeof window.__cicadaVariationViews==='function')await window.__cicadaVariationViews();
    fill(field(),'off');button().click();
    await wait(()=>variation()?.querySelector('form')?.dataset.gosxFormState==='error','invalid variation refused');await ready();
    if(field().value!=='off' || (await read()).source!==initial)throw new Error('Invalid name changed music or lost input');
    fill(field(),'reply');
    if(typeof window.__cicadaVariationSubmit==='function')await window.__cicadaVariationSubmit();else button().click();
    await wait(async()=>/pattern reply performance/.test((await read()).source) && block('return')?.dataset.placementContent==='reply','independent variation committed');
    const copied=(await read()).source;await saved(copied);await ready();
    if(!copied.startsWith(initial) || block('first')?.dataset.placementContent!=='motif.parent' || block('return')?.dataset.placementAt!=='3840' || block('return')?.dataset.placementLength!=='3840')throw new Error('Variation changed original source, another block or timing');
    document.querySelector('#undo-form button').click();await saved(initial);
    await wait(()=>block('return')?.dataset.placementContent==='motif.parent','one Undo restores original use');
    document.querySelector('#redo-form button').click();await saved(copied);
    await wait(()=>block('return')?.dataset.placementContent==='reply','one Redo restores independent use');
    checks.push('one history step copies the imported performance locally and rebinds only the selected placement');
    const unchanged=await render('copied');if(unchanged.hash!==baseline.hash)throw new Error('Forking the phrase changed its sound');
    checks.push('the independent copy exports identical audio before editing');
    [...document.querySelectorAll('[data-placement-inspector=return] a')].find(a=>a.textContent==='Edit reply').click();
    await wait(()=>document.querySelector('.performance-inspector [name=pitch]'),'editable local variation');await ready();
    const note=document.querySelector('.performance-inspector form');fill(note.querySelector('[name=pitch]'),'67');note.querySelector('button[type=submit]').click();
    await wait(async()=>/note\s+67\s+at\s*=\s*17ticks/.test((await read()).source),'independent note edited');
    const final=(await read()).source;await saved(final);
    document.querySelector('#undo-form button').click();await saved(copied);
    document.querySelector('#redo-form button').click();await saved(final);await ready();
    const edited=await render('edited');if(edited.hash===baseline.hash)throw new Error('Editing the independent phrase did not change audio');
    checks.push('the local pattern editor changes the variation with Undo/Redo while retaining its shared gesture lane');
    sessionStorage.setItem(key,JSON.stringify({phase:'reopen',checks,original:initial,copied,final,baseline,unchanged,edited}));
    location.href='/studio?dock=clip&pattern=reply&region=detail';return null;
  })().catch(error=>({type:'cicada-native-smoke',ok:false,checks,error:String(error)+'\n'+String(error.stack||'')})).then(result=>{
    if(!result)return;if(typeof window.__cicadaVariationResult==='function')window.__cicadaVariationResult(result);else window.chrome?.webview?.postMessage(JSON.stringify(result));
  });
})();
