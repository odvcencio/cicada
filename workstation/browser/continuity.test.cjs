/*
 * Disposable Studio continuity regression. Application behavior remains GoSX;
 * JavaScript in this file only drives and observes the browser.
 *
 * node workstation/browser/continuity.test.cjs --write-fixture /tmp/continuity.cicada
 * ./build/cicada studio /tmp/continuity.cicada --audio null --listen 127.0.0.1:8190
 * CICADA_STUDIO_URL=http://127.0.0.1:8190 node --test workstation/browser/continuity.test.cjs
 *
 * Install Playwright in the test environment, or set CICADA_PLAYWRIGHT_MODULE
 * to its package path or a createRequire anchor such as the runtime's browser.cjs.
 * CICADA_CHROME_PATH selects an installed Chrome; CICADA_HEADLESS=0 shows it.
 * CICADA_CONTINUITY_REPEAT=N runs the scenario N times to measure flakiness.
 * The runner refuses unmarked scores and resets its own fixture before testing.
 * A failing run prints its action list and a timeline of requests, history
 * changes and busy spans.
 */
'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {createRequire} = require('node:module');
const test = require('node:test');

const marker = 'cicada-continuity-test: disposable fixture';
const phrase = degree => [degree, ...Array(62).fill('.'), 'c4'].join(' ');
const fixture = `cicada 2
// ${marker}
title "Studio continuity regression"
tempo 120
key c minor
track keys acid { cutoff=700Hz }
${'  '}
pattern continuity notes steps=64 { ${phrase('1')} }
pattern alternate notes steps=64 { ${phrase('5')} }
scene main { keys=continuity }
scene alternate { keys=alternate }
song { main*999 }
`;

if (process.argv[2] === '--write-fixture') {
  assert.ok(process.argv[3], 'supply a new disposable fixture path');
  fs.writeFileSync(process.argv[3], fixture, {flag: 'wx'});
  console.log(`Wrote ${path.resolve(process.argv[3])}`);
  process.exit(0);
}

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

// The first browser session to reach a Studio owns it, so repeated runs share
// one context. Each run still gets its own page.
let shared;
async function sharedBrowser() {
  if (!shared) {
    let executablePath = process.env.CICADA_CHROME_PATH;
    if (!executablePath && process.platform === 'win32') {
      executablePath = [process.env.PROGRAMFILES, process.env['PROGRAMFILES(X86)']]
        .filter(Boolean).map(root => path.join(root, 'Google', 'Chrome', 'Application', 'chrome.exe')).find(candidate => fs.existsSync(candidate));
    }
    const browser = await playwright().chromium.launch({
      headless: process.env.CICADA_HEADLESS !== '0', executablePath,
      args: ['--autoplay-policy=no-user-gesture-required'],
    });
    shared = {browser, context: await browser.newContext({viewport: {width: 1100, height: 760}})};
  }
  return shared;
}

