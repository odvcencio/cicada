'use strict';
// Run against a disposable Studio with null audio and the fixture below.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

if (process.argv[2] === '--write-fixture') {
  assert(process.argv[3]);
  const dir = path.resolve(process.argv[3]); fs.mkdirSync(dir, {recursive:true});
  fs.writeFileSync(path.join(dir, 'main.cicada'), `cicada 2
tempo 120
track bass acid {}
live { macro filter -> bass.cutoff 200Hz..4kHz curve exp smooth 150ms }
midi { port keys keys cc 74 -> bass.cutoff 200Hz..4kHz takeover = pickup }
pattern p { 1 . 3 . }
scene main { bass = p }
song { main*16 }
`, {flag:'wx'});
  process.exit(0);
}

test('Studio macro moves the live audio parameter', {timeout:180000}, async t => {
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.equal(base.hostname, '127.0.0.1');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox','--mute-audio']});
  t.after(() => browser.close());
  const page = await (await browser.newContext()).newPage(), errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.goto(base.href);
  await page.goto(new URL('/studio?tab=session', base).href);
  await page.locator('#studio-shell[data-shell-actions=ready]').waitFor({timeout:90000});
  await page.locator('#transport-form button[value=play]').click();
  const slider = page.locator('[data-live-macro="filter"]');
  await slider.waitFor({timeout:30000});
  await page.waitForFunction(() => !document.querySelector('[data-live-macro="filter"]')?.disabled);
  await page.waitForFunction(async () => {
    const state = await (await fetch('/api/transport')).json();
    return Math.abs((state.midiPickupValues?.['bass.cutoff'] || 0) - 200) < 0.1;
  }, null, {timeout:30000});
  await slider.evaluate(input => { input.value = '1'; input.dispatchEvent(new Event('input', {bubbles:true})); });
  await page.waitForFunction(async () => {
    const state = await (await fetch('/api/transport')).json();
    return state.macroTargets?.filter === 1 && Math.abs((state.midiPickupValues?.['bass.cutoff'] || 0) - 4000) < 0.1;
  }, null, {timeout:30000});
  await page.reload();
  await page.locator('#studio-shell[data-shell-actions=ready]').waitFor({timeout:90000});
  await page.waitForFunction(() => {
    const input = document.querySelector('[data-live-macro="filter"]');
    return input && !input.disabled && Number(input.value) === 1;
  }, null, {timeout:30000});
  assert.deepEqual(errors, []);
  if (process.env.CICADA_MACRO_PROOF_DIR) {
    fs.mkdirSync(process.env.CICADA_MACRO_PROOF_DIR, {recursive:true});
    await slider.scrollIntoViewIfNeeded();
    await page.screenshot({path:path.join(process.env.CICADA_MACRO_PROOF_DIR, 'macro-control.png')});
  }
});
