const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const test = require('node:test');

const source = fs.readFileSync(__dirname + '/studio-history.js', 'utf8');

function loadHistory() {
  const context = {};
  context.globalThis = context;
  vm.runInNewContext(source, context);
  return context.CicadaStudioHistory;
}

class Element {
  constructor(tag, ownerDocument) {
    this.tagName = tag.toUpperCase();
    this.ownerDocument = ownerDocument;
    this.children = [];
    this.dataset = {};
    this.attributes = {};
    this.listeners = new Map();
    this._textContent = '';
  }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  addEventListener(type, listener) {
    const handlers = this.listeners.get(type) || [];
    handlers.push(listener);
    this.listeners.set(type, handlers);
  }
  setAttribute(name, value) { this.attributes[name] = value; }
  set textContent(value) { this._textContent = String(value); this.children = []; }
  get textContent() { return this._textContent + this.children.map(child => child.textContent).join(''); }
}

class Document {
  constructor() { this.listeners = new Map(); }
  createElement(tag) { return new Element(tag, this); }
  addEventListener(type, listener) { this.listeners.set(type, listener); }
  removeEventListener(type) { this.listeners.delete(type); }
}

function all(node, className) {
  const found = [];
  if (node.className === className) found.push(node);
  for (const child of node.children || []) found.push(...all(child, className));
  return found;
}

test('History renders newest edits with expandable diffs and keeps transport events filtered', () => {
  const api = loadHistory();
  const document = new Document();
  const list = new Element('div', document);
  const state = {
    edits: [
      {id: 1, label: 'Older edit', at: '2026-09-28T18:00:00Z', revisionBefore: 'before-a', revisionAfter: 'after-a', diff: '-a\n+b'},
      {id: 2, label: 'Grid · beat step 1 toggled', at: '2026-09-28T18:01:00Z', revisionBefore: 'before-b', revisionAfter: 'after-b', diff: '-x\n+y'}
    ],
    events: [{seq: 5, kind: 'landed', detail: 'Song advanced to outro at bar 9', at: '2026-09-28T18:02:00Z'}]
  };

  api.renderHistory(list, state, {document});
  assert.deepEqual(all(list, 'history-row').map(row => row.dataset.id), ['2', '1']);
  assert.match(list.textContent, /Grid · beat step 1 toggled/);
  assert.match(list.textContent, /Show diff/);
  assert.equal(all(list, 'history-revert').length, 2);
  assert.doesNotMatch(list.textContent, /Song advanced/);

  api.renderHistory(list, state, {document, showTransport: true});
  assert.match(list.textContent, /Song advanced to outro at bar 9/);
  assert.equal(all(list, 'history-transport-event').length, 1);
});

test('Ctrl+Z and Ctrl+Shift+Z call History outside the code editor only', () => {
  const api = loadHistory();
  const document = new Document();
  const editor = {id: 'source-editor', contains() { return false; }};
  let undos = 0;
  let redos = 0;
  api.bindKeys(document, {undo() { undos++; }, redo() { redos++; }}, editor);
  const keydown = document.listeners.get('keydown');
  const outside = {closest() { return null; }};
  const event = (target, shiftKey = false) => ({
    ctrlKey: true, metaKey: false, altKey: false, shiftKey, key: 'z', target,
    prevented: false,
    preventDefault() { this.prevented = true; }
  });

  const undo = event(outside);
  keydown(undo);
  assert.equal(undo.prevented, true);
  assert.equal(undos, 1);

  const redo = event(outside, true);
  keydown(redo);
  assert.equal(redo.prevented, true);
  assert.equal(redos, 1);

  const editorUndo = event(editor);
  keydown(editorUndo);
  assert.equal(editorUndo.prevented, false);
  assert.equal(undos, 1);
});
