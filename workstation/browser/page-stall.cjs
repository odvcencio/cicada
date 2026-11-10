'use strict';
// page-stall.cjs makes a browser step that times out explain itself, and makes
// sure a failed suite cannot keep its Node process alive.
//
// A Playwright click that stops after "scrolling into view if needed" on a
// visible, stable element usually means the page's main thread stopped. A
// frozen page cannot answer page.evaluate, so watchPage records what Node needs
// before anything goes wrong:
//
//   - a heartbeat from setInterval and from requestAnimationFrame, sent to Node
//     through a binding, so Node knows when the main thread last ran;
//   - long tasks and long animation frames, with the scripts that ran in them;
//   - renderer crashes, and console warnings and errors (counted by text);
//   - every entry into a Go/WebAssembly module (wasm_exec.js's _resume), so
//     report() can say whether the main thread is stuck inside Go, in
//     JavaScript, or outside script, and which module and kind of entry;
//   - with { stack: true } (or CICADA_PAGE_STALL_STACK=1), the Debugger domain
//     of a DevTools session, so report() can pause the page and print the
//     stack the main thread is running. V8 serves Debugger.pause through an
//     interrupt, so it works while script is stuck in a loop, but only if
//     Debugger.enable was sent before the page stopped: Debugger.enable itself
//     waits for the main thread. It is off by default because it slows the
//     page (about 2 to 4 s on producer-workflow).
//
// Every capture in report() and closeBrowser() has its own time limit. A
// report on a stalled page takes about 2 s, and at most 12 s.
//
//   const stall = require('./page-stall.cjs');
//   const watch = await stall.watchPage(page, { serverLog: process.env.CICADA_STUDIO_STARTUP_LOG });
//   let finished = false;
//   t.after(async () => {
//     if (!finished) console.error(await stall.within(watch.report('the test did not finish'), 12000, 'no report'));
//     await stall.closeBrowser(browser, { before: watch.stalled() ? null : () => context.storageState({ path }), exitAfterMS: 20000 });
//   }, { timeout: 15000 });
//   ...steps...
//   finished = true;
const fs = require('node:fs');

// within resolves to the promise's value, or to fallback after ms. Its timer is
// unref'd, so a capture that never settles cannot keep the process alive.
function within(promise, ms, fallback) {
  let timer;
  const limit = new Promise(resolve => { timer = setTimeout(() => resolve(fallback), ms); timer.unref(); });
  return Promise.race([Promise.resolve(promise).catch(error => ({ error })), limit]).finally(() => clearTimeout(timer));
}

function tail(file, lines = 40, bytes = 64 << 10) {
  if (!file) return '(no server log path given)';
  try {
    const fd = fs.openSync(file, 'r');
    try {
      const size = fs.fstatSync(fd).size, start = Math.max(0, size - bytes), buffer = Buffer.alloc(size - start);
      fs.readSync(fd, buffer, 0, buffer.length, start);
      return buffer.toString('utf8').split('\n').slice(-lines).join('\n');
    } finally { fs.closeSync(fd); }
  } catch (error) { return `(cannot read ${file}: ${error.message})`; }
}

// The page half. It must not depend on anything the page loads.
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
  // JavaScript enters a Go/WebAssembly module only through Go.prototype._resume
  // (wasm_exec.js): for a call to a Go function ("call") or for one of Go's own
  // timers ("timer"). Every page script that defines Go is wrapped. A resume
  // that never returns stays in open, because a terminated script runs no
  // finally block, so a report can tell "inside Go" from "in JavaScript".
  const go = globalThis.__cicadaGoResumes = { open: [], recent: [], count: 0, timers: 0, modules: 0 };
  const wrap = Go => {
    const proto = Go && Go.prototype;
    if (!proto || typeof proto._resume !== 'function' || proto._resume.__cicadaStall) return;
    const script = document.currentScript && document.currentScript.src || '';
    const module = `${++go.modules}:${script.split('/').pop().split('.')[0] || 'inline'}`;
    const resume = proto._resume;
    proto._resume = Object.assign(function () {
      const entry = { module, kind: this._pendingEvent ? 'call' : 'timer', at: Date.now(), end: 0 };
      go.count++;
      if (entry.kind === 'timer') go.timers++;
      go.open.push(entry);
      try { return resume.apply(this, arguments); } finally {
        entry.end = Date.now();
        go.open.pop();
        go.recent.push(entry);
        if (go.recent.length > 16) go.recent.shift();
      }
    }, { __cicadaStall: true });
  };
  let current = globalThis.Go;
  wrap(current);
  try { Object.defineProperty(globalThis, 'Go', { configurable: true, enumerable: true, get: () => current, set: value => { wrap(value); current = value; } }); } catch {}
  setInterval(() => send('go', Date.now(), { count: go.count, timers: go.timers, open: go.open.length, modules: go.modules }), 250);
}

