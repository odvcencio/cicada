/*
 * Studio shell regression. Application behavior remains GoSX; JavaScript in
 * this file only drives and observes the browser.
 *
 * ./build/cicada studio /path/to/disposable.cicada --audio null --listen 127.0.0.1:8190
 * CICADA_STUDIO_URL=http://127.0.0.1:8190 node --test workstation/browser/shell.test.cjs
 *
 * Install Playwright in the test environment, or set CICADA_PLAYWRIGHT_MODULE
 * to its package path or a createRequire anchor. CICADA_CHROME_PATH selects an
 * installed Chrome; CICADA_HEADLESS=0 shows it. Several tests change the score:
 * they post a pattern edit, press Undo and may record or resample. Run them only
 * against a disposable score on a fresh Studio process, as CI does, because the
 * first browser session to reach a Studio owns it.
 */
'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {createRequire} = require('node:module');
const test = require('node:test');

function playwright() {
  const location = process.env.CICADA_PLAYWRIGHT_MODULE;
  if (!location) return require('playwright');
  try {
    const loaded = require(path.resolve(location));
    if (loaded.chromium) return loaded;
  } catch (error) {
    if (error.code !== 'MODULE_NOT_FOUND') throw error;
  }
  return createRequire(path.resolve(location))('playwright');
}

let shared;
async function sharedBrowser() {
  if (!shared) {
    let executablePath = process.env.CICADA_CHROME_PATH;
    if (!executablePath && process.platform === 'win32') {
      executablePath = [process.env.PROGRAMFILES, process.env['PROGRAMFILES(X86)']]
        .filter(Boolean).map(root => path.join(root, 'Google', 'Chrome', 'Application', 'chrome.exe')).find(candidate => fs.existsSync(candidate));
    }
    const browser = await playwright().chromium.launch({headless: process.env.CICADA_HEADLESS !== '0', executablePath, args: ['--autoplay-policy=no-user-gesture-required']});
    shared = {browser, context: await browser.newContext({viewport: {width: 1440, height: 900}})};
  }
  return shared;
}

async function openShell(search = '', viewport, platform) {
  assert.ok(process.env.CICADA_STUDIO_URL, 'set CICADA_STUDIO_URL to a running Studio');
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(base.hostname), 'use a local Studio');
  const {context} = await sharedBrowser();
  const page = await context.newPage();
  if (platform) await page.addInitScript(value => {
    Object.defineProperty(navigator, 'platform', {get: () => value});
    Object.defineProperty(navigator, 'userAgentData', {get: () => ({platform: value})});
  }, platform);
  page.setDefaultTimeout(15_000);
  if (viewport) await page.setViewportSize(viewport);
  const requests = [], errors = [];
  page.on('request', request => requests.push(`${request.method()} ${new URL(request.url()).pathname}`));
  page.on('pageerror', error => errors.push(error.message));
  const target = new URL('/studio', base);
  target.search = search;
  await page.goto(target.href);
  await page.locator('#studio-shell').waitFor();
  // Commands register in the JS workbench before the Go shell engine mounts.
  // Wait for both before focusing controls or dispatching shell events.
  await page.waitForFunction(() => window.__gosx?.commands?.list?.().length > 0);
  await page.locator('#studio-shell[data-shell-actions="ready"]').waitFor();
  return {page, requests, errors};
}

const actionPosts = requests => requests.filter(request => request.startsWith('POST /__actions/'));
const actionValues = async request => request.headers()['content-type']?.includes('application/json')
  ? request.postDataJSON() : Object.fromEntries(await new Response(request.postDataBuffer(), {headers: {'Content-Type': request.headers()['content-type']}}).formData());

test('shell renders every region without a page scrollbar', async () => {
  const {page, errors} = await openShell();
  for (const id of ['studio-browser', 'studio-center', 'studio-detail', 'studio-code']) {
    assert.ok(await page.locator(`#${id}`).isVisible(), `${id} is visible`);
  }
  assert.ok(await page.locator('[role="toolbar"][aria-label="Transport"]').isVisible());
  const size = await page.evaluate(() => ({scroll: document.documentElement.scrollHeight, client: document.documentElement.clientHeight}));
  assert.equal(size.scroll, size.client, 'the page never scrolls; regions scroll inside');
  assert.deepEqual(errors, []);
  await page.close();
});

