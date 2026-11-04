// Run on the disposable score made by portable-midi.test.cjs --write-fixture.
// CICADA_STUDIO_URL and CICADA_PORTABLE_MIDI_SCORE identify the local Studio.
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

test('Studio binds a portable MIDI port without editing the score', {timeout:180000}, async t => {
  const startup = new URL(process.env.CICADA_STUDIO_URL);
  assert.equal(startup.hostname, '127.0.0.1');
  const score = process.env.CICADA_PORTABLE_MIDI_SCORE;
  assert(path.isAbsolute(score));
  const originalScore = fs.readFileSync(score, 'utf8');
  const local = path.join(path.dirname(score), 'cicada.local');
  fs.writeFileSync(local, '# Keep this comment\nport keys = "Old Keyboard" # Keep this too\n');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox','--mute-audio']});
  t.after(() => browser.close());
  const context = await browser.newContext(), page = await context.newPage(), errors = [], actions = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('response', response => {
    if (new URL(response.url()).pathname !== '/__actions/live') return;
    try { const data = response.request().postDataJSON(); if (data.type === 'input-batch') actions.push(...JSON.parse(data.messages)); } catch {}
  });
  await page.addInitScript(() => {
    const access = new EventTarget(), port = new EventTarget();
    Object.assign(port, {id:'test-keyboard', name:'New Keyboard', state:'connected'});
    access.inputs = new Map([['test-keyboard', port]]);
    window.__sendMIDI = (...bytes) => { const e = new Event('midimessage'); Object.defineProperty(e,'data',{value:new Uint8Array(bytes)}); port.dispatchEvent(e); };
    Object.defineProperty(navigator,'requestMIDIAccess',{configurable:true,value:() => Promise.resolve(access)});
  });
  await page.goto(startup.href);
  await page.goto(new URL('/studio?tab=session', startup).href);
  await page.locator('#studio-shell[data-shell-actions=ready]').waitFor({timeout:90000});
  await page.waitForFunction(() => [...document.querySelectorAll('[data-live-mappings] li')].some(e => e.textContent.includes('Old Keyboard')), null, {timeout:30000});
  await page.locator('[data-live-control=midi]').click();
  await page.waitForFunction(() => [...document.querySelector('[data-live-device]').options].some(e => e.value === 'New Keyboard'), null, {timeout:30000});
  await page.locator('[data-live-device]').selectOption('New Keyboard');
  await page.locator('[data-live-control=bind]').click();
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('bound on this machine'), null, {timeout:30000});
  assert.equal(fs.readFileSync(local, 'utf8'), '# Keep this comment\nport keys = "New Keyboard" # Keep this too\n');
  assert.equal(fs.readFileSync(score, 'utf8'), originalScore);
  fs.writeFileSync(local, '# External edit\nport keys = "Old Keyboard"\n');
  await page.locator('[data-live-control=bind]').click();
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('cicada.local changed'), null, {timeout:30000});
  assert.equal(fs.readFileSync(local, 'utf8'), '# External edit\nport keys = "Old Keyboard"\n', 'stale binding leaves the newer file intact');
  await page.locator('[data-live-control=reload-ports]').click();
  await page.waitForFunction(() => document.querySelector('[data-live-device]')?.value === 'Old Keyboard', null, {timeout:30000});
  await page.locator('[data-live-device]').selectOption('New Keyboard');
  await page.locator('[data-live-control=bind]').click();
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('bound on this machine'), null, {timeout:30000});
  assert.equal(fs.readFileSync(local, 'utf8'), '# External edit\nport keys = "New Keyboard"\n');
  const proof = process.env.CICADA_PORTABLE_MIDI_PROOF_DIR;
  if (proof) {
    fs.mkdirSync(proof,{recursive:true});
    await page.locator('[data-live-device]').scrollIntoViewIfNeeded();
    await page.screenshot({path:path.join(proof,'midi-binding.png'),fullPage:true});
    await page.setViewportSize({width:390, height:844});
    await page.locator('[data-live-device]').scrollIntoViewIfNeeded();
    await page.screenshot({path:path.join(proof,'midi-binding-mobile.png'),fullPage:true});
    await page.setViewportSize({width:1280, height:720});
  }
  await page.reload();
  await page.waitForFunction(() => [...document.querySelectorAll('[data-live-mappings] li')].some(e => e.textContent.includes('New Keyboard')), null, {timeout:60000});
  await page.locator('[data-live-control=midi]').click();
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('MIDI enabled'));
  await page.evaluate(() => __sendMIDI(0xb0, 74, 127));
  await page.waitForFunction(() => document.querySelector('[data-live-mappings]')?.textContent.includes('New Keyboard'));
  const until = Date.now() + 15000;
  while (!actions.some(m => m.type === 'param' && m.address === 'bass.cutoff' && Number(m.value) === 4000) && Date.now() < until) await new Promise(r => setTimeout(r, 50));
  assert(actions.some(m => m.type === 'param' && m.address === 'bass.cutoff' && Number(m.value) === 4000), 'reloaded binding routes controller');
  assert.deepEqual(errors, []);
});
