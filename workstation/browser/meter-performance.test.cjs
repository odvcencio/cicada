'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const stall=require('./page-stall.cjs');

test('Meter layout stays cached while live levels, clipping, track names and viewport update', {timeout: 60000}, async t => {
  const base = process.env.CICADA_METERS_URL;
  if (!base) return t.skip('Set CICADA_METERS_URL to a private silent Studio fixture.');
  assert.equal(new URL(base).hostname, '127.0.0.1');
  const proof = process.env.CICADA_METERS_PROOF_DIR;
  if (proof) assert.ok(path.isAbsolute(proof));
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--no-sandbox']});
  const watches = [];
  stall.teardown(t, browser, {watch: watches, exitWhenDone: true});
  const context = await browser.newContext({viewport: {width: 1440, height: 1100}, reducedMotion: 'reduce', storageState: process.env.CICADA_METERS_STORAGE_STATE});
  watches.push(await stall.watchContext(context, {serverLog: process.env.CICADA_STUDIO_STARTUP_LOG}));
  await context.addInitScript(() => {
    performance.setResourceTimingBufferSize(100000);
    window.meterPaints = {fillRect: 0, fillText: 0, measureText: 0};
    for (const name of Object.keys(window.meterPaints)) {
      const original = CanvasRenderingContext2D.prototype[name];
      CanvasRenderingContext2D.prototype[name] = function (...args) {
        if (this.canvas.parentElement?.id === 'cicada-meters') window.meterPaints[name]++;
        return original.apply(this, args);
      };
    }
  });
  const page = await context.newPage(), errors = [];
  page.on('pageerror', error => errors.push(error.message));
  let peak = -6, prefix = 'track';
  await page.route('**/api/meters', route => {
    const tracks = Object.fromEntries(Array.from({length: 64}, (_, i) => [prefix + String(i).padStart(2, '0'), {peak, rms: peak - 3}]));
    return route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({tracks, master: {peak, rms: peak - 3}})});
  });
  await page.goto(base + '/?panel=mixer', {waitUntil: 'domcontentloaded'});
  await page.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();
  const canvas = page.locator('#cicada-meters canvas[data-meter-track-count="64"]');
  await canvas.waitFor();
  const pixel = (x, y) => page.evaluate(([x, y]) => Array.from(document.querySelector('#cicada-meters canvas').getContext('2d').getImageData(x, y, 1, 1).data).slice(0, 3), [x, y]);
  const waitPixel = color => page.waitForFunction(color => Array.from(document.querySelector('#cicada-meters canvas').getContext('2d').getImageData(8, 20, 1, 1).data).slice(0, 3).every((value, i) => value === color[i]), color);
  await waitPixel([136, 217, 187]);
  await page.waitForTimeout(300);
  await page.evaluate(() => { for (const key in window.meterPaints) window.meterPaints[key] = 0; });
  // Time the poller on the page's own clock, from the browser's resource
  // timing, so a slow test process or a busy runner cannot skew the rate: the
  // meters are polled about 20 times a second, so consecutive requests must be
  // at most 100 ms apart at the median (the old check: 6 requests in 600 ms).
  const since = await page.evaluate(() => performance.now());
  await page.waitForFunction(since => performance.getEntriesByType('resource').filter(e => e.name.includes('/api/meters') && e.startTime > since).length >= 8, since, {timeout: 20000});
  const starts = await page.evaluate(since => performance.getEntriesByType('resource').filter(e => e.name.includes('/api/meters') && e.startTime > since).map(e => e.startTime).sort((a, b) => a - b), since);
  const gaps = starts.slice(1).map((value, i) => value - starts[i]).sort((a, b) => a - b);
  const medianGap = gaps[Math.floor(gaps.length / 2)];
  const steadyPaints = await page.evaluate(() => ({...window.meterPaints}));
  assert.ok(medianGap <= 100, `Live meter polling slowed down: median gap ${medianGap.toFixed(1)} ms over ${starts.length} requests.`);
  assert.deepEqual(steadyPaints, {fillRect: 0, fillText: 0, measureText: 0}, 'An unchanged layout or level was repainted.');

  peak = -40;
  await waitPixel([41, 54, 45]);
  peak = 0;
  await waitPixel([255, 147, 111]);
  assert.deepEqual(await pixel(8, 20), [255, 147, 111]);
  assert.equal(await page.evaluate(() => window.meterPaints.fillText), 0, 'Changing levels repainted static labels.');

  // Membership changes with the same count must invalidate the label cache.
  prefix = 'second';
  await page.waitForFunction(() => window.meterPaints.fillText >= 65);
  assert.match(await canvas.getAttribute('aria-label'), /64 tracks and master/);
  if (proof) {
    fs.mkdirSync(proof, {recursive: true});
    await page.waitForTimeout(1100); // Include the once-per-second numeric readout.
    await page.screenshot({path: path.join(proof, 'meter-cache-1440.png')});
  }
  await page.setViewportSize({width: 390, height: 844});
  await page.waitForFunction(() => Number(document.querySelector('#cicada-meters canvas').dataset.meterColumns) <= 6);
  const layout = await canvas.evaluate(canvas => ({width: canvas.width, cssWidth: canvas.getBoundingClientRect().width, columns: Number(canvas.dataset.meterColumns), height: canvas.height}));
  assert.ok(Math.abs(layout.width - layout.cssWidth) <= 1);
  assert.ok(layout.width / layout.columns >= 64);
  assert.equal(layout.height, Math.ceil(65 / layout.columns) * 140);
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  await waitPixel([255, 147, 111]);
  if (proof) {
    await page.locator('#cicada-meters').screenshot({path: path.join(proof, 'meter-cache-390.png')});
    fs.writeFileSync(path.join(proof, 'meter-cache-proof.json'), JSON.stringify({steadyPaints, livePollingRetained: true, changingLevels: true, clippingColors: true, sameCountTrackRename: true, layout, microphoneActivated: false, errors}, null, 2) + '\n');
  }
  assert.deepEqual(errors, []);
});
