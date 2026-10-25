// Run against a disposable copy of examples/studio-project with --audio null.
// Load owner-session.cjs through NODE_OPTIONS as in the other Studio checks.
'use strict';
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const {inflateRawSync} = require('node:zlib');

// Read the small fixture archive independently of Cicada's ZIP writer. Central
// directory sizes also handle the data descriptors used by streaming writers.
function readZIP(data) {
  let end = data.length - 22;
  while (end >= Math.max(0, data.length - 65557) && data.readUInt32LE(end) !== 0x06054b50) end--;
  assert.ok(end >= 0, 'ZIP has an end record');
  const count = data.readUInt16LE(end + 10), files = new Map();
  let at = data.readUInt32LE(end + 16);
  for (let i = 0; i < count; i++) {
    assert.equal(data.readUInt32LE(at), 0x02014b50, 'ZIP has a central file record');
    const method = data.readUInt16LE(at + 10), packed = data.readUInt32LE(at + 20);
    const size = data.readUInt32LE(at + 24), nameSize = data.readUInt16LE(at + 28);
    const name = data.toString('utf8', at + 46, at + 46 + nameSize);
    const offset = data.readUInt32LE(at + 42);
    assert.ok(!name.includes('/') && !name.includes('\\') && name !== '..', 'archive contains flat safe names');
    assert.ok(!files.has(name), 'archive names are unique');
    assert.equal(data.readUInt32LE(offset), 0x04034b50, 'ZIP has a local file record');
    const start = offset + 30 + data.readUInt16LE(offset + 26) + data.readUInt16LE(offset + 28);
    assert.ok(start + packed <= data.length, 'ZIP member is complete');
    const compressed = data.subarray(start, start + packed);
    assert.ok(method === 0 || method === 8, 'ZIP uses stored or deflated data');
    const body = method === 0 ? compressed : inflateRawSync(compressed, {maxOutputLength: 32 * 1024 * 1024});
    assert.equal(body.length, size, 'ZIP member size matches');
    files.set(name, body);
    at += 46 + nameSize + data.readUInt16LE(at + 30) + data.readUInt16LE(at + 32);
  }
  return files;
}

function inspectStems(archive, rate) {
  const files = readZIP(archive), manifest = JSON.parse(files.get('manifest.json'));
  assert.equal(manifest.version, 2);
  assert.equal(manifest.from, 1);
  assert.ok(manifest.tracks.length >= 2);
  const tracks = manifest.tracks.map((track, i) => String(i + 1).padStart(2, '0') + '-' + track.id + '.wav');
  const names = [...tracks, 'return-a.wav', 'return-b.wav', 'music.wav', 'sfx.wav', 'master.wav'];
  assert.deepEqual([...files.keys()].sort(), [...names, 'manifest.json'].sort());
  let frames;
  const samples = new Map();
  for (const name of names) {
    const wav = files.get(name);
    assert.equal(wav.toString('ascii', 0, 4), 'RIFF', name);
    assert.equal(wav.toString('ascii', 8, 12), 'WAVE', name);
    assert.equal(wav.readUInt32LE(4), wav.length - 8, name + ' complete RIFF');
    assert.equal(wav.readUInt16LE(20), 3, name + ' IEEE float');
    assert.equal(wav.readUInt16LE(22), 2, name + ' stereo');
    assert.equal(wav.readUInt32LE(24), rate, name + ' sample rate');
    assert.equal(wav.readUInt16LE(34), 32, name + ' bit depth');
    assert.equal(wav.toString('ascii', 36, 40), 'data', name);
    const bytes = wav.readUInt32LE(40);
    assert.equal(bytes % 8, 0, name + ' aligned stereo frames');
    frames ??= bytes / 8;
    assert.equal(bytes / 8, frames, name + ' common duration');
    assert.ok(frames > 3 * rate, 'includes song and effects tail');
    const values = new Float32Array(bytes / 4);
    for (let i = 0; i < values.length; i++) {
      values[i] = wav.readFloatLE(44 + 4 * i);
      assert.ok(Number.isFinite(values[i]), name + ' finite PCM');
    }
    samples.set(name, values);
  }
  let residual = 0, masterPeak = 0;
  for (let i = 0; i < frames * 2; i++) {
    let music = samples.get('return-a.wav')[i] + samples.get('return-b.wav')[i], sfx = 0;
    for (let track = 0; track < tracks.length; track++) {
      if (manifest.tracks[track].bus === 'sfx') sfx += samples.get(tracks[track])[i];
      else music += samples.get(tracks[track])[i];
    }
    residual = Math.max(residual, Math.abs(music - samples.get('music.wav')[i]), Math.abs(sfx - samples.get('sfx.wav')[i]));
    masterPeak = Math.max(masterPeak, Math.abs(samples.get('master.wav')[i]));
  }
  assert.ok(residual <= 0.0001, 'track and return sums match bus taps below -80 dBFS');
  assert.ok(masterPeak > 0.001, 'the exported song contains audible PCM');
  return {tracks: tracks.length, files: files.size, frames, rate, residual, masterPeak};
}

