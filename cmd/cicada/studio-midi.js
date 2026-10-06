(() => {
  'use strict';

  const GM_DRUM_LANES = Object.freeze({
    36: 'bd', 37: 'rs', 38: 'sd', 39: 'cp', 41: 'lt', 42: 'ch', 43: 'lt',
    45: 'mt', 46: 'oh', 47: 'mt', 48: 'ht', 49: 'cy', 50: 'ht', 56: 'cb', 57: 'cy'
  });
  const DEFAULT_MAPPING_KEY = 'cicada.midi.mappings.v1';
  const MIDI_PPQ = 960;
  const TICKS_PER_STEP = MIDI_PPQ / 4;

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
        if (min <= 0 || max <= 0) return min + unit * (max - min);
        return Math.exp(Math.log(min) + unit * (Math.log(max) - Math.log(min)));
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

  function quantizeStep(tick, stepCount, ticksPerStep = TICKS_PER_STEP) {
    if (!Number.isFinite(tick) || tick < 0) throw new RangeError('tick must be nonnegative');
    if (!Number.isInteger(stepCount) || stepCount < 1 || stepCount > 64) throw new RangeError('step count must be from 1 to 64');
    if (!Number.isInteger(ticksPerStep) || ticksPerStep < 1) throw new RangeError('ticks per step must be positive');
    return Math.floor((tick + ticksPerStep / 2) / ticksPerStep) % stepCount;
  }

  // Web MIDI carries MIDI 1 packets. Normalize them to note identities and full
  // expression snapshots so the audio and take paths need no MIDI byte parsing.
  function createMPEInput() {
    const devices = new Map();
    const active = new Map();
    let nextID = 1;
    function deviceState(device) {
      if (!devices.has(device)) devices.set(device, {
        enabled: false, configured: false, lower: 15, upper: 0,
        channels: Array.from({length: 16}, () => ({bend: 8192, range: null, pressure: 0, timbre: .5, rpn: [127, 127], coarse: 48, fine: 0})),
        notes: new Map()
      });
      return devices.get(device);
    }
    function member(state, channel) {
      return channel > 0 && channel <= state.lower || channel < 15 && channel >= 15 - state.upper;
    }
    function master(state, channel) { return channel <= state.lower ? 0 : 15; }
    function bendCents(channel, fallback) {
      return (channel.bend - 8192) / (channel.bend < 8192 ? 8192 : 8191) * (channel.range ?? fallback) * 100;
    }
    function snapshot(state, note) {
      const channel = state.channels[note.channel];
      const pitch = bendCents(channel, note.mpe ? 48 : 2) + (note.mpe ? bendCents(state.channels[master(state, note.channel)], 2) : 0);
      return {...note, type: 'note-expression', pitchCents: clamp(pitch, -9600, 9600), pressure: note.pressure ?? channel.pressure, timbre: channel.timbre};
    }
    function allocateID() {
      for (let attempts = 0; attempts < 65534; attempts++) {
        const id = nextID;
        nextID = nextID % 65534 + 1;
        if (!active.has(id)) return id;
      }
      throw new RangeError('too many active MIDI notes');
    }
    function process(device, bytes) {
      if (typeof device !== 'string' || !bytes || bytes.length < 2) return [];
      const status = bytes[0];
      const command = status & 0xf0;
      const channel = status & 15;
      if (status < 0x80 || status >= 0xf0 || bytes[1] > 127 || bytes[1] < 0) return [];
      if (command !== 0xd0 && (bytes.length < 3 || bytes[2] > 127 || bytes[2] < 0)) return [];
      const first = bytes[1], second = bytes[2] || 0;
      const state = deviceState(device), controls = state.channels[channel];
      if (command === 0x90 && second > 0) {
        const mpe = state.enabled && member(state, channel);
        const note = {device, channel, note: first, velocity: second, noteId: allocateID(), mpe};
        active.set(note.noteId, note);
        const key = `${channel}:${first}`, stack = state.notes.get(key) || [];
        stack.push(note);
        state.notes.set(key, stack);
        return [{...snapshot(state, note), type: 'note-on'}];
      }
      if (command === 0x80 || command === 0x90) {
        const key = `${channel}:${first}`, stack = state.notes.get(key);
        if (!stack?.length) return [];
        // Pair retriggers in arrival order so an older off never stops the newer voice.
        const note = stack.shift();
        if (!stack.length) state.notes.delete(key);
        active.delete(note.noteId);
        return [{...note, type: 'note-off'}];
      }
      let expression = false;
      if (command === 0xb0) {
        if (first === 101 || first === 100) controls.rpn[first === 101 ? 0 : 1] = second;
        else if (first === 6 || first === 38) {
          if (controls.rpn[0] === 0 && controls.rpn[1] === 6 && first === 6 && (channel === 0 || channel === 15)) {
            if (!state.configured) state.lower = state.upper = 0;
            state.configured = state.enabled = true;
            state[channel === 0 ? 'lower' : 'upper'] = Math.min(15, second);
            if (state.lower + state.upper > 14 && state.lower && state.upper) state[channel === 0 ? 'upper' : 'lower'] = Math.max(0, 14 - second);
          } else if (controls.rpn[0] === 0 && controls.rpn[1] === 0) {
            if (first === 6) controls.coarse = second;
            else controls.fine = Math.min(99, second);
            controls.range = clamp(controls.coarse + controls.fine / 100, 0, 96);
            expression = true;
          }
        } else if (first === 74) { controls.timbre = second / 127; expression = true; }
      } else if (command === 0xe0) { controls.bend = first + second * 128; expression = true; }
      else if (command === 0xd0) { controls.pressure = first / 127; expression = true; }
      else if (command === 0xa0) { expression = true; }
      if (!expression) return [];
      if (!state.configured && channel !== 0) state.enabled = true;
      const result = [];
      for (const stack of state.notes.values()) for (const note of stack) {
        if (note.channel !== channel && !(note.mpe && master(state, note.channel) === channel && command === 0xe0)) continue;
        if (command === 0xa0 && note.note !== first) continue;
        note.mpe = state.enabled && member(state, note.channel);
        if (command === 0xa0) note.pressure = second / 127;
        else if (command === 0xd0) delete note.pressure;
        const value = snapshot(state, note);
        result.push(value);
      }
      return result;
    }
    function disconnect(device) {
      const state = devices.get(device);
      if (!state) return [];
      const notes = [...state.notes.values()].flat();
      for (const note of notes) active.delete(note.noteId);
      devices.delete(device);
      return notes.map(note => ({...note, type: 'note-off'}));
    }
    return {process, disconnect};
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

  const api = {GM_DRUM_LANES, DEFAULT_MAPPING_KEY, MIDI_PPQ, TICKS_PER_STEP, mapGeneralMIDIDrum, mapCCValue, quantizeStep, createMappingStore, createMPEInput};
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof window !== 'undefined') window.cicadaMidi = api;
})();
