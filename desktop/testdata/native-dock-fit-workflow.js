// Disposable WKWebView workflow; no audio input, playback, or MIDI is opened.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__cicadaDockFitSmoke) return;
  window.__cicadaDockFitSmoke=true;
  if (!sessionStorage.getItem('cicada.dock-fit-seeded')) {
    localStorage.setItem('studio:layout:dockHeight','640');
    sessionStorage.setItem('cicada.dock-fit-seeded','true');
    location.reload();
    return;
  }
  const checks=[],errors=[];
  addEventListener('error',event=>errors.push(event.message));
  addEventListener('unhandledrejection',event=>errors.push(String(event.reason)));
  const pause=()=>new Promise(resolve=>setTimeout(resolve,50));
  async function wait(check,label) {
    const until=performance.now()+20000;
    while(performance.now()<until) {if(await check())return;await pause();}
    throw new Error('Timed out: '+label);
  }
  const rect=selector=>document.querySelector(selector).getBoundingClientRect();
  const handle=()=>document.querySelector('[aria-label="Resize Detail dock"]');
  const height=()=>rect('#studio-detail').height;
  (async()=>{
    await wait(()=>window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready][data-shell-layout=ready]'),'fitted shell');
    const initial=await (await fetch('/api/state',{cache:'no-store'})).json();
    const audio=await (await fetch('/api/audio/config',{cache:'no-store'})).json();
    if (!initial.source.includes('title "Desktop CI"') || audio.options.inputEnabled || String(audio.inventory.backendName||audio.inventory.backend).toLowerCase()!=='null') throw new Error('Use the disposable smoke score and null audio');
    const center=rect('#studio-center'),dock=rect('#studio-detail');
    const value=Number(handle().getAttribute('aria-valuenow')),maximum=Number(handle().getAttribute('aria-valuemax'));
    if(center.height<199 || dock.bottom>innerHeight+1 || Math.abs(dock.height-value)>1 || value>maximum || maximum>=640) throw new Error('Restored dock did not fit the native window: '+JSON.stringify({center:center.height,dock:dock.height,value,maximum,innerHeight}));
    checks.push('restored tall-screen dock leaves a usable native song workspace');
    const editor=document.querySelector('#score textarea'),draft=editor.value+'\n// unsaved dock fit proof\n';
    editor.value=draft;editor.dispatchEvent(new Event('input',{bubbles:true}));
    const before=height();
    handle().focus();handle().dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowDown',bubbles:true,cancelable:true}));
    await wait(()=>Math.abs(height()-(before-8))<1,'first keyboard resize moves eight pixels');
    checks.push('keyboard resizing starts from the visible constrained height');
    handle().dispatchEvent(new KeyboardEvent('keydown',{key:'End',bubbles:true,cancelable:true}));
    await wait(()=>Math.abs(height()-maximum)<1,'End uses the current window maximum');
    handle().dispatchEvent(new KeyboardEvent('keydown',{key:'Home',bubbles:true,cancelable:true}));
    await wait(()=>Math.abs(height()-120)<1,'Home keeps the dock accessible');
    checks.push('Home and End use window-aware accessible bounds');
    document.querySelector('#detail-tab-device').click();
    await wait(()=>document.querySelector('#detail-tab-device').getAttribute('aria-current')==='page','device tab');
    handle().focus();handle().dispatchEvent(new KeyboardEvent('keydown',{key:'End',bubbles:true,cancelable:true}));
    await wait(()=>Math.abs(height()-Number(handle().getAttribute('aria-valuemax')))<1,'bounds survive local navigation');
    const final=await (await fetch('/api/state',{cache:'no-store'})).json();
    if(editor.value!==draft || final.source!==initial.source || final.revision!==initial.revision || errors.length) throw new Error('Resizing changed musical work or reported errors: '+JSON.stringify(errors));
    checks.push('local navigation and resizing preserve the unsaved score and saved revision');
    sessionStorage.removeItem('cicada.dock-fit-seeded');
    return {type:'cicada-native-smoke',ok:true,checks};
  })().catch(error=>({type:'cicada-native-smoke',ok:false,checks,error:String(error)+'\n'+error.stack,errors}))
  .then(result=>{window.__cicadaDockFitResult=result;if(window.chrome?.webview)window.chrome.webview.postMessage(JSON.stringify(result));});
})();
