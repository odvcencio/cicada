(() => {
  'use strict';

  const midi = window.cicadaMidi;
  if (!midi) return;

  const $ = selector => document.querySelector(selector);
  const $$ = selector => [...document.querySelectorAll(selector)];
  const workspace = $('.workspace');
  const liveToggle = $('#live-toggle');
  const liveSurface = $('#live-surface');
  const launchGrid = $('#live-launch-grid');
  const status = $('#studio-status');
  const announcement = $('#live-announcement');
  const quantizeSelect = $('#live-quantize');
  const recordButton = $('#live-record');
  const stopAllButton = $('#live-stop-all');
  const takePanel = $('#live-take-panel');
  const takePreview = $('#live-take-preview');
  const midiControls = $('#midi-controls');
  const midiEnable = $('#midi-enable');
  const midiStatus = $('#midi-status');
  const midiActivity = $('#midi-activity');
  const learnMenu = $('#midi-learn-menu');
  const learnMenuButton = $('#midi-learn');
  const clearMenuButton = $('#midi-clear');
  const mappingList = $('#midi-mapping-list');
  const isMIDIAvailable = typeof navigator.requestMIDIAccess === 'function';
  const settingsKey = 'cicada.live.settings.v1';
  const getStorage = () => { try { return window.localStorage; } catch { return null; } };
  const storage = getStorage();
  const mappings = midi.createMappingStore(storage);
  const mpeInput = midi.createMPEInput();
  const midiNoteTracks = new Map();

  let patternMeta = new Map();
  let sceneNames = [];
  let sceneRows = new Map();
  let tracks = [];
  let trackByID = new Map();
  // readProjection derives the score data Live works from out of the Session and Mix markup.
  // An in-place refresh calls it again, so Live never launches a stale scene or pattern.
  function readProjection() {
    patternMeta = new Map();
    for (const card of $$('.pattern')) {
      const title = card.querySelector('.pattern-title');
      const id = title?.childNodes[0]?.textContent.trim();
      const grid = card.querySelector('.grid-line');
      const steps = Number.parseInt(grid?.style.getPropertyValue('--steps') || '16', 10);
      if (id) patternMeta.set(id, {steps: Number.isInteger(steps) ? steps : 16, drums: card.classList.contains('drum')});
    }
    sceneNames = $$('.scene-matrix thead .scene-pad[data-scene]').map(pad => pad.dataset.scene);
    sceneRows = new Map();
    for (const row of $$('.scene-matrix tbody tr')) {
      const track = row.querySelector('th')?.textContent.trim();
      if (!track) continue;
      const cells = [...row.querySelectorAll('td')].map(cell => {
        const pad = cell.querySelector('button.scene-slot[data-pattern]');
        return {pattern: pad?.dataset.pattern || '', label: pad?.textContent.trim() || cell.textContent.trim() || 'Keep'};
      });
      sceneRows.set(track, cells);
    }
    const mixerRack = $('#mix-rack');
    if (mixerRack && mixerRack.getAttribute('aria-busy') !== 'true') {
      tracks = $$('.mix-strip[data-kind="track"]').map(card => {
        const id = card.dataset.strip || '';
        const kind = card.dataset.sourceKind?.toLowerCase() || '';
        const cells = sceneRows.get(id) || [];
        const drum = kind.includes('drum') || cells.some(cell => cell.pattern && patternMeta.get(cell.pattern)?.drums);
        return {id, kind, drum, acid: kind === 'acid', pitched: !drum && kind !== 'audio'};
      }).filter(track => track.id);
      trackByID = new Map(tracks.map(track => [track.id, track]));
    }
  }
  readProjection();

  function readSettings() {
    try {
      const value = JSON.parse(storage?.getItem(settingsKey) || '{}');
      return value && typeof value === 'object' ? value : {};
    } catch { return {}; }
  }
  let settings = readSettings();
  function saveSettings() {
    try { storage?.setItem(settingsKey, JSON.stringify(settings)); }
    catch { /* The current page keeps working when browser storage is unavailable. */ }
  }
  function setStatus(message, state = '', announceStatus = true) {
    if (!status) return;
    status.textContent = message;
    status.dataset.state = state;
    status.setAttribute('aria-live', announceStatus ? 'polite' : 'off');
  }
  function announce(message) {
    if (!announcement) return;
    announcement.textContent = '';
    requestAnimationFrame(() => { announcement.textContent = message; });
  }
  function fillTrackSelect(select, predicate, selected) {
    if (!select) return;
    const candidates = tracks.filter(predicate);
    select.replaceChildren();
    if (!candidates.length) {
      const option = document.createElement('option');
      option.textContent = 'No matching track';
      option.value = '';
      select.append(option);
      select.disabled = true;
      return;
    }
    select.disabled = false;
    for (const track of candidates) {
      const option = document.createElement('option');
      option.value = track.id;
      option.textContent = track.id;
      select.append(option);
    }
    select.value = candidates.some(track => track.id === selected) ? selected : candidates[0].id;
  }
  const acidSelect = $('#live-acid-track');
  const drumSelect = $('#live-drum-track');
  fillTrackSelect(acidSelect, track => track.pitched, settings.acidTrack);
  fillTrackSelect(drumSelect, track => track.drum, settings.drumTrack);
  if (acidSelect) settings.acidTrack = acidSelect.value;
  if (drumSelect) settings.drumTrack = drumSelect.value;
  if (quantizeSelect) {
    quantizeSelect.value = ['1', '2', '5', '6', '8'].includes(String(settings.quantize)) ? String(settings.quantize) : '2';
    settings.quantize = Number(quantizeSelect.value);
  }
  saveSettings();

  const liveScenePads = [];
  const liveSlotPads = [];
  function makePad(text, className, attrs = {}) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = `live-pad ${className}`;
    button.dataset.baseLabel = text;
    button.dataset.state = 'stopped';
    const label = document.createElement('span');
    label.className = 'live-pad-text';
    label.textContent = text;
    const state = document.createElement('span');
    state.className = 'live-pad-status';
    state.textContent = 'STOPPED';
    button.append(label, state);
    Object.assign(button.dataset, attrs);
    return button;
  }
  function buildLaunchGrid() {
    if (!launchGrid) return;
    liveScenePads.length = 0;
    liveSlotPads.length = 0;
    const table = document.createElement('table');
    table.className = 'live-grid';
    table.setAttribute('aria-label', 'Live scene and track launch pads');
    const head = document.createElement('thead');
    const header = document.createElement('tr');
    const trackHeading = document.createElement('th');
    trackHeading.scope = 'col';
    trackHeading.textContent = 'Track';
    header.append(trackHeading);
    sceneNames.forEach((scene, index) => {
      const cell = document.createElement('th');
      cell.scope = 'col';
      const pad = makePad(scene, 'live-scene-pad', {midiAction: `scene:${scene}`, scene, sceneIndex: String(index)});
      pad.setAttribute('aria-label', `Launch scene ${scene}, stopped`);
      pad.addEventListener('click', () => {
        selectScene(index, false);
        launchScene(scene);
      });
      liveScenePads.push(pad);
      cell.append(pad);
      header.append(cell);
    });
    const stopHeading = document.createElement('th');
    stopHeading.scope = 'col';
    stopHeading.textContent = 'Stop';
    header.append(stopHeading);
    head.append(header);
    const body = document.createElement('tbody');
    for (const track of tracks) {
      const row = document.createElement('tr');
      const label = document.createElement('th');
      label.scope = 'row';
      label.className = 'live-track-label';
      label.textContent = track.id;
      row.append(label);
      const cells = sceneRows.get(track.id) || [];
      sceneNames.forEach((scene, index) => {
        const cell = document.createElement('td');
        const slot = cells[index];
        if (slot?.pattern) {
          const pad = makePad(slot.pattern, 'live-slot-pad', {
            midiAction: `slot:${track.id}:${slot.pattern}`, track: track.id, pattern: slot.pattern, scene
          });
          pad.setAttribute('aria-label', `Launch ${slot.pattern} on ${track.id}, stopped`);
          pad.addEventListener('click', () => launchSlot(track.id, slot.pattern));
          liveSlotPads.push(pad);
          cell.append(pad);
        } else {
          const empty = makePad(slot?.label || 'Keep', 'live-slot-pad', {empty: 'true', track: track.id, scene});
          empty.disabled = true;
          empty.dataset.state = 'stopped';
          empty.querySelector('.live-pad-status').textContent = slot?.label?.toUpperCase() || 'KEEP';
          empty.setAttribute('aria-label', `${slot?.label || 'Keep'} ${track.id} in scene ${scene}`);
          cell.append(empty);
        }
        row.append(cell);
      });
      const stopCell = document.createElement('td');
      const stop = makePad('STOP TRACK', 'live-stop-pad', {midiAction: `stop-track:${track.id}`, track: track.id});
      stop.querySelector('.live-pad-status').textContent = '';
      stop.setAttribute('aria-label', `Stop track ${track.id}`);
      stop.addEventListener('click', () => stopTrack(track.id));
      stopCell.append(stop);
      row.append(stopCell);
      body.append(row);
    }
    table.append(head, body);
    launchGrid.replaceChildren(table);
  }
  buildLaunchGrid();

  const armControls = $('#live-arm-controls');
  const armedTracks = new Set();
  function buildArmControls() {
    for (const id of [...armedTracks]) if (!trackByID.has(id)) armedTracks.delete(id);
    armControls?.replaceChildren();
    for (const track of tracks) {
      const label = document.createElement('label');
      label.className = 'live-arm';
      const checkbox = document.createElement('input');
      checkbox.type = 'checkbox';
      checkbox.value = track.id;
      checkbox.checked = armedTracks.has(track.id);
      checkbox.setAttribute('aria-label', `Arm ${track.id} for MIDI take`);
      checkbox.addEventListener('change', () => {
        if (checkbox.checked) armedTracks.add(track.id);
        else armedTracks.delete(track.id);
        window.dispatchEvent(new CustomEvent('cicada:armchange', {detail: {count: armedTracks.size}}));
      });
      const text = document.createElement('span');
      text.textContent = `Arm ${track.id}`;
      label.append(checkbox, text);
      armControls?.append(label);
    }
    window.dispatchEvent(new CustomEvent('cicada:armchange', {detail: {count: armedTracks.size}}));
  }
  buildArmControls();

  let transportState = {playing: false, bar: 1, step: 1, scene: '', pendingScene: '', pendingSlots: {}, activeSlots: {}, stoppedTracks: []};
  let lastSequence = 0;
  let lastPendingScene = '';
  const lastPendingSlots = new Map();
  let selectedSceneIndex = Math.max(0, sceneNames.indexOf(settings.selectedScene));
  function selectScene(index, focus) {
    if (!sceneNames.length) return;
    selectedSceneIndex = Math.max(0, Math.min(sceneNames.length - 1, index));
    settings.selectedScene = sceneNames[selectedSceneIndex];
    saveSettings();
    liveScenePads.forEach((pad, padIndex) => pad.setAttribute('aria-pressed', padIndex === selectedSceneIndex ? 'true' : 'false'));
    if (focus) liveScenePads[selectedSceneIndex]?.focus();
  }
  selectScene(selectedSceneIndex, false);

  function quantizeTicks(value) {
    const map = {1: 960, 2: 3840, 5: 3840, 6: 7680, 8: 15360};
    return map[value] || 3840;
  }
  function stateTick(state = transportState) {
    return Math.max(0, (Number(state.bar || 1) - 1) * 3840 + (Number(state.step || 1) - 1) * 240);
  }
  function queuedBars(state, selectedQuantize = state.pendingQuantize || quantizeSelect?.value || 2) {
    const current = stateTick(state);
    const quantum = quantizeTicks(Number(selectedQuantize));
    let target = Math.ceil(current / quantum) * quantum;
    if (target <= current) target += quantum;
    return Math.max(1, Math.ceil((target - current) / 3840));
  }
  function setPadState(pad, state, statusText, ariaLabel) {
    pad.dataset.state = state;
    pad.querySelector('.live-pad-status').textContent = statusText;
    pad.setAttribute('aria-label', ariaLabel);
  }
  function renderPosition() {
    const bar = Math.max(1, Number(transportState.bar || 1));
    const step = Math.max(1, Number(transportState.step || 1));
    const beat = Math.floor((step - 1) / 4) + 1;
    const position = $('#live-barbeat');
    if (position) position.textContent = `BAR ${String(bar).padStart(3, '0')} · BEAT ${beat}`;
    $$('#live-beat-lamps .beat-lamp').forEach((lamp, index) => {
      const active = transportState.playing && index === beat - 1;
      lamp.setAttribute('aria-current', active ? 'true' : 'false');
    });
  }
  function renderLaunchStates() {
    const isPlaying = !!transportState.playing;
    const queuedScene = transportState.pendingScene || '';
    const activeScene = transportState.scene || sceneNames[0] || '';
    const stopped = new Set(transportState.stoppedTracks || []);
    const countdown = queuedScene ? queuedBars(transportState) : 0;
    for (const pad of liveScenePads) {
      const scene = pad.dataset.scene;
      const state = queuedScene === scene ? 'queued' : isPlaying && activeScene === scene ? 'playing' : 'stopped';
      const stateText = state === 'queued' ? `QUEUED · ${countdown} BAR${countdown === 1 ? '' : 'S'}` : state.toUpperCase();
      setPadState(pad, state, stateText, `Launch scene ${scene}, ${state === 'queued' ? `queued with ${countdown} bar countdown` : state}`);
    }
    for (const pad of liveSlotPads) {
      const track = pad.dataset.track;
      const scene = pad.dataset.scene;
      const pattern = pad.dataset.pattern;
      const queued = transportState.pendingSlots?.[track] === pattern;
      const sceneIndex = sceneNames.indexOf(activeScene);
      const scenePattern = sceneRows.get(track)?.[sceneIndex]?.pattern || '';
      const activePattern = transportState.activeSlots?.[track] || scenePattern;
      const state = queued ? 'queued' : stopped.has(track) ? 'stopped' : isPlaying && activeScene === scene && activePattern === pattern ? 'playing' : 'stopped';
      const slotCountdown = queued ? queuedBars(transportState, transportState.pendingSlotQuantize?.[track] || quantizeSelect?.value) : 0;
      const stateText = state === 'queued' ? `QUEUED · ${slotCountdown} BAR${slotCountdown === 1 ? '' : 'S'}` : state.toUpperCase();
      setPadState(pad, state, stateText, `Launch ${pattern} on ${track}, ${state === 'queued' ? `queued with ${slotCountdown} bar countdown` : state}`);
    }
    for (const stop of $$('.live-stop-pad')) {
      const track = stop.dataset.track;
      const isStopped = stopped.has(track);
      stop.setAttribute('aria-pressed', isStopped ? 'true' : 'false');
      stop.setAttribute('aria-label', `Stop track ${track}${isStopped ? ', stopped' : ''}`);
      stop.querySelector('.live-pad-status').textContent = isStopped ? 'STOPPED' : '';
    }
    renderPosition();
  }
  function announcePendingLaunches(state) {
    if (state.pendingScene && state.pendingScene !== lastPendingScene) {
      announce(`Queued scene ${state.pendingScene} with a ${queuedBars(state)} bar countdown`);
    }
    lastPendingScene = state.pendingScene || '';
    const pending = state.pendingSlots || {};
    for (const [track, pattern] of Object.entries(pending)) {
      if (lastPendingSlots.get(track) !== pattern) {
        const quantize = state.pendingSlotQuantize?.[track] || quantizeSelect?.value;
        announce(`Queued ${pattern} on ${track} with a ${queuedBars(state, quantize)} bar countdown`);
      }
    }
    for (const track of [...lastPendingSlots.keys()]) if (!pending[track]) lastPendingSlots.delete(track);
    for (const [track, pattern] of Object.entries(pending)) lastPendingSlots.set(track, pattern);
  }
  function acceptTransportState(state) {
    if (!state || typeof state !== 'object') return;
    const sequence = Number(state.sequence || 0);
    if (sequence && sequence < lastSequence) return;
    if (sequence) lastSequence = sequence;
    const wasPlaying = !!transportState.playing;
    transportState = {...transportState, ...state, receivedAt: performance.now()};
    announcePendingLaunches(transportState);
    renderLaunchStates();
    if (recording && wasPlaying && !transportState.playing) finishRecording();
    if (transportState.error) setStatus(transportState.error, 'error');
  }

  const revision = () => document.body.dataset.revision;
  async function transportAction(payload) {
    if (status && status.dataset.state === 'error' && status.textContent.startsWith('Save or discard')) return null;
    try {
      const response = await fetch('/api/transport', {
        method: 'POST', headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({...payload, revision: revision()})
      });
      const state = await response.json();
      if (!response.ok) { setStatus(state.error || 'Transport command failed', 'error'); return null; }
      acceptTransportState(state);
      return state;
    } catch (error) { setStatus(`Transport connection lost: ${error.message}`, 'error'); return null; }
  }
  function selectedQuantize() { return Number(quantizeSelect?.value || 2); }
  function launchScene(scene) {
    const repeated = transportState.pendingScene === scene;
    const request = transportAction({action: 'launch', scene, quantize: selectedQuantize()});
    if (repeated) request.then(state => state && announce(`Queued scene ${scene} with a ${queuedBars(state)} bar countdown`));
    return request;
  }
  function launchSlot(track, pattern) {
    const repeated = transportState.pendingSlots?.[track] === pattern;
    const request = transportAction({action: 'slot', track, pattern, quantize: selectedQuantize()});
    if (repeated) request.then(state => state && announce(`Queued ${pattern} on ${track} with a ${queuedBars(state, state.pendingSlotQuantize?.[track])} bar countdown`));
    return request;
  }
  function stopTrack(track) { return transportAction({action: 'trackStop', track}); }

  $('#live-toggle')?.addEventListener('click', () => {
    const on = liveToggle.getAttribute('aria-pressed') !== 'true';
    liveToggle.setAttribute('aria-pressed', on ? 'true' : 'false');
    liveSurface.hidden = !on;
    workspace.classList.toggle('live-mode', on);
    document.body.classList.toggle('live-mode', on);
    liveToggle.textContent = on ? 'Exit Live' : 'Live';
    if (on) selectScene(selectedSceneIndex, false);
    try { storage?.setItem('cicada.live.enabled', on ? '1' : '0'); } catch {}
  });
  if (settings.liveEnabled === true || storage?.getItem('cicada.live.enabled') === '1') {
    liveToggle?.click();
  }
  quantizeSelect?.addEventListener('change', () => {
    settings.quantize = selectedQuantize();
    saveSettings();
    renderLaunchStates();
  });
  acidSelect?.addEventListener('change', () => { settings.acidTrack = acidSelect.value; saveSettings(); });
  drumSelect?.addEventListener('change', () => { settings.drumTrack = drumSelect.value; saveSettings(); });
  $('#live-stop-all')?.addEventListener('click', async () => {
    router.panic(); window.cicadaAudio?.panic?.(); gamepad.panic();
    await transportAction({action: 'stop'});
    if (recording) finishRecording();
  });

  let currentMidiAccess = null;
  let activityTimer = 0;
  function midiDeviceName(port) { return port?.name || port?.manufacturer || port?.id || 'MIDI device'; }
  function updateMIDIStatus() {
    if (!currentMidiAccess) return;
    const count = [...currentMidiAccess.inputs.values()].filter(input => input.state !== 'disconnected').length;
    midiStatus.textContent = `${count} MIDI device${count === 1 ? '' : 's'}${learnTarget ? ' · learning' : ''}`;
  }
  function flashMIDIActivity() {
    midiActivity.classList.add('active');
    clearTimeout(activityTimer);
    activityTimer = setTimeout(() => midiActivity.classList.remove('active'), 150);
  }
  let parameterCatalog = null, catalogRequest = 0;
  async function refreshParameterCatalog() {
    const request = ++catalogRequest;
    parameterCatalog = null;
    try {
      const catalog = await window.cicadaAudio.params();
      if (request === catalogRequest && catalog.revision === revision()) { parameterCatalog = catalog; window.dispatchEvent(new CustomEvent('cicada:performanceparams')); }
    } catch (error) { setStatus(error.message, 'error'); }
  }
  const dispatchCC = midi.createParameterDispatcher({getAudio: () => window.cicadaAudio, getCatalog: () => parameterCatalog, onError: error => setStatus(error.message, 'error')});
  function sendNoteOn(track, note, velocity, id) {
    if (!window.cicadaAudio) return false;
    try { return window.cicadaAudio.noteOn(track, note, velocity, id); }
    catch (error) { setStatus(error.message, 'error'); return false; }
  }
  function sendNoteOff(track, note, id) {
    try { window.cicadaAudio?.noteOff(track, note, id); }
    catch (error) { setStatus(error.message, 'error'); }
  }
  const router = midi.createInputRouter({noteOn: sendNoteOn, noteOff: sendNoteOff,
    onPress: n => captureNoteOn(n.track, n.note, n.velocity, n.time || performance.now(), n.id),
    onRelease: n => captureNoteOff(n.track, n.note, performance.now(), n.id)});
  const midiPerformance = midi.createMIDIPerformance({router, mappings, dispatchCC,
    learn: storeLearnedInput, action: invokeMappedAction,
    getTrack: channel => trackByID.get(channel === 9 ? drumSelect?.value : acidSelect?.value),
    unsupported: () => setStatus('Pitch bend and aftertouch are unavailable for the current Studio voices; use learned live CC controls')});
  let gamepadControls = {};
  const gamepad = midi.createGamepadPerformance({router, dispatchCC, getTrack: () => acidSelect?.value,
    getAddresses: () => gamepadControls, action: kind => {
      if (kind === 'pattern') { const track=acidSelect?.value, pattern=activePatternFor(track); if (track && pattern) launchSlot(track,pattern); }
      else if (sceneNames[selectedSceneIndex]) launchScene(sceneNames[selectedSceneIndex]);
    }});
  let gamepadEnabled = false, gamepadFrame = 0;
  function pollGamepads() {
    if (!gamepadEnabled) return;
    if (!document.hidden) gamepad.poll(navigator.getGamepads?.() || []);
    gamepadFrame = requestAnimationFrame(pollGamepads);
  }
  function panicInputs() {
    router.panic(); gamepad.panic();
    for (const input of boundMIDIInputs.values()) { releaseMIDIInput(input); if (input.state !== 'disconnected') input.onmidimessage = event => handleMIDIMessage(input,event); }
  }
  for (const event of ['blur', 'pagehide', 'cicada:inputpanic']) window.addEventListener(event, panicInputs);
  document.addEventListener('visibilitychange', () => { if (document.hidden) panicInputs(); });
  window.cicadaPerformance = {router, midi: midiPerformance, gamepad, panic: panicInputs, releaseAll: panicInputs,
    async silence() { panicInputs(); await window.cicadaAudio.silence(); },
    setBrowserController(controller) {
      panicInputs(); window.cicadaAudio?.panic?.(); window.cicadaBrowserPerformance=controller; refreshParameterCatalog();
    },
    setGamepadControls(controls) { gamepadControls = {...controls}; }};
  if (liveSurface) {
    const silence = document.createElement('button'); silence.type='button'; silence.textContent='Silence and reset';
    silence.setAttribute('aria-label','Silence all voices and effects and stop playback');
    silence.addEventListener('click', async () => {
      try { await window.cicadaPerformance.silence(); setStatus('Playback stopped and sound reset'); }
      catch (error) { setStatus(error.message,'error'); }
    });
    liveSurface.append(silence);
  }
  const keyboard = 'awsedftgyhujk';
  document.addEventListener('keydown', event => {
    if (event.repeat || event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey || liveToggle?.getAttribute('aria-pressed') !== 'true' || /^(INPUT|TEXTAREA|SELECT)$/.test(event.target?.tagName || '')) return;
    const index = keyboard.indexOf(event.key.toLowerCase());
    if (index < 0 || !acidSelect?.value) return;
    event.preventDefault();
    router.press({source:'keyboard', device:'studio', channel:0, input:event.code, track:acidSelect.value, note:48+index, velocity:100});
  });
  document.addEventListener('keyup', event => router.release({source:'keyboard', device:'studio', channel:0, input:event.code}));
  if (liveSurface && typeof navigator.getGamepads === 'function') {
    const button = document.createElement('button');
    button.type = 'button'; button.textContent = 'Enable gamepad';
    button.setAttribute('aria-pressed', 'false');
    button.addEventListener('click', () => {
      gamepadEnabled = !gamepadEnabled; gamepad.panic();
      button.setAttribute('aria-pressed', String(gamepadEnabled));
      button.textContent = gamepadEnabled ? 'Disable gamepad' : 'Enable gamepad';
      if (gamepadEnabled) pollGamepads(); else cancelAnimationFrame(gamepadFrame);
    });
    const help = document.createElement('p');
    help.className = 'section-note';
    help.textContent = 'Standard OS-paired gamepads: face buttons play C3, E♭3, G3, C4; left shoulder launches the selected track pattern, right shoulder launches the selected scene. Keyboard A W S E D F T G Y H U J K plays C3–C4. Enable audio first.';
    // Expression bindings are selected from the score's validated registry.
    for (const [name, label] of [['expression', 'Gamepad right trigger'], ['timbre', 'Gamepad right stick X']]) {
      const select = document.createElement('select'); select.setAttribute('aria-label', label + ' live parameter');
      const refresh = () => {
        select.replaceChildren(); const off = document.createElement('option'); off.value = ''; off.textContent = label + ': unassigned'; select.append(off);
        for (const binding of parameterCatalog?.addresses || []) {
          if (!parameterCatalog.registry.find(d => d.id === binding.param)?.live) continue;
          const option = document.createElement('option'); option.value = binding.address; option.textContent = binding.address; select.append(option);
        }
        select.value = gamepadControls[name] || '';
      };
      select.addEventListener('change', () => { gamepadControls[name] = select.value; });
      window.addEventListener('cicada:performanceparams', refresh); refresh(); liveSurface.append(select);
    }
    liveSurface.append(button, help);
  }
  function sendNoteExpression(track, expression) {
    if (!track || !window.cicadaAudio) return;
    try { window.cicadaAudio.noteExpression(track, expression); }
    catch (error) { setStatus(error.message, 'error'); }
  }

  function deviceMappings(device, channel, type, value) {
    return mappings.findInput(device, channel, type, value);
  }
  function invokeMappedAction(action) {
    if (!action) return;
    if (action.startsWith('scene:')) launchScene(action.slice(6));
    else if (action.startsWith('slot:')) {
      const split = action.indexOf(':', 5);
      if (split >= 0) launchSlot(action.slice(5, split), action.slice(split + 1));
    } else if (action.startsWith('stop-track:')) stopTrack(action.slice(11));
  }
  function transportTickAt(time = performance.now()) {
    const base = stateTick();
    if (!transportState.playing || !Number.isFinite(transportState.receivedAt)) return base;
    const tempoText = $('.meta span')?.textContent || '';
    const tempo = Number(tempoText.match(/[0-9]+(?:\.[0-9]+)?/)?.[0] || 120);
    const elapsedMS = Math.max(0, Math.min(500, time - transportState.receivedAt));
    return base + elapsedMS * tempo * midi.MIDI_PPQ / 60000;
  }
  function activePatternFor(track) {
    const manual = transportState.activeSlots?.[track];
    if (manual) return manual;
    const scene = transportState.scene || sceneNames[0];
    const index = sceneNames.indexOf(scene);
    return sceneRows.get(track)?.[index]?.pattern || '';
  }

  let recording = false;
  let capturedNotes = [];
  const activeNotes = new Map();
  function captureKey(track, noteId) { return `${track}:${noteId}`; }
  function captureNoteOn(track, note, velocity, eventTime, identity) {
    const owner = typeof identity === 'string' ? identity : '';
    identity = typeof identity === 'object' && identity ? identity : {noteId:0, channel:0};
    if (!recording || !armedTracks.has(track) || !transportState.playing) return;
    const pattern = activePatternFor(track);
    if (!pattern) { setStatus(`No active pattern is assigned to ${track}`, 'error'); return; }
    const tick = Math.max(0, transportTickAt(eventTime));
    const record = {track, pattern, note, velocity, tick, endTick: tick, noteId: identity.noteId, channel: identity.channel, expressions: []};
    capturedNotes.push(record);
    const key = owner || captureKey(track, identity.noteId);
    const stack = activeNotes.get(key) || [];
    stack.push(record);
    activeNotes.set(key, stack);
    if (identity.mpe) captureNoteExpression(track, identity, eventTime);
  }
  function captureNoteOff(track, noteId, eventTime, owner) {
    const key = owner || captureKey(track, noteId);
    const stack = activeNotes.get(key);
    if (!stack?.length) return;
    const record = stack.pop();
    record.endTick = Math.max(record.tick, record.expressions.at(-1)?.tick || 0, transportTickAt(eventTime));
    if (!stack.length) activeNotes.delete(key);
  }
  function captureNoteExpression(track, expression, eventTime) {
    const stack = activeNotes.get(captureKey(track, expression.noteId));
    if (!stack?.length) return;
    const record = stack[stack.length - 1];
    const prior = record.expressions[record.expressions.length - 1];
    const tick = Math.max(record.tick, prior?.tick || 0, transportTickAt(eventTime));
    const value = {tick, pitchCents: expression.pitchCents, pressure: expression.pressure, timbre: expression.timbre};
    if (prior && prior.pitchCents === value.pitchCents && prior.pressure === value.pressure && prior.timbre === value.timbre) return;
    record.expressions.push(value);
  }
  function closeOpenNotes() {
    const tick = Math.max(0, transportTickAt());
    for (const stack of activeNotes.values()) for (const record of stack) record.endTick = Math.max(record.tick, record.expressions.at(-1)?.tick || 0, tick);
    activeNotes.clear();
  }
  function recordingsFromBuffer() {
    const groups = new Map();
    for (const note of capturedNotes) {
      const key = `${note.track}\u0000${note.pattern}`;
      if (!groups.has(key)) groups.set(key, {track: note.track, pattern: note.pattern, notes: []});
      groups.get(key).notes.push({tick: Math.round(note.tick), endTick: Math.round(note.endTick), note: note.note, velocity: note.velocity,
        noteId: note.noteId, channel: note.channel, expressions: note.expressions.map(value => ({...value, tick: Math.round(value.tick)}))});
    }
    return [...groups.values()];
  }
  function noteName(note) {
    const names = ['C', 'C♯', 'D', 'D♯', 'E', 'F', 'F♯', 'G', 'G♯', 'A', 'A♯', 'B'];
    return `${names[note % 12]}${Math.floor(note / 12) - 1}`;
  }
  function renderTakePreview() {
    const recordings = recordingsFromBuffer();
    takePreview.replaceChildren();
    if (!recordings.length) { takePanel.hidden = true; return; }
    for (const recording of recordings) {
      const meta = patternMeta.get(recording.pattern) || {steps: 16, drums: false};
      const group = document.createElement('div');
      const heading = document.createElement('strong');
      heading.textContent = `${recording.track} · ${recording.pattern}`;
      group.append(heading);
      const preview = document.createElement('div');
      preview.className = 'take-preview';
      preview.style.setProperty('--steps', meta.steps);
      const byStep = new Map();
      for (const note of recording.notes) {
        const step = midi.quantizeStep(note.tick, meta.steps);
        const text = meta.drums ? midi.mapGeneralMIDIDrum(note.note) || '?' : noteName(note.note);
        const slide = !meta.drums && recording.notes.some(next => note.tick < next.tick && note.endTick > next.tick);
        const values = byStep.get(step) || [];
        values.push({text, slide});
        byStep.set(step, values);
      }
      for (let step = 0; step < meta.steps; step++) {
        const cell = document.createElement('span');
        cell.className = 'take-preview-cell';
        cell.textContent = String(step + 1);
        const notes = byStep.get(step) || [];
        if (notes.length) {
          cell.classList.add('has-note');
          if (notes.some(note => note.slide)) cell.classList.add('slide');
          cell.textContent = notes.map(note => `${note.text}${note.slide ? ' ↗' : ''}`).join(' · ');
          cell.setAttribute('aria-label', `Step ${step + 1}, ${cell.textContent}`);
        } else {
          cell.setAttribute('aria-label', `Step ${step + 1}, empty`);
        }
        preview.append(cell);
      }
      group.append(preview);
      takePreview.append(group);
    }
    const velocityNotice = recordings.some(item => !patternMeta.get(item.pattern)?.drums)
      ? ' Note takes use the score format’s fixed velocity.' : '';
    const expressionNotice = recordings.some(item => item.notes.some(note => note.expressions.length))
      ? ' Expression is sampled on the step grid; detected vibrato uses 5 Hz. Overlapping expressive notes cannot be committed to one pattern.' : '';
    $('#live-take-summary').textContent = `${capturedNotes.length} note${capturedNotes.length === 1 ? '' : 's'} quantized to the nearest pattern step.${velocityNotice}${expressionNotice} Nothing has been written to the score.`;
    takePanel.hidden = false;
  }
  function finishRecording() {
    if (!recording && !capturedNotes.length) return;
    closeOpenNotes();
    recording = false;
    recordButton.setAttribute('aria-pressed', 'false');
    recordButton.textContent = 'Record';
    renderTakePreview();
  }
  recordButton?.addEventListener('click', () => {
    if (!recording) {
      if (!transportState.playing) { setStatus('Press Play before recording a MIDI take', 'error'); return; }
      if (!armedTracks.size) { setStatus('Arm a track before recording a MIDI take', 'error'); return; }
      capturedNotes = [];
      activeNotes.clear();
      recording = true;
      takePanel.hidden = true;
      recordButton.setAttribute('aria-pressed', 'true');
      recordButton.textContent = 'Stop recording';
      setStatus('MIDI take recording in page memory');
      return;
    }
    finishRecording();
  });
  $('#live-take-discard')?.addEventListener('click', () => {
    recording = false;
    capturedNotes = [];
    activeNotes.clear();
    takePanel.hidden = true;
    recordButton.setAttribute('aria-pressed', 'false');
    recordButton.textContent = 'Record';
    announce('Recorded take discarded');
  });
  $('#live-take-commit')?.addEventListener('click', async () => {
    const recordings = recordingsFromBuffer();
    if (!recordings.length) return;
    const button = $('#live-take-commit');
    button.disabled = true;
    try {
      const response = await fetch('/api/record', {
        method: 'POST', headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({revision: revision(), recordings})
      });
      const result = await response.json();
      if (!response.ok) { setStatus(result.error || 'Take commit failed', 'error'); return; }
      const count = capturedNotes.length;
      const destination = recordings.length === 1 ? recordings[0].pattern : `${recordings.length} patterns`;
      document.body.dataset.revision = result.revision;
      announce(`Committed ${count} recorded notes into ${destination}`);
      setStatus(`Committed ${count} recorded notes into ${destination}`, 'success', false);
      capturedNotes = [];
      activeNotes.clear();
      takePanel.hidden = true;
      window.cicadaRefreshProjection?.({revision: result.revision}).catch(() => setStatus('Take committed; the projection could not refresh', 'error', false));
    } catch (error) { setStatus(`Take commit failed: ${error.message}`, 'error'); }
    finally { button.disabled = false; }
  });

  function targetFor(element) {
    const target = element?.closest('[data-midi-address],[data-midi-action]');
    if (!target) return null;
    if (target.dataset.midiAddress) return {address: target.dataset.midiAddress, label: target.dataset.midiAddress};
    if (target.dataset.midiAction) return {action: target.dataset.midiAction, label: target.dataset.baseLabel || target.dataset.midiAction};
    return null;
  }
  let menuTarget = null;
  let learnTarget = null;
  function mappingTargetsEqual(left, right) {
    if (!left || !right) return false;
    return left.address ? left.address === right.address : left.action === right.action;
  }
  function openLearnMenu(element, x, y) {
    const target = targetFor(element);
    if (!target || !isMIDIAvailable) return;
    menuTarget = target;
    learnMenu.hidden = false;
    learnMenu.style.left = `${Math.min(x, window.innerWidth - 190)}px`;
    learnMenu.style.top = `${Math.min(y, window.innerHeight - 100)}px`;
    learnMenuButton.textContent = target.address ? 'Learn CC' : 'Learn note';
    learnMenuButton.focus();
  }
  function closeLearnMenu() { learnMenu.hidden = true; menuTarget = null; }
  learnMenuButton?.addEventListener('click', () => {
    if (!currentMidiAccess) { setStatus('Enable MIDI before learning a controller', 'error'); closeLearnMenu(); return; }
    learnTarget = menuTarget;
    closeLearnMenu();
    updateMIDIStatus();
    announce(learnTarget.address ? `Move a MIDI controller for ${learnTarget.label}` : `Play a MIDI note for ${learnTarget.label}`);
    setStatus(learnTarget.address ? `Learning a controller for ${learnTarget.label}` : `Learning a note for ${learnTarget.label}`);
  });
  clearMenuButton?.addEventListener('click', () => {
    if (menuTarget) {
      mappings.clearTarget(menuTarget);
      renderMappings();
      renderLearnedDots();
      announce(`MIDI mapping cleared for ${menuTarget.label}`);
    }
    closeLearnMenu();
  });
  document.addEventListener('contextmenu', event => {
    const target = targetFor(event.target);
    if (!target || !isMIDIAvailable) return;
    event.preventDefault();
    if (event.shiftKey) {
      mappings.clearTarget(target);
      renderMappings();
      renderLearnedDots();
      announce(`MIDI mapping cleared for ${target.label}`);
    } else openLearnMenu(event.target, event.clientX, event.clientY);
  });
  document.addEventListener('pointerdown', event => {
    if (!isMIDIAvailable || event.pointerType !== 'touch') return;
    const target = targetFor(event.target);
    if (!target) return;
    clearTimeout(longPressTimer);
    longPressElement = event.target;
    longPressTimer = setTimeout(() => {
      longPressOpened = true;
      suppressFollowingClick = true;
      openLearnMenu(event.target, event.clientX || 24, event.clientY || 90);
    }, 550);
  }, {capture: true});
  document.addEventListener('pointerup', () => clearTimeout(longPressTimer), {capture: true});
  document.addEventListener('pointercancel', () => clearTimeout(longPressTimer), {capture: true});
  let longPressTimer = 0;
  let longPressElement = null;
  let longPressOpened = false;
  let suppressFollowingClick = false;
  document.addEventListener('click', event => {
    if (suppressFollowingClick && (longPressElement && longPressElement.contains(event.target) || targetFor(event.target))) {
      event.preventDefault();
      event.stopImmediatePropagation();
      suppressFollowingClick = false;
      longPressOpened = false;
      longPressElement = null;
    }
  }, true);
  document.addEventListener('click', event => {
    if (!learnMenu.hidden && !learnMenu.contains(event.target)) closeLearnMenu();
  });
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && !learnMenu.hidden) closeLearnMenu();
    const live = liveToggle?.getAttribute('aria-pressed') === 'true';
    if (!live || event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement?.tagName || '')) return;
    if (/^F[1-8]$/.test(event.key)) {
      const index = Number(event.key.slice(1)) - 1;
      if (sceneNames[index]) { event.preventDefault(); selectScene(index, false); launchScene(sceneNames[index]); }
      return;
    }
    if (event.shiftKey && event.code === 'Space') {
      event.preventDefault();
      if (sceneNames[selectedSceneIndex]) launchScene(sceneNames[selectedSceneIndex]);
      return;
    }
    if (event.key === 'Enter' && event.target.closest?.('button')) return;
    if (['ArrowLeft', 'ArrowUp', 'ArrowRight', 'ArrowDown'].includes(event.key)) {
      event.preventDefault();
      const direction = event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 1;
      selectScene(selectedSceneIndex + direction, true);
      return;
    }
    if (event.key === 'Enter' && sceneNames[selectedSceneIndex]) {
      event.preventDefault();
      launchScene(sceneNames[selectedSceneIndex]);
    }
  });

  function renderMappings() {
    if (!mappingList) return;
    const current = mappings.all();
    mappingList.replaceChildren();
    if (!current.length) {
      const empty = document.createElement('p');
      empty.className = 'section-note';
      empty.textContent = 'No MIDI mappings yet. Right-click or long-press a pad or live parameter to learn.';
      mappingList.append(empty);
      return;
    }
    for (const mapping of current) {
      const row = document.createElement('div');
      row.className = 'midi-mapping-row';
      const description = document.createElement('span');
      const input = mapping.cc === undefined ? `ch ${mapping.channel + 1} note ${mapping.note}` : `ch ${mapping.channel + 1} CC ${mapping.cc}`;
      description.textContent = `${mapping.device} · ${input} → ${mapping.address || mapping.action}`;
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.textContent = 'Remove';
      remove.setAttribute('aria-label', `Remove MIDI mapping ${input} to ${mapping.address || mapping.action}`);
      remove.addEventListener('click', () => { mappings.remove(mapping); renderMappings(); renderLearnedDots(); });
      row.append(description, remove);
      mappingList.append(row);
    }
  }
  function renderLearnedDots() {
    const all = mappings.all();
    for (const element of $$('[data-midi-address],[data-midi-action]')) {
      const target = targetFor(element);
      if (!target) continue;
      const learned = all.some(mapping => mappingTargetsEqual(mapping, target));
      const parent = element.matches('.live-param-row input') ? element.closest('.live-param-row') : element;
      let dot = parent.querySelector('.midi-learn-dot');
      if (!dot && learned) {
        dot = document.createElement('i');
        dot.className = 'midi-learn-dot';
        dot.setAttribute('aria-label', 'MIDI learned');
        const label = parent.querySelector('label');
        if (label) label.append(dot);
        else parent.append(dot);
      }
      if (dot && !learned) dot.remove();
    }
  }
  renderMappings();

  function storeLearnedInput(device, channel, type, value) {
    if (!learnTarget || !currentMidiAccess) return false;
    if (learnTarget.address && type !== 'cc' || learnTarget.action && type !== 'note') return false;
    const mapping = {
      device, channel,
      ...(type === 'cc' ? {cc: value, address: learnTarget.address} : {note: value, action: learnTarget.action})
    };
    mappings.bind(mapping);
    const target = learnTarget;
    learnTarget = null;
    updateMIDIStatus();
    renderMappings();
    renderLearnedDots();
    announce(`MIDI ${type === 'cc' ? 'controller' : 'note'} learned for ${target.label}`);
    setStatus(`MIDI ${type === 'cc' ? 'controller' : 'note'} learned for ${target.label}`, 'success');
    return true;
  }

  const boundMIDIInputs = new Map();
  function handleMIDIMessage(input, event) {
    if (input.state === 'disconnected') return;
    const data = event.data;
    if (!data || data.length < 2) return;
    flashMIDIActivity();
    const statusByte = data[0] & 0xff;
    const command = statusByte & 0xf0;
    const channel = statusByte & 0x0f;
    const first = data[1] & 0x7f;
    const second = data.length > 2 ? data[2] & 0x7f : 0;
    const device = midiDeviceName(input || event.target);
    const source = input?.id || event.target?.id || device;
    const normalized = mpeInput.process(source, data);
    if (command === 0xb0) {
      if (storeLearnedInput(device, channel, 'cc', first)) return;
      const mapping = deviceMappings(device, channel, 'cc', first);
      if (mapping?.address) dispatchCC(mapping.address, second);
    }
    const noteOn = command === 0x90 && second > 0;
    const noteOff = command === 0x80 || command === 0x90 && second === 0;
    if (noteOn && storeLearnedInput(device, channel, 'note', first)) return;
    const learned = deviceMappings(device, channel, 'note', first);
    if (learned?.action) {
      if (noteOn) invokeMappedAction(learned.action);
      return;
    }
    for (const message of normalized) {
      const time = Number(event.timeStamp) || performance.now();
      if (message.type === 'note-on') {
        const drums = message.channel === 9 && !message.mpe;
        const track = drums ? drumSelect?.value : acidSelect?.value;
        if (!track) { setStatus(drums ? 'No drum track is available' : 'No note track is available', 'error'); continue; }
        if (drums && !midi.mapGeneralMIDIDrum(message.note)) continue;
        if (!drums && !trackByID.get(track)?.pitched) continue;
        // Retain the original track even if the selector changes while held.
        midiNoteTracks.set(message.noteId, {track, drums, note:message.note, identity:message});
        captureNoteOn(track, message.note, message.velocity, time, message);
        sendNoteOn(track, message.note, message.velocity, message);
        if (!drums) sendNoteExpression(track, message);
      } else {
        const target = midiNoteTracks.get(message.noteId);
        if (!target) continue;
        if (message.type === 'note-off') {
          captureNoteOff(target.track, message.noteId, time);
          sendNoteOff(target.track, message.note, message);
          midiNoteTracks.delete(message.noteId);
        } else if (!target.drums) {
          captureNoteExpression(target.track, message, time);
          sendNoteExpression(target.track, message);
        }
      }
    }
  }

  function releaseMIDIInput(input) {
    for (const message of mpeInput.disconnect(input.id || midiDeviceName(input))) {
      const target = midiNoteTracks.get(message.noteId);
      if (!target) continue;
      captureNoteOff(target.track, message.noteId, performance.now());
      sendNoteOff(target.track, message.note, message);
      midiNoteTracks.delete(message.noteId);
    }
    input.onmidimessage = null;
  }
  function bindMIDIInputs(event) {
    if (!currentMidiAccess) return;
    const connected = new Set();
    if (event?.port?.type === 'input' && event.port.state === 'disconnected') releaseMIDIInput(event.port);
    for (const input of currentMidiAccess.inputs.values()) {
      const key = input.id || input.name;
      if (input.state === 'disconnected') { releaseMIDIInput(input); continue; }
      connected.add(key);
      const prior = boundMIDIInputs.get(key);
      if (prior && prior !== input) releaseMIDIInput(prior);
      boundMIDIInputs.set(key, input);
      input.onmidimessage = event => handleMIDIMessage(input, event);
    }
    for (const [key, input] of boundMIDIInputs) if (!connected.has(key)) {
      releaseMIDIInput(input); midiPerformance.disconnect(input); boundMIDIInputs.delete(key);
    }
    updateMIDIStatus();
  }
  if (!isMIDIAvailable) {
    midiControls.hidden = true;
    $('#live-midi-help').hidden = true;
    $('.live-map-details').hidden = true;
    $('#live-acid-track')?.closest('label')?.setAttribute('hidden', '');
    $('#live-drum-track')?.closest('label')?.setAttribute('hidden', '');
    recordButton.hidden = true;
    armControls.hidden = true;
  } else {
    midiControls.hidden = false;
    $('#live-midi-help').hidden = false;
    midiEnable?.addEventListener('click', async () => {
      midiEnable.disabled = true;
      try {
        currentMidiAccess = await navigator.requestMIDIAccess({sysex: false});
        currentMidiAccess.onstatechange = bindMIDIInputs;
        bindMIDIInputs();
        midiEnable.textContent = 'MIDI ready';
        setStatus(`MIDI ready: ${currentMidiAccess.inputs.size} device${currentMidiAccess.inputs.size === 1 ? '' : 's'}`);
      } catch (error) {
        currentMidiAccess = null;
        midiStatus.textContent = 'MIDI unavailable';
        setStatus(`MIDI access failed: ${error.message}`, 'error');
      } finally { midiEnable.disabled = false; }
    });
  }
  window.cicadaAudio?.onError(error => setStatus(error.message || 'Audio message failed', 'error'));

  function connectTransport() {
    const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const socket = new WebSocket(`${scheme}//${location.host}/api/transport/ws`);
    socket.onmessage = event => { try { acceptTransportState(JSON.parse(event.data)); } catch {} };
    socket.onclose = () => { panicInputs(); window.cicadaAudio?.panic?.(); setTimeout(connectTransport, 1000); };
  }
  if (transportState) connectTransport();
  if ($('#transport-pending')) $('#transport-pending').setAttribute('aria-live', 'off');
  renderLaunchStates();
  function refreshLiveProjection() {
    panicInputs(); window.cicadaAudio?.panic?.();
    refreshParameterCatalog();
    readProjection();
    fillTrackSelect(acidSelect, track => track.pitched, settings.acidTrack);
    fillTrackSelect(drumSelect, track => track.drum, settings.drumTrack);
    if (acidSelect) settings.acidTrack = acidSelect.value;
    if (drumSelect) settings.drumTrack = drumSelect.value;
    buildLaunchGrid();
    buildArmControls();
    selectScene(Math.max(0, sceneNames.indexOf(settings.selectedScene)), false);
    renderLaunchStates();
  }
  window.addEventListener('cicada:projectionrefreshed', refreshLiveProjection);
  window.addEventListener('cicada:mixrendered', refreshLiveProjection);
  $('#audio-mode')?.addEventListener('change', refreshParameterCatalog);
  refreshParameterCatalog();
})();
