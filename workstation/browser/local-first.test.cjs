'use strict';
/*
 * Local-first walkthrough: for each Studio interaction, does the result wait
 * on the server? This is PR3 of the design "Studio holds the score in the tab"
 * (sections 9, 14.2 and 14.3), in report mode: it prints one table and a JSON
 * file and fails only when the measured kind of a row differs from
 * local-first.expected.json. A change that makes a row local updates that file
 * in the same pull request; a change that makes a row wait again fails here.
 *
 * Each row of ROWS is one interaction (ids from the 2026-11-12 round-trip
 * audit). STEPS scripts the rows that can run on the fixtures; UNSCRIPTED names
 * every other row with the reason. A step runs twice:
 *
 *   open  The network answers. On one page clock (performance.now()) the probe
 *         records the trusted input (pointerdown, pointerup, pointermove,
 *         keydown or wheel) and the first animation frame whose callback sees
 *         the expected result: firstFrameMs is the time in that callback (a
 *         lower bound), ms the next frame's timestamp (an upper bound), as in
 *         edit-undo-benchmark.cjs. Every fetch, XMLHttpRequest, sendBeacon,
 *         WebSocket open and frame, and EventSource the page starts is logged
 *         with its start time; those between input and paint are
 *         requests_in_window. Document requests are taken from Node's request
 *         events.
 *   held  The same step with every HTTP request held unanswered and every
 *         WebSocket frame stopped, from the input until the result paints or
 *         the hold limit passes (CICADA_LOCAL_FIRST_HOLD_MS, default 250 ms,
 *         and at least CICADA_LOCAL_FIRST_HOLD_FRAMES animation frames, default
 *         8, so a busy main thread still gets its frames). Then the network is
 *         released and the step must reach its result (the recovery check).
 *
 * Kind, as measured:
 *   R  the result did not paint while the network was held: waited_on_server.
 *   B  it painted while held, and the step started requests of its own between
 *      the input and the end of its settle (the page idle again).
 *   L  it painted while held and started no request of its own.
 * A request that belongs to another row (a poll, a recovery copy, a stale
 * region read: BACKGROUND below) is listed but not counted for the step.
 * A background row (a poll, a stream, a debounced save) has a display that is
 * already on screen; its step watches for its own request for a bounded time
 * and is B when the request comes, L when it does not.
 * A step whose result comes after a deliberate local delay (the mixer saves
 * 400 ms after the last key) sets its own hold limit (holdMs).

 * Setup is not measured: a step's prepare runs with the network open and may
 * navigate. One failed step does not stop the others; it is retried once on a
 * fresh page (CICADA_LOCAL_FIRST_ATTEMPTS) and the suite fails if it fails
 * every time. Waits are conditions, never fixed sleeps; the hold limit and a
 * background row's watch are bounded, and both are part of the measurement.
 *
 * node workstation/browser/local-first.test.cjs --write-fixture <new-score> [scene|placement]
 * ./build/cicada studio <new-score> --audio null --listen 127.0.0.1:8190 > <log>
 * CICADA_STUDIO_URL=http://127.0.0.1:8190 CICADA_STUDIO_STARTUP_LOG=<log> \
 *   CICADA_LOCAL_FIRST_FIXTURE=scene node --test workstation/browser/local-first.test.cjs
 *
 * The run edits the score: start a fresh Studio on a fresh fixture each time.
 * CICADA_LOCAL_FIRST_PART=k/n runs part k of n of the fixture's steps (each
 * step names its part; CI runs scene 1/3, 2/3, 3/3 and placement 1/1 side by
 * side, each on its own Studio). CICADA_LOCAL_FIRST_PROOF_DIR (default
 * build/local-first) receives local-first-<fixture>[-<k>of<n>].json; when every
 * scripted row has a result there, the run also prints the totals of all 109
 * rows. CICADA_LOCAL_FIRST_ROWS=G1,G3 runs only those rows, and =all every row,
 * without the expectation check. CICADA_PLAYWRIGHT_MODULE and
 * CICADA_CHROME_PATH select local browser tooling.
 */

const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');

const FIXTURES = {
  // continuity.test.cjs's score: one acid track, two 64-step patterns, two scenes.
  scene: {marker: '// cicada-continuity-test: disposable fixture', write: target => require('node:child_process').execFileSync(process.execPath, [path.join(__dirname, 'continuity.test.cjs'), '--write-fixture', target])},
  // placements.test.cjs's score (examples/songs/tidal-glass.cicada): four tracks, an arrangement of nine placements.
  placement: {marker: '// cicada-placement-test: disposable fixture', write: target => require('node:child_process').execFileSync(process.execPath, [path.join(__dirname, 'placements.test.cjs'), '--write-fixture', target])},
};

if (process.argv[2] === '--write-fixture') {
  assert.ok(process.argv[3], 'supply a new disposable fixture path');
  const name = process.argv[4] || 'scene';
  assert.ok(FIXTURES[name], `fixture must be one of ${Object.keys(FIXTURES).join(', ')}`);
  FIXTURES[name].write(process.argv[3]);
  process.exit(0);
}

const env = (name, fallback) => {
  const value = Number(process.env[name] ?? fallback);
  assert.ok(Number.isFinite(value) && value > 0, `${name} must be a positive number`);
  return value;
};
const HOLD_MS = env('CICADA_LOCAL_FIRST_HOLD_MS', 250);
const HOLD_FRAMES = env('CICADA_LOCAL_FIRST_HOLD_FRAMES', 8);
const OPEN_MS = env('CICADA_LOCAL_FIRST_OPEN_MS', 15_000);
const ATTEMPTS = env('CICADA_LOCAL_FIRST_ATTEMPTS', 2);

// CICADA_LOCAL_FIRST_PART=k/n runs the fixture's steps whose part is k of n
// (a step's part field, 1 by default, taken modulo n), so CI can run the parts
// side by side on separate Studios.
function partOf(value) {
  if (!value) return {index: 1, of: 1, label: 'all'};
  const match = /^(\d+)\/(\d+)$/.exec(value);
  assert.ok(match && Number(match[1]) >= 1 && Number(match[1]) <= Number(match[2]), 'CICADA_LOCAL_FIRST_PART must look like 1/3');
  return {index: Number(match[1]), of: Number(match[2]), label: value};
}

// ---------------------------------------------------------------------------
// Rows: every interaction of the design's section 9, with the audit's class
// (R, B or L, read from the code on 2026-11-12) and the design's target class.
// The audit lists 109 rows: 72 R, 19 B, 18 L.

const ROWS = [
  ['T1', 'Play, Pause, Stop, Return to start', 'R', 'A'],
  ['T2', 'Space (play or pause)', 'R', 'A'],
  ['T3', 'Home', 'R', 'A'],
  ['T4', 'Play from a song block', 'R', 'A'],
  ['T5', 'Audition or loop a section or vocal range', 'R', 'A'],
  ['T6', 'Loop selected placement (Mod+L)', 'R', 'E'],
  ['T7', 'Bar and step readout (1 s poll)', 'B', 'P'],
  ['T8', 'Playhead cursor (250 ms poll)', 'B', 'P'],
  ['G1', 'Piano-roll cell click (pitch)', 'B', 'E'],
  ['G2', 'Drum-lane cell click (toggle)', 'B', 'E'],
  ['G3', 'Select a step', 'R', 'L'],
  ['G4', 'Step inspector save', 'R', 'E'],
  ['G5', 'Place chord', 'R', 'E'],
  ['G6', 'Step range and resize', 'R', 'E'],
  ['G7', 'Pattern timing (swing, gate, transpose)', 'R', 'E'],
  ['G8', 'Create, duplicate, rename, delete, bind a pattern', 'R', 'E'],
  ['G9', 'Performance note form and phrase transforms', 'R', 'E'],
  ['G10', 'Gesture, time, pitch-shape, group forms', 'R', 'E'],
  ['G11', 'Quantize (button or Mod+U)', 'R', 'E'],
  ['G12', 'View register (octave)', 'R', 'L'],
  ['G13', 'Select or Draw tool (B)', 'L', 'L'],
  ['S1', 'Launch scene or slot, stop track', 'R', 'A'],
  ['S2', 'F1 to F8 scene keys', 'R', 'A'],
  ['S3', 'Play a note (keys, pads, computer keys)', 'R', 'A'],
  ['S4', 'Release and heartbeat of held notes', 'B', 'A'],
  ['S5', 'Keep played notes', 'R', 'E'],
  ['S6', 'Restore retained notes', 'R', 'L'],
  ['S7', 'Private checkpoint of a played take', 'B', 'L'],
  ['S8', 'MIDI learn and bind', 'R', 'E'],
  ['A1', 'Select a placement or clip', 'R', 'L'],
  ['A2', 'Drag preview of a placement move or resize', 'L', 'L'],
  ['A3', 'Drop a placement', 'R', 'E'],
  ['A4', 'Arrow-key nudge of a placement', 'R', 'E'],
  ['A5', 'Duplicate or delete a placement', 'R', 'E'],
  ['A6', 'Placement inspector, Add placement', 'R', 'E'],
  ['A7', 'Song blocks', 'R', 'E'],
  ['A8', 'Project settings (title, tempo, key)', 'R', 'E'],
  ['A9', 'Tempo map step set or remove', 'R', 'E'],
  ['A10', 'Jump to Song timeline or Record and comp vocals', 'L', 'L'],
  ['A11', 'Vocal lane waveform load', 'B', 'N'],
  ['A12', 'Vocal take edits', 'R', 'E'],
  ['A13', 'Audio track, clip settings, place clip', 'R', 'E'],
  ['M1', 'Fader or pan drag readout', 'L', 'L'],
  ['M2', 'Fader or pan commit', 'R', 'E'],
  ['M3', 'Mute, solo', 'R', 'E'],
  ['M4', 'Effect chain up, down, remove, add', 'R', 'E'],
  ['M5', 'Vocal chain, amp chain, chorus, other mixer values', 'R', 'E'],
  ['M6', 'Device: instrument parameter Apply', 'R', 'E'],
  ['C1', 'Typing in the Code pane', 'L', 'L'],
  ['C2', 'Highlighting and error marks while typing', 'B', 'L'],
  ['C3', 'Recovery copy of unsaved text', 'B', 'L'],
  ['C4', 'Code Save', 'R', 'E'],
  ['C5', 'Check file', 'R', 'L'],
  ['C6', 'Switch project file', 'R', 'L'],
  ['C7', 'Discard, merge, open a recovery copy', 'R', 'L'],
  ['C8', 'Shared-draft typing (Collaborate)', 'B', 'N'],
  ['C9', 'Save shared score, invite, join', 'R', 'E'],
  ['L1', 'Listen to a sound, Loop part', 'R', 'A'],
  ['L2', 'Stop a preview', 'B', 'A'],
  ['L3', 'Add a preset as a track, set an instrument', 'R', 'E'],
  ['L4', 'Save a track preset', 'R', 'H'],
  ['L5', 'Download a sample pack', 'R', 'H'],
  ['L6', 'Import an instrument or sampler file', 'R', 'H'],
  ['L7', 'Capture, build or fit a recorded instrument', 'R', 'H'],
  ['L8', 'Open or close a Library section', 'L', 'L'],
  ['L9', 'Hit a Sampler pad', 'L', 'L'],
  ['L10', 'Sampler edits and resample', 'R', 'E'],
  ['L11', 'Generate, mutate, apply a phrase', 'R', 'E'],
  ['L12', 'Pattern Machine', 'R', 'E'],
  ['Au1', 'Choose the automation parameter', 'R', 'L'],
  ['Au2', 'Select, drag or add a curve point', 'L', 'L'],
  ['Au3', 'Save or remove a point', 'R', 'E'],
  ['Au4', 'Scene automation, modulation forms', 'R', 'E'],
  ['Au5', 'Record movements', 'R', 'E'],
  ['Au6', 'Automation recording state (poll)', 'B', 'L'],
  ['Rc1', 'Record or finish a take', 'R', 'H'],
  ['Rc2', 'Count-in, durable frames, input status (300 ms poll)', 'B', 'P'],
  ['Rc3', 'Keep, discard or select a take pass', 'R', 'H'],
  ['Rc4', 'Audio dialog: devices, enable input', 'R', 'H'],
  ['Rc5', 'Audio dialog timing readouts (500 ms poll)', 'B', 'P'],
  ['Rc6', 'Transcribe a melody', 'R', 'H'],
  ['Rc7', 'Start an export', 'R', 'H'],
  ['Rc8', 'Export progress', 'B', 'P'],
  ['Rc9', 'Download the rendered file', 'R', 'H'],
  ['U1', 'Undo', 'R', 'E'],
  ['U2', 'Redo', 'R', 'E'],
  ['U3', 'Revert from History', 'R', 'E'],
  ['V1', 'Arrange, Session, Mixer tabs', 'L', 'L'],
  ['V2', 'Clip, Device, Automation dock tabs', 'L', 'L'],
  ['V3', 'Show or hide Library and Code', 'L', 'L'],
  ['V4', 'Resize panes', 'L', 'L'],
  ['V5', 'Open the History, Audio, Export or Shortcuts dialog', 'L', 'L'],
  ['V6', 'Phone region switch (390 px)', 'L', 'L'],
  ['V7', 'Read stale regions', 'B', 'L'],
  ['V8', 'Refresh saved state', 'R', 'L'],
  ['Se1', 'Select a pattern, scene or track', 'R', 'L'],
  ['Se2', 'Select a performance note, gesture point, shared lane', 'R', 'L'],
  ['Z1', 'Arrange zoom, snap, copy-drag', 'L', 'L'],
  ['Z2', 'Scroll the timeline, roll or strips', 'L', 'L'],
  ['Z3', 'Autoscroll during a placement drag', 'L', 'L'],
  ['Z4', 'Follow playback', 'B', 'L'],
  ['Me1', 'Mixer meters', 'B', 'P'],
  ['Mi1', 'Web MIDI note in', 'R', 'A'],
  ['Mi2', 'Native MIDI port note in', 'R', 'A'],
  ['Mi3', 'List or choose a MIDI port', 'R', 'H'],
  ['St1', 'Open Studio', 'R', 'H'],
  ['St2', 'Load the UI engine', 'R', 'H'],
  ['St3', 'Engine mount reads', 'B', 'N'],
  ['St4', 'Startup status, reload guard', 'L', 'L'],
].map(([id, what, audit, target]) => ({id, what, audit, target}));

