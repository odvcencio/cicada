// Status ring: engine.MessageCapacity (512) messages of 16 bytes; a Go test keeps this equal.
const MESSAGE_BYTES = 512 * 16;
const fs = require('node:fs');

// process.cpuUsage() adds up the CPU time of every thread in the process. Linux reads
// the calling thread exactly, but credits another running thread only at a scheduler
// tick, so a block that spans a tick can pick up a whole tick (1 ms at 1000 Hz, 4 ms at
// 250 Hz) of that thread's time. The Go test therefore starts this script with
// --single-threaded --no-liftoff --no-wasm-lazy-compilation: V8 compiles the whole kernel
// before the timed loop and runs no background task. A block more than EXCESS_BLOCK_MS
// above the median shows that a tick still got in; the Go test turns too many of them into
// an inconclusive CPU verdict. Do not switch to process.threadCpuUsage(): on Linux it
// advances only once per tick (7 to 10 times in 40 ms on a 250 Hz kernel), too coarse for
// a 0.2 ms block.
const EXCESS_BLOCK_MS = 0.9;
// Keep the first few indices so a periodic cause (a bar boundary) can be told from random ticks.
const FIRST_EXCESS_BLOCKS = 16;

function calibrateCPUClockResolutionMs(cpuUsage = process.cpuUsage, sampleCount = 4096) {
  // Calibrate the user CPU clock used below, not the units in its report label.
  // cpuUsage reports microseconds; retain the smallest observed nonzero tick.
  let previous = cpuUsage().user;
  let smallest = Infinity;
  for (let sample = 0; sample < sampleCount; sample++) {
    const current = cpuUsage().user;
    const difference = current - previous;
    if (difference > 0) smallest = Math.min(smallest, difference);
    previous = current;
  }
  return Number.isFinite(smallest) ? smallest / 1000 : null;
}

// Count the blocks that ran more than excessMs above the median. blockMs is in timing
// order, so the kept indices show whether the slow blocks are periodic or random.
function excessBlocks(blockMs, medianMs, excessMs = EXCESS_BLOCK_MS, keep = FIRST_EXCESS_BLOCKS) {
  const thresholdMs = medianMs + excessMs;
  const first = [];
  let count = 0;
  for (let index = 0; index < blockMs.length; index++) {
    if (!(blockMs[index] > thresholdMs)) continue;
    count++;
    if (first.length < keep) first.push(index);
  }
  return {thresholdMs, count, share: blockMs.length ? count / blockMs.length : 0, first};
}

async function main() {
  const [wasmPath, imagePath, blockCountText] = process.argv.slice(2);
  const blockCount = Number(blockCountText);
  if (!wasmPath || !imagePath || !Number.isInteger(blockCount) || blockCount < 1) {
    throw new Error('usage: node browser_cpu_node.js kernel.wasm image.bin blocks');
  }
  const wasmModule = await WebAssembly.compile(fs.readFileSync(wasmPath));
  const instance = await WebAssembly.instantiate(wasmModule);
  const x = instance.exports;
  const image = new Uint8Array(fs.readFileSync(imagePath));
  x._initialize();
  const imagePtr = x.gosx_audio_project_alloc(image.length);
  if (!imagePtr) throw new Error('kernel rejected project image');
  new Uint8Array(x.memory.buffer, imagePtr, image.length).set(image);
  if (x.gosx_audio_init(48000, 128, 2) !== 0) throw new Error('kernel image does not match test rate');

  const commandPtr = x.gosx_audio_cmd_ptr();
  const commandBytes = new Uint8Array(x.memory.buffer, commandPtr, 48);
  commandBytes[0] = 3; commandBytes[1] = 255;
  commandBytes[24] = 1; commandBytes[25] = 255;
  x.gosx_audio_cmd_commit(2);

  const outPtr = x.gosx_audio_out_ptr();
  const left = new Float32Array(x.memory.buffer, outPtr, 128);
  const right = new Float32Array(x.memory.buffer, outPtr + 128 * 4, 128);
  const copiedLeft = new Float32Array(128), copiedRight = new Float32Array(128);
  const messages = new Uint8Array(x.memory.buffer, x.gosx_audio_msg_ptr(), MESSAGE_BYTES);
  const samples = new Float32Array(blockCount);
  const initialMemoryBytes = x.memory.buffer.byteLength;
  let warmMemoryBytes = initialMemoryBytes;
  let faults = 0;
  let blocksOverDeadlineCpuTime = 0;
  const warmupBlocks = Math.min(256, blockCount);
  const cpuClockResolutionMs = calibrateCPUClockResolutionMs();

  for (let block = 0; block < blockCount; block++) {
    const started = process.cpuUsage();
    x.gosx_audio_render(128);
    for (let frame = 0; frame < 128; frame++) {
      copiedLeft[frame] = left[frame];
      copiedRight[frame] = right[frame];
    }
    const count = x.gosx_audio_msg_drain();
    for (let message = 0; message < count; message++) if (messages[message * 16] === 7) faults++;
    const elapsed = process.cpuUsage(started);
    samples[block] = elapsed.user / 1000;
    if (samples[block] > 128 * 1000 / 48000) blocksOverDeadlineCpuTime++;
    if (block + 1 === warmupBlocks) warmMemoryBytes = x.memory.buffer.byteLength;
  }

  const inOrder = samples.subarray(warmupBlocks);
  const measured = inOrder.slice().sort();
  const percentile = p => measured[Math.min(measured.length - 1, Math.floor((measured.length - 1) * p))] || 0;
  const excess = excessBlocks(inOrder, percentile(0.50));
  const report = {
    engine: 'Node V8 WebAssembly',
    clock: 'process.cpuUsage() user milliseconds',
    cpuClockResolutionMs,
    blockFrames: 128,
    blocks: blockCount,
    warmupBlocks,
    measuredBlocks: measured.length,
    p50: percentile(0.50),
    p95: percentile(0.95),
    p99: percentile(0.99),
    max: measured[measured.length - 1] || 0,
    excessBlockThresholdMs: excess.thresholdMs,
    excessBlocks: excess.count,
    excessBlockShare: excess.share,
    firstExcessBlocks: excess.first,
    blocksOverDeadlineCpuTime,
    faults,
    initialMemoryBytes,
    warmMemoryBytes,
    finalMemoryBytes: x.memory.buffer.byteLength,
    memoryGrowthAfterWarmupBytes: x.memory.buffer.byteLength - warmMemoryBytes,
    outputChecksum: copiedLeft[127] + copiedRight[127],
    nodeVersion: process.version,
    v8Version: process.versions.v8,
    execArgv: process.execArgv
  };
  process.stdout.write(`${JSON.stringify(report)}\n`);
}

module.exports = {calibrateCPUClockResolutionMs, excessBlocks, EXCESS_BLOCK_MS};
if (require.main === module) {
  main().catch(error => {
    process.stderr.write(`${error.stack || error}\n`);
    process.exitCode = 1;
  });
}
