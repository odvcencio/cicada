// A copied secondary-arrangement fixture, synthetic music and null audio only.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__cicadaArrangementSourcesSmoke) return;
  window.__cicadaArrangementSourcesSmoke = true;
  const checks = [];
  const read = async name => {
    const response = await fetch('/api/' + name, {cache: 'no-store'});
    if (!response.ok) throw new Error(name + ': ' + response.status);
    return response.json();
  };
  const wait = async (check, label) => {
    const end = performance.now() + 45000;
    while (performance.now() < end) {
      if (await check()) return;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    throw new Error('Timed out: ' + label);
  };
  const ready = () => wait(() => window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]') && !document.querySelector('form[data-gosx-form-state=pending], form[data-gosx-form-state=submitting]'), 'native shell');
  const form = () => document.querySelector('[data-placement-inspector=one] form:has(input[name=action][value=update])');
  const version = () => form()?.querySelector('[name=source_revision]')?.value;
  const block = () => document.querySelector('[data-placement-block=one]');
  const click = selector => {
    const button = document.querySelector(selector);
    if (!button || button.disabled) throw new Error('Unavailable control: ' + selector);
    button.click();
  };
  (async () => {
    await ready();
    const initial = await read('state'), audio = await read('audio/config');
    if (!initial.valid || !initial.source.includes('cicada-arrangement-sources-test: disposable fixture') || initial.source.includes('arrange {') || audio.options.inputEnabled || String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null') throw new Error('Use only the secondary arrangement fixture with null audio');
    click('[data-placement-block=one] .placement-select');
    await wait(() => document.querySelector('[data-placement-inspector=one]')?.open && block()?.dataset.placementSelected === 'true', 'placement inspector');
    await ready();
    const originalVersion = version();
    if (!originalVersion || block()?.dataset.placementAt !== '0') throw new Error('Secondary arrangement did not load');
    checks.push('native timeline loads the secondary arrangement while entry contains only definitions');
    const update = form();
    for (const [name, value] of Object.entries({at: '@2.1.1', length: '3bars'})) {
      const input = update.querySelector('[name=' + name + ']');
      input.value = value;
      input.dispatchEvent(new Event('input', {bubbles: true}));
      input.dispatchEvent(new Event('change', {bubbles: true}));
    }
    update.requestSubmit(update.querySelector('button[type=submit]'));
    await wait(() => block()?.dataset.placementAt === '3840' && block()?.dataset.placementLength === '11520' && version() !== originalVersion, 'source-aware move and trim');
    await ready();
    if ((await read('state')).source !== initial.source) throw new Error('Move changed entry bytes');
    const movedVersion = version();
    checks.push('move/trim updates timeline and source identity without changing the entry');
    click('#undo-form button');
    await wait(() => block()?.dataset.placementAt === '0' && version() === originalVersion, 'Undo');
    await ready();
    click('#redo-form button');
    await wait(() => block()?.dataset.placementAt === '3840' && version() === movedVersion, 'Redo');
    await ready();
    click('#undo-form button');
    await wait(() => block()?.dataset.placementAt === '0' && version() === originalVersion, 'restored original');
    await ready();
    if ((await read('state')).source !== initial.source) throw new Error('History changed entry bytes');
    checks.push('native Undo/Redo restores the owning source and its displayed identity');
    return {type: 'cicada-native-smoke', ok: true, checks};
  })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks, error: String(error.stack || error)}))
    .then(result => window.chrome?.webview?.postMessage(JSON.stringify(result)));
})();