test('Studio delivers aligned stems and retains them across rejected saves', {timeout: 180000}, async t => {
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const proof = process.env.CICADA_STEMS_PROOF_DIR;
  assert.ok(proof && path.isAbsolute(proof), 'provide an absolute CICADA_STEMS_PROOF_DIR');
  fs.mkdirSync(proof, {recursive: true});
  const browser = await chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH});
  const context = await browser.newContext({viewport: {width: 1280, height: 980}, acceptDownloads: true});
  const page = await context.newPage(), errors = [], steps = [];
  let complete = false;
  t.after(async () => {
    try {
      if (!complete && !page.isClosed()) {
        await page.screenshot({path: path.join(proof, 'failure.png')});
        fs.writeFileSync(path.join(proof, 'failure.txt'), (await page.locator('body').innerText()).slice(0, 16000));
      }
    } finally { await browser.close(); }
  });
  page.on('pageerror', error => errors.push(error.message));
  const origin = new URL(process.env.CICADA_STUDIO_URL);
  const read = async name => {
    const response = await page.request.get(new URL('/api/' + name, origin).href);
    assert.equal(response.status(), 200);
    return response.json();
  };
  const until = async (check, label) => {
    const end = Date.now() + 50000;
    while (Date.now() < end) {
      if (await check()) return;
      await new Promise(resolve => setTimeout(resolve, 50));
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
  const draft = original + '\n// Unsaved stems review draft\n';
  await editor.fill(draft);
  const handle = await editor.elementHandle();
  await page.evaluate(() => {
    window.__stemsReviewNavigations = 0;
    document.addEventListener('gosx:navigate', () => window.__stemsReviewNavigations++);
  });
  const open = async () => {
    await page.getByRole('toolbar', {name: 'Transport'}).getByRole('button', {name: 'Export', exact: true}).click();
    const section = page.locator('#stems-export');
    if (!(await section.evaluate(node => node.open))) await section.locator('summary').click();
  };
  const form = () => page.locator('#stems-export form[action$="/__actions/export-stems"]');
  const result = page.locator('#stems-export-result');
  await open();
  const seen = await form().locator('[name=source_revision]').inputValue();
  assert.match(seen, /^[a-f0-9]{64}$/);
  await form().locator('[name=rate]').selectOption('44100');

  // Hold only the acknowledgement of a real request to exercise a slow local
  // service. Rendering and downloaded bytes still come from the real backend.
  let release, reached;
  const gate = new Promise(resolve => { release = resolve; });
  const requestReached = new Promise(resolve => { reached = resolve; });
  let posts = 0;
  await page.route('**/__actions/export-stems', async route => {
    posts++;
    const response = await route.fetch();
    reached();
    await gate;
    await route.fulfill({response});
  });
  const acknowledgement = page.waitForResponse(reply => new URL(reply.url()).pathname === '/__actions/export-stems');
  const button = form().getByRole('button', {name: 'Render stems', exact: true});
  await button.click();
  await requestReached;
  try {
    await until(() => button.isDisabled(), 'a pending stems request disables repeated submission');
    assert.equal(posts, 1);
    assert.equal(await editor.inputValue(), draft);
    assert.equal(await handle.evaluate(node => node.isConnected), true);
    assert.equal(await page.locator('#studio-workbench').evaluate(node => node.inert), true);
  } finally { release(); }
  assert.equal((await acknowledgement).status(), 200);
  await page.unroute('**/__actions/export-stems');
  let first;
  await until(async () => {
    first = await read('export/stems');
    assert.notEqual(first.state, 'failed', JSON.stringify(first));
    return first.state === 'succeeded' && first.id;
  }, 'stems finish rendering');
  assert.equal(first.source_revision, seen);
  assert.equal(first.rate, 44100);
  await until(async () => await result.locator('a[download]').isVisible() && !(await button.isDisabled()), 'download appears and render becomes available');
  const delivery = page.waitForEvent('download');
  await result.locator('a[download]').click();
  const download = await delivery;
  assert.match(download.suggestedFilename(), /\.zip$/);
  const archivePath = path.join(proof, 'stems.zip');
  await download.saveAs(archivePath);
  const archive = fs.readFileSync(archivePath), inspection = inspectStems(archive, 44100);
  assert.equal(inspection.frames, (2 * 4 * 60 / 120 + 3) * 44100, 'two bars plus the three-second effects tail');
  assert.equal(inspection.tracks, first.tracks);
  const response = await page.request.get(new URL(first.downloadURL, origin).href);
  assert.equal(response.status(), 200);
  assert.equal(response.headers()['cache-control'], 'no-store');
  assert.match(response.headers()['content-type'], /application\/zip/);
  assert.equal((await response.body()).equals(archive), true);
  assert.equal(await editor.inputValue(), draft);
  assert.equal(await page.evaluate(() => window.__stemsReviewNavigations), 0);
  await result.scrollIntoViewIfNeeded();
  await page.screenshot({path: path.join(proof, 'stems-ready-1280.png')});
  steps.push('real UI render produces a private ZIP with aligned finite float32 PCM and matching bus sums');
  steps.push('slow acknowledgement prevents duplicate submission while preserving the Code draft and export dialog');

  const other = await context.newPage();
  await other.goto(new URL('/?panel=code&file=parts/patterns.cicada', origin).href);
  const otherForm = other.locator('#score form'), part = await other.locator('#score textarea').inputValue();
  const changed = part.replace('1 . 5 .', '2 . 6 .');
  assert.notEqual(changed, part);
  const update = await other.request.post(new URL('/__actions/source', origin).href, {
    form: {
      csrf_token: await otherForm.locator('[name=csrf_token]').inputValue(), revision: saved.revision,
      fileRevision: await otherForm.locator('[name=fileRevision]').inputValue(),
      file: 'parts/patterns.cicada', content: changed, intent: 'save',
      __gosx_return_to: '/?panel=code&file=parts/patterns.cicada',
    }, maxRedirects: 0,
  });
  assert.equal(update.status(), 303);
  await other.close();
  assert.equal((await read('state')).revision, saved.revision);
  const rejected = page.waitForResponse(reply => new URL(reply.url()).pathname === '/__actions/export-stems');
  await button.click();
  assert.equal((await rejected).status(), 409);
  assert.equal((await read('export/stems')).id, first.id);
  await until(async () => /earlier/i.test(await result.locator('a[download]').textContent()), 'saved edits identify the earlier stems');
  assert.equal(await editor.inputValue(), draft);
  assert.equal(await handle.evaluate(node => node.isConnected), true);
  assert.equal(await page.evaluate(() => window.__stemsReviewNavigations), 0);
  const previous = await page.request.get(new URL(first.downloadURL, origin).href);
  assert.equal(previous.status(), 200);
  assert.equal((await previous.body()).equals(archive), true);
  await page.screenshot({path: path.join(proof, 'stems-conflict-1280.png')});
  await page.setViewportSize({width: 390, height: 840});
  await result.scrollIntoViewIfNeeded();
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  await page.screenshot({path: path.join(proof, 'stems-conflict-390.png')});
  steps.push('a secondary-file change rejects stale export without losing the prior ZIP, unsaved draft, or dialog');

  // Remove this check's draft and review the changed project before rerendering.
  await page.setViewportSize({width: 1280, height: 980});
  await page.keyboard.press('Escape');
  await editor.fill(original);
  await page.reload();
  await page.locator('#studio-shell[data-shell-actions=ready][data-shell-layout=ready]').waitFor();
  await open();
  const reviewed = await form().locator('[name=source_revision]').inputValue();
  assert.notEqual(reviewed, seen);
  await form().locator('[name=rate]').selectOption('44100');
  const freshAcknowledgement = page.waitForResponse(reply => new URL(reply.url()).pathname === '/__actions/export-stems');
  await form().getByRole('button', {name: 'Render stems', exact: true}).click();
  assert.equal((await freshAcknowledgement).status(), 200);
  let fresh;
  await until(async () => {
    fresh = await read('export/stems');
    assert.notEqual(fresh.state, 'failed', JSON.stringify(fresh));
    return fresh.id !== first.id && fresh.state === 'succeeded';
  }, 'reviewed stems finish rendering');
  assert.equal(fresh.source_revision, reviewed);
  const freshResponse = await page.request.get(new URL(fresh.downloadURL, origin).href);
  assert.equal(freshResponse.status(), 200);
  const freshArchive = await freshResponse.body();
  const freshInspection = inspectStems(freshArchive, 44100);
  assert.equal(readZIP(freshArchive).get('01-lead.wav').equals(readZIP(archive).get('01-lead.wav')), false, 'the reviewed melody produces different lead PCM');
  fs.writeFileSync(path.join(proof, 'reviewed-stems.zip'), freshArchive);
  await until(async () => /match.*saved/i.test(await result.locator('[data-gosx-live-bind=version]').textContent()), 'reviewed stems match the saved score');
  await result.scrollIntoViewIfNeeded();
  await page.screenshot({path: path.join(proof, 'stems-reviewed-1280.png')});
  steps.push('reviewing the saved song permits a new aligned stem export');
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(proof, 'report.json'), JSON.stringify({steps, seen, reviewed, first, fresh, inspection, freshInspection, zipSHA256: crypto.createHash('sha256').update(archive).digest('hex'), reviewedZipSHA256: crypto.createHash('sha256').update(freshArchive).digest('hex'), errors}, null, 2) + '\n');
  complete = true;
});
