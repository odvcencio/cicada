// Host-side decoding of immutable audio. Never call from an audio callback.
const limit = 256 * 1024 * 1024;
const hashPattern = /^[a-f0-9]{64}$/;
function requireThat(ok, message) { if (!ok) throw new Error(message); }
export function powerOfTwo(value) {
  if (!Number.isFinite(value) || value <= 0 || Math.fround(value) !== value) return false;
  const a = new Float32Array([value]); const b = new Uint32Array(a.buffer)[0];
  return (b & 0x807fffff) === 0 && (b & 0x7f800000) !== 0 && (b & 0x7f800000) !== 0x7f800000;
}
export function validateAudioEncoding(a) {
  requireThat(hashPattern.test(a.sha256) && hashPattern.test(a.pcm_sha256), 'invalid encoded audio hash');
  requireThat([a.bytes, a.frames, a.rate, a.channels].every(Number.isSafeInteger) && a.bytes > 0 && a.bytes <= limit && a.frames > 0 && a.frames <= 8*1024*1024 && a.rate >= 8000 && a.rate <= 192000 && a.channels >= 1 && a.channels <= 2 && a.frames*a.channels*4 <= limit && powerOfTwo(a.scale), 'invalid encoded audio bounds or scale');
  return a;
}
async function hash(bytes) {
  const d = await crypto.subtle.digest('SHA-256', bytes);
  return Array.from(new Uint8Array(d), x => x.toString(16).padStart(2, '0')).join('');
}
export async function pcmHash(pcm) {
  const length = pcm.reduce((n, ch) => n+ch.length, 0);
  const bytes = new Uint8Array(length*4); const v = new DataView(bytes.buffer); let offset=0;
  for (const channel of pcm) for (const x of channel) { v.setFloat32(offset, x, true); offset += 4; }
  return hash(bytes);
}
function flacDimensions(bytes, a) {
  requireThat(bytes.length >= 42 && String.fromCharCode(...bytes.subarray(0, 4)) === 'fLaC' && (bytes[4]&127) === 0 && bytes[5] === 0 && bytes[6] === 0 && bytes[7] === 34, 'invalid FLAC STREAMINFO');
  const v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const fields = v.getBigUint64(18, false);
  const rate = Number(fields >> 44n), channels = Number((fields >> 41n)&7n)+1;
  const bits = Number((fields >> 36n)&31n)+1, frames = Number(fields&0xfffffffffn);
  requireThat(rate === a.rate && channels === a.channels && frames === a.frames && bits >= 4 && bits <= 32, 'FLAC dimensions mismatch');
  return bits;
}
// A fresh OfflineAudioContext preserves the authored rate independently of the
// playback device. Callers may provide a context with the same sample rate.
export async function decodeEncodedFLAC(bytes, a, options={}) {
  validateAudioEncoding(a);
  requireThat(a.encoding==='flac','expected FLAC encoding');
  requireThat(bytes.length === a.bytes && await hash(bytes) === a.sha256, 'encoded audio hash/size mismatch');
  const bits = flacDimensions(bytes, a);
  const context = options.context ?? new OfflineAudioContext(1, 1, a.rate);
  requireThat(context.sampleRate === a.rate, 'decoder context would resample audio');
  const data = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset+bytes.byteLength);
  const audio = await context.decodeAudioData(data);
  requireThat(audio.length === a.frames && audio.numberOfChannels === a.channels && audio.sampleRate === a.rate, 'decoded FLAC dimensions mismatch');
  const pcm = Array.from({length:a.channels}, () => new Float32Array(a.frames));
  for (let ch=0; ch<a.channels; ch++) {
    const source = audio.getChannelData(ch);
    for (let i=0; i<a.frames; i++) { const x = Math.fround(source[i]*a.scale); requireThat(Number.isFinite(x), 'nonfinite reconstructed PCM'); pcm[ch][i]=x; }
  }
  if (await pcmHash(pcm) === a.pcm_sha256) return pcm;
  // Some browser decoders normalize positive integer samples by 2^(bits-1)-1.
  // Restore the encoded integer grid, accepting only the pinned reconstruction.
  const denominator = 2**(bits-1);
  for (const positiveDenominator of [denominator, denominator-1]) {
    for (let ch=0; ch<a.channels; ch++) {
      const source=audio.getChannelData(ch);
      for (let i=0; i<a.frames; i++) {
        const integer=Math.max(-denominator,Math.min(denominator-1,Math.round(source[i]*(source[i]>0?positiveDenominator:denominator))));
        pcm[ch][i]=Math.fround(integer/denominator*a.scale);
      }
    }
    if (await pcmHash(pcm) === a.pcm_sha256) return pcm;
  }
  throw new Error('decoded PCM hash mismatch');
}
function wavDimensions(bytes, a) {
  const v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const tag = o => String.fromCharCode(...bytes.subarray(o,o+4));
  requireThat(bytes.length>=44 && tag(0)==='RIFF' && tag(8)==='WAVE' && v.getUint32(4,true)+8===bytes.length, 'invalid WAV container');
  let format=null, data=null;
  for(let o=12;o<bytes.length;) {
    requireThat(o+8<=bytes.length,'truncated WAV chunk');const n=v.getUint32(o+4,true),start=o+8;requireThat(start+n+(n&1)<=bytes.length,'WAV chunk exceeds container');
    if(tag(o)==='fmt '){requireThat(!format&&n>=16,'invalid WAV format');format={encoding:v.getUint16(start,true),channels:v.getUint16(start+2,true),rate:v.getUint32(start+4,true),byteRate:v.getUint32(start+8,true),align:v.getUint16(start+12,true),bits:v.getUint16(start+14,true)};}
    if(tag(o)==='data'){requireThat(!data,'duplicate WAV data');data=n;}
    o=start+n+(n&1);
  }
  requireThat(format&&data&&(format.encoding===1&&[16,24,32].includes(format.bits)||format.encoding===3&&format.bits===32),'unsupported WAV format');
  requireThat(format.rate===a.rate&&format.channels===a.channels&&format.align===a.channels*format.bits/8&&format.byteRate===a.rate*format.align&&data===a.frames*format.align,'WAV dimensions mismatch');
}
export async function decodeEncodedAudio(encoded,a,options={}) {
  requireThat(a.encoding==='flac'||a.encoding==='wav-gzip','unsupported audio encoding');
  if(a.encoding==='flac') return decodeEncodedFLAC(encoded,a,options);
  validateAudioEncoding(a);
  requireThat(a.scale===1&&Number.isSafeInteger(a.decoded_bytes)&&a.decoded_bytes>=44&&a.decoded_bytes<=limit,'invalid gzip WAV bounds');
  requireThat(encoded.length===a.bytes&&await hash(encoded)===a.sha256,'encoded audio hash/size mismatch');
  const stream=new Blob([encoded]).stream().pipeThrough(new DecompressionStream('gzip'));
  const reader=stream.getReader(),parts=[];let n=0;
  try{for(;;){const {value,done}=await reader.read();if(done)break;n+=value.length;requireThat(n<=a.decoded_bytes,'decoded WAV exceeds limit');parts.push(value);}}finally{await reader.cancel();}
  requireThat(n===a.decoded_bytes,'decoded WAV size mismatch');
  const bytes=new Uint8Array(n);let o=0;for(const p of parts){bytes.set(p,o);o+=p.length;}
  wavDimensions(bytes,a);
  // Decode WAV bytes directly. Browser PCM normalization must not round away
  // the quiet float32 tails that require this exact fallback.
  const v=new DataView(bytes.buffer);let fmt=null,start=0;
  for(let o=12;o<bytes.length;){const tag=String.fromCharCode(...bytes.subarray(o,o+4)),size=v.getUint32(o+4,true);if(tag==='fmt ')fmt={encoding:v.getUint16(o+8,true),bits:v.getUint16(o+22,true)};if(tag==='data')start=o+8;o+=8+size+(size&1);}
  const pcm=Array.from({length:a.channels},()=>new Float32Array(a.frames));const width=fmt.bits/8;o=start;
  for(let i=0;i<a.frames;i++)for(let ch=0;ch<a.channels;ch++){let x;if(fmt.encoding===3)x=v.getFloat32(o,true);else if(width===2)x=v.getInt16(o,true)/32768;else if(width===3)x=((v.getUint8(o)|v.getUint8(o+1)<<8|v.getUint8(o+2)<<16)<<8>>8)/8388608;else x=v.getInt32(o,true)/2147483648;requireThat(Number.isFinite(x),'nonfinite source PCM');pcm[ch][i]=x;o+=width;}
  requireThat(await pcmHash(pcm)===a.pcm_sha256,'decoded PCM hash mismatch');return pcm;
}