async function scenario(t) {
  assert.ok(process.env.CICADA_STUDIO_URL, 'set CICADA_STUDIO_URL to the disposable Studio instance');
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(base.hostname), 'use a local disposable Studio');
  const url = panel => {
    const target = new URL(base);
    target.search = new URLSearchParams({panel, ...(panel === 'patterns' ? {pattern: 'continuity', step: '1'} : {})}).toString();
    return target.href;
  };
  const {context} = await sharedBrowser();
  const page = await context.newPage();
  page.setDefaultTimeout(15_000);
  const errors = [], warnings = [], requests = [];
  let armed = false, documentRequests = 0, inFlight = 0, maxInFlight = 0;
  let injectStale = false, delayNextPattern = false, staleRevision = '';
  let injectInterveningSave = false, interveningState;
  // Evidence for a failing run: the network the engine used, the history it
  // wrote and when it was busy, in one ordered list.
  const clock = Date.now(), timeline = [];
  const note = (kind, detail) => timeline.push({at: Date.now() - clock, text: `${kind} ${detail}`});
  const watched = request => {
    const target = new URL(request.url());
    return /^\/(__workspace|api\/revision|__actions\/)/.test(target.pathname) ? target : null;
  };
  page.on('request', request => {
    const target = watched(request);
    if (target) note('request', `${request.method()} ${target.pathname}${target.search}`);
  });
  page.on('response', response => {
    const target = watched(response.request());
    if (target) note('response', `${response.status()} ${target.pathname}`);
  });
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => {
    const text = message.text(), location = message.location().url;
    if (/favicon\.ico(?:\?|$)/.test(location)) return;
    if (message.type() === 'warning') warnings.push(text);
    if (message.type() === 'error' && !(/\/__actions\//.test(location) && /status of (409|422)/.test(text))) errors.push(text);
  });
  page.on('request', request => {
    if (armed && request.resourceType() === 'document') documentRequests++;
  });
  await page.addInitScript(() => { window.__continuityDocument = crypto.randomUUID(); });
  const state = async endpoint => {
    const response = await context.request.get(new URL(endpoint, base).href);
    assert.equal(response.status(), 200, `${endpoint} must be available`);
    return response.json();
  };
  const ready = async () => {
    await page.locator('#cicada-workspace[data-workspace-reactive="ready"]').waitFor({timeout: 30_000});
  };
  const nativeAction = async (name, values) => {
    const csrf = await page.locator('input[name="csrf_token"]').first().inputValue();
    const current = await state('/api/state');
    return context.request.post(new URL(`/__actions/${name}`, base).href, {
      form: {csrf_token: csrf, revision: current.revision, __gosx_return_to: '/?panel=patterns', ...values},
      maxRedirects: 0,
    });
  };
  const waitRequests = async count => {
    await until(() => requests.length >= count && requests.slice(0, count).every(request => request.delivered), `only ${requests.length}/${count} actions settled`);
  };
  const projectedRevision = async record => {
    assert.equal(record.status, 200, `action ${record.action}: ${record.result?.message || 'missing success response'}`);
    assert.equal(typeof record.result.data?.html, 'string', 'success includes its GoSX HTML projection');
    assert.equal(typeof record.result.data.revision, 'string');
    await page.waitForFunction(revision => {
      const inputs = [...document.querySelectorAll('#cicada-workspace input[type="hidden"][name="revision"]')];
      return inputs.length > 0 && inputs.every(input => input.value === revision);
    }, record.result.data.revision);
    return record.result.data.revision;
  };
  const click = async selector => page.locator(selector).evaluate(button => button.click());
  const assertPatternContinuity = async draft => {
    const actual = await page.evaluate(() => {
      const refs = window.__continuityRefs;
      const chance = document.querySelector('#step-inspector input[name="chance"]');
      return {
        document: window.__continuityDocument, root: document.querySelector('#cicada-workspace') === refs.root,
        studio: document.querySelector('.studio') === refs.studio, grid: document.querySelector('.piano-roll') === refs.grid,
        cells: refs.cells.every(({cell, label}) => cell.isConnected && document.querySelector(`[aria-label="${CSS.escape(label)}"]`) === cell),
        input: document.querySelector('.pattern-workspace input[name="swing"]') === refs.input,
        inspector: chance === refs.chance,
        focused: document.activeElement === refs.input, value: refs.input.value,
        chance: {value: chance.value, focused: document.activeElement === chance,
          step: chance.closest('form').querySelector('input[name="step"]').value},
        details: refs.details.every(detail => detail.isConnected && detail.open),
        scroll: {x: scrollX, y: scrollY, left: refs.grid.scrollLeft, top: refs.grid.scrollTop},
      };
    });
    assert.equal(actual.document, draft.document, 'document reloaded');
    for (const key of ['root', 'studio', 'grid', 'cells', 'input', 'details']) assert.equal(actual[key], true, `${key} continuity was lost`);
    assert.equal(actual.focused, draft.focused, 'timing input focus changed');
    assert.equal(actual.value, draft.value, 'unsaved pattern timing draft was erased');
    if (draft.chance) {
      assert.equal(actual.inspector, true, 'same-step inspector control was replaced');
      assert.deepEqual(actual.chance, draft.chance, 'same-step inspector draft or focus changed');
    }
    for (const key of Object.keys(draft.scroll)) assert.ok(Math.abs(actual.scroll[key] - draft.scroll[key]) <= 2, `${key} scroll jumped: ${draft.scroll[key]} to ${actual.scroll[key]}`);
    assert.equal(documentRequests, 0, 'a document GET/navigation occurred during an edit');
    assert.equal((await state('/api/transport')).playing, true, 'playback stopped during an edit');
  };

  try {
    await page.goto(url('patterns'), {waitUntil: 'domcontentloaded'});
    const original = await state('/api/state');
    assert.ok(original.source.includes(marker), 'refusing to mutate an unmarked score: start Studio with --write-fixture output');
    const reset = await nativeAction('source', {content: fixture});
    assert.ok([200, 303].includes(reset.status()), `fixture reset failed: ${reset.status()}`);
    await page.goto(url('patterns'), {waitUntil: 'domcontentloaded'});
    await ready();
    await page.evaluate(() => {
      const log = window.__continuityTimeline = [];
      for (const name of ['pushState', 'replaceState']) {
        const original = history[name].bind(history);
        history[name] = (...args) => { log.push({at: Date.now(), text: `history.${name} ${args[2]}`}); return original(...args); };
      }
      const root = document.querySelector('#cicada-workspace');
      new MutationObserver(records => records.forEach(record => log.push({at: Date.now(), text: `workspace ${record.attributeName}=${root.getAttribute(record.attributeName)}`})))
        .observe(root, {attributes: true, attributeFilter: ['aria-busy']});
    });
    await page.route('**/__actions/**', async route => {
      if (route.request().method() !== 'POST') return route.continue();
      const contentType = route.request().headers()['content-type'] || '';
      assert.match(contentType, /^application\/json\b/i,
        `${new URL(route.request().url()).pathname} bypassed workspace JSON transport: ${contentType}`);
      const payload = JSON.parse(route.request().postData());
      const action = new URL(route.request().url()).pathname.split('/').pop();
      const engineOwned = action === 'live' || action === 'note-preview';
      if (!engineOwned) {
        assert.equal(payload.__cicada_reactive, '1', 'managed action bypassed the GoSX workspace engine');
        assert.equal(typeof payload.__cicada_location, 'string');
      }
      for (const [name, value] of Object.entries(payload)) assert.equal(typeof value, 'string', `action field ${name} is not a string`);
      assert.equal(Object.hasOwn(payload, 'undefined'), false, 'empty submitter name was corrupted during projection');
      if (payload.action === 'step' && payload.pattern === 'continuity') assert.equal(payload.lane, '', 'note inspector acquired a phantom drum lane');
      const record = {action, payload, delivered: false};
      requests.push(record);
      if (!engineOwned) maxInFlight = Math.max(maxInFlight, ++inFlight);
      if (injectStale && action === 'pattern') { payload.revision = staleRevision; injectStale = false; }
      try {
        const requestStarted = performance.now();
        const response = await route.fetch({postData: JSON.stringify(payload), maxRedirects: 0});
        record.status = response.status();
        record.result = await response.json();
        record.responseMs = performance.now() - requestStarted;
        if (injectInterveningSave && action === 'pattern') {
          injectInterveningSave = false;
          const writeRevision = record.result.data.writeRevision;
          assert.equal(writeRevision, record.result.data.revision, 'initial write receipt must agree with its projection');
          const current = await state('/api/state');
          const saved = await nativeAction('source', {content: `${current.source}\n// intervening-save-regression\n`});
          assert.equal(saved.status(), 303, 'intervening save must succeed');
          interveningState = await state('/api/state');
          assert.notEqual(interveningState.revision, writeRevision);
          const projected = await context.request.get(new URL(`/__workspace${payload.__cicada_location.slice(1)}`, base).href);
          assert.equal(projected.status(), 200);
          // Model the server's receipt after another editor saves in the
          // acknowledgment-to-projection interval; the Go test gates that interval.
          record.result.data = {...await projected.json(), writeRevision, saved: true, refreshRequired: true};
          record.result.message = 'Saved. The score changed before the workspace refreshed.';
        }
        if (delayNextPattern && action === 'pattern') {
          delayNextPattern = false;
          await new Promise(resolve => setTimeout(resolve, 120));
        }
        await route.fulfill({response, body: JSON.stringify(record.result)});
        record.delivered = true;
      } finally { if (!engineOwned) inFlight--; }
    });
    armed = true;
    note('step', 'play');
    await click('.toolbar form[action="/__actions/transport"] button[value="play"]');
    await until(async () => (await state('/api/transport')).playing, 'transport did not start');
    await waitRequests(1);
    const initialRevision = (await state('/api/state')).revision;
    staleRevision = initialRevision;
    const rowPitch = await page.locator('.piano-roll .roll-row form input[name="pitch"]').nth(8).inputValue();
    const cell = step => `.piano-roll form:has(input[name="pitch"][value="${rowPitch}"]) .roll-cell[value="${step}"]`;
    await page.locator('#step-inspector input[name="chance"]').fill('37');
    await page.locator('details').filter({has: page.locator('summary', {hasText: 'Pattern timing'})}).evaluate(detail => { detail.open = true; });
    await page.locator('details').filter({has: page.locator('summary', {hasText: 'Range editing'})}).evaluate(detail => { detail.open = true; });
    await page.locator('.pattern-workspace input[name="swing"]').fill('54.5');
    await page.evaluate(pitch => {
      const root = document.querySelector('#cicada-workspace'), grid = document.querySelector('.piano-roll');
      const input = document.querySelector('.pattern-workspace input[name="swing"]');
      input.focus({preventScroll: true});
      grid.scrollLeft = 520; grid.scrollTop = 85; window.scrollTo(0, 250);
      window.__continuityRefs = {root, studio: document.querySelector('.studio'), grid, input,
        chance: document.querySelector('#step-inspector input[name="chance"]'),
        cells: [...grid.querySelectorAll(`form:has(input[name="pitch"][value="${pitch}"]) .roll-cell`)].map(cell => ({cell, label: cell.getAttribute('aria-label')})),
        details: [...document.querySelectorAll('details')].filter(detail => ['Pattern timing', 'Range editing'].includes(detail.querySelector('summary')?.textContent))};
    }, rowPitch);
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const snapshot = async (includeChance = false) => page.evaluate(includeChance => {
      const refs = window.__continuityRefs;
      if (includeChance) refs.chance = document.querySelector('#step-inspector input[name="chance"]');
      return {document: window.__continuityDocument, value: refs.input.value, focused: document.activeElement === refs.input,
        ...(includeChance ? {chance: {value: refs.chance.value, focused: document.activeElement === refs.chance,
          step: refs.chance.closest('form').querySelector('input[name="step"]').value}} : {}),
        scroll: {x: scrollX, y: scrollY, left: refs.grid.scrollLeft, top: refs.grid.scrollTop}};
    }, includeChance);
    let draft = await snapshot();
    assert.equal(await page.evaluate(() => window.__continuityRefs.cells.length), 64, 'fixture must expose a full 64-step row');
    assert.ok(draft.scroll.left > 0 && draft.scroll.top > 0 && draft.scroll.y > 0, 'fixture must exercise scrolled page and piano roll in both axes');
    assert.equal(await page.locator('[data-workspace-tool="select"]').getAttribute('aria-pressed'), 'true', 'Select must be the initial editing tool');
    assert.equal(await page.locator('[data-workspace-tool="draw"]').getAttribute('aria-pressed'), 'false');
    const beforeSelect = await state('/api/state');
    let start = requests.length;
    await click(cell(14));
    await page.waitForFunction(() => new URL(location.href).searchParams.get('step') === '14');
    assert.deepEqual(await state('/api/state'), beforeSelect, 'selecting an empty cell changed the score');
    assert.equal(requests.length, start, 'Select issued a write action');
    assert.equal(await page.locator(cell(14)).getAttribute('data-active'), 'false');
    assert.equal(await page.locator('#step-inspector input[name="chance"]').inputValue(), '100', 'another step inherited the previous step draft');
    await assertPatternContinuity(draft);
    await click('.piano-roll .step-number:nth-of-type(1)');
    await page.waitForFunction(() => new URL(location.href).searchParams.get('step') === '1');
    assert.equal(await page.locator('#step-inspector input[name="chance"]').inputValue(), '37', 'returning to step1 lost its draft');
    await assertPatternContinuity(draft);
    await click(cell(14));
    await page.waitForFunction(() => new URL(location.href).searchParams.get('step') === '14');
    assert.equal(await page.locator('#step-inspector input[name="chance"]').inputValue(), '100');
    assert.deepEqual(await state('/api/state'), beforeSelect, 'draft restoration mutated source');
    assert.equal(requests.length, start, 'draft restoration issued a write action');
    await assertPatternContinuity(draft);
    await click('[data-workspace-tool="draw"]');
    assert.equal(await page.locator('[data-workspace-tool="draw"]').getAttribute('aria-pressed'), 'true');
    assert.equal(await page.locator('[data-workspace-tool="select"]').getAttribute('aria-pressed'), 'false');
    // Hold one response to prove that a focused draft survives a real in-flight projection.
    delayNextPattern = true;
    start = requests.length;
    note('step', 'draw cell 15');
    await click(cell(15));
    await waitRequests(start + 1);
    let revision = await projectedRevision(requests[start]);
    assert.notEqual(revision, initialRevision);
    await assertPatternContinuity(draft);

    await page.evaluate(() => {
      window.__continuityLatencies = [];
      const pending = new Map();
      document.addEventListener('click', event => {
        const button = event.target.closest('.roll-cell[name="step"]');
        if (button?.closest('form').querySelector('input[name="action"]')?.value === 'pitch') pending.set(button, performance.now());
      }, true);
      new MutationObserver(records => {
        for (const record of records) {
          const button = record.target;
          if (record.attributeName !== 'data-active' || button.getAttribute('data-active') !== 'true' || !pending.has(button)) continue;
          const started = pending.get(button); pending.delete(button);
          requestAnimationFrame(() => window.__continuityLatencies.push(performance.now() - started));
        }
      }).observe(window.__continuityRefs.grid, {attributes: true, subtree: true, attributeFilter: ['data-active']});
    });
    start = requests.length;
    note('step', '12 queued edits');
    await page.evaluate(pitch => {
      for (let step = 16; step <= 27; step++) document.querySelector(`.piano-roll form:has(input[name="pitch"][value="${pitch}"]) .roll-cell[value="${step}"]`).click();
    }, rowPitch);
    await waitRequests(start + 12);
    for (const record of requests.slice(start, start + 12)) {
      assert.equal(record.status, 200);
      assert.equal(record.payload.revision, revision, 'queued write used a stale revision');
      revision = record.result.data.revision;
    }
    await projectedRevision(requests[start + 11]);
    await page.waitForFunction(() => window.__continuityLatencies.length === 12);
    note('step', '12 edits painted');
    const latencies = await page.evaluate(() => window.__continuityLatencies.slice().sort((a, b) => a - b));
    t.diagnostic(`12 queued cell edits, click→painted projection p50=${latencies[5].toFixed(1)} ms, p95=${latencies[11].toFixed(1)} ms; peak in-flight writes=${maxInFlight}`);
    const serverTimes = requests.slice(start, start + 12).map(record => record.responseMs).sort((a, b) => a - b);
    t.diagnostic(`Intercepted action round trip and JSON decode p50=${serverTimes[5].toFixed(1)} ms, p95=${serverTimes[11].toFixed(1)} ms`);
    assert.equal(maxInFlight, 1, 'managed writes were sent concurrently');
    assert.equal(new URL(page.url()).searchParams.get('step'), '27', 'inspector did not follow the clicked cell');
    assert.equal(await page.locator('[data-workspace-tool="draw"]').getAttribute('aria-pressed'), 'true', 'projection reset the editing tool');
    await assertPatternContinuity(draft);

    start = requests.length;
    note('step', 'undo');
    await click('.toolbar form[action="/__actions/undo"] button');
    await waitRequests(start + 1);
    revision = await projectedRevision(requests[start]);
    assert.equal(await page.locator(cell(27)).getAttribute('data-active'), 'false', 'Undo did not restore the latest cell');
    await assertPatternContinuity(draft);

    // Bypass HTML constraints to exercise authoritative server validation.
    await page.locator('#step-inspector select[name="mode"]').selectOption('note');
    await page.locator('#step-inspector input[name="pitch"]').fill(rowPitch);
    await page.locator('#step-inspector input[name="chance"]').fill('101');
    draft = await snapshot(true);
    const beforeInvalid = await state('/api/state');
    start = requests.length;
    note('step', 'submit invalid chance');
    await page.locator('#step-inspector form').evaluate(form => { form.noValidate = true; form.requestSubmit(form.querySelector('button[type="submit"]')); });
    await waitRequests(start + 1);
    assert.equal(requests[start].status, 422);
    await until(async () => /chance|invalid|100/i.test(await page.locator('[data-workspace-status],.notices,.notice,[role="alert"]').allTextContents().then(text => text.join(' '))), 'validation feedback was not shown');
    assert.deepEqual(await state('/api/state'), beforeInvalid, 'invalid write altered the score');
    await assertPatternContinuity(draft);
    await page.locator('#step-inspector input[name="chance"]').fill('55');
    draft = await snapshot(true);
    start = requests.length;
    note('step', 'save chance 55');
    await click('#step-inspector button[type="submit"]');
    await waitRequests(start + 1);
    revision = await projectedRevision(requests[start]);
    assert.equal(await page.locator('#step-inspector input[name="chance"]').getAttribute('value'), '55', 'corrected chance was not saved to the rendered model');
    await assertPatternContinuity(draft);

    await page.locator('#step-inspector input[name="chance"]').fill('42');
    draft = await snapshot(true);
    const beforeStale = await state('/api/state');
    assert.notEqual(staleRevision, beforeStale.revision, 'conflict fixture must use an older revision');
    injectStale = true; start = requests.length;
    note('step', 'stale write');
    await click(cell(28));
    await waitRequests(start + 1);
    assert.equal(requests[start].status, 409);
    await until(async () => /score changed|reload|conflict|stale/i.test(await page.locator('[data-workspace-status],.notices,.notice,[role="alert"]').allTextContents().then(text => text.join(' '))), 'stale-write feedback was not shown');
    assert.deepEqual(await state('/api/state'), beforeStale, 'stale write altered the score');
    await assertPatternContinuity(draft);
    await page.locator('[data-workspace-retry]').waitFor({state: 'visible'});
    note('step', 'retry');
    await click('[data-workspace-retry]');
    await until(async () => /Workspace updated/i.test(await page.locator('[data-workspace-status]').textContent()), 'saved-state refresh did not settle');
    assert.deepEqual(await state('/api/state'), beforeStale, 'conflict recovery altered the score');
    await assertPatternContinuity(draft);
    draft = await snapshot();
    note('step', 'select step 23');
    await click('.piano-roll .step-number:nth-of-type(23)');
    await page.waitForFunction(() => new URL(location.href).searchParams.get('step') === '23');
    assert.equal(await page.locator('#step-inspector input[name="chance"]').inputValue(), '100', 'step23 inherited step27 conflict draft');
    await assertPatternContinuity(draft);
    note('step', 'select step 27');
    await click('.piano-roll .step-number:nth-of-type(27)');
    await page.waitForFunction(() => new URL(location.href).searchParams.get('step') === '27');
    assert.equal(await page.locator('#step-inspector input[name="chance"]').inputValue(), '42', 'step27 conflict draft was not restored');
    await assertPatternContinuity(draft);

    // A successful write can be followed by another editor's save before its
    // projection. Drop the second queued gesture and retain the visible drafts.
    draft = await snapshot(true);
    injectInterveningSave = true; start = requests.length;
    await page.evaluate(selectors => selectors.forEach(selector => document.querySelector(selector).click()), [cell(29), cell(30)]);
    await waitRequests(start + 1);
    await page.locator('[data-workspace-retry]').waitFor({state: 'visible'});
    assert.equal(requests.length, start + 1, 'queued gesture overwrote an intervening save');
    assert.deepEqual(await state('/api/state'), interveningState, 'queued write changed the intervening save');
    await assertPatternContinuity(draft);
    await click('[data-workspace-retry]');
    await until(async () => /Workspace updated/i.test(await page.locator('[data-workspace-status]').textContent()), 'intervening-save refresh did not settle');
    assert.deepEqual(await state('/api/state'), interveningState, 'refresh retried the saved command');
    await assertPatternContinuity(draft);
    t.diagnostic('A mismatched write receipt dropped queued gestures, retained drafts, and refreshed without retrying the saved command.');

    // A separate panel verifies preservation of a genuine nested GoSX engine.
    armed = false;
    await page.goto(url('mixer'), {waitUntil: 'domcontentloaded'});
    await ready();
    await page.locator('#cicada-meters canvas').waitFor();
    await page.evaluate(() => { window.__continuityMeters = {root: document.querySelector('#cicada-workspace'), mount: document.querySelector('#cicada-meters'), canvas: document.querySelector('#cicada-meters canvas'), document: window.__continuityDocument}; });
    documentRequests = 0; armed = true;
    const gain = page.locator('#mixer form').filter({has: page.locator('input[name="path"][value="keys.level"]')});
    await gain.locator('input[name="value"]').fill('-7.25');
    start = requests.length;
    await gain.locator('button[type="submit"]').evaluate(button => button.click());
    await waitRequests(start + 1);
    const mixerRecord = requests[start];
    assert.equal(mixerRecord.payload.path, 'keys.level');
    assert.equal(mixerRecord.payload.value, '-7.25');
    if (mixerRecord.status === 200) await projectedRevision(mixerRecord);
    else t.diagnostic(`Mixer action rejected: ${mixerRecord.result?.message || mixerRecord.status}; continuing independent Live checks`);
    const preserved = await page.evaluate(() => {
      const refs = window.__continuityMeters;
      return refs.root === document.querySelector('#cicada-workspace') && refs.mount === document.querySelector('#cicada-meters') && refs.canvas === document.querySelector('#cicada-meters canvas') && refs.canvas.isConnected && refs.document === window.__continuityDocument;
    });
    assert.equal(preserved, true, 'a managed mixer update replaced its nested meter engine');
    assert.equal(documentRequests, 0, 'mixer update performed document navigation');
    assert.equal((await state('/api/transport')).playing, true, 'mixer update interrupted playback');

    // An active Live lease must survive workspace changes and note-take review.
    armed = false;
    await page.goto(url('live'), {waitUntil: 'domcontentloaded'});
    await ready();
    await page.locator('[data-live-acid]').selectOption('keys');
    const livePatterns = await page.locator('[data-live-pattern] option').evaluateAll(options => options.map(option => option.value));
    assert.ok(livePatterns.includes('continuity'), 'fixture must expose the continuity live pattern');
    await page.locator('[data-live-pattern]').selectOption('continuity');
    await until(async () => !(await page.locator('[data-live-note="50"]').isDisabled()), 'Live keyboard did not observe the running transport');
    await page.evaluate(() => {
      document.activeElement?.blur();
      window.__continuityLive = {root: document.querySelector('#cicada-workspace'), mount: document.querySelector('#cicada-live'),
        key50: document.querySelector('[data-live-note="50"]'), key55: document.querySelector('[data-live-note="55"]'), document: window.__continuityDocument};
    });
    const assertLiveContinuity = async () => {
      assert.equal(await page.evaluate(() => {
        const refs = window.__continuityLive;
        return refs.root === document.querySelector('#cicada-workspace') && refs.mount === document.querySelector('#cicada-live') &&
          refs.key50 === document.querySelector('[data-live-note="50"]') && refs.key55 === document.querySelector('[data-live-note="55"]') &&
          refs.mount.isConnected && refs.document === window.__continuityDocument;
      }), true, 'a workspace projection remounted the Live keyboard');
      assert.equal(documentRequests, 0, 'Live projection performed document navigation');
      assert.equal((await state('/api/transport')).playing, true, 'Live projection stopped playback');
    };
    const beforeLive = await state('/api/state');
    documentRequests = 0; armed = true; start = requests.length;
    await page.keyboard.down('s');
    await waitRequests(start + 1);
    const held = requests[start];
    assert.equal(held.action, 'live');
    assert.equal(held.status, 200);
    assert.equal(held.payload.note, '50');
    assert.equal(held.payload.on, 'true');
    assert.ok(held.payload.lease.length >= 8);
    assert.equal(await page.locator('[data-live-note="50"]').getAttribute('aria-pressed'), 'true', 'held note feedback was not immediate');
    start = requests.length;
    await page.locator('form[action="/__actions/launch"] thead button').filter({hasText: /^alternate$/}).evaluate(button => button.click());
    await waitRequests(start + 1);
    assert.equal(requests[start].action, 'launch');
    await projectedRevision(requests[start]);
    await assertLiveContinuity();
    assert.equal(await page.locator('[data-live-note="50"]').getAttribute('aria-pressed'), 'true', 'launch released the held key');
    assert.equal(requests.slice(start).some(record => record.action === 'live' && record.payload.type === 'release'), false, 'launch released the Live lease');
    start = requests.length;
    await page.keyboard.up('s');
    await waitRequests(start + 1);
    const released = requests[start];
    assert.equal(released.action, 'live');
    assert.equal(released.status, 200);
    assert.equal(released.payload.note, '50');
    assert.equal(released.payload.on, 'false');
    assert.equal(released.payload.lease, held.payload.lease, 'key-up used a new Live mount lease');
    assert.ok(Number(released.payload.sequence) > Number(held.payload.sequence), 'Live command sequence did not advance');
    assert.equal(await page.locator('[data-live-note="50"]').getAttribute('aria-pressed'), 'false');
    await click('[data-live-control="record"]');
    assert.equal(await page.locator('[data-live-control="record"]').getAttribute('aria-pressed'), 'true');
    start = requests.length;
    await page.keyboard.down('g');
    await waitRequests(start + 1);
    await page.keyboard.up('g');
    await waitRequests(start + 2);
    for (const [index, on] of [[0, 'true'], [1, 'false']]) {
      const command = requests[start + index];
      assert.equal(command.action, 'live');
      assert.equal(command.status, 200);
      assert.equal(command.payload.note, '55');
      assert.equal(command.payload.on, on);
      assert.equal(command.payload.lease, held.payload.lease, 'recording used a different Live mount lease');
      assert.ok(Number(command.payload.sequence) > Number(requests[start + index - 1].payload.sequence), 'recording command sequence did not advance');
    }
    await click('[data-live-control="finish"]');
    await waitRequests(start + 3);
    assert.equal(requests[start + 2].action, 'note-preview');
    assert.equal(requests[start + 2].status, 200);
    await page.locator('.note-preview').waitFor();
    const recorded = JSON.parse(requests[start + 2].payload.recordings);
    assert.equal(recorded[0].track, 'keys');
    assert.equal(recorded[0].pattern, 'continuity');
    assert.equal(recorded[0].notes[0].note, 55);
    await assertLiveContinuity();
    start = requests.length;
    await click('.note-preview button[value="discard"]');
    await waitRequests(start + 1);
    assert.equal(requests[start].action, 'note-commit');
    await projectedRevision(requests[start]);
    await page.locator('.note-preview').waitFor({state: 'detached'});
    await assertLiveContinuity();
    assert.deepEqual(await state('/api/state'), beforeLive, 'Live launch and discarded take changed the score');
    t.diagnostic('Held Live note retained its lease and key/mount identity through launch; Finish and discard projected without document navigation.');

    // The first-party GoSX editor retains its surface while clean canonical
    // updates and dirty drafts follow their respective owners.
    armed = false;
    await page.goto(url('code'), {waitUntil: 'domcontentloaded'});
    await ready();
    const textarea = page.locator('#editor-content');
    const beforeCode = await state('/api/state');
    const codeOriginal = beforeCode.source.replace(/\r\n/g, '\n');
    assert.ok(codeOriginal.includes('\n  \n'), 'fixture lost its indent-only blank line before the Score checks');
    // The first-party editor displays empty lines without indentation while
    // canonical source retains those bytes until the user saves edited text.
    const cleanCode = codeOriginal.replace(/(^|\n)[ \t]+(?=\n|$)/g, '$1');
    assert.equal(await textarea.inputValue(), cleanCode);
    assert.equal(await textarea.evaluate(editor => editor.value !== editor.defaultValue), false, 'canonical indentation made the clean editor dirty');
    await page.evaluate(() => {
      const editor = document.querySelector('#editor-content');
      window.__continuityCode = {root: document.querySelector('#cicada-workspace'), editor,
        form: editor.closest('form'), shell: editor.closest('.editor-source-shell'), document: window.__continuityDocument};
    });
    const prepareCode = async value => {
      await textarea.fill(value);
      await textarea.evaluate(editor => {
        editor.focus({preventScroll: true});
        editor.setSelectionRange(editor.value.length - 13, editor.value.length - 9, 'backward');
        editor.scrollTop = 85; editor.scrollLeft = 75; window.scrollTo(0, 250);
      });
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    };
    const codeSnapshot = async () => page.evaluate(() => {
      const editor = document.querySelector('#editor-content');
      return {value: editor.value, dirty: editor.value !== editor.defaultValue,
        selection: [editor.selectionStart, editor.selectionEnd, editor.selectionDirection],
        scroll: {x: scrollX, y: scrollY, left: editor.scrollLeft, top: editor.scrollTop}};
    });
    const assertCodeContinuity = async expected => {
      const actual = await page.evaluate(() => {
        const refs = window.__continuityCode, editor = document.querySelector('#editor-content');
        return {same: refs.root === document.querySelector('#cicada-workspace') && refs.editor === editor &&
          refs.form === editor.closest('form') && refs.shell === editor.closest('.editor-source-shell') && refs.document === window.__continuityDocument,
          focused: document.activeElement === editor, value: editor.value, dirty: editor.value !== editor.defaultValue,
          selection: [editor.selectionStart, editor.selectionEnd, editor.selectionDirection],
          scroll: {x: scrollX, y: scrollY, left: editor.scrollLeft, top: editor.scrollTop}};
      });
      assert.equal(actual.same, true, 'Score projection replaced the first-party editor');
      assert.equal(actual.focused, true, 'Score projection lost editor focus');
      assert.equal(actual.value, expected.value, 'visible Score buffer diverged from its owner');
      assert.equal(actual.dirty, expected.dirty, 'Score dirty baseline was not updated');
      if (expected.selection) assert.deepEqual(actual.selection, expected.selection, 'Score projection changed caret/selection');
      if (expected.scroll) for (const key of Object.keys(expected.scroll)) assert.ok(Math.abs(actual.scroll[key] - expected.scroll[key]) <= 2, `Score ${key} scroll jumped`);
      assert.equal(documentRequests, 0, 'Score mutation navigated the document');
      assert.equal((await state('/api/transport')).playing, true, 'Score mutation stopped playback');
    };
    const codeSaved = `${codeOriginal}${codeOriginal.endsWith('\n') ? '' : '\n'}// cicada-continuity-score-save\n${'// editor viewport verification\n'.repeat(40)}`;
    await prepareCode(codeSaved);
    let codeDraft = await codeSnapshot();
    assert.ok(codeDraft.scroll.top > 0 && codeDraft.scroll.y > 0, 'Score fixture must exercise inner and page scrolling');
    documentRequests = 0; armed = true; start = requests.length;
    await click('#editor-native-form button[type="submit"]');
    await waitRequests(start + 1);
    assert.equal(requests[start].action, 'source');
    await projectedRevision(requests[start]);
    assert.equal((await state('/api/state')).source, codeDraft.value, 'Score Save did not persist the exact visible edited text');
    await assertCodeContinuity({...codeDraft, dirty: false});
    start = requests.length;
    await click('.toolbar form[action="/__actions/undo"] button');
    await waitRequests(start + 1);
    await projectedRevision(requests[start]);
    assert.deepEqual(await state('/api/state'), beforeCode, 'Score Undo did not restore the exact saved source');
    await assertCodeContinuity({value: cleanCode, dirty: false});
    const codeUnsaved = `${codeOriginal}${codeOriginal.endsWith('\n') ? '' : '\n'}// cicada-continuity-unsaved-draft\n${'// retained editor draft\n'.repeat(40)}`;
    await prepareCode(codeUnsaved);
    codeDraft = await codeSnapshot();
    assert.equal(codeDraft.dirty, true);
    start = requests.length;
    await click('.toolbar form[action="/__actions/transport"] button[value="play"]');
    await waitRequests(start + 1);
    await projectedRevision(requests[start]);
    assert.deepEqual(await state('/api/state'), beforeCode, 'unrelated transport action saved the Score draft');
    await assertCodeContinuity(codeDraft);
    start = requests.length;
    await page.keyboard.press('Control+z');
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    assert.equal(requests.length, start, 'editor Ctrl-Z escaped into a global score action');
    assert.deepEqual(await state('/api/state'), beforeCode, 'editor Ctrl-Z changed the saved score');
    assert.equal(documentRequests, 0, 'editor Ctrl-Z navigated the document');
    t.diagnostic('Score Save preserved exact visible text/editor/caret/scroll; Undo restored canonical indentation and a clean visible buffer; unrelated action retained unsaved text and native Ctrl-Z issued no score action.');
    assert.deepEqual(errors, [], 'browser callback errors');
    assert.deepEqual(warnings, [], 'browser callback warnings');
    assert.equal(mixerRecord.status, 200, `action mixer: ${mixerRecord.result?.message || 'missing success response'}`);
    t.diagnostic('Select left source/revision unchanged; Draw serialized writes. No document GET, root/grid/cell/timing-input replacements, nested meter or Live remounts, callback errors or warnings; same-step inspector identity and draft/focus/scroll/playback survived422 correction and409 recovery. Held Live lease survived launch; take review and discard stayed in place.');
    assert.ok(latencies[11] < 5000, 'rapid edit queue stalled beyond five seconds');
  } catch (error) {
    try {
      for (const entry of await page.evaluate(() => window.__continuityTimeline || [])) timeline.push({at: entry.at - clock, text: `page ${entry.text}`});
      timeline.sort((a, b) => a.at - b.at);
      const view = await page.evaluate(() => ({
        search: location.search, busy: document.querySelector('#cicada-workspace')?.getAttribute('aria-busy'),
        status: document.querySelector('[data-workspace-status]')?.textContent,
        inspectorStep: document.querySelector('#step-inspector input[name="step"]')?.value,
        chance: document.querySelector('#step-inspector input[name="chance"]')?.value,
      }));
      t.diagnostic(`failure view ${JSON.stringify(view)}`);
      requests.forEach((record, index) => t.diagnostic(`action ${index} ${record.action} status=${record.status} step=${record.payload.step ?? ''} mode=${record.payload.mode ?? ''} chance=${record.payload.chance ?? ''}`));
      timeline.forEach(entry => t.diagnostic(`${String(entry.at).padStart(6)} ms ${entry.text}`));
    } catch (_) { /* The original assertion remains the failure. */ }
    throw error;
  } finally {
    await page.unroute('**/__actions/**');
    try { await nativeAction('transport', {action: 'stop'}); } catch (_) { /* Preserve the primary regression failure. */ }
    await page.close();
  }
}

const repeat = Math.max(1, Number(process.env.CICADA_CONTINUITY_REPEAT) || 5); // experiment: 5 samples per job
for (let run = 1; run <= repeat; run++) {
  const suffix = repeat > 1 ? ` (run ${run} of ${repeat})` : '';
  test(`GoSX projections retain Studio edits, focus, scroll, playback and engine mounts${suffix}`, {timeout: 120_000}, scenario);
}
test.after(async () => { if (shared) await shared.browser.close(); });
