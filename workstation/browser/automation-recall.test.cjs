// Real carried-curve editing with a disposable four-source project and null audio.
'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
async function until(check,label){const end=Date.now()+30000;while(Date.now()<end){if(await check())return;await new Promise(resolve=>setTimeout(resolve,40))}assert.fail(label)}
test('carried curve recalls stay visible while source edits, Undo and outside-save conflicts preserve the project',{timeout:120000},async t=>{
 const fixture=JSON.parse(fs.readFileSync(process.env.CICADA_PRINT_FIXTURE,'utf8'));assert.equal(fixture.sceneControls,true);assert.ok(fixture.curves.startsWith('/tmp/'));
 const proof=process.env.CICADA_PRINT_PROOF_DIR;assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const startup=new URL(process.env.CICADA_STUDIO_URL),base=startup.origin;assert.equal(startup.hostname,'127.0.0.1');
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']});t.after(()=>browser.close());
 const context=await browser.newContext({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));
 await page.goto(startup.href);await page.goto(base+'/?panel=session&curvePath=lead.level');await page.locator('#cicada-automation-curve[data-automation-ready=true]').waitFor();
 const form=page.locator('.automation-point-form'),details=page.locator('[data-automation-recalls="lead.level"]');await details.locator('summary').click();
 assert.match(await details.innerText(),/Curve recalls · 4/);assert.match(await details.innerText(),/@3\.1\.1 · @5\.1\.1 · @7\.1\.1 · @8\.1\.1/);
 const before=fs.readFileSync(fixture.curves,'utf8'),entry=fs.readFileSync(fixture.score,'utf8'),song=fs.readFileSync(fixture.arrangement,'utf8');
 const receipt=await form.locator('[name=source_revision]').inputValue();assert.ok(receipt);
 await form.getByLabel('Point value',{exact:true}).fill('-17dB');await form.getByRole('button',{name:'Save point',exact:true}).click();
 await until(()=>fs.readFileSync(fixture.curves,'utf8').includes('@1.1.1 -17dB step'),'curve edit did not reach its owning file');
 assert.equal(fs.readFileSync(fixture.score,'utf8'),entry);assert.equal(fs.readFileSync(fixture.arrangement,'utf8'),song);
 await until(async()=>await form.locator('[name=source_revision]').inputValue()!==receipt,'project receipt did not follow the auxiliary edit');
 await page.getByRole('button',{name:'Undo',exact:true}).click();await until(()=>fs.readFileSync(fixture.curves,'utf8')===before,'Undo did not restore exact curve bytes');
 await page.locator('#cicada-workspace[aria-busy=true]').waitFor({state:'hidden'});await page.locator('#cicada-automation-curve[data-automation-ready=true]').waitFor();
 if(!(await details.evaluate(node=>node.open)))await details.locator('summary').click();
 await page.locator('#automation-editor').screenshot({path:path.join(proof,'recall-desktop.png')});
 const phoneContext=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,storageState:await context.storageState()}),phone=await phoneContext.newPage();
 await phone.goto(base+'/?panel=session&curvePath=lead.level');await phone.locator('#cicada-automation-curve[data-automation-ready=true]').waitFor();await phone.locator('[data-automation-recalls="lead.level"] summary').click();
 assert.equal(await phone.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await phone.locator('#automation-editor').screenshot({path:path.join(proof,'recall-mobile.png')});await phoneContext.close();
 let injected=false;const external=before+'// another editor saved this\r\n';await page.route('**/__actions/automation',async route=>{if(!injected){injected=true;fs.writeFileSync(fixture.curves,external)}await route.continue()});
 await form.getByLabel('Point value',{exact:true}).fill('-15dB');const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/__actions/automation');await form.getByRole('button',{name:'Save point',exact:true}).click();assert.equal((await response).status(),409);
 assert.equal(fs.readFileSync(fixture.curves,'utf8'),external);assert.equal(fs.readFileSync(fixture.score,'utf8'),entry);assert.equal(fs.readFileSync(fixture.arrangement,'utf8'),song);
 assert.equal(await form.getByLabel('Point value',{exact:true}).inputValue(),'-15dB');assert.deepEqual(errors,[]);
 fs.writeFileSync(path.join(proof,'recall-report.json'),JSON.stringify({checks:['four musical recalls shown in order','curve editing updates only its owning source','Undo restores exact comments, points and recall cues','desktop and phone controls fit without horizontal overflow','outside-save conflict preserves external bytes and the current value draft'],errors},null,2)+'\n');
});
