const test = require('node:test');
const assert = require('node:assert/strict');
const {createHistoryBuffer, targetBand, targetDeviation, targetLufs} = require('./studio-master.js');

test('target deviation is measured from target in LU', () => {
  assert.equal(targetDeviation(-13.4, -14), 0.5999999999999996);
  assert.equal(targetDeviation(-16, -16), 0);
  assert.equal(targetDeviation(null, -14), null);
  assert.equal(targetDeviation(-20, Infinity), null);
});

test('built-in and custom targets resolve to LUFS values', () => {
  assert.equal(targetLufs('streaming', 0), -14);
  assert.equal(targetLufs('podcast', 0), -16);
  assert.equal(targetLufs('ebu-r128', 0), -23);
  assert.equal(targetLufs('custom', -19.5), -19.5);
  assert.equal(targetLufs('custom', 4), -14);
});

test('target band spans the selected target plus and minus tolerance', () => {
  assert.deepEqual(targetBand(-14, 0.5), {upper: -13.5, lower: -14.5});
  assert.deepEqual(targetBand(-59, 2), {upper: -57, lower: -60});
  assert.deepEqual(targetBand(-14, 0), {upper: -14, lower: -14});
});

test('history buffer retains only its fixed capacity in timestamp order', () => {
  const history = createHistoryBuffer(3);
  history.push(0, -30);
  history.push(100, -29);
  history.push(200, null);
  history.push(300, -27);
  assert.equal(history.length, 3);
  const values = [];
  history.forEachSince(300, 250, (time, value) => values.push([time, value]));
  assert.equal(values.length, 3);
  assert.equal(values[0][0], 100);
  assert.equal(values[0][1], -29);
  assert.equal(values[1][0], 200);
  assert.equal(Number.isNaN(values[1][1]), true);
  assert.deepEqual(values[2], [300, -27]);
  history.clear();
  assert.equal(history.length, 0);
});
