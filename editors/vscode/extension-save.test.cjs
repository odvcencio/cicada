const assert = require('node:assert/strict');
const Module = require('node:module');
const {EventEmitter} = require('node:events');

let command, onSave, replacements = 0;
const score = '/tmp/review-score.cicada';
const panel = {
  webview: {set html(value) { assert.match(value, /<iframe/); replacements++; }},
  onDidDispose() {}, dispose() {},
};
const vscode = {
  window: {
    activeTextEditor: {document: {languageId: 'cicada', uri: {scheme: 'file', fsPath: score}}},
    showErrorMessage(message) { throw new Error(message); },
    createWebviewPanel() { return panel; },
  },
  workspace: {
    getConfiguration() { return {get() { return 'cicada'; }}; },
    onDidSaveTextDocument(callback) { onSave = callback; return {dispose() {}}; },
  },
  commands: {registerCommand(name, callback) { command = callback; return {dispose() {}}; }},
  ViewColumn: {Beside: 2},
  Uri: {parse(value) { return new URL(value); }},
  env: {async asExternalUri(value) { return value; }},
};
class Client {
  constructor(id, name, options) { this.options = options; }
  start() { return this.options().then(() => {}); }
  async stop() {}
}
const originalLoad = Module._load;
Module._load = function (request, parent, isMain) {
  if (request === 'vscode') return vscode;
  if (request === 'vscode-languageclient/node') return {LanguageClient: Client};
  if (request === 'node:child_process') return {spawn() {
    const child = new EventEmitter();
    child.stderr = new EventEmitter(); child.stdout = {}; child.stdin = {}; child.kill = () => {};
    process.nextTick(() => child.stderr.emit('data', Buffer.from('Cicada Studio: http://127.0.0.1:9999/\n')));
    return child;
  }};
  return originalLoad.call(this, request, parent, isMain);
};
(async () => {
  try {
    const extension = require('./extension.js');
    extension.activate({subscriptions: []});
    await command();
    assert.equal(replacements, 1);
    onSave?.({uri: {fsPath: score}});
    await new Promise(setImmediate);
    assert.equal(replacements, 1);
    await extension.deactivate();
    console.log('PASS: saving the VS Code document retains the Studio iframe.');
  } finally { Module._load = originalLoad; }
})();
