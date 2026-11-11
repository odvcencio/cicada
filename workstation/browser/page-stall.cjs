'use strict';
// page-stall.cjs makes a browser step that times out explain itself, and makes
// sure a failed suite cannot keep its Node process alive.
//
// Use, one browser per test; call watchContext before the context's pages navigate:
//
//   const stall = require('./page-stall.cjs');
//   const browser = await chromium.launch(stall.withChromeLog({ executablePath, args }, chromium));
//   const context = await browser.newContext(...);
//   const watch = await stall.watchContext(context, { serverLog: process.env.CICADA_STUDIO_STARTUP_LOG });
//   stall.teardown(t, browser, { watch, before: () => context.storageState({ path }) });
//
// teardown() registers the test's after hook: 15 s, plus the native-stack
// budget (15 s, or 100 s with CICADA_PAGE_STALL_NATIVE=install) that only a
// stalled page uses. When the test did not pass it prints watch.report() (at
// most 8 s besides native stacks), runs before() unless a page stopped
// responding, and closes the browser (killing Chrome's process group if close
// does not finish). Pass { exitWhenDone: true } only in the last test of a
// file: if something still holds the process open 5 s after that hook's limit,
// the process exits, with code 0 when every test that used teardown() passed
// and 1 otherwise, after printing what held it open.
//
// What watchContext (or watchPage, for one page) records, because a frozen
// page cannot answer page.evaluate:
//   - a heartbeat from setInterval and from requestAnimationFrame, sent to Node
//     through a binding, so Node knows when the main thread last ran;
//   - long tasks and long animation frames, with the scripts that ran in them;
//   - renderer crashes, page closes, and console warnings and errors;
//   - every entry into a Go/WebAssembly module through wasm_exec.js (start-up
//     through run, calls and timers through _resume), so report() can say
//     whether the main thread is stuck inside Go, in JavaScript, or outside script;
//   - the last 20 things the page did that can block its main thread in native
//     code (a synchronous request, a dialog, AudioContext, media devices or
//     playback, a canvas readback, storage, cookies) and each user event, and
//     every dialog that opened;
//   - when a page stopped and DevTools cannot answer, Chrome's processes read
//     from outside (CICADA_PAGE_STALL_NATIVE): each thread's name, state, kernel
//     wait channel and blocked system call, and with gdb or eu-stack (Linux) or
//     sample (macOS) its stack. "install" (set in CI) installs gdb through sudo,
//     only when a page stalled and only on a GitHub-hosted runner
//     (RUNNER_ENVIRONMENT=github-hosted); that can add up to 100 s to the teardown;
//   - with withChromeLog(), Chrome's own log, so a stuck audio, GPU or storage
//     service shows in the browser's words;
//   - with { stack: true } (or CICADA_PAGE_STALL_STACK=1), the Debugger domain,
//     so report() can print the paused stack. V8 serves Debugger.pause through
//     an interrupt, but only if Debugger.enable was sent before the page
//     stopped. Off by default: it slows producer-workflow by about 2 to 4 s.
// CICADA_PAGE_STALL_REPORT=1 prints the report after passing tests too.
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync, execFile } = require('node:child_process');

const BINDING = '__cicadaStallBeat';
const envStack = () => process.env.CICADA_PAGE_STALL_STACK === '1';

// within resolves to the promise's value, or to fallback after ms. A rejection
// resolves to { error }. Its timer is unref'd, so a capture that never settles
// cannot keep the process alive.
function within(promise, ms, fallback) {
  let timer;
  const limit = new Promise(resolve => { timer = setTimeout(() => resolve(fallback), Math.max(0, ms)); timer.unref(); });
  return Promise.race([Promise.resolve(promise).catch(error => ({ error })), limit]).finally(() => clearTimeout(timer));
}

