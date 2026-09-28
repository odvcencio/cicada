const assert = require('node:assert/strict');
const {spawn} = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {setTimeout: delay} = require('node:timers/promises');

const repoRoot = path.resolve(__dirname, '../..');
const scoreSource = path.join(repoRoot, 'examples', 'fx-bus.cicada');
const binary = path.join(repoRoot, 'build', 'cicada');
const chromeBinary = process.env.CHROME_BIN || '/usr/bin/google-chrome';
const evidenceDirectory = process.env.CICADA_LIVE_EVIDENCE || '/home/draco/.local/state/nightwatch/reports/cicada-ws/evidence/cicada-ws-live';
const tempDirectory = fs.mkdtempSync(path.join(os.tmpdir(), 'cicada-live-midi-'));
const scorePath = path.join(tempDirectory, 'fx-bus.cicada');
const profileDirectory = path.join(tempDirectory, 'chrome-profile');
const originalScore = fs.readFileSync(scoreSource, 'utf8');
const children = [];
const childLogs = new Map();

fs.copyFileSync(scoreSource, scorePath);
fs.mkdirSync(evidenceDirectory, {recursive: true});

function launch(command, args, options = {}) {
  const {env: childEnv = {}, ...spawnOptions} = options;
  const child = spawn(command, args, {
    cwd: repoRoot,
    stdio: ['ignore', 'pipe', 'pipe'],
    env: {...process.env, PULSE_SERVER: 'unix:/nonexistent', ...childEnv},
    ...spawnOptions
  });
  children.push(child);
  childLogs.set(child, {stdout: '', stderr: ''});
  child.stdout?.on('data', chunk => { const logs = childLogs.get(child); logs.stdout = (logs.stdout + chunk).slice(-8000); });
  child.stderr?.on('data', chunk => { const logs = childLogs.get(child); logs.stderr = (logs.stderr + chunk).slice(-8000); });
  return child;
}

async function waitFor(condition, description, timeout = 15000) {
  const started = Date.now();
  while (Date.now() - started < timeout) {
    if (await condition()) return;
    await delay(100);
  }
  throw new Error(`timed out waiting for ${description}`);
}

async function waitForHTTP(url, server) {
  await waitFor(async () => {
    if (server.exitCode !== null) throw new Error(`Studio exited early: ${childLogs.get(server).stderr}`);
    try { return (await fetch(url)).ok; } catch { return false; }
  }, `Studio at ${url}`);
}

async function json(url, options) {
  const response = await fetch(url, options);
  if (!response.ok) throw new Error(`${url} returned ${response.status}`);
  return response.json();
}

function makeCDP(socket) {
  let nextID = 0;
  const pending = new Map();
  socket.addEventListener('message', event => {
    const response = JSON.parse(event.data);
    if (!response.id || !pending.has(response.id)) return;
    const {resolve, reject} = pending.get(response.id);
    pending.delete(response.id);
    if (response.error) reject(new Error(response.error.message));
    else resolve(response.result || {});
  });
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++nextID;
    pending.set(id, {resolve, reject});
    socket.send(JSON.stringify({id, method, params}));
  });
  const evaluate = async expression => {
    const response = await send('Runtime.evaluate', {expression, returnByValue: true, awaitPromise: true});
    if (response.exceptionDetails) throw new Error(response.exceptionDetails.exception?.description || response.exceptionDetails.text);
    return response.result?.value;
  };
  const screenshot = async filename => {
    const result = await send('Page.captureScreenshot', {format: 'png', captureBeyondViewport: false});
    fs.writeFileSync(path.join(evidenceDirectory, filename), Buffer.from(result.data, 'base64'));
  };
  return {send, evaluate, screenshot};
}

