// Acceptance checks against the shipped page, CSP, Go tools and AudioWorklet.
// Chrome DevTools is used directly; no package installation is required.
'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {spawn} = require('node:child_process');
const {setTimeout: sleep} = require('node:timers/promises');
const repo = path.resolve(__dirname, '../..');
const soak = process.argv.includes('--soak');
const evidence = process.env.CICADA_DEMO_EVIDENCE_DIR || path.join(repo, 'build/demo-evidence');
fs.mkdirSync(evidence, {recursive: true});
assert(!fs.readFileSync(path.join(repo, 'build/demo/demo.wasm')).includes(Buffer.from(repo)), 'browser score tools must omit private build paths');

async function until(run, timeout = 20000) {
  const deadline = Date.now() + timeout;
  do { const value = await run(); if (value) return value; await sleep(100); } while (Date.now() < deadline);
  throw new Error('Browser condition timed out');
}

(async () => {
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'cicada-demo-'));
  const server = spawn(path.join(repo, 'build/cicada-demo'), ['-listen', '127.0.0.1:0'], {cwd: repo, stdio: ['ignore', 'pipe', 'pipe']});
  let chrome, socket, chromeLog;
  const pending = new Map();
  try {
    const base = await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('Demo server start timed out')), 15000);
      server.stdout.on('data', data => { const match = data.toString().match(/http:\/\/127\.0\.0\.1:\d+/); if (match) { clearTimeout(timer); resolve(match[0]); } });
      server.once('exit', code => { clearTimeout(timer); reject(new Error(`Demo server exited ${code}`)); });
      server.stderr.on('data', data => process.stderr.write(data));
    });
    chromeLog = fs.openSync(path.join(evidence, 'chrome.log'), 'w');
    chrome = spawn(process.env.CHROME_BIN || 'google-chrome', ['--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--no-first-run', '--no-default-browser-check', '--mute-audio', '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], {env: {...process.env, PULSE_SERVER: 'unix:/nonexistent'}, stdio: ['ignore', chromeLog, chromeLog]});
    let launchError;
    chrome.on('error', error => { launchError = error; });
    const portFile = path.join(profile, 'DevToolsActivePort');
    await until(() => { if (launchError) throw launchError; return fs.existsSync(portFile); });
    const debugPort = fs.readFileSync(portFile, 'utf8').split('\n')[0];
    const targets = await (await fetch(`http://127.0.0.1:${debugPort}/json/list`)).json();
    const target = targets.find(item => item.type === 'page');
    socket = new WebSocket(target.webSocketDebuggerUrl);
    await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
    let nextID = 0;
    const errors = [], requests = [];
    socket.onmessage = event => {
      const data = JSON.parse(event.data);
      if (data.id) {
        const waiter = pending.get(data.id);
        if (waiter) { pending.delete(data.id); clearTimeout(waiter.timer); data.error ? waiter.reject(new Error(JSON.stringify(data.error))) : waiter.resolve(data.result); }
      } else if (data.method === 'Runtime.exceptionThrown') errors.push(data.params.exceptionDetails.text + ': ' + (data.params.exceptionDetails.exception?.description || ''));
      else if (data.method === 'Log.entryAdded' && data.params.entry.level === 'error') errors.push(data.params.entry.text);
      else if (data.method === 'Network.requestWillBeSent') requests.push(data.params.request.url);
    };
    const cdp = (method, params = {}) => new Promise((resolve, reject) => {
      const id = ++nextID;
      const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out`)); }, 20000);
      pending.set(id, {resolve, reject, timer}); socket.send(JSON.stringify({id, method, params}));
    });
    const evaluate = async expression => {
      const reply = await cdp('Runtime.evaluate', {expression, returnByValue: true, awaitPromise: true});
      if (reply.exceptionDetails) throw new Error(reply.exceptionDetails.exception?.description || reply.exceptionDetails.text);
      return reply.result.value;
    };
    const wait = expression => until(() => evaluate(expression));
    const click = async selector => {
      await evaluate(`document.querySelector(${JSON.stringify(selector)}).scrollIntoView({block:'center'})`);
      const rect = await evaluate(`(()=>{const r=document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
      await cdp('Input.dispatchMouseEvent', {type: 'mousePressed', ...rect, button: 'left', clickCount: 1});
      await cdp('Input.dispatchMouseEvent', {type: 'mouseReleased', ...rect, button: 'left', clickCount: 1});
    };
    const viewport = (width, height) => cdp('Emulation.setDeviceMetricsOverride', {width, height, deviceScaleFactor: 1, mobile: false});
    const screenshot = async name => {
      const {data} = await cdp('Page.captureScreenshot', {format: 'png', captureBeyondViewport: false});
      fs.writeFileSync(path.join(evidence, name), Buffer.from(data, 'base64'));
    };
    const edit = async source => {
      await click('#source');
      await cdp('Input.dispatchKeyEvent', {type: 'keyDown', key: 'a', code: 'KeyA', modifiers: 2, windowsVirtualKeyCode: 65});
      await cdp('Input.dispatchKeyEvent', {type: 'keyUp', key: 'a', code: 'KeyA', modifiers: 2, windowsVirtualKeyCode: 65});
      await cdp('Input.insertText', {text: source});
      await click('#apply');
    };
    const energy = () => evaluate(`(()=>{const a=cicadaDemoAudio.analyser,s=new Float32Array(a.fftSize);a.getFloatTimeDomainData(s);return {energy:s.reduce((n,x)=>n+x*x,0),finite:s.every(Number.isFinite)}})()`);
    const play = async () => {
      await wait(`!document.getElementById('play').disabled`); await click('#play');
      await wait('cicadaDemoState.playing && cicadaDemoState.metrics?.callbackSamples>0');
      await until(async () => (await energy()).energy > 1e-7);
      assert((await energy()).finite, 'finite audible PCM');
    };
    await Promise.all([cdp('Page.enable'), cdp('Runtime.enable'), cdp('Network.enable'), cdp('Log.enable')]);
    await viewport(1440, 1000);
    await cdp('Page.navigate', {url: base});
    await wait(`window.cicadaDemo && !document.getElementById('play').disabled`);
    await screenshot('demo-ready-1440.png');
    await play();
    await wait('cicadaDemoState.messageCount>10');
    await evaluate('window.scrollTo(0,0)');
    await screenshot('demo-playing-1440.png');
    const metricsVisible = await evaluate(`(()=>{const r=document.getElementById('metrics-title').getBoundingClientRect();return r.top>=0&&r.bottom<=innerHeight&&document.getElementById('messages').textContent!=='0'})()`);
    assert(metricsVisible, 'live metrics visible in initial desktop viewport');
    await viewport(390, 900); await evaluate('window.scrollTo(0,0)');
    assert(await evaluate('document.documentElement.scrollWidth<=innerWidth'), 'no mobile horizontal overflow');
    await screenshot('demo-playing-390.png');
    await click('#stop'); await wait(`!cicadaDemoState.playing && !document.getElementById('play').disabled`);
    await sleep(200); assert.equal((await energy()).energy, 0, 'Stop silences actual output');
    await play();
    await evaluate(`window.dispatchEvent(new Event('blur'))`);
    await wait(`!cicadaDemoState.playing && !document.getElementById('play').disabled`);
    await sleep(200); assert.equal((await energy()).energy, 0, 'focus loss silences actual output');
    await play();
    await evaluate('cicadaDemoAudio.context.suspend()');
    await wait(`!document.getElementById('play').disabled`);
    await play();
    assert.equal(await evaluate('cicadaDemoAudio.context.state'), 'running', 'Play resumes suspended audio before the fresh image waits for callbacks');
    await click('#stop'); await wait(`!document.getElementById('play').disabled`);
    const original = await evaluate('cicadaDemo.snapshot().source');
    const revision = await evaluate('cicadaDemo.snapshot().revision');
    await edit('invalid score');
    assert.equal(await evaluate('cicadaDemo.snapshot().revision'), revision, 'invalid score cannot commit');
    assert(await evaluate(`document.getElementById('edit-error').textContent.length>0`));
    await screenshot('demo-invalid-390.png');
    await edit(original.replace('tempo 112', 'tempo 116'));
    await wait(`!document.getElementById('play').disabled && cicadaDemo.snapshot().revision!==${JSON.stringify(revision)}`);
    await click('#undo'); await wait(`!document.getElementById('play').disabled`);
    assert.equal(await evaluate('cicadaDemo.snapshot().source'), original);
    await click('#redo'); await wait(`!document.getElementById('play').disabled`);
    assert.match(await evaluate('cicadaDemo.snapshot().source'), /tempo 116/);
    assert.equal(await evaluate(`document.getElementById('preset').value`), 'custom');
    await click('#reset'); await wait(`!document.getElementById('play').disabled`);
    assert.equal(await evaluate('cicadaDemo.snapshot().source'), original);
    assert.equal(await evaluate(`document.getElementById('preset').value`), 'ensemble');
    assert(await evaluate(`document.getElementById('undo').disabled && document.getElementById('redo').disabled`));
    // Every selector option is played through its actual image and kernel.
    for (const id of await evaluate('cicadaDemo.presets().map(p=>p.id)')) {
      await click('#preset');
      await cdp('Input.dispatchKeyEvent', {type: 'keyDown', key: 'Home', code: 'Home', windowsVirtualKeyCode: 36});
      const ids = await evaluate('cicadaDemo.presets().map(p=>p.id)');
      for (let i = 0; i < ids.indexOf(id); i++) await cdp('Input.dispatchKeyEvent', {type: 'keyDown', key: 'ArrowDown', code: 'ArrowDown', windowsVirtualKeyCode: 40});
      await cdp('Input.dispatchKeyEvent', {type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13});
      await wait(`document.getElementById('preset').value===${JSON.stringify(id)} && !document.getElementById('play').disabled`);
      await play();
      await click('#stop'); await wait(`!document.getElementById('play').disabled`);
    }
    for (const route of ['/api/export', '/api/source', '/api/kernel-image', '/agent/execute', '/mcp', '/_gosx/rpc/export']) {
      for (const method of ['GET', 'POST']) assert.equal((await fetch(base + route, {method})).status, 403, `${method} ${route}`);
    }
    assert(!requests.some(url => new URL(url).pathname.startsWith('/api/')), 'playback and edits never use native APIs');
    assert.deepEqual(errors, []); assert.deepEqual(await evaluate('cicadaDemoState.errors'), []);
    await click('#reset'); await wait(`!document.getElementById('play').disabled`);
    await viewport(1440, 1000); await play(); await evaluate('window.scrollTo(0,0)');
    const version = await cdp('Browser.getVersion');
    const info = {browser: version.product, revision: (await fetch(base)).headers.get('X-Cicada-Revision'), presets: await evaluate('cicadaDemo.presets().length'), realAudioWorklet: true, acousticListening: false};
    if (soak) {
      console.log('Public browser demo: warming up for 60 seconds before a 30-minute soak.');
      await sleep(60000);
      const warmup = await evaluate('({...cicadaDemoState.metrics, playhead:cicadaDemoState.playhead, messages:cicadaDemoState.messageCount})');
      assert(warmup.callbackSamples > 1000, 'warmup renders audio callbacks');
      const started = Date.now(); let peakMemory = warmup.memoryBytes, samples = 0;
      while (Date.now() - started < 1800000) {
        await sleep(Math.min(10000, 1800000 - (Date.now() - started)));
        const current = await evaluate('({...cicadaDemoState.metrics, playing:cicadaDemoState.playing, faults:cicadaDemoState.faults})');
        peakMemory = Math.max(peakMemory, current.memoryBytes); samples++;
        assert(current.playing, 'public demo transport continues playing'); assert.equal(current.faults, 0);
        if (samples % 6 === 0) console.log(`Public browser demo soak: ${Math.round((Date.now()-started)/1000)}s, underruns=${current.underruns-warmup.underruns}, callbacks=${current.callbackSamples-warmup.callbackSamples}`);
      }
      const final = await evaluate('({...cicadaDemoState.metrics, playhead:cicadaDemoState.playhead, messages:cicadaDemoState.messageCount, errors:cicadaDemoState.errors, faults:cicadaDemoState.faults})');
      const report = {...info, requestedDurationSeconds: 1800, actualDurationSeconds: (Date.now()-started)/1000, warmupSeconds: 60, underruns: final.underruns-warmup.underruns, warmupUnderruns: warmup.underruns, faults: final.faults, callbackSamples: final.callbackSamples-warmup.callbackSamples, callbackP99Ms: final.callbackP99Ms, messagesDrained: final.messages-warmup.messages, memoryAfterWarmupBytes: warmup.memoryBytes, memoryPeakBytes: peakMemory, memoryFinalBytes: final.memoryBytes, quantumMs: final.quantumMs, durationLimitMs: final.durationLimitMs, gapLimitMs: final.gapLimitMs, playheadAdvanced: final.playhead>warmup.playhead};
      report.gatePass = report.underruns===0 && report.faults===0 && peakMemory===warmup.memoryBytes && report.playheadAdvanced && report.callbackSamples>1000 && report.messagesDrained>0 && final.errors.length===0 && errors.length===0;
      fs.writeFileSync(process.env.CICADA_DEMO_REPORT || path.join(repo, 'build/demo-soak-report.json'), JSON.stringify(report, null, 2)+'\n');
      console.log(JSON.stringify(report)); assert(report.gatePass, 'public demo 30-minute zero-underrun gate');
    }
    assert.deepEqual(errors, []);
    await screenshot('demo-final-1440.png');
    fs.writeFileSync(path.join(evidence, 'build.json'), JSON.stringify(info, null, 2)+'\n');
    console.log('PASS public browser demo: real AudioWorklet PCM and metrics, all instrument sketches, Stop/restart/focus loss/suspend/resume, invalid edit, undo/redo/reset, mobile layout, no native or export APIs.');
  } finally {
    for (const waiter of pending.values()) { clearTimeout(waiter.timer); waiter.reject(new Error('Browser closed')); }
    if (socket) socket.close();
    if (chrome) { chrome.kill('SIGTERM'); await Promise.race([new Promise(resolve=>chrome.once('exit', resolve)), sleep(5000)]); }
    server.kill('SIGTERM');
    if (chromeLog !== undefined) fs.closeSync(chromeLog);
    fs.rmSync(profile, {recursive: true, force: true});
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
