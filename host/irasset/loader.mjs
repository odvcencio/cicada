// Fetch and decode one selected impulse on a host/worker thread. Never call
// this module from an AudioWorklet process callback.
const MAX_BYTES = 16 << 20;
const MAX_FRAMES = 1 << 20;

function validateEntry(entry) {
  if (!entry || !/^[a-z0-9-]{1,64}$/.test(entry.id) ||
      !/^[0-9a-f]{64}$/.test(entry.sha256) ||
      !["MIT", "CC0-1.0", "CC-BY-4.0"].includes(entry.license) ||
      !entry.attribution || !entry.name || !entry.category ||
      !Number.isInteger(entry.bytes) || entry.bytes < 44 || entry.bytes > MAX_BYTES ||
      !Number.isInteger(entry.rate_hz) || entry.rate_hz < 8000 || entry.rate_hz > 192000 ||
      ![1, 2].includes(entry.channels) || ![16, 24, 32].includes(entry.bit_depth) ||
      !Number.isInteger(entry.frames) || entry.frames < 1 || entry.frames > MAX_FRAMES ||
      entry.frames > entry.rate_hz * 12) throw new Error("Invalid impulse metadata");
  for (const field of ["url", "source_url", "license_url"]) {
    const url = new URL(entry[field]);
    if (url.protocol !== "https:" || url.username || url.password || url.hash) {
      throw new Error("Impulse URLs must use HTTPS");
    }
  }
}

async function verifiedBytes(response, entry) {
  if (!response.ok) throw new Error(`Impulse fetch returned HTTP ${response.status}`);
  const declaredLength = Number(response.headers.get("content-length"));
  if (declaredLength > entry.bytes) throw new Error("Impulse exceeds declared byte count");
  const data = new Uint8Array(entry.bytes);
  const reader = response.body.getReader();
  let offset = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      if (offset + value.length > data.length) throw new Error("Impulse exceeds declared byte count");
      data.set(value, offset);
      offset += value.length;
    }
  } catch (error) {
    await reader.cancel().catch(() => {});
    throw error;
  } finally {
    reader.releaseLock();
  }
  if (offset !== data.length) throw new Error("Impulse differs from declared byte count");
  const hash = new Uint8Array(await globalThis.crypto.subtle.digest("SHA-256", data));
  const checksum = Array.from(hash, byte => byte.toString(16).padStart(2, "0")).join("");
  if (checksum !== entry.sha256) throw new Error("Impulse checksum mismatch");
  return data;
}

function resample(input, sourceRate, targetRate, frames) {
  const output = new Float32Array(frames);
  const ratio = sourceRate / targetRate;
  const band = Math.min(1, 1 / ratio);
  const cutoff = .45 * band;
  const radius = Math.ceil(48 / band);
  for (let i = 0; i < output.length; i++) {
    const position = i * ratio;
    const center = Math.floor(position);
    let sum = 0, weights = 0;
    for (let j = center - radius; j <= center + radius; j++) {
      const x = j - position;
      if (Math.abs(x) > radius) continue;
      let weight = Math.abs(x) < 1e-12 ? 2 * cutoff : Math.sin(2 * Math.PI * cutoff * x) / (Math.PI * x);
      weight *= .42 + .5 * Math.cos(Math.PI * x / radius) + .08 * Math.cos(2 * Math.PI * x / radius);
      weights += weight;
      if (j >= 0 && j < input.length) sum += input[j] * weight;
    }
    output[i] = sum * ratio / weights;
  }
  return output;
}

