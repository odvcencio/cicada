'use strict';
const assert = require('node:assert/strict');
const {test} = require('node:test');
const {calibrateCPUClockResolutionMs, excessBlocks} = require('./browser_cpu_node.js');

for (const {name, samples, expected} of [
  {name: 'keeps microsecond resolution and ignores zero deltas', samples: [0, 0, 4, 5, 5, 9], expected: 0.001},
  {name: 'records an observed millisecond clock', samples: [0, 2000, 2000, 3000], expected: 1},
  {name: 'reports unavailable resolution when no clock tick is observed', samples: [5, 5, 5, 5], expected: null}
]) {
  test(name, () => {
    let index = 0;
    const cpuUsage = () => ({user: samples[index++], system: 1000000 * index});
    assert.equal(calibrateCPUClockResolutionMs(cpuUsage, samples.length - 1), expected);
    assert.equal(index, samples.length);
  });
}

test('counts the blocks more than 0.9 ms above the median and keeps their indices', () => {
  // Blocks 2 and 4 each took a whole scheduler tick (1 ms and 4 ms) of foreign CPU time.
  const blocks = Float32Array.from([0.25, 0.25, 1.25, 0.25, 4.25, 0.25, 0.25, 0.25, 0.25, 0.25]);
  const result = excessBlocks(blocks, 0.25);
  assert.equal(result.count, 2);
  assert.equal(result.share, 0.2);
  assert.deepEqual(result.first, [2, 4]);
  assert.equal(result.thresholdMs, 0.25 + 0.9);
});

test('a block exactly at the threshold is not excess', () => {
  const result = excessBlocks(Float32Array.from([2.5, 2.75]), 1, 1.5);
  assert.equal(result.count, 1);
  assert.deepEqual(result.first, [1]);
});

test('counts every excess block but keeps only the first indices', () => {
  const blocks = new Float32Array(100).fill(0.25);
  for (let index = 10; index < 50; index++) blocks[index] = 5;
  const result = excessBlocks(blocks, 0.25, 0.9, 16);
  assert.equal(result.count, 40);
  assert.equal(result.share, 0.4);
  assert.deepEqual(result.first, Array.from({length: 16}, (_, i) => 10 + i));
});

test('a series with no slow block, or no block at all, has no excess', () => {
  assert.deepEqual(excessBlocks(Float32Array.from([0.25, 0.25]), 0.25), {thresholdMs: 1.15, count: 0, share: 0, first: []});
  assert.deepEqual(excessBlocks(new Float32Array(0), 0), {thresholdMs: 0.9, count: 0, share: 0, first: []});
});
