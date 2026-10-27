'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),{performance}=require('node:perf_hooks');
test('one placement develops an independent local variation of an imported performance',{timeout:180000},async t=>{
 const base=new URL(process.env.CICADA_STUDIO_URL),proof=process.env.CICADA_VARIATION_PROOF_DIR;
 assert.equal(base.hostname,'127.0.0.1');assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');
 const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']});t.after(()=>browser.close());
 const page=await browser.newPage({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),errors=[];
 page.on('pageerror',error=>errors.push(error.message));
 let delayedMutationMs=0;
 await page.route('**/__actions/arrange',async route=>{
  const request=route.request(),type=request.headers()['content-type']||'',body=request.postData()||'';
  let values;
  if(type.includes('application/json'))values=request.postDataJSON();
  else if(type.includes('application/x-www-form-urlencoded'))values=Object.fromEntries(new URLSearchParams(body));
  else values={action:body.match(/name="action"\r\n\r\n([^\r]*)/)?.[1],newName:body.match(/name="newName"\r\n\r\n([^\r]*)/)?.[1]};
  if(!delayedMutationMs && values.action==='vary' && values.newName==='reply'){
   const started=performance.now();delayedMutationMs=-1;
   // Timer and wall-clock rounding can otherwise report 15,999 ms for a
   // nominal 16-second wait. Keep the full delay using a monotonic clock.
   while(performance.now()-started<16000)await new Promise(resolve=>setTimeout(resolve,Math.ceil(16000-(performance.now()-started))));
   delayedMutationMs=Math.floor(performance.now()-started);
  }
  await route.continue();
 });
 await page.exposeFunction('__cicadaVariationAudio',(label,base64)=>fs.writeFileSync(path.join(proof,label+'.wav'),Buffer.from(base64,'base64')));
 await page.exposeFunction('__cicadaVariationSubmit',async()=>{const button=page.locator('[data-placement-variation=return] button[type=submit]');assert.equal(await button.isVisible(),true,'variation retry button is hidden');await button.press('Enter');});
 await page.exposeFunction('__cicadaVariationViews',async()=>{
  const variation=page.locator('[data-placement-variation=return]');await variation.evaluate(node=>node.scrollIntoView({block:'center'}));
  await variation.screenshot({path:path.join(proof,'variation-desktop.png')});
  await page.setViewportSize({width:390,height:844});await page.waitForTimeout(150);await variation.evaluate(node=>node.scrollIntoView({block:'center'}));
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  await variation.locator('form').screenshot({path:path.join(proof,'variation-phone.png')});await page.setViewportSize({width:1440,height:1100});
 });
 if(process.env.CICADA_VARIATION_REOPEN==='1'){
  const previous=JSON.parse(fs.readFileSync(path.join(proof,'report.json'),'utf8')).result;
  await page.addInitScript(previous=>sessionStorage.setItem('cicada.placement-variation-proof',JSON.stringify({...previous,phase:'reopen'})),previous);
 }
 await page.addInitScript(()=>{window.__cicadaVariationResult=result=>{window.__variationResult=result;};});
 await page.addInitScript({path:path.resolve(__dirname,'../../desktop/testdata/native-placement-variation-workflow.js')});
 await page.goto(base.href);await page.goto(base.origin+(process.env.CICADA_VARIATION_REOPEN==='1'?'/studio?dock=clip&pattern=reply&region=detail':'/studio?tab=arrange&region=center&placement=return'));
 await page.waitForFunction(()=>window.__variationResult,null,{timeout:160000});const result=await page.evaluate(()=>window.__variationResult);
 fs.writeFileSync(path.join(proof,process.env.CICADA_VARIATION_REOPEN==='1'?'reopen-report.json':'report.json'),JSON.stringify({result,errors,delayedMutationMs},null,2)+'\n');
 if(!result.ok){await page.screenshot({path:path.join(proof,'failure.png')});fs.writeFileSync(path.join(proof,'failure.html'),await page.content());}
 assert.equal(result.ok,true,result.error);assert.deepEqual(errors,[]);
 if(process.env.CICADA_VARIATION_REOPEN!=='1')assert.ok(delayedMutationMs>=16000,'variation did not cross the ordinary 15-second request deadline');
});
