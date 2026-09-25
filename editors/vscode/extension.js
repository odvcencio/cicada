// The editor and live views share one local Cicada process. Its stdout is LSP;
// its loopback HTTP server supplies the same Studio frame as `cicada studio`.
const { spawn } = require("node:child_process");
const { randomBytes } = require("node:crypto");
const vscode = require("vscode");
const { LanguageClient } = require("vscode-languageclient/node");

let client;
let scorePath;
let studioURL;
let panel;
let starting;

function activeScore() {
  const editor = vscode.window.activeTextEditor;
  return editor && editor.document.languageId === "cicada" && editor.document.uri.scheme === "file"
    ? editor.document.uri.fsPath : undefined;
}

async function start(score) {
  const binary = vscode.workspace.getConfiguration("cicada").get("serverPath") || "cicada";
  scorePath = score;
  studioURL = undefined;
  let child;
  const serverOptions = () => new Promise((resolve, reject) => {
    child = spawn(binary, ["studio", score, "--lsp-stdio"], {
      stdio: ["pipe", "pipe", "pipe"], windowsHide: true,
    });
    let pending = "";
    const timeout = setTimeout(() => {
      child.kill();
      reject(new Error("Studio did not report its address within 30 seconds"));
    }, 30000);
    let settled = false;
    function fail(error) {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      reject(error);
    }
    child.once("error", fail);
    child.once("exit", (code) => fail(new Error(`Studio exited before ready (${code})`)));
    child.stderr.on("data", (chunk) => {
      pending += chunk.toString();
      for (;;) {
        const end = pending.indexOf("\n");
        if (end < 0) break;
        const line = pending.slice(0, end).trim();
        pending = pending.slice(end + 1);
        const match = /^Cicada Studio: (http:\/\/[^\s]+\/)$/u.exec(line);
        if (!match || settled) continue;
        const url = new URL(match[1]);
        if (!["127.0.0.1", "localhost", "[::1]"].includes(url.hostname)) {
          fail(new Error("Studio reported a non-loopback address"));
          child.kill();
          return;
        }
        studioURL = url.toString();
        settled = true;
        clearTimeout(timeout);
        resolve({ reader: child.stdout, writer: child.stdin });
      }
    });
  });
  const next = new LanguageClient("cicada", "Cicada Language Server", serverOptions, {
    documentSelector: [{ scheme: "file", language: "cicada" }],
    outputChannelName: "Cicada",
  });
  client = next;
  starting = next.start();
  try {
    await starting;
  } catch (error) {
    if (child) child.kill();
    if (client === next) {
      client = undefined;
      scorePath = undefined;
      studioURL = undefined;
      starting = undefined;
    }
    throw error;
  }
}

async function openStudio() {
  const score = activeScore();
  if (!score) {
    vscode.window.showErrorMessage("Open a .cicada score before opening Studio.");
    return;
  }
  if (score !== scorePath) {
    if (starting) await starting.catch(() => {});
    if (client) await client.stop();
    await start(score);
  } else if (starting) {
    await starting;
  }
  if (!studioURL) throw new Error("Studio is not running");
  if (panel) panel.dispose();
  panel = vscode.window.createWebviewPanel(
    "cicadaStudio", "Cicada Studio", vscode.ViewColumn.Beside,
    { enableScripts: true, retainContextWhenHidden: true, localResourceRoots: [] }
  );
  panel.onDidDispose(() => { panel = undefined; });
  await renderPanel();
}

async function renderPanel() {
  if (!panel || !studioURL) return;
  const external = await vscode.env.asExternalUri(vscode.Uri.parse(studioURL));
  const target = new URL(external.toString());
  if (!["http:", "https:"].includes(target.protocol)) {
    throw new Error("Studio port forwarding returned an unsupported URL");
  }
  const nonce = randomBytes(16).toString("base64");
  panel.webview.html = `<!doctype html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; frame-src ${target.origin}; style-src 'nonce-${nonce}';">
<style nonce="${nonce}">html,body,iframe{width:100%;height:100%;margin:0;border:0;overflow:hidden}</style>
</head><body><iframe title="Cicada Studio" src="${target.toString().replaceAll("&", "&amp;").replaceAll('"', "&quot;")}"></iframe></body></html>`;
}

function activate(context) {
  context.subscriptions.push(vscode.commands.registerCommand("cicada.openStudio", () =>
    openStudio().catch((error) => vscode.window.showErrorMessage(`Cicada: ${error.message}`))
  ));
  context.subscriptions.push(vscode.workspace.onDidSaveTextDocument((document) => {
    if (document.uri.fsPath === scorePath && panel) {
      renderPanel().catch((error) => vscode.window.showErrorMessage(`Cicada: ${error.message}`));
    }
  }));
  const score = activeScore();
  if (score) {
    start(score).catch((error) => vscode.window.showErrorMessage(`Cicada: ${error.message}`));
  }
}

async function deactivate() {
  if (starting) await starting.catch(() => {});
  if (client) await client.stop();
}

module.exports = { activate, deactivate };
