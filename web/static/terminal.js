/* Nodexia in-browser SSH terminal (xterm.js v5 + addon suite).
 *
 * Load order (see handlers.go PageScripts):
 *   xterm.min.js → addon-fit → addon-unicode11 → addon-web-links → addon-search
 *   → addon-serialize → addon-webgl → addon-canvas → xterm-themes.js
 *   → terminal-keybindings.js → terminal.js
 * All vendored locally — the panel runs under a strict `script-src 'self'` CSP,
 * so no CDN/npm runtime is involved.
 *
 * WebSocket protocol (JSON text frames):
 *   Client → server:  {"type":"input","data":"…"}
 *                     {"type":"resize","cols":N,"rows":N}
 *                     {"type":"heartbeat"}                 // every 30 s
 *   Server → client:  {"type":"output","data":"…"}
 *                     {"type":"error","message":"…"}
 *                     {"type":"status","state":"connected|…"}
 *                     {"type":"heartbeat"}                 // echo, used for RTT
 *
 * Reconnect note: the WS ticket is single-use (a documented security invariant),
 * so we deliberately do NOT silently re-dial a dead socket — that would require
 * weakening the ticket model. Instead an unexpected close surfaces a Reconnect
 * action that re-issues a ticket through the normal page flow.
 *
 * Renderer strategy: WebGL (GPU) on desktop with a canvas fallback; canvas on
 * mobile for battery/compat. xterm's built-in DOM renderer is the last resort.
 *
 * Mobile is the hard part. xterm's live rows are not reliably selectable by a
 * native long-press, so the toolbar offers an explicit "Select" mode that
 * overlays the scrollback as plain, natively-selectable text. All clipboard
 * paths fall back to execCommand / prompt so copy & paste keep working on
 * HTTP-served panels outside a secure context. Desktop is unchanged by these.
 */
