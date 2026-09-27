const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const test = require('node:test');

const page = fs.readFileSync(__dirname + '/view.html', 'utf8');
const source = page.match(/function isNewTransportState\(state\) \{[\s\S]*?\n  \}/)?.[0];
assert.ok(source, 'transport ordering helper exists in the score page');
const check = vm.runInNewContext('(() => { let lastTransportSequence = 0; ' + source + '; return isNewTransportState; })()');

test('older transport snapshots cannot replace newer queue state', () => {
  assert.equal(check({sequence: 4}), true);
  assert.equal(check({sequence: 7}), true);
  assert.equal(check({sequence: 5}), false);
  assert.equal(check({sequence: 8}), true);
});

test('legacy snapshots without a sequence remain accepted', () => {
  assert.equal(check({}), true);
});
