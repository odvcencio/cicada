/*
 * Measure note draw, Undo, Redo, the Arrange tab switch and Arrange selection
 * on a disposable Studio project. One run is one cell of the matrix: one
 * fixture, one viewport, one playback state.
 *
 * node workstation/browser/continuity.test.cjs --write-fixture <new-score>
 *   (or: node workstation/browser/placements.test.cjs --write-fixture <new-score>)
 * ./build/cicada studio <new-score> --audio null --listen 127.0.0.1:8190 > <startup-log>
 * CICADA_STUDIO_URL=http://127.0.0.1:8190 CICADA_STUDIO_STARTUP_LOG=<startup-log> \
 *   node workstation/browser/edit-undo-benchmark.cjs
 *
 * A Studio accepts one owner claim (owner-session.cjs), and the run edits the
 * score, so start a fresh Studio on a fresh fixture for each run.
 *
 * CICADA_BENCH_PLAYBACK=stopped|playing        default stopped
 * CICADA_BENCH_VIEWPORT=1440x900|1280x800|390x844   default 1440x900.
 *   390x844 opens the edit loop with ?region=detail and the Arrange loops with ?region=center.
 * CICADA_BENCH_WARMUPS (default 5) and CICADA_BENCH_TRIALS (default 30; use 60 in CI,
 *   because with 30 samples the p95 is the 29th).
 * CICADA_BENCH_GATES=report|enforce            default report. Gates print to stderr as
 *   pass or fail. With enforce the run exits 1 when an enforced gate fails.
 * CICADA_BENCH_REPORT=<path>                   also write the JSON report to this file.
 * CICADA_PLAYWRIGHT_MODULE and CICADA_CHROME_PATH select local browser tooling.
 *
 * Timing definitions (all in one page clock, performance.now()):
 *   input      the trusted pointerdown or keydown that starts the action.
 *   pending    first requestAnimationFrame callback after the one that saw a pending
 *              marker or a selection mark on the target, as a visible-frame estimate.
 *   confirmed  the same estimate for the first frame that shows the saved state (see
 *              "Confirm signal"). Two numbers per sample. ms is the rAF timestamp of the
 *              frame after the one whose callback saw the state: the method of the
 *              earlier measurements, so it stays comparable, and an upper bound because
 *              work that blocks the main thread after the paint is included.
 *              firstFrameMs is performance.now() inside the callback that saw the state:
 *              the frame is painted right after, so it is a lower bound. A rAF
 *              timestamp is the frame's start time and can be older than the moment the
 *              callback runs when the main thread was busy, so it is not used for it.
 * Measured stages: POST and GET request and response times (Resource Timing), the
 * gosx:navigate event, long tasks, frame gaps. Derived stages are differences of those
 * stamps and are named as such in the report.
 */
'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {createHash} = require('node:crypto');
const {execFileSync} = require('node:child_process');

const viewports = {
  '1440x900': {width: 1440, height: 900, phone: false},
  '1280x800': {width: 1280, height: 800, phone: false},
  '390x844': {width: 390, height: 844, phone: true},
};
const fixtures = [
  {name: 'scene', marker: '// cicada-continuity-test: disposable fixture', pattern: 'continuity'},
  {name: 'placement', marker: '// cicada-placement-test: disposable fixture', pattern: 'ripple'},
];
const clipPanel = '#detail-panel-clip';

