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
const zlib = require('node:zlib');
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

// Oversized bodies for the port read, whose limit is 64 KiB:
//   /big?declare=1  8 MiB in 16 KiB chunks 5 ms apart (about 2.6 s), with a
//                   stated Content-Length: the engine must abort it unread;
//   /big            the same body sent at once without a length: buffered whole
//                   by the browser, then refused;
//   /bigjson        valid JSON (a real "ports" object and 8 MiB of padding),
//                   gzip-compressed as Studio compresses its own responses: the
//                   browser buffers it whole, and only the limit applied after
//                   the read makes the engine refuse it.
const bigBody = {chunk: 16 << 10, total: 8 << 20, every: 5};
const bigJSON = zlib.gzipSync(JSON.stringify({ports: {keys: 'Synthetic Keyboard'}, pad: 'x'.repeat(8 << 20)}));

function startServer() {
  const state = {big: [], midi: [], next: null};
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://x');
    if (url.pathname === '/bigjson') {
      res.writeHead(200, {'content-type': 'application/json', 'content-encoding': 'gzip', 'content-length': String(bigJSON.length)});
      res.end(bigJSON);
      return;
    }
    if (url.pathname === '/big') {
      const record = {written: 0, finished: false, closedAt: 0};
      state.big.push(record);
      const head = '{"ports":{"keys":"', tail = '"}}';
      const declared = url.searchParams.has('declare');
      res.writeHead(200, declared
        ? {'content-type': 'application/json', 'content-length': String(head.length + bigBody.total + tail.length)}
        : {'content-type': 'application/json'});
      res.write(head);
      if (!declared) {
        res.end('x'.repeat(bigBody.total) + tail);
        record.written = bigBody.total;
        record.finished = true;
        return;
      }
      const timer = setInterval(() => {
        if (record.written >= bigBody.total) {
          clearInterval(timer);
          record.finished = true;
          res.end(tail);
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
  // Every text the Live status line shows, in order, so a test can count
  // deliveries rather than read only the last one.
  await context.addInitScript(() => {
    window.__liveStatuses = [];
    new MutationObserver(() => {
      const text = document.querySelector('[data-live-status]')?.textContent;
      if (text != null && window.__liveLast !== text) { window.__liveLast = text; window.__liveStatuses.push(text); }
    }).observe(document, {subtree: true, childList: true, characterData: true});
  });
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
  const statuses = page => page.evaluate(() => window.__liveStatuses.slice());
  // shownOnce waits until the status line shows text (or a text starting with
  // it) after mark, then checks over the next 500 ms that it was shown exactly
  // once and that nothing replaced it: one result, delivered once.
  const shownOnce = async (page, mark, text, label, ms = 15_000) => {
    const matches = s => s === text || s.startsWith(text);
    await until(async () => (await statuses(page)).slice(mark).some(matches), label, ms, async () => JSON.stringify((await statuses(page)).slice(mark)));
    await page.waitForTimeout(500);
    const after = (await statuses(page)).slice(mark);
    const at = after.findIndex(matches);
    assert.ok(after.filter(matches).length === 1 && at === after.length - 1, `${label}: delivered more than once or replaced: ${JSON.stringify(after)}`);
  };
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
    const held = [], failed = [], finished = [];
    const ports = request => new URL(request.url()).pathname === '/api/midi/ports';
    page.on('requestfailed', request => { if (ports(request)) failed.push(request.failure()?.errorText || ''); });
    page.on('requestfinished', request => { if (ports(request)) finished.push(Date.now()); });
    await page.route('**/api/midi/ports', async route => {
      if (mode === 'hold') { held.push(route); return; }
      if (mode === 'network') return route.abort('connectionrefused');
      if (mode.startsWith('status:')) {
        return route.fulfill({status: 503, contentType: 'application/json', body: JSON.stringify({error: mode.slice(7)})});
      }
      if (mode === 'oversize') return route.continue({url: origin + '/big?declare=1'});
      if (mode === 'oversize-chunked') return route.continue({url: origin + '/big'});
      if (mode === 'oversize-compressed') return route.continue({url: origin + '/bigjson'});
      return route.continue();
    });
    await openLive(page);
    const reload = async next => {
      mode = next;
      const mark = (await statuses(page)).length;
      await page.locator('[data-live-control=reload-ports]').evaluate(button => button.click());
      return mark;
    };
    const unreadable = 'Could not read local MIDI port settings.';

    // An error status with a JSON body: its message is shown, once.
    let mark = await reload('status:cicada.local cannot be read (first)');
    await shownOnce(page, mark, 'cicada.local cannot be read (first)', 'the error body is shown');
    // A network error.
    mark = await reload('network');
    await shownOnce(page, mark, unreadable, 'a network error is shown');
    // The 5 s timeout aborts a request that never answers. Only the lower
    // bound is checked, so a slow runner cannot fail it.
    mark = await reload('status:second');
    await shownOnce(page, mark, 'second', 'the second error body is shown');
    let before = failed.length;
    const started = Date.now();
    mark = await reload('hold');
    await shownOnce(page, mark, unreadable, 'the timeout is shown', 30_000);
    const waited = Date.now() - started;
    assert.ok(waited >= 4_500, `the read gave up after ${waited} ms, before its 5 s timeout`);
    await until(() => failed.length > before, 'the timed-out request was aborted');
    // An oversized body that states its length: aborted before it is read, so
    // the server stops long before the 8 MiB it would send.
    mark = await reload('status:third');
    await shownOnce(page, mark, 'third', 'the third error body is shown');
    mark = await reload('oversize');
    await shownOnce(page, mark, unreadable, 'the declared oversized body is refused');
    await until(() => state.big.length === 1 && state.big[0].closedAt > 0, 'the oversized response was closed');
    const big = state.big[0];
    assert.equal(big.finished, false, 'the engine read the whole declared oversized body');
    assert.ok(big.written < 2 << 20, `the server wrote ${big.written} bytes before the engine aborted; want well under ${bigBody.total}`);
    // An oversized body without a length: today the browser buffers it whole
    // (the request finishes) and the engine refuses it after the read. The
    // finished request rules out the timeout, which would fail the request.
    for (const [kind, name] of [['oversize-chunked', 'chunked'], ['oversize-compressed', 'compressed']]) {
      mark = await reload('status:before ' + name);
      await shownOnce(page, mark, 'before ' + name, `the error body before the ${name} case is shown`);
      before = failed.length;
      const done = finished.length;
      mark = await reload(kind);
      await shownOnce(page, mark, unreadable, `the ${name} oversized body is refused`);
      assert.ok(finished.length > done && failed.length === before, `the ${name} oversized body was not read whole (finished ${finished.length - done}, failed ${failed.length - before})`);
    }
    // Dispose during a request: the request is aborted and nothing is shown.
    mark = await reload('status:fourth');
    await shownOnce(page, mark, 'fourth', 'the fourth error body is shown');
    const pending = failed.length;
    mark = await reload('hold');
    await until(() => held.length === 2, 'the request is in flight');
    assert.equal(await dispose(page), 1, 'one Live engine is disposed');
    await until(() => failed.length > pending, 'dispose aborts the request', 3_000);
    await page.waitForTimeout(500);
    assert.deepEqual((await statuses(page)).slice(mark), [], 'a disposed engine showed a result');
    for (const route of held) await route.abort().catch(() => {});
    await page.close();
  });

  await t.test('the native MIDI stream delivers events once, ends, fails, keeps alive, reconnects and aborts', async () => {
    const page = await context.newPage();
    // Without Web MIDI the engine reads Studio's native MIDI stream.
    await page.addInitScript(() => Object.defineProperty(Navigator.prototype, 'requestMIDIAccess', {configurable: true, value: undefined}));
    await page.route('**/api/midi/input', route => route.continue({url: origin + '/midi'}));
    await openLive(page);
    const button = page.locator('[data-live-control=midi]');
    const event = (record, value) => record.res.write(JSON.stringify(value) + '\n');
    const devices = name => ({type: 'devices', inputs: [{id: 'stream-' + name, name, epoch: 1}]});
    const nextStream = () => new Promise(resolve => { state.next = record => { state.next = null; resolve(record); }; });
    const connect = async (name = 'Stream One') => {
      const opened = nextStream();
      await button.evaluate(element => element.click());
      const record = await opened;
      const mark = (await statuses(page)).length;
      event(record, devices(name));
      await shownOnce(page, mark, 'MIDI connected · ' + name, 'the first event arrives while the stream is open');
      assert.equal(record.closed, false);
      return record;
    };

    // A later event arrives once on the same open stream; a clean end says so.
    let record = await connect();
    let mark = (await statuses(page)).length;
    event(record, devices('Stream Two'));
    await shownOnce(page, mark, 'MIDI connected · Stream Two', 'a later event arrives on the same stream');
    mark = (await statuses(page)).length;
    record.res.end();
    await shownOnce(page, mark, 'MIDI input ended: Studio closed the stream.', 'a server close ends the stream');
    await until(async () => await button.textContent() === 'Enable MIDI', 'the control resets after the end');

    // A server-reported error is shown.
    record = await connect();
    mark = (await statuses(page)).length;
    event(record, {type: 'error', message: 'The controller was unplugged.'});
    await shownOnce(page, mark, 'The controller was unplugged.', 'the stream error is shown');
    record.res.end();

    // A connection that breaks mid-stream is a fault, told apart from a clean end.
    record = await connect();
    mark = (await statuses(page)).length;
    record.res.socket.destroy();
    await shownOnce(page, mark, 'MIDI input failed: the connection to Studio broke.', 'a broken stream is shown');

    // Studio's once-a-second heartbeat keeps a quiet stream open; silence for
    // 5 s is a stall: the panel says so and opens a new stream.
    record = await connect();
    mark = (await statuses(page)).length;
    let lastBeat = 0;
    for (let i = 0; i < 7; i++) { event(record, {type: 'heartbeat'}); lastBeat = Date.now(); await page.waitForTimeout(1_000); }
    assert.equal(record.closed, false, 'heartbeats did not keep the stream open');
    assert.deepEqual((await statuses(page)).slice(mark), [], 'heartbeats changed the status line');
    const reopened = nextStream();
    await until(async () => (await statuses(page)).slice(mark).some(s => s.startsWith('MIDI input stopped responding')), 'a silent stream is reported', 15_000, async () => JSON.stringify((await statuses(page)).slice(mark)));
    assert.ok(Date.now() - lastBeat >= 4_500, 'the stream was declared silent less than 5 s after its last heartbeat');
    await until(() => record.closed, 'the silent stream is closed', 3_000);
    const again = await reopened;
    mark = (await statuses(page)).length;
    event(again, devices('Stream Again'));
    await shownOnce(page, mark, 'MIDI connected · Stream Again', 'the reconnected stream delivers events');
    await button.evaluate(element => element.click());
    await until(() => again.closed, 'Disconnect closes the reconnected stream', 3_000);

    // Disconnect cancels the stream's context: the request is aborted at once.
    record = await connect();
    mark = (await statuses(page)).length;
    await button.evaluate(element => element.click());
    await until(() => record.closed, 'Disconnect aborts the stream', 3_000);
    await until(async () => (await statuses(page)).slice(mark).some(s => s.startsWith('MIDI disconnected.')), 'Disconnect is shown');

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
