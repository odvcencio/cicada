'use strict';
(() => {
  const $ = id => document.getElementById(id);
  const ui = {play: $('play'), stop: $('stop'), status: $('status'), source: $('source'), preset: $('preset')};
  let api, context, node, gain, analyser, snapshot, modulePromise, resetPromise, stageResolve, stageReject, suppressReset = false, suppressedStopAcks = 0, starting = false, failed = false;
  const state = {playing: false, metrics: null, messageCount: 0, faults: 0, playhead: 0, errors: [], revision: '', resets: 0};
  // Read-only diagnostics are also consumed by the browser acceptance checks.
  window.cicadaDemoState = state;
  const call = (name, ...args) => {
    const result = api[name](...args);
    if (typeof result === 'string' && result) throw new Error(result);
    return result;
  };
  const fail = error => {
    const message = error.message || String(error);
    state.errors.push(message);
    failed = true;
    ui.status.textContent = message;
    state.playing = false;
    if (node) node.disconnect();
    refreshControls();
  };
  window.cicadaDemoError = fail;
  const refreshControls = () => {
    const paused = context && context.state !== 'running';
    ui.play.disabled = !api || failed || starting || (!paused && (!!resetPromise || state.playing));
    ui.stop.disabled = !node || !state.playing;
    ui.preset.disabled = !api || starting || !!resetPromise;
    for (const id of ['apply', 'reset']) $(id).disabled = !api || starting || !!resetPromise;
    $('undo').disabled = !api || starting || !!resetPromise || !snapshot?.canUndo;
    $('redo').disabled = !api || starting || !!resetPromise || !snapshot?.canRedo;
    $('audio-state').textContent = state.playing ? 'AudioWorklet playing' : context ? 'Stopped' : 'Audio off';
  };
  const refreshSource = () => {
    snapshot = call('snapshot');
    ui.source.value = snapshot.source;
    ui.preset.value = snapshot.preset;
    ui.source.disabled = false;
    refreshControls();
  };
  const prepare = () => call('prepare', context.sampleRate);
  const latency = () => ((context.baseLatency || 0) + (context.outputLatency || 0)) * 1000;
  const rebind = () => {
    suppressReset = true;
    try { call('bind', node, context); } finally { suppressReset = false; }
  };
  const resetKernel = () => {
    if (resetPromise) return resetPromise;
    state.playing = false;
    resetPromise = (async () => {
      const fresh = prepare();
      await new Promise((resolve, reject) => {
        const timeout = setTimeout(() => reject(new Error('Score did not load; reload to retry.')), 15000);
        stageResolve = () => { clearTimeout(timeout); resolve(); };
        stageReject = error => { clearTimeout(timeout); reject(error); };
        node.port.postMessage({t: 'i', i: fresh.image.buffer, r: fresh.revision}, [fresh.image.buffer]);
      });
      rebind();
      state.revision = fresh.revision;
      state.resets++;
    })().catch(fail).finally(() => { resetPromise = null; refreshControls(); });
    refreshControls();
    return resetPromise;
  };
  const kindNames = ['Unknown', 'Playhead', 'Meter', 'Note on', 'Note off', 'Switched', 'Overload', 'Fault', 'Late', 'Bar', 'Phrase end', 'Layer changed', 'Macro reached', 'State changed', 'Stinger started', 'Stinger ended'];
  const receive = data => {
    if (data.t === 'm') {
      try {
        if (data.n % 16 || data.n > data.bytes.byteLength) throw new Error('Incomplete kernel message');
        const view = new DataView(data.bytes);
        for (let at = 0; at < data.n; at += 16) {
          const kind = view.getUint8(at), tick = Number(view.getBigInt64(at + 8, true));
          state.messageCount++;
          if (kind === 1) state.playhead = tick;
          if (kind === 7) { state.faults++; throw new Error(`Kernel fault ${view.getUint16(at + 2, true)}`); }
          state.lastMessage = {kind: kindNames[kind] || 'Unknown', track: view.getUint8(at + 1), tick};
        }
      } catch (error) { fail(error); }
      finally { node.port.postMessage({t: 'b', bytes: data.bytes}, [data.bytes]); }
    } else if (data.t === 'q') {
      const histogram = data.d;
      let samples = 0, seen = 0, p99 = 0;
      for (let i = 0; i < 254; i++) samples += histogram[i];
      for (let i = 0; i < 254; i++) {
        seen += histogram[i];
        if (samples && seen >= Math.ceil(samples * .99)) { p99 = (i + 1) / 4; break; }
      }
      state.metrics = {underruns: data.u, memoryBytes: data.m, callbackSamples: samples, callbackP99Ms: p99, durationExceedances: histogram[254], gapExceedances: histogram[255], quantumMs: data.q, durationLimitMs: data.dl, gapLimitMs: data.gl};
      $('p99').textContent = samples ? `${p99.toFixed(2)} ms` : '—';
      $('underruns').textContent = String(data.u);
      $('memory').textContent = `${(data.m / 1048576).toFixed(1)} MiB`;
      $('messages').textContent = state.messageCount.toLocaleString();
      $('timing-detail').textContent = `${context.sampleRate.toLocaleString()} Hz · ${samples.toLocaleString()} callbacks · ${data.q.toFixed(2)} ms quantum · ${state.clock ? 'high resolution clock' : '1 ms clock'} · p99 uses 0.25 ms buckets`;
      const last = state.lastMessage;
      if (last) $('drain').textContent = `${last.kind} · track ${last.track} · tick ${last.tick.toLocaleString()}`;
    } else if (data.t === 's') {
      if (!data.p && suppressedStopAcks) { suppressedStopAcks--; return; }
      state.playing = data.p;
      if (!data.p && !suppressReset) resetKernel();
      refreshControls();
    } else if (data.t === 't') {
      const resolve = stageResolve;
      stageResolve = stageReject = null;
      if (resolve) resolve();
    } else if (['f', 'e', 'x'].includes(data.t)) {
      if (data.t === 'f') state.faults++;
      const error = new Error(data.e || `Kernel fault ${data.a}`);
      if (stageReject) { stageReject(error); stageResolve = stageReject = null; }
      fail(error);
    }
  };
  const start = async () => {
    const Context = window.AudioContext || window.webkitAudioContext;
    if (!Context || !window.AudioWorkletNode) throw new Error('Use a browser with AudioWorklet support over HTTPS.');
    context = new Context({sampleRate: 48000, latencyHint: 'interactive'});
    context.addEventListener('statechange', refreshControls);
    try {
      modulePromise ||= fetch('/assets/kernel.wasm').then(response => {
        if (!response.ok) throw new Error('Cannot load the audio kernel; reload to retry.');
        return response.arrayBuffer();
      }).then(bytes => WebAssembly.compile(bytes));
      const module = await modulePromise, fresh = prepare();
      await context.audioWorklet.addModule('/audio/processor.js');
      node = new AudioWorkletNode(context, 'cicada', {numberOfInputs: 0, numberOfOutputs: 1, outputChannelCount: [2], processorOptions: {m: module, i: fresh.image.buffer, r: fresh.revision, l: latency()}});
      const post = node.port.postMessage.bind(node.port);
      node.port.postMessage = (data, transfer) => {
        if (suppressReset && data.t === 'c' && data.bytes[0] === 2) suppressedStopAcks++;
        post(data, transfer);
      };
      await new Promise((resolve, reject) => {
        const timeout = setTimeout(() => reject(new Error('Audio did not start; reload to retry.')), 15000);
        node.port.onmessage = event => {
          if (event.data.t === 'r') { clearTimeout(timeout); state.clock = event.data.c; state.revision = event.data.r; resolve(); }
          if (event.data.t === 'e') { clearTimeout(timeout); reject(new Error(event.data.e)); }
          receive(event.data);
        };
        node.port.onmessageerror = () => { clearTimeout(timeout); reject(new Error('Cannot read audio messages; reload to retry.')); };
      });
      gain = context.createGain();
      gain.gain.value = Number($('volume').value);
      analyser = context.createAnalyser(); analyser.fftSize = 2048;
      node.connect(gain); gain.connect(analyser); analyser.connect(context.destination);
      await context.resume();
      rebind();
      setInterval(() => { if (node) node.port.postMessage({t: 'q', l: latency()}); }, 1000);
      // The same metrics request used by the UI; no render-thread test hooks.
      window.cicadaDemoAudio = {context, analyser, node};
    } catch (error) {
      if (node) node.disconnect();
      node = null;
      await context.close(); context = null;
      throw error;
    }
  };
  ui.play.onclick = async () => {
    starting = true; refreshControls();
    try {
      const paused = context && context.state !== 'running';
      if (!context) await start();
      await context.resume();
      if (paused) { call('stop'); await resetKernel(); }
      else await resetPromise;
      call('play');
      state.playing = true;
      ui.status.textContent = 'Playing locally. Stop, change a sketch, or edit the score below.';
    } catch (error) { fail(error); }
    finally { starting = false; refreshControls(); }
  };
  ui.stop.onclick = () => {
    try { call('stop'); ui.status.textContent = 'Stopped. Your score is ready to play again.'; }
    catch (error) { fail(error); }
  };
  $('volume').oninput = () => { if (gain) gain.gain.setTargetAtTime(Number($('volume').value), context.currentTime, .01); };
  const change = async (action, value = '') => {
    try {
      call('change', action, snapshot.revision, value);
      $('edit-error').textContent = '';
      refreshSource();
      if (node) { call('stop'); await resetKernel(); }
      ui.status.textContent = 'Score ready. Press Play to hear your changes.';
    } catch (error) { $('edit-error').textContent = error.message; }
    refreshControls();
  };
  $('apply').onclick = () => change('edit', ui.source.value);
  $('undo').onclick = () => change('undo');
  $('redo').onclick = () => change('redo');
  $('reset').onclick = () => change('reset');
  ui.preset.onchange = () => change('preset', ui.preset.value);
  window.addEventListener('pagehide', () => { if (api) call('close'); if (context) context.close(); });
  (async () => {
    const go = new Go();
    const response = await fetch('/assets/demo.wasm');
    if (!response.ok) throw new Error('Cannot load score tools; reload to retry.');
    const {instance} = await WebAssembly.instantiate(await response.arrayBuffer(), go.importObject);
    go.run(instance).catch(fail);
    api = window.cicadaDemo;
    if (!api) throw new Error('Score tools did not initialize; reload to retry.');
    for (const preset of api.presets()) {
      const option = document.createElement('option'); option.value = preset.id; option.textContent = preset.name;
      ui.preset.append(option);
    }
    const custom = document.createElement('option'); custom.value = 'custom'; custom.textContent = 'Custom score'; custom.disabled = true;
    ui.preset.append(custom);
    refreshSource();
    ui.status.textContent = 'Ready. Press Play to enable audio in this tab.';
  })().catch(fail);
})();
