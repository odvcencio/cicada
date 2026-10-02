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
 const audio={context:{destination:{}},modulePromise:Promise.resolve({kernel:true}),playing:false,async startAudio(){}};
 const preview=createPreview({Node,audio,setTimeout:fn=>{timers.set(++id,fn);return id;},clearTimeout:id=>timers.delete(id)});
 return {nodes,timers,preview,audio};
}
test('preview uses the score kernel and processor, replaces prior node and stops after one bar',async () => {
 const {nodes,timers,preview,audio}=harness();
 const image=new ArrayBuffer(32);
 assert.equal(await preview.play(image),true);
 assert.equal(nodes[0].name,'cicada');
 assert.equal(nodes[0].options.processorOptions.i,image);
 assert.equal(nodes[0].options.processorOptions.m,await audio.modulePromise);
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
