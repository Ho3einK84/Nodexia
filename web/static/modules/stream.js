/* Nodexia live streams, auto-refresh & result cards (lazy-loaded ES module).
 *
 * Loaded on demand by app.js only when a page or tab pane contains auto-refresh
 * controls, SSE stream cards, node result cards, bulk progress cards, or manual
 * refresh buttons.
 */
import {
  I18N,
  escapeHTML,
  prefersReducedMotion,
  renderIcons,
  startTopBar,
  tabNavigate,
  tabReload,
  registerCleanup,
  markButtonBusy
} from './core.js';
import { initCopyButtons } from './ui.js';

function navigateOwningPane(pane, targetURL) {
  if (!pane) {
    startTopBar();
    window.location.href = targetURL;
    return;
  }
  if (!pane.hidden) startTopBar();
  if (window.NodexiaTabs && typeof window.NodexiaTabs.navigatePane === 'function') {
    window.NodexiaTabs.navigatePane(pane, targetURL);
    return;
  }
  tabNavigate(targetURL);
}

/* ── Auto-refresh with live countdown ─────────────────────────────────────── */
function initAutoRefresh(root) {
  root = root || document;
  root.querySelectorAll('#auto-refresh-select, [data-auto-refresh]').forEach(function (select) {
    wireAutoRefreshSelect(select, root);
  });
}

function wireAutoRefreshSelect(select, root) {
  if (select.dataset.autoRefreshReady === '1') return;
  select.dataset.autoRefreshReady = '1';
  var url = select.getAttribute('data-refresh-url');
  var refreshMethod = (select.getAttribute('data-refresh-method') || 'get').toLowerCase();
  if (!url && refreshMethod !== 'post') return;
  var refreshURL = url ? (url + (url.indexOf('?') === -1 ? '?' : '&') + 'refresh=1') : '';
  var key = 'nodexia_auto_refresh_v2_' + (select.getAttribute('data-refresh-key') || url);
  var owningPane = select.closest('.tab-pane');

  var pill = document.createElement('span');
  pill.className = 'refresh-countdown';
  pill.hidden = true;
  var iconEl = document.createElement('i');
  iconEl.setAttribute('data-lucide', 'refresh-cw');
  var textSpan = document.createElement('span');
  textSpan.className = 'refresh-countdown__text';
  pill.appendChild(iconEl);
  pill.appendChild(document.createTextNode(' '));
  pill.appendChild(textSpan);
  select.parentNode.appendChild(pill);
  renderIcons(pill);

  var tickTimer = null;
  var deadline = 0;
  var lastRemaining = -1;

  function triggerRefresh() {
    if (owningPane && owningPane.hidden) return;
    if (refreshMethod === 'post') {
      var targetScope = select.closest('[data-nodes-workspace]') || owningPane || root || document;
      var dispatched = targetScope.dispatchEvent(new CustomEvent('nodes:autorefresh', { bubbles: false, cancelable: true }));
      if (dispatched) {
        var btn = targetScope.querySelector('[data-nodes-refresh-btn]');
        if (btn) btn.click();
      }
      var restartMs = parseInt(select.value, 10);
      if (restartMs > 0) start(restartMs);
      return;
    }
    navigateOwningPane(owningPane, refreshURL);
  }

  function stop() {
    if (tickTimer) { clearInterval(tickTimer); tickTimer = null; }
    lastRemaining = -1;
    pill.hidden = true;
  }

  function updatePill() {
    var remaining = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
    if (remaining === lastRemaining) return;
    lastRemaining = remaining;
    textSpan.textContent = I18N.t('js.refresh.next_in', { seconds: remaining });
  }

  function start(ms) {
    stop();
    if (owningPane && owningPane.hidden) return;
    deadline = Date.now() + ms;
    pill.hidden = false;
    updatePill();
    tickTimer = setInterval(function () {
      if (owningPane && owningPane.hidden) { stop(); return; }
      if (Date.now() >= deadline) { stop(); triggerRefresh(); return; }
      updatePill();
    }, 250);
  }

  function apply() {
    var ms = parseInt(select.value, 10);
    try { localStorage.setItem(key, ms > 0 ? select.value : '0'); } catch (e) {}
    if (ms > 0) start(ms);
    else stop();
  }

  function onActivated(e) {
    if (owningPane && e && e.detail && e.detail.pane === owningPane) apply();
  }
  function onDeactivated(e) {
    if (owningPane && e && e.detail && e.detail.pane === owningPane) stop();
  }

  var saved = null;
  try { saved = localStorage.getItem(key); } catch (e) {}
  if (saved !== null) {
    select.value = saved;
  } else {
    var selectedOpt = select.querySelector('option[selected]');
    select.value = selectedOpt ? selectedOpt.value : '0';
  }
  select.addEventListener('change', apply);
  if (owningPane) {
    document.addEventListener('tab-activated', onActivated);
    document.addEventListener('tab-deactivated', onDeactivated);
  }
  apply();
  registerCleanup(owningPane || root, function () {
    stop();
    if (owningPane) {
      document.removeEventListener('tab-activated', onActivated);
      document.removeEventListener('tab-deactivated', onDeactivated);
    }
  });
}

