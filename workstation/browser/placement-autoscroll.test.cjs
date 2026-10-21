/* Held-pointer edge scrolling against a fresh disposable Studio --audio null.
 * --write-fixture PATH isolates the far lane so timing cannot cause overlaps.
 * CICADA_STUDIO_URL, CICADA_PLAYWRIGHT_MODULE, CICADA_CHROME_PATH select tooling.
 * CICADA_AUTOSCROLL_PROOF_DIR retains measured checks and actual captures. */
'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const {createRequire}=require('node:module'),test=require('node:test');
const marker='cicada-autoscroll-test: disposable isolated far lane';
if(process.argv[2]==='--write-fixture'){
 const source=fs.readFileSync(path.join(__dirname,'../../examples/songs/tidal-glass.cicada'),'utf8');
 const following=/\n  place far-(?:open|home)\b[^{}]*\{[^{}]*\}\n/g;
 assert.equal([...source.matchAll(following)].length,2,'fixture far lane changed');
 assert.ok(process.argv[3],'supply a new disposable fixture path');
 fs.writeFileSync(process.argv[3],`// ${marker}\n${source.replace(following,'\n')}`,{flag:'wx'});process.exit(0);
}
function playwright(){
 const location=process.env.CICADA_PLAYWRIGHT_MODULE;if(!location)return require('playwright');
 try{const value=require(path.resolve(location));if(value.chromium)return value}catch(e){if(e.code!=='MODULE_NOT_FOUND')throw e}
 return createRequire(path.resolve(location))('playwright');
}
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn,message){const end=Date.now()+15000;while(Date.now()<end){if(await fn())return;await sleep(40)}assert.fail(message)}
test('held placement gestures pan, remain anchored, save once and cancel safely',{timeout:240000},async()=>{
 const base=process.env.CICADA_STUDIO_URL;assert.ok(base&&['127.0.0.1','localhost','[::1]'].includes(new URL(base).hostname));
 const browser=await playwright().chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox']});
 const context=await browser.newContext({viewport:{width:1440,height:1000},hasTouch:true});
 const page=await context.newPage();page.setDefaultTimeout(15000);
 const errors=[],posts=[],checks=[],measurements=[];const proof=process.env.CICADA_AUTOSCROLL_PROOF_DIR;
 const check=message=>{checks.push(message);console.log(message)};
 page.on('pageerror',e=>errors.push(e.message));
 page.on('request',r=>{if(r.method()==='POST'&&new URL(r.url()).pathname==='/__actions/arrange')posts.push(r)});
 const state=async()=>(await page.request.get(base+'/api/state')).json();
 const ready=()=>page.locator('[data-shell-placement-editor=ready]').waitFor();
 const block=()=>page.locator('[data-placement-block=far-in]');
 const scroller=()=>page.locator('.placement-scroll');
 const scroll=()=>scroller().evaluate(e=>e.scrollLeft);
 const status=()=>page.locator('[data-placement-edit-status]').innerText();
 const preview=async()=>{const message=await status(),length=/length (\d+)ticks/.exec(message),at=/at @(\d+)\.(\d+)\.(\d+)/.exec(message);assert.ok(length||at,message);return length?+length[1]:(+at[1]-1)*3840+(+at[2]-1)*960+(+at[3]-1)*240};
 const geometry=async()=>{const b=await block().boundingBox(),s=await scroller().boundingBox();return {left:s.x+113,right:s.x+s.width-1,y:b.y+b.height/2,top:s.y}};
 const setup=async(zoom='384')=>{await page.getByLabel('Timeline zoom',{exact:true}).selectOption(zoom);await page.getByRole('button',{name:'Show selected',exact:true}).click()};
 let baseline;
 const undo=async()=>{
  await page.getByRole('button',{name:'Undo',exact:true}).click();
  await until(async()=>(await state()).source===baseline,'one Undo did not restore the exact source');
  await until(async()=>await block().getAttribute('data-placement-at')==='16320'&&await block().getAttribute('data-placement-length')==='15360','Undo render did not settle');
  await ready();
 };
 const unchanged=async(count)=>{assert.equal((await state()).source,baseline);assert.equal(posts.length,count)};
 const begin=async(direction='right',resize=false)=>{
  await setup();
  if(resize)await block().locator('[data-placement-resize]').scrollIntoViewIfNeeded();
  const g=await geometry(),b=await block().boundingBox();
  const x=resize?b.x+b.width-8:Math.max(g.left+70,b.x+20),edge=direction==='right'?g.right-2:g.left+2;
  assert.ok(x<g.right&&x>g.left,'start is not in the visible canvas');
  const initial=await scroll(),count=posts.length;
  await page.mouse.move(x,g.y);await page.mouse.down();await page.mouse.move(edge,g.y,{steps:6});
  await until(async()=>direction==='right'?(await scroll())>=initial+128:(await scroll())<=initial-128,'stationary pointer did not pan '+direction);
  await unchanged(count);assert.equal(await scroller().getAttribute('data-placement-auto-scroll'),direction);
  return {g,edge,initial,count};
 };
 const stop=async gesture=>{
  await page.mouse.move(gesture.edge,gesture.g.top-8);
  await until(async()=>await scroller().getAttribute('data-placement-auto-scroll')===null,'leaving timeline did not stop panning');
  const stopped=await scroll();await sleep(160);assert.equal(await scroll(),stopped,'scroll continued outside timeline');
  await unchanged(gesture.count);return preview();
 };
 const saved=async(gesture,field,value)=>{
  await page.mouse.up();await until(async()=>await block().getAttribute('data-placement-'+field)===String(value),'release did not save the preview');await ready();
  assert.equal(posts.length,gesture.count+1,'release must save exactly once');await undo();
 };
 const cancelled=async(gesture,action)=>{
  await action();await page.mouse.up();await sleep(180);
  await unchanged(gesture.count);assert.equal(await block().getAttribute('data-placement-preview'),null);
  assert.equal(await scroller().getAttribute('data-placement-auto-scroll'),null);
  assert.equal(await scroll(),gesture.initial,'cancel did not restore original horizontal view');
  const stopped=await scroll();await sleep(180);assert.equal(await scroll(),stopped,'cancel left an animation running');
 };
 try{
  await page.goto(base+'/studio?placement=far-in');await ready();baseline=(await state()).source;assert.ok(baseline.includes(marker));
  let gesture=await begin();
  const anchor=async()=>block().evaluate(e=>e.getBoundingClientRect().left);
  const before=await anchor(),start=await scroll();await until(async()=>(await scroll())>start+96,'held pointer stopped panning');const after=await anchor();
  assert.ok(Math.abs(after-before)<=25,'preview slipped from stationary pointer');
  measurements.push({scenario:'right',scrollDelta:(await scroll())-gesture.initial,anchorError:Math.abs(after-before)});
  if(proof){fs.mkdirSync(proof,{recursive:true});await page.screenshot({path:path.join(proof,'placement-edge-held-1440.png')})}
  const moved=await stop(gesture);assert.ok(moved>16320);await saved(gesture,'at',moved);
  check('stationary right-edge pointer pans with anchored preview; leaving visible timeline stops; release is one write and one Undo');

  gesture=await begin('left');const earlier=await stop(gesture);assert.ok(earlier<16320);await saved(gesture,'at',earlier);
  check('left-edge panning moves earlier without losing the sticky track-label boundary');

  gesture=await begin('right',true);const longer=await stop(gesture);assert.ok(longer>15360);await saved(gesture,'length',longer);
  check('right-edge trim pans the same viewport and saves the exact preview length once');

  gesture=await begin();await cancelled(gesture,()=>page.keyboard.press('Escape'));
  check('Escape restores source, preview and the viewport from before automatic panning');

  gesture=await begin();await cancelled(gesture,()=>block().evaluate(e=>e.dispatchEvent(new PointerEvent('pointercancel',{bubbles:true,pointerId:1}))));
  check('synthetic pointer cancellation during a real held mouse gesture clears animation without writing');

  gesture=await begin();await cancelled(gesture,()=>page.evaluate(()=>window.dispatchEvent(new Event('blur'))));
  check('window blur cancels the held gesture and restores its original viewport');

  gesture=await begin();
  const limit=await scroller().evaluate(e=>e.querySelector('.placement-timeline').clientWidth-e.clientWidth);
  await until(async()=>(await scroll())===limit,'pan did not stop at the authored ruler boundary');
  await until(async()=>await scroller().getAttribute('data-placement-auto-scroll')===null,'boundary left a scrolling indicator');
  await sleep(200);assert.equal(await scroll(),limit,'preview overflow extended the authored boundary');
  await cancelled(gesture,()=>page.keyboard.press('Escape'));
  check('sustained pan stops at the authored song boundary even when the clip preview overflows');

  gesture=await begin();await page.getByRole('button',{name:'Fit song',exact:true}).evaluate(e=>e.click());await page.mouse.up();await unchanged(gesture.count);
  assert.equal(await block().getAttribute('data-placement-preview'),null);assert.equal(await scroller().getAttribute('data-placement-auto-scroll'),null);
  assert.equal(await scroll(),0);await sleep(180);assert.equal(await scroll(),0);
  check('changing the timeline view cancels safely and applies Fit song without a source write');

  // A preview may overflow an absolutely positioned lane. It must not create
  // a new authored scroll range when the song itself fits the viewport.
  let g=await geometry(),b=await block().boundingBox(),count=posts.length;
  await page.mouse.move(b.x+8,g.y);await page.mouse.down();await page.mouse.move(g.right-2,g.y,{steps:6});await sleep(450);
  assert.equal(await scroll(),0);assert.equal(await scroller().getAttribute('data-placement-auto-scroll'),null);
  await page.keyboard.press('Escape');await page.mouse.up();await unchanged(count);
  check('Fit song previews cannot self-extend the authored scroll range');

  gesture=await begin();await page.setViewportSize({width:1390,height:980});
  await until(async()=>await block().getAttribute('data-placement-preview')===null,'resize event did not cancel the gesture');
  await page.mouse.up();await unchanged(gesture.count);
  assert.equal(await block().getAttribute('data-placement-preview'),null);assert.equal(await scroller().getAttribute('data-placement-auto-scroll'),null);
  check('window resize ends the gesture without saving a placement');

  await page.setViewportSize({width:390,height:844});await page.getByRole('link',{name:'Center',exact:true}).click();await setup('192');
  await block().locator('.placement-select').scrollIntoViewIfNeeded();
  g=await geometry();b=await block().boundingBox();const x=Math.max(g.left+24,b.x+8),initial=await scroll();count=posts.length;
  const cdp=await context.newCDPSession(page);
  await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x,y:g.y}]});
  await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:g.right-2,y:g.y}]});
  await until(async()=>(await scroll())>initial+96,'touch did not pan the viewport');await unchanged(count);
  // Stay at the same X while moving above the timeline to stop the animation.
  await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:g.right-2,y:g.top-8}]});
  const phonePreview=await preview();await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
  await until(async()=>await block().getAttribute('data-placement-at')===String(phonePreview),'touch release lost its preview');await ready();assert.equal(posts.length,count+1);await undo();await cdp.detach();
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
  if(proof)await page.screenshot({path:path.join(proof,'placement-edge-390.png')});
  check('trusted browser touch pans and commits once at phone width, with exact Undo and no page overflow');

  assert.deepEqual(errors,[]);assert.equal((await state()).source,baseline);
  if(proof)fs.writeFileSync(path.join(proof,'placement-autoscroll.json'),JSON.stringify({checks,measurements,arrangePosts:posts.length,errors},null,2)+'\n');
 }catch(error){if(proof){fs.mkdirSync(proof,{recursive:true});await page.screenshot({path:path.join(proof,'failure.png')});fs.writeFileSync(path.join(proof,'failure.json'),JSON.stringify({error:error.message,checks,errors,scroll:await scroll(),status:await status()},null,2)+'\n')}throw error}
 finally{await browser.close()}
});
