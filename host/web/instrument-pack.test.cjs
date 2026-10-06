const assert = require('node:assert/strict');
const { readFile } = require('node:fs/promises');
const { webcrypto } = require('node:crypto');
const { test } = require('node:test');

globalThis.crypto = webcrypto;
const loader = readFile(`${__dirname}/instrument-pack.js`, 'utf8')
  .then(async source => {
    const encoding = await readFile(`${__dirname}/audio-encoding.js`, 'utf8');
    const url = 'data:text/javascript;base64,' + Buffer.from(encoding).toString('base64');
    return import('data:text/javascript;base64,' + Buffer.from(source.replace('./audio-encoding.js', url)).toString('base64'));
  });

test('catalog downloads default to PCM16 and keep exact and legacy pins available', async () => {
  const {selectPack}=await loader;
  const pin='a'.repeat(64),exact='b'.repeat(64),small='c'.repeat(64);
  const old={id:'kit',manifest:'kit/manifest.json',sha256:pin};
  assert.equal(selectPack({packs:[old]},'kit').sha256,pin);
  const pack={...old,tiers:{lossless:{manifest:'lossless/kit/manifest.json',sha256:exact},hq16:{manifest:'hq16/kit/manifest.json',sha256:small}}};
  const catalog={packs:[pack]};
  assert.equal(selectPack(catalog,'kit').sha256,small);
  assert.equal(selectPack(catalog,'kit','lossless').sha256,exact);
  assert.equal(selectPack(catalog,'kit','gzip').sha256,pin);
  assert.throws(()=>selectPack(catalog,'kit','opus'),/tier/);
  pack.tiers.hq16.manifest='../escape.json';
  assert.throws(()=>selectPack(catalog,'kit'),/invalid/);
});

test('loadPack admits encoded FLAC and exact float32 gzip before publication', async () => {
  const {loadPack,sha256}=await loader;
  const encodingSource=await readFile(`${__dirname}/audio-encoding.js`,'utf8');
  const {pcmHash}=await import('data:text/javascript;base64,'+Buffer.from(encodingSource).toString('base64'));
  for(const name of ['integer.flac','quiet.wav.gz']) {
    const manifest=JSON.parse(await readFile(`${__dirname}/../../assets/sampler/grand/manifest.json`,'utf8'));
    const encoded=new Uint8Array(await readFile(`${__dirname}/../audioencoding/testdata/${name}`));
    const quiet=name.endsWith('.gz'),frames=quiet?640:896,scale=quiet?1:2**-20;
    const values=quiet?[0,2**-20,-(2**-25),2**-30,-(2**-38)]:[0,1/32768,-1/32768,.25,-.5,32767/32768,-1];
    const pcm=[Float32Array.from({length:frames},(_,i)=>values[i%values.length]*scale)];
    const asset={...manifest.assets[0],id:'fixture',path:'samples/'+name,encoding:quiet?'wav-gzip':'flac',sha256:await sha256(encoded),bytes:encoded.length,pcm_sha256:await pcmHash(pcm),frames,rate:48000,channels:1,scale};
    delete asset.wav_bytes;delete asset.wav_sha256;
    if(quiet){const wav=require('node:zlib').gunzipSync(encoded);asset.wav_bytes=wav.length;asset.wav_sha256=await sha256(wav);}
    manifest.assets=[asset];manifest.zones=[{...manifest.zones[0],asset:'fixture',Start:0,End:0,Loop:false,LoopStart:0,LoopEnd:0,Crossfade:0}];
    const bytes=new TextEncoder().encode(JSON.stringify(manifest)),url='https://example.org/manifest.json';
    const context={sampleRate:48000,async decodeAudioData(){return {length:frames,numberOfChannels:1,sampleRate:48000,getChannelData:()=>Float32Array.from(pcm[0],x=>x/scale)};}};
    const options={context,cache:false,fetch:async requested=>new Response(requested===url?bytes:encoded)};
    const pack=await loadPack(url,await sha256(bytes),options);
    assert.deepEqual(pack.pcm.get('fixture'),pcm);
    asset.pcm_sha256='a'.repeat(64);
    const bad=new TextEncoder().encode(JSON.stringify(manifest));
    await assert.rejects(loadPack(url,await sha256(bad),{...options,fetch:async requested=>new Response(requested===url?bad:encoded)}),/PCM hash/);
  }
});

test('owner recordings retain checksums without invented remote provenance', async () => {
  const {validateManifest}=await loader;
  const manifest=JSON.parse(await readFile(`${__dirname}/../../assets/sampler/grand/manifest.json`,'utf8'));
  for(const asset of manifest.assets) { asset.license='owner recording';asset.source_url='';asset.license_url='';delete asset.attribution; }
  assert.equal(validateManifest(manifest),manifest);
  manifest.assets[0].source_url='https://example.org/foreign.wav';
  assert.throws(()=>validateManifest(manifest),/licence/);
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
