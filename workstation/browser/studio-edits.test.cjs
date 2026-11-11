/*
 * /studio incremental edits. The shell engine saves piano-roll cells, Undo and
 * Redo through one serial queue, paints them pending at once and patches only
 * the update regions whose content changed, without a GoSX navigation.
 * JavaScript in this file only drives and observes the browser.
 *
 * node workstation/browser/continuity.test.cjs --write-fixture <new-score>
 * ./build/cicada studio <new-score> --audio null --listen 127.0.0.1:8190
 * CICADA_STUDIO_URL=http://127.0.0.1:8190 node --test workstation/browser/studio-edits.test.cjs
 *
 * The suite edits the score: run it only against a fresh disposable fixture on
 * a fresh Studio process, as CI does, because the first browser session to
 * reach a Studio owns it. CICADA_PLAYWRIGHT_MODULE and CICADA_CHROME_PATH
 * select local browser tooling; CICADA_HEADLESS=0 shows the browser.
 */
'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {createRequire} = require('node:module');
const test = require('node:test');

const marker = 'cicada-continuity-test: disposable fixture';

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

async function until(predicate, message, timeout = 15_000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise(resolve => setTimeout(resolve, 20));
  }
  assert.fail(message);
}

let shared;
async function sharedBrowser() {
  if (!shared) {
    let executablePath = process.env.CICADA_CHROME_PATH;
    if (!executablePath && process.platform === 'win32') {
      executablePath = [process.env.PROGRAMFILES, process.env['PROGRAMFILES(X86)']]
        .filter(Boolean).map(root => path.join(root, 'Google', 'Chrome', 'Application', 'chrome.exe')).find(candidate => fs.existsSync(candidate));
    }
    const browser = await playwright().chromium.launch({headless: process.env.CICADA_HEADLESS !== '0', executablePath, args: ['--mute-audio']});
    shared = {browser, context: await browser.newContext({viewport: {width: 1440, height: 900}})};
    const startup = new URL(process.env.CICADA_STUDIO_URL);
    if (startup.pathname.startsWith('/__local/')) {
      const page = await shared.context.newPage();
      await page.goto(startup.href);
      await page.close();
    }
  }
  return shared;
}

test.after(async () => { await shared?.browser.close(); }, {timeout: 15000});

// A second client of the same Studio: another browser context with the
// owner's session, which edits the score while the suite's page is stale.
let otherPage;
async function otherClient() {
  const {browser, context} = await sharedBrowser();
  const {base} = await studio();
  if (!otherPage) {
    const other = await browser.newContext({viewport: {width: 1440, height: 900}, storageState: await context.storageState()});
    otherPage = await other.newPage();
    otherPage.setDefaultTimeout(15_000);
  }
  await otherPage.goto(new URL('/studio?tab=arrange&dock=clip&pattern=continuity&step=1', base).href);
  await otherPage.locator('#studio-shell[data-shell-edits="ready"]').waitFor({timeout: 30_000});
  return otherPage;
}

// One page for the whole suite: every step continues from the last one, and
// none of them may navigate.
let session;
async function studio() {
  if (session) return session;
  assert.ok(process.env.CICADA_STUDIO_URL, 'set CICADA_STUDIO_URL to a disposable Studio');
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(base.hostname), 'use a local disposable Studio');
  const {context} = await sharedBrowser();
  await context.addCookies([{name: 'cicada_panes', value: '11', url: base.origin}]);
  const page = await context.newPage();
  page.setDefaultTimeout(15_000);
  await page.addInitScript(() => {
    try { localStorage.setItem('studio:layout:codeOpen', 'true'); } catch {}
    window.__studioEdits = {navigations: 0, document: crypto.randomUUID()};
    document.addEventListener('gosx:navigate', () => { window.__studioEdits.navigations++; });
  });
  const errors = [], posts = [], reads = [], sentPosts = [];
  let documents = 0, inFlight = 0, maxInFlight = 0;
  page.on('pageerror', error => errors.push(error.message));
  page.on('request', request => {
    const target = new URL(request.url());
    if (request.resourceType() === 'document') documents++;
    if (target.pathname === '/__studio/regions') reads.push(target);
    if (request.method() === 'POST') sentPosts.push(target.pathname);
  });
  // Region reads that failed (readOnly refused them), so a step can wait
  // until a read it must not see has been refused before allowing others.
  const refused = [];
  page.on('requestfailed', request => {
    const target = new URL(request.url());
    if (target.pathname === '/__studio/regions') refused.push(target);
  });
  // A view switch can read a region several times while its panels change;
  // wait for the refused reads of the region to stop before allowing one.
  const refusedRead = async (since, id, message) => {
    const matches = () => refused.slice(since).filter(target => new RegExp(`(^|,)${id}:`).test(target.searchParams.get('ids'))).length;
    await until(() => matches() > 0, message);
    let seen = -1;
    await until(async () => {
      const now = matches();
      if (now === seen) return true;
      seen = now;
      await new Promise(resolve => setTimeout(resolve, 300));
      return false;
    }, message);
  };
  // After a step refused reads: wait until the idle read (its wait doubles
  // after each failure, up to 8 s) has brought every region up to date.
  // A hidden region whose engines changed waits, stale, for a page load
  // until it shows; the idle read skips it.
  const lagging = () => page.evaluate(() => [...document.querySelectorAll('[data-update-region][data-region-stale]:not(#update-code):not([data-region-reload])')].map(node => node.dataset.updateRegion));
  const caughtUp = async () => until(async () => (await lagging()).length === 0, 'the page did not catch up with the saved song', 30_000)
    .catch(async error => { throw new Error(`${error.message}: ${(await lagging()).join(', ')}; status: ${await status()}`); });
  // How many POSTs this page has sent to a path, of any kind.
  const sent = pathname => sentPosts.filter(sentPath => sentPath === pathname).length;
  // Region reads go through only when every region they name is in allow
  // (or allow, a function, accepts the list of names); the rest fail, as if
  // the network dropped them, and the regions stay stale.
  const readOnly = allow => page.route('**/__studio/regions**', route => {
    const ids = (new URL(route.request().url()).searchParams.get('ids') || '').split(',').filter(Boolean).map(entry => entry.split(':')[0]);
    const allowed = typeof allow === 'function' ? allow(ids) : ids.every(id => allow.has(id));
    return ids.length && allowed ? route.continue() : route.abort();
  });
  const state = async endpoint => {
    const response = await context.request.get(new URL(endpoint, base).href);
    assert.equal(response.status(), 200, `${endpoint} must be available`);
    return response.json();
  };
  // Every foreground write goes through this route, so the steps can hold,
  // rewrite or inspect one. Recovery checkpoints pass straight through.
  const gates = [];
  const rewrites = [];
  await page.route('**/__actions/**', async route => {
    const request = route.request();
    const action = new URL(request.url()).pathname.split('/').pop();
    // Only the queue's writes are recorded; engine and GoSX writes pass through.
    if (request.method() !== 'POST' || !['pattern', 'undo', 'redo'].includes(action) || !/json/.test(request.headers()['content-type'] || '')) return route.continue();
    const payload = request.postDataJSON();
    const record = {action, payload, status: 0, result: null, done: false};
    posts.push(record);
    maxInFlight = Math.max(maxInFlight, ++inFlight);
    try {
      const rewrite = rewrites.shift();
      if (rewrite) rewrite(payload);
      const gate = gates.shift();
      if (gate) await gate;
      const response = await route.fetch({postData: JSON.stringify(payload), maxRedirects: 0});
      record.status = response.status();
      record.result = await response.json();
      await route.fulfill({response, body: JSON.stringify(record.result)});
    } finally {
      inFlight--;
      record.done = true;
    }
  });
  const original = await state('/api/state');
  assert.ok(original.source.includes(marker), 'refusing to edit an unmarked score: start Studio on a --write-fixture score');
  await page.goto(new URL('/studio?tab=arrange&dock=clip&pattern=continuity&step=1', base).href);
  await page.locator('#studio-shell[data-shell-actions="ready"]').waitFor({timeout: 30_000});
  await page.locator('#studio-shell[data-shell-edits="ready"]').waitFor();
  const pitch = await page.locator('#detail-panel-clip .piano-roll .roll-row form input[name="pitch"]').nth(8).inputValue();
  const cell = step => `#detail-panel-clip .piano-roll form:has(input[name="pitch"][value="${pitch}"]) .roll-cell[value="${step}"]`;
  const region = id => page.locator(`#update-${id}`);
  const revisionOf = id => region(id).getAttribute('data-region-revision');
  const hold = () => { let release; gates.push(new Promise(resolve => { release = resolve; })); return () => release(); };
  // A cell is confirmed when it shows the wanted state, carries no pending
  // marker, and its region carries the revision the edit produced.
  const confirmed = async (selector, active, revision) => page.waitForFunction(({selector, active, revision}) => {
    const el = document.querySelector(selector);
    const wrapper = el?.closest('[data-update-region]');
    return el && el.getAttribute('data-active') === active && !el.hasAttribute('data-workspace-pending') && wrapper.getAttribute('data-region-revision') === revision;
  }, {selector, active: String(active), revision});
  const settled = async count => until(() => posts.length >= count && posts.slice(0, count).every(record => record.done), `only ${posts.length}/${count} writes settled`);
  const status = () => page.locator('[data-workspace-status]').textContent();
  const noNavigation = async () => {
    const probe = await page.evaluate(() => window.__studioEdits);
    assert.equal(probe.navigations, 0, 'a GoSX navigation ran during an edit');
    assert.equal(documents, 1, 'a document request ran during an edit');
    assert.deepEqual(errors, []);
  };
  session = {page, base, state, posts, reads, sent, readOnly, refused, refusedRead, caughtUp, cell, region, revisionOf, hold, rewrites, confirmed, settled, status, noNavigation, original,
    maxInFlight: () => maxInFlight, documentID: () => page.evaluate(() => window.__studioEdits.document)};
  return session;
}

