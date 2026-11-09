// Replays a recorded command timeline through GoSX's gosx-wasm-audio processor
// with the real Cicada kernel module, one 128-frame render quantum at a time,
// and writes the planar float32 output. sdk/gosxhost's parity test compares it
// with the native engine's output for the same timeline.
//
// usage: node worklet-replay.mjs <processor.js> <kernel.wasm> <timeline.json> <out.json>
//
// The processor runs inside node:vm with the globals an AudioWorkletGlobalScope
// gives it (sampleRate, registerProcessor, AudioWorkletProcessor), so this
// exercises the processor source and the kernel's WASM, but not a browser's
// audio thread.
import { readFileSync, writeFileSync } from 'node:fs';
import vm from 'node:vm';

const [processorPath, kernelPath, timelinePath, outPath] = process.argv.slice(2);
if (!outPath) {
  console.error('usage: node worklet-replay.mjs <processor.js> <kernel.wasm> <timeline.json> <out.json>');
  process.exit(2);
}
const timeline = JSON.parse(readFileSync(timelinePath, 'utf8'));
const source = readFileSync(processorPath, 'utf8');
const kernel = await WebAssembly.compile(readFileSync(kernelPath));
const image = Uint8Array.from(Buffer.from(timeline.image, 'base64'));

const posted = [];
const port = {
  onmessage: null,
  postMessage(message, transfer = []) { posted.push(structuredClone(message, { transfer })); },
  close() {},
};
let Processor;
const context = {
  sampleRate: timeline.rate, performance, WebAssembly, Uint8Array, Uint32Array, Float32Array, ArrayBuffer,
  DataView, Date, Math, Number, Error, Promise, String, Infinity,
  AudioWorkletProcessor: class { constructor() { this.port = port; } },
  registerProcessor(name, cls) {
    if (name !== 'gosx-wasm-audio') throw new Error('unexpected processor ' + name);
    Processor = cls;
  },
};
vm.runInNewContext(source, context);
const node = new Processor({ processorOptions: {
  m: kernel, i: image.buffer.slice(image.byteOffset, image.byteOffset + image.byteLength), r: 'replay', l: 0,
  backend: timeline.backend, info: { tempoMilli: timeline.tempoMilli, requiredCapabilities: timeline.requiredCapabilities },
} });
for (let i = 0; i < 200 && !posted.some(m => m.t === 'r' || m.t === 'e'); i++) await new Promise(r => setImmediate(r));
const failure = posted.find(m => m.t === 'e');
if (failure) throw new Error('processor failed to start: ' + failure.e);
if (!posted.some(m => m.t === 'r')) throw new Error('processor never became ready');

const frames = 128, quanta = timeline.quanta;
const left = new Float32Array(quanta * frames), right = new Float32Array(quanta * frames);
const l = new Float32Array(frames), r = new Float32Array(frames);
const durations = new Float64Array(quanta);
const records = [];
let read = 0;
const batches = new Map();
for (const b of timeline.batches) batches.set(b.q, [...(batches.get(b.q) || []), Uint8Array.from(Buffer.from(b.b, 'base64'))]);
const takeMessages = () => {
  for (; read < posted.length; read++) {
    const m = posted[read];
    if (m.t === 'f') throw new Error('kernel fault ' + m.a);
    if (m.t === 'x' || m.t === 'e') throw new Error('processor error: ' + (m.e || m.r));
    if (m.t !== 'm') continue;
    records.push(Buffer.from(new Uint8Array(m.bytes, 0, m.n)).toString('base64'));
    port.onmessage({ data: { t: 'b', bytes: m.bytes } });
  }
};
for (let q = 0; q < quanta; q++) {
  for (const bytes of batches.get(q) || []) port.onmessage({ data: { t: 'c', bytes } });
  const t0 = performance.now();
  const alive = node.process([], [[l, r]]);
  durations[q] = performance.now() - t0;
  if (!alive) throw new Error('processor stopped at quantum ' + q);
  left.set(l, q * frames);
  right.set(r, q * frames);
  takeMessages();
}
// The processor drains every eighth quantum; render a few more quanta so the
// last batch is delivered, without recording their audio.
for (let q = 0; q < 16; q++) { node.process([], [[l, r]]); takeMessages(); }

// Statistics in microseconds. "steady" skips the first 500 quanta (1.3 s at
// 48 kHz), where V8 is still compiling the kernel.
const stats = from => {
  const sorted = Float64Array.from(durations.subarray(from)).sort();
  const pct = p => sorted[Math.min(sorted.length - 1, Math.floor(p * sorted.length))];
  return { quanta: sorted.length, mean: sorted.reduce((a, b) => a + b, 0) / sorted.length * 1000,
    p50: pct(.5) * 1000, p99: pct(.99) * 1000, max: sorted[sorted.length - 1] * 1000 };
};
writeFileSync(outPath, JSON.stringify({
  left: Buffer.from(left.buffer).toString('base64'),
  right: Buffer.from(right.buffer).toString('base64'),
  messages: records,
  cpuMicros: { all: stats(0), steady: stats(Math.min(500, quanta >> 1)) },
  postedTypes: [...new Set(posted.map(m => m.t))],
}));
