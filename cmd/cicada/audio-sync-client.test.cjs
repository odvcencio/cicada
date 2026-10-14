'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const page = fs.readFileSync(`${__dirname}/view.html`, 'utf8');
const clientSource = fs.readFileSync(`${__dirname}/../../host/web/client.js`, 'utf8');
const syncSource = page.match(/  async function syncBrowserAudio\([\s\S]*?\n  \}\n/)?.[0];
const pollSource = page.match(/  async function pollScoreState\(\) \{[\s\S]*?\n  \}\n(?=  setInterval\(pollScoreState)/)?.[0];
const handlerSource = page.match(/  window\.addEventListener\('cicada:sourcewritten', async event => \{[\s\S]*?\n  \}\);\n(?=  \/\/ pollScoreState)/)?.[0];
assert.ok(syncSource && pollSource && handlerSource, 'sync, poll and handler must exist in view.html');
const settle = () => new Promise(resolve => setImmediate(resolve));

// The real browser client (host/web/client.js) with a fake AudioContext whose
// worklet acknowledges every staged image, and the real page functions.
function harness() {
  const posted = [];
  let disk = {revision: 'base', source: 'base score'}, handler;
  const image = new Uint8Array(32);
  image.set([67, 73, 67, 49, 15]); new DataView(image.buffer).setUint32(12, 120000, true);
  class Context {
    constructor() { this.sampleRate = 48000; this.currentTime = 0; this.audioWorklet = {addModule: async () => {}}; }
    async resume() {}
    async close() {}
  }
  const win = {AudioContext: Context, addEventListener: (name, fn) => { handler = fn; }};
  const env = vm.createContext({
    window: win, performance, setTimeout, clearTimeout,
    setInterval: callback => callback, clearInterval() {}, requestAnimationFrame: callback => callback, cancelAnimationFrame() {},
    WebAssembly: {compile: async () => ({})},
    fetch: async () => ({ok: true, headers: {get: () => disk.revision}, arrayBuffer: async () => image.slice().buffer}),
    AudioWorkletNode: class {
      constructor() {
        this.port = {postMessage: data => {
          posted.push(data);
          if (data.t === 'i') queueMicrotask(() => win.cicadaBrowserAudio.receive({t: 't', r: data.r}));
        }, close() {}};
      }
      connect() { queueMicrotask(() => this.port.onmessage({data: {t: 'r', p: 65537, r: disk.revision, c: true}})); }
      disconnect() {}
    }
  });
  vm.runInContext(clientSource, env);
  const audio = win.cicadaBrowserAudio;
  const document = {body: {dataset: {revision: 'base'}}}, editor = {value: 'base score'};
  const statuses = [];
  const pageEnv = {
    document, editor, dirty: () => false, isBrowser: () => true, browserPlaying: false,
    revision: () => document.body.dataset.revision,
    window: {addEventListener: (name, fn) => { handler = fn; }, cicadaBrowserAudio: audio},
    setStatus: (message, state) => statuses.push([message, state]),
    refreshProjection: async hint => { document.body.dataset.revision = hint.revision; editor.value = hint.source; },
    fetch: async () => ({ok: true, json: async () => ({valid: true, ...disk})})
  };
  const api = vm.runInNewContext(`(()=>{let busy=false,original='',audioWanted='',audioStaging=null;${syncSource}${pollSource}${handlerSource}return {poll:pollScoreState}})()`, pageEnv);
  return {audio, posted, document, editor, statuses, poll: api.poll, handler: () => handler, setDisk: value => { disk = value; }};
}
const stagedRevisions = h => h.posted.filter(data => data.t === 'i').map(data => data.r);

test('after a capture commit stages through the client, an undo to the earlier revision stages again', async () => {
  const h = harness();
  await h.audio.startAudio();
  assert.equal(h.audio.revision, 'base');
  // A library save puts the page and audio at 'bright'.
  h.setDisk({revision: 'bright', source: 'score with preset bright'});
  await h.handler()({detail: {revision: 'bright', source: 'score with preset bright'}});
  assert.equal(h.audio.revision, 'bright');
  // Capture publication commits a take and stages its own image; the page never sees that stage.
  h.setDisk({revision: 'captured', source: 'score with take'});
  await h.audio.startAudio(true);
  assert.equal(h.audio.revision, 'captured');
  h.document.body.dataset.revision = 'captured'; h.editor.value = 'score with take';
  // Immediate undo back to 'bright': history moves the page revision, then fires the event.
  h.setDisk({revision: 'bright', source: 'score with preset bright'});
  h.document.body.dataset.revision = 'bright';
  await h.handler()({detail: {revision: 'bright', source: 'score with preset bright'}});
  await settle();
  assert.equal(h.audio.revision, 'bright');
  assert.equal(h.editor.value, 'score with preset bright');
  const staged = stagedRevisions(h);
  // A later matching-disk poll adds nothing.
  await h.poll();
  assert.deepEqual(stagedRevisions(h), staged);
  assert.equal(staged.at(-1), 'bright');
  assert.deepEqual(staged, ['bright', 'captured', 'bright']);
});

test('a poll repairs audio that capture staging moved away from the displayed revision', async () => {
  const h = harness();
  await h.audio.startAudio();
  h.setDisk({revision: 'captured', source: 'score with take'});
  await h.audio.startAudio(true);
  // The page shows 'base' and disk is back at 'base' (the undo's event was missed).
  h.setDisk({revision: 'base', source: 'base score'});
  await h.poll();
  assert.equal(h.audio.revision, 'base');
  await h.poll();
  assert.deepEqual(stagedRevisions(h), ['captured', 'base']);
});
