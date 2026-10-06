const OP_STATE = 23, OP_STINGER = 24, OP_MACRO = 18;
const STATE_CHANGED = 13, LAYER_CHANGED = 11;
const uint = (n, max, label) => {
  if (!Number.isInteger(n) || n < 0 || n > max) throw new RangeError(`Invalid ${label}`);
  return n;
};
const quantize = (name, phrase) => {
  if (name === '' || name === 'now') return 0;
  if (name === 'beat') return 1;
  if (name === 'bar') return 2;
  if (name === '2bars') return 6;
  if (name === '4bars') return 8;
  if (name === 'phrase' && phrase >= 1 && phrase <= 64) return 21;
  throw new RangeError(`Unsupported director quantize ${name}`);
};

/** Encode one fixed, little-endian 24-byte command. Tick uses bigint. */
export function encodeCommand(c) {
  uint(c.Op, 24, 'opcode');
  if (c.Op === 0) throw new RangeError('Invalid opcode');
  uint(c.Track, 255, 'track'); uint(c.Index ?? 0, 65535, 'index');
  uint(c.Arg0 ?? 0, 0xffffffff, 'arg0'); uint(c.Arg1 ?? 0, 0xffffffff, 'arg1');
  if (c.Pad) throw new RangeError('Nonzero padding');
  const tick = BigInt(c.Tick ?? 0);
  if (typeof c.Tick === 'number' && !Number.isSafeInteger(c.Tick)) throw new RangeError('Unsafe tick');
  if (tick < 0n || tick > 0x7fffffffffffffffn) throw new RangeError('Invalid tick');
  const bytes = new Uint8Array(24), view = new DataView(bytes.buffer);
  view.setUint8(0, c.Op); view.setUint8(1, c.Track);
  view.setUint16(2, c.Index ?? 0, true);
  view.setUint32(4, c.Arg0 ?? 0, true); view.setUint32(8, c.Arg1 ?? 0, true);
  view.setBigInt64(16, tick, true);
  return bytes;
}

/** Decode a whole message batch without losing 64-bit transport ticks. */
export function decodeMessages(bytes) {
  if (!(bytes instanceof Uint8Array) || bytes.byteLength % 16) throw new RangeError('Partial message record');
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength), messages = [];
  for (let i = 0; i < bytes.byteLength; i += 16) {
    const Kind = view.getUint8(i);
    if (Kind < 1 || Kind > 15) throw new RangeError('Unknown message kind');
    messages.push({ Kind, Track: view.getUint8(i + 1), A: view.getUint16(i + 2, true), B: view.getUint32(i + 4, true), Tick: view.getBigInt64(i + 8, true) });
  }
  return messages;
}

/** Command adapter for an initialized Cicada AudioWorklet node's MessagePort. */
export const workletSender = port => bytes => port.postMessage({ t: 'c', bytes }, [bytes.buffer]);

export class GameDirector {
  #surface; #send; #state = ''; #layers;
  constructor(surface, send) {
    // Own the manifest so callers cannot change names or IDs while playing.
    const s = structuredClone(surface);
    if (s.version !== 1 || ![44100, 48000, 96000].includes(s.sample_rate)) throw new RangeError('Invalid director surface');
    uint(s.tracks, 16, 'track count'); if (!s.tracks) throw new RangeError('Invalid track count');
    uint(s.phrase_bars, 64, 'phrase length'); quantize(s.land, s.phrase_bars);
    for (const [key, limit] of [['macros', 16], ['states', 64], ['stingers', 64], ['transitions', 256], ['setup', 512]]) {
      if (!Array.isArray(s[key]) || s[key].length > limit) throw new RangeError(`Invalid ${key}`);
    }
    const unique = (entries, max) => {
      const names = new Set(), ids = new Set();
      for (const v of entries) {
        if (typeof v.name !== 'string' || !v.name || new TextEncoder().encode(v.name).length > 64 || names.has(v.name)) throw new RangeError('Invalid control name');
        names.add(v.name);
        if (max) { uint(v.id, max - 1, 'control ID'); if (ids.has(v.id)) throw new RangeError('Duplicate control ID'); ids.add(v.id); }
      }
      return names;
    };
    unique(s.macros, 16); const states = unique(s.states, 64); unique(s.stingers);
    for (const v of s.macros) uint(v.smooth_frames, 0xffffffff, 'smoothing frames');
    for (const v of s.states) uint(v.scene, 65535, 'scene');
    for (const v of s.stingers) { uint(v.track, s.tracks - 1, 'stinger track'); uint(v.slot, 15, 'slot'); }
    const pairs = new Set();
    for (const v of s.transitions) {
      const key = `${v.from}/${v.to}`;
      if (!states.has(v.from) || !states.has(v.to) || pairs.has(key)) throw new RangeError('Invalid transition');
      pairs.add(key);
    }
    for (const v of [...s.stingers, ...s.transitions]) { quantize(v.quantize, s.phrase_bars); uint(v.crossfade_frames, 0xffffffff, 'crossfade frames'); }
    for (const c of s.setup) encodeCommand(c);
    if (typeof send !== 'function') throw new TypeError('Director requires a command sender');
    this.#surface = s; this.#send = send; this.#layers = (1 << s.tracks) - 1;
  }
  setup() { for (const c of this.#surface.setup) this.#send(encodeCommand(c)); }
  setState(name, tick = 0n) {
    const s = this.#surface, v = s.states.find(v => v.name === name);
    if (!v) throw new RangeError(`Unknown director state ${name}`);
    const t = s.transitions.find(t => t.from === this.#state && t.to === name);
    const q = quantize(t?.quantize ?? (s.land || 'bar'), s.phrase_bars);
    this.#send(encodeCommand({ Op: OP_STATE, Track: 255, Index: v.id, Arg0: v.scene | q << 16, Arg1: t?.crossfade_frames ?? 0, Tick: tick }));
  }
  setMacro(name, value, tick = 0n) {
    const v = this.#surface.macros.find(v => v.name === name);
    if (!v) throw new RangeError(`Unknown director macro ${name}`);
    if (!Number.isFinite(value) || value < 0 || value > 1) throw new RangeError('Macro value must be 0 to 1');
    const data = new DataView(new ArrayBuffer(4)); data.setFloat32(0, value, true);
    this.#send(encodeCommand({ Op: OP_MACRO, Track: 255, Index: v.id, Arg0: data.getUint32(0, true), Arg1: v.smooth_frames, Tick: tick }));
  }
  triggerStinger(name, tick = 0n) {
    const s = this.#surface, v = s.stingers.find(v => v.name === name);
    if (!v) throw new RangeError(`Unknown director stinger ${name}`);
    this.#send(encodeCommand({ Op: OP_STINGER, Track: v.track, Index: v.slot, Arg0: quantize(v.quantize, s.phrase_bars), Arg1: v.crossfade_frames, Tick: tick }));
  }
  handle(message) {
    if (message.Kind === STATE_CHANGED) {
      const state = this.#surface.states.find(v => v.id === message.A && v.scene === message.B);
      if (state) this.#state = state.name;
    } else if (message.Kind === LAYER_CHANGED) this.#layers = message.B;
  }
  get state() { return this.#state; }
  get layerMask() { return this.#layers; }
}
