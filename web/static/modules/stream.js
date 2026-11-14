/* Nodexia live streams, auto-refresh & result cards (native ES module — no
 * bundler, no npm).
 *
 * Everything that keeps a finished-or-running page moving on its own: the
 * auto-refresh countdown, the Server-Sent Events output stream for commands /
 * node actions / installs, the node-action result card (auto-dismiss with a
 * live countdown, manual close on failure), the bulk-action row/summary
 * progress stream and the manual "refresh now" buttons. Extracted from
 * app.js; the shared primitives (i18n, icons, top bar, tab-aware navigation
 * + cleanup, busy buttons) come from ./core.js and the copy-button wiring
 * from ./ui.js.
 *
 * Every init* function is progressive enhancement: absent targets no-op, and
 * the ones that accept `root` can be re-run against a freshly injected tab
 * pane by window.NodexiaApp.rescan().
 */
import { I18N, prefersReducedMotion, renderIcons, startTopBar, tabNavigate, tabReload, registerCleanup, markButtonBusy } from './core.js';
import { initCopyButtons } from './ui.js';

/* ── Auto-refresh with live countdown ───────────────────── */
function navigateOwningPane(pane, targetURL) {
  if (!pane) {
    startTopBar();
    window.location.href = targetURL;
    return;
  }
  if (!pane.hidden) {
    startTopBar();
  }
  if (window.NodexiaTabs && typeof window.NodexiaTabs.navigatePane === 'function') {
    window.NodexiaTabs.navigatePane(pane, targetURL);
    return;
  }
  try {
    var parsed = new URL(targetURL, window.location.origin);
    var form = document.createElement('form');
    form.method = 'get';
    form.action = parsed.pathname;
    form.hidden = true;
    parsed.searchParams.forEach(function (val, k) {
      var input = document.createElement('input');
      input.type = 'hidden';
      input.name = k;
      input.value = val;
      form.appendChild(input);
    });
    pane.appendChild(form);
    var ev = new Event('submit', { bubbles: true, cancelable: true });
    var notCancelled = form.dispatchEvent(ev);
    form.remove();
    if (notCancelled && !pane.hidden) {
      window.location.href = targetURL;
    }
  } catch (e) {
    if (!pane.hidden) {
      window.location.href = targetURL;
    }
  }
}

function initAutoRefresh(root) {
  root = root || document;
  var select = root.querySelector('#auto-refresh-select');
  if (!select) return;
  var url = select.getAttribute('data-refresh-url');
  if (!url) return;
  var refreshURL = url + (url.indexOf('?') === -1 ? '?' : '&') + 'refresh=1';
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
        navigateOwningPane(owningPane, refreshURL);
        return;
      }
      updatePill();
    }, 250);
  }
  function updatePill() {
    var remaining = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
    textSpan.textContent = I18N.t('js.refresh.next_in', { seconds: remaining });
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
  registerCleanup(owningPane || root, stop);
}

/* ── Live output stream (Server-Sent Events) ────────────────
 * Replaces the old 2-second full-page poll. A running command / node action /
 * install renders a result card carrying data-stream-sse-url; we subscribe to
 * that feed and append output chunks in place. EventSource reconnects on its
 * own after a network blip — and because the server resends the buffer from
 * offset 0 on every connect, we reset the output on each (re)open so a
 * reconnect never duplicates lines.
 */
function createStreamTrack(el) {
  var track = {
    el: el,
    autoScroll: true,
    received: el ? el.textContent.length : 0,
    connOffset: 0
  };
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
  if (active) {
    card.classList.add('is-reconnecting');
    var statusLine = card.querySelector('[data-stream-status]');
    if (statusLine && !statusLine.querySelector('[data-stream-reconnect]')) {
      var note = document.createElement('span');
      note.className = 'stream-reconnect-note';
      note.setAttribute('data-stream-reconnect', '');
      note.textContent = ' — ' + I18N.t('js.live.reconnecting');
      statusLine.appendChild(note);
    }
    return;
  }
  card.classList.remove('is-reconnecting');
  var existing = card.querySelector('[data-stream-reconnect]');
  if (existing) existing.remove();
}

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

  // Track independent scroll pinning and deduplicate offset-0 replays on SSE reconnect
  // so transient network drops never clear accumulated output or duplicate lines.
  var outTrack = createStreamTrack(stdout);
  var errTrack = createStreamTrack(stderr);

  var finished = false;
  card.classList.add('is-connecting');
  var es = new EventSource(url);
  registerCleanup(root, function () {
    finished = true;
    try { es.close(); } catch (err) { /* best-effort */ }
  });

  es.addEventListener('open', function () {
    card.classList.remove('is-connecting');
    setReconnectingState(card, false);
    outTrack.connOffset = 0;
    errTrack.connOffset = 0;
  });

  es.addEventListener('output', function (e) {
    appendStreamChunk(outTrack, e.data);
  });

  es.addEventListener('stderr', function (e) {
    appendStreamChunk(errTrack, e.data);
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
    var hasOut = Boolean((doneStdout && doneStdout.textContent) || (doneStderr && doneStderr.textContent));
    emptyNote.hidden = hasOut;
  }

  // A node-action card self-dismisses on success / exposes a manual close on
  // failure, and stderr that arrived on a clean exit is no longer styled red.
  if (card.hasAttribute('data-node-result')) {
    if (doneStderr && !failed) doneStderr.classList.remove('output-block--error');
    scheduleNodeResultDismiss(card, failed);
  }

  // Output that streamed in after page load has no copy button yet.
  initCopyButtons();
  renderIcons();
}

function streamFallback(card) {
  card.classList.remove('is-connecting');
  setReconnectingState(card, false);
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

  es.addEventListener('open', function () {
    if (!finished) hideBulkStreamAlert(card);
  });

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
  if (tableWrap && tableWrap.parentNode === card) {
    card.insertBefore(el, tableWrap);
  } else {
    card.appendChild(el);
  }
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
      markButtonBusy(btn, I18N.t('js.loading'));
      reloadForResult();
    });
    el.appendChild(btn);
  }

  el.hidden = false;
}

function normalizeBulkStatus(status) {
  var allowed = ['ok', 'failed', 'skipped', 'pending', 'running', 'timeout'];
  return allowed.indexOf(status) !== -1 ? status : 'unknown';
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
    if (reason) {
      reason.textContent = row.reason ? row.reason : '—';
    }
  });
}

function bulkChip(status) {
  var safeStatus = normalizeBulkStatus(status);
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
  renderIcons(card);
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

export { initAutoRefresh, initStream, initNodeResult, initBulkStream, initManualRefresh };
