// Runs in a disposable native WebView with null audio and microphone input off.
// Keyboard events exercise the editor's handlers; beforeinput/input exercise
// its typing history. Persisted score reads verify the real form actions.
(() => {
  if (location.protocol !== 'http:' || !['127.0.0.1', 'localhost', '[::1]'].includes(location.hostname)) return;
  if (window.__cicadaNativeEditorSmokeRunning) return;
  window.__cicadaNativeEditorSmokeRunning = true;
  const checks = [], errors = [], submissions = [], responses = [], mutations = [];
  const originalFetch = window.fetch;
  window.fetch = function(...args) {
    const input = args[0], method = args[1]?.method || input?.method || 'GET';
    const path = new URL(typeof input === 'string' ? input : input.url, location.href).pathname;
    if (!['GET', 'HEAD', 'OPTIONS'].includes(method.toUpperCase()) &&
        (path.startsWith('/__actions/') || path.startsWith('/api/'))) mutations.push({path, method});
    return originalFetch.apply(this, args).then(response => {
      if (method.toUpperCase() === 'POST' && path.startsWith('/__actions/')) {
        return response.clone().text().then(text => {
          let body;
          try { body = JSON.parse(text); } catch { body = {diagnosticParseError: 'Action response is not JSON'}; }
          responses.push({path, status: response.status, body});
          return response;
        });
      }
      return response;
    });
  };
  addEventListener('error', event => errors.push(event.message || String(event.error)));
  addEventListener('unhandledrejection', event => errors.push(String(event.reason)));
  document.addEventListener('submit', event => {
    if (!event.target.matches('form[action*="/__actions/"]')) return;
    const fields = new FormData(event.target);
    submissions.push({path: new URL(event.target.getAttribute('action'), location.href).pathname,
      intent: event.submitter?.name === 'intent' ? event.submitter.value : fields.get('intent'),
      revision: fields.get('revision'), fileRevision: fields.get('fileRevision'), csrf: Boolean(fields.get('csrf_token'))});
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
  const normalize = value => value.replace(/\r\n?/g, '\n');
  const editor = () => document.querySelector('#score textarea');
  const form = () => editor()?.closest('form');
  async function ready() {
    await wait(() => window.__gosx?.ready === true && document.querySelector('#studio-shell[data-shell-actions=ready]') &&
      document.querySelector('#score .editor-page.editor-highlight-ready') &&
      !document.querySelector('#studio-shell form[data-gosx-form-state=pending], #studio-shell form[data-gosx-form-state=submitting]'), 'native editor and fresh workspace');
    await wait(async () => document.querySelector('#undo-form input[name=revision]')?.value === (await state()).revision,
      'displayed project history revision');
  }
  let modifier;
  function key(name, code, target = editor(), options = {}) {
    if (document.activeElement !== target) document.activeElement?.blur();
    target.focus();
    const event = new KeyboardEvent('keydown', {key: name, code, bubbles: true, cancelable: true,
      ...(options.mod ? {[modifier + 'Key']: true} : {}), ...options});
    target.dispatchEvent(event);
    return event;
  }
  function menuHistory(undo) {
    if (modifier === 'meta') {
      // The test-only Go fixture prepends the exact scripts editHistory sends
      // to WKWebView. Production does not expose a test menu/message API.
      Function(window.__cicadaNativeEditorMenuScripts[undo ? 'undo' : 'redo'])();
    } else {
      key('z', 'KeyZ', document.activeElement || document.body, {mod: true, shiftKey: !undo});
    }
  }
  function fill(value) {
    const node = editor();
    node.focus();
    node.value = value;
    node.dispatchEvent(new Event('input', {bubbles: true}));
    node.setSelectionRange(value.length, value.length);
  }
  function typeText(value) {
    const node = editor();
    node.focus();
    const event = new InputEvent('beforeinput', {inputType: 'insertText', data: value, bubbles: true, cancelable: true});
    if (node.dispatchEvent(event)) {
      node.setRangeText(value, node.selectionStart, node.selectionEnd, 'end');
      node.dispatchEvent(new InputEvent('input', {inputType: 'insertText', data: value, bubbles: true}));
    }
  }
  async function typeField(node, value) {
    node.focus();
    node.setSelectionRange(0, node.value.length);
    node.setRangeText('', node.selectionStart, node.selectionEnd, 'end');
    node.dispatchEvent(new InputEvent('input', {inputType: 'deleteContentBackward', bubbles: true}));
    for (const character of value) {
      if (document.activeElement !== node) throw new Error('Query field lost focus before typing ' + character);
      const before = new InputEvent('beforeinput', {inputType: 'insertText', data: character, bubbles: true, cancelable: true});
      if (node.dispatchEvent(before)) {
        node.setRangeText(character, node.selectionStart, node.selectionEnd, 'end');
        node.dispatchEvent(new InputEvent('input', {inputType: 'insertText', data: character, bubbles: true}));
      }
      await pause(0);
      if (document.activeElement !== node) throw new Error('Query field lost focus after typing ' + character);
    }
    if (node.value !== value) throw new Error('Query field did not retain typed characters: ' + node.value);
  }
  async function unchanged(source, count, label) {
    await pause(160);
    if ((await state()).source !== source || submissions.length !== count)
      throw new Error(label + ': editor operation changed saved score or submitted an action');
    if ((await state('transport')).playing) throw new Error(label + ': editor operation started playback');
  }
  async function saved(source, label) {
    await wait(async () => {
      const snapshot = await state();
      return snapshot.valid && normalize(snapshot.source) === normalize(source) &&
        document.querySelector('#score input[name=revision]')?.value === snapshot.revision;
    }, label);
    await ready();
  }
  async function actionComplete(start, name) {
    await wait(() => responses.length > start &&
      !document.querySelector('#studio-shell form[data-gosx-form-state=pending], #studio-shell form[data-gosx-form-state=submitting]'), name);
    await ready();
    return responses[responses.length - 1];
  }
  function actionOnce(start, intent, revision) {
    const sent = submissions.slice(start);
    if (sent.length !== 1 || sent[0].path !== '/__actions/source' || (sent[0].intent || '') !== intent ||
        sent[0].revision !== revision || !sent[0].csrf)
      throw new Error('Editor action did not submit once with current revision and CSRF: ' + JSON.stringify(sent));
  }
  (async () => {
    await ready();
    modifier = window.__gosx.commands.platformMod();
    if (!['meta', 'ctrl'].includes(modifier)) throw new Error('Unknown platform modifier: ' + modifier);
    if (modifier === 'meta' && (!window.__cicadaNativeEditorMenuScripts?.undo || !window.__cicadaNativeEditorMenuScripts?.redo))
      throw new Error('Mac editor smoke requires TestMacEditorMenuWorkflow generated production menu scripts');
    const audio = await state('audio/config');
    if (String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null' || audio.options.inputEnabled)
      throw new Error('Editor workflow requires null audio with microphone off');
    const original = (await state()).source;
    if (!original.includes('title "Desktop CI"')) throw new Error('Disposable desktop fixture required');
    checks.push('native editor mounted with platform modifier and microphone off');
    document.querySelector('#studio-code details').open = true;
    const mounted = editor();
    const draft = normalize(original) + '\n// Love song: café, 東京, 🌙\n';
    fill(normalize(original));
    const typingPosts = submissions.length;
    typeText('\n// Love song: café, 東京, 🌙\n');
    if (editor().value !== draft || Number(form().dataset.undoDepth) < 1) throw new Error('Unicode typing history not recorded');
    key('z', 'KeyZ', editor(), {mod: true});
    if (editor().value !== normalize(original)) throw new Error('Focused Undo did not restore typed text');
    key('z', 'KeyZ', editor(), {mod: true, shiftKey: true});
    if (editor().value !== draft) throw new Error('Focused Redo did not restore Unicode text');
    await unchanged(original, typingPosts, 'focused text Undo/Redo');
    checks.push('Unicode typing and focused Undo/Redo leave saved song and transport unchanged');

    const tabStart = editor().value.length;
    key('Tab', 'Tab');
    if (editor().value !== draft + '  ' || editor().selectionStart !== tabStart + 2)
      throw new Error('Tab must insert the configured two spaces at a blank line');
    menuHistory(true);
    if (editor().value !== draft) throw new Error('Menu Undo did not reverse indentation');
    menuHistory(false);
    if (editor().value !== draft + '  ') throw new Error('Menu Redo did not restore indentation');
    key('Tab', 'Tab', editor(), {shiftKey: true});
    if (editor().value !== draft) throw new Error('Shift-Tab must remove one configured indentation level');
    const lines = 'track scratch acid {\n  level = -24dB\n}\n';
    fill(lines);
    const secondLine = lines.indexOf('  level'), thirdLine = lines.indexOf('}\n');
    editor().setSelectionRange(secondLine, thirdLine, 'backward');
    key('Tab', 'Tab');
    if (editor().value !== lines.replace('  level', '    level') || editor().selectionDirection !== 'backward')
      throw new Error('Selected line indentation changed the following line or selection direction');
    key('Tab', 'Tab', editor(), {shiftKey: true});
    if (editor().value !== lines || editor().selectionDirection !== 'backward')
      throw new Error('Selected line outdent did not restore text and selection direction');
    fill(draft);
    await unchanged(original, typingPosts, 'indentation');
    checks.push('Tab and Shift-Tab respect two-space indentation and selected-line boundaries');

    const blockDraft = 'track scratch acid {}';
    fill(blockDraft);
    editor().setSelectionRange(blockDraft.length - 1, blockDraft.length - 1);
    key('Enter', 'Enter');
    const indentedBlock = 'track scratch acid {\n  \n}';
    if (editor().value !== indentedBlock || editor().selectionStart !== indentedBlock.indexOf('  ') + 2)
      throw new Error('Enter did not open a two-space indented block with its closing brace retained');
    menuHistory(true);
    if (editor().value !== blockDraft) throw new Error('Focused Undo did not reverse smart Enter as one edit');
    fill(draft);
    checks.push('Enter opens an indented score block and text Undo reverses it');

    const composing = key('Tab', 'Tab', editor(), {isComposing: true});
    if (composing.defaultPrevented || editor().value !== draft) throw new Error('Composition key was intercepted by the editor');
    checks.push('composition keyboard events retain text input ownership');

    const commentStart = draft.indexOf('// Love song:');
    editor().setSelectionRange(commentStart, commentStart);
    document.querySelector('#score [data-code-command=comment]').click();
    if (editor().value !== draft.replace('// Love song:', 'Love song:')) throw new Error('Comment control did not uncomment the selected line');
    document.querySelector('#score [data-code-command=comment]').click();
    if (editor().value !== draft) throw new Error('Comment control did not restore the line');
    checks.push('Comment control round-trips the selected score line');

    const findDraft = draft.replace('// Love song:', '// Love song Love song Love song:');
    fill(findDraft);
    const firstMatch = findDraft.indexOf('Love song');
    editor().setSelectionRange(firstMatch, firstMatch + 'Love song'.length);
    document.querySelector('#score [data-code-command=find]').click();
    const query = document.querySelector('#score [data-code-find=query]');
    await typeField(query, 'Love song');
    await wait(() => editor().value.slice(editor().selectionStart, editor().selectionEnd) === 'Love song', 'Find selects real score text');
    if (editor().selectionStart !== firstMatch) throw new Error('Find skipped the currently selected first match');
    checks.push('Find query accepts characters without moving input focus into the score');
    const replacement = document.querySelector('#score [data-code-find=replacement]');
    await typeField(replacement, 'Moon song');
    document.querySelector('#score [data-code-find-action=replace]').click();
    const replacedOnce = findDraft.replace('Love song', 'Moon song');
    if (editor().value !== replacedOnce || editor().selectionStart !== firstMatch + 'Moon song'.length ||
        editor().selectionEnd !== editor().selectionStart)
      throw new Error('Replace skipped the selected first phrase or left the caret before the inserted phrase');
    document.querySelector('#score [data-code-find-action=replace]').click();
    const replacedTwice = replacedOnce.replace('Love song', 'Moon song');
    if (editor().value !== replacedTwice || editor().selectionStart !== replacedTwice.lastIndexOf('Moon song') + 'Moon song'.length ||
        editor().selectionEnd !== editor().selectionStart)
      throw new Error('Repeated Replace skipped the next phrase or left the caret before its replacement');
    editor().focus();
    menuHistory(true);
    if (editor().value !== replacedOnce) throw new Error('Menu Undo did not reverse exactly one replacement');
    key('z', 'KeyZ', editor(), {mod: true});
    if (editor().value !== findDraft) throw new Error('Local Undo did not restore the first replaced phrase');
    key('z', 'KeyZ', editor(), {mod: true, shiftKey: true});
    if (editor().value !== replacedOnce) throw new Error('Local Redo did not restore the first replacement');
    menuHistory(false);
    if (editor().value !== replacedTwice) throw new Error('Menu Redo did not restore the second replacement');
    document.querySelector('#score [data-code-find-action=close]').click();
    if (!document.querySelector('#score .editor-code-find').hidden) throw new Error('Find did not close');
    fill(draft);
    await unchanged(original, typingPosts, 'Find and repeated Replace');
    checks.push('Replace edits the selected occurrence then advances; local and menu history restore each replacement');
    if (modifier === 'meta') {
      checks.push('production Mac menu Undo/Redo operate the editor history after Tab, Enter, and Replace');
      const nativeField = document.createElement('input');
      document.body.append(nativeField); nativeField.focus();
      if (!document.execCommand('insertText', false, 'native text')) throw new Error('Native text history fixture could not type');
      menuHistory(true);
      if (nativeField.value !== '') throw new Error('Ordinary text field lost native menu Undo');
      menuHistory(false);
      if (nativeField.value !== 'native text') throw new Error('Ordinary text field lost native menu Redo');
      nativeField.remove(); editor().focus();
      checks.push('Mac menu Undo/Redo preserve native history in ordinary text fields');
    }

    const longLines = (draft + Array.from({length: 180}, (_, index) =>
      '// verse ' + (index + 1) + ' ' + 'wide phrase '.repeat(16) + '\n').join('')).split('\n');
    const targetLine = 160;
    longLines[targetLine - 1] = '// 🌙 café 東京';
    const longDraft = longLines.join('\n');
    fill(longDraft);
    await wait(() => [longDraft, longDraft + '\n'].includes(document.querySelector('#editor-highlight-content')?.textContent),
      'long draft live analysis');
    const sourceBeforeNavigation = await state();
    const navigationEditor = editor(), navigationForm = form();
    const navigationFields = JSON.stringify(['file', 'revision', 'fileRevision', 'diskRevision'].map(name =>
      navigationForm.querySelector('input[name="' + name + '"]')?.value));
    const navigationHistory = [navigationForm.dataset.undoDepth, navigationForm.dataset.redoDepth].join(':');
    const navigationMutations = mutations.length, navigationSubmissions = submissions.length;
    const previousStart = longDraft.indexOf('// verse 2');
    navigationEditor.focus();
    navigationEditor.setSelectionRange(previousStart, previousStart + 10, 'backward');
    navigationEditor.scrollTop = 80; navigationEditor.scrollLeft = 40;
    const previousScroll = [navigationEditor.scrollTop, navigationEditor.scrollLeft];
    if (!previousScroll[0]) throw new Error('Long native editor did not expose vertical scroll');
    const gotoPanel = () => document.querySelector('#score [data-code-goto-panel]');
    const gotoQuery = () => document.querySelector('#score [data-code-goto=query]');
    const gotoButton = document.querySelector('#score [data-code-command=goto]');
    if (gotoButton?.getAttribute('aria-label') !== 'Go to line') throw new Error('Go to line toolbar control is missing its accessible name');
    gotoButton.click();
    await wait(() => !gotoPanel().hidden && document.activeElement === gotoQuery(), 'Go to line toolbar opens focused query');
    if (gotoQuery().getAttribute('aria-label') !== 'Line and column') throw new Error('Go to line query is missing its accessible name');
    for (const invalidQuery of ['0', '2:0', '2:1.5', String(longLines.length + 1)]) {
      await typeField(gotoQuery(), invalidQuery);
      key('Enter', 'Enter', gotoQuery());
      const status = document.querySelector('#score [data-code-goto=status]');
      if (gotoPanel().hidden || document.activeElement !== gotoQuery() || !status?.textContent.trim() ||
          gotoQuery().getAttribute('aria-invalid') !== 'true')
        throw new Error('Invalid Go to line input did not retain focus and inline error: ' + invalidQuery);
    }
    key('Escape', 'Escape', gotoQuery());
    if (!gotoPanel().hidden || document.activeElement !== navigationEditor || navigationEditor.selectionStart !== previousStart ||
        navigationEditor.selectionEnd !== previousStart + 10 || navigationEditor.selectionDirection !== 'backward' ||
        Math.abs(navigationEditor.scrollTop - previousScroll[0]) > 1 || Math.abs(navigationEditor.scrollLeft - previousScroll[1]) > 1)
      throw new Error('Go to line Escape did not restore backward selection and both scroll offsets');
    checks.push('invalid line and column inputs show errors; Escape restores native selection direction and scroll');

    const openGoto = () => key('g', 'KeyG', navigationEditor, {ctrlKey: true});
    if (!openGoto().defaultPrevented || gotoPanel().hidden || document.activeElement !== gotoQuery())
      throw new Error('Ctrl+G did not open Go to line on this platform');
    await typeField(gotoQuery(), targetLine + ':7');
    const lineStart = longLines.slice(0, targetLine - 1).join('\n').length + 1;
    const unicodePosition = lineStart + Array.from(longLines[targetLine - 1]).slice(0, 6).join('').length;
    const jump = key('Enter', 'Enter', gotoQuery());
    if (!jump.defaultPrevented || !gotoPanel().hidden || document.activeElement !== navigationEditor ||
        navigationEditor.selectionStart !== unicodePosition || navigationEditor.selectionEnd !== unicodePosition ||
        navigationEditor.scrollTop <= previousScroll[0] + 100)
      throw new Error('Go to line Enter did not reveal the Unicode scalar column and close the panel');
    openGoto();
    await typeField(gotoQuery(), targetLine + ':999999');
    document.querySelector('#score [data-code-goto=go]').click();
    const lineEnd = lineStart + longLines[targetLine - 1].length;
    if (!gotoPanel().hidden || navigationEditor.selectionStart !== lineEnd || navigationEditor.selectionEnd !== lineEnd)
      throw new Error('Oversized Unicode column did not clamp to the target line end');
    openGoto();
    await typeField(gotoQuery(), '2');
    key('Enter', 'Enter', gotoQuery());
    if (navigationEditor.selectionStart !== longLines[0].length + 1 || navigationEditor.selectionEnd !== longLines[0].length + 1)
      throw new Error('Line-only navigation did not default to column one');
    if (modifier === 'meta' && (key('g', 'KeyG', navigationEditor, {metaKey: true}).defaultPrevented || !gotoPanel().hidden))
      throw new Error('Go to line intercepted native Mac Cmd+G');
    checks.push('Ctrl+G navigates and reveals Unicode scalar columns, clamps at line end, and preserves Mac Cmd+G');

    const afterNavigation = await state(), navigationCapture = await state('capture');
    if (afterNavigation.source !== sourceBeforeNavigation.source || afterNavigation.revision !== sourceBeforeNavigation.revision ||
        navigationEditor !== editor() || navigationEditor.value !== longDraft || mutations.length !== navigationMutations ||
        submissions.length !== navigationSubmissions || (await state('transport')).playing || navigationCapture.activeCapture || navigationCapture.input.enabled ||
        navigationHistory !== [form().dataset.undoDepth, form().dataset.redoDepth].join(':') || navigationFields !==
        JSON.stringify(['file', 'revision', 'fileRevision', 'diskRevision'].map(name => form().querySelector('input[name="' + name + '"]')?.value)))
      throw new Error('Go to line changed score, module, history, transport, capture, or sent a mutation request');
    checks.push('Go to line is local navigation with unchanged draft, module, disk revision, undo history, and zero mutation requests');
    fill(draft);

    const draftSelection = [editor().selectionStart, editor().selectionEnd];
    for (const name of ['mixer', 'session', 'arrange']) {
      document.querySelector('#view-tab-' + name).click();
      await wait(() => !document.querySelector('#view-panel-' + name).hidden, name + ' view');
    }
    if (editor() !== mounted || editor().value !== draft || editor().selectionStart !== draftSelection[0] || editor().selectionEnd !== draftSelection[1])
      throw new Error('View changes lost the mounted editor, draft, or selection');
    await unchanged(original, typingPosts, 'view round-trip');
    checks.push('Arrange, Session, and Mixer changes preserve mounted draft and selection');

    await wait(() => {
      const highlighted = document.querySelector('#editor-highlight-content');
      return highlighted && [editor().value, editor().value + '\n'].includes(highlighted.textContent) && highlighted.querySelector('span[class]');
    }, 'live Cicada highlights agree with current score text');
    checks.push('live Cicada highlights describe the mounted draft text');

    let start = submissions.length, responseStart = responses.length, revision = (await state()).revision;
    const checkButton = [...form().querySelectorAll('button')].find(node => node.textContent.trim() === 'Check file');
    checkButton.click();
    const checked = await actionComplete(responseStart, 'Check file completes');
    actionOnce(start, 'check', revision);
    if (checked.status !== 422 || !/File check passed/.test(checked.body.message || ''))
      throw new Error('Valid Check file did not return retained-draft confirmation: ' + checked.status + ' ' + checked.body.message);
    if ((await state()).source !== original || editor().value !== draft) throw new Error('Check file saved or discarded the draft');
    checks.push('Check file validates current draft without saving it');

    fill(draft + '\nnot valid cicada syntax !!!\n');
    const rejected = editor().value;
    start = submissions.length; responseStart = responses.length;
    [...form().querySelectorAll('button')].find(node => node.textContent.trim() === 'Save file').click();
    const invalid = await actionComplete(responseStart, 'invalid Save completes');
    if (invalid.body.ok || invalid.status !== 422) throw new Error('Invalid Save was not rejected with diagnostics: ' + invalid.status + ' ' + invalid.body.message);
    if ((await state()).source !== original || editor().value !== rejected) throw new Error('Invalid Save changed disk or lost the rejected draft');
    if (![...document.querySelectorAll('[role=alert], .error, .message')].some(node => node.textContent.trim()))
      throw new Error('Invalid Save has no visible feedback');
    checks.push('invalid Save displays feedback and preserves rejected draft without changing disk');

    fill(draft);
    start = submissions.length; responseStart = responses.length; revision = (await state()).revision;
    key('s', 'KeyS', editor(), {mod: true});
    await saved(draft, 'platform Save saves corrected draft');
    await actionComplete(responseStart, 'corrected Save completes');
    actionOnce(start, '', revision);
    if (form().dataset.gosxFormState === 'error') throw new Error('Corrected Save retained the previous validation error state');
    checks.push('platform Save writes corrected Unicode score once with fresh revision and CSRF');

    document.activeElement?.blur();
    menuHistory(true);
    await saved(original, 'project Undo restores initial score and editor');
    if (editor().value !== normalize(original)) throw new Error('Project Undo did not refresh visible Code');
    checks.push('project Undo refreshes the editor after a successful Save');

    // Managed navigation retains the editor but may normalize form.action to
    // an absolute URL. A fresh edit after Undo must still submit this form.
    editor().setSelectionRange(editor().value.length, editor().value.length);
    typeText('\n// second saved phrase: 🌙\n');
    const secondDraft = normalize(original) + '\n// second saved phrase: 🌙\n';
    start = submissions.length; responseStart = responses.length; revision = (await state()).revision;
    key('s', 'KeyS', editor(), {mod: true});
    await saved(secondDraft, 'second platform Save after managed navigation');
    await actionComplete(responseStart, 'second platform Save completes');
    actionOnce(start, '', revision);
    checks.push('a second platform Save works after Save, Undo, and fresh typing');

    document.activeElement?.blur();
    menuHistory(true);
    await saved(original, 'project Undo restores saved score');
    document.activeElement?.blur();
    menuHistory(false);
    await saved(secondDraft, 'project Redo reapplies saved score');
    document.activeElement?.blur();
    menuHistory(true);
    await saved(original, 'project Undo returns fixture');
    checks.push('project Undo/Redo outside editor restore exact saved score');
    if (errors.length) throw new Error(errors.join('; '));
    return {type: 'cicada-native-smoke', ok: true, checks};
  })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks, error: String(error) + '\n' + String(error.stack || '')}))
    .then(result => window.chrome.webview.postMessage(JSON.stringify(result)));
})();
