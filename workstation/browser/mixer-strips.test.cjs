/*
 * Mixer channel strips: live meters, mute and solo, pan, the aligned strip
 * face, the tool strip, and opening a channel in Device.
 *
 * ./build/cicada studio /path/to/disposable.cicada --audio null --listen 127.0.0.1:8190
 * CICADA_STUDIO_URL=http://127.0.0.1:8190 node --test workstation/browser/mixer-strips.test.cjs
 *
 * Use a disposable score from continuity.test.cjs --write-fixture on a fresh
 * Studio process. The suite mutes, pans and restores one track, and sets and
 * undoes a bus level, the master level and a track's output in Device. It answers
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
test.after(async () => { await shared?.browser.close(); }, {timeout: 15000});

async function openMixer(viewport = {width: 1440, height: 900}) {
  const base = process.env.CICADA_STUDIO_URL;
  assert.ok(base && ['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname), 'use a local Studio');
  const page = await (await sharedContext()).newPage();
  await page.setViewportSize(viewport);
  page.setDefaultTimeout(15_000);
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  // The page's meter frame. Tests change it; a null frame serves nothing.
  const state = {peak: -12, rms: -20, ids: null, polls: 0, fail: false, noMaster: false};
  await page.route('**/api/meters', route => {
    state.polls++;
    if (state.fail) return route.abort();
    const level = {peak: state.peak, rms: state.rms};
    const tracks = {};
    for (const id of state.ids || []) tracks[id] = level;
    route.fulfill({contentType: 'application/json', body: JSON.stringify({
      type: 'meters', tick: 1, tracks, returns: {}, buses: {music: level}, ...(state.noMaster ? {} : {master: {peak: state.peak, rms: state.rms, pre_peak: state.peak, comp_gr: 0, limiter_gr: 0, over: false}}),
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
    // The hold tick stays near the loudest peak for a second after the level
    // falls. Sample it inside the page at the first frame that shows the fall,
    // so a slow CI runner cannot push the check past the hold time. If the page
    // itself stalled for over a second, raise the peak again and retry.
    const sampleHold = () => page.evaluate(() => new Promise(resolve => {
      const meterEl = document.querySelector('#view-panel-mixer .strip[data-mixer-strip=track] .strip-meter');
      const end = Date.now() + 10_000;
      const timer = setInterval(() => {
        const peak = parseFloat(meterEl.style.getPropertyValue('--peak'));
        if (Math.abs(peak - 30 / 66) < 0.01 || Date.now() > end) {
          clearInterval(timer);
          resolve({peak, hold: parseFloat(meterEl.style.getPropertyValue('--hold'))});
        }
      }, 10);
    }));
    let held;
    for (let attempt = 0; attempt < 5 && !(held?.hold > 0.8); attempt++) {
      state.peak = 0;
      await until(async () => parseFloat(await css('--hold')) > 0.9, 'the hold tick rises to the clipped peak');
      state.peak = -30;
      held = await sampleHold();
      assert.ok(Math.abs(held.peak - 30 / 66) < 0.01, 'the peak falls back');
    }
    assert.ok(held.hold > 0.8, `the hold tick stays high for a moment, was ${held.hold}`);

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

test('meters read no signal after three failed polls and recover; a missing master is silent', {timeout: 90_000}, async () => {
  const {page, errors, state, close} = await openMixer();
  try {
    const strip = page.locator('#view-panel-mixer .strip[data-mixer-strip=track]').first();
    const meter = strip.locator('.strip-meter');
    const master = page.locator('#view-panel-mixer .strip[data-mixer-strip=master] .strip-meter');
    const name = await meter.getAttribute('data-meter-name');
    state.ids = [(await meter.getAttribute('data-meter')).replace(/^track:/, '')];
    state.peak = 0;
    await until(async () => (await meter.getAttribute('data-clip')) === 'true', 'the clip lamp lights');
    // The label changes at most twice a second, so wait for it before failing the polls.
    await until(async () => /clipping/.test(await meter.getAttribute('aria-label')), 'the label names the clip');
    state.fail = true;
    await until(async () => (await meter.getAttribute('aria-label')) === `${name} meter, no signal`, 'failed polls read no signal');
    assert.equal(await meter.getAttribute('data-clip'), 'false', 'the clip lamp clears');
    assert.equal(parseFloat(await meter.evaluate(node => node.style.getPropertyValue('--peak'))), 0);
    state.fail = false;
    state.peak = -12;
    await until(async () => /peak -12\.0 dBFS/.test(await meter.getAttribute('aria-label')), 'meters resume on the next good frame');
    // A frame without a master reads as silence, not as 0 dBFS.
    state.noMaster = true;
    await until(async () => (await master.getAttribute('aria-label')) === 'Master meter, no signal', 'a missing master is silent');
    assert.equal(await master.getAttribute('data-clip'), 'false');
    assert.deepEqual(errors, []);
  } finally {
    await close();
  }
});

test('M posts the mute path and value, Undo-able; the pan slider saves and resets', {timeout: 90_000}, async () => {
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

test('arrow keys on a pan slider save once, after the last key press', {timeout: 90_000}, async () => {
  const {page, errors, close} = await openMixer();
  try {
    const strip = page.locator('#view-panel-mixer .strip[data-mixer-strip=track]').first();
    const pan = strip.locator('.strip-pan input[type=range]');
    const readout = strip.locator('.strip-pan-readout');
    assert.equal(await readout.textContent(), 'C', 'the suite starts with the pan centred');
    const posts = [];
    page.on('request', request => {
      if (request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/mixer') posts.push(request);
    });
    await pan.focus();
    for (let i = 0; i < 5; i++) await page.keyboard.press('ArrowRight');
    // The readout and the spoken value follow the thumb before any save.
    const shown = await strip.evaluate(node => ({
      readout: node.querySelector('.strip-pan-readout').textContent,
      spoken: node.querySelector('.strip-pan input[type=range]').getAttribute('aria-valuetext'),
    }));
    assert.equal(shown.readout, 'R5');
    assert.equal(shown.spoken, '5% right');
    await until(() => posts.length > 0, 'the pan is saved after the keys stop');
    // Wait past the settle time so a stray second save would show up.
    await new Promise(resolve => setTimeout(resolve, 1200));
    assert.equal(posts.length, 1, `five key presses made ${posts.length} saves`);
    const values = await formValues(posts[0]);
    assert.match(values.path, /\.pan$/);
    assert.ok(Math.abs(parseFloat(values.value) - 0.05) < 1e-9, `saved pan ${values.value}`);
    await until(async () => (await pan.inputValue()) === '0.05' && (await readout.textContent()) === 'R5', 'the saved pan is shown after the page updates');
    // Record where keyboard focus is after the save re-renders the page.
    const focus = await page.evaluate(() => {
      const active = document.activeElement;
      return {tag: active?.tagName, label: active?.getAttribute?.('aria-label') ?? null, body: active === document.body};
    });
    console.log(`focus after the pan save: ${JSON.stringify(focus)}`);
    const reset = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/mixer');
    await strip.locator('.strip-pan-reset').click();
    await reset;
    await until(async () => (await readout.textContent()) === 'C', 'the pan returns to centre');
    assert.deepEqual(errors, []);
  } finally {
    await close();
  }
});

test('M on the Arrange rail saves the mute and shows it pressed after the update', {timeout: 90_000}, async () => {
  const {page, errors, close} = await openMixer();
  try {
    await page.locator('#view-tab-arrange').click();
    const first = page.locator('#view-panel-arrange .rail-toggles button[data-toggle=mute]').first();
    await first.waitFor();
    assert.equal(await first.getAttribute('aria-pressed'), 'false');
    const post = () => page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/mixer');
    let pending = post();
    await first.click();
    let values = await formValues(await pending);
    assert.match(values.path, /\.mute$/);
    assert.equal(values.value, 'true');
    const railButton = page.locator(`#view-panel-arrange form:has(input[name=path][value="${values.path}"]) button[data-toggle=mute]`);
    await until(async () => (await railButton.getAttribute('aria-pressed')) === 'true', 'the rail button shows the saved mute');
    // The Mixer strip for the same track agrees.
    const stripButton = page.locator(`#view-panel-mixer form:has(input[name=path][value="${values.path}"]) button[data-toggle=mute]`);
    assert.equal(await stripButton.getAttribute('aria-pressed'), 'true');
    // Restore the score for the other tests.
    pending = post();
    await railButton.click();
    assert.equal((await formValues(await pending)).value, 'false');
    await until(async () => (await railButton.getAttribute('aria-pressed')) === 'false', 'the rail button is released');
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
    // The master stays at the right edge while the other strips scroll, and
    // at the end of the scroll it does not cover the last of them.
    const pinned = await page.evaluate(async () => {
      const strips = document.querySelector('#view-panel-mixer .strips');
      const master = strips.querySelector('.strip[data-mixer-strip=master]');
      const others = [...strips.querySelectorAll('.strip:not([data-mixer-strip=master])')];
      const box = node => node.getBoundingClientRect();
      const start = {master: box(master).right, edge: box(strips).right, covered: box(others.at(-1)).right > box(master).left};
      strips.scrollLeft = strips.scrollWidth;
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      return {start, scrolled: strips.scrollLeft, end: {master: box(master).right, masterLeft: box(master).left, last: box(others.at(-1)).right, edge: box(strips).right}};
    });
    assert.ok(Math.abs(pinned.start.master - pinned.start.edge) <= 1, `the master sits at the right edge before scrolling: ${JSON.stringify(pinned.start)}`);
    assert.ok(pinned.start.covered, 'the fixture has more strips than fit beside the master');
    assert.ok(pinned.scrolled > 0, 'the strips scrolled');
    assert.ok(Math.abs(pinned.end.master - pinned.end.edge) <= 1, 'the master stays at the right edge after scrolling');
    assert.ok(pinned.end.last <= pinned.end.masterLeft + 1, `scrolled to the end, the master does not cover the last strip: ${JSON.stringify(pinned.end)}`);
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), 'scrolling the strips does not widen the page');
    // On a phone Device is in the Edit region, so a strip's name goes there.
    await page.getByRole('link', {name: 'Open music in Device', exact: true}).click();
    await page.locator('#studio-shell[data-phone-region=detail]').waitFor();
    await page.locator('#device-editor .device-groups[data-device-channel="bus:music"]').waitFor();
    assert.ok(await page.locator('#detail-panel-device').isVisible(), 'Device shows the bus in the Edit region');
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), 'Device does not widen the page');
    assert.deepEqual(errors, []);
  } finally {
    await close();
  }
});

test('each strip shows only direct controls, in rows that line up, with the master last', {timeout: 60_000}, async () => {
  const {page, errors, close} = await openMixer();
  try {
    const face = await page.evaluate(() => {
      const panel = document.querySelector('#view-panel-mixer');
      const strips = [...panel.querySelectorAll('.strip')];
      const rows = ['.strip-name', '.strip-kind', '.strip-inserts', '.strip-sends', '.strip-toggles', '.strip-pan', '.strip-channel'];
      const box = node => node.getBoundingClientRect();
      return {
        kinds: strips.map(strip => [strip.dataset.mixerStrip, strip.querySelector('.strip-kind').textContent]),
        names: strips.map(strip => strip.querySelector('.strip-name a')?.getAttribute('aria-label') || ''),
        // What left the face: Set forms, the out select, effect fields and chains.
        leftovers: panel.querySelectorAll('.strips .mixer-control, .strips .effect-chain, .strips select, .strips input[type=number], .strips input[type=text]').length,
        refusal: panel.innerHTML.includes('CICADA-UNSUPPORTED') || panel.innerText.includes('not implemented'),
        rows: rows.map(row => strips.map(strip => { const cell = strip.querySelector(':scope > ' + row); return cell ? [Math.round(box(cell).top), Math.round(box(cell).height)] : null; })),
        edges: strips.map(strip => [Math.round(box(strip).left), Math.round(box(strip).right)]),
        fits: panel.querySelector('.strips').scrollWidth <= panel.querySelector('.strips').clientWidth,
        selected: strips.filter(strip => strip.dataset.mixerSelected === 'true').map(strip => strip.dataset.mixerStrip),
      };
    });
    assert.equal(face.leftovers, 0, 'no exact-value form, select or chain editor is on a strip face');
    assert.equal(face.refusal, false, 'the Mixer view prints no raw refusal');
    assert.deepEqual(face.kinds[0], ['track', 'acid'], 'a track names its instrument kind');
    assert.ok(face.kinds.some(([kind, label]) => kind === 'bus' && label === 'Bus'), 'a bus says it is a bus');
    assert.deepEqual(face.kinds.at(-1), ['master', 'Master'], 'the master is the last strip');
    for (const name of face.names) assert.match(name, /^Open .+ in Device$/, 'each strip name opens its channel in Device');
    for (const cells of face.rows) {
      assert.ok(cells.every(Boolean), 'every strip has every row');
      assert.equal(new Set(cells.map(([top]) => top)).size, 1, `a row starts at one height on every strip: ${JSON.stringify(cells)}`);
      assert.equal(new Set(cells.map(([, height]) => height)).size, 1, `a row is as tall on every strip: ${JSON.stringify(cells)}`);
    }
    // With room for every strip the pinned master sits after the last one, not over it.
    assert.ok(face.fits, 'the fixture strips fit at 1440 px');
    for (let i = 1; i < face.edges.length; i++) assert.ok(face.edges[i][0] >= face.edges[i - 1][1], `strip ${i} overlaps the one before: ${JSON.stringify(face.edges)}`);
    assert.deepEqual(face.selected, ['track'], 'Device shows the first track by default, and its strip is marked');
    assert.deepEqual(errors, []);
  } finally {
    await close();
  }
});

test('Show and Meter change the view in the tab without a request, and a save keeps the choice', {timeout: 90_000}, async () => {
  const {page, errors, close} = await openMixer();
  try {
    const shown = () => page.evaluate(() => [...document.querySelectorAll('#view-panel-mixer .strip')].filter(strip => strip.getClientRects().length).map(strip => strip.dataset.mixerStrip));
    const all = await shown();
    assert.ok(all.includes('track') && all.includes('bus') && all.at(-1) === 'master', `the fixture shows tracks, buses and the master: ${all}`);
    // Requests that would read or change the page; the engines' status polls are not counted.
    const requests = [];
    page.on('request', request => {
      const url = new URL(request.url());
      if (request.method() !== 'GET' || !url.pathname.startsWith('/api/')) requests.push(`${request.method()} ${url.pathname}`);
    });
    const show = page.locator('#view-panel-mixer [data-mixer-show]');
    await show.selectOption('tracks');
    assert.deepEqual([...new Set(await shown())], ['track', 'master'], 'Tracks hides buses and returns and keeps the master');
    await show.selectOption('buses');
    assert.ok((await shown()).every(kind => kind !== 'track') && (await shown()).includes('bus') && (await shown()).at(-1) === 'master', 'Buses and returns hides the tracks and keeps the master');
    // Meter picks the bars: peak only, RMS only, or both.
    const bars = () => page.evaluate(() => {
      const meter = document.querySelector('#view-panel-mixer .strip[data-mixer-strip=bus] .strip-meter');
      return Object.fromEntries(['peak', 'rms', 'hold', 'clip'].map(bar => [bar, getComputedStyle(meter.querySelector('.strip-meter__' + bar)).display !== 'none']));
    });
    const meterMode = page.locator('#view-panel-mixer [data-mixer-meter]');
    assert.deepEqual(await bars(), {peak: true, rms: true, hold: true, clip: true});
    await meterMode.selectOption('peak');
    assert.deepEqual(await bars(), {peak: true, rms: false, hold: true, clip: true});
    await meterMode.selectOption('rms');
    assert.deepEqual(await bars(), {peak: false, rms: true, hold: false, clip: true});
    assert.deepEqual(requests, [], 'the view controls send nothing');
    assert.equal(await page.locator('#studio-shell [data-edited]').count(), 0, 'a view choice is not unsaved work');

    // A save from a strip updates the page in place; the choices stay.
    const mute = page.locator('#view-panel-mixer .strip[data-mixer-strip=bus] button[data-toggle=mute]').first();
    const post = () => page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/mixer');
    let pending = post();
    await mute.click();
    assert.equal((await formValues(await pending)).value, 'true');
    await until(async () => (await mute.getAttribute('aria-pressed')) === 'true', 'the bus mute is saved');
    assert.equal(await show.inputValue(), 'buses', 'the Show choice survives a save');
    assert.equal(await meterMode.inputValue(), 'rms', 'the Meter choice survives a save');
    assert.ok((await shown()).every(kind => kind !== 'track'), 'the tracks stay hidden after a save');
    pending = post();
    await mute.click();
    assert.equal((await formValues(await pending)).value, 'false');
    await until(async () => (await mute.getAttribute('aria-pressed')) === 'false', 'the bus mute is restored');

    await meterMode.selectOption('both');
    await show.selectOption('all');
    assert.deepEqual(await shown(), all, 'All channels restores every strip');
    assert.deepEqual(errors, []);
  } finally {
    await close();
  }
});

test('Reset peaks clears the hold tick and the clip lamp at once', {timeout: 90_000}, async () => {
  const {page, errors, state, close} = await openMixer();
  try {
    const meter = page.locator('#view-panel-mixer .strip[data-mixer-strip=track]').first().locator('.strip-meter');
    state.ids = [(await meter.getAttribute('data-meter')).replace(/^track:/, '')];
    const css = name => meter.evaluate((node, prop) => node.style.getPropertyValue(prop), name);
    // Without a reset the lamp stays lit for three seconds after a clip and the
    // tick holds for one. Press Reset peaks at the first frame that shows the
    // level fall, and time the clear inside the page. If the page stalled long
    // enough for the holds to run out by themselves, clip again and retry.
    let cleared;
    for (let attempt = 0; attempt < 5 && !(cleared?.after < 700); attempt++) {
      state.peak = 0;
      await until(async () => (await meter.getAttribute('data-clip')) === 'true' && parseFloat(await css('--hold')) > 0.9, 'the meter clips and holds the peak');
      state.peak = -30;
      cleared = await page.evaluate(() => new Promise(resolve => {
        const meterEl = document.querySelector('#view-panel-mixer .strip[data-mixer-strip=track] .strip-meter');
        const end = Date.now() + 10_000;
        let fell = 0;
        const timer = setInterval(() => {
          const peak = parseFloat(meterEl.style.getPropertyValue('--peak'));
          if (!fell && Math.abs(peak - 30 / 66) < 0.01) {
            fell = performance.now();
            document.querySelector('#view-panel-mixer [data-mixer-reset-peaks]').click();
          }
          if (fell && meterEl.getAttribute('data-clip') === 'false' || Date.now() > end) {
            clearInterval(timer);
            resolve({clip: meterEl.getAttribute('data-clip'), hold: parseFloat(meterEl.style.getPropertyValue('--hold')), label: meterEl.getAttribute('aria-label'), after: fell ? performance.now() - fell : Infinity});
          }
        }, 10);
      }));
    }
    assert.ok(cleared.after < 700, `Reset peaks should clear the lamp well inside its three seconds, took ${cleared.after} ms`);
    assert.equal(cleared.clip, 'false');
    assert.ok(Math.abs(cleared.hold - 30 / 66) < 0.01, `the hold tick drops to the level now, was ${cleared.hold}`);
    assert.ok(!/clipping/.test(cleared.label), `the label drops the clip: ${cleared.label}`);
    // A channel that still clips lights again on the next frame.
    state.peak = 0;
    await until(async () => (await meter.getAttribute('data-clip')) === 'true', 'the lamp lights again');
    assert.deepEqual(errors, []);
  } finally {
    await close();
  }
});

test('a strip name opens the bus, the master and a track in Device, where exact values save and undo', {timeout: 120_000}, async () => {
  const {page, errors, close} = await openMixer();
  try {
    const source = async () => (await (await page.request.get(new URL('/api/state', process.env.CICADA_STUDIO_URL).href)).json()).source;
    const original = await source();
    const post = () => page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/__actions/mixer');
    const device = channel => page.locator(`#device-editor .device-groups[data-device-channel="${channel}"]`);
    const control = path => page.locator('#device-editor form').filter({has: page.locator(`input[name=path][value="${path}"]`)});
    const marks = () => page.evaluate(() => ({
      strips: [...document.querySelectorAll('#view-panel-mixer .strip[data-mixer-selected=true]')].map(strip => strip.dataset.mixerChannel),
      links: [...document.querySelectorAll('#view-panel-mixer .strip a[aria-current=true]')].map(link => link.dataset.mixerSelect),
    }));
    // A save or an Undo updates the page in place; its forms then carry the
    // new revision. Wait for that before the next edit, or it would be stale.
    const revision = () => page.locator('#undo-form input[name=revision]').inputValue();
    const settled = before => until(async () => (await revision()) !== before, 'the page shows the saved revision');
    const undo = async message => {
      const before = await revision();
      await page.locator('#undo-form button').click();
      await until(async () => (await source()) === original, message);
      await settled(before);
    };
    // The dock shows Clip until a strip is chosen.
    assert.equal(await page.locator('#detail-tab-device').getAttribute('aria-selected'), 'false');

    // A bus.
    const busName = page.getByRole('link', {name: 'Open music in Device', exact: true});
    await busName.focus();
    await page.keyboard.press('Enter');
    await page.locator('#detail-tab-device[aria-selected="true"]').waitFor();
    await device('bus:music').waitFor();
    assert.ok(await page.locator('#detail-panel-device').isVisible(), 'Device shows after choosing a strip');
    assert.deepEqual(await marks(), {strips: ['bus:music'], links: ['bus:music']}, 'only the bus strip is marked as selected');
    assert.match(await page.locator('#detail-tabs [data-dock-selection=track]').innerText(), /music\s*·\s*Bus/, 'the dock header names the bus');
    // Routing and pan on a bus: not available, with the reason in plain words.
    const pan = page.locator('#device-editor .mixer-unavailable').filter({hasText: /^\s*pan/});
    assert.match(await pan.innerText(), /pan\s+Not available\s+Bus music pan is not implemented\./);
    assert.equal(await pan.getAttribute('data-reason'), 'CICADA-UNSUPPORTED: bus music pan is not implemented');
    assert.match(await page.locator('#device-editor .mixer-unavailable').filter({hasText: /^\s*out/}).innerText(), /out\s+Not available\s+Bus music output routing is not implemented\./);
    assert.ok(!(await page.locator('#device-editor').innerText()).includes('CICADA-UNSUPPORTED'), 'Device does not print the refusal code');
    // Exact level: the bus takes "-3dB" or "off". Its fader on the strip is fixed.
    assert.ok(await page.locator('#view-panel-mixer .strip[data-mixer-channel="bus:music"] .strip-fader input[type=range]').isDisabled(), 'a level the service takes only as listed values has a fixed fader');
    let before = await revision();
    let pending = post();
    await control('music.level').locator('[name=value]').selectOption('off');
    await control('music.level').getByRole('button', {name: 'Set', exact: true}).click();
    let values = await formValues(await pending);
    assert.equal(values.path, 'music.level');
    assert.equal(values.value, 'off');
    assert.equal(values.valueType, 'text', 'the bus level posts the value as the score writes it');
    await until(async () => /bus music \{[^}]*level = off/.test(await source()), 'the bus level is saved to the score');
    await settled(before);
    assert.equal(await control('music.level').locator('[name=value]').inputValue(), 'off', 'Device shows the saved bus level');
    assert.ok(await device('bus:music').isVisible(), 'Device stays on the bus after its own save');
    assert.deepEqual(await marks(), {strips: ['bus:music'], links: ['bus:music']});
    await undo('one Undo restores the bus level');

    // A direct control on another strip saves and leaves Device on the bus.
    const trackMute = page.locator('#view-panel-mixer .strip[data-mixer-strip=track] button[data-toggle=mute]').first();
    before = await revision();
    pending = post();
    await trackMute.click();
    await pending;
    await until(async () => (await trackMute.getAttribute('aria-pressed')) === 'true', 'the track mute is saved');
    await settled(before);
    assert.ok(await device('bus:music').isVisible(), 'a strip gesture does not move Device to another channel');
    assert.deepEqual(await marks(), {strips: ['bus:music'], links: ['bus:music']});
    await undo('one Undo restores the track mute');
    await until(async () => (await trackMute.getAttribute('aria-pressed')) === 'false', 'the track mute is released');

    // The master: an exact level in dB.
    await page.getByRole('link', {name: 'Open Master in Device', exact: true}).click();
    await device('master:master').waitFor();
    // The strip keeps its full reference; its link uses the master's short address.
    assert.deepEqual(await marks(), {strips: ['master:master'], links: ['master']});
    assert.equal(await control('master.level').locator('[name=value]').getAttribute('type'), 'number');
    assert.equal(await control('master.level').locator('.field span').innerText(), 'level (dB)');
    assert.match(await page.locator('#device-editor .mixer-unavailable').filter({hasText: /^\s*out/}).innerText(), /Not available/);
    before = await revision();
    pending = post();
    await control('master.level').locator('[name=value]').fill('-2.5');
    await control('master.level').getByRole('button', {name: 'Set', exact: true}).click();
    values = await formValues(await pending);
    assert.equal(values.path, 'master.level');
    assert.equal(values.value, '-2.5');
    await until(async () => (await page.locator('#view-panel-mixer .strip[data-mixer-strip=master] .strip-readout').innerText()).startsWith('-2.5'), 'the master fader readout follows the exact level');
    assert.notEqual(await source(), original);
    await settled(before);
    await undo('one Undo restores the master level');

    // A track: its output routing.
    const trackName = page.locator('#view-panel-mixer .strip[data-mixer-strip=track] .strip-name a').first();
    const track = await trackName.getAttribute('data-mixer-select');
    await trackName.click();
    await device(track).waitFor();
    assert.equal(await device(track).getAttribute('data-device-track'), track);
    for (const path of [`${track}.level`, `${track}.pan`, `${track}.out`]) assert.equal(await control(path).count(), 1, `Device holds the ${path} field`);
    // A track's exact level is a text field, so it also takes "off".
    assert.match(await control(`${track}.level`).locator('.field span').innerText(), /off or -60 to 6/);
    assert.equal(await control(`${track}.level`).locator('[name=value]').getAttribute('type'), null);
    before = await revision();
    pending = post();
    await control(`${track}.out`).locator('[name=value]').selectOption('sfx');
    await control(`${track}.out`).getByRole('button', {name: 'Set', exact: true}).click();
    values = await formValues(await pending);
    assert.equal(values.path, `${track}.out`);
    assert.equal(values.value, 'sfx');
    await until(async () => /out = sfx/.test(await source()), 'the track output is saved to the score');
    await settled(before);
    await undo('one Undo restores the track output');
    assert.deepEqual(errors, []);
  } finally {
    await close();
  }
});