// redact hides the one-use owner capability Studio prints (/__local/<token>).
const redact = text => String(text).replace(/\/__local\/[^\s/"'?#]+/g, '/__local/<redacted>');

function tail(file, lines = 40, bytes = 64 << 10) {
  if (!file) return '(no server log path given)';
  try {
    const fd = fs.openSync(file, 'r');
    try {
      const size = fs.fstatSync(fd).size, start = Math.max(0, size - bytes), buffer = Buffer.alloc(size - start);
      fs.readSync(fd, buffer, 0, buffer.length, start);
      return redact(buffer.toString('utf8').split('\n').slice(-lines).join('\n'));
    } finally { fs.closeSync(fd); }
  } catch (error) { return `(cannot read ${file}: ${error.message})`; }
}

// The page half, installed as an init script. It must not depend on anything the page loads.
function pageProbe() {
  const send = (...args) => { try { globalThis.__cicadaStallBeat(...args); } catch {} };
  setInterval(() => send('interval', Date.now()), 250);
  let last = 0;
  const frame = now => { if (now - last >= 250) { last = now; send('frame', Date.now()); } requestAnimationFrame(frame); };
  requestAnimationFrame(frame);
  const observe = (type, map) => {
    try { new PerformanceObserver(list => { for (const entry of list.getEntries()) send(type, Date.now(), map(entry)); }).observe({ type, buffered: true }); } catch {}
  };
  const origin = performance.timeOrigin;
  observe('longtask', entry => ({ start: Math.round(origin + entry.startTime), duration: Math.round(entry.duration) }));
  observe('long-animation-frame', entry => ({
    start: Math.round(origin + entry.startTime), duration: Math.round(entry.duration), blocking: Math.round(entry.blockingDuration || 0),
    scripts: (entry.scripts || []).slice(0, 4).map(script => ({ duration: Math.round(script.duration), invoker: script.invoker, source: `${script.sourceURL || ''}${script.sourceFunctionName ? ' ' + script.sourceFunctionName : ''}` })),
  }));
  // JavaScript enters a Go/WebAssembly module only through wasm_exec.js's Go
  // class: run starts it ("start"); _resume enters it for a call to a Go
  // function ("call") or one of Go's own timers ("timer"). Every page script
  // that defines Go is wrapped. An entry that never returns stays in open,
  // because a terminated script runs no finally block.
  const go = globalThis.__cicadaGoResumes = { open: [], recent: [], count: 0, timers: 0, modules: 0 };
  const enter = (proto, method, module, kind) => {
    const original = proto[method];
    if (typeof original !== 'function') return;
    proto[method] = function () {
      const entry = { module, kind: kind(this), at: Date.now(), end: 0 };
      go.count++;
      if (entry.kind === 'timer') go.timers++;
      go.open.push(entry);
      try { return original.apply(this, arguments); } finally {
        entry.end = Date.now();
        go.open.pop();
        go.recent.push(entry);
        if (go.recent.length > 16) go.recent.shift();
      }
    };
  };
  const wrap = Go => {
    const proto = Go && Go.prototype;
    if (!proto || Object.prototype.hasOwnProperty.call(proto, '__cicadaStall')) return;
    proto.__cicadaStall = true;
    const script = document.currentScript && document.currentScript.src || '';
    const module = `${++go.modules}:${script.split('/').pop().split('.')[0] || 'inline'}`;
    enter(proto, 'run', module, () => 'start'); // wasm_exec's run calls exports.run before its first await
    enter(proto, '_resume', module, self => self._pendingEvent ? 'call' : 'timer');
  };
  let current = globalThis.Go;
  wrap(current);
  try { Object.defineProperty(globalThis, 'Go', { configurable: true, enumerable: true, get: () => current, set: value => { wrap(value); current = value; } }); } catch {}
  setInterval(() => send('go', Date.now(), { count: go.count, timers: go.timers, open: go.open.length, modules: go.modules }), 250);

  // Breadcrumbs: each call that can block the main thread in native code (a
  // synchronous request, a dialog, audio or media devices, a canvas readback,
  // storage, cookies) and each user event, sent to Node before the call runs.
  // A binding message sent just before the thread blocks still reaches Node.
  const crumb = what => send('crumb', Date.now(), { what: String(what).slice(0, 160) });
  const text = value => { try { return String(value).slice(0, 80); } catch { return '?'; } };
  const describe = node => {
    try {
      if (!node || !node.tagName) return String(node && node.nodeName || node);
      let out = node.tagName.toLowerCase() + (node.id ? '#' + node.id : '');
      for (const name of node.getAttributeNames().filter(name => name === 'name' || name === 'value' || name.startsWith('data-')).slice(0, 3)) out += `[${name}=${node.getAttribute(name).slice(0, 30)}]`;
      const label = (node.getAttribute('aria-label') || node.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 40);
      return label ? `${out} "${label}"` : out;
    } catch { return '?'; }
  };
  const keep = (wrapper, original) => { try { Object.defineProperties(wrapper, { name: { value: original.name }, length: { value: original.length } }); } catch {} return wrapper; };
  const hook = (owner, name, label) => {
    try {
      const original = owner && owner[name];
      if (typeof original !== 'function') return;
      // Keeps name and length; constructor and native toString() differ, and nothing in the repository reads them.
      owner[name] = keep(function (...args) { try { crumb(label(this, args)); } catch {} return original.apply(this, args); }, original);
    } catch {}
  };
  const hookConstructor = (owner, name) => {
    try {
      const original = owner[name];
      if (typeof original !== 'function') return;
      owner[name] = new Proxy(original, { construct(target, args, newTarget) { crumb(`new ${name}`); return Reflect.construct(target, args, newTarget); } });
    } catch {}
  };
  const synchronous = new WeakSet();
  if (globalThis.XMLHttpRequest) {
    hook(XMLHttpRequest.prototype, 'open', (xhr, [method, url, async]) => { if (async === false) synchronous.add(xhr); return `XMLHttpRequest.open ${method} ${text(url)}${async === false ? ' SYNCHRONOUS' : ''}`; });
    hook(XMLHttpRequest.prototype, 'send', xhr => `XMLHttpRequest.send${synchronous.has(xhr) ? ' SYNCHRONOUS' : ''}`);
  }
  for (const name of ['alert', 'confirm', 'prompt', 'print', 'open']) hook(globalThis, name, (_self, args) => `window.${name}(${args.length ? text(args[0]) : ''})`);
  for (const name of ['AudioContext', 'webkitAudioContext', 'OfflineAudioContext']) hookConstructor(globalThis, name);
  if (globalThis.BaseAudioContext) for (const name of ['resume', 'suspend', 'close']) hook(globalThis.AudioContext && AudioContext.prototype, name, () => `AudioContext.${name}()`);
  if (globalThis.AudioWorklet) hook(AudioWorklet.prototype, 'addModule', (_self, [url]) => `AudioWorklet.addModule(${text(url)})`);
  if (globalThis.MediaDevices) for (const name of ['getUserMedia', 'enumerateDevices', 'getDisplayMedia', 'selectAudioOutput']) hook(MediaDevices.prototype, name, () => `navigator.mediaDevices.${name}()`);
  if (globalThis.HTMLMediaElement) for (const name of ['play', 'load', 'setSinkId']) hook(HTMLMediaElement.prototype, name, self => `${describe(self)}.${name}() src ${text(self.currentSrc || self.getAttribute('src') || '')}`);
  if (globalThis.HTMLCanvasElement) {
    hook(HTMLCanvasElement.prototype, 'getContext', (_self, [type]) => `canvas.getContext(${text(type)})`);
    for (const name of ['toDataURL', 'toBlob', 'transferControlToOffscreen']) hook(HTMLCanvasElement.prototype, name, () => `canvas.${name}()`);
  }
  if (globalThis.CanvasRenderingContext2D) hook(CanvasRenderingContext2D.prototype, 'getImageData', () => 'CanvasRenderingContext2D.getImageData()');
  const storageNames = new WeakMap();
  for (const name of ['localStorage', 'sessionStorage']) {
    try {
      const owner = Object.getOwnPropertyDescriptor(globalThis, name) ? globalThis : Window.prototype;
      const descriptor = Object.getOwnPropertyDescriptor(owner, name);
      if (!descriptor || !descriptor.get || !descriptor.configurable) continue;
      let first = true;
      Object.defineProperty(owner, name, { ...descriptor, get() { if (first) { first = false; crumb(`${name} first access`); } const storage = descriptor.get.call(this); try { storageNames.set(storage, name); } catch {} return storage; } });
    } catch {}
  }
  if (globalThis.Storage) for (const name of ['getItem', 'setItem', 'removeItem', 'clear']) hook(Storage.prototype, name, (self, [key]) => `${storageNames.get(self) || 'Storage'}.${name}(${name === 'clear' ? '' : text(key)})`);
  try {
    const cookie = Object.getOwnPropertyDescriptor(Document.prototype, 'cookie');
    if (cookie && cookie.configurable) Object.defineProperty(Document.prototype, 'cookie', { ...cookie, get() { crumb('document.cookie read'); return cookie.get.call(this); }, set(value) { crumb('document.cookie write'); cookie.set.call(this, value); } });
  } catch {}
  if (globalThis.IDBFactory) hook(IDBFactory.prototype, 'open', (_self, [name]) => `indexedDB.open(${text(name)})`);
  if (globalThis.Atomics) hook(Atomics, 'wait', () => 'Atomics.wait()');
  if (globalThis.WebAssembly) for (const name of ['Module', 'Instance']) hookConstructor(WebAssembly, name);
  if (globalThis.Notification) hook(Notification, 'requestPermission', () => 'Notification.requestPermission()');
  if (globalThis.Element) for (const name of ['requestFullscreen', 'requestPointerLock']) hook(Element.prototype, name, self => `${describe(self)}.${name}()`);
  for (const type of ['pointerdown', 'click', 'keydown', 'submit', 'change', 'input']) {
    document.addEventListener(type, event => crumb(`${type}${event.isTrusted ? '' : ' (script)'} on ${describe(event.target)}`), { capture: true, passive: true });
  }
  for (const type of ['beforeunload', 'pagehide']) addEventListener(type, () => crumb(type), { capture: true });
  crumb(`document ${text(location.pathname + location.search)}`);
}

function pageState(page) {
  return { page, interval: 0, frame: 0, gaps: [], tasks: [], crashed: 0, closed: 0, console: new Map(), crumbs: [], dialogs: [], go: null, session: null, cdp: null, ready: null };
}

function onBeat(state, kind, detail) {
  const received = Date.now();
  if (kind === 'interval' || kind === 'frame') {
    const last = state[kind];
    if (last && received - last >= 1000) { state.gaps.push({ kind, end: received, gap: received - last }); if (state.gaps.length > 8) state.gaps.shift(); }
    state[kind] = received;
  } else if (kind === 'go') state.go = { ...detail, received };
  else if (kind === 'crumb' && detail) {
    // Fold repeats (a scroll that saves a preference each time) into one line.
    const what = redact(detail.what), previous = state.crumbs[state.crumbs.length - 1];
    if (previous && previous.what === what) { previous.count++; previous.at = received; } else { state.crumbs.push({ what, at: received, count: 1 }); if (state.crumbs.length > 20) state.crumbs.shift(); }
  }
  else if (detail && detail.duration >= 200) { state.tasks.push({ kind, received, ...detail }); if (state.tasks.length > 12) state.tasks.shift(); }
}

function listen(state) {
  const { page } = state;
  page.on('crash', () => { state.crashed = Date.now(); });
  page.on('close', () => { state.closed = Date.now(); });
  // A modal dialog blocks the page's main thread until it is answered.
  // Playwright answers dialogs only while nobody listens for them (it accepts
  // beforeunload, so the navigation goes ahead, and dismisses the rest), so
  // this listener does the same unless the suite also listens.
  page.on('dialog', dialog => {
    state.dialogs.push({ at: Date.now(), what: `${dialog.type()} "${redact(dialog.message().slice(0, 120))}"` });
    if (state.dialogs.length > 5) state.dialogs.shift();
    if (page.listenerCount('dialog') === 1 && page.context().listenerCount('dialog') === 0) (dialog.type() === 'beforeunload' ? dialog.accept() : dialog.dismiss()).catch(() => {});
  });
  // DevTools delivers console messages while script runs, so a page stuck in a
  // loop that logs (wasm_exec.js warns "missed timeout event" on every turn of
  // its retry loop) still shows here. Count by text and keep the newest.
  page.on('console', message => {
    if (message.type() !== 'warning' && message.type() !== 'error') return;
    const text = `${message.type()}: ${redact(message.text().slice(0, 200))}`;
    const seen = state.console.get(text) || { count: 0, last: 0 };
    state.console.delete(text);
    state.console.set(text, { count: seen.count + 1, last: Date.now() });
    if (state.console.size > 10) state.console.delete(state.console.keys().next().value);
  });
}

// openSession attaches one DevTools session per page while the page still
// answers (Debugger.enable waits for the main thread).
async function openSession(state, wantStack) {
  const session = await within(state.page.context().newCDPSession(state.page), 5000, null);
  if (!session || session.error) return;
  state.session = session;
  if (!wantStack) return;
  // Skip debugger statements and breakpoints until report() asks for a pause.
  const enabled = await within(session.send('Debugger.enable').then(() => session.send('Debugger.setSkipAllPauses', { skip: true })), 5000, 'timeout');
  if (enabled !== 'timeout' && !(enabled && enabled.error)) state.cdp = session;
}

// A closed page is not stalled: it is no longer watched.
const isStalled = (state, ms = 2000) => !state.closed && (!!state.crashed || (state.interval > 0 && Date.now() - Math.max(state.interval, state.frame) >= ms));

// watchPage watches one page. It must run before the page's first navigation.
async function watchPage(page, { serverLog, stack = envStack() } = {}) {
  const state = pageState(page);
  listen(state);
  await page.exposeBinding(BINDING, (_source, kind, _at, detail) => onBeat(state, kind, detail));
  await page.addInitScript(pageProbe);
  state.ready = openSession(state, stack);
  await state.ready;
  return makeWatch(() => [state], serverLog);
}

// watchContext watches every page of a context, including pages opened later.
// It must run before the context's pages navigate. With stack, a page opened
// later gets the Debugger as soon as Playwright reports it, so a freeze in its
// first milliseconds may have no stack.
async function watchContext(context, { serverLog, stack = envStack() } = {}) {
  const states = new Map();
  const attach = page => {
    if (!states.has(page)) {
      const state = pageState(page);
      states.set(page, state);
      listen(state);
      state.ready = openSession(state, stack);
    }
    return states.get(page);
  };
  await context.exposeBinding(BINDING, (source, kind, _at, detail) => { if (source && source.page) onBeat(attach(source.page), kind, detail); });
  await context.addInitScript(pageProbe);
  context.on('page', attach);
  for (const page of context.pages()) attach(page);
  return makeWatch(() => [...states.values()], serverLog);
}

// goSnapshot reads the Go entry record from a page whose main thread is stuck.
// Runtime.evaluate waits for the main thread, so it is queued first; then
// Runtime.terminateExecution, which V8 serves through an interrupt, ends the
// running script and lets the queued read run. This leaves the page's Go
// programs unusable, so it runs only in a report on a stalled page.
async function goSnapshot(session) {
  if (!session) return 'go: no DevTools session';
  const read = session.send('Runtime.evaluate', { returnByValue: true, expression: '(() => { const g = globalThis.__cicadaGoResumes; return g ? { open: g.open, recent: g.recent.slice(-6), count: g.count, timers: g.timers, now: Date.now() } : null; })()' });
  read.catch(() => {});
  const terminated = await within(session.send('Runtime.terminateExecution'), 1500, 'timeout');
  const value = await within(read, 2500, 'timeout');
  if (value === 'timeout') return `go: the page did not answer within 2500 ms after Runtime.terminateExecution (${terminated === 'timeout' ? 'which also did not answer' : 'which answered'}): the main thread is blocked outside JavaScript, or a flood of console messages (counted above) is delaying DevTools`;
  if (value.error || !value.result || !value.result.value) return `go: no entry record (${value.error ? value.error.message : 'probe missing'})`;
  const g = value.result.value, ago = at => `${g.now - at} ms ago`;
  const lines = [`go: ${g.count} entries since load (${g.timers} for Go timers); open now: ${g.open.length ? '' : 'none (the main thread was not inside Go)'}`];
  for (const entry of g.open) lines.push(`  OPEN ${entry.module} ${entry.kind} entry ${ago(entry.at)} and never returned`);
  lines.push('  last finished entries: ' + g.recent.map(entry => `${entry.module} ${entry.kind} ${entry.end - entry.at} ms (ended ${ago(entry.end)})`).join('; '));
  return lines.join('\n');
}

// rendererCPU samples Chrome's renderer processes twice, 1 s apart. A frozen
// renderer near 100% is computing (a script loop or layout); near 0% it is
// waiting on something outside itself.
async function rendererCPU(browser) {
  if (!browser || typeof browser.newBrowserCDPSession !== 'function') return 'renderer CPU: no browser session';
  const session = await within(browser.newBrowserCDPSession(), 1000, null);
  if (!session || session.error) return 'renderer CPU: no browser session';
  const sample = async () => { const info = await within(session.send('SystemInfo.getProcessInfo'), 750, null); return info && info.processInfo ? new Map(info.processInfo.filter(p => p.type === 'renderer').map(p => [p.id, p.cpuTime])) : null; };
  try {
    const first = await sample();
    await new Promise(resolve => setTimeout(resolve, 1000).unref());
    const second = await sample();
    if (!first || !second) return 'renderer CPU: SystemInfo.getProcessInfo did not answer';
    return 'renderer CPU over 1 s: ' + ([...second].map(([pid, cpu]) => `pid ${pid} ${Math.round(100 * (cpu - (first.get(pid) || 0)))}%`).join(', ') || 'no renderer processes');
  } finally { within(session.detach(), 500, null); }
}

// --- native stacks -----------------------------------------------------------
// A main thread blocked outside script (a synchronous call into another Chrome
// process, a modal dialog, a lock) answers no DevTools command, so the report
// reads Chrome's processes from outside. CICADA_PAGE_STALL_NATIVE: 0 off; 1
// (default) thread tables, plus gdb, eu-stack or sample stacks when the tool is
// there; install (CI) also installs gdb through sudo, only when a page stalled.
const nativeMode = () => String(process.env.CICADA_PAGE_STALL_NATIVE || '1').toLowerCase();
const nativeBudget = () => ({ 0: 0, install: 100000 })[nativeMode()] ?? 15000;

// installRefusal says why installing gdb is not allowed here, or returns '' if
// it is. install runs apt-get through sudo, so it also requires GitHub's own
// marker of a hosted, throwaway runner: a self-hosted machine (buildbox is a
// production host) that sets RUNNER_TEMP and runs ci-studio-browser.sh must
// never install packages because a page stalled.
function installRefusal(env = process.env) {
  if (String(env.CICADA_PAGE_STALL_NATIVE || '').toLowerCase() !== 'install') return 'CICADA_PAGE_STALL_NATIVE is not install';
  if (env.RUNNER_ENVIRONMENT !== 'github-hosted') return `RUNNER_ENVIRONMENT is ${env.RUNNER_ENVIRONMENT ? `"${env.RUNNER_ENVIRONMENT}"` : 'not set'}, not "github-hosted"`;
  return '';
}

// run is execFile with a time limit that never throws; it resolves to the output.
function run(file, args, timeout) {
  return new Promise(resolve => {
    if (timeout <= 0) return resolve('');
    execFile(file, args, { encoding: 'utf8', timeout, killSignal: 'SIGKILL', maxBuffer: 8 << 20 }, (error, stdout, stderr) => resolve(String(stdout || '') + (error && !stdout ? String(stderr || '') : '')));
  });
}
const sh = (script, args, timeout) => run('sh', ['-c', script, 'sh', ...args.map(String)], timeout);
// runSudo runs a command as root within timeout ms. A time limit on sudo does
// not stop the command sudo started (a hung gdb would keep Chrome stopped, a
// hung apt-get would keep the package lock), so timeout(1) runs inside sudo:
// it sends TERM a little before the outer limit and KILL 2 s after that.
function runSudo(args, timeout) {
  const inner = Math.max(0.5, (timeout - 2500) / 1000).toFixed(1);
  return run('sudo', ['-n', 'timeout', '-k', '2', inner, ...args.map(String)], timeout);
}
let sudoChecked = null;
const canSudo = async () => sudoChecked ?? (sudoChecked = process.platform === 'linux' && (await runSudo(['sh', '-c', 'echo yes'], 3000)).trim() === 'yes');
const hasTool = async tool => (await sh('command -v "$1"', [tool], 2000)).trim() !== '';

// chromeProcesses lists the browser's processes with Chrome's own names for them.
async function chromeProcesses(browser) {
  const table = processTable(), byPid = new Map(table.map(p => [p.pid, p]));
  const flag = (args, name) => { const match = String(args || '').match(new RegExp(`--${name}=(\\S+)`)); return match ? match[1] : ''; };
  let list = [];
  const session = browser && typeof browser.newBrowserCDPSession === 'function' ? await within(browser.newBrowserCDPSession(), 1000, null) : null;
  if (session && !session.error) {
    const info = await within(session.send('SystemInfo.getProcessInfo'), 1500, null);
    within(session.detach(), 500, null);
    if (info && info.processInfo) list = info.processInfo.map(p => ({ pid: p.id, type: p.type }));
  }
  if (!list.length) {
    // The browser does not answer: walk this process's tree.
    const tree = [process.pid];
    for (let i = 0; i < tree.length; i++) for (const p of table) if (p.ppid === tree[i]) tree.push(p.pid);
    // withChromeLog's script is a shell around Chrome: leave it out.
    list = tree.slice(1).filter(pid => /chrom/i.test(byPid.get(pid).args) && !/^(\S*\/)?(ba|da)?sh\s/.test(byPid.get(pid).args)).map(pid => ({ pid, type: flag(byPid.get(pid).args, 'type') || 'browser' }));
  }
  return list.map(p => {
    const args = byPid.has(p.pid) ? byPid.get(p.pid).args : '';
    const sub = flag(args, 'utility-sub-type');
    return { ...p, label: `${p.type}${sub && sub !== p.type ? ' ' + sub : ''}` };
  });
}

const syscalls = {
  x64: { 0: 'read', 1: 'write', 7: 'poll', 23: 'select', 35: 'nanosleep', 42: 'connect', 43: 'accept', 45: 'recvfrom', 47: 'recvmsg', 61: 'wait4', 202: 'futex', 230: 'clock_nanosleep', 232: 'epoll_wait', 270: 'pselect6', 271: 'ppoll', 281: 'epoll_pwait', 441: 'epoll_pwait2' },
  arm64: { 22: 'epoll_pwait', 63: 'read', 64: 'write', 73: 'ppoll', 98: 'futex', 101: 'nanosleep', 115: 'clock_nanosleep', 203: 'connect', 207: 'recvfrom', 212: 'recvmsg', 260: 'wait4', 441: 'epoll_pwait2' },
};
const THREADS = 'for t in /proc/$1/task/*; do s=$(cat "$t/stat" 2>/dev/null); printf "%s\\t%s\\t%s\\t%s\\t%s\\n" "${t##*/}" "$(cat "$t/comm" 2>/dev/null)" "${s##*) }" "$(cat "$t/wchan" 2>/dev/null)" "$(cut -d" " -f1 "$t/syscall" 2>/dev/null)"; done';

// threadTable summarises a process's threads from /proc: the main thread on
// its own line, the others grouped. "futex" in a main thread usually means it
// waits for a reply from another thread or process.
async function threadTable(pid, sudo, timeout) {
  const read = async elevated => (await (elevated ? runSudo(['sh', '-c', THREADS, 'sh', pid], timeout) : sh(THREADS, [pid], timeout))).split('\n').filter(Boolean).map(line => {
    const [tid, comm, stat, wchan, nr] = line.split('\t');
    const names = syscalls[process.arch] || {};
    return { tid: Number(tid), comm, state: String(stat || '').split(' ')[0], wchan: wchan && wchan !== '0' ? wchan : '-', syscall: nr === undefined || nr === '' ? '?' : nr === 'running' ? 'running' : names[nr] || `syscall ${nr}` };
  });
  let threads = await read(false);
  if (sudo && threads.length && threads.every(t => t.syscall === '?')) threads = await read(true);
  if (!threads.length) return '  (no /proc entries; the process has exited)';
  const line = t => `${t.comm} ${t.state} wchan=${t.wchan} ${t.syscall}`;
  const main = threads.find(t => t.tid === pid), groups = new Map();
  for (const t of threads) if (t !== main) groups.set(line(t), (groups.get(line(t)) || 0) + 1);
  const out = [`  main thread: ${main ? line(main) : '(not found)'}`];
  out.push(`  other threads (${threads.length - 1}): ` + [...groups].sort((a, b) => b[1] - a[1]).slice(0, 14).map(([text, count]) => `${text}${count > 1 ? ` x${count}` : ''}`).join('; '));
  if (sudo) {
    const kernel = (await runSudo(['cat', `/proc/${pid}/task/${pid}/stack`], timeout)).trim().split('\n').slice(0, 8).map(l => l.replace(/^\[<[0-9a-f]+>\] /, ''));
    if (kernel[0]) out.push(`  main thread kernel stack: ${kernel.join(' < ')}`);
  }
  return out.join('\n');
}

// debuggerStacks prints every thread's backtrace (main thread in full, others
// in short) with gdb or eu-stack. Chrome's binary is stripped, so its frames are
// addresses; library frames (libc, audio, fontconfig, D-Bus) have names.
async function debuggerStacks(pid, tool, sudo, timeout) {
  const command = tool === 'gdb'
    ? ['gdb', ['-p', String(pid), '-batch', '-nx', '-q', '-ex', 'set pagination off', '-ex', 'set print thread-events off', '-ex', 'thread apply all bt 25']]
    : ['eu-stack', ['-p', String(pid), '-n', '25']];
  const text = sudo ? await runSudo([command[0], ...command[1]], timeout) : await run(command[0], command[1], timeout);
  const blocks = text.split(/\n(?=Thread |TID )/).filter(block => /^(Thread|TID) /.test(block));
  if (!blocks.length) return `  ${tool}: no stacks (${text.trim().split('\n').slice(-2).join(' / ').slice(0, 200) || 'no output'})`;
  const isMain = block => new RegExp(`(LWP ${pid}\\)|^TID ${pid}:)`).test(block.split('\n')[0]);
  return blocks.sort((a, b) => isMain(b) - isMain(a)).map(block => {
    const lines = block.split('\n').filter(l => l.trim() && !/^\[Inferior \d+ .*detached\]/.test(l));
    return lines.slice(0, isMain(block) ? 26 : 4).map(l => '  ' + l.slice(0, 160)).join('\n');
  }).join('\n').split('\n').slice(0, 160).join('\n');
}

// elfBuildID reads the GNU build ID note of a 64-bit little-endian ELF file
// (Chrome on Linux) without binutils.
function elfBuildID(file) {
  let fd;
  try {
    fd = fs.openSync(file, 'r');
    const read = (offset, length) => { const buffer = Buffer.alloc(length); fs.readSync(fd, buffer, 0, length, offset); return buffer; };
    const header = read(0, 64);
    if (header.toString('latin1', 1, 4) !== 'ELF' || header[4] !== 2 || header[5] !== 1) return '';
    const phoff = Number(header.readBigUInt64LE(32)), phentsize = header.readUInt16LE(54), phnum = header.readUInt16LE(56);
    for (let i = 0; i < phnum; i++) {
      const ph = read(phoff + i * phentsize, phentsize);
      if (ph.readUInt32LE(0) !== 4) continue; // PT_NOTE
      const notes = read(Number(ph.readBigUInt64LE(8)), Math.min(Number(ph.readBigUInt64LE(32)), 1 << 16));
      for (let at = 0; at + 12 <= notes.length;) {
        const namesz = notes.readUInt32LE(at), descsz = notes.readUInt32LE(at + 4), type = notes.readUInt32LE(at + 8);
        const name = at + 12, desc = name + ((namesz + 3) & ~3);
        if (type === 3 && notes.toString('latin1', name, name + 3) === 'GNU') return notes.toString('hex', desc, desc + descsz);
        at = desc + ((descsz + 3) & ~3);
      }
    }
  } catch {} finally { if (fd !== undefined) fs.closeSync(fd); }
  return '';
}

// nativeStacks describes every Chrome process from outside, within budgetMS.
async function nativeStacks(browser, budgetMS) {
  const deadline = Date.now() + budgetMS, left = () => deadline - Date.now();
  const mode = nativeMode();
  if (mode === '0' || budgetMS <= 0) return 'native: off (CICADA_PAGE_STALL_NATIVE=0)';
  const processes = await chromeProcesses(browser);
  if (!processes.length) return 'native: no Chrome processes found';
  const lines = [`native: ${processes.length} Chrome processes: ${processes.map(p => `${p.pid} ${p.label}`).join(', ')}`];
  // Stacks for the processes a renderer can wait on, most likely first.
  const rank = p => ['renderer', 'browser', 'gpu', 'audio', 'storage', 'network'].findIndex(word => p.label.toLowerCase().includes(word));
  const interesting = processes.filter(p => rank(p) >= 0).sort((a, b) => rank(a) - rank(b));
  if (process.platform === 'darwin') {
    for (const p of interesting) {
      if (left() < 4000) { lines.push(`native: no time left for ${p.pid} ${p.label}`); continue; }
      const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'page-stall-sample-')), file = path.join(dir, 'sample.txt');
      await run('sample', [String(p.pid), '2', '-mayDie', '-file', file], Math.min(left(), 8000));
      let text = '';
      try { text = fs.readFileSync(file, 'utf8'); } catch {}
      fs.rmSync(dir, { recursive: true, force: true });
      const graph = text.split('\n').filter(l => /^\s+\d+ /.test(l) || /^\s*Thread_/.test(l)).slice(0, 60);
      lines.push(`--- ${p.pid} ${p.label} (sample, 2 s) ---`, ...(graph.length ? graph.map(l => '  ' + l.trim().slice(0, 160)) : ['  (sample gave no call graph)']));
    }
    return lines.join('\n');
  }
  if (process.platform !== 'linux') return lines.concat('native: stacks are captured on Linux and macOS only').join('\n');
  const sudo = await canSudo();
  for (const p of processes) {
    if (left() < 1000) { lines.push(`native: no time left for ${p.pid} ${p.label}`); continue; }
    lines.push(`--- ${p.pid} ${p.label} ---`, await threadTable(p.pid, sudo, Math.min(left(), 3000)));
  }
  let tool = (await hasTool('gdb')) ? 'gdb' : (await hasTool('eu-stack')) ? 'eu-stack' : '';
  const refusal = mode === 'install' ? installRefusal() : 'off';
  if (!tool && mode === 'install' && refusal) lines.push(`native: not installing gdb: ${refusal}`);
  if (!tool && mode === 'install' && !refusal && sudo) {
    // Only here, on a stalled page: the CI image has no debugger.
    const install = () => runSudo(['env', 'DEBIAN_FRONTEND=noninteractive', 'apt-get', 'install', '-y', '-qq', '--no-install-recommends', 'gdb'], Math.min(left() - 20000, 60000));
    const started = Date.now();
    await install();
    if (!(await hasTool('gdb')) && left() > 40000) { await runSudo(['apt-get', 'update', '-qq'], Math.min(left() - 30000, 45000)); await install(); }
    tool = (await hasTool('gdb')) ? 'gdb' : '';
    lines.push(`native: ${tool ? 'installed gdb' : 'could not install gdb'} in ${Date.now() - started} ms`);
  }
  if (!tool) return lines.concat('native: no gdb or eu-stack, so no user-space stacks').join('\n');
  let scope = '0';
  try { scope = String(fs.readFileSync('/proc/sys/kernel/yama/ptrace_scope', 'utf8')).trim(); } catch {}
  const traceable = sudo || process.getuid() === 0 || scope === '0';
  if (!traceable) return lines.concat(`native: ${tool} cannot attach (ptrace_scope is not 0 and sudo -n is unavailable)`).join('\n');
  // Chrome's frames are bare addresses: print the executable's build ID and
  // where its code is mapped, so an address can be resolved against symbols later.
  const exe = (await (sudo ? runSudo(['readlink', `/proc/${processes[0].pid}/exe`], 3000) : run('readlink', [`/proc/${processes[0].pid}/exe`], 2000))).trim();
  const id = exe ? elfBuildID(exe) : '';
  lines.push(`native: executable ${exe || '?'} build ID ${id || '?'}`);
  for (const p of interesting) {
    if (left() < 3000) { lines.push(`native: no time left for ${tool} on ${p.pid} ${p.label}`); continue; }
    const maps = (await (sudo ? runSudo(['cat', `/proc/${p.pid}/maps`], 3000) : run('cat', [`/proc/${p.pid}/maps`], 2000))).split('\n').find(line => exe && line.includes(' r-xp ') && line.endsWith(exe)) || '';
    lines.push(`--- ${p.pid} ${p.label} (${tool}; code mapped ${maps.split(' ')[0] || '?'} at file offset 0x${maps.split(' ')[2] || '?'}) ---`, await debuggerStacks(p.pid, tool, sudo, Math.min(left(), 15000)));
  }
  return lines.join('\n');
}

