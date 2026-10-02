'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {createInputRouter, createMIDIPerformance, createMappingStore, createParameterDispatcher, createGamepadPerformance, createBrowserPerformanceSink} = require('./studio-midi.js');
function harness() {
  const events = [], released = [];
  const router = createInputRouter({noteOn:(...args)=>events.push(['on',...args]), noteOff:(...args)=>events.push(['off',...args]), onRelease:n=>released.push(n)});
  const input = (note, device='a', track='bass', extra={}) => ({source:'midi',device,channel:0,track,note,velocity:100,...extra});
  return {router,events,released,input};
}
test('note owners retain their original track; duplicate and unmatched releases are harmless',()=>{
  const h=harness(),n=h.input(60);
  const id=h.router.press(n);
  assert.equal(h.router.press({...n,track:'lead'}),null);
  h.router.release({...n,track:'lead'}); h.router.release(n); h.router.release(h.input(72));
  assert.deepEqual(h.events.map(e=>e.slice(0,3)),[['on','bass',60],['off','bass',60]]);
  assert.equal(h.released[0].id,id); assert.equal(h.router.size,0);
});
test('overlapping device ownership uses last-held priority and restores the preceding note',()=>{
  const h=harness(),a=h.input(60),b=h.input(64,'b'),c=h.input(67,'c');
  h.router.press(a);h.router.press(b);h.router.press(c);
  const count=h.events.length;h.router.release(b);assert.equal(h.events.length,count);
  h.router.release(c);assert.deepEqual(h.events.at(-1).slice(0,3),['on','bass',60]);
  h.router.release(a);assert.equal(h.events.at(-1)[0],'off');assert.equal(h.router.size,0);
});
test('two owners of one pitch share a gate until the final release',()=>{
  const h=harness(),a=h.input(60),b=h.input(60,'b');
  h.router.press(a);h.router.press(b);h.router.release(a);assert.equal(h.events.length,1);
  h.router.release(b);assert.equal(h.events.length,2);assert.equal(h.events[1][3],h.events[0][4]);
});
test('sustain is device/channel scoped; panic clears it and requires a fresh press after release',()=>{
  const h=harness(),a=h.input(60),b=h.input(64,'b');
  h.router.sustain(a,true);h.router.press(a);h.router.release(a);assert.equal(h.router.size,1);
  h.router.press(b);h.router.panic({source:'midi',device:'a'});assert.equal(h.router.size,1);
  h.router.panic();assert.equal(h.router.size,0);assert.equal(h.router.press(b),null);
  h.router.release(b);assert.ok(h.router.press(b));h.router.panic();
});
test('drum ownership is independent per lane, including GM aliases',()=>{
  const h=harness(),kick=h.input(36,'a','kit',{drum:true}),hat=h.input(42,'a','kit',{drum:true});
  h.router.press(kick);h.router.press(hat);assert.deepEqual(h.events.map(e=>e[0]),['on','on']);
  h.router.release(kick);assert.deepEqual(h.events.at(-1).slice(0,3),['off','kit',36]);
  h.router.release(hat);
});
test('failed audio admission does not retain a note for later reconnect playback',()=>{
  let plays=0;
  const router=createInputRouter({noteOn(){plays++;return false;},noteOff(){throw Error('No note admitted');}});
  assert.equal(router.press(harness().input(60)),null);assert.equal(router.size,0);router.panic();assert.equal(plays,1);
});
test('learned CC dispatch validates current live addresses and bounded registry values',()=>{
  const values=[],errors=[];
  const catalog={addresses:[{address:'bass.cutoff',param:'acid.cutoff'},{address:'bass.level',param:'mix.gain'},{address:'bass.wave',param:'acid.wave'}],registry:[{id:'acid.cutoff',live:true,min:100,max:10000,curve:'log'},{id:'mix.gain',live:true,min:-60,max:6,curve:'fader',off:true},{id:'acid.wave',live:false,min:0,max:1}]};
  const dispatch=createParameterDispatcher({getAudio:()=>({setParam:(...x)=>values.push(x)}),getCatalog:()=>catalog,onError:e=>errors.push(e)});
  const h=harness(),store=createMappingStore();
  // Use an actual persistent mapping, as a reloaded page would.
  const data=new Map();const mappings=createMappingStore({getItem:k=>data.get(k),setItem:(k,v)=>data.set(k,v)});
  mappings.bind({device:'Keys',channel:0,cc:74,address:'bass.cutoff'});
  const midi=createMIDIPerformance({router:h.router,mappings,getTrack:()=>({id:'bass'}),dispatchCC:dispatch});
  for(const value of [0,64,127]) midi.receive({id:'port',name:'Keys'},{data:[0xb0,74,value]});
  assert.equal(values.length,3);assert.equal(values[0][1],100);assert.ok(values[1][1]>100 && values[1][1]<10000);assert.equal(values[2][1],10000);
  assert.equal(dispatch('bass.level',0),true);assert.equal(values.at(-1)[1],null);
  for(const [address,value] of [['missing',64],['bass.wave',64],['bass.cutoff',128]]) assert.equal(dispatch(address,value),false);
  assert.equal(errors.length,3);assert.equal(values.length,4);
});
test('MIDI releases use port IDs and the bound track across selection, CC sustain/panic and hotplug',()=>{
  const h=harness();let track='bass';const midi=createMIDIPerformance({router:h.router,mappings:createMappingStore(),getTrack:()=>({id:track}),dispatchCC(){}});
  const a={id:'port-a',name:'Same name'},b={id:'port-b',name:'Same name'};
  const send=(port,data)=>midi.receive(port,{data});
  send(a,[0x90,60,100]);track='lead';send(b,[0x90,60,80]);send(a,[0x80,60,0]);
  assert.deepEqual(h.events.at(-1).slice(0,3),['off','bass',60]);
  send(b,[0xb0,64,127]);send(b,[0x90,60,0]);assert.equal(h.router.size,1);
  send(b,[0xb0,64,0]);assert.equal(h.router.size,0);
  send(a,[0x90,64,100]);midi.disconnect(a);assert.equal(h.router.size,0);
  send(a,[0x90,64,100]);assert.equal(h.router.size,0);send(a,[0x80,64,0]);send(a,[0x90,64,100]);assert.equal(h.router.size,1);
  send(a,[0xb0,123,0]);assert.equal(h.router.size,0);
});
test('MIDI parser rejects malformed packets and action learns fire once per press',()=>{
  const h=harness(),data=new Map(),mappings=createMappingStore({getItem:k=>data.get(k),setItem:(k,v)=>data.set(k,v)});
  let launches=0,unsupported=0;mappings.bind({device:'Keys',channel:0,note:60,action:'scene:main'});
  const midi=createMIDIPerformance({router:h.router,mappings,getTrack:()=>({id:'bass'}),dispatchCC(){},action(){launches++;},unsupported(){unsupported++;}});
  const port={id:'a',name:'Keys'};
  for(const packet of [[0x90,60],[0x90,200,100],[0x90,61,128],[0,60,100]]) midi.receive(port,{data:packet});
  assert.equal(h.router.size,0);midi.receive(port,{data:[0x90,60,100]});midi.receive(port,{data:[0x90,60,100]});assert.equal(launches,1);
  midi.receive(port,{data:[0x80,60,0]});midi.receive(port,{data:[0x90,60,100]});assert.equal(launches,2);
  midi.receive(port,{data:[0xe0,0,64]});midi.receive(port,{data:[0xd0,64]});assert.equal(unsupported,2);
});
test('standard gamepad hysteresis, initial-held suppression, disconnect and reconnect neutral',()=>{
  const h=harness(),gamepad=createGamepadPerformance({router:h.router,getTrack:()=> 'bass',dispatchCC(){}});
  const pad={id:'synthetic',index:0,connected:true,mapping:'standard',buttons:Array.from({length:8},()=>({value:0})),axes:[0,0,0,0]};
  const poll=value=>{pad.buttons[0].value=value;gamepad.poll([pad]);};
  poll(1);assert.equal(h.router.size,0);poll(0);poll(.64);assert.equal(h.router.size,0);poll(.66);assert.equal(h.router.size,1);
  poll(.5);assert.equal(h.router.size,1);poll(.34);assert.equal(h.router.size,0);
  poll(1);gamepad.poll([]);assert.equal(h.router.size,0);poll(1);assert.equal(h.router.size,0);poll(0);poll(1);assert.equal(h.router.size,1);
  gamepad.panic();poll(1);assert.equal(h.router.size,0);poll(0);poll(1);assert.equal(h.router.size,1);
  gamepad.poll([]);poll(0);poll(1);assert.equal(h.router.size,1,'neutral first reconnect sample clears old ownership suppression');
  gamepad.poll([{...pad,mapping:''}]);assert.equal(h.router.size,0);
});
test('repressing a sustained pitch on a different selected track releases its former gate',()=>{
 const h=harness(),old=h.input(60);h.router.press(old);h.router.sustain(old,true);h.router.release(old);
 h.router.press({...old,track:'lead'});
 assert.deepEqual(h.events.slice(-2).map(e=>e.slice(0,3)),[['off','bass',60],['on','lead',60]]);
 h.router.release(old);h.router.sustain(old,false);assert.equal(h.router.size,0);
});

