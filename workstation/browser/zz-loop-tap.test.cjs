'use strict';
// Scratch reproduction (not committed): the mobile steps of placements.test.cjs
// lines 266-271, repeated, with an event log of each Loop in song tap.
const test = require('node:test');
const path = require('node:path');
const {createRequire} = require('node:module');

function playwright() {
  const location = process.env.CICADA_PLAYWRIGHT_MODULE;
  if (!location) return require('playwright');
  try { const loaded = require(path.resolve(location)); if (loaded.chromium) return loaded; } catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw error; }
  return createRequire(path.resolve(location))('playwright');
}

test('Loop in song tap reproduction', {timeout: 280000}, async () => {
  const base = process.env.CICADA_STUDIO_URL;
  const N = Number(process.env.REPRO_N || 30);
  const browser = await playwright().chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--no-sandbox']});
  const owner = await browser.newContext({viewport: {width: 1440, height: 1000}, reducedMotion: 'reduce'});
  const desk = await owner.newPage();
  await desk.goto(new URL('/?panel=session&placement=far-in', base).href);
  await desk.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();
  const transport = async () => (await desk.request.get(new URL('/api/transport', base).href)).json();
  const failures = [];
  for (let i = 0; i < N; i++) {
    const mobileContext = await browser.newContext({viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true, deviceScaleFactor: 1, reducedMotion: 'reduce', storageState: await owner.storageState()});
    await mobileContext.addInitScript(() => {
      window.__tapLog = [];
      const t0 = performance.now();
      const describe = el => el && el.tagName ? `${el.tagName}${el.getAttribute && el.getAttribute('value') ? '[' + el.getAttribute('value') + ']' : ''}:${(el.textContent || '').trim().slice(0, 18)}` : String(el);
      const log = (type, e) => {
        const ws = document.querySelector('#cicada-workspace');
        window.__tapLog.push({type, t: Math.round(performance.now() - t0), target: describe(e.target), connected: !!(e.target && e.target.isConnected), busy: ws && ws.getAttribute('aria-busy'), prevented: e.defaultPrevented});
      };
      for (const type of ['touchstart', 'touchend', 'click', 'submit']) {
        window.addEventListener(type, e => log(type + ':capture', e), true);
        window.addEventListener(type, e => log(type + ':bubble', e), false);
      }
    });
    const mobile = await mobileContext.newPage();
    const requests = [];
    mobile.on('request', r => { const p = new URL(r.url()).pathname; if (r.method() === 'POST' || p.startsWith('/__workspace')) requests.push({t: new Date().toISOString().slice(17, 23), m: r.method(), p, body: (r.postData() || '').slice(0, 100)}); });
    await mobile.goto(new URL('/?panel=session&placement=far-in', base).href, {waitUntil: 'domcontentloaded'});
    await mobile.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();
    await mobile.getByRole('link', {name: 'Edit placement far-in', exact: true}).tap();
    await mobile.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();
    const tapAt = Date.now();
    await mobile.locator('[data-placement-inspector="far-in"]').getByRole('button', {name: 'Loop in song', exact: true}).tap();
    let started = false;
    for (const end = Date.now() + 5000; Date.now() < end;) {
      if ((await transport()).listening?.mode === 'context') { started = true; break; }
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    if (!started) {
      const status = await mobile.evaluate(() => [...document.querySelectorAll('[data-workspace-status]')].map(n => n.textContent.trim()).join(' | '));
      failures.push({i, tapAfterEditMs: Date.now() - tapAt, status, requests, tapLog: await mobile.evaluate(() => window.__tapLog.filter(e => !e.type.startsWith('touch') || e.t > 0))});
    } else {
      await mobile.getByRole('button', {name: 'Stop listening', exact: true}).tap();
      for (const end = Date.now() + 5000; Date.now() < end;) { if (!(await transport()).playing) break; await new Promise(resolve => setTimeout(resolve, 50)); }
    }
    await mobileContext.close();
  }
  console.log(`REPRO failed ${failures.length} of ${N}`);
  for (const failure of failures) console.log('REPRO ' + JSON.stringify(failure));
  await browser.close();
});
