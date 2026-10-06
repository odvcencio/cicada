'use strict';
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const [wasmPath, fixture] = process.argv.slice(2);

async function check(asset, capture, module) {
  let type, port, exports, resolveReady, rejectReady;
  const ready = new Promise((resolve, reject) => { resolveReady = resolve; rejectReady = reject; });
  const context = vm.createContext({
    CICADA_CAPTURE: capture, sampleRate: 48000, performance, Date,
    ArrayBuffer, Uint8Array, Uint32Array, Float32Array, DataView,
    WebAssembly: { instantiate: async m => { const instance = await WebAssembly.instantiate(m); exports = instance.exports; return instance; } },
    AudioWorkletProcessor: class {
      constructor() {
        this.port = port = { onmessage: null, postMessage(message) {
          if (message.t === 'r') resolveReady(message);
          if (['e', 'x', 'f'].includes(message.t)) rejectReady(new Error(message.e || `fault ${message.a}`));
          if (message.t === 'm') {
            const view = new DataView(message.bytes);
            for (let at = 0; at < message.n; at += 16) assert.notEqual(view.getUint8(at), 7, 'spatial kernel fault');
            port.onmessage({ data: { t: 'b', bytes: message.bytes } });
          }
        } };
      }
    },
    registerProcessor: (_, candidate) => { type = candidate; }
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, asset), 'utf8'), context);
  const bytes = fs.readFileSync(path.join(fixture, 'image'));
  const image = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
  const processor = new type({ processorOptions: { m: module, i: image } });
  const info = await ready;
  assert.ok(info.p & 131072, 'missing spatial capability');
  const beforeCount = exports.gosx_audio_allocation_count(), beforeBytes = exports.gosx_audio_alloc_bytes();
  const beforeMemory = exports.memory.buffer.byteLength;
  port.onmessage({ data: { t: 'c', bytes: new Uint8Array(fs.readFileSync(path.join(fixture, 'commands'))) } });
  const pcm = fs.readFileSync(path.join(fixture, 'pcm'));
  const output = [[new Float32Array(128), new Float32Array(128)]];
  let nonzero = false;
  for (let at = 0; at < pcm.length / 8; at += 128) {
    assert.equal(processor.process([], output), true);
    for (let frame = 0; frame < 128; frame++) for (let channel = 0; channel < 2; channel++) {
      const index = at + frame;
      const fade = index < 240 ? (index + 1) / 240 : 1;
      const expected = Math.fround(pcm.readFloatLE(index * 8 + channel * 4) * fade);
      assert.equal(output[0][channel][frame], expected, `${asset}: spatial PCM differs at frame ${index} channel ${channel}`);
      nonzero ||= expected !== 0;
    }
  }
  assert.ok(nonzero);
  assert.equal(exports.gosx_audio_allocation_count(), beforeCount, 'spatial callback allocated');
  assert.equal(exports.gosx_audio_alloc_bytes(), beforeBytes, 'spatial callback allocated bytes');
  assert.equal(exports.memory.buffer.byteLength, beforeMemory, 'spatial callback grew memory');
  console.log(`${asset} capture=${capture}: frames=8192 sample_mismatches=0 new_kernel_allocations=0 memory_growth=0`);
}
(async () => {
  const module = await WebAssembly.compile(fs.readFileSync(wasmPath));
  for (const [asset, capture] of [['processor.js', false], ['processor.js', true], ['processor.min.js', false], ['processor-capture.min.js', true]]) await check(asset, capture, module);
})().catch(error => { console.error(error.stack || error); process.exitCode = 1; });
