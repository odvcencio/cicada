'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const {OPFSStore,openStore,CaptureWriter,recoverJournal,placement}=require('./capture-worker.js');
const {inspectSettings,BrowserCapture,TakeSampler,publishTake,PublishedSampler}=require('./capture-client.js');

function block(frames=4,gap=0) {
  return {EngineEpoch:1,DeviceEpoch:2,EngineFrame:0,DeviceFrame:0,SampleRate:48000,Period:frames,Frames:frames,Layout:1,Flags:gap?2:0,GapFrames:gap,InputLatencyValid:false,OutputLatencyValid:false,InputLatencyNano:0,OutputLatencyNano:0,Calibration:{DeviceEpoch:2,RemainingFrames:0,Valid:false}};
}
function packet(frames=4,gap=0) { return {t:'pcm',bytes:new Float32Array(Array.from({length:frames},(_,i)=>i/10)).buffer,timing:block(frames,gap)}; }
function fakeOPFS() {
  const files=new Map();let fail='';
  function dir(prefix='') {
    return {
      async getDirectoryHandle(name){return dir(prefix+name+'/');},
      async getFileHandle(name,opts){
        const key=prefix+name;
        if(!files.has(key)) { if(!opts.create) throw new Error('not found');files.set(key,new Uint8Array()); }
        return {
          async createWritable(){return {async write(value){files.set(key,new TextEncoder().encode(value));},async close(){}};},
          async getFile(){return new Blob([files.get(key)]);},
          async createSyncAccessHandle(){
            let staging=files.get(key).slice();
            return {
              write(bytes,{at}) {
                if(fail===name+':write') throw new Error('crash at '+fail);
                const size=Math.max(staging.length,at+bytes.length),next=new Uint8Array(size);next.set(staging);next.set(bytes,at);staging=next;return bytes.length;
              },
              flush(){if(fail===name+':flush')throw new Error('crash at '+fail);files.set(key,staging.slice());},close(){}
            };
          }
        };
      }
    };
  }
  return {storage:{async getDirectory(){return dir();}},files,crash(stage){fail=stage;}};
}
function fakeIDB() {
  const values=new Map();let fail=false;
  const db={
    createObjectStore(){},close(){},
    transaction(_name,mode){
      const tx={error:null};const pending=new Map();let aborted=false;
      tx.abort=()=>{aborted=true;queueMicrotask(()=>tx.onabort?.());};
      tx.objectStore=()=>({
        put(value,key){if(fail) {tx.error=new Error('quota');tx.abort();return;}pending.set(key,structuredClone(value));},
        get(key){const r={};queueMicrotask(()=>{r.result=structuredClone(values.get(key));r.onsuccess?.();});return r;}
      });
      queueMicrotask(()=>{if(aborted)return;for(const [k,v] of pending)values.set(k,v);tx.oncomplete?.();});return tx;
    }
  };
  return {indexedDB:{open(){const r={};queueMicrotask(()=>{r.result=db;r.onupgradeneeded?.();r.onsuccess?.();});return r;}},values,fail(){fail=true;}};
}
const metadata={channels:1,sampleRate:48000};

