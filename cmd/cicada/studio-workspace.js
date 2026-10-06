(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else root.CicadaWorkspace = api;
})(typeof globalThis === 'object' ? globalThis : this, function () {
  const panelNames = ['code', 'session', 'song', 'mix', 'master', 'record', 'voice', 'history', 'library'];

  function installWorkspaceKeyboard({document, selectPanel, command = () => {}, toggleDock = () => {}, showShortcuts = () => {}}) {
    document.addEventListener('keydown', event => {
      if (event.defaultPrevented || event.metaKey) return;
      const inEditor = event.target?.id === 'source-editor' || /^(INPUT|TEXTAREA|SELECT)$/.test(event.target?.tagName || '') || event.target?.isContentEditable;
      if (event.ctrlKey && event.key === '\\') {
        event.preventDefault();
        toggleDock();
        return;
      }
      if (inEditor) {
        if (event.altKey && event.shiftKey && /^[1-9]$/.test(event.key)) {
          event.preventDefault();
          selectPanel(panelNames[Number(event.key) - 1]);
        }
        return;
      }
      if (event.ctrlKey || event.altKey || event.shiftKey && event.key !== '?') return;
      // Space and Enter activate a focused button, link, or summary (a pattern grid cell, for example).
      if (event.key === ' ' && /^(BUTTON|A|SUMMARY)$/.test(event.target?.tagName || '')) return;
      if (/^[1-9]$/.test(event.key)) {
        event.preventDefault();
        selectPanel(panelNames[Number(event.key) - 1]);
        return;
      }
      switch (event.key) {
        case ' ': event.preventDefault(); command('playPause'); break;
        case 'Home': event.preventDefault(); command('returnToStart'); break;
        case 'l': case 'L': event.preventDefault(); command('toggleLive'); break;
        case 'r': case 'R': event.preventDefault(); command('toggleRecord'); break;
        case '?': event.preventDefault(); showShortcuts(); break;
      }
    });
  }

  function replaceProjection({document, nextDocument, markup, selectors, onReplace = () => {}}) {
    const parsed = nextDocument || new DOMParser().parseFromString(markup, 'text/html');
    for (const selector of selectors) {
      const current = document.querySelector(selector);
      const next = parsed.querySelector(selector);
      if (!current || !next) continue;
      current.replaceChildren(...[...next.childNodes].map(node => node.cloneNode(true)));
      onReplace(selector, current);
    }
  }

  function installPanelTabs({document, selectPanel, initial = 'session'}) {
    const tabs = [...document.querySelectorAll('[data-panel-tab]')];
    const panels = [...document.querySelectorAll('[data-workspace-panel]')];
    let active = initial;
    function choose(name) {
      if (!panelNames.includes(name)) return;
      if (name === 'code') {
        // Code is a dock beside the active panel, not a panel of its own.
        selectPanel(name);
        return;
      }
      active = name;
      document.documentElement.dataset.activePanel = name;
      tabs.forEach(tab => {
        const selected = tab.dataset.panelTab === name;
        tab.setAttribute('aria-selected', String(selected));
        tab.tabIndex = selected ? 0 : -1;
      });
      panels.forEach(panel => { panel.hidden = panel.dataset.workspacePanel !== name; });
      selectPanel(name);
    }
    tabs.forEach(tab => tab.addEventListener('click', () => choose(tab.dataset.panelTab)));
    choose(initial);
    return {choose, get active() { return active; }};
  }

  return {panelNames, installWorkspaceKeyboard, installPanelTabs, replaceProjection};
});
