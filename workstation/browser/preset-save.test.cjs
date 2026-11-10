'use strict';
// node preset-save.test.cjs --write-fixture DIR creates a disposable score.
// Run Studio with --audio null and set CICADA_STUDIO_URL and CICADA_PRESET_SCORE.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

if (process.argv[2] === '--write-fixture') {
  assert(process.argv[3], 'provide a disposable fixture directory');
  const dir = path.resolve(process.argv[3]);
  fs.mkdirSync(dir, {recursive:true});
  fs.writeFileSync(path.join(dir, 'main.cicada'), 'cicada 2\ntempo 120\ntrack bass acid { cutoff=900Hz pan=0.2 }\npattern phrase { 1 . 3 . }\nscene verse { bass = phrase }\nsong { verse*16 }\n', {flag:'wx'});
  process.exit(0);
}

test('save a track as a preset and undo it in GoSX Studio', {timeout:180000}, async t => {
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.equal(base.hostname, '127.0.0.1');
  const score = process.env.CICADA_PRESET_SCORE;
  assert(path.isAbsolute(score), 'use an absolute disposable score path');
  const before = fs.readFileSync(score, 'utf8');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox','--mute-audio']});
  t.after(() => browser.close(), {timeout: 15000});
  const page = await (await browser.newContext()).newPage(), errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.goto(base.href);
  await page.goto(new URL('/studio?tab=session', base).href);
  await page.locator('#studio-shell[data-shell-actions=ready]').waitFor({timeout:90000});
  const panel = page.locator('[data-preset-save]');
  await panel.locator('summary').click();
  await panel.locator('select[name=track]').selectOption('bass');
  await panel.locator('input[name=name]').fill('bright');
  const oldRevision = await page.locator('#undo-form input[name=revision]').inputValue();
  const proof = process.env.CICADA_PRESET_PROOF_DIR;
  if (proof) { fs.mkdirSync(proof, {recursive:true}); await panel.scrollIntoViewIfNeeded(); await page.screenshot({path:path.join(proof, 'preset-save.png'), fullPage:true}); }
  const saveResponse = page.waitForResponse(r => new URL(r.url()).pathname === '/__actions/preset-save' && r.request().method() === 'POST');
  await panel.getByRole('button', {name:'Save preset'}).click();
  const savedResponse = await saveResponse;
  assert([200, 303].includes(savedResponse.status()), `preset save action returned ${savedResponse.status()}`);
  const until = async (predicate, message) => {
    const end = Date.now() + 30000;
    while (Date.now() < end) { if (predicate()) return; await new Promise(resolve => setTimeout(resolve,50)); }
    assert.fail(message);
  };
  await until(() => fs.readFileSync(score,'utf8').includes('preset bright'), 'preset saved to score');
  const saved = fs.readFileSync(score,'utf8');
  assert.match(saved, /cutoff\s*=\s*900Hz/);
  assert.match(saved, /pan\s*=\s*0\.2/);
  await page.waitForFunction(old => document.querySelector('#undo-form input[name=revision]')?.value !== old, oldRevision, {timeout:30000});
  await page.locator('#undo-form button').click();
  await until(() => fs.readFileSync(score,'utf8') === before, 'undo restored the exact score');
  assert.deepEqual(errors, []);
});
