(() => {
  'use strict';

  const GM_DRUM_LANES = Object.freeze({
    36: 'bd', 37: 'rs', 38: 'sd', 39: 'cp', 41: 'lt', 42: 'ch', 43: 'lt',
    45: 'mt', 46: 'oh', 47: 'mt', 48: 'ht', 49: 'cy', 50: 'ht', 56: 'cb', 57: 'cy'
  });
  const DEFAULT_MAPPING_KEY = 'cicada.midi.mappings.v1';

  function mapGeneralMIDIDrum(note) {
    return Object.hasOwn(GM_DRUM_LANES, note) ? GM_DRUM_LANES[note] : null;
  }

  function clamp(value, min, max) { return Math.min(max, Math.max(min, value)); }

  function mapCCValue(descriptor, controllerValue) {
    if (!descriptor || !Number.isFinite(descriptor.min) || !Number.isFinite(descriptor.max) || descriptor.max < descriptor.min) {
      throw new TypeError('parameter descriptor needs finite min and max values');
    }
    if (!Number.isInteger(controllerValue) || controllerValue < 0 || controllerValue > 127) {
      throw new RangeError('CC value must be an integer from 0 to 127');
    }
    const unit = controllerValue / 127;
    const min = descriptor.min;
    const max = descriptor.max;
    switch (descriptor.curve) {
      case 'log':
        if (controllerValue === 0) return min;
        if (controllerValue === 127) return max;
        if (min <= 0 || max <= 0) return min + unit * (max - min);
        return clamp(Math.exp(Math.log(min) + unit * (Math.log(max) - Math.log(min))), min, max);
      case 'fader': {
        if (descriptor.off && controllerValue === 0) return null;
        if (controllerValue === 127) return max;
        if (controllerValue === 0) return min;
        const low = 10 ** (min / 20);
        const high = 10 ** (max / 20);
        const amplitude = unit * high;
        if (amplitude <= low) return min;
        return clamp(20 * Math.log10(amplitude), min, max);
      }
      case 'toggle':
        return unit >= 0.5 ? max : min;
      case 'enum': {
        const count = Array.isArray(descriptor.values) && descriptor.values.length > 1
          ? descriptor.values.length : Math.max(2, Math.round(max - min) + 1);
        const selected = Math.round(unit * (count - 1));
        return count === 1 ? min : min + selected * (max - min) / (count - 1);
      }
      case 'linear':
      default:
        return min + unit * (max - min);
    }
  }

  function quantizeStep(tick, stepCount, ticksPerStep = 120) {
    if (!Number.isFinite(tick) || tick < 0) throw new RangeError('tick must be nonnegative');
    if (!Number.isInteger(stepCount) || stepCount < 1 || stepCount > 64) throw new RangeError('step count must be from 1 to 64');
    if (!Number.isInteger(ticksPerStep) || ticksPerStep < 1) throw new RangeError('ticks per step must be positive');
    return Math.floor((tick + ticksPerStep / 2) / ticksPerStep) % stepCount;
  }

  function inputKey(mapping) {
    return `${mapping.device}\u0000${mapping.channel}\u0000${mapping.cc === undefined ? `n${mapping.note}` : `c${mapping.cc}`}`;
  }

  function targetKey(mapping) {
    return mapping.address ? `address:${mapping.address}` : `action:${mapping.action}`;
  }

  function createMappingStore(storage, key = DEFAULT_MAPPING_KEY) {
    const read = () => {
      try {
        const value = JSON.parse(storage?.getItem(key) || '[]');
        return Array.isArray(value) ? value.filter(item => item && typeof item.device === 'string' && Number.isInteger(item.channel)) : [];
      } catch { return []; }
    };
    const write = mappings => {
      try { storage?.setItem(key, JSON.stringify(mappings)); }
      catch { /* Browser storage can be disabled; live MIDI remains usable. */ }
    };
    return {
      all: read,
      findInput(device, channel, type, value) {
        const field = type === 'cc' ? 'cc' : 'note';
        return read().find(mapping => mapping.device === device && mapping.channel === channel && mapping[field] === value) || null;
      },
      bind(mapping) {
        const list = read().filter(item => inputKey(item) !== inputKey(mapping));
        list.push({...mapping});
        write(list);
        return list;
      },
      remove(mapping) {
        const identity = inputKey(mapping);
        const list = read().filter(item => inputKey(item) !== identity || targetKey(item) !== targetKey(mapping));
        write(list);
        return list;
      },
      clearTarget(target) {
        const identity = targetKey(target);
        const list = read().filter(item => targetKey(item) !== identity);
        write(list);
        return list;
      }
    };
  }

  // Main-thread ownership. The audio ABI stays monophonic; last-held priority
  // prevents one source's release from closing another source's sounding note.
  function createInputRouter({noteOn, noteOff, onPress = () => {}, onRelease = () => {}}) {
    const notes = new Map(), sounding = new Map(), blocked = new Set(), sustain = new Set();
    let serial = 0;
    const scope = input => JSON.stringify([input.source, input.device, input.channel]);
    const key = input => JSON.stringify([input.source, input.device, input.channel, input.input ?? input.note]);
    const matches = (note, filter) => Object.entries(filter).every(([name, value]) => note[name] === value);
    const route = n => n.drum ? JSON.stringify([n.track, mapGeneralMIDIDrum(n.note)]) : n.track;
    function reconcile(track) {
      const current = sounding.get(track);
      let next;
      for (const n of notes.values()) if (route(n) === track && (!next || n.order > next.order)) next = n;
      if (current?.id === next?.id) return;
      // Same-pitch owners share the monophonic gate without a retrigger on release.
      if (current && (!next || current.note !== next.note)) noteOff(current.track, current.note, current.sinkID || current.id);
      sounding.delete(track);
      if (next) {
        if (current?.note === next.note || noteOn(next.track, next.note, next.velocity, next.id) !== false) sounding.set(track, {...next, sinkID: current?.note === next.note ? current.sinkID || current.id : next.id});
      }
    }
    function release(n) {
      notes.delete(n.key);
      onRelease(n);
    }
    return {
      press(input) {
        if (!input || typeof input.source !== 'string' || typeof input.device !== 'string' ||
            typeof input.track !== 'string' || !input.track || !Number.isInteger(input.channel) || input.channel < 0 || input.channel > 15 ||
            !Number.isInteger(input.note) || input.note < 0 || input.note > 127 || !Number.isInteger(input.velocity) || input.velocity < 1 || input.velocity > 127) throw new TypeError('Invalid musical input');
        const identity = key(input);
        if (blocked.has(identity) || notes.get(identity)?.held) return null;
        if (!notes.has(identity) && notes.size >= 128) return null;
        const previous = notes.get(identity);
        if (previous) release(previous);
        const n = {...input, key: identity, order: ++serial, id: identity + ':' + serial, held: true};
        notes.set(identity, n);
        if (previous && route(previous) !== route(n)) reconcile(route(previous));
        reconcile(route(n));
        if (!sounding.has(route(n))) { notes.delete(identity); return null; }
        onPress(n);
        return n.id;
      },
      release(input) {
        const identity = key(input);
        blocked.delete(identity);
        const n = notes.get(identity);
        if (!n || !n.held) return false;
        n.held = false;
        if (sustain.has(scope(n)) && !n.drum) return true;
        release(n); reconcile(route(n)); return true;
      },
      sustain(input, on) {
        const identity = scope(input);
        if (on) sustain.add(identity);
        else {
          sustain.delete(identity);
          const tracks = new Set();
          for (const n of [...notes.values()]) if (scope(n) === identity && !n.held) { release(n); tracks.add(route(n)); }
          for (const track of tracks) reconcile(track);
        }
      },
      panic(filter = {}) {
        const tracks = new Set();
        for (const n of [...notes.values()]) if (matches(n, filter)) {
          if (n.held) blocked.add(n.key);
          release(n); tracks.add(route(n));
        }
        for (const identity of [...sustain]) {
          const [source, device, channel] = JSON.parse(identity);
          if (matches({source, device, channel}, filter)) sustain.delete(identity);
        }
        for (const track of tracks) reconcile(track);
      },
      get size() { return notes.size; }
    };
  }

  function createParameterDispatcher({getAudio, getCatalog, onError = () => {}}) {
    return (address, controllerValue) => {
      try {
        const catalog = getCatalog();
        const binding = catalog?.addresses?.find(item => item.address === address);
        const descriptor = catalog?.registry?.find(item => item.id === binding?.param);
        if (!binding || !descriptor?.live) throw new Error('MIDI target is unavailable or is not live');
        const value = mapCCValue(descriptor, controllerValue);
        if (value !== null && (!Number.isFinite(value) || value < descriptor.min || value > descriptor.max) || value === null && !descriptor.off) throw new Error('Invalid MIDI parameter value');
        getAudio().setParam(address, value);
        return true;
      } catch (error) { onError(error); return false; }
    };
  }

  function createMIDIPerformance({router, mappings, getTrack, dispatchCC, learn = () => false, action = () => {}, unsupported = () => {}}) {
    const actions = new Set();
    const identity = (device, channel, note) => JSON.stringify([device, channel, note]);
    return {
      receive(port, event) {
        const data = event.data;
        if (!data || data.length < 2 || !Number.isInteger(data[0]) || data[0] < 0x80 || data[0] >= 0xf0) return;
        const command = data[0] & 0xf0, channel = data[0] & 15;
        if (!Number.isInteger(data[1]) || data[1] < 0 || data[1] > 127) return;
        if (![0xc0, 0xd0].includes(command) && (data.length < 3 || !Number.isInteger(data[2]) || data[2] < 0 || data[2] > 127)) return;
        const first = data[1], second = data[2] || 0;
        const device = String(port?.id || port?.name || 'MIDI device');
        const name = port?.name || port?.manufacturer || device;
        const input = {source: 'midi', device, channel, note: first, time: Number(event.timeStamp) || 0};
        if (command === 0xb0) {
          if (first === 64) { router.sustain(input, second >= 64); return; }
          if (first === 120 || first === 123) { router.panic({source: 'midi', device, channel}); return; }
          if (learn(name, channel, 'cc', first)) return;
          const mapping = mappings.findInput(name, channel, 'cc', first);
          if (mapping?.address) dispatchCC(mapping.address, second);
          return;
        }
        if ([0xe0, 0xd0, 0xa0].includes(command)) { unsupported(command); return; }
        const on = command === 0x90 && second > 0;
        const off = command === 0x80 || command === 0x90 && second === 0;
        if (off) { actions.delete(identity(device, channel, first)); router.release(input); return; }
        if (!on) return;
        if (learn(name, channel, 'note', first)) return;
        const mapping = mappings.findInput(name, channel, 'note', first);
        if (mapping?.action) {
          const id = identity(device, channel, first);
          if (!actions.has(id)) { actions.add(id); action(mapping.action); }
          return;
        }
        const target = getTrack(channel);
        if (!target?.id || channel === 9 && !mapGeneralMIDIDrum(first)) return;
        router.press({...input, track: target.id, drum: channel === 9, velocity: second});
      },
      disconnect(port) { router.panic({source: 'midi', device: String(port.id || port.name || 'MIDI device')}); }
    };
  }

  // Bridge the qualified PR109 Go Controller. The host supplies wrappers for
  // Down, Up, SetParam and Panic; this adapter encodes no kernel commands and
  // never acknowledges KernelResetReady or starts playback automatically.
  function createBrowserPerformanceSink({down, up, setParam, panic, disconnect, getCatalog}) {
    if (![down, up, setParam, panic, disconnect, getCatalog].every(fn => typeof fn === 'function')) throw new TypeError('Browser controller callbacks are required');
    const tokens = new Map();
    let serial = 0;
    const check = result => {
      if (result instanceof Error) throw result;
      if (typeof result === 'string' && result) throw new Error(result);
      if (result === false) throw new Error('Browser controller rejected input');
    };
    return {
      backend: 'browser',
      params: getCatalog,
      noteOn(track, note, velocity, id) {
        if (typeof id !== 'string' || !id) throw new TypeError('Browser note owner ID is required');
        if (tokens.has(id)) return true;
        const token = 'device-input:' + ++serial;
        check(down(token, track, note, velocity, false));
        tokens.set(id, token); return true;
      },
      noteOff(_track, _note, id) {
        const token = tokens.get(id); if (!token) return false;
        check(up(token)); tokens.delete(id); return true;
      },
      setParam(address, value) { check(setParam(address, value)); },
      setMute(track, on) { check(setParam(track + '.mute', on ? 1 : 0)); },
      setSolo(track, on) { check(setParam(track + '.solo', on ? 1 : 0)); },
      panic() {
        try { check(panic()); for (const token of tokens.values()) check(up(token)); }
        catch (error) { disconnect(); throw error; }
        finally { tokens.clear(); }
      },
      silence() {
        // Detach output even for pulse/resonator or autonomous graph sources.
        // Reconnection is the host's explicit initialization/gesture workflow.
        try { this.panic(); } finally { disconnect(); }
      }
    };
  }

  // Standard OS-paired Gamepad API only. A button held when first observed must
  // return to neutral before it can play. Analog triggers use .65/.35 hysteresis.
  function createGamepadPerformance({router, getTrack, dispatchCC, getAddresses = () => ({}), action = () => {}}) {
    const devices = new Map();
    const offsets = [[0], [3], [7], [12]];
    function disconnect(device, state) {
      router.panic({source: 'gamepad', device}); devices.delete(device);
    }
    return {
      poll(pads) {
        const connected = new Set();
        for (const pad of pads || []) {
          if (!pad || pad.connected === false || pad.mapping !== 'standard') continue;
          const device = String(pad.index) + ':' + pad.id;
          connected.add(device);
          let state = devices.get(device);
          if (!state) { state = {buttons: new Map(), expressions: new Map()}; devices.set(device, state); }
          for (let i = 0; i < 8; i++) {
            const value = Number(pad.buttons[i]?.value || 0);
            const old = state.buttons.get(i);
            if (old === undefined) {
              state.buttons.set(i, value > .35 ? 'blocked' : false);
              if (value <= .35) for (const offset of offsets[i] || []) router.release({source:'gamepad', device, channel:0, input:i + ':' + offset, note:48+offset});
              continue;
            }
            if (old === 'blocked') { if (value <= .35) { state.buttons.set(i, false); for (const offset of offsets[i] || []) router.release({source:'gamepad', device, channel:0, input:i + ':' + offset, note:48+offset}); } continue; }
            const on = old ? value > .35 : value >= .65;
            if (on === old) continue;
            state.buttons.set(i, on);
            const base = {source: 'gamepad', device, channel: 0};
            if (i < offsets.length) for (const offset of offsets[i]) {
              const input = {...base, input: i + ':' + offset, note: 48 + offset};
              if (on) { const track = getTrack(); if (track) router.press({...input, track, velocity: 100}); }
              else router.release(input);
            }
            else if (on && (i === 4 || i === 5)) action(i === 4 ? 'pattern' : 'scene');
          }
          const addresses = getAddresses();
          for (const [name, raw] of [['expression', Number(pad.buttons[7]?.value || 0)], ['timbre', (() => { const x=Number(pad.axes[2] || 0); const value=Math.abs(x) <= .12 ? 0 : Math.sign(x)*(Math.abs(x)-.12)/.88; return (value+1)/2; })()]]) {
            const value = Math.round(clamp(Number.isFinite(raw) ? raw : 0, 0, 1) * 127);
            if (addresses[name] && state.expressions.get(name) !== value) { dispatchCC(addresses[name], value); state.expressions.set(name, value); }
          }
        }
        for (const [device, state] of devices) if (!connected.has(device)) disconnect(device, state);
      },
      panic() {
        router.panic({source: 'gamepad'});
        for (const state of devices.values()) for (const [button, value] of state.buttons) if (value) state.buttons.set(button, 'blocked');
      }
    };
  }

  const api = {GM_DRUM_LANES, DEFAULT_MAPPING_KEY, mapGeneralMIDIDrum, mapCCValue, quantizeStep, createMappingStore,
    createInputRouter, createParameterDispatcher, createMIDIPerformance, createGamepadPerformance, createBrowserPerformanceSink};
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof window !== 'undefined') window.cicadaMidi = api;
})();
