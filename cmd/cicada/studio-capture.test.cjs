'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const {PublishedSampler}=require('../../host/web/capture-client.js');

function deferred() {
  let resolve;
  const promise=new Promise(done=>{resolve=done;});
  return {promise,resolve};
}

for(const phase of ['resume','fetch','decode']) {
  test(`Stop sample cancels the actual UI audition while ${phase} is pending`,async()=>{
    const elements=new Map(),storage=new Map(),voices=[];
    const byId=id=>{
      if(!elements.has(id)) elements.set(id,{value:'',options:[],checked:false,disabled:false,setAttribute(){},addEventListener(_name,fn){this.click=fn;}});
      return elements.get(id);
    };
    byId('audio-mode').value='browser';byId('pcm-track').value='vox';byId('pcm-scene').value='main';
    byId('sampler-note').value='60';byId('sampler-root').value='60';
    let blocked=false,stops=0,requests=0;
    const entered=deferred(),release=deferred();
    async function wait(at) { if(blocked && phase===at) { entered.resolve();await release.promise; } }
    const context={
      destination:{},async resume(){await wait('resume');},
      async decodeAudioData(){await wait('decode');return {};},
      createBufferSource(){const voice={playbackRate:{value:1},connect(){},disconnect(){},start(){this.started=true;},stop(){this.stopped=true;}};voices.push(voice);return voice;}
    };
    const env={async fetch(){requests++;await wait('fetch');return {ok:true,async arrayBuffer(){return new ArrayBuffer(8);}};}};
    const api={
      BrowserCapture:class {
        constructor(){this.status={state:'idle'};this.take=null;}
        onStatus(){}
        async recover(){return {id:'browser-take'};}
      },
      PublishedSampler:class extends PublishedSampler {
        constructor(...args){super(...args,env);}
        stop(){stops++;super.stop();}
      },
      async publishTake(){return {take:'project-take',revision:'revision',source:'source'};}
    };
    const window={addEventListener(){},CicadaBrowserCapture:api,cicadaBrowserAudio:{context,async startAudio(){}},cicadaStudio:{revision(){return 'revision';}}};
    vm.runInNewContext(fs.readFileSync(require.resolve('./studio-capture.js'),'utf8'),{
      window,document:{getElementById:byId,body:{dataset:{revision:'revision'}}},fetch:async()=>({ok:true,json:async()=>({activeCapture:'',takes:[]})}),
      localStorage:{getItem:key=>storage.get(key)||null,setItem(key,value){storage.set(key,value);},removeItem(key){storage.delete(key);}}
    });
    await new Promise(resolve=>setImmediate(resolve));
    await byId('pcm-recover').click();
    blocked=true;
    const playing=byId('sampler-play').click();
    await entered.promise;
    const before=stops;
    await byId('sampler-stop').click();
    const after=stops;
    release.resolve();await playing;
    assert.equal(after,before+1,'Stop must reach the sampler while Play is working');
    assert.equal(voices.length,0,'a canceled audition must never start a voice');
    if(phase==='resume') assert.equal(requests,0,'Stop during resume must prevent the audition request');
    blocked=false;
    await byId('sampler-play').click();
    assert.equal(voices.length,1);assert.equal(voices[0].started,true,'Play must work again after cancellation');
    await byId('sampler-stop').click();assert.equal(voices[0].stopped,true);
  });
}

function captureUI(server={activeCapture:'',takes:[]},tracks=['vox'],scenes=['main']) {
  const elements=new Map(),listeners=new Map(),commands=[],storage=new Map();
  const option=value=>({value,textContent:value,cloneNode(){return option(value);}});
  const byId=id=>{
    if(!elements.has(id)) {
      let selected='';
      elements.set(id,{options:[],checked:false,disabled:['pcm-record','pcm-stop'].includes(id),textContent:'',
        get value(){return selected;},set value(value){selected=value;},
        replaceChildren(...options){this.options=options;selected=options[0]?.value || '';},
        setAttribute(){},addEventListener(name,fn){this[name]=fn;}});
    }
    return elements.get(id);
  };
  byId('pcm-track').replaceChildren(...tracks.map(option));byId('pcm-scene').replaceChildren(...scenes.map(option));
  byId('audio-mode').value='native';byId('pcm-channels').value='1';
  const window={
    addEventListener(name,fn){listeners.set(name,fn);},dispatchEvent(event){listeners.get(event.type)?.(event);},
    CicadaBrowserCapture:{
      BrowserCapture:class {
        constructor(){this.status={state:'idle'};}
        onStatus(){} async arm(){this.status.state='armed';} async record(){this.status.state='recording';}
      },PublishedSampler:class {stop(){}},
    },
    cicadaStudio:{revision(){return 'current';},dirty(){return false;}},
    cicadaBrowserAudio:{async startAudio(){},context:{}}
  };
  const document={getElementById:byId,body:{dataset:{revision:'current'}},querySelector(selector){return byId(selector.slice(1));},querySelectorAll(){return [];}};
  const env={window,document,CustomEvent:class {constructor(type,options){this.type=type;this.detail=options?.detail;}},
    localStorage:{getItem:key=>storage.get(key)||null,setItem(key,value){storage.set(key,value);}},
    async fetch(path,init) {
      if(path==='/api/takes') {
        if(init?.method==='POST') {commands.push(JSON.parse(init.body));return {ok:true,async json(){return {take:'active',revision:'saved',source:'source',activeCapture:''};}};}
        return {ok:true,async json(){return server;}};
      }
      throw new Error(`Unexpected fetch ${path}`);
    }
  };
  vm.runInNewContext(fs.readFileSync(require.resolve('./studio-capture.js'),'utf8'),env);
  // Run Studio's real source-save projection refresh with the next validated page.
  async function refresh(nextTracks,nextScenes) {
    const next={body:{dataset:{revision:'current'}},querySelector(selector){return {childNodes:(selector==='#pcm-track'?nextTracks:nextScenes).map(option)};}};
    const html=fs.readFileSync(require.resolve('./view.html'),'utf8');
    const script=html.slice(html.indexOf('  const morphSelectors ='),html.indexOf('  async function commitMixer('));
    vm.runInNewContext(script,{...env,
      fetch:async path=>path==='/'?{ok:true,async text(){return '';}}:{ok:true,async json(){return {revision:'current',source:'source'};}},
      DOMParser:class {parseFromString(){return next;}},
      replaceProjection:require('./studio-workspace.js').replaceProjection,
      bindSong(){},initGrids(){},updateBar(){},dirty(){return false;},editor:{},original:'',
    });
    await window.cicadaRefreshProjection();
  }
  return {byId,window,commands,refresh,ready:()=>new Promise(resolve=>setImmediate(resolve))};
}

