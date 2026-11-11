'use strict';
// Suites that page-stall.test.cjs runs in a child process, one per
// PAGE_STALL_MODE:
//   old         a page freezes inside Go and a step fails; the teardown the suites
//               used before page-stall.cjs (save storage state, then close)
//   resume      the same freeze under stall.teardown()
//   start       the page freezes while Go starts (wasm_exec.js run)
//   xhr         the page blocks in native code on a synchronous request that is never answered
//   dialog      the page blocks in native code on alert(), which the suite never answers
//   closed      the test closes its page, waits, then fails
//   hung-close  the test passes but browser.close() never finishes
//   leak-pass   the test passes and leaves a server and a timer open
//   leak-fail   the test fails and leaves a server and a timer open
const test = require('node:test');
const net = require('node:net');
const http = require('node:http');
const stall = require('../../page-stall.cjs');

const mode = process.env.PAGE_STALL_MODE;

// wasm_exec.js defines Go on globalThis, starts Go through run and enters it
// through _resume. This stand-in never returns from the entry named by freeze,
// as a Go program that keeps goroutines runnable never returns to JavaScript.
const html = freeze => `<!doctype html><button>Go</button><script>
globalThis.Go = class {
  run() { if (${freeze === 'start'}) for (;;) {} return new Promise(() => {}); }
  _resume() { if (globalThis.armed) for (;;) {} }
};
const go = new Go();
setInterval(() => go._resume(), 50);
setTimeout(() => { if (${freeze === 'start'}) go.run(); if (${freeze === 'resume'}) globalThis.armed = true; }, 500);
</script>`;

async function open(freeze) {
  const { chromium } = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--no-sandbox'] });
  const context = await browser.newContext();
  return { browser, context, show: async page => {
    await page.route('http://stall.test/', route => route.fulfill({ contentType: 'text/html', body: html(freeze) }));
    await page.goto('http://stall.test/');
  } };
}

if (mode === 'old') {
  test('a step on a frozen page times out', { timeout: 60000 }, async t => {
    const { browser, context, show } = await open('resume');
    t.after(async () => { await context.storageState(); await browser.close(); }, { timeout: 3000 });
    const page = await context.newPage();
    await show(page);
    await new Promise(resolve => setTimeout(resolve, 2500));
    await page.getByRole('button', { name: 'Go' }).click({ timeout: 1000 });
  });
}

if (mode === 'resume' || mode === 'start') {
  test('a step on a frozen page times out', { timeout: 60000 }, async t => {
    const { browser, context, show } = await open(mode);
    const watch = await stall.watchContext(context);
    stall.teardown(t, browser, { watch, before: () => context.storageState(), exitWhenDone: true });
    const page = await context.newPage();
    await show(page);
    await new Promise(resolve => setTimeout(resolve, 2500));
    await page.getByRole('button', { name: 'Go' }).click({ timeout: 1000 });
  });
}

if (mode === 'xhr' || mode === 'dialog') {
  test('a step on a page blocked in native code times out', { timeout: 90000 }, async t => {
    const { chromium } = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
    const browser = await chromium.launch(stall.withChromeLog({ headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--no-sandbox'] }, chromium));
    const context = await browser.newContext();
    const watch = await stall.watchContext(context);
    stall.teardown(t, browser, { watch, exitWhenDone: true });
    // One local server serves the page and never answers /never. The page and
    // the request share an origin, so Chrome sends the request (it refuses a
    // public-looking origin's requests to 127.0.0.1).
    const block = mode === 'xhr'
      ? "const x = new XMLHttpRequest(); x.open('GET', '/never', false); x.send();"
      : 'alert("blocked on purpose");';
    const server = http.createServer((request, response) => {
      if (request.url === '/never') return;
      response.writeHead(200, { 'content-type': 'text/html' });
      response.end(`<!doctype html><button>Go</button><script>setTimeout(() => { ${block} }, 500)</script>`);
    }).listen(0, '127.0.0.1');
    await new Promise(resolve => server.once('listening', resolve));
    t.after(() => { server.closeAllConnections(); server.close(); });
    const page = await context.newPage();
    if (mode === 'dialog') page.on('dialog', () => {}); // the suite listens and never answers
    await page.goto(`http://127.0.0.1:${server.address().port}/`);
    await new Promise(resolve => setTimeout(resolve, 2500));
    await page.getByRole('button', { name: 'Go' }).click({ timeout: 1000 });
  });
}

if (mode === 'closed') {
  test('a test fails after closing its page', { timeout: 60000 }, async t => {
    const { browser, context, show } = await open('none');
    const watch = await stall.watchContext(context);
    stall.teardown(t, browser, { watch, before: () => { console.error('page-stall fixture: teardown step ran'); return context.storageState(); }, exitWhenDone: true });
    const page = await context.newPage();
    await show(page);
    await page.close();
    await new Promise(resolve => setTimeout(resolve, 3000));
    throw new Error('fails on purpose 3 s after closing its page');
  });
}

if (mode === 'hung-close') {
  test('browser.close() never finishes', { timeout: 60000 }, async t => {
    const { browser, context, show } = await open('none');
    browser.close = () => new Promise(() => {});
    const watch = await stall.watchContext(context);
    stall.teardown(t, browser, { watch, exitWhenDone: true });
    await show(await context.newPage());
  });
}

if (mode === 'leak-pass' || mode === 'leak-fail') {
  test(`a ${mode === 'leak-pass' ? 'passing' : 'failing'} test leaves a server and a timer open`, t => {
    stall.teardown(t, null, { exitWhenDone: true, hookMS: 1000, exitAfterMS: 1500 });
    net.createServer().listen(0, '127.0.0.1');
    setInterval(() => {}, 1000);
    if (mode === 'leak-fail') throw new Error('fails on purpose');
  });
}
