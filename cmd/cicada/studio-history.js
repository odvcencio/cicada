(function (root) {
  'use strict';

  function editorTarget(target, editor) {
    if (!target) return false;
    if (editor && (target === editor || editor.contains?.(target))) return true;
    if (typeof target.closest === 'function') {
      return Boolean(target.closest('#source-editor, [data-code-editor], .CodeMirror, .cm-editor, [contenteditable="true"]'));
    }
    return target.id === 'source-editor' || target.isContentEditable === true;
  }

  function bindKeys(document, callbacks, editor) {
    function onKeydown(event) {
      if (!(event.ctrlKey || event.metaKey) || event.altKey || event.key.toLowerCase() !== 'z' || editorTarget(event.target, editor)) return;
      event.preventDefault();
      if (event.shiftKey) callbacks.redo?.();
      else callbacks.undo?.();
    }
    document.addEventListener('keydown', onKeydown);
    return () => document.removeEventListener('keydown', onKeydown);
  }

  function timeLabel(value) {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? '' : date.toLocaleTimeString();
  }

  function renderHistory(list, state, options = {}) {
    const document = options.document || list.ownerDocument;
    const rows = [];
    const edits = [...(state.edits || [])].sort((a, b) => Number(b.id) - Number(a.id));
    if (!edits.length) {
      const empty = document.createElement('p');
      empty.className = 'history-empty';
      empty.textContent = 'No source edits yet.';
      rows.push(empty);
    }
    for (const entry of edits) {
      const row = document.createElement('article');
      row.className = 'history-row';
      row.dataset.id = String(entry.id);

      const heading = document.createElement('div');
      heading.className = 'history-entry-heading';
      const label = document.createElement('strong');
      label.className = 'history-label';
      label.textContent = entry.label || 'Source edited';
      const time = document.createElement('time');
      time.className = 'history-time';
      time.dateTime = entry.at || '';
      time.textContent = timeLabel(entry.at);
      heading.append(label, time);

      const revisions = document.createElement('small');
      revisions.className = 'history-revisions';
      revisions.textContent = `${String(entry.revisionBefore || '').slice(0, 8)} → ${String(entry.revisionAfter || '').slice(0, 8)}`;

      const diff = document.createElement('details');
      diff.className = 'history-diff';
      const summary = document.createElement('summary');
      summary.textContent = 'Show diff';
      const pre = document.createElement('pre');
      pre.textContent = entry.diff || '(No text diff)';
      diff.append(summary, pre);

      const revert = document.createElement('button');
      revert.type = 'button';
      revert.className = 'history-revert';
      revert.dataset.id = String(entry.id);
      revert.textContent = 'Revert';
      revert.setAttribute('aria-label', `Revert ${entry.label || 'source edit'}`);
      if (options.onRevert) revert.addEventListener('click', () => options.onRevert(entry.id));

      row.append(heading, revisions, diff, revert);
      rows.push(row);
    }

    if (options.showTransport) {
      const heading = document.createElement('h3');
      heading.className = 'history-transport-heading';
      heading.textContent = 'Transport events';
      rows.push(heading);
      const events = state.events || [];
      if (!events.length) {
        const empty = document.createElement('p');
        empty.className = 'history-empty';
        empty.textContent = 'No transport events yet.';
        rows.push(empty);
      }
      for (const event of events) {
        const row = document.createElement('div');
        row.className = 'history-transport-event';
        const kind = document.createElement('span');
        kind.className = 'history-kind';
        kind.textContent = event.kind || 'event';
        const detail = document.createElement('span');
        detail.className = 'history-detail';
        detail.textContent = event.detail || '';
        const time = document.createElement('time');
        time.className = 'history-time';
        time.dateTime = event.at || '';
        time.textContent = timeLabel(event.at);
        row.append(kind, detail, time);
        rows.push(row);
      }
    }
    list.replaceChildren(...rows);
    return rows;
  }

  function mount(options = {}) {
    const document = options.document || root.document;
    const section = options.root || document?.querySelector('[data-studio-history]');
    if (!document || !section) return {refresh() {}, destroy() {}};
    if (section._cicadaHistoryController) return section._cicadaHistoryController;
    const list = options.list || section.querySelector('#history-list');
    if (!list) return {refresh() {}, destroy() {}};
    const undoButton = section.querySelector('#history-undo');
    const redoButton = section.querySelector('#history-redo');
    const transportFilter = section.querySelector('#history-show-transport');
    const status = options.status || document.querySelector('#studio-status');
    const editor = options.editor || document.querySelector('#source-editor');
    const fetcher = options.fetcher || root.fetch.bind(root);
    const getRevision = options.getRevision || (() => document.body.dataset.revision);
    const setStatus = message => { if (status) status.textContent = message; };
    let state = {edits: [], events: [], canUndo: false, canRedo: false};
    let busy = false;
    let alive = true;

    function paint() {
      renderHistory(list, state, {
        document,
        showTransport: Boolean(transportFilter?.checked),
        onRevert: id => run(`/api/history/${encodeURIComponent(id)}/revert`)
      });
      if (undoButton) undoButton.disabled = busy || !state.canUndo;
      if (redoButton) redoButton.disabled = busy || !state.canRedo;
    }

    async function refresh() {
      try {
        const response = await fetcher('/api/history', {cache: 'no-store'});
        if (!response.ok) return;
        state = await response.json();
        if (alive) paint();
      } catch { /* The score stays usable if the history request is interrupted. */ }
    }

    async function run(path) {
      if (busy) return false;
      const before = new root.CustomEvent('cicada:historybeforechange', {cancelable: true, detail: {path}});
      root.dispatchEvent(before);
      if (before.defaultPrevented || options.onBeforeAction?.(path) === false) return false;
      busy = true;
      paint();
      try {
        const response = await fetcher(path, {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({revision: getRevision()})
        });
        const result = await response.json();
        if (!response.ok) {
          setStatus(result.error || 'History action failed');
          return false;
        }
        if (result.revision) document.body.dataset.revision = result.revision;
        options.onApplied?.(result);
        root.dispatchEvent(new root.CustomEvent('cicada:sourcewritten', {detail: result}));
        setStatus('Source updated');
        return true;
      } catch (error) {
        setStatus(`History action failed: ${error.message}`);
        return false;
      } finally {
        busy = false;
        await refresh();
      }
    }

    const keyDisposer = bindKeys(document, {
      undo: () => run('/api/undo'),
      redo: () => run('/api/redo')
    }, editor);
    const onUndo = () => run('/api/undo');
    const onRedo = () => run('/api/redo');
    const onFilter = () => paint();
    undoButton?.addEventListener('click', onUndo);
    redoButton?.addEventListener('click', onRedo);
    transportFilter?.addEventListener('change', onFilter);
    refresh();
    const poll = root.setInterval(() => refresh(), 1500);
    const controller = {
      refresh,
      destroy() {
        alive = false;
        root.clearInterval(poll);
        keyDisposer();
        undoButton?.removeEventListener('click', onUndo);
        redoButton?.removeEventListener('click', onRedo);
        transportFilter?.removeEventListener('change', onFilter);
        delete section._cicadaHistoryController;
      }
    };
    section._cicadaHistoryController = controller;
    return controller;
  }

  const api = {bindKeys, editorTarget, mount, renderHistory};
  root.CicadaStudioHistory = api;
  if (root.document) {
    const autoMount = () => {
      if (root.document.querySelector('[data-studio-history]')) mount();
    };
    if (root.document.readyState === 'loading') root.document.addEventListener('DOMContentLoaded', autoMount, {once: true});
    else autoMount();
  }
})(typeof window === 'undefined' ? globalThis : window);