for(const recording of [false,true]) {
  test(`reload restores an active native ${recording?'recording':'armed'} take and Stop/save`,async()=>{
    const ui=captureUI({activeCapture:'active',takes:[{id:'active',track:'voice',scene:'verse',expected:'original',channels:2}],capture:{recording,writtenFrames:96,countInRemainingFrames:0}},['vox','voice'],['main','verse']);
    await ui.ready();
    assert.equal(ui.byId('pcm-stop').disabled,false,'an existing native take must be stoppable after reload');
    assert.equal(ui.byId('pcm-arm').disabled,true);assert.equal(ui.byId('pcm-recover').disabled,true);
    assert.equal(ui.byId('pcm-record').disabled,recording);
    assert.equal(ui.byId('pcm-track').value,'voice');assert.equal(ui.byId('pcm-scene').value,'verse');
    ui.window.dispatchEvent({type:'cicada:audiostate',detail:{runtime:{capture:{recording:true,writtenFrames:192,countInRemainingFrames:0}}}});
    assert.match(ui.byId('pcm-status').textContent,/192/,'native progress must update after reload');
    await ui.byId('pcm-stop').click();
    assert.equal(ui.commands.at(-1).action,'stop');assert.equal(ui.commands.at(-1).track,'voice');assert.equal(ui.commands.at(-1).scene,'verse');
    assert.equal(ui.byId('pcm-arm').disabled,false);
  });
}

test('source saves refresh added, renamed and deleted capture targets without reload',async()=>{
  const ui=captureUI(undefined,[],[]);await ui.ready();
  await ui.refresh(['vox','guitar'],['main','verse']);
  assert.equal(ui.byId('pcm-track').value,'vox','new audio tracks must be available immediately');
  ui.byId('pcm-track').value='guitar';ui.byId('pcm-scene').value='verse';
  await ui.refresh(['vox','guitar','drums'],['main','verse']);
  assert.equal(ui.byId('pcm-track').value,'guitar','keep a selected target that still exists');
  assert.equal(ui.byId('pcm-scene').value,'verse');
  await ui.refresh(['vox','lead'],['main','chorus']);
  assert.deepEqual(ui.byId('pcm-track').options.map(o=>o.value),['vox','lead']);
  assert.equal(ui.byId('pcm-track').value,'vox','renamed target must not stay selected');
  assert.equal(ui.byId('pcm-scene').value,'main');
  await ui.refresh([],[]);
  assert.equal(ui.byId('pcm-track').value,'');assert.equal(ui.byId('pcm-scene').value,'');
  assert.equal(ui.byId('pcm-arm').disabled,true,'no valid target means recording cannot be armed');
  await ui.refresh(['lead'],['chorus']);await ui.byId('pcm-arm').click();
  assert.equal(ui.commands.at(-1).track,'lead');assert.equal(ui.commands.at(-1).scene,'chorus');
});

test('source target changes preserve an active take and keep Stop available',async()=>{
  const ui=captureUI();await ui.ready();await ui.byId('pcm-arm').click();
  await ui.refresh(['renamed'],['new-scene']);
  assert.equal(ui.byId('pcm-stop').disabled,false);
  assert.equal(ui.byId('pcm-record').disabled,true,'do not start recording to a removed target');
  assert.match(ui.byId('pcm-status').textContent,/target.*changed/i);
  await ui.byId('pcm-stop').click();
  assert.equal(ui.commands.at(-1).track,'vox','never silently retarget an active take');
  assert.equal(ui.commands.at(-1).scene,'main');
  assert.equal(ui.byId('pcm-track').value,'renamed');assert.equal(ui.byId('pcm-scene').value,'new-scene');
});
