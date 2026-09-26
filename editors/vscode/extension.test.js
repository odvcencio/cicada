const assert = require("node:assert/strict");
const Module = require("node:module");
const { test } = require("node:test");

test("Studio can retry after an initial server startup failure", async () => {
  let command;
  let attempts = 0;
  const messages = [];
  const vscode = {
    window: {
      activeTextEditor: { document: { languageId: "cicada", uri: { scheme: "file", fsPath: "/tmp/main.cicada" } } },
      showErrorMessage: (message) => messages.push(message),
    },
    workspace: {
      getConfiguration: () => ({ get: () => "cicada" }),
      onDidSaveTextDocument: () => ({ dispose() {} }),
    },
    commands: { registerCommand: (_, callback) => { command = callback; return { dispose() {} }; } },
  };
  class FailedClient {
    start() { attempts++; return Promise.reject(new Error("server unavailable")); }
    stop() { return Promise.resolve(); }
  }
  const originalLoad = Module._load;
  Module._load = function (request, parent, isMain) {
    if (request === "vscode") return vscode;
    if (request === "vscode-languageclient/node") return { LanguageClient: FailedClient };
    return originalLoad.call(this, request, parent, isMain);
  };
  try {
    const extension = require("./extension.js");
    extension.activate({ subscriptions: [] });
    await new Promise(setImmediate);
    assert.equal(attempts, 1);
    await command();
    assert.equal(attempts, 2);
    assert.equal(messages.length, 2);
  } finally {
    Module._load = originalLoad;
    delete require.cache[require.resolve("./extension.js")];
  }
});

test("packaged language configuration is valid JSON", () => {
  const fs = require("node:fs");
  const path = require("node:path");
  const manifest = require("./package.json");
  for (const language of manifest.contributes.languages) {
    const configuration = JSON.parse(fs.readFileSync(path.join(__dirname, language.configuration), "utf8"));
    assert.ok(Array.isArray(configuration.brackets));
    for (const pair of configuration.brackets) {
      assert.equal(pair.length, 2);
      assert.ok(pair.every((value) => typeof value === "string"));
    }
  }
});