test('a piano-roll draw paints at once and is confirmed without a navigation', async () => {
  const {page, state, posts, cell, revisionOf, hold, confirmed, settled, noNavigation, original} = await studio();
  const before = await revisionOf('dock-clip');
  const grid = await page.evaluateHandle(() => document.querySelector('#detail-panel-clip .piano-roll'));
  assert.equal(await page.locator(cell(15)).getAttribute('data-active'), 'false');
  const release = hold();
  await page.locator(cell(15)).click();
  // The answer is held, so this state can only be the pending paint.
  await page.waitForFunction(selector => {
    const el = document.querySelector(selector);
    return el.getAttribute('data-active') === 'true' && el.hasAttribute('data-workspace-pending') && el.textContent === '●';
  }, cell(15));
  assert.equal(await revisionOf('dock-clip'), before, 'the pending paint advanced the region revision');
  release();
  await settled(1);
  const record = posts[0];
  assert.equal(record.status, 200, record.result?.message);
  assert.match(record.payload.__cicada_regions, /(^|,)dock-clip:[0-9a-f]{16}(,|$)/, 'the edit names the visible Clip region with its digest');
  assert.doesNotMatch(record.payload.__cicada_regions, /(^|,)center-session:/, 'a hidden tab is not visible');
  const revision = record.result.data.revision;
  assert.ok(revision && revision !== original.revision);
  await confirmed(cell(15), true, revision);
  assert.notEqual((await state('/api/state')).source, original.source, 'the draw was not saved');
  assert.equal(await grid.evaluate(node => node.isConnected && node === document.querySelector('#detail-panel-clip .piano-roll')), true, 'the piano roll was replaced instead of patched');
  // Rule R1: every region the response confirmed carries the new revision in
  // its forms; a hidden region the response did not render keeps its own.
  const fields = await page.evaluate(() => [...document.querySelectorAll('[data-update-region]')].map(wrapper => ({
    id: wrapper.dataset.updateRegion, stale: wrapper.hasAttribute('data-region-stale'), revision: wrapper.dataset.regionRevision,
    inputs: [...wrapper.querySelectorAll('input[type=hidden][name=revision]')].map(input => input.value),
  })));
  for (const field of fields) {
    if (field.stale) continue;
    assert.equal(field.revision, revision, `region ${field.id} is fresh but carries another revision`);
    for (const value of field.inputs) assert.equal(value, revision, `region ${field.id} kept an old revision field`);
  }
  assert.equal(new URL(page.url()).searchParams.get('step'), '15', 'the inspected step follows the drawn cell');
  await noNavigation();
});

test('the answer to one draw keeps the pending paint of the next', async () => {
  const {page, posts, cell, hold, confirmed, settled, noNavigation} = await studio();
  const start = posts.length;
  const releaseFirst = hold(), releaseSecond = hold();
  await page.locator(cell(16)).click();
  await page.locator(cell(17)).click();
  await page.waitForFunction(selector => document.querySelector(selector).hasAttribute('data-workspace-pending'), cell(17));
  releaseFirst();
  await until(() => posts.length > start && posts[start].done, 'the first draw did not settle');
  const first = posts[start].result.data.revision;
  await confirmed(cell(16), true, first);
  // The second edit is still held: its cell must keep the prediction.
  const second = await page.locator(cell(17)).evaluate(el => ({active: el.getAttribute('data-active'), pending: el.hasAttribute('data-workspace-pending'), text: el.textContent}));
  assert.deepEqual(second, {active: 'true', pending: true, text: '●'}, 'the answer to the first draw erased the second pending paint');
  releaseSecond();
  await settled(start + 2);
  assert.equal(posts[start + 1].payload.revision, first, 'the queued draw did not take the confirmed revision');
  await confirmed(cell(17), true, posts[start + 1].result.data.revision);
  await noNavigation();
});

test('Undo and Redo run through the same queue', async () => {
  const {page, posts, cell, hold, confirmed, settled, noNavigation, maxInFlight} = await studio();
  let start = posts.length;
  // A draw and an immediate Undo: the Undo waits for the draw and takes its
  // revision, so the pair leaves the cell as it was.
  const release = hold();
  await page.locator(cell(18)).click();
  await until(() => posts.length > start, 'the draw was not sent');
  await page.locator('#undo-form button[type=submit]').click();
  assert.equal(await page.locator('#undo-form button[type=submit]').evaluate(el => el.hasAttribute('data-workspace-pending')), true, 'Undo was not painted pending');
  assert.equal(posts.length, start + 1, 'Undo was sent before the draw it follows was answered');
  release();
  await settled(start + 2);
  assert.deepEqual(posts.slice(start).map(record => record.action), ['pattern', 'undo']);
  assert.equal(posts[start + 1].payload.revision, posts[start].result.data.revision, 'Undo did not run after the draw');
  await confirmed(cell(18), false, posts[start + 1].result.data.revision);
  start = posts.length;
  await page.evaluate(() => document.activeElement?.blur());
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+Shift+z' : 'Control+Shift+z');
  await settled(start + 1);
  assert.equal(posts[start].action, 'redo');
  await confirmed(cell(18), true, posts[start].result.data.revision);
  start = posts.length;
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+z' : 'Control+z');
  await settled(start + 1);
  assert.equal(posts[start].action, 'undo');
  await confirmed(cell(18), false, posts[start].result.data.revision);
  assert.equal(maxInFlight(), 1, 'writes were sent concurrently');
  await noNavigation();
});

