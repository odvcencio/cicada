// Runs in a disposable native WebView with null audio and microphone input off.
// Keyboard events exercise the editor's handlers; beforeinput/input exercise
// its typing history. Persisted score reads verify the real form actions.
(() => {
  if (location.protocol !== 'http:' || !['127.0.0.1', 'localhost', '[::1]'].includes(location.hostname)) return;
  if (window.__cicadaNativeEditorSmokeRunning) return;
  window.__cicadaNativeEditorSmokeRunning = true;
  const checks = [], errors = [], submissions = [], responses = [], mutations = [], interactions = [], pageRequests = [];
  const sharedSnapshots = [], checkpoints = [];
  const NativeWebSocket = window.WebSocket;
  // Observe the real transport before the shared engine mounts. Never inject
  // a snapshot or change its handlers, traffic, or constructor behavior.
  window.WebSocket = new Proxy(NativeWebSocket, {
    construct(target, args, newTarget) {
      const socket = Reflect.construct(target, args, newTarget);
      const address = new URL(String(args[0]), location.href);
      if (address.host === location.host && address.pathname === '/collaboration/score') {
        socket.addEventListener('message', event => {
          try {
            const message = JSON.parse(event.data);
            if (message.event === 'state' && message.data?.document && typeof message.data.revision === 'string')
              sharedSnapshots.push({revision: message.data.revision});
          } catch { /* Non-state frames are outside this snapshot assertion. */ }
        });
      }
      return socket;
    },
  });
  let recoveryNavigationCSRF = '';
  function evidence(label, observed) {
    const record = {order: interactions.length + 1, ...observed};
    interactions.push(record);
    // Existing native hosts retain check strings, including this compact trace.
    return label + ' | interaction=' + JSON.stringify(record);
  }
  const originalFetch = window.fetch;
  window.fetch = function(...args) {
    const input = args[0], method = args[1]?.method || input?.method || 'GET';
    const path = new URL(typeof input === 'string' ? input : input.url, location.href).pathname;
    const checkpoint = method.toUpperCase() === 'POST' && ['/__actions/source-checkpoint', '/__actions/note-checkpoint'].includes(path);
    if (checkpoint) {
      const raw = args[1]?.body;
      const fields = JSON.parse(typeof raw === 'string' ? raw : new TextDecoder().decode(raw));
      const headers = new Headers(args[1]?.headers || input?.headers);
      const csrf = document.querySelector('input[name=csrf_token]')?.value;
      const sequence = Number(fields.recoverySequence || fields.sequence);
      if (!csrf || headers.get('X-CSRF-Token') !== csrf || !Number.isSafeInteger(sequence) || sequence <= 0)
        throw new Error('Recovery checkpoint lost its CSRF or sequence');
      checkpoints.push(path);
    }
    if (!checkpoint && !['GET', 'HEAD', 'OPTIONS'].includes(method.toUpperCase()) &&
        (path.startsWith('/__actions/') || path.startsWith('/api/'))) {
      let validRecoveryRelease = false;
      if (recoveryNavigationCSRF && path === '/__actions/live' && method.toUpperCase() === 'POST') {
        try {
          const raw = args[1]?.body;
          const body = JSON.parse(typeof raw === 'string' ? raw : new TextDecoder().decode(raw));
          const headers = new Headers(args[1]?.headers || input?.headers);
          validRecoveryRelease = Object.keys(body).sort().join(',') === 'csrf_token,lease,sequence,type' &&
            body.type === 'release' && /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(body.lease) &&
            /^\d+$/.test(body.sequence) && Number.isSafeInteger(Number(body.sequence)) && Number(body.sequence) > 0 &&
            body.csrf_token === recoveryNavigationCSRF && headers.get('X-CSRF-Token') === recoveryNavigationCSRF &&
            /^application\/json(?:;|$)/i.test(headers.get('Content-Type') || '');
        } catch { /* The strict mutation guard below rejects malformed releases. */ }
      }
      mutations.push({path, method, validRecoveryRelease});
    }
    if (method.toUpperCase() === 'GET' && ['/', '/studio', '/__workspace'].includes(path)) pageRequests.push(path);
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
  const visible = node => Boolean(node && node.getClientRects().length &&
    getComputedStyle(node).display !== 'none' && getComputedStyle(node).visibility !== 'hidden');
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
    const typed = [];
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
      typed.push({value: node.value, focused: node.getAttribute('data-code-find') || node.getAttribute('data-code-goto'),
        selection: [node.selectionStart, node.selectionEnd]});
    }
    if (node.value !== value) throw new Error('Query field did not retain typed characters: ' + node.value);
    return typed;
  }
  function targetVisibility(offset) {
    const source = editor(), highlight = document.querySelector('#editor-highlight-content');
    const layer = highlight.closest('.editor-highlight-layer'), gutter = document.querySelector('#editor-line-numbers');
    const bounds = source.getBoundingClientRect(), dock = document.querySelector('#studio-code').getBoundingClientRect();
    let top = Math.max(bounds.top, dock.top, 0), bottom = Math.min(bounds.bottom, dock.bottom, innerHeight);
    let left = Math.max(bounds.left, dock.left, 0), right = Math.min(bounds.right, dock.right, innerWidth);
    for (let parent = source.parentElement; parent; parent = parent.parentElement) {
      const style = getComputedStyle(parent), rect = parent.getBoundingClientRect();
      if (/^(auto|scroll|hidden|clip)$/.test(style.overflowY)) {
        top = Math.max(top, rect.top + parent.clientTop);
        bottom = Math.min(bottom, rect.top + parent.clientTop + parent.clientHeight);
      }
      if (/^(auto|scroll|hidden|clip)$/.test(style.overflowX)) {
        left = Math.max(left, rect.left + parent.clientLeft);
        right = Math.min(right, rect.left + parent.clientLeft + parent.clientWidth);
      }
    }
    const walker = document.createTreeWalker(highlight, NodeFilter.SHOW_TEXT);
    let node, at = 0;
    while ((node = walker.nextNode())) {
      if (at + node.textContent.length > offset) {
        const range = document.createRange();
        range.setStart(node, offset - at); range.setEnd(node, offset - at + 1);
        const rect = range.getBoundingClientRect();
        return {offset, sourceLength: source.value.length, glyph: range.toString(),
          visible: rect.height > 0 && rect.top >= top - 1 && rect.bottom <= bottom + 1 && rect.left >= left - 1 && rect.right <= right + 1,
          viewportContained: bounds.top >= top - 1 && bounds.bottom <= bottom + 1 && bounds.left >= left - 1 && bounds.right <= right + 1,
          synchronized: Math.abs(layer.scrollTop - source.scrollTop) <= 1 && Math.abs(gutter.scrollTop - source.scrollTop) <= 1,
          target: {top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.right}, clip: {top, bottom, left, right},
          textarea: {top: bounds.top, bottom: bounds.bottom, left: bounds.left, right: bounds.right},
          scroll: {source: source.scrollTop, highlight: layer.scrollTop, gutter: gutter.scrollTop,
            clientHeight: source.clientHeight, scrollHeight: source.scrollHeight}};
      }
      at += node.textContent.length;
    }
    return {visible: false, synchronized: false};
  }
  async function revealedTarget(offset, label) {
    let observed;
    try {
      await wait(() => {
        observed = targetVisibility(offset);
        return observed.visible && observed.synchronized && observed.viewportContained;
      }, label);
    } catch (error) {
      throw new Error(String(error) + ' | geometry=' + JSON.stringify(observed) +
        ' | sourceTailCodes=' + JSON.stringify(Array.from(editor().value.slice(-4), character => character.codePointAt(0))));
    }
    return observed;
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
    await wait(() => responses.slice(start).some(response => response.path === '/__actions/source') &&
      !document.querySelector('#studio-shell form[data-gosx-form-state=pending], #studio-shell form[data-gosx-form-state=submitting]'), name);
    await ready();
    const sourceResponses = responses.slice(start).filter(response => response.path === '/__actions/source');
    if (sourceResponses.length !== 1) throw new Error(name + ': expected exactly one source response');
    return sourceResponses[0];
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
    const findTyping = await typeField(query, 'Love song');
    await wait(() => editor().value.slice(editor().selectionStart, editor().selectionEnd) === 'Love song', 'Find selects real score text');
    if (editor().selectionStart !== firstMatch) throw new Error('Find skipped the currently selected first match');
    checks.push(evidence('Find query accepts characters without moving input focus into the score',
      {action: 'type Find query', expected: {value: 'Love song', focused: 'query'}, observed: findTyping}));
    const replacement = document.querySelector('#score [data-code-find=replacement]');
    await typeField(replacement, 'Moon song');
    document.querySelector('#score [data-code-find-action=replace]').click();
    const replacedOnce = findDraft.replace('Love song', 'Moon song');
    if (editor().value !== replacedOnce || editor().selectionStart !== firstMatch + 'Moon song'.length ||
        editor().selectionEnd !== editor().selectionStart)
      throw new Error('Replace skipped the selected first phrase or left the caret before the inserted phrase');
    const replacements = [{expected: 'Moon song Love song Love song',
      actual: editor().value.slice(firstMatch, firstMatch + 'Moon song Love song Love song'.length)}];
    document.querySelector('#score [data-code-find-action=replace]').click();
    const replacedTwice = replacedOnce.replace('Love song', 'Moon song');
    if (editor().value !== replacedTwice || editor().selectionStart !== replacedTwice.lastIndexOf('Moon song') + 'Moon song'.length ||
        editor().selectionEnd !== editor().selectionStart)
      throw new Error('Repeated Replace skipped the next phrase or left the caret before its replacement');
    replacements.push({expected: 'Moon song Moon song Love song',
      actual: editor().value.slice(firstMatch, firstMatch + 'Moon song Moon song Love song'.length)});
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
    checks.push(evidence('Replace edits the selected occurrence then advances; local and menu history restore each replacement',
      {action: 'replace selected first match then repeat', replacements, history: 'local and menu Undo/Redo each restore one replacement'}));
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

    const shortDraft = '// Desktop CI navigation fixture\n// café 🌙 東京\ncicada 2\ntitle "Desktop CI"\n' +
      'track bass acid { cutoff=700Hz }\npattern pulse { 1 . 5 . }\narrange {\n' +
      '  place one bass pulse { at = @1.1.1 length = 4bars }\n  place two bass pulse { at = @5.1.1 length = 4bars }\n}';
    fill(shortDraft);
    await wait(() => [shortDraft, shortDraft + '\n'].includes(document.querySelector('#editor-highlight-content')?.textContent) &&
      document.querySelector('#editor-highlight-content span[class]'), 'short wrapped draft highlight');
    const shortOffset = shortDraft.split('\n').slice(0, 7).reduce((offset, line) => offset + line.length + 1, 0) + 2;
    editor().setSelectionRange(0, 0);
    editor().scrollTop = 0; editor().scrollLeft = 0;
    await wait(() => targetVisibility(shortOffset).synchronized, 'short draft scrolling settles');
    const shortBefore = targetVisibility(shortOffset), shortState = await state();
    const shortHistory = [form().dataset.undoDepth, form().dataset.redoDepth].join(':');
    const shortFile = form().querySelector('input[name=file]')?.value;
    const shortMutations = mutations.length, shortSubmissions = submissions.length;
    if (!shortBefore.viewportContained)
      throw new Error('Native source viewport extends outside the actual Code dock clip: ' + JSON.stringify(shortBefore));
    key('g', 'KeyG', editor(), {ctrlKey: true});
    await typeField(document.querySelector('#score [data-code-goto=query]'), '8:3');
    key('Enter', 'Enter', document.querySelector('#score [data-code-goto=query]'));
    if (!document.querySelector('#score [data-code-goto-panel]').hidden || editor().selectionStart !== shortOffset || editor().selectionEnd !== shortOffset)
      throw new Error('Short Go to line did not select line eight column three and close the panel');
    const shortAfter = await revealedTarget(shortOffset, 'short Go target revealed inside every clipping ancestor with synchronized highlight and gutter');
    const shortSelection = [editor().selectionStart, editor().selectionEnd];
    const eofQuery = shortDraft.split('\n').length + ':9999';
    key('g', 'KeyG', editor(), {ctrlKey: true});
    await typeField(document.querySelector('#score [data-code-goto=query]'), eofQuery);
    key('Enter', 'Enter', document.querySelector('#score [data-code-goto=query]'));
    if (!document.querySelector('#score [data-code-goto-panel]').hidden ||
        editor().selectionStart !== shortDraft.length || editor().selectionEnd !== shortDraft.length)
      throw new Error('Go to the final line did not clamp its caret at the actual end of the short score');
    const eofTarget = await revealedTarget(shortDraft.length - 1, 'short score EOF and final character revealed inside every clipping ancestor');
    const shortEOF = {query: eofQuery, expectedUTF16: shortDraft.length,
      actualUTF16: [editor().selectionStart, editor().selectionEnd], finalCharacter: eofTarget};
    const shortFinal = await state(), shortCapture = await state('capture');
    if (editor().value !== shortDraft || shortFinal.source !== shortState.source || shortFinal.revision !== shortState.revision ||
        [form().dataset.undoDepth, form().dataset.redoDepth].join(':') !== shortHistory || form().querySelector('input[name=file]')?.value !== shortFile ||
        mutations.length !== shortMutations || submissions.length !== shortSubmissions || (await state('transport')).playing ||
        shortCapture.activeCapture || shortCapture.input.enabled)
      throw new Error('Short Go changed draft, module, saved source, history, transport, capture, or sent a mutation request');
    checks.push(evidence('short Go to line and EOF remain visible within the contained Code viewport',
      {action: 'short wrapped Go 8:3', viewport: [innerWidth, innerHeight], expectedUTF16: shortOffset,
        actualUTF16: shortSelection, before: shortBefore, after: shortAfter, eof: shortEOF,
        mutationRequests: mutations.length - shortMutations, formSubmissions: submissions.length - shortSubmissions}));
    fill(draft);

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
    const invalidPositions = [];
    for (const invalidQuery of ['0', '2:0', '2:1.5', String(longLines.length + 1)]) {
      await typeField(gotoQuery(), invalidQuery);
      key('Enter', 'Enter', gotoQuery());
      const status = document.querySelector('#score [data-code-goto=status]');
      if (gotoPanel().hidden || document.activeElement !== gotoQuery() || !status?.textContent.trim() ||
          gotoQuery().getAttribute('aria-invalid') !== 'true' || navigationEditor.selectionStart !== previousStart ||
          navigationEditor.selectionEnd !== previousStart + 10 || navigationEditor.selectionDirection !== 'backward')
        throw new Error('Invalid Go to line input did not retain focus and inline error: ' + invalidQuery);
      invalidPositions.push({query: invalidQuery, expectedSelection: [previousStart, previousStart + 10, 'backward'],
        actualSelection: [navigationEditor.selectionStart, navigationEditor.selectionEnd, navigationEditor.selectionDirection],
        panelOpen: !gotoPanel().hidden, errorPresent: Boolean(status.textContent.trim())});
    }
    key('Escape', 'Escape', gotoQuery());
    if (!gotoPanel().hidden || document.activeElement !== navigationEditor || navigationEditor.selectionStart !== previousStart ||
        navigationEditor.selectionEnd !== previousStart + 10 || navigationEditor.selectionDirection !== 'backward' ||
        Math.abs(navigationEditor.scrollTop - previousScroll[0]) > 1 || Math.abs(navigationEditor.scrollLeft - previousScroll[1]) > 1)
      throw new Error('Go to line Escape did not restore backward selection and both scroll offsets');
    checks.push(evidence('invalid line and column inputs show errors; Escape restores native selection direction and scroll',
      {action: 'invalid Go to line then Escape', invalid: invalidPositions,
        escape: {expected: {selection: [previousStart, previousStart + 10, 'backward'], scroll: previousScroll},
          actual: {selection: [navigationEditor.selectionStart, navigationEditor.selectionEnd, navigationEditor.selectionDirection],
            scroll: [navigationEditor.scrollTop, navigationEditor.scrollLeft]}}}));

    const openGoto = () => key('g', 'KeyG', navigationEditor, {ctrlKey: true});
    if (!openGoto().defaultPrevented || gotoPanel().hidden || document.activeElement !== gotoQuery())
      throw new Error('Ctrl+G did not open Go to line on this platform');
    const sharedEditor = document.querySelector('textarea[data-collaboration-editor][aria-label="Shared score draft"]');
    if (!sharedEditor?.isConnected) throw new Error('Background shared score editor did not mount');
    const snapshotStart = sharedSnapshots.length, queryBeforeSnapshot = gotoQuery().value;
    const querySelection = [gotoQuery().selectionStart, gotoQuery().selectionEnd, gotoQuery().selectionDirection];
    const sourceSelection = [navigationEditor.selectionStart, navigationEditor.selectionEnd, navigationEditor.selectionDirection];
    const sourceScroll = [navigationEditor.scrollTop, navigationEditor.scrollLeft];
    const snapshotPages = pageRequests.length;
    const requireQueryFocus = () => {
      if (document.activeElement !== gotoQuery())
        throw new Error('Real shared score snapshot stole Go query focus; active shared editor=' +
          (document.activeElement === sharedEditor));
    };
    await wait(() => {
      requireQueryFocus();
      return sharedSnapshots.length > snapshotStart;
    }, 'incoming real shared score snapshot while Go query stays focused');
    // The observer runs before the engine's message handler. Allow that same
    // event turn to finish before checking its selection/focus side effects.
    await pause(50);
    requireQueryFocus();
    const snapshotState = await state(), snapshotCapture = await state('capture');
    if (gotoQuery().value !== queryBeforeSnapshot || querySelection.join(':') !==
        [gotoQuery().selectionStart, gotoQuery().selectionEnd, gotoQuery().selectionDirection].join(':') ||
        sourceSelection.join(':') !== [navigationEditor.selectionStart, navigationEditor.selectionEnd, navigationEditor.selectionDirection].join(':') ||
        sourceScroll.some((value, index) => Math.abs(value - [navigationEditor.scrollTop, navigationEditor.scrollLeft][index]) > 1) ||
        navigationEditor !== editor() || navigationEditor.value !== longDraft || snapshotState.source !== sourceBeforeNavigation.source ||
        snapshotState.revision !== sourceBeforeNavigation.revision || sharedSnapshots.at(-1).revision !== sourceBeforeNavigation.revision ||
        navigationHistory !== [form().dataset.undoDepth, form().dataset.redoDepth].join(':') || navigationFields !==
        JSON.stringify(['file', 'revision', 'fileRevision', 'diskRevision'].map(name => form().querySelector('input[name="' + name + '"]')?.value)) ||
        mutations.length !== navigationMutations || submissions.length !== navigationSubmissions || pageRequests.length !== snapshotPages ||
        (await state('transport')).playing || snapshotCapture.activeCapture || snapshotCapture.input.enabled)
      throw new Error('Real shared score snapshot changed Go input, Code draft/selection/module/history, disk, or recording state');
    checks.push(evidence('a real background shared snapshot preserves Go query focus and all Code draft state without writes',
      {action: 'wait for real shared WebSocket state with Go query focused', realStateSnapshots: sharedSnapshots.length - snapshotStart,
        expectedFocusedControl: 'Go query', actualFocusedControl: 'Go query', queryAndSelectionUnchanged: true,
        exactCodeDraftAndSelectionUnchanged: true, savedSourceAndRevisionUnchanged: true, moduleAndHistoryUnchanged: true,
        mutationRequests: mutations.length - navigationMutations, formSubmissions: submissions.length - navigationSubmissions,
        navigationRequests: pageRequests.length - snapshotPages, transportPlaying: false, captureActive: false, microphoneEnabled: false}));
    await typeField(gotoQuery(), targetLine + ':7');
    const lineStart = longLines.slice(0, targetLine - 1).join('\n').length + 1;
    const unicodePosition = lineStart + Array.from(longLines[targetLine - 1]).slice(0, 6).join('').length;
    const jump = key('Enter', 'Enter', gotoQuery());
    if (!jump.defaultPrevented || !gotoPanel().hidden || document.activeElement !== navigationEditor ||
        navigationEditor.selectionStart !== unicodePosition || navigationEditor.selectionEnd !== unicodePosition ||
        navigationEditor.scrollTop <= previousScroll[0] + 100)
      throw new Error('Go to line Enter did not reveal the Unicode scalar column and close the panel');
    const unicodeJump = {query: targetLine + ':7', expectedUTF16: unicodePosition,
      actualUTF16: [navigationEditor.selectionStart, navigationEditor.selectionEnd],
      previousScroll, actualScroll: [navigationEditor.scrollTop, navigationEditor.scrollLeft], panelClosed: gotoPanel().hidden};
    openGoto();
    await typeField(gotoQuery(), targetLine + ':999999');
    document.querySelector('#score [data-code-goto=go]').click();
    const lineEnd = lineStart + longLines[targetLine - 1].length;
    if (!gotoPanel().hidden || navigationEditor.selectionStart !== lineEnd || navigationEditor.selectionEnd !== lineEnd)
      throw new Error('Oversized Unicode column did not clamp to the target line end');
    const clampedJump = {query: targetLine + ':999999', expectedUTF16: lineEnd,
      actualUTF16: [navigationEditor.selectionStart, navigationEditor.selectionEnd]};
    openGoto();
    await typeField(gotoQuery(), '2');
    key('Enter', 'Enter', gotoQuery());
    if (navigationEditor.selectionStart !== longLines[0].length + 1 || navigationEditor.selectionEnd !== longLines[0].length + 1)
      throw new Error('Line-only navigation did not default to column one');
    if (modifier === 'meta' && (key('g', 'KeyG', navigationEditor, {metaKey: true}).defaultPrevented || !gotoPanel().hidden))
      throw new Error('Go to line intercepted native Mac Cmd+G');
    checks.push(evidence('Ctrl+G navigates and reveals Unicode scalar columns, clamps at line end, and preserves Mac Cmd+G',
      {action: 'Ctrl+G and Enter', unicodeJump, clampedJump,
        defaultColumn: {expectedUTF16: longLines[0].length + 1, actualUTF16: navigationEditor.selectionStart}}));

    const afterNavigation = await state(), navigationCapture = await state('capture');
    if (afterNavigation.source !== sourceBeforeNavigation.source || afterNavigation.revision !== sourceBeforeNavigation.revision ||
        navigationEditor !== editor() || navigationEditor.value !== longDraft || mutations.length !== navigationMutations ||
        submissions.length !== navigationSubmissions || (await state('transport')).playing || navigationCapture.activeCapture || navigationCapture.input.enabled ||
        navigationHistory !== [form().dataset.undoDepth, form().dataset.redoDepth].join(':') || navigationFields !==
        JSON.stringify(['file', 'revision', 'fileRevision', 'diskRevision'].map(name => form().querySelector('input[name="' + name + '"]')?.value)))
      throw new Error('Go to line changed score, module, history, transport, capture, or sent a mutation request');
    checks.push(evidence('Go to line is local navigation with unchanged draft, module, disk revision, undo history, and zero mutation requests',
      {action: 'navigation invariants', mutationRequests: mutations.length - navigationMutations,
        formSubmissions: submissions.length - navigationSubmissions, sameSource: true, sameModule: true, sameHistory: true,
        transportPlaying: false, captureActive: false, microphoneEnabled: false}));
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

    const beforeCheck = await state(), checkMutations = mutations.length;
    let start = submissions.length, responseStart = responses.length, revision = beforeCheck.revision;
    const checkButton = [...form().querySelectorAll('button')].find(node => node.textContent.trim() === 'Check file');
    checkButton.click();
    const checked = await actionComplete(responseStart, 'Check file completes');
    actionOnce(start, 'check', revision);
    const checkMessage = 'File check passed. Draft is not saved.';
    if (checked.status !== 200 || checked.body.ok !== true || checked.body.message !== checkMessage)
      throw new Error('Valid Check file did not return retained-draft confirmation: ' + checked.status + ' ' + checked.body.message);
    await wait(() => [...document.querySelectorAll('.gosx-toast--success[role=status]')]
      .some(node => visible(node) && node.textContent.includes(checkMessage)), 'visible successful Check notification');
    const afterCheck = await state();
    if (afterCheck.source !== original || afterCheck.revision !== beforeCheck.revision || editor().value !== draft ||
        form().dataset.gosxFormState === 'error' || mutations.length !== checkMutations + 1 ||
        mutations[checkMutations].path !== '/__actions/source')
      throw new Error('Check file saved, discarded the draft, retained error feedback, or sent an extra mutation request');

    const checkedFile = form().querySelector('input[name=file]').value;
    const fileLink = [...document.querySelectorAll('#score nav[aria-label="Project files"] a')]
      .find(node => new URL(node.href).searchParams.get('file') === checkedFile);
    if (!fileLink) throw new Error('Current project file navigation link missing');
    const checkedDocument = document, checkPages = pageRequests.length, checkedSubmissions = submissions.length;
    const recoveryMutationStart = mutations.length;
    recoveryNavigationCSRF = form().querySelector('input[name=csrf_token]').value;
    if (!recoveryNavigationCSRF) throw new Error('Recovery navigation needs the current fixture CSRF token');
    fileLink.click();
    await wait(() => pageRequests.length > checkPages && document.querySelector('#score .source-draft-note'), 'real managed GET recovers checked draft');
    await ready();
    await pause(160);
    recoveryNavigationCSRF = '';
    // A real managed GET disposes the previous Live mount. Permit only its
    // authenticated, empty-note lease release during this intentional GET.
    const recoveryReleases = mutations.slice(recoveryMutationStart);
    if (recoveryReleases.length > 1 || recoveryReleases.some(request => !request.validRecoveryRelease))
      throw new Error('Recovery GET sent an unexpected mutation; only one validated Live lease release is permitted');
    const recoveredNote = document.querySelector('#score .source-draft-note[role=status]');
    const recoveredState = await state(), recoveredCapture = await state('capture');
    if (document !== checkedDocument || editor().value !== draft || form().querySelector('input[name=file]').value !== checkedFile ||
        !visible(recoveredNote) || recoveredNote.textContent !== 'Recovered draft. The score file is unchanged.' ||
        [...document.querySelectorAll('#score .editor-save-status-saved')].some(visible) ||
        recoveredState.source !== original || recoveredState.revision !== beforeCheck.revision ||
        mutations.length !== checkMutations + 1 + recoveryReleases.length || submissions.length !== checkedSubmissions ||
        (await state('transport')).playing || recoveredCapture.activeCapture || recoveredCapture.input.enabled)
      throw new Error('Managed GET lost checked draft, hid recovery feedback, showed Saved, or changed disk/recording state');
    checks.push(evidence('Check file succeeds without saving; a managed GET recovers the exact draft with honest visible status',
      {action: 'Check file then current project file link', expectedStatus: 200, actualStatus: checked.status,
        resultOK: checked.body.ok, successFeedbackVisible: true, exactDraftRetained: true, sameDocument: true,
        recoveredStatus: recoveredNote.textContent, visibleSavedLabels: 0, savedSourceAndRevisionUnchanged: true,
        explicitCheckRequests: 1, validatedLiveLeaseReleases: recoveryReleases.length,
        extraMutationRequests: mutations.length - checkMutations - 1 - recoveryReleases.length,
        managedGETObserved: pageRequests.length > checkPages, transportPlaying: false, captureActive: false, microphoneEnabled: false}));

    fill(draft + '\nnot valid cicada syntax !!!\n');
    if (!visible(document.querySelector('#score .editor-save-status-unsaved')))
      throw new Error('Editing a recovered draft hid the Unsaved status');
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
    const corrected = await actionComplete(responseStart, 'corrected Save completes');
    actionOnce(start, '', revision);
    if (form().dataset.gosxFormState === 'error') throw new Error('Corrected Save retained the previous validation error state');
    if (document.querySelector('#score .source-draft-note')) throw new Error('Successful Save retained recovered-draft status');
    checks.push(evidence('platform Save writes corrected Unicode score once with fresh revision and CSRF and clears draft recovery status',
      {action: 'correct rejected draft and platform Save', actualStatus: corrected.status,
        exactDraftSaved: true, savedRevisionChanged: (await state()).revision !== beforeCheck.revision,
        formErrorPresent: false, recoveredStatusPresent: false}));

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
    return {type: 'cicada-native-smoke', ok: true, checks, interactions, recoveryCheckpoints: checkpoints.length};
  })().catch(error => ({type: 'cicada-native-smoke', ok: false, checks, interactions, error: String(error) + '\n' + String(error.stack || '')}))
    .then(result => window.chrome.webview.postMessage(JSON.stringify(result)));
})();
