// UI-only tempo authoring in a copied synthetic score, with null audio.
(() => {
 if (location.hostname !== '127.0.0.1' || window.__cicadaTempoEditorSmoke) return;
 window.__cicadaTempoEditorSmoke = true;
 const read = async name => { const response = await fetch('/api/'+name, {cache:'no-store'}); if (!response.ok) throw new Error(name+': '+response.status); return response.json(); };
 const wait = async (check,label) => { const end=performance.now()+60000; while(performance.now()<end){if(await check())return;await new Promise(r=>setTimeout(r,50))}throw new Error('Timed out: '+label); };
 const ready = () => wait(()=>window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]') && !document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'),'managed forms');
 const button = (label,scope=document) => [...scope.querySelectorAll('button')].find(n=>n.textContent.trim()===label && !n.disabled);
 const open = () => { const node=document.querySelector('[data-arrangement-tempos]'); if(!node.open)node.querySelector('summary').click(); };
 const edit = (form,values,label) => { open();for(const [name,value]of Object.entries(values)){const input=form.querySelector('[name='+name+']');input.value=value;input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));}const submit=button(label,form);if(!submit)throw new Error('Missing '+label);submit.click(); };
 (async()=>{
  await ready();const initial=await read('state'),audio=await read('audio/config');
  if(!initial.source.includes('cicada-tempo-editor-test: disposable fixture') || !initial.source.includes('arrange {') || audio.options.inputEnabled || String(audio.inventory.backendName||audio.inventory.backend).toLowerCase()!=='null')throw new Error('Use only a copied tempo fixture with null audio');
  const key='cicada-tempo-editor-smoke',state=JSON.parse(sessionStorage.getItem(key)||'null')||{phase:'add',original:initial.source,checks:[]};
  const phase=name=>{state.phase=name;sessionStorage.setItem(key,JSON.stringify(state))};
  const saved=async predicate=>wait(async()=>{await ready();return predicate((await read('state')).source)},'saved tempo source');
  while(true){
   await ready();open();
   switch(state.phase){
    case 'add': phase('added');edit(document.querySelector('[data-tempo-add] form'),{bar:'3',bpm:'96'},'Add tempo step');break;
    case 'added': await saved(source=>source.includes('tempo @3.1.1 96'));state.added=(await read('state')).source;phase('moved');edit(document.querySelector('[data-tempo-step="@3.1.1"] form'),{bar:'6',bpm:'137.333'},'Save tempo step');break;
    case 'moved': await saved(source=>source.includes('tempo @6.1.1 137.333')&&!source.includes('tempo @3.1.1'));state.moved=(await read('state')).source;phase('undone');button('Undo').click();break;
    case 'undone': await saved(source=>source===state.added);phase('redone');button('Redo').click();break;
    case 'redone': await saved(source=>source===state.moved);state.checks.push('native tempo move and BPM change restore exact source through Undo/Redo');phase('removed');edit(document.querySelector('[data-tempo-step="@6.1.1"] form'),{},'Remove step');break;
    case 'removed': await saved(source=>!source.includes('tempo @6.1.1'));phase('restored');button('Undo').click();break;
    case 'restored': await saved(source=>source===state.moved);state.checks.push('native removal and Undo restore the tempo step');state.previousExport=(await read('export')).id;phase('export');button('Export').click();break;
    case 'export': await wait(()=>button('Render WAV'),'export controls');phase('render');button('Render WAV').click();break;
    case 'render': {
     let job;await wait(async()=>{job=await read('export');if(job.state==='failed')throw new Error(JSON.stringify(job));return job.id!==state.previousExport&&['succeeded','shortfall'].includes(job.state)},'edited WAV');
     const response=await fetch(job.downloadURL),bytes=new Uint8Array(await response.arrayBuffer());if(!response.ok||String.fromCharCode(...bytes.slice(0,4))!=='RIFF'||bytes.length<100000)throw new Error('Invalid WAV');
     const sha=[...new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))].map(n=>n.toString(16).padStart(2,'0')).join('');state.checks.push('native edited tempo exports WAV '+sha);sessionStorage.removeItem(key);return {type:'cicada-native-smoke',ok:true,checks:state.checks};
    }
    default:throw new Error('Unknown phase '+state.phase);
   }
  }
 })().catch(error=>({type:'cicada-native-smoke',ok:false,checks:[],error:String(error)+'\n'+String(error.stack||'')+'\n'+sessionStorage.getItem('cicada-tempo-editor-smoke')+'\n'+document.body.innerText.slice(-4000)})).then(result=>window.chrome?.webview?.postMessage(JSON.stringify(result)));
})();
