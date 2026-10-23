// Real form edits over disposable scores, null audio, and offline WAV renders.
'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto');
const entry='cicada 2\n// cicada-tempo-editor-test: disposable fixture\ntitle "Room to breathe"\ntempo 120\nseed 42\ntrack bass acid { level=-18dB }\npattern pulse { c3 . g3 . e3 . g3 . c3 . g3 . e3 . g3 . }\n';
const arrangement='// Keep these musical notes.\r\narrange {\r\n  place run bass pulse { at = @1.1.1 length = 8bars } // keep this phrase\r\n  marker chorus { at = @5.1.1 }\r\n}\r\n';
if(process.argv[2]==='--write-fixture'){
 const root=path.resolve(process.argv[3]),primary=process.argv.includes('--primary');fs.mkdirSync(root,{recursive:true});
 fs.writeFileSync(path.join(root,'main.cicada'),entry+(primary?arrangement:''),{flag:'wx'});
 if(!primary){fs.writeFileSync(path.join(root,'song.cicada'),arrangement,{flag:'wx'});fs.writeFileSync(path.join(root,'cicada.mod'),'project tempo-editor\ncicada 2\nentry "main.cicada"\nsource "song.cicada"\n',{flag:'wx'})}
 console.log(JSON.stringify({score:path.join(root,'main.cicada'),owner:path.join(root,primary?'main.cicada':'song.cicada'),primary}));process.exit(0);
}
const digest=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
async function until(check,label){const end=Date.now()+30000;while(Date.now()<end){if(await check())return;await new Promise(resolve=>setTimeout(resolve,40))}assert.fail(label)}
test('tempo forms move, change and remove steps with history, source conflicts, WAV export and reopen',{timeout:180000},async t=>{
 const fixture=JSON.parse(fs.readFileSync(process.env.CICADA_TEMPO_FIXTURE,'utf8')),proof=process.env.CICADA_TEMPO_PROOF_DIR;
 assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const startup=new URL(process.env.CICADA_STUDIO_URL),origin=startup.origin;assert.equal(startup.hostname,'127.0.0.1');
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');
 const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']}),context=await browser.newContext({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),page=await context.newPage(),errors=[],checks=[];
 page.on('pageerror',error=>errors.push(error.message));let completed=false;
 t.after(async()=>{if(!completed)await page.screenshot({path:path.join(proof,'failure.png'),timeout:5000}).catch(()=>{});await browser.close()});
 const owner=()=>fs.readFileSync(fixture.owner,'utf8'),read=async name=>{const r=await page.request.get(origin+'/api/'+name);assert.equal(r.status(),200);return r.json()};
 const ready=async()=>{await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();await page.waitForFunction(()=>window.__gosx?.ready&&!document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'))};
 const open=async()=>{await ready();const details=page.locator('[data-arrangement-tempos]');if(await details.getAttribute('open')===null)await details.locator(':scope > summary').click();return details};
 const row=bar=>page.locator(`[data-tempo-step="@${bar}.1.1"]`),add=()=>page.locator('[data-tempo-add] form');
 const saved=async check=>{await until(()=>check(owner()),'tempo source saved');await ready();if(!fixture.primary)assert.equal(fs.readFileSync(fixture.score,'utf8'),entry)};
 async function submit(form,values,label){await open();for(const [name,value]of Object.entries(values))await form.locator(`[name="${name}"]`).fill(value);const reply=page.waitForResponse(r=>new URL(r.url()).pathname==='/__actions/arrange'&&r.request().method()==='POST');const action=form.getByRole('button',{name:label,exact:true});if(label==='Save tempo step')await action.press('Enter');else await action.click();await reply;await ready()}
 async function history(name,source){await page.locator(name==='Undo'?'#undo-form button':'#redo-form button').click();await saved(value=>value===source);await open()}
 async function render(name){await page.getByRole('toolbar',{name:'Transport'}).getByRole('button',{name:'Export',exact:true}).click();const previous=(await read('export')).id;await page.locator('#export').getByRole('button',{name:'Render WAV',exact:true}).click();let job;await until(async()=>{job=await read('export');assert.notEqual(job.state,'failed',JSON.stringify(job));return job.id!==previous&&['succeeded','shortfall'].includes(job.state)},'WAV render');const r=await page.request.get(new URL(job.downloadURL,origin).href);assert.equal(r.status(),200);const wav=await r.body();assert.equal(wav.subarray(0,4).toString(),'RIFF');fs.writeFileSync(path.join(proof,name),wav);await page.keyboard.press('Escape');return wav}
 await page.goto(startup.href);await page.goto(origin+'/studio?tab=arrange&placement=run');await open();
 const audio=await read('audio/config');assert.equal(audio.options.inputEnabled,false);assert.equal(String(audio.inventory.backendName||audio.inventory.backend).toLowerCase(),'null');
 if(process.env.CICADA_TEMPO_REOPEN==='1'){
  const expected=JSON.parse(fs.readFileSync(path.join(proof,'report.json'),'utf8'));assert.equal(owner(),expected.finalOwner);assert.equal(digest(await render('reopened.wav')),expected.wavSHA256);assert.deepEqual(errors,[]);completed=true;return;
 }
 assert.equal(owner(),fixture.primary?entry+arrangement:arrangement);const original=owner(),baseline=await render('baseline.wav');
 await submit(add(),{bar:'3',bpm:'96'},'Add tempo step');await saved(source=>source.includes('tempo @3.1.1 96'));await row(3).waitFor();const first=owner();
 await submit(row(3).locator('form'),{bar:'6',bpm:'137.333'},'Save tempo step');await saved(source=>source.includes('tempo @6.1.1 137.333')&&!source.includes('tempo @3.1.1'));await row(6).waitFor();const moved=owner();
 await history('Undo',first);await row(3).waitFor();await history('Redo',moved);await row(6).waitFor();checks.push('move and BPM change are one exact Undo/Redo edit');
 await submit(add(),{bar:'1',bpm:'120'},'Add tempo step');await saved(source=>source.includes('tempo @1.1.1 120'));await row(1).waitFor();
 await submit(add(),{bar:'4',bpm:'96'},'Add tempo step');await saved(source=>source.includes('tempo @4.1.1 96'));await row(4).waitFor();
 await submit(row(4).locator('form'),{bpm:'98.125'},'Save tempo step');await saved(source=>source.includes('tempo @4.1.1 98.125'));await row(6).locator('[name=bpm]').fill('140');await submit(add(),{bar:'2',bpm:'110'},'Add tempo step');await saved(source=>source.includes('tempo @2.1.1 110'));assert.equal(await row(6).locator('[name=bpm]').inputValue(),'140');assert.equal(await row(4).locator('[name=bpm]').inputValue(),'98.125');await submit(row(2).locator('form'),{},'Remove step');await saved(source=>!source.includes('tempo @2.1.1'));assert.equal(await row(6).locator('[name=bpm]').inputValue(),'140');await row(6).locator('[name=bpm]').fill('137.333');checks.push('an unsaved tempo value stays with its step while another step is added and removed');const all=owner();
 await submit(row(4).locator('form'),{bar:'6'},'Save tempo step');assert.equal(owner(),all);await until(async()=>/already has a tempo step/.test(await page.locator('body').innerText()),'occupied bar refused');await row(4).locator('[name=bar]').fill('4');checks.push('adding out of order keeps steps sorted; occupied bars refuse without changing source');
 await submit(row(6).locator('form'),{bar:'7',bpm:''},'Remove step');await saved(source=>!source.includes('tempo @6.1.1')&&!source.includes('tempo @7.1.1'));await history('Undo',all);checks.push('remove targets the original step even with unsaved invalid field values');
 for(const bar of [1,4,6]){await submit(row(bar).locator('form'),{},'Remove step');await saved(source=>!source.includes(`tempo @${bar}.1.1`))}
 assert.equal(await page.locator('[data-tempo-step]').count(),0);assert.ok((await render('constant.wav')).equals(baseline));checks.push('removing all steps restores the original constant-tempo audio');
 for(let i=0;i<3;i++){await page.locator('#undo-form button').click();await ready();await until(async()=>await page.locator('[data-tempo-step]').count()===i+1,'Undo tempo removal');await open()}
 assert.equal(owner(),all);const finished=await render('finished.wav');assert.notEqual(digest(finished),digest(baseline));
 await open();for(const close of await page.locator('[data-gosx-toast-host] button').all())await close.click();
 await add().locator('[name=bar]').fill('2');await add().locator('[name=bpm]').fill('120');await page.locator('[data-arrangement-tempos]').screenshot({path:path.join(proof,'tempo-editor-desktop.png')});
 const phoneContext=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,storageState:await context.storageState()}),phone=await phoneContext.newPage();phone.on('pageerror',e=>errors.push(e.message));await phone.goto(origin+'/studio?tab=arrange');await phone.locator('#studio-shell[data-shell-actions=ready]').waitFor();await phone.locator('[data-arrangement-tempos] > summary').click();assert.equal(await phone.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);for(const bar of [1,4,6]){const step=phone.locator(`[data-tempo-step="@${bar}.1.1"]`);await step.scrollIntoViewIfNeeded();const box=await step.boundingBox();assert.ok(box.x>=0&&box.x+box.width<=390);await step.screenshot({path:path.join(proof,`tempo-editor-phone-bar-${bar}.png`)})}await phone.locator('[data-tempo-add]').screenshot({path:path.join(proof,'tempo-editor-phone-add.png')});await phone.screenshot({path:path.join(proof,'tempo-editor-phone.png')});await phoneContext.close();checks.push('desktop and phone controls fit without horizontal overflow; every phone row is reachable');
 const external=all+'// Another editor saved this.\r\n';let intercepted=false;
 await page.route('**/__actions/arrange',async route=>{if(!intercepted){intercepted=true;fs.writeFileSync(fixture.owner,external)}await route.continue()});
 await submit(row(4).locator('form'),{bpm:'100'},'Save tempo step');await until(async()=>(fixture.primary?/score changed on disk; reload before saving/i:/saved score files or libraries changed/i).test(await page.locator('body').innerText()),'source conflict refused');assert.equal(owner(),external);checks.push('a concurrent owning-source save is preserved');
 assert.equal(original,fixture.primary?entry+arrangement:arrangement);assert.deepEqual(errors,[]);fs.writeFileSync(path.join(proof,'report.json'),JSON.stringify({checks,errors,primary:fixture.primary,finalOwner:external,wavSHA256:digest(finished)},null,2)+'\n');completed=true;
});