test('Studio MIDI wiring dispatches CC and releases the original track on hotplug, focus and backend loss',async()=>{
 const fs=require('node:fs'),vm=require('node:vm'),calls=[],listeners=new Map(),elements=new Map();
 function node(tagName='DIV') {
  const attributes=new Map();return {tagName,dataset:{},style:{getPropertyValue(){return ''; }},childNodes:[],textContent:'',value:'',hidden:false,
   classList:{add(){},remove(){},toggle(){},contains(){return false;}},
   append(...children){this.childNodes.push(...children);},replaceChildren(...children){this.childNodes=children;},
   addEventListener(name,fn){(this.events ||= {})[name]=fn;},setAttribute:(name,value)=>attributes.set(name,value),getAttribute:name=>attributes.get(name)||null,
   querySelector(){return node();},querySelectorAll(){return [];},closest(){return null;},focus(){},contains(){return false;}};
 }
 const bySelector=selector=>{if(!elements.has(selector)) elements.set(selector,node());return elements.get(selector);};
 const strips=['bass','lead'].map(id=>({...node(),dataset:{strip:id,sourceKind:'acid'}}));
 const document={body:{dataset:{revision:'one'},classList:{toggle(){}}},hidden:false,querySelector:bySelector,
  querySelectorAll:selector=>selector==='.mix-strip[data-kind="track"]'?strips:[],createElement:node,
  addEventListener(name,fn){(listeners.get('document:'+name)||listeners.set('document:'+name,[]).get('document:'+name)).push(fn);}};
 const port={id:'p',name:'Keys',state:'connected'},access={inputs:new Map([['p',port]])};
 const storage=new Map();storage.set('cicada.midi.mappings.v1',JSON.stringify([{device:'Keys',channel:0,cc:74,address:'bass.cutoff'}]));
 const window={cicadaMidi:require('./studio-midi.js'),localStorage:{getItem:k=>storage.get(k)||null,setItem:(k,v)=>storage.set(k,v)},
  cicadaAudio:{noteOn:(...args)=>{calls.push(['on',...args]);return true;},noteOff:(...args)=>calls.push(['off',...args]),params:async()=>({revision:'one',registry:[{id:'acid.cutoff',live:true,min:20,max:20000,curve:'log'}],addresses:[{address:'bass.cutoff',param:'acid.cutoff'}]}),setParam:(...args)=>calls.push(['param',...args]),onError(){},panic(){}},
  addEventListener(name,fn){(listeners.get(name)||listeners.set(name,[]).get(name)).push(fn);},
  dispatchEvent(event){for(const fn of listeners.get(event.type)||[])fn(event);}};
 const timers=[];
 vm.runInNewContext(fs.readFileSync(require.resolve('./studio-live.js'),'utf8'),{window,document,navigator:{requestMIDIAccess:async()=>access},
  location:{protocol:'http:',host:'localhost'},CustomEvent:class {constructor(type){this.type=type;}},performance:{now:()=>100},
  requestAnimationFrame:fn=>{fn();return 1;},cancelAnimationFrame(){},setTimeout:fn=>{timers.push(fn);return timers.length;},clearTimeout(){},
  WebSocket:class {},fetch:async()=>({ok:true,json:async()=>({})})});
 await new Promise(resolve=>setImmediate(resolve));
 await bySelector('#midi-enable').events.click();
 port.onmidimessage({data:[0xb0,74,127]});assert.equal(calls.at(-1)[0],'param');assert.equal(calls.at(-1)[2],20000);
 port.onmidimessage({data:[0x90,60,100],timeStamp:100});bySelector('#live-acid-track').value='lead';
 port.onmidimessage({data:[0x80,60,0],timeStamp:150});assert.equal(calls.at(-1)[1],'bass');assert.equal(calls.at(-1)[0],'off');
 port.onmidimessage({data:[0x90,64,100]});window.dispatchEvent({type:'blur'});assert.equal(calls.at(-1)[0],'off');
 port.onmidimessage({data:[0x80,64,0]});port.onmidimessage({data:[0x90,64,100]});window.dispatchEvent({type:'cicada:inputpanic'});assert.equal(window.cicadaPerformance.router.size,0);
 port.onmidimessage({data:[0x80,64,0]});port.onmidimessage({data:[0x90,64,100]});port.state='disconnected';access.onstatechange();
 assert.equal(port.onmidimessage,null);assert.equal(window.cicadaPerformance.router.size,0);
});

