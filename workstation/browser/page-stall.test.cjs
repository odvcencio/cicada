'use strict';
// page-stall.cjs: a step that times out on a frozen page says why, and the
// suite's Node process exits instead of waiting for CI's outer time limit.
//
// In pull request #314's run the Producer workflow page stopped answering, its
// click timed out, and the after hook (save storage state, then close the
// browser) waited on the frozen page until its 15 s limit. browser.close() never
// ran, so the open browser kept the process alive until the 300 s suite limit.
// testdata/page-stall/frozen-suite.cjs replays that and the other cases the
// helper handles, each in its own process, as CI runs a suite.
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const { spawn, execFileSync } = require('node:child_process');

const suite = path.join(__dirname, 'testdata', 'page-stall', 'frozen-suite.cjs');

// run starts the fixture suite in its own process group with the TAP reporter
// (the default reporter differs between Node versions) and kills the group if
// it is still running after limitMS.
function run(mode, limitMS) {
  return new Promise(resolve => {
    const started = Date.now();
    // node --test marks its children with NODE_TEST_CONTEXT; a child that
    // inherits it skips its files.
    // Native stacks from /proc and any debugger already present, never an
    // install: these freezes are deliberate and run on every CI run.
    const env = { ...process.env, PAGE_STALL_MODE: mode, CICADA_PAGE_STALL_NATIVE: '1' };
    delete env.NODE_TEST_CONTEXT;
    const child = spawn(process.execPath, ['--test', '--test-reporter=tap', suite], { env, detached: true, stdio: ['ignore', 'pipe', 'pipe'] });
    let output = '';
    child.stdout.on('data', data => { output += data; });
    child.stderr.on('data', data => { output += data; });
    let killed = false;
    const limit = setTimeout(() => { killed = true; try { process.kill(-child.pid, 'SIGKILL'); } catch {} }, limitMS);
    child.on('exit', code => {
      clearTimeout(limit);
      try { process.kill(-child.pid, 'SIGKILL'); } catch {} // Chrome, if the suite left it
      resolve({ code, killed, ms: Date.now() - started, output });
    });
  });
}

// Besides reading native stacks, the teardown must fit 13 s of its 15 s hook
// limit; the whole teardown must fit the limit with native stacks added.
function assertBudget(output) {
  const took = output.match(/teardown took (\d+) ms of its (\d+) ms limit \((\d+) ms of it reading native stacks\)/);
  assert.ok(took, `no teardown timing line:\n${output}`);
  const [total, limit, native] = took.slice(1).map(Number);
  assert.ok(total - native <= 13000, `teardown took ${total - native} ms besides native stacks, over its 13000 ms budget:\n${output}`);
  assert.ok(total <= limit - 1000, `teardown took ${total} ms of its ${limit} ms limit:\n${output}`);
}

const failed = /not ok 1 - /;

test('the server log tail hides the owner link', t => {
  const fs = require('node:fs'), os = require('node:os');
  const { tail } = require('./page-stall.cjs');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'page-stall-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const log = path.join(dir, 'server.log');
  fs.writeFileSync(log, 'Cicada Studio: http://127.0.0.1:8197/__local/KQHRLSZKAJNGINOKRN5GVBWHQA\nGET /__local/abc_DEF-123?x=1 ok\n');
  const text = tail(log);
  assert.doesNotMatch(text, /KQHRLSZ|abc_DEF/);
  assert.equal(text, 'Cicada Studio: http://127.0.0.1:8197/__local/<redacted>\nGET /__local/<redacted>?x=1 ok\n');
});

