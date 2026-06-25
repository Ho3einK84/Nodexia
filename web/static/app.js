/* Nodexia interactive layer.
 *
 * The Content-Security-Policy forbids inline <script> blocks, so ALL client
 * behaviour lives in this file (served from 'self'). Everything is progressive
 * enhancement: if a feature's target elements are absent, it quietly no-ops.
 */
(function () {
  'use strict';

  /* ── Client-side i18n ───────────────────────────────────
   * The server injects only the strings the JS needs as a non-executable JSON
   * island (<script type="application/json" id="nodexia-i18n">), which is exempt
   * from the strict script-src CSP. We parse it once and expose window.nxT /
   * window.nxTn so every page script (loaded after this file) can localize.
   * nxT mirrors the server's {{ t }}; nxTn mirrors {{ tn }} (plural by count).
   * A missing key returns the key itself, matching the Go fallback. */
  var I18N = (function () {
    var map = {};
    try {
      var island = document.getElementById('nodexia-i18n');
      if (island) map = JSON.parse(island.textContent) || {};
    } catch (err) { /* fall back to raw keys */ }
    var lang = document.documentElement.getAttribute('lang') || 'en';
    function category(n) {
      if (lang === 'fa') return (n === 0 || n === 1) ? 'one' : 'other';
      return n === 1 ? 'one' : 'other';
    }
    function substitute(text, params) {
      if (!params) return text;
      return text.replace(/\{(\w+)\}/g, function (whole, name) {
        return params[name] != null ? params[name] : whole;
      });
    }
    return {
      t: function (key, params) {
        var value = map[key];
        if (value == null) return key;
        if (typeof value === 'object') value = value.other || key;
        return substitute(value, params);
      },
      tn: function (key, count, params) {
        var value = map[key];
        if (value == null) return key;
        var text = typeof value === 'object'
          ? (value[category(count)] || value.other || key)
          : value;
        params = params || {};
        if (params.count == null) params.count = count;
        return substitute(text, params);
      }
    };
  })();
  window.nxT = I18N.t;
  window.nxTn = I18N.tn;

  var prefersReducedMotion = window.matchMedia &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  /* ── Icons ──────────────────────────────────────────────
   * Lucide is vendored locally (/static/lucide.min.js, loaded with `defer`).
   * renderIcons() is safe to call repeatedly and after dynamic DOM inserts.
   */
  function renderIcons() {
    if (typeof lucide === 'undefined' || !lucide.createIcons) return;
    try {
      // createIcons only converts <i data-lucide> elements that are still
      // present, so calling it repeatedly is idempotent and cheap.
      lucide.createIcons();
    } catch (err) {
      /* never let an icon failure break the page */
    }
  }

  /* ── Top progress bar ───────────────────────────────────
   * Lightweight navigation indicator shown during form posts and same-origin
   * link navigations, since full-page reloads are the app's primary flow.
   */
  var topBar = null;
  var topBarTimer = null;
  function ensureTopBar() {
    if (topBar) return topBar;
    topBar = document.createElement('div');
    topBar.className = 'top-progress';
    document.body.appendChild(topBar);
    return topBar;
  }
  function startTopBar() {
    if (prefersReducedMotion) return;
    var bar = ensureTopBar();
    bar.classList.remove('is-done');
    bar.classList.add('is-active');
    var width = 8;
    bar.style.width = width + '%';
    clearInterval(topBarTimer);
    topBarTimer = setInterval(function () {
      width += (90 - width) * 0.12;
      bar.style.width = width.toFixed(1) + '%';
    }, 220);
  }
  function finishTopBar() {
    if (!topBar) return;
    clearInterval(topBarTimer);
    topBar.style.width = '100%';
    topBar.classList.add('is-done');
    setTimeout(function () {
      if (topBar) {
        topBar.classList.remove('is-active', 'is-done');
        topBar.style.width = '0%';
      }
    }, 400);
  }
  window.addEventListener('pageshow', finishTopBar);

  /* ── Button busy state ──────────────────────────────────
   * Disables the clicked submit button and swaps in a spinner so double
   * submits are prevented and slow SSH actions feel responsive.
   */
  function markButtonBusy(btn) {
    if (!btn || btn.dataset.busy === '1') return;
    btn.dataset.busy = '1';
    btn.classList.add('is-busy');
    var label = btn.textContent.trim();
    if (label) btn.setAttribute('data-busy-label', label);
    // Keep width stable while we swap content.
    btn.style.minWidth = btn.offsetWidth + 'px';
    btn.innerHTML = '<span class="spinner spinner--sm" aria-hidden="true"></span> ' + I18N.t('js.working');
    // Re-enable as a safety net if navigation never happens (e.g. validation).
    setTimeout(function () { restoreButton(btn); }, 30000);
  }
  function restoreButton(btn) {
    if (!btn || btn.dataset.busy !== '1') return;
    btn.dataset.busy = '';
    btn.classList.remove('is-busy');
    var label = btn.getAttribute('data-busy-label');
    if (label) { btn.textContent = label; }
  }

  /* ── Loading overlay + confirm + busy, on form submit ───── */
  function initForms() {
    var overlay = document.getElementById('loading-overlay');

    document.addEventListener('submit', function (e) {
      var form = e.target;
      if (!form.matches || !form.matches('form')) return;

      var submitter = e.submitter ||
        form.querySelector('button[type="submit"]:focus') ||
        form.querySelector('button[type="submit"]');

      // Confirmation for destructive actions (explicit data-confirm or danger btn).
      var confirmMsg = (submitter && submitter.getAttribute('data-confirm')) ||
        form.getAttribute('data-confirm');
      if (!confirmMsg && submitter && submitter.classList.contains('btn--danger')) {
        confirmMsg = I18N.t('js.confirm_irreversible');
      }
      if (confirmMsg && !window.confirm(confirmMsg)) {
        e.preventDefault();
        return;
      }

      startTopBar();
      if (submitter && !submitter.hasAttribute('data-no-loading')) {
        markButtonBusy(submitter);
      }
      if (overlay && form.matches('form[method="post"]')) {
        if (submitter && submitter.hasAttribute('data-no-loading')) return;
        overlay.style.display = 'flex';
      }
    });
  }

  /* ── Same-origin link navigation → progress bar ───────── */
  function initLinks() {
    document.addEventListener('click', function (e) {
      var a = e.target.closest && e.target.closest('a[href]');
      if (!a) return;
      if (a.target === '_blank' || a.hasAttribute('download')) return;
      if (a.getAttribute('href').charAt(0) === '#') return;
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      var url;
      try { url = new URL(a.href, window.location.href); } catch (err) { return; }
      if (url.origin !== window.location.origin) return;
      startTopBar();
    });
  }

  /* ── Flash banners: dismissible + auto-dismiss ──────────── */
  function initFlash() {
    document.querySelectorAll('.flash-banner').forEach(function (banner) {
      if (banner.querySelector('.flash-banner__close')) return;
      var close = document.createElement('button');
      close.type = 'button';
      close.className = 'flash-banner__close';
      close.setAttribute('aria-label', I18N.t('js.flash.dismiss'));
      close.innerHTML = '<i data-lucide="x"></i>';
      close.addEventListener('click', function () { dismissFlash(banner); });
      banner.appendChild(close);
      banner.classList.add('is-visible');

      // Auto-dismiss success messages; keep errors until dismissed.
      if (banner.classList.contains('flash-banner--success')) {
        setTimeout(function () { dismissFlash(banner); }, 6000);
      }
    });
  }
  function dismissFlash(banner) {
    banner.classList.add('is-leaving');
    setTimeout(function () { banner.remove(); }, 300);
  }

  /* ── Masked secrets with reveal toggle ──────────────────── */
  function initSecrets() {
    document.querySelectorAll('[data-secret]').forEach(function (el) {
      if (el.dataset.secretReady === '1') return;
      var real = el.textContent;
      if (!real.trim()) return;
      el.dataset.secretReady = '1';
      el.dataset.secretValue = real;
      el.textContent = '••••••••';

      var toggle = document.createElement('button');
      toggle.type = 'button';
      toggle.className = 'secret-toggle';
      toggle.setAttribute('aria-label', I18N.t('js.secret.reveal'));
      toggle.innerHTML = '<i data-lucide="eye"></i>';
      var shown = false;
      toggle.addEventListener('click', function () {
        shown = !shown;
        el.textContent = shown ? el.dataset.secretValue : '••••••••';
        toggle.innerHTML = shown ? '<i data-lucide="eye-off"></i>' : '<i data-lucide="eye"></i>';
        toggle.setAttribute('aria-label', shown ? I18N.t('js.secret.hide') : I18N.t('js.secret.reveal'));
        renderIcons();
      });
      if (el.nextSibling) {
        el.parentNode.insertBefore(toggle, el.nextSibling);
      } else {
        el.parentNode.appendChild(toggle);
      }
    });
  }

  /* ── Copy-to-clipboard for output blocks ────────────────── */
  function initCopyButtons() {
    document.querySelectorAll('pre.output-block').forEach(function (pre) {
      if (pre.parentElement && pre.parentElement.classList.contains('output-wrap')) return;
      // Blocks with their own explicit Copy control opt out of the auto button.
      if (pre.hasAttribute('data-no-auto-copy')) return;
      if (!pre.textContent.trim()) return;

      var wrap = document.createElement('div');
      wrap.className = 'output-wrap';
      pre.parentNode.insertBefore(wrap, pre);
      wrap.appendChild(pre);

      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'copy-btn';
      btn.setAttribute('aria-label', I18N.t('js.copy.aria'));
      btn.innerHTML = '<i data-lucide="copy"></i>';
      btn.addEventListener('click', function () {
        copyText(pre.textContent).then(function (ok) {
          btn.classList.add(ok ? 'is-copied' : 'is-failed');
          btn.innerHTML = ok
            ? '<i data-lucide="check"></i>'
            : '<i data-lucide="x"></i>';
          renderIcons();
          setTimeout(function () {
            btn.classList.remove('is-copied', 'is-failed');
            btn.innerHTML = '<i data-lucide="copy"></i>';
            renderIcons();
          }, 1600);
        });
      });
      wrap.appendChild(btn);
    });
  }
  /* ── Explicit labeled copy buttons (data-copy-target="#id") ───────────────
     Copies the FULL value of the referenced element, including the real value
     when that element is masked by initSecrets (data-secret). Shows an inline
     "Copied!" confirmation that reverts after a moment. */
  function initCopyTargets() {
    document.querySelectorAll('[data-copy-target]').forEach(function (btn) {
      if (btn.dataset.copyReady === '1') return;
      btn.dataset.copyReady = '1';
      var label = btn.innerHTML;
      btn.addEventListener('click', function () {
        var target = document.querySelector(btn.getAttribute('data-copy-target'));
        if (!target) return;
        // secretValue holds the unmasked value when initSecrets has masked it.
        var value = target.dataset.secretValue != null ? target.dataset.secretValue : target.textContent;
        // Strip only a trailing newline from the <pre>; never alter inner content.
        copyText(value.replace(/\n$/, '')).then(function (ok) {
          btn.classList.add(ok ? 'is-copied' : 'is-failed');
          btn.innerHTML = ok
            ? '<i data-lucide="check"></i> ' + I18N.t('js.copy.copied')
            : '<i data-lucide="x"></i> ' + I18N.t('js.failed');
          renderIcons();
          setTimeout(function () {
            btn.classList.remove('is-copied', 'is-failed');
            btn.innerHTML = label;
            renderIcons();
          }, 1600);
        });
      });
    });
  }
  /* ── On-demand PasarGuard node credentials (API key + certificate) ────────
     Fetches the secrets live over SSH and renders masked, copyable blocks that
     reuse initSecrets (masking) and initCopyTargets (copy). Nothing is stored. */
  function initNodeCredentials() {
    document.querySelectorAll('[data-node-credentials]').forEach(function (btn) {
      if (btn.dataset.credReady === '1') return;
      btn.dataset.credReady = '1';
      var out = btn.parentNode.querySelector('.node-credentials__out');
      var showLabel = '<i data-lucide="key-round" class="icon-in-button"></i> ' + I18N.t('js.cred.show');
      btn.addEventListener('click', function () {
        if (out && !out.hidden) {
          out.hidden = true;
          out.innerHTML = '';
          btn.innerHTML = showLabel;
          renderIcons();
          return;
        }
        btn.disabled = true;
        btn.innerHTML = '<i data-lucide="loader"></i> ' + I18N.t('js.loading');
        renderIcons();
        fetch(btn.getAttribute('data-url'), { headers: { Accept: 'application/json' } })
          .then(function (resp) {
            return resp.json().then(function (data) { return { ok: resp.ok, data: data }; });
          })
          .then(function (res) {
            btn.disabled = false;
            if (!res.ok) {
              showCredError(out, btn, showLabel, (res.data && res.data.error) || I18N.t('js.cred.load_error'));
              return;
            }
            renderNodeCredentials(out, btn, res.data);
          })
          .catch(function () {
            btn.disabled = false;
            showCredError(out, btn, showLabel, I18N.t('js.cred.load_error'));
          });
      });
    });
  }

  function showCredError(out, btn, showLabel, message) {
    if (out) {
      out.hidden = false;
      out.innerHTML = '';
      var p = document.createElement('p');
      p.className = 'node-actions-note';
      p.textContent = message;
      out.appendChild(p);
    }
    btn.innerHTML = showLabel;
    renderIcons();
  }

  function renderNodeCredentials(out, btn, data) {
    if (!out) return;
    out.innerHTML = '';
    out.hidden = false;
    var name = String(data.node_name || 'node').replace(/[^a-zA-Z0-9._-]/g, '') || 'node';

    if (data.has_api_key) {
      out.appendChild(credentialBlock('key-round', I18N.t('js.cred.api_key'), 'cred-key-' + name, data.api_key, true));
    } else {
      var miss = document.createElement('p');
      miss.className = 'node-actions-note';
      miss.textContent = I18N.t('js.cred.api_key_missing', { name: name });
      out.appendChild(miss);
    }
    if (data.has_cert) {
      out.appendChild(credentialBlock('shield-check', I18N.t('js.cred.ssl_cert'), 'cred-cert-' + name, data.certificate, false));
    }

    // Wire masking + copy on the freshly inserted blocks.
    initSecrets();
    initCopyTargets();
    btn.innerHTML = '<i data-lucide="eye-off" class="icon-in-button"></i> ' + I18N.t('js.cred.hide');
    renderIcons();
  }

  function credentialBlock(icon, label, id, value, mask) {
    var section = document.createElement('div');
    section.className = 'node-section node-cred-block';

    var head = document.createElement('div');
    head.className = 'node-section__head';
    var lab = document.createElement('span');
    lab.className = 'node-section__label';
    lab.innerHTML = '<i data-lucide="' + icon + '"></i> ' + label;
    var copyBtn = document.createElement('button');
    copyBtn.type = 'button';
    copyBtn.className = 'btn btn--ghost btn--sm copy-inline';
    copyBtn.setAttribute('data-copy-target', '#' + id);
    copyBtn.setAttribute('aria-label', I18N.t('js.copy.aria_label', { label: label }));
    copyBtn.innerHTML = '<i data-lucide="copy"></i> ' + I18N.t('js.copy.label');
    head.appendChild(lab);
    head.appendChild(copyBtn);

    var pre = document.createElement('pre');
    pre.id = id;
    pre.className = 'output-block node-secret-block' + (mask ? '' : ' scrollable');
    pre.setAttribute('data-no-auto-copy', '');
    if (mask) pre.setAttribute('data-secret', '');
    pre.textContent = value; // full raw value — never truncated

    section.appendChild(head);
    section.appendChild(pre);
    return section;
  }

  function copyText(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text).then(function () { return true; },
        function () { return fallbackCopy(text); });
    }
    return Promise.resolve(fallbackCopy(text));
  }
  function fallbackCopy(text) {
    try {
      var ta = document.createElement('textarea');
      ta.value = text;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      var ok = document.execCommand('copy');
      document.body.removeChild(ta);
      return ok;
    } catch (err) { return false; }
  }

  /* ── Animated progress bar / mini-bar fills ─────────────── */
  function initProgressBars() {
    var fills = document.querySelectorAll('.mini-metric__fill');
    fills.forEach(function (fill) {
      var target = fill.style.width || '0%';
      if (prefersReducedMotion) { fill.style.width = target; return; }
      fill.style.width = '0%';
      // Force reflow so the transition runs from 0 → target.
      void fill.offsetWidth;
      requestAnimationFrame(function () {
        requestAnimationFrame(function () { fill.style.width = target; });
      });
    });
  }

  /* ── Scroll-reveal entrance for cards ───────────────────── */
  function initReveal() {
    var targets = document.querySelectorAll(
      '.card, .server-card, .stat-card, .dashboard-item, .node-card, .empty-state-card');
    if (!targets.length) return;
    if (prefersReducedMotion || !('IntersectionObserver' in window)) {
      targets.forEach(function (el) { el.classList.add('reveal-in'); });
      return;
    }
    targets.forEach(function (el, i) {
      el.classList.add('reveal');
      el.style.setProperty('--reveal-delay', Math.min(i * 40, 320) + 'ms');
    });
    var observer = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          entry.target.classList.add('reveal-in');
          observer.unobserve(entry.target);
        }
      });
    }, { threshold: 0.08, rootMargin: '0px 0px -40px 0px' });
    targets.forEach(function (el) { observer.observe(el); });
  }

  /* ── Auto-refresh with live countdown ───────────────────── */
  function initAutoRefresh() {
    var select = document.getElementById('auto-refresh-select');
    if (!select) return;
    var url = select.getAttribute('data-refresh-url');
    if (!url) return;
    var refreshURL = url + (url.indexOf('?') === -1 ? '?' : '&') + 'refresh=1';
    var key = 'nodexia_auto_refresh_' + (select.getAttribute('data-refresh-key') || url);

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
          window.location.href = refreshURL;
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
        try { localStorage.removeItem(key); } catch (e) {}
        stop();
      }
    }

    var saved = null;
    try { saved = localStorage.getItem(key); } catch (e) {}
    if (saved) { select.value = saved; }
    select.addEventListener('change', apply);
    apply();
  }

  /* ── Live output stream (Server-Sent Events) ────────────────
   * Replaces the old 2-second full-page poll. A running command / node action /
   * install renders a result card carrying data-stream-sse-url; we subscribe to
   * that feed and append output chunks in place. EventSource reconnects on its
   * own after a network blip — and because the server resends the buffer from
   * offset 0 on every connect, we reset the output on each (re)open so a
   * reconnect never duplicates lines.
   */
  function initStream() {
    var card = document.querySelector('[data-stream-sse-url]');
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

  function initNodeResult() {
    var card = document.querySelector('[data-node-result]');
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
  }

  // dismissNodeCard slides the card out (honoring reduced-motion) then navigates
  // to the clean nodes URL.
  function dismissNodeCard(card, cleanURL) {
    startTopBar();
    if (prefersReducedMotion) { window.location.href = cleanURL; return; }
    var navigated = false;
    function go() { if (navigated) return; navigated = true; window.location.href = cleanURL; }
    card.classList.add('is-dismissing');
    card.addEventListener('transitionend', go, { once: true });
    setTimeout(go, 600); // fallback when transitionend does not fire
  }

  /* ── Bulk action progress stream (SSE) ──────────────────────
   * The bulk result page runs a command across many servers at once. Each
   * server row is pushed as a `row` event as it transitions
   * pending → running → ok/failed, swapping an inline spinner for a check / x.
   */
  function initBulkStream() {
    var card = document.querySelector('[data-bulk-sse-url]');
    if (!card) return;
    var url = card.getAttribute('data-bulk-sse-url');
    if (!url || typeof EventSource === 'undefined') return;

    var finished = false;
    var es = new EventSource(url);

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
    var inner = {
      ok: '<i data-lucide="check-circle-2" class="icon-in-button"></i> ' + I18N.t('bulk.status_ok'),
      failed: '<i data-lucide="x-circle" class="icon-in-button"></i> ' + I18N.t('bulk.status_failed'),
      skipped: '<i data-lucide="skip-forward" class="icon-in-button"></i> ' + I18N.t('bulk.status_skipped'),
      pending: '<span class="spinner spinner--sm"></span> ' + I18N.t('bulk.status_pending'),
      running: '<span class="spinner spinner--sm"></span> ' + I18N.t('bulk.status_running')
    }[status] || ('<i data-lucide="x-circle" class="icon-in-button"></i> ' + I18N.t('bulk.status_failed'));
    return '<span class="bulk-status bulk-status--' + status + '" data-bulk-status>' + inner + '</span>';
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
    window.location.reload();
  }

  function escapeHTML(value) {
    return String(value).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  /* ── Manual "refresh now" buttons ───────────────────────── */
  function initManualRefresh() {
    document.querySelectorAll('[data-refresh-now]').forEach(function (btn) {
      btn.addEventListener('click', function (e) {
        var url = btn.getAttribute('data-refresh-now');
        if (!url) return;
        e.preventDefault();
        startTopBar();
        markButtonBusy(btn);
        window.location.href = url;
      });
    });
  }

  /* ── Server card overflow action menu ──────────────────── */
  function initActionMenus() {
    var menus = document.querySelectorAll('[data-action-menu]');
    if (!menus.length) return;

    function setOpen(menu, open) {
      menu.classList.toggle('is-open', open);
      var toggle = menu.querySelector('.action-menu__toggle');
      if (toggle) toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
      var card = menu.closest('.server-card, .node-card');
      if (card) card.style.zIndex = open ? '30' : '';
    }
    function closeAll(except) {
      menus.forEach(function (menu) {
        if (menu !== except && menu.classList.contains('is-open')) setOpen(menu, false);
      });
    }

    menus.forEach(function (menu) {
      var toggle = menu.querySelector('.action-menu__toggle');
      if (!toggle) return;
      toggle.addEventListener('click', function (e) {
        e.preventDefault();
        e.stopPropagation();
        var willOpen = !menu.classList.contains('is-open');
        closeAll(menu);
        setOpen(menu, willOpen);
      });
    });

    document.addEventListener('click', function (e) {
      if (e.target.closest && e.target.closest('[data-action-menu]')) return;
      closeAll(null);
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') closeAll(null);
    });
    window.addEventListener('resize', function () { closeAll(null); });
  }

  /* ── Modal dialogs (frosted backdrop) ───────────────────── */
  function initModals() {
    var openers = document.querySelectorAll('[data-modal-open]');
    var modals = document.querySelectorAll('[data-modal]');
    if (!modals.length) return;
    var lastFocus = null;

    function openModal(modal) {
      if (!modal) return;
      lastFocus = document.activeElement;
      modal.hidden = false;
      // Force reflow so the entrance transition runs from the hidden state.
      void modal.offsetWidth;
      modal.classList.add('is-open');
      document.body.classList.add('modal-open');
      var focusTarget = modal.querySelector('[data-modal-autofocus]') ||
        modal.querySelector('input:not([type="hidden"]), select, textarea, button');
      if (focusTarget && focusTarget.focus) {
        try { focusTarget.focus({ preventScroll: true }); } catch (err) { focusTarget.focus(); }
      }
    }
    function closeModal(modal) {
      if (!modal || !modal.classList.contains('is-open')) return;
      modal.classList.remove('is-open');
      document.body.classList.remove('modal-open');
      setTimeout(function () {
        if (!modal.classList.contains('is-open')) modal.hidden = true;
      }, prefersReducedMotion ? 0 : 240);
      if (lastFocus && lastFocus.focus) { try { lastFocus.focus(); } catch (err) {} }
    }

    openers.forEach(function (opener) {
      opener.addEventListener('click', function (e) {
        e.preventDefault();
        var id = opener.getAttribute('data-modal-open');
        openModal(id ? document.getElementById(id) : null);
      });
    });

    modals.forEach(function (modal) {
      modal.querySelectorAll('[data-modal-close]').forEach(function (btn) {
        btn.addEventListener('click', function (e) {
          e.preventDefault();
          closeModal(modal);
        });
      });
      // Basic focus trap: keep Tab within the dialog while it is open.
      modal.addEventListener('keydown', function (e) {
        if (e.key !== 'Tab' || !modal.classList.contains('is-open')) return;
        var focusables = modal.querySelectorAll(
          'a[href], button:not([disabled]), input:not([type="hidden"]), select, textarea, [tabindex]:not([tabindex="-1"])');
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
    });

    document.addEventListener('keydown', function (e) {
      if (e.key !== 'Escape') return;
      document.querySelectorAll('[data-modal].is-open').forEach(closeModal);
    });

    // Re-open a modal the server asked to surface (e.g. install form had errors).
    var initial = document.querySelector('[data-modal][data-modal-open-initial]');
    if (initial) openModal(initial);
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
    // If the viewport grows past the mobile breakpoint, ensure it's closed.
    window.addEventListener('resize', function () {
      if (window.innerWidth > 700) close();
    });
  }

  /* ── Back-to-top button ─────────────────────────────────── */
  function initBackToTop() {
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'back-to-top';
    btn.setAttribute('aria-label', I18N.t('js.back_to_top'));
    btn.innerHTML = '<i data-lucide="arrow-up"></i>';
    btn.addEventListener('click', function () {
      window.scrollTo({ top: 0, behavior: prefersReducedMotion ? 'auto' : 'smooth' });
    });
    document.body.appendChild(btn);
    var onScroll = function () {
      btn.classList.toggle('is-visible', window.scrollY > 400);
    };
    window.addEventListener('scroll', onScroll, { passive: true });
    onScroll();
  }

  /* ── Keyboard shortcuts ─────────────────────────────────── */
  function isTyping(el) {
    return el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' ||
      el.tagName === 'SELECT' || el.isContentEditable);
  }
  function initShortcuts() {
    var pendingG = false;
    var gTimer = null;
    document.addEventListener('keydown', function (e) {
      if (isTyping(document.activeElement) || e.metaKey || e.ctrlKey || e.altKey) return;

      if (pendingG) {
        pendingG = false;
        clearTimeout(gTimer);
        if (e.key === 'o') { window.location.href = '/'; return; }
        if (e.key === 's') { window.location.href = '/servers'; return; }
        if (e.key === 'd') { window.location.href = '/ops/diagnostics'; return; }
        return;
      }

      if (e.key === 'g') {
        pendingG = true;
        gTimer = setTimeout(function () { pendingG = false; }, 1000);
        return;
      }
      if (e.key === '/') {
        var input = document.querySelector(
          'input[type="text"], input[type="search"], textarea');
        if (input) { e.preventDefault(); input.focus(); }
        return;
      }
      if (e.key === 'r') {
        var refresh = document.querySelector('[data-refresh-now]') ||
          document.querySelector('a[href*="refresh=1"]');
        if (refresh) {
          e.preventDefault();
          startTopBar();
          window.location.href = refresh.getAttribute('data-refresh-now') ||
            refresh.getAttribute('href');
        }
      }
    });
  }

  /* ── Collapsible sections (persisted open/closed) ───────── */
  function setCollapseIcon(toggle, isOpen) {
    var icon = toggle ? toggle.querySelector('.collapsible__icon') : null;
    if (icon) icon.style.transform = isOpen ? 'rotate(90deg)' : 'rotate(0deg)';
    if (toggle) toggle.setAttribute('aria-expanded', isOpen ? 'true' : 'false');
  }
  function collapseContent(body) {
    body.style.maxHeight = body.scrollHeight + 'px';
    body.style.opacity = '1';
    requestAnimationFrame(function () {
      body.style.maxHeight = '0px';
      body.style.paddingTop = '0';
      body.style.paddingBottom = '0';
      body.style.opacity = '0';
    });
  }
  function expandContent(body) {
    body.style.maxHeight = '0px';
    body.style.paddingTop = '';
    body.style.paddingBottom = '';
    body.style.opacity = '0';
    requestAnimationFrame(function () {
      body.style.maxHeight = body.scrollHeight + 'px';
      body.style.opacity = '1';
    });
  }
  function initCollapsibles() {
    document.querySelectorAll('.collapsible').forEach(function (el) {
      var toggle = el.querySelector('.collapsible__toggle');
      var body = el.querySelector('.collapsible__content');
      if (!toggle || !body) return;

      var key = toggle.getAttribute('data-collapse-key') || '';
      var stored = null;
      if (key) { try { stored = localStorage.getItem('collapse_' + key); } catch (err) {} }
      // A stored preference always wins; otherwise honour data-collapse-default
      // ("closed" starts collapsed), defaulting to open.
      var defaultClosed = toggle.getAttribute('data-collapse-default') === 'closed';
      var isOpen = stored ? stored !== 'closed' : !defaultClosed;

      body.style.transition = 'max-height 0.3s ease, padding 0.3s ease, opacity 0.3s ease';
      body.style.overflow = 'hidden';
      if (!isOpen) {
        body.style.maxHeight = '0px';
        body.style.paddingTop = '0';
        body.style.paddingBottom = '0';
        body.style.opacity = '0';
        setCollapseIcon(toggle, false);
      } else {
        body.style.maxHeight = body.scrollHeight + 'px';
        body.style.opacity = '1';
        setCollapseIcon(toggle, true);
        requestAnimationFrame(function () { body.style.maxHeight = 'none'; });
      }

      toggle.setAttribute('type', 'button');
      toggle.addEventListener('click', function () {
        var closed = body.style.maxHeight === '0px' || body.style.maxHeight === '0';
        if (closed) {
          expandContent(body);
          setCollapseIcon(toggle, true);
          if (key) { try { localStorage.setItem('collapse_' + key, 'open'); } catch (err) {} }
        } else {
          collapseContent(body);
          setCollapseIcon(toggle, false);
          if (key) { try { localStorage.setItem('collapse_' + key, 'closed'); } catch (err) {} }
        }
      });

      body.addEventListener('transitionend', function () {
        if (body.style.maxHeight !== '0px' && body.style.maxHeight !== '0') {
          body.style.maxHeight = 'none';
        }
      });
    });
  }

  /* ── Advanced panel toggle ──────────────────────────────── */
  function initAdvancedToggle() {
    document.querySelectorAll('.advanced-toggle[data-target]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var target = document.getElementById(btn.getAttribute('data-target'));
        if (!target) return;
        var isHidden = target.hasAttribute('hidden');
        if (isHidden) {
          target.removeAttribute('hidden');
          btn.classList.add('is-active');
          btn.setAttribute('aria-expanded', 'true');
        } else {
          target.setAttribute('hidden', '');
          btn.classList.remove('is-active');
          btn.setAttribute('aria-expanded', 'false');
        }
      });
    });
  }

  /* ── Service worker registration (PWA) ──────────────────────
   * Registers the root-scoped worker that powers installability and the
   * offline fallback. Progressive enhancement: unsupported browsers simply
   * skip it. See docs/pwa.md.
   */
  function initServiceWorker() {
    if (!('serviceWorker' in navigator)) return;
    window.addEventListener('load', function () {
      navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(function () {
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
    initNodeResult();
    initBulkStream();
    initManualRefresh();
    initActionMenus();
    initModals();
    initDrawer();
    initBackToTop();
    initShortcuts();
    initCollapsibles();
    initAdvancedToggle();
    initServiceWorker();
    initOrientationLock();
    renderIcons(); // pick up icons injected by the steps above
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
