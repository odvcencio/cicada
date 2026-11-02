'use strict';
const assert = require('node:assert/strict');
const {test} = require('node:test');
const {startSoakTiming, callbackExceedanceDeltas} = require('./soak-timing.cjs');

test('demo timing report separates duration and gap events after warmup', () => {
  const before = {durationExceedances: 5, gapExceedances: 9};
  const after = {durationExceedances: 12, gapExceedances: 11};
  assert.deepEqual(callbackExceedanceDeltas(before, after), {callbackDurationExceedances: 7, callbackGapExceedances: 2});
  assert.throws(() => callbackExceedanceDeltas(before, {durationExceedances: 4, gapExceedances: 11}), /reset callback counter/);
  assert.throws(() => callbackExceedanceDeltas(before, {durationExceedances: 12}), /gapExceedances/);
});

test('demo bridge records shared host evidence and preserves correctness failures', {timeout: 30000}, async t => {
  const sampler = await startSoakTiming();
  t.after(() => sampler.close());
  const result = await sampler.finish({
    underruns: 8, faults: 1, memoryAfterWarmupBytes: 1024, memoryPeakBytes: 1024,
    playheadAdvanced: true, callbackSamples: 2000, messagesDrained: 10,
    errors: [], browserErrors: [], audioWorkletHighResClock: true, quantumMs: 128000 / 48000
  });
  assert.equal(result.gatePass, false);
  assert.equal(result.clockResolutionMs, 0.1);
  assert.equal(result.hostLoadSampleIntervalSeconds, 30);
  assert.equal(result.hostLoadSamples.length, 2);
  for (const sample of result.hostLoadSamples) {
    assert(sample.availableCPUCount > 0);
    assert(sample.sampledAt);
  }
  // Non-Linux hosts cannot read /proc/loadavg. Missing evidence must keep
  // timing inconclusive while the correctness fault still fails the gate.
  const loadUnavailable = result.hostLoadSamples.some(sample => sample.error);
  assert.equal(result.timingVerdict, result.hostBusy || loadUnavailable ? 'inconclusive' : 'fail');
  if (loadUnavailable) assert.match(result.timingReason, /incomplete load samples/);
});

test('demo sampler stops when the runner fails before its final report', {timeout: 30000}, async () => {
  const sampler = await startSoakTiming();
  await sampler.close();
});
