(function () {
  'use strict';
  if (typeof navigator.requestMIDIAccess === 'function') return;
  let access, opening, subscribed = false;
  function event(target, type, properties) {
    const e = new Event(type);
    for (const [name, value] of Object.entries(properties || {})) Object.defineProperty(e, name, {value});
    target.dispatchEvent(e);
    const handler = target['on' + type];
    if (typeof handler === 'function') handler.call(target, e);
  }
  class Input extends EventTarget {
    constructor(value) {
      super();
      Object.assign(this, value);
      this.type = 'input'; this.version = ''; this.state = 'connected'; this.connection = 'open';
      this.onmidimessage = null; this.onstatechange = null;
    }
    open() { if (this.state === 'connected') this.connection = 'open'; return Promise.resolve(this); }
    close() { this.connection = 'closed'; event(this, 'statechange', {port: this}); if (access) event(access, 'statechange', {port: this}); return Promise.resolve(this); }
  }
  function update(inputs) {
    if (!access) return;
    const present = new Set();
    for (const value of inputs || []) {
      if (!value || typeof value.id !== 'string') continue;
      present.add(value.id);
      if (!access.inputs.has(value.id)) {
        const input = new Input(value);
        access.inputs.set(input.id, input);
        event(access, 'statechange', {port: input});
      }
    }
    for (const [id, input] of access.inputs) {
      if (present.has(id)) continue;
      input.state = 'disconnected'; input.connection = 'closed'; access.inputs.delete(id);
      event(input, 'statechange', {port: input});
      event(access, 'statechange', {port: input});
    }
  }
  Object.defineProperty(navigator, 'requestMIDIAccess', {configurable: true, value: function (options) {
    if (options && options.sysex) return Promise.reject(new DOMException('System-exclusive MIDI access is unavailable.', 'NotSupportedError'));
    if (access) return Promise.resolve(access);
    if (opening) return opening;
    const bridge = window.gosxDesktop;
    if (!bridge || typeof bridge.call !== 'function') return Promise.reject(new DOMException('Native MIDI is unavailable.', 'NotSupportedError'));
    if (!subscribed) {
      subscribed = true;
      bridge.on('cicada.midi.state', value => update(value && value.inputs));
      bridge.on('cicada.midi.reset', () => { if (access) event(access, 'statechange', {port: null}); });
      bridge.on('cicada.midi.messages', value => {
        if (!access) return;
        for (const message of value && value.messages || []) {
          const input = access.inputs.get(message.id);
          if (!input || input.connection !== 'open' || !Array.isArray(message.data) || message.data.length > 3) continue;
          event(input, 'midimessage', {data: new Uint8Array(message.data), receivedTime: performance.now()});
        }
      });
    }
    opening = bridge.call('cicada.midi.open', {sysex: false}).then(value => {
      access = new EventTarget();
      access.inputs = new Map(); access.outputs = new Map(); access.sysexEnabled = false; access.onstatechange = null;
      update(value && value.inputs);
      return access;
    }).finally(() => { opening = null; });
    return opening;
  }});
}());
