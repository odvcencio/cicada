/*
 * Clip tab note editing, phase 1: Quantize by button and by Mod+U, one write
 * and one Undo each, for a played performance, a step pattern and drum lanes.
 *
 * node workstation/browser/note-editor.test.cjs --write-fixture /path/to/note-editor.cicada
 * ./build/cicada studio /path/to/note-editor.cicada --audio null --listen 127.0.0.1:8190
 * CICADA_STUDIO_URL=http://127.0.0.1:8190 node --test workstation/browser/note-editor.test.cjs
 *
 * The suite edits the score and undoes each edit, so run it on a fresh Studio
 * process with the disposable fixture. CICADA_NOTES_PROOF_DIR optionally keeps
 * a report and screenshots. CICADA_PLAYWRIGHT_MODULE and CICADA_CHROME_PATH
 * select Playwright and Chrome, as in shell.test.cjs.
 */
'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {createRequire} = require('node:module');

const marker = '// cicada-note-editor-test: disposable fixture';
// A hand-played phrase: each note is up to 51 ticks early or late of its 1/16.
const offsets = [13, -21, 34, -9, 27, -40, 8, 51, -17, 22, -33, 45, -12, 30, -48, 19];
const played = offsets.map((offset, i) => ({pitch: 55 + i, at: i * 240 + offset, velocity: 70 + i}));

if (process.argv[2] === '--write-fixture') {
  const target = path.resolve(process.argv[3] || '');
  if (!process.argv[3] || fs.existsSync(target)) throw new Error('Choose a new disposable fixture path');
  const notes = played.map(note => `  note ${note.pitch} at=${note.at}ticks length=180ticks velocity=${note.velocity};\n`).join('');
  fs.mkdirSync(path.dirname(target), {recursive: true});
  fs.writeFileSync(target, `${marker}
cicada 2
title "Note editor"
tempo 120
instrument glass { voice poly { out = sine(pitch) * env(gate, 150ms) * 0.1 } }
track keys glass { level=-24dB }
track pad glass { level=-24dB }
track drums drums { level=-24dB }
pattern take performance length=3840ticks {
  // played in by hand
${notes}} // keep this take
pattern riff notes {
  c4 . . e4 . . . . . g4 - . . c5 . . // keep this riff
}
pattern beat drums {
  bd: x . . x . . . . x . . . . . . .
  sd: . . . . x . . . . . . . . x . .
}
scene main { keys=take pad=riff drums=beat }
song { main }
`);
  process.exit(0);
}

const test = require('node:test');

function playwright() {
  const location = process.env.CICADA_PLAYWRIGHT_MODULE;
  if (!location) return require('playwright');
  try {
    const loaded = require(path.resolve(location));
    if (loaded.chromium) return loaded;
  } catch (error) {
    if (error.code !== 'MODULE_NOT_FOUND') throw error;
  }
  return createRequire(path.resolve(location))('playwright');
}

async function until(check, message, timeout = 15_000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    if (await check()) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  assert.fail(message);
}

let shared;
const checks = [];
test.after(async () => {
  const proof = process.env.CICADA_NOTES_PROOF_DIR;
  if (proof) {
    fs.mkdirSync(proof, {recursive: true});
    fs.writeFileSync(path.join(proof, 'note-editor-report.json'), JSON.stringify({checks}, null, 2) + '\n');
  }
  await shared?.browser.close();
});

