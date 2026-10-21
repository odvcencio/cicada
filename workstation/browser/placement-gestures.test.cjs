/* Studio drag/trim/nudge integration using a fresh disposable placement score.
 * Create it with placements.test.cjs --write-fixture; start Studio --audio null.
 * CICADA_STUDIO_URL, CICADA_PLAYWRIGHT_MODULE, CICADA_CHROME_PATH.
 * CICADA_GESTURE_SCORE enables the concurrent on-disk edit scenario.
 * CICADA_GESTURE_PROOF_DIR retains ordered checks and desktop/phone captures. */
'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const {createRequire}=require('node:module'),test=require('node:test');
function playwright(){
 const location=process.env.CICADA_PLAYWRIGHT_MODULE;if(!location)return require('playwright');
 try{const value=require(path.resolve(location));if(value.chromium)return value}catch(e){if(e.code!=='MODULE_NOT_FOUND')throw e}
 return createRequire(path.resolve(location))('playwright');
}
async function until(fn,message){const end=Date.now()+15000;while(Date.now()<end){if(await fn())return;await new Promise(r=>setTimeout(r,40))}assert.fail(message)}
test('Studio placement gestures save once, cancel cleanly and retain the working viewport',{timeout:180000},async()=>{
 const base=process.env.CICADA_STUDIO_URL;assert.ok(base&&['127.0.0.1','localhost','[::1]'].includes(new URL(base).hostname));
 const browser=await playwright().chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox']});
 const context=await browser.newContext({viewport:{width:1440,height:1000},hasTouch:true});
 const page=await context.newPage();page.setDefaultTimeout(15000);
 const errors=[],posts=[],responses=[],checks=[];const proof=process.env.CICADA_GESTURE_PROOF_DIR;
 const check=message=>{checks.push(message);console.log(message)};
 page.on('pageerror',e=>errors.push(e.message));
 page.on('request',r=>{if(r.method()==='POST'&&new URL(r.url()).pathname.startsWith('/__actions/'))posts.push(r)});
 page.on('response',r=>{if(new URL(r.url()).pathname==='/__actions/arrange')responses.push(r.status())});
 const state=async()=>(await page.request.get(base+'/api/state')).json();
 const ready=()=>page.locator('[data-shell-placement-editor=ready]').waitFor();
 const block=()=>page.locator('[data-placement-block=far-in]');
 const at=async(value)=>{await until(async()=>await block().getAttribute('data-placement-at')===String(value),'placement start did not become '+value);await ready()};
 const length=async(value)=>{await until(async()=>await block().getAttribute('data-placement-length')===String(value),'placement length did not become '+value);await ready()};
 const arrangePosts=()=>posts.filter(r=>new URL(r.url()).pathname==='/__actions/arrange').length;
 const gesture=async({resize=false,dx=12,cancel=false,hold=false}={})=>{
  const control=resize?block().locator('[data-placement-resize]'):block().locator('.placement-select');
  await control.scrollIntoViewIfNeeded();const b=await control.boundingBox(),s=await page.locator('.placement-scroll').boundingBox();
  const x=resize?b.x+b.width/2:Math.max(s.x+140,Math.min(b.x+b.width/2,s.x+s.width-32)),y=b.y+b.height/2;
  await page.mouse.move(x,y);await page.mouse.down();await page.mouse.move(x+dx,y,{steps:6});
  if(cancel)await page.keyboard.press('Escape');if(!hold)await page.mouse.up();
 };
 try{
  await page.goto(base+'/studio?placement=far-in');await ready();
  let baseline=(await state()).source;assert.ok(baseline.includes('cicada-placement-test: disposable fixture'));
  assert.equal(await page.locator('#cicada-workspace').count(),0,'Studio mounted the legacy polling workspace');
  const undo=async()=>{await page.getByRole('button',{name:'Undo',exact:true}).click();await until(async()=>(await state()).source===baseline,'one Undo did not restore the exact source');await at(16320);await length(15360)};
  const once=async(action,verify)=>{const n=arrangePosts();await action();await verify();assert.equal(arrangePosts(),n+1,'gesture submitted more than once');await undo()};

  await block().locator('.placement-select').focus();await once(()=>page.keyboard.press('ArrowRight'),()=>at(16560));
  await once(async()=>{await block().locator('.placement-select').focus();await page.keyboard.press('Shift+ArrowRight')},()=>at(20160));
  await once(async()=>{await block().locator('[data-placement-resize]').focus();await page.keyboard.press('ArrowLeft')},()=>length(15120));
  check('keyboard move/trim in sixteenths and whole bars; each is one write and one exact Undo');

  const focused=async resize=>until(()=>block().locator(resize?'[data-placement-resize]':'.placement-select').evaluate(e=>e===document.activeElement),'saved gesture did not retain placement focus');
  let repeated=arrangePosts();await block().locator('.placement-select').focus();
  await page.keyboard.press('ArrowRight');await at(16560);await focused(false);
  await page.keyboard.press('ArrowRight');await at(16800);await focused(false);assert.equal(arrangePosts(),repeated+2);
  await page.getByRole('button',{name:'Undo',exact:true}).click();await at(16560);await undo();
  repeated=arrangePosts();await block().locator('[data-placement-resize]').focus();
  await page.keyboard.press('ArrowLeft');await length(15120);await focused(true);
  await page.keyboard.press('ArrowLeft');await length(14880);await focused(true);assert.equal(arrangePosts(),repeated+2);
  await page.getByRole('button',{name:'Undo',exact:true}).click();await length(15120);await undo();
  check('successive keyboard moves and trims retain their focused control and individual Undo steps');

  const n=arrangePosts();await page.getByLabel('Follow song',{exact:true}).check();await gesture({hold:true});
  assert.equal(await page.getByLabel('Follow song',{exact:true}).isChecked(),false);
  assert.equal(await block().getAttribute('data-placement-preview'),'at');assert.equal((await state()).source,baseline);assert.equal(arrangePosts(),n);
  if(proof){fs.mkdirSync(proof,{recursive:true});await page.screenshot({path:path.join(proof,'placement-drag-preview-1440.png')})}
  await page.mouse.up();await at(17280);assert.equal(arrangePosts(),n+1);await undo();
  await once(()=>gesture({resize:true,dx:-12}),()=>length(14400));
  check('real pointer move/trim previews do not write; release commits once and suspends Follow');

  await page.getByLabel('Timeline zoom',{exact:true}).selectOption('192');await page.getByRole('button',{name:'Show selected',exact:true}).click();
  await once(()=>gesture({dx:48}),()=>at(17280));
  await until(async()=>await page.getByLabel('Timeline zoom',{exact:true}).inputValue()==='192','save/Undo reset zoom');
  assert.ok(await page.locator('.placement-scroll').evaluate(e=>e.scrollLeft)>0,'save/Undo lost horizontal position');
  check('4× drag saves one beat; zoom and scroll survive the save and Undo');
  await page.getByLabel('Timeline zoom',{exact:true}).selectOption('48');

  let count=arrangePosts();await gesture({cancel:true});await new Promise(r=>setTimeout(r,100));
  assert.equal((await state()).source,baseline);assert.equal(arrangePosts(),count);await at(16320);
  await gesture({dx:2});await new Promise(r=>setTimeout(r,100));assert.equal(arrangePosts(),count);assert.equal((await state()).source,baseline);
  check('Escape and pointer jitter leave the source and Undo history untouched');

  const editor=page.getByRole('textbox',{name:'Cicada score',exact:true}),clean=await editor.inputValue();
  const draft='// keep this vocal idea\n'+clean;await editor.fill(draft);count=arrangePosts();
  await gesture();assert.equal(arrangePosts(),count);assert.equal(await editor.inputValue(),draft);assert.equal((await state()).source,baseline);
  await until(async()=>(await page.locator('[data-placement-edit-status]').innerText()).includes('unsaved'),'dirty draft did not explain blocked drag');
  await editor.fill(clean);
  check('unsaved Code prevents arranging writes and retains the exact draft');

  // Delay a real action response to verify admission before another gesture.
  let release,entered;const gate=new Promise(r=>release=r),seen=new Promise(r=>entered=r);
  await page.route('**/__actions/arrange',async route=>{entered();await gate;await route.continue()});
  await gesture();await seen;count=arrangePosts();
  await block().locator('.placement-select').focus();await page.keyboard.press('ArrowRight');assert.equal(arrangePosts(),count,'a second action escaped pending admission');
  await editor.focus();assert.ok(await page.evaluate(()=>!!document.activeElement?.closest('#studio-code')));
  release();await at(17280);await page.unroute('**/__actions/arrange');await ready();
  // Managed navigation can focus MAIN; a completed gesture must not reclaim
  // its clip after the user deliberately moved focus into another surface.
  assert.equal(await block().locator('.placement-select').evaluate(e=>e===document.activeElement),false,'returning gesture reclaimed placement focus');await undo();
  check('a real pending action blocks racing nudges and cancels gesture focus return when focus moves elsewhere');

  await page.setViewportSize({width:390,height:844});await page.getByRole('link',{name:'Center',exact:true}).click();
  await page.getByLabel('Timeline zoom',{exact:true}).selectOption('48');await page.getByRole('button',{name:'Show selected',exact:true}).click();
  await block().locator('.placement-select').scrollIntoViewIfNeeded();const box=await block().locator('.placement-select').boundingBox(),scroll=await page.locator('.placement-scroll').boundingBox();
  const x=Math.max(scroll.x+140,Math.min(box.x+box.width*.3,scroll.x+scroll.width-24)),y=box.y+box.height/2;
  const cdp=await context.newCDPSession(page);await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x,y}]});
  await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:x+12,y}]});
  assert.equal((await state()).source,baseline);await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
  await at(17280);await undo();await cdp.detach();
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
  if(proof)await page.screenshot({path:path.join(proof,'placement-gestures-390.png')});
  check('trusted browser touch drag saves one beat at phone width, with no page overflow');

  await page.setViewportSize({width:1440,height:1000});
  // Move across the following pattern: native validation must reject it.
  count=arrangePosts();await gesture({dx:600});await until(()=>responses.includes(422),'overlap did not reach native validation');
  assert.equal((await state()).source,baseline);assert.equal(arrangePosts(),count+1);
  await block().locator('.placement-select').focus();await page.keyboard.press('ArrowLeft');await at(16080);await length(15360);await undo();
  await page.reload();await ready();
  check('overlap rejection preserves source; a corrected nudge works without reload and keeps canonical length');

  const score=process.env.CICADA_GESTURE_SCORE;
  if(score){
   assert.equal(fs.readFileSync(score,'utf8'),baseline,'concurrent edit target is not this disposable score');
   const external=baseline.replace(/(place far-in far ripple\s*\{[^}]*length\s*=\s*)4bars/,(_,prefix)=>prefix+'3bars')+'\n// external trim kept\n';
   assert.notEqual(external,baseline);fs.writeFileSync(score,external);await until(async()=>(await state()).source===external,'external source was not loaded');
   const before=responses.length;await block().locator('.placement-select').focus();await page.keyboard.press('ArrowRight');
   await until(()=>responses.slice(before).includes(409),'stale gesture was not rejected');assert.equal((await state()).source,external);
   await page.reload();await ready();await length(11520);
   await block().locator('.placement-select').focus();await page.keyboard.press('ArrowRight');await at(16560);await length(11520);
   await page.getByRole('button',{name:'Undo',exact:true}).click();await until(async()=>(await state()).source===external,'Undo failed to preserve the external trim/comment');await at(16320);
   baseline=external;check('stale gesture refuses overwrite; refreshed nudge and Undo preserve external length/comment');
  }
  assert.equal((await state()).source,baseline);assert.deepEqual(errors,[]);
  if(proof){await page.screenshot({path:path.join(proof,'placement-gestures-1440.png')});fs.writeFileSync(path.join(proof,'placement-gestures.json'),JSON.stringify({checks,responses,arrangePosts:arrangePosts(),errors},null,2)+'\n')}
 }catch(error){if(proof){fs.mkdirSync(proof,{recursive:true});await page.screenshot({path:path.join(proof,'failure.png')});fs.writeFileSync(path.join(proof,'failure.json'),JSON.stringify({error:error.message,checks,errors,active:await page.evaluate(()=>({tag:document.activeElement?.tagName,id:document.activeElement?.id,classes:document.activeElement?.className}))},null,2)+'\n')}throw error}
 finally{await browser.close()}
});
