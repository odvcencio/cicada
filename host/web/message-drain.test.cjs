'use strict';
// The kernel drains up to engine.MessageCapacity (512) messages in one call. The
// worklet must carry all of them to the client, 16 bytes each.
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const settle = () => new Promise(resolve => setImmediate(resolve));
const MESSAGES = 512;
const image = () => {
  const bytes = new Uint8Array(32);
  bytes.set([67, 73, 67, 49, 15]);
  new DataView(bytes.buffer).setUint32(12, 120000, true);
  return bytes.buffer;
};

async function worklet(asset, drained) {
  let Type, port, now = 1;
  const posted = [];
  const memory = {buffer: new ArrayBuffer(1 << 20)};
  const context = vm.createContext({
    CICADA_CAPTURE: false, CICADA_TEST: false, sampleRate: 48000,
    performance: {now: () => now}, Date,
    WebAssembly: {instantiate: async () => ({exports: {
      memory, _initialize() {}, gosx_audio_capabilities: () => 65537,
      gosx_audio_project_alloc: () => 4096, gosx_audio_init: () => 0,
      gosx_audio_bank_alloc: () => 1024, gosx_audio_bank_install: () => 0,
      gosx_audio_bank_image_ptr: () => 1024, gosx_audio_bank_image_len: () => 16,
      gosx_audio_cmd_cap: () => 512, gosx_audio_cmd_ptr: () => 8192, gosx_audio_cmd_commit() {},
      gosx_audio_msg_ptr: () => 32768, gosx_audio_out_ptr: () => 262144,
      gosx_audio_tick_frames: (from, to) => Math.ceil((to - from) * 60000 * 48000 / (120000 * 960)),
      gosx_audio_render() {},
      gosx_audio_msg_drain() {
        // Message i is a Playhead (kind 1) at tick i.
        const view = new DataView(memory.buffer, 32768, drained * 16);
        for (let i = 0; i < drained; i++) { view.setUint8(i * 16, 1); view.setBigInt64(i * 16 + 8, BigInt(i), true); }
        return drained;
      }
    }})},
    AudioWorkletProcessor: class {constructor() {this.port = port = {postMessage: data => posted.push(data)};}},
    registerProcessor: (_, type) => {Type = type;}
  });
  vm.runInContext(fs.readFileSync(`${__dirname}/${asset}`, 'utf8'), context);
  const processor = new Type({processorOptions: {m: {}, i: image(), r: 'initial'}});
  await settle();
  const commands = new Uint8Array(24); commands[0] = 1; commands[1] = 255; // play
  port.onmessage({data: {t: 'c', bytes: commands}});
  const output = [[new Float32Array(128), new Float32Array(128)]];
  for (let i = 0; i < 16; i++) { now += 128000 / 48000; processor.process([], output); }
  return posted.filter(m => m.t === 'm');
}

function client() {
  const received = [], errors = [];
  const env = vm.createContext({
    window: {AudioContext: class {constructor() {this.audioWorklet = {addModule: async () => {}};}}}, performance, setTimeout, clearTimeout,
    setInterval: () => 0, clearInterval() {}, requestAnimationFrame: () => 1, cancelAnimationFrame() {},
    WebAssembly: {compile: async () => ({})}, fetch: async () => ({ok: true, headers: {get: () => 'r'}, arrayBuffer: async () => image()}),
    AudioWorkletNode: class {constructor() {this.port = {postMessage() {}, close() {}};} connect() {} disconnect() {}}
  });
  vm.runInContext(fs.readFileSync(`${__dirname}/client.js`, 'utf8'), env);
  const audio = env.window.cicadaBrowserAudio;
  audio.node = {port: {postMessage() {}}};
  audio.onError(error => errors.push(error));
  audio.onMessage(message => received.push(message));
  return {audio, received, errors};
}

for (const asset of ['processor.js', 'processor.min.js', 'processor-capture.min.js']) {
  for (const count of [300, MESSAGES]) {
    test(`${asset}: ${count} drained messages reach the client intact`, async () => {
      const posts = await worklet(asset, count);
      assert.ok(posts.length >= 1, 'the worklet posted no message block');
      const block = posts[0];
      assert.equal(block.n, count * 16);
      assert.ok(block.bytes.byteLength >= block.n, `transfer buffer holds ${block.bytes.byteLength} bytes for ${block.n}`);
      const view = new DataView(block.bytes, 0, block.n);
      assert.equal(Number(view.getBigInt64((count - 1) * 16 + 8, true)), count - 1, 'the last message was not copied');
      const c = client();
      c.audio.receive(block);
      assert.deepEqual(c.errors, []);
      assert.equal(c.received.length, count);
      assert.equal(c.received.at(-1).tick, count - 1);
    });
  }
}

test('processor.js keeps its message buffer equal to the kernel message capacity', () => {
  const source = fs.readFileSync(`${__dirname}/processor.js`, 'utf8');
  assert.match(source, new RegExp(`MESSAGE_BYTES = ${MESSAGES} \\* 16`));
});
