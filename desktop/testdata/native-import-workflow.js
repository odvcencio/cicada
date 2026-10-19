// Runs in a disposable null-audio app. This exercises the real file input and
// multipart import form; native chooser presentation is a separate SDK check.
// No pad, transport, microphone, or direct mutation API is triggered.
(() => {
  if (location.protocol !== 'http:' || !['127.0.0.1', 'localhost', '[::1]'].includes(location.hostname)) return;
  if (window.__cicadaImportSmokeRunning) return;
  window.__cicadaImportSmokeRunning = true;
  window.__cicadaImportDocument = Math.random().toString(36);
  const storageKey = 'cicada-native-import-smoke';
  const saved = JSON.parse(sessionStorage.getItem(storageKey) || 'null');
  const progress = saved || {phase: 'upload', started: Date.now(), checks: []};
  const errors = [];
  const filename = 'cicada-private-original.wav', rate = 48000, frames = 12000;
  addEventListener('error', event => errors.push(event.message));
  addEventListener('unhandledrejection', event => errors.push(String(event.reason)));
  const pause = () => new Promise(resolve => setTimeout(resolve, 100));
  function require(condition, message) { if (!condition) throw new Error(message); }
  function save() { sessionStorage.setItem(storageKey, JSON.stringify(progress)); }
  async function wait(check, name) {
    while (Date.now() - progress.started < 35000) {
      if (await check()) return;
      await pause();
    }
    throw new Error('Timed out: ' + name);
  }
  async function state(path) {
    const response = await fetch('/api/' + path, {cache: 'no-store'});
    require(response.ok, path + ': HTTP ' + response.status);
    return response.json();
  }
  function wav() {
    const data = new ArrayBuffer(44 + frames * 2), view = new DataView(data);
    const text = (at, value) => [...value].forEach((character, index) => view.setUint8(at + index, character.charCodeAt(0)));
    text(0, 'RIFF'); view.setUint32(4, data.byteLength - 8, true); text(8, 'WAVE');
    text(12, 'fmt '); view.setUint32(16, 16, true); view.setUint16(20, 1, true);
    view.setUint16(22, 1, true); view.setUint32(24, rate, true); view.setUint32(28, rate * 2, true);
    view.setUint16(32, 2, true); view.setUint16(34, 16, true); text(36, 'data'); view.setUint32(40, frames * 2, true);
    for (let frame = 0; frame < frames; frame++) {
      const envelope = Math.sin(Math.PI * frame / frames) ** 2;
      const sample = .15 * envelope * Math.sin(2 * Math.PI * 233 * frame / rate);
      view.setInt16(44 + frame * 2, Math.round(sample * 32767), true);
    }
    return new File([data], filename, {type: 'audio/wav'});
  }
  async function bankReady(bank) {
    await wait(() => {
      const sampler = document.querySelector('#cicada-sampler');
      return sampler?.dataset.samplerReady === 'true' && sampler.dataset.samplerBankRevision === bank.bankRevision &&
        [...sampler.querySelectorAll('[data-sampler-pad]')].every(pad => !pad.disabled);
    }, 'all sixteen imported pad sounds decoded by the real sampler engine');
  }
  async function verifyImport() {
    let bank;
    await wait(async () => {
      bank = await state('sampler');
      return bank.bank.sources?.some(source => source.name === filename);
    }, 'actual multipart import to persist the source');
    const source = bank.bank.sources.find(source => source.name === filename);
    require(/^[0-9a-f]{64}$/.test(source.id) && source.asset.sha256 === source.id, 'Imported source digest missing');
    require(source.asset.frames === frames && source.asset.rate_hz === rate && source.asset.channels === 2 && source.asset.format === 'wav', 'Imported canonical asset dimensions differ');
    require(source.asset.path === 'sampler/audio/' + source.id + '.wav', 'Imported asset storage path differs');
    require(source.peaks?.some(peak => peak > 0), 'Imported waveform has no original signal');
    require(bank.bank.pads.length === 16 && bank.sounds.length === 16, 'Import did not create sixteen chops');
    bank.bank.pads.forEach((pad, index) => {
      require(pad.source === source.id && pad.end > pad.start, 'Imported chop source or bounds missing');
      require(index === 0 ? pad.start === 0 : pad.start === bank.bank.pads[index - 1].end, 'Chops leave a gap');
      require(/^[0-9a-f]{64}$/.test(bank.sounds[index]), 'Prepared sound ID missing');
    });
    require(bank.bank.pads[15].end === frames, 'Chops do not cover the complete imported source');
    await bankReady(bank);
    const prepared = await fetch('/media/sampler/' + bank.sounds[0] + '.wav');
    require(prepared.ok && prepared.headers.get('content-type')?.startsWith('audio/wav'), 'Prepared imported sound is unavailable');
    const bytes = new Uint8Array(await prepared.arrayBuffer());
    const text = at => String.fromCharCode(...bytes.slice(at, at + 4));
    require(bytes.length > 44 && text(0) === 'RIFF' && text(8) === 'WAVE', 'Prepared imported sound is not WAV');
    progress.source = source;
    progress.bankRevision = bank.bankRevision;
    progress.sounds = bank.sounds;
    progress.checks.push('Actual file input and multipart import persist an original WAV asset and sixteen contiguous chops');
    progress.checks.push('Real sampler engine decodes all sixteen sounds; prepared imported WAV is served');
    progress.document = window.__cicadaImportDocument;
    progress.phase = 'pad-two';
    save();
    document.querySelector('[data-sampler-edit="2"]').click();
  }
  async function afterNavigation(pad) {
    await wait(() => new URL(location.href).searchParams.get('pad') === String(pad) &&
      document.querySelector('#sampler-pad-editor input[name=pad]')?.value === String(pad), 'managed navigation to pad ' + pad);
    require(window.__cicadaImportDocument === progress.document, 'Pad edit unexpectedly replaced the browser document');
    const bank = await state('sampler');
    require(bank.bankRevision === progress.bankRevision && JSON.stringify(bank.sounds) === JSON.stringify(progress.sounds), 'Imported bank changed after managed navigation');
    const source = bank.bank.sources.find(source => source.id === progress.source.id);
    require(JSON.stringify(source) === JSON.stringify(progress.source), 'Imported source metadata changed after managed navigation');
    await bankReady(bank);
    const editor = document.querySelector('#sampler-pad-editor form');
    require(editor.elements.source.value === source.id && Number(editor.elements.start.value) === bank.bank.pads[pad - 1].start &&
      Number(editor.elements.end.value) === bank.bank.pads[pad - 1].end, 'Navigated pad editor shows stale imported sample');
  }
  (async () => {
    await wait(() => document.querySelector('#studio-shell[data-shell-actions=ready]'), 'native shell engine');
    if (progress.phase === 'upload') {
      const original = await state('state'), audio = await state('audio/config?runtime=1');
      require(original.valid && /\btrack\s/.test(original.source), 'Private score is not ready');
      require(audio.options.inputEnabled === false && !(await state('transport')).playing, 'Test requires disabled microphone and stopped transport');
      progress.originalSource = original.source;
      const section = document.querySelector('#browser-sampler');
      require(section, 'Sampler Browser section missing');
      if (!section.open) section.querySelector('summary').click();
      const form = document.querySelector('form[action="/__sampler/import"]');
      require(form, 'Actual Sampler import form missing');
      const input = form.querySelector('input[type=file][name=audio]');
      require(typeof DataTransfer === 'function' && typeof File === 'function', 'WebKit lacks the standard DataTransfer/File API; native file chooser remains a separate proof');
      const file = wav(), transfer = new DataTransfer();
      transfer.items.add(file);
      try { input.files = transfer.files; }
      catch (error) { throw new Error('WebKit rejected the standard file-input files setter: ' + error); }
      require(input.files.length === 1 && input.files[0].name === filename && input.files[0].size === file.size, 'WebKit did not accept the standard file-input files setter');
      input.dispatchEvent(new Event('change', {bubbles: true}));
      const payload = new FormData(form).get('audio');
      require(payload instanceof File && payload.name === filename && payload.size === file.size, 'Actual form does not contain the input File');
      progress.checks.push('Standard WebKit File/DataTransfer populates the real WAV file input and FormData');
      progress.phase = 'imported';
      save();
      form.querySelector('button[type=submit]').click();
      // This multipart form uses native navigation. Its old document's fetches
      // can be aborted during unload, so resume verification only after the
      // host reinjects this workflow into the redirected document.
      await new Promise((_, reject) => setTimeout(() => reject(new Error('Import form did not navigate within fifteen seconds')), 15000));
    }
    if (progress.phase === 'imported') await verifyImport();
    if (progress.phase === 'pad-two') {
      await afterNavigation(2);
      progress.phase = 'pad-one';
      save();
      document.querySelector('[data-sampler-edit="1"]').click();
    }
    if (progress.phase === 'pad-one') {
      await afterNavigation(1);
      const current = await state('state'), audio = await state('audio/config?runtime=1');
      require(current.valid && current.source === progress.originalSource, 'Import or navigation changed the original score tracks');
      require(audio.options.inputEnabled === false && !(await state('transport')).playing, 'Import unexpectedly enabled input or transport');
      require(errors.length === 0, errors.join('; '));
      progress.checks.push('Two real managed pad-editor navigations preserve source, bank, decoded sounds and current chop fields');
      progress.checks.push('Original score tracks remain intact; no pads, transport or microphone triggered');
      progress.checks.push('Imported asset: ' + JSON.stringify(progress.source.asset));
      progress.phase = 'complete';
      save();
    }
    return {type: 'cicada-native-smoke', ok: true, checks: progress.checks};
  })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks: progress.checks, error: String(error) + '\n' + (error.stack || '')}))
    .then(result => window.chrome.webview.postMessage(JSON.stringify(result)));
})();
