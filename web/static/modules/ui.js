/* Nodexia UI widgets & interactions (native ES module — no bundler, no npm).
 *
 * Shared page-level behaviors: form submit guards (180ms delayed overlay),
 * link progress bar, flash banners, masked secrets, copy buttons/targets,
 * GPU-composited progress bars, back-to-top (rAF), shortcuts, collapsibles,
 * advanced-panel toggle, server form strategy, and page-info popovers (rAF).
 */
import {
  I18N,
  prefersReducedMotion,
  registerCleanup,
  markButtonBusy,
  startTopBar,
  renderIcons,
  finishTopBar,
  scheduleLoadingOverlay,
  tabNavigate
} from './core.js';

/* ── Loading overlay (180ms delay) + confirm + busy on form submit ────────── */
function initForms() {
  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form.matches || !form.matches('form')) return;
    if (e.defaultPrevented) return;

    var submitter = e.submitter ||
      form.querySelector('button[type="submit"]:focus') ||
      form.querySelector('button[type="submit"]');

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
    var skipLoading = submitter && submitter.hasAttribute('data-no-loading');
    if (submitter && !skipLoading) markButtonBusy(submitter);
    if (!skipLoading && form.matches('form[method="post"]')) {
      scheduleLoadingOverlay(180);
    }
  });
}

/* ── Same-origin link navigation → progress bar ───────────────────────────── */
function initLinks() {
  document.addEventListener('click', function (e) {
    if (e.defaultPrevented) return;
    var a = e.target.closest && e.target.closest('a[href]');
    if (!a || a.target === '_blank' || a.hasAttribute('download') || a.hasAttribute('data-modal-open') || a.hasAttribute('data-add-server-trigger')) return;
    if ((a.getAttribute('href') || '').charAt(0) === '#') return;
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var url;
    try { url = new URL(a.href, window.location.href); } catch (err) { return; }
    if (url.origin !== window.location.origin) return;
    startTopBar();
  });
}

/* ── Flash banners: dismissible + auto-dismiss ────────────────────────────── */
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

    if (banner.classList.contains('flash-banner--success') || banner.classList.contains('flash-banner--ok')) {
      setTimeout(function () { dismissFlash(banner); }, 6000);
    }
  });
}

function dismissFlash(banner) {
  banner.classList.add('is-leaving');
  setTimeout(function () { banner.remove(); }, 300);
}

/* ── Masked secrets with reveal toggle ────────────────────────────────────── */
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
      renderIcons(toggle);
    });
    if (el.nextSibling) el.parentNode.insertBefore(toggle, el.nextSibling);
    else el.parentNode.appendChild(toggle);
  });
}

/* ── Copy-to-clipboard for output blocks & explicit targets ───────────────── */
function initCopyButtons(root) {
  root = root || document;
  root.querySelectorAll('pre.output-block').forEach(function (pre) {
    if (pre.parentElement && pre.parentElement.classList.contains('output-wrap')) return;
    if (pre.hasAttribute('data-no-auto-copy') || !pre.textContent.trim()) return;

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
        btn.innerHTML = ok ? '<i data-lucide="check"></i>' : '<i data-lucide="x"></i>';
        renderIcons(btn);
        setTimeout(function () {
          btn.classList.remove('is-copied', 'is-failed');
          btn.innerHTML = '<i data-lucide="copy"></i>';
          renderIcons(btn);
        }, 1600);
      });
    });
    wrap.appendChild(btn);
  });
}

