// Observe a private virtual source through actual CoreMIDI → typed IPC → WK MIDIInput.
(() => {
  if (location.protocol !== 'http:' || !['127.0.0.1', 'localhost', '[::1]'].includes(location.hostname)) return;
  if (window.__cicadaNativeMIDISmokeRunning) return;
  window.__cicadaNativeMIDISmokeRunning = true;
  const checks = [];
  const pause = () => new Promise(resolve => setTimeout(resolve, 100));
  async function wait(check, name) {
    const until = performance.now() + 15000;
    while (performance.now() < until) { if (await check()) return; await pause(); }
    throw new Error('Timed out: ' + name);
  }
  (async () => {
    await wait(() => document.querySelector('#studio-shell[data-shell-actions=ready]') && window.gosxDesktop, 'native MIDI bridge');
    if (typeof navigator.requestMIDIAccess !== 'function') throw new Error('Native MIDI access was not installed');
    let sysexDenied = false;
    try { await navigator.requestMIDIAccess({sysex: true}); } catch (error) { sysexDenied = error.name === 'NotSupportedError'; }
    if (!sysexDenied) throw new Error('Native MIDI unexpectedly allowed SysEx');
    checks.push('System-exclusive access is refused');
    const access = await navigator.requestMIDIAccess({sysex: false});
    if (access.sysexEnabled || access.outputs.size) throw new Error('Native MIDI should remain input-only');
    let input;
    await wait(() => {
      input = Array.from(access.inputs.values()).find(port => port.name.startsWith('Cicada private app MIDI '));
      return !!input;
    }, 'private virtual MIDI source');
    checks.push('Private virtual MIDI input discovered through CoreMIDI');
    const messages = new Set(), expected = [[0x92, 60, 100], [0xb2, 74, 88], [0xe2, 0, 96], [0xd2, 70], [0x82, 60, 0], [0xb2, 123, 0]];
    let resets = 0, delivered = 0;
    access.addEventListener('statechange', event => { if (!event.port) resets++; });
    input.addEventListener('midimessage', event => {
      if (!(event.data instanceof Uint8Array) || !Number.isFinite(event.receivedTime)) throw new Error('Invalid native MIDI event');
      delivered++;
      messages.add(Array.from(event.data).join(','));
    });
    document.querySelector('[data-live-control=midi]')?.click();
    await wait(() => expected.every(message => messages.has(message.join(','))) && resets > 0, 'native MIDI messages and all-notes-off');
    checks.push('Note, CC, pitch bend, pressure and note-off reach WK MIDIInput');
    checks.push('Controller all-notes-off reaches Live reset handling');
    const before = delivered;
    await input.close();
    await pause(); await pause(); await pause(); await pause();
    if (input.connection !== 'closed' || delivered !== before) throw new Error('Closed MIDI input kept receiving');
    checks.push('MIDI input closes without changing audio devices');
    return {type: 'cicada-native-smoke', ok: true, checks};
  })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks, error: String(error.stack || error)}))
    .then(result => window.chrome.webview.postMessage(JSON.stringify(result)));
})();