test('OPFS write/recover preserves raw PCM, descriptor, preroll and finalization',async()=>{
  const fake=fakeOPFS(),store=await OPFSStore.open(fake.storage,'one',metadata),writer=new CaptureWriter(store);
  const p=packet();p.timing.EngineFrame=-4;
  await writer.append(p);await writer.append(packet(4,3));await writer.finish(2);
  const reopened=await OPFSStore.open(fake.storage,'one',null,true),take=await reopened.recover();
  assert.deepEqual(Array.from(new Float32Array(take.pcm)),Array.from(new Float32Array([...new Float32Array(p.bytes),0,.1,.2,.3])));
  assert.equal(take.blocks[0].placement.engineFrame,-4);assert.equal(take.blocks[1].rawFrame,7);
  assert.equal(take.rawFrames,13);assert.equal(take.incomplete,true);assert.equal(take.bytes,32);
});
for(const stage of ['pcm.f32:write','pcm.f32:flush','journal.jsonl:write','journal.jsonl:flush']) {
  test('OPFS crash at '+stage+' retains the previous committed block',async()=>{
    const fake=fakeOPFS(),store=await OPFSStore.open(fake.storage,'crash',metadata),writer=new CaptureWriter(store);
    await writer.append(packet());fake.crash(stage);await assert.rejects(writer.append(packet()),/crash/);store.close();
    const reopened=await OPFSStore.open(fake.storage,'crash',null,true),take=await reopened.recover();
    assert.equal(take.blocks.length,1);assert.equal(take.bytes,16);assert.equal(take.incomplete,true);
  });
}
test('recovery ignores partial JSON and truncated PCM; finalized take survives reopen',async()=>{
  const fake=fakeOPFS(),store=await OPFSStore.open(fake.storage,'clean',metadata),writer=new CaptureWriter(store);
  await writer.append(packet());await writer.finish(0);
  const take=await (await OPFSStore.open(fake.storage,'clean',null,true)).recover();assert.equal(take.incomplete,false);
  const journal=[...fake.files].find(([key])=>key.endsWith('journal.jsonl'))[1],text=new TextDecoder().decode(journal);
  assert.equal(recoverJournal(text,8,1,48000).blocks.length,0);
  assert.equal(recoverJournal(text.split('\n')[0],16,1,48000).blocks.length,0);
  assert.equal(recoverJournal(text.split('\n')[0]+'\n{"type":',16,1,48000).blocks.length,1);
});
test('IndexedDB durable fallback commits PCM+record atomically and recovers',async()=>{
  const fake=fakeIDB();
  const env={navigator:{storage:{getDirectory:async()=>{throw new Error('denied');}}},indexedDB:fake.indexedDB};
  const store=await openStore(env,'fallback',metadata);assert.equal(store.mode,'indexeddb');
  const writer=new CaptureWriter(store);await writer.append(packet());await writer.finish(0);
  const recovered=await openStore(env,'fallback',null,true,'indexeddb'),take=await recovered.recover();
  assert.equal(take.blocks.length,1);assert.equal(take.incomplete,false);assert.equal(take.bytes,16);
  const other=await openStore(env,'quota',metadata),w=new CaptureWriter(other);fake.fail();
  await assert.rejects(w.append(packet()),/quota/);assert.equal(w.incomplete,true);
});
test('storage absence rejects admission; a failed recovery never changes backend',async()=>{
  await assert.rejects(openStore({},'none',metadata),/unavailable/);
  const fake=fakeIDB();await assert.rejects(openStore({indexedDB:fake.indexedDB},'missing',null,true,'opfs'),/unavailable/);
});
test('effective microphone processing settings are inspected, including unreported values',()=>{
  assert.deepEqual(inspectSettings({getSettings:()=>({echoCancellation:true,noiseSuppression:false,latency:.01})}),{echoCancellation:true,noiseSuppression:false,autoGainControl:'unreported',latency:.01});
  assert.equal(inspectSettings({}).echoCancellation,'unreported');
  assert.equal(placement(block()).timingConfidence,0);
});

