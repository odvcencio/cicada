'use strict';
const test = require('node:test'), assert = require('node:assert/strict');
const {startupURL, install} = require('./owner-session.cjs');

test('owner startup capability stays on the intended loopback app', () => {
  assert.equal(startupURL('Cicada Studio: http://127.0.0.1:8191/__local/disposable\n', 'http://127.0.0.1:8191'), 'http://127.0.0.1:8191/__local/disposable');
  for (const log of ['Cicada Studio: http://example.com:8191/__local/disposable', 'Cicada Studio: http://127.0.0.1:8192/__local/disposable', 'Cicada Studio: http://127.0.0.1:8191/']) {
    assert.throws(() => startupURL(log, 'http://127.0.0.1:8191'));
  }
});

test('only the first context claims the owner, including concurrent peers and another browser', async () => {
  const claims = [], contexts = [];
  let release;
  const first = new Promise(resolve => release = resolve);
  const type = {launch: async () => ({newContext: async () => {
    const id = contexts.length;
    const context = {request: {get: async url => {claims.push({id, url}); return {ok: () => true};}}};
    contexts.push(context);
    if (id === 0) await first;
    return context;
  }})};
  install(type, () => 'http://127.0.0.1:8191/__local/disposable');
  const browser = await type.launch();
  const owner = browser.newContext(), peer = browser.newContext();
  await peer; release(); await owner;
  await (await type.launch()).newContext();
  assert.deepEqual(claims, [{id: 0, url: 'http://127.0.0.1:8191/__local/disposable'}]);
});

test('failed owner bootstrap closes the context instead of continuing as a viewer', async () => {
  let closed = false;
  const type = {launch: async () => ({newContext: async () => ({request: {get: async () => ({ok: () => false, status: () => 403})}, close: async () => {closed = true;}})})};
  install(type, () => 'http://127.0.0.1:8191/__local/disposable');
  await assert.rejects((await type.launch()).newContext(), /HTTP 403/);
  assert.equal(closed, true);
});