async function openPage() {
  await waitFor(async () => {
    try { await fetch('http://127.0.0.1:8162/json/version'); return true; } catch { return false; }
  }, 'Chrome remote debugging endpoint', 45000);
  const targets = await json('http://127.0.0.1:8162/json/list');
  const page = targets.find(target => target.type === 'page');
  if (!page) throw new Error('Chrome did not create a page target');
  const socket = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, {once: true});
    socket.addEventListener('error', reject, {once: true});
  });
  const cdp = makeCDP(socket);
  await cdp.send('Page.enable');
  await cdp.send('Runtime.enable');
  const midiFixture = `(() => {
    const input = {id:'virtual-midi-1',name:'Virtual MIDI',manufacturer:'Cicada test',state:'connected',connection:'open',onmidimessage:null};
    const access = {inputs:new Map([[input.id,input]]),outputs:new Map(),sysexEnabled:false,onstatechange:null};
    Object.defineProperty(navigator,'requestMIDIAccess',{configurable:true,value:async options => {window.__midiOptions=options;window.__midiAccess=access;return access;}});
    window.__emitMIDI=(status,a,b=0) => {if(input.onmidimessage)input.onmidimessage({data:Uint8Array.from([status,a,b]),timeStamp:performance.now(),target:input});};
  })();`;
  await cdp.send('Page.addScriptToEvaluateOnNewDocument', {source: midiFixture});
  await cdp.send('Page.navigate', {url: 'http://127.0.0.1:8161/'});
  await waitFor(async () => cdp.evaluate('!!window.cicadaMidi && !!window.cicadaAudio && !!document.querySelector(".live-scene-pad")'), 'Studio page scripts and launch grid');
  return {cdp, socket};
}

