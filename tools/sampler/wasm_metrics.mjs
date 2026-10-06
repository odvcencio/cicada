// Measure the production optional sampler kernel in V8, including stereo SRC.
import {readFile} from 'node:fs/promises';
import {performance} from 'node:perf_hooks';
const source=await readFile(new URL('../../host/web/instrument-pack.js',import.meta.url),'utf8');
const {prepareSampler}=await import('data:text/javascript;base64,'+Buffer.from(source).toString('base64'));
const wasm=await WebAssembly.compile(await readFile(process.argv[2]??'build/cicada-sampler.wasm'));
for(const ratio of [1,1.5])for(const voices of [1,8])for(const layers of [1,2]) {
 const pcm=new Float32Array(32768);for(let i=0;i<pcm.length;i++)pcm[i]=((i*17)%101-50)/200;
 const config={Voices:voices,Amp:{Attack:2,Decay:0,Sustain:1,Release:180},Filter:{Attack:0,Decay:0,Sustain:1,Release:0},Cutoff:0,FilterDepth:0,Gain:.1,TuneCents:1200*Math.log2(ratio),Humanize:{Seed:4242,DelayMS:0,Velocity:0,Cents:0}};
 const z={asset:'test',Root:60,KeyLow:60,KeyHigh:60,VelocityLow:1,VelocityHigh:127,Layer:64,Group:0,Position:0,Count:1,Release:false,Gain:1,TuneCents:0,Start:0,End:pcm.length,Loop:true,LoopStart:0,LoopEnd:pcm.length,Crossfade:0};
 const manifest={config,assets:[{id:'test',frames:pcm.length,channels:2,rate:48000}],zones:layers===1?[z]:[{...z,Layer:32},{...z,Layer:96}]};
 const {exports:k}=new WebAssembly.Instance(wasm,{});if(k._initialize)k._initialize();prepareSampler(k,48000,{manifest,pcm:new Map([['test',[pcm,pcm]]])});
 for(let i=0;i<voices;i++)if(!k.sampler_note_on(60,64))throw Error('note failed');
 for(let i=0;i<2000;i++)k.sampler_render(128);
 const elapsed=new Float64Array(10000);const before=k.memory.buffer.byteLength;
 for(let i=0;i<elapsed.length;i++){const start=performance.now();k.sampler_render(128);elapsed[i]=(performance.now()-start)*1000;}
 elapsed.sort();const p50=elapsed[4999],p99=elapsed[9899];
 console.log(JSON.stringify({runtime:'V8 '+process.versions.v8,rate:48000,block:128,voices,layers,ratio,p50_us:p50,p99_us:p99,ns_sample_voice:p50*1000/128/voices,memory_growth_bytes:k.memory.buffer.byteLength-before}));
 if(k.memory.buffer.byteLength!==before)throw Error('callback memory grew');
 k.sampler_reset();k.sampler_render(128);const output=new Float32Array(k.memory.buffer,k.sampler_output_ptr(),128);if(output.some(x=>x!==0))throw Error('reset noise');
}
