const assert = require('node:assert/strict');
const { readFile } = require('node:fs/promises');
const { webcrypto } = require('node:crypto');
const { test } = require('node:test');

globalThis.crypto = webcrypto;
const loader = readFile(`${__dirname}/instrument-pack.js`, 'utf8')
  .then(source => import('data:text/javascript;base64,' + Buffer.from(source).toString('base64')));

test('checkedFetch loads successive pins at one URL and reuses each offline', async () => {
  const { checkedFetch, sha256 } = await loader;
  const url = 'https://example.org/packs/manifest.json';
  const old = new TextEncoder().encode('old manifest');
  const updated = new TextEncoder().encode('updated manifest');
  const entries = new Map([[url, new Response(old)]]);
  const cache = {
    async match(key) { return entries.get(key)?.clone(); },
    async put(key, response) { entries.set(key, response.clone()); }
  };
  let calls = 0;
  const options = { cache, fetch: async requested => {
    assert.equal(requested, url);
    return new Response(calls++ === 0 ? old : updated);
  }};
  const oldPin = await sha256(old), newPin = await sha256(updated);
  assert.deepEqual(await checkedFetch(url, oldPin, 100, options), old);
  assert.deepEqual(await checkedFetch(url, newPin, 100, options), updated);
  assert.equal(calls, 2);
  options.fetch = async () => { throw new Error('offline'); };
  assert.deepEqual(await checkedFetch(url, oldPin, 100, options), old);
  assert.deepEqual(await checkedFetch(url, newPin, 100, options), updated);
});

test('checkedFetch rejects corrupt network bytes before caching', async () => {
  const { checkedFetch, sha256 } = await loader;
  const bytes = new TextEncoder().encode('expected');
  let puts = 0;
  await assert.rejects(checkedFetch('https://example.org/sample.wav.gz', await sha256(bytes), 100, {
    cache: { async match() { return null; }, async put() { puts++; } },
    fetch: async () => new Response('corrupt')
  }), /hash mismatch/);
  assert.equal(puts, 0);
});
