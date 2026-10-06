'use strict';
// Exercise the source and both shipped profiles with the actual reactor.
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');

const [wasmPath, imagePath, pcmPath] = process.argv.slice(2);
const expected = pcmPath ? fs.readFileSync(pcmPath) : null;
if (!wasmPath || !imagePath) throw new Error('usage: node chord_worklet_reactor_test.cjs kernel.wasm poly.image');

async function check(asset, capture, module, image) {
  let type, port, resolveReady, rejectReady;
  const ready = new Promise((resolve, reject) => { resolveReady = resolve; rejectReady = reject; });
  const messages = [];
  const context = vm.createContext({
    CICADA_CAPTURE: capture,
    sampleRate: 48000, performance, Date, ArrayBuffer, Uint8Array, Uint32Array, Float32Array, DataView, WebAssembly,
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
  assert.equal(messages.find(message => message.t === 'r').p, 66559, 'unified/chord capability handshake missing');
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
    if (expected) for (let frame = 0; frame < 128; frame++) for (let channel = 0; channel < 2; channel++) {
      const at = ((block * 128 + frame) * 2 + channel) * 4;
      assert.ok(Math.abs(output[0][channel][frame] - expected.readFloatLE(at)) < 2e-7,
        `native/worklet drift at frame ${block * 128 + frame}, channel ${channel}`);
    }
  }
  assert.ok(nonzero, 'actual chord image rendered only silence');
  assert.ok(!messages.some(message => ['e', 'x', 'f'].includes(message.t)), 'actual worklet faulted');
  console.log(`${asset} (${capture ? 'capture' : 'playback'}): actual reactor initialized and rendered${expected ? ' with native PCM parity' : ' chord audio'}`);
}
(async () => {
  const module = await WebAssembly.compile(fs.readFileSync(wasmPath));
  const bytes = fs.readFileSync(imagePath);
  assert.equal(bytes.readUInt16LE(4), 15, 'test requires a unified chord image');
  const image = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
  for (const [asset, capture] of [['processor.js', false], ['processor.js', true], ['processor.min.js', false], ['processor-capture.min.js', true]]) await check(asset, capture, module, image);
})().catch(error => { console.error(error.stack || error); process.exitCode = 1; });