(function () {
  'use strict';

  // Localization helper (see app.js for window.nxT). Falls back to the key.
  function T(key, params) { return window.nxT ? window.nxT(key, params) : key; }
  function noop() {}

  // setShown drives an element's visibility through BOTH the `hidden` attribute
  // (state / accessibility) AND an inline display style. An inline style beats
  // any stylesheet rule, so a popover (theme menu, search bar, overlays) can
  // never be left visible by a stale or buggy CSS rule — which is exactly what
  // pinned the theme menu open before. `display` is the value to apply when
  // shown; omit it to fall back to the stylesheet's own display.
  function setShown(el, shown, display) {
    if (!el) return;
    el.hidden = !shown;
    el.style.display = shown ? (display || '') : 'none';
  }

  // v0.6.0: scope every lookup to this script's own .tab-pane (if any) so two
  // concurrent terminal tabs never resolve each other's DOM nodes.
  var scopeRoot = (document.currentScript && document.currentScript.closest &&
    document.currentScript.closest('.tab-pane')) || document;
  function byId(id) { return scopeRoot.querySelector('#' + id); }

  // v0.6.3: tab-aware navigation so reconnect/back stay scoped to this tab
  // pane instead of blowing away the entire multi-tab shell.
  function tabNavigate(url) {
    if (window.NodexiaTabs && typeof window.NodexiaTabs.navigate === 'function') {
      window.NodexiaTabs.navigate(url);
    } else {
      window.location.href = url;
    }
  }

  var card = byId('terminal-card');
  if (!card) return;

  var ticket  = card.getAttribute('data-ticket');
  var wsBase  = card.getAttribute('data-ws-url');
  var initCmd = card.getAttribute('data-init-cmd') || '';
  if (!ticket || !wsBase) return;

  // The credential page for this server (used by Reconnect): strip the /ws tail.
  var pageURL = wsBase.replace(/\/ws$/, '');

  var container = byId('terminal-container');
  var statusEl  = byId('terminal-status');
  var statusTextEl = statusEl ? statusEl.querySelector('.terminal-status__text') : null;
  var errorEl   = byId('terminal-error');

  // v0.6.0: true while this pane is the visible/foreground tab. Gates only the
  // resize/measurement work below; the WebSocket, heartbeat, and PTY input
  // keep running regardless so a backgrounded tab never loses output.
  var active = true;

  var isMobile = window.matchMedia('(max-width: 700px)').matches;
  var FONT_KEY = 'nodexia.terminal.fontSize';
  var FONT_MIN = 10;
  var FONT_MAX = 28;

  function defaultFontSize() { return isMobile ? 12 : 14; }

  /* ── Status helpers ───────────────────────────────────── */
  function setStatus(state, text) {
    if (!statusEl) return;
    statusEl.className = 'terminal-status terminal-status--' + state;
    if (statusTextEl) statusTextEl.textContent = text;
    else statusEl.textContent = text;
    if (card) card.dispatchEvent(new CustomEvent('nodexia:terminal-status', { bubbles: true, detail: { state: state } }));
  }

  function showError(msg) {
    if (!errorEl) return;
    errorEl.textContent = msg;
    errorEl.style.display = 'block';
  }
  function clearError() {
    if (!errorEl) return;
    errorEl.textContent = '';
    errorEl.style.display = 'none';
  }

  /* ── Initial font size ────────────────────────────────── */
  function initialFontSize() {
    try {
      var stored = parseInt(window.localStorage.getItem(FONT_KEY), 10);
      if (stored >= FONT_MIN && stored <= FONT_MAX) return stored;
    } catch (e) { /* localStorage unavailable */ }
    return defaultFontSize();
  }

  /* ── Theme ────────────────────────────────────────────── */
  var ThemeStore = window.NodexiaTermThemes;
  var themeId = ThemeStore ? ThemeStore.load() : 'nodexia';
  function currentTheme() {
    return (ThemeStore && ThemeStore.themes[themeId]) || { background: '#0b1120', foreground: '#e2e8f0' };
  }

  /* ── Init xterm.js ────────────────────────────────────── */
  if (typeof Terminal === 'undefined') {
    showError('xterm.js failed to load. Please reload the page.');
    return;
  }

  var term = new Terminal({
    cursorBlink: true,
    cursorStyle: 'block',
    fontFamily: '"JetBrains Mono", "Fira Code", "Cascadia Code", ui-monospace, "SFMono-Regular", monospace',
    fontSize: initialFontSize(),
    theme: currentTheme(),
    allowProposedApi: true,
    scrollback: 100000,
    fastScrollModifier: 'alt',
    fastScrollSensitivity: 3,
    smoothScrollDuration: 125,
    macOptionIsMeta: true,
    altClickMovesCursor: true,
    convertEol: false,
    // Screen-reader mode is opt-out by default; it adds a live region that hurts
    // throughput. The PTY itself is xterm-256color (RequestPty, server side).
    screenReaderMode: false,
  });

  /* ── Addons ───────────────────────────────────────────── */
  var fitAddon = null;
  var searchAddon = null;
  var serializeAddon = null;

  function loadAddon(globalName, ctor) {
    try {
      var ns = window[globalName];
      if (ns && ns[ctor]) {
        var addon = new ns[ctor]();
        term.loadAddon(addon);
        return addon;
      }
    } catch (e) { /* a single addon failing must never break the terminal */ }
    return null;
  }

  if (window.FitAddon && window.FitAddon.FitAddon) {
    fitAddon = new FitAddon.FitAddon();
    term.loadAddon(fitAddon);
  }

  term.open(container);
  container.style.background = currentTheme().background;

  // Renderer: WebGL on desktop (with canvas fallback), canvas on mobile. Must be
  // loaded after term.open(). xterm's DOM renderer is the implicit last resort.
  function loadCanvasRenderer() {
    loadAddon('CanvasAddon', 'CanvasAddon');
  }
  (function loadRenderer() {
    if (isMobile) { loadCanvasRenderer(); return; }
    try {
      var ns = window.WebglAddon;
      if (ns && ns.WebglAddon) {
        var webgl = new ns.WebglAddon();
        // A lost GPU context (tab backgrounded, driver reset) must not blank the
        // terminal — drop to canvas if it happens.
        if (webgl.onContextLoss) {
          webgl.onContextLoss(function () {
            try { webgl.dispose(); } catch (e) { /* ignore */ }
            loadCanvasRenderer();
          });
        }
        term.loadAddon(webgl);
        return;
      }
    } catch (e) { /* WebGL unavailable → canvas */ }
    loadCanvasRenderer();
  })();

  // Wide-char / emoji widths.
  var uni = loadAddon('Unicode11Addon', 'Unicode11Addon');
  if (uni) { try { term.unicode.activeVersion = '11'; } catch (e) { /* ignore */ } }

  // Clickable URLs.
  loadAddon('WebLinksAddon', 'WebLinksAddon');

  // Search + serialize (scrollback export).
  if (window.SearchAddon && window.SearchAddon.SearchAddon) {
    searchAddon = new SearchAddon.SearchAddon();
    term.loadAddon(searchAddon);
  }
  if (window.SerializeAddon && window.SerializeAddon.SerializeAddon) {
    serializeAddon = new SerializeAddon.SerializeAddon();
    term.loadAddon(serializeAddon);
  }

  /* ── Soft-keyboard hardening ──────────────────────────── */
  // xterm's helper <textarea> disables autocorrect/autocapitalize/spellcheck but
  // not autocomplete. Gboard keeps a predictive "composing" region active and
  // re-inserts whole suggested words ("rebecca-" silently becomes
  // "rebecca-rebecca"). Forcing plain input with no prediction stops it.
  if (term.textarea) {
    term.textarea.setAttribute('autocomplete', 'off');
    term.textarea.setAttribute('autocorrect', 'off');
    term.textarea.setAttribute('autocapitalize', 'none');
    term.textarea.setAttribute('spellcheck', 'false');
    term.textarea.setAttribute('inputmode', 'text');
  }

  /* ── Status bar (dims + latency) ──────────────────────── */
  var dimsEl = byId('term-dims');
  var latencyEl = byId('term-latency');
  function updateDims() {
    if (dimsEl && term.cols && term.rows) dimsEl.textContent = term.cols + '×' + term.rows;
  }

  /* ── Fit helper ───────────────────────────────────────── */
  function fitAndResize() {
    if (!active) return;
    if (fitAddon) {
      try { fitAddon.fit(); } catch (e) { /* ignore */ }
    }
    updateDims();
    if (ws && ws.readyState === WebSocket.OPEN && term.cols && term.rows) {
      ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
    }
  }

  /* ── Input helper ─────────────────────────────────────── */
  function sendInput(data) {
    if (data && ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'input', data: data }));
    }
  }

  /* ── Clipboard write (with non-secure-context fallback) ── */
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
  function promptPaste() {
    try {
      var text = window.prompt(T('js.terminal.paste_prompt'));
      if (text) sendInput(text);
    } catch (e) { /* prompt unavailable */ }
  }

  /* ── Selection sources ────────────────────────────────── */
  function currentSelection() {
    var sel = '';
    try { sel = window.getSelection ? String(window.getSelection()) : ''; } catch (e) { /* ignore */ }
    if (sel) return sel;
    try { return term.hasSelection() ? term.getSelection() : ''; } catch (e) { return ''; }
  }

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
    }).catch(noop);
  }

  // Returns true synchronously if a selection existed (so the keybinding handler
  // can decide whether to suppress ^C); the actual write is async/best-effort.
  function copySelection() {
    var text = currentSelection();
    if (!text) return false;
    writeClipboard(text).catch(noop);
    return true;
  }

  function selectAllBuffer() {
    try { term.selectAll(); } catch (e) { /* ignore */ }
  }

  /* ── Font size control ────────────────────────────────── */
  function setFontSize(px) {
    px = Math.max(FONT_MIN, Math.min(FONT_MAX, px));
    if (px === term.options.fontSize) return;
    term.options.fontSize = px;
    try { window.localStorage.setItem(FONT_KEY, String(px)); } catch (e) { /* ignore */ }
    if (selectPre) selectPre.style.fontSize = px + 'px';
    // Defer one frame so xterm has recomputed cell dims for the new size.
    requestAnimationFrame(fitAndResize);
  }

  /* ── Theme switching ──────────────────────────────────── */
  function applyTheme(id) {
    if (!ThemeStore || !ThemeStore.themes[id]) return;
    themeId = id;
    term.options.theme = ThemeStore.themes[id];
    container.style.background = ThemeStore.themes[id].background;
    ThemeStore.save(id);
    syncThemeMenu();
  }

  /* ── Theme menu ───────────────────────────────────────── */
  var themeMenu = byId('terminal-theme-menu');
  var themeBtn = byId('term-tool-theme');
  function buildThemeMenu() {
    if (!themeMenu || !ThemeStore) return;
    themeMenu.innerHTML = '';
    ThemeStore.order.forEach(function (id) {
      var b = document.createElement('button');
      b.type = 'button';
      b.className = 'terminal-theme-menu__item';
      b.setAttribute('role', 'menuitemradio');
      b.setAttribute('data-theme', id);
      b.textContent = ThemeStore.names[id] || id;
      var sw = document.createElement('span');
      sw.className = 'terminal-theme-menu__swatch';
      sw.style.background = ThemeStore.themes[id].background;
      sw.style.borderColor = ThemeStore.themes[id].foreground;
      b.insertBefore(sw, b.firstChild);
      b.addEventListener('click', function () {
        applyTheme(id);
        toggleThemeMenu(false);
        term.focus();
      });
      themeMenu.appendChild(b);
    });
    syncThemeMenu();
  }
  function syncThemeMenu() {
    if (!themeMenu) return;
    var items = themeMenu.querySelectorAll('.terminal-theme-menu__item');
    for (var i = 0; i < items.length; i++) {
      var active = items[i].getAttribute('data-theme') === themeId;
      items[i].classList.toggle('is-active', active);
      items[i].setAttribute('aria-checked', active ? 'true' : 'false');
    }
  }
  function toggleThemeMenu(force) {
    if (!themeMenu) return;
    var show = typeof force === 'boolean' ? force : themeMenu.hidden;
    setShown(themeMenu, show, 'flex');
    if (themeBtn) themeBtn.setAttribute('aria-expanded', show ? 'true' : 'false');
  }
  if (themeBtn) {
    themeBtn.addEventListener('click', function (e) {
      e.stopPropagation();
      toggleThemeMenu();
    });
  }
  // Dismiss the menu on any outside click / Escape.
  document.addEventListener('click', function (e) {
    if (themeMenu && !themeMenu.hidden && !themeMenu.contains(e.target) && e.target !== themeBtn) {
      toggleThemeMenu(false);
    }
  });
  buildThemeMenu();
  // Force-hide at startup so a stale stylesheet can never leave it open.
  setShown(themeMenu, false, 'flex');

  /* ── Fullscreen ───────────────────────────────────────── */
  function fsElement() {
    return document.fullscreenElement || document.webkitFullscreenElement || null;
  }
  function toggleFullscreen() {
    var el = card;
    if (fsElement()) {
      var exit = document.exitFullscreen || document.webkitExitFullscreen;
      if (exit) try { exit.call(document); } catch (e) { /* ignore */ }
      return;
    }
    var req = el.requestFullscreen || el.webkitRequestFullscreen;
    if (req) {
      try {
        var p = req.call(el);
        if (p && p.catch) p.catch(noop);
      } catch (e) { /* iOS Safari only supports fullscreen on <video> */ }
    }
    // On mobile the card is already CSS-fullscreen (terminal-card--mobile), so a
    // missing Fullscreen API is not a regression.
  }
  document.addEventListener('fullscreenchange', function () {
    requestAnimationFrame(fitAndResize);
  });
  document.addEventListener('webkitfullscreenchange', function () {
    requestAnimationFrame(fitAndResize);
  });

  /* ── Search bar ───────────────────────────────────────── */
  var searchBar = byId('terminal-search');
  var searchInput = byId('terminal-search-input');
  var searchCount = byId('terminal-search-count');
  var searchCaseBtn = byId('terminal-search-case');
  var caseSensitive = false;

  function searchOpts() {
    return { caseSensitive: caseSensitive, regex: false, wholeWord: false };
  }
  function runSearch(forward) {
    if (!searchAddon || !searchInput) return;
    var q = searchInput.value;
    if (!q) { if (searchAddon.clearDecorations) try { searchAddon.clearDecorations(); } catch (e) {} updateSearchCount(0, 0); return; }
    try {
      if (forward) searchAddon.findNext(q, searchOpts());
      else searchAddon.findPrevious(q, searchOpts());
    } catch (e) { /* ignore */ }
  }
  function updateSearchCount(index, count) {
    if (!searchCount) return;
    if (!count) searchCount.textContent = searchInput && searchInput.value ? T('js.terminal.search_none') : '';
    else searchCount.textContent = (index + 1) + ' / ' + count;
  }
  if (searchAddon && searchAddon.onDidChangeResults) {
    searchAddon.onDidChangeResults(function (res) {
      if (!res) { updateSearchCount(0, 0); return; }
      updateSearchCount(res.resultIndex < 0 ? -1 : res.resultIndex, res.resultCount || 0);
    });
  }
  function openSearch() {
    if (!searchBar) return;
    setShown(searchBar, true, 'flex');
    if (searchInput) { searchInput.focus(); searchInput.select(); }
    if (searchInput && searchInput.value) runSearch(true);
  }
  function closeSearch() {
    if (!searchBar) return;
    setShown(searchBar, false, 'flex');
    if (searchAddon && searchAddon.clearDecorations) try { searchAddon.clearDecorations(); } catch (e) {}
    term.focus();
  }
  function toggleSearch() {
    if (searchBar && searchBar.hidden) openSearch();
    else closeSearch();
  }
  if (searchInput) {
    searchInput.addEventListener('input', function () { runSearch(true); });
    searchInput.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') { e.preventDefault(); runSearch(!e.shiftKey); }
      else if (e.key === 'Escape') { e.preventDefault(); closeSearch(); }
    });
  }
  bindClick('terminal-search-next', function () { runSearch(true); });
  bindClick('terminal-search-prev', function () { runSearch(false); });
  bindClick('terminal-search-close', closeSearch);
  // Force-hide at startup so a stale stylesheet can never leave the bar showing.
  setShown(searchBar, false, 'flex');
  if (searchCaseBtn) {
    searchCaseBtn.addEventListener('click', function () {
      caseSensitive = !caseSensitive;
      searchCaseBtn.classList.toggle('is-active', caseSensitive);
      searchCaseBtn.setAttribute('aria-pressed', caseSensitive ? 'true' : 'false');
      runSearch(true);
    });
  }

  function bindClick(id, fn) {
    var el = byId(id);
    if (el) el.addEventListener('click', function (e) { e.preventDefault(); fn(); });
  }

  /* ── Header tool buttons ──────────────────────────────── */
  bindClick('term-tool-search', toggleSearch);
  bindClick('term-tool-font-dec', function () { setFontSize(term.options.fontSize - 1); term.focus(); });
  bindClick('term-tool-font-inc', function () { setFontSize(term.options.fontSize + 1); term.focus(); });
  bindClick('term-tool-fullscreen', toggleFullscreen);

  /* ── Mobile tab access ──────────────────────────────────
   * On mobile the terminal owns the full screen and the tab bar is hidden
   * behind the fixed card. This button opens the existing tab switcher
   * (same component the FAB uses) so the user can peek at other tabs and
   * come back. Reuses NodexiaTabs.showSwitcher / hideSwitcher. */
  var terminalTabsBtn = byId('terminal-tabs');
  if (terminalTabsBtn) {
    terminalTabsBtn.addEventListener('click', function () {
      if (window.NodexiaTabs && typeof window.NodexiaTabs.showSwitcher === 'function') {
        window.NodexiaTabs.showSwitcher();
      }
    });
  }

  /* ── Keybindings ──────────────────────────────────────── */
  if (window.NodexiaTermKeybindings) {
    window.NodexiaTermKeybindings.attach(term, {
      copySelection: copySelection,
      paste: doPaste,
      selectAll: selectAllBuffer,
      openSearch: openSearch,
      clear: function () { try { term.clear(); } catch (e) {} },
      fontInc: function () { setFontSize(term.options.fontSize + 1); },
      fontDec: function () { setFontSize(term.options.fontSize - 1); },
      fontReset: function () { setFontSize(defaultFontSize()); },
      reconnect: reconnect,
      scrollLines: function (n) { try { term.scrollLines(n); } catch (e) {} },
    });
  }

  /* ── Right-click context menu (desktop) ───────────────── */
  var ctxMenu = null;
  function buildContextMenu() {
    ctxMenu = document.createElement('div');
    ctxMenu.className = 'terminal-context-menu';
    setShown(ctxMenu, false, 'flex');
    [
      { label: T('js.terminal.ctx_copy'),       fn: function () { doCopy(currentSelection(), null); } },
      { label: T('js.terminal.ctx_paste'),      fn: doPaste },
      { label: T('js.terminal.ctx_select_all'), fn: selectAllBuffer },
      { label: T('js.terminal.ctx_search'),     fn: openSearch },
      { label: T('js.terminal.ctx_clear'),      fn: function () { try { term.clear(); } catch (e) {} } },
    ].forEach(function (item) {
      var b = document.createElement('button');
      b.type = 'button';
      b.className = 'terminal-context-menu__item';
      b.textContent = item.label;
      b.addEventListener('click', function () { hideContextMenu(); item.fn(); });
      ctxMenu.appendChild(b);
    });
    card.appendChild(ctxMenu);
  }
  function showContextMenu(x, y) {
    if (!ctxMenu) buildContextMenu();
    setShown(ctxMenu, true, 'flex');
    var rect = card.getBoundingClientRect();
    var mx = Math.min(x - rect.left, rect.width - 170);
    var my = Math.min(y - rect.top, rect.height - 10);
    ctxMenu.style.left = Math.max(0, mx) + 'px';
    ctxMenu.style.top = Math.max(0, my) + 'px';
  }
  function hideContextMenu() { if (ctxMenu) setShown(ctxMenu, false, 'flex'); }
  if (!isMobile) {
    container.addEventListener('contextmenu', function (e) {
      e.preventDefault();
      showContextMenu(e.clientX, e.clientY);
    });
    document.addEventListener('click', function (e) {
      if (ctxMenu && !ctxMenu.hidden && !ctxMenu.contains(e.target)) hideContextMenu();
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') { hideContextMenu(); toggleThemeMenu(false); }
    });
  }

  /* ── Ctrl / Alt one-shot modifiers (mobile toolbar) ───── */
  var ctrlPending = false, altPending = false;
  var ctrlBtn = null, altBtn = null;
  function setModBtn(btn, on) {
    if (!btn) return;
    btn.classList.toggle('is-active', on);
    btn.setAttribute('aria-pressed', on ? 'true' : 'false');
  }
  function setCtrl(on) { ctrlPending = on; setModBtn(ctrlBtn, on); }
  function setAlt(on)  { altPending = on;  setModBtn(altBtn, on); }

  function ctrlCombine(data) {
    if (!data || data.length !== 1) return null;
    var code = data.toLowerCase().charCodeAt(0);
    if (code >= 97 && code <= 122) return String.fromCharCode(code - 96);
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
    if (ctrlPending || altPending) {
      var out = data;
      if (ctrlPending) {
        var combined = ctrlCombine(data);
        if (combined !== null) out = combined;
      }
      if (altPending) out = '\x1b' + out; // Alt+key → ESC prefix (xterm convention)
      setCtrl(false);
      setAlt(false);
      sendInput(out);
      return;
    }
    sendInput(data);
  });

  /* ── WebSocket ────────────────────────────────────────── */
  var ws = null;
  var heartbeatTimer = null;
  var connectTimer = null;
  var lastPingAt = 0;
  var userClosing = false;

  function startHeartbeat() {
    stopHeartbeat();
    heartbeatTimer = setInterval(function () {
      if (ws && ws.readyState === WebSocket.OPEN) {
        lastPingAt = Date.now();
        ws.send(JSON.stringify({ type: 'heartbeat' }));
      }
    }, 30000);
  }
  function stopHeartbeat() {
    if (heartbeatTimer) { clearInterval(heartbeatTimer); heartbeatTimer = null; }
  }
  function onHeartbeatEcho() {
    if (!latencyEl || !lastPingAt) return;
    var rtt = Date.now() - lastPingAt;
    latencyEl.textContent = rtt + ' ms';
  }

  var initSent = false;
  function maybeSendInitCmd() {
    if (initSent || !initCmd) return;
    initSent = true;
    setTimeout(function () {
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'input', data: initCmd + '\n' }));
      }
    }, 150);
  }

  function connect() {
    var wsScheme = window.location.protocol === 'https:' ? 'wss://' : 'ws://';
    var wsURL = wsScheme + window.location.host + wsBase + '?ticket=' + encodeURIComponent(ticket);
    ws = new WebSocket(wsURL);

    connectTimer = setTimeout(function () {
      if (!ws || ws.readyState === WebSocket.CONNECTING) {
        setStatus('error', T('js.terminal.status_error'));
        showError(T('js.terminal.connection_timeout'));
        try { ws.close(1000, 'connect timeout'); } catch (e) { /* ignore */ }
      }
    }, 30000);

    ws.onopen = function () {
      if (connectTimer) { clearTimeout(connectTimer); connectTimer = null; }
      clearError();
      hideDisconnectOverlay();
      setStatus('connected', T('js.terminal.connected'));
      startHeartbeat();
      fitAndResize();
      term.focus();
      if (initCmd) setTimeout(maybeSendInitCmd, 1200);
    };

    ws.onmessage = function (event) {
      var msg;
      try { msg = JSON.parse(event.data); } catch (e) { return; }
      switch (msg.type) {
        case 'output':
          term.write(msg.data);
          maybeSendInitCmd();
          break;
        case 'error':
          showError(msg.message);
          setStatus('error', T('js.terminal.status_error'));
          break;
        case 'status':
          if (msg.state === 'connected') setStatus('connected', T('js.terminal.connected'));
          else if (msg.state === 'reconnecting') setStatus('reconnecting', T('js.terminal.reconnecting'));
          break;
        case 'heartbeat':
          onHeartbeatEcho();
          break;
      }
    };

    ws.onerror = function () {
      if (connectTimer) { clearTimeout(connectTimer); connectTimer = null; }
      setStatus('error', T('js.terminal.connection_error'));
      showError(T('js.terminal.ws_failed'));
    };

    ws.onclose = function (event) {
      stopHeartbeat();
      setStatus('disconnected', T('js.terminal.disconnected'));
      setScrollLock(false);
      if (!userClosing && !event.wasClean) {
        showDisconnectOverlay(T('js.terminal.closed_unexpectedly', { code: event.code }));
      } else if (!userClosing) {
        // Clean close (remote shell exited): still offer a quick way back in.
        showDisconnectOverlay(T('js.terminal.session_ended'));
      }
    };
  }

  // The WS ticket is single-use, so "reconnect" re-enters the credential page
  // (which issues a fresh ticket) rather than re-dialing the consumed socket.
  function reconnect() {
    userClosing = true;
    try { if (ws) ws.close(1000, 'reconnecting'); } catch (e) { /* ignore */ }
    setScrollLock(false);
    tabNavigate(pageURL);
  }

  /* ── Disconnect overlay (Reconnect action) ────────────── */
  var disconnectOverlay = null;
  function showDisconnectOverlay(message) {
    if (!disconnectOverlay) {
      disconnectOverlay = document.createElement('div');
      disconnectOverlay.className = 'terminal-disconnect';
      var msg = document.createElement('p');
      msg.className = 'terminal-disconnect__msg';
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'btn btn--primary';
      btn.textContent = T('js.terminal.reconnect');
      btn.addEventListener('click', reconnect);
      disconnectOverlay.appendChild(msg);
      disconnectOverlay.appendChild(btn);
      container.appendChild(disconnectOverlay);
    }
    disconnectOverlay.querySelector('.terminal-disconnect__msg').textContent = message;
    setShown(disconnectOverlay, true, 'flex');
  }
  function hideDisconnectOverlay() {
    if (disconnectOverlay) setShown(disconnectOverlay, false, 'flex');
  }

  /* ── Back button ──────────────────────────────────────── */
  var backBtn = byId('terminal-back');
  if (backBtn) {
    backBtn.addEventListener('click', function () {
      userClosing = true;
      setScrollLock(false);
      try { if (ws) ws.close(1000, 'closed by user'); } catch (e) { /* ignore */ }
      if (window.history.length > 1) window.history.back();
      else tabNavigate('/servers');
    });
  }
  window.addEventListener('pagehide', function () { setScrollLock(false); });

  /* ── Select mode (mobile) ─────────────────────────────── */
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
      // Prefer the serialize addon's plain-text export when available; fall back
      // to a manual buffer walk. Both are plain text (no escape sequences).
      selectPre.textContent = getBufferText(false);
      selectOverlay.hidden = false;
      selectOverlay.scrollTop = selectOverlay.scrollHeight;
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
    pgup:  '\x1b[5~',
    pgdn:  '\x1b[6~',
  };

  // Keycap glyphs (Ctrl/Alt/Esc/Tab/arrows/Home/…/A−/A+) stay Latin even in RTL
  // technical UIs by convention; only aria-labels and the action words
  // (Select/Copy/Paste) are localized.
  var SEP = { kind: 'sep' };
  var BUTTONS = [
    { label: 'Esc',   kind: 'seq', key: 'esc' },
    { label: 'Ctrl',  kind: 'ctrl' },
    { label: 'Alt',   kind: 'alt' },
    { label: 'Tab',   kind: 'seq', key: 'tab' },
    SEP,
    { label: '←', kind: 'seq', key: 'left',  aria: T('js.terminal.aria_left') },
    { label: '↑', kind: 'seq', key: 'up',    aria: T('js.terminal.aria_up') },
    { label: '↓', kind: 'seq', key: 'down',  aria: T('js.terminal.aria_down') },
    { label: '→', kind: 'seq', key: 'right', aria: T('js.terminal.aria_right') },
    SEP,
    { label: '|', kind: 'lit' },
    { label: '>', kind: 'lit' },
    { label: '<', kind: 'lit' },
    { label: '/', kind: 'lit' },
    { label: '~', kind: 'lit' },
    { label: '$', kind: 'lit' },
    { label: '`', kind: 'lit' },
    { label: '!', kind: 'lit' },
    { label: '-', kind: 'lit' },
    SEP,
    { label: 'Home', kind: 'seq', key: 'home' },
    { label: 'End',  kind: 'seq', key: 'end' },
    { label: 'PgUp', kind: 'seq', key: 'pgup' },
    { label: 'PgDn', kind: 'seq', key: 'pgdn' },
    { label: 'Del',  kind: 'seq', key: 'del' },
    SEP,
    { label: T('js.terminal.select'),   kind: 'select',  aria: T('js.terminal.aria_select') },
    { label: '⌕',                       kind: 'search',  aria: T('js.terminal.search_label') },
    { label: T('js.copy.label'),        kind: 'copy',    aria: T('js.terminal.aria_copy') },
    { label: T('js.terminal.copy_all'), kind: 'copyall', aria: T('js.terminal.aria_copy_all') },
    { label: T('js.terminal.paste'),    kind: 'paste' },
    { label: 'A−', kind: 'font', key: 'dec', aria: T('js.terminal.aria_font_smaller') },
    { label: 'A+', kind: 'font', key: 'inc', aria: T('js.terminal.aria_font_larger') },
    { label: '⛶',  kind: 'fullscreen', aria: T('js.terminal.fullscreen') },
  ];

  // Actions that touch a selection must not pull focus back to the terminal —
  // that would clear the selection and pop the soft keyboard.
  var NO_REFOCUS = { select: true, copy: true, copyall: true, search: true };

  function handleKey(def, btn) {
    switch (def.kind) {
      case 'seq':        sendInput(SEQ[def.key]); break;
      case 'lit':        sendInput(def.label); break;
      case 'ctrl':       setCtrl(!ctrlPending); break;
      case 'alt':        setAlt(!altPending); break;
      case 'select':     setSelectMode(!selecting); break;
      case 'copy':       doCopy(currentSelection(), btn); break;
      case 'copyall':    doCopy(getBufferText(false), btn); break;
      case 'paste':      doPaste(); break;
      case 'search':     openSearch(); break;
      case 'font':       setFontSize(term.options.fontSize + (def.key === 'inc' ? 1 : -1)); break;
      case 'fullscreen': toggleFullscreen(); break;
    }
  }

  function buildToolbar() {
    var bar = document.createElement('div');
    bar.className = 'terminal-toolbar';
    bar.setAttribute('role', 'toolbar');
    bar.setAttribute('aria-label', T('js.terminal.keys_label'));

    BUTTONS.forEach(function (def) {
      if (def.kind === 'sep') {
        var sep = document.createElement('span');
        sep.className = 'terminal-key-sep';
        sep.setAttribute('aria-hidden', 'true');
        bar.appendChild(sep);
        return;
      }
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'terminal-key';
      btn.textContent = def.label;
      btn.tabIndex = -1;
      btn.setAttribute('data-label', def.label);
      btn.setAttribute('aria-label', def.aria || def.label);

      if (def.kind === 'ctrl') { btn.classList.add('terminal-key--ctrl'); btn.setAttribute('aria-pressed', 'false'); ctrlBtn = btn; }
      if (def.kind === 'alt')  { btn.classList.add('terminal-key--alt');  btn.setAttribute('aria-pressed', 'false'); altBtn = btn; }
      if (def.kind === 'select') { btn.classList.add('terminal-key--select'); btn.setAttribute('aria-pressed', 'false'); selectBtn = btn; }

      btn.addEventListener('mousedown', function (e) { e.preventDefault(); });
      btn.addEventListener('click', function (e) {
        e.preventDefault();
        handleKey(def, btn);
        if (!NO_REFOCUS[def.kind]) term.focus();
      });
      bar.appendChild(btn);
    });

    card.appendChild(bar);
  }

  /* ── Mobile full-screen layout + keyboard reflow ──────── */
  var vv = window.visualViewport;
  var rafPending = false;

  function updateMobileViewport() {
    if (!active) return;
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
    requestAnimationFrame(function () { rafPending = false; updateMobileViewport(); });
  }
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
    if (isMobile) { scheduleViewportUpdate(); return; }
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(fitAndResize, 80);
  });

  if (isMobile) enableMobile();

  /* ── Connect + initial fit ────────────────────────────── */
  connect();
  requestAnimationFrame(function () {
    if (isMobile) updateMobileViewport();
    else fitAndResize();
  });

  /* ── v0.6.0 tab-lifecycle surface ──────────────────────── */
  // Bridges this pane's terminal.js instance to terminal-tab-adapter.js. The
  // two files never reference each other directly — only through this object
  // and the nodexia:terminal-status/ready DOM events.
  card.__nodexiaTerminal = {
    pause: function () { active = false; },
    resume: function () { active = true; fitAndResize(); term.focus(); },
    dispose: function () {
      userClosing = true;
      if (connectTimer) { clearTimeout(connectTimer); connectTimer = null; }
      stopHeartbeat();
      try { if (ws) ws.close(1000, 'tab closed'); } catch (e) {}
      try { term.dispose(); } catch (e) {}
    },
    isConnected: function () { return !!(ws && ws.readyState === WebSocket.OPEN); }
  };
  card.dispatchEvent(new CustomEvent('nodexia:terminal-ready', { bubbles: true }));
})();
