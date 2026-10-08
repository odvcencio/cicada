'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const page = fs.readFileSync(`${__dirname}/view.html`, 'utf8');
const source = page.match(/  async function send\(path, payload\) \{[\s\S]*?\n  \}\n  let resolveIdle/)?.[0].replace(/\n  let resolveIdle$/, '');
assert.ok(source);

function setup() {
  let releaseState, stateRequested;
  const stateGate = new Promise(resolve => {releaseState = resolve;});
  const requested = new Promise(resolve => {stateRequested = resolve;});
  const document = {body: {dataset: {revision: 'base'}}}, editor = {value: 'base score'};
  const statuses = [], posts = [];
  const response = (ok, data) => ({ok, json: async () => data});
  const env = {
    document, editor, save: {disabled: false}, window: {cicadaBrowserAudio: {context: null}}, browserPlaying: false,
    isBrowser: () => true, revision: () => document.body.dataset.revision,
    setStatus: (...args) => statuses.push(args), refreshProjection: async () => {}, showEditor() {},
    fetch: async (path, options) => {
      if (path === '/api/state') {
        stateRequested(); await stateGate;
        return response(true, {valid: true, revision: 'grid-saved', source: 'score with grid edit'});
      }
      const payload = JSON.parse(options.body); posts.push({path, payload});
      return path === '/api/toggle' ? response(true, {revision: 'grid-saved'})
        : response(false, {error: 'score changed on disk; reload before saving'});
    }
  };
  const send = vm.runInNewContext(`(()=>{let busy=false,idle,resolveIdle=()=>{},original=editor.value;const dirty=()=>editor.value!==original;${source};return send})()`, env);
  return {send, document, editor, posts, statuses, requested, releaseState};
}

test('browser grid save preserves a draft typed while the source refresh is pending', async () => {
  const h = setup();
  const save = h.send('/api/toggle', {pattern: 'beat', lane: 'bd', step: 1});
  await h.requested;
  h.editor.value = 'open draft based on the old score';
  h.releaseState(); await save;
  assert.equal(h.editor.value, 'open draft based on the old score');
  assert.equal(h.document.body.dataset.revision, 'base');
  assert.equal(await h.send('/api/source', {source: h.editor.value}), false);
  assert.equal(h.posts.at(-1).payload.revision, 'base');
  assert.match(h.statuses.at(-1)[0], /score changed on disk/);
});

test('a clean browser editor and its revision advance together after a grid save', async () => {
  const h = setup();
  const save = h.send('/api/toggle', {pattern: 'beat', lane: 'bd', step: 1});
  await h.requested;
  assert.equal(h.document.body.dataset.revision, 'base');
  assert.equal(h.editor.value, 'base score');
  h.releaseState(); await save;
  assert.equal(h.document.body.dataset.revision, 'grid-saved');
  assert.equal(h.editor.value, 'score with grid edit');
});
