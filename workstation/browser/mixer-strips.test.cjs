/*
 * Mixer channel strips: live meters, mute and solo, pan.
 *
 * ./build/cicada studio /path/to/disposable.cicada --audio null --listen 127.0.0.1:8190
 * CICADA_STUDIO_URL=http://127.0.0.1:8190 node --test workstation/browser/mixer-strips.test.cjs
 *
 * Use a disposable score from continuity.test.cjs --write-fixture on a fresh
 * Studio process. The suite mutes, pans and restores one track. It answers
 * /api/meters with a fixed frame, so it needs no audio. CICADA_MIXER_PROOF_DIR
 * optionally keeps screenshots. CICADA_PLAYWRIGHT_MODULE and CICADA_CHROME_PATH
 * select Playwright and Chrome, as in shell.test.cjs.
 */
'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {createRequire} = require('node:module');
const test = require('node:test');

const playwright = () => process.env.CICADA_PLAYWRIGHT_MODULE
  ? createRequire(path.resolve(process.env.CICADA_PLAYWRIGHT_MODULE, 'package.json'))('playwright') : require('playwright');

async function until(check, message) {
  const end = Date.now() + 15_000;
  while (Date.now() < end) {
    if (await check()) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  assert.fail(message);
}

// Enhanced forms post JSON; native forms post multipart fields.
const formValues = async request => request.headers()['content-type']?.includes('application/json')
  ? request.postDataJSON() : Object.fromEntries(await new Response(request.postDataBuffer(), {headers: {'Content-Type': request.headers()['content-type']}}).formData());

// The first browser context to reach a Studio owns it, so every test shares one.
let shared;
async function sharedContext() {
  if (!shared) {
    const browser = await playwright().chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--no-sandbox', '--mute-audio']});
    shared = {browser, context: await browser.newContext({viewport: {width: 1440, height: 900}})};
  }
  return shared.context;
}
test.after(async () => { await shared?.browser.close(); });

async function openMixer(viewport = {width: 1440, height: 900}) {
  const base = process.env.CICADA_STUDIO_URL;
  assert.ok(base && ['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname), 'use a local Studio');
  const page = await (await sharedContext()).newPage();
  await page.setViewportSize(viewport);
  page.setDefaultTimeout(15_000);
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  // The page's meter frame. Tests change it; a null frame serves nothing.
  const state = {peak: -12, rms: -20, ids: null, polls: 0};
  await page.route('**/api/meters', route => {
    state.polls++;
    const level = {peak: state.peak, rms: state.rms};
    const tracks = {};
    for (const id of state.ids || []) tracks[id] = level;
    route.fulfill({contentType: 'application/json', body: JSON.stringify({
      type: 'meters', tick: 1, tracks, returns: {}, buses: {music: level}, master: {peak: state.peak, rms: state.rms, pre_peak: state.peak, comp_gr: 0, limiter_gr: 0, over: false},
      loudness: {momentary: -14.2, short_term: -15.1, integrated: -16.3, range: 4, true_peak: -1.4, sample_peak: -2, dropped_blocks: 0},
    })});
  });
  await page.goto(new URL('/studio?tab=mixer', base).href);
  await page.locator('#view-panel-mixer .strip[data-mixer-strip=track]').first().waitFor();
  return {page, errors, state, close: () => page.close()};
}

test('strip meters follow /api/meters, clip at 0 dBFS, and poll only while the Mixer shows', {timeout: 90_000}, async () => {
  const {page, errors, state, close} = await openMixer();
  try {
    const strip = page.locator('#view-panel-mixer .strip[data-mixer-strip=track]').first();
    const meter = strip.locator('.strip-meter');
    const id = (await meter.getAttribute('data-meter')).replace(/^track:/, '');
    assert.ok(id, 'the first track strip has a meter with its track id');
    assert.equal(await meter.getAttribute('aria-live'), 'off');
    assert.equal(await meter.getAttribute('role'), 'img');
    state.ids = [id];
    const css = name => meter.evaluate((node, prop) => node.style.getPropertyValue(prop), name);

    // -12 dBFS peak on the -60..+6 scale is 48/66 = 0.727; -20 dBFS RMS is 0.606.
    await until(async () => Math.abs(parseFloat(await css('--peak')) - 48 / 66) < 0.01, 'the meter peak follows the frame');
    assert.ok(Math.abs(parseFloat(await css('--rms')) - 40 / 66) < 0.01, 'the meter RMS follows the frame');
    assert.equal(await meter.getAttribute('data-clip'), 'false');
    await until(async () => /peak -12\.0 dBFS, RMS -20\.0 dBFS/.test(await meter.getAttribute('aria-label')), 'the label carries the dB values');
    assert.ok(Math.abs(parseFloat(await css('--hold')) - 48 / 66) < 0.01, 'the hold tick sits at the peak');
    const master = page.locator('#view-panel-mixer .strip[data-mixer-strip=master]');
    await until(async () => (await master.locator('[data-loudness=integrated]').textContent()) === '-16.3', 'the master strip shows integrated loudness');
    assert.equal(await master.locator('[data-loudness=momentary]').textContent(), '-14.2');
    assert.equal(await master.locator('[data-loudness=short_term]').textContent(), '-15.1');
    assert.equal(await master.locator('[data-loudness=true_peak]').textContent(), '-1.4');

    // 0 dBFS lights the clip lamp and the label says so; the hold tick stays
    // at the loudest peak while the level falls back.
    state.peak = 0;
    await until(async () => (await meter.getAttribute('data-clip')) === 'true', 'the clip indicator turns on at 0 dBFS');
    await until(async () => /clipping/.test(await meter.getAttribute('aria-label')), 'the label names the clip');
    state.peak = -30;
    await until(async () => Math.abs(parseFloat(await css('--peak')) - 30 / 66) < 0.01, 'the peak falls back');
    assert.ok(parseFloat(await css('--hold')) > 0.8, 'the hold tick stays high for a moment');

    // Other views do no work: no polls while the Mixer is hidden.
    if (process.env.CICADA_MIXER_PROOF_DIR) {
      fs.mkdirSync(process.env.CICADA_MIXER_PROOF_DIR, {recursive: true});
      await page.screenshot({path: path.join(process.env.CICADA_MIXER_PROOF_DIR, 'mixer-1440.png')});
    }
    await page.locator('#view-tab-arrange').click();
    await page.locator('#view-panel-mixer').waitFor({state: 'hidden'});
    await new Promise(resolve => setTimeout(resolve, 500));
    const hiddenAt = state.polls;
    await new Promise(resolve => setTimeout(resolve, 1000));
    assert.equal(state.polls, hiddenAt, 'no /api/meters requests while the Mixer is hidden');
    await page.locator('#view-tab-mixer').click();
    await until(() => state.polls > hiddenAt, 'polling resumes when the Mixer shows again');
    assert.deepEqual(errors, []);
  } finally {
    await close();
  }
});

test('M posts the mute path and value, Undo-able; pan submits on change and resets', {timeout: 90_000}, async () => {
  const {page, errors, close} = await openMixer();
  try {
    const strip = page.locator('#view-panel-mixer .strip[data-mixer-strip=track]').first();
    const mute = strip.locator('button[data-toggle=mute]');
    const solo = strip.locator('button[data-toggle=solo]');
    assert.equal(await mute.textContent(), 'M');
    assert.equal(await solo.textContent(), 'S');
    assert.equal(await mute.getAttribute('aria-pressed'), 'false');

    const post = () => page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/mixer');
    let pending = post();
    await mute.click();
    let values = await formValues(await pending);
    assert.match(values.path, /\.mute$/);
    assert.equal(values.value, 'true');
    assert.equal(values.valueType, 'bool');
    const track = values.path.replace(/\.mute$/, '');
    await until(async () => (await mute.getAttribute('aria-pressed')) === 'true', 'the saved mute shows pressed');
    // The Arrange rail shows the same saved state.
    await page.locator('#view-tab-arrange').click();
    const rail = page.locator('#view-panel-arrange .rail-toggles button[data-toggle=mute][aria-pressed=true]');
    await rail.first().waitFor();
    await page.locator('#view-tab-mixer').click();

    pending = post();
    await mute.click();
    values = await formValues(await pending);
    assert.equal(values.path, `${track}.mute`);
    assert.equal(values.value, 'false');
    await until(async () => (await mute.getAttribute('aria-pressed')) === 'false', 'a second click unmutes');

    pending = post();
    await solo.click();
    values = await formValues(await pending);
    assert.equal(values.path, `${track}.solo`);
    assert.equal(values.value, 'true');
    await until(async () => (await solo.getAttribute('aria-pressed')) === 'true', 'solo shows pressed');
    pending = post();
    await solo.click();
    assert.equal((await formValues(await pending)).value, 'false');
    await until(async () => (await solo.getAttribute('aria-pressed')) === 'false', 'solo is released');

    const pan = strip.locator('.strip-pan input[type=range]');
    pending = post();
    await pan.fill('0.5');
    values = await formValues(await pending);
    assert.equal(values.path, `${track}.pan`);
    assert.equal(parseFloat(values.value), 0.5);
    await until(async () => (await strip.locator('.strip-pan-readout').textContent()) === 'R50', 'the pan readout shows the side and amount');
    pending = post();
    await strip.locator('.strip-pan-reset').click();
    assert.equal(parseFloat((await formValues(await pending)).value), 0);
    await until(async () => (await strip.locator('.strip-pan-readout').textContent()) === 'C', 'reset returns the pan to centre');
    assert.deepEqual(errors, []);
  } finally {
    await close();
  }
});

test('the phone-width Mixer scrolls its strips sideways without widening the page', {timeout: 60_000}, async () => {
  const {page, errors, close} = await openMixer({width: 390, height: 844});
  try {
    const sizes = await page.evaluate(() => {
      const strips = document.querySelector('#view-panel-mixer .strips');
      return {page: document.documentElement.scrollWidth, window: document.documentElement.clientWidth, strips: strips.scrollWidth, shown: strips.clientWidth};
    });
    assert.ok(sizes.page <= sizes.window, `page width ${sizes.page} exceeds the window ${sizes.window}`);
    assert.ok(sizes.strips > sizes.shown, 'the strips scroll sideways inside their own container');
    assert.deepEqual(errors, []);
  } finally {
    await close();
  }
});
