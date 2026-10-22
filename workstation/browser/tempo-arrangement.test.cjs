// Packaged tempo conversion, recording clocks and musical waveform geometry.
'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
test('converted tempo steps, recorded clocks and selection durations survive the real Studio interface',{timeout:90000},async t=>{
 const fixture=JSON.parse(fs.readFileSync(process.env.CICADA_PRINT_FIXTURE,'utf8'));assert.equal(fixture.sceneTempo,true);assert.ok(fixture.score.startsWith('/tmp/'));
 const proof=process.env.CICADA_PRINT_PROOF_DIR;assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const startup=new URL(process.env.CICADA_STUDIO_URL);assert.equal(startup.hostname,'127.0.0.1');
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']});t.after(()=>browser.close());
 const context=await browser.newContext({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(startup.href);await page.goto(startup.origin+'/?panel=session');await page.locator('#cicada-vocal-lanes[data-vocal-lanes-ready=true]').waitFor();
 const kernel=fs.readFileSync(process.env.CICADA_KERNEL_WASM||path.resolve(__dirname,'../../build/cicada-kernel.wasm'));await page.evaluate(async bytes=>{await WebAssembly.compile(new Uint8Array(bytes))},Array.from(kernel));
 const read=async route=>{const r=await page.request.get(startup.origin+'/api/'+route);assert.equal(r.status(),200);return r.json()};
 const state=await read('state'),takes=await read('takes'),config=await read('audio/config');assert.equal(state.valid,true);assert.equal(config.options.inputEnabled,false);assert.equal(String(config.inventory.backendName||config.inventory.backend).toLowerCase(),'null');
 const steps=[{bar:0,bpm_milli:137333},{bar:2,bpm_milli:96000},{bar:4,bpm_milli:137333},{bar:6,bpm_milli:120000},{bar:7,bpm_milli:137333}],source=fs.readFileSync(fixture.arrangement,'utf8');for(const step of steps)assert.ok(source.includes(`tempo @${step.bar+1}.1.1 ${step.bpm_milli/1000}`));
 for(const take of takes.takes)assert.deepEqual(take.timeline.tempoChanges,steps);
 assert.deepEqual(takes.comps[0].tempoChanges,steps);
 const details=page.locator('[data-arrangement-tempos]');await details.locator('summary').click();assert.match(await details.innerText(),/Tempo steps · 5/);for(const step of steps)assert.ok((await details.innerText()).includes(`@${step.bar+1}.1.1 · ${step.bpm_milli/1000} BPM`));
 await details.screenshot({path:path.join(proof,'tempo-desktop.png')});
 const first=takes.takes.find(take=>take.timeline.startTick===0);await page.locator(`[data-vocal-take="${first.id}"] [data-vocal-pick]`).click();await page.locator('[data-vocal-readout]').filter({hasText:'15.74 s'}).waitFor();
 await page.locator('[data-waveform-state=ready]').nth(2).waitFor();assert.equal(await page.locator('[data-waveform-state=error]').count(),0);
 await page.locator(`[data-vocal-take="${first.id}"]`).screenshot({path:path.join(proof,'tempo-waveform.png')});
 const phoneContext=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,storageState:await context.storageState()}),phone=await phoneContext.newPage();await phone.goto(startup.origin+'/?panel=session');await phone.locator('[data-arrangement-tempos] summary').click();assert.equal(await phone.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await phone.locator('[data-arrangement-tempos]').screenshot({path:path.join(proof,'tempo-mobile.png')});await phoneContext.close();
 assert.deepEqual(errors,[]);fs.writeFileSync(path.join(proof,'tempo-report.json'),JSON.stringify({steps,selectionSeconds:'15.74',checks:['portable kernel compiles in Chrome','all five scene tempos carried to arrangement','raw take and comp clocks retained','musical selection duration uses changing tempo','all three waveforms load','desktop and phone tempo steps remain legible'],errors},null,2)+'\n');
});