// --- Chrome's own log ----------------------------------------------------------
const chromeLogs = new Set();
const shellQuote = text => `'${String(text).replace(/'/g, `'\\''`)}'`;

// withChromeLog returns launch options whose Chrome writes its own log
// (--enable-logging=stderr --v=0) to a file that report() tails, so a stuck
// audio, GPU or storage service shows in the browser's words. A short script
// stands in for the executable and runs Chrome with stderr appended to the
// file; Playwright talks to Chrome over a pipe. When Chrome exits with an
// error (a launch that fails, a crash), the script writes the log's last 30
// lines, owner links redacted, to its own stderr, so they appear under
// Playwright's "Browser logs:" before the file is deleted. The script leads
// the process group Playwright starts, with Chrome inside it.
// CICADA_PAGE_STALL_CHROME_LOG=0 turns it off.
function withChromeLog(options = {}, browserType = null) {
  if (process.platform === 'win32' || process.env.CICADA_PAGE_STALL_CHROME_LOG === '0') return options;
  let executable = options.executablePath;
  try { if (!executable && browserType) executable = browserType.executablePath(); } catch {}
  if (!executable) return options;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'page-stall-chrome-'));
  const log = path.join(dir, 'chrome.log'), wrapper = path.join(dir, 'chrome');
  const redactLog = `sed -E ${shellQuote('s|/__local/[^[:space:]/"?#\']+|/__local/<redacted>|g')}`;
  fs.writeFileSync(wrapper, [
    '#!/bin/sh',
    `${shellQuote(executable)} "$@" 2>>${shellQuote(log)}`,
    'code=$?',
    `if [ "$code" -ne 0 ]; then echo "page-stall: Chrome exited with code $code; the last 30 lines of its log:" >&2; tail -n 30 ${shellQuote(log)} | ${redactLog} >&2; fi`,
    'exit "$code"',
    '',
  ].join('\n'), { mode: 0o755 });
  chromeLogs.add(log);
  process.once('exit', () => { try { fs.rmSync(dir, { recursive: true, force: true }); } catch {} });
  return { ...options, executablePath: wrapper, args: [...(options.args || []), '--enable-logging=stderr', '--v=0'] };
}

async function stack(state) {
  const cdp = state.cdp;
  if (!cdp) return 'stack: not captured (watch with { stack: true } or CICADA_PAGE_STALL_STACK=1)';
  let paused;
  const pausedEvent = new Promise(resolve => { paused = resolve; });
  const onPaused = event => paused(event);
  cdp.on('Debugger.paused', onPaused);
  try {
    await within(cdp.send('Debugger.setSkipAllPauses', { skip: false }), 500, null);
    await within(cdp.send('Debugger.pause'), 500, null);
    const event = await within(pausedEvent, 2000, null);
    if (!event || event.error) return 'stack: the main thread did not pause within 2000 ms (blocked outside JavaScript and WebAssembly, or idle)';
    const frames = event.callFrames.slice(0, 40).map(frame => `  ${frame.functionName || '(anonymous)'}  ${frame.url || `script ${frame.location.scriptId}`}:${frame.location.lineNumber + 1}:${frame.location.columnNumber + 1}`);
    return `stack (paused: ${event.reason}), ${event.callFrames.length} frames, innermost first:\n${frames.join('\n')}`;
  } finally {
    cdp.off('Debugger.paused', onPaused);
    await within(cdp.send('Debugger.setSkipAllPauses', { skip: true }), 500, null);
    await within(cdp.send('Debugger.resume'), 500, null);
  }
}

// pageReport describes one open page, within the report's deadline.
// pageLines describes one open page from what Node recorded; it never waits.
function pageLines(state, started) {
  const now = Date.now(), at = ms => ms ? `${now - ms} ms ago` : 'never';
  const lines = [`page ${redact(state.page.url())}`];
  lines.push(`watched for ${now - started} ms; last setInterval beat ${at(state.interval)}; last animation-frame beat ${at(state.frame)}`);
  lines.push(state.crashed ? `renderer CRASHED ${now - state.crashed} ms ago` : 'renderer crash: none reported');
  lines.push(`earlier main-thread gaps of 1 s or more: ${state.gaps.length ? state.gaps.map(gap => `${gap.kind} ${gap.gap} ms ending ${now - gap.end} ms ago`).join('; ') : 'none'}`);
  lines.push(`long tasks and frames of 200 ms or more (newest last): ${state.tasks.length ? '' : 'none'}`);
  for (const task of state.tasks) {
    const scripts = (task.scripts || []).map(script => `${script.duration} ms ${script.invoker || ''} ${redact(script.source)}`.trim()).join(' | ');
    lines.push(`  ${task.kind} ${task.duration} ms, started ${now - task.start} ms ago${scripts ? ': ' + scripts : ''}`);
  }
  lines.push(`console warnings and errors (count, newest last): ${state.console.size ? '' : 'none'}`);
  for (const [text, seen] of state.console) lines.push(`  ${seen.count} x ${text} (last ${now - seen.last} ms ago)`);
  lines.push(`dialogs opened: ${state.dialogs.length ? state.dialogs.map(d => `${d.what} ${now - d.at} ms ago`).join('; ') : 'none'}`);
  lines.push(`last things the page did (newest last): ${state.crumbs.length ? '' : 'none recorded'}`);
  for (const c of state.crumbs) lines.push(`  ${now - c.at} ms ago: ${c.what}${c.count > 1 ? ` (x${c.count})` : ''}`);
  lines.push(state.go ? `go at the last heartbeat (${now - state.go.received} ms ago): ${state.go.modules} Go modules wrapped, ${state.go.count} entries, ${state.go.timers} for Go timers, ${state.go.open} open` : 'go: no heartbeat from the entry probe');
  return lines;
}

async function step(label, fn, deadline) {
  const left = deadline - Date.now();
  if (left < 300) return `${label}: skipped, the report's time budget is spent`;
  const value = await within(fn(), left, `${label}: did not finish within the report's time budget`);
  return typeof value === 'string' ? value : `${label}: failed: ${value && value.error ? value.error.message : String(value)}`;
}

