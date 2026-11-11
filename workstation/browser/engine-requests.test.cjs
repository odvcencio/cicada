// Engine requests through the browser's fetch (engine/fetch_js.go and
// engine/native_request.go), against a real Studio and the real engine.
//
//   node workstation/browser/engine-requests.test.cjs --write-fixture DIR
//   ./build/cicada studio DIR/main.cicada --audio null --listen 127.0.0.1:PORT > LOG
//   CICADA_STUDIO_URL=http://127.0.0.1:PORT CICADA_STUDIO_STARTUP_LOG=LOG \
//     node --test workstation/browser/engine-requests.test.cjs
//
// Playwright routes decide what each request meets. Responses that must
// arrive over time (the MIDI input stream, an oversized body) come from a
// small HTTP server in this process; the route sends the request there.
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');
const stall = require('./page-stall.cjs');

if (process.argv[2] === '--write-fixture') {
  const dir = path.resolve(process.argv[3] || '');
  assert(process.argv[3], 'provide a disposable fixture directory');
  fs.mkdirSync(dir, {recursive: true});
  fs.writeFileSync(path.join(dir, 'main.cicada'), `cicada 2
tempo 120
track lead acid {}
midi {
  port keys
  keys ch 1 -> lead
}
pattern phrase { 1 . 3 . }
scene verse { lead = phrase }
song { verse*8 }
`, {flag: 'wx'});
  fs.writeFileSync(path.join(dir, 'cicada.local'), 'port keys = "Synthetic Keyboard"\n', {flag: 'wx'});
  process.exit(0);
}

// A body larger than the port read's 64 KiB limit, sent in 16 KiB chunks
// 5 ms apart: all of it takes about 2.6 s, so a reader that stops at the
// limit closes the connection long before the end.
const bigBody = {chunk: 16 << 10, total: 8 << 20, every: 5};

function startServer() {
  const state = {big: [], midi: [], next: null};
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://x');
    if (url.pathname === '/big') {
      const record = {written: 0, finished: false, closedAt: 0};
      state.big.push(record);
      res.writeHead(200, {'content-type': 'application/json'});
      res.write('{"ports":{"keys":"');
      const timer = setInterval(() => {
        if (record.written >= bigBody.total) {
          clearInterval(timer);
          record.finished = true;
          res.end('"}}');
          return;
        }
        res.write('x'.repeat(bigBody.chunk));
        record.written += bigBody.chunk;
      }, bigBody.every);
      res.on('close', () => { clearInterval(timer); record.closedAt = Date.now(); });
      return;
    }
    if (url.pathname === '/midi') {
      const record = {closed: false, res};
      state.midi.push(record);
      res.writeHead(200, {'content-type': 'application/x-ndjson'});
      res.on('close', () => { record.closed = true; });
      req.resume();
      if (state.next) state.next(record);
      return;
    }
    res.writeHead(404);
    res.end();
  });
  return new Promise(resolve => server.listen(0, '127.0.0.1', () => resolve({server, state, origin: `http://127.0.0.1:${server.address().port}`})));
}