async function openClip(pattern) {
  const base = process.env.CICADA_STUDIO_URL;
  assert.ok(base && ['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname), 'use a local Studio');
  if (!shared) {
    const browser = await playwright().chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH, args: ['--no-sandbox', '--mute-audio']});
    shared = {browser, context: await browser.newContext({viewport: {width: 1440, height: 900}})};
  }
  const page = await shared.context.newPage();
  page.setDefaultTimeout(15_000);
  const errors = [], posts = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('request', request => {
    if (request.method() === 'POST' && new URL(request.url()).pathname.startsWith('/__actions/')) posts.push(new URL(request.url()).pathname);
  });
  const json = async route => (await page.request.get(new URL(route, base).href)).json();
  await page.goto(new URL(`/studio?dock=clip&pattern=${pattern}`, base).href);
  // Ready means the page shows the saved score: the Quantize form carries the
  // disk revision, the shell engine is mounted and Mod+U is available. A
  // submit or Undo navigates in place, so the old form stays until then.
  const ready = async () => {
    const {revision} = await json('/api/state');
    await page.locator('#studio-shell[data-shell-actions="ready"] #notes-quantize button[type=submit]').waitFor();
    await until(async () => await page.locator('#notes-quantize input[name=revision]').inputValue().catch(() => '') === revision, 'the Clip tab did not show the saved revision');
    await page.waitForFunction(() => window.__gosx?.commands?.available?.('notes.quantize') === true);
  };
  await ready();
  return {
    page, errors, ready,
    source: async () => (await json('/api/state')).source,
    history: async () => (await json('/api/history')).edits || [],
    notePosts: () => posts.filter(name => name === '/__actions/notes').length,
    posts: () => posts.length,
  };
}

// quantizeOnce runs one quantize gesture, checks one POST, one history entry
// and the expected source, then presses Undo and checks the exact bytes.
async function quantizeOnce(view, gesture, verify, label) {
  const {page} = view;
  const baseline = await view.source(), posts = view.notePosts(), entries = (await view.history()).length;
  await gesture();
  await until(async () => (await view.source()) !== baseline, `${label}: the score did not change`);
  const after = await view.source();
  verify(after);
  await view.ready();
  assert.equal(view.notePosts(), posts + 1, `${label}: exactly one POST to /__actions/notes`);
  const history = await view.history();
  assert.equal(history.length, entries + 1, `${label}: exactly one new history entry`);
  await page.getByRole('button', {name: 'Undo', exact: true}).click();
  await until(async () => (await view.source()) === baseline, `${label}: one Undo did not restore the exact source`);
  await view.ready();
  return {after, history};
}

function playedNotes(source) {
  return [...source.matchAll(/note (\d+) at=(\d+)ticks length=(\d+)ticks velocity=(\d+);/g)].map(match => ({pitch: Number(match[1]), at: Number(match[2]), length: Number(match[3]), velocity: Number(match[4])}));
}

function expectTakeOnGrid(source) {
  const notes = playedNotes(source);
  assert.equal(notes.length, played.length, 'every played note remains');
  notes.forEach((note, i) => {
    assert.deepEqual(note, {pitch: played[i].pitch, at: i * 240, length: 180, velocity: played[i].velocity}, `note ${i + 1} lands on its 1/16 and keeps length and velocity`);
  });
  assert.ok(source.includes('// played in by hand') && source.includes('} // keep this take'), 'comments survive the rewrite');
}

