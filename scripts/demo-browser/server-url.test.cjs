'use strict';
const assert = require('node:assert/strict');
const {PassThrough} = require('node:stream');
const {EventEmitter} = require('node:events');
const {test} = require('node:test');
const {findServerURL, waitForServerURL} = require('./server-url.cjs');

function fakeServer() {
  const server = new EventEmitter();
  server.stdout = new PassThrough();
  server.stderr = new PassThrough();
  return server;
}

test('findServerURL needs a complete, terminated URL', () => {
  assert.equal(findServerURL('http://127.0.0.1:4'), undefined);
  assert.equal(findServerURL('http://127.0.0.1:41234\n'), 'http://127.0.0.1:41234');
  assert.equal(findServerURL('starting\nhttp://127.0.0.1:80 \n'), 'http://127.0.0.1:80');
  assert.equal(findServerURL('http://[::1]:5\n'), undefined);
});

test('waitForServerURL joins a URL split across chunks', async () => {
  const server = fakeServer();
  const result = waitForServerURL(server, {timeoutMs: 2000});
  server.stdout.write('http://127.0.0.1:4');
  await new Promise(resolve => setImmediate(resolve));
  server.stdout.write('1234');
  await new Promise(resolve => setImmediate(resolve));
  server.stdout.write('\n');
  assert.equal(await result, 'http://127.0.0.1:41234');
});

test('waitForServerURL handles a split multi-byte rune before the URL', async () => {
  const server = fakeServer();
  const result = waitForServerURL(server, {timeoutMs: 2000});
  const bytes = Buffer.from('é http://127.0.0.1:9\n');
  server.stdout.write(bytes.subarray(0, 1));
  server.stdout.write(bytes.subarray(1));
  assert.equal(await result, 'http://127.0.0.1:9');
});

test('waitForServerURL timeout reports captured stdout and stderr', async () => {
  const server = fakeServer();
  let captured;
  const result = waitForServerURL(server, {timeoutMs: 50, onFailure: output => { captured = output; }});
  server.stdout.write('loading kernel\n');
  server.stderr.write('slow disk\n');
  await assert.rejects(result, error => {
    assert.match(error.message, /start timed out after 50 ms/);
    assert.match(error.message, /loading kernel/);
    assert.match(error.message, /slow disk/);
    return true;
  });
  assert.match(captured.stdout, /loading kernel/);
  assert.match(captured.stderr, /slow disk/);
});

test('waitForServerURL reports an early exit with output', async () => {
  const server = fakeServer();
  const result = waitForServerURL(server, {timeoutMs: 2000});
  server.stdout.write('partial\n');
  await new Promise(resolve => setImmediate(resolve));
  server.emit('exit', 2, null);
  await assert.rejects(result, /exited 2 before printing its URL[\s\S]*partial/);
});
