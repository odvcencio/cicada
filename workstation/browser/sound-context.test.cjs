'use strict';
const fs=require('node:fs'),path=require('node:path');
if(process.argv[2]==='--write-fixture'){
 const root=path.resolve(process.argv[3]);fs.mkdirSync(root,{recursive:true});const score=path.join(root,'main.cicada');if(fs.existsSync(score))throw new Error('Use a new disposable directory');
 fs.writeFileSync(score,`// cicada-sound-context-test: disposable fixture
cicada 2
title "Keep the sound of an idea"
tempo 120
instrument soft { voice poly { out=sine(pitch)*env(gate,150ms)*(0.1+pressure*0.05+timbre*0.05) } }
instrument bright { voice poly { out=saw(pitch)*env(gate,300ms)*(0.1+pressure*0.05+timbre*0.05) } }
track keys soft { level=-24dB }
track upper bright { level=-24dB }
pattern played performance length=3840ticks {
 gesture-group touch {
  gesture at=0ticks bend=0ct pressure=0 timbre=0.5;
  gesture at=960ticks bend=200ct pressure=0.8 timbre=0.9;
 }
 note 60 at=17ticks length=703ticks velocity=103 group=touch {
  gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7;
 }
 note 64 at=19ticks length=1301ticks velocity=87 group=touch;
}
scene main { keys=played upper=played }
scene carry { keys=keep upper=keep }
song { main*2 carry*2 }
`);process.stdout.write(JSON.stringify({score})+'\n');process.exit(0);
}
const test=require('node:test'),assert=require('node:assert/strict');
test('sound choice follows phrase editing without replacing drafts',{timeout:240000},async t=>{
 const base=new URL(process.env.CICADA_STUDIO_URL),proof=process.env.CICADA_CONTEXT_PROOF_DIR;assert.equal(base.hostname,'127.0.0.1');assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']});t.after(() => browser.close(), {timeout: 15000});const context=await browser.newContext({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),page=await context.newPage(),errors=[],auditions=[];page.on('pageerror',e=>errors.push(e.message));page.on('request',request=>{if(request.url().endsWith('/__actions/sound-preview')){const body=request.postDataJSON();if(body?.action==='pattern')auditions.push(body.track);}});
 await page.exposeFunction('__cicadaContextAudio',(label,base64)=>fs.writeFileSync(path.join(proof,label+'.wav'),Buffer.from(base64,'base64')));
 await page.exposeFunction('__cicadaContextViews',async()=>{const preview=page.locator('#cicada-pattern-preview');await preview.screenshot({path:path.join(proof,'sound-desktop.png')});await page.setViewportSize({width:390,height:844});await page.waitForTimeout(150);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await preview.screenshot({path:path.join(proof,'sound-phone.png')});await page.setViewportSize({width:1440,height:1100});});
 if(process.env.CICADA_CONTEXT_REOPEN==='1'){const previous=JSON.parse(fs.readFileSync(path.join(proof,'report.json'),'utf8')).result;await page.addInitScript(previous=>sessionStorage.setItem('cicada.sound-context-proof',JSON.stringify({...previous,phase:'reopen'})),previous);}
 await page.goto(base.href);
 if(process.env.CICADA_CONTEXT_REOPEN!=='1') {
  const initial=await (await page.request.get(base.origin+'/api/state')).json();
  // An unsaved draft refuses clip navigation without changing the dock.
  await page.goto(base.origin+'/studio?tab=arrange&dock=device&region=center');
  await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
  if(!await page.locator('#studio-code > details').evaluate(node=>node.open)) await page.locator('.shell-pane-toggle[data-pane=code][data-pane-action=show]').click();
  const stableURL=()=>{const u=new URL(page.url());u.searchParams.sort();return u.href;};
  const draft=page.locator('#score textarea').first(),beforeDraft=await draft.inputValue(),blockedURL=stableURL();
  await draft.fill(beforeDraft+'\n// unsaved scene clip draft\n');
  await page.locator('#arrangement a.timeline-clip[title="main / upper"]').click();
  await page.waitForFunction(()=>document.querySelector('[data-scene-open-status]')?.textContent==='Save or discard unsaved edits before opening a pattern.');
  assert.equal(await page.locator('[data-scene-open-status]').isVisible(),true);
  assert.equal(stableURL(),blockedURL);assert.equal(await page.locator('#detail-tab-device').getAttribute('aria-selected'),'true');
  assert.equal(await page.locator('#studio-shell').getAttribute('data-phone-region'),'center');assert.equal(await draft.inputValue(),beforeDraft+'\n// unsaved scene clip draft\n');
  await draft.fill(beforeDraft);
  await page.evaluate(()=>document.documentElement.setAttribute('data-gosx-navigation-state','pending'));
  await page.locator('#arrangement a.timeline-clip[title="main / upper"]').click();
  await page.waitForFunction(()=>document.querySelector('[data-scene-open-status]')?.textContent==='Wait for the current action to finish before opening a pattern.');
  assert.equal(stableURL(),blockedURL);assert.equal(await page.locator('#detail-tab-device').getAttribute('aria-selected'),'true');
  await page.evaluate(()=>document.documentElement.setAttribute('data-gosx-navigation-state','idle'));
  // A later local tab choice wins while the clip request is in flight.
  let release,arrived;const held=new Promise(resolve=>{release=resolve;}),requested=new Promise(resolve=>{arrived=resolve;});
  const hold=async route=>{arrived();await held;await route.continue();};await page.route('**/studio?*',hold);
  await page.evaluate(()=>{window.__sceneClipNavigationComplete=false;document.addEventListener('gosx:navigate',()=>{const q=new URL(location.href).searchParams;if(q.get('pattern')==='played'&&q.get('track')==='upper')window.__sceneClipNavigationComplete=true;});});
  try {
   await page.locator('#arrangement a.timeline-clip[title="main / upper"]').click({noWaitAfter:true});
   await Promise.race([requested,new Promise((_,reject)=>setTimeout(()=>reject(new Error('clip navigation did not start')),10000))]);
   await page.locator('#detail-tab-automation').click({noWaitAfter:true});release();
   await page.waitForFunction(()=>window.__sceneClipNavigationComplete&&document.documentElement.getAttribute('data-gosx-navigation-state')==='idle');
   await page.waitForFunction(()=>document.querySelector('#detail-tab-automation')?.getAttribute('aria-selected')==='true');
   assert.equal(new URL(page.url()).searchParams.get('dock'),'automation');
  } finally {release();await page.unroute('**/studio?*',hold);}
  for(const [title,track,scene] of [['main / upper','upper','main'],['carry / upper','upper',null],['main / keys','keys','main']]) {
   await page.goto(base.origin+'/studio?tab=arrange&dock=device&region=center');
   await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
   await page.locator('#arrangement a.timeline-clip[title="'+title+'"]').click();
   await page.waitForFunction(expected=>{const q=new URL(location.href).searchParams;return q.get('pattern')==='played'&&q.get('track')===expected.track&&q.get('scene')===expected.scene&&q.get('dock')==='clip';},{track,scene});
   await page.locator('#detail-panel-clip').waitFor({state:'visible'});
   await page.locator('#cicada-pattern-preview[data-sounds-ready=true]').waitFor();
   assert.equal(await page.locator('[data-pattern-preview-track]').inputValue(),track,'the opened clip previews its own track');
   if(title==='main / upper') {
    const button=page.locator('#cicada-pattern-preview [data-sound-preview]');await button.waitFor({state:'visible'});await page.waitForFunction(()=>!document.querySelector('#cicada-pattern-preview [data-sound-preview]').disabled);
    const request=page.waitForRequest(r=>r.url().endsWith('/__actions/sound-preview')&&r.postDataJSON()?.action==='pattern');await button.click();assert.equal((await request).postDataJSON().track,'upper');
    await page.screenshot({path:path.join(proof,'arrange-upper-track.png')});
   }
  }
  assert.equal((await (await page.request.get(base.origin+'/api/state')).json()).source,initial.source,'opening and auditioning clips must not edit the score');
  auditions.length=0;
 }
 await page.addInitScript(()=>{window.__cicadaContextResult=result=>{window.__contextResult=result;};});await page.addInitScript({path:path.resolve(__dirname,'../../desktop/testdata/native-sound-context-workflow.js')});await page.goto(base.origin+'/studio?dock=clip&pattern=played&note=0&scene=main&track='+(process.env.CICADA_CONTEXT_REOPEN==='1'?'upper':'keys')+'&region=detail');await page.waitForFunction(()=>window.__contextResult,null,{timeout:220000});const result=await page.evaluate(()=>window.__contextResult);
 fs.writeFileSync(path.join(proof,process.env.CICADA_CONTEXT_REOPEN==='1'?'reopen-report.json':'report.json'),JSON.stringify({result,errors,auditions},null,2)+'\n');if(!result.ok){await page.screenshot({path:path.join(proof,'failure.png')});fs.writeFileSync(path.join(proof,'failure.html'),await page.content());}assert.equal(result.ok,true,result.error);assert.deepEqual(errors,[]);if(process.env.CICADA_CONTEXT_REOPEN!=='1')assert.deepEqual(auditions,['upper','upper']);
 if(process.env.CICADA_CONTEXT_REOPEN!=='1'){
  const legacy=await page.context().newPage();legacy.on('pageerror',e=>errors.push(e.message));await legacy.goto(base.origin+'/studio?dock=clip&track=keys&region=detail');await legacy.waitForSelector('#cicada-pattern-preview[data-sounds-ready=true]');await legacy.locator('[data-pattern-preview-track]').selectOption('upper');await legacy.waitForFunction(()=>new URL(location.href).searchParams.get('pattern')==='played'&&new URL(location.href).searchParams.get('track')==='upper');const variationReturn=await legacy.locator('[data-pattern-variation=played] [name=__gosx_return_to]').inputValue();assert.equal(new URL(variationReturn,base).searchParams.get('track'),'upper');assert.equal(new URL(variationReturn,base).searchParams.get('pattern'),'played');
  await legacy.goto(base.origin+'/?panel=patterns&pattern=played&track=keys&note=0');await legacy.waitForSelector('#cicada-workspace[data-workspace-reactive=ready]');await legacy.waitForSelector('#cicada-pattern-preview[data-sounds-ready=true]');
  await legacy.locator('[data-pattern-preview-track]').selectOption('upper');await legacy.waitForFunction(()=>new URL(location.href).searchParams.get('track')==='upper');await legacy.locator('.performance-content tbody th a').nth(1).click();await legacy.waitForFunction(()=>new URL(location.href).searchParams.get('note')==='1'&&document.querySelector('[data-pattern-preview-track]')?.value==='upper');assert.equal(await legacy.locator('.performance-inspector [name=track]').inputValue(),'upper');const state=await legacy.request.get(base.origin+'/api/state');assert.equal(state.ok(),true);assert.equal((await state.json()).source,result.final);fs.writeFileSync(path.join(proof,'legacy.json'),JSON.stringify({sound:'upper',note:1,sourceMutated:false,selectionRetained:true},null,2)+'\n');await legacy.close();assert.deepEqual(errors,[]);
 }
});
