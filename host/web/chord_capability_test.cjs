'use strict';
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');

async function setup(asset, version, capability) {
  let type, port, commits = 0, projectAllocs = 0, initialized = false;
  const messages = [];
  const exports = {
    memory: { buffer: new ArrayBuffer(65536) }, _initialize() { initialized = true; },
    gosx_audio_project_alloc() { projectAllocs++; return 4096; },
    gosx_audio_init() { return 0; }, gosx_audio_bank_image_ptr() { return 1024; },
    gosx_audio_bank_image_len() { return 16; }, gosx_audio_cmd_cap() { return 512; },
    gosx_audio_cmd_ptr() { return 8192; }, gosx_audio_msg_ptr() { return 32768; },
    gosx_audio_out_ptr() { return 49152; }, gosx_audio_cmd_commit() { commits++; },
    gosx_audio_msg_drain() { return 0; }, gosx_audio_render() {}
  };
  if (capability !== null) exports.gosx_audio_capabilities = () => {
    assert.ok(initialized, 'reactor capability queried before runtime initialization');
    return capability;
  };
  const context = vm.createContext({
    sampleRate: 48000, performance, Date, ArrayBuffer, Uint8Array, Uint32Array, Float32Array, DataView,
    WebAssembly: { instantiate: async () => ({ exports }) },
    AudioWorkletProcessor: class { constructor() { this.port = port = { postMessage: data => messages.push(data), onmessage: null }; } },
    registerProcessor: (_, candidate) => { type = candidate; }
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, asset), 'utf8'), context);
  const image = new ArrayBuffer(32);new DataView(image).setUint16(4,version,true);
  const processor = new type({ processorOptions: { m: {}, i: image, r: 'test' } });
  await new Promise(resolve => setImmediate(resolve));
  return { processor, port, messages, exports, counts: () => ({ commits, projectAllocs }) };
}
(async () => {
  for (const asset of ['processor.js', 'processor.min.js']) {
    const old = await setup(asset,14,null);
    assert.match(old.messages.find(m => m.t === 'e').e,/chord image14/);
    assert.equal(old.counts().projectAllocs,0,'unsupported image mutated old kernel');
    const mono = await setup(asset,13,null);assert.ok(mono.messages.some(m => m.t === 'r'));
    const batch = new Uint8Array(48);batch[0]=1;batch[1]=255;batch[24]=22;
    mono.port.onmessage({data:{t:'c',bytes:batch}});
    assert.match(mono.messages.find(m => m.t === 'e').e,/chord opcode22/);
    assert.equal(mono.counts().commits,0,'unsupported opcode partly committed batch');
    const poly = await setup(asset,14,1);assert.ok(poly.messages.some(m => m.t === 'r'));
    poly.port.onmessage({data:{t:'c',bytes:new Uint8Array([22,...Array(23).fill(0)])}});
    assert.equal(poly.counts().commits,1);
    console.log(`${asset}: image/opcode capability rejection is explicit and atomic; v13 and advertised v14 accepted`);
  }
})().catch(error => { console.error(error);process.exitCode=1; });

// The page adapter must turn dedicated fault20 into a useful explanation.
const pageContext = vm.createContext({ window: {}, DataView, Error, Set, Map, Uint32Array });
vm.runInContext(fs.readFileSync(path.join(__dirname,'client.js'),'utf8'),pageContext);
pageContext.window.cicadaBrowserAudio.node={port:{postMessage(){}}};
let liveError;pageContext.window.cicadaBrowserAudio.errors.add(error => {liveError=error;});
const fault = new ArrayBuffer(16);const faultView=new DataView(fault);faultView.setUint8(0,7);faultView.setUint16(2,20,true);
pageContext.window.cicadaBrowserAudio.receive({t:'m',bytes:fault,n:16});
assert.match(liveError.message,/handle-aware.*legacy NoteOn\/NoteOff/);

liveError=undefined;pageContext.window.cicadaBrowserAudio.receive({t:'f',a:20});
assert.match(liveError.message,/handle-aware.*legacy NoteOn\/NoteOff/);