test('a played performance quantizes from the Clip tab by button and by Mod+U, one Undo each', {timeout: 120_000}, async () => {
  const view = await openClip('take');
  const {page, errors} = view;
  try {
    const original = await view.source();
    assert.ok(original.includes(marker), 'refusing to edit a score that is not the disposable fixture');
    assert.equal(playedNotes(original).filter((note, i) => note.at !== i * 240).length, 16, 'the fixture starts off the grid');
    const form = page.locator('#notes-quantize');
    assert.equal(await form.locator('select[name=grid]').inputValue(), '240', '1/16 is the default grid');
    assert.equal(await form.locator('input[name=strength]').inputValue(), '100');
    assert.match(await form.getByRole('button', {name: 'Quantize', exact: true}).getAttribute('aria-keyshortcuts'), /Control\+U/);
    if (process.env.CICADA_NOTES_PROOF_DIR) await page.locator('section.notes-quantize').screenshot({path: path.join(process.env.CICADA_NOTES_PROOF_DIR, 'note-editor-clip-1440.png')});

    const button = await quantizeOnce(view, () => form.getByRole('button', {name: 'Quantize', exact: true}).click(), expectTakeOnGrid, 'Quantize button');
    assert.equal(button.history[0].label, 'Notes · take · 16 notes quantized to 1/16', 'the newest history entry names the batch');
    checks.push('the Quantize button puts all 16 played notes on the 1/16 grid with one POST, one history entry and one exact Undo');

    await page.evaluate(() => document.activeElement?.blur());
    await quantizeOnce(view, () => page.keyboard.press('Control+u'), expectTakeOnGrid, 'Mod+U');
    checks.push('Mod+U submits the same Quantize form, with one POST, one history entry and one exact Undo');

    // Quantizing notes already on the grid says so and writes no history.
    await form.getByRole('button', {name: 'Quantize', exact: true}).click();
    await until(async () => (await view.source()) !== original, 'quantize did not write');
    await view.ready();
    const entries = (await view.history()).length, posts = view.notePosts(), quantized = await view.source();
    await form.getByRole('button', {name: 'Quantize', exact: true}).click();
    await until(async () => (await page.locator('body').innerText()).includes('Already on the grid.'), 'an unchanged quantize did not say why');
    await view.ready();
    assert.equal(view.notePosts(), posts + 1);
    assert.equal((await view.history()).length, entries, 'an unchanged quantize records no history');
    assert.equal(await view.source(), quantized);
    await page.getByRole('button', {name: 'Undo', exact: true}).click();
    await until(async () => (await view.source()) === original, 'Undo after the unchanged quantize did not restore the take');
    await view.ready();
    checks.push('a second quantize reports "Already on the grid." and adds no history entry');

    // Outside the Clip tab, Mod+U is consumed and submits nothing.
    await page.locator('#detail-tab-device').click();
    await until(async () => await page.locator('#detail-panel-device').isVisible(), 'Device tab did not open');
    await page.evaluate(() => document.activeElement?.blur());
    const before = view.posts();
    await page.keyboard.press('Control+u');
    await new Promise(resolve => setTimeout(resolve, 400));
    assert.equal(view.posts(), before, 'Mod+U outside the Clip tab submits no action');
    assert.equal(await view.source(), original);
    checks.push('outside the Clip tab Mod+U submits nothing');
    assert.deepEqual(errors, []);
  } finally {
    await page.close();
  }
});

test('a step pattern and drum lanes quantize to the beat with one Undo each', {timeout: 120_000}, async () => {
  for (const [pattern, before, after] of [
    ['riff', 'c4 . . e4 . . . . . g4 - . . c5 . . // keep this riff', 'c4 . . . e4 . . . g4 - . . c5 . . . // keep this riff'],
    ['beat', 'bd: x . . x . . . . x . . . . . . .\n  sd: . . . . x . . . . . . . . x . .', 'bd: x . . . x . . . x . . . . . . .\n  sd: . . . . x . . . . . . . x . . .'],
  ]) {
    const view = await openClip(pattern);
    try {
      const original = await view.source();
      assert.ok(original.includes(marker) && original.includes(before), `${pattern} starts off the beat`);
      const form = view.page.locator('#notes-quantize');
      assert.equal(await form.locator('select[name=grid]').inputValue(), '960', 'Beat is the default for a 1/16 step grid');
      assert.equal(await form.locator('input[name=strength]').count(), 0, 'step grids quantize at full strength');
      const {history} = await quantizeOnce(view, () => form.getByRole('button', {name: 'Quantize', exact: true}).click(), source => {
        assert.equal(source, original.replace(before, after), `${pattern}: only the moved steps change`);
      }, `${pattern} Quantize`);
      assert.match(history[0].label, new RegExp(`^Notes · ${pattern} · \\d+ notes? quantized to 1 beat$`));
      checks.push(`${pattern}: Quantize to the beat moves its notes with one POST, one history entry and one exact Undo`);
      assert.deepEqual(view.errors, []);
    } finally {
      await view.page.close();
    }
  }
});
