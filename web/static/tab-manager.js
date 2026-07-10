// Nodexia v0.6.3: core multi-tab workspace manager (window.NodexiaTabs).
/* Client-side tab system: a global tab bar plus one .tab-pane per open tab,
 * built entirely from the same full-page HTML a direct URL visit would
 * receive (fetch() + DOMParser extraction of #tab-content, per docs/tab-system.md).
 * No backend render path changes. Progressive enhancement: #nodexia-tabbar
 * ships hidden server-side and only loses that attribute once this file has
 * finished wiring itself up successfully — any failure along the way (missing
 * DOM, no fetch/DOMParser, a thrown error) leaves the bar hidden and the page
 * renders exactly like a pre-v0.6.0 direct load. */
(function () {
  'use strict';

  /* ── Constants ───────────────────────────────────────────── */
  var STORAGE_KEY = 'nodexia.tabs.v1';
  var CLOSED_KEY = 'nodexia.tabs.closed.v1';
  var CLOSED_RING_CAP = 10;
  var MOBILE_BREAKPOINT = 768;
  var PERSIST_DEBOUNCE_MS = 150;
  var LONG_PRESS_MS = 500;
  var LONG_PRESS_TOLERANCE_PX = 16;
  var SWIPE_MIN_PX = 60;
  var TOAST_MS = 3200;

  /* ── i18n / typing-guard bridges ─────────────────────────────
   * T mirrors terminal.js's own tiny wrapper around window.nxT. isTyping is a
   * verbatim copy of app.js's helper of the same name — app.js keeps it as a
   * private, unexported function, so it can't be imported; this mirrors its
   * exact behaviour rather than diverging from it. */
  function T(key, params) { return window.nxT ? window.nxT(key, params) : key; }
  function isTyping(el) {
    return el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' ||
      el.tagName === 'SELECT' || el.isContentEditable);
  }

  function finishTopBar() {
    if (window.NodexiaApp && typeof window.NodexiaApp.finishNavigation === 'function') {
      try { window.NodexiaApp.finishNavigation(); } catch (err) { /* never break tabs over progress bar */ }
    }
  }

  function restoreFormUI(submitter) {
    try {
      var overlay = document.getElementById('loading-overlay');
      if (overlay) overlay.style.display = 'none';
    } catch (err) { /* overlay hide must never fail */ }
    if (submitter && submitter.dataset && submitter.dataset.busy === '1') {
      submitter.dataset.busy = '';
      submitter.classList.remove('is-busy');
      var label = submitter.getAttribute('data-busy-label');
      if (label) submitter.textContent = label;
      submitter.style.minWidth = '';
    }
    finishTopBar();
  }

  function matchNavHref(path, href) {
    if (!href || href.charAt(0) !== '/') return false;
    return path === href || path.indexOf(href + '/') === 0;
  }

  function updateNavHighlight(path) {
    var containers = [
      document.querySelector('.shell-nav'),
      document.querySelector('.bottom-nav'),
      document.getElementById('mobile-drawer')
    ];
    var links = [];
    containers.forEach(function (container) {
      if (!container) return;
      container.querySelectorAll('a[href]').forEach(function (a) {
        var href = a.getAttribute('href');
        a.classList.remove('is-active');
        a.removeAttribute('aria-current');
        if (matchNavHref(path, href)) links.push({ el: a, href: href });
      });
    });
    if (!links.length) return;
    var best = links.reduce(function (a, b) { return a.href.length >= b.href.length ? a : b; });
    containers.forEach(function (container) {
      if (!container) return;
      container.querySelectorAll('a[href]').forEach(function (a) {
        if (a.getAttribute('href') === best.href) {
          a.classList.add('is-active');
          a.setAttribute('aria-current', 'page');
        }
      });
    });
  }

  function renderIcons() {
    if (typeof lucide === 'undefined' || !lucide.createIcons) return;
    try { lucide.createIcons(); } catch (err) { /* never break the page over an icon */ }
  }

  /* ── URL helpers ─────────────────────────────────────────── */
  function toURL(url) {
    try { return new URL(url, window.location.href); } catch (err) { return null; }
  }
  function pathnameOf(url) {
    var u = toURL(url);
    return u ? u.pathname : String(url || '');
  }
  function pathAndSearch(url) {
    var u = toURL(url);
    return u ? (u.pathname + u.search) : String(url || '');
  }

  /* ── Route → icon table (§3) ─────────────────────────────── */
  var ICON_ROUTES = [
    { re: /^\/$/, icon: 'layout-dashboard' },
    { re: /^\/servers\/\d+\/terminal/, icon: 'terminal-square' },
    { re: /^\/servers\/\d+\/monitoring/, icon: 'activity' },
    { re: /^\/servers\/\d+\/commands/, icon: 'square-chevron-right' },
    { re: /^\/servers\/\d+\/files/, icon: 'folder-open' },
    { re: /^\/servers\/\d+\/nodes/, icon: 'share-2' },
    { re: /^\/servers\/\d+\/system/, icon: 'cpu' },
    { re: /^\/servers\/\d+\/analytics/, icon: 'bar-chart-2' },
    { re: /^\/servers\/bulk/, icon: 'layers' },
    { re: /^\/servers/, icon: 'server' },
    { re: /^\/analytics/, icon: 'bar-chart-2' },
    { re: /^\/alerts/, icon: 'bell-ring' },
    { re: /^\/ops\/diagnostics/, icon: 'stethoscope' }
  ];
  var TERMINAL_RE = /^\/servers\/\d+\/terminal$/;
  function iconForURL(url) {
    var path = pathnameOf(url);
    for (var i = 0; i < ICON_ROUTES.length; i++) {
      if (ICON_ROUTES[i].re.test(path)) return ICON_ROUTES[i].icon;
    }
    return 'file';
  }
  function kindForURL(url) {
    return TERMINAL_RE.test(pathnameOf(url)) ? 'terminal' : 'generic';
  }

  var STATUS_LABEL_KEY = {
    connected: 'js.tabs.status_connected',
    connecting: 'js.tabs.status_connecting',
    reconnecting: 'js.tabs.status_connecting',
    disconnected: 'js.tabs.status_disconnected',
    error: 'js.tabs.status_disconnected'
  };

  var idSeq = 0;
  function genId() {
    idSeq += 1;
    return 'tab-' + Date.now().toString(36) + idSeq.toString(36) + Math.random().toString(36).slice(2, 6);
  }

  /* ── State ───────────────────────────────────────────────── */
  var tabs = [];          // ordered-ish array of internal tab records
  var tabsById = {};
  var activeTabId = null;
  var closedRing = [];    // most-recent last, capped at CLOSED_RING_CAP
  var ready = false;

  // DOM refs, resolved once in init().
  var tabBarEl, pinnedEl, listEl, newBtn;
  var contentEl;
  var switcherEl, switcherBackdrop, switcherGrid, switcherCount,
    switcherNewBtn, switcherCloseAllBtn, switcherDismissBtn;
  var fabEl, fabBadge;

  function byOrder(a, b) { return a.order - b.order; }
  function orderedAll() {
    var pinned = [], rest = [];
    tabs.forEach(function (t) { (t.pinned ? pinned : rest).push(t); });
    pinned.sort(byOrder);
    rest.sort(byOrder);
    return pinned.concat(rest);
  }
  function nextOrder() {
    var max = -1;
    tabs.forEach(function (t) { if (t.order > max) max = t.order; });
    return max + 1;
  }
  function registerTab(tab) {
    tabs.push(tab);
    tabsById[tab.id] = tab;
  }
  function toDescriptor(tab) {
    return { id: tab.id, url: tab.url, title: tab.title, icon: tab.icon, pinned: tab.pinned, order: tab.order, kind: tab.kind };
  }

  /* ── sessionStorage persistence (§6) ─────────────────────── */
  var persistTimer = null;
  function schedulePersist() {
    clearTimeout(persistTimer);
    persistTimer = setTimeout(persistNow, PERSIST_DEBOUNCE_MS);
  }
  function persistNow() {
    try {
      var data = {
        version: 1,
        activeTabId: activeTabId,
        tabs: tabs.map(function (t) {
          return {
            id: t.id, url: t.url, title: t.title, icon: t.icon, kind: t.kind,
            pinned: t.pinned, order: t.order, createdAt: t.createdAt
          };
        })
      };
      sessionStorage.setItem(STORAGE_KEY, JSON.stringify(data));
    } catch (err) { /* storage unavailable/full — metadata persistence is best-effort */ }
  }
  function loadStoredTabs() {
    try {
      var raw = sessionStorage.getItem(STORAGE_KEY);
      if (!raw) return null;
      var data = JSON.parse(raw);
      if (!data || data.version !== 1 || !Array.isArray(data.tabs)) return null;
      return data;
    } catch (err) { return null; }
  }
  function loadClosedRing() {
    try {
      var raw = sessionStorage.getItem(CLOSED_KEY);
      if (!raw) return [];
      var data = JSON.parse(raw);
      if (!data || data.version !== 1 || !Array.isArray(data.entries)) return [];
      return data.entries;
    } catch (err) { return []; }
  }
  function persistClosedRing() {
    try { sessionStorage.setItem(CLOSED_KEY, JSON.stringify({ version: 1, entries: closedRing })); }
    catch (err) { /* best-effort */ }
  }
  function pushClosedRing(tab) {
    closedRing.push({ url: tab.url, title: tab.title, icon: tab.icon, kind: tab.kind });
    if (closedRing.length > CLOSED_RING_CAP) closedRing.splice(0, closedRing.length - CLOSED_RING_CAP);
    persistClosedRing();
  }

  /* ── Event dispatch (§4) ─────────────────────────────────── */
  function fire(name, detail, cancelable) {
    return document.dispatchEvent(new CustomEvent(name, { detail: detail, bubbles: true, composed: false, cancelable: !!cancelable }));
  }
  function paneDetail(tab) { return { tabId: tab.id, url: tab.url, kind: tab.kind, pane: tab.pane }; }

  /* ── Tab bar rendering ───────────────────────────────────── */
  function relayoutTabs() {
    orderedAll().forEach(function (t) {
      if (!t.btn) return;
      var container = t.pinned ? pinnedEl : listEl;
      if (container) container.appendChild(t.btn);
    });
  }

  function setTabButtonActive(tab, isActive) {
    if (!tab.btn) return;
    tab.btn.classList.toggle('is-active', isActive);
    tab.btn.setAttribute('aria-selected', isActive ? 'true' : 'false');
  }

  function ensureStatusDot(tab) {
    if (!tab.btn) return;
    var dot = tab.btn.querySelector('.tab__status-dot');
    if (tab.kind === 'terminal') {
      if (!dot) {
        dot = document.createElement('span');
        dot.className = 'tab__status-dot tab__status-dot--' + (tab.statusState || 'connecting');
        var titleEl = tab.btn.querySelector('.tab__title');
        tab.btn.insertBefore(dot, titleEl || null);
      }
    } else if (dot) {
      dot.parentNode.removeChild(dot);
    }
  }

  function renderTabButton(tab) {
    if (!listEl || !pinnedEl) return;
    if (!tab.btn) {
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'tab';
      btn.setAttribute('role', 'tab');
      btn.setAttribute('aria-selected', 'false');
      btn.setAttribute('draggable', 'true');
      btn.dataset.tabId = tab.id;

      var icon = document.createElement('i');
      icon.className = 'tab__icon';
      icon.setAttribute('data-lucide', tab.icon);
      btn.appendChild(icon);

      var titleSpan = document.createElement('span');
      titleSpan.className = 'tab__title';
      btn.appendChild(titleSpan);

      var closeBtn = document.createElement('button');
      closeBtn.type = 'button';
      closeBtn.className = 'tab__close';
      closeBtn.setAttribute('data-tab-close', '');
      closeBtn.setAttribute('aria-label', T('js.tabs.close_tab'));
      closeBtn.innerHTML = '<i data-lucide="x"></i>';
      closeBtn.addEventListener('click', function (e) { e.stopPropagation(); close(tab.id); });
      btn.appendChild(closeBtn);

      btn.addEventListener('click', function (e) {
        if (e.target.closest && e.target.closest('.tab__close')) return;
        activate(tab.id);
      });
      btn.addEventListener('auxclick', function (e) {
        if (e.button !== 1) return;
        e.preventDefault();
        close(tab.id);
      });
      btn.addEventListener('contextmenu', function (e) {
        e.preventDefault();
        showContextMenu(tab, e.clientX, e.clientY);
      });
      attachDragHandlers(btn, tab);
      attachLongPress(btn, function (x, y, startTarget) {
        if (startTarget && startTarget.closest && startTarget.closest('.tab__close')) return;
        showContextMenu(tab, x, y);
      });

      tab.btn = btn;
    }
    var iconEl = tab.btn.querySelector('.tab__icon');
    if (iconEl) iconEl.setAttribute('data-lucide', tab.icon);
    var titleEl = tab.btn.querySelector('.tab__title');
    if (titleEl) titleEl.textContent = tab.title;
    tab.btn.classList.toggle('is-pinned', tab.pinned);
    if (tab.pinned) tab.btn.setAttribute('title', tab.title);
    else tab.btn.removeAttribute('title');
    setTabButtonActive(tab, tab.id === activeTabId);
    ensureStatusDot(tab);
    renderIcons();
  }

  function removeTabButton(tab) {
    if (tab.btn && tab.btn.parentNode) tab.btn.parentNode.removeChild(tab.btn);
  }

  function paintStatusDot(tab, state) {
    if (!tab.btn) return;
    var dot = tab.btn.querySelector('.tab__status-dot');
    if (!dot) return;
    dot.className = 'tab__status-dot tab__status-dot--' + state;
    var key = STATUS_LABEL_KEY[state];
    if (key) dot.setAttribute('title', T(key));
  }

  function updateFabBadge() {
    if (fabBadge) fabBadge.textContent = String(tabs.length);
    if (switcherCount) switcherCount.textContent = String(tabs.length);
  }

  /* ── Drag-to-reorder (native HTML5 DnD; §6 persists "order") ─ */
  var dragTabId = null;
  function attachDragHandlers(btn, tab) {
    btn.addEventListener('dragstart', function (e) {
      dragTabId = tab.id;
      if (e.dataTransfer) {
        e.dataTransfer.effectAllowed = 'move';
        try { e.dataTransfer.setData('text/plain', tab.id); } catch (err) { /* Firefox requires a set; ignore elsewhere */ }
      }
      btn.classList.add('is-dragging');
    });
    btn.addEventListener('dragend', function () {
      dragTabId = null;
      btn.classList.remove('is-dragging');
      document.querySelectorAll('.tab.is-drop-target').forEach(function (el) { el.classList.remove('is-drop-target'); });
    });
    btn.addEventListener('dragover', function (e) {
      var dragged = dragTabId && tabsById[dragTabId];
      if (!dragged || dragged.id === tab.id || dragged.pinned !== tab.pinned) return;
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
      btn.classList.add('is-drop-target');
    });
    btn.addEventListener('dragleave', function () {
      btn.classList.remove('is-drop-target');
    });
    btn.addEventListener('drop', function (e) {
      btn.classList.remove('is-drop-target');
      var dragged = dragTabId && tabsById[dragTabId];
      if (!dragged || dragged.id === tab.id || dragged.pinned !== tab.pinned) return;
      e.preventDefault();
      reorderTab(dragged, tab, e.clientX);
    });
  }
  function reorderTab(dragged, target, clientX) {
    var rect = target.btn.getBoundingClientRect();
    var before = clientX < rect.left + rect.width / 2;
    var group = orderedAll().filter(function (t) { return t.pinned === target.pinned && t.id !== dragged.id; });
    var idx = group.indexOf(target);
    if (idx === -1) return;
    if (!before) idx += 1;
    group.splice(idx, 0, dragged);
    group.forEach(function (t, i) { t.order = i; });
    relayoutTabs();
    schedulePersist();
  }

  /* ── Pane content: fetch + DOMParser extraction (§1.2, §1.2.1) ─ */
  function showPaneLoading(pane) {
    pane.innerHTML = '';
    var wrap = document.createElement('div');
    wrap.className = 'tab-pane__loading';
    wrap.textContent = T('js.tabs.loading');
    pane.appendChild(wrap);
  }
  function showPaneError(pane, tab, url, status) {
    pane.innerHTML = '';
    var wrap = document.createElement('div');
    wrap.className = 'tab-pane__error';
    var msg = document.createElement('p');
    var key = 'js.tabs.load_failed';
    if (status >= 500) key = 'js.tabs.load_failed_server';
    else if (status === 404) key = 'js.tabs.load_failed_not_found';
    else if (status === 403) key = 'js.tabs.load_failed_forbidden';
    else if (status > 0) key = 'js.tabs.load_failed_status';
    msg.textContent = key === 'js.tabs.load_failed_status'
      ? T(key, { status: status })
      : T(key);
    wrap.appendChild(msg);
    var retry = document.createElement('button');
    retry.type = 'button';
    retry.className = 'btn btn--ghost btn--sm';
    retry.textContent = T('js.tabs.retry');
    retry.addEventListener('click', function () { loadPane(tab, url, {}); });
    wrap.appendChild(retry);
    pane.appendChild(wrap);
  }

  // hasTabContent checks whether a fetched HTML document contains the
  // #tab-content wrapper the SSR layout always emits — even for error pages
  // (404, 422, 502, 500).  If it does, the response is renderable in a pane
  // regardless of its HTTP status code.
  function hasTabContent(htmlText) {
    try {
      var doc = new DOMParser().parseFromString(htmlText, 'text/html');
      return !!doc.getElementById('tab-content');
    } catch (err) { return false; }
  }

  // Extracts #tab-content's children plus PER_PANE_SCRIPTS (§1.2.1) from a
  // fetched HTML document's text into `pane`. Returns the fetched <title>.
  function extractAndInject(pane, htmlText) {
    var doc = new DOMParser().parseFromString(htmlText, 'text/html');

    var source = doc.getElementById('tab-content');
    var nodes = source ? Array.prototype.slice.call(source.children) : [];
    pane.innerHTML = '';
    nodes.forEach(function (node) { pane.appendChild(document.importNode(node, true)); });

    // Hoist any stylesheet the real document doesn't have yet, deduped by
    // the exact href string (cache-busting hash makes an identical asset
    // always produce an identical href). Never removed once added.
    var existingHrefs = {};
    Array.prototype.forEach.call(document.head.querySelectorAll('link[rel="stylesheet"]'), function (l) {
      existingHrefs[l.getAttribute('href')] = true;
    });
    Array.prototype.forEach.call(doc.querySelectorAll('head link[rel="stylesheet"]'), function (l) {
      var href = l.getAttribute('href');
      if (!href || existingHrefs[href]) return;
      existingHrefs[href] = true;
      var clone = document.createElement('link');
      clone.rel = 'stylesheet';
      clone.href = href;
      document.head.appendChild(clone);
    });

    // Clone every <script src> that follows /static/app.js in the fetched
    // document (that page's own PageScripts chain), appended INSIDE this
    // pane so document.currentScript.closest('.tab-pane') resolves for
    // pane-aware scripts (terminal.js, terminal-tab-adapter.js, etc.).
    var scripts = Array.prototype.slice.call(doc.querySelectorAll('script[src]'));
    var appIdx = -1;
    for (var i = 0; i < scripts.length; i++) {
      if (/\/static\/app\.js(\?|$)/.test(scripts[i].getAttribute('src') || '')) { appIdx = i; break; }
    }
    var after = appIdx === -1 ? scripts : scripts.slice(appIdx + 1);
    after.forEach(function (s) {
      var src = s.getAttribute('src') || '';
      if (/\/static\/(app|tab-manager)\.js(\?|$)/.test(src)) return;
      var clone = document.createElement('script');
      clone.src = src;
      clone.async = false;
      pane.appendChild(clone);
    });

    return (doc.title || '').trim();
  }

  function loadPane(tab, url, opts) {
    opts = opts || {};
    var pane = tab.pane;
    if (!pane) return Promise.resolve();
    if (tab.abortController) {
      try { tab.abortController.abort(); } catch (err) { /* ignore */ }
    }
    tab.abortController = new AbortController();
    tab.loading = true;
    showPaneLoading(pane);
    var fetchOpts = { method: opts.method || 'GET', signal: tab.abortController.signal };
    if (opts.body) fetchOpts.body = opts.body;
    return fetch(url, fetchOpts).then(function (resp) {
      return resp.text().then(function (text) { return { resp: resp, text: text }; });
    }).then(function (result) {
      var finalURL = result.resp.url || url;
      // The session may have expired mid-tab-system-use; a fetch redirected
      // to /login means the whole app session is gone, not just this pane.
      if (/^\/login(\/|$)/.test(pathnameOf(finalURL))) {
        window.location.href = finalURL;
        return;
      }
      // Non-2xx responses that still carry the SSR layout (#tab-content) are
      // renderable error pages (502 SSH failure, 422 validation, 404 not
      // found, etc.) — inject them rather than masking the real error behind
      // a generic "Failed to load" message.
      if (!result.resp.ok && !hasTabContent(result.text)) {
        throw new Error('http ' + result.resp.status);
      }
      var title = extractAndInject(pane, result.text);
      tab.loading = false;
      tab.loaded = true;
      tab.url = pathAndSearch(finalURL);
      if (!opts.keepTitle) tab.title = title || T('js.tabs.untitled');
      if (!opts.keepIcon) tab.icon = iconForURL(tab.url);
      tab.kind = kindForURL(tab.url);
      renderTabButton(tab);
      if (window.NodexiaApp && typeof window.NodexiaApp.rescan === 'function') {
        try { window.NodexiaApp.rescan(pane); } catch (err) { /* a module's own rescan must not break tabs */ }
      }
      renderIcons();
      schedulePersist();
    }).catch(function (err) {
      if (err && err.name === 'AbortError') return;
      tab.loading = false;
      var status = 0;
      var m = /^http (\d+)$/.exec(err && err.message || '');
      if (m) status = parseInt(m[1], 10);
      showPaneError(pane, tab, url, status);
    });
  }

  // runPaneCleanups executes any cleanup callbacks registered on the pane
  // element by app.js init functions (EventSource.close, clearInterval, etc.).
  // Each callback is wrapped in try/catch so one failing cleanup never blocks
  // the teardown.
  function runPaneCleanups(pane) {
    if (!pane || !pane.__nxCleanups) return;
    var cleanups = pane.__nxCleanups;
    pane.__nxCleanups = [];
    cleanups.forEach(function (fn) { try { fn(); } catch (err) { /* best-effort */ } });
  }

  // Signals a pane's content is about to be torn down/replaced in place
  // (reload, or an in-tab form/link navigation) — reuses the same
  // `tab-closing` event a real close fires, so terminal-tab-adapter.js's one
  // dispose() hook also runs here; a cancelled event aborts the replace.
  function teardownPaneContent(tab) {
    runPaneCleanups(tab.pane);
    return fire('tab-closing', paneDetail(tab), true);
  }

  function navigateInPane(tab, url, fetchOpts, pushHistory) {
    var normUrl = pathAndSearch(url);
    var method = (fetchOpts && fetchOpts.method) || 'GET';
    if (normUrl === tab.url && tab.loaded && method === 'GET') {
      if (pushHistory) {
        try { window.history.pushState({ nodexiaTabId: tab.id }, '', tab.url); } catch (err) { /* ignore */ }
      }
      updateNavHighlight(tab.url.split('?')[0]);
      finishTopBar();
      return Promise.resolve();
    }
    if (!teardownPaneContent(tab)) { restoreFormUI(fetchOpts && fetchOpts.__submitter); return Promise.resolve(); }
    return loadPane(tab, url, fetchOpts || {}).then(function () {
      if (pushHistory) {
        try { window.history.pushState({ nodexiaTabId: tab.id }, '', tab.url); } catch (err) { /* ignore */ }
      }
      if (tab.pane) tab.pane.scrollTop = 0;
      updateNavHighlight(tab.url.split('?')[0]);
      finishTopBar();
    }).catch(function () { restoreFormUI(fetchOpts && fetchOpts.__submitter); });
  }

  /* ── CRUD (§5) ───────────────────────────────────────────── */
  function open(url, opts) {
    if (!ready) return null;
    opts = opts || {};
    if (window.innerWidth < MOBILE_BREAKPOINT && tabs.length >= window.NodexiaTabs.MOBILE_TAB_LIMIT) {
      var victim = null;
      tabs.forEach(function (t) {
        if (t.pinned || t.id === activeTabId) return;
        if (!victim || t.createdAt < victim.createdAt) victim = t;
      });
      if (victim) {
        close(victim.id);
      } else {
        showToast(T('js.tabs.mobile_limit', { count: window.NodexiaTabs.MOBILE_TAB_LIMIT }));
        return null;
      }
    }

    var normUrl = pathAndSearch(url);
    var tab = {
      id: genId(),
      url: normUrl,
      title: opts.title || T('js.tabs.loading'),
      icon: opts.icon || iconForURL(normUrl),
      kind: kindForURL(normUrl),
      pinned: !!opts.pinned,
      order: nextOrder(),
      createdAt: Date.now(),
      pane: null,
      loading: false,
      loaded: false
    };
    registerTab(tab);

    var pane = document.createElement('div');
    pane.className = 'tab-pane';
    pane.setAttribute('hidden', '');
    pane.dataset.tabId = tab.id;
    tab.pane = pane;
    contentEl.appendChild(pane);

    renderTabButton(tab);
    relayoutTabs();
    updateFabBadge();

    fire('tab-created', { tabId: tab.id, url: tab.url, kind: tab.kind, pinned: tab.pinned, background: !!opts.background }, false);

    loadPane(tab, tab.url, { keepTitle: !!opts.title, keepIcon: !!opts.icon })
      .then(function () {
        if (!opts.background && tab.pane) tab.pane.scrollTop = 0;
      })
      .finally(function () {
        finishTopBar();
      });

    if (!opts.background) {
      activate(tab.id);
    } else {
      schedulePersist();
    }
    return tab.id;
  }

  function close(id) {
    if (!ready) return;
    var tab = tabsById[id];
    if (!tab) return;
    if (!teardownPaneContent(tab)) return; // vetoed

    removeTabButton(tab);
    if (tab.pane && tab.pane.parentNode) tab.pane.parentNode.removeChild(tab.pane);
    tabs = tabs.filter(function (t) { return t.id !== id; });
    delete tabsById[id];
    pushClosedRing(tab);
    fire('tab-closed', { tabId: tab.id, url: tab.url, kind: tab.kind }, false);

    if (activeTabId === id) {
      activeTabId = null;
      var neighbors = orderedAll();
      if (neighbors.length) activate(neighbors[0].id);
    }
    updateFabBadge();
    if (tabs.length === 0) {
      open('/');
    } else {
      schedulePersist();
    }
  }

  function closeOthers(id) {
    if (!ready || !tabsById[id]) return;
    tabs.slice().forEach(function (t) { if (t.id !== id) close(t.id); });
  }

  function closeAll() {
    if (!ready) return;
    tabs.slice().forEach(function (t) { close(t.id); });
    if (tabs.length === 0) open('/');
  }

  function activate(id) {
    if (!ready) return;
    var tab = tabsById[id];
    if (!tab || id === activeTabId) return;
    var prev = tabsById[activeTabId];
    if (prev) {
      fire('tab-deactivated', paneDetail(prev), false);
      if (prev.pane) { prev.pane.classList.remove('is-active'); prev.pane.setAttribute('hidden', ''); }
      setTabButtonActive(prev, false);
    }
    activeTabId = id;
    if (tab.pane) { tab.pane.removeAttribute('hidden'); tab.pane.classList.add('is-active'); }
    setTabButtonActive(tab, true);
    if (tab.btn && tab.btn.scrollIntoView) {
      try { tab.btn.scrollIntoView({ block: 'nearest', inline: 'nearest' }); } catch (err) { /* ignore */ }
    }
    updateNavHighlight(tab.url.split('?')[0]);
    fire('tab-activated', paneDetail(tab), false);
    schedulePersist();
  }

  function setPinned(id, val) {
    if (!ready) return;
    var tab = tabsById[id];
    if (!tab || tab.pinned === val) return;
    tab.pinned = val;
    tab.order = nextOrder();
    renderTabButton(tab);
    relayoutTabs();
    schedulePersist();
  }
  function pin(id) { setPinned(id, true); }
  function unpin(id) { setPinned(id, false); }

  function duplicate(id) {
    if (!ready) return null;
    var tab = tabsById[id];
    if (!tab) return null;
    return open(tab.url, { background: false });
  }

  function reload(id) {
    if (!ready) return;
    var tab = tabsById[id];
    if (!tab || !tab.pane) return;
    if (!teardownPaneContent(tab)) return;
    loadPane(tab, tab.url, {});
  }

  function reopenClosed() {
    if (!ready) return null;
    if (!closedRing.length) return null;
    var entry = closedRing.pop();
    persistClosedRing();
    return open(entry.url);
  }

  function getActive() {
    if (!ready) return null;
    var tab = tabsById[activeTabId];
    return tab ? toDescriptor(tab) : null;
  }
  function getAll() {
    if (!ready) return [];
    return orderedAll().map(toDescriptor);
  }

  /* ── Toast (mobile tab cap notice, §9) ───────────────────── */
  var toastEl = null, toastTimer = null;
  function showToast(message) {
    if (!toastEl) {
      toastEl = document.createElement('div');
      toastEl.className = 'tab-toast';
      toastEl.setAttribute('role', 'status');
      document.body.appendChild(toastEl);
    }
    toastEl.textContent = message;
    toastEl.classList.add('is-visible');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { if (toastEl) toastEl.classList.remove('is-visible'); }, TOAST_MS);
  }

  /* ── Floating UI: tab context menu + mobile link action sheet ─
   * Neither element's markup is part of the frozen DOM contract (§3 doesn't
   * enumerate one), so this builds a minimal, self-contained menu rather than
   * assuming CSS support that may not exist for it. */
  var floatingUI = null;
  var floatingUIReturnFocus = null;
  var floatingUIKeyHandler = null;
  function closeFloatingUI() {
    if (floatingUIKeyHandler) {
      document.removeEventListener('keydown', floatingUIKeyHandler, true);
      floatingUIKeyHandler = null;
    }
    if (floatingUI && floatingUI.parentNode) floatingUI.parentNode.removeChild(floatingUI);
    floatingUI = null;
    document.removeEventListener('click', onDocClickCloseFloat, true);
    document.removeEventListener('keydown', onEscCloseFloat);
    if (floatingUIReturnFocus && floatingUIReturnFocus.focus) {
      try { floatingUIReturnFocus.focus(); } catch (err) {}
    }
    floatingUIReturnFocus = null;
  }
  function onDocClickCloseFloat(e) {
    if (floatingUI && !floatingUI.contains(e.target)) closeFloatingUI();
  }
  function onEscCloseFloat(e) {
    if (e.key === 'Escape') { e.stopPropagation(); closeFloatingUI(); }
  }
  function focusMenuItem(items, idx) {
    if (idx < 0) idx = items.length - 1;
    if (idx >= items.length) idx = 0;
    if (items[idx] && items[idx].focus) items[idx].focus();
    return idx;
  }
  function attachMenuKeyboard(menu) {
    floatingUIKeyHandler = function (e) {
      var items = Array.prototype.slice.call(menu.querySelectorAll('.tab-floating-menu__item'));
      var current = document.activeElement;
      var idx = items.indexOf(current);
      if (e.key === 'ArrowDown' || e.key === 'ArrowRight') {
        e.preventDefault();
        focusMenuItem(items, idx + 1);
        return;
      }
      if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') {
        e.preventDefault();
        focusMenuItem(items, idx - 1);
        return;
      }
      if (e.key === 'Home') { e.preventDefault(); focusMenuItem(items, 0); return; }
      if (e.key === 'End') { e.preventDefault(); focusMenuItem(items, items.length - 1); return; }
      if ((e.key === 'Enter' || e.key === ' ') && idx >= 0) {
        e.preventDefault();
        items[idx].click();
      }
    };
    document.addEventListener('keydown', floatingUIKeyHandler, true);
  }
  function openFloatingUI(el, x, y, returnFocus) {
    closeFloatingUI();
    floatingUIReturnFocus = returnFocus || document.activeElement;
    el.className += ' tab-floating-menu';
    document.body.appendChild(el);
    var vw = window.innerWidth, vh = window.innerHeight;
    var rect = el.getBoundingClientRect();
    var left = Math.max(8, Math.min(x, vw - rect.width - 8));
    var top = Math.max(8, Math.min(y, vh - rect.height - 8));
    el.style.left = left + 'px';
    el.style.top = top + 'px';
    floatingUI = el;
    attachMenuKeyboard(el);
    setTimeout(function () {
      var items = el.querySelectorAll('.tab-floating-menu__item');
      if (items.length) focusMenuItem(items, 0);
      document.addEventListener('click', onDocClickCloseFloat, true);
      document.addEventListener('keydown', onEscCloseFloat);
    }, 0);
  }
  function floatingMenuItem(label, onClick) {
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'tab-floating-menu__item';
    btn.setAttribute('role', 'menuitem');
    btn.textContent = label;
    btn.addEventListener('click', function () { closeFloatingUI(); onClick(); });
    return btn;
  }
  function showContextMenu(tab, x, y) {
    var menu = document.createElement('div');
    menu.className = 'tab-context-menu';
    menu.setAttribute('role', 'menu');
    menu.appendChild(floatingMenuItem(T('js.tabs.reload'), function () { reload(tab.id); }));
    menu.appendChild(floatingMenuItem(T('js.tabs.duplicate'), function () { duplicate(tab.id); }));
    menu.appendChild(floatingMenuItem(tab.pinned ? T('js.tabs.unpin') : T('js.tabs.pin'), function () {
      tab.pinned ? unpin(tab.id) : pin(tab.id);
    }));
    menu.appendChild(floatingMenuItem(T('js.tabs.close_tab'), function () { close(tab.id); }));
    menu.appendChild(floatingMenuItem(T('js.tabs.close_others'), function () { closeOthers(tab.id); }));
    menu.appendChild(floatingMenuItem(T('js.tabs.close_all'), function () { closeAll(); }));
    openFloatingUI(menu, x, y, tab.btn);
  }
  function showLinkSheet(anchor, x, y) {
    var url = anchor.getAttribute('href');
    var sheet = document.createElement('div');
    sheet.className = 'tab-link-sheet';
    sheet.setAttribute('role', 'menu');
    sheet.appendChild(floatingMenuItem(T('common.open'), function () {
      var tab = tabsById[activeTabId];
      if (tab) navigateInPane(tab, pathAndSearch(url), {}, true);
    }));
    sheet.appendChild(floatingMenuItem(T('js.tabs.open_in_new_tab'), function () {
      open(url, { background: true });
    }));
    openFloatingUI(sheet, x, y, anchor);
  }

  /* ── Long-press (tabs + links, §9) ───────────────────────── */
  function attachLongPress(el, cb) {
    var timer = null, sx = 0, sy = 0, fired = false, startTarget = null;
    el.addEventListener('touchstart', function (e) {
      if (e.touches.length !== 1) return;
      sx = e.touches[0].clientX; sy = e.touches[0].clientY;
      fired = false; startTarget = e.target;
      clearTimeout(timer);
      timer = setTimeout(function () { fired = true; cb(sx, sy, startTarget); }, LONG_PRESS_MS);
    }, { passive: true });
    el.addEventListener('touchmove', function (e) {
      if (!timer) return;
      var dx = e.touches[0].clientX - sx, dy = e.touches[0].clientY - sy;
      if (Math.abs(dx) > LONG_PRESS_TOLERANCE_PX || Math.abs(dy) > LONG_PRESS_TOLERANCE_PX) {
        clearTimeout(timer); timer = null;
      }
    }, { passive: true });
    el.addEventListener('touchend', function (e) {
      clearTimeout(timer); timer = null;
      if (fired) e.preventDefault();
    });
    el.addEventListener('touchcancel', function () { clearTimeout(timer); timer = null; });
  }

  function shouldBypassLink(a) {
    if (a.hasAttribute('data-tab-bypass')) return true;
    if (a.target === '_blank') return true;
    if (a.hasAttribute('download')) return true;
    var href = a.getAttribute('href') || '';
    if (href.charAt(0) === '#') return true;
    if (/^(mailto:|tel:|javascript:)/i.test(href)) return true;
    return false;
  }

  function initLinkLongPress() {
    var timer = null, sx = 0, sy = 0, targetA = null, fired = false, intent = null;
    document.addEventListener('touchstart', function (e) {
      if (window.innerWidth >= MOBILE_BREAKPOINT || e.touches.length !== 1) { targetA = null; return; }
      var a = e.target.closest && e.target.closest('a[href]');
      if (!a || shouldBypassLink(a)) { targetA = null; return; }
      targetA = a; fired = false; intent = null;
      sx = e.touches[0].clientX; sy = e.touches[0].clientY;
      clearTimeout(timer);
      timer = setTimeout(function () { fired = true; showLinkSheet(targetA, sx, sy); }, LONG_PRESS_MS);
    }, { passive: true });
    document.addEventListener('touchmove', function (e) {
      if (!timer || !targetA) return;
      var dx = e.touches[0].clientX - sx, dy = e.touches[0].clientY - sy;
      if (intent === null && (Math.abs(dx) > LONG_PRESS_TOLERANCE_PX || Math.abs(dy) > LONG_PRESS_TOLERANCE_PX)) {
        intent = Math.abs(dx) > Math.abs(dy) ? 'h' : 'v';
      }
      if (intent === 'v' || Math.abs(dx) > LONG_PRESS_TOLERANCE_PX || Math.abs(dy) > LONG_PRESS_TOLERANCE_PX) {
        clearTimeout(timer); timer = null;
      }
    }, { passive: true });
    document.addEventListener('touchend', function (e) {
      clearTimeout(timer); timer = null;
      if (fired && targetA) e.preventDefault();
      targetA = null;
    });
  }

  /* ── Link click interception (§9 "Mobile navigation default") ─ */
  function initLinkInterception() {
    document.addEventListener('click', function (e) {
      if (e.shiftKey || e.altKey) return;
      var a = e.target.closest && e.target.closest('a[href]');
      if (!a || shouldBypassLink(a)) return;
      var url = toURL(a.href);
      if (!url || url.origin !== window.location.origin) return;
      e.preventDefault();
      if (window.innerWidth < MOBILE_BREAKPOINT) {
        var tab = tabsById[activeTabId];
        if (tab) navigateInPane(tab, url.pathname + url.search, {}, true);
        return;
      }
      open(url.pathname + url.search, { background: e.metaKey || e.ctrlKey });
    });
    // Middle-click fires `auxclick`, not `click`, in every modern browser.
    document.addEventListener('auxclick', function (e) {
      if (e.button !== 1) return;
      var a = e.target.closest && e.target.closest('a[href]');
      if (!a || shouldBypassLink(a)) return;
      var url = toURL(a.href);
      if (!url || url.origin !== window.location.origin) return;
      e.preventDefault();
      open(url.pathname + url.search, { background: true });
    });
  }

  /* ── CSRF token refresh ──────────────────────────────────────
   * v0.6.4: the session cookie can be refreshed between the page load that
   * embedded the CSRF token and a later form POST (the session nears its TTL
   * expiry window, an intermediate fetch triggers a Set-Cookie, etc.). When
   * that happens the embedded token no longer matches the live session and
   * the CSRF middleware returns 403. Fetching a fresh token from the server
   * right before every POST eliminates the race entirely. */
  function refreshCSRFToken() {
    return fetch('/api/csrf-token', { credentials: 'same-origin' }).then(function (resp) {
      if (!resp.ok) return;
      return resp.json().then(function (data) {
        if (data && data.csrf_token) {
          document.querySelectorAll('input[name="_csrf_token"]').forEach(function (input) {
            input.value = data.csrf_token;
          });
        }
      });
    }).catch(function () { /* best-effort — the existing token may still work */ });
  }

  /* ── In-tab form submission (never a full top-level navigation) ─ */
  function initFormInterception() {
    document.addEventListener('submit', function (e) {
      var form = e.target;
      if (!form || !form.matches || !form.matches('form')) return;
      if (e.defaultPrevented) return; // a confirm()-guard upstream already cancelled it
      if (form.hasAttribute('data-tab-bypass') || form.target === '_blank') return;
      var pane = form.closest('.tab-pane');
      if (!pane) return;
      var tab = tabsById[pane.dataset.tabId];
      if (!tab) return;
      var action = toURL(form.getAttribute('action') || window.location.href);
      if (!action || action.origin !== window.location.origin) return;

      e.preventDefault();
      var method = (form.getAttribute('method') || 'GET').toUpperCase();
      var submitter = e.submitter;

      function doNavigate() {
        var fetchOpts = { method: method, __submitter: submitter };
        if (method === 'GET') {
          try {
            var qs = new URLSearchParams(new FormData(form, submitter));
            action.search = qs.toString() ? '?' + qs.toString() : '';
          } catch (err) { /* leave action.search as-is */ }
        } else if (form.enctype === 'multipart/form-data') {
          fetchOpts.body = new FormData(form, submitter);
        } else {
          fetchOpts.body = new URLSearchParams(new FormData(form, submitter));
        }
        navigateInPane(tab, action.pathname + action.search, fetchOpts, tab.id === activeTabId)
          .then(function () { restoreFormUI(submitter); })
          .catch(function () { restoreFormUI(submitter); });
      }

      if (method !== 'GET') {
        refreshCSRFToken().then(doNavigate);
      } else {
        doNavigate();
      }
    });
  }

  /* ── Per-tab history (in-tab navigations push/pop within one tab) ─ */
  function initHistory() {
    window.addEventListener('popstate', function (e) {
      var state = e.state;
      if (state && state.nodexiaTabId && tabsById[state.nodexiaTabId]) {
        var tab = tabsById[state.nodexiaTabId];
        if (tab.id !== activeTabId) activate(tab.id);
        navigateInPane(tab, window.location.pathname + window.location.search, {}, false);
        return;
      }
      var active = tabsById[activeTabId];
      if (active) navigateInPane(active, window.location.pathname + window.location.search, {}, false);
    });
  }

  /* ── Mobile content-area swipe → switch tab (§9) ─────────────
   * Mirrors app.js's initDrawer swipe: bail the moment vertical intent wins
   * so a normal vertical scroll inside a pane is never hijacked. */
  function stepTab(delta) {
    var list = orderedAll();
    if (list.length < 2) return;
    var idx = list.findIndex(function (t) { return t.id === activeTabId; });
    if (idx === -1) idx = 0;
    activate(list[(idx + delta + list.length) % list.length].id);
  }
  function initContentSwipe() {
    if (!contentEl) return;
    var sx = 0, sy = 0, tracking = false, intent = null;
    contentEl.addEventListener('touchstart', function (e) {
      if (window.innerWidth >= MOBILE_BREAKPOINT || e.touches.length !== 1) { tracking = false; return; }
      sx = e.touches[0].clientX; sy = e.touches[0].clientY;
      tracking = true; intent = null;
    }, { passive: true });
    contentEl.addEventListener('touchmove', function (e) {
      if (!tracking) return;
      var dx = e.touches[0].clientX - sx, dy = e.touches[0].clientY - sy;
      if (intent === null && (Math.abs(dx) > LONG_PRESS_TOLERANCE_PX || Math.abs(dy) > LONG_PRESS_TOLERANCE_PX)) {
        intent = Math.abs(dx) > Math.abs(dy) ? 'h' : 'v';
      }
      if (intent === 'v') tracking = false;
    }, { passive: true });
    contentEl.addEventListener('touchend', function (e) {
      if (!tracking || intent !== 'h') { tracking = false; return; }
      tracking = false;
      var dx = e.changedTouches[0].clientX - sx;
      if (Math.abs(dx) < SWIPE_MIN_PX) return;
      var rtl = document.documentElement.getAttribute('dir') === 'rtl';
      var forward = rtl ? dx > 0 : dx < 0;
      stepTab(forward ? 1 : -1);
    });
  }

  /* ── visibilitychange suspension (§9) ────────────────────── */
  function initVisibilitySuspend() {
    document.addEventListener('visibilitychange', function () {
      var tab = tabsById[activeTabId];
      if (!tab) return;
      fire(document.hidden ? 'tab-deactivated' : 'tab-activated', paneDetail(tab), false);
    });
  }

  /* ── terminal-tab-adapter.js → status dot bridge (§4/§7) ───── */
  function initStatusListener() {
    document.addEventListener('tab-status-changed', function (e) {
      var detail = e.detail || {};
      var tab = tabsById[detail.tabId];
      if (!tab) return;
      tab.statusState = detail.state;
      paintStatusDot(tab, detail.state);
    });
  }

  /* ── Keyboard shortcuts (§10; event.code, never event.key) ─── */
  function actionNewTab() { open('/'); }
  function actionCloseActive() { if (activeTabId) close(activeTabId); }
  function actionDuplicate() { if (activeTabId) duplicate(activeTabId); }
  function actionTogglePin() {
    var tab = tabsById[activeTabId];
    if (!tab) return;
    tab.pinned ? unpin(tab.id) : pin(tab.id);
  }
  function actionJump(n) {
    var list = orderedAll();
    var tab = list[n - 1];
    if (tab) activate(tab.id);
  }
  function shortcutsInert() {
    var el = document.activeElement;
    if (isTyping(el)) return true;
    if (el && el.closest && el.closest('[data-modal].is-open')) return true;
    if (switcherEl && !switcherEl.hasAttribute('hidden')) return true;
    return false;
  }
  function initKeyboard() {
    document.addEventListener('keydown', function (e) {
      if (shortcutsInert()) return;

      if (e.altKey && !e.ctrlKey && !e.metaKey) {
        if (e.shiftKey) {
          if (e.code === 'ArrowRight') { e.preventDefault(); stepTab(1); return; }
          if (e.code === 'ArrowLeft') { e.preventDefault(); stepTab(-1); return; }
          if (e.code === 'KeyT') { e.preventDefault(); reopenClosed(); return; }
          if (e.code === 'KeyD') { e.preventDefault(); actionDuplicate(); return; }
          if (e.code === 'KeyA') { e.preventDefault(); showSwitcher(); return; }
          return;
        }
        if (e.code === 'KeyT') { e.preventDefault(); actionNewTab(); return; }
        if (e.code === 'KeyW') { e.preventDefault(); actionCloseActive(); return; }
        if (e.code === 'KeyP') { e.preventDefault(); actionTogglePin(); return; }
        var m = /^Digit([1-8])$/.exec(e.code);
        if (m) { e.preventDefault(); actionJump(parseInt(m[1], 10)); return; }
        return;
      }

      // Best-effort: browser-chrome-reserved combos. A page keydown handler
      // either never sees these or has preventDefault() ignored by the
      // browser's own tab strip — except in an installed PWA window, which
      // has no native tab strip to conflict with (§10).
      if (e.ctrlKey && !e.shiftKey && !e.metaKey) {
        if (e.code === 'KeyT') { e.preventDefault(); actionNewTab(); return; }
        if (e.code === 'KeyW') { e.preventDefault(); actionCloseActive(); return; }
        if (e.code === 'Tab' || e.code === 'PageDown') { e.preventDefault(); stepTab(1); return; }
        if (e.code === 'PageUp') { e.preventDefault(); stepTab(-1); return; }
        var m2 = /^Digit([1-8])$/.exec(e.code);
        if (m2) { e.preventDefault(); actionJump(parseInt(m2[1], 10)); return; }
        return;
      }
      if (e.ctrlKey && e.shiftKey && !e.metaKey) {
        if (e.code === 'Tab') { e.preventDefault(); stepTab(-1); return; }
        if (e.code === 'KeyT') { e.preventDefault(); reopenClosed(); return; }
      }
    });
  }

  /* ── Mobile tab switcher + FAB (§3, §9) ──────────────────── */
  function renderSwitcherGrid() {
    if (!switcherGrid) return;
    switcherGrid.innerHTML = '';
    var list = orderedAll();
    if (!list.length) {
      var empty = document.createElement('div');
      empty.className = 'tab-switcher__empty';
      empty.textContent = T('js.tabs.no_tabs');
      switcherGrid.appendChild(empty);
      renderIcons();
      return;
    }
    list.forEach(function (tab) {
      var card = document.createElement('div');
      card.className = 'tab-switcher__card' + (tab.id === activeTabId ? ' is-active' : '');
      card.dataset.tabId = tab.id;
      card.setAttribute('tabindex', '0');
      card.setAttribute('role', 'button');
      card.setAttribute('aria-label', tab.title);

      var icon = document.createElement('i');
      icon.setAttribute('data-lucide', tab.icon);
      card.appendChild(icon);

      if (tab.kind === 'terminal') {
        var dot = document.createElement('span');
        dot.className = 'tab__status-dot tab__status-dot--' + (tab.statusState || 'connecting');
        card.appendChild(dot);
      }

      var title = document.createElement('span');
      title.className = 'tab-switcher__card-title';
      title.textContent = tab.title;
      card.appendChild(title);

      var urlEl = document.createElement('span');
      urlEl.className = 'tab-switcher__card-url';
      urlEl.textContent = tab.url;
      card.appendChild(urlEl);

      var closeBtn = document.createElement('button');
      closeBtn.type = 'button';
      closeBtn.className = 'tab__close tab-switcher__card-close';
      closeBtn.setAttribute('aria-label', T('js.tabs.close_tab'));
      closeBtn.innerHTML = '<i data-lucide="x"></i>';
      closeBtn.addEventListener('click', function (e) {
        e.stopPropagation();
        close(tab.id);
        renderSwitcherGrid();
        updateFabBadge();
      });
      card.appendChild(closeBtn);

      card.addEventListener('click', function () { activate(tab.id); hideSwitcher(); });
      card.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); activate(tab.id); hideSwitcher(); }
      });
      switcherGrid.appendChild(card);
    });
    renderIcons();
  }
  function showSwitcher() {
    if (!switcherEl) return;
    renderSwitcherGrid();
    updateFabBadge();
    switcherEl.removeAttribute('hidden');
    document.body.classList.add('tab-switcher-open');
    setTimeout(function () {
      var first = switcherEl.querySelector('.tab-switcher__card, [data-tab-switcher-new], [data-tab-switcher-dismiss]');
      if (first && first.focus) first.focus();
    }, 30);
  }
  function hideSwitcher() {
    if (!switcherEl) return;
    switcherEl.setAttribute('hidden', '');
    document.body.classList.remove('tab-switcher-open');
  }
  function initSwitcherUI() {
    if (switcherBackdrop) switcherBackdrop.addEventListener('click', hideSwitcher);
    if (switcherDismissBtn) switcherDismissBtn.addEventListener('click', hideSwitcher);
    if (switcherNewBtn) switcherNewBtn.addEventListener('click', function () { hideSwitcher(); actionNewTab(); });
    if (switcherCloseAllBtn) switcherCloseAllBtn.addEventListener('click', function () { closeAll(); hideSwitcher(); });
    if (fabEl) fabEl.addEventListener('click', showSwitcher);
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && switcherEl && !switcherEl.hasAttribute('hidden')) { e.stopPropagation(); hideSwitcher(); }
    });
    // Focus trap: keep Tab inside the switcher while it is open.
    if (switcherEl) {
      switcherEl.addEventListener('keydown', function (e) {
        if (e.key !== 'Tab' || switcherEl.hasAttribute('hidden')) return;
        var focusables = switcherEl.querySelectorAll(
          'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])');
        if (!focusables.length) return;
        var first = focusables[0];
        var last = focusables[focusables.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      });
    }
    // Swipe-down on the sheet to dismiss.
    var sheet = switcherEl ? switcherEl.querySelector('.tab-switcher__sheet') : null;
    if (sheet) {
      var sx = 0, sy = 0, tracking = false;
      sheet.addEventListener('touchstart', function (e) {
        if (e.touches.length !== 1) return;
        sx = e.touches[0].clientX; sy = e.touches[0].clientY;
        tracking = true;
      }, { passive: true });
      sheet.addEventListener('touchmove', function (e) {
        if (!tracking) return;
        var dy = e.touches[0].clientY - sy;
        if (dy > 0) {
          sheet.style.transition = 'none';
          sheet.style.transform = 'translateY(' + Math.min(dy, 120) + 'px)';
        }
      }, { passive: true });
      sheet.addEventListener('touchend', function (e) {
        if (!tracking) return;
        tracking = false;
        var dy = e.changedTouches[0].clientY - sy;
        sheet.style.transition = '';
        sheet.style.transform = '';
        if (dy > 72) hideSwitcher();
      });
    }
  }

  /* ── Boot ────────────────────────────────────────────────── */
  // Split view (§11) is a v0.6.0 stub only: the reserved event name
  // `tab-split-requested` ({tabId, edge}) is documented but deliberately
  // never dispatched or listened for here — nothing to wire up yet.

  function init() {
    if (ready) return; // idempotent — safe to call more than once
    try {
      tabBarEl = document.getElementById('nodexia-tabbar');
      if (!tabBarEl) return;
      if (typeof window.fetch !== 'function' || typeof window.DOMParser !== 'function') return;

      pinnedEl = tabBarEl.querySelector('[data-tab-pinned]');
      listEl = tabBarEl.querySelector('[data-tab-list]');
      newBtn = tabBarEl.querySelector('[data-tab-new]');
      contentEl = document.getElementById('tab-content');
      if (!pinnedEl || !listEl || !contentEl) return;

      switcherEl = document.getElementById('tab-switcher');
      if (switcherEl) {
        switcherBackdrop = switcherEl.querySelector('[data-tab-switcher-backdrop]');
        switcherGrid = switcherEl.querySelector('[data-tab-switcher-grid]');
        switcherCount = switcherEl.querySelector('[data-tab-switcher-count]');
        switcherNewBtn = switcherEl.querySelector('[data-tab-switcher-new]');
        switcherCloseAllBtn = switcherEl.querySelector('[data-tab-switcher-close-all]');
        switcherDismissBtn = switcherEl.querySelector('[data-tab-switcher-dismiss]');
      }
      fabEl = document.getElementById('tab-fab');
      if (fabEl) fabBadge = fabEl.querySelector('[data-tab-fab-badge]');

      // Adopt whatever is already rendered in #tab-content as the first
      // (bootstrap) pane. This MUST happen synchronously, right here, before
      // this script returns control to the parser — the page's own
      // PageScripts (e.g. terminal.js + terminal-tab-adapter.js) are later
      // <script> tags in the same document and resolve
      // document.currentScript.closest('.tab-pane') the moment they run;
      // that only finds this wrapper if it already exists by then.
      var existingNodes = Array.prototype.slice.call(contentEl.children);
      var stored = loadStoredTabs();
      closedRing = loadClosedRing();
      var currentPath = window.location.pathname + window.location.search;

      var matched = null;
      var restList = [];
      if (stored) {
        stored.tabs.forEach(function (t) {
          if (!matched && t.url === currentPath) matched = t;
          else restList.push(t);
        });
      }

      var bootTab = matched ? {
        id: matched.id, url: matched.url, title: matched.title, icon: matched.icon,
        kind: matched.kind, pinned: !!matched.pinned, order: matched.order,
        createdAt: matched.createdAt || Date.now()
      } : {
        id: genId(), url: currentPath,
        title: (document.title || '').trim() || T('js.tabs.untitled'),
        icon: iconForURL(currentPath), kind: kindForURL(currentPath),
        pinned: false, order: restList.length, createdAt: Date.now()
      };
      bootTab.loading = false;
      bootTab.loaded = true;
      bootTab.pane = document.createElement('div');
      bootTab.pane.className = 'tab-pane is-active';
      bootTab.pane.dataset.tabId = bootTab.id;
      existingNodes.forEach(function (n) { bootTab.pane.appendChild(n); });
      contentEl.appendChild(bootTab.pane);
      registerTab(bootTab);
      activeTabId = bootTab.id;
      renderTabButton(bootTab);

      // Every other restored tab reopens as a background tab, fetched fresh
      // (sessionStorage only ever held metadata — live WS/xterm state can't
      // survive a real reload and isn't meant to, §6).
      restList.forEach(function (t) {
        var tab = {
          id: t.id, url: t.url, title: t.title, icon: t.icon, kind: t.kind,
          pinned: !!t.pinned, order: t.order, createdAt: t.createdAt || Date.now(),
          loading: false, loaded: false
        };
        var pane = document.createElement('div');
        pane.className = 'tab-pane';
        pane.setAttribute('hidden', '');
        pane.dataset.tabId = tab.id;
        tab.pane = pane;
        contentEl.appendChild(pane);
        registerTab(tab);
        renderTabButton(tab);
        loadPane(tab, tab.url, {});
      });

      relayoutTabs();
      updateFabBadge();

      if (newBtn) newBtn.addEventListener('click', function () { actionNewTab(); });
      initSwitcherUI();
      initLinkInterception();
      initFormInterception();
      initHistory();
      initKeyboard();
      initContentSwipe();
      initLinkLongPress();
      initVisibilitySuspend();
      initStatusListener();

      schedulePersist();
      renderIcons();
      // lucide.min.js loads with `defer`, so it may not be defined yet at
      // this point in the parse (this script runs synchronously, earlier);
      // re-render once more after the document + deferred scripts settle.
      document.addEventListener('DOMContentLoaded', renderIcons);

      tabBarEl.removeAttribute('hidden');
      ready = true;

      updateNavHighlight(bootTab.url.split('?')[0]);
      fire('tabs-restored', { tabs: getAll(), activeTabId: activeTabId }, false);
    } catch (err) {
      // A tab-system failure must never break the page's own content — the
      // bar simply stays hidden (never removed above) and everything
      // renders exactly like a pre-v0.6.0 direct load.
    }
  }

  // navigateActive replaces the active tab's content by fetching url, mirroring
  // a normal in-tab navigation.  Falls back to window.location.href when the
  // tab system is not ready so callers can use it unconditionally.
  function navigateActive(url) {
    if (!ready) { window.location.href = url; return; }
    var tab = tabsById[activeTabId];
    if (!tab) { window.location.href = url; return; }
    navigateInPane(tab, pathAndSearch(url), {}, true);
  }

  // reloadActive reloads the active tab's content.  Falls back to
  // window.location.reload() when the tab system is not ready.
  function reloadActive() {
    if (!ready || !tabsById[activeTabId]) { window.location.reload(); return; }
    reload(activeTabId);
  }

  window.NodexiaTabs = {
    init: init,
    open: open,
    close: close,
    closeOthers: closeOthers,
    closeAll: closeAll,
    activate: activate,
    pin: pin,
    unpin: unpin,
    duplicate: duplicate,
    reload: reload,
    reopenClosed: reopenClosed,
    navigate: navigateActive,
    reloadActive: reloadActive,
    getActive: getActive,
    getAll: getAll,
    MOBILE_TAB_LIMIT: 5,
    iconForURL: iconForURL
  };

  init();
  document.addEventListener('DOMContentLoaded', init);
})();
