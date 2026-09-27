(() => {
  'use strict';

  const TARGETS = {streaming: -14, podcast: -16, 'ebu-r128': -23};

  function targetLufs(preset, custom) {
    if (preset in TARGETS) return TARGETS[preset];
    return Number.isFinite(custom) && custom >= -70 && custom <= 0 ? custom : -14;
  }

  function targetDeviation(actual, target) {
    return Number.isFinite(actual) && Number.isFinite(target) ? actual - target : null;
  }

  function targetBand(target, tolerance, min = -60, max = 0) {
    const clamp = value => Math.max(min, Math.min(max, value));
    const width = Number.isFinite(tolerance) ? Math.max(0, tolerance) : 0;
    return {upper: clamp(target + width), lower: clamp(target - width)};
  }

  function createHistoryBuffer(capacity = 600) {
    if (!Number.isSafeInteger(capacity) || capacity < 1) throw new RangeError('history capacity must be a positive integer');
    const times = new Float64Array(capacity);
    const values = new Float64Array(capacity);
    let head = 0;
    let size = 0;
    return {
      push(time, value) {
        if (!Number.isFinite(time)) return;
        times[head] = time;
        values[head] = Number.isFinite(value) ? value : NaN;
        head = (head + 1) % capacity;
        size = Math.min(size + 1, capacity);
      },
      clear() { head = 0; size = 0; },
      get length() { return size; },
      forEachSince(now, duration, callback) {
        const start = (head - size + capacity) % capacity;
        const cutoff = now - duration;
        for (let index = 0; index < size; index++) {
          const slot = (start + index) % capacity;
          if (times[slot] >= cutoff && times[slot] <= now) callback(times[slot], values[slot]);
        }
      }
    };
  }

  function format(value, unit, digits = 1) {
    return Number.isFinite(value) ? `${value.toFixed(digits)} ${unit}` : `— ${unit}`;
  }

  function signed(value, digits = 1) {
    if (!Number.isFinite(value)) return '— LU';
    const rounded = value.toFixed(digits);
    return `${value > 0 ? '+' : ''}${rounded} LU`;
  }

  function mountMaster(document, root) {
    const targetSelect = document.getElementById('loudness-target');
    if (!targetSelect) return;
    const byId = id => document.getElementById(id);
    const customWrap = byId('custom-target-wrap');
    const customInput = byId('custom-target');
    const deviationOutput = byId('target-deviation');
    const toleranceInput = byId('export-tolerance');
    const loudnessCanvas = byId('loudness-history');
    const gainCanvas = byId('gain-reduction-history');
    const announcement = byId('export-announcement');
    const histories = {
      shortTerm: createHistoryBuffer(600),
      compressor: createHistoryBuffer(3600),
      limiter: createHistoryBuffer(3600)
    };
    const storageKey = 'cicada.studio.master.target.v1';
    const saved = readStorage(root.localStorage, storageKey);
    if (saved && typeof saved === 'object') {
      if (saved.preset === 'custom' || saved.preset in TARGETS) targetSelect.value = saved.preset;
      if (Number.isFinite(saved.custom) && saved.custom >= -70 && saved.custom <= 0) customInput.value = String(saved.custom);
    }
    const reducedMotion = root.matchMedia?.('(prefers-reduced-motion: reduce)') || {matches: false, addEventListener() {}};
    let lastHistorySample = -Infinity;
    let lastGainSample = -Infinity;
    let drawPending = false;
    let lastExportAnnouncement = '';
    let polling = false;
    let previousExportState = '';

    function readStorage(storage, key) {
      try { return JSON.parse(storage?.getItem(key) || 'null'); }
      catch { return null; }
    }

    function currentTarget() {
      return targetLufs(targetSelect.value, Number(customInput.value));
    }

    function persistTarget() {
      try { root.localStorage?.setItem(storageKey, JSON.stringify({preset: targetSelect.value, custom: Number(customInput.value)})); }
      catch { /* The selected value remains active for this page. */ }
      customWrap.hidden = targetSelect.value !== 'custom';
      updateDeviation(lastLoudness);
      scheduleDraw();
    }

    let lastLoudness = null;
    function updateDeviation(loudness) {
      if (!deviationOutput) return;
      const deviation = targetDeviation(loudness?.integrated, currentTarget());
      if (deviation === null) {
        deviationOutput.textContent = '— LU';
        return;
      }
      const direction = deviation > 0 ? 'above' : deviation < 0 ? 'below' : 'on target';
      deviationOutput.textContent = `${signed(deviation)} ${direction === 'on target' ? '· on target' : `· ${Math.abs(deviation).toFixed(1)} LU ${direction}`}`;
    }

    function paint(canvas, draw) {
      if (!canvas) return;
      const bounds = canvas.getBoundingClientRect();
      if (!bounds.width || !bounds.height) return;
      const scale = Math.max(1, root.devicePixelRatio || 1);
      const width = Math.round(bounds.width * scale);
      const height = Math.round(bounds.height * scale);
      if (canvas.width !== width || canvas.height !== height) {
        canvas.width = width;
        canvas.height = height;
      }
      const context = canvas.getContext('2d');
      if (!context) return;
      context.setTransform(scale, 0, 0, scale, 0, 0);
      draw(context, bounds.width, bounds.height);
    }

    function drawAxes(context, width, height, labels) {
      const margin = {left: 56, right: 12, top: 18, bottom: 25};
      const chartWidth = Math.max(1, width - margin.left - margin.right);
      const chartHeight = Math.max(1, height - margin.top - margin.bottom);
      context.clearRect(0, 0, width, height);
      context.font = '11px ui-monospace, SFMono-Regular, Consolas, monospace';
      context.textBaseline = 'middle';
      for (const label of labels) {
        const y = margin.top + chartHeight * label.position;
        context.strokeStyle = '#293a30';
        context.lineWidth = 1;
        context.beginPath(); context.moveTo(margin.left, y); context.lineTo(width - margin.right, y); context.stroke();
        context.fillStyle = '#9cad9d';
        context.textAlign = 'right';
        context.fillText(label.text, margin.left - 7, y);
      }
      context.fillStyle = '#9cad9d';
      context.textAlign = 'center';
      context.textBaseline = 'alphabetic';
      context.fillText('60 s', margin.left, height - 6);
      context.fillText('30 s', margin.left + chartWidth / 2, height - 6);
      context.fillText('now', width - margin.right, height - 6);
      return {margin, chartWidth, chartHeight};
    }

    function drawLoudness(context, width, height, now) {
      const area = drawAxes(context, width, height, [
        {text: '0 LUFS', position: 0}, {text: '−20', position: 1 / 3},
        {text: '−40', position: 2 / 3}, {text: '−60', position: 1}
      ]);
      const min = -60, max = 0;
      const yFor = value => area.margin.top + (max - value) / (max - min) * area.chartHeight;
      const target = currentTarget();
      const configuredTolerance = Number(toleranceInput.value);
      const tolerance = Number.isFinite(configuredTolerance) ? Math.max(0, configuredTolerance) : 0.5;
      const band = targetBand(target, tolerance, min, max);
      context.fillStyle = 'rgba(234, 184, 97, .22)';
      context.fillRect(area.margin.left, yFor(band.upper), area.chartWidth, Math.max(1, yFor(band.lower) - yFor(band.upper)));
      context.strokeStyle = '#eab861';
      context.setLineDash([5, 4]);
      context.beginPath(); context.moveTo(area.margin.left, yFor(target)); context.lineTo(width - area.margin.right, yFor(target)); context.stroke();
      context.setLineDash([]);
      context.fillStyle = '#eab861';
      context.textAlign = 'left'; context.textBaseline = 'top';
      context.fillText(`${target.toFixed(1)} LUFS target`, area.margin.left + 6, area.margin.top + 3);
      context.strokeStyle = '#9dd5a6';
      context.lineWidth = 2;
      drawSeries(context, histories.shortTerm, now, area, min, max, yFor);
    }

    function drawSeries(context, history, now, area, min, max, yFor) {
      const start = now - 60_000;
      let drawing = false;
      context.beginPath();
      history.forEachSince(now, 60_000, (time, value) => {
        if (!Number.isFinite(value)) { drawing = false; return; }
        const x = area.margin.left + Math.max(0, time - start) / 60_000 * area.chartWidth;
        const y = yFor(Math.max(min, Math.min(max, value)));
        if (!drawing) context.moveTo(x, y); else context.lineTo(x, y);
        drawing = true;
      });
      context.stroke();
    }

    function drawGainReduction(context, width, height, now) {
      const max = 12;
      const area = drawAxes(context, width, height, [
        {text: '12 dB', position: 0}, {text: '6', position: 0.5}, {text: '0', position: 1}
      ]);
      const yFor = value => area.margin.top + (max - value) / max * area.chartHeight;
      context.lineWidth = 2;
      context.strokeStyle = '#eab861';
      drawSeries(context, histories.compressor, now, area, 0, max, yFor);
      context.strokeStyle = '#ee8b70';
      drawSeries(context, histories.limiter, now, area, 0, max, yFor);
    }

    function scheduleDraw(staticTick = false) {
      if (reducedMotion.matches && !staticTick) return;
      if (drawPending) return;
      drawPending = true;
      root.requestAnimationFrame(() => {
        drawPending = false;
        const now = root.performance.now();
        paint(loudnessCanvas, (context, width, height) => drawLoudness(context, width, height, now));
        paint(gainCanvas, (context, width, height) => drawGainReduction(context, width, height, now));
      });
    }

    function updateMeters(frame) {
      const now = root.performance.now();
      let historyChanged = false;
      if (frame.loudness) {
        const loudness = frame.loudness;
        lastLoudness = loudness;
        byId('master-momentary').textContent = format(loudness.momentary, 'LUFS');
        byId('master-short-term').textContent = format(loudness.short_term, 'LUFS');
        byId('master-integrated').textContent = format(loudness.integrated, 'LUFS');
        byId('master-range').textContent = format(loudness.range, 'LU');
        byId('master-true-peak').textContent = format(loudness.true_peak, 'dBTP');
        byId('master-sample-peak').textContent = format(loudness.sample_peak, 'dBFS');
        byId('master-dropped-blocks').textContent = `${Number(loudness.dropped_blocks) || 0} blocks`;
        updateDeviation(loudness);
        if (now - lastHistorySample >= 100) {
          histories.shortTerm.push(now, loudness.short_term);
          lastHistorySample = now;
          historyChanged = true;
        }
      }
      if (now - lastGainSample >= 1000 / 60) {
        histories.compressor.push(now, frame.master.comp_gr);
        histories.limiter.push(now, frame.master.limiter_gr);
        lastGainSample = now;
        historyChanged = true;
      }
      if (historyChanged) {
        scheduleDraw();
      }
    }

    function renderReport(status) {
      const path = byId('export-path');
      const reportPanel = byId('export-report');
      if (status.path) {
        path.hidden = false;
        path.textContent = status.path;
      }
      const report = status.report;
      if (!report) { reportPanel.hidden = true; return; }
      reportPanel.hidden = false;
      const values = {
        achieved_lufs: format(report.achieved_lufs, 'LUFS'),
        true_peak_dbtp: format(report.true_peak_dbtp, 'dBTP'),
        applied_gain_db: format(report.applied_gain_db, 'dB'),
        passes: `${report.passes} passes`,
        largest_limiter_reduction_db: format(report.largest_limiter_reduction_db, 'dB'),
        dc_correction: report.dc_correction ? `L ${Number(report.dc_correction.left_fs).toFixed(7)} FS · R ${Number(report.dc_correction.right_fs).toFixed(7)} FS` : '—'
      };
      for (const [key, value] of Object.entries(values)) {
        const node = reportPanel.querySelector(`[data-report="${key}"]`);
        if (node) node.textContent = value;
      }
    }

    function showExport(status) {
      const button = byId('export-wav');
      const progress = byId('export-progress');
      button.disabled = status.state === 'queued' || status.state === 'rendering';
      if (status.state === 'queued') progress.textContent = 'Queued…';
      else if (status.state === 'rendering') progress.textContent = `Rendering pass ${status.pass || 1} of ${status.pass_limit || 6}`;
      else if (status.state === 'succeeded') progress.textContent = 'Export complete';
      else if (status.state === 'shortfall') progress.textContent = `Shortfall: ${status.error || 'target not reached'}`;
      else if (status.state === 'failed') progress.textContent = `Export failed: ${status.error || 'render failed'}`;
      else progress.textContent = 'Ready';
      renderReport(status);
      if ((status.state === 'succeeded' || status.state === 'shortfall' || status.state === 'failed') && status.state !== previousExportState) {
        const identity = `${status.state}:${status.path || ''}:${status.error || ''}`;
        if (identity !== lastExportAnnouncement) {
          announcement.textContent = status.state === 'succeeded'
            ? `Export finished. File saved to ${status.path}.`
            : status.state === 'shortfall'
              ? `Export shortfall. ${status.error || 'Target not reached.'} Render saved to ${status.path}.`
              : `Export finished with an error. ${status.error || 'Render failed.'}`;
          lastExportAnnouncement = identity;
        }
      }
      previousExportState = status.state;
    }

    async function refreshExport() {
      if (polling) return;
      polling = true;
      try {
        const response = await root.fetch('/api/export', {cache: 'no-store'});
        if (response.ok) showExport(await response.json());
      } catch { /* Playback and metering remain available while export status is offline. */ }
      finally { polling = false; }
    }

    targetSelect.addEventListener('change', persistTarget);
    customInput.addEventListener('input', persistTarget);
    toleranceInput.addEventListener('input', scheduleDraw);
    byId('reset-integrated').addEventListener('click', () => {
      histories.shortTerm.clear(); histories.compressor.clear(); histories.limiter.clear();
      lastLoudness = null;
      updateDeviation(null);
      root.cicadaAudio?.resetLoudness();
      scheduleDraw();
    });
    byId('export-wav').addEventListener('click', async () => {
      const target = currentTarget();
      const body = {
        target_lufs: target,
        true_peak_max: Number(byId('export-true-peak').value),
        tolerance: Number(toleranceInput.value),
        rate: Number(byId('export-rate').value),
        bits: Number(byId('export-bits').value)
      };
      if (!Number.isFinite(body.true_peak_max) || body.true_peak_max < -24 || body.true_peak_max > 0 || !Number.isFinite(body.tolerance) || body.tolerance < 0 || body.tolerance > 10) {
        byId('export-progress').textContent = 'Check the true-peak ceiling and tolerance values.';
        return;
      }
      try {
        const response = await root.fetch('/api/export', {
          method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)
        });
        const result = await response.json();
        if (!response.ok && response.status !== 409) {
          byId('export-progress').textContent = result.error || 'Export request failed.';
          return;
        }
        if (response.status === 409) byId('export-progress').textContent = 'Another export is already running.';
        showExport(result.job || result);
        await refreshExport();
      } catch (error) {
        byId('export-progress').textContent = `Export request failed: ${error.message}`;
      }
    });
    root.cicadaAudio?.onMeters(updateMeters);
    root.addEventListener('resize', scheduleDraw);
    reducedMotion.addEventListener?.('change', event => { if (!event.matches) scheduleDraw(); });
    root.setInterval(() => { if (reducedMotion.matches) scheduleDraw(true); }, 1000);
    scheduleDraw();
    root.setInterval(refreshExport, 700);
    refreshExport();
    customWrap.hidden = targetSelect.value !== 'custom';
    persistTarget();
    scheduleDraw();
  }

  const api = {createHistoryBuffer, targetBand, targetDeviation, targetLufs, format};
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof window !== 'undefined' && window.document) mountMaster(window.document, window);
})();
