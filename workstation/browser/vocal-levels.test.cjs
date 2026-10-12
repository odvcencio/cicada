/* Fresh Studio, --audio null, and the private seed-wave.go fixture only.
 * Set CICADA_LEVELING_URL, CICADA_LEVELING_SCORE and CICADA_LEVELING_PROOF_DIR.
 * This exercises the shipped UI and native comp/audition APIs without a mic. */
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');

async function until(fn, message) {
  const end = Date.now() + 20000;
  while (Date.now() < end) {
    if (await fn()) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  assert.fail(message);
}

test('Session levels individual vocal phrases and auditions them solo without changing the comp', { timeout: 150000 }, async t => {
  const base = process.env.CICADA_LEVELING_URL;
  const score = process.env.CICADA_LEVELING_SCORE;
  assert.ok(base, 'CICADA_LEVELING_URL is required');
  assert.equal(new URL(base).hostname, '127.0.0.1');
  assert.ok(score && path.isAbsolute(score) && score.startsWith('/tmp/'), 'use an absolute private /tmp fixture path');
  const fixtureSource = fs.readFileSync(score, 'utf8');
  assert.ok(fixtureSource.includes('// cicada-vocal-test: disposable fixture'));

  const { chromium } = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--no-sandbox'] });
  t.after(() => browser.close());
  const context = await browser.newContext({ viewport: { width: 1440, height: 1100 }, reducedMotion: 'reduce' });
  const page = await context.newPage();
  page.setDefaultTimeout(20000);
  const errors = [], checks = [];
  page.on('pageerror', error => errors.push(error.message));

  const json = async route => {
    const response = await page.request.get(base + route);
    assert.equal(response.status(), 200, route);
    return response.json();
  };
  const state = () => json('/api/state');
  const takes = () => json('/api/takes');
  const transport = () => json('/api/transport');
  const ready = async target => {
    await target.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();
    await target.locator('#cicada-workspace[aria-busy=true]').waitFor({ state: 'hidden' });
    await target.locator('#cicada-vocal-lanes[data-vocal-lanes-ready=true]').waitFor();
  };
  const gain = target => target.getByLabel('Phrase gain (dB)', { exact: true });
  const slider = target => target.getByRole('slider', { name: 'Adjust phrase gain', exact: true });
  const mode = target => target.getByLabel('Hear with', { exact: true });
  const range = async target => [
    await target.getByLabel('Range start', { exact: true }).inputValue(),
    await target.getByLabel('Range end', { exact: true }).inputValue(),
  ];
  const setRange = async (target, start, end) => {
    if (!(await target.getByLabel('Range start', { exact: true }).isVisible())) {
      await target.locator('.vocal-exact > summary').click();
    }
    await target.getByLabel('Range start', { exact: true }).fill(start);
    await target.getByLabel('Range end', { exact: true }).fill(end);
    await gain(target).focus();
    assert.deepEqual(await range(target), [start, end]);
  };
  const setGain = async (target, value) => {
    await gain(target).fill(String(value));
    await slider(target).focus();
    await until(async () => Number(await slider(target).inputValue()) === value, 'typed phrase gain did not update its slider');
  };
  const rows = segments => segments.map(segment => [segment.take, segment.startTick, segment.endTick, segment.gainDB || 0]);
  const comp = async () => (await takes()).comps.find(value => value.track === 'vox');
  const chosen = (target, take, start, end) => target.locator(`.vocal-comp-region[data-vocal-pick="${take}"][data-vocal-start="${start}"][data-vocal-end="${end}"]`);
  const stop = async () => {
    await page.getByRole('button', { name: 'Stop', exact: true }).first().click();
    await ready(page);
    await until(async () => !(await transport()).playing, 'audition did not stop');
  };
  const readRaw = raw => {
    const root = path.dirname(score);
    return raw.map(take => {
      const absolute = path.resolve(root, take.asset.path);
      assert.ok(absolute.startsWith(root + path.sep), 'retained asset escaped the private fixture');
      const sha256 = crypto.createHash('sha256').update(fs.readFileSync(absolute)).digest('hex');
      assert.equal(sha256, take.asset.sha256, 'retained asset no longer matches its original hash');
      return { id: take.id, sha256, frames: take.frames, path: take.asset.path };
    });
  };

  await page.goto(base + '/?panel=session', { waitUntil: 'domcontentloaded' });
  await ready(page);
  assert.equal((await state()).source, fixtureSource);
  const originalTakes = (await takes()).takes;
  assert.equal(originalTakes.length, 3);
  assert.equal(originalTakes.filter(take => take.timeline.startTick === 7680).length, 1);
  const full = originalTakes.filter(take => take.timeline.startTick === 0);
  assert.equal(full.length, 2);
  const first = full[0].id, second = full[1].id;
  const rawBefore = readRaw(originalTakes);
  await until(async () => await page.locator('[data-waveform-state=ready]').count() === 3, 'the private retained waveforms did not load');
  assert.equal((await json('/api/capture')).activeCapture, '');
  assert.equal((await json('/api/audio/config')).options.inputEnabled, false);

  await page.locator(`[data-vocal-take="${first}"] [data-vocal-pick]`).click();
  await setGain(page, 8.5);
  await page.getByRole('button', { name: 'Use whole take', exact: true }).click();
  await ready(page);
  await until(async () => (await comp())?.segments.length === 1, 'whole take was not committed');
  assert.deepEqual(rows((await comp()).segments), [[first, 0, 30720, 0]]);
  assert.equal(Number(await gain(page).inputValue()), 0);
  checks.push('Use whole take keeps the original recording level even when the phrase editor has a draft gain');

  await page.getByLabel('Snap', { exact: true }).selectOption('bar');
  const wave = page.locator(`[data-vocal-wave="${second}"]`);
  await wave.scrollIntoViewIfNeeded();
  const box = await wave.boundingBox();
  const y = box.y + box.height * .64;
  await page.mouse.move(box.x + box.width * .25, y);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width * .75, y, { steps: 14 });
  await page.mouse.up();
  assert.deepEqual(await range(page), ['@3.1.1', '@7.1.1']);
  const initialSlider = Number(await slider(page).inputValue());
  await slider(page).focus();
  await page.keyboard.press('ArrowLeft');
  await until(async () => Number(await gain(page).inputValue()) < initialSlider, 'keyboard slider adjustment did not change the phrase level');
  assert.equal(Number(await gain(page).inputValue()), Number(await slider(page).inputValue()));
  await gain(page).fill('');
  await gain(page).pressSequentially('-6.4', { delay: 35 });
  assert.equal(Number(await gain(page).inputValue()), -6.4, 'decimal typing was reformatted before the number was complete');
  await slider(page).focus();
  await until(async () => Number(await slider(page).inputValue()) === -6.4, 'typed decimal level did not update its slider');

  const canonical = (await state()).source;
  const historyBefore = await json('/api/history');
  await mode(page).selectOption('solo');
  await page.getByRole('button', { name: 'Hear selection', exact: true }).click();
  await ready(page);
  await until(async () => {
    const playback = await transport();
    return playback.playing && playback.listening?.name === 'Take in song' && playback.listening.mode === 'solo';
  }, 'vocal-only phrase audition did not start');
  assert.equal((await state()).source, canonical);
  assert.equal(Number(await gain(page).inputValue()), -6.4);
  assert.equal(await mode(page).inputValue(), 'solo');
  await stop();

  await setRange(page, '@3.1.1', '@4.1.1');
  await mode(page).selectOption('context');
  await page.getByRole('button', { name: 'Loop selection', exact: true }).click();
  await ready(page);
  await until(async () => {
    const playback = await transport();
    return playback.playing && playback.listening?.mode === 'context' && playback.listening.loop && playback.listening.cycles >= 2;
  }, 'song-context phrase did not loop twice');
  assert.equal(Number(await gain(page).inputValue()), -6.4);
  await stop();
  const historyAfter = await json('/api/history');
  assert.equal((await state()).source, canonical);
  assert.deepEqual(historyAfter.edits, historyBefore.edits);
  assert.equal(historyAfter.canUndo, historyBefore.canUndo);
  assert.equal(historyAfter.canRedo, historyBefore.canRedo);
  assert.deepEqual(readRaw((await takes()).takes), rawBefore);
  checks.push('vocal-only and song-context auditions use the selected level, loop, preserve the draft, and leave source, Undo history and original PCM unchanged');

  await setRange(page, '@3.1.1', '@7.1.1');
  await page.getByRole('link', { name: 'Mixer', exact: true }).click();
  await page.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();
  await page.locator('#cicada-workspace[aria-busy=true]').waitFor({ state: 'hidden' });
  await page.getByRole('link', { name: 'Session', exact: true }).click();
  await ready(page);
  assert.equal(Number(await gain(page).inputValue()), -6.4);
  assert.equal(await mode(page).inputValue(), 'context');
  assert.deepEqual(await range(page), ['@3.1.1', '@7.1.1']);
  assert.equal((await state()).source, canonical);
  checks.push('returning from another panel keeps the uncommitted phrase level, range and listening mode');

  await page.getByRole('button', { name: 'Use selection', exact: true }).click();
  await ready(page);
  await until(async () => (await comp())?.segments.length === 3, 'leveled phrase did not commit');
  const leveled = [[first, 0, 7680, 0], [second, 7680, 23040, -6.4], [first, 23040, 30720, 0]];
  assert.deepEqual(rows((await comp()).segments), leveled);
  assert.equal(Number(await chosen(page, second, 7680, 23040).getAttribute('data-vocal-gain-db')), -6.4);
  assert.ok((await chosen(page, second, 7680, 23040).innerText()).includes('-6.4 dB'));
  await setGain(page, 2.6);
  await chosen(page, second, 7680, 23040).click();
  assert.equal(Number(await gain(page).inputValue()), -6.4);
  assert.deepEqual(await range(page), ['@3.1.1', '@7.1.1']);
  checks.push('committing a leveled phrase preserves both neighboring takes at their original levels; choosing its region recalls the saved gain');

  await setGain(page, 2.6);
  await page.getByRole('button', { name: 'Undo', exact: true }).click();
  await ready(page);
  assert.deepEqual(rows((await comp()).segments), [[first, 0, 30720, 0]]);
  assert.equal(Number(await gain(page).inputValue()), 0);
  await page.getByRole('button', { name: 'Redo', exact: true }).click();
  await ready(page);
  assert.deepEqual(rows((await comp()).segments), leveled);
  assert.equal(Number(await gain(page).inputValue()), -6.4);
  await chosen(page, second, 7680, 23040).click();
  await mode(page).selectOption('solo');
  await page.locator('.vocal-setup').evaluate(element => element.open = false);
  await page.locator('.vocal-exact').evaluate(element => element.open = false);
  const proof = process.env.CICADA_LEVELING_PROOF_DIR;
  if (proof) {
    fs.mkdirSync(proof, { recursive: true });
    await page.locator('#cicada-vocal-lanes').screenshot({ path: path.join(proof, 'session-phrase-levels-1440.png') });
  }
  checks.push('Undo and Redo recall the canonical phrase level rather than a stale draft');

  await setRange(page, '@4.1.1', '@5.1.1');
  await page.getByRole('button', { name: 'Reset gain', exact: true }).click();
  assert.equal(Number(await gain(page).inputValue()), 0);
  assert.equal(Number(await slider(page).inputValue()), 0);
  await page.getByRole('button', { name: 'Use selection', exact: true }).click();
  await ready(page);
  await until(async () => (await comp())?.segments.length === 5, 'reset subphrase merged across different gain levels');
  const finalSegments = [
    [first, 0, 7680, 0],
    [second, 7680, 11520, -6.4],
    [second, 11520, 15360, 0],
    [second, 15360, 23040, -6.4],
    [first, 23040, 30720, 0],
  ];
  assert.deepEqual(rows((await comp()).segments), finalSegments);
  checks.push('resetting one subphrase keeps the surrounding portions of the same take at their existing levels');

  const mobile = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, reducedMotion: 'reduce', storageState: await context.storageState() });
  const phone = await mobile.newPage();
  phone.setDefaultTimeout(20000);
  phone.on('pageerror', error => errors.push(error.message));
  await phone.goto(base + '/?panel=session', { waitUntil: 'domcontentloaded' });
  await ready(phone);
  await until(async () => await phone.locator('[data-waveform-state=ready]').count() === 3, 'phone waveforms did not load');
  await chosen(phone, second, 7680, 11520).click();
  assert.equal(Number(await gain(phone).inputValue()), -6.4);
  await slider(phone).scrollIntoViewIfNeeded();
  const sliderBox = await slider(phone).boundingBox();
  const beforeTouch = Number(await slider(phone).inputValue());
  const cdp = await mobile.newCDPSession(phone);
  const touch = async (type, fraction) => cdp.send('Input.dispatchTouchEvent', {
    type,
    touchPoints: type === 'touchEnd' ? [] : [{ x: sliderBox.x + sliderBox.width * fraction, y: sliderBox.y + sliderBox.height / 2 }],
  });
  await touch('touchStart', .42);
  for (let step = 1; step <= 8; step++) await touch('touchMove', .42 + .02 * step);
  await touch('touchEnd', 0);
  await until(async () => Number(await slider(phone).inputValue()) !== beforeTouch, 'phone touch did not adjust phrase gain');
  assert.equal(Number(await gain(phone).inputValue()), Number(await slider(phone).inputValue()));
  assert.equal(await phone.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'phrase controls overflow the phone screen');
  await mode(phone).selectOption('solo');
  await phone.locator('.vocal-setup').evaluate(element => element.open = false);
  if (proof) await phone.locator('#cicada-vocal-lanes').screenshot({ path: path.join(proof, 'session-phrase-levels-390.png') });
  assert.deepEqual(rows((await comp()).segments), finalSegments);
  checks.push('the phone slider responds to real touch input, stays in sync with the numeric level, and fits the screen without committing a draft');

  const rawAfter = readRaw((await takes()).takes);
  assert.deepEqual(rawAfter, rawBefore);
  assert.equal((await json('/api/capture')).activeCapture, '');
  assert.equal((await json('/api/audio/config')).options.inputEnabled, false);
  assert.deepEqual(errors, []);
  if (proof) fs.writeFileSync(path.join(proof, 'checks.json'), JSON.stringify({ checks, errors, rawBefore, rawAfter, microphoneActivated: false, silentOutput: true, viewport: { desktop: [1440, 1100], mobile: [390, 844] } }, null, 2));
  await mobile.close();
  console.log(checks.join('\n'));
});
