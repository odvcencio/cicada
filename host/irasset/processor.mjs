// Supply a verified impulse from loader.mjs and a precompiled companion module.
// Run preparation outside the AudioWorklet process callback.
export async function prepareConvolution(module, impulse, partitionFrames=128) {
  const instance = await WebAssembly.instantiate(module);
  const x = instance.exports;
  x._initialize();
  const n = impulse.left.length;
  const ptr = x.cicada_ir_alloc(n);
  if (!ptr) throw Error('convolution kernel rejected impulse');
  new Float32Array(x.memory.buffer, ptr, n).set(impulse.left);
  new Float32Array(x.memory.buffer, x.cicada_ir_right_ptr(), n).set(impulse.right || impulse.left);
  if (x.cicada_ir_init(impulse.rateHz, partitionFrames) !== 0) throw Error('convolution kernel rejected configuration');
  const pcm = new Float32Array(x.memory.buffer, x.cicada_ir_pcm_ptr(), 256);
  const memory = x.memory.buffer;
  return {
    latencyFrames: x.cicada_ir_latency(),
    reset() { x.cicada_ir_reset(); },
    process(left, right) {
      if (left.length !== right.length || left.length < 1 || left.length > 128 || x.memory.buffer !== memory) {
        left.fill(0); right.fill(0); return false;
      }
      for (let i=0;i<left.length;i++) { pcm[i]=left[i];pcm[128+i]=right[i]; }
      if (x.cicada_ir_process(left.length) !== 0) {left.fill(0);right.fill(0);return false;}
      for (let i=0;i<left.length;i++) {left[i]=pcm[i];right[i]=pcm[128+i];}
      return true;
    }
  };
}
