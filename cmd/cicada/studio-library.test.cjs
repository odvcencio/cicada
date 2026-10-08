const test = require('node:test');
const assert = require('node:assert/strict');
const {filterItems, createPreview} = require('./studio-library.js');
const items = [{path:'std/synth',name:'Glass',kind:'instrument'}, {path:'personal/kit',name:'Steel',kind:'kit'}, {path:'std/presets',name:'Bright',kind:'preset'}];
test('library filters combine kind and case-insensitive name or path text', () => {
 assert.deepEqual(filterItems(items,'instrument',' GLASS '),[items[0]]);
 assert.deepEqual(filterItems(items,'','personal'),[items[1]]);
 assert.deepEqual(filterItems(items,'kit','bright'),[]);
});
function harness() {
 const nodes=[], timers=new Map(); let id=0;
 class Node {
  constructor(context,name,options) { this.context=context;this.name=name;this.options=options;this.port={postMessage:data=>this.sent=data,close:()=>this.closed=true};nodes.push(this);queueMicrotask(()=>this.port.onmessage({data:{t:'r'}})); }
  connect(destination) { this.destination=destination; }
  disconnect() { this.disconnected=true; }
 }
 const module={kernel:true},kinds=[];
 const audio={context:{destination:{}},playing:false,async startAudio(){},imageModuleKind(){return 'keys';},async loadModule(kind){kinds.push(kind);return module;}};
 const preview=createPreview({Node,audio,setTimeout:fn=>{timers.set(++id,fn);return id;},clearTimeout:id=>timers.delete(id)});
 return {nodes,timers,preview,audio,module,kinds};
}
test('preview uses the score kernel and processor, replaces prior node and stops after one bar',async () => {
 const {nodes,timers,preview,audio,module,kinds}=harness();
 const image=new ArrayBuffer(32);
 assert.equal(await preview.play(image),true);
 assert.equal(nodes[0].name,'cicada');
 assert.equal(nodes[0].options.processorOptions.i,image);
 assert.equal(nodes[0].options.processorOptions.m,module);
 assert.deepEqual(kinds,['keys']);
 assert.equal(nodes[0].sent.bytes[0],1);
 await preview.play(image);
 assert.equal(nodes[0].disconnected,true); assert.equal(nodes[0].closed,true);
 assert.equal(timers.size,1);
 [...timers.values()][0](); assert.equal(nodes[1].disconnected,true); assert.equal(timers.size,0);
});
test('stop during preparation cancels the preview; playback refuses preview',async () => {
 const {preview,audio,nodes}=harness();
 let resume;audio.startAudio=()=>new Promise(resolve=>{resume=resolve;});
 const pending=preview.play(new ArrayBuffer(32));preview.stop();resume();
 assert.equal(await pending,false); assert.equal(nodes.length,0);
 audio.playing=true;await assert.rejects(preview.play(new ArrayBuffer(32)),/Stop playback/);
});

test('browser transport stops preview before play and playFrom enqueue commands', () => {
 const fs=require('node:fs'), vm=require('node:vm');
 const window={};
 vm.runInNewContext(fs.readFileSync(require('node:path').join(__dirname,'../../host/web/client.js'),'utf8'),{window});
 const calls=[],audio=window.cicadaBrowserAudio;
 audio.onBeforePlay(()=>calls.push('stop-preview'));
 audio.sendCommands=()=>calls.push('play');
 audio.play();audio.playFrom(2);
 assert.deepEqual(calls,['stop-preview','play','stop-preview','play']);
});

test('Stop and audio-mode changes cancel previews while the response body loads', async () => {
 const {mount}=require('./studio-library.js');
 class Element {
  constructor(){this.value='';this.listeners={};this.children=[];}
  addEventListener(type,fn){this.listeners[type]=fn;}
  setAttribute(){}
  append(...children){this.children.push(...children);}
  replaceChildren(...children){this.children=children;}
 }
 const priorListener=globalThis.addEventListener;
 globalThis.addEventListener=()=>{};
 try {
  for(const action of ['stop','mode']) {
   const controls=Object.fromEntries(['kind','query','list','track','new-track','preset-name','save','stop','status'].map(name=>[name,new Element()]));
   const mode=new Element();mode.value='browser';
   const section={querySelector:selector=>controls[selector.slice('#library-'.length)]};
   const document={querySelector:()=>mode,createElement:()=>new Element(),addEventListener(){}};
   let releaseBody,enteredBody;
   const loading=new Promise(resolve=>{enteredBody=resolve;});
   const calls=[];
   const ui=mount({document,section,studio:{revision:()=>'',playing:()=>false},browser:{playing:false,onState(){},onBeforePlay(){},startAudio:async()=>{},context:{sampleRate:48000}},preview:{stop:()=>calls.push('stop'),play:async()=>calls.push('play')},fetcher:async(url,options)=>{
    if(url==='/api/library')return {ok:true,json:async()=>({items:[items[0]],tracks:['lead']})};
    if(JSON.parse(options.body).action==='stop')return {ok:true};
    return {ok:true,arrayBuffer:()=>{enteredBody();return new Promise(resolve=>{releaseBody=resolve;});}};
   }});
   await ui.refresh();
   const pending=controls.list.children[0].children[1].listeners.click();
   await loading;
   if(action==='stop')await ui.stop();else {mode.value='native';await mode.listeners.change();}
   releaseBody(new ArrayBuffer(32));await pending;
   assert.equal(calls.includes('play'),false,`${action} restarted a canceled preview`);
  }
 } finally {
  if(priorListener===undefined)delete globalThis.addEventListener;else globalThis.addEventListener=priorListener;
 }
});
