'use strict';
// page-stall.cjs makes a browser step that times out explain itself, and makes
// sure a failed suite cannot keep its Node process alive.
//
// Use, one browser per test; call watchContext before the context's pages navigate:
//
//   const stall = require('./page-stall.cjs');
//   const browser = await chromium.launch(...);
//   const context = await browser.newContext(...);
//   const watch = await stall.watchContext(context, { serverLog: process.env.CICADA_STUDIO_STARTUP_LOG });
//   stall.teardown(t, browser, { watch, before: () => context.storageState({ path }) });
//
// teardown() registers the test's after hook (15 s). When the test did not pass
// it prints watch.report() (at most 8 s), runs before() unless a page stopped
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
//   - with { stack: true } (or CICADA_PAGE_STALL_STACK=1), the Debugger domain,
//     so report() can print the paused stack. V8 serves Debugger.pause through
//     an interrupt, but only if Debugger.enable was sent before the page
//     stopped. Off by default: it slows producer-workflow by about 2 to 4 s.
// CICADA_PAGE_STALL_REPORT=1 prints the report after passing tests too.
const fs = require('node:fs');
const { execFileSync } = require('node:child_process');

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
}

function pageState(page) {
  return { page, interval: 0, frame: 0, gaps: [], tasks: [], crashed: 0, closed: 0, console: new Map(), go: null, session: null, cdp: null, ready: null };
}

function onBeat(state, kind, detail) {
  const received = Date.now();
  if (kind === 'interval' || kind === 'frame') {
    const last = state[kind];
    if (last && received - last >= 1000) { state.gaps.push({ kind, end: received, gap: received - last }); if (state.gaps.length > 8) state.gaps.shift(); }
    state[kind] = received;
  } else if (kind === 'go') state.go = { ...detail, received };
  else if (detail && detail.duration >= 200) { state.tasks.push({ kind, received, ...detail }); if (state.tasks.length > 12) state.tasks.shift(); }
}

function listen(state) {
  const { page } = state;
  page.on('crash', () => { state.crashed = Date.now(); });
  page.on('close', () => { state.closed = Date.now(); });
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
async function pageReport(state, started, deadline) {
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
  lines.push(state.go ? `go at the last heartbeat (${now - state.go.received} ms ago): ${state.go.modules} Go modules wrapped, ${state.go.count} entries, ${state.go.timers} for Go timers, ${state.go.open} open` : 'go: no heartbeat from the entry probe');
  if (state.crashed || !isStalled(state)) return lines;
  // Order matters: sample CPU while the page is stuck, pause for a stack, then
  // terminate the running script to read the Go record.
  for (const [label, step] of [['renderer CPU', () => rendererCPU(state.page.context().browser())], ['stack', () => stack(state)], ['go', () => goSnapshot(state.session)]]) {
    const left = deadline - Date.now();
    if (left < 300) { lines.push(`${label}: skipped, the report's time budget is spent`); continue; }
    const value = await within(step(), left, `${label}: did not finish within the report's time budget`);
    lines.push(typeof value === 'string' ? value : `${label}: failed: ${value && value.error ? value.error.message : String(value)}`);
  }
  return lines;
}

function makeWatch(states, serverLog) {
  const started = Date.now();
  return {
    // stalled reports whether an open watched page has missed beats for ms.
    stalled: ms => states().some(state => isStalled(state, ms)),
    // report describes every open watched page (stalled ones first), within budgetMS.
    async report(reason, { budgetMS = 8000 } = {}) {
      const deadline = Date.now() + budgetMS;
      const open = states().filter(state => !state.closed).sort((a, b) => isStalled(b) - isStalled(a));
      const lines = [`--- page stall report (${reason && reason.message ? reason.message.split('\n')[0] : reason || 'requested'}) ---`];
      if (!open.length) lines.push('no open watched pages');
      for (const state of open) lines.push(...await pageReport(state, started, deadline));
      lines.push(`--- last 40 lines of the Studio server log ${serverLog || ''} ---`, tail(serverLog));
      return lines.join('\n');
    },
  };
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
  const table = processTable();
  const pids = pid ? [pid] : table.filter(p => p.ppid === process.pid && p.pgid === p.pid && /chrom/i.test(p.args)).map(p => p.pid);
  const killed = [];
  for (const target of pids) {
    try { process.kill(-target, 'SIGKILL'); killed.push(`process group ${target}`); continue; } catch {}
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
function teardown(t, browser, { watch = null, before = null, exitWhenDone = false, hookMS = 15000, reportMS = 8000, beforeMS = 2500, closeMS = 2500, exitAfterMS = hookMS + 5000 } = {}) {
  t.after(async () => {
    const started = Date.now();
    const passed = t.passed !== false;
    if (!passed) anyFailed = true;
    if (exitWhenDone) armExit(exitAfterMS);
    const watches = (Array.isArray(watch) ? watch : [watch]).filter(Boolean);
    // Decide before the report: its Go snapshot ends the stuck script, so the
    // page answers again for a moment and could freeze again under before().
    const stalled = watches.some(w => w.stalled());
    if (watches.length && (!passed || process.env.CICADA_PAGE_STALL_REPORT === '1')) {
      const budget = Math.max(0, reportMS - 250);
      const reports = Promise.all(watches.map(w => w.report(passed ? 'requested by CICADA_PAGE_STALL_REPORT' : 'the test did not pass', { budgetMS: budget })));
      const text = await within(reports, reportMS, null);
      console.error(Array.isArray(text) ? text.join('\n') : `page stall report did not finish within ${reportMS} ms`);
    }
    const outcome = await closeBrowser(browser, { before: stalled ? null : before, beforeMS, closeMS });
    if (!passed || outcome !== 'closed') console.error(`page-stall: teardown took ${Date.now() - started} ms of its ${hookMS} ms limit${stalled && before ? '; skipped the teardown step because a page stopped responding' : ''}; browser ${outcome}`);
  }, { timeout: hookMS });
}

module.exports = { watchContext, watchPage, teardown, closeBrowser, killBrowser, within, tail, redact };
