// Disposable native smoke only: null audio, input off, no mutation or playback.
// Reproduce WebKit's observed late-response edge deterministically. CI caught
// AbortError while Go net/http accessed a cancelled Response.body lazy stream.
// Newer WebKit versions do not always throw there, so inject that exact getter
// failure once after aborting a real polling response. Other Response methods
// remain native and bound, allowing the safe Fetch path to catch cancellation.
(() => {
  if (location.protocol !== 'http:' || !['127.0.0.1', 'localhost', '[::1]'].includes(location.hostname)) return;
  if (window.__cicadaNativeCancellationRunning) return;
  window.__cicadaNativeCancellationRunning = true;
  const checks = [], errors = [], controllers = new WeakMap();
  const fault = {url: '/api/capture', injected: false, aborted: false, bodyGetterThrows: 0, navigations: 0};
  let armed = false;
  addEventListener('error', event => errors.push({type: 'error', message: String(event.message || event.error), stack: String(event.error?.stack || '')}));
  addEventListener('unhandledrejection', event => errors.push({type: 'unhandledrejection', message: String(event.reason), stack: String(event.reason?.stack || '')}));
  const pause = (ms = 50) => new Promise(resolve => setTimeout(resolve, ms));
  async function wait(test, name) {
    const until = performance.now() + 15000;
    while (performance.now() < until) {
      if (await test()) return;
      await pause();
    }
    throw new Error('Timed out: ' + name);
  }
  async function state(path = 'state') {
    const response = await fetch('/api/' + path, {cache: 'no-store'});
    if (!response.ok) throw new Error(path + ': HTTP ' + response.status);
    return response.json();
  }
  const originalController = window.AbortController;
  window.AbortController = new Proxy(originalController, {
    construct(target, args, newTarget) {
      const controller = Reflect.construct(target, args, newTarget);
      controllers.set(controller.signal, controller);
      return controller;
    },
  });
  const originalFetch = window.fetch;
  window.fetch = function(...args) {
    const input = args[0], options = args[1] || {};
    const url = new URL(typeof input === 'string' ? input : input.url, location.href);
    const signal = options.signal || input?.signal;
    const method = String(options.method || input?.method || 'GET').toUpperCase();
    const promise = originalFetch.apply(this, args);
    if (!armed || fault.injected || method !== 'GET' || url.pathname !== fault.url || !controllers.has(signal)) return promise;
    fault.injected = true;
    fault.requestStack = String(new Error().stack || '');
    // Return this chain; a detached .then would itself manufacture an
    // unhandled rejection and make the regression attribution ambiguous.
    return promise.then(response => {
      fault.status = response.status;
      controllers.get(signal).abort();
      fault.aborted = signal.aborted;
      const lateResponse = new Proxy(response, {
        get(target, key) {
          if (key === 'body') {
            fault.bodyGetterThrows++;
            fault.bodyGetterStack = String(new Error().stack || '');
            throw new DOMException('Fetch is aborted', 'AbortError');
          }
          const value = Reflect.get(target, key, target);
          return typeof value === 'function' ? value.bind(target) : value;
        },
      });
      // Exercise actual engine disposal/remount while the cancelled response
      // is pending. The already-cancelled response fulfills after navigation
      // starts, matching the observed headers/cancellation/callback ordering.
      setTimeout(() => {
        const link = document.querySelector('#score nav[aria-label="Project files"] a');
        if (link) link.click();
      }, 0);
      return pause(30).then(() => lateResponse);
    });
  };
  document.addEventListener('gosx:navigate', () => {
    if (!fault.injected) return;
    fault.navigations++;
    document.querySelector('#studio-shell')?.setAttribute('data-recording-transport-locked', 'await-capture-refresh');
  });
  (async () => {
    await wait(() => window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]'), 'native shell');
    const initial = await state(), audio = await state('audio/config');
    if (!initial.source.includes('title "Desktop CI"')) throw new Error('Fresh disposable smoke fixture required');
    if (String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null' || audio.options.inputEnabled)
      throw new Error('Cancellation smoke requires null audio with microphone off');
    checks.push('disposable native workspace with null audio and microphone off');
    armed = true;
    await wait(() => fault.injected && fault.aborted, 'one real capture polling response aborted after headers');
    if (fault.status !== 200) throw new Error('Fault did not use a successful polling response');
    checks.push('real capture polling response is cancelled after headers and fulfilled late');
    await wait(() => fault.navigations > 0 && document.querySelector('#studio-shell[data-shell-actions=ready]') &&
      document.querySelector('#studio-shell')?.getAttribute('data-recording-transport-locked') === 'false',
    'managed navigation remount and subsequent capture publisher refresh');
    checks.push('capture publisher refreshes transport admission after actual managed navigation');
    const final = await state(), capture = await state('capture');
    if (final.source !== initial.source || !final.valid || capture.activeCapture || capture.input.enabled || (await state('transport')).playing)
      throw new Error('Cancellation test changed score, capture, or transport');
    // Keep the strict global guard: old net/http produces the owned AbortError
    // here; no AbortError names or rejected promises are ignored or suppressed.
    if (errors.length) throw new Error('Global browser errors: ' + JSON.stringify(errors));
    checks.push('cancelled polling produces no global errors and changes no musical or microphone state');
    return {type: 'cicada-native-smoke', ok: true, checks};
  })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks,
    error: String(error) + '\n' + String(error.stack || '') + '\nCANCELLATION_FAULT ' + JSON.stringify(fault)}))
    .then(result => window.chrome.webview.postMessage(JSON.stringify(result)));
})();
