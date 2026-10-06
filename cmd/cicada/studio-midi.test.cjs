const test = require('node:test');
const assert = require('node:assert/strict');
const {GM_DRUM_LANES, mapGeneralMIDIDrum, mapCCValue, quantizeStep, createMappingStore, createMPEInput} = require('./studio-midi.js');

test('General MIDI percussion notes map to Cicada drum rows', () => {
  assert.deepEqual(GM_DRUM_LANES, {
    36: 'bd', 37: 'rs', 38: 'sd', 39: 'cp', 41: 'lt', 42: 'ch', 43: 'lt',
    45: 'mt', 46: 'oh', 47: 'mt', 48: 'ht', 49: 'cy', 50: 'ht', 56: 'cb', 57: 'cy'
  });
  for (const [note, lane] of Object.entries(GM_DRUM_LANES)) assert.equal(mapGeneralMIDIDrum(Number(note)), lane);
  assert.equal(mapGeneralMIDIDrum(60), null);
});

test('CC values follow linear, logarithmic, fader, toggle, and enum curves', () => {
  assert.equal(mapCCValue({min: -1, max: 1, curve: 'linear'}, 0), -1);
  assert.equal(mapCCValue({min: -1, max: 1, curve: 'linear'}, 127), 1);
  const logarithmic = mapCCValue({min: 100, max: 10000, curve: 'log'}, 64);
  assert.ok(Math.abs(logarithmic - 1000) < 20);
  assert.equal(mapCCValue({min: -60, max: 6, curve: 'fader', off: true}, 0), null);
  assert.equal(mapCCValue({min: -60, max: 6, curve: 'fader'}, 127), 6);
  assert.ok(Math.abs(mapCCValue({min: -60, max: 6, curve: 'fader'}, 64)) < 1);
  assert.equal(mapCCValue({min: 0, max: 1, curve: 'toggle'}, 63), 0);
  assert.equal(mapCCValue({min: 0, max: 1, curve: 'toggle'}, 64), 1);
  assert.equal(mapCCValue({min: 0, max: 3, curve: 'enum', values: ['a', 'b', 'c', 'd']}, 64), 2);
  assert.throws(() => mapCCValue({min: 0, max: 1, curve: 'linear'}, 128), RangeError);
});

test('MIDI learn replaces an input mapping, supports clear and remove, and persists in localStorage', () => {
  const values = new Map();
  const storage = {getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value)};
  const firstPage = createMappingStore(storage);
  firstPage.bind({device: 'Keyboard A', channel: 0, cc: 74, address: 'bass.cutoff'});
  assert.equal(firstPage.findInput('Keyboard A', 0, 'cc', 74).address, 'bass.cutoff');
  firstPage.bind({device: 'Keyboard A', channel: 0, cc: 74, address: 'bass.resonance'});
  assert.equal(firstPage.all().length, 1);
  assert.equal(firstPage.findInput('Keyboard A', 0, 'cc', 74).address, 'bass.resonance');

  const afterReload = createMappingStore(storage);
  assert.equal(afterReload.all()[0].address, 'bass.resonance');
  afterReload.bind({device: 'Keyboard A', channel: 9, note: 36, action: 'scene:main'});
  assert.equal(afterReload.findInput('Keyboard A', 9, 'note', 36).action, 'scene:main');
  afterReload.clearTarget({action: 'scene:main'});
  assert.equal(afterReload.findInput('Keyboard A', 9, 'note', 36), null);
  const controller = afterReload.findInput('Keyboard A', 0, 'cc', 74);
  afterReload.remove(controller);
  assert.deepEqual(afterReload.all(), []);
});

test('take notes quantize to the nearest pattern step and wrap at pattern length', () => {
  assert.equal(quantizeStep(0, 16), 0);
  assert.equal(quantizeStep(119, 16), 0);
  assert.equal(quantizeStep(120, 16), 1);
  assert.equal(quantizeStep(240 * 15 + 120, 16), 0);
  assert.throws(() => quantizeStep(-1, 16), RangeError);
});

