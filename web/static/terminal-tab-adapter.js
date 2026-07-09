// Nodexia v0.6.1: bridges one terminal pane's terminal.js instance to tab lifecycle events.
(function () {
  'use strict';

  // Loaded as the last PageScripts entry on the terminal page, after
  // terminal.js. Never references terminal.js directly — only through
  // card.__nodexiaTerminal (adapter → terminal.js) and DOM CustomEvents
  // (terminal.js → adapter → tab-manager.js).
  var pane = document.currentScript.closest('.tab-pane');
  var card = pane ? pane.querySelector('#terminal-card') : document.getElementById('terminal-card');
  if (!card || !card.__nodexiaTerminal) return;

  document.addEventListener('tab-activated', function (event) {
    if (event.detail.pane === pane) card.__nodexiaTerminal.resume();
  });
  document.addEventListener('tab-deactivated', function (event) {
    if (event.detail.pane === pane) card.__nodexiaTerminal.pause();
  });
  document.addEventListener('tab-closing', function (event) {
    // The only path that closes the WebSocket or disposes xterm.
    if (event.detail.pane === pane) card.__nodexiaTerminal.dispose();
  });

  card.addEventListener('nodexia:terminal-status', function (event) {
    document.dispatchEvent(new CustomEvent('tab-status-changed', {
      bubbles: true,
      detail: { tabId: pane ? pane.dataset.tabId : undefined, state: event.detail.state }
    }));
  });
})();