// goSnapshot reads the resume record from a page whose main thread is stuck.
// Runtime.evaluate waits for the main thread, so it is queued first; then
// Runtime.terminateExecution, which V8 serves through an interrupt, ends the
// running script and lets the queued read run. This leaves the page's Go
// programs unusable, so it runs only in a failure report.
async function goSnapshot(cdp) {
  const read = cdp.send('Runtime.evaluate', { returnByValue: true, expression: '(() => { const g = globalThis.__cicadaGoResumes; return g ? { open: g.open, recent: g.recent.slice(-6), count: g.count, timers: g.timers, now: Date.now() } : null; })()' });
  read.catch(() => {});
  const terminated = await within(cdp.send('Runtime.terminateExecution'), 1500, 'timeout');
  const value = await within(read, 2500, 'timeout');
  if (value === 'timeout') return `go: the page did not answer within 2500 ms after Runtime.terminateExecution (${terminated === 'timeout' ? 'which also did not answer' : 'which answered'}): the main thread is blocked outside JavaScript, or a flood of console messages (counted above) is delaying DevTools`;
  if (value.error || !value.result || !value.result.value) return `go: no resume record (${value.error ? value.error.message : 'probe missing'})`;
  const g = value.result.value, ago = at => `${g.now - at} ms ago`;
  const lines = [`go: ${g.count} resumes since load (${g.timers} for Go timers); open now: ${g.open.length ? '' : 'none (the main thread was not inside Go)'}`];
  for (const entry of g.open) lines.push(`  OPEN ${entry.module} ${entry.kind} resume entered ${ago(entry.at)} and never returned`);
  lines.push('  last finished resumes: ' + g.recent.map(entry => `${entry.module} ${entry.kind} ${entry.end - entry.at} ms (ended ${ago(entry.end)})`).join('; '));
  return lines.join('\n');
}

// rendererCPU samples Chrome's renderer processes twice, 1 s apart. A frozen
// renderer near 100% is computing (a script loop or layout); near 0% it is
// waiting on something outside itself.
async function rendererCPU(browser) {
  if (!browser || typeof browser.newBrowserCDPSession !== 'function') return 'renderer CPU: no browser session';
  const session = await within(browser.newBrowserCDPSession(), 2000, null);
  if (!session || session.error) return 'renderer CPU: no browser session';
  const sample = async () => { const info = await within(session.send('SystemInfo.getProcessInfo'), 2000, null); return info && info.processInfo ? new Map(info.processInfo.filter(p => p.type === 'renderer').map(p => [p.id, p.cpuTime])) : null; };
  try {
    const first = await sample();
    await new Promise(resolve => setTimeout(resolve, 1000).unref());
    const second = await sample();
    if (!first || !second) return 'renderer CPU: SystemInfo.getProcessInfo did not answer';
    return 'renderer CPU over 1 s: ' + ([...second].map(([pid, cpu]) => `pid ${pid} ${Math.round(100 * (cpu - (first.get(pid) || 0)))}%`).join(', ') || 'no renderer processes');
  } finally { await within(session.detach(), 1000, null); }
}

