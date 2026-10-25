// Disposable WKWebView only. All audio is rendered offline with null output.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__cicadaStemsExportSmoke) return;
  window.__cicadaStemsExportSmoke = true;
  const checks = [], errors = [], visibility = [document.visibilityState];
  let navigations = 0;
  document.addEventListener('visibilitychange', () => visibility.push(document.visibilityState));
  document.addEventListener('gosx:navigate', () => navigations++);
  addEventListener('error', event => errors.push(event.message));
  addEventListener('unhandledrejection', event => errors.push(String(event.reason)));
  const pause = () => new Promise(resolve => setTimeout(resolve, 50));
  async function wait(check, label, timeout = 70000) {
    const end = performance.now() + timeout;
    while (performance.now() < end) { if (await check()) return; await pause(); }
    throw new Error('Timed out: ' + label);
  }
  async function read(path) {
    const response = await fetch('/api/' + path, {cache: 'no-store'});
    if (!response.ok) throw new Error(path + ': ' + response.status);
    return response.json();
  }
  // Inspect the ZIP directory without claiming to decode the audio on this host.
  function zipEntries(bytes) {
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    let end = bytes.length - 22;
    while (end >= Math.max(0, bytes.length - 65557) && view.getUint32(end, true) !== 0x06054b50) end--;
    if (end < 0 || view.getUint32(end, true) !== 0x06054b50) throw new Error('ZIP directory is missing');
    const count = view.getUint16(end + 10, true), names = [];
    let offset = view.getUint32(end + 16, true);
    for (let index = 0; index < count; index++) {
      if (view.getUint32(offset, true) !== 0x02014b50) throw new Error('Invalid ZIP directory entry');
      const length = view.getUint16(offset + 28, true);
      const name = new TextDecoder().decode(bytes.subarray(offset + 46, offset + 46 + length));
      if (!name || name.includes('/') || name.includes('\\')) throw new Error('Unexpected ZIP path');
      names.push(name);
      offset += 46 + length + view.getUint16(offset + 30, true) + view.getUint16(offset + 32, true);
    }
    return names;
  }
  async function download(job) {
    const response = await fetch(job.downloadURL, {cache: 'no-store'});
    const bytes = new Uint8Array(await response.arrayBuffer());
    if (!response.ok || response.headers.get('Content-Type') !== 'application/zip' ||
        response.headers.get('Cache-Control') !== 'no-store' ||
        !/attachment;.*filename=.*\.zip/.test(response.headers.get('Content-Disposition') || '') ||
        bytes.length < 1000 || bytes[0] !== 80 || bytes[1] !== 75) throw new Error('Completed ZIP cannot be downloaded privately');
    const names = zipEntries(bytes);
    for (const name of ['manifest.json', 'return-a.wav', 'return-b.wav', 'music.wav', 'sfx.wav', 'master.wav']) {
      if (!names.includes(name)) throw new Error('Missing ZIP entry: ' + name);
    }
    if (names.filter(name => /^\d+-.*\.wav$/.test(name)).length !== job.tracks || names.length !== job.tracks + 6) {
      throw new Error('ZIP track count does not match the delivery');
    }
    const hash = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), n => n.toString(16).padStart(2, '0')).join('');
    return {bytes: bytes.length, names, sha256: hash};
  }
  (async () => {
    await wait(() => window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]'), 'native shell');
    await wait(() => !document.hidden, 'visible native window');
    const initial = await read('state'), audio = await read('audio/config');
    if (!initial.source.includes('title "Desktop CI"') || audio.options.inputEnabled ||
        String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null') {
      throw new Error('Use the disposable Desktop CI score with null audio and input off');
    }
    const mix = await read('export'), midi = await read('export/midi');
    const editor = document.querySelector('#score textarea'), draft = editor.value + '\n// unsaved stems export draft\n';
    editor.value = draft; editor.dispatchEvent(new Event('input', {bubbles: true}));
    const trigger = document.querySelector('[data-gosx-disclosure-target="#export-dialog"]');
    trigger.focus(); const previousFocus = document.activeElement; trigger.click();
    const dialog = document.querySelector('#export-dialog'), panel = document.querySelector('#stems-export');
    panel.querySelector('summary').click();
    const form = panel.querySelector('form[action="/__actions/export-stems"]');
    const button = form.querySelector('button[type=submit]'), result = document.querySelector('#stems-export-result');
    const sourceRevision = form.elements.namedItem('source_revision').value;
    if (!/^[a-f0-9]{64}$/.test(sourceRevision)) throw new Error('Stems omitted the reviewed source version');
    const conflict = 'Saved score files or libraries changed. Refresh Studio before exporting stems again.';
    form.elements.namedItem('source_revision').value = 'unreviewed-native-test-source';
    form.requestSubmit(button);
    await wait(() => panel.textContent.includes(conflict), 'source conflict');
    const rejected = await read('export/stems');
    if (rejected.id || rejected.state !== 'idle' || dialog.hidden || editor.value !== draft) throw new Error('Rejected export lost its draft or started a job');
    checks.push('unreviewed sources are rejected without starting a native stem export');

    form.elements.namedItem('source_revision').value = sourceRevision;
    form.elements.namedItem('rate').value = '48000';
    form.elements.namedItem('rate').dispatchEvent(new Event('change', {bubbles: true}));
    const before = navigations;
    button.focus(); form.requestSubmit(button);
    const rateControl = form.elements.namedItem('rate');
    rateControl.focus();
    let job;
    await wait(async () => {
      job = await read('export/stems');
      if (job.state === 'failed') throw new Error(job.error);
      return job.state === 'succeeded' && job.id;
    }, 'offline stems render', 90000);
    await wait(() => result.querySelector('a[download]').getAttribute('href') === job.downloadURL &&
      result.querySelector('[data-gosx-live-bind=progress]').textContent === 'Stems ready to download.', 'focused live delivery');
    if (dialog.hidden || !document.querySelector('#studio-workbench').inert || navigations !== before ||
        editor !== document.querySelector('#score textarea') || editor.value !== draft || !panel.open || document.activeElement !== rateControl) {
      throw new Error('Stems render replaced its draft, dialog or open controls');
    }
    if (job.source_revision !== sourceRevision || job.current_source_revision !== sourceRevision ||
        panel.textContent.includes(conflict) || !result.textContent.includes('These stems match the saved score.')) {
      throw new Error('Completed stems lost reviewed-source identity or retry feedback');
    }
    if (form.querySelector('.form-status')?.textContent !== 'Stem rendering started.') throw new Error('Missing background render acknowledgement');
    checks.push('native offline rendering retains the open dialog, modal lock, source editor and unsaved draft');
    checks.push('focused export controls receive completion progress and matching saved-version labels');
    const archive = await download(job);
    checks.push('private ZIP download includes every track, effect return, bus, reference master and manifest');
    const link = result.querySelector('a[download]');
    const saved = await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error('Native export save did not complete')), 30000);
      link.addEventListener('cicada:export-saved', event => {
        clearTimeout(timeout); resolve(event.detail);
      }, {once: true});
      link.click();
      if (link.getAttribute('aria-busy') !== 'true') reject(new Error('Download click did not enter the native save bridge'));
    });
    if (saved.state !== 'saved' || !saved.name.endsWith('.zip') ||
        !result.querySelector('.native-export-status')?.textContent.startsWith('Saved ') || link.hasAttribute('aria-busy')) {
      throw new Error('Native export save failed: ' + JSON.stringify(saved));
    }
    checks.push('clicking Download streams the ZIP through the native host to the test destination; smoke runner verifies saved SHA-256');

    form.elements.namedItem('source_revision').value = 'another-unreviewed-native-test-source';
    await wait(() => !button.disabled, 'render control ready');
    form.requestSubmit(button);
    await wait(() => panel.textContent.includes(conflict), 'repeat source conflict');
    const retained = await read('export/stems'), retainedArchive = await download(retained);
    if (retained.id !== job.id || retainedArchive.sha256 !== archive.sha256 || editor.value !== draft) throw new Error('Rejected retry replaced the last completed ZIP or draft');
    checks.push('a rejected retry preserves the last completed ZIP byte-for-byte');

    dialog.querySelector('[data-gosx-disclosure-close]').click();
    if (!dialog.hidden || document.querySelector('#studio-workbench').inert || document.activeElement !== previousFocus) throw new Error('Close did not unlock Studio and restore focus');
    trigger.click();
    await wait(() => !dialog.hidden && document.querySelector('#stems-export-result a[download]').getAttribute('href') === job.downloadURL, 'reopened delivery');
    dialog.querySelector('[data-gosx-disclosure-close]').dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true, cancelable: true}));
    if (!dialog.hidden || document.querySelector('#studio-workbench').inert) throw new Error('Escape did not close Export');
    checks.push('Close, reopen and Escape preserve the completed delivery and release the modal lock');
    const final = await read('state'), finalMix = await read('export'), finalMIDI = await read('export/midi');
    if (final.source !== initial.source || final.revision !== initial.revision || finalMix.id !== mix.id || finalMIDI.id !== midi.id || errors.length) {
      throw new Error('Stem export changed saved music, another export, or raised browser errors: ' + JSON.stringify(errors));
    }
    checks.push('stems leave the saved score and independent WAV/MIDI deliveries unchanged');
    return {type: 'cicada-native-smoke', ok: true, checks, archive, sampleRate: job.rate, tracks: job.tracks};
  })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks,
    error: String(error) + '\n' + error.stack + '\n' + JSON.stringify({visibility, errors,
      focused: document.activeElement?.id || document.activeElement?.tagName,
      progress: document.querySelector('#stems-export-result [data-gosx-live-bind=progress]')?.textContent,
      status: document.querySelector('#stems-export .form-status')?.textContent})
  })).then(result => {
    window.__cicadaStemsExportResult = result;
    if (window.chrome?.webview) window.chrome.webview.postMessage(JSON.stringify(result));
  });
})();
