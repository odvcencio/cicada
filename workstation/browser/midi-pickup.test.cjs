'use strict';
// node midi-pickup.test.cjs --write-fixture DIR creates a disposable project.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

if (process.argv[2] === '--write-fixture') {
  assert(process.argv[3], 'provide a disposable fixture directory');
  const dir = path.resolve(process.argv[3]); fs.mkdirSync(dir,{recursive:true});
  fs.writeFileSync(path.join(dir,'main.cicada'), `cicada 2
tempo 120
track bass acid { cutoff=900Hz }
midi { port keys keys cc 74 -> bass.cutoff 200Hz..4kHz takeover = pickup }
pattern phrase { 1 . 3 . }
automate bass.cutoff { @1.1.1 1500Hz step }
scene verse { bass = phrase }
song { verse*16 }
`, {flag:'wx'});
  fs.writeFileSync(path.join(dir,'cicada.local'), 'port keys = "Pickup Keyboard"\n', {flag:'wx'});
  process.exit(0);
}

test('MIDI pickup follows the live automated value before taking over', {timeout:180000}, async t => {
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.equal(base.hostname, '127.0.0.1');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox','--mute-audio']});
  t.after(() => browser.close(), {timeout: 15000});
  const page = await (await browser.newContext()).newPage(), commands = [], errors = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('response', response => {
    if (new URL(response.url()).pathname !== '/__actions/live') return;
    try { const data = response.request().postDataJSON(); if (data.type === 'input-batch') commands.push(...JSON.parse(data.messages)); } catch {}
  });
  await page.addInitScript(() => {
    const access = new EventTarget(), port = new EventTarget();
    Object.assign(port,{id:'pickup',name:'Pickup Keyboard',state:'connected'}); access.inputs = new Map([['pickup',port]]);
    window.__sendMIDI = (...bytes) => { const e = new Event('midimessage'); Object.defineProperty(e,'data',{value:new Uint8Array(bytes)}); port.dispatchEvent(e); };
    Object.defineProperty(navigator,'requestMIDIAccess',{configurable:true,value:() => Promise.resolve(access)});
  });
  await page.goto(base.href);
  await page.goto(new URL('/studio?tab=session',base).href);
  await page.locator('#studio-shell[data-shell-actions=ready]').waitFor({timeout:90000});
  await page.waitForFunction(() => [...document.querySelectorAll('[data-live-mappings] li')].some(e => e.textContent.includes('Pickup Keyboard')), null, {timeout:30000});
  await page.locator('[data-live-control=midi]').click();
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('MIDI enabled'));
  await page.locator('#transport-form button[value=play]').click();
  await page.waitForFunction(async () => {
    const r = await fetch('/api/transport'); const state = await r.json();
    return Math.abs((state.midiPickupValues?.['bass.cutoff'] || 0) - 1500) < 0.1;
  }, null, {timeout:30000});
  // Wait for the Live engine's next transport poll before moving the control.
  await page.waitForTimeout(250);
  await page.evaluate(() => __sendMIDI(0xb0,74,0));
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('current value'), null, {timeout:30000});
  assert(!commands.some(m => m.type === 'param' && m.address === 'bass.cutoff'), 'first movement must not jump the parameter');
  await page.evaluate(() => __sendMIDI(0xb0,74,127));
  const end = Date.now()+15000;
  while (!commands.some(m => m.type === 'param' && m.address === 'bass.cutoff' && Number(m.value) === 4000) && Date.now() < end) await new Promise(r => setTimeout(r,50));
  assert(commands.some(m => m.type === 'param' && m.address === 'bass.cutoff' && Number(m.value) === 4000), 'crossing the live target enables control');
  assert.deepEqual(errors, []);
});
