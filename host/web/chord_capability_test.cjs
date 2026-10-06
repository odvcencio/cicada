'use strict';
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');

const settle = () => new Promise(resolve => setImmediate(resolve));
const image = (version = 13) => {
  const bytes = new Uint8Array(32);
  bytes.set([67, 73, 67, 49]);
  const view = new DataView(bytes.buffer);
  view.setUint16(4, version, true);
  view.setUint32(12, 120000, true);
  return bytes.buffer;
};
const commands = (...ops) => {
  const bytes = new Uint8Array(ops.length * 24);
  ops.forEach((op, i) => { bytes[i * 24] = op; bytes[i * 24 + 1] = 255; });
  return bytes;
};

async function setup(asset, capture, version, capability, options = {}) {
  let type, port;
  const messages = [], instances = [];
  const counts = { commits: 0, projectAllocs: 0, renders: 0, initializations: 0 };
  const context = vm.createContext({
    CICADA_CAPTURE: capture,
    sampleRate: 48000, performance, Date, ArrayBuffer, Uint8Array, Uint32Array, Float32Array, DataView,
    WebAssembly: { instantiate: async () => {
      let initialized = false;
      const exports = {
        memory: { buffer: new ArrayBuffer(65536) },
        _initialize() { assert.equal(initialized, false); initialized = true; counts.initializations++; },
        gosx_audio_project_alloc() { counts.projectAllocs++; return 4096; },
        gosx_audio_bank_alloc() { return 1024; }, gosx_audio_bank_install() { return 0; },
        gosx_audio_init() { return 0; }, gosx_audio_bank_image_ptr() { return 1024; },
        gosx_audio_bank_image_len() { return 16; }, gosx_audio_cmd_cap() { return 512; },
        gosx_audio_cmd_ptr() { return 8192; }, gosx_audio_msg_ptr() { return 32768; },
        gosx_audio_out_ptr() { return 49152; }, gosx_audio_cmd_commit() { counts.commits++; },
        gosx_audio_msg_drain() { return 0; }, gosx_audio_render() { counts.renders++; exports.renders++; }, renders: 0
      };
      if (capability !== null) exports.gosx_audio_capabilities = () => capability;
      if (options.malformedExport) exports.gosx_audio_capabilities = 3;
      for (const name of Object.keys(exports)) {
        if (name === '_initialize' || typeof exports[name] !== 'function') continue;
        const fn = exports[name];
        exports[name] = (...args) => { assert.ok(initialized, `${name} called before reactor initialization`); return fn(...args); };
      }
      if (options.missingInitializer) delete exports._initialize;
      if (options.throwInitializer) exports._initialize = () => { throw new Error('reactor initialization failed'); };
      instances.push(exports);
      return { exports };
    } },
    AudioWorkletProcessor: class { constructor() { this.port = port = { postMessage: data => messages.push(data), onmessage: null }; } },
    registerProcessor: (_, candidate) => { type = candidate; }
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, asset), 'utf8'), context);
  const processor = new type({ processorOptions: { m: {}, i: options.image || image(version), r: 'test' } });
  await settle();
  return { processor, port, messages, counts, instances,
    send: bytes => port.onmessage({ data: { t: 'c', bytes } }),
    stage: async bytes => { port.onmessage({ data: { t: 'i', i: bytes, r: 'rejected' } }); await settle(); },
    render: () => processor.process([], [[new Float32Array(128), new Float32Array(128)]])
  };
}