// Runs in the page. Every document gets its own copy through addInitScript.
function installActionProbe() {
  const probe = {result: null, navTimes: [], longTasks: []};
  let armed = null;
  let sample = null;
  try { performance.setResourceTimingBufferSize(2000); } catch {}
  try {
    new PerformanceObserver(list => {
      for (const entry of list.getEntries()) probe.longTasks.push([entry.startTime, entry.duration]);
    }).observe({type: 'longtask', buffered: true});
  } catch {}
  document.addEventListener('gosx:navigate', () => probe.navTimes.push(performance.now()));
  // CICADA_BENCH_TRACE=1 adds two diagnostics that cost some time themselves,
  // so a trace run is never a baseline run: when DOM mutations happen, and the
  // long animation frames (start, duration, renderStart) around the action.
  probe.trace = false;
  probe.mutations = [];
  probe.frames = [];
  try {
    new MutationObserver(() => {
      if (!probe.trace) return;
      const at = performance.now();
      probe.mutations.push(at);
      // The first mutation batch after which the confirm check holds: the DOM
      // carries the saved state here, before the next frame can show it.
      if (sample && probe.domConfirmed == null && isConfirmed(sample.selector ? document.querySelector(sample.selector) : null)) probe.domConfirmed = at;
    })
      .observe(document, {subtree: true, childList: true, attributes: true});
  } catch {}
  try {
    new PerformanceObserver(list => {
      for (const e of list.getEntries()) probe.frames.push([e.startTime, e.duration, e.renderStart, e.blockingDuration]);
    }).observe({type: 'long-animation-frame', buffered: true});
  } catch {}

  // Confirm signal. A cell is confirmed when the server has answered: the
  // region that holds it carries a new revision. Today /studio has no region
  // attribute, so the scope is the page: the first hidden "revision" input,
  // which GoSX's soft navigation replaces only when it reconciles the page
  // fetched after the save. An optimistic paint changes the cell but not that
  // revision, so it cannot satisfy this check. When Phase 1 adds
  // data-region-revision, the closest ancestor that carries it takes over with
  // no change to the callers.
  function revisionOf(el) {
    const scoped = el && el.closest('[data-region-revision]');
    if (scoped) return 'region:' + scoped.getAttribute('data-region-revision');
    return 'page:' + (document.querySelector('input[name="revision"]')?.value ?? '');
  }
  const laidOut = el => !!el && el.getClientRects().length > 0 && el.getBoundingClientRect().width > 0;

  function pendingMarker(el, base) {
    if (!el) return null;
    if (el.closest('[data-workspace-pending]')) return 'data-workspace-pending';
    if (el.closest('[aria-busy="true"]')) return 'aria-busy';
    if (sample.kind === 'select') {
      if (el.getAttribute('data-selected') === 'true' && base.selected !== 'true') return 'data-selected';
      if (el.getAttribute('aria-current') && !base.current) return 'aria-current';
    }
    return null;
  }
  // GoSX's own idle/pending attribute on the form or link. Nothing styles it
  // on /studio, so it is recorded apart from the pending marker.
  function gosxState(el) {
    const holder = el?.closest('form[data-gosx-form-state], a[data-gosx-link-state]');
    const value = holder?.getAttribute('data-gosx-form-state') || holder?.getAttribute('data-gosx-link-state');
    return value && value !== 'idle' ? value : null;
  }
  function isConfirmed(el) {
    if (sample.kind === 'edit') {
      return !!el && el.getAttribute('data-active') === sample.active && laidOut(el) &&
        !el.closest('[data-workspace-pending]') && revisionOf(el) !== sample.startRevision;
    }
    if (sample.kind === 'tab') {
      const panel = document.querySelector('#view-panel-arrange');
      return document.querySelector('#view-tab-arrange')?.getAttribute('aria-selected') === 'true' &&
        !!panel && !panel.hidden && laidOut(panel) && panel.getBoundingClientRect().height > 0;
    }
    // select: a navigation finished and the address names the target.
    const navigated = probe.navTimes.some(t => t >= sample.start);
    return navigated && new URL(location.href).searchParams.get(sample.param) === sample.key && laidOut(el);
  }

  function finish() {
    const {start, obs, pub, cb} = sample;
    const rel = value => (value == null ? null : +(value - start).toFixed(2));
    const fetches = performance.getEntriesByType('resource')
      .filter(e => e.startTime >= start - 1 && ['fetch', 'xmlhttprequest'].includes(e.initiatorType))
      .map(e => ({path: new URL(e.name).pathname, startMs: rel(e.startTime), requestStartMs: rel(e.requestStart),
        responseStartMs: rel(e.responseStart), responseEndMs: rel(e.responseEnd),
        encodedBytes: e.encodedBodySize, transferBytes: e.transferSize}));
    const navTimes = probe.navTimes.filter(t => t >= start);
    // A mark that first shows in the confirming frame is the saved state, not a
    // pending state. Count it only when an earlier frame showed it.
    const pendingFrame = pub.pending != null && pub.pending < pub.confirm ? pub.pending : null;
    const until = start + (cb.confirm - start);
    const tasks = probe.longTasks.filter(([at, ms]) => at + ms >= start && at <= until);
    probe.result = {
      ms: pub.confirm - start, firstFrameMs: cb.confirm - start,
      pendingMs: pendingFrame == null ? null : pendingFrame - start,
      pendingFirstFrameMs: pendingFrame == null ? null : cb.pending - start, pendingMarker: pendingFrame == null ? null : sample.pendingName,
      gosxStateMs: pub.gosx == null ? null : pub.gosx - start, gosxState: sample.gosxName,
      navigations: navTimes.length, firstNavigateMs: navTimes.length ? rel(navTimes[0]) : null,
      longTasks: {count: tasks.length, totalMs: +tasks.reduce((sum, [, ms]) => sum + ms, 0).toFixed(1),
        maxMs: +Math.max(0, ...tasks.map(([, ms]) => ms)).toFixed(1)},
      fetches: fetches.filter(f => f.path.startsWith('/__actions/') || f.path === '/studio'),
      otherFetches: fetches.filter(f => !(f.path.startsWith('/__actions/') || f.path === '/studio')).length,
      frames: sample.frames,
    };
    if (probe.trace) {
      const muts = probe.mutations.filter(t => t >= start && t <= until + 400);
      probe.result.trace = {
        domConfirmedMs: rel(probe.domConfirmed),
        mutationsMs: muts.length ? [rel(muts[0]), rel(muts.at(-1)), muts.length] : null,
        longAnimationFramesMs: probe.frames.filter(([at, ms]) => at + ms >= start && at <= until + 400)
          .map(([at, ms, render, block]) => [rel(at), +ms.toFixed(1), rel(render), +block.toFixed(1)]),
      };
    }
    sample = null;
  }

  function tick(now) {
    if (!sample) return;
    if (sample.last !== null) sample.frames.push(now - sample.last);
    sample.last = now;
    for (const key of Object.keys(sample.obs)) if (!(key in sample.pub)) sample.pub[key] = now;
    if ('confirm' in sample.pub) return finish();
    const el = sample.selector ? document.querySelector(sample.selector) : null;
    if (!('pending' in sample.obs)) {
      const marker = pendingMarker(el, sample.base);
      if (marker) { sample.obs.pending = now; sample.cb.pending = performance.now(); sample.pendingName = marker; }
    }
    if (!('gosx' in sample.obs)) {
      const state = gosxState(el);
      if (state) { sample.obs.gosx = now; sample.cb.gosx = performance.now(); sample.gosxName = state; }
    }
    if (isConfirmed(el)) { sample.obs.confirm = now; sample.cb.confirm = performance.now(); }
    requestAnimationFrame(tick);
  }

  function begin(event, spec) {
    armed = null;
    const el = spec.selector ? document.querySelector(spec.selector) : null;
    sample = {
      kind: spec.kind, selector: spec.selector, active: spec.active, param: spec.param, key: spec.key,
      start: event.timeStamp || performance.now(), last: null, frames: [], obs: {}, pub: {}, cb: {},
      pendingName: null, gosxName: null,
      startRevision: revisionOf(el),
      base: {selected: el?.getAttribute('data-selected') ?? null, current: el?.getAttribute('aria-current') ?? null},
    };
    requestAnimationFrame(tick);
  }
  function matches(spec, event) {
    const wanted = spec.input;
    if (!event.isTrusted || event.type !== wanted.type) return false;
    if (wanted.type === 'pointerdown') {
      const target = event.target instanceof Element ? event.target : null;
      return !!target && !!target.closest(spec.selector);
    }
    return event.code === wanted.code && !!(event.ctrlKey || event.metaKey) === !!wanted.ctrlOrMeta &&
      !!event.shiftKey === !!wanted.shift && !!event.altKey === !!wanted.alt;
  }
  probe.arm = function (spec) {
    probe.trace = !!spec.trace;
    probe.mutations = [];
    probe.domConfirmed = null;
    probe.result = null;
    sample = null;
    armed = spec;
  };
  for (const type of ['pointerdown', 'keydown']) {
    window.addEventListener(type, event => { if (armed && matches(armed, event)) begin(event, armed); }, true);
  }
  window.cicadaEditUndoProbe = probe;
}

