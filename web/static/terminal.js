/* Nodexia in-browser SSH terminal.
 *
 * Requires xterm.min.js and xterm-addon-fit.min.js to be loaded first.
 * Uses a WebSocket + xterm.js PTY to run an interactive SSH shell session.
 *
 * WebSocket protocol (JSON, text frames):
 *   Client → server:
 *     {"type":"input","data":"<utf-8 string>"}
 *     {"type":"resize","cols":<int>,"rows":<int>}
 *   Server → client:
 *     {"type":"output","data":"<utf-8 string>"}
 *     {"type":"error","message":"<string>"}
 */
(function () {
  'use strict';

  var card = document.getElementById('terminal-card');
  if (!card) return;

  var ticket  = card.getAttribute('data-ticket');
  var wsBase  = card.getAttribute('data-ws-url');
  if (!ticket || !wsBase) return;

  var container = document.getElementById('terminal-container');
  var statusEl  = document.getElementById('terminal-status');
  var errorEl   = document.getElementById('terminal-error');

  /* ── Status helpers ───────────────────────────────────── */
  function setStatus(state, text) {
    if (!statusEl) return;
    statusEl.className = 'terminal-status terminal-status--' + state;
    statusEl.textContent = text;
  }

  function showError(msg) {
    if (!errorEl) return;
    errorEl.textContent = msg;
    errorEl.style.display = 'block';
  }

  /* ── Init xterm.js ────────────────────────────────────── */
  if (typeof Terminal === 'undefined') {
    showError('xterm.js failed to load. Please reload the page.');
    return;
  }

  var term = new Terminal({
    cursorBlink: true,
    fontFamily: 'ui-monospace, "Cascadia Code", "Fira Code", monospace',
    fontSize: 14,
    theme: {
      background: '#000000',
      foreground: '#e2e8f0',
      cursor:     '#60a5fa',
    },
  });

  var fitAddon = null;
  if (typeof FitAddon !== 'undefined' && FitAddon.FitAddon) {
    fitAddon = new FitAddon.FitAddon();
    term.loadAddon(fitAddon);
  }

  term.open(container);

  /* ── Fit helper ───────────────────────────────────────── */
  function fitAndResize() {
    if (fitAddon) {
      try { fitAddon.fit(); } catch (e) { /* ignore */ }
    }
    if (ws && ws.readyState === WebSocket.OPEN && term.cols && term.rows) {
      ws.send(JSON.stringify({
        type: 'resize',
        cols: term.cols,
        rows: term.rows,
      }));
    }
  }

  /* ── WebSocket ────────────────────────────────────────── */
  var wsURL = wsBase + '?ticket=' + encodeURIComponent(ticket);
  var ws = new WebSocket(wsURL);

  ws.onopen = function () {
    setStatus('connected', 'connected');
    fitAndResize();
    term.focus();
  };

  ws.onmessage = function (event) {
    try {
      var msg = JSON.parse(event.data);
      if (msg.type === 'output') {
        term.write(msg.data);
      } else if (msg.type === 'error') {
        showError(msg.message);
        setStatus('error', 'error');
      }
    } catch (e) { /* ignore unparseable frames */ }
  };

  ws.onerror = function () {
    setStatus('error', 'connection error');
    showError('WebSocket connection failed.');
  };

  ws.onclose = function (event) {
    setStatus('disconnected', 'disconnected');
    if (!event.wasClean) {
      showError('Connection closed unexpectedly (code ' + event.code + ').');
    }
  };

  /* ── Forward keystrokes ───────────────────────────────── */
  term.onData(function (data) {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'input', data: data }));
    }
  });

  /* ── Resize handling ──────────────────────────────────── */
  var resizeTimer = null;
  window.addEventListener('resize', function () {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(fitAndResize, 80);
  });

  /* ── Initial fit after fonts load ────────────────────── */
  // defer to next frame so the layout has settled
  requestAnimationFrame(function () {
    fitAndResize();
  });
})();