(async () => {
  const profiles = [['processor.js', false], ['processor.js', true]];
  if (!process.argv.includes('--source-only')) profiles.push(['processor.min.js', false], ['processor-capture.min.js', true]);
  for (const [asset, capture] of profiles) {
    for (const version of [8, 9, 10, 11, 12, 13]) {
      for (const capability of [null, 0, 1, 2, 3]) {
        const legacy = await setup(asset, capture, version, capability);
        assert.ok(legacy.messages.some(m => m.t === 'r' && m.p === (capability || 0)), `legacy ${version}, capability ${capability} rejected`);
        assert.equal(legacy.counts.initializations, 1);
      }
    }
    for (const capability of [null, 0, 1, 2, 3]) {
      const old = await setup(asset, capture, 14, capability);
      assert.match(old.messages.find(m => m.t === 'e')?.e || '', /Ambiguous.*image14.*recompile/);
      assert.equal(old.instances.length, 0, 'ambiguous image instantiated a kernel');
    }
    for (const version of [0, 7, 16, 255, 65535]) {
      const unknown = await setup(asset, capture, version, 3);
      assert.match(unknown.messages.find(m => m.t === 'e')?.e || '', /Unsupported image version/);
      assert.equal(unknown.instances.length, 0);
    }
    for (const capability of [null, 0, 1, 2, 3]) {
      const old = await setup(asset, capture, 15, capability);
      assert.match(old.messages.find(m => m.t === 'e')?.e || '', /image15.*CapabilityUnifiedImage/);
      assert.equal(old.counts.projectAllocs, 0, 'unnegotiated image mutated old kernel');
      assert.equal(old.counts.initializations, 1);
    }
    for (const capability of [65536, 65537, 65911, 66047, 197119]) {
      const unified = await setup(asset, capture, 15, capability);
      assert.ok(unified.messages.some(m => m.t === 'r' && m.p === capability));
    }
    for (const capability of [undefined, -1, 1.5, NaN, Infinity, 512, 1024, 0x100000000]) {
      const malformed = await setup(asset, capture, 13, capability);
      assert.match(malformed.messages.find(m => m.t === 'e')?.e || '', /Invalid capabilities/);
      assert.equal(malformed.counts.projectAllocs, 0);
    }
    for (const [options, diagnostic] of [
      [{ missingInitializer: true }, /[Rr]eactor initializer missing/],
      [{ throwInitializer: true }, /reactor initialization failed/],
      [{ malformedExport: true }, /Invalid capability export/],
      [{ image: new ArrayBuffer(5) }, /Invalid CIC1 image header/],
      [{ image: new ArrayBuffer(32) }, /Invalid CIC1 image header/]
    ]) {
      const malformed = await setup(asset, capture, 13, 3, options);
      assert.match(malformed.messages.find(m => m.t === 'e')?.e || '', diagnostic);
      assert.equal(malformed.counts.projectAllocs, 0);
    }
    for (const capability of [null, 0, 2]) {
      const mono = await setup(asset, capture, 13, capability);
      mono.send(commands(1));
      mono.render();
      const before = { ...mono.counts };
      const memory = new Uint8Array(mono.instances[0].memory.buffer).slice();
      mono.send(commands(2, 22));
      assert.match(mono.messages.find(m => m.t === 'x')?.e || '', /chord opcode22/);
      assert.equal(mono.counts.commits, before.commits, 'unsupported opcode partly committed batch');
      assert.deepEqual(new Uint8Array(mono.instances[0].memory.buffer), memory, 'rejected batch changed command memory');
      mono.render();
      assert.equal(mono.counts.renders, before.renders + 1, 'rejected batch stopped active playback');
      await mono.stage(image(14));
      assert.match(mono.messages.find(m => m.t === 'x' && m.r === 'rejected')?.e || '', /Ambiguous.*image14/);
      mono.render();
      assert.equal(mono.counts.renders, before.renders + 2, 'rejected load stopped active playback');
      assert.equal(mono.counts.projectAllocs, before.projectAllocs, 'rejected load uploaded image');
    }
    const noSpatial = await setup(asset, capture, 15, 66047);
    const spatialMemory = new Uint8Array(noSpatial.instances[0].memory.buffer).slice();
    for (const op of [26, 27, 28]) {
      noSpatial.send(commands(1, op));
      assert.match(noSpatial.messages.at(-1).e, /spatial/);
      assert.equal(noSpatial.counts.commits, 0);
      assert.deepEqual(new Uint8Array(noSpatial.instances[0].memory.buffer), spatialMemory);
    }
    const spatial = await setup(asset, capture, 15, 197119);
    spatial.send(commands(26, 27, 28));
    assert.equal(spatial.counts.commits, 1);
    const staged = await setup(asset, capture, 15, 65537);
    staged.send(commands(1));
    await staged.stage(image(15));
    // The bank copy is deliberately spread across process callbacks. Another
    // load must not steal that copy or leave its staging promise unresolved.
    staged.port.onmessage({ data: { t: 'i', i: image(15), r: 'concurrent' } });
    await settle();
    assert.match(staged.messages.find(m => m.r === 'concurrent')?.e || '', /Image load in progress/);
    staged.render();
    await settle();
    assert.ok(staged.messages.some(m => m.t === 't'));
    assert.equal(staged.instances.length, 2);
    // Rejection must also preserve an already prepared replacement.
    await staged.stage(image(14));
    staged.send(commands(2));
    staged.send(commands(1));
    const oldRenders = staged.instances[0].renders;
    staged.render();
    assert.equal(staged.instances[0].renders, oldRenders, 'rejected load discarded the previously prepared replacement');
    assert.equal(staged.instances[1].renders, 1, 'Stop did not promote the prepared replacement');

    const overflow = await setup(asset, capture, 15, 65537);
    overflow.send(commands(1));
    await overflow.stage(image(15));
    overflow.send(commands(...Array(512).fill(9)));
    const committed = overflow.counts.commits;
    const beforeOverflow = new Uint8Array(overflow.instances[0].memory.buffer).slice();
    overflow.send(commands(2, 9));
    assert.match(overflow.messages.find(m => m.t === 'x')?.e || '', /cmd/);
    assert.equal(overflow.counts.commits, committed, 'overflow partly committed a batch');
    assert.deepEqual(new Uint8Array(overflow.instances[0].memory.buffer), beforeOverflow);
    overflow.render();
    assert.equal(overflow.instances[0].renders, 1, 'overflow stopped playback');
    await settle();

    const poly = await setup(asset, capture, 15, 65537);
    poly.send(commands(22));
    assert.equal(poly.counts.commits, 1);
    const seek = commands(3), seekView = new DataView(seek.buffer);
    seekView.setUint32(4, 1118482, true);
    seekView.setUint32(8, 1, true);
    poly.send(seek);
    poly.send(commands(9));
    assert.equal(new DataView(poly.instances[0].memory.buffer).getBigUint64(8192 + 16, true), BigInt(1118483 * 3840), 'quantized tick lost its high 32 bits');
    const oldChord = await setup(asset, capture, 13, 1);
    oldChord.send(commands(22));
    assert.equal(oldChord.counts.commits, 1, 'legacy chord command capability was redefined');
    console.log(`${asset} capture=${capture}: initialization, version/capability allowlists, atomic rejection and legacy command negotiation passed`);
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
