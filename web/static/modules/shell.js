/* Nodexia shell layer (native ES module — no bundler, no npm).
 *
 * The app frame rather than page content: the home fleet warnings, the live log
 * slide-over, the mobile navigation drawer, collapsible server cards, and the
 * PWA plumbing (service-worker registration + portrait orientation lock).
 * Extracted from app.js; the shared primitives (i18n, icons, reduced-motion
 * probe) come from ./core.js and the clipboard helper from ./ui.js.
 *
 * Every init* function is progressive enhancement and idempotent: absent
 * targets no-op, and the ones that accept `root` can be re-run against a
 * freshly injected tab pane by window.NodexiaApp.rescan().
 */
import { I18N, renderIcons, prefersReducedMotion, finishTopBar, registerCleanup } from './core.js';
import { copyText } from './ui.js';
import { syncBodyScrollLock } from './modals.js';

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

function initHomeWarnings(root) {
  root = root || document;
  var wraps = root.querySelectorAll ? root.querySelectorAll('[data-home-warnings]') : [];
  if (!wraps.length) return;
  var dismissed = readWarningDismissals();

  wraps.forEach(function (wrap) {
    wrap.querySelectorAll('[data-warning-id]').forEach(function (banner) {
      if (banner.dataset.warnReady === '1') return;
      banner.dataset.warnReady = '1';
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
  });
  renderIcons(root);
}

/* ── Live Log Drawer (Slide-over) ─────────────────────────── */
var MAX_DRAWER_LOG_LINES = 2000;

function classifyLogLine(text) {
  var low = text.toLowerCase();
  if (/^\s*(\$|#|>)\s/.test(text)) return 'log-line--cmd';
  if (/(^|[\s:\[])(err|error|fatal|panic|fail|failed|exception|traceback|exit status [1-9])/i.test(low)) return 'log-line--err';
  if (/(^|[\s:\[])(warn|warning|retry|retrying|deprecated)/i.test(low)) return 'log-line--warn';
  if (/(^|[\s:\[])(ok|ready|started|active \(running\)|healthy|success|done|complete)/i.test(low)) return 'log-line--ok';
  return '';
}

function setStatusBadge(statusEl, stateClass, iconName, text) {
  if (!statusEl) return;
  var isWrap = statusEl.classList.contains('log-drawer__status-wrap');
  statusEl.className = (isWrap ? 'log-drawer__status-wrap' : 'log-drawer__foot') + (stateClass ? ' ' + stateClass : '');
  statusEl.textContent = '';
  if (iconName) {
    var ic = document.createElement('i');
    ic.setAttribute('data-lucide', iconName);
    ic.setAttribute('aria-hidden', 'true');
    statusEl.appendChild(ic);
    statusEl.appendChild(document.createTextNode(' '));
  } else {
    var dot = document.createElement('span');
    dot.className = 'live-dot';
    dot.setAttribute('aria-hidden', 'true');
    statusEl.appendChild(dot);
    statusEl.appendChild(document.createTextNode(' '));
  }
  var span = document.createElement('span');
  span.textContent = text;
  statusEl.appendChild(span);
  renderIcons(statusEl);
}

function wireLogDrawerInstance(drawer, scope) {
  var openFn = drawer._openLogDrawer;

  if (drawer.dataset.logDrawerReady !== '1') {
    drawer.dataset.logDrawerReady = '1';

    var titleEl = drawer.querySelector('[data-log-title-text]');
    var defaultTitle = titleEl ? titleEl.textContent : (I18N.t('nodes.drawer_title_default') || 'Live Node Output');
    var cmdEl = drawer.querySelector('[data-log-command]');
    var outEl = drawer.querySelector('[data-log-stdout]');
    var errEl = drawer.querySelector('[data-log-stderr]');
    var reconnectEl = drawer.querySelector('[data-log-reconnect]');
    var statusEl = drawer.querySelector('[data-log-status]');
    var autoScrollCb = drawer.querySelector('[data-log-autoscroll]');
    var pauseBtn = drawer.querySelector('[data-log-pause]');
    var clearBtn = drawer.querySelector('[data-log-clear]');
    var copyBtn = drawer.querySelector('[data-log-copy]');
    var searchInput = drawer.querySelector('[data-log-search]');
    var searchCount = drawer.querySelector('[data-log-search-count]');
    var panel = drawer.querySelector('.log-drawer__panel') || drawer;

    var activeES = null;
    var lastFocus = null;
    var finished = false;
    var isPaused = false;
    var pendingOut = '';
    var pendingErr = '';
    var rawOut = '';
    var rawErr = '';
    var outTrack = { received: 0, connOffset: 0, partial: '' };
    var errTrack = { received: 0, connOffset: 0 };
    var onDoneCb = null;

    function applySearchFilter() {
      if (!outEl) return;
      var q = searchInput ? searchInput.value.trim().toLowerCase() : '';
      var lines = outEl.querySelectorAll('.log-line');
      var matches = 0;
      lines.forEach(function (line) {
        if (!q) {
          line.hidden = false;
          line.classList.remove('log-line--match');
          return;
        }
        var hit = line.textContent.toLowerCase().indexOf(q) !== -1;
        line.hidden = !hit;
        line.classList.toggle('log-line--match', hit);
        if (hit) matches++;
      });
      if (searchCount) {
        if (!q) {
          searchCount.textContent = '';
          searchCount.hidden = true;
        } else {
          searchCount.hidden = false;
          searchCount.textContent = I18N.t('nodes.drawer_matches', { count: matches });
        }
      }
    }

    function appendStdoutLines(text) {
      if (!outEl || !text) return;
      rawOut += text;
      if (rawOut.length > 512 * 1024) rawOut = rawOut.slice(rawOut.length - 512 * 1024);

      var combined = outTrack.partial + text;
      var parts = combined.split('\n');
      outTrack.partial = parts.pop() || '';
      var q = searchInput ? searchInput.value.trim().toLowerCase() : '';

      var appendOne = function (lineText, replaceLastPartial) {
        var existingPartial = replaceLastPartial ? outEl.lastElementChild : null;
        var span;
        if (existingPartial && existingPartial.dataset.partial === '1') {
          span = existingPartial;
          span.textContent = lineText;
        } else {
          span = document.createElement('span');
          span.textContent = lineText;
          outEl.appendChild(span);
        }
        span.dataset.partial = replaceLastPartial ? '1' : '';
        var cls = classifyLogLine(lineText);
        span.className = 'log-line' + (cls ? ' ' + cls : '');
        if (q) {
          var hit = lineText.toLowerCase().indexOf(q) !== -1;
          span.hidden = !hit;
          span.classList.toggle('log-line--match', hit);
        }
      };

      var lastChild = outEl.lastElementChild;
      parts.forEach(function (line, idx) {
        if (idx === 0 && lastChild && lastChild.dataset.partial === '1') {
          lastChild.textContent = line;
          lastChild.dataset.partial = '';
          var cls0 = classifyLogLine(line);
          lastChild.className = 'log-line' + (cls0 ? ' ' + cls0 : '');
          if (q) {
            var hit0 = line.toLowerCase().indexOf(q) !== -1;
            lastChild.hidden = !hit0;
            lastChild.classList.toggle('log-line--match', hit0);
          }
          return;
        }
        appendOne(line, false);
      });
      if (outTrack.partial) {
        appendOne(outTrack.partial, true);
      }

      while (outEl.childElementCount > MAX_DRAWER_LOG_LINES) {
        outEl.firstElementChild.remove();
      }
      if (q && searchCount) applySearchFilter();
      if (!autoScrollCb || autoScrollCb.checked) outEl.scrollTop = outEl.scrollHeight;
    }

    function appendStderr(text) {
      if (!errEl || !text) return;
      rawErr += text;
      if (rawErr.length > 256 * 1024) rawErr = rawErr.slice(rawErr.length - 256 * 1024);
      errEl.hidden = false;
      errEl.textContent += text;
      if (errEl.textContent.length > 256 * 1024) {
        errEl.textContent = errEl.textContent.slice(errEl.textContent.length - 256 * 1024);
      }
      if (!autoScrollCb || autoScrollCb.checked) errEl.scrollTop = errEl.scrollHeight;
    }

    function flushPending() {
      if (pendingOut) {
        appendStdoutLines(pendingOut);
        pendingOut = '';
      }
      if (pendingErr) {
        appendStderr(pendingErr);
        pendingErr = '';
      }
    }

    function updatePauseButton() {
      if (!pauseBtn) return;
      pauseBtn.setAttribute('aria-pressed', isPaused ? 'true' : 'false');
      pauseBtn.textContent = '';
      var ic = document.createElement('i');
      ic.setAttribute('data-lucide', isPaused ? 'play' : 'pause');
      ic.className = 'icon-in-button';
      ic.setAttribute('aria-hidden', 'true');
      pauseBtn.appendChild(ic);
      pauseBtn.appendChild(document.createTextNode(' ' + (isPaused ? I18N.t('nodes.drawer_resume') : I18N.t('nodes.drawer_pause'))));
      renderIcons(pauseBtn);
    }

    function takeUnseen(track, chunk) {
      if (!chunk) return '';
      var start = track.connOffset;
      track.connOffset += chunk.length;
      if (track.connOffset <= track.received) return '';
      var unseen = start < track.received ? chunk.slice(track.received - start) : chunk;
      track.received += unseen.length;
      return unseen;
    }

    function stopStream() {
      if (activeES) {
        try { activeES.close(); } catch (err) {}
        activeES = null;
      }
    }

    openFn = function (sseUrl, isRunning, label, doneCb) {
      lastFocus = document.activeElement;
      drawer.hidden = false;
      void drawer.offsetWidth;
      drawer.classList.add('is-open');
      syncBodyScrollLock();

      if (titleEl) titleEl.textContent = label || defaultTitle;
      onDoneCb = typeof doneCb === 'function' ? doneCb : null;
      isPaused = false;
      updatePauseButton();
      if (reconnectEl) reconnectEl.hidden = true;
      if (searchInput) searchInput.value = '';
      if (searchCount) { searchCount.textContent = ''; searchCount.hidden = true; }
      if (outEl) outEl.textContent = '';
      if (errEl) { errEl.textContent = ''; errEl.hidden = true; }
      var cmdWrap = drawer.querySelector('[data-log-cmd-wrap]') || (cmdEl && cmdEl.parentElement);
      if (cmdWrap) {
        var isRealCommand = label && (label.indexOf(' ') !== -1 && (label.startsWith('/') || label.startsWith('sh') || label.startsWith('sudo') || label.startsWith('docker') || label.startsWith('node') || label.startsWith('git')));
        cmdWrap.hidden = !isRealCommand;
      }
      if (cmdEl) cmdEl.textContent = label || '—';

      rawOut = '';
      rawErr = '';
      pendingOut = '';
      pendingErr = '';
      outTrack = { received: 0, connOffset: 0, partial: '' };
      errTrack = { received: 0, connOffset: 0 };

      var closeBtn = drawer.querySelector('[data-log-drawer-close]');
      if (closeBtn && closeBtn.focus) {
        setTimeout(function () {
          try { closeBtn.focus({ preventScroll: true }); } catch (err) { closeBtn.focus(); }
        }, 20);
      }

      stopStream();
      if (!sseUrl) return;
      finished = false;
      setStatusBadge(statusEl, 'is-live', '', I18N.t('nodes.runner_conn_wait'));

      activeES = new EventSource(sseUrl);

      activeES.addEventListener('open', function () {
        if (reconnectEl) reconnectEl.hidden = true;
        outTrack.connOffset = 0;
        errTrack.connOffset = 0;
        setStatusBadge(statusEl, 'is-live', '', I18N.t('nodes.action_running'));
      });

      activeES.addEventListener('output', function (e) {
        var unseen = takeUnseen(outTrack, e.data || '');
        if (!unseen) return;
        if (isPaused) {
          pendingOut += unseen;
          return;
        }
        appendStdoutLines(unseen);
      });

      activeES.addEventListener('stderr', function (e) {
        var unseen = takeUnseen(errTrack, e.data || '');
        if (!unseen) return;
        if (isPaused) {
          pendingOut += unseen;
          return;
        }
        appendStdoutLines(unseen);
      });

      activeES.addEventListener('done', function (e) {
        finished = true;
        stopStream();
        if (reconnectEl) reconnectEl.hidden = true;
        if (isPaused) {
          isPaused = false;
          updatePauseButton();
        }
        flushPending();
        var payload = {};
        try { payload = JSON.parse(e.data || '{}'); } catch (err) {}
        var exitCode = payload.exitCode !== undefined ? String(payload.exitCode) : '0';
        var failed = exitCode !== '0' || payload.status === 'failed';
        var labelText = failed
          ? (I18N.t('nodes.drawer_failed') + (payload.duration ? ' · ' + payload.duration : ''))
          : (I18N.t('nodes.drawer_ready') + (payload.duration ? ' · ' + payload.duration : ''));
        setStatusBadge(statusEl, failed ? 'is-error' : 'is-ready', failed ? 'alert-circle' : 'check-circle', labelText);
        if (onDoneCb) {
          var cb = onDoneCb;
          onDoneCb = null;
          cb(failed, payload);
        }
      });

      activeES.addEventListener('error', function (e) {
        if (e && e.data) {
          finished = true;
          stopStream();
          if (reconnectEl) reconnectEl.hidden = true;
          flushPending();
          var payload = {};
          try { payload = JSON.parse(e.data); } catch (err) {}
          var msg = payload.message || I18N.t('nodes.drawer_failed');
          if (errEl && payload.message) appendStderr(payload.message + '\n');
          setStatusBadge(statusEl, 'is-error', 'alert-circle', msg);
          if (onDoneCb) {
            var cb = onDoneCb;
            onDoneCb = null;
            cb(true, payload);
          }
          return;
        }
        if (finished) return;
        if (activeES && activeES.readyState === EventSource.CLOSED) {
          stopStream();
          if (reconnectEl) reconnectEl.hidden = true;
          setStatusBadge(statusEl, 'is-error', 'wifi-off', I18N.t('js.bulk.stream_error'));
          return;
        }
        if (reconnectEl) reconnectEl.hidden = false;
      });
    };

    drawer._openLogDrawer = openFn;

    function closeDrawer(immediate) {
      if (!drawer.classList.contains('is-open')) return;
      drawer.classList.remove('is-open');
      stopStream();
      syncBodyScrollLock();
      if (prefersReducedMotion || immediate) {
        drawer.hidden = true;
      } else {
        setTimeout(function () {
          if (!drawer.classList.contains('is-open')) drawer.hidden = true;
        }, 240);
      }
      if (lastFocus && lastFocus.focus && document.contains(lastFocus)) {
        var pane = lastFocus.closest ? lastFocus.closest('.tab-pane') : null;
        if (!pane || !pane.hidden) {
          try { lastFocus.focus({ preventScroll: true }); } catch (err) { lastFocus.focus(); }
        }
      }
      lastFocus = null;
    }

    drawer.querySelectorAll('[data-log-drawer-close]').forEach(function (el) {
      el.addEventListener('click', function (e) {
        e.preventDefault();
        closeDrawer(false);
      });
    });

    if (searchInput) {
      searchInput.addEventListener('input', applySearchFilter);
    }

    if (pauseBtn) {
      pauseBtn.addEventListener('click', function () {
        isPaused = !isPaused;
        updatePauseButton();
        if (!isPaused) flushPending();
      });
    }

    if (clearBtn) {
      clearBtn.addEventListener('click', function () {
        rawOut = '';
        rawErr = '';
        pendingOut = '';
        pendingErr = '';
        outTrack.partial = '';
        if (outEl) outEl.textContent = '';
        if (errEl) { errEl.textContent = ''; errEl.hidden = true; }
        if (searchCount) { searchCount.textContent = ''; searchCount.hidden = true; }
      });
    }

    if (copyBtn) {
      var origLabel = I18N.t('nodes.drawer_copy') || I18N.t('nodes.runner_copy_log');
      copyBtn.addEventListener('click', function () {
        var text = rawOut + (rawErr ? '\n' + rawErr : '');
        copyText(text).then(function (ok) {
          copyBtn.textContent = '';
          var ic = document.createElement('i');
          ic.setAttribute('data-lucide', ok ? 'check' : 'x');
          ic.className = 'icon-in-button';
          copyBtn.appendChild(ic);
          copyBtn.appendChild(document.createTextNode(' ' + (ok ? I18N.t('nodes.copy_toast') : I18N.t('js.failed'))));
          renderIcons(copyBtn);
          setTimeout(function () {
            copyBtn.textContent = '';
            var origIc = document.createElement('i');
            origIc.setAttribute('data-lucide', 'copy');
            origIc.className = 'icon-in-button';
            copyBtn.appendChild(origIc);
            copyBtn.appendChild(document.createTextNode(' ' + origLabel));
            renderIcons(copyBtn);
          }, 1500);
        });
      });
    }

    drawer.addEventListener('keydown', function (e) {
      if (!drawer.classList.contains('is-open')) return;
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        closeDrawer(false);
        return;
      }
      if (e.key === 'Tab') {
        var focusables = Array.prototype.slice.call(
          panel.querySelectorAll('a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])')
        ).filter(function (el) { return !el.hidden && !el.closest('[hidden]'); });
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
      }
    });

    var owningPane = drawer.closest('.tab-pane') || scope || document;
    var onDeactivate = function (e) {
      if (owningPane !== document && e && e.detail && e.detail.pane === owningPane) {
        closeDrawer(true);
      }
    };
    if (owningPane !== document) {
      document.addEventListener('tab-deactivated', onDeactivate);
    }

    registerCleanup(owningPane, function () {
      stopStream();
      if (owningPane !== document) {
        document.removeEventListener('tab-deactivated', onDeactivate);
      }
      closeDrawer(true);
    });
  }

  (scope || document).querySelectorAll('[data-log-drawer-open]').forEach(function (btn) {
    if (btn.dataset.logDrawerOpenReady === '1') return;
    btn.dataset.logDrawerOpenReady = '1';
    btn.addEventListener('click', function (e) {
      e.preventDefault();
      var sseUrl = btn.getAttribute('data-log-sse-url');
      var isRunning = btn.getAttribute('data-log-running') === '1';
      var title = btn.getAttribute('data-log-title') || '';
      if (openFn) openFn(sseUrl, isRunning, title);
    });
  });
}

function initLogDrawer(root) {
  root = root || document;
  var drawers = root.querySelectorAll('[data-log-drawer]');
  if (!drawers.length) return;
  drawers.forEach(function (drawer) {
    var scope = (root === document) ? (drawer.closest('.tab-pane') || document) : root;
    wireLogDrawerInstance(drawer, scope);
  });
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
    drawer.style.transition = '';
    drawer.style.transform = '';
    setExpanded(false);
    if (lastFocus && lastFocus.focus && document.contains(lastFocus)) {
      lastFocus.focus();
    }
    lastFocus = null;
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
    if (window.innerWidth >= 768) close();
  });

  // Swipe-to-close: dragging the panel toward its off-canvas edge follows
  // the finger and releases past a threshold. The closing direction flips
  // with text direction (the drawer parks on the inline-end side).
  function isRTL() {
    return document.documentElement.getAttribute('dir') === 'rtl';
  }
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
    var rtl = isRTL();
    var dx = e.touches[0].clientX - touchStartX;
    var dy = e.touches[0].clientY - touchStartY;
    // Vertical intent = the user is scrolling the drawer, not dismissing it.
    if (Math.abs(dy) > Math.abs(dx) && closingDx === 0) { dragging = false; return; }
    var candidate = rtl ? -dx : dx;
    closingDx = candidate > 0 ? candidate : 0;
    drawer.style.transition = 'none';
    drawer.style.transform = closingDx > 0
      ? 'translateX(' + (rtl ? -closingDx : closingDx) + 'px)'
      : 'translateX(0)';
  }, { passive: true });

  function endTouch() {
    if (!dragging) return;
    dragging = false;
    drawer.style.transition = '';
    drawer.style.transform = '';
    if (closingDx > 72) close();
    closingDx = 0;
  }
  drawer.addEventListener('touchend', endTouch);
  drawer.addEventListener('touchcancel', endTouch);
}

/* ── Collapsible server cards & expand all ─────────────── */
function initServerCards(root) {
  root = root || document;
  var cards = root.querySelectorAll('[data-server-card]');
  if (!cards.length) return;

  var toggleAllBtn = root.querySelector('#server-toggle-all');
  var toggleAllLabel = root.querySelector('#server-toggle-all-label');

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

  function positionActionMenu(menu, toggle) {
    var panel = menu.querySelector('.action-menu__panel');
    if (!panel || !toggle) return;
    var panelH = panel.offsetHeight;
    if (!panelH) {
      panel.style.visibility = 'hidden';
      panel.style.display = 'flex';
      panelH = panel.offsetHeight || 260;
      panel.style.display = '';
      panel.style.visibility = '';
    }
    var rect = toggle.getBoundingClientRect();
    var pane = cardScrollBounds(menu);
    var spaceAbove = rect.top - pane.top;
    var spaceBelow = pane.bottom - rect.bottom;
    // The kebab lives in the card header, so prefer dropping down over the
    // card body; flip upward only when the viewport bottom would clip it.
    var openBelow = spaceBelow >= (panelH + 12) || spaceBelow >= spaceAbove;
    menu.classList.toggle('action-menu--below', openBelow);
  }

  function cardScrollBounds(el) {
    var pane = el.closest && el.closest('.tab-pane');
    if (pane) {
      var pr = pane.getBoundingClientRect();
      return {
        top: Math.max(pr.top, 0),
        bottom: Math.min(pr.bottom, window.innerHeight)
      };
    }
    return { top: 0, bottom: window.innerHeight };
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

    var checkboxWrap = card.querySelector('.server-card__checkbox-wrap');
    if (checkboxWrap) {
      checkboxWrap.addEventListener('click', function (e) {
        e.stopPropagation();
      });
    }

    var menu = card.querySelector('[data-action-menu]');
    var menuToggle = menu && menu.querySelector('.action-menu__toggle');
    if (menu && menuToggle) {
      menuToggle.addEventListener('click', function () {
        if (!menu.classList.contains('is-open')) {
          positionActionMenu(menu, menuToggle);
        }
      }, true);
    }

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

/* ── Desktop Collapsible Architectural Sidebar ────────────── */
function initDesktopSidebar() {
  var shell = document.getElementById('shell');
  var sidebar = document.getElementById('shell-sidebar');
  var toggle = document.querySelector('[data-sidebar-toggle]');
  if (!shell || !sidebar || !toggle) return;
  if (toggle.dataset.sidebarToggleReady === '1') return;
  toggle.dataset.sidebarToggleReady = '1';

  var STORAGE_KEY = 'nodexia.sidebar.collapsed';
  var DESKTOP_MIN_PX = 768;
  var resizeTimer = null;

  var tooltip = document.createElement('div');
  tooltip.className = 'shell-rail-tooltip';
  tooltip.setAttribute('role', 'tooltip');
  tooltip.hidden = true;
  document.body.appendChild(tooltip);
  var activeTipTarget = null;

  function hideTooltip() {
    activeTipTarget = null;
    tooltip.classList.remove('is-visible');
    tooltip.hidden = true;
  }

  function showTooltip(target) {
    if (window.innerWidth < DESKTOP_MIN_PX || !shell.classList.contains('is-sidebar-collapsed')) {
      hideTooltip();
      return;
    }
    var text = target && target.getAttribute('data-rail-tooltip');
    if (!text) {
      hideTooltip();
      return;
    }
    activeTipTarget = target;
    tooltip.textContent = text;
    tooltip.hidden = false;
    var rect = target.getBoundingClientRect();
    var sidebarRect = sidebar.getBoundingClientRect();
    var rtl = document.documentElement.getAttribute('dir') === 'rtl';
    tooltip.style.top = Math.round(rect.top + rect.height / 2) + 'px';
    if (rtl) {
      tooltip.style.left = 'auto';
      tooltip.style.right = Math.round(window.innerWidth - sidebarRect.left + 10) + 'px';
    } else {
      tooltip.style.right = 'auto';
      tooltip.style.left = Math.round(sidebarRect.right + 10) + 'px';
    }
    void tooltip.offsetWidth;
    tooltip.classList.add('is-visible');
  }

  function readStored() {
    try { return localStorage.getItem(STORAGE_KEY) === '1'; } catch (err) { return false; }
  }

  function applyState(collapsed, persist, dispatchResize) {
    var effective = !!collapsed && window.innerWidth >= DESKTOP_MIN_PX;
    shell.classList.toggle('is-sidebar-collapsed', effective);
    toggle.setAttribute('aria-expanded', effective ? 'false' : 'true');
    var label = effective
      ? (I18N.t('shell.sidebar.expand') || 'Expand sidebar')
      : (I18N.t('shell.sidebar.collapse') || 'Collapse sidebar');
    if (label === 'shell.sidebar.expand') label = 'Expand sidebar';
    if (label === 'shell.sidebar.collapse') label = 'Collapse sidebar';
    toggle.setAttribute('aria-label', label);
    toggle.setAttribute('title', label + ' ([)');
    if (!effective) hideTooltip();
    if (persist) {
      try { localStorage.setItem(STORAGE_KEY, collapsed ? '1' : '0'); } catch (err) {}
    }
    if (dispatchResize) {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(function () {
        window.dispatchEvent(new Event('resize'));
      }, prefersReducedMotion ? 10 : 230);
    }
  }

  applyState(readStored(), false, false);
  requestAnimationFrame(function () {
    requestAnimationFrame(function () {
      shell.classList.add('is-animated');
    });
  });

  toggle.addEventListener('click', function () {
    applyState(!shell.classList.contains('is-sidebar-collapsed'), true, true);
  });

  sidebar.addEventListener('mouseover', function (e) {
    var target = e.target && e.target.closest ? e.target.closest('[data-rail-tooltip]') : null;
    if (!target || !sidebar.contains(target)) return;
    if (target !== activeTipTarget) showTooltip(target);
  });
  sidebar.addEventListener('mouseout', function (e) {
    if (!activeTipTarget) return;
    var related = e.relatedTarget;
    if (!related || !activeTipTarget.contains(related)) hideTooltip();
  });
  sidebar.addEventListener('focusin', function (e) {
    var target = e.target && e.target.closest ? e.target.closest('[data-rail-tooltip]') : null;
    if (target) showTooltip(target);
  });
  sidebar.addEventListener('focusout', function () { hideTooltip(); });
  sidebar.addEventListener('click', function () { hideTooltip(); });

  var bodyEl = sidebar.querySelector('.shell-sidebar__body');
  if (bodyEl) bodyEl.addEventListener('scroll', hideTooltip, { passive: true });

  window.addEventListener('resize', function () {
    hideTooltip();
    applyState(readStored(), false, false);
  });

  document.addEventListener('keydown', function (e) {
    if (window.innerWidth < DESKTOP_MIN_PX) return;
    if (e.metaKey || e.ctrlKey || e.altKey || e.key !== '[') return;
    var el = document.activeElement;
    if (el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT' || el.isContentEditable)) return;
    if (el && el.closest && el.closest('.xterm, [data-modal].is-open')) return;
    if (document.body.classList.contains('drawer-open') || document.body.classList.contains('tab-switcher-open')) return;
    e.preventDefault();
    applyState(!shell.classList.contains('is-sidebar-collapsed'), true, true);
  });
}

export { initHomeWarnings, initLogDrawer, initDrawer, initServerCards, initServiceWorker, initOrientationLock, initDesktopSidebar };
