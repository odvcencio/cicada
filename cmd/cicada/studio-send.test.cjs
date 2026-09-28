const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const test = require('node:test');

const html = fs.readFileSync(process.env.STUDIO_VIEW_HTML || __dirname + '/view.html', 'utf8');
const sendSource = html.match(/  async function send\(path, payload\) \{[\s\S]*?\n  \}/)[0];

function harness() {
  const requests = [];
  let release, reject;
  const state = {
    original: 'old score', busy: false,
    editor: {value: 'old score'}, save: {disabled: false},
    document: {body: {dataset: {revision: 'old-revision'}}},
    revision() { return state.document.body.dataset.revision; },
    refreshes: 0,
    setStatus(message, kind) { state.status = {message, kind}; },
    dirty() { return state.editor.value !== state.original; },
    refreshProjection() { state.refreshes++; return Promise.resolve(); },
    showEditor() {},
    fetch(path, options) {
      requests.push({path, ...JSON.parse(options.body)});
      return new Promise((resolve, fail) => { release = resolve; reject = fail; });
    },
  };
  vm.runInNewContext(sendSource, state);
  return {
    state, requests,
    finish(ok = true, revision = 'saved-revision') {
      release({ok, async json() { return {revision, error: ok ? undefined : 'save rejected'}; }});
    },
    fail() { reject(new Error('offline')); },
  };
}

test('a normal source save refreshes the projection in place', async () => {
  const h = harness();
  h.state.editor.value = 'saved score';
  const pending = h.state.send('/api/source', {source: h.state.editor.value});
  h.finish();
  await pending;
  assert.equal(h.state.refreshes, 1);
  assert.equal(h.state.save.disabled, false);
});

test('new source edits survive and the next save uses the accepted revision', async () => {
  const h = harness();
  h.state.editor.value = 'sent score';
  const pending = h.state.send('/api/source', {source: h.state.editor.value});
  h.state.editor.value = 'newer draft';
  h.finish();
  await pending;
  assert.equal(h.state.refreshes, 0);
  assert.equal(h.state.editor.value, 'newer draft');
  assert.equal(h.state.original, 'sent score');
  assert.equal(h.state.dirty(), true);
  const next = h.state.send('/api/source', {source: h.state.editor.value});
  assert.equal(h.requests[1].source, 'newer draft');
  assert.equal(h.requests[1].revision, 'saved-revision');
  h.finish(true, 'second-revision');
  await next;
  assert.equal(h.state.refreshes, 1);
});

test('reverting to the old text during a save remains an unsaved draft', async () => {
  const h = harness();
  h.state.editor.value = 'sent score';
  const pending = h.state.send('/api/source', {source: h.state.editor.value});
  h.state.editor.value = 'old score';
  h.finish();
  await pending;
  assert.equal(h.state.refreshes, 0);
  assert.equal(h.state.editor.value, 'old score');
  assert.equal(h.state.dirty(), true);
});

test('a grid save keeps a concurrent source draft and its conflict guard', async () => {
  const h = harness();
  const pending = h.state.send('/api/toggle', {pattern: 'beat', lane: 'bd', step: 0});
  h.state.editor.value = 'draft from the old grid';
  h.finish();
  await pending;
  assert.equal(h.state.refreshes, 0);
  assert.equal(h.state.editor.value, 'draft from the old grid');
  assert.equal(h.state.document.body.dataset.revision, 'old-revision');
  assert.equal(h.state.original, 'old score');
});

for (const failure of ['rejected', 'offline']) {
  test(`${failure} saves preserve the draft and allow retry`, async () => {
    const h = harness();
    h.state.editor.value = 'sent score';
    const pending = h.state.send('/api/source', {source: h.state.editor.value});
    h.state.editor.value = 'newer draft';
    if (failure === 'offline') h.fail();
    else h.finish(false);
    await pending;
    assert.equal(h.state.refreshes, 0);
    assert.equal(h.state.editor.value, 'newer draft');
    assert.equal(h.state.document.body.dataset.revision, 'old-revision');
    assert.equal(h.state.original, 'old score');
    assert.equal(h.state.busy, false);
    assert.equal(h.state.save.disabled, false);
    assert.equal(h.state.status.kind, 'error');
  });
}
