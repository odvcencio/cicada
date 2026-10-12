/* Run against a fresh --audio null Studio with the marked disposable score.
 * Only the capture status response is simulated. No microphone is opened. */
'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
async function until(fn,message){const end=Date.now()+15000;while(Date.now()<end){if(await fn())return;await new Promise(r=>setTimeout(r,50))}assert.fail(message)}
test('Microphone levels and recording transport protection remain usable across panels',{timeout:90000},async t=>{
 const base=process.env.CICADA_LEVELS_URL;assert.ok(base);assert.equal(new URL(base).hostname,'127.0.0.1');
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');
 const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox']});t.after(()=>browser.close());
 const context=await browser.newContext({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),page=await context.newPage(),errors=[],checks=[];
 page.on('pageerror',e=>errors.push(e.message));
 const ready=async()=>{await page.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();await page.locator('#cicada-workspace[aria-busy=true]').waitFor({state:'hidden'})};
 await page.goto(base+'/?panel=session',{waitUntil:'domcontentloaded'});await ready();
 const original=await(await page.request.get(base+'/api/state')).json();assert.ok(original.source.includes('cicada-pro-recording-test: disposable fixture'));
 await until(async()=>(await page.locator('[data-input-status]').innerText())==='Microphone off','disabled input appeared live');
 const initial=await(await page.request.get(base+'/api/capture')).json();assert.equal(initial.input.enabled,false);assert.equal(initial.activeCapture,'');
 let simulated={...initial,input:{enabled:true,active:true,channels:1,peakL:.25,peakR:.25,heldPeakL:.25,heldPeakR:.25,clipped:false,session:1}};
 await page.route('**/api/capture',route=>route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(simulated)}));
 const meter=page.getByRole('meter',{name:'Microphone L level',exact:true});
 await until(async()=>(await meter.getAttribute('aria-valuenow'))==='-12.0','input level did not update');assert.equal(await page.locator('[data-input-channel=R]').isVisible(),false);assert.ok((await page.locator('[data-input-peak]').innerText()).includes('-12.0'));
 const firstWidth=await page.locator('[data-input-channel=L] [data-input-fill]').evaluate(e=>e.style.width);
 simulated.input={...simulated.input,channels:2,peakL:.5,peakR:.125,heldPeakL:.5};await until(async()=>(await meter.getAttribute('aria-valuenow'))==='-6.0','moving level did not update');assert.equal(await page.locator('[data-input-channel=R]').isVisible(),true);assert.notEqual(await page.locator('[data-input-channel=L] [data-input-fill]').evaluate(e=>e.style.width),firstWidth);checks.push('live mono/stereo levels move, use dBFS, and expose accessible values');
 simulated.input={...simulated.input,peakL:.02,heldPeakL:.999969,clipped:true};await until(async()=>(await page.locator('[data-input-status]').innerText())==='Clipped input','short overload did not stay visible');assert.ok((await page.locator('[data-input-hint]').innerText()).includes('microphone volume'));checks.push('a held overload stays visible after the current level falls');
 simulated={...simulated,activeCapture:'simulated-vocal',sampleRate:48000,capture:{recording:true,writtenFrames:96000,countInRemainingFrames:0}};
 await until(async()=>await page.getByRole('button',{name:'Play',exact:true}).first().isDisabled(),'recording did not lock Play');
 for(const name of ['Pause','Return to start','Play full song'])assert.equal(await page.getByRole('button',{name,exact:true}).first().isDisabled(),true,name);
 assert.equal(await page.getByRole('button',{name:'Stop & save take',exact:true}).first().isEnabled(),true);assert.equal(await page.getByLabel('Punch in',{exact:true}).isDisabled(),true);assert.equal(await page.getByLabel('Vocal track',{exact:true}).isDisabled(),true);assert.equal(await page.locator('[data-transport-capture-hint]').isVisible(),true);
 // Setup reflects the actual null backend; collapse it for the simulated-input proof.
 await page.locator('.vocal-setup').evaluate(e=>e.open=false);
 const proof=process.env.CICADA_LEVELS_PROOF_DIR;if(proof){fs.mkdirSync(proof,{recursive:true});await page.locator('#vocals').screenshot({path:path.join(proof,'recording-levels-1440.png')});await page.locator('.toolbar').screenshot({path:path.join(proof,'recording-transport-1440.png')});await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.locator('#vocals').screenshot({path:path.join(proof,'recording-levels-390.png')});await page.setViewportSize({width:1440,height:1100})}
 await page.getByRole('link',{name:'Mixer',exact:true}).click();await ready();await until(async()=>await page.getByRole('button',{name:'Play',exact:true}).first().isDisabled(),'recording truth was lost on Mixer');assert.equal(await page.getByRole('button',{name:'Stop & save take',exact:true}).first().isEnabled(),true);checks.push('recording disables conflicting transport actions across panels and keeps Stop available');
 await page.getByRole('button',{name:'Stop & save take',exact:true}).first().click();await ready();
 simulated={...simulated,activeCapture:'',capture:null,input:{...simulated.input,active:false}};
 await until(async()=>await page.getByRole('button',{name:'Play',exact:true}).first().isEnabled(),'transport did not unlock after capture ended');
 await page.getByRole('link',{name:'Session',exact:true}).click();await ready();await until(async()=>(await page.locator('[data-input-status]').innerText())==='Clipped input','stopping hid the overload');assert.equal(await page.getByLabel('Punch in',{exact:true}).isEnabled(),true);checks.push('finished capture restores controls while retaining its peak warning');
 await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'mobile page overflow');

 simulated={...simulated,input:{enabled:true,active:true,channels:1,peakL:.125,peakR:.125,heldPeakL:.125,heldPeakR:.125,clipped:false,session:2}};
 await until(async()=>(await page.locator('[data-input-status]').innerText())==='Input active','fresh pass kept the old overload');assert.ok((await page.locator('[data-input-peak]').innerText()).includes('-18.1'));checks.push('a new metering session clears the old overload and the phone layout fits');
 await page.unroute('**/api/capture');await until(async()=>(await page.locator('[data-input-status]').innerText())==='Microphone off','native microphone state was not restored');
 const final=await(await page.request.get(base+'/api/state')).json(),audio=await(await page.request.get(base+'/api/audio/config')).json();assert.equal(final.source,original.source);assert.equal(audio.options.inputEnabled,false);assert.deepEqual(errors,[]);
 if(proof)fs.writeFileSync(path.join(proof,'checks.json'),JSON.stringify({checks,errors,simulatedInput:true,microphoneActivated:false,viewports:[[1440,1100],[390,844]]},null,2));console.log(checks.join('\n'));
});
