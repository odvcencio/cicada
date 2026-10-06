// Verify real kit admission and playback in Chromium's AudioWorklet.
// Set PLAYWRIGHT_MODULE and CHROME_BIN to existing installations.
import {createRequire} from 'node:module';
import {readFile,writeFile} from 'node:fs/promises';
import {createReadStream} from 'node:fs';
import http from 'node:http';
import path from 'node:path';
const require=createRequire(import.meta.url);
const {chromium}=require(process.env.PLAYWRIGHT_MODULE??'playwright');
const root=path.resolve(process.argv[2]??'build/kit-final/packs');
const catalog=JSON.parse(await readFile(path.join(root,'catalog.json'),'utf8'));
const server=http.createServer(async(req,res)=>{
 const files={'/instrument-pack.js':'host/web/instrument-pack.js','/sampler-processor.js':'host/web/sampler-processor.js','/sampler.wasm':'build/cicada-sampler.wasm'};
 try {
  if(req.url==='/'){res.setHeader('Content-Type','text/html');res.end('<!doctype html><title>Full kit verification</title>');return;}
  let file=files[req.url];
  if(req.url.startsWith('/packs/')) {file=path.resolve(root,req.url.slice(7));if(!file.startsWith(root+path.sep)){res.writeHead(403);res.end();return;}}
  if(!file){res.writeHead(404);res.end();return;}
  res.setHeader('Content-Type',file.endsWith('.wasm')?'application/wasm':file.endsWith('.js')?'text/javascript':file.endsWith('.json')?'application/json':'application/octet-stream');
  createReadStream(file).on('error',()=>{res.destroy();}).pipe(res);
 }catch(e){res.writeHead(500);res.end(String(e));}
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const browser=await chromium.launch({executablePath:process.env.CHROME_BIN,headless:true,args:['--no-sandbox','--autoplay-policy=no-user-gesture-required','--mute-audio']});
try {
 const results=[];
 for(const pin of catalog.packs) {
  const page=await browser.newPage();await page.goto('http://127.0.0.1:'+server.address().port);
  const result=await page.evaluate(async pin=>{
   const {createSampleInstrument,loadPack}=await import('/instrument-pack.js');
   const context=new AudioContext({sampleRate:48000});await context.suspend();
   let requests=0;
   const fetcher=(...args)=>{requests++;return fetch(...args);};
   const cache=await caches.open('full-kit-'+pin.sha256);
   const manifestURL=location.origin+'/packs/'+pin.manifest;
   const node=await createSampleInstrument(context,{manifestURL,sha256:pin.sha256,wasmURL:'/sampler.wasm',processorURL:'/sampler-processor.js',cache,fetch:fetcher});
   if(context.state!=='suspended')throw Error('prepared on running context');
   const analyser=context.createAnalyser();analyser.fftSize=2048;node.connect(analyser);analyser.connect(context.destination);
   let failure=null;node.onprocessorerror=()=>failure='processor fault';node.port.onmessage=e=>{if(e.data.kind==='error')failure=e.data.message;};
   const note=pin.id.endsWith('shells')?38:51;
   await context.resume();node.port.postMessage({kind:'on',frame:Math.ceil(context.currentTime*48000)+4096,id:1,note,velocity:100,seed:'4242'});
   await new Promise(resolve=>setTimeout(resolve,240));const pcm=new Float32Array(analyser.fftSize);analyser.getFloatTimeDomainData(pcm);const peak=Math.max(...pcm.map(Math.abs));if(!(peak>1e-5)||failure)throw Error(failure??'silent real kit');
   node.port.postMessage({kind:'reset',frame:Math.ceil(context.currentTime*48000)+128});await new Promise(resolve=>setTimeout(resolve,140));analyser.getFloatTimeDomainData(pcm);const resetPeak=Math.max(...pcm.map(Math.abs));if(resetPeak!==0)throw Error('reset noise');
   node.disconnect();await context.close();
   await loadPack(manifestURL,pin.sha256,{cache,fetch:()=>{throw Error('offline cache missed');}});
   if(requests!==pin.assets+1)throw Error('unexpected lazy bank fetches');
   await caches.delete('full-kit-'+pin.sha256);
   return {bank:pin.id,manifest_sha256:pin.sha256,assets:pin.assets,lazy_requests:requests,prepared_suspended:true,offline_cache:true,worklet_peak:peak,reset_peak:resetPeak};
  },pin);
  results.push(result);console.log(JSON.stringify(result));await page.close();
 }
 await writeFile('build/kit-reports/full-kit-browser.json',JSON.stringify({browser:await browser.version(),results},null,2)+'\n');
}finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
