/* Nodexia core utilities (native ES module — no bundler, no npm).
 *
 * Everything the rest of the client layer shares lives here: the i18n bridge,
 * the byte formatter, icon rendering, the top progress bar, tab-aware
 * navigation/cleanup helpers and the button busy state. app.js imports these
 * and page scripts keep consuming the window.nx* globals set up below, so the
 * load order (module executes before DOMContentLoaded) stays transparent to
 * them.
 */

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

/* Shared byte formatter for page-specific scripts. Labels use the concise
 * B/KB/MB/GB/TB/PB UI convention while scaling remains base 1024. */
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

var prefersReducedMotion = window.matchMedia &&
  window.matchMedia('(prefers-reduced-motion: reduce)').matches;

/* ── Icons ──────────────────────────────────────────────
 * Lucide is vendored locally (/static/lucide.min.js, loaded with `defer`).
 * renderIcons() is safe to call repeatedly and after dynamic DOM inserts.
 */
function renderIcons(root) {
  if (typeof lucide === 'undefined' || !lucide.createIcons) return;
  try {
    var scope = root && root.querySelector ? root : document;
    if (!scope.querySelector('[data-lucide]')) return;
    if (root && root !== document) {
      lucide.createIcons({ root: root });
    } else {
      lucide.createIcons();
    }
  } catch (err) {
    try { lucide.createIcons(); } catch (e) { /* never let an icon failure break the page */ }
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

/* ── v0.6.3: tab-aware navigation + cleanup helpers ────
 * tabNavigate / tabReload replace bare window.location.href / .reload()
 * calls so auto-refresh, manual refresh, node-result dismiss, and install
 * reload-all stay scoped to the active tab pane instead of blowing away the
 * entire multi-tab shell.  registerCleanup lets init functions attach
 * teardown callbacks (EventSource.close, clearInterval, …) to a pane
 * element; tab-manager.js calls them before replacing or closing the pane. */
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
window.addEventListener('pageshow', finishTopBar);

/* ── Button busy state ──────────────────────────────────
 * Disables the clicked submit button and swaps in a spinner so double
 * submits are prevented and slow SSH actions feel responsive.
 */
function markButtonBusy(btn) {
  if (!btn || btn.dataset.busy === '1') return;
  btn.dataset.busy = '1';
  btn.classList.add('is-busy');
  btn.dataset.busyHtml = btn.innerHTML;
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
  if (btn.dataset.busyHtml !== undefined) {
    btn.innerHTML = btn.dataset.busyHtml;
    delete btn.dataset.busyHtml;
    renderIcons(btn);
  } else {
    var label = btn.getAttribute('data-busy-label');
    if (label) { btn.textContent = label; }
  }
  btn.style.minWidth = '';
}

export { I18N, humanBytes, prefersReducedMotion, renderIcons, startTopBar, finishTopBar, tabNavigate, tabReload, registerCleanup, markButtonBusy, restoreButton };
