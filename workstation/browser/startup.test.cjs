/* Fresh disposable Studio; verify real loading/failure and drafts entered before
 * its engine is available. CICADA_STARTUP_PROOF_DIR retains captures and checks. */
'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto');
const {createRequire}=require('node:module'),test=require('node:test');
function playwright(){const p=process.env.CICADA_PLAYWRIGHT_MODULE;return p?createRequire(path.resolve(p,'package.json'))(p):require('playwright')}
async function until(fn,label){const end=Date.now()+20000;while(Date.now()<end){if(await fn())return;await new Promise(r=>setTimeout(r,40))}assert.fail(label)}
test('Studio startup explains loading and failures while retaining early drafts',{timeout:120000},async()=>{
 const base=process.env.CICADA_STUDIO_URL;assert.ok(base&&['127.0.0.1','localhost','[::1]'].includes(new URL(base).hostname));
 const browser=await playwright().chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox']});
 const page=await browser.newPage({viewport:{width:1440,height:1000},reducedMotion:'reduce'});page.setDefaultTimeout(20000);
 const checks=[],errors=[],posts=[];const proof=process.env.CICADA_STARTUP_PROOF_DIR;
 const check=s=>{checks.push(s);console.log(s)};
 page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(r.method()==='POST'&&/^\/__actions\/(source|arrange|pattern|clip)$/.test(new URL(r.url()).pathname))posts.push(r)});
 const state=async()=>(await page.request.get(base+'/api/state')).json();
 const root=()=>page.locator('#studio-shell'),status=()=>page.locator('[data-studio-startup-status]'),editor=()=>page.getByRole('textbox',{name:'Cicada score',exact:true});
 let release;const gate=new Promise(r=>release=r);let mode='hold';
 await page.route('**/engines/cicada-meters.*.wasm',async route=>{if(mode==='hold')await gate;if(mode==='error')await route.fulfill({status:503,body:'Intentional disposable startup failure'});else await route.continue()});
 try{
  await page.goto(base+'/studio?placement=far-in');await page.locator('.editor-page.editor-highlight-ready').waitFor();await until(async()=>await root().getAttribute('data-studio-startup')==='loading','loading status');await status().waitFor();
  const baseline=await state();assert.ok(baseline.source.includes('cicada-placement-test: disposable fixture'));const clean=await editor().inputValue(),draft=clean+'\n// vocal idea typed before Studio controls loaded\n';await editor().fill(draft);
  assert.equal(await editor().getAttribute('data-edited'),'true');assert.equal((await state()).source,baseline.source);assert.equal(posts.length,0);
  if(proof){fs.mkdirSync(proof,{recursive:true});await page.screenshot({path:path.join(proof,'startup-loading-1440.png')})}
  check('real delayed engine fetch shows startup feedback; Code remains editable and an early draft is marked without source writes');
  await until(async()=>await root().getAttribute('data-studio-startup')==='slow','slow loading recovery');await page.getByRole('button',{name:'Reload Studio',exact:true}).click();assert.equal(await editor().inputValue(),draft);assert.match(await status().innerText(),/unsaved edits/);assert.equal((await state()).revision,baseline.revision);
  check('slow startup offers Reload Studio, which protects the exact early draft and saved revision');
  mode='normal';release();await until(async()=>await root().getAttribute('data-studio-startup')==='ready','Studio controls ready');assert.equal(await status().isVisible(),false);assert.equal(await editor().inputValue(),draft);
  const clip=()=>page.locator('[data-placement-block=far-in]');await clip().locator('.placement-select').focus();await page.keyboard.press('ArrowRight');assert.equal(posts.length,0);assert.equal((await state()).source,baseline.source);assert.match(await page.locator('[data-placement-edit-status]').innerText(),/unsaved/);
  check('late engine mount retains the early draft and its Arrange write guard; ready state clears the banner');
  await editor().fill(clean);await clip().locator('.placement-select').focus();await page.keyboard.press('ArrowRight');await until(async()=>await clip().getAttribute('data-placement-at')==='16560','normal arranging after draft reset');await page.getByRole('button',{name:'Undo',exact:true}).click();await until(async()=>(await state()).source===baseline.source,'one exact Undo');
  check('reverting the early draft restores normal arranging, one-save nudging and exact Undo');
  mode='error';await page.reload();await page.locator('.editor-page.editor-highlight-ready').waitFor();await until(async()=>await root().getAttribute('data-studio-startup')==='error','actual engine failure visible');assert.match(await status().innerText(),/could not open/);assert.equal((await state()).source,baseline.source);
  if(proof)await page.screenshot({path:path.join(proof,'startup-error-1440.png')});
  const failedDraft=clean+'\n// keep this during retry\n';await editor().fill(failedDraft);await page.getByRole('button',{name:'Reload Studio',exact:true}).click();assert.equal(await editor().inputValue(),failedDraft);assert.match(await status().innerText(),/unsaved edits/);
  await page.setViewportSize({width:390,height:844});if(proof)await page.screenshot({path:path.join(proof,'startup-error-390.png')});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
  check('real HTTP503 engine failure is actionable on desktop and phone; reload keeps unsaved Code and no page overflow');
  await page.getByRole('link',{name:'Code',exact:true}).click();await editor().fill(clean);mode='normal';await Promise.all([page.waitForEvent('framenavigated'),page.getByRole('button',{name:'Reload Studio',exact:true}).click()]);await until(async()=>await root().getAttribute('data-studio-startup')==='ready','clean retry recovered Studio');assert.equal((await state()).source,baseline.source);assert.equal((await state()).revision,baseline.revision);assert.deepEqual(errors,[]);
  check('clean reload fetches the real engine again and restores Studio with exact source/revision and no helper errors');
  if(proof)fs.writeFileSync(path.join(proof,'report.json'),JSON.stringify({checks,errors,sourceSHA256:crypto.createHash('sha256').update(baseline.source).digest('hex'),finalRevision:(await state()).revision,browser:browser.version(),physicalAudioVerified:false},null,2));
 }catch(error){release();if(proof){fs.mkdirSync(proof,{recursive:true});fs.writeFileSync(path.join(proof,'failure.json'),JSON.stringify({error:String(error),checks,errors},null,2));await page.screenshot({path:path.join(proof,'failure.png')})}throw error}finally{release();await browser.close()}
});