function adapterHarness() {
  const context={};vm.createContext(context);vm.runInContext(fs.readFileSync(__dirname+'/capture.js','utf8'),context);
  const writes=[],controls=[],port={postMessage(p){writes.push(structuredClone(p));}},control={postMessage(p){controls.push(structuredClone(p));}};
  const adapter=new context.CicadaCapture({port,channels:1,epoch:2},48000,control);
  return {adapter,writes,controls,context,port};
}
test('worklet contract uses D descriptor, variable periods, count-in, raw input and pooled gaps',()=>{
  const {adapter,writes,controls}=adapterHarness(),inputs=[[new Float32Array([.1,.2,.3,.4])]];
  adapter.receive({op:'begin',countInFrames:6});
  assert.equal(adapter.process(inputs,4,0,120000,true),4);
  assert.equal(adapter.process(inputs,4,0,120000,true),2);
  assert.equal(writes[0].timing.EngineFrame,-6);assert.equal(writes[1].timing.EngineFrame,-2);
  assert.equal(writes[0].timing.Period,4);assert.equal(writes[0].timing.InputTime.Valid,false);
  assert.equal(writes[0].timing.OutputLatencyValid,false);
  assert.deepEqual(Array.from(new Float32Array(writes[0].bytes).slice(0,4)),Array.from(inputs[0][0]));
  for(let i=0;i<30;i++)adapter.process(inputs,4,2+i*4,120000,true);
  adapter.process(inputs,4,122,120000,true);assert.equal(adapter.gap,4);
  adapter.receive({op:'stop'});adapter.process(inputs,4,126,120000,false);
  assert.equal(writes.at(-1).t,'end');assert.equal(writes.at(-1).gapFrames,4);
  assert.deepEqual(controls.map(c=>c.t),['capture-started','capture-stopped']);
});
test('zero measurable typed allocations in 10,000 capture callbacks; gaps recycle explicitly',()=>{
  const {adapter,writes,context,port}=adapterHarness();const inputs=[[new Float32Array(128)]];
  let inProcess=false,allocations=0;const Native=context.Float32Array || vm.runInContext('Float32Array',context);
  context.Float32Array=new Proxy(Native,{construct(t,args){if(inProcess)allocations++;return Reflect.construct(t,args);}});
  adapter.receive({op:'begin',countInFrames:0});
  const envelopes=new Set(),transferLists=new Set();
  // The fake post clones synchronously like MessagePort and returns storage
  // outside process(), so callback construction can be measured independently.
  port.postMessage=(p,list)=>{envelopes.add(p);transferLists.add(list);writes.push(structuredClone(p));};
  for(let i=0;i<10000;i++){
    inProcess=true;adapter.process(inputs,128,i*128,120000,true);inProcess=false;
    const p=writes.pop();p.t='recycle';port.onmessage({data:p});
  }
  assert.equal(allocations,0);assert.equal(adapter.gap,0);assert.equal(adapter.count,32);
  // Identity remains bounded in actual worklet ownership; returned messages
  // are newly deserialized on the control path, outside the callback.
  console.log('METRIC capture_process_typed_allocations=0 callbacks=10000');
});
test('known middle gaps preserve rawFrame and placement; invalid blocks reject',async()=>{
  const fake=fakeOPFS(),store=await OPFSStore.open(fake.storage,'invalid',metadata),w=new CaptureWriter(store);
  const p=packet();p.timing.Layout=2;await assert.rejects(w.append(p),/Invalid/);assert.equal(w.incomplete,true);
});
test('sampler keeps gaps as silence, skips preroll, uses one voice and pitch/loop controls',()=>{
  const voices=[],ctx={destination:{},createBuffer(c,n){const pcm=Array.from({length:c},()=>new Float32Array(n));return {length:n,getChannelData:i=>pcm[i]};},createBufferSource(){const v={playbackRate:{value:1},connect(){},disconnect(){this.disconnected=true;},start(){},stop(){this.stopped=true;}};voices.push(v);return v;}};
  const sampler=new TakeSampler(ctx),b1=block(4),b2=block(4,2);
  b1.EngineFrame=-2;const p1=placement(b1),p2=placement(b2);
  sampler.load({metadata,rawFrames:10,incomplete:true,pcm:new Float32Array([1,2,3,4,5,6,7,8]).buffer,blocks:[{timing:b1,placement:p1,rawFrame:0,offset:0},{timing:b2,placement:p2,rawFrame:6,offset:16}]});
  assert.deepEqual(Array.from(sampler.buffer.getChannelData(0)),[3,4,0,0,5,6,7,8]);
  sampler.play(72,60,true);assert.equal(voices[0].playbackRate.value,2);assert.equal(voices[0].loop,true);
  sampler.play();assert.equal(voices[0].stopped,true);assert.equal(voices[0].disconnected,true);sampler.stop();assert.equal(voices[1].stopped,true);
});