test('a validation error restores the cell and the queue continues', async () => {
  const {page, posts, cell, rewrites, confirmed, settled, status, noNavigation} = await studio();
  const start = posts.length;
  rewrites.push(payload => { payload.step = '99'; });
  await page.locator(cell(19)).click();
  await settled(start + 1);
  assert.equal(posts[start].status, 422);
  await page.waitForFunction(selector => {
    const el = document.querySelector(selector);
    return el.getAttribute('data-active') === 'false' && !el.hasAttribute('data-workspace-pending') && !el.hasAttribute('data-pending-paint');
  }, cell(19));
  await until(async () => /step must be/i.test(await status()), 'the validation message was not shown');
  assert.equal(await page.locator('[data-workspace-retry]').isVisible(), false, 'validation must not block the queue');
  await page.locator(cell(19)).click();
  await settled(start + 2);
  assert.equal(posts[start + 1].status, 200);
  await confirmed(cell(19), true, posts[start + 1].result.data.revision);
  await noNavigation();
});

test('a refused draw drops the Undo queued behind it', async () => {
  const {page, posts, cell, rewrites, hold, settled, noNavigation} = await studio();
  const start = posts.length;
  rewrites.push(payload => { payload.step = '99'; });
  const release = hold();
  await page.locator(cell(25)).click();
  await until(() => posts.length > start, 'the draw was not sent');
  // This Undo was meant for the draw. If the draw is refused, it would undo
  // the user's previous saved edit instead.
  await page.locator('#undo-form button[type=submit]').click();
  release();
  await settled(start + 1);
  assert.equal(posts[start].status, 422);
  await page.waitForFunction(() => !document.querySelector('#undo-form button[type=submit]').hasAttribute('data-workspace-pending'));
  await page.waitForTimeout(400);
  assert.equal(posts.length, start + 1, 'the Undo queued behind a refused draw was sent');
  assert.match(await page.locator('[data-workspace-status]').textContent(), /Undo or Redo waiting behind it was not sent/, 'the dropped Undo was not reported');
  assert.equal(await page.locator(cell(25)).getAttribute('data-active'), 'false');
  await noNavigation();
});

test('a stale revision restores, blocks the queue, and Refresh saved state recovers', async () => {
  const {page, state, posts, reads, cell, rewrites, hold, confirmed, settled, status, noNavigation, original} = await studio();
  const start = posts.length;
  const before = await state('/api/state');
  rewrites.push(payload => { payload.revision = original.revision; });
  const release = hold();
  await page.locator(cell(20)).click();
  await page.locator(cell(21)).click(); // queued behind the refused write
  release();
  await settled(start + 1);
  assert.equal(posts[start].status, 409);
  for (const step of [20, 21]) {
    await page.waitForFunction(selector => {
      const el = document.querySelector(selector);
      return el.getAttribute('data-active') === 'false' && !el.hasAttribute('data-workspace-pending');
    }, cell(step));
  }
  await page.locator('[data-workspace-retry]').waitFor({state: 'visible'});
  assert.match(await status(), /saved revision [0-9a-f]{7}/i, 'the conflict did not show the saved revision');
  assert.equal(posts.length, start + 1, 'the queued draw was sent after a conflict');
  // Blocked: a new draw is refused without a request and without a paint.
  await page.locator(cell(22)).click();
  await until(async () => /refresh saved state/i.test(await status()), 'a blocked draw was not refused');
  assert.equal(posts.length, start + 1, 'a draw was sent while blocked');
  assert.equal(await page.locator(cell(22)).getAttribute('data-active'), 'false');
  assert.deepEqual(await state('/api/state'), before, 'the refused write changed the score');
  const readsBefore = reads.length;
  await page.locator('[data-workspace-retry]').click();
  await until(async () => /Workspace updated/i.test(await status()), 'Refresh saved state did not finish');
  assert.ok(reads.length > readsBefore, 'Refresh saved state did not read the regions');
  assert.equal(await page.locator('[data-workspace-retry]').isVisible(), false);
  await page.locator(cell(22)).click();
  await settled(start + 2);
  assert.equal(posts[start + 1].status, 200, 'the queue did not resume after the refresh');
  await confirmed(cell(22), true, posts[start + 1].result.data.revision);
  await noNavigation();
});

test('a network failure restores the cell, blocks, and is never resent', async () => {
  const {page, state, posts, cell, confirmed, settled, status, noNavigation} = await studio();
  const before = await state('/api/state');
  let aborted = 0;
  await page.route('**/__actions/pattern', route => { aborted++; return route.abort('failed'); });
  await page.locator(cell(26)).click();
  await until(() => aborted === 1, 'the draw did not reach the network');
  await page.waitForFunction(selector => {
    const el = document.querySelector(selector);
    return el.getAttribute('data-active') === 'false' && !el.hasAttribute('data-workspace-pending');
  }, cell(26));
  await page.locator('[data-workspace-retry]').waitFor({state: 'visible'});
  assert.match(await status(), /did not complete/i);
  await page.waitForTimeout(400);
  assert.equal(aborted, 1, 'a failed write was sent again');
  assert.deepEqual(await state('/api/state'), before);
  await page.unroute('**/__actions/pattern');
  await page.locator('[data-workspace-retry]').click();
  await until(async () => /Workspace updated/i.test(await status()), 'Refresh saved state did not finish');
  const start = posts.length;
  await page.locator(cell(26)).click();
  await settled(start + 1);
  await confirmed(cell(26), true, posts[start].result.data.revision);
  await noNavigation();
});

test('a saved write whose view cannot be confirmed keeps its paint pending until a refresh', async () => {
  const {page, state, cell, region, status, noNavigation} = await studio();
  await page.route('**/__actions/pattern', async route => {
    const response = await route.fetch();
    const result = await response.json();
    // Model another editor's save between the write and its view.
    result.data = {...result.data, refreshRequired: true};
    result.message = 'Saved. The score changed before the workspace refreshed.';
    await route.fulfill({response, body: JSON.stringify(result)});
  });
  const before = await state('/api/state');
  await page.locator(cell(27)).click();
  await page.locator('[data-workspace-retry]').waitFor({state: 'visible'});
  const painted = await page.locator(cell(27)).evaluate(el => ({active: el.getAttribute('data-active'), pending: el.hasAttribute('data-workspace-pending')}));
  assert.deepEqual(painted, {active: 'true', pending: true}, 'a saved but unconfirmed write lost its pending paint');
  const saved = await state('/api/state');
  assert.notEqual(saved.revision, before.revision, 'the write was not saved');
  await page.unroute('**/__actions/pattern');
  await page.locator('[data-workspace-retry]').click();
  await until(async () => /Workspace updated/i.test(await status()), 'Refresh saved state did not finish');
  await page.waitForFunction(({selector, revision}) => {
    const el = document.querySelector(selector);
    return el.getAttribute('data-active') === 'true' && !el.hasAttribute('data-workspace-pending') && el.closest('[data-update-region]').dataset.regionRevision === revision;
  }, {selector: cell(27), revision: saved.revision});
  assert.equal(await region('dock-clip').getAttribute('data-region-stale'), null);
  await noNavigation();
});

test('a hidden region is marked stale and refreshed when it opens', async () => {
  const {page, posts, reads, cell, region, revisionOf, confirmed, settled, noNavigation} = await studio();
  // Background reads may refresh hidden regions while the queue is idle.
  // Refuse those here, so only the reveal can refresh the Session view.
  await page.route('**/__studio/regions**', route => {
    const ids = new URL(route.request().url()).searchParams.get('ids') || '';
    return /(^|,)dialog-history:/.test(ids) ? route.abort() : route.continue();
  });
  const start = posts.length;
  await page.locator(cell(23)).click();
  await settled(start + 1);
  const revision = posts[start].result.data.revision;
  await confirmed(cell(23), true, revision);
  await until(async () => await region('center-session').getAttribute('data-region-stale') !== null, 'the hidden Session view was not marked stale');
  assert.notEqual(await revisionOf('center-session'), revision, 'a region not rendered at the new revision advanced (rule R1)');
  const readsBefore = reads.length;
  await page.keyboard.press('Alt+Shift+Digit2');
  await page.locator('#view-tab-session[aria-selected="true"]').waitFor();
  await until(async () => await region('center-session').getAttribute('data-region-stale') === null, 'the revealed Session view stayed stale');
  assert.equal(await revisionOf('center-session'), revision);
  const reveal = reads.slice(readsBefore).find(target => /(^|,)center-session:/.test(target.searchParams.get('ids')));
  assert.ok(reveal, 'the reveal did not read the Session view');
  assert.doesNotMatch(reveal.searchParams.get('ids'), /(^|,)dialog-history:/, 'the reveal read regions that are still hidden');
  const inputs = await page.locator('#update-center-session input[type=hidden][name=revision]').evaluateAll(nodes => nodes.map(node => node.value));
  for (const value of inputs) assert.equal(value, revision, 'the refreshed region kept an old revision field');
  await page.unroute('**/__studio/regions**');
  await page.keyboard.press('Alt+Shift+Digit1');
  await page.locator('#view-tab-arrange[aria-selected="true"]').waitFor();
  await noNavigation();
});

