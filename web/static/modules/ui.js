/* Nodexia UI widgets & interactions (native ES module — no bundler, no npm).
 *
 * The page-level behaviour that every view shares: form submit guards, link
 * progress bar, flash banners, masked secrets, copy buttons/targets, progress
 * fills, scroll reveal, back-to-top, keyboard shortcuts, collapsibles and the
 * advanced-panel toggle. Extracted from app.js; the shared primitives (i18n,
 * icons, top bar, tab-aware navigation, busy buttons) come from ./core.js.
 *
 * Every init* function is progressive enhancement and idempotent: absent
 * targets no-op, and the ones that accept `root` can be re-run against a
 * freshly injected tab pane by window.NodexiaApp.rescan().
 */
import { I18N, prefersReducedMotion, markButtonBusy, restoreButton, startTopBar, renderIcons, finishTopBar, tabNavigate } from './core.js';

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
      finishTopBar();
      return;
    }

    startTopBar();
    if (submitter && !submitter.hasAttribute('data-no-loading')) {
      markButtonBusy(submitter);
    }
    if (overlay && form.matches('form[method="post"]')) {
      if (submitter && submitter.hasAttribute('data-no-loading')) return;
      overlay.hidden = false;
      overlay.classList.add('is-active');
      // Safety net: if the overlay is still visible after 15 seconds, hide it.
      // This covers edge cases where the tab system's fetch hangs or the
      // restoreFormUI callback never fires (e.g. an unhandled promise rejection).
      setTimeout(function () {
        if (!overlay.hidden || overlay.classList.contains('is-active')) {
          overlay.hidden = true;
          overlay.classList.remove('is-active');
          overlay.style.display = '';
        }
      }, 15000);
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
function initFlash(root) {
  root = root || document;
  root.querySelectorAll('.flash-banner').forEach(function (banner) {
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
    if (banner.classList.contains('flash-banner--success') || banner.classList.contains('flash-banner--ok')) {
      setTimeout(function () { dismissFlash(banner); }, 6000);
    }
  });
}
function dismissFlash(banner) {
  banner.classList.add('is-leaving');
  setTimeout(function () { banner.remove(); }, 300);
}

/* ── Masked secrets with reveal toggle ──────────────────── */
function initSecrets(root) {
  root = root || document;
  root.querySelectorAll('[data-secret]').forEach(function (el) {
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
function initCopyButtons(root) {
  root = root || document;
  root.querySelectorAll('pre.output-block').forEach(function (pre) {
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
function initCopyTargets(root) {
  root = root || document;
  root.querySelectorAll('[data-copy-target]').forEach(function (btn) {
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
function initProgressBars(root) {
  root = root || document;
  var fills = root.querySelectorAll('.mini-metric__fill');
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
function initReveal(root) {
  root = root || document;
  var targets = root.querySelectorAll(
    '.card, .server-card, .stat-card, .dashboard-item, .node-card, .empty-state-card');
  if (!targets.length) return;
  targets.forEach(function (el) {
    el.classList.remove('reveal');
    el.classList.add('reveal-in');
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
      if (e.key === 'o') { tabNavigate('/'); return; }
      if (e.key === 's') { tabNavigate('/servers'); return; }
      if (e.key === 'd') { tabNavigate('/ops/diagnostics'); return; }
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
        tabNavigate(refresh.getAttribute('data-refresh-now') ||
          refresh.getAttribute('href'));
      }
    }
  });
}

/* ── Collapsible sections (persisted open/closed) ───────── */
function setCollapseState(el, toggle, body, isOpen) {
  var icon = toggle ? toggle.querySelector('.collapsible__icon') : null;
  if (icon) icon.style.transform = isOpen ? 'rotate(90deg)' : 'rotate(0deg)';
  if (toggle) toggle.setAttribute('aria-expanded', isOpen ? 'true' : 'false');
  el.classList.toggle('is-collapsed', !isOpen);
  body.hidden = !isOpen;
}
function initCollapsibles(root) {
  root = root || document;
  root.querySelectorAll('.collapsible').forEach(function (el) {
    if (el.dataset.collapsibleReady === '1') return;
    var toggle = el.querySelector('.collapsible__toggle');
    var body = el.querySelector('.collapsible__content');
    if (!toggle || !body) return;
    el.dataset.collapsibleReady = '1';

    var key = toggle.getAttribute('data-collapse-key') || '';
    var stored = null;
    if (key) { try { stored = localStorage.getItem('collapse_' + key); } catch (err) {} }
    // A stored preference always wins; otherwise honour data-collapse-default
    // ("closed" starts collapsed), defaulting to open.
    var defaultClosed = toggle.getAttribute('data-collapse-default') === 'closed';
    var isOpen = stored ? stored !== 'closed' : !defaultClosed;

    setCollapseState(el, toggle, body, isOpen);

    toggle.setAttribute('type', 'button');
    toggle.addEventListener('click', function () {
      var nextOpen = body.hidden || el.classList.contains('is-collapsed');
      setCollapseState(el, toggle, body, nextOpen);
      if (key) { try { localStorage.setItem('collapse_' + key, nextOpen ? 'open' : 'closed'); } catch (err) {} }
    });
  });
}

/* ── Advanced panel toggle ──────────────────────────────── */
function initAdvancedToggle(root) {
  root = root || document;
  root.querySelectorAll('.advanced-toggle[data-target]').forEach(function (btn) {
    if (btn.dataset.advancedReady === '1') return;
    btn.dataset.advancedReady = '1';
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
export { initForms, initLinks, initFlash, initSecrets, initCopyButtons, initCopyTargets, initProgressBars, initReveal, initBackToTop, initShortcuts, initCollapsibles, initAdvancedToggle, copyText };
