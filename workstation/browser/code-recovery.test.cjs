// Run capture, recover and verify with Studio restarted between phases.
'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

test('private Code copies survive restarts without changing the song', {timeout: 180000}, async t => {
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const proof = process.env.CICADA_CODE_RECOVERY_PROOF;
  const phase = process.env.CICADA_CODE_RECOVERY_PHASE || 'capture';
  assert.ok(path.isAbsolute(proof));
  fs.mkdirSync(proof, {recursive: true});
  const browser = await chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH});
  const cookies = path.join(proof, 'cookies.json');
  const context = await browser.newContext({viewport: {width: 1280, height: 840}, ...(phase === 'capture' ? {} : {storageState: cookies})});
  const page = await context.newPage(), errors = [], checks = [];
  let complete = false;
  t.after(async () => {
    try {
      if (!complete) {
        await page.screenshot({path: path.join(proof, phase + '-failure.png')});
        fs.writeFileSync(path.join(proof, phase + '-failure.txt'), (await page.locator('body').innerText()).slice(0, 16000));
      }
    } finally { await browser.close(); }
  });
  page.on('pageerror', error => errors.push(error.message));
  page.on('dialog', dialog => dialog.accept());
  const origin = new URL(process.env.CICADA_STUDIO_URL);
  const state = async () => (await page.request.get(new URL('/api/state', origin).href)).json();
  const until = async (check, label) => {
    const end = Date.now() + 40000;
    while (Date.now() < end) {
      if (await check()) return;
      await new Promise(resolve => setTimeout(resolve, 40));
    }
    assert.fail(label);
  };
  const open = async (target, file, copy = '') => {
    await target.goto(new URL('/?panel=code&file=' + encodeURIComponent(file) + (copy ? '&codeDraft=' + encodeURIComponent(copy) : ''), origin).href);
    await target.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();
  };
  const keep = async (target, content) => {
    await target.locator('#score textarea').fill(content);
    await target.getByText('Recovery copy kept on this device. Use Save file to apply edits to the song.', {exact: true}).waitFor();
    return target.locator('#score [name=recoveryId]').inputValue();
  };
  const selectFile = async file => {
    await page.getByRole('navigation', {name: 'Project files', exact: true}).getByRole('link', {name: file, exact: true}).click();
    await until(async () => await page.locator('#score [name=file]').inputValue() === file, 'Code file navigation');
  };
  await page.goto(origin.href);
  await open(page, 'main.cicada');
  if (phase === 'capture') {
    const initial = await state();
    assert.ok(initial.source.includes('title "Studio project"'));
    const other = await context.newPage();
    other.on('dialog', dialog => dialog.accept());
    await open(other, 'main.cicada');
    const first = initial.source.replace('tempo 120', 'tempo 121') + '\n// First idea 🎹\n';
    const second = initial.source.replace('tempo 120', 'tempo 122') + '\n// Second idea 🎵\n';
    const firstID = await keep(page, first);
    const secondID = await keep(other, second);
    assert.notEqual(firstID, secondID, 'two windows keep distinct copies');
    assert.equal(await page.locator('#score textarea').inputValue(), first);
    await selectFile('parts/patterns.cicada');
    const part = await page.locator('#score textarea').inputValue();
    assert.ok(part.includes('pattern riff') && !part.includes('First idea'), 'each file has its own editor buffer');
    await selectFile('main.cicada');
    assert.equal(await page.locator('#score textarea').inputValue(), first, 'file navigation retains this window’s draft');
    assert.equal(await page.locator('#score [name=recoveryId]').inputValue(), firstID, 'the retained buffer keeps its own recovery receipt');
    await selectFile('parts/patterns.cicada');
    const clearedID = await keep(page, part + '\n// Temporary change\n');
    await page.locator('#score textarea').fill(part);
    await page.getByText('No unsaved Code edits.', {exact: true}).waitFor();
    const invalid = part + '\n// Unfinished harmony é\npattern unfinished {\n';
    const partID = await keep(page, invalid);
    assert.notEqual(partID, clearedID, 'editing again after returning to clean uses a new copy');
    assert.equal((await state()).source, initial.source, 'typing never saves the song');
    const audio = await (await page.request.get(new URL('/api/audio/config', origin).href)).json();
    assert.equal(String(audio.inventory.backendName || audio.inventory.backend).toLowerCase(), 'null');
    assert.equal(audio.options.inputEnabled, false);
    fs.writeFileSync(path.join(proof, 'copies.json'), JSON.stringify({original: initial.source, first, second, part, invalid, firstID, secondID, partID}, null, 2));
    checks.push('two windows and an unfinished secondary file checkpoint independently while the saved song stays unchanged');
  } else {
    const copies = JSON.parse(fs.readFileSync(path.join(proof, 'copies.json'), 'utf8'));
    if (phase === 'verify') {
      assert.equal((await state()).source, copies.second);
      assert.equal(await page.locator('#score textarea').inputValue(), copies.second);
      assert.equal(await page.locator('.source-draft-note').count(), 0);
      await open(page, 'parts/patterns.cicada');
      assert.equal(await page.locator('#score textarea').inputValue(), copies.part);
      assert.equal(await page.locator('.source-draft-note').count(), 0);
      checks.push('another restart keeps the explicit save and does not revive discarded copies');
    } else {
      assert.equal((await state()).source, copies.original);
      assert.equal(await page.locator('#score textarea').inputValue(), copies.second);
      await page.getByText('Recovered draft. The score file is unchanged.', {exact: true}).waitFor();
      assert.equal(await page.locator('#score form').evaluate(node => node.hasAttribute('data-code-unsaved')), true);
      await page.getByText('Other recovery copies', {exact: true}).click();
      await page.screenshot({path: path.join(proof, 'recovered-code.png')});
      await page.setViewportSize({width: 390, height: 840});
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      await page.locator('#score').screenshot({path: path.join(proof, 'recovered-code-phone.png')});
      await page.setViewportSize({width: 1280, height: 840});
      await page.getByRole('button', {name: 'Save file', exact: true}).click();
      await until(async () => (await state()).source === copies.second && await page.locator('.source-draft-note').count() === 0, 'saving one copy does not put another copy into the editor');
      assert.equal(await page.locator('#score textarea').inputValue(), copies.second);
      await open(page, 'main.cicada', copies.firstID);
      assert.equal(await page.locator('#score textarea').inputValue(), copies.first);
      await page.getByRole('button', {name: 'Save combined draft', exact: true}).waitFor();
      await page.getByRole('button', {name: 'Discard draft', exact: true}).click();
      await until(async () => await page.locator('.source-draft-note').count() === 0, 'discard removes only the selected copy');
      assert.equal((await state()).source, copies.second);
      await open(page, 'parts/patterns.cicada');
      assert.equal(await page.locator('#score textarea').inputValue(), copies.invalid);
      await page.getByRole('button', {name: 'Discard draft', exact: true}).click();
      await until(async () => await page.locator('.source-draft-note').count() === 0, 'unfinished secondary draft is discarded');
      checks.push('restart restores raw edits; Save applies one copy and Discard leaves the saved song and other files unchanged');
    }
  }
  await context.storageState({path: cookies});
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(proof, phase + '-report.json'), JSON.stringify({checks, errors}, null, 2) + '\n');
  complete = true;
});
