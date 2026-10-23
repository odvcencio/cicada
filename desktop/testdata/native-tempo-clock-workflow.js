// Disposable null-audio clock/capture test. Pointer notes are synthetic; this
// never opens a microphone or requests MIDI discovery. Transport injection
// changes only the UI response, after checking the real renderer's forecast.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__cicadaTempoClockSmoke) return;
  window.__cicadaTempoClockSmoke = true;
  const key = 'cicada.tempo-clock-proof', retained = JSON.parse(sessionStorage.getItem(key) || 'null');
  const checks = retained?.checks || [], errors = [];
  addEventListener('error', e => errors.push(e.message));
  addEventListener('unhandledrejection', e => errors.push(String(e.reason?.stack || e.reason)));
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
  async function wait(check, label) {
    const end = performance.now() + 25000;
    while (performance.now() < end) { if (await check()) return; await pause(50); }
    throw new Error('Timed out: ' + label);
  }
  const originalFetch = window.fetch;
  let mode = '', seen = 0;
  window.fetch = function(...args) {
    const request = args[0], url = new URL(typeof request === 'string' ? request : request.url, location.href);
    if (url.pathname !== '/api/transport' || !mode) return originalFetch.apply(this, args);
    const active = mode;
    return originalFetch.apply(this, args).then(async response => {
      const value = await response.json();
      if (!value.playing) throw new Error('Synthetic clock requires actual null-audio playback');
      const raw = Uint8Array.from(atob(value.frames.transport), c => c.charCodeAt(0)), view = new DataView(raw.buffer);
      if (raw.length !== 64 || raw[0] !== 1) throw new Error('Transport ABI changed');
      view.setUint32(22, 120000, true);
      view.setInt32(52, 1, true); view.setInt32(56, 4, true); view.setInt32(60, active === 'on' ? 768 : 864, true);
      value.frames.transport = btoa(String.fromCharCode(...raw));
      value.frameAgeNanos = 250000000;
      value.nextTempo = {afterNanos:active === 'on' ? 100000000 : 50000000,tick:3840,bpmMilli:60000};
      seen++;
      const headers = new Headers(response.headers);
      headers.delete('content-length'); headers.delete('content-encoding');
      return new Response(JSON.stringify(value), {status:response.status,headers});
    });
  };
  const read = async (name = 'state') => {
    const response = await originalFetch('/api/' + name, {cache:'no-store'});
    if (!response.ok) throw new Error(name + ': ' + response.status);
    return response.json();
  };
  const saved = async source => wait(async () => {
    const current = await read();
    return current.valid && current.source === source && document.querySelector('#undo-form input[name=revision]')?.value === current.revision;
  }, 'saved source and revision');
  const draft = () => JSON.parse(sessionStorage.getItem('cicada.note-take') || '{}');
  (async () => {
    await wait(() => window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]'), 'Studio engines');
    const audio = await read('audio/config');
    if (audio.options.inputEnabled || String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null') throw new Error('Use null audio with input disabled');
    if (retained?.phase === 'reopen') {
      await saved(retained.captured);
      if (new URL(location.href).searchParams.get('pattern') !== 'phrase') throw new Error('Captured pattern was not reopened');
      await wait(() => document.querySelector('#patterns .performance-inspector input[name=tick]'), 'reopened played note');
      if (document.querySelector('#patterns .performance-inspector input[name=tick]').value !== '144' || document.querySelector('#patterns .performance-inspector input[name=end_tick]').value !== '192') throw new Error('Reopened note inspector lost its played timing');
      checks.push('saved played timing survives a full native/browser reload');
      sessionStorage.removeItem(key);
      if (errors.length) throw new Error(JSON.stringify(errors));
      return {type:'cicada-native-smoke',ok:true,checks};
    }
    let source = retained?.source;
    if (!retained) {
      source = (await read()).source;
      if (!source.includes('cicada-tempo-clock-test: disposable fixture')) throw new Error('Use the disposable tempo-clock fixture');
      document.querySelector('#view-tab-session').click();
      await wait(() => document.querySelector('[data-live-pattern]'), 'Live controls');
      const select = document.querySelector('[data-live-pattern]');
      select.value = 'phrase'; select.dispatchEvent(new Event('change', {bubbles:true}));
      if (select.value !== 'phrase') throw new Error('The fixture did not bind the capture destination');
      document.querySelector('#transport-form button[value=play]').click();
      await wait(async () => {
        const value = await read('transport');
        return value.playing && value.nextTempo?.bpmMilli === 60000 && value.nextTempo.afterNanos >= 0 && value.nextTempo.afterNanos <= 350000000;
      }, 'real rendered tempo boundary');
      checks.push('real renderer publishes the upcoming 120 to 60 BPM boundary');
      await wait(() => !document.querySelector('[data-live-control=record]').disabled, 'record enabled');
      document.querySelector('[data-live-control=record]').click();
      mode = 'on'; seen = 0;
      await wait(() => seen >= 3, 'onset clock consumed'); await pause(400);
      const pad = document.querySelector('[data-live-note]');
      if (!pad) throw new Error('Missing playable note pad');
      pad.dispatchEvent(new PointerEvent('pointerdown', {pointerId:7,pointerType:'mouse',button:0,bubbles:true,cancelable:true}));
      await wait(() => draft().takes?.[0]?.notes?.[0]?.tick === 3984, 'onset after tempo boundary');
      mode = 'off'; seen = 0;
      await wait(() => seen >= 3, 'release clock consumed'); await pause(400);
      pad.dispatchEvent(new PointerEvent('pointerup', {pointerId:7,pointerType:'mouse',button:0,bubbles:true}));
      await wait(() => draft().takes?.[0]?.notes?.[0]?.endTick === 4032, 'release after tempo boundary');
      const note = draft().takes[0].notes[0];
      if (draft().takes[0].pattern !== 'phrase') throw new Error('Recording changed its destination');
      checks.push('packaged recording clock captures onset 3984 and release 4032 across the tempo step');
      sessionStorage.setItem(key, JSON.stringify({phase:'review',source,checks,note}));
      mode = '';
      document.querySelector('[data-live-control=finish]').click();
    }
    await wait(() => document.querySelector('.note-preview button[value=played]:not([disabled])'), 'played timing review');
    if (typeof window.__cicadaTempoClockReview === 'function') await window.__cicadaTempoClockReview();
    document.querySelector('#transport-form button[value=stop]').click();
    await wait(async () => !(await read('transport')).playing, 'stop before commit');
    document.querySelector('.note-preview button[value=played]').click();
    await wait(async () => /note \d+ at=144ticks length=48ticks/.test((await read()).source), 'original played timing committed');
    const captured = (await read()).source;
    await saved(captured);
    checks.push('Keep played timing preserves the 48-tick note in source');
    document.querySelector('#undo-form button').click(); await saved(source);
    document.querySelector('#redo-form button').click(); await saved(captured);
    checks.push('one Undo and Redo restore exact score snapshots');
    sessionStorage.setItem(key, JSON.stringify({phase:'reopen',source,captured,checks}));
    location.href = '/studio?dock=clip&pattern=phrase&region=detail';
    return null;
  })().catch(error => ({type:'cicada-native-smoke',ok:false,checks,error:String(error.stack || error)+'\nLIVE '+document.querySelector('[data-live-status]')?.textContent+'\nDRAFT '+JSON.stringify(draft())+'\nERRORS '+JSON.stringify(errors)}))
    .then(result => {
      if (!result) return;
      if (typeof window.__cicadaTempoClockResult === 'function') window.__cicadaTempoClockResult(result);
      else window.chrome?.webview?.postMessage(JSON.stringify(result));
    });
})();
