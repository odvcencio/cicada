const test = require('node:test');
const assert = require('node:assert/strict');
const {createCicadaAudio, decodeMeters} = require('./studio-audio.js');

class FakeSocket {
  static instances = [];
  constructor(url) { this.url = url; this.readyState = 1; this.sent = []; this.listeners = {}; FakeSocket.instances.push(this); }
  addEventListener(name, callback) { this.listeners[name] = callback; }
  send(value) { this.sent.push(JSON.parse(value)); }
  close() { this.readyState = 3; }
}

test('setParam coalesces each address to its last value per animation frame', () => {
  const frames = [];
  const root = {WebSocket: FakeSocket, location: {protocol: 'http:', host: '127.0.0.1:8161'}};
  FakeSocket.instances.length = 0;
  const audio = createCicadaAudio({window: root, requestAnimationFrame: callback => frames.push(callback)});
  const socket = FakeSocket.instances[0];
  audio.setParam('bass.level', -9);
  audio.setParam('bass.pan', .2);
  audio.setParam('bass.level', -3.5);
  audio.setParam('bass.send.delay', .3);
  audio.setParam('delay.feedback', .4);
  assert.equal(frames.length, 1);
  frames.shift()();
  assert.deepEqual(socket.sent, [
    {type: 'param', address: 'bass.level', value: -3.5},
    {type: 'param', address: 'bass.pan', value: .2},
    {type: 'param', address: 'bass.send.delay', value: .3},
    {type: 'param', address: 'delay.feedback', value: .4}
  ]);
  audio.close();
});

test('meter parser keeps finite contract fields and rejects invalid payloads', () => {
  const parsed = decodeMeters({
    type: 'meters', tick: 1234,
    tracks: {bass: {peak: -8.2, rms: -14.9}, invalid: {peak: Infinity, rms: -20}},
    returns: {a: {peak: -30.1, rms: -38}}, buses: {music: {peak: -6.1, rms: -12}},
    master: {pre_peak: -1.2, peak: -1.5, rms: -9.8, comp_gr: 2.4, limiter_gr: .3, over: false},
    loudness: {momentary: -18.2, short_term: -17.9, integrated: -18.4, range: 4.1, true_peak: -1.2, sample_peak: -1.5, dropped_blocks: 0}
  });
  assert.deepEqual(parsed.tracks, {bass: {peak: -8.2, rms: -14.9}});
  assert.equal(parsed.master.limiter_gr, .3);
  assert.equal(parsed.loudness.integrated, -18.4);
  assert.equal(decodeMeters({type: 'meters', tick: 1, master: {peak: NaN}}), null);
});

test('resetLoudness sends the native loudness reset message', () => {
  FakeSocket.instances.length = 0;
  const root = {WebSocket: FakeSocket, location: {protocol: 'http:', host: '127.0.0.1:8161'}};
  const audio = createCicadaAudio({window: root});
  audio.resetLoudness();
  assert.deepEqual(FakeSocket.instances[0].sent, [{type: 'loudness-reset'}]);
  audio.close();
});

test('note facade sends note on and note off messages and validates MIDI ranges', () => {
  FakeSocket.instances.length = 0;
  const root = {WebSocket: FakeSocket, location: {protocol: 'http:', host: '127.0.0.1:8161'}};
  const audio = createCicadaAudio({window: root});
  audio.noteOn('bass', 60, 100);
  audio.noteOff('bass', 60);
  assert.deepEqual(FakeSocket.instances[0].sent, [
    {type: 'note', track: 'bass', note: 60, velocity: 100, on: true},
    {type: 'note', track: 'bass', note: 60, velocity: 0, on: false}
  ]);
  assert.throws(() => audio.noteOn('bass', 128, 100), RangeError);
  assert.throws(() => audio.noteOn('bass', 60, 128), RangeError);
  assert.throws(() => audio.noteOff('bass', -1), RangeError);
  audio.close();
});

