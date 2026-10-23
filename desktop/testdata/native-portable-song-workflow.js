// Disposable portable-song fixture, synthetic recordings and null audio only.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__cicadaPortableSmoke) return;
  window.__cicadaPortableSmoke = true;
  const key = 'cicada-portable-smoke';
  const read = async name => {
    const response = await fetch('/api/' + name, {cache: 'no-store'});
    if (!response.ok) throw new Error(name + ': ' + response.status);
    return response.json();
  };
  async function wait(check, label, timeout = 60000) {
    const end = performance.now() + timeout;
    while (performance.now() < end) {
      if (await check()) return;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    throw new Error('Timed out: ' + label);
  }
  const persist = state => sessionStorage.setItem(key, JSON.stringify(state));
  (async () => {
    await wait(() => window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]'), 'native shell');
    const initial = await read('state'), audio = await read('audio/config');
    if (!initial.valid || !initial.source.includes('title "Carry the song"') || audio.options.inputEnabled || String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null') {
      throw new Error('Use only the portable synthetic fixture and null audio');
    }
    let state = JSON.parse(sessionStorage.getItem(key) || 'null');
    if (!state) {
      const rows = [...document.querySelectorAll('#scene-takes [data-scene-take]')];
      if (rows.length !== 2 || rows[0].querySelector('button').disabled || !rows[1].querySelector('button').disabled) throw new Error('The two retained scene takes are missing');
      const preview = await fetch(rows[0].querySelector('audio').src);
      const bytes = new Uint8Array(await preview.arrayBuffer());
      if (!preview.ok || String.fromCharCode(...bytes.slice(0, 4)) !== 'RIFF') throw new Error('Retained preview failed');
      state = {phase: 'select', source: initial.source, revision: initial.revision, first: rows[0].dataset.sceneTake, checks: ['copied project opens with two retained scene takes and a WAV preview']};
      persist(state);
    }
    const saved = async check => wait(async () => {
      const value = await read('state');
      return value.valid && check(value.source) && document.querySelector('#undo-form input[name=revision]')?.value === value.revision;
    }, state.phase);
    const click = (next, selector) => {
      state.phase = next;
      persist(state); // Survive either a GoSX update or a full document navigation.
      const button = document.querySelector(selector);
      if (!button || button.disabled) throw new Error('Unavailable action: ' + selector);
      button.click();
    };
    while (true) {
      switch (state.phase) {
        case 'select':
          click('selected', '[data-scene-take="' + state.first + '"] button');
          break;
        case 'selected':
          await saved(source => source.includes('asset ' + state.first + ' '));
          await wait(() => {
            const button = document.querySelector('[data-scene-take="' + state.first + '"] button');
            return button?.disabled && button.textContent === 'In verse';
          }, 'selected scene binding');
          state.selected = (await read('state')).source;
          state.checks.push('native selection resolves imported instruments, secondary patterns and the owning scene file');
          click('undone', '#undo-form button');
          break;
        case 'undone':
          await saved(source => source === state.source);
          state.checks.push('Undo restores the exact saved source');
          click('redone', '#redo-form button');
          break;
        case 'redone':
          await saved(source => source === state.selected);
          state.checks.push('Redo restores the chosen take');
          click('restored', '#undo-form button');
          break;
        case 'restored': {
          await saved(source => source === state.source);
          document.querySelector('[data-gosx-disclosure-target="#export-dialog"]').click();
          await wait(() => !document.querySelector('#export-dialog').hidden, 'export dialog');
          const previous = (await read('export')).id;
          document.querySelector('#export button[type=submit]').click();
          let job;
          await wait(async () => {
            job = await read('export');
            if (job.state === 'failed') throw new Error(JSON.stringify(job));
            return job.id !== previous && ['succeeded', 'shortfall'].includes(job.state);
          }, 'portable WAV export', 90000);
          const response = await fetch(job.downloadURL);
          const bytes = new Uint8Array(await response.arrayBuffer());
          if (!response.ok || String.fromCharCode(...bytes.slice(0, 4)) !== 'RIFF' || job.revision !== state.revision) throw new Error('Export lost the restored song');
          const digest = [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))].map(n => n.toString(16).padStart(2, '0')).join('');
          state.checks.push('restored portable song exports WAV ' + digest);
          const midi = document.querySelector('#midi-export');
          if (!midi.open) midi.querySelector('summary').click();
          midi.querySelector('button[type=submit]').click();
          let notes;
          await wait(async () => {
            notes = await read('export/midi');
            return notes.state === 'ready' && midi.querySelector('a[download]')?.getAttribute('href') === notes.downloadURL;
          }, 'native MIDI delivery');
          const midiResponse = await fetch(notes.downloadURL);
          const midiBytes = new Uint8Array(await midiResponse.arrayBuffer());
          if (!midiResponse.ok || String.fromCharCode(...midiBytes.slice(0, 4)) !== 'MThd' || notes.notes !== 52 || notes.audio_tracks_omitted.join(',') !== 'guitar') throw new Error('MIDI lost notes or implied it included recordings');
          if ((await read('export')).id !== job.id || (await read('state')).source !== state.source) throw new Error('MIDI replaced the WAV or changed the song');
          const midiDigest = [...new Uint8Array(await crypto.subtle.digest('SHA-256', midiBytes))].map(n => n.toString(16).padStart(2, '0')).join('');
          state.checks.push('native MIDI preserves the WAV and exports notes ' + midiDigest);
          sessionStorage.removeItem(key);
          return {type: 'cicada-native-smoke', ok: true, checks: state.checks};
        }
        default: throw new Error('Unexpected smoke phase: ' + state.phase);
      }
    }
  })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks: [], error: String(error) + '\n' + error.stack + '\n' + sessionStorage.getItem(key)}))
    .then(result => window.chrome?.webview?.postMessage(JSON.stringify(result)));
})();