function clientHarness(settings={channelCount:1,echoCancellation:true}) {
  const messages=[],workerMessages=[],requests=[],track={getSettings:()=>settings,stop(){this.stopped=true;}};
  class Worker {postMessage(data){workerMessages.push(data);queueMicrotask(()=>{if(data.t==='open')this.onmessage({data:{t:'opened',mode:'indexeddb'}});if(data.t==='recover')this.onmessage({data:{t:'recovered',blocks:[]}});});}terminate(){this.terminated=true;}}
  const node={port:{postMessage(d){messages.push(d);}}},context={sampleRate:48000,createMediaStreamSource(){return {connect(){},disconnect(){}};}};
  const audio={context,node,playing:false,bpmMilli:120000,stageCurrentScore:async()=>{},startAudio:async()=>{},contextLatencyMs:()=>10,play(){this.playing=true;},stop(){this.playing=false;}};
  const env={setTimeout,clearTimeout,Worker,MessageChannel:class {constructor(){this.port1={};this.port2={};}},crypto:{randomUUID:()=> 'fake'},localStorage:{setItem(){}},navigator:{mediaDevices:{getUserMedia:async(r)=>{requests.push(r);return {getAudioTracks:()=>[track],getTracks:()=>[track]};}}},fetch:async()=>{const image=new ArrayBuffer(16);new DataView(image).setUint32(12,120000,true);return {ok:true,arrayBuffer:async()=>image};}};
  return {client:new BrowserCapture(audio,env),messages,workerMessages,requests,track,audio};
}
test('client requests processing off, reports effective settings and wires worker/worklet input',async()=>{
  const h=clientHarness();await h.client.arm();
  assert.deepEqual(h.requests[0],{audio:{channelCount:{ideal:1},echoCancellation:false,noiseSuppression:false,autoGainControl:false},video:false});
  assert.equal(h.client.status.settings.echoCancellation,true);assert.equal(h.client.status.settings.noiseSuppression,'unreported');assert.equal(h.client.status.timing,'unavailable');assert.equal(h.client.status.storage,'indexeddb');
  assert.equal(h.messages[0].t,'capture-init');assert.equal(h.messages[0].inputLatencyValid,false);
  await h.client.record();assert.equal(h.messages[1].countInFrames,96000);assert.equal(h.audio.playing,true);
  const stopped=h.client.stop();h.client.receive({t:'finished',incomplete:false});await stopped;assert.equal(h.track.stopped,true);assert.equal(h.client.status.state,'stopped');
});
test('channel mismatch releases permission stream; worker faults mark incomplete and stop',async()=>{
  const h=clientHarness({channelCount:2});await assert.rejects(h.client.arm(1),/channel count/);assert.equal(h.track.stopped,true);
  const h2=clientHarness();await h2.client.arm();await h2.client.record();h2.client.receive({t:'fault',error:'quota'});
  assert.equal(h2.client.status.incomplete,true);assert.equal(h2.client.status.state,'saving');
  h2.client.receive({t:'finished',incomplete:true,error:'quota'});
});