function configureZone(input, device, master, members) {
  for (const [cc, value] of [[101, 0], [100, 6], [6, members]]) input.process(device, [0xb0 | master, cc, value]);
}

test('MPE lower and upper zones normalize member bend, pressure, and timbre independently', () => {
  const input = createMPEInput();
  configureZone(input, 'lower', 0, 15);
  configureZone(input, 'upper', 15, 4);
  const lower = input.process('lower', [0x99, 60, 96])[0];
  const upper = input.process('upper', [0x9e, 64, 80])[0];
  assert.equal(lower.mpe, true, 'channel 10 is a pitched member when explicitly in an MPE zone');
  assert.equal(upper.mpe, true);
  assert.notEqual(lower.noteId, upper.noteId);
  assert.equal(input.process('upper', [0x9f, 67, 80])[0].mpe, false, 'upper master remains ordinary MIDI');
  const bend = input.process('lower', [0xe9, 127, 127])[0];
  assert.equal(bend.pitchCents, 4800);
  assert.equal(bend.noteId, lower.noteId);
  assert.equal(input.process('lower', [0xd9, 127])[0].pressure, 1);
  const timbre = input.process('lower', [0xb9, 74, 0])[0];
  assert.equal(timbre.timbre, 0);
  assert.equal(timbre.pressure, 1);
  assert.equal(input.process('upper', [0xee, 0, 0])[0].pitchCents, -4800);
});

test('MPE bend sensitivity RPN and master bend combine without losing normalized values', () => {
  const input = createMPEInput();
  configureZone(input, 'controller', 0, 2);
  for (const [cc, value] of [[101, 0], [100, 0], [6, 12], [38, 50]]) input.process('controller', [0xb1, cc, value]);
  input.process('controller', [0xe1, 127, 127]);
  const note = input.process('controller', [0x91, 60, 127])[0];
  assert.equal(note.pitchCents, 1250);
  assert.equal(note.timbre, .5);
  const global = input.process('controller', [0xe0, 127, 127])[0];
  assert.equal(global.pitchCents, 1450);
  assert.equal(global.noteId, note.noteId);
});

test('MIDI note identities separate devices, channels, retriggers, and stale note offs', () => {
  const input = createMPEInput();
  const old = input.process('a', [0x91, 60, 90])[0];
  const recent = input.process('a', [0x91, 60, 100])[0];
  const channel = input.process('a', [0x92, 60, 100])[0];
  const device = input.process('b', [0x91, 60, 100])[0];
  assert.equal(new Set([old, recent, channel, device].map(note => note.noteId)).size, 4);
  assert.equal(input.process('a', [0x81, 60, 0])[0].noteId, old.noteId);
  assert.equal(input.process('a', [0xd1, 127])[0].noteId, recent.noteId);
  assert.equal(input.process('a', [0x91, 60, 0])[0].noteId, recent.noteId);
  assert.deepEqual(input.process('a', [0x81, 60, 0]), []);
  assert.deepEqual(input.disconnect('a').map(note => note.noteId), [channel.noteId]);
  assert.deepEqual(input.disconnect('b').map(note => note.noteId), [device.noteId]);
});

test('poly pressure stays with its note and malformed packets create no events', () => {
  const input = createMPEInput();
  input.process('keys', [0x90, 60, 90]);
  input.process('keys', [0x90, 64, 90]);
  const update = input.process('keys', [0xa0, 60, 100]);
  assert.equal(update.length, 1);
  assert.equal(update[0].note, 60);
  assert.equal(update[0].pressure, 100 / 127);
  assert.equal(input.process('keys', [0xb0, 74, 64])[0].pressure, 100 / 127);
  assert.deepEqual(input.process('keys', [0x91, 60]), []);
  assert.deepEqual(input.process('keys', [0x91, 128, 80]), []);
  assert.deepEqual(input.process('keys', [0xf8, 1]), []);
});
