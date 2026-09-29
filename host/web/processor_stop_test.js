'use strict';
// Regression test: edit while playing, then Stop before the next bar, then Play.
// The staged instance must render right after the stop; the old one must not.
const fs = require('node:fs');
const path = require('node:path');

const [wasmPath, imagePath] = process.argv.slice(2);
if (!wasmPath || !imagePath) throw new Error('usage: node processor_stop_test.js kernel.wasm kernel.image');

let processorType, port, readyResolve, stageComplete = false;
const ready = new Promise(resolve => { readyResolve = resolve; });
const renders = [];
globalThis.sampleRate = 48000;
globalThis.registerProcessor = (_, type) => { processorType = type; };
globalThis.AudioWorkletProcessor = class {
  constructor() {
    this.port = port = {
      onmessage: null,
      postMessage(message) {
        if (message.t === 'r') readyResolve(message);
        if (message.t === 't') stageComplete = true;
      }
    };
  }
};
const instantiate = WebAssembly.instantiate.bind(WebAssembly);
WebAssembly.instantiate = async (...args) => {
  const result = await instantiate(...args);
  if (!(result instanceof WebAssembly.Instance)) return result;
  const id = renders.push(0) - 1;
  const exports = { ...result.exports };
  const render = exports.gosx_audio_render;
  exports.gosx_audio_render = (...a) => { renders[id]++; return render(...a); };
  return { exports };
};

eval(fs.readFileSync(path.resolve(__dirname, 'processor.min.js'), 'utf8'));
if (!processorType) throw new Error('the shipped minified asset did not register a processor');

const command = (...ops) => {
  const bytes = new Uint8Array(24 * ops.length);
  ops.forEach((op, i) => { bytes[i * 24] = op; bytes[i * 24 + 1] = 255; });
  port.onmessage({ data: { t: 'c', bytes } });
};

async function main() {
  const module = await WebAssembly.compile(fs.readFileSync(wasmPath));
  const bytes = fs.readFileSync(imagePath);
  const image = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
  const processor = new processorType({ processorOptions: { m: module, i: image, r: 'stop-test', l: 10 } });
  await Promise.race([ready, new Promise((_, reject) => setTimeout(() => reject(new Error('processor did not become ready')), 10000))]);
  const output = [[new Float32Array(128), new Float32Array(128)]];
  const render = () => processor.process([], output);
  command(3, 1); // seek to the start, then play
  for (let i = 0; i < 50; i++) render();
  port.onmessage({ data: { t: 'i', i: image, r: 'stage' } });
  for (let i = 0; i < 2000 && !stageComplete; i++) { await new Promise(resolve => setImmediate(resolve)); render(); }
  if (!stageComplete) throw new Error('staging did not finish');
  const oldBefore = renders[0], newBefore = renders[1] ?? 0;
  command(2); // stop before the next bar line
  command(1); // play again
  for (let i = 0; i < 100; i++) render();
  const oldAfter = renders[0] - oldBefore, newAfter = renders[1] - newBefore;
  process.stdout.write(JSON.stringify({ oldInstanceRenders: oldAfter, newInstanceRenders: newAfter }) + '\n');
  if (newAfter < 100) throw new Error(`the staged instance rendered ${newAfter} of 100 callbacks after Stop and Play`);
  if (oldAfter > 0) throw new Error(`the old instance still rendered ${oldAfter} callbacks after Stop and Play`);
}
setTimeout(() => { process.stderr.write('timeout: the test did not finish\n'); process.exit(1); }, 60000).unref();
main().catch(error => { process.stderr.write(`${error.stack || error}\n`); process.exitCode = 1; });
