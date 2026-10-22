// Copied synthetic recordings and a stored pad performance; no pad playback or hardware.
(() => {
 if (location.hostname !== '127.0.0.1' || window.__cicadaPrintSourcesSmoke) return;
 window.__cicadaPrintSourcesSmoke = true;
 const key = 'cicada-print-sources-smoke';
 const read = async name => {
  const end = performance.now() + 5000;
  while (true) {
   try { const response = await fetch('/api/' + name, {cache: 'no-store'}); if (!response.ok) throw new Error(name + ': ' + response.status); return await response.json(); }
   catch (error) { if (!(error instanceof TypeError) || performance.now() >= end) throw error; await new Promise(resolve => setTimeout(resolve, 50)); }
  }
 };
 const wait = async (check, label) => { const end = performance.now() + 60000; while (performance.now() < end) { if (await check()) return; await new Promise(resolve => setTimeout(resolve, 50)); } throw new Error('Timed out: ' + label); };
 const ready = () => wait(() => window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]') && !document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'), 'managed forms');
 const button = (label, scope = document) => [...scope.querySelectorAll('button')].find(node => node.textContent.trim() === label && !node.disabled);
 const persist = state => sessionStorage.setItem(key, JSON.stringify(state));
 const click = (state, phase, node) => { if (!node) throw new Error('Missing action for ' + phase); state.phase = phase; persist(state); node.click(); };
 (async () => {
  await ready(); const initial = await read('state'), audio = await read('audio/config');
  if (!initial.source.includes('cicada-print-sources-test: disposable fixture') || initial.source.includes('arrange {') || audio.options.inputEnabled || String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null') throw new Error('Use only the copied print fixture with null audio');
  let state = JSON.parse(sessionStorage.getItem(key) || 'null');
  if (!state) {
   const takes = await read('takes'); if (takes.takes.length !== 3) throw new Error('Missing synthetic takes');
   state = {phase: 'pick', source: initial.source, take: takes.takes.find(t => t.timeline.startTick === 0).id, checks: ['three synthetic takes and a secondary arrangement load without hardware']}; persist(state);
  }
  if (window.__cicadaPortableKernelBase64 && !state.portableKernelCompiled) {
   await WebAssembly.compile(Uint8Array.from(atob(window.__cicadaPortableKernelBase64), c => c.charCodeAt(0))); state.portableKernelCompiled = true;
   state.checks.push('portable audio kernel compiles in the native Mac webview'); persist(state);
  }
   const saved = async predicate => wait(async () => { await ready(); const value = await read('state'); const status = document.querySelector('[data-sampler-status]')?.textContent || ''; if (state.phase === 'sampler-printed' && /retained|could not|failed/i.test(status)) throw new Error(status); return value.valid && predicate(value.source) && document.querySelector('#undo-form [name=revision]')?.value === value.revision; }, state.phase);
  while (true) {
   switch (state.phase) {
    case 'pick':
     await wait(() => document.querySelector('#cicada-vocal-lanes[data-vocal-lanes-ready=true]'), 'vocal lanes');
     click(state, 'whole', document.querySelector('[data-vocal-take="' + state.take + '"] [data-vocal-pick]')); break;
    case 'whole': {
     await wait(() => { const b = button('Use whole take'); return b?.closest('form').querySelector('[name=takeId]')?.value === state.take; }, 'selected whole take');
     click(state, 'vocal-printed', button('Use whole take')); break;
    }
    case 'vocal-printed':
     await saved(source => source.includes('// cicada-vocal-begin vox'));
     await wait(() => document.querySelector('[data-placement-block=vocal-vox]'), 'vocal placement');
     state.vocalSource = (await read('state')).source; state.checks.push('vocal metadata stays in entry while its secondary placement becomes active');
     click(state, 'vocal-undone', button('Undo')); break;
    case 'vocal-undone': await saved(source => source === state.source); click(state, 'vocal-redone', button('Redo')); break;
    case 'vocal-redone':
     await saved(source => source === state.vocalSource);
     state.phase = 'sampler'; persist(state); break;
    case 'sampler': {
     const section = document.querySelector('#browser-sampler'); if (!section.open) section.querySelector('summary').click();
     await wait(() => document.querySelector('#cicada-sampler[data-sampler-ready=true]'), 'stored sampler performance');
     const form = document.querySelector('[data-sampler-resample] form');
     const perf = JSON.parse(form.querySelector('[name=performance]').value); if (perf.hits.length !== 1) throw new Error('Stored performance missing');
     for (const [name,value] of Object.entries({newName: 'native-print', at: '@5.1.1'})) { const input = form.querySelector('[name=' + name + ']'); input.value = value; input.dispatchEvent(new Event('input', {bubbles: true})); input.dispatchEvent(new Event('change', {bubbles: true})); }
     click(state, 'sampler-printed', button('Resample performance', form)); break;
    }
    case 'sampler-printed':
     await saved(source => source.includes('asset native-print-audio'));
     state.printedSource = (await read('state')).source; state.checks.push('stored sampler performance prints into the same secondary arrangement');
     click(state, 'sampler-undone', button('Undo')); break;
    case 'sampler-undone': await saved(source => source === state.vocalSource); click(state, 'sampler-redone', button('Redo')); break;
    case 'sampler-redone':
     await saved(source => source === state.printedSource); state.checks.push('native Undo/Redo restores both kinds of print across files');
     state.previousExport = (await read('export')).id; click(state, 'export', button('Export')); break;
    case 'export': await wait(() => button('Render WAV'), 'WAV controls'); click(state, 'render', button('Render WAV')); break;
    case 'render': {
     let job; await wait(async () => { job = await read('export'); if (job.state === 'failed') throw new Error(JSON.stringify(job)); return job.id !== state.previousExport && ['succeeded', 'shortfall'].includes(job.state); }, 'WAV export');
     const response = await fetch(job.downloadURL); const bytes = new Uint8Array(await response.arrayBuffer());
     if (!response.ok || String.fromCharCode(...bytes.slice(0,4)) !== 'RIFF' || bytes.length < 100000) throw new Error('Invalid printed WAV');
     const digest = [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))].map(n => n.toString(16).padStart(2,'0')).join('');
     state.checks.push('native printed song exports WAV ' + digest); sessionStorage.removeItem(key);
     return {type: 'cicada-native-smoke', ok: true, checks: state.checks};
    }
    default: throw new Error('Unexpected phase: ' + state.phase);
   }
  }
 })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks: [], error: String(error) + '\n' + String(error.stack || '') + '\n' + sessionStorage.getItem(key) + '\n' + document.body.innerText.slice(-4000)})).then(result => window.chrome?.webview?.postMessage(JSON.stringify(result)));
})();
