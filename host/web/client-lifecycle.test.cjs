'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const settle = () => new Promise(resolve => setImmediate(resolve));
function setup() {
  const stats = {contexts: [], nodes: [], posted: [], intervals: new Set(), states: [], errors: [], frames: new Set()};
  let revision = 'initial', failResume = false, holdReady = false;
  const image = new Uint8Array(32);
  image.set([67, 73, 67, 49, 15]); new DataView(image.buffer).setUint32(12, 120000, true);
  class Context {
    constructor() {this.sampleRate = 48000; this.currentTime = 0; this.audioWorklet = {addModule: async () => {}}; this.closed = 0; stats.contexts.push(this);}
    async resume() {if (failResume) throw new Error('resume failed');}
    async close() {this.closed++;}
  }
  const env = vm.createContext({
    window: {AudioContext: Context}, performance, setTimeout, clearTimeout,
    setInterval: callback => {stats.intervals.add(callback); return callback;},
    clearInterval: callback => stats.intervals.delete(callback),
    requestAnimationFrame: callback => {stats.frames.add(callback); return callback;},
    cancelAnimationFrame: callback => stats.frames.delete(callback),
    WebAssembly: {compile: async () => ({})},
    fetch: async () => ({ok: true, headers: {get: () => revision}, arrayBuffer: async () => image.slice().buffer}),
    AudioWorkletNode: class {
      constructor() {
        this.disconnected = this.closed = 0;
        this.port = {postMessage: data => stats.posted.push(data), close: () => {this.closed++;}};
        stats.nodes.push(this);
      }
      connect() {if (!holdReady) queueMicrotask(() => this.port.onmessage({data: {t: 'r', p: 65537, r: revision, c: true}}));}
      disconnect() {this.disconnected++;}
    }
  });
  vm.runInContext(fs.readFileSync(`${__dirname}/client.js`, 'utf8'), env);
  const audio = env.window.cicadaBrowserAudio;
  audio.onState(state => stats.states.push(state)); audio.onError(error => stats.errors.push(error));
  return {audio, stats, env, revision: value => {revision = value;}, failResume: () => {failResume = true;},
    holdReplacement() {holdReady = true; new DataView(image.buffer).setUint16(30, 512, true);}};
}

test('close clears timers, disconnects/closes the port and context, and permits restart', async () => {
  const h = setup(); await h.audio.startAudio();
  h.audio.receive({t: 's', p: true});
  const bytes = new ArrayBuffer(16); new Uint8Array(bytes)[0] = 2;
  h.audio.receive({t: 'm', bytes, n: 16});
  assert.equal(h.stats.frames.size, 1); assert.equal(h.stats.intervals.size, 1);
  const node = h.audio.node;
  await Promise.all([h.audio.close(), h.audio.close()]);
  assert.equal(node.disconnected, 1); assert.equal(node.closed, 1);
  assert.equal(node.port.onmessage, null); assert.equal(h.stats.contexts[0].closed, 1);
  assert.equal(h.stats.intervals.size, 0); assert.equal(h.stats.frames.size, 0);
  assert.equal(h.audio.context, null); assert.equal(h.audio.node, null);
  assert.equal(h.audio.playing, false); assert.equal(h.stats.states.at(-1), false);
  await h.audio.close(); assert.equal(h.stats.contexts[0].closed, 1);
  await h.audio.startAudio(); assert.equal(h.stats.contexts.length, 2);
  await h.audio.close();
});

test('failed startup uses the same cleanup and stops an existing timeline timer', async () => {
  const h = setup(); h.failResume();
  const timer = () => {}; h.stats.intervals.add(timer); h.audio.outputTimelineTimer = timer;
  await assert.rejects(h.audio.startAudio(), /resume failed/);
  assert.equal(h.stats.intervals.size, 0); assert.equal(h.audio.outputTimelineTimer, 0);
  assert.equal(h.stats.nodes[0].closed, 1); assert.equal(h.stats.contexts[0].closed, 1);
});

test('close rejects staging and metrics, and cancels queued staging', async () => {
  const h = setup(); await h.audio.startAudio(); h.revision('edited');
  const stage = h.audio.stageCurrentScore('edited'); await settle();
  const queued = h.audio.stageCurrentScore('edited');
  const metrics = h.audio.requestMetrics();
  const rejected = [stage, queued, metrics].map(promise => assert.rejects(promise, /Browser audio closed/));
  await h.audio.close(); await Promise.all(rejected);
  assert.equal(h.audio.stageWaiters.size, 0); assert.equal(h.audio.metricWaiters.length, 0);
  assert.equal(h.stats.posted.filter(data => data.t === 'i').length, 1);
});

test('overlapping stages serialize until the first acknowledgement, including after rejection', async () => {
  const h = setup(); await h.audio.startAudio(); h.revision('one');
  const first = h.audio.stageCurrentScore('one'); await settle();
  const second = h.audio.stageCurrentScore('two'); await settle();
  assert.equal(h.stats.posted.filter(data => data.t === 'i').length, 1);
  h.revision('two');
  const rejected = assert.rejects(first, /stage failed/);
  h.audio.receive({t: 'x', r: 'one', e: 'stage failed'}); await rejected; await settle();
  assert.equal(h.stats.posted.filter(data => data.t === 'i').length, 2);
  h.audio.receive({t: 't', r: 'two'}); await second;
  assert.equal(h.audio.revision, 'two'); await h.audio.close();
});

