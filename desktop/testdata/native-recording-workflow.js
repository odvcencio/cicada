// Run with desktop/smoke.py --workflow and its disposable --audio null app.
// This exercises real forms and navigation; it never enables a microphone.
// Null output cannot capture PCM. Saved takes/waveforms need the separate,
// explicitly coordinated native microphone test, not a simulated success here.
(() => {
  if (location.protocol !== 'http:' || !['127.0.0.1', 'localhost', '[::1]'].includes(location.hostname)) return;
  if (window.__cicadaNativeRecordingSmokeRunning) return;
  window.__cicadaNativeRecordingSmokeRunning = true;
  const checks = [], errors = [], submissions = [], responses = [];
  const fetchOriginal = window.fetch;
  // Observe the real action's completion, without replacing any response.
  window.fetch = async function(...args) {
    const response = await fetchOriginal.apply(this, args);
    if (new URL(typeof args[0] === 'string' ? args[0] : args[0].url, location.href).pathname === '/__actions/take')
      responses.push(response.status);
    return response;
  };
  addEventListener('error', event => errors.push(event.message));
  addEventListener('unhandledrejection', event => errors.push(String(event.reason)));
  document.addEventListener('submit', event => {
    if (!event.target.matches('.vocal-record form[action$="/__actions/take"]')) return;
    const fields = new FormData(event.target);
    submissions.push({
      action: event.submitter?.value, track: fields.get('track'),
      revision: fields.get('revision'), csrf: fields.get('csrf_token'),
    });
  }, true);
  async function wait(check, name) {
    const until = performance.now() + 15000;
    while (performance.now() < until) {
      if (await check()) return;
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    throw new Error('Timed out: ' + name);
  }
  async function state(path) {
    const response = await fetch('/api/' + path, {cache: 'no-store'});
    if (!response.ok) throw new Error(path + ': HTTP ' + response.status);
    return response.json();
  }
  const trackSelect = () => document.querySelector('.vocal-record select[name=track]');
  async function ready() {
    await wait(() => document.querySelector('#studio-shell[data-shell-actions=ready]') &&
      document.querySelector('#cicada-vocals[data-capture-active=false]'), 'recording engines');
  }
  async function addTrack(name) {
    const form = document.querySelector('.vocal-setup form[action$="/__actions/clip"]');
    if (!form) throw new Error('Add vocal track form missing');
    const input = form.querySelector('input[name=newName]');
    input.value = name;
    input.dispatchEvent(new Event('input', {bubbles: true}));
    form.querySelector('button[type=submit]').click();
    await wait(() => [...(trackSelect()?.options || [])].some(option => option.value === name), name + ' track appears');
    await ready();
    if (!(await state('state')).source.includes('track ' + name + ' audio')) throw new Error(name + ' was not saved');
  }
  async function selectTrack(name) {
    const link = [...document.querySelectorAll('nav[aria-label="Vocal tracks"] a')]
      .find(node => node.textContent === name);
    if (!link) throw new Error(name + ' selection link missing');
    link.click();
    await wait(() => trackSelect()?.value === name && new URL(location.href).searchParams.get('vocalTrack') === name,
      name + ' selected after navigation');
    await ready();
    const revision = (await state('state')).revision;
    if (document.querySelector('.vocal-record input[name=revision]')?.value !== revision) throw new Error('Stale recording revision');
  }
  async function recordShortcut(name) {
    await wait(() => !document.querySelector('.vocal-record [data-capture-control=record]')?.disabled, 'Record take enabled');
    const revision = (await state('state')).revision;
    const count = submissions.length;
    const responseCount = responses.length;
    document.activeElement?.blur();
    document.body.dispatchEvent(new KeyboardEvent('keydown', {
      key: 'R', code: 'KeyR', metaKey: true, shiftKey: true, bubbles: true, cancelable: true,
    }));
    await wait(() => submissions.length === count + 1, 'Command-Shift-R submits actual record form');
    const sent = submissions[count];
    if (sent.action !== 'record' || sent.track !== name || sent.revision !== revision || !sent.csrf)
      throw new Error('Recording shortcut lost selected track, revision, CSRF, or action');
    await wait(() => responses.length === responseCount + 1, 'record action completes');
    if (responses[responseCount] !== 422) throw new Error('Disabled input did not return its validation error');
    await wait(() => [...document.querySelectorAll('[role=alert], .notice')]
      .some(node => /enable duplex input/i.test(node.textContent)), 'disabled input is explained');
    await ready();
    const capture = await state('capture');
    if (capture.activeCapture || capture.input.enabled) throw new Error('Silent smoke unexpectedly opened input');
  }
  (async () => {
    await wait(() => document.querySelector('#studio-shell[data-shell-actions=ready]'), 'native shell engine');
    if (window.__gosx.commands.platformMod() !== 'meta') throw new Error('Mac Command modifier not selected');
    const audio = await state('audio/config');
    const backend = String(audio.inventory.backendName || audio.inventory.backend).toLowerCase();
    if (backend !== 'null' || audio.options.inputEnabled) throw new Error('Recording smoke requires null audio with input off');
    const initial = await state('state');
    if (!initial.source.includes('title "Desktop CI"')) throw new Error('Disposable desktop smoke fixture required');
    if ((await state('takes')).takes?.length) throw new Error('Fresh smoke must not contain retained takes');
    checks.push('disposable null backend and microphone off');

    // smoke.py's minimal playback fixture is edition 1. Enable the edition 2
    // audio-track substrate using the same saved Code form a person uses.
    if (!/^cicada 2\b/.test(initial.source)) {
      const editor = document.querySelector('#score textarea');
      editor.value = 'cicada 2\n' + initial.source.replace('pattern pulse {', 'pattern pulse acid steps=4 {');
      editor.dispatchEvent(new Event('input', {bubbles: true}));
      document.querySelector('#score form[action$="/__actions/source"] button[type=submit]').click();
      await wait(async () => /^cicada 2\b/.test((await state('state')).source), 'edition 2 score saved');
    }
    await ready();
    checks.push('edition 2 vocal workspace appears after actual Code save');

    // The DAW calls the song arrangement Arrange; its vocal Session surface
    // stays mounted there while the Session tab displays the launch matrix.
    document.querySelector('#view-tab-arrange').click();
    await wait(() => !document.querySelector('#view-panel-arrange').hidden, 'Arrange vocal workspace');
    await addTrack('native-vocal');
    await addTrack('native-harmony');
    checks.push('two vocal tracks saved through actual Add vocal track forms');

    await selectTrack('native-harmony');
    await recordShortcut('native-harmony');
    checks.push('Command-Shift-R submits selected harmony track with current revision and CSRF');
    checks.push('silent backend explains disabled input without opening a microphone');

    const csrf = document.querySelector('.vocal-record input[name=csrf_token]').value;
    document.querySelector('#view-tab-session').click();
    await wait(() => !document.querySelector('#view-panel-session').hidden, 'Session launch matrix');
    document.querySelector('#view-tab-arrange').click();
    await wait(() => !document.querySelector('#view-panel-arrange').hidden, 'return to vocal workspace');
    await selectTrack('native-vocal');
    if (document.querySelector('.vocal-record input[name=csrf_token]').value === csrf)
      throw new Error('Vocal track navigation did not refresh form credentials');
    if (location.pathname !== '/studio') throw new Error('Vocal navigation left the Studio shell');
    await recordShortcut('native-vocal');
    checks.push('record shortcut and fresh form bindings survive managed vocal navigation and tab switching');

    const final = await state('state');
    if (!final.valid || !final.source.includes('track bass acid') || !final.source.includes('track native-harmony audio'))
      throw new Error('Vocal setup damaged the song');
    if ((await state('takes')).takes?.length) throw new Error('Disabled input created an unexpected take');
    if (document.querySelector('.vocal-record [data-capture-control=stop]')?.disabled !== true)
      throw new Error('Finish take enabled with no active recording');
    if (errors.length) throw new Error(errors.join('; '));
    checks.push('song remains valid; no false take or enabled Finish control');
    return {type: 'cicada-native-smoke', ok: true, checks};
  })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks, error: String(error.stack || error)}))
    .then(result => window.chrome.webview.postMessage(JSON.stringify(result)));
})();