test('worker message pipeline drains before finalization; disk fault includes every lost frame',async()=>{
  const fake=fakeOPFS(),responses=[],recycles=[],waits=new Map();
  const ctx={WorkerGlobalScope:{[Symbol.hasInstance]:()=>true},navigator:{storage:fake.storage},TextEncoder,Uint8Array,ArrayBuffer,Blob,console,postMessage(data){responses.push(data);waits.get(data.t)?.(data);}};
  vm.createContext(ctx);vm.runInContext(fs.readFileSync(__dirname+'/capture-worker.js','utf8'),ctx);
  const port={start(){},postMessage(p){recycles.push(structuredClone(p));}};
  await ctx.onmessage({data:{t:'open',id:'pipeline',metadata,port}});
  assert.equal(responses[0].t,'opened');
  port.onmessage({data:packet()});
  // Wait for one commit before simulating a storage fault.
  await new Promise(resolve=>setImmediate(resolve));
  fake.crash('pcm.f32:flush');
  port.onmessage({data:packet(4,2)});port.onmessage({data:packet(4,3)});
  const end=new Promise(resolve=>waits.set('finished',resolve));port.onmessage({data:{t:'end',gapFrames:1}});
  const finished=await end;
  assert.equal(recycles.length,3);assert.equal(finished.writtenFrames,4);assert.equal(finished.rawFrames,18);assert.equal(finished.gapFrames,14);assert.equal(finished.incomplete,true);
  assert.equal(responses.find(r=>r.t==='fault').error,'crash at pcm.f32:flush');
  const recovered=await (await OPFSStore.open(fake.storage,'pipeline',null,true)).recover();assert.equal(recovered.blocks.length,1);assert.equal(recovered.rawFrames,18);assert.equal(recovered.incomplete,true);
});
test('count-in click and exact boundary precede rendering without input monitoring',()=>{
  const {adapter}=adapterHarness(),input=[[new Float32Array(128).fill(.7)]],left=new Float32Array(128),right=new Float32Array(128);
  adapter.receive({op:'begin',countInFrames:129});
  assert.equal(adapter.process(input,128,0,120000,true,left,right),128);
  assert.equal(left[0],new Float32Array([.2])[0]);assert.equal(left[31],new Float32Array([-.2])[0]);assert.equal(left[32],0);
  assert.equal(adapter.process(input,128,0,120000,true,left,right),1);assert.equal(adapter.remaining,0);
});
test('workstation worklet fits declared raw and Brotli envelopes; core asset is unchanged',()=>{
  const {brotliCompressSync}=require('node:zlib');
  const assets=['capture.js','processor-capture.min.js'].map(p=>fs.readFileSync(__dirname+'/'+p));
  const raw=assets.reduce((sum,a)=>sum+a.length,0),compressed=assets.reduce((sum,a)=>sum+brotliCompressSync(a).length,0);
  assert.ok(raw<=16384);assert.ok(compressed<=8192);assert.ok(fs.statSync(__dirname+'/processor.min.js').size<=5120);
  console.log(`METRIC capture_worklet_raw_bytes=${raw} brotli_bytes=${compressed} pool_slots=32 max_period_frames=2048`);
});

test('sampler aligns count-in when the first committed block follows an initial gap',()=>{
  const ctx={createBuffer(c,n){const pcm=new Float32Array(n);return {getChannelData:()=>pcm};}};
  const sampler=new TakeSampler(ctx),b=block(4,2);b.EngineFrame=-2;
  sampler.load({metadata,rawFrames:6,incomplete:true,pcm:new Float32Array([1,2,3,4]).buffer,blocks:[{timing:b,placement:placement(b),rawFrame:2,offset:0}]});
  assert.deepEqual(Array.from(sampler.buffer.getChannelData(0)),[3,4]);
});
test('fatal worker error releases microphone and permits recovery/re-arm',async()=>{
  const h=clientHarness();await h.client.arm();await h.client.record();h.client.receive({t:'fault',fatal:true,error:'worker terminated'});
  assert.equal(h.client.status.state,'stopped');assert.equal(h.client.status.incomplete,true);assert.equal(h.track.stopped,true);assert.equal(h.client.worker,null);
});

test('project publication retains worker PCM and exposes a recoverable revision conflict',async()=>{
  const pcm=new Float32Array([.25,-.5]).buffer;
  const take={metadata:{sampleRate:48000,channels:1},pcm,rawFrames:2,incomplete:false,blocks:[{timing:block(2),rawFrame:0,offset:0,length:8,placement:{engineFrame:0},type:'block'}]};
  let sent,status=409;
  const env={btoa,async fetch(url,options){assert.equal(url,'/api/takes');sent=JSON.parse(options.body);return {ok:false,status,async json(){return {error:'publication failed; take retained',take:'native-retained'};}};}};
  await assert.rejects(publishTake(take,{track:'vox',scene:'main',revision:'before-recording'},env),error=>error.takeId==='native-retained');
  assert.deepEqual(Buffer.from(sent.capture.pcm,'base64'),Buffer.from(pcm));
  assert.equal(sent.revision,'before-recording');
  assert.equal(sent.capture.blocks[0].timing.GapFrames,0);
  assert.deepEqual(new Float32Array(take.pcm),new Float32Array([.25,-.5]));
  status=422;
  await assert.rejects(publishTake(take,{track:'vox',scene:'main',revision:'before-recording'},env),error=>error.takeId===undefined);
});

