'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const page = fs.readFileSync(`${__dirname}/view.html`, 'utf8');
const handlerSource = page.match(/  window\.addEventListener\('cicada:sourcewritten', async event => \{[\s\S]*?\n  \}\);\n(?=  \/\/ pollScoreState)/)?.[0];
const staleSource = page.match(/  function staleRefresh\([\s\S]*?\n  \}\n/)?.[0];
assert.ok(handlerSource, 'cicada:sourcewritten handler must exist in view.html');
assert.ok(staleSource, 'staleRefresh must exist in view.html');
const syncSource = page.match(/  async function syncBrowserAudio\([\s\S]*?\n  \}\n/)?.[0];
const pollSource = page.match(/  async function pollScoreState\(\) \{[\s\S]*?\n  \}\n(?=  setInterval\(pollScoreState)/)?.[0];
assert.ok(syncSource && pollSource, 'syncBrowserAudio and pollScoreState must exist in view.html');


// Runs the real handler with a browser-audio stage that the test controls.
function setup(stage) {
  let handler;
  const document = {body: {dataset: {revision: 'redone'}}}, editor = {value: 'score without preset'};
  const statuses = [];
  const env = {
    document, editor, dirty: () => false, isBrowser: () => true,
    window: {addEventListener: (name, fn) => { handler = fn; }, cicadaBrowserAudio: {context: {}, stageCurrentScore: stage}},
    setStatus: (message, state) => statuses.push([message, state]),
    refreshProjection: async hint => { document.body.dataset.revision = hint.revision; editor.value = hint.source; }
  };
  vm.runInNewContext(`let audioRevision='',audioWanted='',audioStaging=null;${syncSource}${handlerSource}`, env);
  return {handler, document, editor, statuses};
}

test('a redo shows its source even while browser audio staging has not finished', async () => {
  const h = setup(() => new Promise(() => {})); // an earlier stage that never completes
  h.handler({detail: {revision: 'redone', source: 'score with preset bright'}});
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(h.editor.value, 'score with preset bright');
});

test('a redo shows its source when browser audio staging fails', async () => {
  const h = setup(async () => { throw new Error('Score changed while preparing the browser kernel'); });
  await h.handler({detail: {revision: 'redone', source: 'score with preset bright'}});
  assert.equal(h.editor.value, 'score with preset bright');
  assert.match(h.statuses.at(-1)[0], /could not stage/);
});

test('a projection reply read before a local write is dropped', () => {
  const context = vm.runInNewContext(`(()=>{${staleSource};return staleRefresh})()`, {});
  // Redo moved the page from "undone" to "redone" while a reply for "undone" was in flight.
  assert.equal(context('undone', 'redone', 'undone'), true);
  // The write's own reply, an unchanged page revision, and a missing reply revision all apply.
  assert.equal(context('undone', 'redone', 'redone'), false);
  assert.equal(context('saved', 'saved', 'external'), false);
  assert.equal(context('undone', 'redone', ''), false);
});

// The real syncBrowserAudio, pollScoreState, and source-written handler share
// audio state, as they do in view.html. The fake stage records every attempt.
function audioHarness(stage, refresh) {
  let handler;
  const document = {body: {dataset: {revision: 'undone'}}}, editor = {value: 'score without preset'};
  const statuses = [], staged = [], disk = {revision: 'undone', source: 'score without preset'};
  const env = {
    document, editor, dirty: () => false, isBrowser: () => true, browserPlaying: false, staged, disk,
    revision: () => document.body.dataset.revision,
    window: {addEventListener: (name, fn) => { handler = fn; }, cicadaBrowserAudio: {context: {}, stageCurrentScore: async revision => { staged.push(revision); return stage(revision); }}},
    setStatus: (message, state) => statuses.push([message, state]),
    refreshProjection: refresh || (async hint => { document.body.dataset.revision = hint.revision; editor.value = hint.source; }),
    fetch: async () => ({ok: true, json: async () => ({valid: true, ...disk})})
  };
  const api = vm.runInNewContext(`(()=>{let busy=false,original='',audioRevision='',audioWanted='',audioStaging=null;${syncSource}${pollSource}${handlerSource}return {poll:pollScoreState,sync:syncBrowserAudio,audio:()=>audioRevision}})()`, env);
  return {...api, handler, document, editor, statuses, staged, disk};
}

test('a poll retries a failed stage and audio reaches the new revision', async () => {
  let failures = 1;
  const h = audioHarness(async () => { if (failures-- > 0) throw new Error('stage failed'); });
  h.disk.revision = 'redone'; h.disk.source = 'score with preset bright';
  await h.poll();                      // outside edit: the page refreshes, the stage rejects
  assert.equal(h.editor.value, 'score with preset bright');
  assert.equal(h.document.body.dataset.revision, 'redone');
  assert.equal(h.audio(), '');
  assert.match(h.statuses.at(-1)[0], /retrying/);
  await h.poll();                      // page matches disk, audio is behind: retry
  assert.equal(h.audio(), 'redone');
  assert.deepEqual(h.staged, ['redone', 'redone']);
  await h.poll();                      // audio caught up: no further stage
  assert.equal(h.staged.length, 2);
});

test('a source write whose stage fails is repaired by the next poll', async () => {
  let failures = 1;
  const h = audioHarness(async () => { if (failures-- > 0) throw new Error('Score changed while preparing the browser kernel'); });
  h.disk.revision = 'redone'; h.disk.source = 'score with preset bright';
  await h.handler({detail: {revision: 'redone', source: 'score with preset bright'}});
  assert.equal(h.editor.value, 'score with preset bright');
  assert.equal(h.audio(), '');
  await h.poll();
  assert.equal(h.audio(), 'redone');
  assert.equal(h.document.body.dataset.revision, 'redone');
});

test('concurrent requests share one stage attempt and an older target never overwrites a newer one', async () => {
  const release = [];
  const h = audioHarness(() => new Promise(resolve => release.push(resolve)));
  const first = h.sync('undone');
  const joined = h.sync('undone');
  const newer = h.sync('redone');      // replaces the wanted target while 'undone' is in flight
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(h.staged, ['undone']);
  release.shift()();
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(h.staged, ['undone', 'redone']);
  release.shift()();
  await Promise.all([first, joined, newer]);
  assert.equal(h.audio(), 'redone');
  assert.deepEqual(h.staged, ['undone', 'redone']);
});

test('a projection failure still stages audio in the same handler and keeps the projection error', async () => {
  const h = audioHarness(async () => {}, async () => { throw new Error('projection unavailable'); });
  await h.handler({detail: {revision: 'redone', source: 'score with preset bright'}});
  assert.deepEqual(h.staged, ['redone']);
  assert.equal(h.audio(), 'redone');
  assert.deepEqual(h.statuses.map(s => s[0]), ['Source changed; the projection could not refresh']);
});

test('an empty audio target, a projection failure and a stage failure are repaired by later polls', async () => {
  let projection = false, stageFailures = 1;
  const h = audioHarness(async () => { if (stageFailures-- > 0) throw new Error('stage failed'); }, async function (hint) {
    if (!projection) throw new Error('projection unavailable');
    h.document.body.dataset.revision = hint.revision; h.editor.value = hint.source;
  });
  assert.equal(h.audio(), '');
  // The history write already moved the page revision before the handler ran.
  h.document.body.dataset.revision = 'redone';
  h.disk.revision = 'redone'; h.disk.source = 'score with preset bright';
  await h.handler({detail: {revision: 'redone', source: 'score with preset bright'}});
  assert.equal(h.audio(), '');                       // stage failed after the projection failed
  assert.match(h.statuses.at(-1)[0], /projection could not refresh; browser audio could not stage/);
  projection = true;
  await h.poll();                                    // page matches disk: the poll stages again
  assert.equal(h.audio(), 'redone');
  await h.poll();
  assert.deepEqual(h.staged, ['redone', 'redone']);
});

test('a poll stages when audio is behind even if no earlier write recorded a target', async () => {
  const h = audioHarness(async () => {});
  h.document.body.dataset.revision = 'redone'; h.disk.revision = 'redone';
  await h.poll();
  assert.equal(h.audio(), 'redone');
});
