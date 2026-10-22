// Runs only in smoke.py's disposable WKWebView with null audio and input off.
// Drive the same controls/forms/shortcuts a Mac user operates; API reads below
// verify persisted musical state. No microphone, hardware output, or pad input.
(() => {
  if (location.protocol !== 'http:' || !['127.0.0.1', 'localhost', '[::1]'].includes(location.hostname)) return;
  if (window.__cicadaNativeArrangeSmokeRunning) return;
  window.__cicadaNativeArrangeSmokeRunning = true;
  const checks = [], errors = [], submissions = [], posts = [], navigations = [], mutations = [], checkpoints = [];
  let managedNavigations = 0;
  const documentIdentity = document;
  addEventListener('gosx:navigate', () => managedNavigations++);
  addEventListener('error', event => errors.push(event.message || String(event.error)));
  addEventListener('unhandledrejection', event => errors.push(String(event.reason)));
  const fetchOriginal = window.fetch;
  window.fetch = function(...args) {
    const request = args[0];
    const method = args[1]?.method || request?.method || 'GET';
    const pathname = new URL(typeof request === 'string' ? request : request.url, location.href).pathname;
    const checkpoint = method.toUpperCase() === 'POST' && ['/__actions/source-checkpoint', '/__actions/note-checkpoint'].includes(pathname);
    if (checkpoint) {
      const headers = new Headers(args[1]?.headers || request?.headers);
      const body = args[1]?.body;
      const fields = JSON.parse(typeof body === 'string' ? body : new TextDecoder().decode(body));
      const csrf = document.querySelector('input[name=csrf_token]')?.value;
      if (!csrf || headers.get('X-CSRF-Token') !== csrf || !Number.isSafeInteger(Number(fields.recoverySequence || fields.sequence)) || Number(fields.recoverySequence || fields.sequence) <= 0)
        throw new Error('Recovery checkpoint lost its CSRF or sequence');
      checkpoints.push(pathname);
    }
    if (!checkpoint && method.toUpperCase() === 'POST' && pathname.startsWith('/__actions/')) posts.push(pathname);
    if (!checkpoint && !['GET', 'HEAD', 'OPTIONS'].includes(method.toUpperCase()) &&
      (pathname.startsWith('/__actions/') || pathname.startsWith('/api/'))) mutations.push(pathname);
    if (method.toUpperCase() === 'GET' && ['/studio', '/__workspace'].includes(pathname)) navigations.push(pathname);
    return fetchOriginal.apply(this, args);
  };
  document.addEventListener('submit', event => {
    const form = event.target;
    if (!form.matches('form[action*="/__actions/"]')) return;
    const fields = new FormData(form);
    submissions.push({
      path: new URL(form.getAttribute('action'), location.href).pathname,
      action: event.submitter?.name === 'action' ? event.submitter.value : fields.get('action'),
      name: fields.get('name'), parameter: fields.get('path'), track: fields.get('track'), control: fields.get('newName'),
      revision: fields.get('revision'), hasCSRF: Boolean(fields.get('csrf_token')),
    });
  }, true);
  const pause = (ms = 50) => new Promise(resolve => setTimeout(resolve, ms));
  async function wait(check, name) {
    const until = performance.now() + 15000;
    while (performance.now() < until) {
      if (await check()) return;
      await pause();
    }
    throw new Error('Timed out: ' + name);
  }
  async function state(path = 'state') {
    const response = await fetch('/api/' + path, {cache: 'no-store'});
    if (!response.ok) throw new Error(path + ': HTTP ' + response.status);
    return response.json();
  }
  async function ready() {
    await wait(() => window.__gosx?.ready === true && document.querySelector('#studio-shell[data-shell-actions=ready]') &&
      !document.querySelector('#studio-shell form[data-gosx-form-state=pending], #studio-shell form[data-gosx-form-state=submitting]'), 'fresh native shell engine');
    await wait(async () => document.querySelector('#undo-form input[name=revision]')?.value === (await state()).revision,
      'displayed Undo revision agrees with disk');
  }
  const block = id => document.querySelector('[data-placement-block="' + id + '"]');
  const placements = snapshot => [...snapshot.source.matchAll(/^\s*place\s+(\S+)\s+(\S+)\s+(\S+)\s*\{/gm)]
    .map(([, id, track, content]) => ({id, track, content}));
  const button = (root, label) => [...root.querySelectorAll('button')].find(node => node.textContent.trim() === label);
  function key(name, code, meta = false, target = document.body, shift = false) {
    document.activeElement?.blur();
    target.focus();
    const event = new KeyboardEvent('keydown', {key: name, code, metaKey: meta, shiftKey: shift, bubbles: true, cancelable: true});
    target.dispatchEvent(event);
    return event;
  }
  async function select(id) {
    await ready();
    const anchor = block(id)?.querySelector('.placement-select');
    if (!anchor) throw new Error('Missing placement ' + id);
    anchor.click();
    await wait(() => block(id)?.getAttribute('data-placement-selected') === 'true' &&
      new URL(location.href).searchParams.get('placement') === id, id + ' selected');
    await ready();
    if (document.querySelector('#view-panel-arrange').hidden)
      throw new Error('Selecting placement ' + id + ' left Arrange: ' + location.search);
  }
  async function dock(name) {
    document.querySelector('#detail-tab-' + name).click();
    await wait(() => !document.querySelector('#detail-panel-' + name).hidden, name + ' dock visible');
    await ready();
  }
  function headingInCenter(id) {
    const heading = document.querySelector(id + ' .panel-heading');
    const scroller = document.querySelector('#studio-center .shell-scroll');
    if (!heading || !scroller) return false;
    const bounds = heading.getBoundingClientRect(), pane = scroller.getBoundingClientRect();
    return bounds.top >= pane.top - 1 && bounds.bottom <= Math.min(pane.bottom, innerHeight) + 1;
  }
  async function recordingRevealed(selected = 'verse') {
    await wait(() => !document.querySelector('#view-panel-arrange').hidden && headingInCenter('#vocals'), 'recording controls visible in Arrange');
    if (!document.querySelector('#vocals [data-capture-control=record]')?.disabled)
      throw new Error('Fixture without an audio track must keep Record take disabled');
    const chosen = document.querySelectorAll('#view-panel-arrange [data-placement-block][data-placement-selected=true]');
    if (chosen.length !== 1 || chosen[0].getAttribute('data-placement-block') !== selected)
      throw new Error('Revealing recording changed the selected placement');
  }
  async function unchanged(source, count) {
    await pause(200);
    if ((await state()).source !== source || posts.length !== count)
      throw new Error('Protected keyboard context submitted an action or changed the score: ' + JSON.stringify({sourceChanged: (await state()).source !== source, requests: posts.slice(count)}));
    if ((await state('transport')).playing) throw new Error('Protected keyboard context started playback');
  }
  function fill(input, value) {
    input.focus();
    input.value = value;
    input.dispatchEvent(new Event('input', {bubbles: true}));
    input.dispatchEvent(new Event('change', {bubbles: true}));
  }
  async function stableSource(source, name) {
    const until = performance.now() + 600;
    do {
      if ((await state()).source !== source) throw new Error(name + ': a delayed edit replayed after Undo');
      await pause();
    } while (performance.now() < until);
  }
  async function undo(source, refresh = false) {
    await ready();
    key('z', 'KeyZ', true);
    await wait(async () => (await state()).source === source, 'Command-Z restores exact score');
    await ready();
    if (refresh) {
      await stableSource(source, 'Saved source');
      // Both links perform real managed GET navigation and re-read the score.
      // A transient API rollback cannot satisfy the persisted Undo check.
      await select('answer');
      await select('verse');
      const displayed = document.querySelector('#score textarea').value;
      if (displayed.replace(/\r\n?/g, '\n') !== source.replace(/\r\n?/g, '\n'))
        throw new Error('Fresh Code view did not load the exact Undo baseline');
      await stableSource(source, 'Reloaded source');
    }
  }
  function curveProps() {
    const node = document.querySelector('#detail-panel-automation [data-automation-curve-props]');
    return node ? JSON.parse(node.getAttribute('data-automation-curve-props')) : null;
  }
  async function renderedPoint(path, value) {
    await wait(() => {
      const props = curveProps();
      const points = document.querySelectorAll('#detail-panel-automation [data-curve-point]');
      return props?.Path === path && props.Points.length === 1 && props.Points[0].tick === 3840 &&
        props.Points[0].value === value && points.length === 1 &&
        /@2\.1\.1/.test(points[0].getAttribute('aria-label')) &&
        Boolean(document.querySelector('#detail-panel-automation [data-automation-line]')?.getAttribute('d'));
    }, 'saved point and line match the canonical native curve');
  }
  async function emptyCurve() {
    await wait(() => curveProps()?.Points.length === 0 &&
      document.querySelectorAll('#detail-panel-automation [data-curve-point]').length === 0, 'Undo removes canonical and rendered curve points');
  }
  function freshSubmission(start, expected, revision) {
    const sent = submissions.slice(start).filter(entry => entry.path === expected.path &&
      (expected.action === undefined || entry.action === expected.action) &&
      (expected.name === undefined || entry.name === expected.name) &&
      (expected.parameter === undefined || entry.parameter === expected.parameter) &&
      (expected.track === undefined || entry.track === expected.track) &&
      (expected.control === undefined || entry.control === expected.control));
    if (sent.length !== 1 || sent[0].revision !== revision || !sent[0].hasCSRF)
      throw new Error('Action did not submit exactly once with current revision and CSRF: ' + JSON.stringify({matched: sent, observed: submissions.slice(start)}));
  }
  let fixture = `cicada 2

title "Desktop CI"
tempo 96
key c minor

instrument native-tone {
  param brightness = 700Hz
  voice mono {
    out = lowpass(sine(pitch), brightness) * env(gate, 100ms) * velocity * 0.1
  }
}
track bass native-tone { level = -24dB }
track lead acid { level = -24dB }
pattern pulse notes steps=4 { c3 . g3 . }
pattern reply acid steps=4 { 1 . 5 . }
arrange {
  marker intro { at = @1.1.1 }
  place verse bass pulse { at = @1.1.1 length = 1bars }
  place answer lead reply { at = @3.1.1 length = 1bars }
}
`;
  (async () => {
    await ready();
    if (window.__gosx.commands.platformMod() !== 'meta') throw new Error('Mac Command modifier not selected');
    const audio = await state('audio/config');
    if (String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null' || audio.options.inputEnabled)
      throw new Error('Arrange smoke requires null audio with input off');
    const initial = await state();
    if (!initial.source.includes('title "Desktop CI"') || placements(initial).length)
      throw new Error('Fresh disposable desktop smoke fixture required');
    checks.push('disposable Mac workspace with null output and microphone off');

    const editor = document.querySelector('#score textarea');
    editor.value = fixture;
    editor.dispatchEvent(new Event('input', {bubbles: true}));
    key('s', 'KeyS', true, editor);
    await wait(async () => { const value = await state(); return value.valid && value.source.replace(/\r\n?/g, '\n') === fixture; }, 'named arrangement saved via Command-S');
    // The native Code editor normalizes line endings on submit. Undo must
    // restore those exact saved bytes, not the test script's textarea literal.
    fixture = (await state()).source;
    await ready();
    document.querySelector('#view-tab-arrange').click();
    await wait(() => !document.querySelector('#view-panel-arrange').hidden && block('verse') && block('answer'), 'Arrange displays both persisted placements');
    const timeline = document.querySelector('#arrangement'), vocals = document.querySelector('#vocals');
    if (!timeline || !vocals || !(timeline.compareDocumentPosition(vocals) & Node.DOCUMENT_POSITION_FOLLOWING))
      throw new Error('Song timeline must precede vocal recording in Arrange');
    await wait(() => headingInCenter('#arrangement'), 'song timeline visible when Arrange first opens');
    const timelineBounds = timeline.querySelector('.placement-scroll').getBoundingClientRect();
    const centerBounds = document.querySelector('#studio-center .shell-scroll').getBoundingClientRect();
    if (timelineBounds.top < centerBounds.top || timelineBounds.top >= Math.min(centerBounds.bottom, innerHeight) || timelineBounds.bottom <= centerBounds.top)
      throw new Error('Placement timeline is outside the initial Arrange viewport');
    checks.push('Arrange initially shows the song timeline above vocal recording');
    await select('verse');
    checks.push('Command-S creates named arrangement and mounted controls refresh');

    const workspaceNav = document.querySelector('#view-panel-arrange .arrange-workspace-nav');
    const vocalLink = workspaceNav?.querySelector('a[href="#vocals"]');
    const timelineLink = workspaceNav?.querySelector('a[href="#arrangement"]');
    if (!vocalLink || !timelineLink) throw new Error('Arrange workspace links missing');
    const linkEditor = document.querySelector('#score textarea');
    const linkShell = document.querySelector('#studio-shell'), linkLive = document.querySelector('#cicada-live');
    if (!linkEditor || !linkShell || !linkLive) throw new Error('Arrange link draft and mounted shell/Live roots missing');
    const linkDraft = fixture.replace(/\r\n?/g, '\n') + '\n// Keep this unsaved Arrange jump idea\n';
    fill(linkEditor, linkDraft);
    // Local view/dock changes deliberately leave GoSX's last fetched URL
    // behind. A native hash navigation must not reload that cached document.
    await dock('device');
    for (const view of ['session', 'mixer', 'arrange']) {
      document.querySelector('#view-tab-' + view).click();
      await wait(() => !document.querySelector('#view-panel-' + view).hidden, view + ' before Arrange pane links');
    }
    const linkBefore = await state();
    const linkQuery = location.search;
    const linkPosts = posts.length, linkNavigations = navigations.length, linkMutations = mutations.length;
    const linkManaged = managedNavigations, linkSubmissions = submissions.length;
    const linkInteractions = [];
    vocalLink.click();
    await recordingRevealed();
    if (location.hash !== '#vocals') throw new Error('Vocal workspace link did not update its hash');
    linkInteractions.push({action: 'Record & comp vocals', expectedHash: '#vocals', actualHash: location.hash, headingVisible: true});
    timelineLink.click();
    await wait(() => headingInCenter('#arrangement'), 'Song timeline link returns to visible timeline');
    await unchanged(fixture, linkPosts);
    const linkAfter = await state(), linkCapture = await state('capture');
    if (document !== documentIdentity || document.querySelector('#studio-shell') !== linkShell ||
        document.querySelector('#cicada-live') !== linkLive || document.querySelector('#score textarea') !== linkEditor ||
        linkEditor.value !== linkDraft || location.search !== linkQuery || location.hash !== '#arrangement' ||
        linkAfter.source !== linkBefore.source || linkAfter.revision !== linkBefore.revision ||
        navigations.length !== linkNavigations || managedNavigations !== linkManaged ||
        mutations.length !== linkMutations || submissions.length !== linkSubmissions ||
        linkCapture.activeCapture || linkCapture.input.enabled)
      throw new Error('Arrange workspace links lost the draft/mount, navigated, submitted, or changed saved/capture state');
    linkInteractions.push({action: 'Song timeline', expectedHash: '#arrangement', actualHash: location.hash, headingVisible: true,
      sameDocument: true, sameShell: true, sameLive: true, sameEditor: true, exactUnsavedDraft: true,
      queryUnchanged: true, savedSourceAndRevisionUnchanged: true, documentRequests: navigations.length - linkNavigations,
      managedNavigationEvents: managedNavigations - linkManaged, mutationRequests: mutations.length - linkMutations,
      formSubmissions: submissions.length - linkSubmissions, transportPlaying: false, captureActive: false, microphoneEnabled: false});
    checks.push('Song timeline and Record & comp vocals links preserve the unsaved draft and mounted document after local view/dock changes | interaction=' + JSON.stringify(linkInteractions));
    fill(linkEditor, fixture.replace(/\r\n?/g, '\n'));

    // Use the second placement so a silent fallback to the first cannot
    // satisfy the selected-placement preservation check.
    await select('answer');
    const draftEditor = document.querySelector('#score textarea');
    const scoreDraft = fixture.replace(/\r\n?/g, '\n') + '\n// Keep this unsaved vocal idea\n';
    fill(draftEditor, scoreDraft);
    for (const entry of ['button', 'shortcut']) {
      document.querySelector('#view-tab-session').click();
      await wait(() => !document.querySelector('#view-panel-session').hidden, 'Session view before recording reveal');
      const recordPosts = posts.length, recordNavigations = navigations.length;
      if (entry === 'button') {
        const record = document.querySelector('[data-gosx-command="transport.record"]');
        record.focus(); record.click();
      } else key('R', 'KeyR', true, document.body, true);
      await recordingRevealed('answer');
      await unchanged(fixture, recordPosts);
      if (draftEditor !== document.querySelector('#score textarea') || draftEditor.value !== scoreDraft ||
        document !== documentIdentity || navigations.length !== recordNavigations)
        throw new Error('Record ' + entry + ' lost the Code draft or navigated away');
    }
    fill(draftEditor, fixture.replace(/\r\n?/g, '\n'));
    document.querySelector('#view-panel-arrange .arrange-workspace-nav a[href="#arrangement"]').click();
    await wait(() => headingInCenter('#arrangement'), 'return to timeline after recording reveal');
    await select('verse');
    checks.push('Record button and Command-Shift-R reveal disabled vocal controls from Session while preserving selection and Code draft');

    // Context follows actual managed placement navigation before any explicit
    // Device track choice is made.
    await dock('device');
    await wait(() => document.querySelector('#device-editor select[name=deviceTrack]')?.value === 'bass', 'Device follows bass placement');
    await select('answer');
    await wait(() => document.querySelector('#device-editor select[name=deviceTrack]')?.value === 'lead', 'Device follows lead placement');
    const cutoffForm = document.querySelector('#device-editor [data-device-parameter="lead.cutoff"] form[action$="/__actions/instrument"]');
    if (!cutoffForm) throw new Error('Built-in acid Device cutoff control missing');
    const builtinSnapshot = await state(), builtinCount = submissions.length;
    const cutoffDefault = (await state('params')).addresses.find(parameter => parameter.address === 'lead.cutoff')?.value;
    fill(cutoffForm.querySelector('[name=value]'), '1250.125');
    button(cutoffForm, 'Apply').click();
    await wait(async () => /track\s+lead\s+acid\s*\{[^}]*cutoff\s*=\s*1250\.125Hz/.test((await state()).source), 'built-in acid cutoff persists in saved score');
    await wait(async () => (await state('params')).addresses.some(parameter => parameter.address === 'lead.cutoff' && parameter.value === 1250.125), 'built-in acid cutoff reaches native catalog');
    await ready();
    freshSubmission(builtinCount, {path: '/__actions/instrument', action: 'set-parameter', track: 'lead', control: 'cutoff'}, builtinSnapshot.revision);
    if (document.querySelector('#detail-panel-device').hidden) throw new Error('Built-in sound edit left Device dock');
    await undo(fixture, true);
    await wait(async () => (await state('params')).addresses.some(parameter => parameter.address === 'lead.cutoff' && parameter.value === cutoffDefault), 'Undo restores native acid cutoff default');
    checks.push('Built-in acid cutoff saves through the selected Device instrument form and exact settled Undo restores defaults');
    await select('verse');
    await wait(() => document.querySelector('#device-editor select[name=deviceTrack]')?.value === 'bass', 'Device returns to bass placement');
    checks.push('Device track follows selected placement across managed navigation');

    let snapshot = await state(), count = submissions.length;
    key('d', 'KeyD', true);
    await wait(async () => placements(await state()).length === 3 && block('verse-copy'), 'Command-D creates persisted placement copy');
    await ready();
    const copy = placements(await state()).find(place => place.id === 'verse-copy');
    if (copy.track !== 'bass' || copy.content !== 'pulse' || Number(block('verse-copy').getAttribute('data-placement-at')) !== 3840 ||
      Number(block('verse-copy').getAttribute('data-placement-length')) !== 3840)
      throw new Error('Placement copy lost content, track, or next free position: ' + JSON.stringify(copy));
    freshSubmission(count, {path: '/__actions/arrange', action: 'duplicate', name: 'verse'}, snapshot.revision);
    await undo(fixture);
    checks.push('Command-D submits one fresh managed form, saves next free copy, and Undo restores exact score');

    for (const [name, code] of [['Backspace', 'Backspace'], ['Delete', 'Delete']]) {
      await select('verse');
      snapshot = await state(); count = submissions.length;
      key(name, code);
      await wait(async () => placements(await state()).length === 1 && !block('verse'), name + ' removes selected placement');
      await ready();
      if (placements(await state())[0].id !== 'answer') throw new Error('Delete removed the wrong placement');
      freshSubmission(count, {path: '/__actions/arrange', action: 'delete', name: 'verse'}, snapshot.revision);
      await undo(fixture);
    }
    checks.push('Mac Backspace and Delete remove only selected placement with fresh forms; Undo restores identity');

    await select('verse');
    key('l', 'KeyL', true);
    await wait(async () => { const t = await state('transport'), l = t.listening;
      return t.playing && l?.name === 'verse' && l.mode === 'context' && l.loop && l.startTick === 0 && l.endTick === 3840;
    }, 'Command-L loops selected placement in song context');
    await ready();
    if ((await state()).source !== fixture) throw new Error('Loop changed score');
    key('l', 'KeyL', true);
    await wait(async () => !(await state('transport')).playing, 'Command-L stops selected loop');
    await ready();
    const loopButton = document.querySelector('[data-gosx-command="transport.loop"]');
    if (!loopButton) throw new Error('Transport Loop button missing');
    loopButton.click();
    await wait(async () => (await state('transport')).playing && (await state('transport')).listening?.name === 'verse', 'Loop button uses selected placement');
    key('l', 'KeyL', true);
    await wait(async () => !(await state('transport')).playing, 'Command-L stops button loop');
    await ready();
    checks.push('Command-L and Loop button audition selected range through null output without editing source');

    await dock('device');
    const panForm = [...document.querySelectorAll('#device-editor form[action$="/__actions/mixer"]')]
      .find(form => form.querySelector('input[name=path]')?.value === 'bass.pan');
    if (!panForm) throw new Error('Selected Device pan form missing');
    const pan = panForm.querySelector('[name=value]');
    const postCount = posts.length;
    key('d', 'KeyD', true, pan); key('Backspace', 'Backspace', false, pan); key('l', 'KeyL', true, pan);
    await unchanged(fixture, postCount);
    checks.push('editable Device field protects typing from duplicate, delete, and loop shortcuts');
    snapshot = await state(); count = submissions.length;
    fill(pan, '0.25');
    button(panForm, 'Set').click();
    await wait(async () => (await state('params')).addresses.some(parameter => parameter.address === 'bass.pan' && parameter.value === 0.25),
      'Device pan saved in native parameter catalog');
    await ready();
    freshSubmission(count, {path: '/__actions/mixer', parameter: 'bass.pan'}, snapshot.revision);
    if (document.querySelector('#detail-panel-device').hidden || location.pathname !== '/studio') throw new Error('Device edit left native Studio dock');
    await undo(fixture, true);
    checks.push('Device pan saves real project state in one fresh action and Undo keeps dock navigation');

    const soundForm = [...document.querySelectorAll('#device-editor form[action$="/__actions/instrument"]')]
      .find(form => form.querySelector('input[name=newName]')?.value === 'brightness');
    if (!soundForm) throw new Error('Device sound parameter form missing');
    fill(soundForm.querySelector('[name=value]'), '900');
    snapshot = await state(); count = submissions.length;
    button(soundForm, 'Apply').click();
    await wait(async () => /track\s+bass\s+native-tone\s*\{[^}]*brightness\s*=\s*900Hz/.test((await state()).source),
      'Device instrument parameter saved on selected track');
    await ready();
    freshSubmission(count, {path: '/__actions/instrument', action: 'set-parameter'}, snapshot.revision);
    if (document.querySelector('#detail-panel-device').hidden) throw new Error('Sound edit left Device dock');
    await undo(fixture, true);
    checks.push('Device sound parameter applies a real track override and supports exact Undo');

    await dock('automation');
    // The first save must work immediately after revealing the hidden engine,
    // without a parameter-navigation GET that could conceal a mount problem.
    const firstPointForm = document.querySelector('#detail-panel-automation .automation-point-form');
    if (!firstPointForm) throw new Error('Initial Automation point form missing');
    const firstPath = firstPointForm.querySelector('input[name=path]').value;
    const firstValue = firstPointForm.querySelector('input[name=value]').value;
    fill(firstPointForm.querySelector('input[name=at]'), '@2.1.1');
    fill(firstPointForm.querySelector('input[name=value]'), firstValue);
    snapshot = await state(); count = submissions.length;
    button(firstPointForm, 'Save point').click();
    await wait(async () => (await state()).source.includes('automate ' + firstPath + ' {'), 'first Automation point saved without Show curve');
    await ready();
    await renderedPoint(firstPath, Number.parseFloat(firstValue));
    freshSubmission(count, {path: '/__actions/automation', action: 'automation-point-set', parameter: firstPath}, snapshot.revision);
    await undo(fixture, true);
    await emptyCurve();
    checks.push('Automation first save draws canonical point and line without Show curve; settled Undo clears both');
    const chooser = document.querySelector('#detail-panel-automation select[name=curvePath]');
    if (!chooser || ![...chooser.options].some(option => option.value === 'bass.level')) throw new Error('Automation parameter chooser missing bass.level');
    chooser.value = 'bass.level'; chooser.dispatchEvent(new Event('change', {bubbles: true}));
    button(chooser.closest('form'), 'Show curve').click();
    await wait(() => document.querySelector('#detail-panel-automation select[name=curvePath]')?.value === 'bass.level' &&
      document.querySelector('.automation-point-form input[name=path]')?.value === 'bass.level', 'Automation chooser navigates to actual parameter');
    await ready();
    const curve = document.querySelector('#detail-panel-automation [data-automation-curve]');
    const curvePosts = posts.length;
    key('d', 'KeyD', true, curve); key('Delete', 'Delete', false, curve); key('l', 'KeyL', true, curve);
    await unchanged(fixture, curvePosts);
    checks.push('focused Automation graph keeps duplicate, delete, and loop keys away from Arrange');
    const pointForm = document.querySelector('#detail-panel-automation .automation-point-form');
    fill(pointForm.querySelector('input[name=at]'), '@2.1.1');
    fill(pointForm.querySelector('input[name=value]'), '-9dB');
    snapshot = await state(); count = submissions.length;
    button(pointForm, 'Save point').click();
    await wait(async () => /automate\s+bass\.level\s*\{\s*@2\.1\.1\s+-9dB/.test((await state()).source), 'Automation point persisted at bar two');
    await ready();
    await renderedPoint('bass.level', -9);
    freshSubmission(count, {path: '/__actions/automation', action: 'automation-point-set', parameter: 'bass.level'}, snapshot.revision);
    if (document.querySelector('#detail-panel-automation').hidden || !document.querySelector('#detail-panel-automation [data-automation-curve]'))
      throw new Error('Automation save lost active dock or graph');
    await undo(fixture, true);
    await emptyCurve();
    checks.push('Automation dock edits actual bar-two level point with fresh credentials and exact Undo');

    document.querySelector('#view-tab-session').click();
    await wait(() => !document.querySelector('#view-panel-session').hidden, 'Session view active');
    const inactivePosts = posts.length;
    key('d', 'KeyD', true); key('Backspace', 'Backspace'); key('l', 'KeyL', true);
    await unchanged(fixture, inactivePosts);
    checks.push('inactive Arrange view cannot duplicate, delete, or start placement listening');
    if (document !== documentIdentity || location.pathname !== '/studio') throw new Error('Arrange workflow left original native Studio document');
    const capture = await state('capture');
    if (capture.activeCapture || capture.input.enabled || (await state('audio/config')).options.inputEnabled)
      throw new Error('Arrange workflow unexpectedly opened capture');
    if (errors.length) throw new Error(errors.join('; '));
    return {type: 'cicada-native-smoke', ok: true, checks, recoveryCheckpoints: checkpoints.length};
  })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks, error: String(error) + (error.stack ? '\n' + error.stack : ''), errors}))
    .then(result => window.chrome.webview.postMessage(JSON.stringify(result)));
})();
