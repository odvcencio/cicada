// Run against a disposable copy of examples/studio-project with --audio null.
'use strict';
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

test('an unseen secondary-file save cannot replace the previous export', {timeout: 120000}, async t => {
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const proof = process.env.CICADA_EXPORT_PROOF_DIR;
  assert.ok(path.isAbsolute(proof));
  fs.mkdirSync(proof, {recursive: true});
  const browser = await chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH});
  const context = await browser.newContext({viewport: {width: 1280, height: 840}, acceptDownloads: true});
  const page = await context.newPage(), errors = [], steps = [];
  let complete = false;
  t.after(async () => {
    try {
      if (!complete) {
        await page.screenshot({path: path.join(proof, 'failure.png')});
        fs.writeFileSync(path.join(proof, 'failure.txt'), (await page.locator('body').innerText()).slice(0, 12000));
      }
    } finally {
      await browser.close();
    }
  });
  page.on('pageerror', error => errors.push(error.message));
  const origin = new URL(process.env.CICADA_STUDIO_URL);
  const read = async name => {
    const response = await page.request.get(new URL('/api/' + name, origin).href);
    assert.equal(response.status(), 200);
    return response.json();
  };
  const until = async (check, label) => {
    const end = Date.now() + 40000;
    while (Date.now() < end) {
      if (await check()) return;
      await new Promise(resolve => setTimeout(resolve, 40));
    }
    assert.fail(label);
  };
  await page.goto(origin.href);
  await page.goto(new URL('/studio?file=main.cicada', origin).href);
  await page.locator('#studio-shell[data-shell-actions=ready][data-shell-layout=ready]').waitFor();
  const saved = await read('state'), audio = await read('audio/config');
  assert.ok(saved.source.includes('title "Studio project"'), 'use the disposable multi-file fixture');
  assert.equal(String(audio.inventory.backendName || audio.inventory.backend).toLowerCase(), 'null');
  assert.equal(audio.options.inputEnabled, false);
  const editor = page.locator('#score textarea'), original = await editor.inputValue();
  const open = () => page.getByRole('toolbar', {name: 'Transport'}).getByRole('button', {name: 'Export', exact: true}).click();
  const form = () => page.locator('#export form[action$="/__actions/export"]');
  const render = async previousID => {
    const response = page.waitForResponse(reply => new URL(reply.url()).pathname === '/__actions/export');
    await form().getByRole('button', {name: 'Render WAV', exact: true}).click();
    const acknowledgement = await response;
    assert.equal(acknowledgement.status(), 200);
    assert.equal((await acknowledgement.json()).message, 'Rendering started.');
    let job;
    await until(async () => {
      job = await read('export');
      assert.notEqual(job.state, 'failed', JSON.stringify(job));
      return job.id !== previousID && ['succeeded', 'shortfall'].includes(job.state);
    }, 'render completes');
    return job;
  };
  await open();
  const seen = await form().locator('[name=source_revision]').inputValue();
  assert.match(seen, /^[a-f0-9]{64}$/);
  const first = await render('');
  assert.equal(first.source_revision, seen);
  const firstWAV = await (await page.request.get(new URL(first.downloadURL, origin).href)).body();
  assert.equal(firstWAV.subarray(0, 4).toString(), 'RIFF');
  fs.writeFileSync(path.join(proof, 'before.wav'), firstWAV);
  await page.keyboard.press('Escape');
  const draft = original + '\n// Unsaved export review draft\n';
  await editor.fill(draft);
  await open();
  const handle = await editor.elementHandle();
  await page.evaluate(() => {
    window.__exportReviewNavigations = 0;
    document.addEventListener('gosx:navigate', () => window.__exportReviewNavigations++);
  });

  // A second editor changes a different score file; the main-file revision stays put.
  const other = await context.newPage();
  await other.goto(new URL('/?panel=code&file=parts/patterns.cicada', origin).href);
  const otherForm = other.locator('#score form');
  const part = await other.locator('#score textarea').inputValue();
  const changed = part.replace('1 . 5 .', '2 . 6 .');
  assert.notEqual(changed, part);
  const update = await other.request.post(new URL('/__actions/source', origin).href, {
    form: {
      csrf_token: await otherForm.locator('[name=csrf_token]').inputValue(),
      revision: saved.revision,
      fileRevision: await otherForm.locator('[name=fileRevision]').inputValue(),
      file: 'parts/patterns.cicada', content: changed, intent: 'save',
      __gosx_return_to: '/?panel=code&file=parts/patterns.cicada',
    }, maxRedirects: 0,
  });
  assert.equal(update.status(), 303);
  await other.close();
  assert.equal((await read('state')).revision, saved.revision);
  const response = page.waitForResponse(reply => new URL(reply.url()).pathname === '/__actions/export');
  await form().getByRole('button', {name: 'Render WAV', exact: true}).click();
  assert.equal((await response).status(), 409);
  await page.locator('#export').getByText('Saved score files or libraries changed. Refresh Studio before rendering again.', {exact: true}).waitFor();
  assert.equal((await read('export')).id, first.id, 'the rejected request keeps the prior render');
  assert.equal(await editor.inputValue(), draft);
  assert.equal(await handle.evaluate(node => node.isConnected), true);
  assert.equal(await page.locator('#studio-workbench').evaluate(node => node.inert), true);
  assert.equal(await page.evaluate(() => window.__exportReviewNavigations), 0);
  await until(async () => (await page.locator('#export a[download]').textContent()) === 'Download earlier render', 'the earlier WAV remains identified');
  await page.screenshot({path: path.join(proof, 'source-conflict.png')});
  await page.setViewportSize({width: 390, height: 840});
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  const delivery = page.waitForEvent('download');
  await page.getByRole('link', {name: 'Download earlier render', exact: true}).click();
  await (await delivery).saveAs(path.join(proof, 'after-rejection.wav'));
  assert.ok(fs.readFileSync(path.join(proof, 'after-rejection.wav')).equals(firstWAV));
  await page.screenshot({path: path.join(proof, 'source-conflict-phone.png')});
  steps.push('a real secondary-file save is rejected without losing the draft, modal, or previous WAV');

  // Explicitly discard only this test's draft before reloading the saved song.
  await page.setViewportSize({width: 1280, height: 840});
  await page.keyboard.press('Escape');
  await editor.fill(original);
  await page.reload();
  await page.locator('#studio-shell[data-shell-actions=ready][data-shell-layout=ready]').waitFor();
  await open();
  const reviewed = await form().locator('[name=source_revision]').inputValue();
  assert.notEqual(reviewed, seen);
  const fresh = await render(first.id);
  assert.equal(fresh.source_revision, reviewed);
  const freshDownload = await page.request.get(new URL(fresh.downloadURL, origin).href);
  assert.equal(freshDownload.status(), 200);
  const freshWAV = await freshDownload.body();
  assert.equal(freshWAV.equals(firstWAV), false, 'the reviewed melody produces different audio');
  fs.writeFileSync(path.join(proof, 'reviewed.wav'), freshWAV);
  await until(async () => /matches the saved score/.test(await page.locator('#export [data-gosx-live-bind=version]').textContent()), 'the reviewed song renders');
  await page.screenshot({path: path.join(proof, 'reviewed-export.png')});
  steps.push('reloading supplies the reviewed source version and permits a fresh render');
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(proof, 'report.json'), JSON.stringify({steps, seen, reviewed, first, fresh, wavSHA256: crypto.createHash('sha256').update(firstWAV).digest('hex'), reviewedWavSHA256: crypto.createHash('sha256').update(freshWAV).digest('hex'), errors}, null, 2) + '\n');
  complete = true;
});
