// Test-only synthetic PCM backend; no physical input device is opened.
(() => {
 if(location.hostname!=='127.0.0.1'||window.__liveWriteSmoke||location.pathname.startsWith('/__local/'))return;window.__liveWriteSmoke=true;
 if(location.pathname!=='/studio'){location.href='/studio?tab=session&dock=automation&curvePath=guitar.amp_drive&vocalTrack=guitar';return}
 let unloading=false;addEventListener('pagehide',()=>{unloading=true});
 const key='cicada-live-write-smoke',wait=async(fn,label)=>{const end=Date.now()+30000;while(Date.now()<end){try{if(await fn())return}catch(e){if(!(e instanceof TypeError))throw e}await new Promise(r=>setTimeout(r,50))}throw Error('Timed out: '+label)};
 const read=async name=>{const response=await fetch('/api/'+name,{cache:'no-store'});if(!response.ok)throw Error(name+': '+response.status);return response.json()};
 const ready=()=>wait(()=>window.__gosx?.ready&&document.querySelector('#studio-shell[data-shell-actions=ready]')&&!document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'),'Studio ready');
 const click=(selector,scope=document)=>{const el=scope.querySelector(selector);if(!el||el.disabled)throw Error('Unavailable '+selector);el.click()};
 const input=(el,value)=>{if(!el)throw Error('Missing input');el.value=value;el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}))};
 (async()=>{
  await ready();const state=JSON.parse(sessionStorage.getItem(key)||'null')||{phase:0,checks:[]};const save=()=>sessionStorage.setItem(key,JSON.stringify(state));
  if(!(await read('state')).source.includes('title "Synthetic live automation"'))throw Error('Disposable synthetic fixture required');
  if(state.phase===0){state.phase=1;save();location.href='/studio?tab=session&dock=automation&curvePath=guitar.amp_drive&vocalTrack=guitar';return null}
  while(true){
   await ready();
   if(state.phase===7){if((await read('state')).source!==state.kept||!(await read('takes')).takes?.some(t=>t.id===state.takeID))throw Error('Reopened project lost the kept curve or audio take');state.checks.push('reopening retained both the audio take and automation curve');sessionStorage.removeItem(key);return {type:'cicada-native-smoke',ok:true,checks:state.checks,takeID:state.takeID,physicalAudioVerified:false,microphoneOpened:false,input:'synthetic PCM'}}
   await wait(()=>!document.querySelector('#detail-panel-automation')?.hidden&&document.querySelector('#cicada-automation-curve[data-automation-ready=true]')&&document.querySelector('#detail-panel-automation .automation-recording [name=path]')?.value==='guitar.amp_drive','selected automation controls');
   const form=()=>document.querySelector('#detail-panel-automation .automation-recording');form().querySelector('details').open=true;
   switch(state.phase){
   case 1:{
    const linked=form().querySelector('[name=monitor]');if(!linked||linked.checked)throw Error('link must be explicit and off by default');linked.checked=true;linked.dispatchEvent(new Event('change',{bubbles:true}));
    await wait(()=>!form().querySelector('[data-automation-record-command=start]').disabled,'prepare available');click('[data-automation-record-command=start]',form());
    await wait(async()=>(await read('automation/recording')).recording?.prepared,'prepared pass');if((await read('capture')).activeCapture)throw Error('prepare opened input');
    await wait(()=>!form().querySelector('[data-automation-record-command=discard]').disabled,'cancel available');click('[data-automation-record-command=discard]',form());await wait(async()=>!(await read('automation/recording')).recording,'cancel preparation');
    await wait(()=>!form().querySelector('[data-automation-record-command=start]').disabled,'prepare available again');click('[data-automation-record-command=start]',form());await wait(async()=>(await read('automation/recording')).recording?.prepared,'prepared again');
    await wait(()=>form().querySelector('[data-automation-record-status]')?.textContent.includes('prepared'),'prepared state visible');if(typeof window.__liveWriteEvidence==='function')await window.__liveWriteEvidence('prepared');state.checks.push('explicit preparation opens no input and can be canceled');
    const record=document.querySelector('#vocals [data-capture-control=record]');if(!record||record.disabled)throw Error('Record take unavailable');state.phase=2;save();record.click();break;
   }
   case 2:{
    await wait(async()=>{const c=await read('capture'),r=(await read('automation/recording')).recording;return c.activeCapture&&c.capture?.recording&&r?.active&&r.monitor&&!r.prepared},'linked audio capture');
    const slider=form().querySelector('[data-automation-record-value]');await wait(()=>slider&&!slider.disabled,'Perform enabled');input(slider,'0.7');await wait(async()=>(await read('automation/recording')).recording?.points.some(p=>Math.abs(p.value-.7)<.001),'movement recorded');
    await wait(()=>form().querySelector('[data-automation-record-command=keep]').disabled,'Keep locked during audio capture');if(typeof window.__liveWriteEvidence==='function')await window.__liveWriteEvidence('recording');state.checks.push('Record starts the prepared pass and a movement is captured while Keep waits');state.phase=3;save();click('#vocals [data-capture-control=stop]');break;
   }
   case 3:{
    await wait(async()=>!(await read('capture')).activeCapture,'audio finalized');await wait(async()=>{const r=(await read('automation/recording')).recording;return r&&!r.active&&!r.prepared&&!r.invalid},'retained pass');
    await ready();state.afterTake=(await read('state')).source;state.takeID=(await read('takes')).takes?.find(t=>t.track==='guitar'&&t.stage==='published')?.id;if(!state.takeID)throw Error('audio take was not published');await wait(()=>!form().querySelector('[data-automation-record-command=keep]').disabled,'Keep enabled');state.phase=4;save();click('[data-automation-record-command=keep]',form());const undo=document.querySelector('#undo-form'),submit=new Event('submit',{bubbles:true,cancelable:true});if(!undo.querySelector('button').disabled||undo.dispatchEvent(submit))throw Error('Undo was not gated while Keep refreshed');state.checks.push('Undo button and direct submit are gated while Keep refreshes');save();break;
   }
   case 4:await wait(async()=>!(await read('automation/recording')).recording&&(await read('state')).source.includes('automate guitar.amp_drive'),'curve kept');await ready();state.kept=(await read('state')).source;await wait(()=>{const b=document.querySelector('#undo-form button');return b&&!b.disabled},'Undo available after Keep');state.phase=5;save();click('#undo-form button');break;
   case 5:await wait(async()=>(await read('state')).source===state.afterTake,'Undo curve only');if(!(await read('takes')).takes?.some(t=>t.id===state.takeID))throw Error('Undo lost the recorded take');await ready();await wait(()=>{const b=document.querySelector('#redo-form button');return b&&!b.disabled},'Redo available');state.phase=6;save();click('#redo-form button');break;
   case 6:await wait(async()=>(await read('state')).source===state.kept,'Redo curve');state.checks.push('Undo and Redo affect the curve while retaining the recorded take');state.phase=7;save();location.reload();return null;
   default:throw Error('unexpected phase '+state.phase);
   }
  }
 })().catch(e=>{if(unloading)return new Promise(()=>{});return {type:'cicada-native-smoke',ok:false,error:String(e)+'\n'+String(e.stack||'')}}).then(r=>{if(!r)return;if(typeof window.__liveWriteResult==='function')window.__liveWriteResult(r);else window.chrome.webview.postMessage(JSON.stringify(r))});
})();