test('shell keyboard: a reserved chord does nothing and is listed as unavailable', {timeout: 45_000}, async () => {
  const {page, requests} = await openShell();
  try {
    const before = requests.length;
    // Install the observer before sending either chord. Code owns shortcuts
    // while it has focus; body.focus() alone does not blur a native textarea.
    await page.evaluate(() => {
      window.__reservedChord = {unavailable: [], keys: []};
      document.addEventListener('gosx:command:unavailable', event => window.__reservedChord.unavailable.push(event.detail.id));
      document.addEventListener('keydown', event => window.__reservedChord.keys.push({
        key: event.key, code: event.code, ctrl: event.ctrlKey, meta: event.metaKey,
        prevented: event.defaultPrevented, target: event.target?.id || event.target?.tagName,
      }));
    });
    const editor = page.locator('#studio-code textarea').first();
    await editor.focus();
    await page.keyboard.press('Control+j');
    assert.deepEqual(await page.evaluate(() => window.__reservedChord.unavailable), [], 'the editor retains its shortcut ownership');
    assert.equal(await editor.evaluate(node => document.activeElement === node), true, 'the guarded chord retains editor focus');
    assert.deepEqual(actionPosts(requests.slice(before)), [], 'a chord in the editor submits no shell action');
    await page.evaluate(() => document.activeElement?.blur());
    assert.equal(await page.evaluate(() => document.activeElement === document.body), true, 'the global shortcut starts with body focus');
    await page.keyboard.press('Control+j');
    try {
      await page.waitForFunction(() => window.__reservedChord.unavailable.length > 0, null, {timeout: 15_000});
    } catch (error) {
      const diagnostic = await page.evaluate(() => {
        const commands = window.__gosx?.commands;
        const active = document.activeElement;
        return {
          active: {tag: active?.tagName, id: active?.id, editable: active?.isContentEditable},
          registered: commands?.list?.().some(command => command.id === 'arrange.consolidate'),
          available: commands?.available?.('arrange.consolidate'),
          shell: document.querySelector('#studio-shell')?.getAttribute('data-shell-actions'),
          observed: window.__reservedChord,
        };
      });
      assert.fail('reserved Ctrl+J did not report its unavailable command: ' + JSON.stringify(diagnostic) + '; ' + error.message);
    }
    assert.deepEqual(await page.evaluate(() => window.__reservedChord.unavailable), ['arrange.consolidate']);
    assert.deepEqual(actionPosts(requests.slice(before)), [], 'a reserved chord submits no action');
    // The Shortcuts button opens the same disclosure the palette chord does.
    await page.locator('button[data-gosx-disclosure-target="#palette"]').click();
    const dialog = page.locator('#palette');
    await dialog.waitFor({state: 'visible'});
    const row = dialog.locator('li', {hasText: 'Consolidate'});
    assert.match(await row.innerText(), /Unavailable/);
    await page.keyboard.press('Escape');
    await dialog.waitFor({state: 'hidden'});
    // Browsers report ? as key "?" or, with Shift held, as "/"; both chords are registered.
    await page.keyboard.press('Shift+/');
    await dialog.waitFor({state: 'visible'});
    await page.keyboard.press('Escape');
    await dialog.waitFor({state: 'hidden'});
  } finally {
    await page.close();
  }
});

test('shell fits a 390 px phone without horizontal scroll and shows one region', async () => {
  const {page} = await openShell('', {width: 390, height: 800});
  assert.ok(await page.locator('#studio-center').isVisible());
  for (const id of ['studio-browser', 'studio-detail', 'studio-code']) {
    assert.ok(!(await page.locator(`#${id}`).isVisible()), `${id} is hidden until chosen`);
  }
  const width = await page.evaluate(() => ({scroll: document.documentElement.scrollWidth, client: document.documentElement.clientWidth}));
  assert.ok(width.scroll <= width.client, 'no horizontal scroll');
  await page.locator('.shell-phone-nav a', {hasText: 'Browser'}).click();
  await page.locator('#studio-browser').waitFor({state: 'visible'});
  assert.ok(!(await page.locator('#studio-center').isVisible()));
  await page.close();
});