// Requests that belong to a row of their own. A step that is not that row
// lists them but does not count them as its own traffic.
const BACKGROUND = [
  {row: 'Me1', method: 'GET', path: /^\/api\/meters$/},
  {row: 'T7', method: 'GET', path: /^\/api\/transport$/},
  // The Live engine renews its lease every second while notes are held and
  // releases it when it unmounts (a soft navigation remounts it).
  {row: 'S4', method: 'POST', path: /^\/__actions\/live$/, body: /heartbeat|"type":"release"/},
  {row: 'Rc2', method: 'GET', path: /^\/api\/capture$/},
  {row: 'Rc5', method: 'GET', path: /^\/api\/audio\/config$/},
  {row: 'Rc8', method: 'GET', path: /^\/api\/export(\/stems|\/midi)?$/},
  {row: 'Au6', method: 'GET', path: /^\/api\/automation\/recording$/},
  {row: 'V7', method: 'GET', path: /^\/__studio\/regions$/},
  {row: 'C2', method: 'POST', path: /^\/__code\/analysis$/},
  {row: 'C3', method: 'POST', path: /^\/__actions\/source-checkpoint$/},
  {row: 'S7', method: 'POST', path: /^\/__actions\/note-checkpoint$/},
  {row: 'A11', method: 'GET', path: /^\/api\/takes\/[^/]+\/waveform$/},
  {row: 'St3', method: 'GET', path: /^\/api\/(sampler|params)$/},
  {row: 'C8', method: 'GET', path: /^\/collaboration\/score$/},
  {row: 'C8', method: 'SEND', path: /^\/collaboration\/score$/},
  // GoSX reports client events (errors, timings) to the workstation; no row.
  {row: 'gosx-client-events', method: 'POST', path: /^\/_gosx\/client-events$/},
];
const backgroundRow = entry => BACKGROUND.find(rule => rule.method === entry.method && rule.path.test(entry.path) && (!rule.body || rule.body.test(entry.body || '')))?.row ?? null;

// ---------------------------------------------------------------------------
// The page half. Installed with addInitScript, so it runs before Studio's own
// scripts and every fetch Studio or GoSX makes goes through its wrapper.

function localFirstProbe() {
  if (window.__lf) return;
  const lf = window.__lf = {net: [], inflight: 0, spec: null, check: null, base: null, sample: null, result: null};
  const now = () => performance.now();
  const peek = body => {
    try {
      if (body == null) return '';
      if (typeof body === 'string') return body.slice(0, 300);
      if (body instanceof URLSearchParams) return body.toString().slice(0, 300);
      if (body instanceof ArrayBuffer) return new TextDecoder().decode(new Uint8Array(body, 0, Math.min(300, body.byteLength)));
      if (ArrayBuffer.isView(body)) return new TextDecoder().decode(new Uint8Array(body.buffer, body.byteOffset, Math.min(300, body.byteLength)));
      if (body instanceof FormData) return [...body.keys()].join('&').slice(0, 300);
    } catch {}
    return '';
  };
  const log = (kind, method, url, body) => {
    const entry = {t: now(), kind, method: String(method || 'GET').toUpperCase(), path: '', search: '', body: peek(body), end: null};
    try { const target = new URL(String(url), location.href); entry.path = target.pathname; entry.search = target.search.slice(0, 160); } catch { entry.path = String(url).slice(0, 160); }
    lf.net.push(entry);
    return entry;
  };
  const done = entry => () => { entry.end = now(); lf.inflight--; };
  const fetch0 = window.fetch;
  if (typeof fetch0 === 'function') {
    window.fetch = function (input, init) {
      const request = typeof Request === 'function' && input instanceof Request ? input : null;
      const entry = log('fetch', (init && init.method) || (request && request.method), request ? request.url : input, init && init.body);
      lf.inflight++;
      const settle = done(entry);
      try {
        const pending = fetch0.apply(this, arguments);
        pending.then(response => {
          entry.status = response.status;
          // Keep the start of a refusal, so a failed step can say why.
          if (response.status >= 400) response.clone().text().then(text => { entry.reply = text.slice(0, 200); }, () => {});
          settle();
        }, settle);
        return pending;
      } catch (error) { settle(); throw error; }
    };
  }
  const open0 = XMLHttpRequest.prototype.open, send0 = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.open = function (method, url) { this.__lfTarget = [method, url]; return open0.apply(this, arguments); };
  XMLHttpRequest.prototype.send = function (body) {
    const target = this.__lfTarget || [];
    const entry = log('xhr', target[0], target[1], body);
    lf.inflight++;
    const finished = done(entry);
    this.addEventListener('loadend', () => { entry.status = this.status; finished(); }, {once: true});
    return send0.apply(this, arguments);
  };
  if (navigator.sendBeacon) {
    const beacon0 = navigator.sendBeacon;
    navigator.sendBeacon = function (url, data) { log('beacon', 'POST', url, data).end = now(); return beacon0.apply(this, arguments); };
  }
  if (typeof window.WebSocket === 'function') {
    const Socket = window.WebSocket;
    const send = Socket.prototype.send;
    Socket.prototype.send = function (data) { log('ws-frame', 'SEND', this.url, data).end = now(); return send.apply(this, arguments); };
    window.WebSocket = new Proxy(Socket, {construct(target, args, newTarget) { log('websocket', 'GET', args[0]).end = now(); return Reflect.construct(target, args, newTarget); }});
  }
  if (typeof window.EventSource === 'function') {
    window.EventSource = new Proxy(window.EventSource, {construct(target, args, newTarget) { log('eventsource', 'GET', args[0]).end = now(); return Reflect.construct(target, args, newTarget); }});
  }

  function begin(start) {
    lf.sample = {start, frames: 0, seen: null, error: null};
    requestAnimationFrame(tick);
  }
  function tick(timestamp) {
    const sample = lf.sample;
    if (!sample || lf.result) return;
    sample.frames++;
    if (sample.seen != null) {
      lf.result = {painted: true, start: sample.start, firstFrameMs: sample.seen - sample.start, ms: timestamp - sample.start, frames: sample.frames, error: sample.error};
      return;
    }
    let ok = false;
    try { ok = !!lf.check(lf.spec.args, lf.base); } catch (error) { sample.error = String(error && error.message || error).slice(0, 200); }
    const at = now();
    if (ok) { sample.seen = at; requestAnimationFrame(tick); return; }
    if (at - sample.start >= lf.spec.limitMs && sample.frames >= lf.spec.limitFrames) {
      lf.result = {painted: false, start: sample.start, firstFrameMs: null, ms: null, waitedMs: at - sample.start, frames: sample.frames, error: sample.error};
      return;
    }
    requestAnimationFrame(tick);
  }
  const accepts = event => {
    const want = lf.spec && lf.spec.input;
    if (!want || lf.sample || !event.isTrusted || event.type !== want.type) return false;
    if (want.key && event.key !== want.key && event.code !== want.key) return false;
    return true;
  };
  for (const type of ['pointerdown', 'pointerup', 'pointermove', 'keydown', 'wheel']) {
    window.addEventListener(type, event => { if (accepts(event)) begin(event.timeStamp); }, {capture: true, passive: true});
  }
  lf.arm = (spec, check, snapshot) => {
    lf.spec = spec;
    lf.check = check;
    lf.base = snapshot ? snapshot(spec.args) : null;
    lf.result = null;
    lf.sample = null;
    lf.mark = lf.net.length;
    if (spec.input.type === 'none') begin(now());
    return lf.base;
  };
  // Helpers for the steps' checks.
  lf.shown = selector => {
    const node = document.querySelector(selector);
    return !!node && !node.closest('[hidden]') && node.getClientRects().length > 0;
  };
  // The saved revision the page shows: the Undo form carries it in every view.
  lf.revision = () => document.querySelector('#undo-form input[name=revision]')?.value || '';
  lf.position = () => {
    const bar = document.querySelector('[data-gosx-live-bind=bar]')?.textContent?.trim();
    const step = document.querySelector('[data-gosx-live-bind=step]')?.textContent?.trim();
    return `BAR ${bar} STEP ${step}`;
  };
  // A request to path (and with body containing text) started since the step
  // was armed and was answered.
  lf.replied = (path, text) => lf.net.slice(lf.mark).some(entry => entry.path === path && entry.end != null && (!text || entry.body.includes(text)));
}

// ---------------------------------------------------------------------------
// Steps. Each step names its fixture and its part (1 by default) and its
// input type. prepare (optional) sets the page up with the network open and
// returns args for the check; act makes the trusted input; check runs in the
// page every frame and is true once the expected result shows; snapshot
// (optional) runs in the page when the step is armed and reaches check as
// base. others lists request paths that belong to another row the step sets
// off; settled (optional) is a condition the step waits for before it ends;
// background (a poll or debounced save) counts the step's own request within
// a bounded time; holdMs overrides the hold limit; finish (optional) undoes
// setup with the network open.

const SCENE = '/studio?tab=arrange&dock=clip&pattern=continuity&step=1';
const PLACEMENT = '/studio?tab=arrange&dock=clip&pattern=ripple&step=1';
const PLACEMENT_SELECTED = '/studio?tab=arrange&dock=clip&placement=near-home';
const MACHINE = '/studio?tab=arrange&dock=clip&machine=1&pattern=continuity&track=keys';
const TIDE = '/studio?tab=arrange&dock=clip&pattern=tide&step=1';
// The Code pane's Check file button renders with region=code (code-editor.test.cjs opens it so).
const CODE = '/studio?tab=arrange&dock=clip&pattern=continuity&step=1&region=code';
const mod = process.platform === 'darwin' ? 'Meta' : 'Control';

// Page-side checks used by several steps. Each runs in the page and must not
// close over Node values: it reads its arguments and window.__lf.
const checks = {
  // The page shows a newer saved revision than when the step was armed.
  saved: (args, base) => window.__lf.revision() !== base.revision,
  // The host answered a request the step started (audio and native rows: the
  // host makes the sound; the tab can only see that the host answered).
  replied: args => window.__lf.replied(args.path, args.body),
  playing: (args, base) => window.__lf.position() !== base.position,
};
const snapshots = {
  revision: () => ({revision: window.__lf.revision()}),
  position: () => ({position: window.__lf.position(), revision: window.__lf.revision()}),
};

