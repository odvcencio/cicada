// Create a disposable fixture with: node portable-midi.test.cjs --write-fixture DIR
// Run Studio on DIR/main.cicada with --audio null. Set CICADA_STUDIO_URL,
// CICADA_PORTABLE_MIDI_SCORE, CICADA_PLAYWRIGHT_MODULE and CICADA_CHROME_PATH.
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

if (process.argv[2] === '--write-fixture') {
  const dir = path.resolve(process.argv[3] || '');
  assert(process.argv[3], 'provide a disposable fixture directory');
  fs.mkdirSync(dir, {recursive:true});
  fs.writeFileSync(path.join(dir, 'main.cicada'), `cicada 2
tempo 120
track bass acid {}
track lead acid {}
midi {
  port keys
  keys ch 1 -> lead
  keys cc 74 -> bass.cutoff 200Hz..4kHz
  keys note c1 -> launch verse
}
pattern phrase { 1 . 3 . }
scene verse { bass = phrase lead = phrase }
song { verse*16 }
`, {flag:'wx'});
  fs.writeFileSync(path.join(dir, 'cicada.local'), 'port keys = "Synthetic Keyboard"\n', {flag:'wx'});
  process.exit(0);
}

test('portable MIDI routes input, learns to the score, and survives reload', {timeout:180000}, async t => {
  const startup = new URL(process.env.CICADA_STUDIO_URL);
  assert.equal(startup.hostname, '127.0.0.1');
  const scorePath = process.env.CICADA_PORTABLE_MIDI_SCORE;
  assert(path.isAbsolute(scorePath), 'use a disposable absolute score path');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox','--mute-audio']});
  t.after(() => browser.close(), {timeout: 15000});
  const context = await browser.newContext(), page = await context.newPage(), errors = [], actions = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('response', response => {
    const endpoint = new URL(response.url()).pathname;
    if (!['/__actions/live', '/__actions/transport'].includes(endpoint)) return;
    try { const data = response.request().postDataJSON(); if (data.type === 'input-batch') actions.push(...JSON.parse(data.messages)); else if (endpoint === '/__actions/transport') actions.push(data); } catch {}
  });
  await page.addInitScript(() => {
    const access = new EventTarget(), port = new EventTarget();
    Object.assign(port, {id:'test-keys', name:localStorage.getItem('portable-test-device') || 'Synthetic Keyboard', state:'connected'});
    access.inputs = new Map([['test-keys', port]]);
    window.__sendMIDI = (...bytes) => { const e = new Event('midimessage'); Object.defineProperty(e,'data',{value:new Uint8Array(bytes)}); port.dispatchEvent(e); };
    Object.defineProperty(navigator,'requestMIDIAccess',{configurable:true,value:() => Promise.resolve(access)});
  });
  const until = async (predicate, message) => {
    const end = Date.now() + 15000;
    while (Date.now() < end) { if (predicate()) return; await new Promise(resolve => setTimeout(resolve,50)); }
    assert.fail(message);
  };
  await page.goto(startup.href);
  await page.goto(new URL('/studio?tab=session',startup).href);
  await page.locator('#studio-shell[data-shell-actions=ready]').waitFor({timeout:90000});
  await page.waitForFunction(() => [...document.querySelectorAll('[data-live-mappings] li')].some(e => e.textContent.includes('Synthetic Keyboard')), null, {timeout:30000});
  await page.locator('[data-live-control=midi]').click();
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('MIDI enabled'), null, {timeout:30000});
  await page.evaluate(() => __sendMIDI(0xb0, 74, 127));
  await until(() => actions.some(m => m.type === 'param' && m.address === 'bass.cutoff' && Number(m.value) === 4000), 'CC reaches the declared range maximum');
  await page.locator('#transport-form button[value=play]').click();
  await page.waitForFunction(() => !document.querySelector('[data-live-control=record]').disabled);
  await page.evaluate(() => __sendMIDI(0x90, 60, 100));
  await until(() => actions.some(m => m.type === 'note' && m.on === 'true' && m.track === 'lead'), 'channel routes the note to lead');
  await page.evaluate(() => __sendMIDI(0x80, 60, 0));
  await page.locator('[data-live-target]').selectOption('param:bass.pan');
  await page.locator('[data-live-control=learn]').click();
  await page.evaluate(() => __sendMIDI(0xb0, 71, 96));
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('saved in the score'), null, {timeout:30000});
  const score = fs.readFileSync(scorePath,'utf8');
  assert(score.includes('keys ch 1 cc 71 -> bass.pan'));
  assert(!score.includes('Synthetic Keyboard'), 'device identity remains machine local');
  await page.waitForFunction(() => [...document.querySelectorAll('[data-live-mappings] li')].some(e => e.textContent.includes('bass.pan')), null, {timeout:60000});
  fs.writeFileSync(path.join(path.dirname(scorePath),'cicada.local'), 'port keys = "Second Keyboard"\n');
  await page.evaluate(() => localStorage.setItem('portable-test-device', 'Second Keyboard'));
  await page.reload();
  await page.waitForFunction(() => [...document.querySelectorAll('[data-live-mappings] li')].some(e => e.textContent.includes('bass.pan') && e.textContent.includes('Second Keyboard')), null, {timeout:60000});
  await page.locator('[data-live-control=midi]').click();
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('MIDI enabled'), null, {timeout:30000});
  await page.evaluate(() => __sendMIDI(0xb0, 71, 127));
  await until(() => actions.some(m => m.type === 'param' && m.address === 'bass.pan' && Number(m.value) === 1), 'learned mapping works with the second device');
  await page.locator('#transport-form button[value=play]').click();
  await page.waitForFunction(() => !document.querySelector('[data-live-control=record]').disabled);
  await page.locator('[data-live-target]').selectOption('stop-track:bass');
  await page.locator('[data-live-control=learn]').click();
  await page.evaluate(() => __sendMIDI(0x90, 65, 100));
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('saved in the score'), null, {timeout:30000});
  assert(fs.readFileSync(scorePath, 'utf8').includes('keys ch 1 note 65 -> stop-track bass'));
  await page.waitForFunction(() => [...document.querySelectorAll('[data-live-mappings] li')].some(e => e.textContent.includes('stop-track bass')), null, {timeout:60000});
  await page.locator('[data-live-control=midi]').click();
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('MIDI enabled'), null, {timeout:30000});
  await page.evaluate(() => __sendMIDI(0x90, 65, 100));
  await until(() => actions.some(m => m.action === 'trackStop' && m.track === 'bass'), 'portable track stop reaches the transport');
  await page.locator('[data-live-target]').selectOption('slot:bass:phrase');
  await page.locator('[data-live-control=learn]').click();
  await page.evaluate(() => __sendMIDI(0x90, 66, 100));
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('saved in the score'), null, {timeout:30000});
  assert(fs.readFileSync(scorePath, 'utf8').includes('keys ch 1 note 66 -> launch-slot bass phrase'));
  await page.waitForFunction(() => [...document.querySelectorAll('[data-live-mappings] li')].some(e => e.textContent.includes('launch-slot bass phrase')), null, {timeout:60000});
  const proof = process.env.CICADA_PORTABLE_MIDI_PROOF_DIR;
  if (proof) { fs.mkdirSync(proof,{recursive:true}); await page.locator('[data-live-mappings]').scrollIntoViewIfNeeded(); await page.screenshot({path:path.join(proof,'portable-midi-learn.png'),fullPage:true}); }
  await page.locator('[data-live-control=midi]').click();
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('MIDI enabled'), null, {timeout:30000});
  await page.evaluate(() => __sendMIDI(0x90, 66, 100));
  await until(() => actions.some(m => m.action === 'slot' && m.track === 'bass' && m.pattern === 'phrase'), 'portable slot launch reaches the transport');
  await page.reload();
  await page.waitForFunction(() => [...document.querySelectorAll('[data-live-mappings] li')].some(e => e.textContent.includes('launch-slot bass phrase')), null, {timeout:60000});
  await page.locator('[data-live-control=midi]').click();
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('MIDI enabled'), null, {timeout:30000});
  await page.locator('#transport-form button[value=play]').click();
  await page.waitForFunction(() => !document.querySelector('[data-live-control=record]').disabled);
  const beforeReloadActions = actions.length;
  await page.evaluate(() => { __sendMIDI(0x90, 65, 100); __sendMIDI(0x90, 66, 100); });
  await until(() => actions.slice(beforeReloadActions).some(m => m.action === 'trackStop' && m.track === 'bass') && actions.slice(beforeReloadActions).some(m => m.action === 'slot' && m.track === 'bass' && m.pattern === 'phrase'), 'score mappings survive reload and operate on the rebound device');
  await page.locator('[data-live-acid]').selectOption('bass');
  await page.locator('[data-live-control=record]').click();
  await page.evaluate(() => __sendMIDI(0x90, 64, 100));
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('Select mapped track lead'), null, {timeout:30000});
  assert(!actions.some(m => m.type === 'note' && m.on === 'true' && m.note === '64'), 'recording does not send a note to the wrong pattern');
  assert.deepEqual(errors, []);
});
