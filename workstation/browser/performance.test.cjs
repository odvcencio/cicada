'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const stall=require('./page-stall.cjs');
// --write-fixture PATH writes the disposable score this suite needs: 64 tracks (63 synths and the vocal track "vox"), silent null audio.
// One Studio serves both checks, so CICADA_CAPACITY_URL and CICADA_BUFFER_URL can be the same address.
if(process.argv[2]==='--write-fixture'){const target=path.resolve(process.argv[3]);fs.mkdirSync(path.dirname(target),{recursive:true});const synths=Array.from({length:63},(_,i)=>'track s'+String(i).padStart(2,'0')+' acid {}');fs.writeFileSync(target,['cicada 2','// cicada-performance-test: disposable fixture','title "Sixty-four tracks"','tempo 120','seed 42','track vox audio {}',...synths,'pattern pulse notes steps=16 { c4 c4 c4 c4 c4 c4 c4 c4 c4 c4 c4 c4 c4 c4 c4 c4 }','scene main { '+synths.map((_,i)=>'s'+String(i).padStart(2,'0')+'=pulse').join(' ')+' }','song { main*8 }',''].join('\n'));process.exit(0)}

test('Native64-track meters and recording buffers remain usable on desktop and phone',{timeout:120000},async t=>{
 const capacity=process.env.CICADA_CAPACITY_URL,vocal=process.env.CICADA_BUFFER_URL,proof=process.env.CICADA_PERFORMANCE_PROOF_DIR;
 // CICADA_PERFORMANCE_PART=capacity or buffer runs one half on its own Studio: playback leaves the audio runtime armed, which locks the audio form. Unset runs both halves in order, on two Studios (CICADA_CAPACITY_URL and CICADA_BUFFER_URL).
 const part=process.env.CICADA_PERFORMANCE_PART||'both';assert.ok(['both','capacity','buffer'].includes(part));
 for(const [base,used] of [[capacity,part!=='buffer'],[vocal,part!=='capacity']])if(used){assert.ok(base);assert.equal(new URL(base).hostname,'127.0.0.1')}
 assert.ok(proof&&path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');
 const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox']});const watches=[];stall.teardown(t, browser, {watch: watches, exitWhenDone: true});
 const errors=[],desktop=await browser.newContext({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),page=await (async()=>{watches.push(await stall.watchContext(desktop, {serverLog: process.env.CICADA_STUDIO_STARTUP_LOG}));return desktop.newPage()})();
 page.on('pageerror',e=>errors.push(e.message));
 const ready=async p=>{await p.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();await p.locator('#cicada-workspace[aria-busy=true]').waitFor({state:'hidden'})};
 if(part!=='buffer'){
 await page.goto(capacity+'/?panel=mixer',{waitUntil:'domcontentloaded'});await ready(page);
 const canvas=page.locator('#cicada-meters canvas[data-meter-track-count="64"]');await page.locator('#cicada-meters canvas').waitFor();
 await page.getByRole('button',{name:'Play',exact:true}).click();await canvas.waitFor();assert.equal(await canvas.getAttribute('height'),'560');assert.equal(await canvas.getAttribute('data-meter-columns'),'17');assert.match(await canvas.getAttribute('aria-label'),/64/);
 await page.waitForTimeout(900);
 const transport=await(await page.request.get(capacity+'/api/transport')).json();assert.equal(transport.playing,true);assert.match(transport.activeBackend,/Null/);assert.ok(!transport.error);
 // Audible levels can lag playback by a moment on a busy machine, so poll for them instead of reading once.
 let meters;for(let i=0;i<100;i++){meters=await(await page.request.get(capacity+'/api/meters')).json();if(Object.values(meters.tracks).some(x=>x.peak>-80))break;await page.waitForTimeout(100)}
 assert.equal(Object.keys(meters.tracks).length,64);assert.ok(Object.values(meters.tracks).some(x=>x.peak>-80),'no track level rose above -80 dB within 10 s of Play');
 await page.screenshot({path:path.join(proof,'native-64-tracks-1440.png')});
 const state=await desktop.storageState();const phone=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,reducedMotion:'reduce',storageState:state});watches.push(await stall.watchContext(phone, {serverLog: process.env.CICADA_STUDIO_STARTUP_LOG}));const mobile=await phone.newPage();
 mobile.on('pageerror',e=>errors.push(e.message));await mobile.goto(capacity+'/?panel=mixer',{waitUntil:'domcontentloaded'});await ready(mobile);const phoneCanvas=mobile.locator('#cicada-meters canvas[data-meter-track-count="64"]');await phoneCanvas.waitFor();const layout=await phoneCanvas.evaluate(c=>({columns:Number(c.dataset.meterColumns),width:c.width,css:c.getBoundingClientRect().width,height:c.height}));assert.ok(layout.columns<=6);assert.ok(layout.width/layout.columns>=64);assert.ok(Math.abs(layout.width-layout.css)<1);assert.equal(layout.height,Math.ceil(65/layout.columns)*140);assert.equal(await mobile.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
 await mobile.locator('#cicada-meters').screenshot({path:path.join(proof,'native-64-tracks-390.png')});
 await page.getByRole('button',{name:'Stop',exact:true}).click();
 // The buffer checks can start only when playback has stopped (the audio form is locked while it plays); the capacity and buffer checks may share one Studio.
 for(let i=0;i<100&&(await(await page.request.get(capacity+'/api/transport')).json()).playing;i++)await page.waitForTimeout(100);
 }

 if(part!=='capacity'){const audio=require('./audio-performance.cjs');await audio.checkAudioPerformance({browser,base:vocal,proof,ready,errors,watches,context:part==='buffer'?desktop:undefined})}
 assert.deepEqual(errors,[]);fs.writeFileSync(path.join(proof,part==='both'?'browser-proof.json':'browser-proof-'+part+'.json'),JSON.stringify({nativeTracks:64,allMetersVisible:true,mobileOverflow:false,bufferChoices:[64,128,256,512,1024],microphoneActivated:false,errors},null,2)+'\n');
});
