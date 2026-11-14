/* Real scene arrangement controls against a fresh disposable Studio. */
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const fixture = `// cicada-scene-block-test: disposable fixture
cicada 2
title "Scene block editing"
track bass acid {}
midi { port keys; keys ch 1 -> bass }
${Array.from({length:14}, (_, i) => `track layer${i} acid {}`).join('\n')}
pattern pulse { 1 . 5 . }
scene main { bass=pulse }
scene alternate_long_scene_name { bass=pulse }
song { main*4 alternate_long_scene_name*1 }
`;
if (process.argv[2] === '--write-fixture') {
  fs.writeFileSync(process.argv[3], fixture, {flag:'wx'});
  fs.writeFileSync(path.join(path.dirname(process.argv[3]), 'cicada.local'), 'port keys = \"Synthetic Keyboard\"\n', {flag:'wx'});
  process.exit(0);
}
async function until(predicate, message) {
  const end = Date.now() + 15000;
  while (Date.now() < end) {
    if (await predicate()) return;
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  assert.fail(message);
}
test('scene headers open guarded block controls and Undo restores the song', {timeout: 60000}, async t => {
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.equal(base.hostname, '127.0.0.1');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox','--mute-audio']});
  t.after(() => browser.close());
  const page = await browser.newPage({viewport:{width:1440,height:1000}});
  const errors = [], writes = [], notes = [];
  await page.addInitScript(() => {
    const access = new EventTarget(), port = new EventTarget();
    Object.assign(port, {id:'test-keys', name:'Synthetic Keyboard', state:'connected'});
    access.inputs = new Map([['test-keys',port]]);
    window.__sendSceneMIDI = (...bytes) => { const event = new Event('midimessage'); Object.defineProperty(event,'data',{value:new Uint8Array(bytes)}); port.dispatchEvent(event); };
    Object.defineProperty(navigator,'requestMIDIAccess',{configurable:true,value:() => Promise.resolve(access)});
  });
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', msg => { if(msg.text().includes('scene reveal')) console.log(msg.text()); });
  page.on('request', request => {
    const endpoint = new URL(request.url()).pathname;
    if (request.method()==='POST' && endpoint==='/__actions/song') writes.push(request);
    if (request.method()==='POST' && endpoint==='/__actions/live') {
      try { const data=request.postDataJSON(); if(data.type==='input-batch') notes.push(...JSON.parse(data.messages).filter(message=>message.type==='note')); } catch {}
    }
  });
  const state = async () => (await page.request.get(new URL('/api/state',base).href)).json();
  const ready = async () => {
    await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
    await until(async () => await page.locator('#undo-form input[name=revision]').inputValue() === (await state()).revision, 'shell revision did not settle');
  };
  await page.goto(new URL('/studio',base).href);
  await ready();
  // Isolated engine builds can exercise source CSS before assets are rebuilt.
  if (process.env.CICADA_SCENE_SOURCE_CSS === '1') await page.addStyleTag({path:path.resolve(__dirname,'../app/ui/view-arrange.css')});
  const before = await state();
  assert.equal(before.source, fixture);
  await page.locator('#view-tab-session').click();
  await page.locator('[data-live-control=midi]').click();
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('MIDI enabled'));
  await page.locator('#transport-form button[value=play]').click();
  await page.waitForFunction(() => !document.querySelector('[data-live-control=record]').disabled);
  await page.locator('#view-tab-arrange').click();
  await page.evaluate(() => { window.__sceneLive = document.querySelector('#cicada-live'); window.__sceneDocument = document; __sendSceneMIDI(0x90,60,100); });
  await until(() => notes.some(note => note.on === 'true' && note.note === '60'), 'MIDI note-on was not delivered');
  const edit = page.getByRole('link', {name:'Edit alternate_long_scene_name · bars 5–5', exact:true});
  assert.equal(await edit.evaluate(el => { const r=el.getBoundingClientRect(), p=el.closest('.timeline-heading').getBoundingClientRect(); return r.width>0 && r.left>=p.left && r.right<=p.right; }),true,'Edit must fit in a one-bar header');
  assert.equal(await edit.locator('..').locator('small').evaluate(el => el.getBoundingClientRect().height <= parseFloat(getComputedStyle(el).fontSize) * 1.5),true,'block number must remain on one line');
  const oldURL = await edit.getAttribute('href');
  const beforeSelectionURL = page.url();
  if (process.env.CICADA_SCENE_PROOF_DIR) { fs.mkdirSync(process.env.CICADA_SCENE_PROOF_DIR,{recursive:true}); await page.locator('.timeline-heading').nth(1).screenshot({path:path.join(process.env.CICADA_SCENE_PROOF_DIR,'scene-header.png')}); }
  await edit.focus();
  await page.keyboard.press('Enter');
  await until(async () => await page.locator('[data-song-index="1"][data-song-selected]').count() === 1, 'block selection did not open');
  await ready();
  const selected = page.locator('[data-song-index="1"][data-song-selected]');
  assert.equal(await selected.getAttribute('open'), '');
  await until(async () => selected.locator('summary').evaluate(el => { const r=el.getBoundingClientRect(), pane=el.closest('.shell-scroll').getBoundingClientRect(); return r.bottom>pane.top && r.top<pane.bottom && r.bottom>0 && r.top<innerHeight; }), 'selected block controls should be scrolled into view');
  assert.equal(await selected.locator('summary').evaluate(el => document.activeElement === el),true,'keyboard activation should focus the block');
  if (process.env.CICADA_SCENE_PROOF_DIR) await selected.screenshot({path:path.join(process.env.CICADA_SCENE_PROOF_DIR,'scene-controls.png')});
  await page.keyboard.press('Tab');
  assert.equal(await selected.evaluate(el => el.contains(document.activeElement)), true, 'Tab should reach the selected block controls');
  await page.locator('#view-tab-mixer').focus();
  await page.waitForTimeout(150);
  assert.equal(await page.locator('#view-tab-mixer').evaluate(el => document.activeElement === el), true, 'later focus must remain the user’s choice');
  assert.equal(await selected.locator('input[name=target_key]').first().inputValue(), 'alternate_long_scene_name@5+1');
  assert.equal((await state()).source, before.source);
  assert.equal(writes.length, 0);
  assert.equal(await page.locator('.timeline-heading').nth(1).getByRole('button',{name:'alternate_long_scene_name',exact:true}).count(),1);
  assert.equal(await page.evaluate(() => window.__sceneLive === document.querySelector('#cicada-live') && window.__sceneDocument === document), true, 'selection must keep Live mounted');
  assert.equal(page.url(),beforeSelectionURL,'selection must not navigate');
  assert.equal(notes.some(note => note.on === 'false' && note.note === '60'),false,'selection must not release the held note');
  await page.evaluate(() => __sendSceneMIDI(0x80,60,0));
  await until(() => notes.some(note => note.on === 'false' && note.note === '60'), 'MIDI note-off did not reach the same Live engine');
  await selected.getByRole('button',{name:'Duplicate block',exact:true}).click();
  await until(async () => (await state()).revision !== before.revision, 'duplicate was not saved');
  await ready();
  assert.equal(writes.length,1);
  assert.notEqual((await state()).source,before.source);
  assert.equal(await page.locator('[data-song-selected]').count(),0);
  await page.getByText('After a song edit, choose Edit again to continue.', {exact:false}).waitFor();
  // A copied URL from an older revision cannot target a new occupant of the slot.
  await page.goto(new URL(oldURL,base).href);
  await ready();
  assert.equal(await page.locator('[data-song-selected]').count(),0);
  await page.locator('#undo-form button[type=submit]').click();
  await until(async () => (await state()).source === before.source, 'Undo did not restore the score');
  await ready();
  await page.setViewportSize({width:390,height:844});
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),true);
  assert.deepEqual(errors,[]);
});
