const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {webcrypto} = require('node:crypto');
globalThis.crypto = webcrypto;
const source = fs.readFileSync(path.join(__dirname,'audio-encoding.js'),'utf8');
const modulePromise = import('data:text/javascript;base64,'+Buffer.from(source).toString('base64'));
async function hash(b) {return Buffer.from(await webcrypto.subtle.digest('SHA-256',b)).toString('hex');}
async function fixture(name='integer.flac') {
  const m=await modulePromise, encoded=new Uint8Array(fs.readFileSync(path.join(__dirname,'../audioencoding/testdata',name)));
  const quiet=name==='quiet.wav.gz', frames=quiet?640:896;
  const values=quiet?[0,2**-20,-(2**-25),2**-30,-(2**-38)]:[0,1/32768,-1/32768,.25,-.5,32767/32768,-1];
  const pcm=[Float32Array.from({length:frames},(_,i)=>values[i%values.length])];
  const a={encoding:quiet?'wav-gzip':'flac',sha256:await hash(encoded),bytes:encoded.length,pcm_sha256:await m.pcmHash(pcm),frames,rate:48000,channels:1,scale:1};
  if(quiet)a.decoded_bytes=require('node:zlib').gunzipSync(encoded).length;
  return {m,encoded,a,pcm};
}
test('checks encoded and decoded pins with a rate-preserving decoder',async()=>{
  const {m,encoded,a,pcm}=await fixture();let calls=0;
  const context={sampleRate:48000,async decodeAudioData(){calls++;return {length:a.frames,numberOfChannels:1,sampleRate:48000,getChannelData:()=>pcm[0]};}};
  const out=await m.decodeEncodedAudio(encoded,a,{context});assert.deepEqual(out,pcm);assert.equal(calls,1);
  const bad=encoded.slice();bad[bad.length-1]^=1;await assert.rejects(m.decodeEncodedAudio(bad,a,{context}),/hash\/size/);assert.equal(calls,1);
  await assert.rejects(m.decodeEncodedAudio(encoded,{...a,frames:a.frames+1},{context}),/dimensions/);assert.equal(calls,1);
  await assert.rejects(m.decodeEncodedAudio(encoded,a,{context:{...context,sampleRate:44100}}),/resample/);
  await assert.rejects(m.decodeEncodedAudio(encoded,{...a,pcm_sha256:'0'.repeat(64)},{context}),/PCM hash/);
});
test('power-of-two scale reconstructs quiet PCM exactly',async()=>{
  const {m,encoded,a,pcm}=await fixture();a.scale=2**-20;const scaled=[Float32Array.from(pcm[0],x=>x*a.scale)];a.pcm_sha256=await m.pcmHash(scaled);
  const context={sampleRate:48000,async decodeAudioData(){return {length:a.frames,numberOfChannels:1,sampleRate:48000,getChannelData:()=>pcm[0]};}};
  assert.deepEqual(await m.decodeEncodedAudio(encoded,a,{context}),scaled);
  for(const scale of [0,-1,.3,Infinity,NaN,2**-149])assert.throws(()=>m.validateAudioEncoding({...a,scale}),/bounds or scale/);
});
test('float32 fallback retains quiet tails and rejects expansion',async()=>{
  const {m,encoded,a,pcm}=await fixture('quiet.wav.gz');assert.deepEqual(await m.decodeEncodedAudio(encoded,a),pcm);
  await assert.rejects(m.decodeEncodedAudio(encoded,{...a,decoded_bytes:a.decoded_bytes-1}),/exceeds limit/);
  const trailing=new Uint8Array(encoded.length*2);trailing.set(encoded);trailing.set(encoded,encoded.length);
  await assert.rejects(m.decodeEncodedAudio(trailing,{...a,bytes:trailing.length,sha256:await hash(trailing)}),/exceeds limit|Trailing junk/);
});

test('canonicalizes asymmetric browser PCM16 normalization with the pinned hash',async()=>{
  const {m,encoded,a,pcm}=await fixture();a.scale=2**-20;const scaled=[Float32Array.from(pcm[0],x=>x*a.scale)];a.pcm_sha256=await m.pcmHash(scaled);
  const browser=Float32Array.from(pcm[0],x=>x>0?Math.round(x*32768)/32767:x);
  const context={sampleRate:48000,async decodeAudioData(){return {length:a.frames,numberOfChannels:1,sampleRate:48000,getChannelData:()=>browser};}};
  assert.deepEqual(await m.decodeEncodedAudio(encoded,a,{context}),scaled);
  await assert.rejects(m.decodeEncodedAudio(encoded,{...a,pcm_sha256:'0'.repeat(64)},{context}),/PCM hash/);
});
