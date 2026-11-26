/* Nodexia core utilities (native ES module — no bundler, no npm).
 *
 * Shared primitives for ES modules and classic page scripts (via window.nx*):
 *   - I18N (`nxT`, `nxTn`), `humanBytes` (`nxHumanBytes`), `escapeHTML` (`nxEscape`)
 *   - `getCSRF` (`nxCSRF`), `paneScope` (`nxPane`), `el` (`nxEl`), `showToast` (`nxToast`)
 *   - Strictly scoped `renderIcons` (`nxRenderIcons`) — never full-DOM fallback on subtree
 *   - Top progress bar, delayed loading overlay cancellation, tab-aware nav & cleanup, busy buttons
 */

/* ── Client-side i18n ─────────────────────────────────────────────────────── */
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

/* ── Shared formatters, escaping, CSRF, DOM factory & pane scope ──────────── */
function humanBytes(bytes) {
  if (!bytes || bytes < 0) bytes = 0;
  var units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  var size = bytes;
  var unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex++;
  }
  return size.toFixed(size >= 100 || unitIndex === 0 ? 0 : 1) + ' ' + units[unitIndex];
}
window.nxHumanBytes = humanBytes;

var ESC_MAP = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
function escapeHTML(value) {
  return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) { return ESC_MAP[c]; });
}
window.nxEscape = escapeHTML;

function setCSRF(token) {
  if (!token) return;
  window.__nxCSRFToken = token;
  document.querySelectorAll('input[name="_csrf_token"]').forEach(function (input) {
    input.value = token;
  });
}
window.nxSetCSRF = setCSRF;

function getCSRF(root) {
  if (window.__nxCSRFToken) return window.__nxCSRFToken;
  var scope = (root && root.querySelector) ? root : document;
  var holder = scope.querySelector('[data-csrf-token], [data-csrf]') ||
    (scope !== document ? document.querySelector('[data-csrf-token], [data-csrf]') : null);
  if (holder) {
    var attr = holder.getAttribute('data-csrf-token') || holder.getAttribute('data-csrf');
    if (attr) return attr;
  }
  var input = scope.querySelector('input[name="_csrf_token"]') ||
    (scope !== document ? document.querySelector('input[name="_csrf_token"]') : null);
  return input ? (input.value || '') : '';
}
window.nxCSRF = getCSRF;

function appendChildren(parent, children) {
  if (children == null || children === false) return;
  if (Array.isArray(children)) {
    children.forEach(function (child) { appendChildren(parent, child); });
  } else if (typeof children === 'string' || typeof children === 'number') {
    parent.appendChild(document.createTextNode(String(children)));
  } else if (children.nodeType) {
    parent.appendChild(children);
  }
}

function el(tag, attrs, children) {
  var node = document.createElement(tag);
  if (attrs && typeof attrs === 'object' && !Array.isArray(attrs) && !attrs.nodeType) {
    Object.keys(attrs).forEach(function (k) {
      var v = attrs[k];
      if (v == null || v === false) return;
      if (k === 'class' || k === 'className') node.className = v;
      else if (k === 'text') node.textContent = String(v);
      else if (k === 'dataset' && typeof v === 'object') {
        Object.keys(v).forEach(function (dk) { if (v[dk] != null) node.dataset[dk] = String(v[dk]); });
      } else if (k === 'on' && typeof v === 'object') {
        Object.keys(v).forEach(function (ev) { node.addEventListener(ev, v[ev]); });
      } else if (v === true) node.setAttribute(k, '');
      else node.setAttribute(k, String(v));
    });
  } else if (children === undefined) {
    children = attrs;
  }
  appendChildren(node, children);
  return node;
}
window.nxEl = el;

function paneScope(scriptEl) {
  var pane = (scriptEl && scriptEl.closest) ? scriptEl.closest('.tab-pane') : null;
  var root = pane || document;
  return {
    pane: pane,
    root: root,
    isScoped: !!pane,
    byId: function (id) { return pane ? pane.querySelector('#' + id) : document.getElementById(id); },
    onCleanup: function (fn) { registerCleanup(root, fn); }
  };
}
window.nxPane = paneScope;

var prefersReducedMotion = window.matchMedia &&
  window.matchMedia('(prefers-reduced-motion: reduce)').matches;

/* ── Unified Application Toast Stack ──────────────────────────────────────── */
var toastRegion = null;
function ensureToastRegion() {
  if (toastRegion && document.body.contains(toastRegion)) return toastRegion;
  toastRegion = document.querySelector('.nx-toast-region');
  if (!toastRegion) {
    toastRegion = el('div', { class: 'nx-toast-region', 'aria-live': 'polite', 'aria-atomic': 'false' });
    document.body.appendChild(toastRegion);
  }
  return toastRegion;
}

function showToast(message, kind, durationMs) {
  if (!message) return;
  var region = ensureToastRegion();
  var tone = { ok: 'ok', success: 'ok', warn: 'warn', warning: 'warn', error: 'error', err: 'error', info: 'info' }[kind || 'info'] || 'info';
  var iconName = tone === 'ok' ? 'check-circle-2' : tone === 'warn' ? 'alert-triangle' : tone === 'error' ? 'alert-circle' : 'info';

  while (region.childElementCount >= 4) {
    region.firstElementChild.remove();
  }

  var toast = el('div', { class: 'nx-toast nx-toast--' + tone, role: tone === 'error' ? 'alert' : 'status' }, [
    el('i', { 'data-lucide': iconName, 'aria-hidden': 'true' }),
    el('span', { text: message })
  ]);
  region.appendChild(toast);
  renderIcons(toast);

  requestAnimationFrame(function () { toast.classList.add('is-visible'); });

  var ttl = typeof durationMs === 'number' ? durationMs : (tone === 'error' ? 4600 : 3200);
  setTimeout(function () {
    toast.classList.remove('is-visible');
    setTimeout(function () { toast.remove(); }, prefersReducedMotion ? 0 : 220);
  }, ttl);
}
window.nxToast = showToast;

