// CPU report for the production kernel. Run with generated one-track images.
// Timing includes the mixer/limiter; it is not an isolated model timing.
const fs = require('node:fs');
const { performance } = require('node:perf_hooks');
async function run(wasmPath, imagePath, name) {
  const { instance } = await WebAssembly.instantiate(fs.readFileSync(wasmPath));
  const x = instance.exports;
  x._initialize();
  const image = fs.readFileSync(imagePath);
  const ptr = x.gosx_audio_project_alloc(image.length);
  if (!ptr) throw Error('project allocation rejected');
  new Uint8Array(x.memory.buffer, ptr, image.length).set(image);
  if (x.gosx_audio_init(48000, 128, 2)) throw Error('project init rejected');
  const command = new DataView(x.memory.buffer, x.gosx_audio_cmd_ptr(), 24);
  const strike = () => {
    command.setUint8(0, 12); command.setUint8(1, 0);
    command.setUint16(2, 0, true); command.setUint32(4, 60 | (100 << 8), true);
    command.setUint32(8, 0, true); command.setUint32(12, 0, true);
    command.setBigInt64(16, 0n, true);
    x.gosx_audio_cmd_commit(1);
  };
  const count = 8192, warm = 512, elapsed = [];
  let memory = 0, faults = 0, peak = 0;
  for (let i = 0; i < count + warm; i++) {
    // Four overlapping strikes; every 32 blocks exercises excitation setup.
    if (i % 32 === 0) strike();
    const start = performance.now();
    x.gosx_audio_render(128);
    const time = (performance.now() - start) * 1000;
    if (i >= warm) elapsed.push(time);
    const n = x.gosx_audio_msg_drain();
    const messages = new Uint8Array(x.memory.buffer, x.gosx_audio_msg_ptr(), n * 16);
    for (let j = 0; j < n; j++) if (messages[j * 16] === 7) faults++;
    const audio = new Float32Array(x.memory.buffer, x.gosx_audio_out_ptr(), 256);
    for (const value of audio) peak = Math.max(peak, Math.abs(value));
    if (i === warm - 1) memory = x.memory.buffer.byteLength;
  }
  elapsed.sort((a,b) => a-b);
  const p = quantile => elapsed[Math.floor((elapsed.length - 1) * quantile)];
  const report = { name, runtime: 'Node V8 WebAssembly', rate: 48000, block: 128,
    blocks: count, median_us: p(.5), p99_us: p(.99), max_us: elapsed.at(-1),
    median_ns_per_sample: p(.5)*1000/128, faults, peak,
    memory_growth_bytes: x.memory.buffer.byteLength-memory };
  if (faults || peak === 0 || report.memory_growth_bytes) throw Error(JSON.stringify(report));
  return report;
}
async function main() {
  const [wasm, directory] = process.argv.slice(2);
  if (!wasm || !directory) throw Error('usage: node modal-cpu.cjs kernel.wasm image-directory');
  for (const file of fs.readdirSync(directory).filter(f => f.endsWith('.bin')).sort())
    console.log(JSON.stringify(await run(wasm, `${directory}/${file}`, file.slice(0,-4))));
}
main().catch(error => { console.error(error); process.exitCode = 1; });