test('Loop part after a draw sends the revision its region shows', async () => {
  const {page, state, cell, posts, settled, confirmed, noNavigation} = await studio();
  const start = posts.length;
  await page.locator(cell(28)).click();
  await settled(start + 1);
  await confirmed(cell(28), true, posts[start].result.data.revision);
  const loop = page.locator('#detail-panel-clip [data-sound-preview]').first();
  await page.waitForFunction(el => !el.disabled, await loop.elementHandle());
  const sent = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/sound-preview' && /"action":"pattern"/.test(request.postData() || ''));
  const answered = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/__actions/sound-preview' && /"action":"pattern"/.test(response.request().postData() || ''));
  await loop.click();
  const payload = (await sent).postDataJSON();
  assert.equal(payload.revision, (await state('/api/state')).revision, 'the preview engine sent the revision it was mounted with');
  assert.equal((await answered).status(), 200, 'the preview was refused after a draw');
  // Stop the loop again.
  if (await loop.getAttribute('aria-pressed') === 'true') await loop.click();
  await noNavigation();
});

test('the Code editor stays mounted while other regions are patched', async () => {
  const {page, posts, cell, settled, confirmed, region, noNavigation} = await studio();
  const editor = await page.locator('#score textarea').elementHandle();
  let start = posts.length;
  await page.locator(cell(29)).click();
  await settled(start + 1);
  await confirmed(cell(29), true, posts[start].result.data.revision);
  await page.waitForTimeout(400);
  assert.equal(await editor.evaluate(node => node.isConnected && node === document.querySelector('#score textarea')), true, 'a draw replaced the visible Code editor');
  // Hidden, the Code lens is left stale; it is read when it opens again.
  await page.locator('.shell-pane-toggle[data-pane=code][data-pane-action=hide]').click();
  await page.waitForFunction(() => !document.querySelector('#studio-code > details').open);
  start = posts.length;
  await page.locator(cell(29)).click();
  await settled(start + 1);
  const revision = posts[start].result.data.revision;
  await confirmed(cell(29), false, revision);
  await page.waitForTimeout(600);
  assert.equal(await editor.evaluate(node => node.isConnected), true, 'a background read replaced the hidden Code editor');
  assert.notEqual(await region('code').getAttribute('data-region-stale'), null, 'the hidden Code lens was read in the background');
  await page.locator('.shell-pane-toggle[data-pane=code][data-pane-action=show]').click();
  await until(async () => await region('code').getAttribute('data-region-revision') === revision, 'the opened Code lens was not read');
  assert.equal(await editor.evaluate(node => node.isConnected && node === document.querySelector('#score textarea')), true, 'opening Code replaced its editor');
  await noNavigation();
});

test('Code typed while a response is applied is held with its file revision', async () => {
  const {page, posts, cell, settled, region} = await studio();
  const fileRevision = await page.locator('#score input[name="fileRevision"]').inputValue();
  // Type as soon as the Clip region changes, which is while the rest of the
  // response is still being applied.
  await page.evaluate(() => {
    const observer = new MutationObserver(() => {
      observer.disconnect();
      const editor = document.querySelector('#score textarea');
      editor.value += '\n// typed during an apply';
      editor.dispatchEvent(new Event('input', {bubbles: true}));
    });
    observer.observe(document.querySelector('#update-dock-clip'), {subtree: true, attributes: true, childList: true, characterData: true});
  });
  const start = posts.length;
  await page.locator(cell(30)).click();
  await settled(start + 1);
  const revision = posts[start].result.data.revision;
  await page.waitForFunction(r => document.querySelector('#update-dock-clip').dataset.regionRevision === r, revision);
  await page.waitForTimeout(400);
  assert.match(await page.locator('#score textarea').inputValue(), /typed during an apply/, 'the Code draft was replaced');
  assert.equal(await page.locator('#score input[name="fileRevision"]').inputValue(), fileRevision, 'the draft took a file revision it was not typed on');
  assert.notEqual(await region('code').getAttribute('data-region-revision'), revision, 'a held Code lens was confirmed fresh');
});

test('a dirty Code draft is held while a Clip edit lands', async () => {
  const {page, posts, cell, region, confirmed, settled, noNavigation} = await studio();
  const editor = page.locator('#score textarea').first();
  await editor.waitFor({state: 'attached'});
  const codeRevision = await region('code').getAttribute('data-region-revision');
  const fileRevision = await page.locator('#score input[name="fileRevision"]').inputValue();
  await editor.focus();
  await page.keyboard.press('Control+End');
  await page.keyboard.type('\n// held draft');
  const draft = await editor.inputValue();
  assert.match(draft, /held draft/);
  const start = posts.length;
  await page.locator(cell(24)).click();
  await settled(start + 1);
  assert.doesNotMatch(posts[start].payload.__cicada_regions, /(^|,)code:/, 'the edit asked for the held Code lens');
  await confirmed(cell(24), true, posts[start].result.data.revision);
  assert.equal(await editor.inputValue(), draft, 'the Code draft was replaced');
  assert.equal(await region('code').getAttribute('data-region-revision'), codeRevision, 'the held Code lens advanced its revision');
  assert.equal(await page.locator('#score input[name="fileRevision"]').inputValue(), fileRevision, 'the held draft lost the revision it was typed on');
  await noNavigation();
});