/* ── Strictly Scoped Lucide Icon Renderer ─────────────────────────────────── */
function renderIcons(root) {
  if (typeof lucide === 'undefined' || !lucide.createIcons) return;
  var scope = (root && root.querySelector) ? root : document;
  if (!scope.querySelector('[data-lucide]')) return;
  try {
    if (root && root !== document) {
      lucide.createIcons({ root: root });
    } else {
      lucide.createIcons();
    }
  } catch (err) { /* never let an icon failure break interaction */ }
}
window.nxRenderIcons = renderIcons;

/* ── Top Progress Bar & Delayed Loading Overlay ───────────────────────────── */
var topBar = null;
var topBarTimer = null;
var overlayDelayTimer = null;
var overlaySafetyTimer = null;

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
  var progress = 8;
  bar.style.transform = 'scaleX(' + (progress / 100).toFixed(3) + ')';
  clearInterval(topBarTimer);
  topBarTimer = setInterval(function () {
    progress += (90 - progress) * 0.12;
    bar.style.transform = 'scaleX(' + (progress / 100).toFixed(3) + ')';
  }, 220);
}

function finishTopBar() {
  if (!topBar) return;
  clearInterval(topBarTimer);
  topBar.style.transform = 'scaleX(1)';
  topBar.classList.add('is-done');
  setTimeout(function () {
    if (topBar) {
      topBar.classList.remove('is-active', 'is-done');
      topBar.style.transform = 'scaleX(0)';
    }
  }, 220);
}

function scheduleLoadingOverlay(delayMs) {
  var overlay = document.getElementById('loading-overlay');
  if (!overlay) return;
  cancelLoadingOverlay();
  overlayDelayTimer = setTimeout(function () {
    overlayDelayTimer = null;
    overlay.hidden = false;
    overlay.classList.add('is-active');
    overlaySafetyTimer = setTimeout(cancelLoadingOverlay, 15000);
  }, typeof delayMs === 'number' ? delayMs : 180);
}

function cancelLoadingOverlay() {
  if (overlayDelayTimer) { clearTimeout(overlayDelayTimer); overlayDelayTimer = null; }
  if (overlaySafetyTimer) { clearTimeout(overlaySafetyTimer); overlaySafetyTimer = null; }
  var overlay = document.getElementById('loading-overlay');
  if (overlay) {
    overlay.hidden = true;
    overlay.classList.remove('is-active');
    overlay.style.display = '';
  }
}

/* ── Tab-Aware Navigation & Pane Cleanup ──────────────────────────────────── */
function tabNavigate(url) {
  if (window.NodexiaTabs && typeof window.NodexiaTabs.navigate === 'function') {
    window.NodexiaTabs.navigate(url);
  } else {
    window.location.href = url;
  }
}

function tabReload() {
  if (window.NodexiaTabs && typeof window.NodexiaTabs.reloadActive === 'function') {
    window.NodexiaTabs.reloadActive();
  } else {
    window.location.reload();
  }
}

function registerCleanup(root, fn) {
  if (!root || typeof fn !== 'function') return;
  if (!root.__nxCleanups) root.__nxCleanups = [];
  root.__nxCleanups.push(fn);
}

window.addEventListener('pageshow', function () {
  finishTopBar();
  cancelLoadingOverlay();
});

/* ── Button Busy State ────────────────────────────────────────────────────── */
function markButtonBusy(btn) {
  if (!btn || btn.dataset.busy === '1') return;
  btn.dataset.busy = '1';
  btn.classList.add('is-busy');
  btn.dataset.busyHtml = btn.innerHTML;
  var label = btn.textContent.trim();
  if (label) btn.setAttribute('data-busy-label', label);
  btn.style.minWidth = btn.offsetWidth + 'px';
  btn.innerHTML = '<span class="spinner spinner--sm" aria-hidden="true"></span> ' + escapeHTML(I18N.t('js.working'));
  setTimeout(function () { restoreButton(btn); }, 30000);
}

function restoreButton(btn) {
  if (!btn || btn.dataset.busy !== '1') return;
  btn.dataset.busy = '';
  btn.classList.remove('is-busy');
  if (btn.dataset.busyHtml !== undefined) {
    btn.innerHTML = btn.dataset.busyHtml;
    delete btn.dataset.busyHtml;
    renderIcons(btn);
  } else {
    var label = btn.getAttribute('data-busy-label');
    if (label) btn.textContent = label;
  }
  btn.style.minWidth = '';
}

export {
  I18N,
  humanBytes,
  escapeHTML,
  getCSRF,
  setCSRF,
  el,
  paneScope,
  showToast,
  prefersReducedMotion,
  renderIcons,
  startTopBar,
  finishTopBar,
  scheduleLoadingOverlay,
  cancelLoadingOverlay,
  tabNavigate,
  tabReload,
  registerCleanup,
  markButtonBusy,
  restoreButton
};
