// Real controls over TestElasticAlbumWorkflow's disposable original-song project.
// Null output and a synthesized guide; never opens a microphone.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__elasticAutomationRunning) return;
  window.__elasticAutomationRunning = true;
  let unloading = false;
  addEventListener('pagehide', () => { unloading = true; });
  const key = 'cicada-elastic-automation-smoke';
  const read = async name => {
    for (let attempt = 0; ; attempt++) {
      try {
        const response = await fetch('/api/' + name, {cache: 'no-store'});
        if (!response.ok) throw new Error(name + ': ' + response.status);
        return await response.json();
      } catch (error) {
        if (!(error instanceof TypeError) || attempt >= 4) throw error;
        await new Promise(resolve => setTimeout(resolve, 100));
      }
    }
  };
  const wait = async (check, label, timeout = 45000) => {
    const end = Date.now() + timeout;
    while (Date.now() < end) {
      try { if (await check()) return; } catch (error) { if (!(error instanceof TypeError)) throw error; }
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    throw new Error('Timed out: ' + label);
  };
  const ready = () => wait(() => window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]') && !document.querySelector('form[data-gosx-form-state=pending],form[data-gosx-form-state=submitting]'), 'managed forms');
  const click = (selector, scope = document) => {
    const node = scope.querySelector(selector);
    if (!node || node.disabled) throw new Error('Unavailable control: ' + selector);
    node.click();
  };
  const input = (node, value) => {
    node.value = value;
    node.dispatchEvent(new Event('input', {bubbles: true}));
    node.dispatchEvent(new Event('change', {bubbles: true}));
  };
  const saved = predicate => wait(async () => {
    await ready(); const state = await read('state');
    return predicate(state.source) && (document.querySelector('#undo-form input[name=fileRevision]')?.value || document.querySelector('#undo-form input[name=revision]')?.value) === state.revision;
  }, 'saved source and refreshed controls');
  window.__elasticAutomationPromise = (async () => {
    await ready();
    const initial = await read('state'), audio = await read('audio/config'), takes = await read('takes');
    if (!initial.source.includes('title "Anywhere You Are"') || !initial.source.includes('cicada-vocal-comp') || !takes.comps?.[0]?.elastic || audio.options.inputEnabled || String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null') throw new Error('Use only the generated original-song fixture with null audio');
    const state = JSON.parse(sessionStorage.getItem(key) || 'null') || {phase: 0, original: initial.source, raw: JSON.stringify(takes.takes), asset: takes.comps[0].asset, checks: [], timings: {}};
    const phase = number => { state.phase = number; sessionStorage.setItem(key, JSON.stringify(state)); };
    while (true) {
      await ready();
      window.__elasticAutomationStep = state.phase;
      console.log('elastic-workflow: phase ' + state.phase);
      switch (state.phase) {
        case 0: {
          const settings = [...document.querySelectorAll('details')].find(node => node.querySelector(':scope > summary')?.textContent === 'Project settings');
          if (!settings) throw new Error('Project settings missing');
          settings.open = true;
          const form = settings.querySelector('form');
          if (!form.querySelector('[name=source_revision]')?.value) throw new Error('Settings missing source revision');
          input(form.querySelector('[name=tempo]'), '116');
          state.started = Date.now(); phase(1); click('button[type=submit]', form); break;
        }
        case 1: {
          await saved(source => /^tempo 116$/m.test(source));
          state.timings.elasticTempoEditMs = Date.now() - state.started;
          state.changed = (await read('state')).source;
          const changedTakes = await read('takes');
          if (changedTakes.comps[0].asset === state.asset || changedTakes.comps[0].tempoMilli !== 116000 || JSON.stringify(changedTakes.takes) !== state.raw) throw new Error('Tempo did not reprint independently of raw take');
          phase(2); click('#undo-form button'); break;
        }
        case 2: await saved(source => source === state.original); phase(3); click('#redo-form button'); break;
        case 3:
          await saved(source => source === state.changed);
          state.checks.push('new 80-bar album song: elastic 108→116 BPM and exact source/print Undo/Redo; raw journal unchanged');
          phase(4); click('#detail-tab-automation'); break;
        case 4: {
          await wait(() => document.querySelector('.automation-recording [name=path]')?.value === 'danilo.level', 'vocal automation');
          click('#transport-form button[value=play]'); await wait(async () => (await read('transport')).playing, 'native playback');
          await wait(() => !document.querySelector('[data-automation-record-command=start]')?.disabled, 'recorder binding');
          click('[data-automation-record-command=start]'); await wait(async () => (await read('automation/recording')).recording?.active, 'recording pass');
          const slider = document.querySelector('[data-automation-record-value]');
          await wait(() => !slider.disabled, 'perform control');
          for (const value of ['-18', '-5', '-11']) {
            await new Promise(resolve => setTimeout(resolve, 240)); input(slider, value);
            await wait(async () => (await read('automation/recording')).recording?.points.some(point => point.value === Number(value)), 'recorded value ' + value);
          }
          await wait(() => document.querySelector('[data-automation-record-line]')?.getAttribute('d')?.includes('L'), 'visible recorded curve');
          phase(5); click('[data-automation-record-command=keep]'); break;
        }
        case 5:
          await saved(source => /automate danilo\.level\s*\{/.test(source));
          await wait(async () => !(await read('automation/recording')).recording, 'kept pass released');
          state.recorded = (await read('state')).source;
          click('#transport-form button[value=stop]'); await wait(async () => !(await read('transport')).playing, 'stopped');
          phase(6); break;
        case 6:
          await saved(source => source === state.recorded || source === state.changed);
          if ((await read('state')).source === state.recorded) { click('#undo-form button'); await saved(source => source === state.changed); }
          phase(7); break;
        case 7:
          await saved(source => source === state.recorded || source === state.changed);
          if ((await read('state')).source === state.changed) { click('#redo-form button'); await saved(source => source === state.recorded); }
          state.checks.push('native playback: visible slider records real clocked points, Keep saves editable curve, one Undo/Redo restores exact source');
          phase(8); break;
        case 8: {
          click('#transport-form button[value=play]'); await wait(async () => (await read('transport')).playing, 'replacement playback');
          click('[data-automation-record-command=start]');
          await wait(() => /Replace saved curve/.test(document.querySelector('[data-automation-record-status]')?.textContent), 'explicit replacement required');
          if ((await read('automation/recording')).recording) throw new Error('Existing lane was silently replaced');
          const replace = document.querySelector('.automation-recording [name=replace]'); replace.checked = true; replace.dispatchEvent(new Event('change', {bubbles: true}));
          click('[data-automation-record-command=start]'); await wait(async () => (await read('automation/recording')).recording?.active, 'replacement pass');
          await wait(() => !document.querySelector('[data-automation-record-command=discard]')?.disabled, 'discard control');
          click('[data-automation-record-command=discard]'); await wait(async () => !(await read('automation/recording')).recording, 'discarded pass');
          click('#transport-form button[value=stop]');
          if ((await read('state')).source !== state.recorded) throw new Error('Discard changed the saved song');
          state.checks.push('existing curve replacement is explicit; discard preserves saved song');
          phase(9); break;
        }
        case 9: {
          await saved(source => source === state.recorded);
          const form = document.querySelector('#score form[action$="/__actions/source"]');
          if (!form?.querySelector('[name=source_revision]')?.value) throw new Error('Code save missing project revision');
          input(form.querySelector('textarea'), state.recorded.replace(/^tempo 116$/m, 'tempo 112'));
          phase(10); click('button[type=submit]', form); break;
        }
        case 10:
          await saved(source => /^tempo 112$/m.test(source));
          if ((await read('takes')).comps[0].tempoMilli !== 112000) throw new Error('Code save left elastic audio stale');
          phase(11); click('#undo-form button'); break;
        case 11:
          await saved(source => source === state.recorded);
          state.checks.push('entry Code save carries the full source revision, reprints elastic vocal at 112 BPM, and undoes exactly');
          sessionStorage.removeItem(key);
          return {type: 'cicada-native-smoke', ok: true, checks: state.checks, timings: state.timings, physicalAudioVerified: false, microphoneOpened: false, syntheticGuide: true};
        default: throw new Error('Unknown phase ' + state.phase);
      }
    }
  })().catch(async error => {
    if (error instanceof TypeError) await new Promise(resolve => setTimeout(resolve, 250));
    if (unloading) return new Promise(() => {});
    return {type: 'cicada-native-smoke', ok: false, checks: [], error: String(error) + '\n' + String(error.stack || '') + '\n' + 'phase ' + JSON.parse(sessionStorage.getItem(key) || '{}').phase + '\n' + document.body.innerText.slice(-2000)};
  }).then(result => {
    window.__elasticAutomationResult = result;
    window.chrome?.webview?.postMessage(JSON.stringify(result));
    return result;
  });
})();
