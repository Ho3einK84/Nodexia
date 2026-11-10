/* Nodexia interactive layer.
 *
 * The Content-Security-Policy forbids inline <script> blocks, so ALL client
 * behaviour lives in this file (served from 'self'). Everything is progressive
 * enhancement: if a feature's target elements are absent, it quietly no-ops.
 *
 * Loaded as a native ES module (no bundler, no npm). Shared primitives — the
 * i18n bridge, byte formatter, icon rendering, top progress bar, tab-aware
 * navigation/cleanup and button busy state — live in ./modules/core.js and
 * are imported below. core.js also publishes the window.nxT / window.nxTn /
 * window.nxHumanBytes bridges that the classic page scripts rely on.
 */
import { I18N, prefersReducedMotion, renderIcons, startTopBar, finishTopBar, tabNavigate, tabReload, registerCleanup, markButtonBusy } from './modules/core.js';
import { initForms, initLinks, initFlash, initSecrets, initCopyButtons, initCopyTargets, initProgressBars, initReveal, initBackToTop, initShortcuts, initCollapsibles, initAdvancedToggle, copyText } from './modules/ui.js';
import { initNodeCredentials, initActionMenus, initModals, initDeployModal } from './modules/modals.js';

(function () {
  'use strict';

  /* ── Auto-refresh with live countdown ───────────────────── */
  function initAutoRefresh(root) {
    root = root || document;
    var select = root.querySelector('#auto-refresh-select');
    if (!select) return;
    var url = select.getAttribute('data-refresh-url');
    if (!url) return;
    var refreshURL = url + (url.indexOf('?') === -1 ? '?' : '&') + 'refresh=1';
    var key = 'nodexia_auto_refresh_v2_' + (select.getAttribute('data-refresh-key') || url);

    var pill = document.createElement('span');
    pill.className = 'refresh-countdown';
    pill.hidden = true;
    select.parentNode.appendChild(pill);

    var tickTimer = null;
    var deadline = 0;

    function stop() {
      if (tickTimer) { clearInterval(tickTimer); tickTimer = null; }
      pill.hidden = true;
    }
    function start(ms) {
      stop();
      deadline = Date.now() + ms;
      pill.hidden = false;
      updatePill();
      tickTimer = setInterval(function () {
        if (Date.now() >= deadline) {
          stop();
          startTopBar();
          tabNavigate(refreshURL);
          return;
        }
        updatePill();
      }, 250);
    }
    function updatePill() {
      var remaining = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
      pill.innerHTML = '<i data-lucide="refresh-cw"></i> ' +
        escapeHTML(I18N.t('js.refresh.next_in', { seconds: remaining }));
      renderIcons();
    }
    function apply() {
      var ms = parseInt(select.value, 10);
      if (ms > 0) {
        try { localStorage.setItem(key, select.value); } catch (e) {}
        start(ms);
      } else {
        try { localStorage.setItem(key, '0'); } catch (e) {}
        stop();
      }
    }

    var saved = null;
    try { saved = localStorage.getItem(key); } catch (e) {}
    if (saved !== null) {
      select.value = saved;
    } else {
      var selectedOpt = select.querySelector('option[selected]');
      if (selectedOpt) {
        select.value = selectedOpt.value;
      } else {
        select.value = '0';
      }
    }
    select.addEventListener('change', apply);
    apply();
    registerCleanup(root, stop);
  }

  /* ── Live output stream (Server-Sent Events) ────────────────
   * Replaces the old 2-second full-page poll. A running command / node action /
   * install renders a result card carrying data-stream-sse-url; we subscribe to
   * that feed and append output chunks in place. EventSource reconnects on its
   * own after a network blip — and because the server resends the buffer from
   * offset 0 on every connect, we reset the output on each (re)open so a
   * reconnect never duplicates lines.
   */
  function initStream(root) {
    root = root || document;
    var card = root.querySelector('[data-stream-sse-url]');
    if (!card) return;
    var url = card.getAttribute('data-stream-sse-url');
    if (!url) return;

    var stdout = card.querySelector('[data-stream-stdout]');
    var stderr = card.querySelector('[data-stream-stderr]');
    var reloadOnDone = card.hasAttribute('data-stream-reload');

    if (typeof EventSource === 'undefined') {
      streamFallback(card);
      return;
    }

    // Auto-scroll the output to the bottom unless the user scrolled up to read.
    var autoScroll = true;
    if (stdout) {
      stdout.addEventListener('scroll', function () {
        autoScroll = stdout.scrollHeight - stdout.scrollTop - stdout.clientHeight < 24;
      }, { passive: true });
    }
    function scrollPinned(el) {
      if (el && autoScroll) el.scrollTop = el.scrollHeight;
    }

    var finished = false;
    card.classList.add('is-connecting');
    var es = new EventSource(url);
    registerCleanup(root, function () {
      finished = true;
      try { es.close(); } catch (err) { /* best-effort */ }
    });

    es.addEventListener('open', function () {
      card.classList.remove('is-connecting', 'is-reconnecting');
      // Server resends from offset 0 on each connect → reset before re-streaming.
      if (stdout) stdout.textContent = '';
      if (stderr) { stderr.textContent = ''; stderr.hidden = true; }
    });

    es.addEventListener('output', function (e) {
      if (!stdout) return;
      stdout.textContent += e.data;
      scrollPinned(stdout);
    });

    es.addEventListener('stderr', function (e) {
      if (!stderr) return;
      stderr.hidden = false;
      stderr.textContent += e.data;
      scrollPinned(stderr);
    });

    es.addEventListener('done', function (e) {
      finished = true;
      es.close();
      if (reloadOnDone) { reloadForResult(); return; }
      finishStreamCard(card, parseJSON(e.data), false);
    });

    es.addEventListener('error', function (e) {
      // A server-sent `event: error` carries data; a transport drop does not.
      if (e && e.data) {
        finished = true;
        es.close();
        if (reloadOnDone) { reloadForResult(); return; }
        finishStreamCard(card, parseJSON(e.data), true);
        return;
      }
      // Transport error. EventSource retries on its own while readyState is
      // CONNECTING; if it gave up (CLOSED), fall back to a manual refresh so the
      // connecting overlay never sticks.
      card.classList.remove('is-connecting');
      if (finished) return;
      if (es.readyState === EventSource.CLOSED) {
        streamFallback(card);
      } else {
        card.classList.add('is-reconnecting');
      }
    });
  }

  function finishStreamCard(card, payload, isError) {
    payload = payload || {};
    card.classList.remove('is-connecting', 'is-reconnecting', 'cmd-result-card--live');
    card.removeAttribute('data-stream-sse-url');

    var status = String(payload.status || (isError ? 'failed' : 'completed'));
    var failed = isError || status === 'failed' ||
      (payload.exitCode && payload.exitCode !== '0' && payload.exitCode !== 'n/a');
    card.classList.add(failed ? 'cmd-result-card--error' : 'cmd-result-card--ok');

    var label = card.querySelector('[data-stream-label]');
    if (label) {
      var doneText = failed
        ? (card.getAttribute('data-stream-label-error') || I18N.t('js.failed'))
        : (card.getAttribute('data-stream-label-done') || I18N.t('js.stream.complete'));
      label.innerHTML = '<i data-lucide="' + (failed ? 'x-circle' : 'check-circle') +
        '" class="icon-in-button"></i> ' + escapeHTML(doneText);
      label.classList.add('stream-fade-in');
    }

    var meta = card.querySelector('[data-stream-meta]');
    if (meta) {
      var html = '';
      if (payload.exitCode && payload.exitCode !== 'n/a') {
        var ok = payload.exitCode === '0';
        html += '<span class="result-exit ' + (ok ? 'result-exit--ok' : 'result-exit--err') +
          '">' + escapeHTML(I18N.t('commands.exit', { code: payload.exitCode })) + '</span>';
      }
      if (payload.duration) {
        html += '<code class="result-duration">' + escapeHTML(payload.duration) + '</code>';
      }
      meta.innerHTML = html;
      meta.classList.add('stream-fade-in');
    }

    var statusLine = card.querySelector('[data-stream-status]');
    if (statusLine) {
      if (failed && payload.message) {
        statusLine.className = 'result-message result-message--error';
        statusLine.textContent = payload.message;
      } else {
        statusLine.remove();
      }
    }

    // A node-action card self-dismisses on success / exposes a manual close on
    // failure, and stderr that arrived on a clean exit is no longer styled red.
    if (card.hasAttribute('data-node-result')) {
      var doneStderr = card.querySelector('[data-stream-stderr]');
      if (doneStderr && !failed) doneStderr.classList.remove('output-block--error');
      scheduleNodeResultDismiss(card, failed);
    }

    // Output that streamed in after page load has no copy button yet.
    initCopyButtons();
    renderIcons();
  }

  function streamFallback(card) {
    card.classList.remove('is-connecting');
    var status = card.querySelector('[data-stream-status]');
    var url = card.getAttribute('data-stream-page-url') || window.location.href;
    if (status) {
      status.innerHTML = '<a class="btn btn--ghost btn--sm" href="' + escapeHTML(url) +
        '"><i data-lucide="refresh-cw" class="icon-in-button"></i> ' + escapeHTML(I18N.t('js.stream.refresh')) + '</a>';
      renderIcons();
    }
  }

  /* ── Node action result card: auto-dismiss / manual close ───
   * Once a node action completes, a successful result counts down ("Closing in
   * 5…") and then slides out, navigating to the clean nodes URL (no ?stream=) so
   * the node list is shown without a stale result card. A failed result stays
   * put — the error output must remain readable — and gets a manual close (X)
   * that navigates to the clean URL. Two entry points feed this: a card already
   * completed at page load (server-rendered) and a live card that finished over
   * SSE (finishStreamCard calls scheduleNodeResultDismiss directly).
   */
  var NODE_RESULT_COUNTDOWN_SECS = 5;

  function initNodeResult(root) {
    root = root || document;
    var card = root.querySelector('[data-node-result]');
    if (!card) return;
    // A still-running card finishes through the SSE path, which schedules its own
    // dismiss; skip it here so we don't act before the action completes.
    if (card.hasAttribute('data-stream-sse-url')) return;
    scheduleNodeResultDismiss(card, card.getAttribute('data-node-failed') === 'true');
  }

  function scheduleNodeResultDismiss(card, failed) {
    if (card.dataset.nodeDismissReady === '1') return;
    card.dataset.nodeDismissReady = '1';
    var cleanURL = card.getAttribute('data-node-clean-url') || '/servers';
    if (failed) {
      addNodeResultClose(card, cleanURL);
      return;
    }
    startNodeResultCountdown(card, cleanURL);
  }

  // addNodeResultClose pins a dismiss (X) button to the card corner.
  function addNodeResultClose(card, cleanURL) {
    if (card.querySelector('.node-result-close')) return;
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'node-result-close';
    btn.setAttribute('aria-label', I18N.t('js.node_result.dismiss'));
    btn.innerHTML = '<i data-lucide="x"></i>';
    btn.addEventListener('click', function () { dismissNodeCard(card, cleanURL); });
    card.appendChild(btn);
    renderIcons();
  }

  // startNodeResultCountdown shows a live "Closing in N…" pill, then dismisses.
  // Hovering the card pauses it so a user reading the output is never yanked away.
  function startNodeResultCountdown(card, cleanURL) {
    var meta = card.querySelector('[data-stream-meta]') || card.querySelector('.result-header');
    var pill = document.createElement('span');
    pill.className = 'node-result-countdown';
    if (meta) meta.appendChild(pill);

    var remaining = NODE_RESULT_COUNTDOWN_SECS;
    var paused = false;
    function paint() {
      pill.innerHTML = '<span class="node-result-countdown__dot"></span> ' +
        escapeHTML(I18N.t('js.node_result.closing', { seconds: remaining }));
    }
    paint();

    card.addEventListener('mouseenter', function () { paused = true; pill.classList.add('is-paused'); });
    card.addEventListener('mouseleave', function () { paused = false; pill.classList.remove('is-paused'); });

    var timer = setInterval(function () {
      if (paused) return;
      remaining -= 1;
      if (remaining <= 0) {
        clearInterval(timer);
        dismissNodeCard(card, cleanURL);
        return;
      }
      paint();
    }, 1000);
    registerCleanup(card.closest('.tab-pane') || document, function () { clearInterval(timer); });
  }

  // dismissNodeCard slides the card out (honoring reduced-motion) then navigates
  // to the clean nodes URL.
  function dismissNodeCard(card, cleanURL) {
    startTopBar();
    if (prefersReducedMotion) { tabNavigate(cleanURL); return; }
    var navigated = false;
    function go() { if (navigated) return; navigated = true; tabNavigate(cleanURL); }
    card.classList.add('is-dismissing');
    card.addEventListener('transitionend', go, { once: true });
    setTimeout(go, 600); // fallback when transitionend does not fire
  }

  /* ── Bulk action progress stream (SSE) ──────────────────────
   * The bulk result page runs a command across many servers at once. Each
   * server row is pushed as a `row` event as it transitions
   * pending → running → ok/failed, swapping an inline spinner for a check / x.
   */
  function initBulkStream(root) {
    root = root || document;
    var card = root.querySelector('[data-bulk-sse-url]');
    if (!card) return;
    var url = card.getAttribute('data-bulk-sse-url');
    if (!url || typeof EventSource === 'undefined') return;

    var finished = false;
    var es = new EventSource(url);
    registerCleanup(root, function () {
      finished = true;
      try { es.close(); } catch (err) { /* best-effort */ }
    });

    es.addEventListener('row', function (e) {
      var row = parseJSON(e.data);
      if (row.index == null) return;
      updateBulkRow(card, row);
    });

    es.addEventListener('done', function (e) {
      finished = true;
      es.close();
      updateBulkSummary(card, parseJSON(e.data));
    });

    es.addEventListener('error', function (e) {
      if (e && e.data) { finished = true; es.close(); return; }
      // Transient transport error → EventSource retries on its own.
    });
  }

  function updateBulkRow(card, row) {
    card.querySelectorAll('[data-bulk-index="' + row.index + '"]').forEach(function (el) {
      if (el.classList.contains('bulk-result-card')) {
        el.className = 'bulk-result-card bulk-result-card--' + row.status;
      }
      var chip = el.querySelector('[data-bulk-status]');
      if (chip) chip.outerHTML = bulkChip(row.status);
      var code = el.querySelector('[data-bulk-exit]');
      if (code) code.textContent = row.exitCode ? row.exitCode : '—';
      var reason = el.querySelector('[data-bulk-reason]');
      if (reason) {
        var isCell = reason.tagName === 'TD';
        reason.textContent = row.reason ? row.reason : (isCell ? '—' : '');
        if (!isCell) reason.hidden = !row.reason;
      }
    });
    renderIcons();
  }

  function bulkChip(status) {
    var allowed = ['ok', 'failed', 'skipped', 'pending', 'running', 'timeout'];
    var safeStatus = allowed.indexOf(status) !== -1 ? status : 'unknown';
    var inner = {
      ok: '<i data-lucide="check-circle-2" class="icon-in-button"></i> ' + I18N.t('bulk.status_ok'),
      failed: '<i data-lucide="x-circle" class="icon-in-button"></i> ' + I18N.t('bulk.status_failed'),
      skipped: '<i data-lucide="skip-forward" class="icon-in-button"></i> ' + I18N.t('bulk.status_skipped'),
      pending: '<span class="spinner spinner--sm"></span> ' + I18N.t('bulk.status_pending'),
      running: '<span class="spinner spinner--sm"></span> ' + I18N.t('bulk.status_running'),
      timeout: '<i data-lucide="x-circle" class="icon-in-button"></i> ' + I18N.t('bulk.status_failed')
    }[safeStatus] || ('<i data-lucide="x-circle" class="icon-in-button"></i> ' + I18N.t('bulk.status_failed'));
    return '<span class="bulk-status bulk-status--' + safeStatus + '" data-bulk-status>' + inner + '</span>';
  }

  function updateBulkSummary(card, done) {
    card.removeAttribute('data-bulk-sse-url');
    var note = card.querySelector('[data-bulk-progress-note]');
    if (note) note.remove();
    setBulkCount(card, 'inprogress', done.inProgress || 0);
    setBulkCount(card, 'ok', done.ok || 0);
    setBulkCount(card, 'failed', done.failed || 0);
    setBulkCount(card, 'skipped', done.skipped || 0);
    renderIcons();
  }

  function setBulkCount(card, key, value) {
    var el = card.querySelector('[data-bulk-count="' + key + '"]');
    if (!el) return;
    el.innerHTML = bulkCountInner(key, value);
    if (key !== 'ok') el.hidden = value <= 0;
  }

  function bulkCountInner(key, value) {
    if (key === 'ok') return '<i data-lucide="check-circle-2" class="icon-in-button"></i> ' + I18N.t('bulk.count_ok', { count: value });
    if (key === 'failed') return '<i data-lucide="x-circle" class="icon-in-button"></i> ' + I18N.t('bulk.count_failed', { count: value });
    if (key === 'skipped') return '<i data-lucide="skip-forward" class="icon-in-button"></i> ' + I18N.t('bulk.count_skipped', { count: value });
    return '<span class="spinner spinner--sm"></span> ' + I18N.t('bulk.count_in_progress', { count: value });
  }

  function parseJSON(data) {
    try { return JSON.parse(data); } catch (err) { return {}; }
  }

  function reloadForResult() {
    startTopBar();
    tabReload();
  }

  function escapeHTML(value) {
    return String(value).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  /* ── Home fleet warnings: reveal undismissed, dismiss to localStorage ─────
   * Banners render hidden; each carries a stable data-warning-id. A dismissed
   * id stays hidden until the underlying condition changes (the server then
   * emits a different id). Dismissals expire after 7 days so a long-standing
   * problem eventually resurfaces even if its id never changes.
   */
  var WARNING_DISMISS_TTL_MS = 7 * 24 * 60 * 60 * 1000;

  function readWarningDismissals() {
    try {
      var raw = localStorage.getItem('nodexia_warn_dismissed');
      var map = raw ? JSON.parse(raw) : {};
      var now = Date.now();
      var changed = false;
      Object.keys(map).forEach(function (id) {
        if (typeof map[id] !== 'number' || now - map[id] > WARNING_DISMISS_TTL_MS) {
          delete map[id];
          changed = true;
        }
      });
      if (changed) localStorage.setItem('nodexia_warn_dismissed', JSON.stringify(map));
      return map;
    } catch (err) { return {}; }
  }

  function initHomeWarnings() {
    var wrap = document.querySelector('[data-home-warnings]');
    if (!wrap) return;
    var dismissed = readWarningDismissals();

    wrap.querySelectorAll('[data-warning-id]').forEach(function (banner) {
      var id = banner.getAttribute('data-warning-id');
      if (!dismissed[id]) banner.hidden = false;

      var close = banner.querySelector('[data-warning-dismiss]');
      if (!close) return;
      close.addEventListener('click', function () {
        try {
          var map = readWarningDismissals();
          map[id] = Date.now();
          localStorage.setItem('nodexia_warn_dismissed', JSON.stringify(map));
        } catch (err) { /* private mode — dismiss for this view only */ }
        banner.classList.add('is-dismissing');
        setTimeout(function () { banner.remove(); }, prefersReducedMotion ? 0 : 200);
      });
    });
    renderIcons();
  }

  /* ── Manual "refresh now" buttons ───────────────────────── */
  function initManualRefresh(root) {
    root = root || document;
    root.querySelectorAll('[data-refresh-now]').forEach(function (btn) {
      if (btn.dataset.refreshReady === '1') return;
      btn.dataset.refreshReady = '1';
      btn.addEventListener('click', function (e) {
        var url = btn.getAttribute('data-refresh-now');
        if (!url) return;
        e.preventDefault();
        startTopBar();
        markButtonBusy(btn);
        tabNavigate(url);
      });
    });
  }

  /* ── Live Log Drawer (Slide-over) ─────────────────────────── */
  function initLogDrawer(root) {
    root = root || document;
    var drawer = document.getElementById('node-log-drawer');
    if (!drawer || drawer.dataset.drawerReady === '1') return;
    drawer.dataset.drawerReady = '1';

    var cmdEl = drawer.querySelector('[data-log-command]');
    var outEl = drawer.querySelector('[data-log-stdout]');
    var errEl = drawer.querySelector('[data-log-stderr]');
    var statusEl = drawer.querySelector('[data-log-status]');
    var autoScrollCb = drawer.querySelector('[data-log-autoscroll]');
    var copyBtn = drawer.querySelector('[data-log-copy]');
    var activeES = null;

    function openDrawer(sseUrl, isRunning) {
      drawer.hidden = false;
      void drawer.offsetWidth;
      drawer.classList.add('is-open');
      document.body.classList.add('modal-open');

      if (!sseUrl) return;
      if (activeES) activeES.close();

      if (outEl) outEl.textContent = '';
      if (errEl) { errEl.textContent = ''; errEl.hidden = true; }
      if (statusEl) {
        statusEl.className = 'log-drawer__foot is-live';
        statusEl.innerHTML = '<span class="live-dot"></span> ' + (I18N.t('nodes.runner_conn_wait') || 'Connecting to live stream…');
      }

      activeES = new EventSource(sseUrl);
      activeES.addEventListener('chunk', function (e) {
        try {
          var payload = JSON.parse(e.data);
          if (payload.stdout && outEl) {
            outEl.textContent += payload.stdout;
            if (!autoScrollCb || autoScrollCb.checked) outEl.scrollTop = outEl.scrollHeight;
          }
          if (payload.stderr && errEl) {
            errEl.hidden = false;
            errEl.textContent += payload.stderr;
            if (!autoScrollCb || autoScrollCb.checked) errEl.scrollTop = errEl.scrollHeight;
          }
          if (payload.command && cmdEl) {
            cmdEl.textContent = payload.command;
          }
        } catch (err) {}
      });

      activeES.addEventListener('done', function () {
        if (activeES) { activeES.close(); activeES = null; }
        if (statusEl) {
          statusEl.className = 'log-drawer__foot';
          statusEl.innerHTML = '<i data-lucide="check-circle"></i> ' + (I18N.t('nodes.stage_ready') || 'Ready');
          renderIcons();
        }
      });

      activeES.onerror = function () {
        if (activeES) { activeES.close(); activeES = null; }
        if (statusEl) {
          statusEl.className = 'log-drawer__foot';
          statusEl.innerHTML = '<i data-lucide="info"></i> ' + (I18N.t('nodes.stage_ready') || 'Completed');
          renderIcons();
        }
      };
    }

    function closeDrawer() {
      if (!drawer.classList.contains('is-open')) return;
      drawer.classList.remove('is-open');
      document.body.classList.remove('modal-open');
      if (activeES) { activeES.close(); activeES = null; }
      setTimeout(function () {
        if (!drawer.classList.contains('is-open')) drawer.hidden = true;
      }, 240);
    }

    drawer.querySelectorAll('[data-log-drawer-close]').forEach(function (el) {
      el.addEventListener('click', function (e) {
        e.preventDefault();
        closeDrawer();
      });
    });

    document.querySelectorAll('[data-log-drawer-open]').forEach(function (btn) {
      btn.addEventListener('click', function (e) {
        e.preventDefault();
        var sseUrl = btn.getAttribute('data-log-sse-url');
        var isRunning = btn.getAttribute('data-log-running') === '1';
        openDrawer(sseUrl, isRunning);
      });
    });

    if (copyBtn) {
      copyBtn.addEventListener('click', function () {
        var text = (outEl ? outEl.textContent : '') + (errEl && !errEl.hidden ? '\n' + errEl.textContent : '');
        copyText(text).then(function (ok) {
          copyBtn.innerHTML = ok
            ? '<i data-lucide="check"></i> ' + (I18N.t('nodes.copy_toast') || 'Copied')
            : '<i data-lucide="x"></i> ' + (I18N.t('js.failed') || 'Failed');
          renderIcons();
          setTimeout(function () {
            copyBtn.innerHTML = '<i data-lucide="copy" class="icon-in-button"></i> ' + (I18N.t('nodes.runner_copy_log') || 'Copy log');
            renderIcons();
          }, 1500);
        });
      });
    }
  }

  /* ── Mobile drawer (hamburger menu) ─────────────────────── */
  function initDrawer() {
    var drawer = document.getElementById('mobile-drawer');
    var backdrop = document.querySelector('[data-drawer-backdrop]');
    if (!drawer || !backdrop) return;
    var openers = document.querySelectorAll('[data-drawer-open]');
    var lastFocus = null;

    function setExpanded(value) {
      openers.forEach(function (o) { o.setAttribute('aria-expanded', value ? 'true' : 'false'); });
    }
    function open() {
      lastFocus = document.activeElement;
      drawer.classList.add('is-open');
      backdrop.classList.add('is-open');
      drawer.setAttribute('aria-hidden', 'false');
      document.body.classList.add('drawer-open');
      setExpanded(true);
      var closeBtn = drawer.querySelector('[data-drawer-close]');
      if (closeBtn) closeBtn.focus();
    }
    function close() {
      if (!drawer.classList.contains('is-open')) return;
      drawer.classList.remove('is-open');
      backdrop.classList.remove('is-open');
      drawer.setAttribute('aria-hidden', 'true');
      document.body.classList.remove('drawer-open');
      setExpanded(false);
      if (lastFocus && lastFocus.focus) lastFocus.focus();
    }

    openers.forEach(function (o) {
      o.addEventListener('click', function (e) { e.preventDefault(); open(); });
    });
    backdrop.addEventListener('click', close);
    drawer.querySelectorAll('[data-drawer-close]').forEach(function (b) {
      b.addEventListener('click', close);
    });
    // Tapping a link navigates and should dismiss the drawer.
    drawer.querySelectorAll('a[href]').forEach(function (a) {
      a.addEventListener('click', close);
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') close();
    });
    // Trap Tab inside the open drawer (mirrors the modal focus trap) so
    // keyboard users can't wander into the inert page behind it.
    drawer.addEventListener('keydown', function (e) {
      if (e.key !== 'Tab' || !drawer.classList.contains('is-open')) return;
      var focusables = drawer.querySelectorAll(
        'a[href], button:not([disabled]), input, select, textarea, [tabindex]:not([tabindex="-1"])');
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
    // If the viewport grows past the mobile breakpoint, ensure it's closed.
    window.addEventListener('resize', function () {
      if (window.innerWidth > 700) close();
    });

    // Swipe-to-close: dragging the panel toward its off-canvas edge follows
    // the finger and releases past a threshold. The closing direction flips
    // with text direction (the drawer parks on the inline-end side).
    var rtl = (document.documentElement.getAttribute('dir') === 'rtl');
    var touchStartX = 0, touchStartY = 0, dragging = false, closingDx = 0;

    drawer.addEventListener('touchstart', function (e) {
      if (!drawer.classList.contains('is-open') || e.touches.length !== 1) return;
      touchStartX = e.touches[0].clientX;
      touchStartY = e.touches[0].clientY;
      dragging = true;
      closingDx = 0;
    }, { passive: true });

    drawer.addEventListener('touchmove', function (e) {
      if (!dragging) return;
      var dx = e.touches[0].clientX - touchStartX;
      var dy = e.touches[0].clientY - touchStartY;
      // Vertical intent = the user is scrolling the drawer, not dismissing it.
      if (Math.abs(dy) > Math.abs(dx) && closingDx === 0) { dragging = false; return; }
      closingDx = rtl ? -dx : dx;
      if (closingDx > 0) {
        drawer.style.transition = 'none';
        drawer.style.transform = 'translateX(' + (rtl ? -closingDx : closingDx) + 'px)';
      }
    }, { passive: true });

    drawer.addEventListener('touchend', function () {
      if (!dragging) return;
      dragging = false;
      drawer.style.transition = '';
      drawer.style.transform = '';
      if (closingDx > 72) close();
      closingDx = 0;
    });
  }

  /* ── Collapsible server cards & expand all ─────────────── */
  function initServerCards(root) {
    root = root || document;
    var cards = root.querySelectorAll('[data-server-card]');
    if (!cards.length) return;

    var toggleAllBtn = root.getElementById ? root.getElementById('server-toggle-all') : document.getElementById('server-toggle-all');
    var toggleAllLabel = root.getElementById ? root.getElementById('server-toggle-all-label') : document.getElementById('server-toggle-all-label');

    function updateCard(card, open, persist) {
      var body = card.querySelector('[data-server-body]');
      var toggleBtn = card.querySelector('[data-server-toggle-btn]');
      var serverId = card.getAttribute('data-server-id');

      if (open) {
        card.classList.add('server-card--expanded');
        if (body) body.removeAttribute('hidden');
        if (toggleBtn) toggleBtn.setAttribute('aria-expanded', 'true');
        if (persist && serverId) {
          try { localStorage.setItem('server_card_' + serverId, 'open'); } catch (e) {}
        }
      } else {
        card.classList.remove('server-card--expanded');
        if (body) body.setAttribute('hidden', '');
        if (toggleBtn) toggleBtn.setAttribute('aria-expanded', 'false');
        if (persist && serverId) {
          try { localStorage.setItem('server_card_' + serverId, 'closed'); } catch (e) {}
        }
      }
    }

    cards.forEach(function (card) {
      if (card.dataset.serverCardReady === '1') return;
      card.dataset.serverCardReady = '1';

      var serverId = card.getAttribute('data-server-id');
      var stored = null;
      if (serverId) {
        try { stored = localStorage.getItem('server_card_' + serverId); } catch (e) {}
      }
      // Default to closed so the server list is compact and space-efficient.
      var isOpen = stored === 'open';
      updateCard(card, isOpen, false);

      var header = card.querySelector('[data-server-toggle]');
      if (header) {
        header.addEventListener('click', function (e) {
          if (e.target.closest('a, button, input, select, textarea, .action-menu, .server-card__checkbox-wrap, .server-card__quick-actions')) {
            return;
          }
          var currentlyOpen = card.classList.contains('server-card--expanded');
          updateCard(card, !currentlyOpen, true);
          checkAllState();
        });
      }

      var toggleBtn = card.querySelector('[data-server-toggle-btn]');
      if (toggleBtn) {
        toggleBtn.addEventListener('click', function (e) {
          e.stopPropagation();
          var currentlyOpen = card.classList.contains('server-card--expanded');
          updateCard(card, !currentlyOpen, true);
          checkAllState();
        });
      }
    });

    function checkAllState() {
      if (!toggleAllBtn) return;
      var allExpanded = true;
      cards.forEach(function (card) {
        if (!card.classList.contains('server-card--expanded')) allExpanded = false;
      });
      var collapseLabel = toggleAllBtn.getAttribute('data-collapse-label') || 'Collapse all';
      var expandLabel = toggleAllBtn.getAttribute('data-expand-label') || 'Expand all';
      if (toggleAllLabel) {
        toggleAllLabel.textContent = allExpanded ? collapseLabel : expandLabel;
      }
      toggleAllBtn.setAttribute('title', allExpanded ? collapseLabel : expandLabel);
      toggleAllBtn.setAttribute('aria-label', allExpanded ? collapseLabel : expandLabel);
    }

    if (toggleAllBtn && toggleAllBtn.dataset.toggleAllReady !== '1') {
      toggleAllBtn.dataset.toggleAllReady = '1';
      toggleAllBtn.addEventListener('click', function () {
        var anyClosed = false;
        cards.forEach(function (card) {
          if (!card.classList.contains('server-card--expanded')) anyClosed = true;
        });
        cards.forEach(function (card) {
          updateCard(card, anyClosed, true);
        });
        checkAllState();
      });
      checkAllState();
    }
  }

  /* ── Service worker registration (PWA) ──────────────────────
   * Registers the root-scoped worker that powers installability and the
   * offline fallback. Progressive enhancement: unsupported browsers simply
   * skip it. See docs/pwa.md.
   */
  function initServiceWorker() {
    if (!('serviceWorker' in navigator)) return;
    window.addEventListener('load', function () {
      navigator.serviceWorker.register('/sw.js', { scope: '/' }).then(function (reg) {
        if (reg && typeof reg.update === 'function') {
          reg.update().catch(function () {});
        }
      }).catch(function () {
        /* registration failure must never break the page */
      });
    });
  }

  /* ── Lock screen orientation to portrait (PWA) ──────────────
   * Defence-in-depth for the manifest `orientation: "portrait"` member: where
   * the platform supports the Screen Orientation API we also lock at runtime, so
   * installs/browsers that don't fully honour the manifest field still stay
   * upright. This is strictly best-effort — screen.orientation.lock() rejects (a
   * Promise) or throws on platforms where it is unavailable or not permitted
   * (iOS Safari has no lock(); Chromium only allows it for an installed/
   * fullscreen app), so every path is feature-detected and swallowed. A failure
   * here must never surface an error or break the page.
   */
  function initOrientationLock() {
    try {
      var so = window.screen && window.screen.orientation;
      if (!so || typeof so.lock !== 'function') return; // e.g. iOS Safari
      var p = so.lock('portrait');
      // Spec returns a Promise; older shims may return undefined or throw.
      if (p && typeof p.catch === 'function') {
        p.catch(function () { /* not allowed (not installed/fullscreen) — ignore */ });
      }
    } catch (err) {
      /* unsupported / not permitted — fail silently */
    }
  }

  /* ── Boot ───────────────────────────────────────────────── */
  function boot() {
    renderIcons();
    initForms();
    initLinks();
    initFlash();
    initSecrets();
    initCopyButtons();
    initCopyTargets();
    initNodeCredentials();
    initProgressBars();
    initReveal();
    initAutoRefresh();
    initStream();
    initHomeWarnings();
    initNodeResult();
    initBulkStream();
    initManualRefresh();
    initActionMenus();
    initModals();
    initDeployModal();
    initLogDrawer();
    initDrawer();
    initBackToTop();
    initShortcuts();
    initCollapsibles();
    initAdvancedToggle();
    initServerCards();
    initServiceWorker();
    initOrientationLock();
    renderIcons(); // pick up icons injected by the steps above
  }

  /* ── v0.6.0: tab-pane rescan bridge ──────────────────────
   * tab-manager.js populates a .tab-pane by DOMParser-extracting another
   * page's rendered markup rather than a real navigation, so none of the
   * init*() calls above ever ran against that subtree — it never passed
   * through boot(). rescan() re-invokes the idempotent-safe subset of them,
   * scoped to the given root, so a freshly-injected pane gets the same
   * flash-banner dismiss buttons, secret-reveal toggles, copy buttons, modal
   * wiring, collapsible sections and SSE stream hookups a full page load
   * would have given it. Icons are re-rendered too, since DOMParser-parsed
   * markup never passed through lucide in its original document.
   */
  window.NodexiaApp = {
    rescan: function (root) {
      root = root || document;
      renderIcons();
      initFlash(root);
      initSecrets(root);
      initCopyButtons(root);
      initCopyTargets(root);
      initModals(root);
      initDeployModal(root);
      initLogDrawer(root);
      initCollapsibles(root);
      initServerCards(root);
      initStream(root);
      initBulkStream(root);
      initNodeCredentials(root);
      initActionMenus(root);
      initManualRefresh(root);
      initAutoRefresh(root);
      initNodeResult(root);
      initAdvancedToggle(root);
      initProgressBars(root);
      initReveal(root);
      renderIcons();
    },
    finishNavigation: function () {
      finishTopBar();
      var overlay = document.getElementById('loading-overlay');
      if (overlay) overlay.style.display = 'none';
    }
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
