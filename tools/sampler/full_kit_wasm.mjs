// Admit the real full-kit banks through the browser loader and production ABI.
// Measure render only; note setup is outside each timed interval. Run with
// node --expose-gc after make build-sampler-wasm. Node is not a worklet deadline gate.
import {readFile,writeFile} from 'node:fs/promises';
import {performance} from 'node:perf_hooks';
import path from 'node:path';
const root=path.resolve(process.argv[2]??'build/kit-final/packs');
let source=await readFile(new URL('../../host/web/instrument-pack.js',import.meta.url),'utf8');
const encoding=await readFile(new URL('../../host/web/audio-encoding.js',import.meta.url),'utf8');
source=source.replace('./audio-encoding.js','data:text/javascript;base64,'+Buffer.from(encoding).toString('base64'));
const {loadPack,prepareSampler}=await import('data:text/javascript;base64,'+Buffer.from(source).toString('base64'));
const wasm=await WebAssembly.compile(await readFile('build/cicada-sampler.wasm'));
const catalog=JSON.parse(await readFile(path.join(root,'catalog.json'),'utf8'));
const results=[];
const fetcher=async address=>new Response(await readFile(path.join(root,new URL(address).pathname.slice(1))));
for(const pin of catalog.packs) {
 const pack=await loadPack('https://assets.invalid/'+pin.manifest,pin.sha256,{fetch:fetcher,cache:null});
 const note=pin.id.endsWith('shells')?38:51;
 const centers=[...new Set(pack.manifest.zones.filter(z=>z.Root===note).map(z=>z.Layer))].sort((a,b)=>a-b);
 for(const voices of [1,8,16])for(const layers of [1,2]) {
  const velocity=layers===1?centers[2]:Math.round((centers[1]+centers[2])/2);
  const manifest={...pack.manifest,config:{...pack.manifest.config,Voices:voices}};
  let kernel=new WebAssembly.Instance(wasm,{}).exports;
  if(kernel._initialize)kernel._initialize();
  prepareSampler(kernel,48000,{manifest,pcm:pack.pcm});
  const outputPtr=kernel.sampler_output_ptr();
  const memory=kernel.memory.buffer.byteLength;
  const times=new Float64Array(2000);
  for(let b=0;b<times.length+300;b++) {
   // Refill the bounded pool to keep exactly this many active note slots;
   // include their bounded steal tails in the render cost.
   for(let i=0;i<voices;i++)if(!kernel.sampler_note_on(note,velocity))throw Error('note rejected');
   const start=performance.now();kernel.sampler_render(128);const elapsed=(performance.now()-start)*1000;
   if(b>=300)times[b-300]=elapsed;
  }
  times.sort();
  const metric={bank:pin.id,rate:48000,block:128,voices,layers,ratio:1,velocity,p50_us:times[999],p99_us:times[1979],ns_sample_voice:times[999]*1000/128/voices,memory_growth_bytes:kernel.memory.buffer.byteLength-memory};
  if(metric.memory_growth_bytes)throw Error('render-time memory growth');
  kernel.sampler_reset();kernel.sampler_render(128);
  const silence=new Float32Array(kernel.memory.buffer,outputPtr,128);
  if(silence.some(v=>v!==0))throw Error('reset noise');
  results.push(metric);console.log(JSON.stringify(metric));
  kernel=null;if(globalThis.gc)globalThis.gc();
 }
}
await writeFile('build/kit-reports/full-kit-wasm.json',JSON.stringify({runtime:'Node '+process.versions.node+' / V8 '+process.versions.v8,scenario:'verified real banks, native pitch, repeated notes and bounded steal tails; note setup excluded; render-time memory fixed',results},null,2)+'\n');
