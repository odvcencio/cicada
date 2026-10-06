'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {createRequire} = require('node:module');
const test = require('node:test');

function playwright() {
  const location = process.env.CICADA_PLAYWRIGHT_MODULE;
  if (!location) return require('playwright');
  try { const loaded = require(path.resolve(location)); if (loaded.chromium) return loaded; } catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw error; }
  return createRequire(path.resolve(location))('playwright');
}

test('shared score merges offline edits, retains per-user undo and rejects viewers', {timeout: 120_000}, async t => {
  const base = process.env.CICADA_STUDIO_URL;
  assert.ok(base, 'set CICADA_STUDIO_URL to a disposable Studio');
  assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname));
  const browser = await playwright().chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH || undefined, args: ['--no-sandbox', '--autoplay-policy=no-user-gesture-required']});
  t.after(() => browser.close());
  const contexts = await Promise.all([browser.newContext({viewport: {width: 1440, height: 1000}}), browser.newContext({viewport: {width: 1440, height: 1000}}), browser.newContext({viewport: {width: 390, height: 844}})]);
  const pages = await Promise.all(contexts.map(context => context.newPage()));
  const errors = [];
  for (const page of pages) page.on('pageerror', error => errors.push(error.message));
  const open = async page => { await page.goto(base + '/?panel=collaboration'); await page.locator('[data-collaboration-editor]').waitFor({timeout: 60_000}); };
  await open(pages[0]);
  const first = pages[0].locator('[data-collaboration-editor]');
  await first.waitFor();
  const wait = async (predicate, message) => {
    const deadline = Date.now() + 30_000;
    while (Date.now() < deadline) { if (await predicate()) return; await new Promise(resolve => setTimeout(resolve, 50)); }
    assert.fail(message);
  };
  await wait(async () => !(await first.evaluate(node => node.readOnly)), 'owner editor did not become writable');
  const original = await first.inputValue();
  assert.ok(original.includes('cicada-continuity-test: disposable fixture'), 'refuse to edit an unmarked score');
  await open(pages[1]);
  const second = pages[1].locator('[data-collaboration-editor]');
  assert.equal(await second.evaluate(node => node.readOnly), true, 'new participant must be a viewer');
  await pages[0].getByRole('button', {name: 'Create editor invitation'}).click();
  const invitation = pages[0].getByRole('textbox', {name: 'Invitation code'});
  await wait(async () => (await invitation.inputValue()).length === 32, 'invitation was not created');
  await pages[1].getByRole('textbox', {name: 'Invitation code'}).fill(await invitation.inputValue());
  await pages[1].getByRole('button', {name: 'Use invitation'}).click();
  await wait(async () => await second.count() && !(await second.evaluate(node => node.readOnly)), 'invited editor remained read-only');
  await first.fill(original + '\n// first user\n');
  await wait(async () => (await second.inputValue()).includes('// first user'), 'remote edit did not arrive');
  await second.fill(await second.inputValue() + '// second user\n');
  await wait(async () => (await first.inputValue()).includes('// second user'), 'second editor did not publish');
  await pages[0].getByRole('button', {name: 'Undo my edit'}).click();
  await wait(async () => { const value = await second.inputValue(); return !value.includes('// first user') && value.includes('// second user'); }, 'undo removed another user’s edit');
  await pages[0].getByRole('button', {name: 'Redo my edit'}).click();
  await wait(async () => (await second.inputValue()).includes('// first user'), 'redo was not replicated');
  await contexts[0].setOffline(true);
  await first.fill(await first.inputValue() + '// offline user 🎵\n');
  await second.fill(await second.inputValue() + '// connected user\n');
  await contexts[0].setOffline(false);
  await wait(async () => (await first.inputValue()) === (await second.inputValue()) && (await first.inputValue()).includes('// offline user 🎵') && (await first.inputValue()).includes('// connected user'), 'offline edits did not merge');
  await pages[0].reload();
  await wait(async () => (await first.inputValue()) === (await second.inputValue()), 'reload lost the replicated draft');
  await open(pages[2]);
  const viewer = pages[2].locator('[data-collaboration-editor]');
  assert.equal(await viewer.evaluate(node => node.readOnly), true);
  assert.equal(await pages[2].getByRole('button', {name: 'Save shared score'}).isDisabled(), true);
  const response = await pages[2].evaluate(async () => {
    const token = document.querySelector('[name=csrf_token]').value;
    return (await fetch('/__actions/source', {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': token}, body: JSON.stringify({content: 'forbidden', revision: 'forged', role: 'owner'})})).status;
  });
  assert.equal(response, 403);
  await wait(async () => !(await pages[0].getByRole('button', {name: 'Save shared score'}).isDisabled()), 'shared score did not sync before save');
  await pages[0].getByRole('button', {name: 'Save shared score'}).click();
  await pages[0].getByText('Shared score saved.', {exact: true}).waitFor();
  const shots = process.env.CICADA_COLLAB_SCREENSHOTS;
  if (shots) {
    fs.mkdirSync(shots, {recursive: true});
    await pages[0].screenshot({path: path.join(shots, 'collaboration-desktop.png'), fullPage: true});
    await pages[2].screenshot({path: path.join(shots, 'collaboration-viewer-mobile.png'), fullPage: true});
  }
  for (const page of [pages[0], pages[2]]) assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'shared editor overflows its viewport');
  assert.deepEqual(errors, []);
});