test('published sampler uses shared DSP output once and Stop cancels pending audition',async()=>{
  const voices=[],requests=[];
  let release;
  const gate=new Promise(resolve=>release=resolve);
  const context={async decodeAudioData(bytes){await gate;return {rendered:bytes};},createBufferSource(){const voice={playbackRate:{value:99},connect(){},disconnect(){this.disconnected=true;},start(){this.started=true;},stop(){this.stopped=true;}};voices.push(voice);return voice;},destination:{}};
  const env={async fetch(url,options){requests.push(JSON.parse(options.body));return {ok:true,async arrayBuffer(){return new ArrayBuffer(8);}};}};
  const sampler=new PublishedSampler(context,'project-take','current-revision',env);
  const pending=sampler.play(72,60,true);
  sampler.stop();release();await pending;
  assert.equal(voices.length,0);
  await sampler.play(72,60,true);
  assert.equal(voices.length,1);assert.equal(voices[0].playbackRate.value,1);assert.equal(voices[0].loop,true);
  assert.deepEqual(requests[1].sample,{root:60,note:72,loop:true});
  await sampler.play(60,60,false);
  assert.equal(voices[0].stopped,true);assert.equal(voices[0].disconnected,true);
  assert.equal(voices[1].started,true);
});

for(const event of ['onerror','onmessageerror']) {
 test(`fatal worker ${event} stops capture/accompaniment and permits a new take`,async()=>{
  const h=clientHarness();await h.client.arm();await h.client.record();
  const worker=h.client.worker,oldTake=h.client.take;
  worker[event]({message:'fatal worker fault'});
  assert.equal(h.audio.playing,false,'fatal faults must stop accompaniment');
  assert.deepEqual(h.messages.at(-1),{t:'capture-control',op:'stop'},'fatal faults must stop worklet capture without awaiting the worker');
  assert.equal(h.client.waiters.size,0);assert.equal(worker.terminated,true);assert.equal(h.track.stopped,true);
  assert.equal(h.client.status.state,'stopped');assert.equal(h.client.status.incomplete,true);
  assert.equal(h.client.take,oldTake,'retain the failed take for explicit recovery');
  await h.client.arm();assert.equal(h.client.status.state,'armed');assert.notEqual(h.client.worker,worker);
  await h.client.record();assert.equal(h.audio.playing,true);
  const stopped=h.client.stop();h.client.receive({t:'finished',incomplete:false});await stopped;
 });
}

test('fatal capture fault stops even a play command whose state acknowledgement is pending',async()=>{
 const h=clientHarness(),window={};
 vm.runInNewContext(fs.readFileSync(__dirname+'/client.js','utf8'),{window,Uint8Array,Uint32Array,DataView});
 const audio=window.cicadaBrowserAudio;
 audio.context={...h.audio.context,async resume(){}};audio.node=h.audio.node;audio.readyPromise=Promise.resolve();audio.bpmMilli=120000;audio.stageCurrentScore=async()=>{};
 h.client.audio=audio;
 await h.client.arm();await h.client.record();
 assert.equal(audio.playing,false,'worklet has not acknowledged the queued play command yet');
 h.client.worker.onerror({message:'worker failed before play acknowledgement'});
 const commands=h.messages.filter(message=>message.t==='c');
 assert.equal(new DataView(commands.at(-1).bytes.buffer).getUint8(0),2,'a stop command must follow the queued play command');
 assert.ok(h.messages.some(message=>message.t==='capture-control'&&message.op==='stop'));
 audio.receive({t:'s',p:false});await h.client.arm();assert.equal(h.client.status.state,'armed');
});
