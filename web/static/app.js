/* Nodexia interactive layer.
 *
 * The Content-Security-Policy forbids inline <script> blocks, so ALL client
 * behaviour lives in this file (served from 'self'). Everything is progressive
 * enhancement: if a feature's target elements are absent, it quietly no-ops.
 */
(function () {
  'use strict';

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
    btn.innerHTML = '<span class="btn-spinner" aria-hidden="true"></span> Working…';
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
        confirmMsg = 'This action cannot be undone. Continue?';
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
      close.setAttribute('aria-label', 'Dismiss message');
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
      toggle.setAttribute('aria-label', 'Reveal value');
      toggle.innerHTML = '<i data-lucide="eye"></i>';
      var shown = false;
      toggle.addEventListener('click', function () {
        shown = !shown;
        el.textContent = shown ? el.dataset.secretValue : '••••••••';
        toggle.innerHTML = shown ? '<i data-lucide="eye-off"></i>' : '<i data-lucide="eye"></i>';
        toggle.setAttribute('aria-label', shown ? 'Hide value' : 'Reveal value');
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
      btn.setAttribute('aria-label', 'Copy to clipboard');
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
            ? '<i data-lucide="check"></i> Copied!'
            : '<i data-lucide="x"></i> Failed';
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
      pill.innerHTML = '<i data-lucide="refresh-cw"></i> next in ' + remaining + 's';
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

  /* ── Live command stream auto-refresh ───────────────────── */
  function initStreamRefresh() {
    var node = document.querySelector('[data-stream-refresh-url]');
    if (!node) return;
    var url = node.getAttribute('data-stream-refresh-url');
    var ms = parseInt(node.getAttribute('data-stream-refresh-ms'), 10);
    if (!url || !(ms > 0)) return;
    setTimeout(function () {
      startTopBar();
      window.location.href = url;
    }, ms);
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
      var card = menu.closest('.server-card');
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
    btn.setAttribute('aria-label', 'Back to top');
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
      var isOpen = stored !== 'closed';

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

  /* ── Boot ───────────────────────────────────────────────── */
  function boot() {
    renderIcons();
    initForms();
    initLinks();
    initFlash();
    initSecrets();
    initCopyButtons();
    initCopyTargets();
    initProgressBars();
    initReveal();
    initAutoRefresh();
    initStreamRefresh();
    initManualRefresh();
    initActionMenus();
    initDrawer();
    initBackToTop();
    initShortcuts();
    initCollapsibles();
    initAdvancedToggle();
    renderIcons(); // pick up icons injected by the steps above
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