test('browser adapter calls PR109 Down/Up/SetParam and preserves its reset barrier without an encoder or network',()=>{
 const calls=[],tokens=new Set();let needsReset=false,disconnected=0;
 const sink=createBrowserPerformanceSink({
  down:(token,track,note,velocity,repeat)=>{calls.push(['Down',token,track,note,velocity,repeat]);if(needsReset)return 'CICADA-AUDIO: wait for a fresh worklet kernel image after Stop';tokens.add(token);return '';},
  up:token=>{calls.push(['Up',token]);tokens.delete(token);return '';},
  setParam:(...args)=>{calls.push(['SetParam',...args]);return '';},panic:()=>{calls.push(['Panic']);needsReset=true;tokens.clear();return '';},disconnect:()=>disconnected++,getCatalog:()=>({registry:[],addresses:[]})
 });
 const id='source-device-channel-track:'+'very-long-device-id'.repeat(20);
 assert.equal(sink.noteOn('bass',60,100,id),true);assert.ok(calls[0][1].length<=128,'Go Controller tokens stay within its bound');
 sink.noteOff('different-track',99,id);assert.equal(tokens.size,0,'Up uses the original token binding');
 sink.setParam('bass.cutoff',1234);sink.setMute('bass',true);assert.deepEqual(calls.at(-1),['SetParam','bass.mute',1]);
 sink.panic();assert.throws(()=>sink.noteOn('bass',64,100,'new'),/fresh worklet kernel image/);assert.equal(disconnected,0);
 // Only the app's matching kernel receipt acknowledges readiness. The adapter
 // has no resetReady method and does not infer one from a Stop acknowledgement.
 assert.equal(sink.resetReady,undefined);needsReset=false;sink.noteOn('bass',64,100,'new');sink.silence();assert.equal(disconnected,1);
});

