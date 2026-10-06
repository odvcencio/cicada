// Measure actual production-kernel callbacks with images from kit-images.go.
// Node V8 timing includes the shared mixer and limiter, not only the DSP voice.
const fs = require('node:fs');
const path = require('node:path');
const { performance } = require('node:perf_hooks');

async function run(bytes, directory, probe) {
  const { instance } = await WebAssembly.instantiate(bytes);
  const x = instance.exports;
  x._initialize();
  const image = fs.readFileSync(path.join(directory, probe.file));
  const ptr = x.gosx_audio_project_alloc(image.length);
  if (!ptr) throw Error('project allocation rejected');
  new Uint8Array(x.memory.buffer, ptr, image.length).set(image);
  if (x.gosx_audio_init(48000, 128, 2)) throw Error('project init rejected');
  const command = new DataView(x.memory.buffer, x.gosx_audio_cmd_ptr(), probe.strikes.length * 24);
  const audio = new Float32Array(x.memory.buffer,x.gosx_audio_out_ptr(),256);
  const messages = new Uint8Array(x.memory.buffer,x.gosx_audio_msg_ptr(),256*16);
  const initialMemory = x.memory.buffer;
  const strike = () => {
    for (let i = 0; i < probe.strikes.length; i++) {
      const s = probe.strikes[i], offset = i * 24;
      command.setUint8(offset, 12); command.setUint8(offset+1, s.track);
      command.setUint16(offset+2, s.lane, true);
      command.setUint32(offset+4, s.note | (s.velocity << 8), true);
      command.setUint32(offset+8, 0, true); command.setUint32(offset+12, 0, true);
      command.setBigInt64(offset+16, 0n, true);
    }
    x.gosx_audio_cmd_commit(probe.strikes.length);
  };
  const measured = 8192, warm = 512, times = new Float64Array(measured);
  const cpuBatches = new Float64Array(measured/32);
  let batchIndex = 0;
  const cpuBatchBlocks = 32;
  let cpuBatchStart;
  let memory = 0, faults = 0, peak = 0, nonfinite = 0;
  for (let i = 0; i < warm+measured; i++) {
    if (i % cpuBatchBlocks === 0) cpuBatchStart = process.cpuUsage();
    // Sustained pressure includes repeated excitation and one active tail per
    // lane. Actual kit hat choking remains enabled in the full-kit probe.
    if (i % 32 === 0) strike();
    const start = performance.now();
    x.gosx_audio_render(128);
    const elapsed = (performance.now()-start)*1000;
    if (i >= warm) times[i-warm]=elapsed;
    const count = x.gosx_audio_msg_drain();
    for (let j=0;j<count;j++) if (messages[j*16]===7) faults++;
    if (x.memory.buffer!==initialMemory) throw Error('WASM memory grew during timing');
    for (let frame=0;frame<audio.length;frame++) {
      const value=audio[frame];
      if (!Number.isFinite(value)) nonfinite++;
      peak = Math.max(peak,Math.abs(value));
    }
    if (i===warm-1) memory=x.memory.buffer.byteLength;
    if ((i+1) % cpuBatchBlocks === 0 && i >= warm) {
      const batch = process.cpuUsage(cpuBatchStart);
      cpuBatches[batchIndex++]=(batch.user+batch.system)/cpuBatchBlocks;
    }
  }
  times.sort((a,b)=>a-b);
  cpuBatches.sort((a,b)=>a-b);
  const at=p=>times[Math.ceil(times.length*p)-1];
  const batchAt=p=>cpuBatches[Math.ceil(cpuBatches.length*p)-1];
  const budget=probe.name==='full-kit'?670:266.7;
  const report={name:probe.name,runtime:'Node V8 WebAssembly',node:process.version,
    rate_hz:48000,block_frames:128,measured_blocks:measured,
    triggered_articulations:probe.active_voices,
    median_us:at(.5),p99_us:at(.99),max_us:times.at(-1),
    cpu_batch_blocks:cpuBatchBlocks,median_batch_cpu_us_per_callback:batchAt(.5),
    p99_batch_cpu_us_per_callback:batchAt(.99),max_batch_cpu_us_per_callback:cpuBatches.at(-1),
    batch_cpu_measurement:'32 callback process CPU average; includes all harness timing, output and message checks; excludes other processes',
    median_us_per_triggered_articulation:at(.5)/probe.active_voices,
    p99_budget_us:budget,p99_budget_pass:at(.99)<budget,
    p99_batch_cpu_budget_pass:batchAt(.99)<budget,
    faults,nonfinite,peak,memory_growth_bytes:x.memory.buffer.byteLength-memory};
  if (faults || nonfinite || peak===0 || report.memory_growth_bytes) throw Error(JSON.stringify(report));
  return report;
}
async function main() {
  const [wasm,directory,output]=process.argv.slice(2);
  if (!wasm || !directory) throw Error('usage: node kit-cpu.cjs kernel.wasm image-directory [report.json]');
  const probes=JSON.parse(fs.readFileSync(path.join(directory,'images.json')));
  const bytes=fs.readFileSync(wasm),reports=[];
  for (const probe of probes) {
    const report=await run(bytes,directory,probe);reports.push(report);
    console.log(JSON.stringify(report));
  }
  if (output) fs.writeFileSync(output,JSON.stringify(reports,null,2)+'\n');
  if (reports.some(r=>!r.p99_budget_pass || !r.p99_batch_cpu_budget_pass)) process.exitCode=1;
}
main().catch(error=>{console.error(error);process.exitCode=1;});
