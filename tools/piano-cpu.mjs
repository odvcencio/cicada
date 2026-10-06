// Measure the piano DSP fixture in V8; includes shared strings and soundboard.
// Build kernel/voice/piano/testdata/wasm with the kernel's TinyGo flags first.
import { readFileSync } from 'node:fs';
import { performance } from 'node:perf_hooks';

if (process.argv.length !== 3) throw new Error('usage: node tools/piano-cpu.mjs piano-fixture.wasm');
const { instance } = await WebAssembly.instantiate(readFileSync(process.argv[2]), {});
const w = instance.exports;
w._initialize();
const rows = [];
for (const voices of [0, 1, 4, 8]) {
  if (w.piano_bench_init(voices) !== 0) throw new Error('init rejected');
  for (let i = 0; i < 1000; i++) {
    if (i % 256 === 0) w.piano_bench_strike();
    w.piano_bench_render(128);
  }
  const before = w.piano_bench_alloc();
  const times = [];
  const cpuTimes = [];
  for (let i = 0; i < 6000; i++) {
    const cpuStart = process.cpuUsage();
    const start = performance.now();
    if (i % 256 === 0) w.piano_bench_strike();
    w.piano_bench_render(128);
    times.push(performance.now() - start);
    cpuTimes.push(process.cpuUsage(cpuStart).user / 1000);
  }
  const allocated = Number(w.piano_bench_alloc() - before);
  if (allocated !== 0) throw new Error(`render allocated ${allocated} bytes`);
  times.sort((a, b) => a - b);
  cpuTimes.sort((a, b) => a - b);
  const mean = times.reduce((a, b) => a + b, 0) / times.length;
  const cpuMean = cpuTimes.reduce((a, b) => a + b, 0) / cpuTimes.length;
  rows.push({ voices, mean_block_ms: mean, p50_block_ms: times[3000], p99_block_ms: times[5940], max_block_ms: times.at(-1), mean_user_cpu_block_ms: cpuMean, p99_user_cpu_block_ms: cpuTimes[5940], allocation_bytes: allocated });
}
const idle = rows[0].mean_block_ms;
const cpuIdle = rows[0].mean_user_cpu_block_ms;
for (const row of rows) {
  row.incremental_mean_ns_per_voice_sample = row.voices ? (row.mean_block_ms-idle)*1e6/(128*row.voices) : 0;
  row.incremental_mean_cpu_ns_per_voice_sample = row.voices ? (row.mean_user_cpu_block_ms-cpuIdle)*1e6/(128*row.voices) : 0;
}
console.log(JSON.stringify({ runtime: `Node ${process.version} / V8 ${process.versions.v8}`, sample_rate: 48000, block_frames: 128, blocks: 6000, shared_idle_included: true, measurement: 'DSP fixture, not AudioWorklet scheduling', rows }, null, 2));