test('Tab and Shift+Tab move keyboard focus and never switch views', async () => {
  const {page} = await openShell();
  const commands = [];
  await page.exposeFunction('recordCommand', id => commands.push(id));
  await page.evaluate(() => document.addEventListener('gosx:command', event => window.recordCommand(event.detail.id)));
  await page.locator('body').focus();
  const seen = new Set();
  for (let step = 0; step < 6; step++) {
    await page.keyboard.press('Tab');
    seen.add(await page.evaluate(() => document.activeElement?.id || document.activeElement?.tagName));
  }
  assert.ok(seen.size > 1, 'Tab walks through different elements');
  await page.keyboard.press('Shift+Tab');
  assert.deepEqual(commands, [], 'Tab and Shift+Tab run no command');
  await page.locator('#view-tab-arrange[aria-current="page"]').waitFor();
  await page.close();
});

test('tab lists expose roles, states and labels (axe-style structure check)', async () => {
  const {page} = await openShell('?tab=session');
  const problems = await page.evaluate(() => {
    const found = [];
    for (const list of document.querySelectorAll('[role="tablist"]')) {
      if (!list.getAttribute('aria-label')) found.push('tablist without a label');
      const tabs = [...list.querySelectorAll('[role="tab"]')];
      if (tabs.length < 2) found.push('tablist with fewer than two tabs');
      if (tabs.filter(tab => tab.getAttribute('aria-selected') === 'true').length !== 1) found.push('want exactly one selected tab');
      // The chunk gives exactly one tab stop; the server markup gives none or all.
      const stops = tabs.filter(tab => tab.tabIndex === 0).length;
      if (stops !== 1 && stops !== tabs.length) found.push(`tab stops: ${stops}`);
      for (const tab of tabs) {
        const panel = document.getElementById(tab.getAttribute('aria-controls'));
        if (!panel || panel.getAttribute('role') !== 'tabpanel' || panel.getAttribute('aria-labelledby') !== tab.id) found.push(`tab ${tab.id} has no matching tabpanel`);
        else if (panel.hidden === (tab.getAttribute('aria-selected') === 'true')) found.push(`panel for ${tab.id} hidden state disagrees`);
        if (!tab.textContent.trim()) found.push(`tab ${tab.id} has no name`);
      }
    }
    for (const control of document.querySelectorAll('button, [role="separator"]')) {
      if (!(control.textContent.trim() || control.getAttribute('aria-label'))) found.push(`unnamed ${control.tagName} ${control.className}`);
    }
    return found;
  });
  assert.deepEqual(problems, []);
  await page.close();
});

// Engines that save (sampler resample, Live note takes, finished takes) bump a
// signal, and on /studio one watcher reloads the current URL. This test makes the
// change behind the page's back and reloads, which is what that watcher does, and
// checks what a reload guarantees: every revision field matches the saved score,
// the shell is the page that came back, and the next edit is not rejected as stale.
test('a reload after an engine save leaves the shell current', async () => {
  const {page, requests} = await openShell('?tab=arrange');
  const base = new URL(process.env.CICADA_STUDIO_URL);
  const {context} = await sharedBrowser();
  const state = async () => (await context.request.get(new URL('/api/state', base).href)).json();
  const before = await state();
  const csrf = await page.locator('input[name="csrf_token"]').first().inputValue();
  const edit = await context.request.post(new URL('/__actions/pattern', base).href, {
    form: {csrf_token: csrf, revision: before.revision, action: 'toggle', pattern: 'continuity', step: '2', __gosx_return_to: '/studio'},
    maxRedirects: 0,
  });
  assert.ok(edit.status() < 400, `the behind-the-back edit must save, got ${edit.status()}`);
  const after = await state();
  assert.notEqual(after.revision, before.revision, 'the edit advanced the score revision');
  const documents = requests.filter(request => request === 'GET /studio').length;
  await page.reload();
  await page.locator('#studio-shell').waitFor();
  assert.ok(requests.filter(request => request === 'GET /studio').length > documents, 'the reload fetched the shell again');
  const revisions = await page.evaluate(() => [...document.querySelectorAll('#studio-shell input[type="hidden"][name="revision"]')].map(input => input.value));
  assert.ok(revisions.length > 0 && revisions.every(value => value === after.revision), `revision fields ${JSON.stringify(revisions)} must all be ${after.revision}`);
  // The next edit, with no manual navigation, is not rejected as stale.
  const undone = page.waitForResponse(response => response.url().endsWith('/__actions/undo'));
  await page.locator('#undo-form button[type="submit"]').click();
  assert.ok((await undone).status() < 400, 'the edit after a refresh must not conflict');
  await page.close();
});

