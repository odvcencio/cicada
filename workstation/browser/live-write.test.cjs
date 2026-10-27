// Real Studio UI and test-only synthetic PCM backend; no physical input device.
'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
test('prepare live amp Write before capture, keep and reopen',{timeout:120000},async t=>{
 const base=new URL(process.env.CICADA_STUDIO_URL),proof=process.env.CICADA_LIVE_WRITE_PROOF_DIR,score=process.env.CICADA_LIVE_WRITE_SCORE;
 assert.equal(base.hostname,'127.0.0.1');assert.ok(path.isAbsolute(proof)&&path.isAbsolute(score));fs.mkdirSync(proof,{recursive:true});
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright'),browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox','--mute-audio']});t.after(()=>browser.close());
 const page=await browser.newPage({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.exposeFunction('__liveWriteEvidence',async name=>{await page.locator('#detail-panel-automation .automation-recording').screenshot({path:path.join(proof,name+'.png')})});
 await page.addInitScript(()=>{window.__liveWriteResult=result=>{window.__liveWriteResultValue=result}});
 await page.addInitScript({path:path.resolve(__dirname,'../../desktop/testdata/native-live-write-workflow.js')});
 await page.goto(base.href);await page.waitForFunction(()=>window.__liveWriteResultValue,null,{timeout:100000});
 const result=await page.evaluate(()=>window.__liveWriteResultValue);fs.writeFileSync(path.join(proof,'report.json'),JSON.stringify({result,errors},null,2)+'\n');
 if(!result.ok){await page.screenshot({path:path.join(proof,'failure.png')});fs.writeFileSync(path.join(proof,'failure.html'),await page.content())}
 assert.equal(result.ok,true,result.error);assert.deepEqual(errors,[]);assert.equal(result.physicalAudioVerified,false);
 const takes=await(await page.request.get(new URL('/api/takes',base).href)).json(),take=takes.takes.find(t=>t.id===result.takeID);assert.ok(take?.asset?.path);
 const root=path.dirname(score),asset=path.resolve(root,take.asset.path);assert.ok(asset.startsWith(root+path.sep));const wav=fs.readFileSync(asset),data=wav.indexOf('data');assert.ok(data>=0&&wav.readUInt16LE(20)===3&&wav.readUInt16LE(22)===2,'synthetic take is stereo float WAV');const bytes=wav.readUInt32LE(data+4);assert.ok(bytes>0&&data+8+bytes<=wav.length);for(let i=data+8;i<data+8+bytes;i+=4)assert.ok(Math.abs(wav.readFloatLE(i)-.2)<1e-6,'take must contain dry synthetic input');
 const phoneContext=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,storageState:await page.context().storageState()}),phone=await phoneContext.newPage();await phone.goto(new URL('/studio?tab=session&region=detail&dock=automation&curvePath=guitar.amp_drive&vocalTrack=guitar',base).href);await phone.locator('#studio-shell[data-shell-actions=ready]').waitFor();assert.equal(await phone.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await phone.locator('#detail-panel-automation .automation-recording').screenshot({path:path.join(proof,'phone.png')});await phoneContext.close();
});
