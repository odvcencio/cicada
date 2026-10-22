// Synthetic authored grids, disposable project and null audio only.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__cicadaPatternGridSmoke) return;
  window.__cicadaPatternGridSmoke = true;
  const key = 'cicada.pattern-grid-proof', retained = JSON.parse(sessionStorage.getItem(key) || 'null');
  const checks = retained?.checks || [];
  const read = async (name = 'state') => {
    const response = await fetch('/api/' + name, {cache:'no-store'});
    if (!response.ok) throw new Error(name + ': ' + response.status);
    return response.json();
  };
  async function wait(check, label) {
    const end = performance.now() + 30000;
    while (performance.now() < end) { if (await check()) return; await new Promise(resolve => setTimeout(resolve, 50)); }
    throw new Error('Timed out: ' + label);
  }
  const ready = () => wait(() => window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]') && !document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'), 'Studio ready');
  const saved = source => wait(async () => (await read()).source === source && document.querySelector('#undo-form input[name=revision]')?.value === (await read()).revision, 'source and projection saved');
  async function render(label) {
    document.querySelector('[data-gosx-disclosure-target="#export-dialog"]').click();
    const previous = (await read('export')).id;
    document.querySelector('#export button[type=submit]').click();
    let job;
    await wait(async () => {
      job = await read('export');
      if (job.state === 'failed') throw new Error(job.error);
      return job.id !== previous && ['succeeded','shortfall'].includes(job.state);
    }, 'exported WAV');
    const response = await fetch(job.downloadURL), bytes = new Uint8Array(await response.arrayBuffer());
    if (!response.ok || new TextDecoder().decode(bytes.slice(0, 4)) !== 'RIFF') throw new Error('Invalid WAV');
    const hash = [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))].map(x => x.toString(16).padStart(2, '0')).join('');
    if (typeof window.__cicadaPatternGridAudio === 'function') {
      let binary = ''; for (const byte of bytes) binary += String.fromCharCode(byte);
      await window.__cicadaPatternGridAudio(label, btoa(binary));
    }
    document.querySelector('#export-dialog [data-gosx-disclosure-close]').click();
    return {hash, bytes:bytes.length};
  }
  (async () => {
    await ready();
    const initial = (await read()).source, audio = await read('audio/config');
    if (!initial.includes('cicada-pattern-grid-test: disposable fixture') || audio.options.inputEnabled || String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null') throw new Error('Use the disposable null-audio grid fixture');
    if (new URL(location.href).searchParams.get('pattern') !== 'slow') { location.href = '/studio?dock=clip&pattern=slow&region=detail'; return null; }
    if (retained?.phase === 'reopen') {
      await saved(retained.final);
      const reopened = await render('reopened');
      if (reopened.hash !== retained.arranged.hash) throw new Error('Reopened grid arrangement changed audio');
      sessionStorage.removeItem(key); checks.push('freshly loaded arrangement reproduces the same WAV');
      return {type:'cicada-native-smoke',ok:true,checks,final:retained.final,baseline:retained.baseline,arranged:retained.arranged,reopened};
    }
    const definitions = initial.slice(0, initial.indexOf('arrange {')), baseline = await render('baseline');
    for (const [id, step, length, at, track, description] of [
      ['slow',1920,7680,'@2.1.1','keys','4 steps · half notes · 2 bars per loop'],
      ['triplet',320,1280,'@4.1.1','keys','4 steps · eighth-note triplets · 4/3 beats per loop'],
      ['dotted',720,2880,'@4.2.3','keys','4 steps · dotted eighth notes · 3 beats per loop'],
      ['tuplet',160,960,'@5.1.3','bass','6 steps · sixteenth-note triplets · 1 beat per loop']
    ]) {
      if (!document.querySelector('[data-pattern-grid=' + id + ']')) document.querySelector('[data-gosx-key=pattern-' + id + ']').click();
      await wait(() => document.querySelector('[data-pattern-grid=' + id + ']'), 'selected grid ' + id); await ready();
      const summary = document.querySelector('[data-pattern-grid=' + id + ']');
      if (summary.dataset.patternStepTicks !== String(step) || summary.dataset.patternLoopTicks !== String(length) || summary.textContent !== description) throw new Error('Grid timing differs from the authored music: ' + summary.textContent);
      const details = document.querySelector('[data-pattern-management=' + id + ']'); details.open = true;
      const form = details.querySelector('form:has(input[name=action][value=append])');
      if (form.querySelector('[name=length]').value !== length+'ticks' || form.querySelector('[name=at]').value !== at || form.querySelector('[name=track]').value !== track) throw new Error('Placement defaults changed the phrase duration or destination: ' + id);
      if (typeof window.__cicadaPatternGridViews === 'function' && id === 'triplet') await window.__cicadaPatternGridViews(id);
      const before = (await read()).source;
      form.querySelector('button[type=submit]').click();
      await wait(async () => new RegExp('place '+id+'-place '+track+' '+id+'[^]*?length\\s*=\\s*'+length+'ticks').test((await read()).source), 'placed complete loop ' + id);
      const after = (await read()).source; await saved(after);
      if (!after.startsWith(definitions)) throw new Error('Placing rewrote the authored notes');
      document.querySelector('#undo-form button').click(); await saved(before);
      document.querySelector('#redo-form button').click(); await saved(after); await ready();
      checks.push(id + ' uses ' + step + '-tick cells and places its complete ' + length + '-tick loop with Undo/Redo');
    }
    const final = (await read()).source, arranged = await render('arranged');
    if (arranged.hash === baseline.hash) throw new Error('Placed phrases did not change the song');
    sessionStorage.setItem(key, JSON.stringify({phase:'reopen',checks,final,baseline,arranged}));
    location.href = '/studio?dock=clip&pattern=slow&region=detail';
    return null;
  })().catch(error => ({type:'cicada-native-smoke',ok:false,checks,error:String(error)+'\n'+String(error.stack || '')})).then(result => {
    if (!result) return;
    if (typeof window.__cicadaPatternGridResult === 'function') window.__cicadaPatternGridResult(result);
    else window.chrome?.webview?.postMessage(JSON.stringify(result));
  });
})();