function makeWatch(states, serverLog) {
  const started = Date.now();
  const watch = {
    // stalled reports whether an open watched page has missed beats for ms.
    stalled: ms => states().some(state => isStalled(state, ms)),
    // nativeMS is how long the last report spent on native stacks.
    nativeMS: 0,
    // report describes every open watched page (stalled ones first) within
    // budgetMS, plus, when a page stalled, Chrome's processes from outside
    // within nativeMS. Order matters: sample CPU and native stacks while the
    // page is stuck, pause for a stack, then end the running script to read
    // the Go record.
    async report(reason, { budgetMS = 8000, nativeMS = nativeBudget() } = {}) {
      let deadline = Date.now() + budgetMS;
      const open = states().filter(state => !state.closed).sort((a, b) => isStalled(b) - isStalled(a));
      const stuck = open.filter(state => !state.crashed && isStalled(state));
      const lines = [`--- page stall report (${reason && reason.message ? reason.message.split('\n')[0] : reason || 'requested'}) ---`];
      if (!open.length) lines.push('no open watched pages');
      for (const state of open) lines.push(...pageLines(state, started));
      watch.nativeMS = 0;
      if (stuck.length) {
        const browser = stuck[0].page.context().browser();
        lines.push(await step('renderer CPU', () => rendererCPU(browser), deadline));
        const nativeStarted = Date.now();
        lines.push(await within(nativeStacks(browser, nativeMS), nativeMS + 1000, 'native: did not finish within its budget'));
        watch.nativeMS = Date.now() - nativeStarted;
        deadline += watch.nativeMS;
        for (const state of stuck) {
          lines.push(`page ${redact(state.page.url())}:`, await step('stack', () => stack(state), deadline), await step('go', () => goSnapshot(state.session), deadline));
        }
      }
      for (const log of chromeLogs) lines.push(`--- last 30 lines of Chrome's log ---`, tail(log, 30));
      lines.push(`--- last 40 lines of the Studio server log ${serverLog || ''} ---`, tail(serverLog));
      return lines.join('\n');
    },
  };
  return watch;
}

