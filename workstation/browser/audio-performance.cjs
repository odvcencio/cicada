'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

async function checkAudioPerformance({browser, base, proof, ready, errors}) {
  const storage = process.env.CICADA_AUDIO_PERFORMANCE_STORAGE;
  if (storage) assert.ok(path.isAbsolute(storage) && storage.startsWith('/tmp/'), 'Save Studio identity only in a private /tmp file.');
  const desktop = await browser.newContext({viewport: {width: 1440, height: 1100}, reducedMotion: 'reduce', ...(storage && fs.existsSync(storage) ? {storageState: storage} : {})});
  let phone;
  try {
    const page = await desktop.newPage();
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(base + '/?panel=audio&returnTo=session&vocalTrack=vox', {waitUntil: 'domcontentloaded'});
    await ready(page);
    const buffer = page.getByRole('combobox', {name: 'Buffer size', exact: true});
    assert.deepEqual(await buffer.locator('option').evaluateAll(options => options.map(option => Number(option.value))), [64, 128, 256, 512, 1024]);
    const before = await (await page.request.get(base + '/api/audio/config')).json();
    assert.equal(before.inventory.backendName, 'null', 'This proof must use silent audio and never open hardware endpoints.');
    await page.getByRole('checkbox', {name: 'Enable input', exact: true}).uncheck();
    await page.getByText('Recording timing', {exact: true}).click();
    await page.getByRole('spinbutton', {name: 'Recording timing correction (ms)', exact: true}).fill('17.5');
    await buffer.selectOption('64');
    await page.getByRole('button', {name: 'Apply and return to Session', exact: true}).click();
    await page.locator('#session').waitFor();
    await ready(page);
    const after = await (await page.request.get(base + '/api/audio/config')).json();
    assert.equal(after.options.periodFrames, 64);
    assert.equal(after.options.recordingOffsetMS, 17.5);
    assert.equal(after.options.inputEnabled, false);
    assert.equal(after.options.monitorMuted, before.options.monitorMuted);
    assert.equal(after.options.monitorGain, before.options.monitorGain);
    assert.equal(after.options.monitorMode, before.options.monitorMode);

    await page.goto(base + '/?panel=audio', {waitUntil: 'domcontentloaded'});
    await ready(page);
    assert.equal(await page.getByRole('combobox', {name: 'Buffer size', exact: true}).inputValue(), '64');
    assert.match(await page.locator('[data-audio-value=requested]').innerText(), /64 frames/);
    const requests = [];
    page.on('request', request => {
      const url = new URL(request.url());
      if (url.pathname === '/api/audio/config') requests.push(url.searchParams.get('runtime'));
    });
    await page.getByRole('button', {name: 'Play', exact: true}).click();
    await page.locator('[data-audio-value=granted]').filter({hasText: '64 frames'}).waitFor();
    assert.equal(await page.getByRole('combobox', {name: 'Buffer size', exact: true}).isDisabled(), true);
    assert.match(await page.locator('[data-audio-runtime]').innerText(), /hardware loopback measurement/);
    await page.waitForTimeout(1200);
    assert.ok(requests.filter(value => value === '1').length >= 2, 'Native status did not refresh.');
    assert.equal(requests.filter(value => value !== '1').length, 0, 'Status polling repeatedly enumerated devices.');
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await page.screenshot({path: path.join(proof, 'audio-performance-1440.png'), fullPage: true});
    await page.getByRole('button', {name: 'Stop', exact: true}).click();
    await page.getByRole('combobox', {name: 'Buffer size', exact: true}).waitFor();
    await ready(page);

    // Keep the original authorized Studio identity for the narrow viewport.
    phone = await browser.newContext({viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true, reducedMotion: 'reduce', storageState: await desktop.storageState()});
    const mobile = await phone.newPage();
    mobile.on('pageerror', error => errors.push(error.message));
    await mobile.goto(base + '/?panel=audio', {waitUntil: 'domcontentloaded'});
    await ready(mobile);
    assert.equal(await mobile.getByRole('combobox', {name: 'Buffer size', exact: true}).inputValue(), '64');
    await mobile.getByRole('button', {name: 'Play', exact: true}).click();
    await mobile.locator('[data-audio-value=granted]').filter({hasText: '64 frames'}).waitFor();
    assert.equal(await mobile.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await mobile.screenshot({path: path.join(proof, 'audio-performance-390.png'), fullPage: true});
    await mobile.getByRole('button', {name: 'Stop', exact: true}).click();
    const final = await (await mobile.request.get(base + '/api/audio/config?runtime=1')).json();
    assert.equal(final.options.periodFrames, 64);
    assert.equal(final.options.recordingOffsetMS, 17.5);
    assert.equal(final.options.inputEnabled, false);
    assert.equal(final.runtime.armed, false);
  } finally {
    if (storage) await desktop.storageState({path: storage});
    if (process.env.CICADA_PRODUCER_STORAGE) await desktop.storageState({path: process.env.CICADA_PRODUCER_STORAGE});
    if (phone) await phone.close();
    await desktop.close();
  }
}

module.exports = {checkAudioPerformance};
