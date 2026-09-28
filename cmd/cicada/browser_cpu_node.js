const fs = require('node:fs');

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
  const messages = new Uint8Array(x.memory.buffer, x.gosx_audio_msg_ptr(), 256 * 16);
  const samples = new Float32Array(blockCount);
  const initialMemoryBytes = x.memory.buffer.byteLength;
  let warmMemoryBytes = initialMemoryBytes;
  let faults = 0;
  let blocksOverDeadlineCpuTime = 0;
  const warmupBlocks = Math.min(256, blockCount);

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
    samples[block] = (elapsed.user + elapsed.system) / 1000;
    if (samples[block] > 128 * 1000 / 48000) blocksOverDeadlineCpuTime++;
    if (block + 1 === warmupBlocks) warmMemoryBytes = x.memory.buffer.byteLength;
  }

  const measured = samples.slice(warmupBlocks).sort();
  const percentile = p => measured[Math.min(measured.length - 1, Math.floor((measured.length - 1) * p))] || 0;
  const report = {
    engine: 'Node V8 WebAssembly',
    clock: 'process.cpuUsage() user+system milliseconds',
    blockFrames: 128,
    blocks: blockCount,
    warmupBlocks,
    measuredBlocks: measured.length,
    p50: percentile(0.50),
    p95: percentile(0.95),
    p99: percentile(0.99),
    max: measured[measured.length - 1] || 0,
    blocksOverDeadlineCpuTime,
    faults,
    initialMemoryBytes,
    warmMemoryBytes,
    finalMemoryBytes: x.memory.buffer.byteLength,
    memoryGrowthAfterWarmupBytes: x.memory.buffer.byteLength - warmMemoryBytes,
    outputChecksum: copiedLeft[127] + copiedRight[127]
  };
  process.stdout.write(`${JSON.stringify(report)}\n`);
}

main().catch(error => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
