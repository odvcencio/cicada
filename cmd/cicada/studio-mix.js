(() => {
  'use strict';

  const clamp = (value, min, max) => Math.max(min, Math.min(max, value));

  // x is the physical fader travel in units where x=1 is 0 dB.
  function faderPositionToDb(x) {
    return clamp(60 * (clamp(x, 0, 1.1) - 1), -60, 6);
  }
  function faderDbToPosition(db) {
    return clamp(Number(db) / 60 + 1, 0, 1.1);
  }
  function knobValueAt(position, descriptor) {
    const t = clamp(Number(position), 0, 1);
    const min = Number(descriptor.min), max = Number(descriptor.max);
    if (descriptor.curve === 'log' && min > 0 && max > min) return min * Math.pow(max / min, t);
    return min + (max - min) * t;
  }
  function knobPositionAt(value, descriptor) {
    const min = Number(descriptor.min), max = Number(descriptor.max), number = Number(value);
    if (descriptor.curve === 'log' && min > 0 && max > min && number > 0) return clamp(Math.log(number / min) / Math.log(max / min), 0, 1);
    return clamp((number - min) / (max - min || 1), 0, 1);
  }
  function createFrameCoalescer(flush, requestFrame = callback => setTimeout(callback, 0)) {
    const pending = new Map();
    let scheduled = false;
    const drain = () => {
      scheduled = false;
      if (!pending.size) return;
      const values = new Map(pending);
      pending.clear();
      flush(values);
    };
    return {
      push(key, value) {
        pending.set(key, value);
        if (scheduled) return;
        scheduled = true;
        requestFrame(drain);
      },
      flushNow:drain,
      get size() { return pending.size; }
    };
  }

  const api = {faderPositionToDb, faderDbToPosition, knobValueAt, knobPositionAt, createFrameCoalescer};
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof window === 'undefined' || !window.document) return;

  const root = document.getElementById('mix-rack');
  const warning = document.getElementById('mix-warning');
  if (!root || !window.cicadaStudio) return;

  let model = null;
  let addressValues = new Map();
  let descriptors = new Map();
  let liveAddresses = new Set();
  let meterHolds = new Map();
  const raf = window.requestAnimationFrame.bind(window);
  const preview = createFrameCoalescer(values => {
    if (document.body.dataset.playing !== 'true' || !window.cicadaAudio) return;
    for (const [address, value] of values) {
      try { window.cicadaAudio.setParam(address, value); }
      catch (error) { window.cicadaStudio.setStatus(error.message || 'Audio preview failed', 'error'); }
    }
  }, raf);

  const element = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };
  const button = (label, className, onClick) => {
    const node = element('button', className, label);
    node.type = 'button';
    node.addEventListener('click', onClick);
    return node;
  };
  function numeric(value, fallback = 0) {
    if (value === null || value === undefined || value === '') return fallback;
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : fallback;
  }
  function rounded(value, descriptor) {
    const step = Number(descriptor.display_step) || 0.01;
    const result = Math.round(Number(value) / step) * step;
    return Math.abs(result) < step / 1000 ? 0 : Number(result.toFixed(8));
  }
  function unitText(unit) {
    return ({dB:'decibels', Hz:'hertz', ms:'milliseconds', ratio:'ratio', semitone:'semitones'})[unit] || '';
  }
  function delayDivisionMilliseconds(division, tempo) {
    const beats = ({'1/32':0.125, '1/16':0.25, '1/16T':1 / 6, '1/16.':0.375, '1/8':0.5, '1/8T':1 / 3, '1/8.':0.75, '3/16':0.75, '1/4':1, '1/4.':1.5, '1/2':2})[division];
    return beats === undefined || !(tempo > 0) ? null : beats * 60000 / tempo;
  }
  function displayValue(value, descriptor) {
    if (value === null || value === undefined) return '—';
    if (typeof value === 'string') return value;
    const number = Number(value);
    const formatted = Number.isFinite(number) ? String(Number(number.toFixed(3))) : String(value);
    return descriptor.unit ? `${formatted} ${descriptor.unit}` : formatted;
  }
  function valueText(value, descriptor) {
    if (typeof value === 'string') return value;
    const suffix = unitText(descriptor.unit);
    const formatted = Number.isFinite(Number(value)) ? String(Number(Number(value).toFixed(3))) : String(value);
    return suffix ? `${formatted} ${suffix}` : formatted;
  }
  function labelFor(field, descriptor) {
    const source = descriptor.source || field;
    return source.replaceAll('_', ' ').replace(/\b\w/g, letter => letter.toUpperCase());
  }
  function sourceSpanButton(name, range, className = 'mix-strip-name') {
    const target = element('span', className, name);
    target.title = range?.start ? `Show source lines ${range.start}–${range.end}` : 'Show source declaration';
    return target;
  }
  function goToSource(range) {
    const target = document.querySelector(`#source-view [data-source-line="${Number(range?.start) || 1}"]`);
    if (target) {
      target.scrollIntoView({block:'center', behavior:'smooth'});
      target.dataset.highlight = '';
      window.setTimeout(() => target.removeAttribute('data-highlight'), 1800);
    }
    document.getElementById('notation')?.scrollIntoView({block:'start', behavior:'smooth'});
  }
  function stripSummary(strip) {
    const summary = element('summary');
    const name = sourceSpanButton(strip.name, strip.sourceRange);
    summary.append(name, element('span', 'mix-kind', strip.kind));
    summary.addEventListener('click', () => goToSource(strip.sourceRange));
    return summary;
  }
  function makeStrip(strip, builder, extraClass = '') {
    const details = element('details', `mix-strip ${extraClass}`.trim());
    details.open = !window.matchMedia('(max-width: 640px)').matches;
    details.dataset.strip = strip.id;
    details.append(stripSummary(strip));
    const body = element('div', 'mix-strip-body');
    details.append(body);
    builder(body, strip);
    return details;
  }

  function createValueHeader(title, value, descriptor) {
    const header = element('div', 'mix-control-head');
    header.append(element('span', '', title));
    const output = element('output', '', displayValue(value, descriptor));
    header.append(output);
    return {header, output};
  }
  function sliderAttributes(node, descriptor, value) {
    node.setAttribute('role', 'slider');
    node.setAttribute('aria-valuemin', String(descriptor.min));
    node.setAttribute('aria-valuemax', String(descriptor.max));
    node.setAttribute('aria-valuenow', String(value));
    node.setAttribute('aria-valuetext', valueText(value, descriptor));
    node.setAttribute('aria-label', labelFor(descriptor.path, descriptor));
    node.tabIndex = 0;
  }
  function previewAddress(control, value) {
    if (!control.address || !addressValues.has(control.address) || !liveAddresses.has(control.address)) return;
    preview.push(control.address, value);
  }
  function createFader(control, strip, onCommit) {
    const descriptor = control.descriptor;
    const wrapper = element('div', 'mix-control');
    wrapper.dataset.path = control.path;
    const current = numeric(control.value, descriptor.default);
    const {header, output} = createValueHeader(labelFor('level', descriptor), current, descriptor);
    wrapper.append(header);
    const row = element('div', 'mix-fader-wrap');
    const slider = element('div', 'mix-fader');
    slider.setAttribute('role', 'slider');
    slider.setAttribute('aria-label', `${strip.name} level`);
    slider.setAttribute('aria-valuemin', String(descriptor.min));
    slider.setAttribute('aria-valuemax', String(descriptor.max));
    slider.tabIndex = 0;
    const thumb = element('span', 'mix-fader-thumb');
    slider.append(thumb);
    const side = element('div', 'mix-level-side');
    const off = button('Off', 'mix-off', () => {
      const wasMuted = Number(fieldFor(model, `${strip.id}.mute`)?.value) === 1;
      if (strip.kind === 'track' && window.cicadaAudio && document.body.dataset.playing === 'true') window.cicadaAudio.setMute(strip.id, true);
      Promise.resolve(onCommit(control.path, 'off')).then(saved => {
        if (!saved && strip.kind === 'track' && window.cicadaAudio && document.body.dataset.playing === 'true') window.cicadaAudio.setMute(strip.id, wasMuted);
      });
    });
    off.dataset.offFor = strip.id;
    side.append(off);
    row.append(slider, side); wrapper.append(row);

    let value = current, pointer = null, keyTimer = 0, wheelTimer = 0, unmutedAfterOff = false;
    let sourceOff = control.sourceValue === 'off';
    const apply = (next, sendPreview = true) => {
      value = rounded(clamp(next, descriptor.min, descriptor.max), descriptor);
      const position = faderDbToPosition(value);
      thumb.style.top = `${(1 - position / 1.1) * 100}%`;
      output.textContent = `${Number(value.toFixed(2))} dB`;
      slider.setAttribute('aria-valuenow', String(value));
      slider.setAttribute('aria-valuetext', `${Number(value.toFixed(2))} decibels`);
      if (sendPreview) {
        if (strip.kind === 'track' && sourceOff && !unmutedAfterOff && window.cicadaAudio && document.body.dataset.playing === 'true') {
          window.cicadaAudio.setMute(strip.id, false);
          unmutedAfterOff = true;
        }
        previewAddress(control, value);
      }
    };
    const commit = () => { window.clearTimeout(keyTimer); window.clearTimeout(wheelTimer); onCommit(control.path, value); };
    const setFromPointer = event => {
      const rect = slider.getBoundingClientRect();
      if (pointer.fine || event.shiftKey) {
        const delta = (pointer.startY - event.clientY) / Math.max(100, rect.height) * 66 * 0.1;
        apply(pointer.startValue + delta);
      } else {
        const physicalX = clamp((rect.bottom - event.clientY) / Math.max(1, rect.height), 0, 1) * 1.1;
        apply(faderPositionToDb(physicalX));
      }
    };
    slider.addEventListener('pointerdown', event => {
      if (control.supported === false) return;
      pointer = {startY:event.clientY, startValue:value, fine:event.shiftKey};
      slider.setPointerCapture(event.pointerId);
      setFromPointer(event);
      event.preventDefault();
    });
    slider.addEventListener('pointermove', event => { if (pointer) setFromPointer(event); });
    slider.addEventListener('pointerup', event => { if (!pointer) return; pointer = null; event.preventDefault(); commit(); });
    slider.addEventListener('pointercancel', () => { if (!pointer) return; pointer = null; apply(current, true); });
    slider.addEventListener('wheel', event => {
      event.preventDefault();
      apply(value + (event.deltaY < 0 ? 0.5 : -0.5));
      window.clearTimeout(wheelTimer); wheelTimer = window.setTimeout(commit, 400);
    }, {passive:false});
    slider.addEventListener('dblclick', event => { event.preventDefault(); apply(descriptor.default); commit(); });
    slider.addEventListener('keydown', event => {
      let next;
      const step = event.shiftKey ? 0.1 : 0.5;
      switch (event.key) {
        case 'ArrowUp': case 'ArrowRight': next = value + step; break;
        case 'ArrowDown': case 'ArrowLeft': next = value - step; break;
        case 'PageUp': next = value + 3; break;
        case 'PageDown': next = value - 3; break;
        case 'Home': next = descriptor.min; break;
        case 'End': next = descriptor.max; break;
        case 'Enter': event.preventDefault(); commit(); return;
        default: return;
      }
      event.preventDefault(); apply(next);
      window.clearTimeout(keyTimer); keyTimer = window.setTimeout(commit, 400);
    });
    wrapper._sync = nextControl => {
      const nextValue = numeric(nextControl?.value, descriptor.default);
      sourceOff = nextControl?.sourceValue === 'off';
      unmutedAfterOff = false;
      apply(nextValue, false);
      const mute = fieldFor(model, `${strip.id}.mute`);
      off.setAttribute('aria-pressed', String(Number(mute?.value) === 1));
    };
    wrapper._sync(control);
    return wrapper;
  }

  function createKnob(control, onCommit, options = {}) {
    const descriptor = control.descriptor;
    const wrapper = element('div', 'mix-control');
    wrapper.dataset.path = control.path;
    let value = numeric(control.value, descriptor.default);
    const {header, output} = createValueHeader(options.label || labelFor(control.path, descriptor), value, descriptor);
    wrapper.append(header);
    const knob = element('div', 'mix-knob');
    knob.tabIndex = 0;
    sliderAttributes(knob, descriptor, value);
    knob.setAttribute('aria-label', `${options.owner ? `${options.owner} ` : ''}${options.label || labelFor(control.path, descriptor)}`);
    knob.textContent = displayValue(value, descriptor);
    wrapper.append(knob);
    let pointer = null, keyTimer = 0, wheelTimer = 0;
    const apply = (next, sendPreview = true) => {
      value = rounded(clamp(next, descriptor.min, descriptor.max), descriptor);
      output.textContent = displayValue(value, descriptor);
      knob.textContent = displayValue(value, descriptor);
      knob.setAttribute('aria-valuenow', String(value));
      knob.setAttribute('aria-valuetext', valueText(value, descriptor));
      if (sendPreview) previewAddress(control, value);
    };
    const commit = () => { window.clearTimeout(keyTimer); window.clearTimeout(wheelTimer); onCommit(control.path, value); };
    knob.addEventListener('pointerdown', event => {
      pointer = {y:event.clientY, position:knobPositionAt(value, descriptor), fine:event.shiftKey};
      knob.setPointerCapture(event.pointerId); event.preventDefault();
      const rect = knob.getBoundingClientRect();
      const factor = pointer.fine ? 0.1 : 1;
      apply(knobValueAt(pointer.position + (pointer.y - event.clientY) / Math.max(1, rect.height) * factor, descriptor));
    });
    knob.addEventListener('pointermove', event => {
      if (!pointer) return;
      const factor = pointer.fine || event.shiftKey ? 0.1 : 1;
      const position = clamp(pointer.position + (pointer.y - event.clientY) / 180 * factor, 0, 1);
      apply(knobValueAt(position, descriptor));
    });
    knob.addEventListener('pointerup', event => { if (!pointer) return; pointer = null; event.preventDefault(); commit(); });
    knob.addEventListener('pointercancel', () => { if (pointer) { pointer = null; apply(numeric(control.value, descriptor.default), true); } });
    knob.addEventListener('wheel', event => {
      event.preventDefault();
      const delta = event.deltaY < 0 ? 0.01 : -0.01;
      apply(knobValueAt(knobPositionAt(value, descriptor) + delta * (event.shiftKey ? 0.1 : 1), descriptor));
      window.clearTimeout(wheelTimer); wheelTimer = window.setTimeout(commit, 400);
    }, {passive:false});
    knob.addEventListener('dblclick', event => { event.preventDefault(); apply(descriptor.default); commit(); });
    knob.addEventListener('keydown', event => {
      const step = (Number(descriptor.display_step) || .01) * (event.shiftKey ? .1 : 1);
      let next;
      switch (event.key) {
        case 'ArrowUp': case 'ArrowRight': next = value + step; break;
        case 'ArrowDown': case 'ArrowLeft': next = value - step; break;
        case 'PageUp': next = value + step * 3; break;
        case 'PageDown': next = value - step * 3; break;
        case 'Home': next = descriptor.min; break;
        case 'End': next = descriptor.max; break;
        case 'Enter': event.preventDefault(); commit(); return;
        default: return;
      }
      event.preventDefault(); apply(next);
      window.clearTimeout(keyTimer); keyTimer = window.setTimeout(commit, 400);
    });
    wrapper._sync = nextControl => apply(numeric(nextControl?.value, descriptor.default), false);
    return wrapper;
  }

  function fieldFor(source, path) {
    if (!source) return null;
    for (const track of source.tracks || []) if (track.fields[path.split('.')[1]]?.path === path) return track.fields[path.split('.')[1]];
    for (const bus of source.buses || []) {
      const field = bus.fields[path.slice(bus.id.length + 1)];
      if (field?.path === path) return field;
    }
    const masterField = source.master?.fields?.[path.slice('master.'.length)];
    if (masterField?.path === path) return masterField;
    for (const effect of source.effects || []) {
      const field = effect.fields.find(item => item.path === path);
      if (field) return field;
    }
    for (const track of source.tracks || []) {
      const send = track.sends?.find(item => item.path === path);
      if (send) return send;
    }
    return null;
  }
  function automaticMakeupValue(effectName) {
    const effect = model?.effects?.find(item => item.id === effectName);
    const value = field => Number(effect?.fields?.find(item => item.path === `${effectName}.${field}`)?.value);
    const threshold = Number.isFinite(value('threshold')) ? value('threshold') : -18;
    const ratio = Number.isFinite(value('ratio')) && value('ratio') > 0 ? value('ratio') : 4;
    return -(threshold * (1 - 1 / ratio)) / 2;
  }

  function allFields(source) {
    const output = [];
    for (const strip of [...(source.tracks || []), ...(source.buses || []), source.master]) {
      if (!strip) continue;
      output.push(...Object.values(strip.fields || {}));
      output.push(...(strip.sends || []));
    }
    for (const effect of source.effects || []) output.push(...effect.fields);
    return output;
  }

  async function commit(path, value, structure = false, previewAfter = null) {
    const result = await window.cicadaStudio.commitMixer(path, value, previewAfter);
    if (!result) return false;
    await refreshModel();
    if (structure) renderMixer();
    else syncControls();
    return result;
  }

  function unsupported(parent, reason) {
    if (!reason) return;
    parent.append(element('div', 'mix-unsupported', reason));
  }

  function createChoiceControl(control, parent, options = {}) {
    const wrapper = element('label', 'mix-control');
    wrapper.dataset.path = control.path;
    const descriptor = control.descriptor;
    const caption = element('span', 'mix-control-head', options.label || labelFor(control.path, descriptor));
    const select = element('select');
    select.setAttribute('aria-label', `${options.owner || ''} ${caption.textContent}`.trim());
    const values = control.choices || descriptor.values || [];
    const sourceCurrent = control.sourceValue || control.value;
    let selected = String(sourceCurrent ?? '');
    if (descriptor.curve === 'toggle') selected = sourceCurrent === 'true' || sourceCurrent === 'on' || Number(sourceCurrent) === 1 ? 'true' : 'false';
    if (control.path.endsWith('.out')) selected = String(sourceCurrent || control.value || 'music');
    if (control.path.endsWith('.insert')) selected = String(sourceCurrent || control.value || 'none');
    if (control.path.endsWith('.level') && control.choices) {
      if (!control.sourceValue) selected = control.path.startsWith('music.') ? '-3dB' : '';
      if (control.sourceValue && control.sourceValue.toLowerCase() === 'off') selected = 'off';
    }
    if (descriptor.source === 'sidechain') {
      for (const choice of [...new Set([...values, ...(model.tracks || []).map(track => track.id)])]) {
        addOption(select, choice, choice);
      }
    } else if (control.path.endsWith('.level') && control.choices?.length === 1 && !control.sourceValue) {
      addOption(select, '', `${Number(control.value) || 0} dB (default)`, true);
      for (const choice of values) addOption(select, choice, choice === 'off' ? 'Off' : choice);
    } else {
      for (const choice of values) addOption(select, choice, choice === 'off' ? 'Off' : choice);
    }
    select.value = selected;
    select.addEventListener('change', () => {
      if (!select.value) return;
      let value = select.value;
      if (control.path.endsWith('.level') && value === '-3dB') value = '-3dB';
      const previewValue = descriptor.curve === 'toggle' ? (value === 'true' ? 1 : 0) : Number.isFinite(Number(value)) ? Number(value) : value;
      previewAddress(control, previewValue);
      commit(control.path, value, false);
    });
    wrapper.append(caption, select);
    wrapper._sync = nextControl => {
      const freshValue = nextControl?.sourceValue || nextControl?.value;
      if (freshValue !== undefined && freshValue !== null && [...select.options].some(option => option.value === String(freshValue))) select.value = String(freshValue);
      else if (nextControl?.choices?.length === 1 && !nextControl.sourceValue) select.value = '';
    };
    parent.append(wrapper);
    return wrapper;
  }
  function addOption(select, value, label, disabled = false) {
    const option = element('option', '', label);
    option.value = String(value); option.disabled = disabled;
    select.append(option);
  }

  function createParameterControl(control, parent, options = {}) {
    if (!control.supported) {
      unsupported(parent, control.reason || 'CICADA-UNSUPPORTED: setting is unavailable');
      return null;
    }
    const descriptor = control.descriptor;
    if (control.choices?.length || descriptor.curve === 'enum' || descriptor.curve === 'toggle' || descriptor.source === 'sidechain') {
      return createChoiceControl(control, parent, options);
    }
    if (descriptor.source === 'makeup' && descriptor.values?.includes('auto')) {
      const wrap = element('div', 'mix-control'); wrap.dataset.path = control.path;
      const current = element('div', 'mix-control-head', 'Makeup');
      const auto = button('Auto', 'mix-toggle', () => {
        previewAddress(control, automaticMakeupValue(options.owner));
        commit(control.path, 'auto');
      });
      auto.setAttribute('aria-label', `${options.owner || ''} automatic makeup`.trim());
      auto.setAttribute('aria-pressed', String(control.sourceValue === 'auto'));
      wrap.append(current, auto);
      const knob = createKnob(control, commit, {owner:options.owner, label:'Manual makeup'}); wrap.append(knob);
      wrap._sync = next => {
        auto.setAttribute('aria-pressed', String(next?.sourceValue === 'auto'));
        knob._sync?.(next);
      };
      parent.append(wrap); return wrap;
    }
    if (descriptor.source === 'time' && descriptor.values?.length) {
      const selectWrap = element('label', 'mix-control');
      const label = element('span', 'mix-control-head', 'Delay division');
      const select = element('select'); select.setAttribute('aria-label', `${options.owner || ''} delay division`.trim());
      for (const division of descriptor.values) addOption(select, division, division);
      addOption(select, '__custom', 'Custom milliseconds');
      const sourceCurrent = control.sourceValue || '';
      select.value = descriptor.values.includes(sourceCurrent) ? sourceCurrent : '__custom';
      select.addEventListener('change', () => {
        if (select.value === '__custom') return;
        const milliseconds = delayDivisionMilliseconds(select.value, model.tempo);
        if (milliseconds !== null) previewAddress(control, milliseconds);
        commit(control.path, select.value);
      });
      selectWrap.append(label, select); parent.append(selectWrap);
      const knob = createKnob(control, commit, {owner:options.owner, label:'Delay time'}); parent.append(knob);
      selectWrap._sync = next => { select.value = descriptor.values.includes(next?.sourceValue) ? next.sourceValue : '__custom'; };
      return [selectWrap, knob];
    }
    const knob = createKnob(control, commit, options);
    parent.append(knob);
    return knob;
  }

  function createToggle(control, owner, labelText) {
    const on = switchOn(control.value);
    const toggle = button(labelText, 'mix-toggle', () => {
      const next = toggle.getAttribute('aria-pressed') !== 'true';
      if (document.body.dataset.playing === 'true' && window.cicadaAudio) {
        if (labelText === 'M') window.cicadaAudio.setMute(owner.id, next);
        else if (labelText === 'S') window.cicadaAudio.setSolo(owner.id, next);
      }
      Promise.resolve(commit(control.path, next)).then(saved => {
        if (!saved && document.body.dataset.playing === 'true' && window.cicadaAudio) {
          if (labelText === 'M') window.cicadaAudio.setMute(owner.id, !next);
          else if (labelText === 'S') window.cicadaAudio.setSolo(owner.id, !next);
        }
      });
    });
    toggle.setAttribute('aria-label', `${owner.name} ${labelText === 'M' ? 'mute' : 'solo'}`);
    toggle.setAttribute('aria-pressed', String(on));
    toggle._sync = next => toggle.setAttribute('aria-pressed', String(switchOn(next?.value)));
    return toggle;
  }
  function switchOn(value) { return value === true || value === 'on' || value === 'true' || Number(value) === 1; }

  function createFaderForStrip(strip, body) {
    const level = strip.fields.level;
    if (!level) return;
    if (!level.supported) { unsupported(body, level.reason); return; }
    if (level.choices?.length) {
      createChoiceControl(level, body, {owner:strip.name});
      return;
    }
    const control = createFader(level, strip, commit);
    body.append(control);
  }

  function addMixButtons(strip, body) {
    if (strip.kind !== 'track') return;
    const buttons = element('div', 'mix-buttons');
    if (strip.fields.mute) buttons.append(createToggle(strip.fields.mute, strip, 'M'));
    if (strip.fields.solo) buttons.append(createToggle(strip.fields.solo, strip, 'S'));
    if (buttons.childNodes.length) body.append(buttons);
  }

  function createPanControl(strip, body) {
    const pan = strip.fields.pan;
    if (!pan) return;
    if (!pan.supported) { unsupported(body, pan.reason); return; }
    createParameterControl(pan, body, {owner:strip.name, label:'Pan'});
  }

  function createOutControl(strip, body) {
    const control = strip.fields.out;
    if (!control) return;
    if (!control.supported) { unsupported(body, control.reason); return; }
    createChoiceControl(control, body, {owner:strip.name, label:'Output bus'});
  }

  function insertChoices(strip) {
    if (strip.kind === 'track') return model.effects.filter(effect => effect.kind === 'drive').map(effect => effect.id);
    if (strip.kind === 'bus' && strip.id === 'music') return model.effects.filter(effect => effect.kind === 'comp').map(effect => effect.id);
    return [];
  }
  function createInsertChain(strip, body) {
    const chain = element('div', 'mix-chain');
    chain.dataset.path = strip.insertPath;
    const title = element('span', 'mix-control-head mix-section-title', strip.kind === 'bus' ? 'Music bus insert' : 'Insert chain');
    chain.append(title);
    const names = [...(strip.inserts || [])];
    const eligible = strip.insertSupported && (strip.kind === 'track' || strip.kind === 'bus' && strip.id === 'music');
    names.forEach((name, index) => {
      const row = element('div', 'mix-chain-item');
      row.dataset.index = String(index); row.draggable = names.length > 1 && eligible;
      row.append(element('span', 'mix-chain-label', `${index + 1}. ${name}`));
      if (eligible) row.append(button('Remove', 'mix-remove', () => commit(strip.insertPath, names.filter(item => item !== name), true)));
      if (row.draggable) wireChainDrag(row, chain, strip, names);
      chain.append(row);
    });
    if (!names.length && (strip.kind === 'bus' && strip.id === 'music' || strip.kind === 'track')) {
      const choices = insertChoices(strip);
      if (eligible && choices.length) {
        const select = element('select'); select.setAttribute('aria-label', `${strip.name} add insert`);
        addOption(select, '', '+ Insert');
        for (const choice of choices) addOption(select, choice, choice);
        select.addEventListener('change', () => { if (select.value) commit(strip.insertPath, [select.value], true); });
        chain.append(select);
      } else if (strip.kind === 'track' && eligible) {
        chain.append(button('+ Drive effect', 'mix-add', () => addEffect('drive', 'drive')));
      } else if (strip.kind === 'bus' && strip.id === 'music') {
        chain.append(button('+ Bus compressor', 'mix-add', () => addEffect('glue', 'comp', 'music')));
      }
    }
    if (strip.kind === 'master') chain.append(element('span', 'mix-chain-label', 'Safety limiter · fixed last'));
    if (!strip.insertSupported) unsupported(chain, strip.insertReason);
    if (names.length > 1 && !eligible) unsupported(chain, strip.insertReason || 'CICADA-UNSUPPORTED: insert chain is unavailable');
    body.append(chain);
  }
  function wireChainDrag(row, chain, strip, original) {
    let pointer = null;
    row.addEventListener('pointerdown', event => {
      if (event.target.closest('button')) return;
      pointer = {id:event.pointerId, start:Number(row.dataset.index), target:Number(row.dataset.index)};
      row.setPointerCapture(event.pointerId); row.dataset.dragging = ''; event.preventDefault();
    });
    row.addEventListener('pointermove', event => {
      if (!pointer) return;
      const rows = [...chain.querySelectorAll('.mix-chain-item')];
      const target = rows.find(candidate => {
        const rect = candidate.getBoundingClientRect(); return event.clientY >= rect.top && event.clientY <= rect.bottom;
      });
      if (target) pointer.target = Number(target.dataset.index);
    });
    row.addEventListener('pointercancel', () => { pointer = null; delete row.dataset.dragging; });
    row.addEventListener('pointerup', () => {
      if (!pointer) return;
      const {start, target} = pointer; pointer = null; delete row.dataset.dragging;
      if (start === target) return;
      const next = [...original]; const [item] = next.splice(start, 1); next.splice(target, 0, item);
      commit(strip.insertPath, next, true);
    });
  }

  function createSends(strip, body) {
    const sends = element('div', 'mix-sends');
    sends.append(element('span', 'mix-control-head mix-section-title', 'Sends'));
    for (const send of strip.sends || []) {
      const row = element('div', 'mix-send'); row.dataset.path = send.path;
      row.append(element('span', 'mix-send-title', send.to));
      if (!send.supported) unsupported(row, send.reason);
      else {
        const knobControl = {...send, value:send.value ?? 0, descriptor:send.descriptor};
        const knob = createKnob(knobControl, async (path, amount) => {
          await commit(path, {level:amount, pre:send.pre}, false);
        }, {owner:strip.name, label:'Send level'});
        row.append(knob);
        const actions = element('div', 'mix-send-actions');
        const pre = button('Pre', 'mix-pre', () => commit(send.path, {level:send.level, pre:pre.getAttribute('aria-pressed') !== 'true'}));
        pre.setAttribute('aria-pressed', String(send.pre));
        pre.setAttribute('aria-label', `${strip.name} ${send.to} pre-fader send`);
        pre._sync = next => pre.setAttribute('aria-pressed', String(next?.pre));
        actions.append(pre, button('Remove', 'mix-remove', () => commit(send.path, {remove:true}, true)));
        row.append(actions);
        row._sync = next => { knob._sync?.({...next, value:next.value}); pre._sync?.(next); };
      }
      sends.append(row);
    }
    const used = new Set((strip.sends || []).map(send => send.to));
    const usedKinds = new Set((strip.sends || []).map(send => send.kind));
    const destinations = model.effects.filter(effect => (effect.kind === 'delay' || effect.kind === 'reverb') && !used.has(effect.id) && !usedKinds.has(effect.kind));
    if (destinations.length) {
      const select = element('select'); select.setAttribute('aria-label', `${strip.name} add send`);
      addOption(select, '', '+ Send');
      for (const target of destinations) addOption(select, target.id, `${target.name} · ${target.kind}`);
      select.addEventListener('change', () => {
        if (select.value) commit(`${strip.id}.send.${select.value}`, {level:0, pre:false}, true);
      });
      sends.append(select);
    }
    for (const kind of ['delay', 'reverb']) {
      if (model.effects.some(effect => effect.kind === kind)) continue;
      sends.append(button(`+ ${kind === 'delay' ? 'Delay' : 'Reverb'}`, 'mix-add', () => addEffect(kind, kind)));
    }
    body.append(sends);
  }

  function addEffect(name, kind, insertOn = '') {
    const ids = new Set([
      ...model.effects.map(effect => effect.id),
      ...(model.tracks || []).map(strip => strip.id),
      ...(model.buses || []).map(strip => strip.id),
      model.master?.id
    ]);
    let id = name;
    if (ids.has(id)) id = `${name}-return`;
    let suffix = 2;
    while (ids.has(id)) id = `${name}-${suffix++}`;
    commit(`fx.${id}`, {kind, ...(insertOn ? {insertOn} : {})}, true);
  }

  function makeMeter(kind, id, label) {
    const meter = element('div', 'mix-meter');
    meter.dataset.meterKind = kind; meter.dataset.meterId = id;
    meter.setAttribute('role', 'meter'); meter.setAttribute('aria-label', `${label} peak level`);
    meter.setAttribute('aria-valuemin', '-60'); meter.setAttribute('aria-valuemax', '0'); meter.setAttribute('aria-valuenow', '-60');
    const heading = element('span', '', label);
    const track = element('div', 'mix-meter-track'); const fill = element('i'); const hold = element('b');
    track.append(fill, hold);
    const output = element('output', '', '−120 dBFS peak · −120 dBFS RMS');
    meter.append(heading, track, output);
    return meter;
  }
  function makeGR(label, key, ceiling = '') {
    const row = element('div', 'mix-gr');
    row.dataset.gr = key;
    row.setAttribute('role', 'meter'); row.setAttribute('aria-valuemin', '0'); row.setAttribute('aria-valuemax', '24'); row.setAttribute('aria-valuenow', '0');
    row.append(element('span', '', ceiling ? `${label} · ${ceiling}` : label), element('output', '', '0.0 dB')); return row;
  }
  function meterValue(frame, kind, id) {
    if (kind === 'master') return {peak:frame.master.peak, rms:frame.master.rms || 0, over:frame.master.over};
    return frame[kind]?.[id] || null;
  }
  function updateMeter(meter, value, now) {
    if (!value) return;
    const peak = Number(value.peak), rms = Number(value.rms);
    let hold = meterHolds.get(meter);
    if (!hold || peak >= hold.value || now >= hold.until) hold = {value:peak, until:now + 1000};
    meterHolds.set(meter, hold);
    const percent = value => clamp((value + 60) / 60 * 100, 0, 100);
    meter.querySelector('.mix-meter-track i').style.width = `${percent(peak)}%`;
    meter.querySelector('.mix-meter-track b').style.left = `${percent(hold.value)}%`;
    meter.querySelector('output').textContent = `${peak.toFixed(1)} dBFS peak · ${rms.toFixed(1)} dBFS RMS`;
    meter.setAttribute('aria-valuenow', String(clamp(peak, -60, 0)));
    meter.setAttribute('aria-valuetext', `${peak.toFixed(1)} decibels peak, ${rms.toFixed(1)} decibels RMS`);
  }
  function updateMeters(frame) {
    const now = performance.now();
    for (const meter of root.querySelectorAll('[data-meter-kind]')) {
      updateMeter(meter, meterValue(frame, meter.dataset.meterKind, meter.dataset.meterId), now);
    }
    for (const meter of root.querySelectorAll('[data-gr]')) {
      const db = Number(frame.master[meter.dataset.gr]) || 0;
      meter.querySelector('output').textContent = `${db.toFixed(1)} dB`;
      meter.setAttribute('aria-valuenow', String(Math.max(0, db)));
      meter.setAttribute('aria-valuetext', `${Math.max(0, db).toFixed(1)} decibels gain reduction`);
    }
    const over = root.querySelector('.mix-meter-over');
    if (over) {
      over.dataset.over = String(Boolean(frame.master.over));
      over.textContent = frame.master.over ? 'Pre-limiter over' : 'No pre-limiter over';
    }
  }

  function fieldSection(body, title) { body.append(element('span', 'mix-control-head mix-section-title', title)); }
  function renderTrack(strip) {
    return makeStrip(strip, (body, current) => {
      if (current.kind === 'track') {
        const fader = createFader(current.fields.level, current, commit);
        body.append(fader);
        addMixButtons(current, body);
        createPanControl(current, body);
        createOutControl(current, body);
        createInsertChain(current, body);
        createSends(current, body);
      } else if (current.kind === 'bus') {
        createFaderForStrip(current, body);
        addMixButtons(current, body);
        if (current.fields.pan) createPanControl(current, body);
        createInsertChain(current, body);
        for (const field of Object.values(current.fields)) {
          if (['level','mute','solo','insert'].includes(field.path.slice(current.id.length + 1))) continue;
          if (!field.supported) unsupported(body, field.reason);
        }
      } else if (current.kind === 'master') {
        createFaderForStrip(current, body);
        addMixButtons(current, body);
        createInsertChain(current, body);
        if (!model.effects.some(effect => effect.kind === 'comp')) {
          body.append(button('+ Bus compressor', 'mix-add', () => addEffect('glue', 'comp', 'music')));
        }
        body.append(makeGR('Bus compressor', 'comp_gr'));
        body.append(makeGR('Limiter gain reduction', 'limiter_gr', '−0.3 dBFS ceiling'));
        body.append(makeMeter('master', 'master', 'Output'));
        const lamp = element('div', 'mix-meter-over', 'No pre-limiter over');
        lamp.setAttribute('role','status'); body.append(lamp);
        for (const field of Object.values(current.fields)) {
          if (['level','mute','solo','insert'].includes(field.path.slice('master.'.length))) continue;
          if (!field.supported) unsupported(body, field.reason);
        }
      }
      body.append(makeMeter(current.kind === 'track' ? 'tracks' : 'buses', current.meter, current.name));
    }, strip.kind === 'master' ? 'master-strip' : '');
  }

  function renderEffect(effect) {
    const strip = {id:effect.id, name:effect.name, kind:effect.kind === 'delay' || effect.kind === 'reverb' ? 'return' : 'effect', sourceRange:effect.sourceRange};
    return makeStrip(strip, body => {
      for (const control of effect.fields) createParameterControl(control, body, {owner:effect.name});
      if (effect.meter) body.append(makeMeter('returns', effect.meter, effect.name));
    });
  }

  function renderMixer() {
    if (!model) return;
    const strips = [
      ...(model.tracks || []).map(renderTrack),
      ...(model.buses || []).map(renderTrack),
      ...(model.effects || []).map(renderEffect),
      renderTrack(model.master)
    ];
    root.replaceChildren(...strips);
    const savedSolo = [...(model.tracks || []), ...(model.buses || []), model.master].some(strip => Number(strip.fields?.solo?.value) === 1);
    warning.hidden = !savedSolo;
    warning.textContent = savedSolo ? 'CICADA-SOLO: a saved solo switch is on. Render and check report this warning.' : '';
    root.setAttribute('aria-busy', 'false');
  }

  function syncControls() {
    const fields = new Map(allFields(model).map(control => [control.path, control]));
    for (const node of root.querySelectorAll('[data-path]')) {
      const control = fields.get(node.dataset.path);
      if (control) node._sync?.(control);
    }
    const savedSolo = [...(model.tracks || []), ...(model.buses || []), model.master].some(strip => Number(strip.fields?.solo?.value) === 1);
    warning.hidden = !savedSolo;
    warning.textContent = savedSolo ? 'CICADA-SOLO: a saved solo switch is on. Render and check report this warning.' : '';
  }

  async function refreshModel() {
    const [mixerResponse, paramsResponse] = await Promise.all([
      fetch('/api/mixer', {cache:'no-store'}),
      fetch('/api/params', {cache:'no-store'})
    ]);
    const [nextModel, params] = await Promise.all([mixerResponse.json(), paramsResponse.json()]);
    if (!mixerResponse.ok) throw new Error(nextModel.error || 'Cannot load the mixer');
    if (!paramsResponse.ok) throw new Error('Cannot load parameter addresses');
    model = nextModel;
    model.tempo = params.tempo || nextModel.tempo || 120;
    model.addresses = params.addresses || [];
    model.registry = params.registry || [];
    addressValues = new Map(model.addresses.map(address => [address.address, address.value]));
    descriptors = new Map(model.registry.map(descriptor => [descriptor.id, descriptor]));
    liveAddresses = new Set(model.addresses.filter(address => descriptors.get(address.param)?.live).map(address => address.address));
  }

  window.cicadaAudio?.onMeters(updateMeters);
  refreshModel().then(renderMixer).catch(error => {
    root.setAttribute('aria-busy', 'false');
    root.replaceChildren(element('p', 'mix-unsupported', error.message));
  });
})();
