// Exercise the real sound browser in an isolated WKWebView with null audio.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__cicadaSoundsSmoke) return;
  window.__cicadaSoundsSmoke = true;
  const checks = [], errors = [], receipts = [];
  let navigations = 0, losePlayAcknowledgement = false;
  addEventListener('error', e => errors.push(e.message));
  addEventListener('unhandledrejection', e => errors.push(String(e.reason)));
  document.addEventListener('gosx:navigate', () => navigations++);
  const originalFetch = window.fetch;
  window.fetch = function(...args) {
    const input = args[0], options = args[1] || {};
    const path = new URL(typeof input === 'string' ? input : input.url, location.href).pathname;
    return originalFetch.apply(this, args).then(async response => {
      if (path === '/__actions/sound-preview') {
        const raw = options.body;
        const request = JSON.parse(typeof raw === 'string' ? raw : new TextDecoder().decode(raw));
        receipts.push({request, status: response.status, response: await response.clone().json()});
        if (losePlayAcknowledgement && request.action === 'play' && response.ok) {
          losePlayAcknowledgement = false;
          throw new TypeError('Disposable smoke: lost successful play acknowledgement');
        }
      }
      return response;
    });
  };
  const pause = () => new Promise(resolve => setTimeout(resolve, 50));
  async function wait(check, label) {
    const until = performance.now() + 20000;
    while (performance.now() < until) { if (await check()) return; await pause(); }
    throw new Error('Timed out: ' + label);
  }
  async function state(path = 'state') {
    const response = await fetch('/api/' + path, {cache:'no-store'});
    if (!response.ok) throw new Error(path + ': ' + response.status);
    return response.json();
  }
  const root = () => document.querySelector('#cicada-sounds[data-sounds-ready=true]');
  const button = id => root().querySelector('[data-sound-preview="' + id + '"]');
  const message = () => root().querySelector('[data-sound-status]').textContent;
  const active = id => button(id).textContent === 'Stop' && button(id).getAttribute('aria-busy') === 'false';
  async function ready() {
    await wait(() => window.__gosx?.ready && root() && document.querySelector('#studio-shell[data-shell-actions=ready]'), 'sound engine');
  }
  async function stopped(label) {
    await wait(() => !root().querySelector('[data-sound-preview][aria-pressed=true]') && root().querySelector('[data-sound-stop]').disabled, label);
    checks.push(label);
  }
  (async () => {
    await ready();
    const initial = await state(), audio = await state('audio/config');
    if (!initial.source.includes('title "Desktop CI"') || audio.options.inputEnabled ||
        String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null')
      throw new Error('Use the disposable null-audio smoke fixture with microphone off');
    button('soft-bell').focus();
    await new Promise(requestAnimationFrame);
    const sidebar = document.querySelector('#studio-browser'), scroller = sidebar.querySelector('.shell-scroll');
    const bounds = button('soft-bell').getBoundingClientRect(), viewport = scroller.getBoundingClientRect();
    if (sidebar.scrollTop !== 0 || scroller.scrollTop <= 0 || bounds.top < viewport.top || bounds.bottom > viewport.bottom)
      throw new Error('Sound list focus must scroll inside its sidebar and preserve the Browser header: ' + JSON.stringify({outer:sidebar.scrollTop, inner:scroller.scrollTop, innerHeight:scroller.clientHeight, contentHeight:scroller.scrollHeight, buttonTop:bounds.top, buttonBottom:bounds.bottom, viewportTop:viewport.top, viewportBottom:viewport.bottom}));
    checks.push('the last sound remains reachable without scrolling the Browser header away');
    const editor = document.querySelector('#score textarea');
    const draft = initial.source + '\n// Unsaved sound browsing draft\n';
    editor.value = draft;
    editor.dispatchEvent(new Event('input', {bubbles:true}));
    const navigationCount = navigations;
    button('warm-pad').click();
    await wait(() => active('warm-pad'), 'warm pad acknowledgement');
    const opened = receipts.find(r => r.request.action === 'open');
    const played = receipts.find(r => r.request.action === 'play');
    if (!opened?.response.data?.lease || played?.request.tempo !== '96000' || played?.request.root !== '0' || played?.request.scale !== 'minor')
      throw new Error('Preview lost its server lease or song context');
    if (played.status !== 200 || !played.response.data.active || (await state('transport')).playing)
      throw new Error('Preview did not remain separate from song playback');
    checks.push('native preview acknowledges the factory sound in the song key and tempo');
    for (const id of ['round-bass', 'silk-pluck', 'soft-bell']) button(id).click();
    await wait(() => active('soft-bell'), 'latest rapid sound choice');
    if (root().querySelectorAll('[aria-pressed=true]').length !== 1) throw new Error('Multiple preview selections');
    checks.push('rapid choices settle on the last selected sound');
    root().querySelector('[data-sound-stop]').click();
    await stopped('Stop acknowledges cancellation and clears the preview');
    if (!receipts.some(r => r.request.action === 'stop' && r.status === 200)) throw new Error('Stop was not acknowledged');
    const current = await state();
    if (current.source !== initial.source || current.revision !== initial.revision || editor !== document.querySelector('#score textarea') ||
        editor.value !== draft || navigations !== navigationCount)
      throw new Error('Listening changed the score, revision, draft, editor or navigation');
    checks.push('listening preserves the mounted editor, unsaved draft, score and history revision');
    losePlayAcknowledgement = true;
    const beforeLostReceipt = receipts.length;
    button('wide-strings').click();
    await wait(() => receipts.slice(beforeLostReceipt).some(r => r.request.action === 'play'), 'real play before acknowledgement loss');
    await stopped('failed play returns the sound browser to idle');
    if (!message().includes('could not reach') || !receipts.slice(beforeLostReceipt).some(r => r.request.action === 'stop' && r.status === 200))
      throw new Error('Lost acknowledgement did not produce an acknowledged Stop and actionable error');
    checks.push('a lost play acknowledgement is retired by an acknowledged Stop');
    button('round-bass').click();
    await wait(() => active('round-bass'), 'Escape preview');
    button('round-bass').focus();
    button('round-bass').dispatchEvent(new KeyboardEvent('keydown', {key:'Escape', code:'Escape', bubbles:true, cancelable:true}));
    await stopped('Escape stops the focused preview');
    button('silk-pluck').click();
    await wait(() => active('silk-pluck'), 'finite phrase');
    await stopped('a complete phrase and release finishes without looping');
    if (!message().includes('Choose another')) throw new Error('Natural completion did not update the sound browser');
    // Use the real project-file link to dispose and remount the sound engine.
    editor.value = initial.source;
    editor.dispatchEvent(new Event('input', {bubbles:true}));
    button('wide-strings').click();
    await wait(() => active('wide-strings'), 'preview before navigation');
    const oldRoot = root();
    const oldLease = receipts.filter(r => r.request.action === 'open').at(-1).response.data.lease;
    document.querySelector('#score nav[aria-label="Project files"] a').click();
    await wait(() => root() && root() !== oldRoot && navigations > navigationCount, 'managed sound engine remount');
    await ready();
    const csrf = document.querySelector('#score input[name=csrf_token]').value;
    await wait(async () => {
      const response = await originalFetch('/__actions/sound-preview', {method:'POST', headers:{'Content-Type':'application/json', 'X-CSRF-Token':csrf},
        body:JSON.stringify({csrf_token:csrf, action:'play', lease:oldLease, sequence:'1000000', patch:'warm-pad', tempo:'96000', root:'0', scale:'minor'})});
      return response.status === 409;
    }, 'disposed lease refuses late playback');
    button('poly-brass').click();
    await wait(() => active('poly-brass'), 'new sound browser after navigation');
    root().querySelector('[data-sound-stop]').click();
    await stopped('new sound browser works after managed navigation');
    if ((await state()).source !== initial.source || (await state('transport')).playing || errors.length)
      throw new Error('Unexpected score, transport or global browser errors: ' + JSON.stringify(errors));
    checks.push('disposed sound leases cannot revive after navigation');
    return {type:'cicada-native-smoke', ok:true, checks};
  })().catch(error => ({type:'cicada-native-smoke', ok:false, checks,
    error:String(error) + '\n' + String(error.stack || '') + '\nRECEIPTS ' + JSON.stringify(receipts.map(r => ({action:r.request.action, patch:r.request.patch, status:r.status, response:r.response}))) + '\nERRORS ' + JSON.stringify(errors)}))
    .then(result => window.chrome.webview.postMessage(JSON.stringify(result)));
})();
