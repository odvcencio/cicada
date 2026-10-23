// Run only against the disposable null-audio fixture. All notes are synthetic
// pointer events; no microphone or MIDI device discovery is requested.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__cicadaLibraryRecordingSmoke) return;
  window.__cicadaLibraryRecordingSmoke = true;
  const key = 'cicada.library-recording-proof', retained = JSON.parse(sessionStorage.getItem(key) || 'null');
  const checks = retained?.checks || [], errors = [];
  addEventListener('error', e => errors.push(e.message));
  addEventListener('unhandledrejection', e => errors.push(String(e.reason?.stack || e.reason)));
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
  async function wait(check, label) {
    const end = performance.now() + 25000;
    while (performance.now() < end) { if (await check()) return; await pause(50); }
    throw new Error('Timed out: ' + label);
  }
  const read = async (name = 'state') => {
    const response = await fetch('/api/' + name, {cache:'no-store'});
    if (!response.ok) throw new Error(name + ': ' + response.status);
    return response.json();
  };
  const saved = async source => wait(async () => {
    const value = await read();
    return value.valid && value.source === source && document.querySelector('#undo-form input[name=revision]')?.value === value.revision;
  }, 'saved source and revision');
  const change = (node, value) => { node.value = value; node.dispatchEvent(new Event('change', {bubbles:true})); };
  const draft = () => JSON.parse(sessionStorage.getItem('cicada.note-take') || '{}');
  async function unused() {
    const value = await read();
    if (/keys\s*=\s*fresh|\bslot\s*=/.test(value.source)) throw new Error('Library recording assigned a launch slot or scene');
    if (!value.source.includes('scene main { bass=pulse keys=off }\nsong { main*8 }')) throw new Error('Library recording changed the song');
  }
  (async () => {
    await wait(() => window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]'), 'Studio engines');
    const audio = await read('audio/config');
    if (audio.options.inputEnabled || String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null') throw new Error('Use null audio with input disabled');
    if (retained?.phase === 'reopen') {
      await saved(retained.captured); await unused();
      await wait(() => document.querySelector('#patterns .performance-inspector input[name=tick]'), 'reopened library note');
      const actual = document.querySelector('#patterns .performance-inspector input[name=tick]').value;
      if (actual !== String(retained.start) || document.querySelector('#patterns .performance-inspector input[name=end_tick]').value !== String(retained.end)) throw new Error('Reopened library note lost played timing');
      const preview = document.querySelector('#cicada-pattern-preview button[data-sound-preview=fresh]');
      await wait(() => preview && !preview.disabled, 'unassigned pattern preview'); preview.click();
      await wait(() => preview.getAttribute('aria-pressed') === 'true' && /Looping fresh with keys/.test(document.querySelector('#cicada-pattern-preview [data-sound-status]').textContent), 'audible library preview started');
      preview.click(); await wait(() => preview.getAttribute('aria-pressed') === 'false', 'library preview stopped');
      await saved(retained.captured); await unused();
      checks.push('unused phrase reopens with exact note timing and auditions with its chosen sound');
      sessionStorage.removeItem(key);
      if (errors.length) throw new Error(JSON.stringify(errors));
      return {type:'cicada-native-smoke',ok:true,checks,note:retained.note,start:retained.start,end:retained.end};
    }
    let created = retained?.created;
    if (!retained) {
      const initial = (await read()).source;
      if (!initial.includes('cicada-library-recording-test: disposable fixture')) throw new Error('Use the disposable library-recording fixture');
      document.querySelector('#view-tab-session').click();
      await wait(() => document.querySelector('#live [data-live-pattern]'), 'Live controls');
      if ([...document.querySelector('[data-live-pattern]').options].some(o => o.value === 'fresh')) throw new Error('Fixture already contains the new pattern');
      const details = document.querySelector('#live details[aria-label="Create pattern"]');
      if (!details) throw new Error('Live cannot create its first pattern');
      details.open = true;
      details.querySelector('input[name=newName]').value = 'fresh';
      change(details.querySelector('select[name=track]'), 'keys');
      change(details.querySelector('select[name=starter]'), 'empty');
      change(details.querySelector('select[name=scene]'), '');
      if (typeof window.__cicadaLibraryCreationProof === 'function') await window.__cicadaLibraryCreationProof();
      details.querySelector('button[type=submit]').click();
      await wait(async () => (await read()).source.includes('pattern fresh notes steps=16'), 'unused library pattern created');
      created = (await read()).source; await saved(created); await unused();
      await wait(() => [...document.querySelector('[data-live-pattern]').options].some(o => o.value === 'fresh' && /library/.test(o.textContent)), 'new library destination');
      change(document.querySelector('[data-live-pattern]'), 'fresh');
      if (document.querySelector('[data-live-pattern]').value !== 'fresh') throw new Error('Destination fell back to a song pattern');
      checks.push('Live creates and selects its first unused pattern without a scene assignment');
      if (typeof window.__cicadaLibraryPickerProof === 'function') await window.__cicadaLibraryPickerProof();
      document.querySelector('#transport-form button[value=play]').click();
      await wait(() => !document.querySelector('[data-live-control=record]').disabled, 'record enabled');
      document.querySelector('[data-live-control=record]').click();
      const pad = document.querySelector('[data-live-note]');
      pad.dispatchEvent(new PointerEvent('pointerdown', {pointerId:9,pointerType:'mouse',button:0,bubbles:true,cancelable:true}));
      await pause(180);
      pad.dispatchEvent(new PointerEvent('pointerup', {pointerId:9,pointerType:'mouse',button:0,bubbles:true}));
      await wait(() => draft().takes?.[0]?.notes?.[0]?.endTick > draft().takes[0].notes[0].tick, 'retained note release');
      const take = draft().takes[0], note = take.notes[0];
      if (take.pattern !== 'fresh' || take.track !== 'keys') throw new Error('Take recorded into another destination');
      checks.push('real packaged recording retains the note in the unused destination');
      sessionStorage.setItem(key, JSON.stringify({phase:'review',created,note,checks}));
      document.querySelector('[data-live-control=finish]').click();
    }
    await wait(() => document.querySelector('.note-preview button[value=played]:not([disabled])'), 'library take review');
    if (typeof window.__cicadaLibraryReviewProof === 'function') await window.__cicadaLibraryReviewProof();
    const note = JSON.parse(sessionStorage.getItem(key)).note, origin = Math.floor(note.tick/3840)*3840, start = note.tick-origin, end = note.endTick-origin;
    document.querySelector('#transport-form button[value=stop]').click();
    await wait(async () => !(await read('transport')).playing, 'stop before commit');
    document.querySelector('.note-preview button[value=played]').click();
    await wait(async () => (await read()).source.includes(`note ${note.note} at=${start}ticks length=${end-start}ticks velocity=${note.velocity}`), 'played timing kept in library');
    const captured = (await read()).source; await saved(captured); await unused();
    checks.push('keeping the take writes the chosen library pattern and preserves song/slots');
    document.querySelector('#undo-form button').click(); await saved(created);
    document.querySelector('#redo-form button').click(); await saved(captured);
    checks.push('one Undo and Redo restore exact library pattern snapshots');
    sessionStorage.setItem(key, JSON.stringify({phase:'reopen',created,captured,note,start,end,checks}));
    location.href = '/studio?dock=clip&pattern=fresh&region=detail';
    return null;
  })().catch(error => ({type:'cicada-native-smoke',ok:false,checks,error:String(error)+'\n'+String(error.stack || '')+'\nLIVE '+document.querySelector('[data-live-status]')?.textContent+'\nERRORS '+JSON.stringify(errors)}))
    .then(result => {
      if (!result) return;
      if (typeof window.__cicadaLibraryRecordingResult === 'function') window.__cicadaLibraryRecordingResult(result);
      else window.chrome?.webview?.postMessage(JSON.stringify(result));
    });
})();
