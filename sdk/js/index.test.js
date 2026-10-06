import { test } from 'node:test';
import assert from 'node:assert/strict';
import { GameDirector, encodeCommand, decodeMessages, workletSender } from './index.js';
const surface = {
 version: 1, sample_rate: 48000, tracks: 3, land: 'bar', phrase_bars: 8,
 macros: [{ name: 'intensity', id: 0, smooth_frames: 19200 }],
 states: [{ name: 'explore', id: 0, scene: 0 }, { name: 'combat', id: 1, scene: 1 }],
 stingers: [{ name: 'pickup', track: 2, slot: 0, quantize: 'beat', crossfade_frames: 480 }],
 transitions: [{ from: 'explore', to: 'combat', quantize: 'phrase', crossfade_frames: 9600 }], setup: []
};
test('director uses landed states and preserves large absolute ticks', () => {
 const records = [], client = new GameDirector(surface, c => records.push(c));
 client.setState('explore'); assert.equal(client.state, '');
 client.handle({ Kind: 13, A: 0, B: 0 });
 const tick = (1n << 54n) + 3n; client.setState('combat', tick);
 const view = new DataView(records.at(-1).buffer);
 assert.equal(view.getUint32(4, true), 1 | 21 << 16);
 assert.equal(view.getUint32(8, true), 9600); assert.equal(view.getBigInt64(16, true), tick);
 client.setMacro('intensity', 0.75); client.triggerStinger('pickup');
 assert.equal(records.at(-1)[0], 23);
 client.handle({ Kind: 11, B: 5 }); assert.equal(client.layerMask, 5);
 for (const call of [() => client.setState('missing'), () => client.setMacro('intensity', NaN), () => client.setMacro('intensity', 1.1), () => client.triggerStinger('missing'), () => client.setState('combat', -1n)]) assert.throws(call);
});
test('wire codec and worklet transfer', () => {
 const tick = (1n << 55n) + 1n, data = new Uint8Array(16), view = new DataView(data.buffer);
 view.setUint8(0, 14); view.setUint8(1, 2); view.setUint16(2, 3, true); view.setBigInt64(8, tick, true);
 assert.deepEqual(decodeMessages(data), [{ Kind: 14, Track: 2, A: 3, B: 0, Tick: tick }]);
 assert.throws(() => decodeMessages(data.subarray(1)));
 assert.throws(() => encodeCommand({ Op: 22, Track: 255, Tick: Number.MAX_SAFE_INTEGER + 2 }));
 const sent = []; workletSender({ postMessage: (...args) => sent.push(args) })(data);
 assert.deepEqual(sent[0], [{ t: 'c', bytes: data }, [data.buffer]]);
});
test('manifest ownership and invalid mappings', () => {
 const copy = structuredClone(surface), records = [], client = new GameDirector(copy, c => records.push(c));
 copy.states[0].scene = 999; client.setState('explore'); assert.equal(new DataView(records[0].buffer).getUint32(4, true), 2 << 16);
 for (const change of [s => s.states.push(s.states[0]), s => s.stingers[0].track = 4, s => s.transitions[0].from = 'missing', s => s.phrase_bars = 0, s => s.sample_rate = 123]) {
  const bad = structuredClone(surface); change(bad); assert.throws(() => new GameDirector(bad, () => {}));
 }
});