const STEPS = {
  // --- Transport -----------------------------------------------------------
  T1: {
    fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.stop(); },
    act: ctx => ctx.tap('#transport-form button[value=play]'),
    check: checks.playing, snapshot: snapshots.position,
    finish: ctx => ctx.stop(),
  },
  T2: {
    fixture: 'scene', input: {type: 'keydown', key: 'Space'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.stop(); await ctx.blur(); },
    act: ctx => ctx.page.keyboard.press('Space'),
    check: checks.playing, snapshot: snapshots.position,
    finish: ctx => ctx.stop(),
  },
  T3: {
    fixture: 'scene', input: {type: 'keydown', key: 'Home'},
    prepare: async ctx => {
      await ctx.open(SCENE);
      await ctx.play();
      await until(() => ctx.page.evaluate(() => Number(/BAR (\d+)/.exec(window.__lf.position())?.[1]) >= 2), 'playback did not reach bar 2', 10_000);
      await ctx.blur();
    },
    act: ctx => ctx.page.keyboard.press('Home'),
    check: (args, base) => window.__lf.position() !== base.position && /^BAR 1 /.test(window.__lf.position()),
    snapshot: snapshots.position,
    finish: ctx => ctx.stop(),
  },
  T7: {
    fixture: 'scene', input: {type: 'none'}, background: {method: 'GET', path: /^\/api\/transport$/, count: 1, ms: 2500},
    prepare: ctx => ctx.open(SCENE),
    act: async () => {},
    check: () => !!document.querySelector('[data-gosx-live-bind=bar]'),
  },
  T8: {
    fixture: 'placement', input: {type: 'none'}, background: {method: 'GET', path: /^\/api\/transport$/, count: 3, ms: 1200},
    prepare: async ctx => { await ctx.open(PLACEMENT); await ctx.view('arrange'); await ctx.play(); },
    act: async () => {},
    check: () => !!document.querySelector('#view-panel-arrange .placement-timeline'),
    finish: ctx => ctx.stop(),
  },

  // --- Grid and notes (Clip dock) ------------------------------------------
  G1: {
    part: 3, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.dock('clip'); return {cell: await ctx.inactiveCell()}; },
    act: (ctx, args) => ctx.tap(args.cell),
    check: args => document.querySelector(args.cell)?.getAttribute('data-active') === 'true',
  },
  G3: {
    part: 3, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.dock('clip');
      const step = await ctx.page.evaluate(() => document.querySelector('#detail-panel-clip a.step-number[aria-current=true]')?.textContent === '5' ? '6' : '5');
      return {step, link: `#detail-panel-clip .roll-row.ruler a.step-number:nth-child(${Number(step) + 1})`};
    },
    act: (ctx, args) => ctx.tap(args.link),
    check: args => document.querySelector('#detail-panel-clip a.step-number[aria-current=true]')?.textContent === args.step,
  },
  G4: {
    part: 3, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.dock('clip');
      // A notes pattern keeps articulation in a closed details; the acid
      // pattern of this fixture shows the form directly.
      await ctx.openDetails('#step-inspector form:has(input[name=chance])');
      const chance = pass === 'open' ? '90' : '80';
      await ctx.page.locator('#step-inspector input[name=chance]').fill(chance);
      return {chance, button: '#step-inspector form:has(input[name=chance]) button[type=submit]'};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: (args, base) => window.__lf.revision() !== base.revision && document.querySelector('#step-inspector input[name=chance]')?.value === args.chance,
    snapshot: snapshots.revision,
  },
  G5: {
    // The chord editor renders for notes patterns only: the placement
    // fixture's tide (a pad) is one; the scene fixture's patterns are acid.
    fixture: 'placement', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(TIDE); await ctx.dock('clip');
      await ctx.openDetails('#step-inspector form:has(select[name=chord_root])');
      await ctx.page.locator('#step-inspector select[name=chord_root]').selectOption(pass === 'open' ? '2' : '4');
      return {button: '#step-inspector form:has(select[name=chord_root]) button[type=submit]'};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: checks.saved, snapshot: snapshots.revision,
  },
  G6: {
    part: 3, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.dock('clip');
      await ctx.openDetails('#detail-panel-clip details:has(select[name=operation])');
      await ctx.page.locator('#detail-panel-clip select[name=operation]').selectOption('reverse');
      return {button: '#detail-panel-clip form:has(select[name=operation]) button[type=submit]'};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: checks.saved, snapshot: snapshots.revision,
  },
  G7: {
    part: 3, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.dock('clip');
      await ctx.openDetails('#detail-panel-clip details:has(input[name=swing])');
      const form = '#detail-panel-clip form:has(input[name=swing])';
      await ctx.page.locator(`${form} input[name=swing]`).fill(pass === 'open' ? '55' : '60');
      return {button: `${form} button[type=submit]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: checks.saved, snapshot: snapshots.revision,
  },
  G8: {
    part: 3, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.dock('clip');
      await ctx.openDetails('#detail-panel-clip details.pattern-management:has(input[name=newName]):has(select[name=starter])');
      const name = `walk-${pass}`;
      const form = '#detail-panel-clip details.pattern-management form:has(select[name=starter])';
      await ctx.page.locator(`${form} input[name=newName]`).fill(name);
      return {name, button: `${form} button[type=submit]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: args => !!document.querySelector(`a.pattern-choice[data-gosx-key="pattern-${args.name}"]`),
  },
  G11: {
    // The result is the service's answer, shown by the Quantize form: on this
    // acid pattern the service refuses with a reason (for example "tuplets
    // expand beyond 64 cells"), and nothing changes.
    part: 3, fixture: 'scene', input: {type: 'keydown', key: 'KeyU'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.dock('clip'); await ctx.blur(); },
    act: ctx => ctx.page.keyboard.press(`${mod}+u`),
    check: () => window.__lf.replied('/__actions/notes') && document.querySelector('#notes-quantize')?.getAttribute('data-gosx-form-state') !== 'submitting',
    snapshot: () => ({revision: window.__lf.revision(), status: document.querySelector('.notes-quantize-status')?.textContent.trim() || ''}),
  },
  G12: {
    part: 3, fixture: 'scene', input: {type: 'keydown', key: 'Enter'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.dock('clip');
      const input = ctx.page.locator('#detail-panel-clip form[method=get] input[name=octave]');
      const octave = String(Number(await input.inputValue()) === 3 ? 4 : 3);
      await input.fill(octave);
      await input.focus();
      return {octave};
    },
    act: ctx => ctx.page.keyboard.press('Enter'),
    check: args => document.querySelector('#detail-panel-clip .roll-row[data-gosx-key^="pitch-"] form input[name=octave]')?.value === args.octave,
    snapshot: () => ({first: document.querySelector('#detail-panel-clip .roll-row[data-gosx-key^="pitch-"]')?.getAttribute('data-gosx-key')}),
  },
  // --- Session view ------------------------------------------------------------
  S1: {
    fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.view('session'); await ctx.play(); return {button: '#view-panel-session th[data-live-scene="2"] button'}; },
    act: (ctx, args) => ctx.tap(args.button),
    check: checks.replied, snapshot: snapshots.revision,
    args: {path: '/__actions/launch'},
    finish: ctx => ctx.stop(),
  },
  S2: {
    fixture: 'scene', input: {type: 'keydown', key: 'F2'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.view('session'); await ctx.play(); await ctx.page.locator('#studio-shell').click({position: {x: 2, y: 2}}); },
    act: ctx => ctx.page.keyboard.press('F2'),
    check: checks.replied,
    args: {path: '/__actions/launch'},
    finish: ctx => ctx.stop(),
  },
  S3: {
    fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.view('session'); await ctx.play();
      await ctx.page.locator('#view-panel-session [data-live-note="50"]:not([disabled])').waitFor();
    },
    act: ctx => ctx.tap('#view-panel-session [data-live-note="50"]'),
    check: checks.replied,
    args: {path: '/__actions/live', body: 'note'},
    finish: ctx => ctx.stop(),
  },

  S5: {
    fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.view('session');
      // Record into a pattern of its own (G8 makes one per pass): keeping a
      // take turns its pattern into a performance, and later steps need the
      // continuity pattern's step grid.
      const pattern = ctx.page.locator('#view-panel-session select[data-live-pattern]');
      const own = await pattern.locator(`option[value="walk-${pass}"]`).count() ? `walk-${pass}` : 'alternate';
      await pattern.selectOption(own);
      await ctx.recordNote();
      await ctx.page.locator('[data-live-control=finish]').click();
      await ctx.page.locator('.note-preview').waitFor({timeout: 15_000});
      await ctx.stop();
      return {button: '.note-preview button[name=intent][value=commit]:not([disabled]), .note-preview button[name=intent][value=played]:not([disabled])'};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: (args, base) => !document.querySelector('.note-preview') && window.__lf.revision() !== base.revision,
    snapshot: snapshots.revision,
  },
  S7: {
    fixture: 'scene', input: {type: 'pointerdown'}, background: {method: 'POST', path: /^\/__actions\/note-checkpoint$/, count: 1, ms: 3000},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.view('session'); await ctx.play();
      await ctx.page.locator('[data-live-control=record]:not([disabled])').waitFor();
      await ctx.page.locator('[data-live-control=record]').click();
      await ctx.page.locator('[data-live-control=record][aria-pressed=true]').waitFor();
    },
    act: ctx => ctx.tap('#view-panel-session [data-live-note="55"]'),
    check: () => /Recording\D+[1-9]/.test(document.querySelector('[data-live-status]')?.textContent || ''),
    finish: async ctx => {
      // Leave no take behind: finish it and discard the review.
      await ctx.page.locator('[data-live-control=finish]').click();
      await ctx.page.locator('.note-preview').waitFor({timeout: 15_000});
      await ctx.stop();
      await ctx.page.locator('.note-preview button[value=discard]').click();
      await ctx.page.locator('.note-preview').waitFor({state: 'hidden'});
    },
  },

  // --- Undo and Redo ----------------------------------------------------------
  U1: {
    fixture: 'scene', input: {type: 'keydown', key: 'KeyZ'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.dock('clip');
      const cell = await ctx.inactiveCell();
      await ctx.tap(cell);
      await ctx.page.locator(`${cell}[data-active=true]:not([data-workspace-pending])`).waitFor();
      await ctx.settle();
      await ctx.blur();
      return {cell};
    },
    act: ctx => ctx.page.keyboard.press(`${mod}+z`),
    check: (args, base) => document.querySelector(args.cell)?.getAttribute('data-active') === 'false' && window.__lf.revision() !== base.revision,
    snapshot: snapshots.revision,
  },
  U2: {
    fixture: 'scene', input: {type: 'keydown', key: 'KeyZ'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.dock('clip');
      const cell = await ctx.inactiveCell();
      await ctx.tap(cell);
      await ctx.page.locator(`${cell}[data-active=true]:not([data-workspace-pending])`).waitFor();
      await ctx.settle();
      await ctx.blur();
      await ctx.page.keyboard.press(`${mod}+z`);
      await ctx.page.locator(`${cell}[data-active=false]:not([data-workspace-pending])`).waitFor();
      await ctx.settle();
      await ctx.blur();
      return {cell};
    },
    act: ctx => ctx.page.keyboard.press(`${mod}+Shift+z`),
    check: (args, base) => document.querySelector(args.cell)?.getAttribute('data-active') === 'true' && window.__lf.revision() !== base.revision,
    snapshot: snapshots.revision,
  },

  // --- View and tab switches -------------------------------------------------
  V1: {
    part: 3, fixture: 'scene', input: {type: 'keydown', key: 'Digit2'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.view('arrange'); await ctx.blur(); },
    act: ctx => ctx.page.keyboard.press('Alt+Shift+Digit2'),
    check: () => document.querySelector('#view-tab-session')?.getAttribute('aria-selected') === 'true' && window.__lf.shown('#view-panel-session'),
  },
  V2: {
    part: 3, fixture: 'scene', input: {type: 'keydown', key: 'Digit5'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.dock('clip'); await ctx.blur(); },
    act: ctx => ctx.page.keyboard.press('Alt+Shift+Digit5'),
    check: () => document.querySelector('#detail-tab-device')?.getAttribute('aria-selected') === 'true' && window.__lf.shown('#detail-panel-device'),
  },
  V3: {
    part: 3, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.pane('code', true); },
    act: ctx => ctx.tap('.shell-pane-toggle[data-pane=code][data-pane-action=hide]'),
    check: () => window.__lf.shown('.shell-pane-toggle[data-pane=code][data-pane-action=show]') && !window.__lf.shown('.shell-pane-toggle[data-pane=code][data-pane-action=hide]'),
    finish: ctx => ctx.pane('code', true),
  },
  V4: {
    part: 3, fixture: 'scene', input: {type: 'keydown', key: 'ArrowLeft'},
    prepare: async ctx => {
      await ctx.open(SCENE);
      await ctx.page.locator('.gsx-split__handle[aria-orientation=vertical]').first().focus();
    },
    act: ctx => ctx.page.keyboard.press('ArrowLeft'),
    check: (args, base) => document.querySelector('.gsx-split__handle[aria-orientation=vertical]')?.getAttribute('aria-valuenow') !== base.value,
    snapshot: () => ({value: document.querySelector('.gsx-split__handle[aria-orientation=vertical]')?.getAttribute('aria-valuenow')}),
    finish: ctx => ctx.page.keyboard.press('ArrowRight'),
  },
  V5: {
    part: 3, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: ctx => ctx.open(SCENE),
    act: ctx => ctx.tap('button[aria-controls=history-dialog]'),
    check: () => window.__lf.shown('#history-dialog'),
    finish: async ctx => { await ctx.page.keyboard.press('Escape'); await ctx.page.locator('#history-dialog').waitFor({state: 'hidden'}); },
  },
  V6: {
    part: 3, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => {
      await ctx.open(SCENE);
      await ctx.page.setViewportSize({width: 390, height: 844});
      await ctx.page.locator('.shell-phone-nav').waitFor();
      const region = await ctx.page.evaluate(() => document.querySelector('#studio-shell')?.getAttribute('data-phone-region') === 'detail' ? 'center' : 'detail');
      return {region};
    },
    act: (ctx, args) => ctx.tap(`.shell-phone-nav a[data-region="${args.region}"], .shell-phone-nav a[href*="region=${args.region}"]`),
    check: args => document.querySelector('#studio-shell')?.getAttribute('data-phone-region') === args.region,
    finish: ctx => ctx.page.setViewportSize({width: 1440, height: 900}),
  },

  // --- Selection ---------------------------------------------------------------
  Se1: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.dock('clip');
      const current = await ctx.page.evaluate(() => document.querySelector('#detail-panel-clip .piano-roll')?.getAttribute('data-pattern-id'));
      const target = current === 'alternate' ? 'continuity' : 'alternate';
      return {target, link: `a.pattern-choice[data-gosx-key="pattern-${target}"]`};
    },
    act: (ctx, args) => ctx.tap(args.link),
    check: args => document.querySelector('#detail-panel-clip .piano-roll')?.getAttribute('data-pattern-id') === args.target,
  },

  // --- Arrange (placement fixture) ---------------------------------------------
  A1: {
    fixture: 'placement', input: {type: 'pointerdown'},
    prepare: async ctx => {
      await ctx.open(PLACEMENT); await ctx.view('arrange');
      const target = await ctx.page.evaluate(() => document.querySelector('[data-placement-block="far-in"]')?.getAttribute('data-placement-selected') === 'true' ? 'near-in' : 'far-in');
      return {target, link: `[data-placement-block="${target}"] a.placement-select`};
    },
    act: (ctx, args) => ctx.tap(args.link),
    // The result: the block shows selected and its inspector opens. (Checked:
    // the Clip dock does not follow a placement selection, with or without
    // the server.)
    check: args => document.querySelector(`[data-placement-block="${args.target}"]`)?.getAttribute('data-placement-selected') === 'true' && !!document.querySelector(`details[data-placement-inspector="${args.target}"][open]`),
  },
  A2: {
    fixture: 'placement', input: {type: 'pointermove'},
    prepare: async ctx => {
      await ctx.open(PLACEMENT_SELECTED); await ctx.view('arrange');
      const box = await ctx.grab('[data-placement-block="near-home"] a.placement-select');
      return {box};
    },
    act: (ctx, args) => ctx.page.mouse.move(args.box.x + 90, args.box.y, {steps: 3}),
    check: (args, base) => document.querySelector('[data-placement-block="near-home"]')?.getBoundingClientRect().left !== base.left,
    snapshot: () => ({left: document.querySelector('[data-placement-block="near-home"]')?.getBoundingClientRect().left}),
    finish: async ctx => { await ctx.page.keyboard.press('Escape'); await ctx.page.mouse.up(); },
  },
  A3: {
    fixture: 'placement', input: {type: 'pointerup'},
    prepare: async ctx => {
      await ctx.open(PLACEMENT_SELECTED); await ctx.view('arrange');
      const box = await ctx.grab('[data-placement-block="near-home"] a.placement-select');
      await ctx.page.mouse.move(box.x + 90, box.y, {steps: 3});
      return {box};
    },
    act: ctx => ctx.page.mouse.up(),
    check: (args, base) => document.querySelector('[data-placement-block="near-home"]')?.getAttribute('data-placement-at') !== base.at,
    snapshot: () => ({at: document.querySelector('[data-placement-block="near-home"]')?.getAttribute('data-placement-at')}),
  },
  A4: {
    fixture: 'placement', input: {type: 'keydown', key: 'ArrowRight'},
    prepare: async ctx => { await ctx.open(PLACEMENT_SELECTED); await ctx.view('arrange'); await ctx.page.locator('[data-placement-block="near-home"] a.placement-select').focus(); },
    act: ctx => ctx.page.keyboard.press('ArrowRight'),
    check: (args, base) => document.querySelector('[data-placement-block="near-home"]')?.getAttribute('data-placement-at') !== base.at,
    snapshot: () => ({at: document.querySelector('[data-placement-block="near-home"]')?.getAttribute('data-placement-at')}),
  },
  A5: {
    fixture: 'placement', input: {type: 'keydown', key: 'KeyD'},
    prepare: async ctx => { await ctx.open(PLACEMENT_SELECTED); await ctx.view('arrange'); await ctx.page.locator('#view-tab-arrange').focus(); },
    act: ctx => ctx.page.keyboard.press(`${mod}+d`),
    check: (args, base) => document.querySelectorAll('[data-placement-block]').length > base.count,
    snapshot: () => ({count: document.querySelectorAll('[data-placement-block]').length}),
  },
  A6: {
    fixture: 'placement', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(PLACEMENT_SELECTED); await ctx.view('arrange');
      const form = 'details[data-placement-inspector="near-home"] form:has(input[name=length])';
      await ctx.openDetails('details[data-placement-inspector="near-home"]');
      const length = pass === 'open' ? '3bars' : '2bars';
      await ctx.page.locator(`${form} input[name=length]`).fill(length);
      return {button: `${form} button[type=submit]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: (args, base) => document.querySelector('[data-placement-block="near-home"]')?.getAttribute('data-placement-length') !== base.length,
    snapshot: () => ({length: document.querySelector('[data-placement-block="near-home"]')?.getAttribute('data-placement-length')}),
  },
  A10: {
    fixture: 'placement', input: {type: 'pointerdown'},
    prepare: async ctx => { await ctx.open(PLACEMENT); await ctx.view('arrange'); await ctx.page.evaluate(() => { for (const el of document.querySelectorAll('#view-panel-arrange, #studio-center')) el.scrollTop = 0; }); },
    act: ctx => ctx.tap('#view-panel-arrange a[href="#vocals"]'),
    check: (args, base) => { const box = document.querySelector('#vocals')?.getBoundingClientRect(); return !!box && box.top !== base.top && box.top < innerHeight && box.bottom > 0; },
    snapshot: () => ({top: document.querySelector('#vocals')?.getBoundingClientRect().top}),
  },

  // --- Mixer ------------------------------------------------------------------
  M1: {
    part: 2, fixture: 'scene', input: {type: 'keydown', key: 'ArrowRight'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.view('mixer');
      await ctx.page.locator('#view-panel-mixer .strip[data-mixer-strip=track] .strip-pan input[type=range]').first().focus();
    },
    act: ctx => ctx.page.keyboard.press('ArrowRight'),
    check: (args, base) => document.querySelector('#view-panel-mixer .strip[data-mixer-strip=track] .strip-pan-readout')?.textContent !== base.readout,
    snapshot: () => ({readout: document.querySelector('#view-panel-mixer .strip[data-mixer-strip=track] .strip-pan-readout')?.textContent}),
    // The strip saves 400 ms after the key (row M2), then reloads the page
    // state; both belong to M2.
    others: [{row: 'M2', path: /^\/__actions\/mixer$/}, {row: 'M2', path: /^\/studio$/}],
    settled: ctx => ctx.page.evaluate(() => window.__lf.net.slice(window.__lf.mark).some(entry => entry.path === '/__actions/mixer' && entry.end != null)),
  },
  M3: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.view('mixer'); },
    act: ctx => ctx.tap('#view-panel-mixer .strip[data-mixer-strip=track] button[data-toggle=mute]'),
    check: (args, base) => document.querySelector('#view-panel-mixer .strip[data-mixer-strip=track] button[data-toggle=mute]')?.getAttribute('aria-pressed') !== base.pressed && window.__lf.revision() !== base.revision,
    snapshot: () => ({pressed: document.querySelector('#view-panel-mixer .strip[data-mixer-strip=track] button[data-toggle=mute]')?.getAttribute('aria-pressed'), revision: window.__lf.revision()}),
  },

  M2: {
    // The strip saves 400 ms after the last key, so the hold limit covers that wait.
    part: 2, fixture: 'scene', input: {type: 'keydown', key: 'ArrowLeft'}, holdMs: 1000,
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.view('mixer');
      await ctx.page.locator('#view-panel-mixer .strip[data-mixer-strip=track] .strip-pan input[type=range]').first().focus();
    },
    act: ctx => ctx.page.keyboard.press('ArrowLeft'),
    check: checks.saved, snapshot: snapshots.revision,
  },
  M4: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.view('mixer');
      const chain = '#view-panel-mixer .strip[data-mixer-strip=track] .effect-chain';
      const add = `${chain} form:has(input[name=operation][value=add])`;
      if (!(await ctx.page.locator(add).count())) {
        // An insert needs an effect of an insert kind; the scene fixture has
        // none, so make a chorus first (setup, not measured).
        const form = '#view-panel-mixer form:has(input[name=action][value=add-chorus])';
        await ctx.page.locator(`${form} input[name=name]`).fill('chorus-chain');
        await ctx.page.locator(`${form} button[type=submit]`).click();
        await ctx.page.locator(add).first().waitFor();
        await ctx.settle();
      }
      // The open pass adds the effect; the held pass removes it again.
      if (pass === 'open') return {button: `${add} button[type=submit]`};
      return {button: `${chain} li:first-child button[value=remove]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: checks.saved, snapshot: snapshots.revision,
  },
  M5: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.view('mixer');
      const form = '#view-panel-mixer form:has(input[name=action][value=add-chorus])';
      const name = `chorus-${pass}`;
      await ctx.page.locator(`${form} input[name=name]`).fill(name);
      return {name, button: `${form} button[type=submit]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: checks.saved, snapshot: snapshots.revision,
  },
  M6: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.dock('device');
      const form = '#detail-panel-device form:has(input[name=newName][value=cutoff])';
      const value = pass === 'open' ? '900' : '800';
      await ctx.page.locator(`${form} input[name=value]`).fill(value);
      return {value, form, button: `${form} button[value=set-parameter]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: (args, base) => window.__lf.revision() !== base.revision && document.querySelector(`${args.form} input[name=value]`)?.value === args.value,
    snapshot: snapshots.revision,
  },

  // --- Code and Collaborate ------------------------------------------------------
  C1: {
    part: 3, fixture: 'scene', input: {type: 'keydown', key: 'x'},
    prepare: ctx => ctx.codeEnd(),
    act: ctx => ctx.page.keyboard.press('x'),
    check: (args, base) => document.querySelector('#editor-content')?.value.length > base.length,
    snapshot: () => ({length: document.querySelector('#editor-content')?.value.length ?? 0}),
    finish: ctx => ctx.codeRevert(),
  },
  C2: {
    part: 3, fixture: 'scene', input: {type: 'keydown', key: 'x'}, background: {method: 'POST', path: /^\/__code\/analysis$/, count: 1, ms: 2000},
    prepare: ctx => ctx.codeEnd(),
    act: ctx => ctx.page.keyboard.press('x'),
    check: (args, base) => document.querySelector('#editor-content')?.value.length > base.length,
    snapshot: () => ({length: document.querySelector('#editor-content')?.value.length ?? 0}),
    finish: ctx => ctx.codeRevert(),
  },
  C3: {
    part: 3, fixture: 'scene', input: {type: 'keydown', key: 'x'}, background: {method: 'POST', path: /^\/__actions\/source-checkpoint$/, count: 1, ms: 4000},
    prepare: ctx => ctx.codeEnd(),
    act: ctx => ctx.page.keyboard.press('x'),
    check: (args, base) => document.querySelector('#editor-content')?.value.length > base.length,
    snapshot: () => ({length: document.querySelector('#editor-content')?.value.length ?? 0}),
    finish: ctx => ctx.codeRevert(),
  },
  C4: {
    part: 3, fixture: 'scene', input: {type: 'keydown', key: 'KeyS'},
    prepare: async (ctx, pass) => { await ctx.codeEnd(); await ctx.page.keyboard.type(`\n// local-first ${pass}\n`); },
    act: ctx => ctx.page.keyboard.press(`${mod}+s`),
    check: checks.saved,
    snapshot: snapshots.revision,
  },
  C5: {
    part: 3, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => {
      // A save re-renders the editor without its Check button until the page
      // loads again, so start from a fresh page.
      await ctx.open(CODE, {force: true});
      await ctx.codeEnd(CODE); await ctx.page.keyboard.type('\n// check this draft\n');
    },
    act: ctx => ctx.tap('#editor-native-form button[name=intent][value=check]'),
    check: () => document.querySelector('#editor-native-form')?.getAttribute('data-gosx-form-state') === 'success',
    finish: ctx => ctx.codeRevert(),
  },
  C8: {
    part: 3, fixture: 'scene', input: {type: 'keydown', key: 'y'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.pane('code', true);
      const editor = ctx.page.locator('[data-collaboration-editor]:not([readonly])').first();
      await editor.waitFor();
      await editor.focus();
      await ctx.page.keyboard.press('End');
    },
    act: ctx => ctx.page.keyboard.press('y'),
    check: (args, base) => (document.querySelector('[data-collaboration-editor]')?.value ?? '').length > base.length,
    snapshot: () => ({length: (document.querySelector('[data-collaboration-editor]')?.value ?? '').length}),
  },
  C9: {
    part: 3, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.pane('code', true); await ctx.reveal('#collaboration button:text-is("Save shared score")'); },
    act: ctx => ctx.tap('#collaboration button:text-is("Save shared score")'),
    check: checks.replied,
    args: {path: '/api/collaboration/save'},
  },

  // --- Library ---------------------------------------------------------------------
  L1: {
    part: 2, fixture: 'scene', input: {type: 'keydown', key: 'Enter'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.pane('library', true);
      await ctx.openDetails('#browser-instruments');
      await ctx.page.locator('#cicada-sounds[data-sounds-ready=true] [data-sound-preview="warm-pad"][aria-pressed=false]').focus();
    },
    act: ctx => ctx.page.keyboard.press('Enter'),
    check: () => document.querySelector('[data-sound-preview="warm-pad"]')?.textContent.trim() === 'Stop',
    finish: async ctx => { await ctx.page.keyboard.press('Escape'); await ctx.page.locator('[data-sound-preview="warm-pad"][aria-pressed=false]').waitFor(); },
  },
  L2: {
    part: 2, fixture: 'scene', input: {type: 'keydown', key: 'Escape'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.pane('library', true);
      await ctx.openDetails('#browser-instruments');
      const listen = ctx.page.locator('#cicada-sounds[data-sounds-ready=true] [data-sound-preview="warm-pad"]');
      await listen.focus();
      await ctx.page.keyboard.press('Enter');
      await ctx.page.waitForFunction(() => document.querySelector('[data-sound-preview="warm-pad"]')?.textContent.trim() === 'Stop');
    },
    act: ctx => ctx.page.keyboard.press('Escape'),
    check: () => document.querySelector('[data-sound-preview="warm-pad"]')?.getAttribute('aria-pressed') === 'false',
  },
  L3: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.pane('library', true);
      const form = '#browser-instruments form:has(input[name=pattern][value=soft-bell])';
      await ctx.openDetails(form);
      const name = `bell-${pass}`;
      await ctx.page.locator(`${form} input[name=newName]`).fill(name);
      await ctx.page.locator(`${form} input[name=track]`).fill(name);
      return {button: `${form} button[type=submit]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: checks.saved, snapshot: snapshots.revision,
  },
  L4: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.pane('library', true);
      const form = '#browser-instruments form[action="/__actions/preset-save"]';
      await ctx.openDetails(form);
      await ctx.page.locator(`${form} input[name=name]`).fill(`kit-${pass}`);
      return {button: `${form} button[type=submit]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: checks.saved, snapshot: snapshots.revision,
  },
  L8: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.pane('library', true); },
    act: ctx => ctx.tap('#browser-generate > summary'),
    check: (args, base) => document.querySelector('#browser-generate')?.open !== base.open,
    snapshot: () => ({open: document.querySelector('#browser-generate')?.open}),
  },
  L11: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.pane('library', true);
      const form = '#generator form[action="/__actions/generate"]';
      await ctx.openDetails(form);
      await ctx.page.locator(`${form} input[name=seed]`).fill(pass === 'open' ? '101' : '202');
      return {button: `${form} button[type=submit]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: (args, base) => (document.querySelector('#generator pre.history-diff')?.textContent ?? '') !== base.preview && !!document.querySelector('#generator pre.history-diff'),
    snapshot: () => ({preview: document.querySelector('#generator pre.history-diff')?.textContent ?? ''}),
  },
  L12: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => { await ctx.open(MACHINE); await ctx.page.locator('[data-machine-preview]').waitFor(); },
    act: ctx => ctx.tap('button.machine-vary'),
    check: (args, base) => document.querySelector('[data-machine-preview]')?.innerHTML !== base.preview,
    snapshot: () => ({preview: document.querySelector('[data-machine-preview]')?.innerHTML}),
  },

  // --- Automation dock -------------------------------------------------------------
  Au1: {
    fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.dock('automation');
      const select = ctx.page.locator('#detail-panel-automation form[action="/studio"] select[name=curvePath]');
      const options = await select.locator('option').evaluateAll(nodes => nodes.map(node => node.value));
      const current = await select.inputValue();
      const target = options.find(value => value !== current);
      if (!target) throw new Error('the automation dock offers one parameter');
      await select.selectOption(target);
      return {target, button: '#detail-panel-automation form[action="/studio"]:has(select[name=curvePath]) button[type=submit]'};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: args => document.querySelector('#detail-panel-automation select[name=curvePath]')?.value === args.target && new URL(location.href).searchParams.get('curvePath') === args.target,
  },
  Au2: {
    fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.dock('automation'); await ctx.page.locator('#cicada-automation-curve[data-automation-ready=true]').waitFor(); },
    act: ctx => ctx.tap('#detail-panel-automation [data-automation-new]'),
    check: (args, base) => (document.querySelector('#detail-panel-automation .automation-point-form input[name=at]')?.value ?? '') !== base.at || document.querySelectorAll('#detail-panel-automation [data-curve-point]').length !== base.points,
    snapshot: () => ({at: document.querySelector('#detail-panel-automation .automation-point-form input[name=at]')?.value ?? '', points: document.querySelectorAll('#detail-panel-automation [data-curve-point]').length}),
  },
  Au3: {
    fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.dock('automation');
      const form = '#detail-panel-automation form:has(input[name=oldAt])';
      await ctx.page.locator(`${form} input[name=at]`).fill(pass === 'open' ? '@2.1.1' : '@3.1.1');
      await ctx.page.locator(`${form} input[name=value]`).fill('-9dB');
      return {button: `${form} button[type=submit]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: checks.saved, snapshot: snapshots.revision,
  },
  Au4: {
    fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.dock('automation');
      const form = '#detail-panel-automation form:has(input[name=action][value=mod-source-add])';
      await ctx.openDetails(form);
      await ctx.page.locator(`${form} input[name=name]`).fill(`wobble-${pass}`);
      return {button: `${form} button[type=submit]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: checks.saved, snapshot: snapshots.revision,
  },
  Au6: {
    fixture: 'scene', input: {type: 'none'}, background: {method: 'GET', path: /^\/api\/automation\/recording$/, count: 1, ms: 2500},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.dock('automation'); },
    act: async () => {},
    check: () => !!document.querySelector('#detail-panel-automation'),
  },

  // --- Recording, capture and export -------------------------------------------------
  Rc2: {
    fixture: 'placement', input: {type: 'none'}, background: {method: 'GET', path: /^\/api\/capture$/, count: 1, ms: 2500},
    prepare: ctx => ctx.open(PLACEMENT),
    act: async () => {},
    check: () => !!document.querySelector('[data-gosx-command="transport.record"]'),
  },
  Rc4: {
    fixture: 'placement', input: {type: 'pointerdown'},
    prepare: async ctx => {
      await ctx.open(PLACEMENT);
      await ctx.page.locator('button.shell-status__trigger[aria-controls=audio-dialog]').click();
      await ctx.page.locator('#audio-dialog:not([hidden])').waitFor();
      const select = ctx.page.locator('#audio-dialog select[name=periodFrames]');
      const options = await select.locator('option').evaluateAll(nodes => nodes.map(node => node.value));
      const current = await select.inputValue();
      await select.selectOption(options.find(value => value !== current));
      return {button: '#audio-dialog form[action="/__actions/audio"] button[type=submit]'};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: () => window.__lf.replied('/__actions/audio'),
    finish: ctx => ctx.closeDialog('audio-dialog'),
  },
  Rc5: {
    fixture: 'placement', input: {type: 'pointerdown'}, background: {method: 'GET', path: /^\/api\/audio\/config$/, count: 1, ms: 2000},
    prepare: ctx => ctx.open(PLACEMENT),
    act: ctx => ctx.tap('button.shell-status__trigger[aria-controls=audio-dialog]'),
    check: () => window.__lf.shown('#audio-dialog'),
    finish: ctx => ctx.closeDialog('audio-dialog'),
  },
  Rc7: {
    fixture: 'placement', input: {type: 'pointerdown'},
    prepare: async ctx => {
      await ctx.open(PLACEMENT);
      await ctx.page.locator('button[aria-controls=export-dialog]').click();
      await ctx.page.locator('#export-dialog:not([hidden])').waitFor();
      await ctx.openDetails('#export-dialog form[action="/__actions/export-midi"]');
      return {button: '#export-dialog form[action="/__actions/export-midi"] button[type=submit]'};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: () => window.__lf.replied('/__actions/export-midi') && document.querySelector('#export-dialog form[action="/__actions/export-midi"]')?.getAttribute('data-gosx-form-state') !== 'submitting',
    finish: ctx => ctx.closeDialog('export-dialog'),
  },
  Rc8: {
    fixture: 'placement', input: {type: 'none'}, background: {method: 'GET', path: /^\/api\/export(\/stems|\/midi)?$/, count: 1, ms: 2500},
    prepare: ctx => ctx.open(PLACEMENT),
    act: async () => {},
    check: () => !!document.querySelector('#export-dialog'),
  },

  // --- History, view, selection, zoom (late: they read earlier edits) ----------------
  U3: {
    part: 3, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => {
      // Make an edit to revert, then load the page so the History dialog lists it.
      await ctx.open(SCENE); await ctx.dock('clip');
      const cell = await ctx.inactiveCell();
      await ctx.tap(cell);
      await ctx.page.locator(`${cell}[data-active=true]:not([data-workspace-pending])`).waitFor();
      await ctx.settle();
      await ctx.open(SCENE, {force: true});
      await ctx.page.locator('button.shell-status__trigger[aria-controls=history-dialog]').click();
      await ctx.page.locator('#history-dialog:not([hidden])').waitFor();
      // The newest edit has the largest id.
      const id = await ctx.page.evaluate(() => Math.max(...[...document.querySelectorAll('#history-dialog form input[name=id]')].map(input => Number(input.value))));
      const form = `#history-dialog form:has(input[name=id][value="${id}"])`;
      await ctx.openDetails(form);
      return {button: `${form} button[type=submit]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: checks.saved, snapshot: snapshots.revision,
    finish: ctx => ctx.closeDialog('history-dialog'),
  },
  V7: {
    // After an edit, hidden update regions are stale and the shell reads them
    // when shown or after 250 ms idle. The edit itself (a piano-roll cell, row
    // G1) paints at once; the step watches for the region read.
    part: 2, fixture: 'scene', input: {type: 'pointerdown'}, background: {method: 'GET', path: /^\/__studio\/regions$/, count: 1, ms: 2500},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.view('arrange'); await ctx.dock('clip'); return {cell: await ctx.inactiveCell()}; },
    act: (ctx, args) => ctx.tap(args.cell),
    check: args => document.querySelector(args.cell)?.getAttribute('data-active') === 'true',
  },
  V8: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.dock('clip');
      // A write that names a stale revision is refused (409) and blocks the
      // edit queue until "Refresh saved state".
      const stale = ctx.originalRevision;
      let rewritten = false;
      const rewrite = async route => {
        const request = route.request();
        if (rewritten || request.method() !== 'POST' || !/json/.test(request.headers()['content-type'] || '')) return route.fallback();
        rewritten = true;
        const payload = request.postDataJSON();
        payload.revision = stale;
        return route.fallback({postData: JSON.stringify(payload)});
      };
      await ctx.page.route('**/__actions/pattern', rewrite);
      const before = await ctx.page.evaluate(() => window.__lf.net.length);
      await ctx.tap(await ctx.inactiveCell());
      await ctx.page.locator('[data-workspace-retry]:not([hidden])').waitFor({timeout: 5_000}).finally(() => ctx.page.unroute('**/__actions/pattern', rewrite)).catch(async error => {
        const sent = await ctx.page.evaluate(before => window.__lf.net.slice(before).filter(entry => entry.method !== 'GET' || !entry.path.startsWith('/api/')).map(entry => `${entry.method} ${entry.path} ${entry.status ?? ''} ${entry.reply ?? ''}`), before);
        throw new Error(`${error.message.split('\n')[0]}; the last requests: ${sent.join('; ')}; status: ${await ctx.page.locator('[data-workspace-status]').first().textContent().catch(() => '?')}`);
      });
    },
    act: ctx => ctx.tap('[data-workspace-retry]'),
    check: () => !window.__lf.shown('[data-workspace-retry]') && /updated/i.test(document.querySelector('[data-workspace-status]')?.textContent || ''),
  },
  Z1: {
    fixture: 'placement', input: {type: 'keydown', key: 'ArrowDown'},
    prepare: async ctx => {
      await ctx.open(PLACEMENT); await ctx.view('arrange');
      const select = ctx.page.locator('#view-panel-arrange select[name=placementZoom]');
      const last = await select.locator('option').evaluateAll(nodes => nodes.at(-1).value);
      if (await select.inputValue() === last) await select.selectOption({index: 0});
      await select.focus();
    },
    act: ctx => ctx.page.keyboard.press('ArrowDown'),
    check: (args, base) => document.querySelector('#view-panel-arrange .placement-timeline')?.getBoundingClientRect().width !== base.width,
    snapshot: () => ({width: document.querySelector('#view-panel-arrange .placement-timeline')?.getBoundingClientRect().width}),
  },
  Z2: {
    fixture: 'placement', input: {type: 'wheel'},
    prepare: async ctx => {
      await ctx.open(PLACEMENT); await ctx.view('arrange');
      await ctx.page.evaluate(() => { const scroll = document.querySelector('#view-panel-arrange .placement-scroll'); scroll.scrollLeft = 0; });
      // Over the bar ruler, not a placement link: hovering a link makes GoSX
      // prefetch it, which is not the scroll's traffic.
      const box = await ctx.page.locator('#view-panel-arrange .placement-scroll').boundingBox();
      const ruler = await ctx.page.locator('#view-panel-arrange .placement-ruler').first().boundingBox();
      const y = Math.min(Math.max(ruler ? ruler.y + ruler.height / 2 : box.y + 8, box.y + 4), box.y + box.height - 4, 890);
      await ctx.page.mouse.move(box.x + box.width / 2, y);
    },
    act: ctx => ctx.page.mouse.wheel(240, 0),
    check: (args, base) => document.querySelector('#view-panel-arrange .placement-scroll')?.scrollLeft !== base.left,
    snapshot: () => ({left: document.querySelector('#view-panel-arrange .placement-scroll')?.scrollLeft}),
  },
  Z4: {
    fixture: 'placement', input: {type: 'none'}, background: {method: 'GET', path: /^\/api\/transport$/, count: 3, ms: 1200},
    prepare: async ctx => {
      await ctx.open(PLACEMENT); await ctx.view('arrange');
      const follow = ctx.page.locator('#view-panel-arrange input[name=placementFollow]');
      if (!(await follow.isChecked())) await follow.check();
      await ctx.play();
    },
    act: async () => {},
    check: () => !!document.querySelector('#view-panel-arrange input[name=placementFollow]:checked'),
    finish: ctx => ctx.stop(),
  },
  T5: {
    fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async ctx => { await ctx.open(SCENE); await ctx.stop(); await ctx.view('arrange'); await ctx.openDetails('#view-panel-arrange form:has(button[name=action][value=loopSection])'); },
    act: ctx => ctx.tap('#view-panel-arrange button[name=action][value=loopSection]'),
    check: checks.playing, snapshot: snapshots.position,
    finish: ctx => ctx.stop(),
  },
  T6: {
    fixture: 'placement', input: {type: 'keydown', key: 'KeyL'},
    prepare: async ctx => { await ctx.open(PLACEMENT_SELECTED); await ctx.stop(); await ctx.view('arrange'); await ctx.page.locator('#view-tab-arrange').focus(); },
    act: ctx => ctx.page.keyboard.press(`${mod}+l`),
    check: checks.playing, snapshot: snapshots.position,
    finish: ctx => ctx.stop(),
  },
  A7: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.view('arrange');
      const form = '#view-panel-arrange form:has(input[name=action][value=bars])';
      await ctx.openDetails(`${form}`);
      await ctx.page.locator(`${form} input[name=bars]`).fill(pass === 'open' ? '998' : '997');
      return {button: `${form} button[type=submit]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: checks.saved, snapshot: snapshots.revision,
  },
  A8: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.view('arrange');
      const form = '#view-panel-arrange form[action="/__actions/project"]';
      await ctx.openDetails(form);
      const title = `Walkthrough ${pass}`;
      await ctx.page.locator(`${form} input[name=title]`).fill(title);
      return {title, button: `${form} button[type=submit]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: (args, base) => window.__lf.revision() !== base.revision && (document.querySelector('[data-update-region="project"]')?.textContent || '').includes(args.title),
    snapshot: snapshots.revision,
  },
  A9: {
    fixture: 'placement', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(PLACEMENT); await ctx.view('arrange');
      const form = ctx.page.locator('#view-panel-arrange form').filter({has: ctx.page.getByRole('button', {name: 'Add tempo step'})});
      await ctx.openDetails('#view-panel-arrange form:has(button[value=set-tempo])');
      await form.locator('input[name=bar]').fill(pass === 'open' ? '9' : '13');
      await form.locator('input[name=bpm]').fill('84');
    },
    act: ctx => ctx.page.locator('#view-panel-arrange').getByRole('button', {name: 'Add tempo step'}).click(),
    check: checks.saved, snapshot: snapshots.revision,
  },
  A13: {
    part: 2, fixture: 'scene', input: {type: 'pointerdown'},
    prepare: async (ctx, pass) => {
      await ctx.open(SCENE); await ctx.pane('library', true);
      await ctx.openDetails('#browser-takes');
      const form = '#browser-takes form:has(input[name=action][value=audio-track])';
      await ctx.page.locator(`${form} input[name=newName]`).fill(`audio-${pass}`);
      return {button: `${form} button[type=submit]`};
    },
    act: (ctx, args) => ctx.tap(args.button),
    check: checks.saved, snapshot: snapshots.revision,
  },
  S4: {
    fixture: 'scene', input: {type: 'pointerup'},
    prepare: async ctx => {
      await ctx.open(SCENE); await ctx.view('session'); await ctx.play();
      await ctx.page.locator('#view-panel-session [data-live-note="52"]:not([disabled])').waitFor();
      await ctx.grab('#view-panel-session [data-live-note="52"]');
      await ctx.page.locator('#view-panel-session [data-live-note="52"][aria-pressed=true]').waitFor();
    },
    act: ctx => ctx.page.mouse.up(),
    check: () => document.querySelector('#view-panel-session [data-live-note="52"]')?.getAttribute('aria-pressed') === 'false',
    finish: ctx => ctx.stop(),
  },
  Z3: {
    // Autoscroll starts after the pointer rests near the edge (about 270 ms
    // measured), so the hold limit covers that wait.
    fixture: 'placement', input: {type: 'pointermove'}, holdMs: 1000,
    prepare: async ctx => {
      await ctx.open(PLACEMENT_SELECTED); await ctx.view('arrange');
      await ctx.page.evaluate(() => { document.querySelector('#view-panel-arrange .placement-scroll').scrollLeft = 0; });
      const point = await ctx.grab('[data-placement-block="near-home"] a.placement-select');
      const box = await ctx.page.locator('#view-panel-arrange .placement-scroll').boundingBox();
      return {y: point.y, edge: box.x + box.width - 4};
    },
    act: (ctx, args) => ctx.page.mouse.move(args.edge, args.y, {steps: 4}),
    check: (args, base) => document.querySelector('#view-panel-arrange .placement-scroll')?.scrollLeft > base.left,
    snapshot: () => ({left: document.querySelector('#view-panel-arrange .placement-scroll')?.scrollLeft}),
    finish: async ctx => { await ctx.page.keyboard.press('Escape'); await ctx.page.mouse.up(); },
  },

  // --- Meters --------------------------------------------------------------------
  Me1: {
    fixture: 'placement', input: {type: 'none'}, background: {method: 'GET', path: /^\/api\/meters$/, count: 3, ms: 1000},
    prepare: async ctx => { await ctx.open(PLACEMENT); await ctx.view('mixer'); },
    act: async () => {},
    check: () => !!document.querySelector('#view-panel-mixer .strip-meter'),
  },
};

// Rows the walkthrough does not script yet, each with the reason. The totals
// count them with the audit's class and say so.
const UNSCRIPTED = {
  T4: 'the Play-from button fails on main: its form sends no scene, so the service answers 422 "song block is no longer in this position" (ws/arrangement.go:47, ws/actions.go:367, cmd/studio_transport.go:1420)',
  G2: 'neither fixture has a drum track, so no drum lane renders',
  G9: 'neither fixture has performance notes, so the performance note forms do not render',
  G10: 'neither fixture has performance notes, so the gesture, time, pitch-shape and group forms do not render',
  G13: 'pressing B sets the Select or Draw tool, but nothing on /studio reads it and the tool buttons stay hidden, so no result paints (checked: aria-pressed stays false)',
  S6: 'needs notes retained by an earlier Studio process; the walkthrough runs one process per fixture',
  S8: 'needs an incoming MIDI message: headless Chrome has no Web MIDI device and CI has no MIDI port',
  A11: 'needs a recorded vocal take; the fixtures have none',
  A12: 'needs a recorded vocal take; the fixtures have none',
  C6: 'both fixtures are single-file scores; switching needs a project with several files',
  C7: 'needs a recovery copy written by an earlier Studio process (code-recovery.test.cjs runs three)',
  L5: 'downloads a sample pack from a catalog URL over the network; CI has no catalog',
  L6: 'a native multipart upload that loads a new document; the probe does not follow a document load yet',
  L7: 'records from a microphone; CI runs Studio with --audio null',
  L9: 'the Sampler pads are empty in both fixtures; a pad needs a sampler bank with audio',
  L10: 'sampler edits act on pads with audio, and the save reloads the document; the probe does not follow a document load yet',
  Au5: 'records automation against playback and Keep reloads the document; the probe does not follow a document load yet',
  Rc1: 'records from an audio input; CI runs Studio with --audio null',
  Rc3: 'needs a recorded take pass; the fixtures have none',
  Rc6: 'needs a recorded take or a WAV upload; the fixtures have none',
  Rc9: 'a file download has no in-page result to paint; it is a request by definition',
  Se2: 'neither fixture has performance notes, gesture points or shared lanes',
  Mi1: 'needs a Web MIDI input; headless Chrome has none',
  Mi2: 'needs a native MIDI port; CI has none',
  Mi3: 'the native port list needs a MIDI host; CI has none',
  St1: 'the first document load; the probe does not follow a document load yet',
  St2: 'the engine load is part of the first document load; the probe does not follow a document load yet',
  St3: 'the engine mount reads run during the first document load; the probe does not follow a document load yet',
  St4: 'the startup status and reload guard run during a document load; the probe does not follow a document load yet',
};

// ---------------------------------------------------------------------------

function playwright() {
  const location = process.env.CICADA_PLAYWRIGHT_MODULE;
  if (!location) return require('playwright');
  try {
    const loaded = require(path.resolve(location));
    if (loaded.chromium) return loaded;
  } catch (error) {
    if (error.code !== 'MODULE_NOT_FOUND') throw error;
  }
  return require('node:module').createRequire(path.resolve(location))('playwright');
}

async function until(predicate, message, timeout = 15_000, every = 20) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise(resolve => setTimeout(resolve, every));
  }
  throw new Error(message);
}

// network installs the hold switch. HTTP requests are routed only while held,
// so the open pass keeps the browser cache and pays no interception cost.
// WebSockets are routed from the start: a socket opened before a hold must
// still be stoppable.
async function network(context) {
  const state = {holding: false, held: [], frames: [], documents: []};
  const handler = route => {
    if (state.holding) { state.held.push(route); return undefined; }
    return route.fallback();
  };
  await context.routeWebSocket(/.*/, socket => {
    const server = socket.connectToServer();
    socket.onMessage(message => { if (state.holding) state.frames.push(() => server.send(message)); else server.send(message); });
    server.onMessage(message => { if (state.holding) state.frames.push(() => socket.send(message)); else socket.send(message); });
  });
  context.on('request', request => {
    if (request.resourceType() === 'document') state.documents.push({at: Date.now(), method: request.method(), path: new URL(request.url()).pathname});
  });
  state.hold = async () => {
    state.holding = true;
    await context.route('**/*', handler);
  };
  state.release = async () => {
    state.holding = false;
    // Release in the order the page sent them, then stop routing.
    for (const route of state.held.splice(0)) await route.fallback().catch(() => {});
    for (const send of state.frames.splice(0)) { try { send(); } catch {} }
    await context.unroute('**/*', handler);
  };
  return state;
}

const ready = page => page.locator('#studio-shell[data-shell-actions=ready]').waitFor({timeout: 30_000});

// settle waits until the page has no request of its own in flight (polls and
// streams aside) and no pending marker, on three checks in a row.
// Requests that stay open by design (a stream) never settle.
const STREAMS = /^\/(api\/midi\/input|collaboration\/score)$/;

async function settle(page, extra) {
  let quiet = 0, last = '';
  await until(async () => {
    const state = await page.evaluate(() => ({
      open: window.__lf.net.filter(entry => entry.end == null && entry.kind !== 'websocket' && entry.kind !== 'eventsource').map(({method, path, body}) => ({method, path, body})),
      pending: document.querySelectorAll('[data-workspace-pending]').length,
      ready: !!document.querySelector('#studio-shell[data-shell-actions=ready]'),
    })).catch(() => null);
    const open = state ? state.open.filter(entry => !backgroundRow(entry) && !STREAMS.test(entry.path)) : [];
    const waiting = extra ? !(await extra()) : false;
    last = state ? `${open.map(entry => `${entry.method} ${entry.path}`).join(', ') || 'no request'}; ${state.pending} pending marks; engines ${state.ready ? 'ready' : 'not ready'}${waiting ? '; the step waits for its own condition' : ''}` : 'the page did not answer';
    quiet = state && open.length === 0 && state.pending === 0 && state.ready && !waiting ? quiet + 1 : 0;
    return quiet >= 3;
  }, 'the page did not settle', 15_000, 25).catch(error => { throw new Error(`${error.message}: ${last}`); });
}

// tap clicks the first visible match (a Playwright selector). Playwright's
// actionability waits run before the trusted pointerdown, so they are not
// part of the measured time.
async function tap(page, selector) {
  await page.locator(selector).filter({visible: true}).first().click({timeout: 5_000}).catch(async error => {
    // Say where the control is and what hides it.
    const where = await page.locator(selector).first().evaluate(node => {
      const box = node.getBoundingClientRect();
      const hidden = [];
      for (let el = node; el && el !== document.body; el = el.parentElement) {
        const style = getComputedStyle(el);
        if (el.hidden || style.display === 'none' || style.visibility === 'hidden' || el.getBoundingClientRect().width === 0) hidden.push(`${el.tagName.toLowerCase()}${el.id ? '#' + el.id : ''}.${[...el.classList].join('.')}${el.hidden ? '[hidden]' : ''} display=${style.display} width=${Math.round(el.getBoundingClientRect().width)}`);
      }
      return `box ${Math.round(box.x)},${Math.round(box.y)} ${Math.round(box.width)}x${Math.round(box.height)}; hidden by: ${hidden.slice(0, 3).join(' < ') || 'nothing'}`;
    }).catch(() => 'no element matches');
    throw new Error(`${error.message.split('\n')[0]} (${where})`);
  });
}

function checkSource(fn) { return `(${fn.toString()})`; }

async function runStep(ctx, step, pass) {
  const record = {row: step.id, pass, fixture: ctx.fixture};
  const args = {...(step.args || {}), ...((step.prepare ? await step.prepare(ctx, pass) : null) || {})};
  const {page, net} = ctx; // prepare opens the page on the first step
  await settle(page);
  const spec = {id: step.id, input: step.input, args, limitMs: pass === 'held' ? (step.holdMs || HOLD_MS) : OPEN_MS, limitFrames: pass === 'held' ? HOLD_FRAMES : 0};
  const mark = await page.evaluate(`(() => { window.__lf.arm(${JSON.stringify(spec)}, ${checkSource(step.check)}, ${step.snapshot ? checkSource(step.snapshot) : 'null'}); return window.__lf.mark; })()`);
  const documentsBefore = net.documents.length;
  const nodeStart = Date.now();
  if (pass === 'held') await net.hold();
  let result;
  try {
    await step.act(ctx, args);
    const handle = await page.waitForFunction(() => window.__lf.result, null, {timeout: spec.limitMs + 10_000, polling: 'raf'});
    result = await handle.jsonValue();
    await handle.dispose();
  } finally {
    if (pass === 'held') await net.release();
  }
  record.painted = result.painted;
  record.firstFrameMs = result.firstFrameMs == null ? null : +result.firstFrameMs.toFixed(1);
  record.ms = result.ms == null ? null : +result.ms.toFixed(1);
  record.frames = result.frames;
  if (result.error) record.checkError = result.error;
  if (pass === 'held') {
    // Recovery: with the network back, the result must arrive.
    record.recovered = await page.waitForFunction(`(() => { try { return !!(${checkSource(step.check)})(${JSON.stringify(args)}, window.__lf.base); } catch { return false; } })()`, null, {timeout: OPEN_MS, polling: 'raf'})
      .then(() => true, () => false);
    if (!record.recovered) {
      record.recoveryNote = await page.evaluate(mark => window.__lf.net.slice(mark).filter(entry => !/^\/api\/(meters|transport|capture|sampler|automation\/recording|export)/.test(entry.path)).map(entry => `${entry.method} ${entry.path} ${entry.status ?? (entry.end == null ? 'open' : '')} ${entry.reply ?? ''}`.trim()), mark).catch(error => [error.message]);
    }
  } else if (!result.painted) {
    // Say what the page sent and what the snapshot reads now, so a broken
    // step explains itself.
    const sent = await page.evaluate(mark => window.__lf.net.slice(mark).filter(entry => !/^\/api\/(meters|transport|capture|sampler|automation\/recording|export)/.test(entry.path)).map(entry => `${entry.method} ${entry.path}${entry.end == null ? ' (open)' : entry.status ? ` ${entry.status}` : ''}${entry.reply ? ` ${JSON.stringify(entry.reply)}` : ''}`), mark).catch(() => []);
    const now = step.snapshot ? await page.evaluate(`(${checkSource(step.snapshot)})(${JSON.stringify(args)})`).catch(error => error.message) : null;
    const before = await page.evaluate(() => window.__lf.base).catch(() => null);
    throw new Error(`the expected result did not show within ${OPEN_MS} ms with the network open${result.error ? ` (check threw: ${result.error})` : ''}; sent: ${sent.join(', ') || 'nothing'}${now ? `; the snapshot read ${JSON.stringify(before)} at input and ${JSON.stringify(now)} now` : ''}`);
  }
  // A background row watches its poll for a bounded time; other rows wait for
  // their own condition (if any) and for the page to go quiet.
  const rule = step.background;
  const observed = () => page.evaluate(([mark, method, source, start]) => {
    const path = new RegExp(source);
    return window.__lf.net.slice(mark).filter(entry => entry.method === method && path.test(entry.path) && entry.t >= start).length;
  }, [mark, rule.method, rule.path.source, result.start]);
  const extra = rule ? async () => (await observed()) >= rule.count || Date.now() - nodeStart >= rule.ms
    : step.settled ? () => step.settled(ctx, args) : null;
  await settle(page, extra);
  const entries = await page.evaluate(mark => window.__lf.net.slice(mark), mark);
  const paintAt = result.painted ? result.start + result.firstFrameMs : null;
  const windowEnd = paintAt ?? result.start + (result.waitedMs ?? 0);
  const describe = entry => ({kind: entry.kind, method: entry.method, path: entry.path + (entry.search || ''), atMs: +(entry.t - result.start).toFixed(1), background: backgroundRow(entry)});
  const started = entries.filter(entry => entry.t >= result.start - 1);
  record.requests_in_window = started.filter(entry => entry.t <= windowEnd + 1).map(describe);
  const documents = net.documents.slice(documentsBefore).map(doc => ({kind: 'document', method: doc.method, path: doc.path, atMs: doc.at - nodeStart, background: null}));
  record.requests_in_window.push(...documents.filter(doc => paintAt == null || doc.atMs <= (paintAt - result.start) + 50));
  if (rule) {
    // A background row is B when its poll runs: count requests within rule.ms.
    const polls = started.filter(entry => entry.method === rule.method && rule.path.test(entry.path) && entry.t - result.start <= rule.ms);
    record.own_requests = polls.length >= rule.count ? polls.map(describe) : [];
  } else {
    const own = entry => {
      const row = backgroundRow(entry);
      if (row && row !== step.id) return false;
      return !(step.others || []).some(other => other.path.test(entry.path));
    };
    record.own_requests = [...started.filter(own).map(describe), ...documents];
  }
  if (step.finish) await step.finish(ctx, args);
  return record;
}

function kindOf(open, held) {
  if (!open || !held) return null;
  if (!held.painted) return 'R';
  return open.own_requests.length > 0 ? 'B' : 'L';
}

// ---------------------------------------------------------------------------

test('local-first walkthrough: does each interaction wait on the server?', {timeout: 600_000}, async t => {
  assert.ok(process.env.CICADA_STUDIO_URL, 'set CICADA_STUDIO_URL to a disposable Studio');
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(base.hostname), 'use a local disposable Studio');
  const fixtureName = process.env.CICADA_LOCAL_FIRST_FIXTURE || 'scene';
  const fixture = FIXTURES[fixtureName];
  assert.ok(fixture, `CICADA_LOCAL_FIRST_FIXTURE must be one of ${Object.keys(FIXTURES).join(', ')}`);
  // CICADA_LOCAL_FIRST_ROWS=all runs every row and =G1,G3 those rows; either skips the expectation check.
  const only = (process.env.CICADA_LOCAL_FIRST_ROWS || '').split(',').map(id => id.trim()).filter(id => id && id !== 'all');
  const compare = !process.env.CICADA_LOCAL_FIRST_ROWS;
  const part = partOf(process.env.CICADA_LOCAL_FIRST_PART);
  const steps = ROWS.filter(row => STEPS[row.id] && STEPS[row.id].fixture === fixtureName && (!only.length || only.includes(row.id)) && (STEPS[row.id].part || 1) % part.of === part.index % part.of)
    .map(row => ({id: row.id, ...STEPS[row.id]}));

  const stall = require('./page-stall.cjs');
  const {chromium} = playwright();
  const browser = await chromium.launch({headless: process.env.CICADA_HEADLESS !== '0', executablePath: process.env.CICADA_CHROME_PATH, args: ['--mute-audio']});
  const watches = [];
  stall.teardown(t, browser, {watch: watches, exitWhenDone: true});
  const context = await browser.newContext({viewport: {width: 1440, height: 900}, deviceScaleFactor: 1});
  watches.push(await stall.watchContext(context, {serverLog: process.env.CICADA_STUDIO_STARTUP_LOG}));
  await context.addInitScript(localFirstProbe);
  const net = await network(context);
  const errors = [];
  let page;
  const ctx = {base, fixture: fixtureName, net, context, errors, get page() { return page; }};
  const state = async endpoint => {
    const response = await context.request.get(new URL(endpoint, base).href);
    assert.equal(response.status(), 200, `${endpoint} must be available`);
    return response.json();
  };
  ctx.state = state;
  const original = await state('/api/state');
  assert.ok(original.source.split(/\r?\n/).includes(fixture.marker), `refusing to edit an unmarked score: start Studio on a --write-fixture ${fixtureName} score`);
  ctx.originalRevision = original.revision;

  // open loads Studio at a path (the step's prepare calls it when the page is
  // elsewhere) and waits for the engines.
  ctx.open = async (target, {force = false} = {}) => {
    const href = new URL(target, base).href;
    if (!page) {
      page = await context.newPage();
      page.setDefaultTimeout(15_000);
      page.on('pageerror', error => errors.push(`${ctx.current || 'startup'}: ${error.message}`));
    }
    // Reuse the page while it still shows the selection the path names; a
    // soft navigation that changed pattern, placement, step or the Pattern
    // Machine needs a fresh load.
    const same = () => {
      if (ctx.loaded !== href) return false;
      const now = new URL(page.url()), want = new URL(href);
      return ['pattern', 'placement', 'machine', 'step'].every(name => !want.searchParams.has(name) || (now.searchParams.get(name) || '') === want.searchParams.get(name));
    };
    if (!force && same()) return;
    await page.goto(href);
    await ready(page);
    await page.locator('#studio-shell[data-shell-edits="ready"]').waitFor({timeout: 30_000}).catch(() => {});
    ctx.loaded = href;
  };
  ctx.tap = selector => tap(page, selector);
  ctx.settle = () => settle(page);
  ctx.blur = () => page.evaluate(() => document.activeElement?.blur?.());
  // Setup helpers: each leaves the page in a known state with the network open.
  // A tab's update region may be stale when it shows (the shell reads it
  // then): wait until it is current, so the step sees the saved state.
  const tabs = (group, name) => async () => {
    const tab = page.locator(`#${group}-tab-${name}`);
    if (await tab.getAttribute('aria-selected') !== 'true') await tab.click();
    await page.locator(`#${group}-panel-${name}:not([hidden])`).waitFor();
    const region = `#update-${group === 'view' ? 'center' : 'dock'}-${name}`;
    await until(() => page.evaluate(region => !document.querySelector(`${region}[data-region-stale]`), region), `${region} stayed stale`);
  };
  ctx.view = name => tabs('view', name)();
  ctx.dock = name => tabs('detail', name)();
  // A pane is open when its Hide toggle shows (the shell swaps the toggles).
  ctx.pane = async (name, open) => {
    const shown = () => page.evaluate(name => window.__lf.shown(`.shell-pane-toggle[data-pane=${name}][data-pane-action=hide]`), name);
    if (await shown() !== open) await page.locator(`.shell-pane-toggle[data-pane=${name}][data-pane-action=${open ? 'show' : 'hide'}]`).click();
    await until(async () => (await shown()) === open, `the ${name} pane did not ${open ? 'open' : 'close'}`);
  };
  ctx.openDetails = async selector => {
    const found = await page.evaluate(selector => {
      const node = document.querySelector(selector);
      if (!node) return false;
      for (let el = node; el; el = el.parentElement?.closest('details')) el.open = true;
      return true;
    }, selector);
    if (!found) throw new Error(`no ${selector}; the page shows ${await page.evaluate(selector => document.querySelector(selector.split(' ')[0])?.outerHTML.replace(/\s+/g, ' ').slice(0, 300) ?? 'none of it', selector)}`);
  };
  // grab presses the mouse on the centre of the first visible match and keeps it down.
  ctx.grab = async selector => {
    await page.locator(selector).first().scrollIntoViewIfNeeded();
    const box = await page.locator(selector).first().boundingBox();
    if (!box) throw new Error(`nothing visible matches ${selector}`);
    const point = {x: box.x + box.width / 2, y: box.y + box.height / 2};
    await page.mouse.move(point.x, point.y);
    await page.mouse.down();
    return point;
  };
  ctx.reveal = selector => page.locator(selector).first().scrollIntoViewIfNeeded();
  // A visible inactive piano-roll cell in the Clip dock (as the benchmark picks one).
  ctx.inactiveCell = async ({offGrid = false} = {}) => {
    const selector = await page.evaluate(offGrid => {
      // offGrid: a step that is not on a quarter note (steps 1, 5, 9, ... are).
      const cells = [...document.querySelectorAll('#detail-panel-clip .roll-row .roll-cell[data-active="false"]')].filter(cell => cell.getClientRects().length && (!offGrid || Number(cell.value) % 4 !== 1));
      const cell = cells.find(candidate => {
        const box = candidate.getBoundingClientRect(), grid = candidate.closest('.piano-roll')?.getBoundingClientRect();
        return grid && box.left >= grid.left && box.right <= Math.min(innerWidth, grid.right) && box.top >= Math.max(0, grid.top) && box.bottom <= Math.min(innerHeight, grid.bottom);
      }) ?? cells[0];
      if (!cell) return null;
      const row = cell.closest('.roll-row').getAttribute('data-gosx-key');
      return `#detail-panel-clip .roll-row[data-gosx-key="${CSS.escape(row)}"] .roll-cell[value="${CSS.escape(cell.value)}"]`;
    }, offGrid);
    if (!selector) throw new Error('the Clip dock shows no inactive piano-roll cell');
    return selector;
  };
  ctx.closeDialog = async id => {
    if (await page.evaluate(id => window.__lf.shown(`#${id}`), id)) await page.keyboard.press('Escape');
    await page.locator(`#${id}`).waitFor({state: 'hidden'});
  };
  // codeEnd puts the caret at the end of the Code pane's text; codeRevert
  // puts the saved text back, so a typed draft does not leak into later steps.
  ctx.codeEnd = async (target = SCENE) => {
    await ctx.open(target); await ctx.pane('code', true);
    const editor = page.locator('#editor-content');
    await editor.waitFor();
    await editor.focus();
    await page.keyboard.press(`${mod}+End`);
  };
  ctx.codeRevert = async () => {
    await page.locator('#editor-content').fill((await state('/api/state')).source);
    await ctx.blur();
  };
  // recordNote records one Live note take of one note while playing (setup).
  ctx.recordNote = async () => {
    await ctx.play();
    await page.locator('[data-live-control=record]:not([disabled])').waitFor();
    await page.locator('[data-live-control=record]').click();
    await page.locator('[data-live-control=record][aria-pressed=true]').waitFor();
    await page.locator('#view-panel-session [data-live-note="57"]').hover();
    await page.mouse.down();
    await page.locator('#view-panel-session [data-live-note="57"][aria-pressed=true]').waitFor();
    await page.mouse.up();
    await until(() => page.evaluate(() => !!document.querySelector('[data-live-control=finish]:not([disabled])')), 'the take recorded no note', 5_000)
      .catch(async error => { throw new Error(`${error.message}; Live status: ${await page.evaluate(() => document.querySelector('[data-live-status]')?.textContent)}`); });
  };
  ctx.transport = async () => (await state('/api/transport')).playing === true;
  ctx.play = async () => {
    if (!(await ctx.transport())) await page.locator('#transport-form button[value=play]').click();
    await until(() => ctx.transport(), 'playback did not start');
    await until(() => page.evaluate(() => /BAR \d+ STEP \d+/.test(window.__lf.position())), 'the position readout did not show');
  };
  ctx.stop = async () => {
    if (await ctx.transport()) await page.locator('#transport-form button[value=stop]').click();
    await until(async () => !(await ctx.transport()), 'playback did not stop');
    // The readout polls once a second: wait until it shows the stopped
    // position, so a step's snapshot does not catch it moving.
    const stopped = await state('/api/transport');
    await until(() => page.evaluate(want => window.__lf.position() === want, `BAR ${stopped.bar} STEP ${stopped.step}`), 'the position readout did not settle after Stop');
  };

  const records = [];
  const failures = [];
  for (const pass of ['open', 'held']) {
    for (const step of steps) {
      ctx.current = `${step.id} (${pass})`;
      let record = null, failure = null;
      for (let attempt = 1; attempt <= ATTEMPTS && !record; attempt++) {
        const began = Date.now();
        try {
          record = await runStep(ctx, step, pass);
          record.attempt = attempt;
          record.elapsedMs = Date.now() - began;
        } catch (error) {
          failure = `${step.id} ${pass} attempt ${attempt}: ${error.message.split('\n').filter(Boolean).slice(0, 4).join(' | ')}`;
          console.error(`local-first: ${failure}`);
          ctx.loaded = null; // the next attempt and the next step start on a fresh page
          if (net.holding) await net.release().catch(() => {});
          await page?.setViewportSize({width: 1440, height: 900}).catch(() => {});
          await ctx.stop().catch(() => {});
        }
      }
      if (record) records.push(record); else failures.push(failure);
    }
  }

  // After the held pass: the page still answers, and a reload shows the score
  // the server holds.
  ctx.current = 'recovery';
  let recovery = 'not checked (no step ran)';
  if (page) {
    await ctx.open('/studio?tab=arrange', {force: true});
    recovery = await page.evaluate(() => !!document.querySelector('#studio-shell[data-shell-actions=ready]')) ? 'the page reloads and its engines mount' : 'the page did not mount';
  }

  const report = summarize(steps, records, failures, fixtureName, browser.version(), recovery, errors);
  const dir = process.env.CICADA_LOCAL_FIRST_PROOF_DIR || path.join(__dirname, '..', '..', 'build', 'local-first');
  fs.mkdirSync(dir, {recursive: true});
  report.part = part.label;
  fs.writeFileSync(path.join(dir, `local-first-${fixtureName}${part.of > 1 ? `-${part.index}of${part.of}` : ''}.json`), JSON.stringify(report, null, 1) + '\n');
  console.log(table(report));
  const combined = combine(dir);
  if (combined) console.log(combined);

  assert.deepEqual(failures, [], 'steps that failed twice');
  if (compare) {
    const expected = JSON.parse(fs.readFileSync(path.join(__dirname, 'local-first.expected.json'), 'utf8'));
    const mismatches = report.rows.filter(row => row.kind !== expected.rows[row.id]).map(row =>
      `${row.id}: measured ${row.kind}, expected ${expected.rows[row.id] ?? '(none)'}${row.kind === 'R' ? ' (waited on the server)' : ''}`);
    assert.deepEqual(mismatches, [], 'a row changed kind: update workstation/browser/local-first.expected.json in the change that moved it');
  }
  assert.deepEqual(errors, [], 'page errors');
});

