'use strict';

const fs = require('node:fs');
const path = require('node:path');
const v8 = require('node:v8');

const [wasmPath, imagePath, captureProfile] = process.argv.slice(2);
let capturePort, capturePending, capturePosts = 0;
if (!global.gc) throw new Error('run Node with --expose-gc');
if (!wasmPath || !imagePath) throw new Error('usage: node --expose-gc processor_alloc_test.js kernel.wasm kernel.image');

let processorType, port, readyResolve, inProcess = false, callback = 0, queuedCount = 0;
let postCount = 0, previousPost = -100, constructorAllocations = 0, stageComplete = false, pooledBuffer;
const pending = new Array(1);
const envelopes = new Map(), transfers = new Map(), slots = new Set();
const ready = new Promise(resolve => { readyResolve = resolve; });

globalThis.sampleRate = 48000;
globalThis.registerProcessor = (_, type) => { processorType = type; };
globalThis.AudioWorkletProcessor = class {
  constructor() {
    this.port = port = {
      onmessage: null,
      postMessage(message, transfer) {
        if (message.t === 'r') readyResolve(message);
        if (message.t === 't') stageComplete = true;
        if (inProcess && message.t === 'm') {
          if (callback - previousPost < 4) throw new Error('more than one meter/playhead post in four callbacks');
          previousPost = callback;
          if (envelopes.has(0) && envelopes.get(0) !== message) throw new Error('the buffer pool allocated a new message envelope');
          if (transfers.has(0) && transfers.get(0) !== transfer) throw new Error('the buffer pool allocated a new transfer list');
          if (pooledBuffer && pooledBuffer !== message.bytes) throw new Error('the buffer pool allocated a new transfer buffer');
          envelopes.set(0, message);
          transfers.set(0, transfer);
          pooledBuffer = message.bytes;
          slots.add(0);
          pending[queuedCount++] = message;
          postCount++;
        }
      }
    };
  }
};

for (const name of ['Uint8Array', 'Float32Array', 'DataView', 'ArrayBuffer']) {
  const Native = globalThis[name];
  globalThis[name] = new Proxy(Native, {
    construct(target, args) {
      if (inProcess) constructorAllocations++;
      return Reflect.construct(target, args, target);
    }
  });
}

if (captureProfile) eval(fs.readFileSync(path.resolve('../../host/web/capture.js'), 'utf8'));
const processorPath = path.resolve(captureProfile ? '../../host/web/processor-capture.min.js' : '../../host/web/processor.min.js');
eval(fs.readFileSync(processorPath, 'utf8'));
if (!processorType) throw new Error('the shipped minified asset did not register a processor');

async function main() {
  const wasmBytes = fs.readFileSync(wasmPath);
  const imageBytes = fs.readFileSync(imagePath);
  const module = await WebAssembly.compile(wasmBytes);
  const image = imageBytes.buffer.slice(imageBytes.byteOffset, imageBytes.byteOffset + imageBytes.byteLength);
  const processor = new processorType({ processorOptions: { m: module, i: image, r: 'allocation-test', l: 10 } });
  await ready;

  const commands = new Uint8Array(48), view = new DataView(commands.buffer);
  view.setUint8(0, 3); view.setUint8(1, 255); view.setUint8(24, 1); view.setUint8(25, 255);
  port.onmessage({ data: { t: 'c', bytes: commands } });
  const output = [[new Float32Array(128), new Float32Array(128)]];
  const input = captureProfile ? [[new Float32Array(128)]] : [];
  if (captureProfile) {
    capturePort = {postMessage(packet, list) {
      if (packet.t === 'pcm') {
        capturePosts++;
        capturePending = structuredClone(packet, {transfer:list});
      }
    }};
    port.onmessage({data:{t:'capture-init',port:capturePort,channels:1,epoch:1}});
    port.onmessage({data:{t:'capture-control',op:'begin',countInFrames:48000}});
  }
  const returnBuffers = () => {
    for (let i = 0; i < queuedCount; i++) {
      const message = pending[i];
      pending[i] = undefined;
      port.onmessage({ data: { t: 'b', bytes: message.bytes } });
    }
    queuedCount = 0;
  };
  const render = () => {
    callback++;
    inProcess = true;
    try { processor.process(input, output); }
    finally { inProcess = false; }
    if (capturePending) {
      capturePending.t = 'recycle';
      capturePort.onmessage({data:capturePending});
      capturePending = null;
    }
    if (queuedCount === pending.length) returnBuffers();
  };

  for (let i = 0; i < 1024; i++) render();
  returnBuffers();
  global.gc();
  global.gc();
  const before = v8.getHeapStatistics().used_heap_size;
  constructorAllocations = 0;
  postCount = 0;
  for (let i = 0; i < 10000; i++) render();
  returnBuffers();
  global.gc();
  global.gc();
  const after = v8.getHeapStatistics().used_heap_size;
  const delta = after - before;
  const steadyConstructorAllocations = constructorAllocations;
  constructorAllocations = 0;
  port.onmessage({ data: { t: 'i', i: image, r: 'allocation-stage' } });
  let stagedCallbacks = 0;
  while (!stageComplete && stagedCallbacks < 10000) {
    await new Promise(resolve => setImmediate(resolve));
    render();
    stagedCallbacks++;
  }
  const stagedConstructorAllocations = constructorAllocations;
  const result = {
    callbacks: 10000,
    captureProfile: !!captureProfile,
    capturePosts,
    postMessages: postCount,
    poolSlotsUsed: slots.size,
    typedConstructorAllocationsInsideProcess: steadyConstructorAllocations,
    stagedBankCopyCallbacks: stagedCallbacks,
    stagedTypedConstructorAllocationsInsideProcess: stagedConstructorAllocations,
    retainedHeapDeltaBytes: delta,
    retainedHeapAllowanceBytes: 65536,
    method: 'Node --expose-gc, V8 used_heap_size before/after full GC across 10,000 callbacks; constructor traps and pooled postMessage identity checks scoped to process()'
  };
  process.stdout.write(JSON.stringify(result) + '\n');
  if (captureProfile && capturePosts < 10000) throw new Error('capture path did not exercise enough callbacks');
  if (postCount < 1000) throw new Error(`message path did not run often enough: ${postCount}`);
  if (slots.size !== 1) throw new Error(`steady-state did not reuse the fixed pool buffer: ${slots.size}`);
  if (steadyConstructorAllocations !== 0) throw new Error(`process() allocated ${steadyConstructorAllocations} typed buffers`);
  if (!stageComplete) throw new Error(`bank copy did not finish after ${stagedCallbacks} callbacks`);
  if (stagedConstructorAllocations !== 0) throw new Error(`process() allocated ${stagedConstructorAllocations} typed buffers during a staged bank copy`);
  if (delta > result.retainedHeapAllowanceBytes) throw new Error(`retained V8 heap grew by ${delta} bytes`);
}

main().catch(error => { process.stderr.write(`${error.stack || error}\n`); process.exitCode = 1; });
