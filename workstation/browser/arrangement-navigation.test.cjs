/* View-only navigation, native null transport, and draft preservation.
 * Use a disposable fixture written by placements.test.cjs --write-fixture.
 * CICADA_STUDIO_URL, CICADA_PLAYWRIGHT_MODULE, CICADA_CHROME_PATH;
 * CICADA_ARRANGEMENT_PROOF_DIR optionally retains screenshots/report. */
'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {createRequire} = require('node:module');
const test = require('node:test');
const playwright = () => process.env.CICADA_PLAYWRIGHT_MODULE
  ? createRequire(path.resolve(process.env.CICADA_PLAYWRIGHT_MODULE, 'package.json'))('playwright') : require('playwright');
async function until(fn, message) {
  const end = Date.now()+15000;
  while (Date.now()<end) { if(await fn())return; await new Promise(r=>setTimeout(r,40)); }
  assert.fail(message);
}
test('arranging zooms, reveals and follows playback without editing the song', {timeout:120000}, async()=>{
 const base=process.env.CICADA_STUDIO_URL;
 assert.ok(base && ['127.0.0.1','localhost','[::1]'].includes(new URL(base).hostname));
 const browser=await playwright().chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox']});
 const context=await browser.newContext({viewport:{width:1440,height:1000}});
 const page=await context.newPage(); page.setDefaultTimeout(15000);
 const errors=[], posts=[], checks=[];
 page.on('pageerror',e=>errors.push(e.message));
 page.on('request',r=>{if(r.method()==='POST' && new URL(r.url()).pathname.startsWith('/__actions/'))posts.push(new URL(r.url()).pathname)});
 const state=async()=>(await page.request.get(base+'/api/state')).json();
 const transport=async()=>(await page.request.get(base+'/api/transport')).json();
 const ready=async()=>page.locator('[data-placement-navigation-ready]').waitFor();
 const scroller=page.locator('.placement-scroll');
 const width=()=>page.locator('.placement-timeline').evaluate(el=>el.getBoundingClientRect().width);
 const zoom=page.getByLabel('Timeline zoom',{exact:true});
 const follow=page.getByLabel('Follow song',{exact:true});
 try {
  await page.goto(base+'/studio?placement=far-home'); await ready();
  const before=await state(); assert.ok(before.source.includes('cicada-placement-test: disposable fixture'));
  const initial=await width();
  for(const [value,multiple] of [['96',2],['192',4],['384',8],['48',1]]){
   await zoom.selectOption(value); await until(async()=>Math.abs((await width())-(112+(initial-112)*multiple))<1,'zoom scale incorrect');
  }
  await zoom.selectOption('192');
  await page.getByRole('button',{name:'Show selected',exact:true}).click();
  await until(async()=>(await scroller.evaluate(el=>el.scrollLeft))>2500,'late placement was not revealed');
  const selected=page.locator('[data-placement-block="far-home"]');
  const bounds=await selected.boundingBox(), scrollBounds=await scroller.boundingBox();
  assert.ok(bounds.x>=scrollBounds.x+112 && bounds.x<scrollBounds.x+scrollBounds.width);
  checks.push('1×/2×/4×/8× zoom and late-placement reveal');

  await follow.check(); await page.getByRole('button',{name:'Show selected',exact:true}).click(); assert.equal(await follow.isChecked(),false);
  await page.getByRole('button',{name:'Fit song',exact:true}).click();
  await until(async()=>await zoom.inputValue()==='fit' && await scroller.evaluate(el=>el.scrollWidth<=el.clientWidth+1),'Fit song overflows');
  assert.equal((await state()).revision,before.revision); assert.equal((await state()).source,before.source);
  assert.deepEqual(posts,[]);
  await selected.locator('.placement-select').focus();
  await until(()=>page.evaluate(()=>window.__gosx.commands.available('edit.duplicate')),'view changes were counted as unsaved song work');
  checks.push('Fit song shows all bars without a score write or blocking placement commands');

  const editor=page.getByRole('textbox',{name:'Cicada score',exact:true});
  const draft='// arrangement navigation draft\n'+await editor.inputValue(); await editor.fill(draft);
  await zoom.selectOption('96'); await follow.check();
  await page.getByRole('tab',{name:'Session',exact:true}).click();
  await page.getByRole('tab',{name:'Arrange',exact:true}).click(); await ready();
  assert.equal(await zoom.inputValue(),'96'); assert.equal(await follow.isChecked(),true); assert.equal(await editor.inputValue(),draft);
  await follow.uncheck();
  await page.setViewportSize({width:390,height:844});
  await page.getByRole('link',{name:'Center',exact:true}).click();
  await page.getByRole('button',{name:'Fit song',exact:true}).click();
  await until(async()=>await scroller.evaluate(el=>el.scrollWidth<=el.clientWidth+1),'phone Fit song overflows');
  assert.ok(await zoom.isVisible()); assert.ok(await follow.isVisible());
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
  const proof=process.env.CICADA_ARRANGEMENT_PROOF_DIR;
  if(proof){fs.mkdirSync(proof,{recursive:true});await page.screenshot({path:path.join(proof,'arrangement-navigation-390.png')})}
  checks.push('draft/zoom/follow survive tabs; phone controls and Fit song remain reachable');

  await page.setViewportSize({width:1440,height:1000});
  await zoom.selectOption('384'); await follow.check();
  await page.getByRole('button',{name:'Play',exact:true}).first().click();
  const line=page.locator('[data-placement-playhead]');
  await until(async()=>Number(await line.getAttribute('data-playhead-tick'))>1400,'native playhead did not move');
  await until(async()=>(await scroller.evaluate(el=>el.scrollLeft))>0,'Follow did not scroll');
  const first=Number(await line.getAttribute('data-playhead-tick'));
  await until(async()=>Number(await line.getAttribute('data-playhead-tick'))>first+240,'playhead did not advance');
  await scroller.hover(); await page.mouse.wheel(250,0); await until(async()=>!(await follow.isChecked()),'manual timeline interaction did not release Follow');
  await page.getByRole('button',{name:'Pause',exact:true}).first().click();
  await until(async()=>!(await transport()).playing,'Pause did not settle');
  await new Promise(r=>setTimeout(r,400)); const paused=await line.getAttribute('data-playhead-tick');
  await new Promise(r=>setTimeout(r,300)); assert.equal(await line.getAttribute('data-playhead-tick'),paused);
  await page.getByRole('button',{name:'Return to start',exact:true}).first().click();
  await until(async()=>Number(await line.getAttribute('data-playhead-tick'))===0,'Home did not reset the playhead');
  checks.push('native playhead advances, Follow scrolls, manual scroll releases it, Pause and Home settle');

  await page.getByRole('button',{name:'Fit song',exact:true}).click();
  await until(async()=>(await page.locator('[data-gosx-live-bind=bar]').first().innerText())==='1' && (await page.locator('[data-gosx-live-bind=step]').first().innerText())==='1','toolbar position did not settle after Home');
  if(proof)await page.screenshot({path:path.join(proof,'arrangement-navigation-1440.png')});
  assert.equal(await editor.inputValue(),draft); assert.equal((await state()).revision,before.revision); assert.equal((await state()).source,before.source);
  assert.ok(posts.every(p=>['/__actions/transport','/__actions/source-checkpoint','/__actions/note-checkpoint'].includes(p)),'view controls issued a write: '+posts.join(','));
  assert.deepEqual(errors,[]);
  if(proof)fs.writeFileSync(path.join(proof,'arrangement-navigation.json'),JSON.stringify({checks,posts,errors,revision:before.revision},null,2)+'\n');
 }catch(error){
  console.log('navigation failure state',await page.locator('[data-placement-navigation]').evaluate(el=>({zoom:el.querySelector('[name=placementZoom]').value,selected:[...el.querySelectorAll('[data-placement-selected=true]')].map(b=>b.dataset.placementBlock),scroll:el.querySelector('.placement-scroll').scrollLeft,width:el.querySelector('.placement-timeline').getBoundingClientRect().width})));
  if(process.env.CICADA_ARRANGEMENT_PROOF_DIR)await page.screenshot({path:path.join(process.env.CICADA_ARRANGEMENT_PROOF_DIR,'failure.png')});throw error;
 }finally{await browser.close()}
});
