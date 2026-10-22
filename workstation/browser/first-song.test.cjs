// Requires a fresh disposable `cicada new first-light` project and --audio null.
// Run again after restarting the same project with CICADA_SONG_REOPEN=1.
'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const reopening=process.env.CICADA_SONG_REOPEN==='1';
test(reopening?'reopen the song and reproduce its WAV':'compose, arrange and export a song through Studio controls',{timeout:180000},async t=>{
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright'),proof=process.env.CICADA_SONG_PROOF_DIR;assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH});t.after(()=>browser.close());
 const page=await browser.newPage({viewport:{width:1280,height:840},acceptDownloads:true}),errors=[],steps=[];
 page.on('pageerror',e=>errors.push(e.message));const writes=[];page.on('request',request=>{if(request.method()==='POST')writes.push(new URL(request.url()).pathname);});
 const origin=new URL(process.env.CICADA_STUDIO_URL);await page.goto(origin.href);await page.goto(new URL('/studio',origin).href);
 const until=async(check,label)=>{const end=Date.now()+40000;while(Date.now()<end){if(await check())return;await new Promise(r=>setTimeout(r,40));}assert.fail(label);};
 const state=async()=>{const r=await page.request.get(new URL('/api/state',origin).href);assert.equal(r.status(),200);return r.json();};
 const ready=()=>page.locator('#studio-shell[data-shell-actions=ready][data-shell-layout=ready]').waitFor();await ready();
 const saved=async(check,label)=>{await until(async()=>{const s=await state();return s.valid&&check(s.source)&&(await page.locator('#undo-form input[name=revision]').inputValue())===s.revision;},label);await ready();steps.push(label);return state();};
 const settings=()=>page.locator('#arrangement form[action$="/__actions/project"]');
 async function projectSettings(title,tempo){await page.locator('#view-tab-arrange').click();const details=page.locator('#arrangement details').filter({has:page.getByText('Project settings',{exact:true})});if(!(await details.evaluate(node=>node.open)))await details.locator('summary').click();await settings().locator('[name=title]').fill(title);await settings().locator('[name=tempo]').fill(String(tempo));await settings().getByRole('button',{name:'Save project settings',exact:true}).click();await saved(s=>s.includes('title "'+title+'"')&&s.includes('tempo '+tempo),'title and tempo saved at '+tempo+' BPM');}
 if(reopening){
  const expected=JSON.parse(fs.readFileSync(path.join(proof,'song-report.json'),'utf8'));
  const reopened=await state();assert.equal(reopened.source,expected.source);assert.equal(reopened.revision,expected.revision);
  const tracks=await page.locator('#arrangement .timeline-track').allTextContents();
  for(const name of ['bass','drums','keys'])assert.ok(tracks.some(text=>text.includes(name)),name+' reopens in the timeline');
  await page.locator('#transport-form button[value=play]').click();
  await until(async()=>(await(await page.request.get(new URL('/api/transport',origin).href)).json()).playing,'reopened song starts');
  await page.locator('#transport-form button[value=stop]').click();
  await until(async()=>!(await(await page.request.get(new URL('/api/transport',origin).href)).json()).playing,'reopened song stops');
  const render=await exportWave('first-light-reopened.wav');
  assert.ok(fs.readFileSync(path.join(proof,'first-light.wav')).equals(fs.readFileSync(path.join(proof,'first-light-reopened.wav'))),'reopening reproduces the complete WAV byte for byte');
  fs.writeFileSync(path.join(proof,'reopen-report.json'),JSON.stringify({revision:reopened.revision,render,byteIdentical:true,errors},null,2)+'\n');
  assert.deepEqual(errors,[]);return;
 }
 const initial=await state();assert.ok(initial.source.includes('title \"first-light\"')&&initial.source.includes('track bass acid')&&initial.source.includes('track drums drums'),'use the untouched disposable first-light template');
 const audio=await(await page.request.get(new URL('/api/audio/config',origin).href)).json();assert.equal(audio.options.inputEnabled,false);assert.equal(String(audio.inventory.backendName||audio.inventory.backend).toLowerCase(),'null');
 await page.locator('#studio-code > details > summary').click();
 await projectSettings('First Light',108);
 const patch=page.locator('[data-instrument-patch="warm-pad"]');await patch.locator('summary').click();await patch.locator('[name=newName]').fill('first-light-keys');await patch.locator('[name=track]').fill('keys');await patch.getByRole('button',{name:'Add instrument and track',exact:true}).click();
 await saved(s=>s.includes('track keys first-light-keys'),'keys sound added to the song');
 const create=page.locator('[data-gosx-key="create-pattern-keys"]');await create.locator('summary').click();await create.locator('[name=newName]').fill('light-chords');await create.locator('[name=starter]').selectOption('chords');await create.locator('[name=scene]').selectOption('main');await create.getByRole('button',{name:'Create pattern',exact:true}).click();
 await saved(s=>s.includes('pattern light-chords notes steps=16')&&s.includes('keys = light-chords'),'editable held chords assigned to the first section');
 await page.locator('#detail-tab-clip').click();const h=page.getByRole('separator',{name:'Resize Detail dock',exact:true});await h.focus();await page.keyboard.press('End');
 await page.getByText('Variations and scene assignment',{exact:true}).click();
 const bind=page.locator('#patterns form:has(input[name=action][value=bind])');await bind.locator('[name=scene]').selectOption('break');await bind.locator('[name=track]').selectOption('keys');await bind.getByRole('button',{name:'Stop in this scene',exact:true}).click();
 await saved(s=>/scene break\s*\{[^}]*keys\s*=\s*off/.test(s),'keys drop out in the middle section');
 await page.locator('#view-tab-arrange').click();await h.focus();await page.keyboard.press('Home');
 for(const index of [0,1]){const block=page.locator('[data-song-index="'+index+'"]');await block.locator('summary').click();const form=block.locator('form:has(input[name=action][value=bars])');await form.locator('[name=bars]').fill('2');await form.getByRole('button',{name:'Set length',exact:true}).click();await saved(s=>new RegExp((index?'break':'main')+'\\*2').test(s),'section '+index+' set to two bars');}
 const append=page.locator('#arrangement form:has(input[name=action][value=append])');await append.locator('[name=scene]').selectOption('main');await append.locator('[name=bars]').fill('2');await append.getByRole('button',{name:'Add to arrangement',exact:true}).click();
 const song=await saved(s=>/song\s*\{\s*main\*2\s+break\*2\s+main\*2\s*\}/.test(s),'six-bar arrangement returns to the opening part');
 await page.screenshot({path:path.join(proof,'arranged-song.png')});
 await projectSettings('First Light',112);
 await page.locator('#undo-form button').click();await saved(s=>s===song.source,'Undo restores the exact arranged song');
 const job=await exportWave('first-light.wav');assert.equal(job.revision,song.revision);
 await projectSettings('First Light',112);
 await page.getByRole('toolbar',{name:'Transport'}).getByRole('button',{name:'Export',exact:true}).click();
 await until(async()=>/score has changed/.test(await page.locator('#export .export-result [data-gosx-live-bind=version]').textContent()),'Export identifies the earlier saved song');
 const earlier=await(await page.request.get(new URL('/api/export',origin).href)).json();assert.equal(earlier.id,job.id);assert.equal(earlier.revision,song.revision);assert.notEqual(earlier.current_source_revision,job.source_revision);
 const earlierDownload=page.waitForEvent('download');await page.getByRole('link',{name:'Download earlier render',exact:true}).click();await(await earlierDownload).saveAs(path.join(proof,'first-light-earlier.wav'));
 assert.ok(fs.readFileSync(path.join(proof,'first-light.wav')).equals(fs.readFileSync(path.join(proof,'first-light-earlier.wav'))),'editing the song does not alter its earlier WAV');
 await page.screenshot({path:path.join(proof,'earlier-render.png')});
 await page.setViewportSize({width:390,height:840});
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'version details fit a phone');
 await page.screenshot({path:path.join(proof,'earlier-render-phone.png')});
  const phoneDownload=page.waitForEvent('download');await page.getByRole('link',{name:'Download earlier render',exact:true}).click();await(await phoneDownload).saveAs(path.join(proof,'first-light-earlier-phone.wav'));
  assert.ok(fs.readFileSync(path.join(proof,'first-light.wav')).equals(fs.readFileSync(path.join(proof,'first-light-earlier-phone.wav'))),'the earlier WAV remains reachable and unchanged on a phone');
  await page.screenshot({path:path.join(proof,'earlier-render-phone-delivery.png')});
 await page.setViewportSize({width:1280,height:840});await page.keyboard.press('Escape');
 await page.locator('#undo-form button').click();await saved(s=>s===song.source,'Undo restores the song version that was rendered');
 await page.getByRole('toolbar',{name:'Transport'}).getByRole('button',{name:'Export',exact:true}).click();
 await until(async()=>/matches the saved score/.test(await page.locator('#export .export-result [data-gosx-live-bind=version]').textContent()),'Undo restores the matching-render label');
 assert.equal((await(await page.request.get(new URL('/api/export',origin).href)).json()).id,job.id,'Undo does not rerender the song');
 await page.keyboard.press('Escape');steps.push('saved edits label the earlier WAV, keep its bytes intact and return to a match after Undo');
 // The standalone panel has a separate GoSX workspace owner. It must also
 // leave export to the managed background action instead of patching the page.
 await page.goto(new URL('/?panel=export',origin).href);
 await page.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();
 const panel=await page.locator('#export').elementHandle();
 await page.locator('#export [name=target_lufs]').fill('-18');
 await page.locator('#export').getByRole('button',{name:'Render WAV',exact:true}).click();
 let standalone;await until(async()=>{standalone=await(await page.request.get(new URL('/api/export',origin).href)).json();assert.notEqual(standalone.state,'failed',JSON.stringify(standalone));return standalone.id!==job.id&&['succeeded','shortfall'].includes(standalone.state);},'standalone export completes');
 assert.equal(await panel.evaluate(node=>node.isConnected),true,'standalone export leaves its panel mounted');
 assert.equal(await page.locator('#export [name=target_lufs]').inputValue(),'-18');
 assert.equal(new URL(page.url()).searchParams.get('panel'),'export');
 assert.equal(writes.some(target=>target==='/__actions/source'||target==='/__actions/files'),false,'song was composed without score-text writes');
 steps.push('standalone Export renders without replacing its workspace');
 fs.writeFileSync(path.join(proof,'song-report.json'),JSON.stringify({steps,revision:song.revision,render:job,standalone,source:song.source,writes,errors},null,2)+'\n');
 assert.deepEqual(errors,[]);

 async function exportWave(filename){
  await page.getByRole('toolbar',{name:'Transport'}).getByRole('button',{name:'Export',exact:true}).click();
  const editor=await page.locator('#score textarea').elementHandle();
  await page.evaluate(()=>{window.__songExportNavigations=0;document.addEventListener('gosx:navigate',()=>window.__songExportNavigations++);});
  await page.locator('#export').getByRole('button',{name:'Render WAV',exact:true}).click();
  let job;await until(async()=>{job=await(await page.request.get(new URL('/api/export',origin).href)).json();assert.notEqual(job.state,'failed',JSON.stringify(job));return ['succeeded','shortfall'].includes(job.state);},'WAV render completes');
  assert.equal(await page.locator('#export-dialog').isVisible(),true,'Export remains open while the render completes');
  assert.equal(await page.evaluate(()=>document.querySelector('#studio-workbench').inert),true,'rendering keeps the modal background locked');
  assert.equal(await page.evaluate(()=>window.__songExportNavigations),0,'rendering never navigates');
  assert.equal(await editor.evaluate(node=>node.isConnected),true,'editor stays mounted during export');
  const delivery=page.locator('#export .export-result a[download]');await delivery.waitFor({state:'visible'});
  assert.match(job.source_revision,/^[a-f0-9]{64}$/,'export identifies its captured score files');
  assert.equal(job.current_source_revision,job.source_revision,'export identifies the current saved score');
  await until(async()=>/matches the saved score/.test(await page.locator('#export .export-result [data-gosx-live-bind=version]').textContent()),'completed render identifies the saved song');
  const downloadPromise=page.waitForEvent('download');await delivery.click();const download=await downloadPromise;await download.saveAs(path.join(proof,filename));
  steps.push('rendered WAV downloaded through Export');
  await page.screenshot({path:path.join(proof,reopening?'reopened-export.png':'finished-export.png')});
  await page.keyboard.press('Escape');assert.equal(await page.locator('#export-dialog').isVisible(),false);
  assert.equal(await page.evaluate(()=>document.querySelector('#studio-workbench').inert),false,'Escape unlocks the song');
  return job;
 }
});
