'use strict';
// Keyboard scene launch (F1 to F8) must keep working after the user moves between views.
// Needs a fresh Studio on a continuity.test.cjs fixture (scenes "main" and "alternate"), CICADA_STUDIO_URL.
// The engine finds the launch button by its form's action. Managed navigation can rewrite that
// attribute to an absolute URL, which a selector on the literal path misses.
const test = require('node:test');
const assert = require('node:assert/strict');
const stall = require('./page-stall.cjs');

test('F-key scene launch works on first load and after moving between views', {timeout: 120000}, async t => {
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.equal(base.hostname, '127.0.0.1');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--no-sandbox']});
  const watches = [];
  stall.teardown(t, browser, {watch: watches, exitWhenDone: true});
  const context = await browser.newContext({viewport: {width: 1440, height: 1000}, reducedMotion: 'reduce'});
  watches.push(await stall.watchContext(context, {serverLog: process.env.CICADA_STUDIO_STARTUP_LOG}));
  const page = await context.newPage(), errors = [], launches = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('response', async response => {
    if (response.request().method() === 'POST' && new URL(response.url()).pathname === '/__actions/launch') launches.push({body: response.request().postData() || '', status: response.status(), text: (await response.text().catch(() => '')).slice(0, 300)});
  });
  const ready = () => page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
  const until = async (check, label) => {
    for (const end = Date.now() + 15000; Date.now() < end;) { if (await check()) return; await new Promise(resolve => setTimeout(resolve, 50)); }
    assert.fail(label);
  };
  const launch = async (key, scene, label) => {
    const before = launches.length;
    await page.locator('#studio-shell').click({position: {x: 2, y: 2}});
    await page.keyboard.press(key);
    await until(() => launches.length > before, `${label}: ${key} sent no scene launch`);
    assert.match(launches[before].body, new RegExp(scene), `${label}: ${key} launched the wrong scene`);
    assert.equal(launches[before].status, 200, `${label}: ${launches[before].text}`);
  };

  await page.goto(new URL('/studio?tab=session', base).href);
  await ready();
  // The server refuses a launch while stopped ("press Play before launching a scene").
  await page.locator('#transport-form button[value=play]').click();
  await until(async () => (await (await page.request.get(new URL('/api/transport', base).href)).json()).playing, 'playback did not start');
  await launch('F2', 'alternate', 'first load');
  await launch('F1', 'main', 'first load');
  // Leave the Session view and come back by client-side navigation, as a user does.
  // F-keys do nothing while Arrange or Mixer is showing (observed 2026-11-10; whether that is
  // intended is an open question), so the launch is checked on returning to Session.
  for (const view of ['arrange', 'mixer']) {
    await page.locator(`#view-tab-${view}`).click();
    await page.locator(`#view-panel-${view}:not([hidden])`).waitFor();
    await page.locator('#view-tab-session').click();
    await page.locator('#view-panel-session:not([hidden])').waitFor();
    await ready();
    await launch('F2', 'alternate', `back from ${view}`);
    await launch('F1', 'main', `back from ${view}`);
  }
  assert.deepEqual(errors, []);
});
