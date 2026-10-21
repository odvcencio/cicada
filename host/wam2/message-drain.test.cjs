'use strict';
// The kernel drains up to engine.MessageCapacity (512) messages in one call. The
// plugin must see a fault anywhere in that block, not only in the first 256.
// This test needs neither the WAM SDK nor a built kernel: it runs the shipped
// processor module against a fake kernel.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

async function processor(drain) {
  const memory = { buffer: new ArrayBuffer(1 << 20) };
  const scope = {
    WamProcessor: class { constructor() { this.port = { postMessage: message => this.posted.push(message) }; this.posted = []; } },
    WamParameterInfo: class {}, WamParameter: class {},
  };
  globalThis.sampleRate = 48000;
  globalThis.webAudioModules = { getModuleScope: () => scope };
  let Type;
  globalThis.registerProcessor = (_, ctor) => { Type = ctor; };
  const realInstantiate = WebAssembly.instantiate;
  WebAssembly.instantiate = async () => ({ exports: {
    memory, _initialize() {}, gosx_audio_capabilities: () => 65536,
    gosx_audio_project_alloc: () => 4096, gosx_audio_init: () => 0,
    gosx_audio_cmd_ptr: () => 8192, gosx_audio_cmd_cap: () => 512, gosx_audio_cmd_commit() {},
    gosx_audio_out_ptr: () => 262144, gosx_audio_msg_ptr: () => 32768, gosx_audio_render() {},
    gosx_audio_msg_drain() { return drain(new DataView(memory.buffer, 32768, 512 * 16)); },
  } });
  try {
    const source = fs.readFileSync(path.join(__dirname, 'processor.js'), 'utf8').replace('export default function', 'function');
    new Function(`${source}; return registerProcessor;`)()('cicada-drain-test');
    const image = new ArrayBuffer(32);
    new DataView(image).setUint16(4, 15, true);
    const instance = new Type({ processorOptions: { kernel: {}, image, manifest: { tempo: 120, macros: [] }, setup: new Uint8Array(0) } });
    await instance.ready;
    instance.playing = true;
    return instance;
  } finally {
    WebAssembly.instantiate = realInstantiate;
  }
}

for (const index of [0, 255, 256, 300, 511]) {
  test(`a fault at index ${index} of a 512-message drain stops the plugin`, async () => {
    const instance = await processor(view => {
      for (let i = 0; i < 512; i++) { view.setUint8(i * 16, 1); view.setUint16(i * 16 + 2, 0, true); }
      view.setUint8(index * 16, 7); view.setUint16(index * 16 + 2, 99, true);
      return 512;
    });
    instance.render(0, 128, new Float32Array(128), new Float32Array(128));
    const fault = instance.posted.find(message => 'cicadaFault' in message);
    assert.ok(fault, 'the fault was not delivered');
    assert.equal(fault.cicadaFault, 99);
    assert.equal(instance.playing, false);
  });
}

test('the plugin and every other drained-block reader use the shared message capacity', () => {
  const root = path.resolve(__dirname, '../..');
  for (const file of ['host/wam2/processor.js', 'host/web/processor.js', 'cmd/cicada/browser_cpu_node.js', 'tools/audio/kernel-cpu-compare.mjs', 'tools/audio/kit-cpu.cjs']) {
    assert.match(fs.readFileSync(path.join(root, file), 'utf8'), /MESSAGE_BYTES = 512 \* 16/, `${file} must size its message view from the 512-message capacity`);
  }
});
