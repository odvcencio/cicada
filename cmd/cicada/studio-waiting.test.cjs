const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const test = require('node:test');

const page = fs.readFileSync(__dirname + '/studio_waiting.go', 'utf8');
const script = page.match(/<script>([\s\S]*?)<\/script>/)[1];

test('the invalid startup page waits for a successful valid score response', async () => {
  let poll, response, reloads = 0;
  vm.runInNewContext(script, {
    setInterval(callback) { poll = callback; },
    async fetch(path) {
      assert.equal(path, '/api/state');
      if (response instanceof Error) throw response;
      return {ok: response.ok, async json() { return response; }};
    },
    location: {reload() { reloads++; }},
  });
  for (response of [{ok: true, valid: false}, {ok: false, valid: true}, new Error('offline')]) {
    await poll();
    assert.equal(reloads, 0);
  }
  response = {ok: true, valid: true};
  await poll();
  assert.equal(reloads, 1);
});
