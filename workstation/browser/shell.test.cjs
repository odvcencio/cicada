/*
 * Studio shell regression. Application behavior remains GoSX; JavaScript in
 * this file only drives and observes the browser.
 *
 * ./build/cicada studio /path/to/disposable.cicada --audio null --listen 127.0.0.1:8190
 * CICADA_STUDIO_URL=http://127.0.0.1:8190 node --test workstation/browser/shell.test.cjs
 *
 * Install Playwright in the test environment, or set CICADA_PLAYWRIGHT_MODULE
 * to its package path or a createRequire anchor. CICADA_CHROME_PATH selects an
 * installed Chrome; CICADA_HEADLESS=0 shows it. The tests only read pages and
 * observe requests, so any running Studio works.
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
  await page.keyboard.press('Shift+/');
  const dialog = page.locator('#palette');
  await dialog.waitFor({state: 'visible'});
  const row = dialog.locator('li', {hasText: 'Consolidate'});
  assert.match(await row.innerText(), /Unavailable/);
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

test('Tab switches the view and Shift+Tab the dock', {skip: 'pending GoSX Tabs recipe binding (GoSX PR 605): $view has no listener until the recipe lands'}, async () => {
  const {page} = await openShell();
  await page.keyboard.press('Tab');
  await page.locator('a[href="?tab=session"][aria-current="page"]').waitFor();
  await page.keyboard.press('Tab');
  await page.locator('a[href="?tab=arrange"][aria-current="page"]').waitFor();
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