// processTable lists processes as { pid, ppid, pgid, args } through ps, which
// Linux and macOS both provide.
function processTable() {
  try {
    return execFileSync('ps', ['-A', '-o', 'pid=,ppid=,pgid=,args='], { encoding: 'utf8', timeout: 2000 }).split('\n').map(line => {
      const match = line.trim().match(/^(\d+)\s+(\d+)\s+(\d+)\s+(.*)$/);
      return match && { pid: Number(match[1]), ppid: Number(match[2]), pgid: Number(match[3]), args: match[4] };
    }).filter(Boolean);
  } catch { return []; }
}

// browserProcessID asks the browser for its own process ID while it answers.
async function browserProcessID(browser) {
  if (!browser || typeof browser.newBrowserCDPSession !== 'function') return 0;
  const session = await within(browser.newBrowserCDPSession(), 1000, null);
  if (!session || session.error) return 0;
  const info = await within(session.send('SystemInfo.getProcessInfo'), 1000, null);
  within(session.detach(), 500, null);
  const entry = info && info.processInfo && info.processInfo.find(process => process.type === 'browser');
  return entry ? entry.id : 0;
}

// killBrowser kills a browser process and everything it started. Playwright
// launches the browser as the leader of its own process group on Linux and
// macOS, so one group kill reaches its renderer, GPU and utility processes. If
// the process leads no group, its descendants are found through ps.
function killBrowser(pid) {
  const table = processTable(), byPid = new Map(table.map(p => [p.pid, p]));
  const own = byPid.has(process.pid) ? byPid.get(process.pid).pgid : -1;
  const pids = pid ? [pid] : table.filter(p => p.ppid === process.pid && p.pgid === p.pid && /chrom/i.test(p.args)).map(p => p.pid);
  const killed = [];
  for (const target of pids) {
    // The group Playwright started: the browser's own, or withChromeLog's script's. Never this process's.
    const group = byPid.has(target) ? byPid.get(target).pgid : target;
    if (group > 1 && group !== own) { try { process.kill(-group, 'SIGKILL'); killed.push(`process group ${group}`); continue; } catch {} }
    const tree = [target];
    for (let i = 0; i < tree.length; i++) for (const p of table) if (p.ppid === tree[i]) tree.push(p.pid);
    for (const p of tree.reverse()) { try { process.kill(p, 'SIGKILL'); } catch {} }
    killed.push(`process ${target} and ${tree.length - 1} descendants`);
  }
  return killed.length ? killed.join(', ') : 'no browser process found';
}

