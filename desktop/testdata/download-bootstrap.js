(function () {
  'use strict';
  if (window !== window.top || window.__cicadaNativeDownloads) return;
  window.__cicadaNativeDownloads = true;
  let active = null, sequence = 0, subscribed = false;
  const media = /^\/media\/(?:exports\/[A-Za-z0-9_-]+\.wav|midi-exports\/[A-Za-z0-9_-]+\.mid|stem-exports\/[A-Za-z0-9_-]+\.zip)$/;
  function finish(value) {
    if (!active || !value || value.requestId !== active.requestId) return;
    const pending = active;
    active = null;
    for (const [name, value] of pending.attributes) {
      if (value === null) pending.link.removeAttribute(name);
      else pending.link.setAttribute(name, value);
    }
    if (value.state === 'saved') pending.feedback.textContent = 'Saved ' + value.name + '.';
    else if (value.state === 'cancelled') pending.feedback.textContent = 'Save cancelled. Your export is still available.';
    else pending.feedback.textContent = 'Could not save the export. ' + (value.error || 'Try again.');
    pending.link.dispatchEvent(new CustomEvent('cicada:export-saved', {bubbles: true, detail: value}));
  }
  document.addEventListener('click', function (event) {
    const link = event.target?.closest?.('a[download]');
    if (!link || event.defaultPrevented || event.button > 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    let url;
    try { url = new URL(link.href, location.href); } catch (_) { return; }
    if (url.origin !== location.origin || url.search || url.hash || !media.test(url.pathname)) return;
    const bridge = window.gosxDesktop;
    if (!bridge || typeof bridge.call !== 'function' || typeof bridge.on !== 'function') return;
    event.preventDefault(); event.stopImmediatePropagation();
    if (active) return;
    if (!subscribed) { bridge.on('cicada.export.saved', finish); subscribed = true; }
    let feedback = link.parentElement.querySelector('.native-export-status');
    if (!feedback) {
      feedback = document.createElement('p');
      feedback.className = 'native-export-status';
      feedback.setAttribute('role', 'status'); feedback.setAttribute('aria-live', 'polite');
      link.parentElement.appendChild(feedback);
    }
    const requestId = String(Date.now()) + '-' + String(++sequence);
    const attributes = ['aria-disabled', 'aria-busy'].map(name => [name, link.getAttribute(name)]);
    active = {requestId, link, feedback, attributes};
    link.setAttribute('aria-disabled', 'true'); link.setAttribute('aria-busy', 'true');
    feedback.textContent = 'Choose where to save the export…';
    Promise.resolve(bridge.call('cicada.export.save', {path: url.pathname, requestId})).catch(error => {
      finish({requestId, state: 'failed', error: error?.message || String(error)});
    });
  }, true);
}());
