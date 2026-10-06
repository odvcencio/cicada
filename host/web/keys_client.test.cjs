'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const {BrowserCapture} = require('./capture-client.js');

function scoreImage(keys) {
  const image = new ArrayBuffer(32), view = new DataView(image);
  new Uint8Array(image).set([67, 73, 67, 49]);
  view.setUint16(4, 13, true);
  view.setUint32(12, 120000, true);
  view.setUint16(30, keys ? 32 : 0, true);
  return image;
}

function setup(initialKeys = false) {
  const requests = [], nodes = [], contexts = [], commands = [], captureActions = [];
  let keys = initialKeys, revision = 'one', failFetch = false, failInit = false, compilations = 0, capture;
  class Context {
    constructor() {
      contexts.push(this); this.sampleRate = 48000; this.currentTime = 0; this.state = 'running'; this.closed = false;
      this.destination = {}; this.audioWorklet = {addModule: async () => {}};
    }
    async resume() { this.state = 'running'; }
    async close() { this.closed = true; }
  }
  class WorkletNode {
    constructor(context, _, options) {
      this.context = context; this.options = options; this.connected = false;
      nodes.push(this);
      this.port = {
        closed: false, onmessage: null, onmessageerror: null,
        close() { this.closed = true; },
        postMessage: data => {
          if (data.t === 'i') queueMicrotask(() => this.port.onmessage?.({data: {t: 't', r: data.r}}));
          if (data.t === 'c') {
            const view = new DataView(data.bytes.buffer, data.bytes.byteOffset, data.bytes.byteLength);
            for (let at = 0; at < data.bytes.byteLength; at += 24) {
              const record = {op: view.getUint8(at), arg0: view.getUint32(at + 4, true), arg1: view.getUint32(at + 8, true)};
              commands.push({node: this, ...record});
              if (record.op === 1 || record.op === 2) queueMicrotask(() => this.port.onmessage?.({data: {t: 's', p: record.op === 1}}));
            }
          }
          if (data.t === 'capture-control' && data.op === 'stop') {
            captureActions.push('stop');
            assert.ok(this.connected, 'capture stopped after retiring its node');
            queueMicrotask(() => capture.receive({t: 'finished', error: '', incomplete: false}));
          }
        }
      };
      queueMicrotask(() => this.port.onmessage?.({data: failInit && options.processorOptions.m.kind === 'keys'
        ? {t: 'e', e: 'optional kernel initialization failed'}
        : {t: 'r', r: options.processorOptions.r, c: true}}));
    }
    connect() { this.connected = true; }
    disconnect() { this.connected = false; }
  }
  const context = vm.createContext({
    window: {AudioContext: Context}, AudioWorkletNode: WorkletNode,
    ArrayBuffer, Uint8Array, Uint32Array, DataView, Set, Map, Error, Promise,
    performance, setTimeout, clearTimeout, setInterval: () => 1,
    requestAnimationFrame: callback => { queueMicrotask(callback); return 1; },
    WebAssembly: {compile: async bytes => { compilations++; return {kind: new Uint8Array(bytes)[0] === 2 ? 'keys' : 'core'}; }},
    fetch: async url => {
      requests.push(url);
      if (url.startsWith('/api/kernel-image')) return {ok: true, headers: {get: () => revision}, arrayBuffer: async () => scoreImage(keys)};
      if (url === '/api/kernel.wasm?keys=1' && failFetch) return {ok: false, text: async () => 'optional module is missing'};
      return {ok: true, arrayBuffer: async () => new Uint8Array([url.includes('keys=1') ? 2 : 1]).buffer};
    }
  });
  vm.runInContext(fs.readFileSync(require.resolve('./client.js'), 'utf8'), context);
  const audio = context.window.cicadaBrowserAudio;
  return {audio, requests, nodes, contexts, commands, captureActions,
    score(nextKeys, nextRevision) { keys = nextKeys; revision = nextRevision; },
    failFetch(value) { failFetch = value; }, failInit(value) { failInit = value; },
    compilations: () => compilations,
    attachCapture() {
      capture = new BrowserCapture(audio, {setTimeout, clearTimeout});
      capture.status.state = 'recording'; capture.take = {id: 'take'};
      capture.source = {disconnect: () => captureActions.push('disconnect-input')};
      capture.stream = {getTracks: () => [{stop: () => captureActions.push('stop-input')}]};
      return capture;
    }
  };
}

