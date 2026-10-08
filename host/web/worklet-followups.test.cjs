'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const settle = () => new Promise(resolve => setImmediate(resolve));
const image = () => {
  const bytes = new Uint8Array(32);
  bytes.set([67, 73, 67, 49, 15]);
  new DataView(bytes.buffer).setUint32(12, 120000, true);
  return bytes.buffer;
};
const commands = records => {
  const bytes = new Uint8Array(records.length * 24), view = new DataView(bytes.buffer);
  records.forEach((record, i) => {
    bytes[i * 24] = record.op; bytes[i * 24 + 1] = 255;
    view.setUint32(i * 24 + 4, record.bar || 0, true);
    view.setUint32(i * 24 + 8, record.tick || 0, true);
  });
  return bytes;
};
async function setup(asset, hooks = false) {
  let Type, port, now = 1, failNext = false;
  const messages = [], instances = [];
  const context = vm.createContext({
    CICADA_CAPTURE: false, CICADA_TEST: hooks, sampleRate: 48000,
    performance: {now: () => now}, Date,
    WebAssembly: {instantiate: async () => {
      const fail = failNext; failNext = false;
      const instance = {memory: {buffer: new ArrayBuffer(65536)}, commits: [], renders: 0, fault: 0};
      const x = {
        memory: instance.memory, _initialize() {}, gosx_audio_capabilities: () => 65537,
        gosx_audio_project_alloc: () => 4096, gosx_audio_init: () => fail ? 1 : 0,
        gosx_audio_bank_alloc: () => 1024, gosx_audio_bank_install: () => 0,
        gosx_audio_bank_image_ptr: () => 1024, gosx_audio_bank_image_len: () => 16,
        gosx_audio_cmd_cap: () => 512, gosx_audio_cmd_ptr: () => 8192,
        gosx_audio_msg_ptr: () => 32768, gosx_audio_out_ptr: () => 49152,
        gosx_audio_cmd_commit: count => instance.commits.push(new Uint8Array(instance.memory.buffer, 8192, count * 24).slice()),
        gosx_audio_render() {
          instance.renders++;
          new Float32Array(instance.memory.buffer, 49152, 256).fill(1);
        },
        gosx_audio_msg_drain() {
          if (!instance.fault) return 0;
          // Fault is the second message: fallback must report its code.
          const view = new DataView(instance.memory.buffer, 32768, 32);
          view.setUint8(0, 1); view.setUint16(2, 99, true);
          view.setUint8(16, 7); view.setUint16(18, instance.fault, true);
          instance.fault = 0;
          return 2;
        }
      };
      instances.push(instance);
      return {exports: x};
    }},
    AudioWorkletProcessor: class {constructor() {this.port = port = {postMessage: data => messages.push(data)};}},
    registerProcessor: (_, type) => {Type = type;}
  });
  vm.runInContext(fs.readFileSync(`${__dirname}/${asset}`, 'utf8'), context);
  const processor = new Type({processorOptions: {m: {}, i: image(), r: 'initial'}});
  await settle();
  const output = [[new Float32Array(128), new Float32Array(128)]];
  const render = (count = 1) => {for (let i = 0; i < count; i++) {now += 128000 / 48000; processor.process([], output);}};
  const send = records => port.onmessage({data: {t: 'c', bytes: commands(records)}});
  const metrics = () => {port.onmessage({data: {t: 'q', l: 10}}); return messages.at(-1);};
  const beginStage = async () => {port.onmessage({data: {t: 'i', i: image(), r: `stage-${instances.length}`}}); await settle();};
  const stage = async () => {await beginStage(); render(); await settle();};
  return {instances, messages, output, render, send, metrics, beginStage, stage, port, failNext: () => {failNext = true;}};
}