// Playwright accepts beforeunload when nobody listens for dialogs; the
// helper's own dialog listener must do the same, or a watched page cancels
// navigations an unwatched page completes. Studio registers such a handler
// (assets/studio-startup.js) while a form control is edited.
test('a watched page whose beforeunload handler asks to stay still navigates', { timeout: 60000 }, async () => {
  const http = require('node:http');
  const stall = require('./page-stall.cjs');
  const { chromium } = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const server = http.createServer((request, response) => {
    response.writeHead(200, { 'content-type': 'text/html' });
    response.end(request.url === '/next' ? '<p>next</p>' : '<input id=i><script>addEventListener("beforeunload", e => { e.preventDefault(); e.returnValue = ""; })</script>');
  }).listen(0, '127.0.0.1');
  await new Promise(resolve => server.once('listening', resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--no-sandbox'] });
  try {
    const context = await browser.newContext();
    const watch = await stall.watchContext(context);
    const page = await context.newPage();
    await page.goto(origin + '/');
    await page.locator('#i').click();
    await page.keyboard.type('x'); // beforeunload asks only after a user gesture
    const outcome = await page.goto(origin + '/next', { timeout: 5000 }).then(() => 'navigated', error => error.message.split('\n')[0]);
    assert.equal(outcome, 'navigated');
    assert.equal(page.url(), origin + '/next');
    assert.match(await watch.report('check'), /dialogs opened: beforeunload/, 'the beforeunload dialog did not open, so this test proves nothing');
  } finally {
    await browser.close();
    server.closeAllConnections();
    server.close();
  }
});

test('gdb is installed only on a GitHub-hosted runner', () => {
  const { installRefusal } = require('./page-stall.cjs');
  assert.match(installRefusal({ CICADA_PAGE_STALL_NATIVE: 'install' }), /RUNNER_ENVIRONMENT is not set/);
  assert.match(installRefusal({ CICADA_PAGE_STALL_NATIVE: 'install', RUNNER_ENVIRONMENT: 'self-hosted' }), /RUNNER_ENVIRONMENT is "self-hosted"/);
  assert.match(installRefusal({ CICADA_PAGE_STALL_NATIVE: '1', RUNNER_ENVIRONMENT: 'github-hosted' }), /not install/);
  assert.equal(installRefusal({ CICADA_PAGE_STALL_NATIVE: 'install', RUNNER_ENVIRONMENT: 'github-hosted' }), '');
});

// A time limit on sudo alone leaves the root command running; runSudo puts
// timeout(1) inside sudo. Needs passwordless sudo (CI's hosted runners have it).
const sudoWorks = process.platform === 'linux' && (() => { try { execFileSync('sudo', ['-n', 'true'], { stdio: 'ignore', timeout: 3000 }); return true; } catch { return false; } })();
test('a command run through sudo stops at its time limit', { skip: !sudoWorks && 'needs passwordless sudo on Linux', timeout: 30000 }, async () => {
  const { runSudo } = require('./page-stall.cjs');
  const marker = `sleep ${37 + Math.random()}`.slice(0, 15);
  const started = Date.now();
  await runSudo(['sh', '-c', marker], 3000);
  assert.ok(Date.now() - started < 4000, `runSudo returned after ${Date.now() - started} ms`);
  const running = () => execFileSync('ps', ['-A', '-o', 'args='], { encoding: 'utf8' }).split('\n').filter(line => line.trim() === marker);
  for (let i = 0; i < 40 && running().length; i++) await new Promise(resolve => setTimeout(resolve, 100));
  assert.deepEqual(running(), [], `the root "${marker}" outlived its time limit`);
});

test('a Chrome that fails to start shows its log, owner links redacted', { timeout: 30000 }, async t => {
  const fs = require('node:fs'), os = require('node:os');
  const stall = require('./page-stall.cjs');
  const { chromium } = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'page-stall-fake-chrome-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const fake = path.join(dir, 'chrome');
  fs.writeFileSync(fake, '#!/bin/sh\necho "[FATAL:fake_chrome.cc(1)] cannot start http://127.0.0.1:1/__local/OWNERTOKEN123" >&2\nexit 3\n', { mode: 0o755 });
  const error = await chromium.launch(stall.withChromeLog({ headless: true, executablePath: fake, args: ['--no-sandbox'] }, chromium)).then(browser => browser.close().then(() => null), e => e);
  assert.ok(error, 'the fake Chrome launched');
  assert.match(error.message, /Chrome exited with code 3; the last 30 lines of its log:/, error.message);
  assert.match(error.message, /FATAL:fake_chrome\.cc\(1\)\] cannot start http:\/\/127\.0\.0\.1:1\/__local\/<redacted>/, error.message);
  assert.doesNotMatch(error.message, /OWNERTOKEN123/);
});

test('the old teardown keeps a failed suite alive on a frozen page', { timeout: 60000 }, async () => {
  const result = await run('old', 12000);
  assert.match(result.output, failed, result.output);
  assert.equal(result.killed, true, `the old teardown exited by itself after ${result.ms} ms; the fixture no longer reproduces the hang:\n${result.output}`);
});

test('a freeze inside a Go entry is reported and the suite exits within its teardown budget', { timeout: 60000 }, async () => {
  const result = await run('resume', 30000);
  assert.equal(result.killed, false, `the suite was still running after 30000 ms:\n${result.output}`);
  assert.notEqual(result.code, 0, 'the failed step must fail the suite');
  assert.match(result.output, failed);
  assert.match(result.output, /page stall report \(the test did not pass\)/, result.output);
  assert.match(result.output, /last setInterval beat \d+ ms ago/);
  assert.match(result.output, /renderer CPU over 1 s: pid \d+ \d+%/, result.output);
  assert.match(result.output, /OPEN 1:inline timer entry \d+ ms ago and never returned/, result.output);
  // The stand-in re-enters "Go" every 50 ms. One termination used to let that
  // timer run before the queued read, which then never answered (CI, three times).
  assert.doesNotMatch(result.output, /go: the page did not answer/, result.output);
  assert.match(result.output, /skipped the teardown step because a page stopped responding; browser closed/, result.output);
  assert.doesNotMatch(result.output, /Exiting with code/, 'the suite must exit without the forced exit');
  assertBudget(result.output);
});

// The order CI hit by chance, forced: the frozen entry posts a message that
// calls it again before it spins, so that message is queued ahead of the
// helper's read and runs first once the script is terminated. With one
// termination the read never answered (8 of 8 runs). The read is queued ahead
// of the message the second freeze posts, so the next termination lets it run.
test('a page that re-enters Go after its script is terminated still reports the open entry', { timeout: 60000 }, async () => {
  const result = await run('reenter', 30000);
  assert.equal(result.killed, false, `the suite was still running after 30000 ms:\n${result.output}`);
  assert.match(result.output, failed);
  assert.doesNotMatch(result.output, /go: the page did not answer/, result.output);
  assert.match(result.output, /OPEN 1:inline timer entry \d+ ms ago and never returned/, result.output);
  assert.match(result.output, /[1-9]\d* more entries opened after the first Runtime\.terminateExecution: the page re-entered Go/, `the page did not re-enter Go, so this test proves nothing:\n${result.output}`);
  assertBudget(result.output);
});

test('a freeze while Go starts is reported as inside Go', { timeout: 60000 }, async () => {
  const result = await run('start', 30000);
  assert.equal(result.killed, false, result.output);
  assert.match(result.output, /OPEN 1:inline start entry \d+ ms ago and never returned/, result.output);
  assertBudget(result.output);
});

for (const [mode, crumb] of [['xhr', /XMLHttpRequest\.open GET \/never SYNCHRONOUS/], ['dialog', /window\.alert\(blocked on purpose\)/]]) {
  test(`a page blocked in native code (${mode}) is reported from outside`, { timeout: 90000 }, async () => {
    const result = await run(mode, 60000);
    assert.equal(result.killed, false, result.output);
    assert.match(result.output, failed);
    assert.match(result.output, crumb, result.output);
    if (mode === 'dialog') assert.match(result.output, /dialogs opened: alert "blocked on purpose"/, result.output);
    assert.match(result.output, /renderer CPU over 1 s: pid \d+ \d+%/, result.output);
    assert.match(result.output, /native: \d+ Chrome processes: \d+ browser/, result.output);
    if (process.platform === 'linux') assert.match(result.output, /--- \d+ renderer ---\n(?:# )? {2}main thread: \S+ [SD] wchan=/, result.output);
    assert.match(result.output, /--- last 30 lines of Chrome's log ---/, result.output);
    assertBudget(result.output);
  });
}

test('a closed page is neither stalled nor reported', { timeout: 60000 }, async () => {
  const result = await run('closed', 30000);
  assert.equal(result.killed, false, result.output);
  assert.match(result.output, failed);
  assert.match(result.output, /no open watched pages/, result.output);
  assert.match(result.output, /page-stall fixture: teardown step ran/, 'the teardown step must run when no open page stalled');
  assert.doesNotMatch(result.output, /renderer CPU|OPEN /, result.output);
});

test('when browser.close() never finishes, the browser process group is killed', { timeout: 60000 }, async () => {
  const result = await run('hung-close', 30000);
  assert.equal(result.killed, false, result.output);
  assert.equal(result.code, 0, `the passing suite must pass:\n${result.output}`);
  const group = result.output.match(/browser\.close\(\) did not finish within 2500 ms; killed process group (\d+)/);
  assert.ok(group, `no process group kill:\n${result.output}`);
  assert.doesNotMatch(result.output, /Exiting with code/, 'killing Chrome must free the process without the forced exit');
  assertBudget(result.output);
  // Every process of the browser's group is gone (ps exists on Linux and macOS).
  const leftover = () => execFileSync('ps', ['-A', '-o', 'pid=,pgid='], { encoding: 'utf8' }).split('\n').filter(line => line.trim().split(/\s+/)[1] === group[1]);
  for (let i = 0; i < 30 && leftover().length; i++) await new Promise(resolve => setTimeout(resolve, 100));
  assert.deepEqual(leftover(), [], `processes of group ${group[1]} survived`);
});

test('a passing suite held open by a leaked handle exits with code 0 and names the handle', { timeout: 60000 }, async () => {
  const result = await run('leak-pass', 15000);
  assert.equal(result.killed, false, result.output);
  assert.equal(result.code, 0, result.output);
  assert.match(result.output, /every watched test passed, but the process was still alive 1500 ms after its last teardown began; held open by: .*Server x1.*Exiting with code 0/, result.output);
});

test('a failing suite held open by a leaked handle exits with code 1', { timeout: 60000 }, async () => {
  const result = await run('leak-fail', 15000);
  assert.equal(result.killed, false, result.output);
  assert.notEqual(result.code, 0, result.output);
  assert.match(result.output, /a test failed, but the process was still alive 1500 ms after its last teardown began; held open by: .*Exiting with code 1/, result.output);
});

// A dead DevTools session rejects every send at once. The report used to retry
// in a loop with only microtask waits, which held Node's event loop for 4 s and
// then blamed "0 Runtime.terminateExecution" instead of the error.
test('a dead DevTools session is reported with its error and is not polled in a loop', { timeout: 30000 }, async () => {
  const { goSnapshot } = require('./page-stall.cjs');
  let sends = 0;
  const dead = { send: () => { sends++; return Promise.reject(new Error('Session closed. Most likely the page has been closed.')); } };
  const timer = setInterval(() => { ticks++; }, 10);
  const started = Date.now();
  const report = await goSnapshot(dead);
  clearInterval(timer);
  assert.equal(report, 'go: no entry record (Session closed. Most likely the page has been closed.)');
  assert.ok(sends <= 3, `the dead session was asked ${sends} times`);
  assert.ok(Date.now() - started < 1000, `the report took ${Date.now() - started} ms`);
});

// A read that a termination keeps rejecting is retried, but 25 ms apart, so
// the event loop keeps running (392,128 reads in 1 s before, with timers late).
test('a read that every termination rejects is retried without blocking the event loop', { timeout: 30000 }, async () => {
  const { goSnapshot } = require('./page-stall.cjs');
  let reads = 0, ticks = 0;
  const session = { send: method => method === 'Runtime.evaluate' ? (reads++, Promise.reject(new Error('Execution was terminated'))) : Promise.resolve({}) };
  const timer = setInterval(() => { ticks++; }, 10);
  const started = Date.now();
  const report = await goSnapshot(session);
  clearInterval(timer);
  const ms = Date.now() - started;
  assert.match(report, /^go: the page did not answer within 4000 ms/);
  assert.ok(reads <= ms / 20, `${reads} reads in ${ms} ms: the retry loop is not paused`);
  assert.ok(ticks >= ms / 10 / 3, `a 10 ms timer ticked ${ticks} times in ${ms} ms: the event loop was blocked`);
});