test('close also cancels and disposes a replacement node awaiting readiness', async () => {
  const h = setup(); await h.audio.startAudio(); h.holdReplacement(); h.revision('keys');
  const rejected = assert.rejects(h.audio.stageCurrentScore('keys'), /Browser audio closed/);
  await settle(); assert.equal(h.stats.nodes.length, 2);
  await h.audio.close(); await rejected;
  for (const node of h.stats.nodes) {assert.equal(node.disconnected, 1); assert.equal(node.closed, 1);}
  assert.equal(h.audio.nodes.size, 0);
});

for (const fallback of [false, true]) {
  test(`fault resets playback and Play reloads even the same revision (fallback=${fallback})`, async () => {
    const h = setup(); await h.audio.startAudio(); h.audio.receive({t: 's', p: true});
    if (fallback) h.audio.receive({t: 'f', a: 20});
    else {
      const bytes = new ArrayBuffer(16), view = new DataView(bytes);
      view.setUint8(0, 7); view.setUint16(2, 20, true); h.audio.receive({t: 'm', bytes, n: 16});
    }
    assert.equal(h.audio.playing, false); assert.equal(h.stats.states.at(-1), false);
    assert.match(h.stats.errors.at(-1).message, /handle-aware/);
    assert.throws(() => h.audio.sendCommands([{op: 1}]), /Press Play to reload the score/);
    const play = h.audio.play(); await settle();
    assert.equal(h.stats.posted.filter(data => data.t === 'i').length, 1);
    h.audio.receive({t: 't', r: 'initial'}); await play;
    assert.equal(h.audio.fault, null); assert.equal(h.stats.posted.at(-1).t, 'c');
    assert.equal(h.stats.posted.at(-1).bytes[0], 3); assert.equal(h.stats.posted.at(-1).bytes[24], 1);
    await h.audio.close();
  });
}

test('production client exposes no stall injection', () => {
  const h = setup(); assert.equal(h.audio.injectStall, undefined);
});

test('processor exceptions reset playback and Play replaces the stopped node', async () => {
  const h = setup(); await h.audio.startAudio(); h.audio.receive({t: 's', p: true});
  const failed = h.audio.node; failed.onprocessorerror();
  assert.equal(h.audio.playing, false); assert.equal(h.stats.states.at(-1), false);
  assert.match(h.stats.errors.at(-1).message, /AudioWorklet stopped unexpectedly.*Press Play/);
  await h.audio.play();
  assert.equal(h.stats.nodes.length, 2); assert.notEqual(h.audio.node, failed);
  assert.equal(failed.closed, 1); assert.equal(failed.disconnected, 1); assert.equal(failed.onprocessorerror, null);
  assert.equal(h.audio.fault, null); assert.equal(h.audio.nodeFault, false);
  assert.equal(h.stats.posted.at(-1).bytes[24], 1);
  await h.audio.close();
});

for (const legacy of [false, true]) {
  test(`fatal processor failure cancels in-flight staging and metrics (legacy=${legacy})`, async () => {
    const h = setup(); await h.audio.startAudio(); h.revision('edited');
    const stage = h.audio.stageCurrentScore('edited'); await settle();
    const rejectedStage = assert.rejects(stage, /processor failed|stopped unexpectedly/);
    const rejectedMetrics = assert.rejects(h.audio.requestMetrics(), /processor failed|stopped unexpectedly/);
    if (legacy) h.audio.receive({t: 'e', e: 'processor failed'});
    else h.audio.node.onprocessorerror();
    await Promise.all([rejectedStage, rejectedMetrics]);
    assert.equal(h.audio.stageWaiters.size, 0); assert.equal(h.audio.metricWaiters.length, 0);
    // An acknowledgement queued before the crash must not hide the failure.
    h.audio.receive({t: 't', r: 'edited'});
    await assert.rejects(h.audio.requestMetrics(), /processor failed|stopped unexpectedly/);
    await h.audio.play(); assert.equal(h.stats.nodes.length, 2);
    assert.equal(h.audio.nodeFault, false); await h.audio.close();
  });
}

test('score page retains history controls without the unused historyList binding', () => {
  const page = fs.readFileSync(`${__dirname}/../../cmd/cicada/view.html`, 'utf8');
  assert.match(page, /id="history-list"/);
  assert.doesNotMatch(page, /\bhistoryList\b/);
});

test('score page enables Start audio after close clears the context', () => {
  const page = fs.readFileSync(`${__dirname}/../../cmd/cicada/view.html`, 'utf8');
  const source = page.match(/window\.cicadaBrowserAudio\.onState\(state => \{[\s\S]*?\n  \}\);/)?.[0];
  assert.ok(source);
  const startAudioButton = {disabled: true, textContent: 'Audio ready'}, transportButton = {textContent: 'Stop'};
  vm.runInNewContext(source, {window: {cicadaBrowserAudio: {context: null, onState: callback => callback(false)}},
    reportBrowserAudioStatus() {}, isBrowser: () => true, startAudioButton, transportButton});
  assert.equal(startAudioButton.disabled, false); assert.equal(startAudioButton.textContent, 'Start audio');
  assert.equal(transportButton.textContent, 'Play');
});
