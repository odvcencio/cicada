(() => {
 if (window.__liveAmpReopen) return; window.__liveAmpReopen=true;
 const checks=[];
 const wait=async(fn,label)=>{const end=Date.now()+20000;while(Date.now()<end){if(await fn())return;await new Promise(r=>setTimeout(r,100))}throw Error('Timed out: '+label)};
 const state=async(path='state')=>{const r=await fetch('/api/'+path,{cache:'no-store'});if(!r.ok)throw Error(path+' '+r.status);return r.json()};
 (async()=>{
  await wait(()=>window.__gosx?.ready===true&&document.querySelector('#studio-shell[data-shell-actions=ready]'),'reopened Studio');
  const audio=await state('audio/config');if(audio.options.inputEnabled||!audio.options.monitorMuted||!audio.options.ampEnabled)throw Error('stored amp settings or muted startup were not preserved');checks.push('saved amp enabled but input disabled and monitoring muted after restart');
  const score=await state();if(!/fx\s+guitar_live_amp\s+ampcab/.test(score.source)||!/drive\s*=\s*0\.2/.test(score.source)||!/bypass\s*=\s*(false|off)/.test(score.source))throw Error('reopened score lost amp edits');checks.push('reopened score retains playback amp and bypass state');
  await wait(()=>[...document.querySelectorAll('#vocals button')].some(x=>x.textContent.trim()==='Save playback amp'),'reopened track editor'); checks.push('reopened track editor is available');
  return {type:'cicada-native-smoke',ok:true,checks};
 })().catch(e=>({type:'cicada-native-smoke',ok:false,checks,error:String(e)+'\n'+e.stack})).then(r=>window.chrome.webview.postMessage(JSON.stringify(r)));
})();
