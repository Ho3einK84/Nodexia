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
 * Resume note: the WS ticket creates at most one SSH shell. After consumption it
 * is also the opaque handle for reattaching that same shell, so a mobile network
 * suspension can replace the WebSocket without creating a second PTY.
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

  // Terminal panes are created and destroyed without a full page unload. Keep
  // global listener references so dispose() can release the removed pane and
  // its xterm instance instead of retaining them through document/window.
  var globalListeners = [];
  function addGlobalListener(target, type, listener, options) {
    target.addEventListener(type, listener, options);
    globalListeners.push({ target: target, type: type, listener: listener, options: options });
  }
  function removeGlobalListeners() {
    globalListeners.forEach(function (entry) {
      entry.target.removeEventListener(entry.type, entry.listener, entry.options);
    });
    globalListeners = [];
  }

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

  var ticket    = card.getAttribute('data-ticket');
  var wsBase    = card.getAttribute('data-ws-url');
  var csrfToken = card.getAttribute('data-csrf') || '';
  var initCmd   = card.getAttribute('data-init-cmd') || '';
  if (!ticket || !wsBase) return;

  // The credential page for this server (used by Reconnect): strip the /ws tail.
  var pageURL   = wsBase.replace(/\/ws$/, '');
  var uploadURL = wsBase.replace(/\/ws$/, '/upload');

  var container = byId('terminal-container');
  var statusEl  = byId('terminal-status');
  var statusTextEl = statusEl ? statusEl.querySelector('.terminal-status__text') : null;
  var errorEl   = byId('terminal-error');

  // v0.6.0: true while this pane is the visible/foreground tab. Gates only the
  // resize/measurement work below; the WebSocket, heartbeat, and PTY input
  // keep running regardless so a backgrounded tab never loses output.
  var active = true;

  var isMobile = window.matchMedia('(max-width: 700px)').matches;
  var FONT_KEY = isMobile ? 'nodexia.terminal.fontSize.mobile' : 'nodexia.terminal.fontSize';
  var FONT_MIN = 9;
  var FONT_MAX = 24;

  function defaultFontSize() { return isMobile ? 11 : 13; }

  /* ── Status helpers ───────────────────────────────────── */
  function setStatus(state, text) {
    if (!statusEl) return;
    statusEl.className = 'terminal-status terminal-status--' + state;
    statusEl.setAttribute('title', text);
    statusEl.setAttribute('aria-label', text);
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
    fontFamily: 'ui-monospace, "SF Mono", "Cascadia Code", "Roboto Mono", "DejaVu Sans Mono", "Liberation Mono", Menlo, Monaco, Consolas, monospace',
    fontSize: initialFontSize(),
    letterSpacing: 0,
    lineHeight: 1.18,
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
  updateFontValDisplay();

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

  /* ── Fast Clipboard paste (with non-secure-context fallback) ── */
  function fastPaste(text) {
    if (!text) return;
    // Normalize newlines for interactive PTY shell
    var normalized = text.replace(/\r\n/g, '\r').replace(/\n/g, '\r');
    var CHUNK = 16384;
    if (normalized.length <= CHUNK) {
      sendInput(normalized);
    } else {
      for (var i = 0; i < normalized.length; i += CHUNK) {
        sendInput(normalized.slice(i, i + CHUNK));
      }
    }
  }

  function doPaste() {
    if (navigator.clipboard && navigator.clipboard.readText) {
      navigator.clipboard.readText().then(fastPaste).catch(promptPaste);
    } else {
      promptPaste();
    }
  }
  function promptPaste() {
    try {
      var text = window.prompt(T('js.terminal.paste_prompt'));
      if (text) fastPaste(text);
    } catch (e) { /* prompt unavailable */ }
  }

  // Intercept native browser paste on container and textarea for instant response
  function handleNativePaste(e) {
    var clip = e.clipboardData || window.clipboardData;
    if (!clip) return;
    var text = clip.getData('text');
    if (text) {
      e.preventDefault();
      e.stopPropagation();
      fastPaste(text);
    }
  }
  container.addEventListener('paste', handleNativePaste);
  card.addEventListener('paste', handleNativePaste);
  if (term.textarea) {
    term.textarea.addEventListener('paste', handleNativePaste);
  }

  /* ── Persian language & output reshaping ──────────────── */
  var PERSIAN_KEY = 'nodexia.terminal.persianMode';
  var persianMode = false;
  try {
    var storedPM = window.localStorage.getItem(PERSIAN_KEY);
    if (storedPM !== null) persianMode = (storedPM === 'true');
  } catch (e) {}

  var outputBuffer = '';
  var outputRaf = null;

  function flushOutput() {
    outputRaf = null;
    if (!outputBuffer) return;
    var chunk = outputBuffer;
    outputBuffer = '';
    term.write(chunk);
  }

  function writeTerminalOutput(data) {
    if (!data) return;
    var processed = data;
    // Guard: only reshape multiline or complete blocks of Persian text, never single-character typing echo
    if (persianMode && window.NodexiaPersian && data.length > 2 && (data.indexOf('\n') !== -1 || data.indexOf('\r') !== -1 || data.length > 16) && window.NodexiaPersian.hasPersian(data)) {
      processed = window.NodexiaPersian.reshapeOutput(data);
    }
    if (processed.length > 8192) {
      if (outputBuffer) {
        term.write(outputBuffer);
        outputBuffer = '';
      }
      term.write(processed);
    } else {
      outputBuffer += processed;
      if (!outputRaf) {
        outputRaf = requestAnimationFrame(flushOutput);
      }
    }
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
    var origHTML = btn.innerHTML;
    var origText = btn.getAttribute('data-label') || btn.textContent;
    btn.classList.add('is-copied');
    if (btn.querySelector('svg')) {
      btn.innerHTML = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>';
    } else {
      btn.textContent = T('js.copy.copied');
    }
    setTimeout(function () {
      if (btn.querySelector('svg') || origHTML.indexOf('svg') !== -1) {
        btn.innerHTML = origHTML;
      } else {
        btn.textContent = origText;
      }
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
  function updateFontValDisplay() {
    var valEl = byId('term-more-font-val');
    if (valEl && term && term.options) {
      valEl.textContent = (term.options.fontSize || defaultFontSize()) + 'px';
    }
  }

  function setFontSize(px) {
    px = Math.max(FONT_MIN, Math.min(FONT_MAX, px));
    updateFontValDisplay();
    if (px === term.options.fontSize) return;
    term.options.fontSize = px;
    try { window.localStorage.setItem(FONT_KEY, String(px)); } catch (e) { /* ignore */ }
    if (selectPre) selectPre.style.fontSize = px + 'px';
    updateFontValDisplay();
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
      b.addEventListener('click', function (e) {
        if (e && e.stopPropagation) e.stopPropagation();
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
    if (show) syncThemeMenu();
    if (themeBtn) themeBtn.setAttribute('aria-expanded', show ? 'true' : 'false');
  }
  /* ── More menu ────────────────────────────────────────── */
  var moreMenu = byId('terminal-more-menu');
  var moreBtn = byId('term-tool-more');

  function openMoreMenu() {
    if (!moreMenu) return;
    setShown(moreMenu, true, 'flex');
    updateFontValDisplay();
    if (moreBtn) {
      moreBtn.setAttribute('aria-expanded', 'true');
      moreBtn.classList.add('is-active');
    }
    if (themeMenu && !themeMenu.hidden) toggleThemeMenu(false);
  }

  function closeMoreMenu() {
    if (!moreMenu) return;
    setShown(moreMenu, false);
    if (moreBtn) {
      moreBtn.setAttribute('aria-expanded', 'false');
      moreBtn.classList.remove('is-active');
    }
  }

  function toggleMoreMenu(force) {
    if (!moreMenu) return;
    var show = typeof force === 'boolean' ? force : moreMenu.hidden;
    if (show) openMoreMenu();
    else closeMoreMenu();
  }

  if (themeBtn) {
    themeBtn.addEventListener('click', function (e) {
      e.stopPropagation();
      closeMoreMenu();
      toggleThemeMenu();
    });
  }
  // Dismiss menus on any outside click / Escape.
  addGlobalListener(document, 'click', function (e) {
    var moreThemeBtn = byId('term-more-theme');
    var isThemeTrigger = (themeBtn && (e.target === themeBtn || themeBtn.contains(e.target))) ||
                         (moreThemeBtn && (e.target === moreThemeBtn || moreThemeBtn.contains(e.target)));
    if (themeMenu && !themeMenu.hidden && !themeMenu.contains(e.target) && !isThemeTrigger) {
      toggleThemeMenu(false);
    }
    var isMoreTrigger = moreBtn && (e.target === moreBtn || moreBtn.contains(e.target));
    if (moreMenu && !moreMenu.hidden && !moreMenu.contains(e.target) && !isMoreTrigger) {
      closeMoreMenu();
    }
  });
  buildThemeMenu();
  // Force-hide at startup so a stale stylesheet can never leave it open.
  setShown(themeMenu, false, 'flex');
  setShown(moreMenu, false, 'flex');

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
  addGlobalListener(document, 'fullscreenchange', function () {
    requestAnimationFrame(fitAndResize);
  });
  addGlobalListener(document, 'webkitfullscreenchange', function () {
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
    if (el) el.addEventListener('click', function (e) { e.preventDefault(); fn(e); });
  }

  /* ── Quick Snippets & Command Runner (Termius-inspired) ── */
  var DEFAULT_SNIPPETS = [
    // System
    { id: 'sys-htop',   cat: 'System',      name: 'HTop Monitor',          cmd: 'htop' },
    { id: 'sys-df',     cat: 'System',      name: 'Disk Usage',            cmd: 'df -h' },
    { id: 'sys-free',   cat: 'System',      name: 'Memory Stats',          cmd: 'free -h' },
    { id: 'sys-uptime', cat: 'System',      name: 'Uptime & Load',         cmd: 'uptime' },
    { id: 'sys-uname',  cat: 'System',      name: 'Kernel & OS',           cmd: 'uname -a' },
    // Services
    { id: 'srv-nodexia-st', cat: 'Services', name: 'Nodexia Status',       cmd: 'systemctl status nodexia' },
    { id: 'srv-nodexia-log', cat: 'Services', name: 'Nodexia Logs (50)',   cmd: 'journalctl -u nodexia -n 50 --no-pager' },
    { id: 'srv-nodexia-f',  cat: 'Services', name: 'Follow Nodexia Logs',  cmd: 'journalctl -u nodexia -f' },
    { id: 'srv-nodexia-re', cat: 'Services', name: 'Restart Nodexia',      cmd: 'systemctl restart nodexia' },
    { id: 'srv-docker-ps',  cat: 'Services', name: 'Docker Containers',    cmd: 'docker ps -a' },
    // Network
    { id: 'net-ports',  cat: 'Network',     name: 'Listening Ports',       cmd: 'ss -tulpn' },
    { id: 'net-ip',     cat: 'Network',     name: 'Network Interfaces',    cmd: 'ip -c a' },
    { id: 'net-pubip',  cat: 'Network',     name: 'Public IPv4',           cmd: 'curl -s https://api.ipify.org && echo' },
    { id: 'net-ping',   cat: 'Network',     name: 'Ping Cloudflare DNS',   cmd: 'ping -c 4 1.1.1.1' },
    // Diagnostics
    { id: 'diag-dmesg', cat: 'Diagnostics', name: 'Kernel Messages',       cmd: 'dmesg -T | tail -n 30' },
    { id: 'diag-boot',  cat: 'Diagnostics', name: 'Boot Errors',           cmd: 'journalctl -p 3 -xb --no-pager' },
    { id: 'diag-cpu',   cat: 'Diagnostics', name: 'Top CPU Processes',     cmd: 'ps aux --sort=-%cpu | head -n 10' },
    { id: 'diag-mem',   cat: 'Diagnostics', name: 'Top RAM Processes',     cmd: 'ps aux --sort=-%mem | head -n 10' },
  ];

  var SNIPPETS_STORAGE_KEY = 'nodexia.terminal.customSnippets';
  function loadCustomSnippets() {
    try {
      var raw = window.localStorage.getItem(SNIPPETS_STORAGE_KEY);
      if (raw) return JSON.parse(raw) || [];
    } catch (e) {}
    return [];
  }
  function saveCustomSnippets(items) {
    try {
      window.localStorage.setItem(SNIPPETS_STORAGE_KEY, JSON.stringify(items));
    } catch (e) {}
  }

  var snippetsModal = byId('terminal-snippets-modal');
  var snippetsList = byId('terminal-snippets-list');
  var snippetsSearchInput = byId('terminal-snippets-search');
  var snippetAddForm = byId('terminal-snippet-add-form');

  function renderSnippets() {
    if (!snippetsList) return;
    snippetsList.innerHTML = '';
    var q = (snippetsSearchInput && snippetsSearchInput.value ? snippetsSearchInput.value.trim().toLowerCase() : '');
    var customs = loadCustomSnippets();
    var all = customs.concat(DEFAULT_SNIPPETS);

    var filtered = all.filter(function (s) {
      if (!q) return true;
      return (s.name && s.name.toLowerCase().indexOf(q) !== -1) ||
             (s.cmd && s.cmd.toLowerCase().indexOf(q) !== -1) ||
             (s.cat && s.cat.toLowerCase().indexOf(q) !== -1);
    });

    if (filtered.length === 0) {
      var empty = document.createElement('div');
      empty.className = 'terminal-snippets__empty';
      empty.style.textAlign = 'center';
      empty.style.padding = '24px 0';
      empty.style.color = 'var(--text-dim)';
      empty.style.fontSize = '0.85rem';
      empty.textContent = T('js.terminal.search_none');
      snippetsList.appendChild(empty);
      return;
    }

    filtered.forEach(function (s) {
      var item = document.createElement('div');
      item.className = 'terminal-snippet-item';

      var info = document.createElement('div');
      info.className = 'terminal-snippet-item__info';

      var header = document.createElement('div');
      header.className = 'terminal-snippet-item__header';

      var name = document.createElement('span');
      name.className = 'terminal-snippet-item__name';
      name.textContent = s.name;

      var cat = document.createElement('span');
      cat.className = 'terminal-snippet-item__category';
      cat.textContent = s.cat || 'Custom';

      header.appendChild(name);
      header.appendChild(cat);

      var cmd = document.createElement('div');
      cmd.className = 'terminal-snippet-item__cmd';
      cmd.textContent = s.cmd;

      info.appendChild(header);
      info.appendChild(cmd);

      var actions = document.createElement('div');
      actions.className = 'terminal-snippet-item__actions';

      var runBtn = document.createElement('button');
      runBtn.type = 'button';
      runBtn.className = 'terminal-snippet-btn terminal-snippet-btn--run';
      runBtn.title = T('js.terminal.run') + ' (' + s.cmd + ')';
      runBtn.setAttribute('aria-label', T('js.terminal.run'));
      runBtn.innerHTML = '<svg viewBox="0 0 24 24" width="13" height="13" fill="currentColor" stroke="none"><polygon points="6 4 20 12 6 20 6 4"/></svg>';
      runBtn.addEventListener('click', function () {
        fastPaste(s.cmd + '\r');
        closeSnippetsModal();
      });

      var insBtn = document.createElement('button');
      insBtn.type = 'button';
      insBtn.className = 'terminal-snippet-btn';
      insBtn.title = T('js.terminal.insert');
      insBtn.setAttribute('aria-label', T('js.terminal.insert'));
      insBtn.innerHTML = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 10 4 15 9 20"/><path d="M20 4v7a4 4 0 0 1-4 4H4"/></svg>';
      insBtn.addEventListener('click', function () {
        fastPaste(s.cmd);
        closeSnippetsModal();
      });

      var cpBtn = document.createElement('button');
      cpBtn.type = 'button';
      cpBtn.className = 'terminal-snippet-btn';
      cpBtn.title = T('js.copy.label');
      cpBtn.setAttribute('aria-label', T('js.copy.label'));
      cpBtn.innerHTML = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="13" height="13" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>';
      cpBtn.addEventListener('click', function () {
        doCopy(s.cmd, cpBtn);
      });

      actions.appendChild(runBtn);
      actions.appendChild(insBtn);
      actions.appendChild(cpBtn);

      if (s.isCustom) {
        var delBtn = document.createElement('button');
        delBtn.type = 'button';
        delBtn.className = 'terminal-snippet-btn terminal-snippet-btn--del';
        delBtn.title = T('common.delete');
        delBtn.setAttribute('aria-label', T('common.delete'));
        delBtn.innerHTML = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/></svg>';
        delBtn.addEventListener('click', function () {
          var updated = loadCustomSnippets().filter(function (c) { return c.id !== s.id; });
          saveCustomSnippets(updated);
          renderSnippets();
        });
        actions.appendChild(delBtn);
      }

      item.appendChild(info);
      item.appendChild(actions);
      snippetsList.appendChild(item);
    });

    if (window.lucide && window.lucide.createIcons) {
      try { window.lucide.createIcons(); } catch (e) {}
    }
  }

  function openSnippetsModal() {
    if (!snippetsModal) return;
    setShown(snippetsModal, true, 'flex');
    renderSnippets();
    if (snippetsSearchInput) {
      snippetsSearchInput.focus();
      snippetsSearchInput.select();
    }
  }
  function closeSnippetsModal() {
    if (!snippetsModal) return;
    setShown(snippetsModal, false, 'flex');
    term.focus();
  }
  function toggleSnippetsModal() {
    if (snippetsModal && !snippetsModal.hidden) closeSnippetsModal();
    else openSnippetsModal();
  }

  if (snippetsSearchInput) {
    snippetsSearchInput.addEventListener('input', function () {
      renderSnippets();
    });
    snippetsSearchInput.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') {
        e.preventDefault();
        closeSnippetsModal();
      }
    });
  }

  if (snippetAddForm) {
    snippetAddForm.addEventListener('submit', function (e) {
      e.preventDefault();
      var nInp = byId('term-snippet-name');
      var cInp = byId('term-snippet-cmd');
      if (!nInp || !cInp) return;
      var sName = nInp.value.trim();
      var sCmd = cInp.value.trim();
      if (!sName || !sCmd) return;
      var customs = loadCustomSnippets();
      customs.push({
        id: 'cust-' + Date.now(),
        cat: 'Custom',
        name: sName,
        cmd: sCmd,
        isCustom: true
      });
      saveCustomSnippets(customs);
      nInp.value = '';
      cInp.value = '';
      renderSnippets();
    });
  }

  bindClick('terminal-snippets-backdrop', closeSnippetsModal);
  bindClick('terminal-snippets-close', closeSnippetsModal);

  /* ── Persian Language & Input Helper ─────────────────── */
  var persianModal = byId('terminal-persian-modal');
  var persianInput = byId('term-persian-input');
  var persianPreview = byId('term-persian-preview');
  var persianToggle = byId('term-persian-mode-toggle');

  function updatePersianPreview() {
    if (!persianPreview || !persianInput) return;
    var txt = persianInput.value;
    if (!txt) {
      persianPreview.textContent = '';
      return;
    }
    if (window.NodexiaPersian && window.NodexiaPersian.hasPersian(txt)) {
      persianPreview.textContent = window.NodexiaPersian.shape(txt);
    } else {
      persianPreview.textContent = txt;
    }
  }

  function sendPersianInput(withEnter) {
    if (!persianInput) return;
    var val = persianInput.value;
    if (val) {
      fastPaste(val + (withEnter ? '\r' : ''));
      persianInput.value = '';
      updatePersianPreview();
      closePersianModal();
    }
  }

  function openPersianModal() {
    if (!persianModal) return;
    setShown(persianModal, true, 'flex');
    if (persianToggle) persianToggle.checked = persianMode;
    if (persianInput) {
      persianInput.focus();
    }
    updatePersianPreview();
    if (window.lucide && window.lucide.createIcons) {
      try { window.lucide.createIcons(); } catch (e) {}
    }
  }
  function closePersianModal() {
    if (!persianModal) return;
    setShown(persianModal, false, 'flex');
    term.focus();
  }
  function togglePersianModal() {
    if (persianModal && !persianModal.hidden) closePersianModal();
    else openPersianModal();
  }

  if (persianInput) {
    persianInput.addEventListener('input', updatePersianPreview);
    persianInput.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        sendPersianInput(true);
      } else if (e.key === 'Escape') {
        e.preventDefault();
        closePersianModal();
      }
    });
  }

  if (persianToggle) {
    persianToggle.checked = persianMode;
    persianToggle.addEventListener('change', function () {
      persianMode = persianToggle.checked;
      try { window.localStorage.setItem(PERSIAN_KEY, String(persianMode)); } catch (e) {}
      updatePersianPreview();
    });
  }

  bindClick('term-persian-send', function () { sendPersianInput(false); });
  bindClick('term-persian-send-enter', function () { sendPersianInput(true); });
  bindClick('terminal-persian-backdrop', closePersianModal);
  bindClick('terminal-persian-close', closePersianModal);

  setShown(snippetsModal, false, 'flex');
  setShown(persianModal, false, 'flex');

  /* ── Header tool buttons & More menu ──────────────────── */
  bindClick('term-tool-snippets', toggleSnippetsModal);
  bindClick('term-tool-keyboard', function () { toggleToolbar(); });
  bindClick('term-tool-persian', togglePersianModal);
  bindClick('term-tool-search', toggleSearch);
  bindClick('term-tool-theme', function () { toggleThemeMenu(); });
  bindClick('term-tool-fullscreen', toggleFullscreen);

  if (moreBtn) {
    moreBtn.addEventListener('click', function (e) {
      e.stopPropagation();
      toggleMoreMenu();
    });
  }

  bindClick('term-more-persian', function (e) { if (e && e.stopPropagation) e.stopPropagation(); closeMoreMenu(); openPersianModal(); });
  bindClick('term-more-search', function (e) { if (e && e.stopPropagation) e.stopPropagation(); closeMoreMenu(); openSearch(); });
  bindClick('term-more-theme', function (e) { if (e && e.stopPropagation) e.stopPropagation(); closeMoreMenu(); toggleThemeMenu(true); });
  bindClick('term-more-upload', function (e) { if (e && e.stopPropagation) e.stopPropagation(); closeMoreMenu(); triggerFileUpload(); });
  bindClick('term-more-clear', function (e) { if (e && e.stopPropagation) e.stopPropagation(); closeMoreMenu(); try { term.clear(); } catch(e) {} term.focus(); });
  bindClick('term-more-font-dec', function (e) { if (e && e.stopPropagation) e.stopPropagation(); setFontSize((term.options.fontSize || defaultFontSize()) - 1); });
  bindClick('term-more-font-inc', function (e) { if (e && e.stopPropagation) e.stopPropagation(); setFontSize((term.options.fontSize || defaultFontSize()) + 1); });
  bindClick('term-more-fullscreen', function (e) { if (e && e.stopPropagation) e.stopPropagation(); closeMoreMenu(); toggleFullscreen(); });

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
      { label: T('js.terminal.snippets'),       fn: openSnippetsModal },
      { label: T('js.terminal.persian'),        fn: openPersianModal },
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
    addGlobalListener(document, 'click', function (e) {
      if (ctxMenu && !ctxMenu.hidden && !ctxMenu.contains(e.target)) hideContextMenu();
    });
    addGlobalListener(document, 'keydown', function (e) {
      if (e.key === 'Escape') { hideContextMenu(); toggleThemeMenu(false); closeMoreMenu(); }
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
  var reconnectTimer = null;
  var resumeProbeTimer = null;
  var reconnectAttempts = 0;
  var lastPingAt = 0;
  var userClosing = false;
  var disposed = false;
  var everConnected = false;
  var MAX_RECONNECT_ATTEMPTS = 6;

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
    if (resumeProbeTimer) { clearTimeout(resumeProbeTimer); resumeProbeTimer = null; }
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

  function clearReconnectTimer() {
    if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
  }

  function probeConnection() {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    var socket = ws;
    lastPingAt = Date.now();
    try { socket.send(JSON.stringify({ type: 'heartbeat' })); } catch (e) { return; }
    if (resumeProbeTimer) clearTimeout(resumeProbeTimer);
    resumeProbeTimer = setTimeout(function () {
      resumeProbeTimer = null;
      if (socket === ws && socket.readyState === WebSocket.OPEN) {
        try { socket.close(4000, 'resume probe timeout'); } catch (e) { /* ignore */ }
      }
    }, 8000);
  }

  function scheduleReconnect(event) {
    if (userClosing || disposed) return;
    setStatus('reconnecting', T('js.terminal.reconnecting'));
    if (document.hidden) return;
    if (reconnectAttempts >= MAX_RECONNECT_ATTEMPTS) {
      showDisconnectOverlay(T('js.terminal.closed_unexpectedly', { code: event ? event.code : 1006 }));
      return;
    }
    var delays = [0, 1000, 2000, 4000, 8000, 15000];
    var delay = delays[reconnectAttempts] || 15000;
    reconnectAttempts += 1;
    clearReconnectTimer();
    reconnectTimer = setTimeout(function () {
      reconnectTimer = null;
      connect();
    }, delay);
  }

  function connect() {
    if (userClosing || disposed) return;
    if (ws && (ws.readyState === WebSocket.CONNECTING || ws.readyState === WebSocket.OPEN)) return;
    clearReconnectTimer();
    var wsScheme = window.location.protocol === 'https:' ? 'wss://' : 'ws://';
    var wsURL = wsScheme + window.location.host + wsBase + '?ticket=' + encodeURIComponent(ticket);
    var socket = new WebSocket(wsURL);
    ws = socket;
    if (everConnected) setStatus('reconnecting', T('js.terminal.reconnecting'));

    connectTimer = setTimeout(function () {
      if (socket === ws && socket.readyState === WebSocket.CONNECTING) {
        setStatus('error', T('js.terminal.status_error'));
        showError(T('js.terminal.connection_timeout'));
        try { socket.close(1000, 'connect timeout'); } catch (e) { /* ignore */ }
      }
    }, 30000);

    socket.onopen = function () {
      if (socket !== ws) return;
      if (connectTimer) { clearTimeout(connectTimer); connectTimer = null; }
      if (resumeProbeTimer) { clearTimeout(resumeProbeTimer); resumeProbeTimer = null; }
      reconnectAttempts = 0;
      everConnected = true;
      clearError();
      hideDisconnectOverlay();
      setStatus('connected', T('js.terminal.connected'));
      if (isMobile && active) setScrollLock(true);
      startHeartbeat();
      fitAndResize();
      term.focus();
      if (initCmd) setTimeout(maybeSendInitCmd, 1200);
    };

    socket.onmessage = function (event) {
      if (socket !== ws) return;
      var msg;
      try { msg = JSON.parse(event.data); } catch (e) { return; }
      switch (msg.type) {
        case 'output':
          writeTerminalOutput(msg.data);
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

    socket.onerror = function () {
      if (socket !== ws) return;
      if (connectTimer) { clearTimeout(connectTimer); connectTimer = null; }
      setStatus('reconnecting', T('js.terminal.reconnecting'));
    };

    socket.onclose = function (event) {
      if (socket !== ws) return;
      ws = null;
      if (connectTimer) { clearTimeout(connectTimer); connectTimer = null; }
      if (resumeProbeTimer) { clearTimeout(resumeProbeTimer); resumeProbeTimer = null; }
      stopHeartbeat();
      setScrollLock(false);
      if (userClosing || disposed) return;
      if (event.code === 1000 && event.reason === 'session ended') {
        setStatus('disconnected', T('js.terminal.disconnected'));
        showDisconnectOverlay(T('js.terminal.session_ended'));
        return;
      }
      scheduleReconnect(event);
    };
  }

  // The explicit Reconnect button starts a genuinely new shell. Automatic
  // transport recovery above always reuses the ticket to reattach the old one.
  function reconnect() {
    userClosing = true;
    clearReconnectTimer();
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
  addGlobalListener(window, 'pagehide', function () { setScrollLock(false); });
  addGlobalListener(document, 'visibilitychange', function () {
    if (!document.hidden && !userClosing && !disposed) {
      if (ws && ws.readyState === WebSocket.OPEN) probeConnection();
      else if (!ws || ws.readyState === WebSocket.CLOSED) {
        reconnectAttempts = 0;
        connect();
      }
    }
  });

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

  /* ── File upload & Toast feedback (Termius-style) ──────── */
  var fileInput = byId('term-file-upload-input');
  var uploadToastTimer = null;

  function showTerminalToast(msg, durationMs, isError) {
    var toast = card.querySelector('.terminal-toast');
    if (!toast) {
      toast = document.createElement('div');
      toast.className = 'terminal-toast';
      card.appendChild(toast);
    }
    toast.textContent = msg;
    toast.classList.toggle('terminal-toast--error', !!isError);
    toast.classList.add('is-active');
    if (uploadToastTimer) clearTimeout(uploadToastTimer);
    if (durationMs > 0) {
      uploadToastTimer = setTimeout(function () {
        toast.classList.remove('is-active');
      }, durationMs);
    }
  }

  function formatUploadSize(bytes) {
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
  }

  function triggerFileUpload() {
    if (!fileInput) fileInput = byId('term-file-upload-input');
    if (fileInput) {
      fileInput.value = '';
      fileInput.click();
    }
  }

  function handleFileSelected(e) {
    var files = e.target.files;
    if (!files || files.length === 0) return;
    var file = files[0];

    showTerminalToast(T('js.terminal.uploading') + ' (' + formatUploadSize(file.size) + ')', 0, false);

    var targetURL = uploadURL;
    var params = [];
    if (ticket) params.push('ticket=' + encodeURIComponent(ticket));
    if (csrfToken) params.push('_csrf_token=' + encodeURIComponent(csrfToken));
    if (params.length > 0) targetURL += '?' + params.join('&');

    var fd = new FormData();
    fd.append('file', file);
    if (ticket) fd.append('ticket', ticket);
    if (csrfToken) fd.append('_csrf_token', csrfToken);

    fetch(targetURL, {
      method: 'POST',
      body: fd,
      credentials: 'same-origin'
    })
      .then(function (resp) {
        return resp.json().then(function (data) {
          return { ok: resp.ok, status: resp.status, data: data };
        }).catch(function () {
          return { ok: resp.ok, status: resp.status, data: null };
        });
      })
      .then(function (res) {
        if (!res.ok || !res.data || !res.data.ok) {
          var errMsg = (res.data && res.data.error) ? res.data.error : ('HTTP ' + res.status);
          showTerminalToast(T('js.terminal.upload_failed', { error: errMsg }), 4500, true);
          return;
        }

        var remotePath = res.data.path;
        showTerminalToast(T('js.terminal.uploaded', { path: remotePath }), 3500, false);

        // Termius behavior: type the uploaded remote file path directly into the active shell
        sendInput(remotePath + ' ');
        term.focus();
      })
      .catch(function (err) {
        showTerminalToast(T('js.terminal.upload_failed', { error: err.message || 'Network error' }), 4500, true);
      })
      .finally(function () {
        if (fileInput) fileInput.value = '';
      });
  }

  if (fileInput) {
    fileInput.addEventListener('change', handleFileSelected);
  }

  /* ── Mobile key toolbar ───────────────────────────────── */
  var SEQ = {
    esc:    '\x1b',
    tab:    '\x09',
    up:     '\x1b[A',
    down:   '\x1b[B',
    right:  '\x1b[C',
    left:   '\x1b[D',
    home:   '\x1b[H',
    end:    '\x1b[F',
    del:    '\x1b[3~',
    pgup:   '\x1b[5~',
    pgdn:   '\x1b[6~',
    ctrl_c: '\x03',
    ctrl_d: '\x04',
    ctrl_z: '\x1a',
    ctrl_l: '\x0c',
    ctrl_a: '\x01',
    ctrl_e: '\x05',
    ctrl_r: '\x12',
    ctrl_w: '\x17',
  };

  // Keycap glyphs (Ctrl/Alt/Esc/Tab/arrows/Home/…/A−/A+) stay Latin even in RTL
  // technical UIs by convention; only aria-labels and the action words
  // (Select/Copy/Paste) are localized.
  var SEP = { kind: 'sep' };
  var TOGGLE_ROWS = { kind: 'togglerows' };

  var BUTTONS_ROW1 = [
    { label: 'Esc',    kind: 'seq', key: 'esc' },
    { label: 'Ctrl',   kind: 'ctrl' },
    { label: 'Alt',    kind: 'alt' },
    { label: 'Tab',    kind: 'seq', key: 'tab' },
    SEP,
    { label: 'Ctrl+C', kind: 'seq', key: 'ctrl_c', highlight: 'crit', aria: 'Ctrl+C' },
    { label: 'Ctrl+D', kind: 'seq', key: 'ctrl_d', highlight: 'crit', aria: 'Ctrl+D' },
    { label: 'Ctrl+Z', kind: 'seq', key: 'ctrl_z', aria: 'Ctrl+Z' },
    { label: 'Ctrl+L', kind: 'seq', key: 'ctrl_l', aria: 'Ctrl+L' },
    SEP,
    { label: '←', kind: 'seq', key: 'left',  aria: T('js.terminal.aria_left') },
    { label: '↑', kind: 'seq', key: 'up',    aria: T('js.terminal.aria_up') },
    { label: '↓', kind: 'seq', key: 'down',  aria: T('js.terminal.aria_down') },
    { label: '→', kind: 'seq', key: 'right', aria: T('js.terminal.aria_right') },
    SEP,
    { label: '📎', kind: 'upload', aria: T('js.terminal.upload') },
    { label: T('js.terminal.paste'), kind: 'paste' },
    TOGGLE_ROWS,
  ];

  var BUTTONS_ROW2 = [
    { label: 'Ctrl+R', kind: 'seq', key: 'ctrl_r', aria: 'Ctrl+R' },
    { label: 'Ctrl+A', kind: 'seq', key: 'ctrl_a', aria: 'Ctrl+A' },
    { label: 'Ctrl+E', kind: 'seq', key: 'ctrl_e', aria: 'Ctrl+E' },
    { label: 'Ctrl+W', kind: 'seq', key: 'ctrl_w', aria: 'Ctrl+W' },
    SEP,
    { label: 'Home', kind: 'seq', key: 'home' },
    { label: 'End',  kind: 'seq', key: 'end' },
    { label: 'Del',  kind: 'seq', key: 'del' },
    { label: 'PgUp', kind: 'seq', key: 'pgup' },
    { label: 'PgDn', kind: 'seq', key: 'pgdn' },
    SEP,
    { label: T('js.terminal.select'), kind: 'select', aria: T('js.terminal.aria_select') },
    { label: T('js.copy.label'),      kind: 'copy',   aria: T('js.terminal.aria_copy') },
    { label: 'A−', kind: 'font', key: 'dec', aria: T('js.terminal.aria_font_smaller') },
    { label: 'A+', kind: 'font', key: 'inc', aria: T('js.terminal.aria_font_larger') },
  ];

  var BUTTONS_SINGLE = [
    { label: 'Esc',    kind: 'seq', key: 'esc' },
    { label: 'Ctrl',   kind: 'ctrl' },
    { label: 'Alt',    kind: 'alt' },
    { label: 'Tab',    kind: 'seq', key: 'tab' },
    SEP,
    { label: 'Ctrl+C', kind: 'seq', key: 'ctrl_c', highlight: 'crit', aria: 'Ctrl+C' },
    { label: 'Ctrl+D', kind: 'seq', key: 'ctrl_d', highlight: 'crit', aria: 'Ctrl+D' },
    { label: 'Ctrl+Z', kind: 'seq', key: 'ctrl_z', aria: 'Ctrl+Z' },
    { label: 'Ctrl+L', kind: 'seq', key: 'ctrl_l', aria: 'Ctrl+L' },
    { label: 'Ctrl+R', kind: 'seq', key: 'ctrl_r', aria: 'Ctrl+R' },
    { label: 'Ctrl+A', kind: 'seq', key: 'ctrl_a', aria: 'Ctrl+A' },
    { label: 'Ctrl+E', kind: 'seq', key: 'ctrl_e', aria: 'Ctrl+E' },
    { label: 'Ctrl+W', kind: 'seq', key: 'ctrl_w', aria: 'Ctrl+W' },
    SEP,
    { label: '←', kind: 'seq', key: 'left',  aria: T('js.terminal.aria_left') },
    { label: '↑', kind: 'seq', key: 'up',    aria: T('js.terminal.aria_up') },
    { label: '↓', kind: 'seq', key: 'down',  aria: T('js.terminal.aria_down') },
    { label: '→', kind: 'seq', key: 'right', aria: T('js.terminal.aria_right') },
    SEP,
    { label: 'Home', kind: 'seq', key: 'home' },
    { label: 'End',  kind: 'seq', key: 'end' },
    { label: 'Del',  kind: 'seq', key: 'del' },
    { label: 'PgUp', kind: 'seq', key: 'pgup' },
    { label: 'PgDn', kind: 'seq', key: 'pgdn' },
    SEP,
    { label: '📎', kind: 'upload', aria: T('js.terminal.upload') },
    { label: T('js.terminal.select'),   kind: 'select',  aria: T('js.terminal.aria_select') },
    { label: T('js.copy.label'),        kind: 'copy',    aria: T('js.terminal.aria_copy') },
    { label: T('js.terminal.paste'),    kind: 'paste' },
    { label: 'A−', kind: 'font', key: 'dec', aria: T('js.terminal.aria_font_smaller') },
    { label: 'A+', kind: 'font', key: 'inc', aria: T('js.terminal.aria_font_larger') },
    SEP,
    TOGGLE_ROWS,
  ];

  // Actions that touch a selection must not pull focus back to the terminal —
  // that would clear the selection and pop the soft keyboard.
  var NO_REFOCUS = { select: true, copy: true, copyall: true, search: true, snippets: true, persian: true, togglerows: true, upload: true };

  function handleKey(def, btn) {
    switch (def.kind) {
      case 'seq':        sendInput(SEQ[def.key]); break;
      case 'lit':        sendInput(def.label); break;
      case 'ctrl':       setCtrl(!ctrlPending); break;
      case 'alt':        setAlt(!altPending); break;
      case 'upload':     triggerFileUpload(); break;
      case 'snippets':   toggleSnippetsModal(); break;
      case 'persian':    togglePersianModal(); break;
      case 'clear':      try { term.clear(); } catch (e) {} break;
      case 'select':     setSelectMode(!selecting); break;
      case 'copy':       doCopy(currentSelection(), btn); break;
      case 'copyall':    doCopy(getBufferText(false), btn); break;
      case 'paste':      doPaste(); break;
      case 'search':     openSearch(); break;
      case 'font':       setFontSize(term.options.fontSize + (def.key === 'inc' ? 1 : -1)); break;
      case 'fullscreen': toggleFullscreen(); break;
      case 'togglerows':
        toolbar2Rows = !toolbar2Rows;
        try { window.localStorage.setItem(TOOLBAR_ROWS_KEY, String(toolbar2Rows)); } catch (e) {}
        renderToolbarContent();
        requestAnimationFrame(fitAndResize);
        break;
    }
  }

  var toolbarEl = null;
  var toolbarVisible = isMobile;
  var TOOLBAR_STORAGE_KEY = 'nodexia.terminal.keyboardToolbar';
  var TOOLBAR_ROWS_KEY = 'nodexia.terminal.toolbar2Rows';
  var toolbar2Rows = false;
  try {
    var storedTB = window.localStorage.getItem(TOOLBAR_STORAGE_KEY);
    if (storedTB !== null) {
      toolbarVisible = (storedTB === 'true');
    }
    var storedRows = window.localStorage.getItem(TOOLBAR_ROWS_KEY);
    if (storedRows !== null) {
      toolbar2Rows = (storedRows === 'true');
    }
  } catch (e) {}

  function updateKeyboardBtnState() {
    var kbBtn = byId('term-tool-keyboard');
    if (kbBtn) {
      kbBtn.classList.toggle('is-active', toolbarVisible);
      kbBtn.setAttribute('aria-pressed', toolbarVisible ? 'true' : 'false');
    }
  }

  function renderToolbarContent() {
    if (!toolbarEl) return;
    toolbarEl.innerHTML = '';
    toolbarEl.classList.toggle('terminal-toolbar--two-rows', toolbar2Rows);

    var row1El = document.createElement('div');
    row1El.className = 'terminal-toolbar__row terminal-toolbar__row--primary';
    row1El.setAttribute('role', 'toolbar');

    var row2El = null;
    if (toolbar2Rows) {
      row2El = document.createElement('div');
      row2El.className = 'terminal-toolbar__row terminal-toolbar__row--secondary';
      row2El.setAttribute('role', 'toolbar');
    }

    var keysRow1 = toolbar2Rows ? BUTTONS_ROW1 : BUTTONS_SINGLE;
    var keysRow2 = toolbar2Rows ? BUTTONS_ROW2 : [];

    function populateRow(rowContainer, buttonDefs) {
      buttonDefs.forEach(function (def) {
        if (def.kind === 'sep') {
          var sep = document.createElement('span');
          sep.className = 'terminal-key-sep';
          sep.setAttribute('aria-hidden', 'true');
          rowContainer.appendChild(sep);
          return;
        }
        var btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'terminal-key';
        btn.textContent = def.label;
        btn.tabIndex = -1;
        btn.setAttribute('data-label', def.label);
        btn.setAttribute('aria-label', def.aria || def.label);

        if (def.kind === 'ctrl') {
          btn.classList.add('terminal-key--ctrl');
          btn.classList.toggle('is-active', ctrlPending);
          btn.setAttribute('aria-pressed', ctrlPending ? 'true' : 'false');
          ctrlBtn = btn;
        }
        if (def.kind === 'alt') {
          btn.classList.add('terminal-key--alt');
          btn.classList.toggle('is-active', altPending);
          btn.setAttribute('aria-pressed', altPending ? 'true' : 'false');
          altBtn = btn;
        }
        if (def.kind === 'select') {
          btn.classList.add('terminal-key--select');
          btn.classList.toggle('is-active', selecting);
          btn.setAttribute('aria-pressed', selecting ? 'true' : 'false');
          btn.textContent = selecting ? T('js.terminal.done') : T('js.terminal.select');
          selectBtn = btn;
        }
        if (def.kind === 'togglerows') {
          btn.classList.add('terminal-key--togglerows');
          btn.classList.toggle('is-active', toolbar2Rows);
          btn.setAttribute('title', toolbar2Rows ? T('js.terminal.toolbar_collapse') : T('js.terminal.toolbar_expand'));
          btn.setAttribute('aria-label', toolbar2Rows ? T('js.terminal.toolbar_collapse') : T('js.terminal.toolbar_expand'));
          btn.textContent = toolbar2Rows ? '1-Row ▴' : '2-Row ▾';
        }
        if (def.highlight === 'crit') {
          btn.classList.add('terminal-key--crit');
        }
        if (def.kind === 'upload') {
          btn.classList.add('terminal-key--upload');
        }

        btn.addEventListener('mousedown', function (e) { e.preventDefault(); });
        btn.addEventListener('click', function (e) {
          e.preventDefault();
          handleKey(def, btn);
          if (!NO_REFOCUS[def.kind]) term.focus();
        });
        rowContainer.appendChild(btn);
      });
    }

    populateRow(row1El, keysRow1);
    toolbarEl.appendChild(row1El);

    if (row2El) {
      populateRow(row2El, keysRow2);
      toolbarEl.appendChild(row2El);
    }
  }

  function buildToolbar() {
    if (toolbarEl) return toolbarEl;
    var bar = document.createElement('div');
    bar.className = 'terminal-toolbar';
    bar.setAttribute('role', 'toolbar');
    bar.setAttribute('aria-label', T('js.terminal.keys_label'));

    toolbarEl = bar;
    renderToolbarContent();

    var statusbar = byId('terminal-statusbar');
    if (statusbar && statusbar.parentNode === card) {
      card.insertBefore(bar, statusbar);
    } else {
      card.appendChild(bar);
    }
    setShown(toolbarEl, toolbarVisible, 'flex');
    updateKeyboardBtnState();
    return bar;
  }

  function toggleToolbar(force) {
    if (!toolbarEl) buildToolbar();
    toolbarVisible = (typeof force === 'boolean') ? force : !toolbarVisible;
    try { window.localStorage.setItem(TOOLBAR_STORAGE_KEY, String(toolbarVisible)); } catch (e) {}
    setShown(toolbarEl, toolbarVisible, 'flex');
    updateKeyboardBtnState();
    requestAnimationFrame(fitAndResize);
    term.focus();
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
      addGlobalListener(vv, 'resize', scheduleViewportUpdate);
      addGlobalListener(vv, 'scroll', scheduleViewportUpdate);
    }
    addGlobalListener(window, 'orientationchange', function () {
      setTimeout(scheduleViewportUpdate, 250);
    });
  }

  /* ── Resize handling ──────────────────────────────────── */
  var resizeTimer = null;
  addGlobalListener(window, 'resize', function () {
    if (isMobile) { scheduleViewportUpdate(); return; }
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(fitAndResize, 80);
  });

  buildToolbar();
  if (isMobile) enableMobile();
  updateKeyboardBtnState();

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
    pause: function () {
      active = false;
      // Release the mobile scroll/body lock when the terminal pane leaves the
      // foreground. Without this, the html.terminal-mobile-active class set
      // by enableMobile() persists across tab switches, leaving a newly
      // created or newly activated tab with overflow:hidden and the bottom
      // nav hidden. Only the active terminal pane should own the lock.
      if (isMobile) setScrollLock(false);
    },
    resume: function () {
      active = true;
      if (isMobile) setScrollLock(true);
      if (!userClosing && !disposed) {
        if (ws && ws.readyState === WebSocket.OPEN) probeConnection();
        else if (!ws || ws.readyState === WebSocket.CLOSED) {
          reconnectAttempts = 0;
          connect();
        }
      }
      fitAndResize();
      term.focus();
    },
    dispose: function () {
      if (disposed) return;
      userClosing = true;
      disposed = true;
      clearReconnectTimer();
      if (resumeProbeTimer) { clearTimeout(resumeProbeTimer); resumeProbeTimer = null; }
      if (connectTimer) { clearTimeout(connectTimer); connectTimer = null; }
      if (outputRaf) { cancelAnimationFrame(outputRaf); outputRaf = null; }
      if (uploadToastTimer) { clearTimeout(uploadToastTimer); uploadToastTimer = null; }
      outputBuffer = '';
      stopHeartbeat();
      clearTimeout(resizeTimer);
      removeGlobalListeners();
      try { if (ws) ws.close(1000, 'tab closed'); } catch (e) {}
      try { term.dispose(); } catch (e) {}
      if (isMobile) setScrollLock(false);
    },
    isConnected: function () { return !!(ws && ws.readyState === WebSocket.OPEN); }
  };
  card.dispatchEvent(new CustomEvent('nodexia:terminal-ready', { bubbles: true }));
})();
