/* Project-wide track rename against a disposable owner-session Studio. */
'use strict';
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path'), test = require('node:test');
const entry = `// track rename fixture: entry
cicada 2
title "Track rename"
track bass acid { level=-18dB }
track lead acid {}
pattern pulse { c3 . g3 . }
midi { port keys; keys ch 1 -> bass }
`;
const secondary = `// Keep these notes.\r\nscene verse { bass=pulse lead=off }\r\nsong { verse*4 }\r\n`;
if (process.argv[2] === '--write-fixture') {
  const dir = path.resolve(process.argv[3]); fs.mkdirSync(dir,{recursive:true});
  for (const [name, text] of Object.entries({'main.cicada':entry, 'sections.cicada':secondary, 'cicada.mod':'project track-rename\ncicada 2\nentry "main.cicada"\nsource "sections.cicada"\n', 'cicada.local':'port keys = "Synthetic Keyboard"\n'})) fs.writeFileSync(path.join(dir,name),text,{flag:'wx'});
  process.exit(0);
}
async function until(check, message) { const end=Date.now()+20000; while(Date.now()<end){if(await check()) return; await new Promise(resolve=>setTimeout(resolve,40));} assert.fail(message); }
test('track rename updates project references, guards conflicts, and is one Undo', {timeout:120000}, async t => {
  const origin = new URL(process.env.CICADA_STUDIO_URL); assert.equal(origin.hostname,'127.0.0.1');
  const dir=process.env.CICADA_TRACK_RENAME_DIR; assert.ok(path.isAbsolute(dir),'use a disposable absolute fixture directory');
  const file=path.join(dir,'main.cicada'), part=path.join(dir,'sections.cicada');
  assert.equal(fs.readFileSync(file,'utf8'),entry); assert.equal(fs.readFileSync(part,'utf8'),secondary);
  const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox','--mute-audio']}); t.after(()=>browser.close());
  const page=await browser.newPage({viewport:{width:1440,height:1000}}), errors=[], notes=[], renameWrites=[];
  page.on('pageerror',error=>errors.push(error.message));
  page.on('request',request=>{ if(request.method()==='POST' && new URL(request.url()).pathname==='/__actions/track-rename') renameWrites.push(request); if(request.method()==='POST' && new URL(request.url()).pathname==='/__actions/live') try { const data=request.postDataJSON(); if(data.type==='input-batch') notes.push(...JSON.parse(data.messages).filter(n=>n.type==='note')); } catch {} });
  await page.addInitScript(()=>{
    const access=new EventTarget(), port=new EventTarget(); Object.assign(port,{id:'keys',name:'Synthetic Keyboard',state:'connected'}); access.inputs=new Map([['keys',port]]);
    window.__sendRenameMIDI=(...bytes)=>{ const e=new Event('midimessage'); Object.defineProperty(e,'data',{value:new Uint8Array(bytes)}); port.dispatchEvent(e); };
    Object.defineProperty(navigator,'requestMIDIAccess',{configurable:true,value:()=>Promise.resolve(access)});
  });
  const state=async()=> (await page.request.get(new URL('/api/state',origin).href)).json();
  const ready=async()=>{await page.locator('#studio-shell[data-shell-actions=ready]').waitFor(); await until(async()=>await page.locator('#undo-form input[name=revision]').inputValue()===(await state()).revision,'revision did not settle');};
  await page.goto(new URL('/studio',origin).href); await ready();
  await page.locator('#view-tab-session').click(); await page.locator('[data-live-control=midi]').click();
  await page.waitForFunction(()=>document.querySelector('[data-live-status]')?.textContent.includes('MIDI enabled'));
  await page.locator('#transport-form button[value=play]').click(); await page.waitForFunction(()=>!document.querySelector('[data-live-control=record]').disabled);
  await page.locator('#view-tab-arrange').click();
  await page.evaluate(()=>{ window.__renameLive=document.querySelector('#cicada-live'); window.__renameDocument=document; __sendRenameMIDI(0x90,60,100); });
  await until(()=>notes.some(n=>n.on==='true' && n.note==='60'),'MIDI note-on was not delivered');
  const summary=page.locator('.timeline-track .rail-rename summary[aria-label="Rename bass"]');
  await summary.click();
  if (process.env.CICADA_TRACK_RENAME_PROOF_DIR) { fs.mkdirSync(process.env.CICADA_TRACK_RENAME_PROOF_DIR,{recursive:true}); await summary.locator('..').screenshot({path:path.join(process.env.CICADA_TRACK_RENAME_PROOF_DIR,'track-rename.png')}); }
  const form=summary.locator('..').locator('form');
  await form.getByLabel('New name for bass').fill('low');
  // Managed navigation may rewrite the form action as an absolute URL.
  await form.evaluate(node=>node.setAttribute('action',new URL(node.getAttribute('action'),location.href).href));
  // The server refuses a rename while a live note can still be sounding.
  await form.getByRole('button',{name:'Rename track'}).click();
  await until(async()=>/Finish or discard unsaved performances and recordings before renaming/i.test(await page.locator('body').innerText()),'held-note refusal not shown');
  assert.equal(renameWrites.length,0,'held note must block submission');
  assert.equal(fs.readFileSync(file,'utf8'),entry); assert.equal(fs.readFileSync(part,'utf8'),secondary);
  assert.equal(await page.evaluate(()=>window.__renameLive===document.querySelector('#cicada-live') && window.__renameDocument===document),true,'refused rename remounted Live');
  assert.equal(notes.some(n=>n.on==='false' && n.note==='60'),false,'refused rename released held MIDI');
  const released=page.waitForResponse(response=>{if(!response.url().includes('/__actions/live')) return false; try {const data=response.request().postDataJSON(); return data.type==='input-batch' && JSON.parse(data.messages).some(n=>n.type==='note' && n.on==='false' && n.note==='60');} catch {return false;}});
  await page.evaluate(()=>__sendRenameMIDI(0x80,60,0)); assert.equal((await released).status(),200,'note-off must reach the playing track');
  const blocked=page.waitForResponse(response=>response.url().includes('/__actions/track-rename'));
  await form.getByRole('button',{name:'Rename track'}).click();
  assert.equal((await blocked).status(),409,'playing transport must refuse rename');
  await until(async()=>/Stop playback and finish recording before renaming/i.test(await page.locator('body').innerText()),'playing refusal not shown');
  await page.locator('#transport-form button[value=stop]').click();
  await until(async()=>!(await (await page.request.get(new URL('/api/transport',origin).href)).json()).playing,'transport did not stop');
  const retry=page.locator('[data-workspace-retry]'); if(await retry.isVisible()) { await retry.click(); await until(async()=>!(await retry.isVisible()),'refresh did not finish'); }
  await ready();
  // A score draft must remain intact while the rename is applied.
  if (!(await page.locator('#studio-code > details').evaluate(node=>node.open))) await page.locator('.shell-pane-toggle[data-pane=code][data-pane-action=show]').click();
  const code=page.getByRole('textbox',{name:'Cicada score',exact:true});
  const originalCode=await code.inputValue(); await code.fill(originalCode+'// unsaved composer draft\n');
  const currentSummary=page.locator('.timeline-track .rail-rename summary[aria-label="Rename bass"]');
  if (await currentSummary.locator('..').getAttribute('open')===null) await currentSummary.click();
  const currentForm=currentSummary.locator('..').locator('form');
  const otherSummary=page.locator('.timeline-track .rail-rename summary[aria-label="Rename lead"]');
  await otherSummary.click();
  const otherDraft=otherSummary.locator('..').getByLabel('New name for lead'); await otherDraft.fill('upper');
  await currentForm.getByLabel('New name for bass').fill('low');
  await currentForm.getByRole('button',{name:'Rename track'}).click();
  await until(()=>fs.readFileSync(file,'utf8').includes('track low acid') && fs.readFileSync(part,'utf8').includes('low=pulse'),'rename not written across files');
  assert.equal(await code.inputValue(),originalCode+'// unsaved composer draft\n','rename lost unsaved score work');
  assert.equal(await otherDraft.inputValue(),'upper','rename lost another track’s pending name');
  await otherDraft.fill('lead');
  await code.fill(originalCode);
  // An unfinished performance must survive a history gesture after Stop.
  await page.reload(); await ready();
  await page.locator('#view-tab-session').click(); await page.locator('[data-live-control=midi]').click();
  await page.waitForFunction(()=>document.querySelector('[data-live-status]')?.textContent.includes('MIDI enabled'));
  await page.locator('#transport-form button[value=play]').click();
  await page.waitForFunction(()=>!document.querySelector('[data-live-control=record]').disabled);
  await page.locator('[data-live-control=record]').click();
  await page.evaluate(()=>__sendRenameMIDI(0x90,62,100));
  await until(()=>notes.some(n=>n.on==='true' && n.note==='62'),'recorded MIDI note-on was not delivered');
  await page.evaluate(()=>__sendRenameMIDI(0x80,62,0));
  await until(()=>notes.some(n=>n.on==='false' && n.note==='62'),'recorded MIDI note-off was not delivered');
  await page.locator('#transport-form button[value=stop]').click();
  await until(async()=>!(await (await page.request.get(new URL('/api/transport',origin).href)).json()).playing,'transport did not stop');
  await page.locator('#view-tab-arrange').click();
  const protectedUndo=page.waitForResponse(response=>response.url().includes('/__actions/undo'));
  await page.locator('#undo-form button[type=submit]').click();
  assert.equal((await protectedUndo).status(),409,'unfinished take must block track-changing history');
  await until(async()=>/Finish or discard unsaved performances and recordings before changing track names through history/i.test(await page.locator('body').innerText()),'unfinished take refusal not shown');
  assert.ok(fs.readFileSync(file,'utf8').includes('track low acid'));
  await page.locator('#view-tab-session').click();
  await page.locator('[data-live-control=finish]').click();
  await page.locator('.note-preview').waitFor({timeout:15000});
  await page.locator('#view-tab-arrange').click();
  const retainedSummary=page.locator('.timeline-track .rail-rename summary[aria-label="Rename low"]');
  if (await retainedSummary.locator('..').getAttribute('open')===null) await retainedSummary.click();
  const retainedForm=retainedSummary.locator('..').locator('form');
  await retainedForm.getByLabel('New name for low').fill('lower');
  const retainedRename=page.waitForResponse(response=>response.url().includes('/__actions/track-rename'));
  await retainedForm.getByRole('button',{name:'Rename track'}).click();
  assert.equal((await retainedRename).status(),409,'retained take must block another track rename');
  const retainedRetry=page.locator('[data-workspace-retry]');
  if (await retainedRetry.isVisible()) { await retainedRetry.click(); await until(async()=>!(await retainedRetry.isVisible()),'refresh after retained rename did not finish'); }
  const retainedUndo=page.waitForResponse(response=>response.url().includes('/__actions/undo'));
  await page.locator('#undo-form button[type=submit]').click();
  assert.equal((await retainedUndo).status(),409,'retained take must block track-changing history');
  await page.locator('#view-tab-session').click();
  await page.locator('.note-preview button[value=discard]').click();
  await page.locator('.note-preview').waitFor({state:'hidden'});
  // History's managed Revert sends JSON through the same protected queue.
  const history=(await (await page.request.get(new URL('/api/history',origin).href)).json()).edits;
  const renameEntry=history.find(item=>item.diff.includes('track low acid'));
  assert.ok(renameEntry,'rename history entry is available');
  await page.locator('[aria-controls=history-dialog]').click();
  const restore=page.locator('#history-dialog form[action$="/__actions/revert"]').filter({has:page.locator(`input[name=id][value="${renameEntry.id}"]`)});
  await restore.locator('..').locator('summary').click();
  const revertResponse=page.waitForResponse(response=>response.url().includes('/__actions/revert'));
  await restore.getByRole('button',{name:'Restore this revision'}).click();
  assert.equal((await revertResponse).status(),200,'managed JSON Revert should succeed');
  await until(()=>fs.readFileSync(file,'utf8')===entry && fs.readFileSync(part,'utf8')===secondary,'Revert did not restore both files');
  await page.locator('#history-dialog').waitFor({state:'hidden'});
  await page.locator('#undo-form button[type=submit]').click();
  await until(()=>fs.readFileSync(file,'utf8').includes('track low acid'),'Undo of Revert did not restore rename');
  await ready();
  await page.locator('#undo-form button[type=submit]').click();
  await until(()=>fs.readFileSync(file,'utf8')===entry && fs.readFileSync(part,'utf8')===secondary,'one Undo did not restore both exact files'); await ready();
  await page.reload(); await ready();
  await page.locator('#view-tab-arrange').click();
  const open=async()=>{const s=page.locator('.timeline-track .rail-rename summary[aria-label="Rename bass"]'); await s.click(); return s.locator('..').locator('form');};
  let invalid=await open(); await invalid.getByLabel('New name for bass').fill('lead');
  const duplicateResponse=page.waitForResponse(response=>response.url().includes('/__actions/track-rename'));
  await invalid.getByRole('button',{name:'Rename track'}).click();
  assert.equal((await duplicateResponse).status(),422);
  await until(async()=>/already|taken|exists/i.test(await page.locator('body').innerText()),'duplicate name refusal not shown');
  assert.equal(fs.readFileSync(file,'utf8'),entry); assert.equal(fs.readFileSync(part,'utf8'),secondary);
  // A secondary source changed after form capture must never be overwritten.
  const external=secondary+'// external edit\r\n'; let intercepted=false;
  await page.route('**/__actions/track-rename',async route=>{if(!intercepted){intercepted=true;fs.writeFileSync(part,external)} await route.continue()});
  await invalid.getByLabel('New name for bass').fill('low');
  const conflictResponse=page.waitForResponse(response=>response.url().includes('/__actions/track-rename'));
  await invalid.getByRole('button',{name:'Rename track'}).click();
  assert.equal((await conflictResponse).status(),409);
  await until(async()=>intercepted && /changed|refresh/i.test(await page.locator('body').innerText()),'stale source refusal not shown');
  assert.equal(fs.readFileSync(file,'utf8'),entry); assert.equal(fs.readFileSync(part,'utf8'),external);
  assert.deepEqual(errors,[]);
});
