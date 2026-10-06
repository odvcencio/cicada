'use strict';
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');

const settle = () => new Promise(resolve => setImmediate(resolve));
function image(version, bpm = 120000) {
  const bytes = new Uint8Array(32);
  bytes.set([67, 73, 67, 49]);
  const view = new DataView(bytes.buffer);
  view.setUint16(4, version, true);
  view.setUint32(12, bpm, true);
  return bytes.buffer;
}
function setup(version = 13, message = { t: 'r', p: 65911, r: 'initial', c: true }) {
  let nextImage = image(version), nextRevision = 'initial';
  const stats = { nodes: 0, disconnected: 0, closed: 0, posted: [], timers: new Set(), errors: [] };
  class Context {
    constructor() { this.sampleRate = 48000; this.audioWorklet = { addModule: async () => {} }; }
    async resume() {}
    async close() { stats.closed++; }
  }
  const context = vm.createContext({
    window: { AudioContext: Context }, performance,
    Uint8Array, Uint32Array, ArrayBuffer, DataView, Set, Map, Error,
    WebAssembly: { compile: async () => ({}) },
    fetch: async url => ({ ok: true, headers: { get: () => nextRevision }, arrayBuffer: async () => url.includes('kernel-image') ? nextImage : new ArrayBuffer(0) }),
    setTimeout: callback => { stats.timers.add(callback); return callback; },
    clearTimeout: callback => stats.timers.delete(callback),
    setInterval: () => 1,
    AudioWorkletNode: class {
      constructor() { stats.nodes++; this.port = { postMessage: data => stats.posted.push(data) }; }
      connect() { queueMicrotask(() => this.port.onmessage({ data: message })); }
      disconnect() { stats.disconnected++; }
    }
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, 'client.js'), 'utf8'), context);
  const client = context.window.cicadaBrowserAudio;
  client.onError(error => stats.errors.push(error));
  return { client, stats, setImage: (version, bpm = 140000) => { nextImage = image(version, bpm); nextRevision = 'edited'; } };
}
async function start(harness) {
  let outcome;
  harness.client.startAudio().then(result => { outcome = { result }; }, error => { outcome = { error }; });
  await settle();
  assert.ok(outcome, 'startup left its promise pending instead of delivering the worklet result');
  assert.equal(harness.stats.timers.size, 0, 'startup left its timeout pending');
  return outcome;
}
(async () => {
  for (const version of [8, 9, 10, 11, 12, 13]) {
    const legacy = setup(version, { t: 'r', r: 'legacy', c: true });
    assert.ok((await start(legacy)).result);
    assert.equal(legacy.client.capabilities, 0);
  }
  for (const version of [14, 16, 65535]) {
    const invalid = setup(version);
    assert.match((await start(invalid)).error.message, version === 14 ? /Ambiguous.*image14.*recompile/ : /Unsupported image version/);
    assert.equal(invalid.stats.nodes, 0, 'invalid image reached the worklet');
    assert.equal(invalid.stats.closed, 1);
  }
  const old = setup(15, { t: 'e', e: 'Unified image15 requires CapabilityUnifiedImage (bit16); update the audio kernel' });
  assert.match((await start(old)).error.message, /image15.*CapabilityUnifiedImage/);
  assert.equal(old.client.context, null);
  assert.equal(old.stats.closed, 1, 'failed startup did not close the context');
  assert.equal(old.stats.disconnected, 1, 'failed startup did not disconnect the candidate node');
  const cached = setup(15, { t: 'r', p: 1, c: true });
  assert.match((await start(cached)).error.message, /image15.*CapabilityUnifiedImage/);
  assert.equal(cached.stats.closed, 1);
  assert.equal(cached.stats.disconnected, 1);

  const legacy = setup(13, { t: 'r', p: 0, r: 'initial', c: true });
  assert.ok((await start(legacy)).result);
  const before = legacy.stats.posted.length;
  assert.throws(() => legacy.client.sendCommands([{ op: 2 }, { op: 22 }]), /chord opcode22/);
  assert.equal(legacy.stats.posted.length, before, 'client partly posted a rejected command batch');
  for (const version of [14, 15, 16]) {
    legacy.setImage(version);
    await assert.rejects(legacy.client.stageCurrentScore('edited'), version === 14 ? /Ambiguous.*image14/ : version === 15 ? /image15.*CapabilityUnifiedImage/ : /Unsupported image version/);
    assert.equal(legacy.stats.posted.length, before, 'client posted an incompatible staged image');
    assert.equal(legacy.client.stageWaiters.size, 0);
    assert.equal(legacy.client.bpmMilli, 120000, 'rejected stage changed tempo');
    assert.equal(legacy.client.revision, 'initial');
  }
  assert.throws(() => legacy.client.sendCommands([{ op: 1 }, { op: 26 }]), /spatial/);
  assert.equal(legacy.stats.posted.length, before);
  const spatial = setup(15, { t: 'r', p: 197119, r: 'initial', c: true });
  assert.ok((await start(spatial)).result);
  spatial.client.sendCommands([{ op: 26, track: 0, index: 1, arg0: 0x3f800000, pad: 0x3f000000 }]);
  assert.equal(new DataView(spatial.stats.posted.at(-1).bytes.buffer).getFloat32(12, true), .5);
  const unified = setup(15);
  assert.ok((await start(unified)).result);
  unified.client.sendCommands([{ op: 22 }]);
  assert.equal(unified.stats.posted.at(-1).t, 'c');
  unified.setImage(15);
  const failedStage = unified.client.stageCurrentScore('edited');
  await settle();
  unified.client.receive({ t: 'x', r: 'edited', e: 'decoder rejected image' });
  await assert.rejects(failedStage, /decoder rejected image/);
  assert.equal(unified.client.bpmMilli, 120000);
  assert.equal(unified.client.revision, 'initial');
  const successfulStage = unified.client.stageCurrentScore('edited');
  await settle();
  unified.client.receive({ t: 't', r: 'edited' });
  await successfulStage;
  assert.equal(unified.client.bpmMilli, 140000);
  assert.equal(unified.client.revision, 'edited');

  // Dedicated fault20 still reaches both message and fallback adapters clearly.
  const fault = new ArrayBuffer(16), faultView = new DataView(fault);
  faultView.setUint8(0, 7); faultView.setUint16(2, 20, true);
  unified.client.receive({ t: 'm', bytes: fault, n: 16 });
  assert.match(unified.stats.errors.at(-1).message, /handle-aware.*legacy NoteOn\/NoteOff/);
  unified.client.receive({ t: 'f', a: 20 });
  assert.match(unified.stats.errors.at(-1).message, /handle-aware.*legacy NoteOn\/NoteOff/);
  console.log('client.js: legacy/unified negotiation, immediate startup errors, atomic preflight and staged tempo preservation passed');
})().catch(error => { console.error(error); process.exitCode = 1; });
