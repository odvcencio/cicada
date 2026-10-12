/* Run against an isolated Studio containing the disposable sampler fixture.
 * CICADA_STUDIO_URL, CICADA_PLAYWRIGHT_MODULE, CICADA_CHROME_PATH select the host.
 * CICADA_SAMPLER_PROOF_DIR retains desktop/mobile images and test evidence.
 */
'use strict';
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const test=require('node:test');
const marker='cicada-sampler-test: disposable fixture';
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function until(fn,message){const deadline=Date.now()+25000;while(Date.now()<deadline){if(await fn())return;await pause(40)}assert.fail(message)}

test('sampler imports, performs, chops visually, resamples and restores with Undo', {timeout:180000},async t=>{
 const base=process.env.CICADA_STUDIO_URL; assert.ok(base); assert.equal(new URL(base).hostname,'127.0.0.1');
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');
 const browser=await chromium.launch({executablePath:process.env.CICADA_CHROME_PATH,headless:true,args:['--no-sandbox']});
 t.after(()=>browser.close());
 const context=await browser.newContext({viewport:{width:1440,height:1000},reducedMotion:'reduce'});
 const page=await context.newPage(); page.setDefaultTimeout(20000);
 const errors=[],checks=[];page.on('pageerror',error=>errors.push(error.message));
 await page.addInitScript(()=>{const Original=window.AudioContext;window.__samplerContexts=[];window.AudioContext=new Proxy(Original,{construct(target,args){const a=Reflect.construct(target,args);window.__samplerContexts.push(a);return a}})});
 const state=async()=>(await page.request.get(base+'/api/sampler')).json();
 const score=async()=>(await page.request.get(base+'/api/state')).json();
 const ready=async()=>{await page.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();const revision=(await state()).bankRevision;await page.waitForFunction(rev=>document.querySelector('#cicada-sampler')?.dataset.samplerReady==='true'&&document.querySelector('#cicada-sampler')?.dataset.samplerBankRevision===rev,revision)};
 const editor=()=>page.locator('#sampler-pad-editor form');
 const hits=async()=>JSON.parse(await page.locator('[data-sampler-resample] input[name=performance]').inputValue());
 const selected=async n=>{await page.getByRole('link',{name:`Edit pad ${n}`,exact:true}).click();await page.waitForFunction(n=>document.querySelector('#sampler-pad-editor input[name=pad]')?.value===String(n),n);await until(async()=>await page.locator(`[data-sampler-pad="${n}"]`).evaluate(el=>el.closest('.sampler-pad').dataset.padSelected==='true'),'selected pad did not follow navigation')};
 try {
  await page.goto(base+'/?panel=sampler',{waitUntil:'networkidle'});await ready();
  const original=(await score()).source;assert.ok(original.includes(marker));assert.equal(await page.locator('[data-sampler-pad]').count(),16);
  await page.getByRole('button',{name:'Clean',exact:true}).click();await until(async()=>(await state()).bank.effects.drive===0,'initial bank did not reset to Clean');await ready();
  await page.getByRole('spinbutton',{name:'Placement slices',exact:true}).fill('4');await page.getByRole('button',{name:'Sample placement',exact:true}).click();await until(async()=>(await state()).bank.sources.some(s=>s.name==='lead'&&Math.max(...s.peaks)>.7),'Session sampling did not produce audible audio');await ready();checks.push('Session placement sampling produces nonzero PCM');
  // Stereo float WAV exercises the real upload and decoder, with audible regions.
  const frames=96000,wav=Buffer.alloc(44+frames*8);wav.write('RIFF');wav.writeUInt32LE(wav.length-8,4);wav.write('WAVEfmt ',8);wav.writeUInt32LE(16,16);wav.writeUInt16LE(3,20);wav.writeUInt16LE(2,22);wav.writeUInt32LE(48000,24);wav.writeUInt32LE(384000,28);wav.writeUInt16LE(8,32);wav.writeUInt16LE(32,34);wav.write('data',36);wav.writeUInt32LE(frames*8,40);
  for(let i=0;i<frames;i++){wav.writeFloatLE(.35*Math.sin(i*2*Math.PI*220/48000),44+i*8);wav.writeFloatLE(.2*Math.sin(i*2*Math.PI*330/48000),48+i*8)}
  await page.locator('input[type=file][name=audio]').setInputFiles({name:'Stereo sampler regression.wav',mimeType:'audio/wav',buffer:wav});
  await page.getByRole('button',{name:'Import and chop WAV',exact:true}).click();await until(async()=>(await state()).bank.pads[0].source===(await state()).bank.sources.find(s=>s.name==='Stereo sampler regression.wav')?.id,'WAV import did not save');await ready();
  let s=await state();assert.equal(s.bank.pads[15].end,frames);assert.equal(s.bank.sources.find(src=>src.id===s.bank.pads[0].source).asset.channels,2);assert.equal((await score()).source,original);checks.push('stereo upload and sixteen exact slices');
  await page.getByRole('button',{name:'Play pad 1',exact:true}).click();await until(async()=>await page.locator('[data-sampler-pad="1"]').getAttribute('data-playing')==='true','pad did not play');
  assert.ok(await page.evaluate(()=>window.__samplerContexts.some(a=>a.state==='running')));checks.push('WebAudio resumes and pad plays');
  await selected(1);await editor().locator('[name=end]').fill('48000');await editor().locator('[name=mode]').selectOption('gate');await page.getByRole('button',{name:'Save pad',exact:true}).click();await until(async()=>(await state()).bank.pads[0].mode==='gate','gate edit did not save');await ready();
  const button=page.locator('[data-sampler-pad="1"]');await button.scrollIntoViewIfNeeded();const box=await button.boundingBox();await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.down();await until(async()=>await button.getAttribute('data-playing')==='true','gate did not start');await page.mouse.up();await until(async()=>await button.getAttribute('data-playing')==='false','gate did not release');checks.push('gate releases on pointer up');
  await page.getByRole('button',{name:'Undo pad edit',exact:true}).click();await until(async()=>(await state()).bank.pads[0].mode==='oneshot','pad Undo failed');await ready();
  const beforeDrag=(await state()).bankRevision,canvas=page.locator('[data-sampler-wave]');await canvas.scrollIntoViewIfNeeded();const wave=await canvas.boundingBox();
  await page.mouse.move(wave.x+wave.width/16,wave.y+wave.height/2);await page.mouse.down();await page.mouse.move(wave.x+wave.width/10,wave.y+wave.height/2,{steps:5});await page.mouse.up();
  assert.ok(Number(await editor().locator('[name=end]').inputValue())>8000);assert.equal((await state()).bankRevision,beforeDrag);
  await page.getByRole('button',{name:'Save pad',exact:true}).click();await until(async()=>(await state()).bankRevision!==beforeDrag,'wave boundary did not save');await ready();checks.push('wave boundary draft and save preserve source');
  await page.getByRole('button',{name:'Undo pad edit',exact:true}).click();await until(async()=>(await state()).bankRevision===beforeDrag,'wave Undo did not restore bank');await ready();
  await page.locator('[name=recordBars]').fill('8');await page.getByRole('button',{name:'Record pads',exact:true}).click();await page.keyboard.press('1');await until(async()=>(await hits()).hits.length===1,'keyboard did not record');
  const cleanID=(await hits()).hits[0].sound;
  await page.getByRole('button',{name:'Dust',exact:true}).click();await until(async()=>(await state()).bank.effects.drive===9,'Dust did not save');await until(async()=>(await state()).sounds[0]!==cleanID,'effects did not change prepared sounds');await ready();
  await page.keyboard.press('Shift+1');await until(async()=>(await hits()).hits.length===2,'second hit did not record');await pause(250);await page.getByRole('button',{name:'Stop pads',exact:true}).click();
  let performance=await hits();assert.equal(performance.hits[0].sound,cleanID);assert.notEqual(performance.hits[1].sound,cleanID);assert.equal(performance.hits[1].velocity,.65);assert.equal(performance.frames,768000);checks.push('recording retains immutable sound across effect changes');
  await selected(5);assert.equal((await hits()).hits.length,2);checks.push('pad navigation retains recorded performance');
  await page.getByRole('button',{name:'Play performance',exact:true}).click();await pause(80);await page.getByRole('button',{name:'Stop pads',exact:true}).click();await pause(500);assert.equal(await page.locator('[data-sampler-pad][data-playing=true]').count(),0);checks.push('Stop cancels queued replay hits');
  const bankBeforePrint=(await state()).bankRevision;
  const resample=page.locator('[data-sampler-resample] form');await resample.locator('[name=newName]').fill('qa-print');await resample.locator('[name=at]').fill('@3.1.1');await page.getByRole('button',{name:'Resample performance',exact:true}).click();
  await until(async()=>(await score()).source.includes('place qa-print sampler qa-print-clip'),'resample did not reach Session');await ready();s=await state();assert.equal(s.bank.pads[15].bypassEffects,true);assert.equal(s.bank.performance.hits.length,2);checks.push('resample saves a bypassed pad and Session audio placement');
  if(process.env.CICADA_SAMPLER_PROOF_DIR){fs.mkdirSync(process.env.CICADA_SAMPLER_PROOF_DIR,{recursive:true});await page.screenshot({path:path.join(process.env.CICADA_SAMPLER_PROOF_DIR,'sampler-desktop.png'),fullPage:true})}
  await page.getByRole('link',{name:'Session',exact:true}).click();await page.locator('[data-placement-block=qa-print]').waitFor();
  await page.getByRole('button',{name:'Undo',exact:true}).click();await until(async()=>(await score()).source===original,'source Undo did not restore fixture');assert.equal((await state()).bankRevision,bankBeforePrint);checks.push('Session Undo restores source and pad bank together');
  await page.getByRole('link',{name:'Sampler',exact:true}).click();await ready();
  // Native forms and audio previews remain usable with browser WASM disabled.
  const native=await browser.newContext({javaScriptEnabled:false,storageState:await context.storageState()});const nojs=await native.newPage();await nojs.goto(base+'/?panel=sampler&pad=1');await nojs.getByRole('button',{name:'Clean',exact:true}).click();assert.equal((await state()).bank.effects.drive,0);assert.ok(await nojs.locator('audio[controls]').count());await native.close();checks.push('native effect form and selected-pad audio fallback');
  const mobile=await browser.newContext({viewport:{width:390,height:844},hasTouch:true,isMobile:true,storageState:await context.storageState()});const phone=await mobile.newPage();phone.on('pageerror',error=>errors.push(error.message));await phone.goto(base+'/?panel=sampler',{waitUntil:'networkidle'});await phone.locator('#cicada-sampler[data-sampler-ready=true]').waitFor();await phone.getByRole('button',{name:'Play pad 1',exact:true}).tap();
  assert.equal(await phone.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);if(process.env.CICADA_SAMPLER_PROOF_DIR)await phone.screenshot({path:path.join(process.env.CICADA_SAMPLER_PROOF_DIR,'sampler-mobile.png'),fullPage:true});await mobile.close();checks.push('touch pads and 390px layout without overflow');
  assert.deepEqual(errors,[]);if(process.env.CICADA_SAMPLER_PROOF_DIR)fs.writeFileSync(path.join(process.env.CICADA_SAMPLER_PROOF_DIR,'sampler-proof.json'),JSON.stringify({checks,errors,desktop:{width:1440,height:1000},mobile:{width:390,height:844}},null,2));console.log(checks.join('\n'));
 } catch(error){if(process.env.CICADA_SAMPLER_PROOF_DIR){await page.screenshot({path:path.join(process.env.CICADA_SAMPLER_PROOF_DIR,'sampler-failure.png'),fullPage:true});fs.writeFileSync(path.join(process.env.CICADA_SAMPLER_PROOF_DIR,'sampler-failure.html'),await page.content())}console.error(errors);throw error}
});