// Engines ask for a shell refresh through a signal; "cicada:shell-refresh" on the
// document is the same request. A refresh never discards unsaved work: a clean
// page reloads, and a page with typed Code lens text keeps it and shows a notice.
test('a refresh request on a clean page reloads the shell', async () => {
  const {page} = await openShell('?tab=arrange');
  await page.evaluate(() => { window.__alive = true; });
  const reloaded = page.waitForEvent('load');
  await page.evaluate(() => document.dispatchEvent(new Event('cicada:shell-refresh')));
  await reloaded;
  await page.locator('#studio-shell').waitFor();
  assert.equal(await page.evaluate(() => window.__alive === true), false, 'the page reloaded');
  assert.ok(!(await page.locator('#studio-refresh-notice').isVisible()), 'no notice on a clean reload');
  await page.close();
});

test('a refresh request keeps unsaved Code lens text and shows a notice', async () => {
  const {page, requests} = await openShell('?tab=arrange');
  const editor = page.locator('#studio-code textarea').first();
  await editor.focus();
  await page.keyboard.type(' // unsaved note');
  await page.evaluate(() => { window.__alive = true; });
  const documents = requests.filter(request => request === 'GET /studio').length;
  await page.evaluate(() => document.dispatchEvent(new Event('cicada:shell-refresh')));
  await page.locator('#studio-refresh-notice').waitFor({state: 'visible'});
  assert.equal(await page.evaluate(() => window.__alive === true), true, 'the page was not reloaded');
  assert.match(await editor.inputValue(), /\/\/ unsaved note/, 'the typed text survived');
  assert.equal(requests.filter(request => request === 'GET /studio').length, documents, 'no new shell request');
  assert.ok(await page.locator('#studio-refresh-notice a[href^="/studio"]').count() > 0, 'the notice links to the current page');
  await page.close();
});

// A select that renders no selected option shows its first one. Changing it and
// changing it back leaves nothing unsaved, so a refresh request still reloads.
test('changing a select and changing it back is clean', async () => {
  const {page, requests, errors} = await openShell('?tab=session');
  const select = page.locator('[data-live-target]');
  const values = await select.locator('option').evaluateAll(options => options.map(option => option.value));
  assert.ok(values.length >= 2, 'the learn-target select has options to change between');
  const original = await select.inputValue();
  await select.focus(); // a person focuses a control before changing it
  const other = values.find(value => value !== original);
  await select.selectOption(other);
  await select.selectOption(original);
  await page.evaluate(() => { window.__alive = true; });
  const documents = requests.filter(request => request === 'GET /studio').length;
  await page.evaluate(() => document.dispatchEvent(new Event('cicada:shell-refresh')));
  await page.waitForFunction(() => window.__alive !== true);
  await page.locator('#studio-shell[data-shell-actions="ready"]').waitFor();
  assert.equal(await page.evaluate(() => window.__alive === true), false, 'returning the select to its original value permits a real reload');
  assert.ok(requests.filter(request => request === 'GET /studio').length > documents, 'the clean select allowed a new shell request');
  assert.ok(!(await page.locator('#studio-refresh-notice').isVisible()), 'no unsaved-work notice after returning to the original value');
  assert.deepEqual(errors, []);
  await page.close();
});

// Capture state has one publisher for the page. On /studio the Takes engine
// starts it; without it the status would stay on its initial line forever.
test('a fresh shell load publishes native capture status', async () => {
  const {page} = await openShell('?tab=arrange');
  await page.waitForFunction(() => {
    const status = document.querySelector('[data-capture-status]');
    return status && status.textContent.trim() !== '' && !/Refreshing native capture/.test(status.textContent);
  }, null, {timeout: 15_000});
  await page.close();
});