test('native notes never queue while disconnected or replay on reconnect; close sends panic', () => {
  FakeSocket.instances.length = 0;
  const timers = [];
  const root = {WebSocket:FakeSocket,location:{protocol:'http:',host:'127.0.0.1:8161'}};
  const audio = createCicadaAudio({window:root,setTimeout:fn=>{timers.push(fn);return 1;},clearTimeout(){}});
  const first = FakeSocket.instances[0];
  first.readyState=0;assert.equal(audio.noteOn('bass',60,100,'held-a'),false);
  first.readyState=1;first.listeners.open();assert.deepEqual(first.sent,[]);
  audio.noteOn('bass',64,100,'held-b');first.readyState=3;first.listeners.close();
  assert.equal(timers.length,1);timers.shift()();
  const second=FakeSocket.instances[1];second.listeners.open();assert.deepEqual(second.sent,[]);
  audio.noteOff('bass',64,'held-b');assert.deepEqual(second.sent,[]);
  audio.noteOn('bass',67,100,'held-c');audio.close();
  assert.equal(second.sent.at(-1).on,false);assert.equal(second.sent.at(-1).noteId,'held-c');
});

test('browser mode routes live notes and controls locally through the audio facade', () => {
  FakeSocket.instances.length=0;const calls=[];
  const browser={noteOn:(...x)=>calls.push(['on',...x]),noteOff:(...x)=>calls.push(['off',...x]),setParam:(...x)=>calls.push(['param',...x]),setMute:(...x)=>calls.push(['mute',...x]),setSolo:(...x)=>calls.push(['solo',...x]),panic(){}};
  const root={WebSocket:FakeSocket,location:{protocol:'http:',host:'localhost'},document:{getElementById:()=>({value:'browser',addEventListener(){}})},cicadaBrowserPerformance:browser};
  const audio=createCicadaAudio({window:root});
  audio.noteOn('bass',60,100,'owner');audio.noteOff('bass',60,'owner');audio.setParam('bass.cutoff',1000);audio.setMute('bass',true);audio.setSolo('bass',true);
  assert.equal(calls.length,5);assert.deepEqual(FakeSocket.instances[0].sent,[]);audio.close();
});

test('normal panic releases native owners; explicit silence discards native playback separately',async()=>{
 FakeSocket.instances.length=0;const requests=[];
 const root={WebSocket:FakeSocket,location:{protocol:'http:',host:'localhost'},document:{body:{dataset:{revision:'current'}},getElementById(){return null;}}};
 const audio=createCicadaAudio({window:root,fetch:async(url,options)=>{requests.push([url,JSON.parse(options.body)]);return {ok:true,json:async()=>({playing:false})};}});
 const socket=FakeSocket.instances[0];
 audio.noteOn('bass',60,100,'acid-owner');audio.noteOn('drums',36,100,'drum-owner');
 audio.panic();assert.equal(requests.length,0,'gate release permits voice/effect tails');
 assert.equal(socket.sent.filter(message=>message.on===false).length,2);
 await audio.silence();assert.deepEqual(requests,[['/api/transport',{action:'stop',revision:'current'}]]);
 audio.close();
});

test('note expression facade preserves identities and high resolution snapshots', () => {
  FakeSocket.instances.length = 0;
  const root = {WebSocket: FakeSocket, location: {protocol: 'http:', host: '127.0.0.1:8161'}};
  const audio = createCicadaAudio({window: root});
  const identity = {noteId: 1234, channel: 14};
  const expression = {...identity, pitchCents: 12.345, pressure: .123456, timbre: .765432};
  audio.noteOn('voice', 60, 100, identity);
  audio.noteExpression('voice', expression);
  audio.noteOff('voice', 60, identity);
  assert.deepEqual(FakeSocket.instances[0].sent, [
    {type: 'note', track: 'voice', note: 60, velocity: 100, on: true, ...identity},
    {type: 'note-expression', track: 'voice', ...expression},
    {type: 'note', track: 'voice', note: 60, velocity: 0, on: false, ...identity}
  ]);
  assert.throws(() => audio.noteExpression('voice', {...expression, pitchCents: NaN}), RangeError);
  assert.throws(() => audio.noteExpression('voice', {...expression, pressure: 1.01}), RangeError);
  assert.throws(() => audio.noteExpression('voice', {...expression, noteId: 65535}), RangeError);
  assert.throws(() => audio.noteOff('voice', 60, {...identity, channel: 16}), RangeError);
  audio.close();
});
