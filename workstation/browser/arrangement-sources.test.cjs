// Source-aware placement controls against a disposable, self-contained project.
'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto'),test=require('node:test');
const entry=`cicada 2
// cicada-arrangement-sources-test: disposable fixture
title "Arrangement sources"
tempo 108
seed 42
track bass acid { level = -18dB }
pattern pulse { c3 . g3 . e3 . g3 . c3 . g3 . e3 . g3 . }
`;
const arrangement=`// Keep these section notes.\r\narrange {\r\n  place one bass pulse { at = @1.1.1 length = 4bars } // keep this comment\r\n  place two bass pulse { at = @5.1.1 length = 4bars }\r\n  marker chorus { at = @5.1.1 }\r\n}\r\n`;
if(process.argv[2]==='--write-fixture'){
 const root=path.resolve(process.argv[3]);fs.mkdirSync(root,{recursive:true});
 for(const [name,source] of Object.entries({'main.cicada':entry,'arrangement.cicada':arrangement,'cicada.mod':'project arrangement-sources\ncicada 2\nentry "main.cicada"\nsource "arrangement.cicada"\n'}))fs.writeFileSync(path.join(root,name),source,{flag:'wx'});
 console.log(JSON.stringify({score:path.join(root,'main.cicada'),arrangement:path.join(root,'arrangement.cicada')}));process.exit(0);
}
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function until(check,label){const end=Date.now()+30000;while(Date.now()<end){if(await check())return;await pause(40)}assert.fail(label)}
const digest=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
test('secondary arrangement controls, source conflicts, WAV and reopen', {timeout:180000},async t=>{
 const fixture=JSON.parse(fs.readFileSync(process.env.CICADA_ARRANGEMENT_FIXTURE,'utf8')),proof=process.env.CICADA_ARRANGEMENT_PROOF_DIR;
 assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const startup=new URL(process.env.CICADA_STUDIO_URL),origin=startup.origin;
 assert.equal(startup.hostname,'127.0.0.1');assert.equal(fs.readFileSync(fixture.score,'utf8'),entry);
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');
 const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH});
 const context=await browser.newContext({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),page=await context.newPage(),errors=[],checks=[];
 await page.addInitScript(()=>Object.defineProperty(navigator,'platform',{get:()=>'MacIntel'}));
 let completed=false;
 t.after(async()=>{if(!completed){await page.screenshot({path:path.join(proof,'failure.png'),timeout:5000}).catch(()=>{});fs.writeFileSync(path.join(proof,'failure.html'),await page.content().catch(()=>''))}await browser.close()});
 page.on('pageerror',error=>errors.push(error.message));
 const read=async name=>{const response=await page.request.get(origin+'/api/'+name);assert.equal(response.status(),200);return response.json()};
 const owner=()=>fs.readFileSync(fixture.arrangement,'utf8');
 const ready=async()=>{await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();await until(async()=>!await page.locator('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]').count(),'managed form finished')};
 const inspector=name=>page.locator(`[data-placement-inspector="${name}"]`);
 const block=name=>page.locator(`[data-placement-block="${name}"]`);
 const action=(kind,name)=>page.locator(`#arrangement form[action$="/__actions/arrange"]:has(input[name=action][value="${kind}"])${name?`:has(input[name=name][value="${name}"])`:''}`);
 const saved=async check=>{await until(()=>check(owner()),'owning arrangement saved');await ready();assert.equal(fs.readFileSync(fixture.score,'utf8'),entry)};
 const undo=async expected=>{await page.locator('#undo-form button').click();await saved(source=>source===expected)};
 const select=async name=>{await page.getByRole('link',{name:'Edit placement '+name,exact:true}).click();await until(async()=>await block(name).getAttribute('data-placement-selected')==='true','placement selected');await ready()};
 const edit=async(form,values,label)=>{for(const details of await form.locator('xpath=ancestor::details').all())if(await details.getAttribute('open')===null)await details.locator(':scope > summary').click();for(const [name,value]of Object.entries(values))await form.locator(`[name="${name}"]`).fill(value);await form.getByRole('button',{name:label,exact:true}).click()};
 async function render(filename){
  await page.getByRole('toolbar',{name:'Transport'}).getByRole('button',{name:'Export',exact:true}).click();const previous=(await read('export')).id;
  await page.locator('#export').getByRole('button',{name:'Render WAV',exact:true}).click();let job;
  await until(async()=>{job=await read('export');assert.notEqual(job.state,'failed',JSON.stringify(job));return job.id!==previous&&['succeeded','shortfall'].includes(job.state)},'WAV export');
  const response=await page.request.get(new URL(job.downloadURL,origin).href);assert.equal(response.status(),200);const wav=await response.body();assert.equal(wav.subarray(0,4).toString(),'RIFF');fs.writeFileSync(path.join(proof,filename),wav);await page.keyboard.press('Escape');return wav;
 }
 async function captureViews(){
  for(const close of await page.locator('[data-gosx-toast-host] button').all())await close.click();
  await until(async()=>!/connecting/i.test(await page.locator('[data-placement-playback-status]').innerText()),'desktop timeline playback status');
  await page.screenshot({path:path.join(proof,'arrangement-desktop.png')});
  const mobile=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,storageState:await context.storageState()}),phone=await mobile.newPage();
  phone.on('pageerror',error=>errors.push(error.message));
  await phone.goto(origin+'/studio?placement=one');await phone.locator('#studio-shell[data-shell-actions=ready]').waitFor();
  await until(async()=>!/connecting/i.test(await phone.locator('[data-placement-playback-status]').innerText()),'phone timeline playback status');
  assert.equal(await phone.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  await phone.screenshot({path:path.join(proof,'arrangement-phone.png')});await mobile.close();
 }
 await page.goto(startup.href);await page.goto(origin+'/studio?placement=one');await ready();
 assert.equal((await read('audio/config')).options.inputEnabled,false);
 assert.equal((await read('state')).source,entry);assert.equal(await block('one').count(),1);
 if(process.env.CICADA_ARRANGEMENT_REOPEN==='1'){
  const expected=JSON.parse(fs.readFileSync(path.join(proof,'report.json'),'utf8'));assert.equal(owner(),expected.finalOwner);
  const wav=await render('reopened.wav');assert.equal(digest(wav),expected.wavSHA256);assert.deepEqual(errors,[]);
  fs.writeFileSync(path.join(proof,'reopen-report.json'),JSON.stringify({wavSHA256:digest(wav),sourceUnchanged:true,errors},null,2)+'\n');await captureViews();completed=true;return;
 }
 assert.equal(owner(),arrangement);const baseline=await render('baseline.wav');await select('one');
 const initialVersion=await action('update','one').locator('[name=source_revision]').inputValue();assert.ok(initialVersion);
 await edit(action('update','one'),{at:'@2.1.1',length:'3bars'},'Update placement');await saved(source=>source.includes('at = @2.1.1 length = 3bars'));
 await until(async()=>await block('one').getAttribute('data-placement-at')==='3840','timeline follows secondary save');const moved=owner();
 assert.notEqual(await action('update','one').locator('[name=source_revision]').inputValue(),initialVersion);
 await block('one').locator('.placement-select').focus();await page.keyboard.press('ArrowLeft');await saved(source=>source.includes('at = 3600ticks')||source.includes('at = @1.4.4'));const nudged=owner();
 await undo(moved);await undo(arrangement);
 for(const expected of [moved,nudged]){await page.locator('#redo-form button').click();await saved(source=>source===expected)}await undo(moved);await undo(arrangement);
 checks.push('move/trim and keyboard nudge update the owning file; Undo/Redo preserve entry and exact CRLF bytes');
 await select('one');await inspector('one').locator('.placement-copy > summary').click();
 await edit(action('duplicate','one'),{newName:'one-copy',at:'@9.1.1'},'Duplicate placement');await saved(source=>source.includes('place one-copy'));const copied=owner();
 await inspector('one-copy').locator('summary').first().click();await inspector('one-copy').getByRole('button',{name:'Remove placement',exact:true}).click();await saved(source=>!source.includes('place one-copy'));await undo(copied);await undo(arrangement);
 await page.locator('.placement-management > summary').filter({hasText:'Add a placement'}).click();await edit(action('append'),{newName:'ending',at:'@9.1.1',length:'1bar'},'Add placement');await saved(source=>source.includes('place ending'));await undo(arrangement);
 await select('one');await inspector('one').getByRole('button',{name:'Remove placement',exact:true}).click();await saved(source=>!source.includes('place one bass'));await undo(arrangement);
 checks.push('copy, remove and add placements preserve comments and undo together');
 const markers=page.locator('.placement-management').filter({has:page.locator('summary').filter({hasText:'Edit markers'})});
 if(await markers.getAttribute('open')===null)await markers.locator('summary').first().click();
 const chorus=page.locator('form[action$="/__actions/arrange"]:has(input[name=name][value=chorus])');
 await edit(chorus,{at:'@6.1.1'},'Move marker');await saved(source=>source.includes('marker chorus { at = @6.1.1 }'));await undo(arrangement);
 await edit(action('append-marker'),{newName:'bridge',at:'@9.1.1'},'Add marker');await saved(source=>source.includes('marker bridge'));await undo(arrangement);
 await edit(chorus,{},'Remove marker');await saved(source=>!source.includes('marker chorus'));await undo(arrangement);
 checks.push('marker move, add and remove target the secondary arrangement and restore exactly');
 assert.ok((await render('restored.wav')).equals(baseline),'restored project WAV differs');
 await select('one');await captureViews();
 const external=arrangement+'// Another editor saved this.\r\n';let intercepted=false;
 await page.route('**/__actions/arrange',async route=>{if(!intercepted){intercepted=true;fs.writeFileSync(fixture.arrangement,external)}await route.continue()});
 await edit(action('update','one'),{at:'@2.1.1',length:'3bars'},'Update placement');
 await until(async()=>intercepted&&/saved score files or libraries changed/i.test(await page.locator('body').innerText()),'unseen secondary save rejected');await ready();assert.equal(owner(),external);assert.equal((await read('state')).source,entry);
 checks.push('a secondary save after payload capture is rejected without overwriting it');assert.deepEqual(errors,[]);
 fs.writeFileSync(path.join(proof,'report.json'),JSON.stringify({checks,errors,finalOwner:external,wavSHA256:digest(baseline)},null,2)+'\n');completed=true;console.log(checks.join('\n'));
});
