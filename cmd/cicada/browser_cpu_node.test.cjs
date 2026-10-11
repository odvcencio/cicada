'use strict';
const assert = require('node:assert/strict');
const {test} = require('node:test');
const {calibrateCPUClockResolutionMs} = require('./browser_cpu_node.js');

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
