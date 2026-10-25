// The production native host against a small HTTP fixture, without Studio WASM.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__cicadaHostSmoke) return;
  window.__cicadaHostSmoke = true;
  const checks = [], errors = [];
  addEventListener('error', event => errors.push(event.message));
  addEventListener('unhandledrejection', event => errors.push(String(event.reason)));
  const wait = async (predicate, label) => {
    const deadline = performance.now() + 10000;
    while (!predicate()) {
      if (performance.now() > deadline) throw new Error('Timed out: ' + label + ' ' + JSON.stringify({
        fixture: document.body?.dataset.nativeHostFixture,
        nativeDownloads: window.__cicadaNativeDownloads,
        bridge: typeof window.gosxDesktop, call: typeof window.gosxDesktop?.call,
        messages: typeof window.chrome?.webview?.postMessage,
        url: location.href, title: document.title, body: document.body?.innerHTML.slice(0, 800),
      }));
      await new Promise(resolve => setTimeout(resolve, 25));
    }
  };
  (async () => {
    await wait(() => document.body?.dataset.nativeHostFixture === '1' &&
      window.__cicadaNativeDownloads && typeof window.gosxDesktop?.call === 'function',
    'native fixture and production bridge');
    checks.push('actual WKWebView navigated to the private host fixture with the production bridge');

    let invalidRejected = false;
    try {
      await window.gosxDesktop.call('cicada.export.save', {
        path: 'https://example.invalid/untrusted.zip', requestId: '1',
      });
    } catch (_) { invalidRejected = true; }
    if (!invalidRejected) throw new Error('Native bridge accepted an external download URL');
    checks.push('native bridge rejects a download outside the active loopback service');

    const metadata = await (await fetch('/fixture.json', {cache: 'no-store'})).json();
    const link = document.querySelector('#host-download');
    const response = await fetch(link.href, {cache: 'no-store'});
    const bytes = new Uint8Array(await response.arrayBuffer());
    const sha256 = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)),
      byte => byte.toString(16).padStart(2, '0')).join('');
    if (!response.ok || response.headers.get('Content-Type') !== 'application/zip' ||
        response.headers.get('Cache-Control') !== 'no-store' || metadata.scope !== 'native-host-fixture' ||
        bytes.length !== metadata.bytes || sha256 !== metadata.sha256 || bytes[0] !== 80 || bytes[1] !== 75)
      throw new Error('Browser fixture download does not match its known ZIP');

    const saved = await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error('Native host save did not complete')), 15000);
      link.addEventListener('cicada:export-saved', event => {
        clearTimeout(timeout); resolve(event.detail);
      }, {once: true});
      link.click();
      if (link.getAttribute('aria-busy') !== 'true') {
        clearTimeout(timeout); reject(new Error('Download click did not enter the production native bridge'));
      }
    });
    if (saved.state !== 'saved' || saved.name !== 'host-fixture.zip' || link.hasAttribute('aria-busy') ||
        document.querySelector('.native-export-status[role=status]')?.textContent !== 'Saved host-fixture.zip.')
      throw new Error('Native host save did not restore the link and announce completion: ' + JSON.stringify(saved));
    checks.push('clicked download passed through the native host and reported a completed file save');
    if (errors.length) throw new Error('WKWebView errors: ' + errors.join('; '));
    checks.push('no browser errors; native harness verifies saved SHA-256, score preservation and process shutdown');
    return {type: 'cicada-native-smoke', ok: true, checks, archive: {bytes: bytes.length, sha256}};
  })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks,
    error: String(error), errors})).then(result => window.chrome.webview.postMessage(JSON.stringify(result)));
})();
