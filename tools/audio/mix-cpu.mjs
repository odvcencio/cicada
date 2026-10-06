// Measure the optional TinyGo mix companion in Node V8 at 48 kHz/128 frames.
// Timing covers the WASM process call, excluding JS input copy and inspection.
import fs from 'node:fs';
import { performance } from 'node:perf_hooks';

const presets = ['eq', 'compressor', 'transient', 'width', 'reverb', 'limiter', 'mastering'];
const rate = 48000, frames = 128, warmup = 1024, blocks = 8192;

async function measure(module, name, preset) {
  const instance = await WebAssembly.instantiate(module);
  const x = instance.exports;
  x._initialize();
  if (x.cicada_mix_init(rate, preset) !== 0) throw Error(`init rejected ${name}`);
  const pointer = x.cicada_mix_pcm_ptr();
  let pcm = new Float32Array(x.memory.buffer, pointer, 256);
  const input = new Float32Array(256);
  const times = new Float64Array(blocks);
  let initialMemory = 0, faults = 0, peak = 0;
  for (let block = 0; block < warmup + blocks; block++) {
    for (let i = 0; i < frames; i++) {
      const frame = block * frames + i;
      const gain = frame % 641 > 210 && frame % 641 < 350 ? 8 : 1;
      const value = gain * (.2 * Math.sin(2 * Math.PI * 997 * frame / rate) +
        .08 * Math.sin(2 * Math.PI * 6301 * frame / rate));
      input[i] = value;
      input[128 + i] = -value * .47;
    }
    pcm.set(input);
    const start = performance.now();
    const status = x.cicada_mix_process(frames);
    const elapsed = (performance.now() - start) * 1000;
    if (block >= warmup) times[block - warmup] = elapsed;
    if (status !== 0) faults++;
    if (pcm.buffer !== x.memory.buffer) pcm = new Float32Array(x.memory.buffer, pointer, 256);
    for (const value of pcm) {
      if (!Number.isFinite(value)) faults++;
      peak = Math.max(peak, Math.abs(value));
    }
    if (block === warmup - 1) initialMemory = x.memory.buffer.byteLength;
  }
  times.sort();
  const percentile = quantile => times[Math.floor((times.length - 1) * quantile)];
  const report = {
    preset: name, runtime: 'Node V8 WebAssembly', rate, frames, blocks,
    latency_frames: x.cicada_mix_latency(),
    median_us: percentile(.5), p99_us: percentile(.99), max_us: times.at(-1),
    median_ns_per_frame: percentile(.5) * 1000 / frames,
    p99_callback_percent: percentile(.99) / (frames / rate * 1e6) * 100,
    candidate_cpu_target_met: percentile(.99) <= (frames / rate * 1e6) * .1,
    peak, faults, memory_bytes: x.memory.buffer.byteLength,
    memory_growth_bytes: x.memory.buffer.byteLength - initialMemory,
  };
  if (faults || peak === 0 || report.memory_growth_bytes) throw Error(JSON.stringify(report));
  pcm.fill(0);
  pcm[0] = NaN;
  if (x.cicada_mix_process(frames) !== -1) throw Error(`fault input accepted by ${name}`);
  x.cicada_mix_reset();
  pcm.fill(0);
  if (x.cicada_mix_process(frames) !== 0 || pcm.some(value => value !== 0))
    throw Error(`fault reset failed for ${name}`);
  return report;
}

const path = process.argv[2];
if (!path) throw Error('usage: node tools/audio/mix-cpu.mjs mix.wasm');
const module = await WebAssembly.compile(fs.readFileSync(path));
for (const [preset, name] of presets.entries()) console.log(JSON.stringify(await measure(module, name, preset)));
