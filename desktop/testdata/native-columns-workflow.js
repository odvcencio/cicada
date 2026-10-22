// Disposable WKWebView only. No recording, MIDI, or audio input is opened.
(() => {
 if(location.hostname!=='127.0.0.1'||window.__cicadaColumnsSmoke)return;
 window.__cicadaColumnsSmoke=true;
 if(!sessionStorage.getItem('cicada.columns-seeded')){
  for(const [name,value] of Object.entries({browserWidth:640,codeWidth:720,dockHeight:120,browserOpen:true,codeOpen:true}))localStorage.setItem('studio:layout:'+name,JSON.stringify(value));
  sessionStorage.setItem('cicada.columns-seeded','true');location.reload();return;
 }
 const checks=[],errors=[];
 addEventListener('error',event=>errors.push(event.message));addEventListener('unhandledrejection',event=>errors.push(String(event.reason)));
 const pause=()=>new Promise(resolve=>setTimeout(resolve,50));
 async function wait(check,label){const end=performance.now()+20000;while(performance.now()<end){if(await check())return;await pause();}throw new Error('Timed out: '+label);}
 const rect=id=>document.getElementById(id).getBoundingClientRect();
 const handle=name=>document.querySelector('[aria-label="'+name+'"]');
 const fits=()=>{
  if(rect('studio-center').width<399||document.documentElement.scrollWidth>innerWidth)throw new Error('Sidebars squeezed the native workspace');
  for(const [id,key,label]of[['studio-browser','browserWidth','Resize Browser'],['studio-code','codeWidth','Resize Code lens']]){
   const open=document.querySelector('#'+id+' details').open,h=handle(label),value=Number(h.getAttribute('aria-valuenow'));
   if(open&&(Math.abs(rect(id).width-value)>1||value>Number(h.getAttribute('aria-valuemax'))||value!==JSON.parse(localStorage.getItem('studio:layout:'+key))))throw new Error('Native panel, resize bounds and storage disagree');
   if(!open&&rect(id).width!==36)throw new Error('Closed sidebar did not return its space');
  }
 };
 (async()=>{
  await wait(()=>window.__gosx?.ready&&document.querySelector('#studio-shell[data-shell-actions=ready][data-shell-layout=ready]'),'fitted shell');
  const initial=await(await fetch('/api/state',{cache:'no-store'})).json(),audio=await(await fetch('/api/audio/config',{cache:'no-store'})).json();
  if(!initial.source.includes('title "Desktop CI"')||audio.options.inputEnabled||String(audio.inventory.backendName||audio.inventory.backend).toLowerCase()!=='null')throw new Error('Use disposable smoke score and null audio');
  fits();checks.push('oversized saved sidebars leave a visible native song workspace');
  const editor=document.querySelector('#score textarea'),draft=editor.value+'\n// unsaved sidebar proof\n';editor.value=draft;editor.dispatchEvent(new Event('input',{bubbles:true}));
  const browser=handle('Resize Browser'),before=rect('studio-browser').width;
  browser.focus();browser.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowLeft',bubbles:true,cancelable:true}));
  await wait(()=>rect('studio-browser').width===before-8,'first keyboard resize starts at visible width');fits();checks.push('native keyboard resizing uses the constrained size');
  const codeWidth=rect('studio-code').width,center=rect('studio-center').width;
  document.querySelector('#studio-code > details > summary').click();await wait(()=>rect('studio-code').width===36,'Code rail');fits();
  if(rect('studio-center').width<center+codeWidth-37)throw new Error('Code collapse did not expand the song');
  document.querySelector('#studio-browser > details > summary').click();await wait(()=>rect('studio-browser').width===36,'Browser rail');fits();checks.push('closing both sidebars gives their space to the song');
  document.querySelector('#studio-browser > details > summary').click();document.querySelector('#studio-code > details > summary').click();await wait(()=>rect('studio-browser').width>36&&rect('studio-code').width>36,'reopened sidebars');fits();
  document.querySelector('#detail-tab-device').click();browser.focus();browser.dispatchEvent(new KeyboardEvent('keydown',{key:'End',bubbles:true,cancelable:true}));
  await wait(()=>rect('studio-browser').width===Number(browser.getAttribute('aria-valuemax')),'End uses current bounds');fits();checks.push('rails reopen and resize bounds survive local tab navigation');
  const final=await(await fetch('/api/state',{cache:'no-store'})).json();
  if(editor.value!==draft||initial.source!==final.source||initial.revision!==final.revision||errors.length)throw new Error('Layout changed musical work or reported errors: '+JSON.stringify(errors));
  checks.push('native layout interactions preserve saved and unsaved musical work');
  sessionStorage.removeItem('cicada.columns-seeded');return{type:'cicada-native-smoke',ok:true,checks};
 })().catch(error=>({type:'cicada-native-smoke',ok:false,checks,error:String(error)+'\n'+error.stack,errors})).then(result=>{window.__cicadaColumnsResult=result;if(window.chrome?.webview)window.chrome.webview.postMessage(JSON.stringify(result));});
})();