// Live and the Sampler both claim letter keys and both stay mounted. The Sampler
// stops the event at the document when it plays, so a probe on window sees the
// key only if the Sampler did not take it.
test('the closed Sampler does not take the Live keyboard', async () => {
  const {page} = await openShell('?tab=session');
  await page.waitForFunction(() => document.querySelector('#browser-sampler') && document.querySelector('[data-live-control]'));
  await page.evaluate(() => { window.__keys = []; window.addEventListener('keydown', event => window.__keys.push(event.key)); });
  await page.locator('body').focus();
  await page.keyboard.press('a');
  assert.deepEqual(await page.evaluate(() => window.__keys), ['a'], 'with the Sampler section closed, a reaches Live');
  await page.locator('#browser-sampler > summary').click();
  await page.keyboard.press('a');
  assert.deepEqual(await page.evaluate(() => window.__keys), ['a'], 'with the Sampler section open and focused, the Sampler owns the key');
  await page.close();
});

// The Live note take and sampler resample scenarios drive the engines
// themselves. They need pads loaded or an armed audio context, which a Studio
// started with --audio null may not provide; they skip with that reason.
test('a finished Live note take shows its review and commit controls without a reload', async t => {
  const {page, requests} = await openShell('?tab=session');
  const record = page.locator('[data-live-control="record"]');
  try {
    await page.waitForFunction(() => document.querySelector('[data-live-control="record"]:not([disabled])'), null, {timeout: 10_000});
  } catch {
    t.skip('Live input is not ready in this environment (no audio or lease)');
    await page.close();
    return;
  }
  const documents = requests.filter(request => request.startsWith('GET /studio')).length;
  await record.click();
  await page.keyboard.press('a');
  await page.waitForTimeout(150);
  await page.keyboard.up('a');
  await page.locator('[data-live-control="finish"]').click();
  await page.locator('.note-preview').waitFor({timeout: 15_000});
  assert.ok(await page.locator('.note-preview form[action="/__actions/note-commit"], .note-preview form[action$="note-commit"]').count() > 0, 'review shows the commit form');
  assert.ok(requests.filter(request => request.startsWith('GET /studio')).length > documents, 'shown by a shell refresh, not by hand');
  await page.close();
});

