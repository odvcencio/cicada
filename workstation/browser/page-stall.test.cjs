'use strict';
// page-stall.cjs: a step that times out on a frozen page says why, and the
// suite's Node process exits instead of waiting for CI's outer time limit.
//
// In pull request #314's run the Producer workflow page stopped answering, its
// click timed out, and the after hook (save storage state, then close the
// browser) waited on the frozen page until its 15 s limit. browser.close() never
// ran, so the open browser kept the process alive until the 300 s suite limit.
// testdata/page-stall/frozen-suite.cjs replays that with a page frozen inside a
// stand-in Go resume, under the old teardown and under page-stall.cjs.
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const { spawn } = require('node:child_process');

const suite = path.join(__dirname, 'testdata', 'page-stall', 'frozen-suite.cjs');

// run starts the fixture suite in its own process group and kills the group if
// it is still running after limitMS.
function run(teardown, limitMS) {
  return new Promise(resolve => {
    const started = Date.now();
    // node --test marks its children with NODE_TEST_CONTEXT; a child that
    // inherits it skips its files, so the fixture runs as CI runs a suite.
    const env = { ...process.env, PAGE_STALL_TEARDOWN: teardown, PAGE_STALL_HOOK_MS: '3000' };
    delete env.NODE_TEST_CONTEXT;
    const child = spawn(process.execPath, ['--test', suite], { env, detached: true, stdio: ['ignore', 'pipe', 'pipe'] });
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

test('the old teardown keeps a failed suite alive on a frozen page', { timeout: 60000 }, async () => {
  const result = await run('old', 15000);
  assert.match(result.output, /not ok 1 - a step on a frozen page times out/, result.output);
  assert.equal(result.killed, true, `the old teardown exited by itself after ${result.ms} ms; the fixture no longer reproduces the hang:\n${result.output}`);
});

test('page-stall.cjs reports the frozen page and the suite exits', { timeout: 60000 }, async () => {
  const result = await run('new', 15000);
  assert.equal(result.killed, false, `the suite was still running after 15000 ms:\n${result.output}`);
  assert.notEqual(result.code, 0, 'the failed step must fail the suite');
  assert.match(result.output, /not ok 1 - a step on a frozen page times out/);
  assert.match(result.output, /page stall report \(the test did not finish\)/);
  assert.match(result.output, /last setInterval beat \d+ ms ago/);
  assert.match(result.output, /OPEN 1:inline timer resume entered \d+ ms ago and never returned/, result.output);
  assert.match(result.output, /renderer CPU over 1 s: pid \d+ \d+%/, result.output);
});
