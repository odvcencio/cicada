// Disposable synthetic audio fixture and null output. No microphone is opened.
(() => {
 if(location.hostname!=='127.0.0.1'||window.__ampAutomationSmoke)return;window.__ampAutomationSmoke=true;
 let unloading=false;addEventListener('pagehide',()=>{unloading=true});const key='cicada-amp-automation-smoke';
 const wait=async(fn,label)=>{const end=Date.now()+30000;while(Date.now()<end){try{if(await fn())return}catch(e){if(!(e instanceof TypeError))throw e}await new Promise(r=>setTimeout(r,50))}throw Error('Timed out: '+label)};
 const read=async name=>{for(let attempt=0;;attempt++){try{const r=await fetch('/api/'+name,{cache:'no-store'});if(!r.ok)throw Error(name+': '+r.status);return await r.json()}catch(e){if(!(e instanceof TypeError)||attempt>=4)throw e;await new Promise(r=>setTimeout(r,100))}}};
 const ready=()=>wait(()=>window.__gosx?.ready&&document.querySelector('#studio-shell[data-shell-actions=ready]')&&!document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'),'Studio controls');
 const click=(selector,scope=document)=>{const el=scope.querySelector(selector);if(!el||el.disabled)throw Error('Unavailable '+selector);el.click()};
 const input=(el,value)=>{if(!el)throw Error('Missing input');el.value=value;el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}))};
 const saved=fn=>wait(async()=>{await ready();return fn((await read('state')).source)},'score saved');
 (async()=>{
  await ready();const initial=await read('state'),audio=await read('audio/config');if(!initial.source.includes('title "Synthetic guitar"')||audio.options.inputEnabled||String(audio.inventory.backendName||audio.inventory.backend).toLowerCase()!=='null')throw Error('disposable null-audio fixture required');
  const state=JSON.parse(sessionStorage.getItem(key)||'null')||{phase:0,original:initial.source,checks:[]};const phase=n=>{state.phase=n;sessionStorage.setItem(key,JSON.stringify(state))};
  while(true){await ready();switch(state.phase){
   case 0:{
    click('#detail-tab-device');await wait(()=>!document.querySelector('#detail-panel-device')?.hidden,'Device dock');
    const link=[...document.querySelectorAll('a')].find(a=>a.textContent.trim()==='Automate Drive'&&a.closest('#detail-panel-device'));if(!link)throw Error('track amp automation entry missing');phase(1);link.click();break;
   }
   case 1:{
    await wait(()=>document.querySelector('#detail-panel-automation .automation-recording [name=path]')?.value==='guitar.amp_drive'&&!document.querySelector('#detail-panel-automation').hidden,'selected amp curve');
    if(!document.querySelector('#detail-panel-automation').textContent.includes('Input monitoring uses separate settings'))throw Error('monitor separation missing');
    click('#transport-form button[value=play]');await wait(async()=>(await read('transport')).playing,'playback');await wait(async()=>Number((await read('transport')).bar)>=2,'second bar');
    const form=document.querySelector('#detail-panel-automation .automation-recording');await wait(()=>document.querySelector('#cicada-automation-curve[data-automation-ready=true]')&&!form.querySelector('[data-automation-record-command=start]').disabled,'recorder ready');const replace=form.querySelector('[name=replace]');replace.checked=true;replace.dispatchEvent(new Event('change',{bubbles:true}));click('[data-automation-record-command=start]',form);await wait(async()=>(await read('automation/recording')).recording?.active,'pass');
    const first=(await read('automation/recording')).recording.points[0].value;if(Math.abs(first-.8)>.001)throw Error('wrong grid start '+first);const slider=form.querySelector('[data-automation-record-value]');await wait(()=>!slider.disabled&&Math.abs(Number(slider.value)-first)<.001,'seed readout');input(slider,'0.45');await wait(async()=>(await read('automation/recording')).recording?.points.some(p=>Math.abs(p.value-.45)<.001),'movement');await new Promise(r=>setTimeout(r,700));if(Math.abs(Number(slider.value)-.45)>.001)throw Error('poll overwrote performance');state.checks.push('track link selects amp curve; pass starts at saved mid-song value and preserves movement');phase(2);click('[data-automation-record-command=keep]',form);break;
   }
   case 2:{await saved(s=>s!==state.original&&s.includes('automate guitar.amp_drive'));state.kept=(await read('state')).source;await wait(async()=>!(await read('automation/recording')).recording,'kept');click('#transport-form button[value=stop]');await wait(async()=>!(await read('transport')).playing,'stopped');phase(3);click('#undo-form button');break}
   case 3:await saved(s=>s===state.original);phase(4);click('#redo-form button');break;
   case 4:await saved(s=>s===state.kept);state.checks.push('Keep, Undo and Redo preserve the editable track curve');sessionStorage.removeItem(key);return {type:'cicada-native-smoke',ok:true,checks:state.checks,physicalAudioVerified:false,microphoneOpened:false};
   default:throw Error('unexpected phase '+state.phase);
  }}
 })().catch(e=>{if(unloading)return new Promise(()=>{});return {type:'cicada-native-smoke',ok:false,error:String(e)+'\n'+String(e.stack||''),phase:JSON.parse(sessionStorage.getItem(key)||'{}').phase}}).then(r=>window.chrome.webview.postMessage(JSON.stringify(r)));
})();
