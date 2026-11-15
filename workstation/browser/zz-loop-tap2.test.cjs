'use strict';
// Scratch reproduction (not committed). A Loop in song tap aimed where the
// button is while the "Edit placement" read is still out; the read is then
// applied before the touch lands, as can happen between Playwright's hit
// check and its touch. Control: the same with no read.
const test = require('node:test');
const path = require('node:path');
const {createRequire} = require('node:module');

function playwright() {
  const location = process.env.CICADA_PLAYWRIGHT_MODULE;
  if (!location) return require('playwright');
  try { const loaded = require(path.resolve(location)); if (loaded.chromium) return loaded; } catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw error; }
  return createRequire(path.resolve(location))('playwright');
}

test('Loop in song tap aimed before a workspace read is applied', {timeout: 280000}, async () => {
  const base = process.env.CICADA_STUDIO_URL;
  const N = Number(process.env.REPRO_N || 5);
  const browser = await playwright().chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--no-sandbox']});
  const owner = await browser.newContext({viewport: {width: 1440, height: 1000}, reducedMotion: 'reduce'});
  const desk = await owner.newPage();
  await desk.goto(new URL('/?panel=session&placement=far-in', base).href);
  await desk.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();
  const transport = async () => (await desk.request.get(new URL('/api/transport', base).href)).json();
  const outcomes = {split: [], control: []};
  for (let i = 0; i < N; i++) {
    for (const kind of ['split', 'control']) {
      const mobileContext = await browser.newContext({viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true, deviceScaleFactor: 1, reducedMotion: 'reduce', storageState: await owner.storageState()});
      await mobileContext.addInitScript(() => {
        window.__tapLog = [];
        for (const type of ['touchstart', 'touchend', 'click', 'submit']) {
          window.addEventListener(type, e => window.__tapLog.push(`${type}:${e.target && e.target.tagName}:${(e.target && e.target.textContent || '').trim().slice(0, 14)}`), true);
        }
      });
      const mobile = await mobileContext.newPage();
      const posts = [];
      mobile.on('request', r => { if (r.method() === 'POST' && !r.url().includes('/_gosx/')) posts.push(new URL(r.url()).pathname); });
      await mobile.goto(new URL('/?panel=session&placement=far-in', base).href, {waitUntil: 'domcontentloaded'});
      await mobile.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();
      const loop = mobile.locator('[data-placement-inspector="far-in"]').getByRole('button', {name: 'Loop in song', exact: true});
      const cdp = await mobileContext.newCDPSession(mobile);
      let release = null;
      if (kind === 'split') {
        let held = null;
        const gate = new Promise(resolve => { release = resolve; });
        await mobile.route('**/__workspace**', async route => { held = route; await gate; await route.continue().catch(() => {}); });
        await mobile.getByRole('link', {name: 'Edit placement far-in', exact: true}).tap();
        for (const end = Date.now() + 5000; !held && Date.now() < end;) await new Promise(resolve => setTimeout(resolve, 20));
      }
      // Aim as Playwright does: into view, then the middle of the button.
      await loop.scrollIntoViewIfNeeded();
      const aimed = await loop.boundingBox();
      const point = {x: aimed.x + aimed.width / 2, y: aimed.y + aimed.height / 2};
      const before = await mobile.evaluate(() => ({scrollY: Math.round(scrollY), status: [...document.querySelectorAll('[data-workspace-status]')].map(n => n.textContent.trim()).join('|')}));
      if (kind === 'split') {
        release();
        await mobile.locator('#cicada-workspace[aria-busy=true]').waitFor({state: 'hidden'}).catch(() => {});
        await mobile.evaluate(() => new Promise(resolve => requestAnimationFrame(() => resolve())));
      }
      const landed = await mobile.evaluate(({x, y}) => { const at = document.elementFromPoint(x, y); return {scrollY: Math.round(scrollY), at: at ? `${at.tagName}:${(at.textContent || '').trim().slice(0, 16)}` : null, status: [...document.querySelectorAll('[data-workspace-status]')].map(n => n.textContent.trim()).join('|')}; }, point);
      const now = await loop.boundingBox();
      await cdp.send('Input.dispatchTouchEvent', {type: 'touchStart', touchPoints: [point]});
      await cdp.send('Input.dispatchTouchEvent', {type: 'touchEnd', touchPoints: []});
      let started = false;
      for (const end = Date.now() + 4000; Date.now() < end;) {
        if ((await transport()).listening?.mode === 'context') { started = true; break; }
        await new Promise(resolve => setTimeout(resolve, 50));
      }
      outcomes[kind].push({i, started, aimedY: Math.round(aimed.y), nowY: now && Math.round(now.y), before, landed, posts: posts.slice(), log: await mobile.evaluate(() => window.__tapLog)});
      if (started) {
        await desk.locator('form[action="/__actions/transport"] button[value="stop"]').first().click().catch(() => {});
        for (const end = Date.now() + 5000; Date.now() < end;) { if (!(await transport()).playing) break; await new Promise(resolve => setTimeout(resolve, 50)); }
      }
      await mobileContext.close();
    }
  }
  for (const kind of ['split', 'control']) {
    console.log(`REPRO ${kind}: started ${outcomes[kind].filter(o => o.started).length} of ${outcomes[kind].length}`);
    for (const o of outcomes[kind]) console.log(`REPRO ${kind} ` + JSON.stringify(o));
  }
  await browser.close();
});
