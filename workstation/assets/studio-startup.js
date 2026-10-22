/* Keep startup feedback and early drafts independent of the Studio engine. */
(() => {
  'use strict';
  if (window.__cicadaStartup) return;
  window.__cicadaStartup = true;
  let root, timer, watching = false, failure = false, blocked = '';
  const isControl = node => node instanceof Element && node.matches('input:not([type=hidden]),textarea,select') && !node.hasAttribute('data-studio-view-control');
  const value = node => /^(checkbox|radio)$/.test(node.type) ? String(node.checked) : node.value;
  const defaultValue = node => /^(checkbox|radio)$/.test(node.type) ? String(node.defaultChecked) : node.tagName === 'SELECT' ? (Array.from(node.options).find(o => o.defaultSelected) || node.options[0])?.value || '' : node.defaultValue;
  function baseline(event) {
    const node = event.target;
    if (root?.contains(node) && isControl(node) && !node.hasAttribute('data-baseline')) node.setAttribute('data-baseline', value(node));
  }
  function edited(event) {
    const node = event.target;
    if (!root?.contains(node) || !isControl(node)) return;
    if (!node.hasAttribute('data-baseline')) node.setAttribute('data-baseline', defaultValue(node));
    node.setAttribute('data-edited', 'true');
    blocked = '';
    update();
  }
  const observer = new MutationObserver(() => update());
  function observe(active) {
    if (active === watching) return;
    watching = active;
    for (const name of ['focusin', 'pointerdown', 'beforeinput']) document[active ? 'addEventListener' : 'removeEventListener'](name, baseline, true);
    for (const name of ['input', 'change']) document[active ? 'addEventListener' : 'removeEventListener'](name, edited, true);
    if (active) observer.observe(document.documentElement, {subtree:true, childList:true, attributes:true, attributeFilter:['data-shell-actions','class','data-gosx-runtime-state']});
    else observer.disconnect();
  }
  function reloadBlock() {
    if (root.querySelector('[data-capture-control=stop]:not([disabled])')) return {message:'Finish recording before reloading Studio.'};
    const dirty = Array.from(root.querySelectorAll('[data-edited=true]')).find(n => isControl(n) && value(n) !== (n.getAttribute('data-baseline') ?? defaultValue(n)));
    const draft = root.querySelector('form[data-code-unsaved],form[data-gosx-form-state=error]');
    return dirty || draft ? {node:dirty, message:'Save or copy your unsaved edits before reloading Studio.'} : null;
  }
  function update() {
    const current = document.getElementById('studio-shell');
    if (!current) { clearTimeout(timer); observe(false); root = null; return; }
    if (root !== current) {
      root = current; failure = false; blocked = ''; clearTimeout(timer);
      timer = setTimeout(() => { timer = null; update(); }, 8000);
      observe(true);
    }
    if (root.querySelector('[id^=cicada-][data-gosx-runtime-state=error]')) failure = true;
    const status = root.querySelector('[data-studio-startup-status]');
    if (!status) return;
    const ready = root.getAttribute('data-shell-actions') === 'ready' && !!root.querySelector('.editor-page.editor-highlight-ready') && window.__gosx?.ready === true;
    if (!ready && !failure && !watching) { clearTimeout(timer); timer = setTimeout(() => { timer = null; update(); }, 8000); }
    const state = failure ? 'error' : ready ? 'ready' : timer ? 'loading' : 'slow';
    if (root.getAttribute('data-studio-startup') !== state) root.setAttribute('data-studio-startup', state);
    status.hidden = state === 'ready';
    const button = status.querySelector('button');
    button.hidden = state !== 'error' && state !== 'slow';
    const text = blocked || (state === 'error' ? 'Some Studio controls could not open. Reload Studio to try again.' : state === 'slow' ? 'Studio is taking longer to open. You can wait or reload.' : 'Opening Studio controls…');
    const message = status.querySelector('[data-studio-startup-message]');
    if (message.textContent !== text) message.textContent = text;
    if (state === 'ready') {
      clearTimeout(timer); observe(false);
      if (document.activeElement === button) root.querySelector('#studio-center')?.focus({preventScroll:true});
    } else observe(true);
  }
  document.addEventListener('click', event => {
    if (!(event.target instanceof Element) || !event.target.closest('[data-studio-startup-reload]') || !root) return;
    event.preventDefault();
    const reason = reloadBlock();
    if (reason) { blocked = reason.message; update(); reason.node?.focus({preventScroll:true}); return; }
    location.reload();
  });
  document.addEventListener('gosx:error', event => {
    if (event.detail?.issue?.scope === 'engine' && event.detail.issue.component?.startsWith('Cicada')) { update(); failure = true; update(); }
  });
  document.addEventListener('gosx:ready', update);
  document.addEventListener('gosx:navigate', update);
  window.addEventListener('beforeunload', event => { if (watching && root && reloadBlock()) { event.preventDefault(); event.returnValue = ''; } });
  window.addEventListener('pageshow', update);
  window.addEventListener('pagehide', () => { clearTimeout(timer); observe(false); });
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', update, {once:true});
  else update();
})();
