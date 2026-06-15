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
 * the UI uses): a scrollable key toolbar, visualViewport-driven reflow so the
 * grid never gets squished by the soft keyboard, and a persisted font size.
 *
 * Copying terminal output on a phone is the hard part: xterm's live rows are not
 * reliably selectable by a native long-press, so the toolbar offers explicit
 * actions instead — a "Select" mode that overlays the scrollback as plain,
 * natively-selectable text, a one-tap "Copy all", and a "Copy" that grabs the
 * current selection. All copy paths fall back to a hidden-textarea execCommand
 * when the async clipboard API is unavailable (e.g. an HTTP-served panel), so
 * copy works outside secure contexts too. Desktop behaviour is unchanged.
 */
(function () {
  'use strict';

  // Localization helper (see app.js for window.nxT). Falls back to the key.
  function T(key, params) { return window.nxT ? window.nxT(key, params) : key; }

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

  // Auto-run an initial command (e.g. an interactive command forwarded from the
  // command center) exactly once. A fixed delay races the shell startup, so we
  // wait for the first output (the prompt) and add a short settle delay; a
  // generous fallback timer covers shells that emit nothing before the prompt.
  var initSent = false;
  function maybeSendInitCmd() {
    if (initSent || !initCmd) return;
    initSent = true;
    setTimeout(function () {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'input', data: initCmd + '\n' }));
      }
    }, 150);
  }

  ws.onopen = function () {
    setStatus('connected', T('js.terminal.connected'));
    fitAndResize();
    term.focus();
    if (initCmd) setTimeout(maybeSendInitCmd, 1200);
  };

  ws.onmessage = function (event) {
    try {
      var msg = JSON.parse(event.data);
      if (msg.type === 'output') {
        term.write(msg.data);
        maybeSendInitCmd();
      } else if (msg.type === 'error') {
        showError(msg.message);
        setStatus('error', T('js.terminal.status_error'));
      }
    } catch (e) { /* ignore unparseable frames */ }
  };

  ws.onerror = function () {
    setStatus('error', T('js.terminal.connection_error'));
    showError(T('js.terminal.ws_failed'));
  };

  ws.onclose = function (event) {
    setStatus('disconnected', T('js.terminal.disconnected'));
    // The terminal no longer owns the screen — restore background scrolling.
    setScrollLock(false);
    if (!event.wasClean) {
      showError(T('js.terminal.closed_unexpectedly', { code: event.code }));
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
    if (selectPre) selectPre.style.fontSize = px + 'px';
    // Defer the fit one frame so xterm has recomputed its cell dimensions for the
    // new font size; fitting synchronously here can size the grid from stale dims.
    requestAnimationFrame(fitAndResize);
  }

  /* ── Clipboard write (with non-secure-context fallback) ── */
  // navigator.clipboard only exists in secure contexts (HTTPS or localhost). When
  // it is missing — or rejects — fall back to a hidden <textarea> + execCommand so
  // copy still works on HTTP-served panels. Returns a Promise.
  function fallbackCopy(text) {
    return new Promise(function (resolve, reject) {
      var ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.top = '0';
      ta.style.left = '0';
      ta.style.width = '1px';
      ta.style.height = '1px';
      ta.style.padding = '0';
      ta.style.border = 'none';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      // Preserve any existing (e.g. select-mode) selection across the copy.
      var sel = window.getSelection ? window.getSelection() : null;
      var saved = sel && sel.rangeCount ? sel.getRangeAt(0) : null;
      ta.focus();
      ta.select();
      try { ta.setSelectionRange(0, text.length); } catch (e) { /* ignore */ }
      var ok = false;
      try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
      document.body.removeChild(ta);
      if (saved && sel) { sel.removeAllRanges(); sel.addRange(saved); }
      ok ? resolve() : reject();
    });
  }

  function writeClipboard(text) {
    if (!text) return Promise.reject();
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text).catch(function () {
        return fallbackCopy(text);
      });
    }
    return fallbackCopy(text);
  }

  /* ── Clipboard paste (with non-secure-context fallback) ── */
  function doPaste() {
    if (navigator.clipboard && navigator.clipboard.readText) {
      navigator.clipboard.readText().then(sendInput).catch(promptPaste);
    } else {
      promptPaste();
    }
  }

  // Without the async clipboard API there is no way to read the clipboard
  // silently, so ask the user — they can paste into the prompt on any browser.
  function promptPaste() {
    try {
      var text = window.prompt(T('js.terminal.paste_prompt'));
      if (text) sendInput(text);
    } catch (e) { /* prompt unavailable */ }
  }

  /* ── Selection sources ────────────────────────────────── */
  // Prefer a live DOM selection (the select-mode overlay, or a desktop mouse
  // drag over selectable text); fall back to xterm's own selection.
  function currentSelection() {
    var sel = '';
    try { sel = window.getSelection ? String(window.getSelection()) : ''; } catch (e) { /* ignore */ }
    if (sel) return sel;
    try { return term.hasSelection() ? term.getSelection() : ''; } catch (e) { return ''; }
  }

  // Extract plain text straight from xterm's buffer (scrollback + screen) rather
  // than the DOM, so "Copy all" works regardless of what is selected or visible.
  function getBufferText(visibleOnly) {
    try {
      var buf = term.buffer.active;
      var start = 0;
      var end = buf.length;
      if (visibleOnly) {
        start = buf.viewportY;
        end = Math.min(buf.length, start + term.rows);
      }
      var lines = [];
      for (var i = start; i < end; i++) {
        var line = buf.getLine(i);
        lines.push(line ? line.translateToString(true) : '');
      }
      while (lines.length && lines[lines.length - 1] === '') lines.pop();
      return lines.join('\n');
    } catch (e) { return ''; }
  }

  function flashCopied(btn) {
    if (!btn) return;
    var orig = btn.getAttribute('data-label') || btn.textContent;
    btn.textContent = T('js.copy.copied');
    btn.classList.add('is-copied');
    setTimeout(function () {
      btn.textContent = orig;
      btn.classList.remove('is-copied');
    }, 1000);
  }

  function doCopy(text, btn) {
    writeClipboard(text).then(function () {
      flashCopied(btn);
    }).catch(function () { /* nothing selected / copy blocked */ });
  }

  /* ── Select mode (mobile) ─────────────────────────────── */
  // Overlay the scrollback as plain, natively-selectable text so a phone user can
  // long-press and drag-handle a region, then Copy (or use the OS copy menu, which
  // works even without the clipboard API). Built lazily on first use.
  var selecting = false;
  var selectOverlay = null;
  var selectPre = null;
  var selectBtn = null;

  function buildSelectOverlay() {
    selectOverlay = document.createElement('div');
    selectOverlay.className = 'terminal-select-overlay';
    selectOverlay.hidden = true;
    selectPre = document.createElement('pre');
    selectPre.className = 'terminal-select-text';
    selectOverlay.appendChild(selectPre);
    container.appendChild(selectOverlay);
  }

  function setSelectMode(on) {
    if (on && !selectOverlay) buildSelectOverlay();
    if (!selectOverlay) return;
    selecting = on;
    if (on) {
      selectPre.style.fontSize = term.options.fontSize + 'px';
      selectPre.textContent = getBufferText(false);
      selectOverlay.hidden = false;
      selectOverlay.scrollTop = selectOverlay.scrollHeight; // mirror the live view
      // Drop keyboard focus so the soft keyboard hides, leaving room to select.
      var ta = container.querySelector('textarea');
      if (ta && ta.blur) ta.blur();
    } else {
      selectOverlay.hidden = true;
      selectPre.textContent = '';
      try {
        var sel = window.getSelection && window.getSelection();
        if (sel && sel.removeAllRanges) sel.removeAllRanges();
      } catch (e) { /* ignore */ }
      term.focus();
    }
    if (selectBtn) {
      selectBtn.classList.toggle('is-active', on);
      selectBtn.setAttribute('aria-pressed', on ? 'true' : 'false');
      selectBtn.textContent = on ? T('js.terminal.done') : T('js.terminal.select');
    }
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

  // Physical keycap labels (Ctrl/Esc/Tab/Home/End/Del/A−/A+ and the arrow glyphs)
  // are conventionally kept in Latin even in RTL/Persian technical UIs, so only
  // their aria-labels and the action buttons (Select/Copy/Paste) are localized.
  var BUTTONS = [
    { label: 'Ctrl',  kind: 'ctrl' },
    { label: 'Esc',   kind: 'seq', key: 'esc' },
    { label: 'Tab',   kind: 'seq', key: 'tab' },
    { label: '←',     kind: 'seq', key: 'left',  aria: T('js.terminal.aria_left') },
    { label: '↑',     kind: 'seq', key: 'up',    aria: T('js.terminal.aria_up') },
    { label: '↓',     kind: 'seq', key: 'down',  aria: T('js.terminal.aria_down') },
    { label: '→',     kind: 'seq', key: 'right', aria: T('js.terminal.aria_right') },
    { label: 'Home',  kind: 'seq', key: 'home' },
    { label: 'End',   kind: 'seq', key: 'end' },
    { label: 'Del',   kind: 'seq', key: 'del' },
    { label: T('js.terminal.select'),   kind: 'select', aria: T('js.terminal.aria_select') },
    { label: T('js.copy.label'),        kind: 'copy', aria: T('js.terminal.aria_copy') },
    { label: T('js.terminal.copy_all'), kind: 'copyall', aria: T('js.terminal.aria_copy_all') },
    { label: T('js.terminal.paste'),    kind: 'paste' },
    { label: 'A−',    kind: 'font', key: 'dec', aria: T('js.terminal.aria_font_smaller') },
    { label: 'A+',    kind: 'font', key: 'inc', aria: T('js.terminal.aria_font_larger') },
  ];

  // Actions that touch (or depend on) a text selection must not pull focus back
  // to the terminal — that would clear the selection and pop the soft keyboard.
  var NO_REFOCUS = { select: true, copy: true, copyall: true };

  function handleKey(def, btn) {
    switch (def.kind) {
      case 'seq':     sendInput(SEQ[def.key]); break;
      case 'ctrl':    setCtrl(!ctrlPending); break;
      case 'select':  setSelectMode(!selecting); break;
      case 'copy':    doCopy(currentSelection(), btn); break;
      case 'copyall': doCopy(getBufferText(false), btn); break;
      case 'paste':   doPaste(); break;
      case 'font':    setFontSize(term.options.fontSize + (def.key === 'inc' ? 1 : -1)); break;
    }
  }

  function buildToolbar() {
    var bar = document.createElement('div');
    bar.className = 'terminal-toolbar';
    bar.setAttribute('role', 'toolbar');
    bar.setAttribute('aria-label', T('js.terminal.keys_label'));

    BUTTONS.forEach(function (def) {
      // Copy/Paste are always offered now: even without the async clipboard API
      // they fall back to execCommand / prompt, so HTTP panels keep a copy path.
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'terminal-key';
      btn.textContent = def.label;
      btn.tabIndex = -1;
      btn.setAttribute('data-label', def.label);
      btn.setAttribute('aria-label', def.aria || def.label);

      if (def.kind === 'ctrl') {
        btn.classList.add('terminal-key--ctrl');
        btn.setAttribute('aria-pressed', 'false');
        ctrlBtn = btn;
      }
      if (def.kind === 'select') {
        btn.classList.add('terminal-key--select');
        btn.setAttribute('aria-pressed', 'false');
        selectBtn = btn;
      }

      // Prevent the button from stealing focus from the xterm textarea, which
      // would dismiss the soft keyboard.
      btn.addEventListener('mousedown', function (e) { e.preventDefault(); });
      btn.addEventListener('click', function (e) {
        e.preventDefault();
        handleKey(def, btn);
        // Selection-related actions manage focus themselves; everything else
        // pulls focus back to the terminal to keep the keyboard up.
        if (!NO_REFOCUS[def.kind]) term.focus();
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
    // The app is locked to portrait (manifest orientation + app.js runtime lock),
    // so orientationchange should no longer fire on installed/locked clients —
    // this listener is now effectively a no-op there. It is kept as a harmless
    // safety net for any browser that still allows a rotation (e.g. a desktop
    // tab, or a platform that ignores the lock): if the viewport ever does flip,
    // the grid still reflows correctly instead of being left mis-sized.
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
