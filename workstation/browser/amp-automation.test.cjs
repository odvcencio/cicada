// Disposable synthetic guitar fixture, null audio: no physical input is opened.
'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto');
const stall=require('./page-stall.cjs');
// --write-fixture DIR writes a disposable project into the new directory DIR (see live-fixture.cjs) and prints the score path.
if(process.argv[2]==='--write-fixture'){process.stdout.write(require('./live-fixture.cjs').writeFixture(process.argv[3])+'\n');process.exit(0)}
const pause=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn,label){const end=Date.now()+30000;while(Date.now()<end){if(await fn())return;await pause(50)}assert.fail(label)}
test('track amp automation uses the saved playback path and keeps a grid-aligned pass',{timeout:120000},async t=>{
 const base=process.env.CICADA_STUDIO_URL, fixture=process.env.CICADA_AMP_AUTOMATION_FIXTURE, proof=process.env.CICADA_AMP_AUTOMATION_PROOF_DIR;
 assert.equal(new URL(base).hostname,'127.0.0.1');assert.ok(path.isAbsolute(fixture)&&path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox','--mute-audio']});const watches=[];stall.teardown(t, browser, {watch: watches, exitWhenDone: true});
 const context=await browser.newContext({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),page=await (async()=>{watches.push(await stall.watchContext(context, {serverLog: process.env.CICADA_STUDIO_STARTUP_LOG}));return context.newPage()})(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 const read=async name=>(await (await page.request.get(new URL('/api/'+name,base).href)).json());
 const ready=async()=>{await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();await until(async()=>await page.locator('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]').count()===0,'managed forms')};
 // Undo and Redo are pressed the moment the click is allowed, with no wait for the shell to catch up. Before the shell
 // disabled them while it updated the page (shell_edits_js.go, syncHistoryLock) a Redo pressed then was dropped without
 // a request; a request that reaches the server with an old revision gets 409. Neither may happen now.
 const history=[];page.on('response',r=>{const m=new URL(r.url()).pathname.match(/^\/__actions\/(undo|redo)$/);if(m)history.push([m[1],r.status()])});
 const dry=path.join(path.dirname(fixture),'dry.wav'),digest=()=>crypto.createHash('sha256').update(fs.readFileSync(dry)).digest('hex');const beforeHash=digest();
 await page.goto(new URL('/studio?tab=arrange&dock=device&deviceTrack=guitar',base).href);await ready();
 let before=(await read('state')).source;assert.match(before,/automate guitar\.amp_drive/);const catalog=(await read('params')).addresses.find(a=>a.address==='guitar.amp_drive');assert.ok(catalog&&Math.abs(catalog.value-.2)<.001,'catalog must expose saved amp baseline');assert.equal((await read('audio/config')).options.inputEnabled,false);
 const code=page.locator('#score textarea');await code.fill(before+'\n// unsaved automation idea\n');
 const controls=page.getByRole('navigation',{name:'Playback amp automation for guitar'});await controls.getByRole('link',{name:'Automate Drive'}).click();
 await until(async()=>new URL(page.url()).searchParams.get('curvePath')==='guitar.amp_drive','amp curve selected');await ready();
 assert.equal(await code.inputValue(),before+'\n// unsaved automation idea\n','amp navigation must preserve Code draft');await page.getByRole('button',{name:'Save file',exact:true}).click();await until(async()=>(await read('state')).source.includes('// unsaved automation idea'),'saved Code draft');await ready();before=(await read('state')).source;await page.locator('#detail-tab-device').click();await page.getByRole('navigation',{name:'Playback amp automation for guitar'}).getByRole('link',{name:'Automate Drive'}).click();await until(async()=>new URL(page.url()).searchParams.get('curvePath')==='guitar.amp_drive','amp curve reselected after save');await ready();
 const panel=page.locator('#detail-panel-automation');await until(async()=>await panel.isVisible(),'automation dock');await panel.locator('#cicada-automation-curve[data-automation-ready=true]').waitFor();assert.equal(await panel.locator('.automation-recording [name=path]').inputValue(),'guitar.amp_drive');assert.match(await panel.innerText(),/Input monitoring uses separate settings/);
 const record=panel.locator('.automation-recording');await record.locator('summary').click();assert.match(await record.innerText(),/start an audio take/);assert.equal(await record.getByRole('checkbox',{name:'Link Perform to the live input amp'}).isChecked(),false);
 await page.locator('#transport-form button[value=play]').click();await until(async()=>(await read('transport')).playing,'playback');
 await until(async()=>Number((await read('transport')).bar)>=2,'playback reached second bar');
 await record.locator('[name=replace]').check();await until(async()=>!await record.locator('[data-automation-record-command=start]').isDisabled(),'recorder ready');await record.locator('[data-automation-record-command=start]').click();
 try { await until(async()=>(await read('automation/recording')).recording?.active,'recording started'); } catch(e) { console.error('recording status:', await record.locator('[data-automation-record-status]').textContent(), 'transport:', JSON.stringify(await read('transport'))); throw e; }
 const first=(await read('automation/recording')).recording.points[0].value;assert.ok(Math.abs(first-.8)<.001,'pass starts from curve at current grid position');
 const slider=record.locator('[data-automation-record-value]');await until(async()=>!await slider.isDisabled()&&Math.abs(Number(await slider.inputValue())-first)<.001,'perform shows grid-aligned seed');
 await slider.fill('0.45');await until(async()=>(await read('automation/recording')).recording?.points.some(p=>Math.abs(p.value-.45)<.001),'movement reached recorder');await pause(800);assert.ok(Math.abs(Number(await slider.inputValue())-.45)<.001,'polling overwrote user movement');assert.equal((await read('state')).source,before,'live movement wrote source before Keep');
 await panel.getByRole('heading',{name:'Automation curves',exact:true}).scrollIntoViewIfNeeded();await page.screenshot({path:path.join(proof,'amp-automation-desktop.png')});await record.locator('[data-automation-record-command=keep]').click();
 await until(async()=>!(await read('automation/recording')).recording && (await read('state')).source!==before,'pass kept');const kept=(await read('state')).source;assert.match(kept,/automate guitar\.amp_drive/);
 await page.locator('#transport-form button[value=stop]').click();await until(async()=>!(await read('transport')).playing,'playback stopped');await ready();
 await page.locator('#undo-form button').click();await until(async()=>(await read('state')).source===before,'one Undo restores previous curve');await ready();await page.locator('#redo-form button').click();await until(async()=>(await read('state')).source===kept,'Redo restores pass');
 await page.reload();await ready();assert.equal((await read('state')).source,kept);assert.equal(digest(),beforeHash);assert.deepEqual(history.map(h=>h[0]),['undo','redo'],'one Undo and one Redo were sent');assert.ok(history.every(h=>h[1]!==409),'Undo or Redo was refused as stale: '+JSON.stringify(history));
 // Two reasons lock Undo and Redo: a blocked queue and a Keep in flight. Take them in the order block, Keep,
 // refresh, rejected Keep: the buttons must come back (an earlier design restored one lock's saved "disabled").
 {
  const undoBtn=page.locator('#undo-form button'),redoBtn=page.locator('#redo-form button'),lockedNow=async()=>(await undoBtn.getAttribute('aria-disabled'))==='true';
  await page.route('**/__actions/undo',route=>route.abort('failed'));
  await ready();await undoBtn.click();
  await page.locator('[data-workspace-retry]').waitFor({state:'visible'});
  assert.equal(await lockedNow(),true,'a blocked queue did not lock Undo');
  await page.unroute('**/__actions/undo');
  let releaseKeep;const keepGate=new Promise(r=>{releaseKeep=r});let keeps=0;
  await page.route('**/__actions/automation-record',async route=>{const body=route.request().postDataJSON();if(body.command==='keep'){keeps++;await keepGate;return route.fulfill({status:409,contentType:'application/json',body:JSON.stringify({ok:false,message:'Intentional Keep rejection'})})}return route.continue()});
  await page.locator('#transport-form button[value=play]').click();await until(async()=>Number((await read('transport')).bar)>=2,'second pass playback');
  await record.locator('[name=replace]').check();await until(async()=>!await record.locator('[data-automation-record-command=start]').isDisabled(),'recorder ready again');await record.locator('[data-automation-record-command=start]').click();
  await until(async()=>(await read('automation/recording')).recording?.active,'second pass started');
  await slider.fill('0.3');await until(async()=>(await read('automation/recording')).recording?.points.some(p=>Math.abs(p.value-.3)<.001),'movement reached recorder');
  // Keep (held in flight), then refresh the blocked queue: the refresh patches the buttons and unblocks.
  await record.locator('[data-automation-record-command=keep]').click();await until(async()=>keeps===1,'Keep reached the network');
  assert.equal(await lockedNow(),true,'a Keep in flight did not lock Undo');
  await page.locator('[data-workspace-retry]').click();
  await until(async()=>!(await page.locator('[data-workspace-retry]').isVisible()),'refresh unblocked the queue');
  assert.equal(await lockedNow(),true,'the refresh released the lock while the Keep was still in flight');
  assert.match(await undoBtn.getAttribute('data-history-lock'),/recording/);
  releaseKeep();
  await until(async()=>!(await lockedNow()),'Undo stayed locked after the Keep was rejected and the queue was unblocked');
  assert.equal(await undoBtn.getAttribute('data-history-lock'),null);
  await page.unroute('**/__actions/automation-record');
  await page.locator('#transport-form button[value=stop]').click();await until(async()=>!(await read('transport')).playing,'playback stopped again');
 }
 const phoneContext=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,storageState:await context.storageState()}),phone=await (async()=>{watches.push(await stall.watchContext(phoneContext, {serverLog: process.env.CICADA_STUDIO_STARTUP_LOG}));return phoneContext.newPage()})();
 await phone.goto(new URL('/studio?tab=arrange&region=detail&dock=automation&curvePath=guitar.amp_drive',base).href);await phone.locator('#studio-shell[data-shell-actions=ready]').waitFor();assert.equal(await phone.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await phone.locator('#detail-panel-automation').screenshot({path:path.join(proof,'amp-automation-phone.png')});await phoneContext.close();assert.deepEqual(errors,[]);
 fs.writeFileSync(path.join(proof,'browser-report.json'),JSON.stringify({checks:['track-scoped amp entry, preserved Code draft and monitoring distinction','mid-song curve seed and user-movement preservation','Keep, Undo, Redo and reload','synthetic dry WAV unchanged'],drySHA256:beforeHash,errors},null,2)+'\n');
});
