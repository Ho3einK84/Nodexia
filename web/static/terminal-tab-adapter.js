// Nodexia v0.6.5: bridges one terminal pane's terminal.js instance to tab lifecycle events.
(function () {
  'use strict';

  // Loaded as the last PageScripts entry on the terminal page, after
  // terminal.js. Never references terminal.js directly — only through
  // card.__nodexiaTerminal (adapter → terminal.js) and DOM CustomEvents
  // (terminal.js → adapter → tab-manager.js).
  //
  // On the initial page load (direct URL visit), the PageScripts are rendered
  // in <body> outside #tab-content, so document.currentScript.closest('.tab-pane')
  // returns null — the tab-manager hasn't wrapped the content yet.  We detect
  // this and resolve the pane lazily from the active tab record on first use.
  var pane = document.currentScript && document.currentScript.closest
    ? document.currentScript.closest('.tab-pane')
    : null;
  var card = pane ? pane.querySelector('#terminal-card') : document.getElementById('terminal-card');
  if (!card || !card.__nodexiaTerminal) return;

  function getPane() {
    if (pane) return pane;
    // Lazy resolution for the initial page load: the boot tab's pane is the
    // active tab in NodexiaTabs.
    if (window.NodexiaTabs && typeof window.NodexiaTabs.getActive === 'function') {
      var active = window.NodexiaTabs.getActive();
      if (active && active.id) {
        var el = document.querySelector('.tab-pane[data-tab-id="' + active.id + '"]');
        if (el) { pane = el; return pane; }
      }
    }
    // Fallback: find the pane that contains our card element.
    if (card) {
      var p = card.closest('.tab-pane');
      if (p) { pane = p; return pane; }
    }
    return null;
  }

  // Resolve immediately so the status event can carry a valid tabId.
  getPane();

  document.addEventListener('tab-activated', function (event) {
    if (event.detail.pane === getPane()) card.__nodexiaTerminal.resume();
  });
  document.addEventListener('tab-deactivated', function (event) {
    if (event.detail.pane === getPane()) card.__nodexiaTerminal.pause();
  });
  document.addEventListener('tab-closing', function (event) {
    // The only path that closes the WebSocket or disposes xterm.
    if (event.detail.pane === getPane()) card.__nodexiaTerminal.dispose();
  });

  card.addEventListener('nodexia:terminal-status', function (event) {
    var p = getPane();
    document.dispatchEvent(new CustomEvent('tab-status-changed', {
      bubbles: true,
      detail: { tabId: p ? p.dataset.tabId : undefined, state: event.detail.state }
    }));
  });
})();
