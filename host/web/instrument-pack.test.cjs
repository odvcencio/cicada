const assert = require('node:assert/strict');
const { readFile } = require('node:fs/promises');
const { webcrypto } = require('node:crypto');
const { test } = require('node:test');

globalThis.crypto = webcrypto;
const loader = readFile(`${__dirname}/instrument-pack.js`, 'utf8')
  .then(source => import('data:text/javascript;base64,' + Buffer.from(source).toString('base64')));

test('user recordings retain checksums without invented remote provenance', async () => {
  const {validateManifest}=await loader;
  const manifest=JSON.parse(await readFile(`${__dirname}/../../assets/sampler/grand/manifest.json`,'utf8'));
  for (const license of ['user recording', 'owner recording']) {
    for(const asset of manifest.assets) { asset.license=license;asset.source_url='';asset.license_url='';delete asset.attribution; }
    assert.equal(validateManifest(manifest),manifest);
    manifest.assets[0].source_url='https://example.org/foreign.wav';
    assert.throws(()=>validateManifest(manifest),/licence/);
  }
  manifest.assets[0].source_url='';manifest.assets[0].license='unknown';
  assert.throws(()=>validateManifest(manifest),/licence/);
});

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

test('loadPack rejects encoded traversal before fetching any sample', async () => {
  const { loadPack, sha256 } = await loader;
  const url = 'https://example.org/packs/trumpet/manifest.json';
  for (const path of ['%2e%2e/%2e%2e/private.wav.gz', '%2E%2E/private.wav.gz', '.%2e/private.wav.gz', '%2e./private.wav.gz', 'samples%2f..%2fprivate.wav.gz', 'samples%5cprivate.wav.gz']) {
    const manifest = {
      format: 'cicada.instrument-pack/1', id: 'trumpet',
      assets: [{ id: 'take', path }], zones: [{}]
    };
    const bytes = new TextEncoder().encode(JSON.stringify(manifest));
    const calls = [];
    await assert.rejects(loadPack(url, await sha256(bytes), {
      fetch: async requested => { calls.push(requested); return new Response(bytes); }
    }), /sample path/);
    assert.deepEqual(calls, [url]);
  }
});