// closeBrowser ends the browser even when a page is frozen. before (optional)
// runs first with its own limit, for work such as saving storage state, which
// waits on every page and never settles on a frozen one. browser.close() works
// on a frozen page (the browser process answers) but gets a limit too; past
// it, the browser's process group is killed. It returns 'closed' or 'killed'.
async function closeBrowser(browser, { before = null, beforeMS = 2500, closeMS = 2500 } = {}) {
  if (!browser) return 'closed';
  if (before) {
    const result = await within(Promise.resolve().then(before), beforeMS, 'timeout');
    if (result === 'timeout') console.error(`page-stall: teardown step did not finish within ${beforeMS} ms; closing the browser anyway`);
    else if (result && result.error) console.error(`page-stall: teardown step failed: ${result.error.message}`);
  }
  const pid = await browserProcessID(browser);
  const closed = await within(Promise.resolve().then(() => browser.close()), closeMS, 'timeout');
  if (closed !== 'timeout') return 'closed';
  console.error(`page-stall: browser.close() did not finish within ${closeMS} ms; killed ${killBrowser(pid)}`);
  return 'killed';
}

// openHandles names what keeps the event loop alive (types and counts, at most 10).
function openHandles() {
  const counts = new Map();
  let handles = [];
  try { handles = process._getActiveHandles(); } catch {}
  for (const handle of handles) {
    if (handle === process.stdin || handle === process.stdout || handle === process.stderr) continue;
    let name = handle && handle.constructor ? handle.constructor.name : typeof handle;
    if (handle && typeof handle.pid === 'number') name += ` (pid ${handle.pid}${handle.spawnfile ? ' ' + String(handle.spawnfile).split('/').pop() : ''})`;
    counts.set(name, (counts.get(name) || 0) + 1);
  }
  try { for (const type of process.getActiveResourcesInfo()) if (type === 'Timeout' || type === 'Immediate') counts.set(type, (counts.get(type) || 0) + 1); } catch {}
  const list = [...counts].slice(0, 10).map(([name, count]) => `${name} x${count}`);
  return list.length ? list.join(', ') : 'nothing Node lists (a pending native request?)';
}

