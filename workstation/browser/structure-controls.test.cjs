'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const fixture = `// cicada-structure-controls-test: disposable fixture
cicada 2
title "Structure controls"
tempo 120
track bass acid {}
track spare acid {}
pattern pulse { 1 . 5 . }
scene main { bass=pulse }
scene unused { bass=pulse }
song { main*4 }
`;
if (process.argv[2] === '--write-fixture') {
  fs.writeFileSync(process.argv[3], fixture, {flag:'wx'});
  process.exit(0);
}
async function until(predicate, message) {
  const end = Date.now() + 15000;
  while (Date.now() < end) { if (await predicate()) return; await new Promise(resolve => setTimeout(resolve, 40)); }
  assert.fail(message);
}
test('structure controls edit the score and Undo restores it', {timeout:120000}, async t => {
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.equal(base.hostname, '127.0.0.1');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox','--mute-audio']});
  t.after(() => browser.close());
  const page = await browser.newPage({viewport:{width:1000,height:900}});
  const state = async () => (await page.request.get(new URL('/api/state', base).href)).json();
  const ready = async () => {
    await page.locator('#studio-shell[data-shell-actions=ready]:not([aria-busy])').waitFor();
    await until(async () => await page.locator('#undo-form input[name=revision]').inputValue() === (await state()).revision, 'shell revision did not settle');
  };
  await page.goto(new URL('/studio', base).href);
  await ready();
  await page.locator('#view-tab-arrange').click();
  const openInspector = async () => { await page.getByRole('link',{name:/^Edit main/}).first().click(); await page.locator('[aria-label="Section inspector"] details[open]').waitFor(); };
  const panel = name => page.locator('details.arrange-popover').filter({has:page.locator('summary', {hasText:name})});
  const openPanel = async name => { const details = panel(name); if (!(await details.evaluate(node => node.open))) await details.locator('summary').click(); };
  const submit = async (button, action) => {
    const response = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/__actions/' + action);
    await button.click(); await response; await ready();
  };
  const undo = () => submit(page.locator('#undo-form button[type=submit]'), 'undo');
  const changedAndUndo = async (before, match) => {
    await until(async () => (await state()).source !== before, 'score did not change');
    assert.match((await state()).source, match);
    await ready();
    await undo();
    await until(async () => (await state()).source === before, 'Undo did not restore the score');
    await ready();
  };
  assert.equal((await state()).source, fixture);
  await openInspector();
  let before = (await state()).source;
  let form = page.locator('form[action$="/__actions/scene-rename"]').first();
  await form.getByLabel('Scene name').fill('verse'); await submit(form.getByRole('button',{name:'Rename',exact:true}), 'scene-rename');
  await changedAndUndo(before, /scene verse/);
  await openInspector(); before = (await state()).source;
  form = page.locator('form[action$="/__actions/scene-duplicate"]').first();
  await submit(form.getByRole('button',{name:'Duplicate',exact:true}), 'scene-duplicate');
  await changedAndUndo(before, /scene main-2/);
  await openPanel('Add section'); before = (await state()).source;
  form = page.locator('form[action$="/__actions/scene-add"]').first();
  await form.getByLabel('New scene').fill('fresh'); await submit(form.getByRole('button',{name:'Create'}), 'scene-add');
  await until(async () => (await state()).source !== before, 'scene was not created');
  assert.match((await state()).source, /scene fresh/); await ready();
  // Keep the new scene for the append test, then undo both changes.
  await openPanel('Add section');
  const created = (await state()).source;
  form = panel('Add section').locator('form[action$="/__actions/song"]');
  await form.getByLabel('Scene').selectOption('fresh'); await submit(form.getByRole('button',{name:'Add to arrangement'}), 'song');
  await changedAndUndo(created, /fresh\*4/);
  await undo(); await until(async () => (await state()).source === before, 'Undo did not remove the new scene'); await ready();
  await openPanel('Add section'); before = (await state()).source;
  await submit(panel('Add section').locator('form[action$="/__actions/scene-delete"]').getByRole('button',{name:'Delete scene unused'}), 'scene-delete');
  await until(async () => !(await state()).source.includes('scene unused'), 'scene was not deleted');
  await undo(); await until(async () => (await state()).source === before, 'Undo did not restore scene'); await ready();
  await page.locator('.timeline-track summary[aria-label="Rename spare"]').click(); before = (await state()).source;
  await submit(page.locator('form[action$="/__actions/track-delete"]').getByRole('button',{name:'Delete track spare'}), 'track-delete');
  await until(async () => !(await state()).source.includes('track spare'), 'track was not deleted');
  await ready();
  await page.locator('.timeline-track summary[aria-label="Rename bass"]').click();
  await submit(page.locator('form[action$="/__actions/track-delete"]').getByRole('button',{name:'Delete track bass'}), 'track-delete');
  await until(async () => (await page.locator('body').textContent()).includes('only track'), 'last track refusal was not shown');
  assert.ok(!(await state()).source.includes('track spare'));
  await undo(); await until(async () => (await state()).source === before, 'Undo did not restore track'); await ready();
  await openPanel('Project'); before = (await state()).source;
  await submit(panel('Project').locator('form[action$="/__actions/song-convert"]').getByRole('button',{name:'Convert',exact:true}), 'song-convert');
  await changedAndUndo(before, /arrange\s*\{/);
});
