'use strict';

const assert = require('node:assert/strict');
const {createHash} = require('node:crypto');
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

test('shared score respects focus, follows selection anchors, merges offline edits and rejects viewers', {timeout: 120_000}, async t => {
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
  await Promise.all([
    pages[1].waitForEvent('domcontentloaded'),
    pages[1].getByRole('button', {name: 'Use invitation'}).click(),
  ]);
  await wait(async () => await second.count() && !(await second.evaluate(node => node.readOnly)), 'invited editor remained read-only');

  // Deterministic WebKit behavior: setSelectionRange can focus its textarea.
  // Only this shared editor is shimmed; the updates still come from a real peer.
  await first.evaluate(node => {
    const setSelectionRange = node.setSelectionRange;
    node.collaborationSelectionProbe = {setSelectionRange, calls: []};
    node.setSelectionRange = function (...args) {
      this.collaborationSelectionProbe.calls.push({focused: document.activeElement === this});
      const result = setSelectionRange.apply(this, args);
      this.focus({preventScroll: true});
      return result;
    };
  });
  await invitation.focus();
  await invitation.evaluate(node => node.setSelectionRange(2, 6));
  const backgroundCalls = await first.evaluate(node => node.collaborationSelectionProbe.calls.length);
  const backgroundDraft = (await second.inputValue()) + '\n// remote background update\n';
  await second.fill(backgroundDraft);
  await wait(async () => (await first.inputValue()) === backgroundDraft, 'background remote edit did not arrive');
  assert.deepEqual(await invitation.evaluate(node => ({focused: document.activeElement === node, start: node.selectionStart, end: node.selectionEnd})),
    {focused: true, start: 2, end: 6}, 'a background shared snapshot stole the invitation field focus or selection');
  assert.equal(await first.evaluate(node => node.collaborationSelectionProbe.calls.length), backgroundCalls,
    'a background shared snapshot called setSelectionRange on its inactive textarea');

  const selectedText = 'Studio continuity regression';
  const selectionStart = backgroundDraft.indexOf(selectedText);
  assert.ok(selectionStart >= 0, 'fixture title is missing');
  await first.focus();
  await first.evaluate((node, selection) => node.setSelectionRange(selection.start, selection.end),
    {start: selectionStart, end: selectionStart + selectedText.length});
  const focusedCalls = await first.evaluate(node => node.collaborationSelectionProbe.calls.length);
  const prefix = '// remote 🌙 café\n';
  const anchoredDraft = prefix + backgroundDraft;
  await second.fill(anchoredDraft);
  await wait(async () => (await first.inputValue()) === anchoredDraft, 'focused remote edit did not arrive');
  assert.deepEqual(await first.evaluate(node => ({focused: document.activeElement === node, start: node.selectionStart, end: node.selectionEnd,
    selected: node.value.slice(node.selectionStart, node.selectionEnd)})),
    {focused: true, start: selectionStart + prefix.length, end: selectionStart + prefix.length + selectedText.length, selected: selectedText},
    'the active shared editor did not retain its CRDT selection anchors through a Unicode prefix insertion');
  assert.ok(await first.evaluate((node, before) => node.collaborationSelectionProbe.calls.slice(before).some(call => call.focused), focusedCalls),
    'the focused shared editor never restored its selection after the remote edit');
  await first.evaluate(node => {
    node.setSelectionRange = node.collaborationSelectionProbe.setSelectionRange;
    delete node.collaborationSelectionProbe;
  });
  t.diagnostic('Real peer snapshots preserved another field’s focus and shifted the active selection by the Unicode prefix’s UTF-16 length; the selection-focus shim models WebKit.');

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
  const draft = await first.inputValue();
  const [saved] = await Promise.all([
    pages[0].waitForResponse(response => new URL(response.url()).pathname === '/api/collaboration/save' && response.request().method() === 'POST'),
    pages[0].getByRole('button', {name: 'Save shared score'}).click(),
  ]);
  assert.equal(saved.status(), 200, `shared score save failed: ${await saved.text()}`);
  const persisted = await (await pages[0].request.get(base + '/api/state')).json();
  assert.equal(persisted.source, draft, 'shared draft was not saved');
  assert.equal(persisted.revision, createHash('sha256').update(draft).digest('hex'), 'persisted revision does not match the shared draft');
  const shots = process.env.CICADA_COLLAB_SCREENSHOTS;
  if (shots) {
    fs.mkdirSync(shots, {recursive: true});
    await pages[0].screenshot({path: path.join(shots, 'collaboration-desktop.png'), fullPage: true});
    await pages[2].screenshot({path: path.join(shots, 'collaboration-viewer-mobile.png'), fullPage: true});
  }
  for (const page of [pages[0], pages[2]]) assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'shared editor overflows its viewport');
  assert.deepEqual(errors, []);
});
