// Optional immutable sample-pack preparation. No fetch/decode runs in process().
export const MAX_PCM_BYTES = 256 * 1024 * 1024;
const MAX_MANIFEST_BYTES = 2 * 1024 * 1024;
const hashPattern = /^[a-f0-9]{64}$/;
export async function sha256(bytes) {
  const hash = await crypto.subtle.digest('SHA-256', bytes);
  return Array.from(new Uint8Array(hash), x => x.toString(16).padStart(2, '0')).join('');
}
function requireThat(ok, message) { if (!ok) throw new Error(message); }
function safePath(path) { return typeof path === 'string' && path.length && !path.startsWith('/') && !/[\\:\0?#]/.test(path) && path.split('/').every(x => x && x !== '.' && x !== '..'); }
function https(url) { try { const u = new URL(url); return u.protocol === 'https:' && !u.username && !u.password; } catch { return false; } }
export function validateManifest(m) {
  requireThat(m?.format === 'cicada.instrument-pack/1' && safePath(m.id) && !m.id.includes('/'), 'invalid pack format/id');
  requireThat(Array.isArray(m.assets) && m.assets.length && m.assets.length <= 4096 && Array.isArray(m.zones) && m.zones.length && m.zones.length <= 4096, 'invalid pack dimensions');
  const ids = new Set(); let pcmBytes = 0;
  for (const a of m.assets) {
    requireThat(typeof a.id === 'string' && a.id && !ids.has(a.id) && safePath(a.path) && a.path.endsWith('.wav.gz'), 'invalid/duplicate sample path');
    requireThat([a.sha256, a.wav_sha256, a.source_sha256].every(x => hashPattern.test(x)), 'invalid sample hash');
    requireThat([a.bytes,a.wav_bytes,a.frames,a.rate,a.channels].every(Number.isSafeInteger) && a.bytes > 0 && a.bytes <= MAX_PCM_BYTES && a.wav_bytes >= 44 && a.wav_bytes <= MAX_PCM_BYTES && a.frames > 0 && a.frames <= 8*1024*1024 && a.rate >= 8000 && a.rate <= 192000 && a.channels >= 1 && a.channels <= 2, 'invalid sample bounds');
    requireThat(https(a.source_url) && https(a.license_url) && (a.license === 'CC0-1.0' || a.license === 'CC-BY-4.0' && a.attribution), 'sample needs redistributable licence/provenance');
    ids.add(a.id); pcmBytes += a.frames*a.channels*4;
  }
  requireThat(pcmBytes <= MAX_PCM_BYTES, 'pack exceeds resident PCM budget');
  for (const z of m.zones) {
    requireThat(ids.has(z.asset), 'zone references unknown sample');
    const bounds = {Root:[0,127],KeyLow:[0,127],KeyHigh:[0,127],VelocityLow:[1,127],VelocityHigh:[1,127],Layer:[1,127],Group:[0,255],Position:[0,31],Count:[1,32],Start:[0,8*1024*1024],End:[0,8*1024*1024],LoopStart:[0,8*1024*1024],LoopEnd:[0,8*1024*1024],Crossfade:[0,8*1024*1024]};
    for (const [key,[lo,hi]] of Object.entries(bounds)) requireThat(Number.isInteger(z[key]) && z[key]>=lo && z[key]<=hi, 'invalid zone '+key);
    requireThat(Number.isInteger(z.ChokeGroup??0)&&(z.ChokeGroup??0)>=0&&(z.ChokeGroup??0)<=255,'invalid choke group');
    requireThat(z.KeyLow<=z.KeyHigh && z.VelocityLow<=z.VelocityHigh && z.Layer>=z.VelocityLow && z.Layer<=z.VelocityHigh && z.Position<z.Count && Number.isFinite(z.Gain) && Number.isFinite(z.TuneCents), 'invalid zone mapping');
  }
  return m;
}
async function limitedBytes(response, limit) {
  requireThat(response.ok, 'sample fetch failed: '+response.status);
  const reader=response.body.getReader(); const parts=[];let length=0;
  try { while(true) {const {done,value}=await reader.read();if(done)break;length+=value.length;requireThat(length<=limit,'asset exceeds declared size');parts.push(value);} }
  finally { await reader.cancel(); }
  const bytes=new Uint8Array(length);let offset=0;for(const part of parts){bytes.set(part,offset);offset+=part.length;}return bytes;
}
export async function checkedFetch(url, hash, bytes, options={}) {
  requireThat(hashPattern.test(hash), 'explicit SHA-256 pin required');
  const cache = options.cache ?? (globalThis.caches ? await caches.open('cicada-instrument-pack-v1') : null);
  const cacheKey = new URL(url);
  cacheKey.searchParams.set('__cicada_sha256', hash);
  let response=cache ? await cache.match(cacheKey.href) : null;
  if(!response) response=await (options.fetch ?? fetch)(url,{signal:options.signal,credentials:'omit'});
  const data=await limitedBytes(response,bytes);
  requireThat(await sha256(data)===hash,'asset hash mismatch');
  if(cache) await cache.put(cacheKey.href,new Response(data));
  return data;
}
export function decodeWAV(bytes,a) {
  const v=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);
  const tag=o=>String.fromCharCode(...bytes.subarray(o,o+4));
  requireThat(bytes.length>=44 && tag(0)==='RIFF' && tag(8)==='WAVE' && v.getUint32(4,true)+8===bytes.length,'invalid WAV container');
  let fmt=null,data=null;
  for(let o=12;o<bytes.length;) {
    requireThat(o+8<=bytes.length,'truncated WAV chunk');const n=v.getUint32(o+4,true),start=o+8;requireThat(start+n<=bytes.length,'WAV chunk exceeds container');
    if(tag(o)==='fmt ') {requireThat(!fmt&&n>=16,'invalid WAV format chunk');fmt={encoding:v.getUint16(start,true),channels:v.getUint16(start+2,true),rate:v.getUint32(start+4,true),align:v.getUint16(start+12,true),bits:v.getUint16(start+14,true),byteRate:v.getUint32(start+8,true)};}
    if(tag(o)==='data') {requireThat(!data,'duplicate WAV data');data={start,n};}
    o=start+n+(n&1);
  }
  requireThat(fmt&&data&&(fmt.encoding===1&&[16,24,32].includes(fmt.bits)||fmt.encoding===3&&fmt.bits===32),'unsupported WAV format');
  const width=fmt.bits/8;
  requireThat(fmt.rate===a.rate&&fmt.channels===a.channels&&fmt.align===width*a.channels&&fmt.byteRate===fmt.rate*fmt.align&&data.n===a.frames*fmt.align,'WAV dimensions mismatch');
  const pcm=Array.from({length:a.channels},()=>new Float32Array(a.frames));let offset=data.start;
  for(let frame=0;frame<a.frames;frame++)for(let ch=0;ch<a.channels;ch++) {
    let x;if(fmt.encoding===3)x=v.getFloat32(offset,true);else if(width===2)x=v.getInt16(offset,true)/32768;else if(width===3)x=((v.getUint8(offset)|v.getUint8(offset+1)<<8|v.getUint8(offset+2)<<16)<<8>>8)/8388608;else x=v.getInt32(offset,true)/2147483648;
    requireThat(Number.isFinite(x),'nonfinite PCM');pcm[ch][frame]=x;offset+=width;
  }
  return pcm;
}
export async function loadPack(manifestURL,pin,options={}) {
  const data=await checkedFetch(manifestURL,pin,MAX_MANIFEST_BYTES,options);
  const manifest=validateManifest(JSON.parse(new TextDecoder().decode(data)));
  const pcm=new Map();
  // Sequential admission caps temporary decode memory. Nothing reaches the
  // audio owner until every selected asset and the complete map is verified.
  for(const a of manifest.assets) {
    const compressed=await checkedFetch(new URL(a.path,manifestURL).href,a.sha256,a.bytes,options);
    requireThat(compressed.length===a.bytes,'compressed sample size mismatch');
    const stream=new Blob([compressed]).stream().pipeThrough(new DecompressionStream('gzip'));
    const wav=await limitedBytes(new Response(stream),a.wav_bytes);
    requireThat(wav.length===a.wav_bytes&&await sha256(wav)===a.wav_sha256,'decoded WAV hash mismatch');
    pcm.set(a.id,decodeWAV(wav,a));
  }
  return {manifest,pcm};
}

// Upload only before sealing. A fresh memory view follows each allocation.
export function prepareSampler(exports,rate,pack,seedOverride) {
  const m=pack.manifest,c=m.config,h=c.Humanize;
  requireThat(exports.sampler_init(rate,m.zones.length)===0,'sampler initialization rejected');
  const seedValue=seedOverride ?? h.Seed;requireThat(typeof seedValue==='string'&&/^\d+$/.test(seedValue)||Number.isSafeInteger(seedValue),'seed must be an exact uint64 string or safe integer');
  const seed=BigInt(seedValue);requireThat(seed>=0n&&seed<=0xffffffffffffffffn,'seed outside uint64');
  const setup=[c.Voices,c.Amp.Attack,c.Amp.Decay,c.Amp.Sustain,c.Amp.Release,c.Filter.Attack,c.Filter.Decay,c.Filter.Sustain,c.Filter.Release,c.Cutoff,c.FilterDepth,c.Gain,c.TuneCents,h.DelayMS,h.Velocity,h.Cents,Number(seed&0xffffffffn),Number(seed>>32n)];
  new Float64Array(exports.memory.buffer,exports.sampler_config_ptr(),18).set(setup);
  const assets=new Map();
  for(const a of m.assets) {const index=exports.sampler_pcm_alloc(a.frames,a.channels,a.rate);requireThat(index>=0,'PCM budget rejected');assets.set(a.id,index);const pcm=pack.pcm.get(a.id);for(let ch=0;ch<a.channels;ch++)new Float32Array(exports.memory.buffer,exports.sampler_pcm_ptr(index,ch),a.frames).set(pcm[ch]);}
  for(let i=0;i<m.zones.length;i++) {const z=m.zones[i];const values=[assets.get(z.asset),z.Root,z.KeyLow,z.KeyHigh,z.VelocityLow,z.VelocityHigh,z.Layer,z.Group,z.Position,z.Count,+z.Release,z.Gain,z.TuneCents,z.Start,z.End,+z.Loop,z.LoopStart,z.LoopEnd,z.Crossfade,(z.ChokeGroup??0)+(z.OneShot?256:0)];new Float64Array(exports.memory.buffer,exports.sampler_zone_ptr(i),20).set(values);}
  requireThat(exports.sampler_prepare()===0,'sampler map/configuration rejected');
  return exports;
}

// Fetch outside the worklet, then publish the fully prepared immutable pack.
export async function createSampleInstrument(context,options) {
  const pack=await loadPack(new URL(options.manifestURL,location.href).href,options.sha256,options);
  const wasmResponse=await fetch(options.wasmURL,{signal:options.signal});requireThat(wasmResponse.ok,'sampler kernel fetch failed');const module=await WebAssembly.compile(await wasmResponse.arrayBuffer());
  await context.audioWorklet.addModule(options.processorURL);
  const node=new AudioWorkletNode(context,'cicada-sampler',{numberOfInputs:0,numberOfOutputs:1,outputChannelCount:[2],processorOptions:{module,pack,seed:options.seed}});
  await new Promise((resolve,reject)=>{node.port.onmessage=e=>{if(e.data.kind==='ready')resolve();if(e.data.kind==='error')reject(new Error(e.data.message));};node.onprocessorerror=()=>reject(new Error('sampler processor initialization failed'));});
  return node;
}
