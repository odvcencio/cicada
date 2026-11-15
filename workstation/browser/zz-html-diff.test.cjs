'use strict';
// Scratch (not committed): the Session panel's first page and the workspace
// read of the same location, saved for a diff.
const test = require('node:test');
const fs = require('node:fs');
const path = require('node:path');
const {createRequire} = require('node:module');

function playwright() {
  const location = process.env.CICADA_PLAYWRIGHT_MODULE;
  if (!location) return require('playwright');
  try { const loaded = require(path.resolve(location)); if (loaded.chromium) return loaded; } catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw error; }
  return createRequire(path.resolve(location))('playwright');
}

test('save first page and read', async () => {
  const base = process.env.CICADA_STUDIO_URL;
  const out = process.env.CICADA_PLACEMENT_PROOF_DIR;
  fs.mkdirSync(out, {recursive: true});
  const browser = await playwright().chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--no-sandbox']});
  const owner = await browser.newContext({viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true});
  const page = await owner.newPage();
  const where = '/?panel=session&placement=far-in';
  await page.goto(new URL(where, base).href, {waitUntil: 'domcontentloaded'});
  await page.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();
  const first = await (await owner.request.get(new URL(where, base).href)).text();
  const read = await (await owner.request.get(new URL('/__workspace' + where.slice(1), base).href, {headers: {Accept: 'application/json', 'X-Cicada-Reactive': '1'}})).text();
  fs.writeFileSync(path.join(out, 'first.html'), first);
  fs.writeFileSync(path.join(out, 'read.json'), read);
  // What the live page shows after its engines mounted, for comparison.
  fs.writeFileSync(path.join(out, 'live.html'), await page.evaluate(() => document.querySelector('#cicada-workspace').outerHTML));
  await browser.close();
});