function summarize(steps, records, failures, fixture, browserVersion, recovery, errors) {
  const rows = steps.map(step => {
    const open = records.find(record => record.row === step.id && record.pass === 'open');
    const held = records.find(record => record.row === step.id && record.pass === 'held');
    const row = ROWS.find(candidate => candidate.id === step.id);
    return {
      id: step.id, what: row.what, audit: row.audit, target: row.target, kind: kindOf(open, held),
      requests_in_window: open ? open.requests_in_window.length : null,
      painted_while_held: held ? held.painted : null,
      time_to_paint_ms: open ? open.ms : null,
      first_frame_ms: open ? open.firstFrameMs : null,
      held_paint_ms: held && held.painted ? held.ms : null,
      recovered: held ? held.recovered : null,
      recovery_note: held?.recoveryNote,
      elapsed_ms: [open?.elapsedMs, held?.elapsedMs],
      own_requests: open ? open.own_requests : null,
      window: open ? open.requests_in_window : null,
      held_window: held ? held.requests_in_window : null,
      attempts: [open?.attempt, held?.attempt],
    };
  });
  return {
    generatedAt: new Date().toISOString(), fixture, browser: browserVersion,
    host: {platform: process.platform, arch: process.arch, cpus: os.cpus().length, load: os.loadavg()},
    holdMs: HOLD_MS, holdFrames: HOLD_FRAMES,
    waited_on_server: rows.filter(row => row.painted_while_held === false).length,
    recovery, failures, pageErrors: errors, rows,
  };
}

