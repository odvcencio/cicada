// Compare two kernel builds on the same score, interleaving their callbacks.
// Timing uses the same user CPU clock as the browser budget's Node fallback.
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';

const [baselinePath, candidatePath, imagePath, blocksText = '11264'] = process.argv.slice(2);
const blocks = Number(blocksText), warmupBlocks = 256;
if (!baselinePath || !candidatePath || !imagePath || !Number.isInteger(blocks) || blocks <= warmupBlocks) {
  throw Error('usage: node tools/audio/kernel-cpu-compare.mjs baseline.wasm candidate.wasm score.image [blocks>256]');
}
const image = readFileSync(imagePath);
async function prepare(path) {
  const binary = readFileSync(path);
  const x = (await WebAssembly.instantiate(await WebAssembly.compile(binary))).exports;
  x._initialize();
  const pointer = x.gosx_audio_project_alloc(image.length);
  if (!pointer) throw Error('project allocation failed');
  new Uint8Array(x.memory.buffer, pointer, image.length).set(image);
  if (x.gosx_audio_init(48000, 128, 2) !== 0) throw Error('project initialization failed');
  const commands = new Uint8Array(x.memory.buffer, x.gosx_audio_cmd_ptr(), 48);
  commands[0] = 3; commands[1] = 255; commands[24] = 1; commands[25] = 255;
  x.gosx_audio_cmd_commit(2);
  return {
    x, binaryBytes: binary.length, binarySHA256: createHash('sha256').update(binary).digest('hex'),
    output: Buffer.from(x.memory.buffer, x.gosx_audio_out_ptr(), 256 * 4),
    messages: new Uint8Array(x.memory.buffer, x.gosx_audio_msg_ptr(), 256 * 16),
    times: new Float64Array(blocks - warmupBlocks), hash: createHash('sha256'),
    memoryBytes: x.memory.buffer.byteLength, allocationBytes: x.gosx_audio_alloc_bytes(), faults: 0
  };
}
const engines = [await prepare(baselinePath), await prepare(candidatePath)];
for (let block = 0; block < blocks; block++) {
  for (let turn = 0; turn < 2; turn++) {
    const e = engines[(block + turn) & 1];
    const started = process.cpuUsage();
    e.x.gosx_audio_render(128);
    const count = e.x.gosx_audio_msg_drain();
    const elapsed = process.cpuUsage(started).user / 1000;
    if (block >= warmupBlocks) e.times[block - warmupBlocks] = elapsed;
    for (let i = 0; i < count; i++) if (e.messages[i * 16] === 7) e.faults++;
    if (e.x.memory.buffer.byteLength !== e.memoryBytes) throw Error('render grew WASM memory');
    e.hash.update(e.output);
  }
  if (!engines[0].output.equals(engines[1].output)) throw Error(`PCM32 differs at block ${block}`);
}
function report(e) {
  e.times.sort();
  const at = p => e.times[Math.floor((e.times.length - 1) * p)];
  return {
    p50: at(.5), p95: at(.95), p99: at(.99), max: e.times.at(-1), faults: e.faults,
    rawBytes: e.binaryBytes, wasmSHA256: e.binarySHA256, pcmSHA256: e.hash.digest('hex'),
    memoryBytes: e.memoryBytes, renderAllocationBytes: Number(e.x.gosx_audio_alloc_bytes() - e.allocationBytes)
  };
}
const [baseline, candidate] = engines.map(report);
const reductionPercent = (1 - candidate.p99 / baseline.p99) * 100;
console.log(JSON.stringify({
  engine: 'Node V8 WebAssembly', nodeVersion: process.version,
  clock: 'process.cpuUsage() user milliseconds', sampleRate: 48000, blockFrames: 128,
  blocks, warmupBlocks, measuredBlocks: blocks - warmupBlocks,
  order: 'alternate baseline/candidate first each block', imageSHA256: createHash('sha256').update(image).digest('hex'),
  baseline, candidate, p99ReductionPercent: reductionPercent,
  pcm32Parity: baseline.pcmSHA256 === candidate.pcmSHA256,
  targetMet: reductionPercent >= 25,
  qualification: 'Offline V8 CPU measurement; AudioWorklet and device deadlines require browser qualification.'
}, null, 2));
if (engines.some(e => e.faults) || baseline.renderAllocationBytes || candidate.renderAllocationBytes) {
  throw Error('render faulted or allocated');
}
