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
 *
 * Mobile enhancements (only active under the same 700px breakpoint the rest of
 * the UI uses): a scrollable key toolbar (with Copy/Paste), visualViewport-driven
 * reflow so the grid never gets squished by the soft keyboard, native long-press
 * text selection, and a persisted font size. Desktop behaviour is unchanged.
 */
(function () {
  'use strict';

  var card = document.getElementById('terminal-card');
  if (!card) return;

  var ticket  = card.getAttribute('data-ticket');
  var wsBase  = card.getAttribute('data-ws-url');
  var initCmd = card.getAttribute('data-init-cmd') || '';
  if (!ticket || !wsBase) return;

  var container = document.getElementById('terminal-container');
  var statusEl  = document.getElementById('terminal-status');
  var errorEl   = document.getElementById('terminal-error');

  var isMobile = window.matchMedia('(max-width: 700px)').matches;
  var FONT_KEY = 'nodexia.terminal.fontSize';
  var FONT_MIN = 10;
  var FONT_MAX = 24;

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

  /* ── Initial font size ────────────────────────────────── */
  // Desktop stays pinned at 14 (unchanged). Mobile defaults larger for legible
  // taps; the user's A-/A+ choice is restored from localStorage when present.
  function initialFontSize() {
    if (!isMobile) return 14;
    try {
      var stored = parseInt(window.localStorage.getItem(FONT_KEY), 10);
      if (stored >= FONT_MIN && stored <= FONT_MAX) return stored;
    } catch (e) { /* localStorage unavailable */ }
    return 15;
  }

  /* ── Init xterm.js ────────────────────────────────────── */
  if (typeof Terminal === 'undefined') {
    showError('xterm.js failed to load. Please reload the page.');
    return;
  }

  var term = new Terminal({
    cursorBlink: true,
    fontFamily: 'ui-monospace, "Cascadia Code", "Fira Code", monospace',
    fontSize: initialFontSize(),
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

  /* ── Input helper ─────────────────────────────────────── */
  function sendInput(data) {
    if (data && ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'input', data: data }));
    }
  }

  /* ── WebSocket ────────────────────────────────────────── */
  // Build an absolute ws(s):// URL: relative URLs in the WebSocket
  // constructor are not supported by all browsers.
  var wsScheme = window.location.protocol === 'https:' ? 'wss://' : 'ws://';
  var wsURL = wsScheme + window.location.host + wsBase +
    '?ticket=' + encodeURIComponent(ticket);
  var ws = new WebSocket(wsURL);

  ws.onopen = function () {
    setStatus('connected', 'connected');
    fitAndResize();
    term.focus();

    // Auto-run an initial command (e.g. an interactive command forwarded from
    // the command center). Defer briefly so the shell prompt is ready.
    if (initCmd) {
      setTimeout(function () {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'input', data: initCmd + '\n' }));
        }
      }, 350);
    }
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
    // The terminal no longer owns the screen — restore background scrolling.
    setScrollLock(false);
    if (!event.wasClean) {
      showError('Connection closed unexpectedly (code ' + event.code + ').');
    }
  };

  /* ── Back button ──────────────────────────────────────── */
  // Critical on mobile where the terminal owns the screen and browser chrome is
  // often hidden. Close the socket cleanly, then return to the previous page
  // (falling back to the server list when there is no history to go back to).
  var backBtn = document.getElementById('terminal-back');
  if (backBtn) {
    backBtn.addEventListener('click', function () {
      setScrollLock(false);
      try { if (ws) ws.close(1000, 'closed by user'); } catch (e) { /* ignore */ }
      if (window.history.length > 1) {
        window.history.back();
      } else {
        window.location.href = '/servers';
      }
    });
  }

  // Never leave the page locked if it is bfcached and later restored.
  window.addEventListener('pagehide', function () { setScrollLock(false); });

  /* ── Ctrl-pending modifier ────────────────────────────── */
  // Tapping the toolbar's Ctrl key arms a one-shot modifier: the next character
  // typed on the physical/soft keyboard is folded into its control code and the
  // modifier disarms itself. Tapping Ctrl again disarms it manually.
  var ctrlPending = false;
  var ctrlBtn = null;

  function setCtrl(on) {
    ctrlPending = on;
    if (ctrlBtn) {
      ctrlBtn.classList.toggle('is-active', on);
      ctrlBtn.setAttribute('aria-pressed', on ? 'true' : 'false');
    }
  }

  // Map a single typed character to its Ctrl-combined control code, or null
  // when the character has no meaningful control form.
  function ctrlCombine(data) {
    if (!data || data.length !== 1) return null;
    var code = data.toLowerCase().charCodeAt(0);
    if (code >= 97 && code <= 122) return String.fromCharCode(code - 96); // a-z → 0x01-0x1a
    switch (data) {
      case ' ': case '@': return '\x00';
      case '[': return '\x1b';
      case '\\': return '\x1c';
      case ']': return '\x1d';
      case '^': return '\x1e';
      case '_': return '\x1f';
      case '?': return '\x7f';
    }
    return null;
  }

  /* ── Forward keystrokes ───────────────────────────────── */
  term.onData(function (data) {
    if (ctrlPending) {
      var combined = ctrlCombine(data);
      setCtrl(false);
      if (combined !== null) {
        sendInput(combined);
        return;
      }
      // Not combinable — fall through and send the original keystroke.
    }
    sendInput(data);
  });

  /* ── Font size control ────────────────────────────────── */
  function setFontSize(px) {
    px = Math.max(FONT_MIN, Math.min(FONT_MAX, px));
    if (px === term.options.fontSize) return;
    term.options.fontSize = px;
    if (isMobile) {
      try { window.localStorage.setItem(FONT_KEY, String(px)); } catch (e) { /* ignore */ }
    }
    fitAndResize();
  }

  /* ── Clipboard paste ──────────────────────────────────── */
  function clipboardAvailable() {
    return !!(navigator.clipboard && navigator.clipboard.readText);
  }

  function doPaste() {
    if (!clipboardAvailable()) return;
    navigator.clipboard.readText().then(function (text) {
      sendInput(text);
    }).catch(function () { /* permission denied / unavailable */ });
  }

  /* ── Clipboard copy ───────────────────────────────────── */
  function canWriteClipboard() {
    return !!(navigator.clipboard && navigator.clipboard.writeText);
  }

  // Prefer the user's native long-press selection over the DOM-rendered rows;
  // fall back to xterm's own selection (a desktop mouse drag, say).
  function currentSelection() {
    var sel = '';
    try { sel = window.getSelection ? String(window.getSelection()) : ''; } catch (e) { /* ignore */ }
    if (sel) return sel;
    try { return term.hasSelection() ? term.getSelection() : ''; } catch (e) { return ''; }
  }

  function flashCopied(btn) {
    if (!btn) return;
    btn.textContent = 'Copied!';
    btn.classList.add('is-copied');
    setTimeout(function () {
      btn.textContent = 'Copy';
      btn.classList.remove('is-copied');
    }, 1000);
  }

  function doCopy(btn) {
    var text = currentSelection();
    if (!text || !canWriteClipboard()) return;
    navigator.clipboard.writeText(text).then(function () {
      flashCopied(btn);
    }).catch(function () { /* permission denied / unavailable */ });
  }

  /* ── Mobile key toolbar ───────────────────────────────── */
  var SEQ = {
    esc:   '\x1b',
    tab:   '\x09',
    up:    '\x1b[A',
    down:  '\x1b[B',
    right: '\x1b[C',
    left:  '\x1b[D',
    home:  '\x1b[H',
    end:   '\x1b[F',
    del:   '\x1b[3~',
  };

  var BUTTONS = [
    { label: 'Ctrl',  kind: 'ctrl' },
    { label: 'Esc',   kind: 'seq', key: 'esc' },
    { label: 'Tab',   kind: 'seq', key: 'tab' },
    { label: '←',     kind: 'seq', key: 'left',  aria: 'Left' },
    { label: '↑',     kind: 'seq', key: 'up',    aria: 'Up' },
    { label: '↓',     kind: 'seq', key: 'down',  aria: 'Down' },
    { label: '→',     kind: 'seq', key: 'right', aria: 'Right' },
    { label: 'Home',  kind: 'seq', key: 'home' },
    { label: 'End',   kind: 'seq', key: 'end' },
    { label: 'Del',   kind: 'seq', key: 'del' },
    { label: 'Copy',  kind: 'copy' },
    { label: 'Paste', kind: 'paste' },
    { label: 'A−',    kind: 'font', key: 'dec', aria: 'Smaller font' },
    { label: 'A+',    kind: 'font', key: 'inc', aria: 'Larger font' },
  ];

  function handleKey(def, btn) {
    switch (def.kind) {
      case 'seq':   sendInput(SEQ[def.key]); break;
      case 'ctrl':  setCtrl(!ctrlPending); break;
      case 'copy':  doCopy(btn); break;
      case 'paste': doPaste(); break;
      case 'font':  setFontSize(term.options.fontSize + (def.key === 'inc' ? 1 : -1)); break;
    }
  }

  function buildToolbar() {
    var bar = document.createElement('div');
    bar.className = 'terminal-toolbar';
    bar.setAttribute('role', 'toolbar');
    bar.setAttribute('aria-label', 'Terminal keys');

    BUTTONS.forEach(function (def) {
      if (def.kind === 'paste' && !clipboardAvailable()) return;
      if (def.kind === 'copy' && !canWriteClipboard()) return;

      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'terminal-key';
      btn.textContent = def.label;
      btn.tabIndex = -1;
      btn.setAttribute('aria-label', def.aria || def.label);

      if (def.kind === 'ctrl') {
        btn.classList.add('terminal-key--ctrl');
        btn.setAttribute('aria-pressed', 'false');
        ctrlBtn = btn;
      }

      // Prevent the button from stealing focus from the xterm textarea, which
      // would dismiss the soft keyboard.
      btn.addEventListener('mousedown', function (e) { e.preventDefault(); });
      btn.addEventListener('click', function (e) {
        e.preventDefault();
        handleKey(def, btn);
        // Copy must keep the user's selection and not pop the keyboard, so it
        // is the one action that does not pull focus back to the terminal.
        if (def.kind !== 'copy') term.focus();
      });

      bar.appendChild(btn);
    });

    card.appendChild(bar);
  }

  /* ── Mobile full-screen layout + keyboard reflow ──────── */
  // The soft keyboard does not fire `window.resize` on iOS Safari; it only
  // changes window.visualViewport. We pin the card to the visible viewport so
  // the grid reflows above the keyboard instead of being squished or hidden.
  var vv = window.visualViewport;
  var rafPending = false;

  function updateMobileViewport() {
    if (!card.classList.contains('terminal-card--mobile')) return;
    if (vv) {
      card.style.top = vv.offsetTop + 'px';
      card.style.height = vv.height + 'px';
    } else {
      card.style.top = '0px';
      card.style.height = window.innerHeight + 'px';
    }
    fitAndResize();
  }

  function scheduleViewportUpdate() {
    if (rafPending) return;
    rafPending = true;
    requestAnimationFrame(function () {
      rafPending = false;
      updateMobileViewport();
    });
  }

  // Lock/unlock the page behind the full-screen terminal. The matching CSS
  // pins <body> in place (iOS Safari ignores overflow:hidden alone), so the
  // class must live on both <html> and <body>.
  function setScrollLock(on) {
    document.documentElement.classList.toggle('terminal-mobile-active', on);
    document.body.classList.toggle('terminal-mobile-active', on);
  }

  function enableMobile() {
    buildToolbar();
    card.classList.add('terminal-card--mobile');
    setScrollLock(true);
    updateMobileViewport();

    if (vv) {
      vv.addEventListener('resize', scheduleViewportUpdate);
      vv.addEventListener('scroll', scheduleViewportUpdate);
    }
    window.addEventListener('orientationchange', function () {
      setTimeout(scheduleViewportUpdate, 250);
    });
  }

  /* ── Resize handling ──────────────────────────────────── */
  var resizeTimer = null;
  window.addEventListener('resize', function () {
    if (isMobile) {
      scheduleViewportUpdate();
      return;
    }
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(fitAndResize, 80);
  });

  if (isMobile) {
    enableMobile();
  }

  /* ── Initial fit after fonts load ────────────────────── */
  // defer to next frame so the layout has settled
  requestAnimationFrame(function () {
    if (isMobile) {
      updateMobileViewport();
    } else {
      fitAndResize();
    }
  });
})();