async function main() {
  let cdp;
  let socket;
  try {
    if (!fs.existsSync(binary)) throw new Error(`missing Studio binary: ${binary}`);
    if (!fs.existsSync(chromeBinary)) throw new Error(`missing headless Chrome: ${chromeBinary}`);

    const studio = launch(binary, ['studio', scorePath, '--listen', '127.0.0.1:8161', '--audio', 'null'], {env: {PULSE_SERVER: 'unix:/nonexistent'}});
    await waitForHTTP('http://127.0.0.1:8161/', studio);
    launch(chromeBinary, [
      '--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--mute-audio',
      '--remote-debugging-port=8162', '--remote-allow-origins=*', `--user-data-dir=${profileDirectory}`, 'about:blank'
    ], {env: {PULSE_SERVER: 'unix:/nonexistent'}});
    ({cdp, socket} = await openPage());

    await cdp.send('Emulation.setDeviceMetricsOverride', {width: 1440, height: 960, deviceScaleFactor: 1, mobile: false});
    await cdp.evaluate('document.querySelector("#live-toggle").click()');
    const desktopLayout = await cdp.evaluate(`(() => ({
      padHeight: document.querySelector('.live-scene-pad').getBoundingClientRect().height,
      arrangementHidden: getComputedStyle(document.querySelector('#arrangement')).display === 'none',
      patternsHidden: getComputedStyle(document.querySelector('#patterns')).display === 'none',
      sourceHeight: document.querySelector('.source-pane').getBoundingClientRect().height,
      masterVisible: document.querySelector('.master-meter').getBoundingClientRect().height > 0
    }))()`);
    assert.ok(desktopLayout.padHeight >= 64, `desktop launch pad is ${desktopLayout.padHeight}px tall`);
    assert.ok(desktopLayout.arrangementHidden && desktopLayout.patternsHidden, 'Live mode should hide arrangement and pattern grids');
    assert.ok(desktopLayout.sourceHeight <= 50, `Live source pane is ${desktopLayout.sourceHeight}px tall`);
    assert.ok(desktopLayout.masterVisible, 'Live mode should keep the master meter visible');
    await cdp.evaluate('document.querySelector("#transport-button").click()');
    await waitFor(async () => cdp.evaluate('fetch("/api/transport").then(response=>response.json()).then(state=>state.playing)'), 'null-device playback');

    const scene = await cdp.evaluate('document.querySelector(".live-scene-pad")?.dataset.scene || ""');
    assert.equal(scene, 'main', 'example score should expose its main scene');
    await cdp.evaluate('document.querySelector(".live-scene-pad").click()');
    await waitFor(async () => cdp.evaluate('fetch("/api/transport").then(response=>response.json()).then(state=>state.pendingScene === "main")'), 'queued scene launch');
    assert.match(await cdp.evaluate('document.querySelector(".live-scene-pad").getAttribute("aria-label")'), /queued with \d+ bar countdown/);
    await cdp.screenshot('live-1440-queued-launch.png');

    await cdp.send('Emulation.setDeviceMetricsOverride', {width: 390, height: 844, deviceScaleFactor: 1, mobile: true});
    await delay(300);
    const mobileLayout = await cdp.evaluate(`(() => ({
      padHeight: document.querySelector('.live-scene-pad').getBoundingClientRect().height
    }))()`);
    assert.ok(mobileLayout.padHeight >= 56, `390px launch pad is ${mobileLayout.padHeight}px tall`);
    assert.equal(await cdp.evaluate('fetch("/api/transport").then(response=>response.json()).then(state=>state.playing)'), true, '390px Live mode should remain in playback');
    await cdp.screenshot('live-390-playing.png');
    await cdp.send('Emulation.setDeviceMetricsOverride', {width: 1440, height: 960, deviceScaleFactor: 1, mobile: false});

    await cdp.evaluate('document.querySelector(".live-parameter-details").open=true');
    await waitFor(async () => cdp.evaluate('document.querySelectorAll(".live-param-row input").length > 0'), 'registry-backed parameter controls');
    await cdp.evaluate(`(() => {const input=document.querySelector('.live-param-row input');input.dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,cancelable:true,button:2,clientX:410,clientY:220}));})()`);
    await waitFor(async () => cdp.evaluate('!document.querySelector("#midi-learn-menu").hidden'), 'MIDI learn menu');
    await cdp.screenshot('midi-learn-menu.png');
    await cdp.evaluate('document.querySelector("#midi-enable").click()');
    await waitFor(async () => cdp.evaluate('window.__midiOptions?.sysex === false && document.querySelector("#midi-status").textContent.includes("1 MIDI device")'), 'Web MIDI access without SysEx');
    await cdp.evaluate(`(() => {const input=document.querySelector('.live-param-row input');input.dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,cancelable:true,button:2,clientX:410,clientY:220}));})()`);
    await waitFor(async () => cdp.evaluate('!document.querySelector("#midi-learn-menu").hidden'), 'MIDI learn menu after access');
    await cdp.evaluate('document.querySelector("#midi-learn").click()');
    await cdp.evaluate('window.__emitMIDI(0xb0,74,96)');
    await waitFor(async () => cdp.evaluate('JSON.parse(localStorage.getItem("cicada.midi.mappings.v1") || "[]").some(mapping=>mapping.cc===74 && mapping.address)'), 'learned CC persisted');
    const controlBefore = await cdp.evaluate('document.querySelector(".live-param-row output").value');
    await cdp.evaluate('window.__emitMIDI(0xb0,74,120)');
    await waitFor(async () => cdp.evaluate(`document.querySelector('.live-param-row output').value !== ${JSON.stringify(controlBefore)}`), 'learned CC changes its registry control');
    await cdp.evaluate('window.__emitMIDI(0x90,60,100);window.__emitMIDI(0x80,60,0)');
    await delay(80);

    await cdp.evaluate(`(() => {const arm=document.querySelector('.live-arm input[value="bass"]');arm.checked=true;arm.dispatchEvent(new Event('change',{bubbles:true}));document.querySelector('#live-record').click();})()`);
    await waitFor(async () => cdp.evaluate('document.querySelector("#live-record").getAttribute("aria-pressed") === "true"'), 'armed MIDI capture');
    await cdp.evaluate('window.__emitMIDI(0x90,64,91)');
    await delay(100);
    await cdp.evaluate('window.__emitMIDI(0x80,64,0)');
    await delay(60);
    await cdp.evaluate('document.querySelector("#live-stop-all").click()');
    await waitFor(async () => cdp.evaluate('!document.querySelector("#live-take-panel").hidden && document.querySelectorAll(".take-preview-cell.has-note").length > 0'), 'take preview after Stop');
    await cdp.screenshot('midi-take-before-commit.png');

    assert.match(await cdp.evaluate('document.querySelector("#live-take-summary").textContent'), /MIDI velocity is not saved/, 'take preview must disclose acid velocity normalization');
    const oldSource = fs.readFileSync(scorePath, 'utf8');
    const oldHistory = await json('http://127.0.0.1:8161/api/history');
    const oldTimeOrigin = await cdp.evaluate('performance.timeOrigin');
    await cdp.evaluate('document.querySelector("#live-take-commit").click()');
    await waitFor(async () => {
      const text = fs.readFileSync(scorePath, 'utf8');
      return text !== oldSource;
    }, 'take source commit', 10000);
    await waitFor(async () => {
      try { return await cdp.evaluate(`performance.timeOrigin !== ${oldTimeOrigin}`); }
      catch { return false; }
    }, 'Studio reload after commit');
    const newSource = fs.readFileSync(scorePath, 'utf8');
    const diff = require('node:child_process').spawnSync('diff', ['-u', '-L', 'before', '-L', 'after', scoreSource, scorePath], {encoding: 'utf8'});
    if (diff.status !== 1) throw new Error(`expected one committed source diff, diff exited ${diff.status}`);
    fs.writeFileSync(path.join(evidenceDirectory, 'committed-take-source.diff'), diff.stdout);
    const newHistory = await json('http://127.0.0.1:8161/api/history');
    const recorded = newHistory.events.filter(event => event.detail === 'Recorded 1 notes into riff');
    assert.equal(recorded.length, 1, 'commit must write one history entry');
    assert.notEqual(newSource, originalScore, 'take commit should change the score copy');
    assert.ok(oldHistory.events.length < newHistory.events.length, 'take commit should append history');
    await cdp.send('Emulation.setDeviceMetricsOverride', {width: 1440, height: 960, deviceScaleFactor: 1, mobile: false});
    await cdp.evaluate('document.querySelector("#live-toggle").click()');
    await cdp.screenshot('committed-take-source.png');
    await cdp.send('Page.addScriptToEvaluateOnNewDocument', {source: "Object.defineProperty(navigator, 'requestMIDIAccess', {configurable:true,value:undefined});"});
    await cdp.send('Page.reload');
    await waitFor(async () => {
      try { return await cdp.evaluate('typeof navigator.requestMIDIAccess === "undefined" && !!window.cicadaAudio && !!document.querySelector(".live-param-row input")'); }
      catch { return false; }
    }, 'parameter controls without Web MIDI');
    await cdp.evaluate(`(() => {const toggle=document.querySelector('#live-toggle');if(toggle.getAttribute('aria-pressed')!=='true')toggle.click();document.querySelector('.live-parameter-details').open=true;})()`);
    assert.equal(await cdp.evaluate('document.querySelector(".live-parameter-details").hidden'), false, 'manual sliders must remain visible without Web MIDI');
    assert.ok(await cdp.evaluate('document.querySelector(".live-param-row input").getBoundingClientRect().height > 0'), 'manual slider is rendered without Web MIDI');
    console.log(`headless Chrome MIDI capture and no-MIDI parameter controls passed; evidence: ${evidenceDirectory}`);
  } finally {
    socket?.close();
    for (const child of [...children].reverse()) {
      if (child.exitCode === null) child.kill('SIGTERM');
    }
    await Promise.all(children.map(child => new Promise(resolve => {
      if (child.exitCode !== null) return resolve();
      const timeout = setTimeout(() => resolve(), 5000);
      child.once('exit', () => { clearTimeout(timeout); resolve(); });
    })));
    const logs = children.map(child => {
      const captured = childLogs.get(child);
      return `pid=${child.pid}\nstdout:\n${captured.stdout}\nstderr:\n${captured.stderr}`;
    }).join('\n---\n');
    fs.writeFileSync(path.join(evidenceDirectory, 'midi-virtual-processes.log'), logs);
    fs.rmSync(tempDirectory, {recursive: true, force: true});
  }
}

main().catch(error => {
  console.error(error.stack || error);
  process.exitCode = 1;
});
