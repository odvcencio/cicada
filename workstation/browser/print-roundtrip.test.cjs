// Finish and reopen the synthetic printed-source song after the workflow checks.
'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto'),test=require('node:test');
const hash=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
async function until(check,label){const end=Date.now()+45000;while(Date.now()<end){if(await check())return;await new Promise(resolve=>setTimeout(resolve,50))}assert.fail(label)}
test('printed audio and comp metadata survive WAV export and a fresh process', {timeout:150000},async t=>{
 const fixture=JSON.parse(fs.readFileSync(process.env.CICADA_PRINT_FIXTURE,'utf8')),proof=process.env.CICADA_PRINT_PROOF_DIR;
 const startup=new URL(process.env.CICADA_STUDIO_URL),base=startup.origin;
 assert.equal(startup.hostname,'127.0.0.1');assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio']});t.after(()=>browser.close());
 const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],checks=[];page.on('pageerror',e=>errors.push(e.message));
 const read=async name=>{const r=await page.request.get(base+'/api/'+name);assert.equal(r.status(),200);return r.json()};
 const sources=()=>({entry:fs.readFileSync(fixture.score,'utf8'),arrangement:fs.readFileSync(fixture.arrangement,'utf8'),...(fixture.scenes?{scenes:fs.readFileSync(fixture.scenes,'utf8')}:{}),...(fixture.curves?{curves:fs.readFileSync(fixture.curves,'utf8')}:{})});
 const rawHashes=async()=>{const state=await read('takes');return state.takes.map(take=>{const file=path.resolve(path.dirname(fixture.score),take.asset.path);const actual=hash(fs.readFileSync(file));assert.equal(actual,take.asset.sha256);return {id:take.id,sha256:actual}}).sort((a,b)=>a.id.localeCompare(b.id))};
 await page.goto(startup.href);await page.goto(base+'/studio');await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
 const config=await read('audio/config');assert.equal(config.options.inputEnabled,false);assert.equal(String(config.inventory.backendName||config.inventory.backend).toLowerCase(),'null');
 if(process.env.CICADA_PRINT_REOPEN!=='1'){
  await page.goto(base+'/?panel=sampler');await page.locator('#cicada-sampler[data-sampler-ready=true]').waitFor();
  const form=page.locator('[data-sampler-resample] form');await form.locator('[name=newName]').fill('finished-print');await form.locator('[name=at]').fill('@5.1.1');await form.locator('[name=place]').check();
  await form.getByRole('button',{name:'Resample performance',exact:true}).click();
  await until(()=>sources().arrangement.includes('place finished-print sampler finished-print-clip'),'final sampler print');
  await page.goto(base+'/studio');await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
 }else{
  const expected=JSON.parse(fs.readFileSync(path.join(proof,'roundtrip-report.json'),'utf8'));assert.deepEqual(sources(),expected.sources);assert.deepEqual(await rawHashes(),expected.rawHashes);
 }
 const takes=await read('takes');assert.equal(takes.comps.length,1);assert.equal(takes.comps[0].segments.length,3);assert.equal(takes.takes.length,3);
 assert.ok(sources().arrangement.includes('place vocal-vox'));assert.ok(sources().arrangement.includes('place finished-print'));
 const previous=(await read('export')).id;await page.getByRole('toolbar',{name:'Transport'}).getByRole('button',{name:'Export',exact:true}).click();await page.locator('#export').getByRole('button',{name:'Render WAV',exact:true}).click();let job;
 await until(async()=>{job=await read('export');assert.notEqual(job.state,'failed',JSON.stringify(job));return job.id!==previous&&['succeeded','shortfall'].includes(job.state)},'finished WAV');
 const response=await page.request.get(new URL(job.downloadURL,base).href);assert.equal(response.status(),200);const wav=await response.body();assert.equal(wav.subarray(0,4).toString(),'RIFF');assert.ok(wav.length>100000);
 const reopening=process.env.CICADA_PRINT_REOPEN==='1';const file=reopening?'reopened.wav':'finished.wav';fs.writeFileSync(path.join(proof,file),wav);
 if(reopening)assert.ok(wav.equals(fs.readFileSync(path.join(proof,'finished.wav'))));
 checks.push('both printed parts, three comp segments and three immutable raw takes reopen together');checks.push('fresh-process WAV matches the finished export byte for byte');assert.deepEqual(errors,[]);
 fs.writeFileSync(path.join(proof,reopening?'reopen-report.json':'roundtrip-report.json'),JSON.stringify({sources:sources(),rawHashes:await rawHashes(),wavSHA256:hash(wav),checks,errors},null,2)+'\n');
});