function count(name, fallback, min, max) {
  const value = Number(process.env[name] ?? fallback);
  assert.ok(Number.isInteger(value) && value >= min && value <= max, `${name} must be an integer from ${min} to ${max}`);
  return value;
}

// Nearest-rank percentile: with 30 samples the p95 is the 29th.
function summarize(values) {
  if (!values.length) return {n: 0, medianMs: null, p95Ms: null, maxMs: null};
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  const median = sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2;
  const round = value => +value.toFixed(2);
  return {n: sorted.length, medianMs: round(median), p95Ms: round(sorted[Math.ceil(.95 * sorted.length) - 1]), maxMs: round(sorted.at(-1))};
}

async function json(page, origin, endpoint) {
  const response = await page.request.get(new URL(endpoint, origin).href);
  assert.ok(response.ok(), `${endpoint} failed with ${response.status()}`);
  return response.json();
}

async function checkActionResponse(response) {
  // GoSX can return 303 for managed edits. The DOM and source checks below
  // verify that the refreshed projection actually applied the mutation.
  if (response.status() === 303) return;
  assert.ok(response.ok(), `${new URL(response.url()).pathname} failed with ${response.status()}`);
  assert.ok(!await response.finished(), 'Action response did not finish');
}

async function measure(page, spec, interact) {
  await page.evaluate(args => window.cicadaEditUndoProbe.arm(args), {...spec, trace: process.env.CICADA_BENCH_TRACE === '1'});
  const waits = spec.route
    ? [page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === spec.route)]
    : [];
  const [response] = await Promise.all([...waits, interact()]);
  if (response) await checkActionResponse(response);
  const handle = await page.waitForFunction(() => window.cicadaEditUndoProbe.result);
  const observation = await handle.jsonValue();
  await handle.dispose();
  assert.ok(Number.isFinite(observation.ms) && observation.ms >= 0, 'Invalid action latency');
  return observation;
}

