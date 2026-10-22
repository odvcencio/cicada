// Actual WKWebView cold launch. Null audio/input off; no synthesized input.
(() => {
 if(location.protocol!=='http:'||!['127.0.0.1','localhost','[::1]'].includes(location.hostname)||window.__startupSmoke)return;window.__startupSmoke=true;
 const expectStartupUI=true,checks=[],errors=[],start=performance.now();let previous=start,maxFrameGap=0,done=false;
 const pause=()=>new Promise(r=>setTimeout(r,25));
 function frame(t){maxFrameGap=Math.max(maxFrameGap,t-previous);previous=t;if(!done)requestAnimationFrame(frame)}requestAnimationFrame(frame);
 addEventListener('error',e=>errors.push(e.message));addEventListener('unhandledrejection',e=>errors.push(String(e.reason)));
 (async()=>{
  const deadline=performance.now()+30000;
  while(!(window.__gosx?.ready&&document.querySelector('#studio-shell[data-shell-actions=ready]')&&document.querySelector('.editor-page.editor-highlight-ready'))){if(performance.now()>deadline)throw Error('Studio controls did not become ready');await pause()}
  const controlsReadyMs=performance.now();await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));const firstReadyFrameMs=performance.now();
  if(expectStartupUI&&(!window.__cicadaStartup||document.querySelector('#studio-shell')?.dataset.studioStartup!=='ready'||!document.querySelector('[data-studio-startup-status]')?.hidden))throw Error('Startup feedback did not settle');
  checks.push('real WKWebView controls and highlighted Code ready; startup feedback cleared');
  const state=await(await fetch('/api/state')).json(),audio=await(await fetch('/api/audio/config')).json();
  if(!state.source.includes('title "Desktop CI"')||String(audio.inventory.backendName||audio.inventory.backend).toLowerCase()!=='null'||audio.options.inputEnabled)throw Error('Disposable fixture and null audio/input-off required');
  checks.push('task-owned fixture; null audio, input off; source untouched');
  if(errors.length)throw Error(errors.join('; '));checks.push('no runtime errors during cold launch');
  return {type:'cicada-native-smoke',ok:true,checks,timing:{probeStartMs:start,controlsReadyMs,firstReadyFrameMs,maxFrameGapMs:maxFrameGap},resources:performance.getEntriesByType('resource').filter(r=>/cicada-meters.*wasm/.test(r.name)).map(r=>({file:r.name.split('/').pop(),startMs:r.startTime,durationMs:r.duration,bytes:r.decodedBodySize})),revision:state.revision,input:'none',physicalAudioVerified:false};
 })().catch(e=>({type:'cicada-native-smoke',ok:false,checks,error:String(e),errors})).then(result=>{done=true;window.chrome.webview.postMessage(JSON.stringify(result))});
})();
