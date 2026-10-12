'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');

test('Native64-track meters and recording buffers remain usable on desktop and phone',{timeout:120000},async t=>{
 const capacity=process.env.CICADA_CAPACITY_URL,vocal=process.env.CICADA_BUFFER_URL,proof=process.env.CICADA_PERFORMANCE_PROOF_DIR;
 for(const base of [capacity,vocal]){assert.ok(base);assert.equal(new URL(base).hostname,'127.0.0.1')}
 assert.ok(proof&&path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');
 const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox']});t.after(()=>browser.close());
 const errors=[],desktop=await browser.newContext({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),page=await desktop.newPage();
 page.on('pageerror',e=>errors.push(e.message));
 const ready=async p=>{await p.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();await p.locator('#cicada-workspace[aria-busy=true]').waitFor({state:'hidden'})};
 await page.goto(capacity+'/?panel=mixer',{waitUntil:'domcontentloaded'});await ready(page);
 const canvas=page.locator('#cicada-meters canvas[data-meter-track-count="64"]');await page.locator('#cicada-meters canvas').waitFor();
 await page.getByRole('button',{name:'Play',exact:true}).click();await canvas.waitFor();assert.equal(await canvas.getAttribute('height'),'560');assert.equal(await canvas.getAttribute('data-meter-columns'),'17');assert.match(await canvas.getAttribute('aria-label'),/64/);
 await page.waitForTimeout(900);
 const transport=await(await page.request.get(capacity+'/api/transport')).json();assert.equal(transport.playing,true);assert.match(transport.activeBackend,/Null/);assert.ok(!transport.error);
 const meters=await(await page.request.get(capacity+'/api/meters')).json();assert.equal(Object.keys(meters.tracks).length,64);assert.ok(Object.values(meters.tracks).some(x=>x.peak>-80));
 await page.screenshot({path:path.join(proof,'native-64-tracks-1440.png')});
 const state=await desktop.storageState();const phone=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,reducedMotion:'reduce',storageState:state}),mobile=await phone.newPage();
 mobile.on('pageerror',e=>errors.push(e.message));await mobile.goto(capacity+'/?panel=mixer',{waitUntil:'domcontentloaded'});await ready(mobile);const phoneCanvas=mobile.locator('#cicada-meters canvas[data-meter-track-count="64"]');await phoneCanvas.waitFor();const layout=await phoneCanvas.evaluate(c=>({columns:Number(c.dataset.meterColumns),width:c.width,css:c.getBoundingClientRect().width,height:c.height}));assert.ok(layout.columns<=6);assert.ok(layout.width/layout.columns>=64);assert.ok(Math.abs(layout.width-layout.css)<1);assert.equal(layout.height,Math.ceil(65/layout.columns)*140);assert.equal(await mobile.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
 await mobile.locator('#cicada-meters').screenshot({path:path.join(proof,'native-64-tracks-390.png')});
 await page.getByRole('button',{name:'Stop',exact:true}).click();
 const audio=require('./audio-performance.cjs');await audio.checkAudioPerformance({browser,base:vocal,proof,ready,errors});
 assert.deepEqual(errors,[]);fs.writeFileSync(path.join(proof,'browser-proof.json'),JSON.stringify({nativeTracks:64,allMetersVisible:true,mobileOverflow:false,bufferChoices:[64,128,256,512,1024],microphoneActivated:false,errors},null,2)+'\n');
});