async function useDraw(page) {
  const draw = page.locator(`${clipPanel} [data-workspace-tool="draw"]:visible`).first();
  if (await draw.count()) {
    if (await draw.getAttribute('aria-pressed') !== 'true') await draw.click();
    return;
  }
  await page.evaluate(() => document.activeElement?.blur());
  await page.keyboard.press('b');
}

async function selectNote(page) {
  const selector = await page.evaluate(panel => {
    const cells = [...document.querySelectorAll(`${panel} .roll-row .roll-cell[data-active="false"]`)];
    const visible = cells.filter(cell => cell.getClientRects().length);
    const cell = visible.find(candidate => {
      const box = candidate.getBoundingClientRect();
      const grid = candidate.closest('.piano-roll')?.getBoundingClientRect();
      return grid && box.right > Math.max(0, grid.left) && box.left < Math.min(innerWidth, grid.right) &&
        box.bottom > Math.max(0, grid.top) && box.top < Math.min(innerHeight, grid.bottom);
    }) ?? visible[0];
    if (!cell) return null;
    const row = cell.closest('.roll-row').getAttribute('data-gosx-key');
    return `${panel} .roll-row[data-gosx-key="${CSS.escape(row)}"] .roll-cell[value="${CSS.escape(cell.value)}"]`;
  }, clipPanel);
  assert.ok(selector, 'The fixture did not expose an inactive piano-roll cell');
  return selector;
}

const undoKeys = process.platform === 'darwin' ? 'Meta+z' : 'Control+z';
const redoKeys = process.platform === 'darwin' ? 'Meta+Shift+z' : 'Control+Shift+z';
const chord = {type: 'keydown', code: 'KeyZ', ctrlOrMeta: true};

