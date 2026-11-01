'use strict';
(() => {
  const $ = id => document.getElementById(id);
  const ui = {play: $('play'), stop: $('stop'), status: $('status'), source: $('source'), preset: $('preset')};
  let api, context, node, gain, analyser, snapshot, modulePromise, resetPromise, recoveryPromise, metricsTimer, lastGoodSource, stageResolve, stageReject, suppressReset = false, suppressedStopAcks = 0, starting = false, failed = false, disposing = false;
  const state = {playing: false, metrics: null, messageCount: 0, faults: 0, playhead: 0, errors: [], revision: '', resets: 0};
  const draftKey = 'cicada-demo-score-v1', maxSourceBytes = 128 * 1024;
  let inputVersion = 0;
  const saveDraft = () => {
    try {
      if (new TextEncoder().encode(ui.source.value).length > maxSourceBytes) throw new Error('Draft exceeds 128 KiB. Download it to avoid losing changes.');
      localStorage.setItem(draftKey, JSON.stringify({source: snapshot.source, draft: ui.source.value}));
      $('storage-status').textContent = 'Draft saved in this browser.';
    } catch (error) {
      $('storage-status').textContent = error.message.startsWith('Draft exceeds') ? error.message : 'Could not save the draft in this browser. Download it to keep your changes.';
    }
  };
  // Read-only diagnostics are also consumed by the browser acceptance checks.
  window.cicadaDemoState = state;
  const call = (name, ...args) => {
    const result = api[name](...args);
    if (typeof result === 'string' && result) throw new Error(result);
    return result;
  };
  const fail = error => {
    if (disposing || recoveryPromise) return;
    disposing = true;
    const message = error.message || String(error);
    state.errors.push(message);
    failed = !api;
    ui.status.textContent = api ? `${message} Press Play to retry the last working score.` : message;
    state.playing = false;
    const oldNode = node, oldContext = context, reject = stageReject;
    node = context = gain = analyser = null;
    stageResolve = stageReject = null;
    suppressedStopAcks = 0;
    clearInterval(metricsTimer);
    if (oldNode) {
      oldNode.port.onmessage = oldNode.port.onmessageerror = null;
      oldNode.onprocessorerror = null;
      oldNode.disconnect();
    }
    if (reject) reject(error);
    if (api) {
      try {
        call('close');
        if (lastGoodSource && call('snapshot').source !== lastGoodSource) {
          const draft = ui.source.value;
          call('change', 'edit', call('snapshot').revision, lastGoodSource);
          refreshSource();
          ui.source.value = draft;
          saveDraft();
        }
      } catch (closeError) { state.errors.push(closeError.message); }
    }
    if (oldNode) oldNode.port.close();
    recoveryPromise = (oldContext ? oldContext.close() : Promise.resolve())
      .catch(() => {}).finally(() => { recoveryPromise = null; refreshControls(); });
    disposing = false;
    refreshControls();
  };
  window.cicadaDemoError = fail;
  const refreshControls = () => {
    const paused = context && context.state !== 'running';
    ui.play.disabled = !api || failed || starting || !!recoveryPromise || (!paused && (!!resetPromise || state.playing));
    ui.stop.disabled = !node || !state.playing;
    ui.preset.disabled = !api || starting || !!resetPromise;
    for (const id of ['apply', 'reset']) $(id).disabled = !api || starting || !!resetPromise;
    for (const id of ['open-score', 'save-score']) $(id).disabled = !api || starting || !!resetPromise;
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
      lastGoodSource = call('snapshot').source;
      state.resets++;
      return true;
    })().catch(error => { fail(error); return false; }).finally(() => { resetPromise = null; refreshControls(); });
    refreshControls();
    return resetPromise;
  };
  // The worklet stages a complete image and promotes it at the next bar while
  // playing. The demo controller only sends transport commands, so its routing
  // is not used until it is rebuilt when playback stops.
  const stageKernel = () => {
    if (resetPromise) return resetPromise;
    resetPromise = (async () => {
      const fresh = prepare();
      await new Promise((resolve, reject) => {
        const timeout = setTimeout(() => reject(new Error('Score did not load; reload to retry.')), 15000);
        stageResolve = () => { clearTimeout(timeout); resolve(); };
        stageReject = error => { clearTimeout(timeout); reject(error); };
        node.port.postMessage({t: 'i', i: fresh.image.buffer, r: fresh.revision}, [fresh.image.buffer]);
      });
      // Stop can arrive while the image is loading. In that case the worklet
      // promotes it immediately and the controller must be reset before Play.
      if (!state.playing) { rebind(); state.revision = fresh.revision; }
      lastGoodSource = call('snapshot').source;
      return true;
    })().catch(error => { fail(error); return false; }).finally(() => { resetPromise = null; refreshControls(); });
    refreshControls();
    return resetPromise;
  };
  const kindNames = ['Unknown', 'Playhead', 'Meter', 'Note on', 'Note off', 'Switched', 'Overload', 'Fault', 'Late', 'Bar', 'Phrase end', 'Layer changed', 'Macro reached', 'State changed', 'Stinger started', 'Stinger ended'];
  const receive = data => {
    const sender = node;
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
      finally { if (sender === node) sender.port.postMessage({t: 'b', bytes: data.bytes}, [data.bytes]); }
    } else if (data.t === 'q') {
      const histogram = data.d;
      let samples = 0, seen = 0, p99 = 0;
      for (let i = 0; i < 254; i++) samples += histogram[i];
      for (let i = 0; i < 254; i++) {
        seen += histogram[i];
        if (samples && seen >= Math.ceil(samples * .99)) { p99 = (i + 1) / 4; break; }
      }
      state.metrics = {underruns: data.u, memoryBytes: data.m, peakMemoryBytes: data.mp, instanceCount: data.n, callbackSamples: samples, callbackP99Ms: p99, durationExceedances: histogram[254], gapExceedances: histogram[255], quantumMs: data.q, durationLimitMs: data.dl, gapLimitMs: data.gl};
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
      const activeNode = node;
      node.onprocessorerror = () => fail(new Error('Audio processor stopped.'));
      const post = node.port.postMessage.bind(node.port);
      node.port.postMessage = (data, transfer) => {
        if (suppressReset && data.t === 'c' && data.bytes[0] === 2) suppressedStopAcks++;
        post(data, transfer);
      };
      await new Promise((resolve, reject) => {
        const timeout = setTimeout(() => reject(new Error('Audio did not start; reload to retry.')), 15000);
        node.port.onmessage = event => {
          if (node !== activeNode) return;
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
      lastGoodSource = call('snapshot').source;
      metricsTimer = setInterval(() => { if (node) node.port.postMessage({t: 'q', l: latency()}); }, 1000);
      // The same metrics request used by the UI; no render-thread test hooks.
      window.cicadaDemoAudio = {context, analyser, node};
    } catch (error) {
      if (node) node.disconnect();
      node = null;
      if (context) await context.close(); context = null;
      modulePromise = null;
      throw error;
    }
  };
  ui.play.onclick = async () => {
    starting = true; refreshControls();
    try {
      const paused = context && context.state !== 'running';
      if (!context) await start();
      await context.resume();
      if (paused) { call('stop'); if (!await resetKernel()) return; }
      else if (resetPromise && !await resetPromise) return;
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
      inputVersion++;
      $('edit-error').textContent = '';
      refreshSource();
      saveDraft();
      if (node && state.playing) {
        if (!await stageKernel()) return;
        ui.status.textContent = state.playing ? 'Changes queued for the next bar.' : 'Score ready. Press Play to hear your changes.';
      } else {
        if (node) { call('stop'); if (!await resetKernel()) return; }
        ui.status.textContent = 'Score ready. Press Play to hear your changes.';
      }
    } catch (error) { $('edit-error').textContent = error.message; }
    refreshControls();
  };
  $('apply').onclick = () => change('edit', ui.source.value);
  ui.source.addEventListener('input', () => { inputVersion++; saveDraft(); });
  $('open-score').onclick = () => $('score-file').click();
  $('score-file').onchange = async event => {
    const file = event.target.files[0], version = inputVersion;
    event.target.value = '';
    if (!file) return;
    try {
      if (file.size > maxSourceBytes) throw new Error('Score must be at most 128 KiB.');
      const source = new TextDecoder('utf-8', {fatal: true}).decode(await file.arrayBuffer());
      if (inputVersion !== version) throw new Error('The editor changed while the file was opening; try again.');
      ui.source.value = source;
      inputVersion++;
      $('edit-error').textContent = '';
      saveDraft();
      ui.status.textContent = 'Score opened. Apply it when you are ready to play.';
    } catch (error) { $('edit-error').textContent = error instanceof TypeError ? 'Score must be UTF-8 text.' : error.message; }
  };
  $('save-score').onclick = () => {
    const url = URL.createObjectURL(new Blob([ui.source.value], {type: 'text/plain;charset=utf-8'}));
    const link = document.createElement('a');
    link.href = url; link.download = 'score.cicada'; link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };
  ui.source.addEventListener('keydown', event => {
    if (event.key === 'Enter' && (event.ctrlKey || event.metaKey) && !event.isComposing && !$('apply').disabled) {
      event.preventDefault();
      $('apply').click();
    }
  });
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
    try {
      const saved = JSON.parse(localStorage.getItem(draftKey));
      if (saved && typeof saved.source === 'string' && typeof saved.draft === 'string' && new TextEncoder().encode(saved.source).length <= maxSourceBytes && new TextEncoder().encode(saved.draft).length <= maxSourceBytes) {
        try {
          call('change', 'edit', snapshot.revision, saved.source);
          refreshSource();
        } catch (error) {
          $('edit-error').textContent = 'The last applied score could not be restored. Your draft is still available below; apply it to see any errors.';
        }
        ui.source.value = saved.draft;
        $('storage-status').textContent = 'Draft restored from this browser.';
      }
    } catch (error) {
      $('storage-status').textContent = 'Could not restore the saved draft; it is still stored in this browser.';
    }
    lastGoodSource = snapshot.source;
    ui.status.textContent = 'Ready. Press Play to enable audio in this tab.';
  })().catch(fail);
})();