/* ── Live output stream (SSE) ─────────────────────────────────────────────── */
function createStreamTrack(el) {
  var track = { el: el, autoScroll: true, received: el ? el.textContent.length : 0, connOffset: 0 };
  if (el) {
    el.addEventListener('scroll', function () {
      track.autoScroll = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
    }, { passive: true });
  }
  return track;
}

function appendStreamChunk(track, chunk) {
  if (!track.el || !chunk) return;
  var start = track.connOffset;
  track.connOffset += chunk.length;
  if (track.connOffset <= track.received) return;
  var unseen = start < track.received ? chunk.slice(track.received - start) : chunk;
  if (!unseen) return;
  if (track.el.hidden) track.el.hidden = false;
  track.el.textContent += unseen;
  track.received += unseen.length;
  if (track.autoScroll) track.el.scrollTop = track.el.scrollHeight;
}

function setReconnectingState(card, active) {
  if (!card) return;
  card.classList.toggle('is-reconnecting', active);
  var existing = card.querySelector('[data-stream-reconnect]');
  if (active) {
    var statusLine = card.querySelector('[data-stream-status]');
    if (statusLine && !existing) {
      var note = document.createElement('span');
      note.className = 'stream-reconnect-note';
      note.setAttribute('data-stream-reconnect', '');
      note.textContent = ' — ' + I18N.t('js.live.reconnecting');
      statusLine.appendChild(note);
    }
  } else if (existing) {
    existing.remove();
  }
}

function initStream(root) {
  root = root || document;
  var card = root.querySelector('[data-stream-sse-url]');
  if (!card || card.dataset.streamReady === '1') return;
  card.dataset.streamReady = '1';
  var url = card.getAttribute('data-stream-sse-url');
  if (!url) return;

  var stdout = card.querySelector('[data-stream-stdout]');
  var stderr = card.querySelector('[data-stream-stderr]');
  var reloadOnDone = card.hasAttribute('data-stream-reload');

  if (typeof EventSource === 'undefined') { streamFallback(card); return; }

  var outTrack = createStreamTrack(stdout);
  var errTrack = createStreamTrack(stderr);
  var finished = false;
  card.classList.add('is-connecting');
  var es = new EventSource(url);

  registerCleanup(card.closest('.tab-pane') || root, function () {
    finished = true;
    try { es.close(); } catch (err) {}
  });

  es.addEventListener('open', function () {
    card.classList.remove('is-connecting');
    setReconnectingState(card, false);
    outTrack.connOffset = 0;
    errTrack.connOffset = 0;
  });

  es.addEventListener('output', function (e) { appendStreamChunk(outTrack, e.data); });
  es.addEventListener('stderr', function (e) { appendStreamChunk(errTrack, e.data); });

  es.addEventListener('done', function (e) {
    finished = true;
    es.close();
    if (reloadOnDone) { reloadForResult(); return; }
    finishStreamCard(card, parseJSON(e.data), false);
  });

  es.addEventListener('error', function (e) {
    if (e && e.data) {
      finished = true;
      es.close();
      if (reloadOnDone) { reloadForResult(); return; }
      finishStreamCard(card, parseJSON(e.data), true);
      return;
    }
    card.classList.remove('is-connecting');
    if (finished) return;
    if (es.readyState === EventSource.CLOSED) {
      setReconnectingState(card, false);
      streamFallback(card);
    } else {
      setReconnectingState(card, true);
    }
  });
}

function finishStreamCard(card, payload, isError) {
  payload = payload || {};
  card.classList.remove('is-connecting', 'cmd-result-card--live');
  setReconnectingState(card, false);
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

  var doneStdout = card.querySelector('[data-stream-stdout]');
  var doneStderr = card.querySelector('[data-stream-stderr]');
  if (doneStdout) doneStdout.hidden = !doneStdout.textContent;
  if (doneStderr) doneStderr.hidden = !doneStderr.textContent;
  var emptyNote = card.querySelector('[data-stream-empty]');
  if (emptyNote) {
    emptyNote.hidden = Boolean((doneStdout && doneStdout.textContent) || (doneStderr && doneStderr.textContent));
  }

  if (card.hasAttribute('data-node-result')) {
    if (doneStderr && !failed) doneStderr.classList.remove('output-block--error');
    scheduleNodeResultDismiss(card, failed);
  }

  initCopyButtons(card);
  renderIcons(card);
}

