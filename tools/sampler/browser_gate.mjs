// Browser gate for lazy cache admission and a real AudioWorklet. Run after
// make build-sampler-wasm. PLAYWRIGHT_MODULE may name an installed entrypoint.
import {createRequire} from 'node:module';
import {readFile} from 'node:fs/promises';
import http from 'node:http';
const require=createRequire(import.meta.url);
const {chromium}=require(process.env.PLAYWRIGHT_MODULE??'playwright');
const server=http.createServer(async(req,res)=>{
 const files={'/instrument-pack.js':'host/web/instrument-pack.js','/sampler-processor.js':'host/web/sampler-processor.js','/sampler.wasm':'build/cicada-sampler.wasm'};
 try {if(req.url==='/'){res.setHeader('Content-Type','text/html');res.end('<!doctype html><title>Sampler gate</title>');return;}const path=files[req.url];if(!path){res.writeHead(404);res.end();return;}res.setHeader('Content-Type',path.endsWith('.wasm')?'application/wasm':'text/javascript');res.end(await readFile(path));}catch(e){res.writeHead(500);res.end(String(e));}
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const url='http://127.0.0.1:'+server.address().port;
const browser=await chromium.launch({executablePath:process.env.CHROME_BIN??'/usr/bin/google-chrome',headless:true,args:['--no-sandbox','--autoplay-policy=no-user-gesture-required','--mute-audio']});
try {
 const page=await browser.newPage();await page.goto(url);
 const result=await page.evaluate(async()=>{
  const {sha256,loadPack,createSampleInstrument}=await import('/instrument-pack.js');
  const wav=new Uint8Array(44+4096*2);const v=new DataView(wav.buffer);const tag=(offset,s)=>{for(let i=0;i<s.length;i++)wav[offset+i]=s.charCodeAt(i);};tag(0,'RIFF');v.setUint32(4,wav.length-8,true);tag(8,'WAVEfmt ');v.setUint32(16,16,true);v.setUint16(20,1,true);v.setUint16(22,1,true);v.setUint32(24,48000,true);v.setUint32(28,96000,true);v.setUint16(32,2,true);v.setUint16(34,16,true);tag(36,'data');v.setUint32(40,8192,true);for(let i=44;i<wav.length;i+=2)v.setInt16(i,(i%192<96?8192:-8192),true);
  const zipped=new Uint8Array(await new Response(new Blob([wav]).stream().pipeThrough(new CompressionStream('gzip'))).arrayBuffer());
  const asset={id:'test',path:'sample.wav.gz',sha256:await sha256(zipped),bytes:zipped.length,wav_sha256:await sha256(wav),wav_bytes:wav.length,frames:4096,rate:48000,channels:1,source_url:'https://example.org/test.wav',source_sha256:await sha256(wav),license:'CC0-1.0',license_url:'https://creativecommons.org/publicdomain/zero/1.0/'};
  const z={asset:'test',Root:60,KeyLow:60,KeyHigh:60,VelocityLow:1,VelocityHigh:127,Layer:64,Group:0,Position:0,Count:1,Release:false,Gain:.1,TuneCents:0,Start:0,End:4096,Loop:true,LoopStart:0,LoopEnd:4096,Crossfade:0,ChokeGroup:0,OneShot:false};
  const config={Voices:8,Amp:{Attack:2,Decay:0,Sustain:1,Release:10},Filter:{Attack:0,Decay:0,Sustain:1,Release:0},Cutoff:0,FilterDepth:0,Gain:1,TuneCents:0,Humanize:{DelayMS:0,Velocity:0,Cents:0,Seed:'18446744073709551615'}};
  const manifest=new TextEncoder().encode(JSON.stringify({format:'cicada.instrument-pack/1',id:'test',config,assets:[asset],zones:[z]}));const pin=await sha256(manifest);
  const manifestURL=location.origin+'/fixture/manifest.json';const sampleURL=location.origin+'/fixture/sample.wav.gz';let requests=0;const entries=new Map();
  const cache={match:async url=>entries.has(url)?entries.get(url).clone():null,put:async(url,response)=>entries.set(url,response.clone())};
  const fetcher=async url=>{requests++;return new Response(url===manifestURL?manifest:zipped);};
  await loadPack(manifestURL,pin,{cache,fetch:fetcher});await loadPack(manifestURL,pin,{cache,fetch:()=>{throw Error('offline cache missed')}});if(requests!==2)throw Error('not lazy/cached');
  let rejected=false;try{await loadPack(manifestURL,'0'.repeat(64),{cache,fetch:fetcher});}catch{rejected=true;}if(!rejected)throw Error('wrong pin accepted');
  const context=new AudioContext({sampleRate:48000});await context.resume();
  const node=await createSampleInstrument(context,{manifestURL,sha256:pin,wasmURL:'/sampler.wasm',processorURL:'/sampler-processor.js',cache,fetch:fetcher});
  const analyser=context.createAnalyser();analyser.fftSize=2048;node.connect(analyser);analyser.connect(context.destination);
  let failure=null;node.onprocessorerror=()=>failure='processor fault';node.port.onmessage=e=>{if(e.data.kind==='error')failure=e.data.message;};
  const at=Math.ceil(context.currentTime*context.sampleRate)+4096;for(let i=0;i<8;i++)node.port.postMessage({kind:'on',frame:at,id:i+1,note:60,velocity:64,seed:String(i+123)});
  await new Promise(resolve=>setTimeout(resolve,180));const pcm=new Float32Array(analyser.fftSize);analyser.getFloatTimeDomainData(pcm);const peak=Math.max(...pcm.map(Math.abs));if(!(peak>.01)||failure)throw Error(failure??'silent worklet');
  node.port.postMessage({kind:'reset',frame:Math.ceil(context.currentTime*context.sampleRate)+128});await new Promise(resolve=>setTimeout(resolve,100));analyser.getFloatTimeDomainData(pcm);const resetPeak=Math.max(...pcm.map(Math.abs));if(resetPeak!==0)throw Error('reset noise');
  node.disconnect();await context.close();
  return {lazy_requests:requests,offline_cache:true,corrupt_pin_rejected:rejected,worklet_peak:peak,reset_peak:resetPeak,exact_uint64_seed:true};
 });
 console.log(JSON.stringify({browser:await browser.version(),...result}));
}finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