let anyFailed = false, exitArmed = false;

// armExit exits the process ms from now if something still holds it open.
// The timer is unref'd, so it never keeps a finished process alive.
function armExit(ms) {
  if (exitArmed) return;
  exitArmed = true;
  setTimeout(() => {
    const code = anyFailed || (process.exitCode !== undefined && process.exitCode !== 0) ? 1 : 0;
    console.error(`page-stall: ${code ? 'a test failed' : 'every watched test passed'}, but the process was still alive ${ms} ms after its last teardown began; held open by: ${openHandles()}. Exiting with code ${code}.`);
    process.exit(code);
  }, ms).unref();
}

// teardown registers the test's after hook; see the top of this file. The exit
// code of a forced exit counts only tests that used teardown(), so pass
// exitWhenDone only in a file's last test. node --test, which CI uses, still
// fails the file for any test that reported a failure, whatever the code.
function teardown(t, browser, { watch = null, before = null, exitWhenDone = false, hookMS = 15000, reportMS = 8000, beforeMS = 2500, closeMS = 2500, nativeMS = nativeBudget(), exitAfterMS = hookMS + nativeMS + 5000 } = {}) {
  t.after(async () => {
    const started = Date.now();
    const passed = t.passed !== false;
    if (!passed) anyFailed = true;
    if (exitWhenDone) armExit(exitAfterMS);
    const watches = (Array.isArray(watch) ? watch : [watch]).filter(Boolean);
    // Decide before the report: its Go snapshot ends the stuck script, so the
    // page answers again for a moment and could freeze again under before().
    const stalled = watches.some(w => w.stalled());
    let native = 0;
    if (watches.length && (!passed || process.env.CICADA_PAGE_STALL_REPORT === '1')) {
      for (const w of watches) {
        // Native stacks cover the whole browser, so only the first stalled report reads them.
        const budget = Math.max(0, reportMS - 250 - (Date.now() - started - native));
        const text = await within(w.report(passed ? 'requested by CICADA_PAGE_STALL_REPORT' : 'the test did not pass', { budgetMS: budget, nativeMS: native ? 0 : nativeMS }), budget + (native ? 0 : nativeMS) + 1500, null);
        native += w.nativeMS;
        console.error(typeof text === 'string' ? text : `page stall report did not finish within ${reportMS} ms`);
      }
    }
    const outcome = await closeBrowser(browser, { before: stalled ? null : before, beforeMS, closeMS });
    if (!passed || outcome !== 'closed') console.error(`page-stall: teardown took ${Date.now() - started} ms of its ${hookMS + nativeMS} ms limit (${native} ms of it reading native stacks)${stalled && before ? '; skipped the teardown step because a page stopped responding' : ''}; browser ${outcome}`);
  }, { timeout: hookMS + nativeMS });
}

module.exports = { watchContext, watchPage, teardown, closeBrowser, killBrowser, withChromeLog, nativeStacks, installRefusal, runSudo, within, tail, redact };
