// Requires Node and Playwright. This validates execution, not acoustic realism.
// WASM_PATH and WASM_EXEC_PATH point to generated artifacts outside the repo.
const { chromium } = require('playwright');
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');

(async () => {
  const files = {
    '/': path.join(__dirname, 'index.html'),
    '/expressive.wasm': process.env.WASM_PATH,
    '/wasm_exec.js': process.env.WASM_EXEC_PATH,
  };
  for (const file of Object.values(files)) assert(file && fs.existsSync(file), 'Set valid WASM_PATH and WASM_EXEC_PATH');
  const server = http.createServer((req, res) => {
    const file = files[req.url];
    if (!file) { res.writeHead(404).end(); return; }
    res.setHeader('Content-Type', req.url.endsWith('.wasm') ? 'application/wasm' : req.url.endsWith('.js') ? 'application/javascript' : 'text/html');
    fs.createReadStream(file).pipe(res);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  let browser;
  try {
    const executablePath = process.env.CHROMIUM || '/usr/bin/chromium';
    browser = await chromium.launch({ executablePath, headless: true, args: ['--no-sandbox', '--autoplay-policy=no-user-gesture-required'] });
    console.log(JSON.stringify({ executablePath, browserVersion: browser.version(), headless: true, acousticListening: false }));
    const page = await browser.newPage();
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    await page.waitForFunction(() => !document.querySelector('#start').disabled);
    await page.click('#start');
    await page.waitForFunction(() => document.querySelector('#status').textContent.startsWith('Ready ·'));
    console.log(await page.locator('#status').textContent());
    // Observe actual ScriptProcessor buffers while the UI changes controls.
    await page.evaluate(() => {
      const api = globalThis.cicadaExpressive, render = api.render;
      globalThis.bufferEvidence = { blocks: 0, nonfinite: 0, energy: 0, peak: 0 };
      api.render = bytes => {
        const ok = render(bytes), stats = globalThis.bufferEvidence;
        stats.blocks++;
        for (const x of new Float32Array(bytes.buffer, bytes.byteOffset, bytes.byteLength / 4)) {
          if (!Number.isFinite(x)) stats.nonfinite++;
          stats.energy += x * x; stats.peak = Math.max(stats.peak, Math.abs(x));
        }
        return ok;
      };
    });
    for (const instrument of ['bow', 'brass', 'guitar']) {
      await page.selectOption('#instrument', instrument);
      assert.equal(await page.locator('.keys button.active').count(), 0, 'switch clears held-note UI');
      await page.evaluate(() => { globalThis.bufferEvidence = { blocks: 0, nonfinite: 0, energy: 0, peak: 0 }; });
      await page.locator('h1').click();
      await page.keyboard.down('a');
      assert.equal(await page.locator('.keys button.active').count(), 1, 'note down');
      for (const fraction of [0, .5, 1, .3]) {
        await page.evaluate(f => {
          for (const input of document.querySelectorAll('input[type=range]')) {
            if (input.getAttribute('aria-label') === 'Listening level') continue;
            input.value = +input.min + (+input.max - +input.min) * f;
            input.dispatchEvent(new Event('input'));
          }
        }, fraction);
        await page.waitForTimeout(160);
      }
      const stats = await page.evaluate(() => globalThis.bufferEvidence);
      assert(stats.blocks > 0, 'audio callback must run');
      assert.equal(stats.nonfinite, 0, 'sustained slider sweep finite');
      assert(stats.energy > 1e-8, 'nonzero audio');
      console.log(JSON.stringify({ instrument, sliderSweep: stats }));
      await page.keyboard.up('a');
      assert.equal(await page.locator('.keys button.active').count(), 0, 'final release');
      await page.keyboard.down('s');
      await page.selectOption('#instrument', instrument === 'bow' ? 'brass' : 'bow');
      assert.equal(await page.locator('.keys button.active').count(), 0, 'held note cleared by switch');
      await page.keyboard.up('s');
      // Long enough for constructor/transient filter state to settle; a new
      // unexcited model must remain silent after switching away from a note.
      const silence = await page.evaluate(() => {
        const a = new Float32Array(1024), bytes = new Uint8Array(a.buffer);
        let peak = 0;
        for (let b = 0; b < 8; b++) { globalThis.cicadaExpressive.render(bytes); for (const x of a) peak = Math.max(peak, Math.abs(x)); }
        return peak;
      });
      assert(silence < 1e-6, `instrument switch silence: ${silence}`);
    }
    await page.click('#stop');
    assert.deepEqual(errors, [], 'page errors');
    console.log('PASS: WASM initialization, live audio callbacks, keyboard release, continuous sliders, switch resets/silence; no page errors. No acoustic listening performed.');
  } finally {
    if (browser) await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