test('a form in a stale region is sent only after its region is read', async () => {
  const {page, posts, cell, settled, region} = await studio();
  // Hold the reads that follow a write, so the hidden Mixer stays stale.
  const held = [];
  let mixerPost = null;
  page.on('request', request => { if (!mixerPost && request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/mixer') mixerPost = request; });
  await page.route('**/__studio/regions**', route => { held.push(route); });
  const mute = () => page.locator('#view-panel-mixer button[data-toggle=mute]').first();
  let pressed;
  try {
    const start = posts.length;
    await page.locator(cell(32)).click();
    await settled(start + 1);
    await until(() => held.length > 0, 'the write was not followed by a read');
    await page.keyboard.press('Alt+Shift+Digit3');
    await page.locator('#view-tab-mixer[aria-selected="true"]').waitFor();
    assert.notEqual(await region('center-mixer').getAttribute('data-region-stale'), null, 'the Mixer was not stale');
    pressed = await mute().getAttribute('aria-pressed');
    await mute().click();
    await page.waitForTimeout(300);
    assert.equal(mixerPost, null, 'a form in a stale region was sent before its region was read');
  } finally {
    await page.unroute('**/__studio/regions**');
    for (const route of held) await route.continue().catch(() => {});
  }
  await until(() => mixerPost !== null, 'the form was not sent once its region was current');
  const response = await mixerPost.response();
  assert.ok(response && response.status() < 400, `the mixer write was refused: ${response && response.status()}`);
  // Put the track back as it was.
  await until(async () => await mute().getAttribute('aria-pressed') !== pressed, 'the mute did not apply');
  await mute().click();
  await until(async () => await mute().getAttribute('aria-pressed') === pressed, 'the mute was not undone');
});

test('a stale region whose text changed still sends a form whose values did not, once', async () => {
  const {page, state, posts, sent, cell, settled, region, status} = await studio();
  await page.keyboard.press('Alt+Shift+Digit1');
  const dialog = page.locator('#export-dialog');
  const render = page.locator('#export-render-button');
  const open = async () => {
    await page.locator('[data-gosx-disclosure-target="#export-dialog"]').click();
    await until(async () => !(await dialog.evaluate(node => node.hidden)), 'the Export dialog did not open');
  };
  const close = () => page.locator('#export-dialog [data-gosx-disclosure-close]').click();
  // Any end state counts: the job's status text is what changes the region.
  // (This fixture's song is longer than a render allows, so its jobs fail.)
  const finished = async previous => until(async () => {
    const job = await state('/api/export');
    return job.id !== previous && ['succeeded', 'shortfall', 'failed'].includes(job.state);
  }, 'the WAV export did not finish', 60_000);
  // A first render, so the Export dialog's delivery status names a job that
  // the dialog's last read did not show.
  await open();
  await until(async () => await region('dialog-export').getAttribute('data-region-stale') === null, 'the Export dialog stayed stale');
  const first = (await state('/api/export')).id;
  await render.click();
  await finished(first);
  await close();
  // Hold the reads after a draw, so the dialog stays stale while it opens.
  const held = [];
  let exportPost = null;
  page.on('request', request => { if (!exportPost && request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/export') exportPost = request; });
  await page.route('**/__studio/regions**', route => { held.push(route); });
  const previous = (await state('/api/export')).id, exports = sent('/__actions/export');
  try {
    try {
      const start = posts.length;
      await page.locator(cell(30)).click();
      await settled(start + 1);
      await until(() => held.length > 0, 'the write was not followed by a read');
      await open();
      assert.notEqual(await region('dialog-export').getAttribute('data-region-stale'), null, 'the Export dialog was not stale');
      // Held, the button shows the queue's pending marker; a second click
      // while it waits does nothing.
      await render.click();
      assert.notEqual(await render.getAttribute('data-workspace-pending'), null, 'the held submit was not marked pending');
      await render.click();
      await page.waitForTimeout(300);
      assert.equal(exportPost, null, 'a form in a stale region was sent before its region was read');
    } finally {
      await page.unroute('**/__studio/regions**');
      for (const route of held) await route.continue().catch(() => {});
    }
    // The read changes the status text but not the export settings, so the
    // render the user asked for goes out.
    await until(() => exportPost !== null, 'the export was not sent once its region was current')
      .catch(async error => { throw new Error(`${error.message}; status: ${await status()}`); });
    const response = await exportPost.response();
    assert.ok(response && response.status() < 400, `the export was refused: ${response && response.status()}`);
    await finished(previous);
    assert.doesNotMatch(await status(), /changed since it was shown/);
    assert.equal(sent('/__actions/export') - exports, 1, 'the export was not sent exactly once');
    assert.equal(await render.getAttribute('data-workspace-pending'), null, 'the pending marker stayed');
  } finally {
    if (!(await dialog.evaluate(node => node.hidden))) await close();
  }
});

test('Escape cancels an export held for its stale dialog', async () => {
  const {page, posts, sent, cell, settled, region, status} = await studio();
  const dialog = page.locator('#export-dialog');
  const render = page.locator('#export-render-button');
  const held = [];
  await page.route('**/__studio/regions**', route => { held.push(route); });
  const exports = sent('/__actions/export');
  try {
    const start = posts.length;
    await page.locator(cell(30)).click();
    await settled(start + 1);
    await until(() => held.length > 0, 'the write was not followed by a read');
    await page.locator('[data-gosx-disclosure-target="#export-dialog"]').click();
    await until(async () => !(await dialog.evaluate(node => node.hidden)), 'the Export dialog did not open');
    assert.notEqual(await region('dialog-export').getAttribute('data-region-stale'), null, 'the Export dialog was not stale');
    await render.click();
    assert.notEqual(await render.getAttribute('data-workspace-pending'), null, 'the held submit was not marked pending');
    await page.keyboard.press('Escape');
    await until(async () => await dialog.evaluate(node => node.hidden), 'Escape did not close the Export dialog');
    assert.equal(await render.getAttribute('data-workspace-pending'), null, 'the cancelled submit stayed pending');
    assert.match(await status(), /not sent/i, 'the cancelled export was not reported');
  } finally {
    await page.unroute('**/__studio/regions**');
    for (const route of held) await route.continue().catch(() => {});
  }
  await until(async () => await region('dialog-export').getAttribute('data-region-stale') === null, 'the Export dialog was never read');
  await page.waitForTimeout(500);
  assert.equal(sent('/__actions/export'), exports, 'an export was sent after Escape');
});

test('Quantize from a stale Clip tab is sent only after the tab is read, with the saved revision', async () => {
  const {page, state, posts, settled, region} = await studio();
  // The queue does not capture Quantize: GoSX sends it. The stale-region gate
  // still holds it until the Clip tab shows the saved score.
  const field = (request, name) => {
    const body = request.postData() || '';
    const multipart = body.match(new RegExp(`name="${name}"\\r\\n\\r\\n([^\\r]*)`));
    return multipart ? multipart[1] : new URLSearchParams(body).get(name);
  };
  const quantize = page.locator('#notes-quantize button[type=submit]');
  await quantize.waitFor();
  const held = [];
  let notesPost = null;
  page.on('request', request => { if (!notesPost && request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/notes') notesPost = request; });
  await page.locator('#detail-tab-device').click();
  await page.locator('#detail-tab-device[aria-selected="true"]').waitFor();
  await page.route('**/__studio/regions**', route => { held.push(route); });
  let saved;
  try {
    // An Undo while the Clip tab is hidden leaves that tab stale.
    const start = posts.length;
    await page.locator('#undo-form button').click();
    await settled(start + 1);
    saved = await state('/api/state');
    await until(() => held.length > 0, 'the write was not followed by a read');
    await page.locator('#detail-tab-clip').click();
    await page.locator('#detail-tab-clip[aria-selected="true"]').waitFor();
    assert.notEqual(await region('dock-clip').getAttribute('data-region-stale'), null, 'the Clip tab was not stale');
    await quantize.click();
    await page.waitForTimeout(300);
    assert.equal(notesPost, null, 'Quantize was sent from a stale Clip tab');
  } finally {
    await page.unroute('**/__studio/regions**');
    for (const route of held) await route.continue().catch(() => {});
  }
  await until(() => notesPost !== null, 'Quantize was not sent once the Clip tab was current');
  assert.equal(field(notesPost, 'revision'), saved.revision, 'Quantize did not carry the saved revision');
  // The drawn notes may not fit the grid (422, with the reason); a stale
  // revision would be refused with 409.
  const response = await notesPost.response();
  assert.ok(response, 'Quantize got no answer');
  assert.notEqual(response.status(), 409, `Quantize was refused as stale: ${await response.text()}`);
  // Put the notes back as they were. Notes already on the grid write nothing.
  const after = await state('/api/state');
  if (after.revision !== saved.revision) {
    await until(async () => await page.locator('#notes-quantize input[name=revision]').inputValue().catch(() => '') === after.revision, 'the Clip tab did not show the quantized score');
    await page.locator('#studio-shell[data-shell-edits="ready"]').waitFor();
    const start = posts.length;
    await page.locator('#undo-form button').click();
    await settled(start + 1);
    await until(async () => (await state('/api/state')).source === saved.source, 'Undo did not restore the notes');
  }
});

// The tests below show the rule for a held submit: it goes out by itself only
// when its region's read returns the revision of this page's own writes.
// After another client's save it is not sent; the user sees the refreshed
// panel and a notice, and submits again if they still want to.
const anotherChange = /Another change to the song was saved, so this was not sent/;

test('Undo after another client saved is not sent, and that save survives', async () => {
  const {caughtUp, page, state, posts, readOnly, cell, settled, region, status} = await studio();
  await page.keyboard.press('Alt+Shift+Digit1');
  await page.locator('#view-tab-arrange[aria-selected="true"]').waitFor();
  const allow = new Set();
  await readOnly(allow);
  try {
    // A draw here leaves the hidden Mixer stale; its idle read fails.
    let start = posts.length;
    await page.locator(cell(40)).click();
    await settled(start + 1);
    await until(async () => await region('center-mixer').getAttribute('data-region-stale') !== null, 'the hidden Mixer was not stale');
    // Another client saves a note this page has not seen.
    const other = await otherClient();
    await other.locator(cell(41)).click();
    await other.waitForFunction(selector => document.querySelector(selector)?.getAttribute('data-active') === 'true' && !document.querySelector(selector).hasAttribute('data-workspace-pending'), cell(41));
    const theirs = await state('/api/state');
    // Opening the Mixer reads it at that save's revision, so every region
    // the read did not cover, the Undo button's among them, is now stale.
    allow.add('center-mixer');
    await page.keyboard.press('Alt+Shift+Digit3');
    await until(async () => await region('actions').getAttribute('data-region-stale') !== null, 'the Undo button did not go stale');
    allow.clear();
    allow.add('actions');
    start = posts.length;
    await page.evaluate(() => document.activeElement?.blur());
    await page.keyboard.press('Control+z');
    await until(async () => anotherChange.test(await status()), 'the held Undo was not reported');
    await page.waitForTimeout(300);
    assert.equal(posts.length, start, 'the Undo was sent after another client saved');
    assert.equal(await region('actions').getAttribute('data-region-stale'), null, 'the Undo button was not refreshed');
    assert.notEqual(await page.locator('#undo-form').getAttribute('data-workspace-updated'), null, 'the refreshed Undo was not marked');
    assert.equal(await page.locator('#undo-form button[type=submit]').getAttribute('data-workspace-pending'), null, 'the Undo stayed pending');
    assert.equal((await state('/api/state')).source, theirs.source, "the other client's save was undone");
  } finally {
    await page.unroute('**/__studio/regions**');
  }
  await caughtUp();
  // With reads allowed again, the page shows the other client's note.
  await until(async () => await page.locator(cell(41)).getAttribute('data-active') === 'true', "the other client's note did not appear");
  await page.keyboard.press('Alt+Shift+Digit1');
  await page.locator('#view-tab-arrange[aria-selected="true"]').waitFor();
});

test('Remove block after another client inserted a block earlier is not sent', async () => {
  const {caughtUp, page, state, posts, refused, refusedRead, sent, readOnly, cell, settled, region, status} = await studio();
  const blocks = source => (source.match(/song\s*\{([^}]*)\}/)?.[1] || '').trim().split(/\s+/);
  // Two blocks to choose from: main, then alternate.
  await page.keyboard.press('Alt+Shift+Digit1');
  await page.locator('#view-tab-arrange[aria-selected="true"]').waitFor();
  const add = page.locator('#update-center-arrange form:has(input[name=action][value=append])');
  await add.locator('select[name=scene]').selectOption('alternate');
  await add.locator('input[name=bars]').fill('4');
  await add.getByRole('button', {name: 'Add to arrangement'}).click();
  await until(async () => blocks((await state('/api/state')).source).length === 2, 'the block was not added');
  await page.locator('#studio-shell[data-shell-edits="ready"]').waitFor();
  await until(async () => await page.locator('#update-center-arrange details[data-song-index]').count() === 2, 'the page did not show two blocks');
  // A draw with the Arrange view hidden leaves it stale.
  await page.keyboard.press('Alt+Shift+Digit3');
  await page.locator('#view-tab-mixer[aria-selected="true"]').waitFor();
  const allow = new Set();
  await readOnly(allow);
  try {
    const start = posts.length;
    await page.locator(cell(42)).click();
    await settled(start + 1);
    await until(async () => await region('center-arrange').getAttribute('data-region-stale') !== null, 'the hidden Arrange view was not stale');
    // Another client copies the first block, which moves alternate to 3.
    const other = await otherClient();
    await other.locator('#update-center-arrange details[data-song-index="0"] > summary').click();
    await other.locator('#update-center-arrange details[data-song-index="0"]').getByRole('button', {name: 'Duplicate block'}).click();
    await until(async () => blocks((await state('/api/state')).source).length === 3, 'the other client did not copy the block');
    const theirs = await state('/api/state');
    // This page still shows main, alternate (the read the Arrange view
    // makes when it opens fails). Its user removes alternate.
    const opened = refused.length;
    await page.keyboard.press('Alt+Shift+Digit1');
    await page.locator('#view-tab-arrange[aria-selected="true"]').waitFor();
    await refusedRead(opened, 'center-arrange', 'the Arrange view was not read when it opened');
    assert.equal(await page.locator('#update-center-arrange details[data-song-index]').count(), 2, 'the stale view already showed the copy');
    const second = page.locator('#update-center-arrange details[data-song-index="1"]');
    await second.locator('summary').click();
    allow.add('center-arrange');
    const songs = sent('/__actions/song');
    await second.getByRole('button', {name: 'Remove block'}).click();
    await until(async () => anotherChange.test(await status()), 'the held removal was not reported');
    await page.waitForTimeout(300);
    assert.equal(sent('/__actions/song'), songs, 'Remove block was sent after another client changed the song');
    assert.equal(await page.locator('#update-center-arrange details[data-song-index]').count(), 3, 'the page does not show the copied block');
    assert.equal((await state('/api/state')).source, theirs.source, 'the song changed');
  } finally {
    await page.unroute('**/__studio/regions**');
  }
  await caughtUp();
});

test('a piano-roll click on a cell another client just filled is not sent', async () => {
  const {caughtUp, page, state, posts, readOnly, cell, settled, region, status} = await studio();
  await page.keyboard.press('Alt+Shift+Digit1');
  await page.locator('#view-tab-arrange[aria-selected="true"]').waitFor();
  const allow = new Set();
  await readOnly(allow);
  try {
    let start = posts.length;
    await page.locator(cell(43)).click();
    await settled(start + 1);
    await until(async () => await region('center-mixer').getAttribute('data-region-stale') !== null, 'the hidden Mixer was not stale');
    const other = await otherClient();
    await other.locator(cell(44)).click();
    await other.waitForFunction(selector => document.querySelector(selector)?.getAttribute('data-active') === 'true' && !document.querySelector(selector).hasAttribute('data-workspace-pending'), cell(44));
    const theirs = await state('/api/state');
    // Reading the Mixer finds that save and leaves this Clip region stale:
    // it still shows the cell empty.
    allow.add('center-mixer');
    await page.keyboard.press('Alt+Shift+Digit3');
    await until(async () => await region('dock-clip').getAttribute('data-region-stale') !== null, 'the Clip region did not go stale');
    assert.equal(await page.locator(cell(44)).getAttribute('data-active'), 'false');
    allow.clear();
    allow.add('dock-clip');
    start = posts.length;
    // Meant as "add this note"; sent now it would delete the other note.
    await page.locator(cell(44)).click();
    await until(async () => anotherChange.test(await status()), 'the held click was not reported');
    await page.waitForTimeout(300);
    assert.equal(posts.length, start, 'the click was sent after another client filled the cell');
    assert.equal(await page.locator(cell(44)).getAttribute('data-active'), 'true', 'the refreshed cell does not show the other note');
    assert.equal((await state('/api/state')).source, theirs.source, "the other client's note was removed");
  } finally {
    await page.unroute('**/__studio/regions**');
  }
  await caughtUp();
  await page.keyboard.press('Alt+Shift+Digit1');
  await page.locator('#view-tab-arrange[aria-selected="true"]').waitFor();
});

test('a held form whose own values changed after the read is not sent', async () => {
  const {caughtUp, page, posts, sent, readOnly, refused, refusedRead, settled, region, status} = await studio();
  const mute = () => page.locator('#view-panel-mixer button[data-toggle=mute]').first();
  await page.keyboard.press('Alt+Shift+Digit3');
  await page.locator('#view-tab-mixer[aria-selected="true"]').waitFor();
  const before = await mute().getAttribute('aria-pressed');
  await mute().click();
  await until(async () => await mute().getAttribute('aria-pressed') !== before, 'the mute did not apply');
  await page.locator('#studio-shell[data-shell-edits="ready"]').waitFor();
  // Undo the mute with the Mixer hidden: the Mixer is stale and still shows
  // the track muted.
  await page.keyboard.press('Alt+Shift+Digit1');
  await page.locator('#view-tab-arrange[aria-selected="true"]').waitFor();
  const allow = new Set();
  await readOnly(allow);
  try {
    const start = posts.length;
    await page.locator('#undo-form button[type=submit]').click();
    await settled(start + 1);
    await until(async () => await region('center-mixer').getAttribute('data-region-stale') !== null, 'the hidden Mixer was not stale');
    const opened = refused.length;
    await page.keyboard.press('Alt+Shift+Digit3');
    await page.locator('#view-tab-mixer[aria-selected="true"]').waitFor();
    await refusedRead(opened, 'center-mixer', 'the Mixer was not read when it opened');
    assert.notEqual(await mute().getAttribute('aria-pressed'), before, 'the stale Mixer already showed the Undo');
    // The read returns this page's own revision, but the button it holds now
    // says the opposite of what the user saw: nothing is sent.
    allow.add('center-mixer');
    const mixer = sent('/__actions/mixer') + sent('/__actions/toggle');
    await mute().click();
    await until(async () => /changed since it was shown/.test(await status()), 'the changed form was not reported');
    await page.waitForTimeout(300);
    assert.equal(sent('/__actions/mixer') + sent('/__actions/toggle'), mixer, 'a form whose values changed was sent');
    assert.equal(await mute().getAttribute('aria-pressed'), before, 'the refreshed Mixer does not show the Undo');
  } finally {
    await page.unroute('**/__studio/regions**');
  }
  await caughtUp();
  await page.keyboard.press('Alt+Shift+Digit1');
  await page.locator('#view-tab-arrange[aria-selected="true"]').waitFor();
});

test('a click in a panel still showing the song from before another save waits for a second click, even after an own edit', async () => {
  const {caughtUp, page, state, posts, sent, readOnly, refused, refusedRead, cell, settled, region, status} = await studio();
  const mute = () => page.locator('#view-panel-mixer button[data-toggle=mute]').first();
  const mixerPosts = () => sent('/__actions/mixer') + sent('/__actions/toggle');
  await page.keyboard.press('Alt+Shift+Digit1');
  await page.locator('#view-tab-arrange[aria-selected="true"]').waitFor();
  // Reads that name the hidden Mixer fail (the idle read among them), so it
  // keeps what it showed before the other client saved. After the click only
  // the held submit's own read of the Mixer goes through.
  let clicked = false;
  await readOnly(ids => !ids.includes('center-mixer') || clicked && ids.length === 1);
  try {
    let start = posts.length;
    await page.locator(cell(47)).click();
    await settled(start + 1);
    await until(async () => await region('center-mixer').getAttribute('data-region-stale') !== null, 'the hidden Mixer was not stale');
    // Another client saves; this page learns of it by reading the Session
    // view, a read that does not cover the hidden Mixer.
    const other = await otherClient();
    await other.locator(cell(48)).click();
    await other.waitForFunction(selector => document.querySelector(selector)?.getAttribute('data-active') === 'true' && !document.querySelector(selector).hasAttribute('data-workspace-pending'), cell(48));
    await page.keyboard.press('Alt+Shift+Digit2');
    await page.locator('#view-tab-session[aria-selected="true"]').waitFor();
    await until(async () => await region('center-mixer').getAttribute('data-region-foreign') !== null, 'the Mixer was not marked as showing the song from before that save');
    // Back in the Arrange view the visible regions are read again, and an
    // own edit succeeds. Its revision is the one the Mixer's read returns.
    await page.keyboard.press('Alt+Shift+Digit1');
    await page.locator('#view-tab-arrange[aria-selected="true"]').waitFor();
    await until(async () => await region('dock-clip').getAttribute('data-region-stale') === null, 'the Clip region was not read again');
    start = posts.length;
    await page.locator(cell(49)).click();
    await settled(start + 1);
    assert.equal(posts[start].status, 200, posts[start].result?.message);
    assert.notEqual(await region('center-mixer').getAttribute('data-region-foreign'), null, 'an own edit cleared the mark of a region it did not render');
    // Open the Mixer (its read fails) and click at once.
    const opened = refused.length;
    await page.keyboard.press('Alt+Shift+Digit3');
    await page.locator('#view-tab-mixer[aria-selected="true"]').waitFor();
    await refusedRead(opened, 'center-mixer', 'the Mixer was not read when it opened');
    const before = await mute().getAttribute('aria-pressed'), mixer = mixerPosts();
    clicked = true;
    await mute().click();
    await until(async () => anotherChange.test(await status()), 'the held click was not reported');
    await page.waitForTimeout(300);
    assert.equal(mixerPosts(), mixer, 'a click on a panel from before another save was sent by itself');
    assert.equal(await region('center-mixer').getAttribute('data-region-stale'), null, 'the Mixer was not refreshed');
    assert.notEqual(await page.locator('#view-panel-mixer form:has(button[data-toggle=mute])').first().getAttribute('data-workspace-updated'), null, 'the refreshed form was not marked');
    // The second click is the user's own decision on the refreshed panel.
    await mute().click();
    await until(() => mixerPosts() === mixer + 1, 'the second click was not sent');
    await until(async () => await mute().getAttribute('aria-pressed') !== before, 'the second click did not apply');
    await page.locator('#studio-shell[data-shell-edits="ready"]').waitFor();
    // Put the track back as it was.
    await mute().click();
    await until(async () => await mute().getAttribute('aria-pressed') === before, 'the mute was not undone');
  } finally {
    await page.unroute('**/__studio/regions**');
  }
  await caughtUp();
  await page.keyboard.press('Alt+Shift+Digit1');
  await page.locator('#view-tab-arrange[aria-selected="true"]').waitFor();
});

test('a held Undo is dropped after a refused edit', async () => {
  const {page, posts, cell, rewrites, hold, settled, status} = await studio();
  const undo = page.locator('#undo-form button[type=submit]');
  const start = posts.length;
  rewrites.push(payload => { payload.step = '99'; });
  const release = hold();
  await page.locator(cell(45)).click();
  await until(() => posts.length > start, 'the draw was not sent');
  // The Undo button's region goes stale only after another client's save is
  // found; mark it stale here, so the Undo is held behind the refused draw.
  await page.evaluate(() => document.querySelector('#update-actions').setAttribute('data-region-stale', ''));
  await undo.click();
  assert.notEqual(await undo.getAttribute('data-workspace-pending'), null, 'the Undo was not held');
  release();
  await settled(start + 1);
  assert.equal(posts[start].status, 422);
  await until(async () => await undo.getAttribute('data-workspace-pending') === null, 'the held Undo stayed pending');
  await page.waitForTimeout(400);
  assert.equal(posts.length, start + 1, 'the held Undo was sent after a refused edit');
  assert.match(await status(), /Undo or Redo waiting behind it was not sent/, 'the dropped Undo was not reported');
});

// Transport commands the service runs without a revision check (play, pause,
// stop, return to start) never wait behind a score write and are never
// dropped when a read finds another client's save.
test('Play in the shell starts while a read that finds another save is held', async () => {
  const {caughtUp, page, state, posts, cell, settled} = await studio();
  const held = [];
  await page.route('**/__studio/regions**', route => { held.push(route); });
  try {
    const start = posts.length;
    await page.locator(cell(50)).click();
    await settled(start + 1);
    await until(() => held.length > 0, 'the write was not followed by a read');
    const other = await otherClient();
    await other.locator(cell(51)).click();
    await other.waitForFunction(selector => document.querySelector(selector)?.getAttribute('data-active') === 'true' && !document.querySelector(selector).hasAttribute('data-workspace-pending'), cell(51));
    await page.locator('#transport-form button[value=play]').click();
    await until(async () => (await state('/api/transport')).playing, 'Play did not start while a read was held');
  } finally {
    await page.unroute('**/__studio/regions**');
    for (const route of held) await route.continue().catch(() => {});
  }
  // The held read finds the other save; playback goes on.
  await caughtUp();
  assert.equal((await state('/api/transport')).playing, true, 'the read that found another save stopped playback');
  await page.locator('#transport-form button[value=stop]').click();
  await until(async () => !(await state('/api/transport')).playing, 'Stop did not stop playback');
});

test('Play in the panel view is not dropped by a refresh that finds another save, and a dropped edit is named', async () => {
  const {base, state, cell} = await studio();
  const {context} = await sharedBrowser();
  const panel = await context.newPage();
  panel.setDefaultTimeout(15_000);
  // The panel view polls the project revision each second and queues a
  // refresh when it moves. Hold that refresh.
  let refresh = null, release;
  const released = new Promise(resolve => { release = resolve; });
  await panel.route('**/__workspace**', async route => {
    if (route.request().method() === 'GET' && !refresh) {
      refresh = route;
      await released;
    }
    await route.continue().catch(() => {});
  });
  try {
    await panel.goto(new URL('/?panel=patterns&pattern=continuity&step=1', base).href);
    await panel.locator('#cicada-workspace[data-workspace-reactive="ready"]').waitFor({timeout: 30_000});
    const other = await otherClient();
    await other.locator(cell(52)).click();
    await other.waitForFunction(selector => document.querySelector(selector)?.getAttribute('data-active') === 'true' && !document.querySelector(selector).hasAttribute('data-workspace-pending'), cell(52));
    await until(() => refresh !== null, 'the panel view did not refresh after another save');
    // Play goes out at once; a draw waits behind the refresh, which finds the
    // other save, so the draw is dropped and named.
    await panel.locator('.toolbar form[action="/__actions/transport"] button[value="play"]').click();
    await until(async () => (await state('/api/transport')).playing, 'Play waited behind the refresh');
    await panel.locator('[data-workspace-tool=draw]').click();
    const target = panel.locator(cell(53).replace('#detail-panel-clip ', ''));
    const label = await target.getAttribute('aria-label');
    await target.click();
    release();
    await until(async () => /Another change to the song was saved, so this was not sent: /.test(await panel.locator('[data-workspace-status]').textContent()), 'the dropped draw was not reported');
    assert.match(await panel.locator('[data-workspace-status]').textContent(), new RegExp(label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), 'the report did not name the dropped draw');
    assert.equal((await state('/api/transport')).playing, true, 'the refresh stopped playback');
    await panel.locator('.toolbar form[action="/__actions/transport"] button[value="stop"]').click();
    await until(async () => !(await state('/api/transport')).playing, 'Stop did not stop playback');
  } finally {
    release();
    await panel.close();
  }
  // The suite's page has not seen the other client's save. A step link (a
  // GoSX navigation) renders it again at the saved revision.
  const {page} = await studio();
  await page.locator('#detail-panel-clip .piano-roll .step-number').nth(1).click();
  await page.waitForFunction(() => new URL(location.href).searchParams.get('step') === '2');
  await page.locator('#studio-shell[data-shell-edits="ready"]').waitFor();
  await until(async () => await page.locator('#undo-form input[name=revision]').inputValue() === (await state('/api/state')).revision, 'the page did not catch up with the saved song');
});

test('closing the shell while a write is in flight leaves no error and the next edit works', async () => {
  const {page, state, posts, cell, hold, settled} = await studio();
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.keyboard.press('Alt+Shift+Digit1');
  const before = (await state('/api/state')).revision;
  const release = hold();
  const start = posts.length;
  await page.locator(cell(33)).click();
  await until(() => posts.length > start, 'the draw was not sent');
  // A step link is a GoSX navigation: it disposes the shell engine.
  await page.locator('#detail-panel-clip .piano-roll .step-number').nth(2).click();
  await page.waitForFunction(() => new URL(location.href).searchParams.get('step') === '3');
  release();
  await settled(start + 1);
  await page.locator('#studio-shell[data-shell-edits="ready"]').waitFor();
  assert.notEqual((await state('/api/state')).revision, before, 'the write sent before the navigation was lost');
  // The new page was rendered before that write landed, so it is one save
  // behind; a write from it would be refused. Load the saved state again.
  await page.locator('#detail-panel-clip .piano-roll .step-number').nth(3).click();
  await page.waitForFunction(() => new URL(location.href).searchParams.get('step') === '4');
  await page.locator('#studio-shell[data-shell-edits="ready"]').waitFor();
  const next = posts.length;
  await page.locator(cell(34)).click();
  await settled(next + 1);
  assert.equal(posts[next].status, 200, posts[next].result?.message);
  assert.deepEqual(errors, []);
});

test('a region that needs a reload is not read again while a draft blocks the reload', async () => {
  const {page, posts, reads, cell, settled, region} = await studio();
  // An unsaved Code draft means the shell must not reload. Then make the Clip
  // region's engine look changed, so its next patch needs a reload.
  const editor = page.locator('#score textarea').first();
  await editor.focus();
  await page.keyboard.press('Control+End');
  await page.keyboard.type('\n// unsaved draft');
  await page.locator('#cicada-pattern-preview').evaluate(node => node.setAttribute('data-engine-props', 'changed'));
  const start = posts.length;
  await page.locator(cell(31)).click();
  await settled(start + 1);
  await until(async () => await region('dock-clip').getAttribute('data-region-reload') !== null, 'the region was not marked for a reload');
  await page.locator('#studio-refresh-notice').waitFor({state: 'visible'});
  const before = reads.length;
  await page.waitForTimeout(3000);
  const repeated = reads.slice(before).filter(target => /(^|,)dock-clip:/.test(target.searchParams.get('ids') || ''));
  assert.equal(repeated.length, 0, `the region awaiting a reload was read ${repeated.length} times in 3 s`);
  assert.ok(reads.length - before <= 2, `${reads.length - before} region reads in 3 s with a reload pending`);
});

// Last, on a page of its own: no read backoff left by an earlier step can
// fire inside the window.
test('an idle page reads no region once every region it reads is current', async () => {
  const {base, cell} = await studio();
  const {context} = await sharedBrowser();
  const page = await context.newPage();
  const reads = [];
  page.on('request', request => {
    const target = new URL(request.url());
    if (target.pathname === '/__studio/regions') reads.push(target);
  });
  try {
    await page.goto(new URL('/studio?tab=arrange&dock=clip&pattern=continuity&step=1', base).href);
    await page.locator('#studio-shell[data-shell-edits="ready"]').waitFor({timeout: 30_000});
    const stale = () => page.locator('[data-update-region][data-region-stale]:not(#update-code):not([data-region-reload])').count();
    const before = await page.locator(cell(46)).getAttribute('data-active');
    await page.locator(cell(46)).click();
    await page.waitForFunction(([selector, before]) => {
      const el = document.querySelector(selector);
      return el && el.getAttribute('data-active') !== before && !el.hasAttribute('data-workspace-pending');
    }, [cell(46), before]);
    // The hidden regions go stale, and the idle read refreshes them once (the
    // Code lens is read only when it shows, and a region waiting for a page
    // load is not read).
    await until(async () => await stale() > 0, 'the draw left no hidden region stale');
    await until(async () => await stale() === 0, 'stale regions stayed after the idle read');
    const count = reads.length;
    await page.waitForTimeout(3000);
    assert.equal(reads.length - count, 0, `region reads in 3 s on an idle page: ${reads.slice(count).map(target => target.searchParams.get('ids')).join(' | ')}`);
  } finally {
    await page.close();
  }
});