function table(report) {
  const lines = [`local-first walkthrough, fixture ${report.fixture} (report mode; hold ${report.holdMs} ms and ${report.holdFrames} frames)`,
    'in-window: requests and frames the page started between input and paint (polls included); own: those the kind counts',
    'row   kind  audit  in-window  own  painted-held  paint-ms  recovered'];
  for (const row of report.rows) {
    lines.push(`${row.id.padEnd(5)} ${String(row.kind ?? '-').padEnd(5)} ${row.audit.padEnd(5)} ${String(row.requests_in_window ?? '-').padStart(10)} ${String(row.own_requests?.length ?? '-').padStart(4)}  ${String(row.painted_while_held ?? '-').padEnd(12)}  ${String(row.time_to_paint_ms ?? '-').padStart(8)}  ${row.recovered ?? '-'}`);
  }
  lines.push(`waited_on_server ${report.waited_on_server} of ${report.rows.length} steps; ${report.recovery}`);
  return lines.join('\n');
}

// combine prints the totals over all 109 rows when every fixture's JSON is in
// dir: measured kinds for scripted rows, the audit's class for the others.
function combine(dir) {
  const reports = fs.readdirSync(dir).filter(name => /^local-first-.*\.json$/.test(name)).map(name => path.join(dir, name));
  const measured = new Map();
  for (const file of reports) for (const row of JSON.parse(fs.readFileSync(file, 'utf8')).rows) measured.set(row.id, row.kind);
  const scripted = ROWS.filter(row => STEPS[row.id]).map(row => row.id);
  if (scripted.some(id => !measured.has(id))) return null; // another part has not finished yet
  const count = {measured: {R: 0, B: 0, L: 0}, audit: {R: 0, B: 0, L: 0}, total: {R: 0, B: 0, L: 0}};
  const disagree = [];
  for (const row of ROWS) {
    count.audit[row.audit]++;
    const kind = measured.get(row.id);
    if (kind) {
      count.measured[kind] = (count.measured[kind] || 0) + 1;
      if (kind !== row.audit) disagree.push(`${row.id} measured ${kind}, audit ${row.audit}`);
    }
    count.total[kind || row.audit] = (count.total[kind || row.audit] || 0) + 1;
  }
  const unscripted = ROWS.filter(row => !measured.has(row.id)).map(row => `${row.id} (${UNSCRIPTED[row.id] || 'not run'})`);
  return [`local-first totals over ${ROWS.length} rows: R ${count.total.R}, B ${count.total.B}, L ${count.total.L} (audit: R ${count.audit.R}, B ${count.audit.B}, L ${count.audit.L})`,
    `measured ${measured.size} rows: R ${count.measured.R}, B ${count.measured.B}, L ${count.measured.L}`,
    `disagreements with the audit: ${disagree.length ? disagree.join('; ') : 'none'}`,
    `not scripted (${unscripted.length}): ${unscripted.join('; ')}`].join('\n');
}