function decode(data, entry, targetRate) {
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
  const tag = offset => String.fromCharCode(...data.subarray(offset, offset + 4));
  if (data.length < 44 || tag(0) !== "RIFF" || tag(8) !== "WAVE" || view.getUint32(4, true) + 8 !== data.length) {
    throw new Error("Invalid impulse RIFF WAV");
  }
  let encoding = 0, formatSeen = false, dataSeen = false, pcmOffset = 0, pcmBytes = 0;
  for (let offset = 12; offset < data.length;) {
    if (offset + 8 > data.length) throw new Error("Truncated impulse WAV chunk");
    const length = view.getUint32(offset + 4, true);
    const start = offset + 8;
    if (start + length + (length & 1) > data.length) throw new Error("Invalid impulse WAV chunk size");
    if (tag(offset) === "fmt ") {
      if (formatSeen || length < 16) throw new Error("Invalid impulse WAV format chunk");
      formatSeen = true;
      encoding = view.getUint16(start, true);
      const channels = view.getUint16(start + 2, true);
      const rate = view.getUint32(start + 4, true);
      const bytesPerSecond = view.getUint32(start + 8, true);
      const alignment = view.getUint16(start + 12, true);
      const bits = view.getUint16(start + 14, true);
      if (channels !== entry.channels || rate !== entry.rate_hz || bits !== entry.bit_depth ||
          alignment !== channels * bits / 8 || bytesPerSecond !== rate * alignment ||
          !(encoding === 1 || encoding === 3 && bits === 32)) throw new Error("Impulse WAV dimensions differ from manifest");
    } else if (tag(offset) === "data") {
      if (dataSeen) throw new Error("Duplicate impulse WAV data chunk");
      dataSeen = true;
      pcmOffset = start;
      pcmBytes = length;
    }
    offset = start + length + (length & 1);
  }
  const width = entry.bit_depth / 8;
  if (!formatSeen || !dataSeen || pcmBytes !== entry.frames * entry.channels * width) {
    throw new Error("Impulse WAV frame count differs from manifest");
  }
  const left = new Float32Array(entry.frames);
  const right = entry.channels === 2 ? new Float32Array(entry.frames) : null;
  let offset = pcmOffset;
  for (let frame = 0; frame < entry.frames; frame++) {
    for (let channel = 0; channel < entry.channels; channel++, offset += width) {
      let sample;
      if (encoding === 3) sample = view.getFloat32(offset, true);
      else if (width === 2) sample = view.getInt16(offset, true) / 32768;
      else if (width === 3) sample = ((data[offset] | data[offset + 1] << 8 | data[offset + 2] << 16) << 8 >> 8) / 8388608;
      else sample = view.getInt32(offset, true) / 2147483648;
      if (!Number.isFinite(sample)) throw new Error("Impulse contains non-finite PCM");
      (channel === 0 ? left : right)[frame] = sample;
    }
  }
  if (targetRate === entry.rate_hz) return { left, right, rateHz: targetRate };
  const frames = Math.ceil(entry.frames * targetRate / entry.rate_hz);
  return { left: resample(left, entry.rate_hz, targetRate, frames),
    right: right && resample(right, entry.rate_hz, targetRate, frames), rateHz: targetRate };
}

// Selection is explicit: loading the manifest alone fetches no audio. Transfer
// returned Float32Array buffers to the audio owner after preparation finishes.
export async function loadImpulse(entry, { targetRate = 48000, signal, fetcher = globalThis.fetch } = {}) {
  validateEntry(entry);
  if (!Number.isInteger(targetRate) || targetRate < 8000 || targetRate > 192000 ||
      Math.ceil(entry.frames * targetRate / entry.rate_hz) > MAX_FRAMES) throw new Error("Invalid impulse target rate or frame bound");
  const response = await fetcher(entry.url, { signal });
  return decode(await verifiedBytes(response, entry), entry, targetRate);
}

// Optional host preparation mirrors Go Impulse.Condition. It preserves the
// loaded PCM and applies one attenuation gain to both impulse channels.
export function conditionImpulse(impulse, { highpassHz = 20, energyLimit = 1 } = {}) {
  if (!impulse || !Number.isInteger(impulse.rateHz) || impulse.rateHz < 8000 || impulse.rateHz > 192000 ||
      !(impulse.left instanceof Float32Array) || impulse.left.length === 0 ||
      impulse.right && (!(impulse.right instanceof Float32Array) || impulse.right.length !== impulse.left.length) ||
      !Number.isFinite(highpassHz) || highpassHz < 0 || highpassHz > 1000 ||
      !Number.isFinite(energyLimit) || energyLimit < 0) throw new Error("Invalid impulse conditioning controls or PCM");
  const extra = highpassHz > 0 ? Math.ceil(8 * impulse.rateHz / (2 * Math.PI * highpassHz)) : 0;
  const frames = impulse.left.length + extra;
  if (frames > MAX_FRAMES || frames > impulse.rateHz * 12) throw new Error("Conditioned impulse exceeds frame bound");
  const left = new Float32Array(frames);
  const right = impulse.right ? new Float32Array(frames) : null;
  const alpha = Math.exp(-2 * Math.PI * highpassHz / impulse.rateHz);
  let maximumEnergy = 0;
  for (const [source, output] of [[impulse.left, left], [impulse.right, right]]) {
    if (!source) continue;
    let previousInput = 0, previousOutput = 0, energy = 0;
    for (let i = 0; i < frames; i++) {
      const x = i < source.length ? source[i] : 0;
      if (!Number.isFinite(x)) throw new Error("Impulse conditioning contains non-finite PCM");
      const y = highpassHz > 0 ? alpha * (previousOutput + x - previousInput) : x;
      previousInput = x;
      previousOutput = y;
      output[i] = y;
      if (!Number.isFinite(output[i])) throw new Error("Impulse conditioning exceeds float32 range");
      energy += y * y;
    }
    maximumEnergy = Math.max(maximumEnergy, energy);
  }
  if (energyLimit > 0 && maximumEnergy > energyLimit * energyLimit) {
    const gain = Math.fround(energyLimit / Math.sqrt(maximumEnergy));
    for (const channel of [left, right]) {
      if (channel) for (let i = 0; i < frames; i++) channel[i] *= gain;
    }
  }
  return { left, right, rateHz: impulse.rateHz };
}