// Two visible selection targets in the Arrange view that open a different
// placement or pattern. Prefer the Arrange timeline; where it offers one clip
// (the scene fixture), fall back to the Clip dock's pattern list and say so.
async function selectionTargets(page) {
  return page.evaluate(() => {
    const visible = el => el.getClientRects().length > 0;
    const find = (selector, param, label) => {
      const seen = new Map();
      for (const a of document.querySelectorAll(selector)) {
        if (!visible(a)) continue;
        const key = new URL(a.href).searchParams.get(param);
        if (key && !seen.has(key)) seen.set(key, {kind: label, param, key, selector: `${selector}[href*="${param}=${CSS.escape(key)}"]`});
      }
      return [...seen.values()];
    };
    const arrange = [
      ...find('#view-panel-arrange a.placement-select', 'placement', 'arrange placement'),
      ...find('#view-panel-arrange a.timeline-clip', 'pattern', 'arrange clip'),
    ];
    if (arrange.length >= 2) return arrange.slice(0, 2);
    return find('a.pattern-choice', 'pattern', 'clip-dock pattern').slice(0, 2);
  });
}

async function run() {
  assert.ok(process.env.CICADA_STUDIO_URL && process.env.CICADA_STUDIO_STARTUP_LOG, 'Set CICADA_STUDIO_URL and CICADA_STUDIO_STARTUP_LOG');
  const url = new URL(process.env.CICADA_STUDIO_URL);
  assert.ok(url.protocol === 'http:' && ['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname), 'Use a disposable loopback Studio');
  const origin = url.origin;
  const warmups = count('CICADA_BENCH_WARMUPS', 5, 0, 20);
  const trials = count('CICADA_BENCH_TRIALS', 30, 1, 100);
  const playback = process.env.CICADA_BENCH_PLAYBACK || 'stopped';
  assert.ok(['stopped', 'playing'].includes(playback), 'CICADA_BENCH_PLAYBACK must be stopped or playing');
  const viewportName = process.env.CICADA_BENCH_VIEWPORT || '1440x900';
  const viewport = viewports[viewportName];
  assert.ok(viewport, `CICADA_BENCH_VIEWPORT must be one of ${Object.keys(viewports).join(', ')}`);
  const trace = process.env.CICADA_BENCH_TRACE === '1';
  const gateMode = process.env.CICADA_BENCH_GATES || 'report';
  assert.ok(['report', 'enforce'].includes(gateMode), 'CICADA_BENCH_GATES must be report or enforce');
  const loadBefore = os.loadavg();
  require('./owner-session.cjs');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--mute-audio']});
  try {
    const context = await browser.newContext({viewport: {width: viewport.width, height: viewport.height}, deviceScaleFactor: 1});
    await context.addInitScript(installActionProbe);
    const page = await context.newPage();
    page.setDefaultTimeout(15000);
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));

    await page.goto(new URL('/studio?tab=arrange', origin).href);
    await page.locator('#studio-shell[data-shell-actions="ready"]').waitFor();
    const original = await json(page, origin, '/api/state');
    const fixture = fixtures.find(candidate => original.source?.split(/\r?\n/).includes(candidate.marker));
    assert.ok(fixture, 'Start Studio with a disposable continuity or placement fixture');
    assert.ok(original.revision, 'Studio did not report a source revision');
    const scorePath = region => `/studio?tab=arrange&dock=clip&pattern=${fixture.pattern}${region ? `&region=${region}` : ''}`;
    const verifyPlayback = async () => {
      const transport = await json(page, origin, '/api/transport');
      assert.equal(transport.playing, playback === 'playing', 'Playback state changed during the benchmark');
    };
    const goto = async region => {
      await page.goto(new URL(scorePath(region), origin).href);
      await page.locator('#studio-shell[data-shell-actions="ready"]').waitFor();
    };
    const transportButton = name => page.locator('#transport-form').getByRole('button', {name, exact: true});

    // The phone shows one region at a time. Transport sits in the centre region.
    if (viewport.phone) await goto('center'); else await goto('');
    if (playback === 'playing') {
      const [response] = await Promise.all([
        page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === '/__actions/transport'),
        transportButton('Play').click(),
      ]);
      await checkActionResponse(response);
    }
    await verifyPlayback();

    // 1. Draw, Undo, Redo, Undo again (restores the bytes for the next trial).
    if (viewport.phone) await goto('detail');
    const samples = {draw: [], undo: [], redo: []};
    let restored = original;
    for (let index = 0; index < warmups + trials; index++) {
      await useDraw(page);
      const selector = await selectNote(page);
      const edit = (kind, active, input, route, interact) =>
        measure(page, {kind: 'edit', selector, active: String(active), input, route}, interact);

      const drawn = await edit('draw', true, {type: 'pointerdown'}, '/__actions/pattern', () => page.locator(selector).click());
      const edited = await json(page, origin, '/api/state');
      assert.notEqual(edited.source, original.source, 'Draw did not change saved source');
      assert.ok(edited.revision && edited.revision !== restored.revision, 'Draw did not change revision');
      await verifyPlayback();

      await page.evaluate(() => document.activeElement?.blur());
      const reverted = await edit('undo', false, chord, '/__actions/undo', () => page.keyboard.press(undoKeys));
      const undone = await json(page, origin, '/api/state');
      assert.equal(undone.source, original.source, 'Undo did not restore the exact source bytes');
      assert.ok(undone.revision && undone.revision !== edited.revision, 'Undo did not change revision');
      await verifyPlayback();

      await page.evaluate(() => document.activeElement?.blur());
      const redone = await edit('redo', true, {...chord, shift: true}, '/__actions/redo', () => page.keyboard.press(redoKeys));
      const again = await json(page, origin, '/api/state');
      assert.equal(again.source, edited.source, 'Redo did not restore the drawn source bytes');
      assert.ok(again.revision && again.revision !== undone.revision, 'Redo did not change revision');
      await verifyPlayback();

      // Cleanup Undo, not sampled: leaves the score as it started.
      await page.evaluate(() => document.activeElement?.blur());
      await edit('undo', false, chord, '/__actions/undo', () => page.keyboard.press(undoKeys));
      restored = await json(page, origin, '/api/state');
      assert.equal(restored.source, original.source, 'Cleanup Undo did not restore the exact source bytes');
      assert.deepEqual(errors, []);
      if (index >= warmups) { samples.draw.push(drawn); samples.undo.push(reverted); samples.redo.push(redone); }
    }

    // 2. Arrange tab switch: Alt+Shift+2 (Session), settle, then time Alt+Shift+1.
    if (viewport.phone) await goto('center');
    const tabs = [];
    for (let index = 0; index < warmups + trials; index++) {
      await page.keyboard.press('Alt+Shift+Digit2');
      await page.locator('#view-tab-session[aria-selected="true"]').waitFor();
      await page.waitForFunction(() => document.querySelector('#view-panel-arrange')?.hidden === true);
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(resolve, 50)))));
      const switched = await measure(page, {kind: 'tab', selector: null, input: {type: 'keydown', code: 'Digit1', alt: true, shift: true}},
        () => page.keyboard.press('Alt+Shift+Digit1'));
      assert.deepEqual(errors, []);
      if (index >= warmups) tabs.push(switched);
    }

    // 3. Arrange selection: click a clip or placement that opens a different one.
    await page.evaluate(() => document.activeElement?.blur());
    let targets = await selectionTargets(page);
    if (viewport.phone && targets.length < 2) { await goto('detail'); targets = await selectionTargets(page); }
    const selections = [];
    let selectionNote = null;
    if (targets.length < 2) {
      selectionNote = 'The page offered fewer than two selection targets; selection was not measured.';
    } else {
      for (let index = 0; index < warmups + trials; index++) {
        const target = targets[index % 2];
        const picked = await measure(page, {kind: 'select', selector: target.selector, param: target.param, key: target.key,
          input: {type: 'pointerdown'}}, () => page.locator(target.selector).first().click());
        assert.deepEqual(errors, []);
        if (index >= warmups) selections.push(picked);
      }
    }

    await verifyPlayback();
    if (playback === 'playing') {
      if (viewport.phone) await goto('center');
      await transportButton('Stop').click();
    }

    const fetchOf = (sample, test) => sample.fetches.find(test);
    const stages = sample => {
      const post = fetchOf(sample, f => f.path.startsWith('/__actions/'));
      const get = fetchOf(sample, f => f.path === '/studio');
      const diff = (a, b) => (a == null || b == null ? null : +(a - b).toFixed(2));
      return {
        // measured
        inputToPostStartMs: post?.startMs ?? null,
        actionResponseMs: post?.responseEndMs ?? null,
        postServerMs: post ? diff(post.responseStartMs, post.requestStartMs) : null,
        getServerMs: get ? diff(get.responseStartMs, get.requestStartMs) : null,
        getResponseEndMs: get?.responseEndMs ?? null,
        navigateEventMs: sample.firstNavigateMs,
        // derived: differences between two measured stamps
        fetchPageMs: post && get ? diff(get.responseEndMs, post.responseEndMs) : null,
        applyMs: get && sample.firstNavigateMs != null ? diff(sample.firstNavigateMs, get.responseEndMs) : null,
        navigateToFrameMs: sample.firstNavigateMs != null ? diff(sample.ms, sample.firstNavigateMs) : null,
      };
    };
    const median = values => summarize(values.filter(v => v != null)).medianMs;
    const series = list => {
      const painted = list.filter(s => s.pendingMs != null);
      const stage = list.map(stages);
      return {
        confirmed: {...summarize(list.map(s => s.ms)), samplesMs: list.map(s => +s.ms.toFixed(2))},
        firstFrame: summarize(list.map(s => s.firstFrameMs)),
        pending: {
          painted: painted.length, of: list.length,
          marker: painted[0]?.pendingMarker ?? null,
          ...(painted.length ? summarize(painted.map(s => s.pendingMs)) : {n: 0, medianMs: null, p95Ms: null, maxMs: null}),
          firstFrame: painted.length ? summarize(painted.map(s => s.pendingFirstFrameMs)) : null,
          note: painted.length === list.length ? undefined : 'not painted before confirm in ' + (list.length - painted.length) + ' of ' + list.length,
        },
        gosxStateMs: summarize(list.map(s => s.gosxStateMs).filter(v => v != null)),
        navigations: {perAction: summarize(list.map(s => s.navigations)).medianMs, total: list.reduce((sum, s) => sum + s.navigations, 0)},
        longTasks: {medianCount: median(list.map(s => s.longTasks.count)), medianTotalMs: median(list.map(s => s.longTasks.totalMs)),
          maxMs: Math.max(0, ...list.map(s => s.longTasks.maxMs))},
        otherFetchesMedian: median(list.map(s => s.otherFetches)),
        stageMedians: Object.fromEntries(Object.keys(stage[0] ?? {}).map(key => [key, median(stage.map(s => s[key]))])),
      };
    };
    const allFrames = [...samples.draw, ...samples.undo, ...samples.redo, ...tabs, ...selections].flatMap(s => s.frames);
    const results = {
      draw: series(samples.draw), undo: series(samples.undo), redo: series(samples.redo),
      arrangeTab: series(tabs), arrangeSelect: selections.length ? series(selections) : null,
    };
    if (trace) results.traces = {draw: samples.draw.map(s => s.trace), undo: samples.undo.map(s => s.trace), select: selections.map(s => s.trace)};
    const editFrames = [...samples.draw, ...samples.undo, ...samples.redo].flatMap(s => s.frames);

    // Gates from the studio-redesign spec ("Performance gates"). The phone limit
    // for the pending paint is looser. Scene selection is reported against the
    // 50 ms switch gate but not enforced: which interaction "Arrange switch"
    // means is an open question in the design.
    const pendingLimit = viewport.phone ? 100 : 50;
    const gates = [];
    // observed uses the legacy-comparable timestamp (an upper bound); firstFrame uses
    // the callback clock (a lower bound). A gate that passes only on the lower bound
    // is not a pass.
    const gate = (name, observed, limit, unit, enforced, detail, firstFrame) => gates.push({name, observed, limit, unit, enforced, detail,
      pass: observed != null && observed <= limit, firstFrameObserved: firstFrame ?? null,
      passOnFirstFrame: firstFrame != null ? firstFrame <= limit : null});
    for (const kind of ['draw', 'undo', 'redo']) {
      const r = results[kind];
      gate(`${kind} confirmed median`, r.confirmed.medianMs, 150, 'ms', true, undefined, r.firstFrame.medianMs);
      gate(`${kind} confirmed p95`, r.confirmed.p95Ms, 250, 'ms', true, undefined, r.firstFrame.p95Ms);
      gate(`${kind} pending p95`, r.pending.painted === r.pending.of ? r.pending.p95Ms : null, pendingLimit, 'ms', true,
        r.pending.painted === r.pending.of ? undefined : 'no pending state painted before confirm');
    }
    gate('arrange tab switch median', results.arrangeTab.confirmed.medianMs, 50, 'ms', true, undefined, results.arrangeTab.firstFrame.medianMs);
    if (results.arrangeSelect) {
      gate('arrange selection median', results.arrangeSelect.confirmed.medianMs, 50, 'ms', false, `${targets[0].kind}; not enforced`, results.arrangeSelect.firstFrame.medianMs);
      const r = results.arrangeSelect.pending;
      gate('arrange selection pending p95', r.painted === r.of ? r.p95Ms : null, pendingLimit, 'ms', false,
        r.painted === r.of ? 'not enforced' : 'no pending state painted before confirm; not enforced');
    }

    const git = args => { try { return execFileSync('git', args, {encoding: 'utf8', cwd: __dirname}).trim(); } catch { return null; } };
    const report = {
      generatedAt: new Date().toISOString(), git: {commit: git(['rev-parse', 'HEAD']), branch: git(['rev-parse', '--abbrev-ref', 'HEAD'])},
      browser: browser.version(), host: {platform: process.platform, cpus: os.cpus().length, loadBefore, loadAfter: os.loadavg()},
      fixture: fixture.name, viewport: viewportName, playback, warmups, trials, trace,
      confirmSignal: 'cell matches, no pending marker, page revision input changed (region revision when present)',
      sourceRevision: original.revision, fixtureSha256: createHash('sha256').update(original.source).digest('hex'),
      sourceRestored: restored.source === original.source,
      selection: {kind: targets[0]?.kind ?? null, targets: targets.map(t => t.key), note: selectionNote},
      results,
      frameGaps: {...summarize(allFrames), over50: allFrames.filter(ms => ms > 50).length, over100: allFrames.filter(ms => ms > 100).length,
        editOnly: {n: editFrames.length, over50: editFrames.filter(ms => ms > 50).length, over100: editFrames.filter(ms => ms > 100).length}},
      gateMode, gates,
      measured: ['input to confirmed frame', 'input to pending frame', 'POST and GET request/response times (Resource Timing)',
        'gosx:navigate event time', 'long tasks', 'frame gaps'],
      derived: ['fetchPageMs = GET responseEnd - POST responseEnd', 'applyMs = gosx:navigate - GET responseEnd',
        'navigateToFrameMs = confirmed - gosx:navigate'],
    };
    const text = JSON.stringify(report);
    if (process.env.CICADA_BENCH_REPORT) {
      fs.mkdirSync(path.dirname(path.resolve(process.env.CICADA_BENCH_REPORT)), {recursive: true});
      fs.writeFileSync(process.env.CICADA_BENCH_REPORT, text + '\n');
    }
    console.log(text);
    console.error(`gates (${fixture.name}, ${viewportName}, ${playback}, ${gateMode}):`);
    for (const g of gates) {
      console.error(`  ${g.pass ? 'pass' : 'FAIL'}  ${g.name}: ${g.observed ?? 'n/a'} ${g.unit} (limit ${g.limit})${g.firstFrameObserved != null ? `, first frame ${g.firstFrameObserved}` : ''}${g.detail ? ' - ' + g.detail : ''}`);
    }
    if (gateMode === 'enforce' && gates.some(g => g.enforced && !g.pass)) process.exitCode = 1;
  } finally {
    await browser.close();
  }
}

run().catch(error => { console.error(error); process.exitCode = 1; });
