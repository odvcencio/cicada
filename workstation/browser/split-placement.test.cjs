/* Render-checked splits against a disposable owner-session project. */
'use strict';
const assert=require('node:assert/strict'), fs=require('node:fs'), path=require('node:path'), test=require('node:test');
const entry=`// split fixture: definitions\ncicada 2\ntrack drums drums {}\ntrack bass acid {}\npattern beat drums {\n  bd: X... .... X... ....\n  sd: .... X... .... X...\n}\npattern chance { c3?50 . c4?50 c3?50 }\n`;
const arrangement=`// arrangement owner\r\narrange {\r\n  place good drums beat { at = @1.1.1 length = 4bars }\r\n  place risk bass chance { at = @5.1.1 length = 4bars }\r\n}\r\n`;
if(process.argv[2]==='--write-fixture'){
 const dir=path.resolve(process.argv[3]);fs.mkdirSync(dir,{recursive:true});
 for(const [name,text] of Object.entries({'main.cicada':entry,'sections.cicada':arrangement,'cicada.mod':'project split-fixture\ncicada 2\nentry "main.cicada"\nsource "sections.cicada"\n'}))fs.writeFileSync(path.join(dir,name),text,{flag:'wx'});
 process.exit(0);
}
async function until(check,message){const end=Date.now()+30000;while(Date.now()<end){if(await check())return;await new Promise(r=>setTimeout(r,50));}assert.fail(message)}
test('split uses render proof, refuses invalid and stale targets, and has one Undo',{timeout:180000},async t=>{
 const origin=new URL(process.env.CICADA_STUDIO_URL);assert.equal(origin.hostname,'127.0.0.1');
 const dir=process.env.CICADA_SPLIT_DIR;assert.ok(path.isAbsolute(dir));const owner=path.join(dir,'sections.cicada');
 assert.equal(fs.readFileSync(owner,'utf8'),arrangement);
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');
 const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox','--mute-audio']});t.after(()=>browser.close());
 const page=await browser.newPage({viewport:{width:1440,height:1000}}), errors=[];page.on('pageerror',e=>errors.push(e.message));
 const state=async()=>(await page.request.get(new URL('/api/state',origin).href)).json();
 const ready=async()=>{await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();await until(async()=>await page.locator('#undo-form input[name=revision]').inputValue()===(await state()).revision,'revision did not settle')};
 const select=async name=>{await page.goto(new URL('/studio?placement='+encodeURIComponent(name),origin).href);await ready();const box=inspector(name);if(await box.getAttribute('open')===null)await box.locator('summary').first().click()};
 const inspector=name=>page.locator(`[data-placement-inspector="${name}"]`);
 const splitForm=async name=>{const section=inspector(name).locator('.placement-split');if(await section.getAttribute('open')===null)await section.locator('summary').click();return section.locator('form')};
 const split=async(form,tick)=>{await form.getByLabel('Split position').selectOption(String(tick));const response=page.waitForResponse(r=>r.url().includes('/__actions/split-placement'));await form.getByRole('button',{name:'Split placement',exact:true}).click();return response};
 await page.goto(new URL('/studio?placement=good',origin).href);await ready();
 // Native forms remain usable without JavaScript, including an audible refusal.
 const nativeContext=await browser.newContext({javaScriptEnabled:false,storageState:await page.context().storageState()}), native=await nativeContext.newPage();
 await native.goto(new URL('/studio?placement=good',origin).href);
 const nativeBox=native.locator('[data-placement-inspector="good"]');
 if(await nativeBox.getAttribute('open')===null)await nativeBox.locator('summary').first().click();
 await nativeBox.locator('.placement-split summary').click();
 await nativeBox.getByLabel('Split position').selectOption('7680');
 const nativeWrite=native.waitForResponse(r=>r.url().includes('/__actions/split-placement'));
 await nativeBox.getByRole('button',{name:'Split placement',exact:true}).click();assert.ok((await nativeWrite).status()<400,'native split failed');
 assert.match(fs.readFileSync(owner,'utf8'),/place good-2 drums beat\s*\{[^}]*at\s*=\s*@3\.1\.1/,'native split must use the selected absolute tick');
 await native.locator('#undo-form button[type=submit]').click();
 await until(()=>fs.readFileSync(owner,'utf8')===arrangement,'native Undo did not restore arrangement');
 await native.goto(new URL('/studio?placement=risk',origin).href);
 const nativeRisk=native.locator('[data-placement-inspector="risk"]');
 if(await nativeRisk.getAttribute('open')===null)await nativeRisk.locator('summary').first().click();
 await nativeRisk.locator('.placement-split summary').click();await nativeRisk.getByLabel('Split position').selectOption('23040');
 const nativeRefusal=native.waitForResponse(r=>r.url().includes('/__actions/split-placement'));
 await nativeRisk.getByRole('button',{name:'Split placement',exact:true}).click();assert.ok((await nativeRefusal).status()<400,'native refusal must return to the form');
 await until(async()=>/chance steps|restarts/i.test(await native.locator('body').innerText()),'native audible refusal not explained');
 assert.equal(fs.readFileSync(owner,'utf8'),arrangement);await nativeContext.close();
 await page.reload();await ready();
 let form=await splitForm('good');
 if(process.env.CICADA_SPLIT_PROOF_DIR){fs.mkdirSync(process.env.CICADA_SPLIT_PROOF_DIR,{recursive:true});await inspector('good').screenshot({path:path.join(process.env.CICADA_SPLIT_PROOF_DIR,'split-placement.png')});}
 await form.getByLabel('Split position').evaluate(el=>el.add(new Option('invalid','100')));
 const bad=await split(form,100);assert.equal((await bad).status(),422);assert.equal(fs.readFileSync(owner,'utf8'),arrangement);
 await until(async()=>/pattern-length boundary/i.test(await page.locator('body').innerText()),'boundary refusal was not explained');
 form=await splitForm('good');
 if (!(await page.locator('#studio-code > details').evaluate(node=>node.open))) await page.locator('.shell-pane-toggle[data-pane=code][data-pane-action=show]').click();
 const code=page.getByRole('textbox',{name:'Cicada score',exact:true}), clean=await code.inputValue();
 await code.fill(clean+'// unsaved split draft\n');
 const success=await split(form,7680);assert.equal((await success).status(),200);
 await until(()=>fs.readFileSync(owner,'utf8').includes('place good-2'),'split not written to arrangement owner');
 assert.equal(fs.readFileSync(path.join(dir,'main.cicada'),'utf8'),entry);
 assert.equal(await code.inputValue(),clean+'// unsaved split draft\n','split lost an unsaved Code draft');
 await code.fill(clean);
 await page.locator('#undo-form button[type=submit]').click();await until(()=>fs.readFileSync(owner,'utf8')===arrangement,'one Undo did not restore exact CRLF arrangement');await ready();
 // An earlier queued grid edit cannot silently advance this split's receipt.
 await page.goto(new URL('/studio?placement=good&dock=clip&pattern=beat',origin).href);await ready();
 form=await splitForm('good');await form.getByLabel('Split position').selectOption('7680');
 let releaseFirst, signalFirst;const held=new Promise(resolve=>releaseFirst=resolve), started=new Promise(resolve=>signalFirst=resolve);
 await page.route('**/__actions/pattern',async route=>{signalFirst();await held;await route.continue()});
 await page.locator('#detail-panel-clip .piano-roll .roll-cell').first().click();await started;
 const queued=page.waitForResponse(r=>r.url().includes('/__actions/split-placement'));
 await form.getByRole('button',{name:'Split placement',exact:true}).click();releaseFirst();
 const queuedResult=await queued;assert.equal(queuedResult.status(),409,'queued split must not inherit an earlier edit receipt');
 assert.equal(fs.readFileSync(owner,'utf8'),arrangement,'queued split changed the placement owner');
 await page.unroute('**/__actions/pattern');
 const retry=page.locator('[data-workspace-retry]');if(await retry.isVisible()){await retry.click();await until(async()=>!(await retry.isVisible()),'refresh after stale split did not settle')}
 await ready();await page.locator('#undo-form button[type=submit]').click();await until(()=>fs.readFileSync(path.join(dir,'main.cicada'),'utf8')===entry,'Undo did not restore queued grid edit');await ready();
 // Restore the refused split input so placement navigation is not blocked by its draft.
 form=await splitForm('good');await form.getByLabel('Split position').selectOption('3840');
 // Follow a managed placement link and submit the newly rendered inspector.
 const riskLink=page.getByRole('link',{name:'Edit placement risk',exact:true});await riskLink.focus();await page.keyboard.press('Enter');
 await until(()=>new URL(page.url()).searchParams.get('placement')==='risk','managed placement navigation did not settle');
 await ready();const riskBox=inspector('risk');if(await riskBox.getAttribute('open')===null)await riskBox.locator('summary').first().click();
 form=await splitForm('risk');const audible=await split(form,23040);assert.equal((await audible).status(),422);
 await until(async()=>/chance steps|restarts/i.test(await page.locator('body').innerText()),'audible refusal not explained');assert.equal(fs.readFileSync(owner,'utf8'),arrangement);
 await select('good');form=await splitForm('good');
 // Replace the source after the browser submits its original target/receipt.
 const external=arrangement.replace('place good drums beat','place good drums chance')+'// external edit\r\n';let intercepted=false;
 await page.route('**/__actions/split-placement',async route=>{if(!intercepted){intercepted=true;fs.writeFileSync(owner,external)}await route.continue()});
 const stale=await split(form,7680);assert.equal((await stale).status(),409);assert.equal(fs.readFileSync(owner,'utf8'),external);
 assert.deepEqual(errors,[]);
});