// Pads load without audio hardware: the upload decodes in the browser's own
// audio context, as the sampler regression does. Record one hit, check the
// unsaved performance blocks a refresh, resample, and check that the accepted
// performance no longer blocks it, so the shell refreshes by itself.
test('a sampler resample from the shell refreshes Arrange and the Code lens', {timeout: 120_000}, async () => {
  const {page, requests} = await openShell('?tab=arrange');
  const base = new URL(process.env.CICADA_STUDIO_URL);
  const {context} = await sharedBrowser();
  const current = await (await context.request.get(new URL('/api/state', base).href)).json();
  // CI reuses the continuity fixture, whose scenes and song have no Session
  // arrangement. Give this scenario a real placement destination before it
  // resamples, so it exercises both the saved score and the Arrange view.
  if (!/^arrange\s*\{/m.test(current.source)) {
    const source = current.source.replace(/^song\s*\{[^}]*\}/m, 'arrange { place shell-backing keys continuity { at=@1.1.1 length=4bars } }');
    assert.notEqual(source, current.source, 'the disposable song fixture became an arrangement');
    const csrf = await page.locator('input[name=csrf_token]').first().inputValue();
    const response = await context.request.post(new URL('/__actions/source', base).href, {
      form: {csrf_token: csrf, revision: current.revision, content: source, __gosx_return_to: '/studio?tab=arrange'},
      maxRedirects: 0,
    });
    assert.equal(response.status(), 303, 'the arrangement fixture saved');
    const saved = await (await context.request.get(new URL('/api/state', base).href)).json();
    assert.equal(saved.source, source, 'the service accepted the arrangement fixture');
    await page.reload();
    await page.locator('#studio-shell').waitFor();
  }
  const frames = 24000, wav = Buffer.alloc(44 + frames * 8);
  wav.write('RIFF'); wav.writeUInt32LE(wav.length - 8, 4); wav.write('WAVEfmt ', 8); wav.writeUInt32LE(16, 16);
  wav.writeUInt16LE(3, 20); wav.writeUInt16LE(2, 22); wav.writeUInt32LE(48000, 24); wav.writeUInt32LE(384000, 28);
  wav.writeUInt16LE(8, 32); wav.writeUInt16LE(32, 34); wav.write('data', 36); wav.writeUInt32LE(frames * 8, 40);
  for (let i = 0; i < frames; i++) { wav.writeFloatLE(.35 * Math.sin(i * 2 * Math.PI * 220 / 48000), 44 + i * 8); wav.writeFloatLE(.2 * Math.sin(i * 2 * Math.PI * 330 / 48000), 48 + i * 8); }
  const openSampler = async () => {
    if (!(await page.locator('#browser-sampler').evaluate(section => section.open))) await page.locator('#browser-sampler > summary').click();
  };
  await openSampler();
  await page.locator('input[type=file][name=audio]').setInputFiles({name: 'Shell regression.wav', mimeType: 'audio/wav', buffer: wav});
  await page.getByRole('button', {name: 'Import and chop WAV', exact: true}).click();
  await page.waitForFunction(() => document.querySelectorAll('[data-sampler-pad]:not([disabled])').length > 0, null, {timeout: 45_000});
  await openSampler();
  // A waveform boundary drag writes the pad editor through a synthetic input
  // event. The unsaved boundary holds a refresh back until it is saved.
  const endField = page.locator('#sampler-pad-editor form [name=end]');
  const endBefore = await endField.inputValue();
  const canvas = page.locator('[data-sampler-wave]');
  await canvas.scrollIntoViewIfNeeded();
  const wave = await canvas.boundingBox();
  await page.mouse.move(wave.x + wave.width / 16, wave.y + wave.height / 2);
  await page.mouse.down();
  await page.mouse.move(wave.x + wave.width / 10, wave.y + wave.height / 2, {steps: 5});
  await page.mouse.up();
  assert.notEqual(await endField.inputValue(), endBefore, 'the drag moved the end boundary');
  await page.evaluate(() => { window.__alive = true; document.dispatchEvent(new Event('cicada:shell-refresh')); });
  await page.locator('#studio-refresh-notice').waitFor({state: 'visible'});
  assert.equal(await page.evaluate(() => window.__alive === true), true, 'the unsaved boundary survived the refresh request');
  // Saving the pad accepts it. GoSX may swap the page in place or reload it, so
  // wait for the service to hold the new bank, then ask for a refresh until the
  // page reloads: a saved boundary no longer holds the refresh back.
  const bank = async () => (await context.request.get(new URL('/api/sampler', base).href)).json();
  const bankBefore = (await bank()).bankRevision;
  await page.getByRole('button', {name: 'Save pad', exact: true}).click();
  const deadline = Date.now() + 20_000;
  while ((await bank()).bankRevision === bankBefore && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 100));
  assert.notEqual((await bank()).bankRevision, bankBefore, 'the pad saved');
  await page.waitForURL(url => url.pathname === '/studio' && url.searchParams.get('pad') === '1');
  await page.locator('#studio-shell').waitFor();
  assert.equal(new URL(page.url()).pathname, '/studio', 'the accepted managed pad save stays in the shell');
  let reloaded = false;
  for (let attempt = 0; attempt < 10 && !reloaded; attempt++) {
    const loaded = page.waitForEvent('load', {timeout: 2_000}).then(() => true, () => false);
    await page.evaluate(() => document.dispatchEvent(new Event('cicada:shell-refresh'))).catch(() => {});
    reloaded = await loaded;
  }
  assert.ok(reloaded, 'after the save, a refresh request reloads the page');
  await page.locator('#studio-shell').waitFor();
  assert.ok(!(await page.locator('#studio-refresh-notice').isVisible()), 'no notice after the reload');
  await page.waitForFunction(() => document.querySelectorAll('[data-sampler-pad]:not([disabled])').length > 0, null, {timeout: 45_000});
  await openSampler();
  await page.getByRole('button', {name: 'Record pads', exact: true}).click();
  await page.keyboard.press('1');
  const hits = async () => JSON.parse(await page.locator('[data-sampler-resample] input[name=performance]').inputValue()).hits.length;
  await page.waitForFunction(() => JSON.parse(document.querySelector('[data-sampler-resample] input[name=performance]').value).hits.length === 1);
  // Recording has a 100 ms lead-in and rounds hits to the next sixteenth.
  // Let the scheduled hit start; Stop intentionally cancels future hits.
  await page.waitForTimeout(250);
  await page.getByRole('button', {name: 'Stop pads', exact: true}).click();
  assert.equal(await hits(), 1);
  // The unsaved performance is protected from an automatic refresh.
  await page.evaluate(() => { window.__alive = true; document.dispatchEvent(new Event('cicada:shell-refresh')); });
  await page.locator('#studio-refresh-notice').waitFor({state: 'visible'});
  assert.equal(await page.evaluate(() => window.__alive === true), true, 'the unresampled performance survived the refresh request');
  // After a confirmed resample the same request goes through.
  const documents = requests.filter(request => request === 'GET /studio').length;
  await page.locator('[data-sampler-resample] form [name=newName]').fill('shell-print');
  await page.getByRole('button', {name: 'Resample performance', exact: true}).click();
  await page.waitForFunction(() => {
    const status = document.querySelector('[data-sampler-status]')?.textContent || '';
    if (status.startsWith('Resampling failed')) throw new Error(status);
    return document.querySelector('#studio-code textarea')?.value.includes('shell-print');
  }, null, {timeout: 60_000});
  assert.ok(requests.filter(request => request === 'GET /studio').length > documents, 'the shell refreshed by itself');
  assert.ok(!(await page.locator('#studio-refresh-notice').isVisible()), 'no notice after the accepted resample');
  await page.close();
});

