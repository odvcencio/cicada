'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

// The kernel owns the tempo map. This stub plays one bar at 120 BPM and then
// 60 BPM, the way a scene that sets `tempo = 60` does, so a bar is 96000 frames
// and then 192000 frames at 48 kHz.
const bar = 3840, rate = 48000;
const frames = (from, to) => {
  const slow = tick => Math.max(0, tick - bar) * 60000 * rate / (60000 * 960);
  const fast = tick => Math.min(tick, bar) * 60000 * rate / (120000 * 960);
  const at = tick => Math.ceil(fast(tick)) + Math.ceil(slow(tick));
  return from >= bar ? Math.ceil((to - from) * 60000 * rate / (60000 * 960)) : at(to) - at(from);
};

async function harness(asset) {
  let Type, port;
  const messages = [], instances = [];
  const context = vm.createContext({
    CICADA_CAPTURE: false, sampleRate: rate, performance: { now: () => 1 },
    WebAssembly: { instantiate: async () => {
      const memory = { buffer: new ArrayBuffer(65536) };
      const instance = { rendered: 0, commits: [] };
      instances.push(instance);
      return { exports: {
        memory, _initialize() {}, gosx_audio_project_alloc: () => 4096, gosx_audio_init: () => 0,
        gosx_audio_bank_alloc: () => 1024, gosx_audio_bank_install: () => 0, gosx_audio_bank_image_ptr: () => 1024, gosx_audio_bank_image_len: () => 16,
        gosx_audio_cmd_cap: () => 512, gosx_audio_cmd_ptr: () => 8192, gosx_audio_msg_ptr: () => 32768,
        gosx_audio_out_ptr: () => 49152, gosx_audio_msg_drain: () => 0,
        gosx_audio_cmd_commit: count => instance.commits.push(new Uint8Array(memory.buffer, 8192, count * 24).slice()),
        gosx_audio_tick_frames: frames,
        gosx_audio_render(count) {
          if (instance === instances[1] && !instance.rendered) instances.swapAt = instances[0].rendered;
          instance.rendered += count;
        }
      } };
    } },
    AudioWorkletProcessor: class { constructor() { this.port = port = { postMessage: m => messages.push(m) }; } },
    registerProcessor: (_, type) => { Type = type; }
  });
  vm.runInContext(fs.readFileSync(`${__dirname}/${asset}`, 'utf8'), context);
  const image = new ArrayBuffer(32);
  new Uint8Array(image).set([67, 73, 67, 49, 13]);
  const instance = new Type({ processorOptions: { m: {}, i: image, l: 10 } });
  await new Promise(resolve => setImmediate(resolve));
  return { instance, port: () => port, instances, messages, image };
}

for (const asset of ['processor.js', 'processor.min.js', 'processor-capture.min.js']) {
  test(`${asset}: a live edit swaps on the bar after a scene tempo change`, async () => {
    const h = await harness(asset);
    const command = op => { const bytes = new Uint8Array(24); bytes[0] = op; bytes[1] = 255; h.port().onmessage({ data: { t: 'c', bytes } }); };
    command(1);
    const quantum = [[new Float32Array(128), new Float32Array(128)]];
    let rendered = 0;
    const run = count => { for (let i = 0; i < count; i += 128, rendered += 128) h.instance.process([], quantum); };
    run(100 * 1024); // 102400 frames: inside the slow bar, which started at 96000
    h.port().onmessage({ data: { t: 'i', i: h.image, r: 'edit' } });
    // The new instance copies its oscillator bank during process calls.
    while (!h.messages.some(m => m.t === 't')) { run(128); await new Promise(resolve => setImmediate(resolve)); }
    assert.equal(h.instances.length, 2);
    run(96000 - (rendered - 102400)); // 198400 frames: the old bar length would have swapped near here
    assert.equal(h.instances[1].rendered, 0, 'the edit landed before the slow bar ended');
    assert.equal(h.instances[0].rendered, rendered);
    run(96000 + 128); // past 288000
    const swapAt = h.instances.swapAt;
    assert.equal(swapAt, 288000, `the edit landed after ${swapAt} frames, want 288000`);
    assert.ok(h.instances[1].rendered > 0);
    const swap = h.instances[1].commits.find(bytes => bytes[0] === 3 && bytes[24] === 1);
    assert.ok(swap, 'the new engine received the swap seek and play');
    assert.equal(swap[4], 2, 'the swap seeks to bar 3 (index 2)');
  });
}
