// Disposable studio-project copy only. Run three times with the same profile.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__cicadaCodeRecovery) return;
  window.__cicadaCodeRecovery = true;
  const marker = '// Native recovery variation 🎹';
  const applied = '// Applied after recovery';
  const read = async path => {
    const response = await fetch('/api/' + path, {cache: 'no-store'});
    if (!response.ok) throw new Error(path + ': ' + response.status);
    return response.json();
  };
  async function wait(check, label) {
    const end = performance.now() + 60000;
    while (performance.now() < end) {
      if (await check()) return;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    throw new Error('Timed out: ' + label);
  }
  (async () => {
    await wait(() => window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready][data-shell-layout=ready]'), 'native Code shell');
    const initial = await read('state'), audio = await read('audio/config');
    if (!initial.source.includes('title "Studio project"') || audio.options.inputEnabled || String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null') throw new Error('Use only the disposable studio-project fixture and null audio');
    const editor = document.querySelector('#score textarea');
    const checked = () => /Recovery copy kept on this device/.test(document.querySelector('[data-source-recovery-status]')?.textContent || '');
    const set = source => { editor.value = source; editor.dispatchEvent(new Event('input', {bubbles: true})); };
    const checks = [];
    let phase;
    if (initial.source.includes(applied)) {
      phase = 'verify';
      if (editor.value !== initial.source.replace(/\r\n/g, '\n') || document.querySelector('.source-draft-note')) throw new Error('A finished copy returned after restarting');
      checks.push('a third native launch retains the explicit save and does not revive recovery copies');
    } else if (editor.value.includes(marker)) {
      phase = 'recover';
      if (initial.source.includes(marker) || !document.querySelector('.source-draft-note') || !editor.closest('form').hasAttribute('data-code-unsaved')) throw new Error('Recovery changed playback or claimed the draft was saved');
      checks.push('native restart restores raw Code while keeping the saved song unchanged');
      set(editor.value + '\n' + applied + '\n');
      await wait(checked, 'forked recovery checkpoint');
      const save = [...editor.closest('form').querySelectorAll('button')].find(node => node.textContent === 'Save file');
      if (!save) throw new Error('Recovered draft has no Save file control');
      save.click();
      await wait(async () => (await read('state')).source.includes(applied) && !document.querySelector('.source-draft-note'), 'explicit native save');
      checks.push('Save file applies the recovered edit and retires its unchanged parent copy');
    } else {
      phase = 'capture';
      set(editor.value + '\n' + marker + '\n');
      await wait(checked, 'typing checkpoint');
      if ((await read('state')).source !== initial.source) throw new Error('Typing saved the song');
      checks.push('native typing keeps a private recovery copy without Check or Save');
      [...editor.closest('form').querySelectorAll('button')].find(node => node.textContent === 'Check file').click();
      await wait(() => /File check passed\. Draft is not saved\./.test(document.body.textContent), 'native file check');
      if (/could not be kept|could not be read/.test(document.body.textContent) || (await read('state')).source !== initial.source) throw new Error('Native Check lost recovery or saved the song');
      checks.push('native Check retains the same recovery copy across form newline encoding');
    }
    return {type: 'cicada-native-smoke', ok: true, checks: checks.map(check => phase + ': ' + check)};
  })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks: [], error: String(error) + '\n' + error.stack}))
    .then(result => window.chrome?.webview?.postMessage(JSON.stringify(result)));
})();