// watchPage must run before the page's first navigation.
async function watchPage(page, { serverLog, stack: wantStack = process.env.CICADA_PAGE_STALL_STACK === '1' } = {}) {
  const started = Date.now();
  const state = { interval: 0, frame: 0, gaps: [], tasks: [], crashed: 0, console: new Map() };
  const beat = (kind, at) => {
    if (state[kind]) {
      const gap = at - state[kind];
      if (gap >= 1000) { state.gaps.push({ kind, end: at, gap }); if (state.gaps.length > 8) state.gaps.shift(); }
    }
    state[kind] = at;
  };
  page.on('crash', () => { state.crashed = Date.now(); });
  // DevTools delivers console messages while script runs, so a page stuck in a
  // loop that logs (Go's wasm_exec.js warns "missed timeout event" on every turn
  // of its retry loop) still shows here. Count by text and keep the newest.
  page.on('console', message => {
    if (message.type() !== 'warning' && message.type() !== 'error') return;
    const text = `${message.type()}: ${message.text().slice(0, 200)}`;
    const seen = state.console.get(text) || { count: 0, last: 0 };
    state.console.delete(text);
    state.console.set(text, { count: seen.count + 1, last: Date.now() });
    if (state.console.size > 10) state.console.delete(state.console.keys().next().value);
  });
  await page.exposeBinding('__cicadaStallBeat', (_source, kind, at, detail) => {
    const received = Date.now();
    if (kind === 'interval' || kind === 'frame') beat(kind, received);
    else if (kind === 'go') state.go = { ...detail, received };
    else if (detail && detail.duration >= 200) { state.tasks.push({ kind, received, ...detail }); if (state.tasks.length > 12) state.tasks.shift(); }
  });
  await page.addInitScript(pageProbe);
  // One DevTools session, opened now: attaching later can wait on the page.
  let session = await within(page.context().newCDPSession(page), 5000, null);
  if (session && session.error) session = null;
  let cdp = null;
  if (wantStack && session) {
    // Skip debugger statements and breakpoints until report() asks for a pause.
    const enabled = await within(session.send('Debugger.enable').then(() => session.send('Debugger.setSkipAllPauses', { skip: true })), 5000, 'timeout');
    if (enabled !== 'timeout' && !(enabled && enabled.error)) cdp = session;
  }
  // stalled reports whether the page's main thread has missed beats for ms.
  const stalled = (ms = 2000) => !!state.crashed || (state.interval > 0 && Date.now() - Math.max(state.interval, state.frame) >= ms);

  async function stack() {
    if (!cdp) return 'stack: not captured (watchPage({ stack: true }) or CICADA_PAGE_STALL_STACK=1 enables it)';
    let paused;
    const pausedEvent = new Promise(resolve => { paused = resolve; });
    const onPaused = event => paused(event);
    cdp.on('Debugger.paused', onPaused);
    try {
      await within(cdp.send('Debugger.setSkipAllPauses', { skip: false }), 1000, null);
      await within(cdp.send('Debugger.pause'), 1000, null);
      const event = await within(pausedEvent, 2500, null);
      if (!event || event.error) return 'stack: the main thread did not pause within 2500 ms (blocked outside JavaScript and WebAssembly, or idle)';
      const frames = event.callFrames.slice(0, 40).map(frame => {
        const where = frame.url || `script ${frame.location.scriptId}`;
        return `  ${frame.functionName || '(anonymous)'}  ${where}:${frame.location.lineNumber + 1}:${frame.location.columnNumber + 1}`;
      });
      return `stack (paused: ${event.reason}), ${event.callFrames.length} frames, innermost first:\n${frames.join('\n')}`;
    } finally {
      cdp.off('Debugger.paused', onPaused);
      await within(cdp.send('Debugger.setSkipAllPauses', { skip: true }), 1000, null);
      await within(cdp.send('Debugger.resume'), 1000, null);
    }
  }

  // report(reason) describes the page's main thread at the moment of a failure.
  async function report(reason) {
    const now = Date.now(), at = ms => ms ? `${now - ms} ms ago` : 'never';
    const lines = [`--- page stall report (${reason && reason.message ? reason.message.split('\n')[0] : reason || 'requested'}) ---`];
    lines.push(`watched for ${now - started} ms; last setInterval beat ${at(state.interval)}; last animation-frame beat ${at(state.frame)}`);
    lines.push(state.crashed ? `renderer CRASHED ${now - state.crashed} ms ago` : 'renderer crash: none reported');
    lines.push(`earlier main-thread gaps of 1 s or more: ${state.gaps.length ? state.gaps.map(gap => `${gap.kind} ${gap.gap} ms ending ${now - gap.end} ms ago`).join('; ') : 'none'}`);
    lines.push(`long tasks and frames of 200 ms or more (newest last): ${state.tasks.length ? '' : 'none'}`);
    for (const task of state.tasks) {
      const scripts = (task.scripts || []).map(script => `${script.duration} ms ${script.invoker || ''} ${script.source}`.trim()).join(' | ');
      lines.push(`  ${task.kind} ${task.duration} ms, started ${now - task.start} ms ago${scripts ? ': ' + scripts : ''}`);
    }
    lines.push(`console warnings and errors (count, newest last): ${state.console.size ? '' : 'none'}`);
    for (const [text, seen] of state.console) lines.push(`  ${seen.count} x ${text} (last ${now - seen.last} ms ago)`);
    lines.push(state.go ? `go at the last heartbeat (${now - state.go.received} ms ago): ${state.go.modules} Go modules wrapped, ${state.go.count} resumes, ${state.go.timers} for Go timers, ${state.go.open} open` : 'go: no heartbeat from the resume probe');
    if (!state.crashed && stalled()) {
      lines.push(await within(rendererCPU(page.context().browser()), 2500, 'renderer CPU: sample did not finish within 2500 ms'));
      lines.push(await within(stack(), 5000, 'stack: capture did not finish within 5000 ms'));
      lines.push(session ? await within(goSnapshot(session), 4500, 'go: snapshot did not finish within 4500 ms') : 'go: no DevTools session');
    }
    lines.push(`--- last 40 lines of the Studio server log ${serverLog || ''} ---`, tail(serverLog));
    return lines.join('\n');
  }

  return { report, stalled, state };
}

