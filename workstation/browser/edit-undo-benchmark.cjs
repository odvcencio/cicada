/*
 * Measure note draw and Undo on a disposable Studio project.
 * node workstation/browser/continuity.test.cjs --write-fixture <new-score>
 * ./build/cicada studio <new-score> --audio null --listen 127.0.0.1:8190 > <startup-log>
 * CICADA_STUDIO_URL=http://127.0.0.1:8190 CICADA_STUDIO_STARTUP_LOG=<startup-log> \
 *   node workstation/browser/edit-undo-benchmark.cjs
 * Set CICADA_BENCH_PLAYBACK=playing to repeat with playback running.
 * CICADA_BENCH_WARMUPS and CICADA_BENCH_TRIALS default to 5 and 30.
 * CICADA_PLAYWRIGHT_MODULE and CICADA_CHROME_PATH select local browser tooling.
 */
'use strict';

const assert = require('node:assert/strict');
const {createHash} = require('node:crypto');

const viewport = {width: 1440, height: 900};
const fixtureMarker = '// cicada-continuity-test: disposable fixture';
const clipPanel = '#detail-panel-clip';

function installActionProbe() {
  let armed = null;
  let sample = null;
  const probe = { result: null };
  function tick(now) {
    if (!sample) return;
    if (sample.last !== null) sample.frames.push(now - sample.last);
    sample.last = now;
    if (sample.publishNext) {
      probe.result = { ms: now - sample.start, frames: sample.frames.slice() };
      sample = null;
      return;
    }
    if (sample.confirmed) {
      const el = document.querySelector(sample.selector);
      if (el?.getAttribute('data-active') === sample.active && el.getClientRects().length) sample.publishNext = true;
    }
    requestAnimationFrame(tick);
  }
  function begin(event) {
    const spec = armed;
    armed = null;
    sample = { selector: spec.selector, active: spec.active, start: event.timeStamp || performance.now(),
      last: null, frames: [], confirmed: false, publishNext: false };
    requestAnimationFrame(tick);
  }
  probe.arm = function ({ selector, active, input }) {
    probe.result = null;
    sample = null;
    armed = { selector, active: String(active), input: input || 'pointerdown' };
  };
  probe.confirm = function () {
    if (!sample) return false;
    sample.confirmed = true;
    return true;
  };
  window.addEventListener('pointerdown', (event) => {
    if (!armed || !event.isTrusted || armed.input !== 'pointerdown') return;
    const target = event.target instanceof Element ? event.target : null;
    if (target && target.closest(armed.selector)) begin(event);
  }, true);
  window.addEventListener('keydown', (event) => {
    if (!armed || !event.isTrusted || armed.input !== 'keydown') return;
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'z') begin(event);
  }, true);
  window.cicadaEditUndoProbe = probe;
}

function count(name, fallback, min, max) {
  const value = Number(process.env[name] ?? fallback);
  assert.ok(Number.isInteger(value) && value >= min && value <= max, `${name} must be an integer from ${min} to ${max}`);
  return value;
}

function summarize(values) {
  assert.ok(values.length, 'No samples recorded');
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  const median = sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2;
  return {medianMs: +median.toFixed(2), p95Ms: sorted[Math.ceil(.95 * sorted.length) - 1], maxMs: sorted.at(-1)};
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

async function measure(page, selector, active, input, route, interact) {
  await page.evaluate(args => window.cicadaEditUndoProbe.arm(args), {selector, active, input});
  const [response] = await Promise.all([
    page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === route),
    interact(),
  ]);
  await checkActionResponse(response);
  assert.ok(await page.evaluate(() => window.cicadaEditUndoProbe.confirm()), 'No trusted input reached the probe');
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
  assert.ok(selector, 'The continuity fixture did not expose an inactive piano-roll cell');
  return selector;
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
  require('./owner-session.cjs');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--mute-audio']});
  try {
    const context = await browser.newContext({viewport, deviceScaleFactor: 1});
    await context.addInitScript(installActionProbe);
    const page = await context.newPage();
    page.setDefaultTimeout(15000);
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(new URL('/studio?tab=arrange&dock=clip&pattern=continuity', origin).href);
    await page.locator('#studio-shell[data-shell-actions="ready"]').waitFor();
    const original = await json(page, origin, '/api/state');
    assert.ok(original.source?.split(/\r?\n/).includes(fixtureMarker), 'Start Studio with the disposable continuity fixture');
    assert.ok(original.revision, 'Studio did not report a source revision');
    const verifyPlayback = async () => {
      const transport = await json(page, origin, '/api/transport');
      assert.equal(transport.playing, playback === 'playing', 'Playback state changed during the benchmark');
    };
    if (playback === 'playing') {
      const [response] = await Promise.all([
        page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === '/__actions/transport'),
        page.locator('#transport-form').getByRole('button', {name: 'Play', exact: true}).click(),
      ]);
      await checkActionResponse(response);
    }
    await verifyPlayback();
    const edit = [], undo = [], frames = [];
    let restored = original;
    for (let index = 0; index < warmups + trials; index++) {
      await useDraw(page);
      const selector = await selectNote(page);
      const drawn = await measure(page, selector, 'true', 'pointerdown', '/__actions/pattern', () => page.locator(selector).click());
      const edited = await json(page, origin, '/api/state');
      assert.notEqual(edited.source, original.source, 'Draw did not change saved source');
      assert.ok(edited.revision && edited.revision !== restored.revision, 'Draw did not change revision');
      await verifyPlayback();

      await page.evaluate(() => document.activeElement?.blur());
      const reverted = await measure(page, selector, 'false', 'keydown', '/__actions/undo',
        () => page.keyboard.press(process.platform === 'darwin' ? 'Meta+z' : 'Control+z'));
      restored = await json(page, origin, '/api/state');
      assert.equal(restored.source, original.source, 'Undo did not restore the exact source bytes');
      assert.ok(restored.revision && restored.revision !== edited.revision, 'Undo did not change revision');
      await verifyPlayback();
      assert.deepEqual(errors, []);
      if (index >= warmups) {
        edit.push(drawn.ms);
        undo.push(reverted.ms);
        frames.push(...drawn.frames, ...reverted.frames);
      }
    }
    if (playback === 'playing') await page.locator('#transport-form').getByRole('button', {name: 'Stop', exact: true}).click();
    console.log(JSON.stringify({
      browser: browser.version(), viewport, playback, warmups, trials,
      fixtureSha256: createHash('sha256').update(original.source).digest('hex'),
      sourceRestored: restored.source === original.source,
      edit: {...summarize(edit), samplesMs: edit}, undo: {...summarize(undo), samplesMs: undo},
      frameGaps: {...summarize(frames), samplesMs: frames, over50: frames.filter(ms => ms > 50).length,
        over100: frames.filter(ms => ms > 100).length},
    }));
  } finally {
    await browser.close();
  }
}

run().catch(error => { console.error(error); process.exitCode = 1; });