for (const asset of ['processor.js', 'processor.min.js', 'processor-capture.min.js']) {
  test(`${asset}: seek-and-start immediately promotes the staged score`, async () => {
    const h = await setup(asset);
    h.send([{op: 1}]); await h.stage();
    const oldRenders = h.instances[0].renders;
    h.send([{op: 3, bar: 5, tick: 240}, {op: 1}]);
    h.render();
    assert.equal(h.instances[0].renders, oldRenders);
    assert.equal(h.instances[1].renders, 1);
    const bytes = h.instances[1].commits.at(-1), view = new DataView(bytes.buffer);
    assert.equal(bytes[0], 3); assert.equal(view.getUint32(4, true), 5); assert.equal(view.getUint32(8, true), 240);
    assert.equal(bytes[24], 1);
    assert.equal(h.metrics().n, 1);
  });

  for (const ops of [[2, 3], [3, 2]]) {
    test(`${asset}: Stop/seek ${ops} retains seek anchors and commands`, async () => {
      const h = await setup(asset);
      h.send([{op: 1}]); await h.stage();
      h.send(ops.map(op => ({op, bar: op === 3 ? 1118482 : 0, tick: op === 3 ? 240 : 0})));
      const batch = h.instances[1].commits.at(-1);
      assert.deepEqual(Array.from(batch.filter((_, i) => i % 24 === 0)), ops);
      h.send([{op: 9}]);
      const view = new DataView(h.instances[1].commits.at(-1).buffer);
      assert.equal(view.getBigUint64(16, true), BigInt(1118483 * 3840));
      assert.equal(h.messages.filter(m => m.t === 's').at(-1).p, false);
    });
  }

  test(`${asset}: memory includes loading, staged, crossfade and active instances`, async () => {
    const h = await setup(asset);
    h.send([{op: 1}]);
    await h.beginStage(); assert.equal(h.metrics().m, 2 * 65536);
    h.render(); await settle(); assert.equal(h.metrics().n, 2);
    // Seek just before a boundary without starting: exercise the bar swap/fade.
    h.send([{op: 3, tick: 3839}]); h.render();
    assert.equal(h.metrics().n, 2, 'crossfade instance must still be counted');
    await h.stage(); assert.equal(h.metrics().n, 3);
    await h.beginStage(); assert.equal(h.metrics().n, 4);
    h.render(); await settle();
    assert.ok(h.metrics().n <= 3, 'replaced staged instance was retained');
    assert.equal(h.metrics().mp, 4 * 65536);
    h.render(4); h.send([{op: 2}]);
    assert.equal(h.metrics().n, 1);
    h.failNext(); await h.stage();
    assert.equal(h.metrics().n, 1, 'failed instance was retained');
  });

  test(`${asset}: fault posts stopped state, silence and a clear command error`, async () => {
    const h = await setup(asset);
    h.send([{op: 1}]); h.instances[0].fault = 20; h.render(8);
    assert.equal(h.messages.at(-1).t, 's'); assert.equal(h.messages.at(-1).p, false);
    assert.ok(h.output[0].every(channel => channel.every(sample => sample === 0)));
    assert.equal(h.metrics().n, 0);
    h.send([{op: 1}]); assert.match(h.messages.at(-1).e, /Reload audio/);
    await h.stage(); h.send([{op: 1}]); h.render();
    assert.equal(h.instances[1].renders, 1, 'reloading did not recover playback');
  });

  test(`${asset}: production stall injection is absent or ignored`, async () => {
    const h = await setup(asset);
    h.port.onmessage({data: {t: 'z'}}); h.render();
    assert.equal(h.metrics().u, 0);
    if (asset !== 'processor.js') assert.doesNotMatch(fs.readFileSync(`${__dirname}/${asset}`, 'utf8'), /["']z["']|Date\.now\(\)\s*\+\s*20/);
  });

  test(`${asset}: a fading instance fault reports its own code and stops playback`, async () => {
    const h = await setup(asset);
    h.send([{op: 1}]); h.render(6); await h.stage();
    h.instances[0].fault = 20; h.send([{op: 3, tick: 3839}]); h.render();
    assert.equal(h.messages.find(message => message.t === 'f')?.a, 20);
    assert.equal(h.messages.at(-1).t, 's'); assert.equal(h.messages.at(-1).p, false);
    assert.equal(h.metrics().n, 0);
    assert.ok(h.output[0].every(channel => channel.every(sample => sample === 0)));
  });

  test(`${asset}: seek-and-start releases a fading instance without a staged replacement`, async () => {
    const h = await setup(asset);
    h.send([{op: 1}]); await h.stage();
    h.send([{op: 3, tick: 3839}]); h.render(); assert.equal(h.metrics().n, 2);
    h.send([{op: 3, bar: 2}, {op: 1}]); assert.equal(h.metrics().n, 1);
  });
}

test('test processor retains stall injection', async () => {
  const h = await setup('processor.js', true);
  const started = performance.now(); h.port.onmessage({data: {t: 'z'}}); h.render();
  assert.ok(performance.now() - started >= 18);
});
