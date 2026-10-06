'use strict';
// Exercise both worklet assets with the actual reactor, not mocked exports.
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');

const [wasmPath, imagePath] = process.argv.slice(2);
if (!wasmPath || !imagePath) throw new Error('usage: node chord_worklet_reactor_test.cjs kernel.wasm poly.image');

async function check(asset, module, image) {
  let type, port, resolveReady, rejectReady;
  const ready = new Promise((resolve, reject) => { resolveReady = resolve; rejectReady = reject; });
  const messages = [];
  const context = vm.createContext({
    CICADA_CAPTURE: false, sampleRate: 48000, performance, Date, ArrayBuffer, Uint8Array, Uint32Array, Float32Array, DataView, WebAssembly,
    AudioWorkletProcessor: class {
      constructor() {
        this.port = port = { onmessage: null, postMessage(message) {
          messages.push(message);
          if (message.t === 'r') resolveReady(message);
          if (message.t === 'e' || message.t === 'x') rejectReady(new Error(message.e));
        } };
      }
    },
    registerProcessor: (_, candidate) => { type = candidate; }
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, asset), 'utf8'), context);
  assert.ok(type, 'worklet was not registered');
  const processor = new type({ processorOptions: { m: module, i: image.slice(0), r: 'actual-chord-reactor' } });
  let timeout;
  try {
    await Promise.race([ready, new Promise((_, reject) => {
      timeout = setTimeout(() => reject(new Error('actual worklet did not become ready')), 30000);
    })]);
  } finally { clearTimeout(timeout); }
  const play = new Uint8Array(24); play[0] = 1; play[1] = 255;
  port.onmessage({ data: { t: 'c', bytes: play } });
  const output = [[new Float32Array(128), new Float32Array(128)]];
  let nonzero = false;
  for (let block = 0; block < 32; block++) {
    assert.equal(processor.process([], output), true);
    for (const channel of output[0]) for (const sample of channel) {
      assert.ok(Number.isFinite(sample), 'non-finite worklet output');
      nonzero ||= sample !== 0;
    }
  }
  assert.ok(nonzero, 'actual chord image rendered only silence');
  assert.ok(!messages.some(message => ['e', 'x', 'f'].includes(message.t)), 'actual worklet faulted');
  console.log(`${asset}: actual reactor initialized, accepted image16 and rendered chord audio`);
}
(async () => {
  const module = await WebAssembly.compile(fs.readFileSync(wasmPath));
  const bytes = fs.readFileSync(imagePath);
  assert.equal(bytes.readUInt16LE(4), 16, 'test requires an opt-in chord image');
  const image = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
  for (const asset of ['processor.js', 'processor.min.js']) await check(asset, module, image);
})().catch(error => { console.error(error.stack || error); process.exitCode = 1; });
