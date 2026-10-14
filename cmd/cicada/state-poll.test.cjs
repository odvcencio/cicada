'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const page = fs.readFileSync(`${__dirname}/view.html`, 'utf8');
const source = page.match(/  async function pollScoreState\(\) \{[\s\S]*?\n  \}\n  setInterval\(pollScoreState/)?.[0].replace(/\n  setInterval\(pollScoreState$/, '');
assert.ok(source, 'pollScoreState must exist in view.html');

function setup() {
  let release, requested;
  const gate = new Promise(resolve => { release = resolve; });
  const seen = new Promise(resolve => { requested = resolve; });
  const document = {body: {dataset: {revision: 'saved'}}}, editor = {value: 'score with preset bright'};
  const refreshes = [];
  const env = {
    document, editor, window: {cicadaBrowserAudio: {context: null}}, browserPlaying: false,
    isBrowser: () => false, revision: () => document.body.dataset.revision,
    setStatus() {}, dirty: () => false, audioWanted: '', syncBrowserAudio: async () => {},
    refreshProjection: async hint => { refreshes.push(hint); document.body.dataset.revision = hint.revision; editor.value = hint.source; },
    fetch: async () => { requested(); await gate; return {ok: true, json: async () => ({valid: true, revision: 'saved', source: 'score with preset bright'})}; }
  };
  const poll = vm.runInNewContext(`(()=>{let busy=false,original='';${source};return pollScoreState})()`, env);
  return {poll, document, editor, refreshes, seen, release};
}

test('a state reply read before a local undo does not roll the editor back', async () => {
  const h = setup();
  const polling = h.poll();
  await h.seen;
  // Undo finishes while the poll request is in flight.
  h.document.body.dataset.revision = 'undone';
  h.editor.value = 'score without preset';
  h.release(); await polling;
  assert.equal(h.document.body.dataset.revision, 'undone');
  assert.equal(h.editor.value, 'score without preset');
  assert.deepEqual(h.refreshes, []);
});

test('an outside edit is still picked up when nothing local changed', async () => {
  const h = setup();
  h.document.body.dataset.revision = 'older';
  const polling = h.poll();
  await h.seen; h.release(); await polling;
  assert.equal(h.refreshes.length, 1);
  assert.equal(h.document.body.dataset.revision, 'saved');
});
