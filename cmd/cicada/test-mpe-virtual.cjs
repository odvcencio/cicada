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
const evidenceDirectory = process.env.CICADA_LIVE_EVIDENCE || path.join(os.tmpdir(), 'cicada-mpe-evidence');
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
  child.on('error', error => { childLogs.get(child).stderr += error.message; });
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
  let socket;
  let cdp;
  try {
    const studio = launch(binary, ['studio', scorePath, '--listen', '127.0.0.1:8161', '--audio', 'null']);
    await waitForHTTP('http://127.0.0.1:8161/', studio);
    launch(chromeBinary, ['--headless=new','--no-sandbox','--disable-gpu','--disable-dev-shm-usage','--mute-audio','--remote-debugging-port=8162','--remote-allow-origins=*',`--user-data-dir=${profileDirectory}`,'about:blank']);
    const page = await openPage();
    cdp = page.cdp;
    socket = page.socket;
    await cdp.send('Emulation.setDeviceMetricsOverride', {width:1440,height:960,deviceScaleFactor:1,mobile:false});
    await cdp.evaluate(`document.querySelector('#live-toggle').click();document.querySelector('#midi-enable').click();document.querySelector('#transport-button').click()`);
    await waitFor(async () => cdp.evaluate('!!window.__midiAccess?.inputs.values().next().value.onmidimessage'), 'Web MIDI input');
    await waitFor(async () => cdp.evaluate('fetch("/api/transport").then(r=>r.json()).then(s=>s.playing)'), 'MPE playback');
    await waitFor(async () => cdp.evaluate('!!document.querySelector(".beat-lamp[aria-current=true]")'), 'Live playback clock');
    await cdp.evaluate(`(() => {
      const arm=document.querySelector('.live-arm input[value="bass"]');arm.checked=true;arm.dispatchEvent(new Event('change',{bubbles:true}));
      document.querySelector('#live-record').click();
    })()`);
    await waitFor(async () => cdp.evaluate('document.querySelector("#live-record").getAttribute("aria-pressed") === "true"'), 'MPE record arm');
    await cdp.evaluate(`(() => {
      window.__emitMIDI(0xb0,101,0);window.__emitMIDI(0xb0,100,6);window.__emitMIDI(0xb0,6,14);
      window.__emitMIDI(0xb1,101,0);window.__emitMIDI(0xb1,100,0);window.__emitMIDI(0xb1,6,2);
      window.__emitMIDI(0x91,67,100);window.__emitMIDI(0xd1,80);window.__emitMIDI(0xb1,74,100);window.__emitMIDI(0xe1,0,80);
    })()`);
    for (const value of [88,72,88,72,88,72,88,72,88,72,88,72]) {
      await delay(8);
      await cdp.evaluate(`window.__emitMIDI(0xe1,0,${value})`);
    }
    await delay(150);
    await cdp.evaluate('window.__emitMIDI(0x81,67,0);document.querySelector("#live-stop-all").click()');
    await waitFor(async () => cdp.evaluate('!document.querySelector("#live-take-panel").hidden && document.querySelectorAll(".take-preview-cell.has-note").length > 0'), 'MPE take preview');
    await cdp.evaluate('document.querySelector("#live-take-panel").scrollIntoView({block:"center"})');
    await cdp.screenshot('mpe-take-desktop-1440.png');
    await cdp.send('Emulation.setDeviceMetricsOverride', {width:390,height:844,deviceScaleFactor:1,mobile:true});
    await delay(150);
    await cdp.evaluate(`(() => {
      const panel = document.querySelector('#live-take-panel');
      panel.scrollIntoView({block:'start'});
      let scroll = panel.parentElement;
      while (scroll && scroll.scrollHeight <= scroll.clientHeight) scroll = scroll.parentElement;
      if (scroll) scroll.scrollTop += panel.getBoundingClientRect().top - 260;
    })()`);
    await delay(150);
    await cdp.screenshot('mpe-take-mobile-390.png');
    assert.equal(await cdp.evaluate('document.documentElement.scrollWidth <= window.innerWidth'), true, 'MPE preview fits narrow viewport');
    const before = fs.readFileSync(scorePath,'utf8');
    const timeOrigin = await cdp.evaluate('performance.timeOrigin');
    await cdp.evaluate('document.querySelector("#live-take-commit").click()');
    await waitFor(async () => fs.readFileSync(scorePath,'utf8') !== before, 'MPE score commit');
    const captured = fs.readFileSync(scorePath,'utf8');
    for (const row of ['bend:','vibrato:','pressure:','timbre:']) assert.ok(captured.includes(row), `recorded score includes ${row}`);
    fs.writeFileSync(path.join(evidenceDirectory,'mpe-take.cicada'),captured);
    await cdp.send('Page.reload');
    await waitFor(async () => {
      try { return await cdp.evaluate(`performance.timeOrigin !== ${timeOrigin} && !!window.cicadaAudio && document.querySelector('.source-pane')?.textContent.includes('bend:')`); }
      catch { return false; }
    }, 'committed MPE score projection');
    await cdp.send('Emulation.setDeviceMetricsOverride', {width:1440,height:960,deviceScaleFactor:1,mobile:false});
    await cdp.evaluate(`document.querySelector('#live-toggle').click()`);
    await delay(150);
    await cdp.evaluate(`document.querySelector('[data-panel-tab="code"]').click();document.querySelector('.source-pane').scrollTop=600`);
    await delay(150);
    await cdp.screenshot('mpe-committed-score.png');
    const run = args => {
      const result = require('node:child_process').spawnSync(binary,args,{encoding:'utf8'});
      assert.equal(result.status,0,result.stderr || result.stdout);
    };
    run(['check',scorePath]);
    run(['fmt','-w',scorePath]);
    run(['fmt',scorePath,'--check']);
    fs.copyFileSync(scorePath,path.join(evidenceDirectory,'mpe-take.cicada'));
    console.log('Web MIDI MPE take committed bend/vibrato/pressure/timbre rows; check and fmt round-trip passed');
  } catch (error) {
    if (cdp) {
      try { console.error(await cdp.evaluate('document.querySelector("#studio-status")?.textContent')); }
      catch {}
    }
    throw error;
  } finally {
    socket?.close();
    for (const child of [...children].reverse()) if (child.exitCode === null) child.kill('SIGTERM');
    await Promise.all(children.map(child => new Promise(resolve => {
      if (child.exitCode !== null) return resolve();
      const timeout=setTimeout(resolve,5000);
      child.once('exit',()=>{clearTimeout(timeout);resolve();});
    })));
    const logs=children.map(child=>{const log=childLogs.get(child);return `stdout:\n${log.stdout}\nstderr:\n${log.stderr}`;}).join('\n');
    fs.writeFileSync(path.join(evidenceDirectory,'mpe-virtual-processes.log'),logs);
    fs.rmSync(tempDirectory,{recursive:true,force:true});
  }
}

main().catch(error=>{console.error(error.stack || error);process.exitCode=1;});
