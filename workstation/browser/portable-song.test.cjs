// Run twice against a generated portable-song fixture, restarting Studio between runs.
'use strict';
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const reopening = process.env.CICADA_PORTABLE_REOPEN === '1';
test(reopening ? 'reopen a portable recording project and reproduce its WAV' : 'select retained scene takes in a portable recording project', {timeout: 180000}, async t => {
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const fixture = JSON.parse(fs.readFileSync(process.env.CICADA_PORTABLE_FIXTURE, 'utf8'));
  const proof = process.env.CICADA_PORTABLE_PROOF_DIR;
  assert.ok(path.isAbsolute(proof));
  assert.ok(fixture.originalRemoved && fixture.userLibrariesRemoved);
  assert.equal(fs.existsSync(path.join(fixture.fixture, 'original')), false);
  assert.equal(fs.existsSync(fixture.userLibraries), false);
  fs.mkdirSync(proof, {recursive: true});
  const browser = await chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH});
  const page = await browser.newPage({viewport: {width: 1280, height: 840}, acceptDownloads: true});
  const errors = [], checks = [];
  let complete = false;
  t.after(async () => {
    try {
      if (!complete) {
        await page.screenshot({path: path.join(proof, 'failure.png')});
        fs.writeFileSync(path.join(proof, 'failure.txt'), (await page.locator('body').innerText()).slice(0, 16000));
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
  const ready = () => page.locator('#studio-shell[data-shell-actions=ready][data-shell-layout=ready]').waitFor();
  const saved = async (check, label) => {
    await until(async () => {
      const state = await read('state');
      return state.valid && check(state.source) && await page.locator('#undo-form input[name=revision]').inputValue() === state.revision;
    }, label);
    await ready();
    checks.push(label);
  };
  await page.goto(origin.href);
  await page.goto(new URL('/studio', origin).href);
  await ready();
  const initial = await read('state'), audio = await read('audio/config');
  assert.ok(initial.valid && initial.source.includes('title "Carry the song"'));
  assert.equal(String(audio.inventory.backendName || audio.inventory.backend).toLowerCase(), 'null');
  assert.equal(audio.options.inputEnabled, false);
  const rows = page.locator('#scene-takes [data-scene-take]');
  assert.equal(await rows.count(), 2);
  const first = page.locator(`[data-scene-take="${fixture.takeIDs[0]}"]`);
  const second = page.locator(`[data-scene-take="${fixture.takeIDs[1]}"]`);
  assert.equal(await second.getByRole('button', {name: 'In verse', exact: true}).isDisabled(), true);
  const preview = await page.request.get(new URL(await first.locator('audio').getAttribute('src'), origin).href);
  assert.equal(preview.status(), 200);
  assert.equal((await preview.body()).subarray(0, 4).toString(), 'RIFF');
  checks.push('both scene recordings and their private WAV previews survive Save As');
  if (reopening) {
    const expected = JSON.parse(fs.readFileSync(path.join(proof, 'report.json'), 'utf8'));
    assert.equal(initial.source, expected.source);
    assert.equal(initial.revision, expected.revision);
    const wav = await render('reopened.wav');
    assert.ok(wav.equals(fs.readFileSync(path.join(proof, 'baseline.wav'))));
    const notes = await exportNotes('reopened.mid');
    assert.ok(notes.equals(fs.readFileSync(path.join(proof, 'baseline.mid'))));
    checks.push('a fresh Studio process reproduces the saved song byte for byte');
  } else {
    const baseline = await render('baseline.wav');
    const baselineNotes = await exportNotes('baseline.mid');
    const part = path.join(path.dirname(fixture.score), 'parts/rhythm.cicada');
    const secondary = fs.readFileSync(part);
    assert.ok(await first.locator('input[name=source_revision]').inputValue(), 'take selection carries the complete displayed source revision');
    const external = Buffer.concat([secondary, Buffer.from('\n// Another arrangement editor\n')]);
    await page.route('**/__actions/take', async route => {
      fs.writeFileSync(part, external); // The form already captured the displayed snapshot.
      await route.continue();
    }, {times: 1});
    await first.getByRole('button', {name: 'Use in verse', exact: true}).click();
    await until(async () => (await page.locator('body').innerText()).includes('saved score files or libraries changed'), 'an unseen secondary write rejects take selection');
    assert.equal((await read('state')).source, initial.source);
    assert.ok(fs.readFileSync(part).equals(external));
    fs.writeFileSync(part, secondary);
    await page.reload();
    await ready();
    checks.push('an unseen secondary-source edit refuses stale take selection and retains the external arrangement');
    await first.getByRole('button', {name: 'Use in verse', exact: true}).focus();
    await page.keyboard.press('Enter');
    await saved(source => source.includes('asset ' + fixture.takeIDs[0] + ' '), 'keyboard selection uses the retained first pass');
    assert.equal(await first.getByRole('button', {name: 'In verse', exact: true}).isDisabled(), true);
    const selected = (await read('state')).source;
    assert.ok(selected.includes('import "demo/tone"'));
    const expectedPart = fixture.secondaryScenes ? Buffer.from(secondary.toString().replace('guitar = ' + fixture.takeIDs[1] + '-clip', 'guitar = ' + fixture.takeIDs[0] + '-clip')) : secondary;
    assert.ok(fs.readFileSync(part).equals(expectedPart), 'selection changes only the owning scene assignment and preserves the secondary patterns');
    const changed = await render('first-pass.wav');
    assert.equal(changed.equals(baseline), false, 'the selected recording changes the rendered song');
    assert.ok((await exportNotes('first-pass.mid')).equals(baselineNotes), 'changing only the recording leaves exported instrument notes unchanged');
    await page.locator('#undo-form button').click();
    await saved(source => source === initial.source, 'Undo restores the exact saved score');
    await page.locator('#redo-form button').click();
    await saved(source => source === selected, 'Redo restores the selected recording');
    await page.locator('#undo-form button').click();
    await saved(source => source === initial.source, 'Undo returns to the original second pass');
    assert.ok(fs.readFileSync(part).equals(secondary), 'Undo restores the exact secondary source');
    assert.ok((await render('restored.wav')).equals(baseline), 'Undo restores the exported audio too');
    await midiSourceConflict(part, secondary, baselineNotes);
    const notices = page.locator('[data-gosx-toast-host] button');
    while (await notices.count()) await notices.first().click();
    const dock = page.getByRole('separator', {name: 'Resize Detail dock', exact: true});
    await dock.focus();
    await page.keyboard.press('Home');
    await page.getByRole('link', {name: 'Record & comp vocals', exact: true}).click();
    await frameTakes();
    await page.screenshot({path: path.join(proof, 'scene-takes.png')});
    await page.setViewportSize({width: 390, height: 840});
    await frameTakes();
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    assert.equal(await first.getByRole('button', {name: 'Use in verse', exact: true}).isEnabled(), true);
    await page.screenshot({path: path.join(proof, 'scene-takes-phone.png')});
  }
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(proof, reopening ? 'reopen-report.json' : 'report.json'), JSON.stringify({checks, source: initial.source, revision: initial.revision, originalWavSHA256: fixture.wavSHA256, exportedWavSHA256: crypto.createHash('sha256').update(fs.readFileSync(path.join(proof, reopening ? 'reopened.wav' : 'baseline.wav'))).digest('hex'), errors}, null, 2) + '\n');
  complete = true;

  async function frameTakes() {
    await page.locator('#scene-takes').evaluate(node => {
      const pane = node.closest('.shell-scroll');
      // Leave the section heading below Arrange's sticky navigation.
      pane.scrollTop += node.getBoundingClientRect().top - pane.getBoundingClientRect().top - 56;
    });
  }

  async function render(filename) {
    await page.getByRole('toolbar', {name: 'Transport'}).getByRole('button', {name: 'Export', exact: true}).click();
    const previous = (await read('export')).id;
    await page.locator('#export').getByRole('button', {name: 'Render WAV', exact: true}).click();
    let job;
    await until(async () => {
      job = await read('export');
      assert.notEqual(job.state, 'failed', JSON.stringify(job));
      return job.id !== previous && ['succeeded', 'shortfall'].includes(job.state);
    }, 'WAV render completes');
    const response = await page.request.get(new URL(job.downloadURL, origin).href);
    assert.equal(response.status(), 200);
    const wav = await response.body();
    assert.equal(wav.subarray(0, 4).toString(), 'RIFF');
    fs.writeFileSync(path.join(proof, filename), wav);
    await page.keyboard.press('Escape');
    return wav;
  }

  async function exportNotes(filename) {
    const wav = await read('export');
    await page.getByRole('toolbar', {name: 'Transport'}).getByRole('button', {name: 'Export', exact: true}).click();
    const details = page.locator('#midi-export');
    if (!await details.evaluate(node => node.open)) await details.locator('summary').click();
    const response = page.waitForResponse(reply => new URL(reply.url()).pathname === '/__actions/export-midi');
    await details.getByRole('button', {name: 'Prepare MIDI', exact: true}).click();
    assert.equal((await response).status(), 200);
    let notes;
    await until(async () => { notes = await read('export/midi'); return notes.state === 'ready'; }, 'MIDI completes');
    await until(async () => await details.locator('a[download]').getAttribute('href') === notes.downloadURL, 'the new MIDI link is projected');
    await details.getByText('These notes match the saved score.', {exact: true}).waitFor();
    const delivery = page.waitForEvent('download');
    await details.getByRole('link', {name: 'Download MIDI', exact: true}).click();
    const download = await delivery;
    assert.match(download.suggestedFilename(), /-notes\.mid$/);
    await download.saveAs(path.join(proof, filename));
    const bytes = fs.readFileSync(path.join(proof, filename));
    assert.equal(bytes.subarray(0, 4).toString(), 'MThd');
    assert.equal(crypto.createHash('sha256').update(bytes).digest('hex'), fixture.midiSHA256);
    assert.equal((await read('export')).id, wav.id, 'MIDI preserves the completed WAV');
    checks.push('MIDI downloads instrument notes and preserves the WAV');
    await page.keyboard.press('Escape');
    return bytes;
  }

  async function midiSourceConflict(part, original, baseline) {
    const editor = page.locator('#score textarea');
    const savedCode = await editor.inputValue();
    const draft = savedCode.replace('tempo 108', 'tempo 114');
    assert.notEqual(draft, savedCode);
    await editor.fill(draft);
    await page.getByRole('toolbar', {name: 'Transport'}).getByRole('button', {name: 'Export', exact: true}).click();
    const details = page.locator('#midi-export');
    if (!await details.evaluate(node => node.open)) await details.locator('summary').click();
    const previous = await read('export/midi'), wav = await read('export');
    const handle = await editor.elementHandle();
    try {
      fs.writeFileSync(part, Buffer.concat([original, Buffer.from('\n// another editor saved this file\n')]));
      const response = page.waitForResponse(reply => new URL(reply.url()).pathname === '/__actions/export-midi');
      await details.getByRole('button', {name: 'Prepare MIDI', exact: true}).click();
      assert.equal((await response).status(), 409);
      await details.getByText('Saved score files or libraries changed. Refresh Studio before exporting notes again.', {exact: true}).waitFor();
      assert.equal((await read('export/midi')).id, previous.id);
      assert.equal((await read('export')).id, wav.id);
      assert.equal(await editor.inputValue(), draft);
      assert.equal(await handle.evaluate(node => node.isConnected), true);
      assert.equal(await page.locator('#studio-workbench').evaluate(node => node.inert), true);
      const delivery = page.waitForEvent('download');
      await details.getByRole('link', {name: 'Download earlier MIDI', exact: true}).click();
      await (await delivery).saveAs(path.join(proof, 'earlier.mid'));
      assert.ok(fs.readFileSync(path.join(proof, 'earlier.mid')).equals(baseline));
      await details.scrollIntoViewIfNeeded();
      await page.screenshot({path: path.join(proof, 'midi-conflict.png')});
      checks.push('an unseen secondary save retains the draft, modal, prior MIDI and WAV');
    } finally {
      fs.writeFileSync(part, original);
    }
    const response = page.waitForResponse(reply => new URL(reply.url()).pathname === '/__actions/export-midi');
    await details.getByRole('button', {name: 'Prepare MIDI', exact: true}).click();
    assert.equal((await response).status(), 200);
    const retry = await read('export/midi');
    await until(async () => await details.locator('a[download]').getAttribute('href') === retry.downloadURL, 'retry projects its MIDI link');
    await details.getByText('These notes match the saved score.', {exact: true}).waitFor();
    assert.equal(await details.locator('.form-status').textContent(), 'MIDI is ready to download.');
    assert.equal(await editor.inputValue(), draft);
    await details.scrollIntoViewIfNeeded();
    await page.screenshot({path: path.join(proof, 'midi-export.png')});
    await page.setViewportSize({width: 390, height: 840});
    await details.scrollIntoViewIfNeeded();
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    const delivery = page.waitForEvent('download');
    await details.getByRole('link', {name: 'Download MIDI', exact: true}).click();
    await (await delivery).saveAs(path.join(proof, 'phone.mid'));
    assert.ok(fs.readFileSync(path.join(proof, 'phone.mid')).equals(baseline));
    await page.screenshot({path: path.join(proof, 'midi-export-phone.png')});
    await page.setViewportSize({width: 1280, height: 840});
    await page.keyboard.press('Escape');
    await editor.fill(savedCode);
    checks.push('restoring the reviewed saved files allows retry without losing the unsaved draft');
  }
});
