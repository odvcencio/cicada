const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const { execFileSync } = require('node:child_process');

test('WAM2 timed MIDI, automation, state and allocation-free processing', async () => {
  const root = path.resolve(__dirname, '../..');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'cicada-wam-test-'));
  try {
    const plugin = path.join(dir, 'plugin');
    execFileSync('go', ['run', './cmd/cicada', 'wam2', 'examples/live-intensity.cicada', '-o', plugin, '--midi-track', 'bass'], { cwd: root, env: { ...process.env, GOWORK: 'off' } });
    let Processor;
    const scopes = new Map();
    let inside = false, constructors = 0;
    const envelopes = new Set();
    globalThis.sampleRate = 48000;
    globalThis.currentTime = 0;
    globalThis.AudioWorkletProcessor = class {
      constructor() { this.port = { postMessage(message) { if (inside) envelopes.add(message); }, close() {} }; }
    };
    globalThis.AudioWorkletNode = class {};
    globalThis.webAudioModules = {
      getModuleScope(id) { if (!scopes.has(id)) scopes.set(id, {}); return scopes.get(id); },
      addWam() {}, removeWam() {},
    };
    globalThis.registerProcessor = (id, ctor) => { Processor = ctor; };
    const sdk = await import(path.join(root, 'build/wam2-sdk/node_modules/@webaudiomodules/sdk/dist/index.js'));
    const id = 'cicada-test';
    for (const get of ['getRingBuffer','getWamEventRingBuffer','getWamParameter','getWamParameterInfo','getWamParameterInterpolator','getWamProcessor']) sdk[get](id);
    const source = fs.readFileSync(path.join(__dirname, 'processor.js'), 'utf8').replace('export default function', 'function');
    new Function(`${source}; return registerProcessor;`)()(id);
    const kernel = await WebAssembly.compile(fs.readFileSync(path.join(plugin, 'kernel.wasm')));
    const data = fs.readFileSync(path.join(plugin, 'score-48000.bin'));
    const image = data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength);
    const manifest = JSON.parse(fs.readFileSync(path.join(plugin, 'score.json')));
    manifest.macros.push({id:'__proto__',index:1,defaultValue:.4,smoothMs:0});
    const definition = Buffer.alloc(24);
    definition[0]=17;definition[1]=255;definition.writeUInt16LE(1,2);definition.writeFloatLE(.4,4);
    const setup = Buffer.concat([Buffer.from(manifest.setup[48000], 'base64'),definition]);
    const processor = new Processor({ processorOptions: { kernel, image, manifest, setup, moduleId: id, instanceId: 'test', groupId: 'test' } });
    await processor.ready;
    processor._initialize(); processor._initialized = true;
    const output = [[new Float32Array(128), new Float32Array(128)]];
    const notices = processor.port.postMessage;
    let lastNotice = null;
    processor.port.postMessage = message => { lastNotice = message; notices(message); };
    await processor._onMessage({data:{id:42,request:'get/parameterInfo',content:{parameterIds:[]}}});
    assert.equal(lastNotice.content.__proto__.id,'__proto__','macro ID changed the metadata object prototype');
    processor.scheduleEvents(
      { type: 'wam-midi', time: 64/48000, data: { bytes: [0x90,60,100] } },
      { type: 'wam-automation', time: 96/48000, data: { id: 'intensity', value: .8, normalized: false } },
    );
    for (const name of ['Array','Uint8Array','Uint32Array','Float32Array','DataView','ArrayBuffer']) {
      const Native = globalThis[name];
      globalThis[name] = new Proxy(Native, { construct(target, args) { if (inside) constructors++; return Reflect.construct(target,args,target); } });
    }
    const input = [];
    const render = () => { inside = true; try { processor.process(input,output); } finally { inside=false; } };
    render();
    assert.equal(output[0][0].slice(0,64).some(value => value !== 0), false);
    assert.equal(processor._getParameterValues(false).intensity.value,.8);
    const state = processor._getState();
    processor._setParameterValue({ id: 'intensity', value: .1 }, true);
    processor._setState(state);
    assert.equal(processor._getParameterValues(false).intensity.value,.8);
    processor._onTransport({ playing: true });
    const memory = processor.x.memory.buffer.byteLength;
    for (let i=0;i<10000;i++) { globalThis.currentTime += 128/48000; render(); }
    assert.equal(constructors,0,'render constructed an array or typed view');
    assert.equal(processor.x.memory.buffer.byteLength,memory,'render grew WASM memory');
    assert.equal(envelopes.size,1,'event delivery did not reuse its envelope');
    assert.equal(processor.eventCount,0);
    assert.equal(lastNotice.event.type,'wam-automation');
    processor._onTransport({ playing: false });
    processor._onMidi({bytes:[0x90,64,100]});
    render();
    assert(output[0][0].some(value => value !== 0),'stopped transport suppressed MIDI');
    processor._onMidi({bytes:[0x90,64,0]});
    processor._onMidi({bytes:[0xb0,123,0]});
    render();
    processor.manifest.midiTrack=1;processor.manifest.midiDrums=true;
    processor._onMidi({bytes:[0x90,36,100]});
    render();
    assert(output[0][0].some(value => value!==0),'MIDI drum note did not play');
    processor._onMidi({bytes:[0xb0,123,0]});
    render();
    processor.destroy();
    assert.equal(processor.process([],output),false);
    process.stdout.write(JSON.stringify({callbacks:10000,renderConstructors:constructors,wasmMemoryGrowth:processor.x.memory.buffer.byteLength-memory,reusedEventEnvelopes:envelopes.size})+'\n');
  } finally { fs.rmSync(dir,{recursive:true,force:true}); }
});
