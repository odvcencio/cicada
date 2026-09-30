const test = require('node:test');
const assert = require('node:assert/strict');
const {faderPositionToDb, faderDbToPosition, knobValueAt, knobPositionAt, createFrameCoalescer} = require('./studio-mix.js');

test('fader law maps physical travel to dB with 0 dB at x=1', () => {
  assert.equal(faderPositionToDb(0), -60);
  assert.equal(faderPositionToDb(1), 0);
  assert.equal(faderPositionToDb(1.1), 6);
  assert.ok(Math.abs(faderPositionToDb(1.05) - 3) < 1e-12);
  assert.equal(faderDbToPosition(-30), 0.5);
});
test('logarithmic knob mapping is reversible and geometric', () => {
  const descriptor = {min:20, max:20000, curve:'log'};
  assert.equal(knobValueAt(0, descriptor), 20);
  assert.equal(knobValueAt(1, descriptor), 20000);
  const midpoint = Math.sqrt(20 * 20000);
  assert.ok(Math.abs(knobValueAt(0.5, descriptor) - midpoint) < 1e-10);
  assert.ok(Math.abs(knobPositionAt(midpoint, descriptor) - 0.5) < 1e-12);
});

test('preview coalescer flushes only the latest value once per animation frame', () => {
  const frames = [];
  const output = [];
  const coalescer = createFrameCoalescer(values => output.push([...values]), callback => frames.push(callback));
  coalescer.push('bass.level', -4);
  coalescer.push('bass.level', -3.5);
  coalescer.push('room.time', 200);
  assert.equal(frames.length, 1);
  assert.equal(output.length, 0);
  frames.shift()();
  assert.deepEqual(output, [[['bass.level', -3.5], ['room.time', 200]]]);
  assert.equal(coalescer.size, 0);
});

test('projection refresh event reloads the mixer model and re-renders its current root', async () => {
  const {installProjectionRefresh} = require('./studio-mix.js');
  const target = new EventTarget();
  const root = {id:'fresh-mix-rack'};
  let modelRevision = 1;
  let rendered = null;
  installProjectionRefresh(target, {
    getRoot:() => root,
    async refreshModel() { modelRevision += 1; },
    render(currentRoot) { rendered = {root:currentRoot, revision:modelRevision}; },
    onError(_root, error) { throw error; }
  });

  target.dispatchEvent(new Event('cicada:projectionrefreshed'));
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(rendered, {root, revision:2});
});
