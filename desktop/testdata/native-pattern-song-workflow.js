// Disposable synthetic phrase only. Native output is null; no input devices.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__cicadaPatternSongSmoke) return;
  window.__cicadaPatternSongSmoke = true;
  const key = 'cicada.pattern-song-proof', retained = JSON.parse(sessionStorage.getItem(key) || 'null');
  const checks = retained?.checks || [];
  const read = async (name = 'state') => {
    const response = await fetch('/api/' + name, {cache:'no-store'});
    if (!response.ok) throw new Error(name + ': ' + response.status);
    return response.json();
  };
  async function wait(check, label) {
    const end = performance.now() + 30000;
    while (performance.now() < end) {
      if (await check()) return;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    throw new Error('Timed out: ' + label);
  }
  const ready = () => wait(() => window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]') && !document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'), 'Studio ready');
  const saved = source => wait(async () => (await read()).source === source && document.querySelector('#undo-form input[name=revision]')?.value === (await read()).revision, 'saved source projection');
  const management = () => document.querySelector('[data-pattern-management=fresh]');
  const form = action => management()?.querySelector('form:has(input[name=action][value=' + action + '])');
  const submit = (action, label) => {
    const button = [...form(action).querySelectorAll('button')].find(button => button.textContent === label);
    if (!button || button.disabled) throw new Error('Unavailable: ' + label);
    button.click();
  };
  const fill = (action, name, value) => {
    const input = form(action).querySelector('[name=' + name + ']');
    input.value = value;
    input.dispatchEvent(new Event('input', {bubbles:true}));
    input.dispatchEvent(new Event('change', {bubbles:true}));
  };
  const phrase = source => source.match(/pattern fresh performance[^]*?\n}/)?.[0];
  async function render(label) {
    document.querySelector('[data-gosx-disclosure-target="#export-dialog"]').click();
    await wait(() => !document.querySelector('#export-dialog').hidden, 'Export dialog');
    const previous = (await read('export')).id;
    document.querySelector('#export button[type=submit]').click();
    let job;
    await wait(async () => {
      job = await read('export');
      if (job.state === 'failed') throw new Error(job.error);
      return job.id !== previous && ['succeeded','shortfall'].includes(job.state);
    }, 'WAV export');
    const response = await fetch(job.downloadURL), bytes = new Uint8Array(await response.arrayBuffer());
    if (!response.ok || new TextDecoder().decode(bytes.slice(0, 4)) !== 'RIFF') throw new Error('Invalid WAV');
    const hash = [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))].map(x => x.toString(16).padStart(2, '0')).join('');
    if (typeof window.__cicadaPatternSongAudio === 'function') {
      let binary = ''; for (const byte of bytes) binary += String.fromCharCode(byte);
      await window.__cicadaPatternSongAudio(label, btoa(binary));
    }
    document.querySelector('#export-dialog [data-gosx-disclosure-close]').click();
    return {hash, bytes:bytes.length};
  }
  (async () => {
    await ready();
    const initial = (await read()).source, audio = await read('audio/config');
    if (!initial.includes('cicada-pattern-song-test: disposable fixture') || audio.options.inputEnabled || String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null') throw new Error('Use the disposable null-audio phrase fixture');
    if (!management()) {
      if (new URL(location.href).searchParams.get('pattern') === 'fresh') throw new Error('Played phrase has no arrangement controls');
      location.href = '/studio?dock=clip&pattern=fresh&region=detail';
      return null;
    }
    const placement = initial.includes('// named placements');
    if (retained?.phase === 'reopen') {
      await saved(retained.final);
      if (document.querySelector('.performance-inspector [name=tick]').value !== '17' || document.querySelector('.performance-inspector [name=end_tick]').value !== '343') throw new Error('Reopened phrase lost exact played timing');
      const reopened = await render('reopened');
      if (reopened.hash !== retained.arranged.hash) throw new Error('Reopened WAV differs');
      checks.push('reopened phrase keeps exact timing and produces the same exported WAV');
      sessionStorage.removeItem(key);
      return {type:'cicada-native-smoke',ok:true,checks,placement,final:retained.final,baseline:retained.baseline,arranged:retained.arranged,reopened};
    }
    const originalPhrase = phrase(initial), baseline = await render('baseline');
    management().open = true;
    if ([...management().querySelectorAll('select[name=track] option')].some(option => option.value === 'drums')) throw new Error('Incompatible destination offered');
    if (form(placement ? 'append' : 'bind').querySelector('[name=source_revision]').value === '') throw new Error('Missing source receipt');
    fill('duplicate', 'newName', 'fresh');
    submit('duplicate', 'Create variation');
    await wait(() => management()?.open && form('duplicate')?.dataset.gosxFormState === 'error' && /already exists/i.test(document.body.textContent), 'duplicate validation');
    await ready();
    if (form('duplicate').querySelector('[name=newName]').value !== 'fresh' || (await read()).source !== initial) throw new Error('Failed variation lost input or changed the source');
    fill('duplicate', 'newName', 'fresh-idea');
    submit('duplicate', 'Create variation');
    await wait(async () => (await read()).source.includes('pattern fresh-idea performance'), 'variation created');
    const copied = (await read()).source;
    const copyPhrase = copied.match(/pattern fresh-idea performance[^]*?\n}/)?.[0]?.replace('pattern fresh-idea ', 'pattern fresh ');
    if (copyPhrase !== originalPhrase) throw new Error('Variation lost performance bytes');
    await saved(copied); document.querySelector('#undo-form button').click(); await saved(initial);
    document.querySelector('#redo-form button').click(); await saved(copied);
    document.querySelector('#undo-form button').click(); await saved(initial); await ready();
    checks.push('played phrase creates an exact editable variation; validation and Undo/Redo preserve the original');
    if (!management()) document.querySelector('[data-gosx-key=pattern-fresh]').click();
    await wait(() => management(), 'original phrase selected'); await ready();
    management().open = true;
    if (placement) {
      const add = form('append');
      if (add.querySelector('[name=length]').value !== '4001ticks' || add.querySelector('[name=at]').value !== '@2.1.1') throw new Error('Placement rounded phrase length or did not start after the song');
      fill('append', 'newName', 'fresh-in');
      fill('append', 'length', 'wrong');
      submit('append', 'Place in song');
      await wait(() => management()?.open && form('append')?.querySelector('[name=length]')?.value === 'wrong' && form('append')?.dataset.gosxFormState === 'error', 'placement validation');
      await ready();
      if ((await read()).source !== initial || form('append').querySelector('[name=newName]').value !== 'fresh-in') throw new Error('Placement validation lost input or changed source');
      fill('append', 'length', '4001ticks');
    }
    if (typeof window.__cicadaPatternSongViews === 'function') await window.__cicadaPatternSongViews();
    submit(placement ? 'append' : 'bind', placement ? 'Place in song' : 'Assign to scene');
    await wait(async () => {
      const source = (await read()).source;
      return placement ? /place fresh-in keys fresh[^]*?length\s*=\s*4001ticks/.test(source) : /keys\s*=\s*fresh/.test(source);
    }, 'phrase arranged');
    const final = (await read()).source;
    if (phrase(final) !== originalPhrase) throw new Error('Arranging rewrote the performance');
    await saved(final); document.querySelector('#undo-form button').click(); await saved(initial);
    document.querySelector('#redo-form button').click(); await saved(final); await ready();
    checks.push(placement ? 'exact-length placement uses the chosen sound; failed fields and one-step Undo/Redo are retained' : 'played phrase assigns to the scene with one-step Undo/Redo and unchanged note bytes');
    const arranged = await render('arranged');
    if (arranged.hash === baseline.hash) throw new Error('Adding the phrase did not change the rendered song');
    checks.push('arranged phrase changes the exported song');
    sessionStorage.setItem(key, JSON.stringify({phase:'reopen',checks,final,baseline,arranged}));
    location.href = '/studio?dock=clip&pattern=fresh&region=detail';
    return null;
  })().catch(error => ({type:'cicada-native-smoke',ok:false,checks,error:String(error)+'\n'+String(error.stack || '')})).then(result => {
    if (!result) return;
    if (typeof window.__cicadaPatternSongResult === 'function') window.__cicadaPatternSongResult(result);
    else window.chrome?.webview?.postMessage(JSON.stringify(result));
  });
})();
