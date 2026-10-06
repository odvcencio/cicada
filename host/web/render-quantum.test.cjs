'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

async function processor(asset) {
  let Type, port, frame = 0, now = 1, duration = 0;
  const messages = [], renders = [];
  const memory = { buffer: new ArrayBuffer(65536) };
  const pcm = new Float32Array(memory.buffer, 49152, 256);
  const exports = {
    memory, _initialize() {}, gosx_audio_project_alloc: () => 4096,
    gosx_audio_init: () => 0, gosx_audio_bank_image_ptr: () => 1024,
    gosx_audio_bank_image_len: () => 16, gosx_audio_cmd_cap: () => 512,
    gosx_audio_cmd_ptr: () => 8192, gosx_audio_msg_ptr: () => 32768,
    gosx_audio_out_ptr: () => 49152, gosx_audio_cmd_commit() {},
    gosx_audio_msg_drain: () => 0,
    gosx_audio_render(count) {
      renders.push(count);
      for (let i = 0; i < count; i++) { pcm[i] = ++frame; pcm[128 + i] = -frame; }
    }
  };
  const context = vm.createContext({
    CICADA_CAPTURE: false, sampleRate: 48000,
    performance: { now() { const value = now; now += duration; return value; } },
    WebAssembly: { instantiate: async () => ({ exports }) },
    AudioWorkletProcessor: class { constructor() { this.port = port = { postMessage: m => messages.push(m) }; } },
    registerProcessor: (_, type) => { Type = type; }
  });
  vm.runInContext(fs.readFileSync(`${__dirname}/${asset}`, 'utf8'), context);
  const image = new ArrayBuffer(32);
  new Uint8Array(image).set([67, 73, 67, 49, 13]);
  const instance = new Type({ processorOptions: { m: {}, i: image, l: 10 } });
  await new Promise(resolve => setImmediate(resolve));
  assert.ok(messages.some(m => m.t === 'r'));
  const play = new Uint8Array(24); play[0] = 1;
  port.onmessage({ data: { t: 'c', bytes: play } });
  return {
    instance, renders,
    duration(ms) { duration = ms; },
    metrics() { port.onmessage({ data: { t: 'q', l: 10 } }); return messages.at(-1); }
  };
}

for (const asset of ['processor.js', 'processor.min.js', 'processor-capture.min.js']) {
  test(`${asset}: variable quanta preserve ordered PCM and actual deadlines`, async () => {
    const h = await processor(asset);
    let frame = 0;
    // Nonmultiples exercise the final partial kernel call as well.
    for (const size of [64, 128, 256, 512, 192, 64]) {
      const output = [[new Float32Array(size), new Float32Array(size)]];
      h.renders.length = 0;
      assert.equal(h.instance.process([], output), true);
      assert.equal(h.renders.reduce((a, b) => a + b, 0), size);
      assert.ok(h.renders.every(n => n > 0 && n <= 128));
      for (let i = 0; i < size; i++) {
        assert.equal(output[0][0][i], ++frame);
        assert.equal(output[0][1][i], -frame);
      }
      const metrics = h.metrics();
      assert.equal(metrics.q, size * 1000 / 48000);
      assert.equal(metrics.dl, metrics.q + .1);
      assert.equal(metrics.gl, 10 + metrics.dl);
      assert.equal(metrics.u, 0);
    }
    // A duration above the 64-frame deadline must still count as an underrun.
    h.duration(1.5);
    h.instance.process([], [[new Float32Array(64), new Float32Array(64)]]);
    assert.equal(h.metrics().u, 1);
    h.instance.process([], [[new Float32Array(512), new Float32Array(512)]]);
    assert.equal(h.metrics().u, 1);
  });
}

test('page metrics and output timeline use the negotiated quantum with a legacy fallback', () => {
  const window = {};
  const context = vm.createContext({ window, performance: { now: () => 100 } });
  vm.runInContext(fs.readFileSync(`${__dirname}/client.js`, 'utf8'), context);
  const audio = window.cicadaBrowserAudio;
  audio.context = { sampleRate: 48000, renderQuantumSize: 512, baseLatency: 0, outputLatency: 0,
    state: 'running', currentTime: .11, getOutputTimestamp: () => ({ contextTime: .1, performanceTime: 100 }) };
  assert.equal(audio.quantumMs(), 512000 / 48000);
  audio.playing = true;
  audio.sampleOutputTimeline();
  assert.equal(audio.outputTimeline.misses, 0);
  let metrics;
  audio.metricWaiters.push(value => { metrics = value; });
  audio.receive({ t: 'q', q: 256000 / 48000, u: 0 });
  assert.equal(metrics.q, 256000 / 48000);
  assert.equal(metrics.outputTimeline.quantumMs, metrics.q);
  delete audio.context.renderQuantumSize;
  assert.equal(audio.quantumMs(), 128000 / 48000);
  audio.sampleOutputTimeline();
  assert.equal(audio.outputTimeline.misses, 1);
  audio.context.renderQuantumSize = 0;
  assert.equal(audio.quantumMs(), 128000 / 48000);
});

for (const actualQuantum of [undefined, 512]) {
  test(`audio startup requests hardware sizing; negotiated quantum=${actualQuantum ?? 'legacy'}`, async () => {
    const window = {};
    let options;
    const context = vm.createContext({
      window, setTimeout, clearTimeout, setInterval: () => 1,
      fetch: async () => ({ ok: true, headers: { get: () => 'test' }, arrayBuffer: async () => { const image = new ArrayBuffer(32); new Uint8Array(image).set([67, 73, 67, 49, 13]); return image; } }),
      WebAssembly: { compile: async () => ({}) },
      AudioWorkletNode: class {
        constructor() { this.port = { postMessage() {} }; }
        connect() { this.port.onmessage({ data: { t: 'r', r: 'test', c: true } }); }
      }
    });
    window.AudioContext = class {
      constructor(value) {
        options = value;
        this.sampleRate = 48000;
        this.audioWorklet = { addModule: async () => {} };
      }
      async resume() { this.renderQuantumSize = actualQuantum; }
    };
    vm.runInContext(fs.readFileSync(`${__dirname}/client.js`, 'utf8'), context);
    const audio = window.cicadaBrowserAudio;
    await audio.startAudio();
    assert.equal(options.renderSizeHint, 'hardware');
    assert.equal(options.latencyHint, 'interactive');
    assert.equal(audio.quantumMs(), (actualQuantum || 128) * 1000 / 48000);
  });
}
