// Measure cold full-kit download through first audible AudioWorklet output.
// Arguments: gzip packs, tier distribution, output JSON, optional trials.
import {createRequire} from 'node:module';
import {readFile,writeFile} from 'node:fs/promises';
import {createReadStream} from 'node:fs';
import http from 'node:http';
import path from 'node:path';
const require=createRequire(import.meta.url);
const {chromium}=require(process.env.PLAYWRIGHT_MODULE??'playwright');
const [gzipRoot,tierRoot,output,trials='3']=process.argv.slice(2);
if(!output)throw Error('gzip packs, tier distribution and external output JSON required');
const roots={gzip:path.resolve(gzipRoot),lossless:path.resolve(tierRoot,'lossless/full-kit-packs'),hq16:path.resolve(tierRoot,'hq16/full-kit-packs')};
const catalogs=Object.fromEntries(await Promise.all(Object.entries(roots).map(async([tier,root])=>[tier,JSON.parse(await readFile(path.join(root,'catalog.json'),'utf8'))])));
const server=http.createServer((req,res)=>{
  const pathname=new URL(req.url,'http://localhost').pathname;
  if(pathname==='/'){res.setHeader('Content-Type','text/html');res.end('<!doctype html><title>Pack load timing</title>');return;}
  let file={'/instrument-pack.js':'host/web/instrument-pack.js','/audio-encoding.js':'host/web/audio-encoding.js','/sampler-processor.js':'host/web/sampler-processor.js','/sampler.wasm':'build/cicada-sampler.wasm','/baseline.js':process.env.BASELINE_LOADER}[pathname];
  const match=/^\/(gzip|lossless|hq16)\/(.*)$/.exec(pathname);
  if(match){const root=roots[match[1]];file=path.resolve(root,match[2]);if(!file.startsWith(root+path.sep)){res.writeHead(403);res.end();return;}}
  if(!file){res.writeHead(404);res.end();return;}
  res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.wasm')?'application/wasm':'application/octet-stream');
  res.setHeader('Cache-Control','no-store');
  createReadStream(file).on('error',()=>res.destroy()).pipe(res);
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const browser=await chromium.launch({executablePath:process.env.CHROME_BIN,headless:true,args:['--no-sandbox','--autoplay-policy=no-user-gesture-required','--mute-audio']});
const results=[];
try{
  for(let trial=0;trial<Number(trials);trial++)for(const tier of ['gzip','hq16','lossless']){
    const page=await browser.newPage();await page.goto('http://127.0.0.1:'+server.address().port);
    // A fixed 20 Mbit/s download link makes byte savings visible. Local server
    // and a fresh page/cache keep the comparison reproducible on one machine.
    const cdp=await page.context().newCDPSession(page);
    await cdp.send('Network.enable');await cdp.send('Network.setCacheDisabled',{cacheDisabled:true});
    await cdp.send('Network.emulateNetworkConditions',{offline:false,latency:20,downloadThroughput:2500000,uploadThroughput:2500000});
    const result=await page.evaluate(async({tier,catalog,baseline})=>{
      const {createSampleInstrument}=await import(baseline?'/baseline.js':'/instrument-pack.js');
      const context=new AudioContext({sampleRate:48000});await context.suspend();
      const nodes=[],start=performance.now();
      for(const pin of catalog.packs)nodes.push(await createSampleInstrument(context,{manifestURL:`/${tier}/${pin.manifest}`,sha256:pin.sha256,wasmURL:'/sampler.wasm',processorURL:'/sampler-processor.js',cache:false}));
      const preparedMS=performance.now()-start;
      const analyser=context.createAnalyser();analyser.fftSize=2048;nodes.forEach(n=>n.connect(analyser));analyser.connect(context.destination);
      await context.resume();const frame=Math.ceil(context.currentTime*48000)+512;
      nodes.forEach((node,i)=>node.port.postMessage({kind:'on',frame,id:1,note:catalog.packs[i].id.endsWith('shells')?38:51,velocity:100}));
      const pcm=new Float32Array(analyser.fftSize);let peak=0;
      for(let i=0;i<100;i++){await new Promise(resolve=>setTimeout(resolve,10));analyser.getFloatTimeDomainData(pcm);peak=Math.max(...pcm.map(Math.abs));if(peak>1e-5)break;}
      if(peak<=1e-5)throw Error('silent first play');
      const firstPlayMS=performance.now()-start;nodes.forEach(n=>{n.disconnect();n.port.close();});await context.close();
      return {tier,prepared_ms:preparedMS,first_play_ms:firstPlayMS,peak,encoded_bytes:catalog.packs.reduce((n,p)=>n+p.compressed_bytes,0)};
    },{tier,catalog:catalogs[tier],baseline:tier==='gzip'&&!!process.env.BASELINE_LOADER});
    results.push({trial,...result});console.log(JSON.stringify(results.at(-1)));await page.close();
  }
  await writeFile(output,JSON.stringify({browser:await browser.version(),download_mbit_s:20,latency_ms:20,cold_cache:true,results},null,2)+'\n');
}finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
