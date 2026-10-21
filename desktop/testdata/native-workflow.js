// Runs inside the disposable desktop app, using its actual WKWebView runtime.
(() => {
  if (location.protocol !== 'http:' || !['127.0.0.1', 'localhost', '[::1]'].includes(location.hostname)) return;
  if (window.__cicadaNativeSmokeRunning) return;
  window.__cicadaNativeSmokeRunning = true;
  const checks = [], errors = [];
  addEventListener('error', event => errors.push(event.message));
  addEventListener('unhandledrejection', event => errors.push(String(event.reason)));
  const pause = () => new Promise(resolve => setTimeout(resolve, 100));
  async function wait(check, name) {
    const until = performance.now() + 15000;
    while (performance.now() < until) {
      if (await check()) return;
      await pause();
    }
    throw new Error('Timed out: ' + name);
  }
  async function state(path) {
    const response = await fetch('/api/' + path, {cache: 'no-store'});
    if (!response.ok) throw new Error(path + ': HTTP ' + response.status);
    return response.json();
  }
  function key(name, code, meta = false, shift = false, target = document.body) {
    target.focus();
    target.dispatchEvent(new KeyboardEvent('keydown', {key: name, code, metaKey: meta, shiftKey: shift, bubbles: true, cancelable: true}));
  }
  async function saved(source) {
    await wait(async () => {
      const current = await state('state');
      return current.source.replace(/\r\n?/g, '\n') === source.replace(/\r\n?/g, '\n') && current.valid &&
        document.querySelector('#score input[name=revision]')?.value === current.revision &&
        document.querySelector('#undo-form input[name=revision]')?.value === current.revision;
    }, 'score and displayed revision to agree');
  }
  (async () => {
    await wait(() => document.querySelector('#studio-shell[data-shell-actions=ready]') && window.__gosx?.commands?.available('edit.save'), 'native engines');
    checks.push('native engines mounted');
    if (window.__gosx.commands.platformMod() !== 'meta') throw new Error('Mac Command modifier not selected');
    checks.push('Mac Command modifier');

    const original = (await state('state')).source;
    const changed = original + '\n// Native WKWebView save check\n';
    const editor = document.querySelector('#score textarea');
    if (!editor) throw new Error('Score editor missing');
    editor.value = changed;
    editor.dispatchEvent(new Event('input', {bubbles: true}));
    key('s', 'KeyS', true, false, editor);
    await saved(changed);
    checks.push('Command-S saves actual score');

    editor.blur();
    key('z', 'KeyZ', true);
    await saved(original);
    checks.push('Command-Z restores actual score');
    key('z', 'KeyZ', true, true);
    await saved(changed);
    checks.push('Command-Shift-Z reapplies actual score');
    key('z', 'KeyZ', true);
    await saved(original);

    const audioButton = document.querySelector('button[data-gosx-disclosure-target="#audio-dialog"]');
    if (!audioButton) throw new Error('Audio settings button missing');
    audioButton.click();
    await wait(() => !document.querySelector('#audio-dialog').hidden, 'Audio dialog');
    key(' ', 'Space');
    await wait(async () => (await state('transport')).playing, 'Space starts playback');
    await wait(() => document.querySelector('[data-audio-routing]').disabled, 'audio route locks during playback');
    checks.push('Space starts playback and locks audio routing');

    key(' ', 'Space');
    await wait(async () => !(await state('transport')).playing, 'Space pauses playback');
    await wait(() => !document.querySelector('[data-audio-routing]').disabled, 'audio route unlocks after stop');
    checks.push('Space pauses playback and unlocks audio routing');
    key('Home', 'Home');
    await wait(async () => {
      const transport = await state('transport');
      return transport.bar === 1 && transport.step === 1;
    }, 'Home returns to beginning');
    checks.push('Home returns to beginning');
    if (errors.length) throw new Error(errors.join('; '));
    return {type: 'cicada-native-smoke', ok: true, checks};
  })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks, error: String(error.stack || error)}))
    .then(result => window.chrome.webview.postMessage(JSON.stringify(result)));
})();