test('initial keys score fetches only the optional module', async () => {
  const h = setup(true);
  await h.audio.startAudio();
  assert.equal(h.audio.moduleKind, 'keys');
  assert.equal(h.nodes[0].options.processorOptions.m.kind, 'keys');
  assert.deepEqual(h.requests.filter(url => url.startsWith('/api/kernel.wasm')), ['/api/kernel.wasm?keys=1']);
});

test('module changes preserve context, estimated transport and play state while retiring old nodes', async () => {
  const h = setup();
  await h.audio.startAudio();
  h.audio.play(); await new Promise(resolve => setImmediate(resolve));
  const original = h.audio.node, oldHandler = original.port.onmessage;
  const message = new ArrayBuffer(16), view = new DataView(message);
  view.setUint8(0, 1); view.setBigInt64(8, 5072n, true);
  original.port.onmessage({data: {t: 'm', bytes: message, n: 16}});
  h.audio.context.currentTime = .125;
  h.score(true, 'two');
  await h.audio.stageCurrentScore('two');
  assert.equal(h.contexts.length, 1); assert.equal(h.audio.context.closed, false);
  assert.equal(h.audio.clock, true); assert.equal(h.audio.playing, true);
  assert.equal(original.connected, false); assert.equal(original.port.closed, true);
  const restored = h.commands.filter(command => command.node === h.audio.node);
  assert.equal(restored[0].op, 3); assert.equal(restored[0].arg0, 1); assert.equal(restored[0].arg1, 1472);
  assert.equal(restored[1].op, 1);
  oldHandler({data: {t: 's', p: false}});
  assert.equal(h.audio.playing, true, 'retired node changed replacement state');
  h.score(false, 'three'); await h.audio.stageCurrentScore('three');
  assert.equal(h.audio.moduleKind, 'core'); assert.equal(h.compilations(), 2, 'core module was compiled again');
  assert.equal(h.nodes.filter(node => node.connected).length, 1);
  assert.ok(h.nodes.slice(0, -1).every(node => node.port.closed));
});

test('same-module reprepare retains its node and does not fetch another module', async () => {
  const h = setup(); await h.audio.startAudio();
  const node = h.audio.node;
  h.score(false, 'two'); await h.audio.stageCurrentScore('two');
  assert.equal(h.audio.node, node); assert.equal(h.audio.revision, 'two');
  assert.equal(h.nodes.length, 1); assert.equal(h.compilations(), 1);
});

test('optional fetch failure leaves core audio intact and permits retry', async () => {
  const h = setup(); await h.audio.startAudio();
  const core = h.audio.node;
  h.score(true, 'two'); h.failFetch(true);
  await assert.rejects(h.audio.stageCurrentScore('two'), /optional module is missing/);
  assert.equal(h.audio.node, core); assert.equal(core.connected, true);
  assert.equal(h.audio.modulePromises.has('keys'), false);
  h.failFetch(false); await h.audio.stageCurrentScore('two');
  assert.equal(h.audio.moduleKind, 'keys'); assert.equal(core.connected, false);
});

test('failed replacement initialization disconnects the candidate and keeps the old node', async () => {
  const h = setup(); await h.audio.startAudio();
  const core = h.audio.node;
  h.score(true, 'two'); h.failInit(true);
  await assert.rejects(h.audio.stageCurrentScore('two'), /initialization failed/);
  assert.equal(h.audio.node, core); assert.equal(core.connected, true);
  assert.equal(h.nodes[1].connected, false); assert.equal(h.nodes[1].port.closed, true);
  h.failInit(false); await h.audio.stageCurrentScore('two');
  assert.equal(h.audio.moduleKind, 'keys'); assert.equal(h.nodes.filter(node => node.connected).length, 1);
});

test('module replacement finishes active capture before disarming its microphone', async () => {
  const h = setup(); await h.audio.startAudio();
  h.audio.play(); await new Promise(resolve => setImmediate(resolve));
  const capture = h.attachCapture();
  h.score(true, 'two'); await h.audio.stageCurrentScore('two');
  assert.deepEqual(h.captureActions, ['stop', 'disconnect-input', 'stop-input']);
  assert.equal(capture.status.state, 'stopped'); assert.equal(h.audio.playing, false);
  assert.match(capture.status.error, /score changed audio modules.*Arm the microphone again/);
  assert.equal(h.nodes.filter(node => node.connected).length, 1);
});

test('concurrent capture stops share the same finish handshake', async () => {
  const h = setup(); await h.audio.startAudio();
  const capture = h.attachCapture();
  const first = capture.stop(), second = capture.stop();
  assert.equal(first, second); await Promise.all([first, second]);
  assert.equal(h.captureActions.filter(action => action === 'stop').length, 1);
  assert.equal(capture.stopPromise, null);
});