test('failed browser panic detaches the worklet rather than allowing an audible stuck input',()=>{
 let disconnected=0;
 const sink=createBrowserPerformanceSink({down(){return '';},up(){return '';},setParam(){return '';},panic(){return 'worklet send failed';},disconnect(){disconnected++;},getCatalog(){return {};}});
 sink.noteOn('bass',60,100,'owner');assert.throws(()=>sink.panic(),/send failed/);assert.equal(disconnected,1);
});

test('gamepad shoulder buttons dispatch authored pattern/scene actions and sticks use a deadzone',()=>{
 const h=harness(),actions=[],controls=[];
 const gamepad=createGamepadPerformance({router:h.router,getTrack:()=> 'bass',action:x=>actions.push(x),dispatchCC:(...x)=>controls.push(x),getAddresses:()=>({timbre:'bass.cutoff'})});
 const pad={id:'standard',index:0,mapping:'standard',buttons:Array.from({length:8},()=>({value:0})),axes:[0,0,0,0]};
 gamepad.poll([pad]);pad.axes[2]=.05;gamepad.poll([pad]);assert.equal(controls.length,1,'neutral stick noise produces no new parameter dispatch');
 pad.buttons[4].value=1;pad.buttons[5].value=1;gamepad.poll([pad]);gamepad.poll([pad]);assert.deepEqual(actions,['pattern','scene']);
 assert.equal(h.router.size,0,'pattern launch never spoofs polyphonic voices');
 pad.axes[2]=1;gamepad.poll([pad]);assert.deepEqual(controls.at(-1),['bass.cutoff',127]);
});