// closeBrowser ends the browser even when a page is frozen.
//
// An open browser is what keeps a failed suite's Node process alive: its
// connection and child process hold the event loop. The old teardown awaited
// context.storageState() first, which waits on every page and never settles
// on a frozen one, so browser.close() never ran.
//
//   - before (optional) runs first with its own limit, for work such as saving
//     storage state. Skip it when watch.stalled() is true.
//   - browser.close() works on a frozen page (the browser process answers), but
//     it gets a limit too; past it, every Chrome process this Node process
//     started is killed.
//   - exitAfterMS (off by default) arms an unref'd timer, so it never keeps a
//     healthy process alive, that exits the process if something still holds
//     the event loop that long after teardown began. Use it only in a file's
//     last teardown: it ends the process, so later tests in the file would not run.
async function closeBrowser(browser, { before, beforeMS = 4000, closeMS = 6000, exitAfterMS = 0 } = {}) {
  if (exitAfterMS > 0) {
    setTimeout(() => {
      console.error(`page-stall: the process was still alive ${exitAfterMS} ms after teardown began; open handles: ${JSON.stringify(process.getActiveResourcesInfo())}. Exiting.`);
      process.exit(process.exitCode || 1);
    }, exitAfterMS).unref();
  }
  if (before) {
    const result = await within(before(), beforeMS, 'timeout');
    if (result === 'timeout') console.error(`page-stall: teardown step did not finish within ${beforeMS} ms; closing the browser anyway`);
    else if (result && result.error) console.error(`page-stall: teardown step failed: ${result.error.message}`);
  }
  const closed = await within(browser.close(), closeMS, 'timeout');
  if (closed === 'timeout') {
    const pids = chromeChildren();
    console.error(`page-stall: browser.close() did not finish within ${closeMS} ms; killing Chrome ${pids.join(' ') || '(none found)'}`);
    for (const pid of pids) { try { process.kill(pid, 'SIGKILL'); } catch {} }
  }
}

// chromeChildren lists the Chrome processes this Node process started (Linux
// /proc; Playwright does not expose the browser's process ID).
function chromeChildren() {
  try {
    const children = [];
    for (const task of fs.readdirSync(`/proc/${process.pid}/task`)) {
      for (const pid of fs.readFileSync(`/proc/${process.pid}/task/${task}/children`, 'utf8').split(' ').filter(Boolean)) children.push(Number(pid));
    }
    return children.filter(pid => { try { return /chrom/i.test(fs.readlinkSync(`/proc/${pid}/exe`)); } catch { return false; } });
  } catch { return []; }
}

module.exports = { watchPage, closeBrowser, within, tail };