test('arrow keys, Home and End move between tabs inside the tab list', async () => {
  const {page} = await openShell();
  const first = page.locator('#view-tab-arrange');
  await first.focus();
  await page.keyboard.press('ArrowRight');
  assert.equal(await page.evaluate(() => document.activeElement.id), 'view-tab-session');
  await page.keyboard.press('End');
  assert.equal(await page.evaluate(() => document.activeElement.id), 'view-tab-mixer');
  await page.keyboard.press('Home');
  assert.equal(await page.evaluate(() => document.activeElement.id), 'view-tab-arrange');
  await page.keyboard.press('ArrowLeft');
  assert.equal(await page.evaluate(() => document.activeElement.id), 'view-tab-mixer', 'Left wraps');
  await page.keyboard.press('Enter');
  await page.locator('#view-tab-mixer[aria-selected="true"]').waitFor();
  assert.ok(await page.locator('#view-panel-mixer').isVisible());
  // One tab stop: Tab leaves the tab list instead of visiting each tab.
  await page.keyboard.press('Tab');
  assert.ok(!(await page.evaluate(() => document.activeElement.closest('[role="tablist"]'))));
  await page.close();
});

test('Alt+Shift+2 shows the Session view and Alt+Shift+5 the Device tab', async () => {
  const {page} = await openShell();
  await page.keyboard.press('Alt+Shift+Digit2');
  await page.locator('#view-tab-session[aria-selected="true"]').waitFor();
  await page.keyboard.press('Alt+Shift+Digit5');
  await page.locator('#detail-tab-device[aria-selected="true"]').waitFor();
  await page.close();
});

test('Mac Cmd save, undo and redo submit the current score forms and leave text undo to the editor', async () => {
  const {page, requests} = await openShell('', undefined, 'MacIntel');
  await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
  await page.route('**/__actions/**', route => route.fulfill({status: 409, contentType: 'application/json', body: '{"ok":false,"message":"Keyboard dispatch checked."}'}));
  for (const [chord, action] of [['Meta+z', 'undo'], ['Meta+Shift+z', 'redo'], ['Meta+s', 'source']]) {
    await page.locator('body').focus();
    const sent = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/' + action);
    await page.keyboard.press(chord);
    const data = await actionValues(await sent);
    assert.ok(data.csrf_token, chord + ' retains the editor token');
    assert.ok(data.revision, chord + ' retains the displayed revision');
    assert.ok(Object.hasOwn(data, 'fileRevision'), chord + ' retains the file revision');
  }
  const editor = page.locator('#score textarea').first();
  await editor.focus();
  const before = actionPosts(requests).length;
  await page.keyboard.press('Meta+z');
  await page.keyboard.press('Meta+Shift+z');
  await page.waitForTimeout(100);
  assert.equal(actionPosts(requests).length, before, 'typing undo never undoes the saved project');
  const saved = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/source');
  await page.keyboard.press('Meta+s');
  assert.ok(Object.hasOwn(await actionValues(await saved), 'content'), 'Cmd+S works while editing score text');
  await page.close();
});

