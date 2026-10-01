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
      if(!elements.has(id)) elements.set(id,{value:'',checked:false,disabled:false,setAttribute(){},addEventListener(_name,fn){this.click=fn;}});
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
    const window={CicadaBrowserCapture:api,cicadaBrowserAudio:{context,async startAudio(){}},cicadaStudio:{revision(){return 'revision';}}};
    vm.runInNewContext(fs.readFileSync(require.resolve('./studio-capture.js'),'utf8'),{
      window,document:{getElementById:byId,body:{dataset:{revision:'revision'}}},
      localStorage:{getItem:key=>storage.get(key)||null,setItem(key,value){storage.set(key,value);},removeItem(key){storage.delete(key);}}
    });
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