function initCopyTargets(root) {
  root = root || document;
  root.querySelectorAll('[data-copy-target]').forEach(function (btn) {
    if (btn.dataset.copyReady === '1') return;
    btn.dataset.copyReady = '1';
    var label = btn.innerHTML;
    btn.addEventListener('click', function () {
      var selector = btn.getAttribute('data-copy-target');
      var container = btn.closest('.node-section, .node-cred-block, .node-register-card, .deploy-end-card, .tab-pane') || root;
      var target = (selector && container && container.querySelector ? container.querySelector(selector) : null) ||
        (selector && root && root.querySelector ? root.querySelector(selector) : null) ||
        (selector ? document.querySelector(selector) : null);
      if (!target) return;
      var value = target.dataset.secretValue != null ? target.dataset.secretValue : target.textContent;
      copyText(value.replace(/\n$/, '')).then(function (ok) {
        btn.classList.add(ok ? 'is-copied' : 'is-failed');
        btn.innerHTML = ok
          ? '<i data-lucide="check"></i> ' + I18N.t('js.copy.copied')
          : '<i data-lucide="x"></i> ' + I18N.t('js.failed');
        renderIcons(btn);
        setTimeout(function () {
          btn.classList.remove('is-copied', 'is-failed');
          btn.innerHTML = label;
          renderIcons(btn);
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

/* ── GPU-composited progress bars (zero forced reflow) ────────────────────── */
function initProgressBars(root) {
  root = root || document;
  if (prefersReducedMotion) return;
  var fills = root.querySelectorAll('.mini-metric__fill');
  if (!fills.length) return;
  fills.forEach(function (fill) {
    if (fill.dataset.barReady === '1') return;
    fill.dataset.barReady = '1';
    var targetPct = fill.style.getPropertyValue('--pct');
    if (targetPct !== '') {
      fill.style.setProperty('--pct', '0');
      requestAnimationFrame(function () {
        requestAnimationFrame(function () { fill.style.setProperty('--pct', targetPct); });
      });
    }
  });
}

function initReveal(root) {
  root = root || document;
  root.querySelectorAll('.card, .server-card, .stat-card, .dashboard-item, .node-card, .empty-state-card').forEach(function (el) {
    el.classList.remove('reveal');
    el.classList.add('reveal-in');
  });
}

/* ── Back-to-top button (rAF-throttled scroll) ────────────────────────────── */
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

  var raf = 0;
  function update() {
    raf = 0;
    btn.classList.toggle('is-visible', window.scrollY > 400);
  }
  window.addEventListener('scroll', function () {
    if (!raf) raf = requestAnimationFrame(update);
  }, { passive: true });
  update();
}

/* ── Keyboard shortcuts ───────────────────────────────────────────────────── */
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
    var activeRoot = document.querySelector('.tab-pane:not([hidden])') || document;
    if (e.key === '/') {
      var input = activeRoot.querySelector('input[type="text"], input[type="search"], textarea');
      if (input) { e.preventDefault(); input.focus(); }
      return;
    }
    if (e.key === 'r') {
      var refresh = activeRoot.querySelector('[data-refresh-now], [data-nodes-refresh-btn]') ||
        activeRoot.querySelector('a[href*="refresh=1"]');
      if (refresh) {
        e.preventDefault();
        if (refresh.tagName === 'BUTTON' || refresh.hasAttribute('data-nodes-refresh-btn')) {
          refresh.click();
        } else {
          startTopBar();
          tabNavigate(refresh.getAttribute('data-refresh-now') || refresh.getAttribute('href'));
        }
      }
    }
  });
}

/* ── Collapsible sections (persisted open/closed) ─────────────────────────── */
function setCollapseState(el, toggle, body, isOpen) {
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

/* ── Advanced panel toggle ────────────────────────────────────────────────── */
function initAdvancedToggle(root) {
  root = root || document;
  root.querySelectorAll('.advanced-toggle[data-target]').forEach(function (btn) {
    if (btn.dataset.advancedReady === '1') return;
    btn.dataset.advancedReady = '1';
    btn.addEventListener('click', function () {
      var targetKey = btn.getAttribute('data-target');
      var container = btn.closest('form, .tab-pane') || root;
      var target = (container && targetKey && container.querySelector('[data-advanced-panel="' + targetKey + '"]')) ||
        (container && targetKey && container.querySelector('#' + targetKey)) ||
        (targetKey ? document.getElementById(targetKey) : null);
      if (!target) return;
      var isHidden = target.hasAttribute('hidden');
      target.hidden = !isHidden;
      btn.classList.toggle('is-active', isHidden);
      btn.setAttribute('aria-expanded', isHidden ? 'true' : 'false');
    });
  });
}

/* ── Page header info popover (rAF-throttled repositioning) ───────────────── */
var pageInfoDocWired = false;
var allPageInfos = new Set();
var pageInfoRAF = 0;

function positionPageInfo(wrap) {
  var trigger = wrap.querySelector('[data-page-info-trigger]');
  var popover = wrap.querySelector('[data-page-info-popover]');
  if (!trigger || !popover) return;

  var rect = trigger.getBoundingClientRect();
  var vw = document.documentElement.clientWidth || window.innerWidth || 360;
  var vh = document.documentElement.clientHeight || window.innerHeight || 640;
  var isRTL = (document.documentElement.getAttribute('dir') || '').toLowerCase() === 'rtl';
  var margin = 12;

  var centerFromStart = isRTL ? (vw - (rect.left + rect.width / 2)) : (rect.left + rect.width / 2);
  var popW = popover.offsetWidth || Math.min(340, vw - margin * 2);
  var maxStart = Math.max(margin, vw - popW - margin);
  var startOffset = Math.min(maxStart, Math.max(margin, Math.round(centerFromStart - popW / 2)));
  var arrowOffset = Math.min(Math.max(16, Math.round(popW - 16)), Math.max(16, Math.round(centerFromStart - startOffset)));

  var popH = popover.offsetHeight || 64;
  var topOffset = Math.round(rect.bottom + 10);
  var above = false;
  if (topOffset + popH > vh - margin && rect.top - popH - 10 >= margin) {
    topOffset = Math.round(rect.top - popH - 10);
    above = true;
  }

  popover.classList.toggle('page-info__popover--above', above);
  popover.style.setProperty('--info-top', topOffset + 'px');
  popover.style.setProperty('--info-start', startOffset + 'px');
  popover.style.setProperty('--info-arrow', arrowOffset + 'px');
}

function setPageInfoOpen(wrap, open, pinned) {
  var trigger = wrap.querySelector('[data-page-info-trigger]');
  var popover = wrap.querySelector('[data-page-info-popover]');
  if (!trigger || !popover) return;

  clearTimeout(wrap._hoverTimer);
  clearTimeout(wrap._hideTimer);

  if (open) {
    wrap.dataset.pinned = pinned ? '1' : '0';
    wrap.classList.add('is-open');
    trigger.setAttribute('aria-expanded', 'true');
    popover.hidden = false;
    positionPageInfo(wrap);
    if (prefersReducedMotion) {
      popover.classList.add('is-open');
    } else {
      requestAnimationFrame(function () {
        if (wrap.classList.contains('is-open')) popover.classList.add('is-open');
      });
    }
    return;
  }

  wrap.dataset.pinned = '0';
  wrap.classList.remove('is-open');
  trigger.setAttribute('aria-expanded', 'false');
  popover.classList.remove('is-open');

  var hidePopover = function () { if (!popover.classList.contains('is-open')) popover.hidden = true; };
  if (prefersReducedMotion) hidePopover();
  else wrap._hideTimer = setTimeout(hidePopover, 160);
}

function closeAllPageInfo(except) {
  allPageInfos.forEach(function (wrap) {
    if (!document.contains(wrap)) { allPageInfos.delete(wrap); return; }
    if (wrap !== except && (wrap.classList.contains('is-open') || wrap.dataset.pinned === '1')) {
      setPageInfoOpen(wrap, false, false);
    }
  });
}

function syncOpenPageInfos(closeIfOffscreen) {
  var hasOpen = false;
  allPageInfos.forEach(function (wrap) {
    if (wrap.classList.contains('is-open')) hasOpen = true;
  });
  if (!hasOpen) return;

  var vh = document.documentElement.clientHeight || window.innerHeight || 640;
  allPageInfos.forEach(function (wrap) {
    if (!document.contains(wrap)) { allPageInfos.delete(wrap); return; }
    if (!wrap.classList.contains('is-open')) return;
    var trigger = wrap.querySelector('[data-page-info-trigger]');
    if (!trigger) return;
    var rect = trigger.getBoundingClientRect();
    if (closeIfOffscreen && (rect.bottom < 0 || rect.top > vh || rect.width === 0)) {
      setPageInfoOpen(wrap, false, false);
      return;
    }
    positionPageInfo(wrap);
  });
}

function scheduleSyncPageInfos() {
  if (pageInfoRAF) return;
  pageInfoRAF = requestAnimationFrame(function () {
    pageInfoRAF = 0;
    syncOpenPageInfos(true);
  });
}

function initPageInfo(root) {
  root = root || document;
  var items = root.querySelectorAll('[data-page-info]');
  if (!items.length) return;

  items.forEach(function (wrap) {
    if (wrap.dataset.pageInfoReady === '1') return;
    wrap.dataset.pageInfoReady = '1';
    allPageInfos.add(wrap);

    var trigger = wrap.querySelector('[data-page-info-trigger]');
    var popover = wrap.querySelector('[data-page-info-popover]');
    if (!trigger || !popover) return;

    trigger.addEventListener('click', function (e) {
      e.preventDefault();
      e.stopPropagation();
      if (wrap.dataset.pinned === '1') {
        setPageInfoOpen(wrap, false, false);
      } else {
        closeAllPageInfo(wrap);
        setPageInfoOpen(wrap, true, true);
      }
    });

    var scheduleHoverClose = function () {
      clearTimeout(wrap._hoverTimer);
      wrap._hoverTimer = setTimeout(function () {
        if (wrap.dataset.pinned !== '1') setPageInfoOpen(wrap, false, false);
      }, 110);
    };

    var cancelHoverClose = function (e) {
      if (e.pointerType && e.pointerType !== 'mouse') return;
      clearTimeout(wrap._hoverTimer);
      if (!wrap.classList.contains('is-open')) {
        closeAllPageInfo(wrap);
        setPageInfoOpen(wrap, true, false);
      }
    };

    trigger.addEventListener('pointerenter', cancelHoverClose);
    trigger.addEventListener('pointerleave', function (e) { if (!e.pointerType || e.pointerType === 'mouse') scheduleHoverClose(); });
    popover.addEventListener('pointerenter', function (e) { if (!e.pointerType || e.pointerType === 'mouse') clearTimeout(wrap._hoverTimer); });
    popover.addEventListener('pointerleave', function (e) { if (!e.pointerType || e.pointerType === 'mouse') scheduleHoverClose(); });

    var owningPane = wrap.closest('.tab-pane');
    if (owningPane) {
      registerCleanup(owningPane, function () {
        setPageInfoOpen(wrap, false, false);
        allPageInfos.delete(wrap);
      });
    }
  });

  if (pageInfoDocWired) return;
  pageInfoDocWired = true;

  document.addEventListener('click', function (e) {
    if (e.target.closest && e.target.closest('[data-page-info]')) return;
    closeAllPageInfo(null);
  });

  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape') return;
    var activeWrap = document.activeElement && document.activeElement.closest
      ? document.activeElement.closest('[data-page-info]')
      : null;
    closeAllPageInfo(null);
    if (activeWrap) {
      var btn = activeWrap.querySelector('[data-page-info-trigger]');
      if (btn && btn.focus) btn.focus();
    }
  });

  window.addEventListener('resize', scheduleSyncPageInfos);
  document.addEventListener('scroll', scheduleSyncPageInfos, { passive: true, capture: true });
}

export {
  initForms,
  initLinks,
  initFlash,
  initSecrets,
  initCopyButtons,
  initCopyTargets,
  initProgressBars,
  initReveal,
  initBackToTop,
  initShortcuts,
  initCollapsibles,
  initAdvancedToggle,
  initPageInfo,
  copyText
};
