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
  assert.equal(frames.length, 1);
  frames.shift()();
  assert.deepEqual(socket.sent, [
    {type: 'param', address: 'bass.level', value: -3.5},
    {type: 'param', address: 'bass.pan', value: .2}
  ]);
  audio.close();
});

test('meter parser keeps finite contract fields and rejects invalid payloads', () => {
  const parsed = decodeMeters({
    type: 'meters', tick: 1234,
    tracks: {bass: {peak: -8.2, rms: -14.9}, invalid: {peak: Infinity, rms: -20}},
    returns: {a: {peak: -30.1, rms: -38}}, buses: {music: {peak: -6.1, rms: -12}},
    master: {pre_peak: -1.2, peak: -1.5, rms: -9.8, comp_gr: 2.4, limiter_gr: .3, over: false}
  });
  assert.deepEqual(parsed.tracks, {bass: {peak: -8.2, rms: -14.9}});
  assert.equal(parsed.master.limiter_gr, .3);
  assert.equal(decodeMeters({type: 'meters', tick: 1, master: {peak: NaN}}), null);
});
