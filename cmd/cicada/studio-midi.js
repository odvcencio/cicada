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

  const api = {GM_DRUM_LANES, DEFAULT_MAPPING_KEY, mapGeneralMIDIDrum, mapCCValue, quantizeStep, createMappingStore};
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof window !== 'undefined') window.cicadaMidi = api;
})();
