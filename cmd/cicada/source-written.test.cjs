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
  vm.runInNewContext(handlerSource, env);
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
