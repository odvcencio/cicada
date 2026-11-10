'use strict';
// A suite whose page freezes inside a Go resume, then fails a step.
// page-stall.test.cjs runs it in a child process with PAGE_STALL_TEARDOWN=old
// (the teardown the suites used before page-stall.cjs: save storage state, then
// close the browser) or new (page-stall.cjs), and checks whether it exits.
const test = require('node:test');
const stall = require('../../page-stall.cjs');
const { chromium } = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');

// wasm_exec.js defines Go on globalThis and enters Go through _resume. This
// stand-in enters "Go" from a timer and, once armed, never returns, as a Go
// program that keeps goroutines runnable never returns to JavaScript.
const frozen = `<!doctype html><button>Go</button><script>
globalThis.Go = class { _resume() { if (globalThis.armed) for (;;) {} } };
const go = new Go();
setInterval(() => go._resume(), 50);
setTimeout(() => { globalThis.armed = true; }, 500);
</script>`;

test('a step on a frozen page times out', { timeout: 60000 }, async t => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--no-sandbox'] });
  const context = await browser.newContext();
  const hookMS = Number(process.env.PAGE_STALL_HOOK_MS || 15000);
  let watch = null, finished = false;
  if (process.env.PAGE_STALL_TEARDOWN === 'old') {
    t.after(async () => { await context.storageState(); await browser.close(); }, { timeout: hookMS });
  } else {
    t.after(async () => {
      if (!finished && watch) console.error(await stall.within(watch.report('the test did not finish'), 8000, 'page stall report did not finish'));
      await stall.closeBrowser(browser, { before: watch && watch.stalled() ? null : () => context.storageState(), beforeMS: 2000, closeMS: 3000, exitAfterMS: hookMS + 5000 });
    }, { timeout: hookMS });
  }
  const page = await context.newPage();
  if (process.env.PAGE_STALL_TEARDOWN !== 'old') watch = await stall.watchPage(page);
  await page.route('http://stall.test/', route => route.fulfill({ contentType: 'text/html', body: frozen }));
  await page.goto('http://stall.test/');
  await new Promise(resolve => setTimeout(resolve, 2500));
  await page.getByRole('button', { name: 'Go' }).click({ timeout: 1000 });
  finished = true;
});
