// Optional companion mix module; prepare before connecting the audio graph.
// The returned process function copies samples without creating typed views.
export async function prepareMix(module, rate, preset) {
  const instance = await WebAssembly.instantiate(module);
  const x = instance.exports;
  x._initialize();
  if (x.cicada_mix_init(rate, preset) !== 0) throw Error('mix kernel rejected configuration');
  const pcm = new Float32Array(x.memory.buffer, x.cicada_mix_pcm_ptr(), 256);
  const memory = x.memory.buffer;
  return {
    latencyFrames: x.cicada_mix_latency(),
    reset() { x.cicada_mix_reset(); },
    process(left, right) {
      if (left.length !== right.length || left.length < 1 || left.length > 128 || x.memory.buffer !== memory) {
        left.fill(0); right.fill(0); return false;
      }
      for (let i = 0; i < left.length; i++) { pcm[i] = left[i]; pcm[128+i] = right[i]; }
      if (x.cicada_mix_process(left.length) !== 0) { left.fill(0); right.fill(0); return false; }
      for (let i = 0; i < left.length; i++) { left[i] = pcm[i]; right[i] = pcm[128+i]; }
      return true;
    }
  };
}
