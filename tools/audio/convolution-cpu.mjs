// Offline V8 measurement of the separately loaded convolution WASM module.
// Usage: node tools/audio/convolution-cpu.mjs <wasm> <asset-pack> [callbacks]
import { readFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import { performance } from "node:perf_hooks";
import { webcrypto } from "node:crypto";
import { loadImpulse } from "../../host/irasset/loader.mjs";

globalThis.crypto ??= webcrypto;
const [wasmPath, packPath, callbacksText = "512"] = process.argv.slice(2);
if (!wasmPath || !packPath) throw new Error("Provide a WASM module and fetched impulse pack");
const callbacks = Number(callbacksText);
if (!Number.isInteger(callbacks) || callbacks < 128 || callbacks > 16384) throw new Error("Callbacks must be 128 through 16384");
const binary = await readFile(resolve(wasmPath));
const module = await WebAssembly.compile(binary);
const manifest = JSON.parse(await readFile(join(packPath, "manifest.json")));
if (manifest.version !== 1 || manifest.assets.length !== 4) throw new Error("Expected the four-response impulse pack");

async function checkDirectConvolution() {
  const instance = await WebAssembly.instantiate(module);
  const e = instance.exports;
  e._initialize();
  const irFrames = 9003, inputFrames = 197, partition = 128;
  const left = Float32Array.from({ length: irFrames }, (_, i) => .02 * Math.cos(i * .37));
  const right = Float32Array.from(left, x => -.7 * x);
  const inputL = Float32Array.from({ length: inputFrames }, (_, i) => .2 * Math.sin(i * .29));
  const inputR = Float32Array.from(inputL, x => -.4 * x);
  const ptr = e.cicada_ir_alloc(irFrames);
  new Float32Array(e.memory.buffer, ptr, irFrames).set(left);
  new Float32Array(e.memory.buffer, e.cicada_ir_right_ptr(), irFrames).set(right);
  if (e.cicada_ir_init(48000, partition) !== 0) throw new Error("WASM parity preparation failed");
  const pcm = new Float32Array(e.memory.buffer, e.cicada_ir_pcm_ptr(), 256);
  const frames = inputFrames + irFrames - 1 + partition + 16;
  let maximumError = 0;
  for (let base = 0; base < frames; base += 128) {
    const count = Math.min(128, frames - base);
    pcm.fill(0);
    for (let i = 0; i < count && base + i < inputFrames; i++) {
      pcm[i] = inputL[base + i];
      pcm[128 + i] = inputR[base + i];
    }
    if (e.cicada_ir_process(count) !== 0) throw new Error("WASM parity render failed");
    for (let i = 0; i < count; i++) {
      const frame = base + i - partition;
      for (let channel = 0; channel < 2; channel++) {
        const input = channel === 0 ? inputL : inputR;
        const impulse = channel === 0 ? left : right;
        let expected = 0;
        for (let j = 0; j < inputFrames; j++) {
          const tap = frame - j;
          if (tap >= 0 && tap < irFrames) expected += input[j] * impulse[tap];
        }
        maximumError = Math.max(maximumError, Math.abs(pcm[channel * 128 + i] - expected));
      }
    }
  }
  if (maximumError > 1e-5) throw new Error(`WASM direct convolution error ${maximumError}`);
  e.cicada_ir_reset();
  pcm.fill(0);
  if (e.cicada_ir_process(128) !== 0 || pcm.some(x => x !== 0)) throw new Error("WASM reset silence failed");
  return maximumError;
}

const parityError = await checkDirectConvolution();
console.log(JSON.stringify({ runtime: "Node V8", node_version: process.version, wasm_bytes: binary.length,
  callbacks, frames_per_callback: 128, sample_rate: 48000,
  direct_convolution_max_error: parityError, reset_silence: "exact zero",
  design: "Nonuniform staged tail for head partitions <=512; uniform for 2048",
  deadline_us: 128 / 48000 * 1e6, candidate_cpu_us: 128 / 48000 * 1e5,
  qualification: "Offline V8 timing; AudioWorklet and device deadline qualification remain separate" }));

for (const entry of manifest.assets) {
  const wav = await readFile(join(packPath, entry.id + ".wav"));
  const impulse = await loadImpulse(entry, { fetcher: async () => new Response(wav) });
  for (const partition of [128, 512, 2048]) {
    const instance = await WebAssembly.instantiate(module);
    const e = instance.exports;
    e._initialize();
    const leftPtr = e.cicada_ir_alloc(impulse.left.length);
    if (!leftPtr) throw new Error("WASM impulse allocation failed");
    const rightPtr = e.cicada_ir_right_ptr();
    new Float32Array(e.memory.buffer, leftPtr, impulse.left.length).set(impulse.left);
    new Float32Array(e.memory.buffer, rightPtr, impulse.left.length).set(impulse.right ?? impulse.left);
    if (e.cicada_ir_init(48000, partition) !== 0) throw new Error("WASM impulse preparation failed");
    const pcm = new Float32Array(e.memory.buffer, e.cicada_ir_pcm_ptr(), 256);
    const memory = e.memory.buffer.byteLength;
    const times = [];
    // Warmup runs every partition boundary repeatedly, including large ones.
    for (let i = 0; i < 128; i++) {
      pcm.fill(.01);
      if (e.cicada_ir_process(128) !== 0) throw new Error("WASM convolution fault during warmup");
    }
    for (let i = 0; i < callbacks; i++) {
      pcm.fill(.01);
      const start = performance.now();
      const status = e.cicada_ir_process(128);
      const elapsed = (performance.now() - start) * 1000;
      if (status !== 0) throw new Error("WASM convolution fault during measurement");
      if (e.memory.buffer.byteLength !== memory) throw new Error("WASM memory grew during callback");
      times.push(elapsed);
    }
    times.sort((a, b) => a - b);
    const quantile = p => times[Math.min(times.length - 1, Math.floor(times.length * p))];
    const mean = times.reduce((a, b) => a + b, 0) / times.length;
    console.log(JSON.stringify({ asset: entry.id, impulse_frames: impulse.left.length, partition_frames: partition,
      latency_frames: e.cicada_ir_latency(), latency_ms: e.cicada_ir_latency() / 48,
      mean_us: mean, median_us: quantile(.5), p99_us: quantile(.99), max_us: times.at(-1),
      ns_per_sample: mean * 1000 / 128, callbacks, prepared_memory_bytes: memory, callback_memory_growth_bytes: 0 }));
  }
}
