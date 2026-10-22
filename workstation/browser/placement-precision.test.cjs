/* Disposable Studio precision/copy interaction proof. Null audio; no microphone.
 * --write-fixture PATH writes the marked source; run against a fresh Studio.
 * CICADA_PRECISION_PROOF_DIR retains source-bound checks and actual screenshots. */
'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto');
const {createRequire}=require('node:module'),test=require('node:test');
const marker='cicada-precision-test: disposable fixture';
const wave=Buffer.alloc(44+48000*2);wave.write('RIFF');wave.writeUInt32LE(wave.length-8,4);wave.write('WAVEfmt ',8);wave.writeUInt32LE(16,16);wave.writeUInt16LE(1,20);wave.writeUInt16LE(1,22);wave.writeUInt32LE(48000,24);wave.writeUInt32LE(96000,28);wave.writeUInt16LE(2,32);wave.writeUInt16LE(16,34);wave.write('data',36);wave.writeUInt32LE(wave.length-44,40);
const waveHash=crypto.createHash('sha256').update(wave).digest('hex');
const fixture=`// ${marker}\n`+fs.readFileSync(path.join(__dirname,'../../examples/songs/tidal-glass.cicada'),'utf8').replace('track far pluck {','track far audio {').replace('place far-in far ripple {','place far-in far precision-clip {').replace('place far-open far return {','place far-open near return {').replace('place far-home far ripple {','place far-home far precision-clip {').replace('at = @5.2.1 length = 4bars','at = 16327ticks length = 1bars').replace('place far-open near return {','place far-open pattern-far return {')+`\ntrack pattern-far pluck { level = -24dB }\nasset precision-wave "precision.wav" { sha256="${waveHash}" format=wav frames=48000 rate=48000Hz channels=1 }\nclip precision-clip precision-wave { start=0frames end=48000frames }\n`;
if(process.argv[2]==='--write-fixture'){fs.writeFileSync(path.join(path.dirname(process.argv[3]),'precision.wav'),wave,{flag:'wx'});fs.writeFileSync(process.argv[3],fixture,{flag:'wx'});process.exit(0)}
function playwright(){const p=process.env.CICADA_PLAYWRIGHT_MODULE;return p?createRequire(path.resolve(p,'package.json'))(p):require('playwright')}
async function until(fn,label){const end=Date.now()+15000;while(Date.now()<end){if(await fn())return;await new Promise(r=>setTimeout(r,40))}assert.fail(label)}
test('placement precision and copy gestures retain musical offsets and commit once',{timeout:180000},async()=>{
 const base=process.env.CICADA_STUDIO_URL;assert.ok(base&&['127.0.0.1','localhost','[::1]'].includes(new URL(base).hostname));
 const browser=await playwright().chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox']});
 const page=await browser.newPage({viewport:{width:1440,height:1000},hasTouch:true,reducedMotion:'reduce'});page.setDefaultTimeout(15000);
 const errors=[],posts=[],checks=[],proof=process.env.CICADA_PRECISION_PROOF_DIR;
 const check=label=>{checks.push(label);console.log(label)};
 page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(r.method()==='POST'&&new URL(r.url()).pathname==='/__actions/arrange')posts.push(r)});
 const state=async()=>(await page.request.get(base+'/api/state')).json(),block=(id='far-in')=>page.locator(`[data-placement-block="${id}"]`);
 const ready=async()=>{await until(()=>page.evaluate(()=>document.documentElement.getAttribute('data-gosx-navigation-state')==='idle'&&!document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]')),'managed navigation idle');await page.locator('[data-shell-placement-editor=ready]').waitFor();await page.locator('[data-placement-navigation-ready]').waitFor();await until(async()=>await page.locator('#undo-form input[name=revision]').inputValue()===(await state()).revision,'fresh revision')};
 let baseline,baselineRevision;
 const undo=async()=>{await page.getByRole('button',{name:'Undo',exact:true}).click();await until(async()=>(await state()).source===baseline,'exact Undo');await ready();assert.equal((await state()).revision,baselineRevision)};
 const value=async(attr,n,id='far-in')=>{await until(async()=>await block(id).getAttribute('data-placement-'+attr)===String(n),`${id} ${attr} ${n}`);await ready()};
 const choose=async(value)=>page.getByLabel('Snap step',{exact:true}).selectOption(value);
 const coordinates=async(resize=false,id='far-in')=>{const control=block(id).locator(resize?'[data-placement-resize]':'.placement-select');await control.scrollIntoViewIfNeeded();const b=await control.boundingBox();return {x:b.x+(resize?b.width/2:Math.min(20,b.width/3)),y:b.y+b.height/2}};
 const gesture=async({dx=96,alt=false,shift=false,resize=false,hold=false,id='far-in'}={})=>{await page.getByRole('button',{name:'Show selected',exact:true}).click();const {x,y}=await coordinates(resize,id);if(alt)await page.keyboard.down('Alt');if(shift)await page.keyboard.down('Shift');await page.mouse.move(x,y);await page.mouse.down();await page.mouse.move(x+dx,y,{steps:5});if(!hold){await page.mouse.up();if(alt)await page.keyboard.up('Alt');if(shift)await page.keyboard.up('Shift')}};
 try{
  await page.goto(base+'/studio?tab=arrange&placement=far-in');await ready();baseline=(await state()).source;baselineRevision=(await state()).revision;assert.ok(baseline.includes(marker));
  await page.getByLabel('Timeline zoom',{exact:true}).selectOption('96');
  for(const step of [3840,960,480,320,240,160,120,1]){await choose(String(step));const count=posts.length;await block().locator('.placement-select').focus();await page.keyboard.press('ArrowRight');await value('at',16327+step);assert.equal(posts.length,count+1);await undo();assert.equal(await page.getByLabel('Snap step',{exact:true}).inputValue(),String(step))}
  check('all eight snap increments nudge an off-grid phrase once and preserve its seven-tick offset; one Undo restores exact source/revision');
  await choose('320');let count=posts.length;await gesture({dx:17});await value('at',16967);assert.equal(posts.length,count+1);await undo();
  count=posts.length;await gesture({dx:-17,resize:true});await value('length',3200);assert.equal(posts.length,count+1);await undo();
  check('triplet drag and trim quantize movement rather than the original musical offset');
  await choose('3840');count=posts.length;await gesture({dx:37,shift:true});await value('at',17807);assert.equal(posts.length,count+1);await undo();
  await choose('1');await block().locator('.placement-select').focus();await page.keyboard.press('Shift+ArrowRight');await value('at',20167);await undo();
  check('Shift-drag bypasses coarse snap at exact ticks; Shift-arrow remains one bar with snap Off');
  await choose('240');count=posts.length;await gesture({alt:true,hold:true});
  assert.equal(await page.locator('.placement-copy-origin').count(),1);assert.equal(await page.locator('[data-placement-copy-preview]').count(),1);assert.equal(await block().getAttribute('data-placement-at'),'16327');assert.equal((await state()).source,baseline);assert.equal(posts.length,count);
  await page.keyboard.press('ArrowRight');await page.keyboard.press('Control+d');assert.equal(posts.length,count);assert.equal((await state()).source,baseline);
  if(proof){fs.mkdirSync(proof,{recursive:true});await page.screenshot({path:path.join(proof,'copy-preview-1440.png')})}
  await page.mouse.up();await page.keyboard.up('Alt');await value('at',20167,'far-in-copy');assert.equal(posts.length,count+1);assert.equal(await block().getAttribute('data-placement-at'),'16327');assert.equal(await block('far-in-copy').getAttribute('data-placement-length'),'3840');assert.equal(await block('far-in-copy').getAttribute('data-placement-selected'),'true');
  await until(()=>block('far-in-copy').locator('.placement-select').evaluate(e=>e===document.activeElement),'copy focus');await undo();
  check('Alt-drag previews original and copy without writes; held arrow/duplicate cannot race; release saves one selected, focused copy with exact Undo');
  count=posts.length;await gesture({alt:true,hold:true});await page.keyboard.press('Escape');await page.mouse.up();await page.keyboard.up('Alt');await new Promise(r=>setTimeout(r,150));assert.equal(posts.length,count);assert.equal((await state()).source,baseline);assert.equal(await page.locator('.placement-copy-origin,[data-placement-copy-preview],[data-placement-dragging]').count(),0);
  await gesture({dx:2,alt:true});await ready();assert.equal(posts.length,count);assert.equal((await state()).source,baseline);
  check('Escape and pointer jitter leave no copy, preview, gesture marker, score write or Undo entry');
  // A rejected or edited inspector draft cannot rename the gesture copy.
  await page.locator('[data-placement-inspector=far-in] .placement-copy > summary').click();
  await page.locator('[data-placement-inspector=far-in] input[name=newName]').fill('stale-draft-name');
  await page.getByLabel('Copy on drag',{exact:true}).check();count=posts.length;await gesture();assert.equal(posts.length,count);assert.equal((await state()).source,baseline);await page.locator('[data-placement-inspector=far-in] input[name=newName]').fill('far-in-copy');await gesture();await value('at',20167,'far-in-copy');assert.equal(await block('stale-draft-name').count(),0);assert.equal(posts.length,count+1);await undo();
  assert.equal(await page.getByLabel('Copy on drag',{exact:true}).isChecked(),true);
  check('dirty copy-name draft blocks a gesture; restored form uses the canonical unique name and Copy drag persists through save/Undo');
  count=posts.length;await gesture({dx:-24,resize:true});await value('length',2880);assert.equal(posts.length,count+1);assert.equal(await block('far-in-copy').count(),0);await undo();
  check('Copy mode keeps edge gestures as trims');
  await block('far-open').locator('.placement-select').click();await ready();
  await choose('1');count=posts.length;await block('far-open').locator('.placement-select').focus();await page.keyboard.press('ArrowRight');await value('at',47280,'far-open');assert.equal(posts.length,count+1);await undo();await choose('240');
  count=posts.length;await gesture({dx:48,id:'far-open'});await until(async()=>posts.length===count+1&&await page.locator('[data-gosx-form-state=error]').count()>0,'overlap rejected');assert.equal((await state()).source,baseline);assert.equal(await block('far-in-copy').count(),0);assert.equal(await page.locator('.placement-copy-origin,[data-placement-dragging]').count(),0);
  check('overlapping pattern copy returns a real form error, leaves the exact source and cleans up the preview');
  // Recover via the normal selection GET and verify protected Code/pending contexts.
  await block().locator('.placement-select').click();await ready();
  const editor=page.getByRole('textbox',{name:'Cicada score',exact:true}),clean=await editor.inputValue();await editor.fill(clean+'\n// vocal draft\n');count=posts.length;await gesture();assert.equal(posts.length,count);assert.equal((await state()).source,baseline);await editor.fill(clean);await ready();
  let release,entered=false;const gate=new Promise(r=>release=r);await page.route('**/__actions/arrange',async route=>{entered=true;await gate;await route.continue()});await gesture();await until(()=>entered,'pending copy was not submitted: '+await page.evaluate(()=>JSON.stringify({status:document.querySelector('[data-placement-edit-status]')?.textContent,copy:document.querySelector('[name=placementCopy]')?.checked,edited:[...document.querySelectorAll('[data-edited=true]')].map(n=>({name:n.name,value:n.value,baseline:n.dataset.baseline,default:n.defaultValue})),navigation:window.__gosx?.navigation?.state}))).catch(e=>{release();throw e});count=posts.length;await gesture();assert.equal(posts.length,count);release();await value('at',20167,'far-in-copy');await page.unroute('**/__actions/arrange');await undo();
  check('unsaved Code and a real pending copy block additional gesture writes');
  await page.getByRole('tab',{name:'Session',exact:true}).click();await page.getByRole('tab',{name:'Arrange',exact:true}).click();assert.equal(await page.getByLabel('Copy on drag',{exact:true}).isChecked(),true);assert.equal(await page.getByLabel('Snap step',{exact:true}).inputValue(),'240');
  await page.setViewportSize({width:390,height:844});await page.getByRole('link',{name:'Center',exact:true}).click();await page.getByLabel('Timeline zoom',{exact:true}).selectOption('48');await page.getByRole('button',{name:'Show selected',exact:true}).click();
  const {x,y}=await coordinates();const cdp=await page.context().newCDPSession(page);count=posts.length;await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x,y}]});await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:x+48,y}]});
  if(proof)await page.screenshot({path:path.join(proof,'copy-preview-390.png')});await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});await value('at',20167,'far-in-copy');assert.equal(posts.length,count+1);await undo();
  const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth);assert.equal(overflow,false);assert.equal(await page.locator('.placement-copy-origin,[data-placement-dragging]').count(),0);
  check('390px touch copy saves once with exact Undo; preferences survive Arrange/Session and no page overflow');
  await page.getByLabel('Copy on drag',{exact:true}).uncheck();await choose('240');await page.getByLabel('Timeline zoom',{exact:true}).selectOption('48');assert.equal((await state()).source,baseline);assert.deepEqual(errors,[]);
  await page.setViewportSize({width:1440,height:1000});await page.goto(base+'/?panel=session&placement=far-in');await page.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();await page.getByLabel('Timeline zoom',{exact:true}).selectOption('96');await page.getByLabel('Copy on drag',{exact:true}).check();count=posts.length;await gesture();await until(async()=>await block('far-in-copy').count()===1&&await block('far-in-copy').getAttribute('data-placement-at')==='20167','panel copy saved');assert.equal(posts.length,count+1);assert.equal(await block('far-in-copy').getAttribute('data-placement-selected'),'true');await until(()=>block('far-in-copy').locator('.placement-select').evaluate(e=>e===document.activeElement),'panel copy focused');await page.getByRole('button',{name:'Undo',exact:true}).click();await until(async()=>(await state()).source===baseline,'panel exact Undo');await page.getByLabel('Copy on drag',{exact:true}).uncheck();
  check('panel Session uses the same guarded copy gesture, selects and focuses the new copy, and exact Undo restores the song');
  if(proof)fs.writeFileSync(path.join(proof,'report.json'),JSON.stringify({checks,errors,arrangePOSTs:posts.length,sourceSHA256:crypto.createHash('sha256').update(baseline).digest('hex'),finalRevision:(await state()).revision,platform:process.platform,browser:browser.version(),physicalAudioVerified:false},null,2));
 }catch(error){if(proof){fs.mkdirSync(proof,{recursive:true});fs.writeFileSync(path.join(proof,'failure.json'),JSON.stringify({error:String(error),checks,posts:posts.map(r=>r.postData()),status:await page.locator('[data-placement-edit-status]').innerText()},null,2));await page.screenshot({path:path.join(proof,'failure.png')})}throw error}finally{await browser.close()}
});