test('shell Space toggles actual playback, Home returns to start, and Audio unlocks after stop', async () => {
  const {page} = await openShell();
  await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
  await page.getByRole('toolbar', {name: 'Transport'}).getByRole('button', {name: 'Audio', exact: true}).click();
  let playing = false;
  await page.route('**/api/transport', route => route.fulfill({contentType: 'application/json', body: JSON.stringify({playing, bar: 1, step: 1})}));
  await page.route('**/api/audio/config?runtime=1', route => route.fulfill({contentType: 'application/json', body: JSON.stringify({playing, runtime: {}, options: {periodFrames: 128}})}));
  await page.route('**/__actions/transport', route => route.fulfill({status: 409, contentType: 'application/json', body: '{"ok":false,"message":"Keyboard dispatch checked."}'}));
  for (const [chord, action] of [['Space', 'play'], ['Space', 'pause'], ['Home', 'home']]) {
    await page.locator('body').focus();
    const sent = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/transport');
    await page.keyboard.press(chord);
    assert.equal((await actionValues(await sent)).action, action);
    if (action === 'play') {
      playing = true;
      await page.waitForFunction(() => document.querySelector('[data-audio-routing]').disabled);
    }
  }
  playing = false;
  await page.waitForFunction(() => !document.querySelector('[data-audio-routing]').disabled);
  assert.ok(await page.locator('[data-audio-lock-notice]').isHidden());
  await page.close();
});

test('Mac record shortcut starts and finishes the chosen vocal take', async () => {
  const {page} = await openShell('', undefined, 'MacIntel');
  await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
  if (!(await page.locator('.vocal-record select[name=track] option[value=keyboard-vocal]').count())) {
    const addTrack = page.locator('.vocal-setup form[action="/__actions/clip"]');
    await addTrack.locator('input[name=newName]').fill('keyboard-vocal');
    await addTrack.locator('button[type=submit]').click();
    await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
  }
  await page.locator('.vocal-record select[name=track] option[value=keyboard-vocal]').waitFor({state: 'attached'});
  await page.locator('.vocal-record select[name=track]').selectOption('keyboard-vocal');
  let active = '';
  await page.route('**/api/capture', route => route.fulfill({contentType: 'application/json', body: JSON.stringify({activeCapture: active, capture: active ? {recording: true} : null})}));
  await page.route('**/__actions/take', route => route.fulfill({status: 409, contentType: 'application/json', body: '{"ok":false,"message":"Keyboard dispatch checked."}'}));
  for (const action of ['record', 'stop']) {
    if (action === 'stop') {
      active = 'keyboard-active';
      await page.waitForFunction(() => !document.querySelector('.vocal-record [data-capture-control=stop]').disabled);
    }
    await page.locator('body').focus();
    const sent = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/take');
    await page.keyboard.press('Meta+Shift+r');
    const data = await actionValues(await sent);
    assert.equal(data.action, action);
    if (action === 'record') assert.equal(data.track, 'keyboard-vocal');
  }
  await page.close();
});

test('shell scene keys submit the corresponding launch-matrix scene', async () => {
  const {page} = await openShell('?tab=session');
  await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
  await page.route('**/__actions/launch', route => route.fulfill({status: 409, contentType: 'application/json', body: '{"ok":false,"message":"Keyboard dispatch checked."}'}));
  const scene = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/launch');
  await page.locator('body').focus();
  await page.keyboard.press('F2');
  assert.deepEqual(JSON.parse((await actionValues(await scene)).command), {action: 'launch', scene: 'alternate'});
  await page.close();
});

test.after(async () => { if (shared) await shared.browser.close(); });
