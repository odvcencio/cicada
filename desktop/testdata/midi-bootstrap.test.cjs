'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');
const source = fs.readFileSync(path.join(__dirname, 'midi-bootstrap.js'), 'utf8');

function host(existing) {
  const calls = [], listeners = new Map();
  const context = vm.createContext({navigator: {requestMIDIAccess: existing}, EventTarget, Event, DOMException, Uint8Array, performance, window: {gosxDesktop: {
    on: (topic, handler) => listeners.set(topic, handler),
    call: async (method, payload) => { calls.push({method, payload}); return {inputs: [{id: 'private', name: 'Private keyboard', manufacturer: 'Cicada'}]}; }
  }}});
  vm.runInContext(source, context);
  return {context, calls, listeners, request: options => context.navigator.requestMIDIAccess(options)};
}

test('native MIDI is explicit, preserves browser MIDI, and refuses SysEx', async () => {
  const existing = () => Promise.resolve('browser');
  const browser = host(existing);
  assert.equal(browser.context.navigator.requestMIDIAccess, existing);
  const native = host();
  assert.equal(native.calls.length, 0, 'bootstrap never starts MIDI');
  await assert.rejects(native.request({sysex: true}), {name: 'NotSupportedError'});
  assert.equal(native.calls.length, 0);
  const access = await native.request({sysex: false});
  assert.equal(native.calls.length, 1);
  assert.equal(native.calls[0].method, 'cicada.midi.open');
  assert.equal(access.sysexEnabled, false);
  assert.equal(access.outputs.size, 0, 'native MIDI remains input-only');
  assert.equal(await native.request(), access);
});

test('native note, CC and expression delivery follows the MIDIInput event contract', async () => {
  const native = host();
  const access = await native.request();
  const input = access.inputs.get('private');
  const messages = [], propertyMessages = [];
  input.addEventListener('midimessage', event => messages.push(Array.from(event.data)));
  input.onmidimessage = event => propertyMessages.push(Array.from(event.data));
  const data = [[0x90, 60, 100], [0xb0, 74, 90], [0xe0, 0, 96], [0xd0, 70], [0x80, 60, 0]];
  native.listeners.get('cicada.midi.messages')({messages: data.map(data => ({id: 'private', data}))});
  assert.deepEqual(messages, data);
  assert.deepEqual(propertyMessages, data);
  await input.close();
  native.listeners.get('cicada.midi.messages')({messages: [{id: 'private', data: [0x90, 62, 100]}]});
  assert.equal(messages.length, data.length, 'closed input does not deliver notes');
});

test('unplug and queue overflow release held notes through existing statechange handling', async () => {
  const native = host();
  const access = await native.request();
  const input = access.inputs.get('private');
  let changes = 0;
  access.addEventListener('statechange', () => changes++);
  native.listeners.get('cicada.midi.reset')();
  assert.equal(changes, 1);
  native.listeners.get('cicada.midi.state')({inputs: []});
  assert.equal(changes, 2);
  assert.equal(access.inputs.size, 0);
  assert.equal(input.state, 'disconnected');
  assert.equal(input.connection, 'closed');
  native.listeners.get('cicada.midi.state')({inputs: [{id: 'private', name: 'Private keyboard'}]});
  assert.equal(changes, 3);
  assert.equal(access.inputs.get('private').state, 'connected');
});
