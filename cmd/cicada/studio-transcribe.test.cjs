'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const {recordingWAV,discardTake,mount}=require('./studio-transcribe.js');

test('recorded melody WAV preserves mono samples and rejects incomplete audio',()=>{
  const take={metadata:{sampleRate:16000,channels:2},rawFrames:3,pcm:new Float32Array([.2,.4,.4,.6]).buffer,blocks:[{rawFrame:1,offset:0,timing:{Frames:2}}]};
  const wav=new DataView(recordingWAV(take));
  assert.equal(wav.getUint16(22,true),1);assert.equal(wav.getUint32(24,true),16000);
  assert.equal(wav.getFloat32(44,true),0);assert.ok(Math.abs(wav.getFloat32(48,true)-.3)<1e-6);
  assert.ok(Math.abs(wav.getFloat32(52,true)-.5)<1e-6);
  assert.throws(()=>recordingWAV({...take,incomplete:true}),/missing audio/);
  assert.throws(()=>recordingWAV({...take,rawFrames:16000*61}),/60 seconds/);
});

test('temporary browser recording is deleted without removing another retained take',async()=>{
  const removed=[],storage=new Map([['cicada-last-take',JSON.stringify({id:'other'})]]);let terminated=0;
  const env={navigator:{storage:{async getDirectory(){return {async getDirectoryHandle(name){assert.equal(name,'cicada-takes');return {async removeEntry(id,options){removed.push(id);assert.equal(options.recursive,true);}};}};}}},
    localStorage:{getItem:key=>storage.get(key)||null,removeItem:key=>storage.delete(key)}};
  const capture={take:{id:'temporary',mode:'opfs'},worker:{terminate(){terminated++;}}};
  await discardTake(capture,env);
  assert.deepEqual(removed,['temporary']);assert.equal(terminated,1);assert.equal(capture.take,null);
  assert.equal(JSON.parse(storage.get('cicada-last-take')).id,'other');
});

function fixture() {
  const nodes=new Map(),requests=[],storage=new Map(),revoked=[],events=[];let response;
  const byId=id=>{
    if(!nodes.has(id))nodes.set(id,{value:'',hidden:true,disabled:false,textContent:'',setAttribute(){},removeAttribute(){},pause(){},addEventListener(type,fn){this[type]=fn;}});
    return nodes.get(id);
  };
  byId('melody-grid').value='16';
  const document={getElementById:byId,createElement(){return {click(){}};}};
  const env={CicadaBrowserCapture:{BrowserCapture:class {constructor(){this.status={state:'idle'};}onStatus(){}async stop(){} }},
    cicadaBrowserAudio:{},cicadaStudio:{revision:()=> 'before',dirty:()=>false,busy:()=>false},
    URL:{createObjectURL:()=> 'blob:original',revokeObjectURL:url=>revoked.push(url)},
    localStorage:{getItem:key=>storage.get(key)||null,setItem:(key,value)=>storage.set(key,value),removeItem:key=>storage.delete(key)},
    FormData:class {append(){}},Blob:class {},AbortController,File:class {},
    setTimeout(){},clearTimeout(){},CustomEvent:class{constructor(type,options){this.type=type;this.detail=options.detail;}},
    dispatchEvent:event=>events.push(event),addEventListener(){},
    async fetch(path,options){requests.push({path,options});if(path==='/api/source')return {ok:false,json:async()=>({error:'score changed'})};return response;}
  };
  const app=mount(document,env);
  const result={source:'cicada 2',tempo:120,tempoConfidence:.8,meter:'4/4',meterConfidence:.6,key:'C major',keyConfidence:.7,notes:[{start:.1,end:.5,pitchHz:440,midi:69,cents:0,confidence:.9}],warnings:['Review the key.']};
  response={ok:true,json:async()=>result};
  return {byId,env,app,requests,revoked,events,result,setResponse(value){response=value;}};
}

test('preview leaves source unchanged and Apply uses its original revision',async()=>{
  const ui=fixture();await ui.byId('melody-file').change({target:{files:[{size:20}]}});
  assert.equal(ui.requests.length,1);assert.equal(ui.requests[0].path,'/api/transcribe');
  assert.equal(ui.byId('melody-result').hidden,false);assert.equal(ui.byId('melody-score').textContent,'cicada 2');
  assert.match(ui.byId('melody-summary').textContent,/pitch confidence 90%/);
  ui.env.cicadaStudio.revision=()=> 'changed';await ui.byId('melody-apply').click();
  assert.deepEqual(JSON.parse(ui.requests[1].options.body),{revision:'before',source:'cicada 2'});
  assert.match(ui.byId('melody-status').textContent,/score changed/);assert.equal(ui.events.length,0);
  await ui.byId('melody-cancel').click();assert.equal(ui.byId('melody-result').hidden,true);assert.deepEqual(ui.revoked,['blob:original']);
});

test('canceling a pending upload cannot publish a late preview',async()=>{
  const ui=fixture();let resolve;
  ui.setResponse({ok:true,json:()=>new Promise(done=>{resolve=done;})});
  const upload=ui.byId('melody-file').change({target:{files:[{size:20}]}});
  await new Promise(done=>setImmediate(done));
  const signal=ui.requests[0].options.signal;
  await ui.byId('melody-cancel').click();assert.equal(signal.aborted,true);
  resolve(ui.result);await upload;
  assert.equal(ui.byId('melody-result').hidden,true);assert.equal(ui.byId('melody-apply').disabled,true);
});

test('clearing a preview waits for an authorized score replacement to finish',async()=>{
  const ui=fixture();await ui.byId('melody-file').change({target:{files:[{size:20}]}});
  let resolve;ui.env.fetch=()=>new Promise(done=>{resolve=done;});
  const applying=ui.byId('melody-apply').click();
  assert.equal(ui.byId('melody-cancel').disabled,true);
  await ui.byId('melody-cancel').click();assert.equal(ui.byId('melody-result').hidden,false);
  resolve({ok:true,json:async()=>({revision:'saved',source:ui.result.source})});await applying;
  assert.match(ui.byId('melody-status').textContent,/Score replaced/);assert.equal(ui.events.length,1);
  await ui.byId('melody-cancel').click();assert.equal(ui.byId('melody-result').hidden,true);
});