function streamFallback(card) {
  card.classList.remove('is-connecting');
  setReconnectingState(card, false);
  var status = card.querySelector('[data-stream-status]');
  var url = card.getAttribute('data-stream-page-url') || window.location.href;
  if (status) {
    status.innerHTML = '<a class="btn btn--ghost btn--sm" href="' + escapeHTML(url) +
      '"><i data-lucide="refresh-cw" class="icon-in-button"></i> ' + escapeHTML(I18N.t('js.stream.refresh')) + '</a>';
    renderIcons(status);
  }
}

/* ── Node action result card: auto-dismiss / manual close ─────────────────── */
var NODE_RESULT_COUNTDOWN_SECS = 5;

function initNodeResult(root) {
  root = root || document;
  var card = root.querySelector('[data-node-result]');
  if (!card || card.hasAttribute('data-stream-sse-url')) return;
  scheduleNodeResultDismiss(card, card.getAttribute('data-node-failed') === 'true');
}

function scheduleNodeResultDismiss(card, failed) {
  if (card.dataset.nodeDismissReady === '1') return;
  card.dataset.nodeDismissReady = '1';
  var cleanURL = card.getAttribute('data-node-clean-url') || '/servers';
  if (failed) addNodeResultClose(card, cleanURL);
  else startNodeResultCountdown(card, cleanURL);
}

function addNodeResultClose(card, cleanURL) {
  if (card.querySelector('.node-result-close')) return;
  var btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'node-result-close';
  btn.setAttribute('aria-label', I18N.t('js.node_result.dismiss'));
  btn.innerHTML = '<i data-lucide="x"></i>';
  btn.addEventListener('click', function () { dismissNodeCard(card, cleanURL); });
  card.appendChild(btn);
  renderIcons(btn);
}

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

function dismissNodeCard(card, cleanURL) {
  var pane = card.closest('.tab-pane');
  if (!pane || !pane.hidden) startTopBar();
  if (prefersReducedMotion) { navigateOwningPane(pane, cleanURL); return; }
  var navigated = false;
  var fallbackTimer = null;
  function go() {
    if (navigated) return;
    navigated = true;
    if (fallbackTimer) { clearTimeout(fallbackTimer); fallbackTimer = null; }
    navigateOwningPane(pane, cleanURL);
  }
  card.classList.add('is-dismissing');
  card.addEventListener('transitionend', go, { once: true });
  fallbackTimer = setTimeout(go, 600);
  registerCleanup(pane || document, function () {
    navigated = true;
    if (fallbackTimer) { clearTimeout(fallbackTimer); fallbackTimer = null; }
  });
}

/* ── Bulk action progress stream (SSE) ────────────────────────────────────── */
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
    try { es.close(); } catch (err) {}
  });

  es.addEventListener('open', function () { if (!finished) hideBulkStreamAlert(card); });
  es.addEventListener('row', function (e) {
    var row = parseJSON(e.data);
    if (row.index == null) return;
    hideBulkStreamAlert(card);
    updateBulkRow(card, row);
  });
  es.addEventListener('done', function (e) {
    finished = true;
    es.close();
    hideBulkStreamAlert(card);
    updateBulkSummary(card, parseJSON(e.data));
  });
  es.addEventListener('error', function (e) {
    if (e && e.data) {
      var payload = parseJSON(e.data);
      finished = true;
      es.close();
      showBulkStreamAlert(card, 'error', payload.error || I18N.t('js.bulk.stream_error'), true);
      return;
    }
    if (finished) return;
    if (es.readyState === EventSource.CLOSED) {
      finished = true;
      showBulkStreamAlert(card, 'error', I18N.t('js.bulk.stream_error'), true);
      return;
    }
    showBulkStreamAlert(card, 'warn', I18N.t('js.bulk.stream_reconnecting'), false);
  });
}

function ensureBulkStreamAlert(card) {
  var el = card.querySelector('[data-bulk-stream-alert]');
  if (el) return el;
  el = document.createElement('div');
  el.className = 'flash-banner bulk-stream-alert';
  el.setAttribute('data-bulk-stream-alert', '');
  el.hidden = true;
  var tableWrap = card.querySelector('.bulk-result-table-wrap');
  if (tableWrap && tableWrap.parentNode === card) card.insertBefore(el, tableWrap);
  else card.appendChild(el);
  return el;
}