async function until(check, label, ms = 15_000, detail = async () => '') {
  const deadline = Date.now() + ms;
  while (Date.now() < deadline) {
    if (await check()) return;
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  assert.fail(`${label} ${await detail()}`.trim());
}

test('engine requests time out, fail, refuse, stop at their limit, stream and abort through the browser fetch', {timeout: 180_000}, async t => {
  const startup = new URL(process.env.CICADA_STUDIO_URL);
  assert.equal(startup.hostname, '127.0.0.1');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--no-sandbox', '--mute-audio']});
  const context = await browser.newContext({viewport: {width: 1440, height: 1000}});
  const watch = await stall.watchContext(context, {serverLog: process.env.CICADA_STUDIO_STARTUP_LOG});
  const {server, state, origin} = await startServer();
  stall.teardown(t, browser, {watch, exitWhenDone: true});
  t.after(() => server.close());
  const errors = [];
  context.on('page', page => page.on('pageerror', error => errors.push(error.message)));

  const bootstrap = await context.newPage();
  await bootstrap.goto(startup.href);
  await bootstrap.close();

  const status = page => page.locator('[data-live-status]').textContent();
  const openLive = async page => {
    await page.goto(new URL('/studio?tab=session', startup).href);
    await page.locator('#studio-shell[data-shell-actions=ready]').waitFor({timeout: 90_000});
    await page.locator('[data-live-control=midi]').waitFor({state: 'attached', timeout: 30_000});
  };
  const dispose = page => page.evaluate(() => {
    let disposed = 0;
    for (const record of window.__gosx.engines.values()) {
      if (record.component === 'CicadaLive' && record.handle && typeof record.handle.dispose === 'function') {
        record.handle.dispose();
        disposed++;
      }
    }
    return disposed;
  });

  await t.test('a JSON read: error status, network error, timeout, oversized body and dispose', async () => {
    const page = await context.newPage();
    let mode = 'pass';
    const held = [], failed = [];
    page.on('requestfailed', request => {
      if (new URL(request.url()).pathname === '/api/midi/ports') failed.push(request.failure()?.errorText || '');
    });
    await page.route('**/api/midi/ports', async route => {
      if (mode === 'hold') { held.push(route); return; }
      if (mode === 'network') return route.abort('connectionrefused');
      if (mode.startsWith('status:')) {
        return route.fulfill({status: 503, contentType: 'application/json', body: JSON.stringify({error: mode.slice(7)})});
      }
      if (mode === 'oversize') return route.continue({url: origin + '/big'});
      return route.continue();
    });
    await openLive(page);
    const reload = async next => {
      mode = next;
      await page.locator('[data-live-control=reload-ports]').evaluate(button => button.click());
    };
    const unreadable = 'Could not read local MIDI port settings.';

    // An error status with a JSON body: the body's message is shown.
    await reload('status:cicada.local cannot be read (first)');
    await until(async () => await status(page) === 'cicada.local cannot be read (first)', 'the error body is shown');
    // A network error.
    await reload('network');
    await until(async () => await status(page) === unreadable, 'a network error is shown');
    // The 5 s timeout aborts a request that never answers.
    await reload('status:second');
    await until(async () => await status(page) === 'second', 'the second error body is shown');
    const before = failed.length;
    const started = Date.now();
    await reload('hold');
    await until(async () => await status(page) === unreadable, 'the timeout is shown', 15_000);
    const waited = Date.now() - started;
    assert.ok(waited >= 4_500 && waited < 9_000, `the read timed out after ${waited} ms, want about 5 s`);
    await until(() => failed.length > before, 'the timed-out request was aborted');
    // An oversized body: the engine stops reading past its 64 KiB limit and
    // aborts, so the server stops long before the 8 MiB it would send.
    await reload('status:third');
    await until(async () => await status(page) === 'third', 'the third error body is shown');
    await reload('oversize');
    await until(async () => await status(page) === unreadable, 'the oversized body is refused');
    await until(() => state.big.length === 1 && state.big[0].closedAt > 0, 'the oversized response was closed');
    const big = state.big[0];
    assert.equal(big.finished, false, 'the engine read the whole oversized body');
    assert.ok(big.written < 2 << 20, `the server wrote ${big.written} bytes before the engine aborted; want well under ${bigBody.total}`);
    // Dispose during a request: the request is aborted and nothing is shown.
    await reload('status:fourth');
    await until(async () => await status(page) === 'fourth', 'the fourth error body is shown');
    const pending = failed.length;
    await reload('hold');
    await until(() => held.length === 2, 'the request is in flight');
    assert.equal(await dispose(page), 1, 'one Live engine is disposed');
    await until(() => failed.length > pending, 'dispose aborts the request', 3_000);
    await page.waitForTimeout(500);
    assert.equal(await status(page), 'fourth', 'a disposed engine showed a result');
    for (const route of held) await route.abort().catch(() => {});
    await page.close();
  });

  await t.test('the native MIDI stream delivers events as they arrive, ends, fails and aborts', async () => {
    const page = await context.newPage();
    // Without Web MIDI the engine reads Studio's native MIDI stream.
    await page.addInitScript(() => Object.defineProperty(Navigator.prototype, 'requestMIDIAccess', {configurable: true, value: undefined}));
    await page.route('**/api/midi/input', route => route.continue({url: origin + '/midi'}));
    await openLive(page);
    const button = page.locator('[data-live-control=midi]');
    const event = (record, value) => record.res.write(JSON.stringify(value) + '\n');
    const devices = name => ({type: 'devices', inputs: [{id: 'stream-' + name, name, epoch: 1}]});
    const connect = async () => {
      const opened = new Promise(resolve => { state.next = resolve; });
      await button.evaluate(element => element.click());
      const record = await opened;
      state.next = null;
      event(record, devices('Stream One'));
      await until(async () => /MIDI connected · Stream One/.test(await status(page)), 'the first event arrives while the stream is open');
      assert.equal(record.closed, false);
      return record;
    };

    // Events arrive while the stream stays open; a clean end resets the control.
    let record = await connect();
    event(record, devices('Stream Two'));
    await until(async () => /MIDI connected · Stream Two/.test(await status(page)), 'a later event arrives on the same stream');
    record.res.end();
    await until(async () => (await status(page)).startsWith('MIDI connection ended. Connect again to resume.'), 'a server close ends the stream');
    await until(async () => await button.textContent() === 'Enable MIDI', 'the control resets after the end');

    // A server-reported error is shown.
    record = await connect();
    event(record, {type: 'error', message: 'The controller was unplugged.'});
    await until(async () => (await status(page)).startsWith('The controller was unplugged.'), 'the stream error is shown');
    record.res.end();

    // A connection that breaks mid-stream is a reader fault.
    record = await connect();
    record.res.socket.destroy();
    await until(async () => (await status(page)).startsWith('MIDI connection ended. Connect again to resume.'), 'a broken stream is shown');

    // Disconnect cancels the stream's context: the request is aborted at once.
    record = await connect();
    await button.evaluate(element => element.click());
    await until(() => record.closed, 'Disconnect aborts the stream', 3_000);
    await until(async () => (await status(page)).startsWith('MIDI disconnected.'), 'Disconnect is shown');

    // Disposing the engine aborts an open stream.
    record = await connect();
    assert.equal(await dispose(page), 1, 'one Live engine is disposed');
    await until(() => record.closed, 'dispose aborts the stream', 3_000);
    await page.close();
  });

  await t.test('a restored take checks the saved note recovery', async () => {
    const page = await context.newPage();
    // One recorded note on the fixture's lead track, as the engine stores an
    // unfinished take in this window.
    const takes = [{track: 'lead', pattern: 'phrase', notes: [{tick: 0, endTick: 240, note: 60, velocity: 100}]}];
    await page.addInitScript(takes => {
      if (!sessionStorage.getItem('cicada.note-take')) {
        sessionStorage.setItem('cicada.note-take', JSON.stringify({revision: '', sourceRevision: '', takes, id: 'restore-test', sequence: 0}));
      }
      // Every Live status this window shows, kept across the refresh that
      // clearing a handled take starts (it remounts Live and resets its status).
      new MutationObserver(() => {
        const text = document.querySelector('[data-live-status]')?.textContent;
        const shown = JSON.parse(sessionStorage.getItem('engine-requests.statuses') || '[]');
        if (text && shown[shown.length - 1] !== text) sessionStorage.setItem('engine-requests.statuses', JSON.stringify([...shown, text]));
      }).observe(document, {subtree: true, childList: true, characterData: true});
    }, takes);
    const read = async key => {
      try { return JSON.parse(await page.evaluate(key => sessionStorage.getItem(key), key)); } catch { return null; }
    };
    let answer = 'unavailable';
    const asked = [];
    await page.route('**/api/note-recovery/**', route => {
      asked.push(new URL(route.request().url()).pathname);
      if (answer === 'unavailable') return route.fulfill({status: 503, contentType: 'text/plain', body: 'busy'});
      return route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({state: 'applied', sourceRevision: '', sequence: 0, takes})});
    });
    await openLive(page);
    const seen = async () => `(status ${JSON.stringify(await status(page))}; requests ${JSON.stringify(asked)}; stored ${await page.evaluate(() => sessionStorage.getItem('cicada.note-take'))})`;
    await until(async () => await status(page) === 'Saved note recovery is unavailable. Your browser copy is retained.', 'an unavailable recovery is shown', 15_000, seen);
    assert.ok(asked.every(p => p === '/api/note-recovery/restore-test'), asked.join(', '));
    answer = 'applied';
    await until(async () => (await read('cicada.note-take'))?.id === '', 'the handled take is cleared from this window', 15_000, seen);
    await until(async () => ((await read('engine-requests.statuses')) || []).some(text => text.startsWith('Your saved performance is already retained or handled.')), 'the handled take is announced', 5_000, async () => JSON.stringify(await read('engine-requests.statuses')));
    const checks = asked.length;
    await page.waitForTimeout(1_500);
    assert.equal(asked.length, checks, 'the engine kept checking a handled take');
    await page.close();
  });

  assert.deepEqual(errors, [], 'page errors');
});
