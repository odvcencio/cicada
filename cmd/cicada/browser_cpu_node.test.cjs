'use strict';
const assert = require('node:assert/strict');
const {test} = require('node:test');
const {calibrateCPUClockResolutionMs, warmCPUClock, excessBlocks, summarizeBlocks, EXCESS_BLOCK_MS, FIRST_EXCESS_BLOCKS} = require('./browser_cpu_node.js');

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
  // Blocks 2 and 4 each took a whole scheduler tick (1 ms and 4 ms) of another thread's CPU time.
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

// Four warmup blocks that must not count, then twenty measured blocks with slow ones at
// measured indices 3, 7 and 12. Every value is exact in float32.
function seriesWithSlowBlocks() {
  const measured = new Array(20).fill(0.25);
  measured[3] = 1.25;
  measured[7] = 4.25;
  measured[12] = 2.5;
  return [9, 9, 9, 9, ...measured];
}

test('summarizes the measured blocks: warmup dropped, percentiles sorted, indices in timing order', () => {
  const summary = summarizeBlocks(seriesWithSlowBlocks(), 4);
  assert.equal(summary.measuredBlocks, 20);
  assert.equal(summary.p50, 0.25);
  assert.equal(summary.p95, 2.5);
  assert.equal(summary.p99, 2.5);
  assert.equal(summary.max, 4.25);
  assert.equal(summary.excessBlockThresholdMs, 0.25 + EXCESS_BLOCK_MS);
  assert.equal(summary.excessBlocks, 3);
  assert.equal(summary.excessBlockShare, 0.15);
  // Counted in timing order from the first measured block. Counting the sorted series
  // would give 17, 18 and 19.
  assert.deepEqual(summary.firstExcessBlocks, [3, 7, 12]);
});

test('the excess threshold comes from the median, not from a high percentile', () => {
  const summary = summarizeBlocks(seriesWithSlowBlocks(), 4);
  assert.ok(summary.p99 > summary.p50 + EXCESS_BLOCK_MS);
  assert.equal(summary.excessBlockThresholdMs, summary.p50 + EXCESS_BLOCK_MS);
  assert.equal(summary.excessBlocks, 3);
});

test('summarizes a plain array of milliseconds like a Float32Array', () => {
  assert.deepEqual(summarizeBlocks(seriesWithSlowBlocks(), 4), summarizeBlocks(Float32Array.from(seriesWithSlowBlocks()), 4));
});

test('the summary keeps every excess block in the count but only the first indices', () => {
  const blocks = new Float32Array(300).fill(0.25);
  for (let index = 100; index < 200; index++) blocks[index] = 5;
  const summary = summarizeBlocks(blocks, 0);
  assert.equal(summary.excessBlocks, 100);
  assert.equal(summary.excessBlockShare, 100 / 300);
  assert.deepEqual(summary.firstExcessBlocks, Array.from({length: FIRST_EXCESS_BLOCKS}, (_, i) => 100 + i));
});

test('a run with no measured block summarizes to zeros', () => {
  assert.deepEqual(summarizeBlocks([9, 9], 2), {
    measuredBlocks: 0, p50: 0, p95: 0, p99: 0, max: 0,
    excessBlockThresholdMs: EXCESS_BLOCK_MS, excessBlocks: 0, excessBlockShare: 0, firstExcessBlocks: []
  });
});

test('warms the clock with the two readings the timed loop makes per block', () => {
  const calls = [];
  const cpuUsage = previous => {
    calls.push(previous);
    return {user: calls.length};
  };
  assert.equal(warmCPUClock(cpuUsage, 3), 3);
  assert.equal(calls.length, 6);
  // Each pair is a reading with no argument and then a reading against that first one.
  for (let pair = 0; pair < 3; pair++) {
    assert.equal(calls[2 * pair], undefined);
    assert.deepEqual(calls[2 * pair + 1], {user: 2 * pair + 1});
  }
});