function hideBulkStreamAlert(card) {
  var el = card.querySelector('[data-bulk-stream-alert]');
  if (el) el.hidden = true;
}

function showBulkStreamAlert(card, kind, message, withRetry) {
  var el = ensureBulkStreamAlert(card);
  el.className = 'flash-banner flash-banner--' + (kind === 'error' ? 'error' : 'warn') + ' bulk-stream-alert';
  el.setAttribute('role', kind === 'error' ? 'alert' : 'status');
  el.innerHTML = '';

  var text = document.createElement('p');
  text.textContent = message;
  el.appendChild(text);

  if (withRetry) {
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'btn btn--secondary btn--sm';
    btn.textContent = I18N.t('js.tabs.retry');
    btn.addEventListener('click', function () {
      markButtonBusy(btn);
      reloadForResult();
    });
    el.appendChild(btn);
  }
  el.hidden = false;
}

function normalizeBulkStatus(status) {
  return ['ok', 'failed', 'skipped', 'pending', 'running', 'timeout'].indexOf(status) !== -1 ? status : 'unknown';
}

function updateBulkRow(card, row) {
  var safeStatus = normalizeBulkStatus(row.status);
  card.querySelectorAll('[data-bulk-index="' + row.index + '"]').forEach(function (el) {
    var chip = el.querySelector('[data-bulk-status]');
    if (chip && !chip.classList.contains('bulk-status--' + safeStatus)) {
      chip.outerHTML = bulkChip(safeStatus);
      renderIcons(el);
    }
    var code = el.querySelector('[data-bulk-exit]');
    if (code) code.textContent = row.exitCode ? row.exitCode : '—';
    var reason = el.querySelector('[data-bulk-reason]');
    if (reason) reason.textContent = row.reason ? row.reason : '—';
  });
}

function bulkChip(status) {
  var safeStatus = normalizeBulkStatus(status);
  var inner = {
    ok: '<i data-lucide="check-circle-2" class="icon-in-button"></i> ' + escapeHTML(I18N.t('bulk.status_ok')),
    failed: '<i data-lucide="x-circle" class="icon-in-button"></i> ' + escapeHTML(I18N.t('bulk.status_failed')),
    skipped: '<i data-lucide="skip-forward" class="icon-in-button"></i> ' + escapeHTML(I18N.t('bulk.status_skipped')),
    pending: '<span class="spinner spinner--sm"></span> ' + escapeHTML(I18N.t('bulk.status_pending')),
    running: '<span class="spinner spinner--sm"></span> ' + escapeHTML(I18N.t('bulk.status_running')),
    timeout: '<i data-lucide="x-circle" class="icon-in-button"></i> ' + escapeHTML(I18N.t('bulk.status_failed'))
  }[safeStatus] || ('<i data-lucide="x-circle" class="icon-in-button"></i> ' + escapeHTML(I18N.t('bulk.status_failed')));
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
  renderIcons(card);
}

function setBulkCount(card, key, value) {
  var el = card.querySelector('[data-bulk-count="' + key + '"]');
  if (!el) return;
  el.innerHTML = bulkCountInner(key, value);
  if (key !== 'ok') el.hidden = value <= 0;
}

function bulkCountInner(key, value) {
  if (key === 'ok') return '<i data-lucide="check-circle-2" class="icon-in-button"></i> ' + escapeHTML(I18N.t('bulk.count_ok', { count: value }));
  if (key === 'failed') return '<i data-lucide="x-circle" class="icon-in-button"></i> ' + escapeHTML(I18N.t('bulk.count_failed', { count: value }));
  if (key === 'skipped') return '<i data-lucide="skip-forward" class="icon-in-button"></i> ' + escapeHTML(I18N.t('bulk.count_skipped', { count: value }));
  return '<span class="spinner spinner--sm"></span> ' + escapeHTML(I18N.t('bulk.count_in_progress', { count: value }));
}

function parseJSON(data) {
  try { return JSON.parse(data); } catch (err) { return {}; }
}

function reloadForResult() {
  startTopBar();
  tabReload();
}

/* ── Manual "refresh now" buttons ─────────────────────────────────────────── */
function initManualRefresh(root) {
  root = root || document;
  root.querySelectorAll('[data-refresh-now]').forEach(function (btn) {
    if (btn.hasAttribute('data-nodes-refresh-btn')) return;
    if ((btn.getAttribute('data-refresh-method') || '').toLowerCase() === 'post') return;
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

export { initAutoRefresh, initStream, initNodeResult, initBulkStream, initManualRefresh };
