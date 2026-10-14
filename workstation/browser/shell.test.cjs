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
    const browser = await playwright().chromium.launch({headless: process.env.CICADA_HEADLESS !== '0', executablePath});
    shared = {browser, context: await browser.newContext({viewport: {width: 1440, height: 900}})};
  }
  return shared;
}

async function openShell(search = '', viewport) {
  assert.ok(process.env.CICADA_STUDIO_URL, 'set CICADA_STUDIO_URL to a running Studio');
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(base.hostname), 'use a local Studio');
  const {context} = await sharedBrowser();
  const page = await context.newPage();
  page.setDefaultTimeout(15_000);
  if (viewport) await page.setViewportSize(viewport);
  const requests = [], errors = [];
  page.on('request', request => requests.push(`${request.method()} ${new URL(request.url()).pathname}`));
  page.on('pageerror', error => errors.push(error.message));
  const target = new URL('/studio', base);
  target.search = search;
  await page.goto(target.href);
  await page.locator('#studio-shell').waitFor();
  // The workbench chunk registers the commands once it has loaded.
  await page.waitForFunction(() => window.__gosx?.commands?.list?.().length > 0);
  return {page, requests, errors};
}

const actionPosts = requests => requests.filter(request => request.startsWith('POST /__actions/'));

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

test('shell keyboard: a reserved chord does nothing and is listed as unavailable', async () => {
  const {page, requests} = await openShell();
  const before = requests.length;
  const unavailable = page.evaluate(() => new Promise(resolve => document.addEventListener('gosx:command:unavailable', event => resolve(event.detail.id), {once: true})));
  await page.keyboard.press('Control+j');
  assert.equal(await unavailable, 'arrange.consolidate');
  assert.deepEqual(requests.slice(before), [], 'a reserved chord sends nothing');
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
  await page.close();
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

// The next tests wait on pieces that are not in GoSX yet. They stay in the file
// as skipped tests so the assertions are ready when the pieces land.

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

test('a sampler resample from the shell refreshes Arrange and the Code lens', async t => {
  const {page} = await openShell('?tab=arrange');
  const loaded = await page.evaluate(() => document.querySelectorAll('[data-sampler-pad]:not([disabled])').length);
  if (loaded === 0) {
    t.skip('no sample pads are loaded in this score');
    await page.close();
    return;
  }
  await page.locator('[data-sampler-control="record"]').click();
  await page.locator('[data-sampler-pad]:not([disabled])').first().click();
  await page.locator('[data-sampler-control="stop"]').click();
  await page.locator('[data-sampler-resample] button[type="submit"]').click();
  await page.locator('#studio-code', {hasText: 'pad-print'}).waitFor({timeout: 20_000});
  await page.close();
});

// The workbench chunk in GoSX PR 605 adds the roving tab stop and the tab keys,
// and wires $view and $dock to the tabs. Set CICADA_SHELL_TABS=1 once the pinned
// GoSX has them.
const tabsReady = process.env.CICADA_SHELL_TABS === '1' ? false : 'pending GoSX PR 605 (tab keys and the $view binding)';

test('arrow keys, Home and End move between tabs inside the tab list', {skip: tabsReady}, async () => {
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

test('Alt+Shift+2 shows the Session view and Alt+Shift+5 the Device tab', {skip: tabsReady}, async () => {
  const {page} = await openShell();
  await page.keyboard.press('Alt+Shift+Digit2');
  await page.locator('#view-tab-session[aria-selected="true"]').waitFor();
  await page.keyboard.press('Alt+Shift+Digit5');
  await page.locator('#detail-tab-device[aria-selected="true"]').waitFor();
  await page.close();
});

test('Control+Z and Control+Shift+Z submit the undo and redo forms', {skip: 'pending S0'}, async () => {
  const {page, requests} = await openShell();
  await page.keyboard.press('Control+z');
  await page.waitForFunction(() => performance.getEntriesByType('resource').some(entry => entry.name.endsWith('/__actions/undo')));
  assert.ok(actionPosts(requests).some(request => request.endsWith('/__actions/undo')));
  await page.keyboard.press('Control+Shift+z');
  await page.waitForFunction(() => performance.getEntriesByType('resource').some(entry => entry.name.endsWith('/__actions/redo')));
  await page.close();
});

test('Home posts the transport home action', {skip: 'pending S2: the command relay forwards the $command signal'}, async () => {
  const {page, requests} = await openShell();
  await page.keyboard.press('Home');
  await page.waitForFunction(() => performance.getEntriesByType('resource').some(entry => entry.name.endsWith('/__actions/transport')));
  assert.ok(actionPosts(requests).some(request => request.endsWith('/__actions/transport')));
  await page.close();
});

test.after(async () => { if (shared) await shared.browser.close(); });
