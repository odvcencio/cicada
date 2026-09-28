const test = require('node:test');
const assert = require('node:assert/strict');
const {installWorkspaceKeyboard, installPanelTabs, replaceProjection} = require('./studio-workspace.js');

function fakeDocument(activeElement = {tagName: 'BODY'}) {
  const listeners = new Map();
  return {
    activeElement,
    addEventListener(type, callback) { listeners.set(type, callback); },
    dispatch(event) { event.preventDefault = () => { event.prevented = true; }; listeners.get('keydown')(event); }
  };
}

test('panel digits switch panels outside the source editor', () => {
  const selected = [];
  const document = fakeDocument();
  installWorkspaceKeyboard({document, selectPanel: id => selected.push(id)});
  const event = {key: '3', target: document.activeElement};
  document.dispatch(event);
  assert.deepEqual(selected, ['song']);
  assert.equal(event.prevented, true);
});

test('digits type normally inside the source editor; Alt+Shift digits switch panels', () => {
  const selected = [];
  const editor = {tagName: 'TEXTAREA', id: 'source-editor'};
  const document = fakeDocument(editor);
  installWorkspaceKeyboard({document, selectPanel: id => selected.push(id)});
  const typed = {key: '3', target: editor};
  document.dispatch(typed);
  assert.equal(typed.prevented, undefined);
  const switched = {key: '3', altKey: true, shiftKey: true, target: editor};
  document.dispatch(switched);
  assert.deepEqual(selected, ['song']);
  assert.equal(switched.prevented, true);
});

test('in-place projection replacement leaves persistent workspace and MIDI state untouched', () => {
  const activePanel = {id: 'record'};
  const midiAccess = {inputs: new Map([['controller', {}]])};
  const armSet = new Set(['bass']);
  const editor = {value: 'draft', selectionStart: 4};
  const focus = {current: editor};
  const child = {cloneNode() { return this; }};
  const panel = {childNodes: [child]};
  const target = {childNodes: [child], replaceChildren(...nodes) { this.nodes = nodes; }};
  const parsed = {querySelector(selector) { return selector === '#projection' ? panel : target; }};
  const projection = {replaceChildren(...nodes) { this.nodes = nodes; }};
  const document = {querySelector(selector) { return selector === '#projection' ? projection : target; }};
  replaceProjection({document, nextDocument: parsed, selectors: ['#projection', '#pattern-card']});
  assert.equal(projection.nodes.length, 1);
  assert.equal(activePanel.id, 'record');
  assert.equal(focus.current, editor);
  assert.equal(editor.value, 'draft');
  assert.equal(midiAccess.inputs.size, 1);
  assert.deepEqual([...armSet], ['bass']);
});

test('transport and utility shortcuts invoke their commands', () => {
  const calls = [];
  const document = fakeDocument();
  installWorkspaceKeyboard({document, command: name => calls.push(name), showShortcuts: () => calls.push('shortcuts'), selectPanel() {}});
  for (const key of [' ', 'Home', 'l', 'r', '?']) {
    const event = {key, target: document.activeElement};
    document.dispatch(event);
    assert.equal(event.prevented, true, `${key} should be handled`);
  }
  assert.deepEqual(calls, ['playPause', 'returnToStart', 'toggleLive', 'toggleRecord', 'shortcuts']);
});

test('Space on a focused button or grid cell activates it instead of toggling playback', () => {
  const calls = [];
  const button = {tagName: 'BUTTON'};
  const document = fakeDocument(button);
  installWorkspaceKeyboard({document, command: name => calls.push(name), selectPanel() {}});
  const event = {key: ' ', target: button};
  document.dispatch(event);
  assert.equal(event.prevented, undefined);
  assert.deepEqual(calls, []);
});

test('Ctrl+backslash toggles the dock even inside the editor', () => {
  let toggled = 0;
  const editor = {tagName: 'TEXTAREA', id: 'source-editor'};
  const document = fakeDocument(editor);
  installWorkspaceKeyboard({document, toggleDock: () => toggled++, selectPanel() {}});
  document.dispatch({key: '\\', ctrlKey: true, target: editor});
  assert.equal(toggled, 1);
});

function panelDocument() {
  const mk = name => ({dataset: {workspacePanel: name}, hidden: false});
  const tab = name => ({dataset: {panelTab: name}, attrs: {}, tabIndex: -1, setAttribute(key, value) { this.attrs[key] = value; }, addEventListener() {}});
  const names = ['session', 'song', 'mix'];
  const panels = names.map(mk), tabs = ['code', ...names].map(tab);
  return {panels, tabs, document: {documentElement: {dataset: {}}, querySelectorAll: sel => sel === '[data-panel-tab]' ? tabs : panels}};
}

test('choosing a panel shows only that panel and marks its tab', () => {
  const {panels, tabs, document} = panelDocument();
  const chosen = [];
  const controller = installPanelTabs({document, selectPanel: name => chosen.push(name), initial: 'session'});
  controller.choose('mix');
  assert.deepEqual(panels.map(panel => panel.hidden), [true, true, false]);
  assert.equal(document.documentElement.dataset.activePanel, 'mix');
  assert.equal(tabs.find(tab => tab.dataset.panelTab === 'mix').attrs['aria-selected'], 'true');
  assert.equal(tabs.find(tab => tab.dataset.panelTab === 'mix').tabIndex, 0);
  assert.equal(controller.active, 'mix');
});

test('choosing Code focuses the dock and keeps the active panel visible', () => {
  const {panels, document} = panelDocument();
  const chosen = [];
  const controller = installPanelTabs({document, selectPanel: name => chosen.push(name), initial: 'song'});
  controller.choose('code');
  assert.deepEqual(panels.map(panel => panel.hidden), [true, false, true]);
  assert.equal(controller.active, 'song');
  assert.equal(chosen.at(-1), 'code');
});
