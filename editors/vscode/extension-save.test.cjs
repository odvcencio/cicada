const assert = require('node:assert/strict');
const Module = require('node:module');
const {EventEmitter} = require('node:events');
const {test} = require('node:test');

for (const capability of [false, true]) {
  test(`Studio retains its iframe and can reopen (${capability ? 'owner capability' : 'root URL'})`, async () => {
    let command, onSave, currentPanel, replacements = 0, starts = 0, stops = 0, reveals = 0;
    const score = '/tmp/review-score.cicada';
    const sources = [];
    const vscode = {
      window: {
        activeTextEditor: {document: {languageId: 'cicada', uri: {scheme: 'file', fsPath: score}}},
        showErrorMessage(message) { throw new Error(message); },
        createWebviewPanel() {
          let onDispose;
          currentPanel = {
            webview: {set html(value) {
              assert.match(value, /<iframe/);
              sources.push(/<iframe[^>]+src="([^"]+)"/u.exec(value)[1]);
              replacements++;
            }},
            reveal() { reveals++; },
            onDidDispose(callback) { onDispose = callback; },
            dispose() { onDispose?.(); },
          };
          return currentPanel;
        },
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
      async stop() { stops++; }
    }
    const originalLoad = Module._load;
    Module._load = function (request, parent, isMain) {
      if (request === 'vscode') return vscode;
      if (request === 'vscode-languageclient/node') return {LanguageClient: Client};
      if (request === 'node:child_process') return {spawn() {
        starts++;
        const path = capability ? '__local/' + String(starts).repeat(26) : '';
        const child = new EventEmitter();
        child.stderr = new EventEmitter(); child.stdout = {}; child.stdin = {}; child.kill = () => {};
        process.nextTick(() => child.stderr.emit('data', Buffer.from(`Cicada Studio: http://127.0.0.1:9999/${path}\n`)));
        return child;
      }};
      return originalLoad.call(this, request, parent, isMain);
    };
    try {
      const extension = require('./extension.js');
      extension.activate({subscriptions: []});
      await command();
      assert.equal(replacements, 1);
      assert.equal(sources[0], 'http://127.0.0.1:9999/' + (capability ? '__local/' + '1'.repeat(26) : ''));
      onSave?.({uri: {fsPath: score}});
      await new Promise(setImmediate);
      assert.equal(replacements, 1, 'saving must retain the mounted performance engine');
      await command();
      assert.equal(reveals, 1);
      assert.equal(replacements, 1, 'opening an existing panel must retain its iframe');
      currentPanel.dispose();
      await command();
      assert.equal(replacements, 2);
      assert.equal(starts, capability ? 2 : 1);
      if (capability) {
        assert.equal(stops, 1);
        assert.notEqual(sources[0], sources[1], 'a new panel needs a fresh startup capability');
      }
      await extension.deactivate();
    } finally {
      Module._load = originalLoad;
      delete require.cache[require.resolve('./extension.js')];
    }
  });
}
