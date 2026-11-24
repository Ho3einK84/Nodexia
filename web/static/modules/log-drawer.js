/* Nodexia Live Log Drawer (lazy-loaded native ES module — no bundler, no npm).
 *
 * Extracted from shell.js so pages without a live log drawer never download or
 * parse it. Incoming SSE log lines are batched per animation frame via
 * DocumentFragment for smooth 60fps streaming under high log volume.
 */
import { I18N, el, renderIcons, prefersReducedMotion, registerCleanup } from './core.js';
import { copyText } from './ui.js';
import { syncBodyScrollLock } from './modals.js';

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
    statusEl.appendChild(el('i', { 'data-lucide': iconName, 'aria-hidden': 'true' }));
  } else {
    statusEl.appendChild(el('span', { class: 'live-dot', 'aria-hidden': 'true' }));
  }
  statusEl.appendChild(document.createTextNode(' '));
  statusEl.appendChild(el('span', { text: text }));
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
    var frameQueue = '';
    var frameRAF = 0;
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
      var frag = document.createDocumentFragment();

      function styleSpan(span, lineText, isPartial) {
        span.textContent = lineText;
        span.dataset.partial = isPartial ? '1' : '';
        var cls = classifyLogLine(lineText);
        span.className = 'log-line' + (cls ? ' ' + cls : '');
        if (q) {
          var hit = lineText.toLowerCase().indexOf(q) !== -1;
          span.hidden = !hit;
          span.classList.toggle('log-line--match', hit);
        }
      }

      var lastChild = outEl.lastElementChild;
      parts.forEach(function (line, idx) {
        if (idx === 0 && lastChild && lastChild.dataset.partial === '1') {
          styleSpan(lastChild, line, false);
          return;
        }
        var span = document.createElement('span');
        styleSpan(span, line, false);
        frag.appendChild(span);
      });

      if (outTrack.partial) {
        if (frag.childNodes.length === 0 && lastChild && lastChild.dataset.partial === '1') {
          styleSpan(lastChild, outTrack.partial, true);
        } else {
          var pSpan = document.createElement('span');
          styleSpan(pSpan, outTrack.partial, true);
          frag.appendChild(pSpan);
        }
      }

      if (frag.childNodes.length) outEl.appendChild(frag);
      while (outEl.childElementCount > MAX_DRAWER_LOG_LINES) {
        outEl.firstElementChild.remove();
      }
      if (q && searchCount) applySearchFilter();
      if (!autoScrollCb || autoScrollCb.checked) outEl.scrollTop = outEl.scrollHeight;
    }

    function enqueueStdout(chunk) {
      if (!chunk) return;
      frameQueue += chunk;
      if (frameRAF) return;
      frameRAF = requestAnimationFrame(function () {
        frameRAF = 0;
        var batch = frameQueue;
        frameQueue = '';
        if (batch) appendStdoutLines(batch);
      });
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
      if (frameRAF) { cancelAnimationFrame(frameRAF); frameRAF = 0; }
      var combined = frameQueue + pendingOut;
      frameQueue = '';
      pendingOut = '';
      if (combined) appendStdoutLines(combined);
      if (pendingErr) {
        appendStderr(pendingErr);
        pendingErr = '';
      }
    }

    function updatePauseButton() {
      if (!pauseBtn) return;
      pauseBtn.setAttribute('aria-pressed', isPaused ? 'true' : 'false');
      pauseBtn.textContent = '';
      pauseBtn.appendChild(el('i', { 'data-lucide': isPaused ? 'play' : 'pause', class: 'icon-in-button', 'aria-hidden': 'true' }));
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
      if (frameRAF) { cancelAnimationFrame(frameRAF); frameRAF = 0; }
      if (activeES) {
        try { activeES.close(); } catch (err) {}
        activeES = null;
      }
    }

    openFn = function (sseUrl, isRunning, label, doneCb) {
      lastFocus = document.activeElement;
      drawer.hidden = false;
      requestAnimationFrame(function () {
        drawer.classList.add('is-open');
        syncBodyScrollLock();
      });

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
      frameQueue = '';
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
        if (isPaused) { pendingOut += unseen; return; }
        enqueueStdout(unseen);
      });

      activeES.addEventListener('stderr', function (e) {
        var unseen = takeUnseen(errTrack, e.data || '');
        if (!unseen) return;
        if (isPaused) { pendingOut += unseen; return; }
        enqueueStdout(unseen);
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
        }, 220);
      }
      if (lastFocus && lastFocus.focus && document.contains(lastFocus)) {
        var pane = lastFocus.closest ? lastFocus.closest('.tab-pane') : null;
        if (!pane || !pane.hidden) {
          try { lastFocus.focus({ preventScroll: true }); } catch (err) { lastFocus.focus(); }
        }
      }
      lastFocus = null;
    }

    drawer.querySelectorAll('[data-log-drawer-close]').forEach(function (closeEl) {
      closeEl.addEventListener('click', function (e) {
        e.preventDefault();
        closeDrawer(false);
      });
    });

    if (searchInput) searchInput.addEventListener('input', applySearchFilter);

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
        frameQueue = '';
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
          copyBtn.appendChild(el('i', { 'data-lucide': ok ? 'check' : 'x', class: 'icon-in-button' }));
          copyBtn.appendChild(document.createTextNode(' ' + (ok ? I18N.t('nodes.copy_toast') : I18N.t('js.failed'))));
          renderIcons(copyBtn);
          setTimeout(function () {
            copyBtn.textContent = '';
            copyBtn.appendChild(el('i', { 'data-lucide': 'copy', class: 'icon-in-button' }));
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
        ).filter(function (node) { return !node.hidden && !node.closest('[hidden]'); });
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

export { initLogDrawer };
