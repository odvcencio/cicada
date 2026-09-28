(() => {
  'use strict';

  const byId = id => document.getElementById(id);
  const outputSelect = byId('audio-output-device');
  const inputSelect = byId('audio-input-device');
  const inputEnabled = byId('audio-input-enabled');
  const monitorMuted = byId('audio-monitor-muted');
  const monitorGain = byId('audio-monitor-gain');
  const monitorGainValue = byId('audio-monitor-gain-value');
  const monitorMode = byId('audio-monitor-mode');
  const applyButton = byId('audio-apply-devices');
  const status = byId('audio-io-status');
  const statusBar = byId('audio-status-bar');
  const meters = byId('audio-io-meters');
  const help = byId('audio-io-help');
  if (!outputSelect || !inputSelect || !status) return;

  let latest = null;
  let monitorTimer = 0;
  let pending = false;
  let inventoryKey = '';

  function dbfs(value) {
    if (!Number.isFinite(value) || value <= 0) return '−120';
    return Math.max(-120, 20 * Math.log10(value)).toFixed(1).replace('-', '−');
  }

  function endpointLabel(device, input) {
    const channels = input ? device.inputs : device.outputs;
    return `${device.name} · ${channels} ch · ${device.sampleRate || 'unknown'} Hz`;
  }

  function populate(select, devices, input, selected, defaultName) {
    const options = [new Option(`Default ${input ? 'input' : 'output'}${defaultName ? ` · ${defaultName}` : ''}`, '')];
    for (const device of devices) {
      if ((input ? device.inputs : device.outputs) <= 0) continue;
      if ((input ? device.defaultInput : device.defaultOutput)) continue;
      options.push(new Option(endpointLabel(device, input), device.id));
    }
    select.replaceChildren(...options);
    select.value = selected || '';
    if (select.value !== (selected || '')) select.value = '';
  }

  function readOptions() {
    return {
      inputDevice: inputSelect.value,
      outputDevice: outputSelect.value,
      inputEnabled: inputEnabled.checked,
      monitorMuted: monitorMuted.checked,
      monitorGain: Number(monitorGain.value),
      monitorMode: monitorMode.value
    };
  }

  function setMeterText(runtime) {
    if (!meters) return;
    meters.replaceChildren();
    const inputLine = document.createElement('span');
    inputLine.textContent = `Input peak: L ${dbfs(runtime.inputPeakL)} dBFS · R ${dbfs(runtime.inputPeakR)} dBFS`;
    const outputLine = document.createElement('span');
    outputLine.textContent = `Output peak: L ${dbfs(runtime.outputPeakL)} dBFS · R ${dbfs(runtime.outputPeakR)} dBFS`;
    meters.append(inputLine, outputLine);
  }

  function formatRuntime(state) {
    const runtime = state.runtime || {};
    if (!state.inventory.supported) {
      return `${runtime.backend || state.inventory.backend || 'Oto'} · ${runtime.outputName || 'system default output'} · ${runtime.sampleRate || 48000} Hz`;
    }
    if (runtime.error) return runtime.error;
    const inputName = runtime.inputName || (state.options.inputEnabled ? 'input unavailable' : 'input off');
    const outputName = runtime.outputName || 'output not started';
    if (!runtime.periodFrames || !runtime.sampleRate) {
      const idle = state.playing ? `${outputName} · ${inputName}` : 'device endpoints closed · input capture off';
      return `${state.playing ? 'Starting' : 'Stopped'} · ${idle}`;
    }
    const periodMS = runtime.periodFrames * 1000 / runtime.sampleRate;
    const inputLatency = runtime.inputLatencyKnown ? `${runtime.inputLatencyMs.toFixed(1)} ms` : 'unreported';
    const outputLatency = runtime.outputLatencyKnown ? `${runtime.outputLatencyMs.toFixed(1)} ms` : 'unreported';
    return `${runtime.backend} · ${outputName} ← ${inputName} · ${runtime.sampleRate} Hz · period ${runtime.periodFrames} frames (${periodMS.toFixed(1)} ms) · device latency in ${inputLatency} / out ${outputLatency} · callbacks ${runtime.callbacks || 0} · dropouts ${runtime.dropouts} · callback max ${runtime.callbackMaxUs.toFixed(0)} μs`;
  }

  function updateControls(state, syncOptions) {
    const supported = !!state.inventory.supported;
    const devices = state.inventory.devices || [];
    const defaultInput = devices.find(device => device.defaultInput)?.name || '';
    const defaultOutput = devices.find(device => device.defaultOutput)?.name || '';
    const nextInventoryKey = JSON.stringify(devices.map(device => [device.id, device.inputs, device.outputs, device.sampleRate, device.defaultInput, device.defaultOutput]));
    if (syncOptions || nextInventoryKey !== inventoryKey) {
      populate(outputSelect, devices, false, state.options.outputDevice, defaultOutput);
      populate(inputSelect, devices, true, state.options.inputDevice, defaultInput);
      inventoryKey = nextInventoryKey;
    }
    if (syncOptions) {
      inputEnabled.checked = !!state.options.inputEnabled;
      monitorMuted.checked = !!state.options.monitorMuted;
      monitorGain.value = String(state.options.monitorGain);
      monitorMode.value = state.options.monitorMode || 'stereo';
      monitorGainValue.value = `${Math.round(Number(monitorGain.value) * 100)}%`;
    }
    outputSelect.disabled = !supported || !!state.playing;
    inputSelect.disabled = !supported || !inputEnabled.checked || !!state.playing;
    inputEnabled.disabled = !supported || !devices.some(device => device.inputs > 0) || !!state.playing;
    const monitorAvailable = supported && inputEnabled.checked && devices.some(device => device.inputs > 0);
    monitorMuted.disabled = !monitorAvailable;
    monitorGain.disabled = !monitorAvailable;
    monitorMode.disabled = !monitorAvailable;
    applyButton.disabled = !supported || !!state.playing;
    if (!supported) {
      help.textContent = state.inventory.message || 'Oto uses the system default output. Native device selection and capture are available with Tymbal on Windows and Linux.';
    } else if (state.inventory.message) {
      help.textContent = state.inventory.message;
    } else if (state.inventory.host === 'alsa') {
      help.textContent = 'Tymbal uses ALSA on Linux. Select output and capture devices while stopped; input monitoring starts muted to prevent feedback.';
    } else {
      help.textContent = 'WASAPI shared mode runs capture and render together. Input monitoring starts muted to prevent feedback; select devices while stopped.';
    }
  }

  function render(state, syncOptions = false) {
    latest = state;
    if (statusBar) {
      statusBar.textContent = state.status || 'Audio status unavailable';
      statusBar.dataset.state = state.runtime?.error ? 'error' : '';
    }
    status.textContent = formatRuntime(state);
    status.classList.toggle('audio-io-error', !!state.runtime?.error);
    if (state.inventory.message && state.inventory.supported) status.title = state.inventory.message;
    else status.removeAttribute('title');
    setMeterText(state.runtime || {});
    updateControls(state, syncOptions);
  }

  async function loadState() {
    try {
      const response = await fetch('/api/audio/config', {cache: 'no-store'});
      const state = await response.json();
      if (response.ok) render(state, latest === null);
    } catch { /* Audio controls remain available if one status poll is interrupted. */ }
  }

  async function saveOptions(options, announce) {
    if (pending) return;
    pending = true;
    try {
      const response = await fetch('/api/audio/config', {
        method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(options)
      });
      const result = await response.json();
      if (!response.ok) {
        status.textContent = result.error || 'Audio settings could not be applied';
        status.classList.add('audio-io-error');
        return;
      }
      render(result, true);
      if (announce) status.textContent = 'Audio settings applied · ' + formatRuntime(result);
    } catch (error) {
      status.textContent = `Audio settings could not be saved: ${error.message}`;
      status.classList.add('audio-io-error');
    } finally {
      pending = false;
    }
  }

  function scheduleMonitorUpdate() {
    monitorGainValue.value = `${Math.round(Number(monitorGain.value) * 100)}%`;
    clearTimeout(monitorTimer);
    monitorTimer = setTimeout(() => saveOptions(readOptions(), false), 120);
  }

  applyButton.addEventListener('click', () => saveOptions(readOptions(), true));
  for (const control of [monitorMuted, monitorGain, monitorMode]) {
    control.addEventListener('input', scheduleMonitorUpdate);
    control.addEventListener('change', scheduleMonitorUpdate);
  }

  loadState();
  setInterval(loadState, 1000);
})();
