'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),{performance}=require('node:perf_hooks');
test('unused pinned ideas become editable local phrases with their chosen sound',{timeout:300000},async t=>{
 const base=new URL(process.env.CICADA_STUDIO_URL),proof=process.env.CICADA_DIRECT_PROOF_DIR;assert.equal(base.hostname,'127.0.0.1');assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']});t.after(()=>browser.close());
 const page=await browser.newPage({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),errors=[];page.on('pageerror',e=>errors.push(e.message));let delayedMutationMs=0;
 // The first copy named chorus must stay open longer than the page's ordinary 15-second request deadline. Node arms a
 // timer from the event loop's cached millisecond clock, so setTimeout(16000) can fire up to about a millisecond early
 // as Date.now() and performance.now() see it (two CI runs read 15,999 ms). Hold the request until the monotonic clock
 // itself shows the full time, then require only that it outlasted the 15-second deadline.
 const ORDINARY_REQUEST_DEADLINE_MS=15000,HOLD_MS=ORDINARY_REQUEST_DEADLINE_MS+1000;
 await page.route('**/__actions/pattern',async route=>{
  const request=route.request(),type=request.headers()['content-type']||'';
  const newName=/^application\/json\b/i.test(type)?request.postDataJSON().newName:(await new Response(request.postDataBuffer(),{headers:{'Content-Type':type}}).formData()).get('newName');
  if(!delayedMutationMs&&newName==='chorus'){
   const started=performance.now();delayedMutationMs=-1;
   while(performance.now()-started<HOLD_MS)await new Promise(r=>setTimeout(r,Math.ceil(HOLD_MS-(performance.now()-started))));
   delayedMutationMs=Math.floor(performance.now()-started);
  }
  await route.continue();
 });
 await page.exposeFunction('__cicadaDirectAudio',(label,base64)=>fs.writeFileSync(path.join(proof,label+'.wav'),Buffer.from(base64,'base64')));
 await page.exposeFunction('__cicadaDirectSubmit',async id=>{await page.locator('[data-pattern-variation="'+id+'"] button[type=submit]').press('Enter');});
 await page.exposeFunction('__cicadaDirectViews',async()=>{const section=page.locator('[data-pattern-variation="motif.idea"]');await section.screenshot({path:path.join(proof,'variation-desktop.png')});await page.setViewportSize({width:390,height:844});await page.waitForTimeout(150);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await section.screenshot({path:path.join(proof,'variation-phone.png')});await page.setViewportSize({width:1440,height:1100});});
 if(process.env.CICADA_DIRECT_REOPEN==='1'){const previous=JSON.parse(fs.readFileSync(path.join(proof,'report.json'),'utf8')).result;await page.addInitScript(previous=>sessionStorage.setItem('cicada.direct-variation-proof',JSON.stringify({...previous,phase:'reopen'})),previous);}
 await page.addInitScript(()=>{window.__cicadaDirectResult=result=>{window.__directResult=result;};});await page.addInitScript({path:path.resolve(__dirname,'../../desktop/testdata/native-direct-variation-workflow.js')});
 await page.goto(base.href);await page.goto(base.origin+'/studio?dock=clip&pattern='+(process.env.CICADA_DIRECT_REOPEN==='1'?'answer&track=upper':'motif.idea&track=keys')+'&scene=main&region=detail');await page.waitForFunction(()=>window.__directResult,null,{timeout:280000});const result=await page.evaluate(()=>window.__directResult);
 fs.writeFileSync(path.join(proof,process.env.CICADA_DIRECT_REOPEN==='1'?'reopen-report.json':'report.json'),JSON.stringify({result,errors,delayedMutationMs},null,2)+'\n');if(!result.ok){await page.screenshot({path:path.join(proof,'failure.png')});fs.writeFileSync(path.join(proof,'failure.html'),await page.content());}
 assert.equal(result.ok,true,result.error);assert.deepEqual(errors,[]);if(process.env.CICADA_DIRECT_REOPEN!=='1')assert.ok(delayedMutationMs>ORDINARY_REQUEST_DEADLINE_MS,'the copy was held '+delayedMutationMs+' ms, not past the ordinary 15-second request deadline');
});
