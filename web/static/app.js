/* Nodexia interactive layer — entrypoint and orchestrator.
 *
 * The Content-Security-Policy forbids inline <script> blocks, so ALL client
 * behaviour is loaded from this module (served from 'self'). Everything is
 * progressive enhancement: if a feature's target elements are absent, it
 * quietly no-ops. Native ES modules only — no bundler, no npm.
 *
 * The behaviour itself lives in five sibling modules; this file just wires it:
 *   core.js   — i18n bridge, byte formatter, icons, top progress bar,
 *               tab-aware navigation/cleanup, button busy state (also publishes
 *               the window.nx* bridges the classic page scripts rely on).
 *   ui.js     — forms, links, flash, secrets, copy, progress, reveal,
 *               back-to-top, shortcuts, collapsibles, advanced toggle.
 *   modals.js — modals, deploy wizard, action menus, node credentials.
 *   stream.js — SSE streams, node results, auto/manual refresh.
 *   shell.js  — home warnings, log drawer, mobile drawer, server cards, PWA.
 */
import { renderIcons, finishTopBar } from './modules/core.js';
import { initForms, initLinks, initFlash, initSecrets, initCopyButtons, initCopyTargets, initProgressBars, initReveal, initBackToTop, initShortcuts, initCollapsibles, initAdvancedToggle } from './modules/ui.js';
import { initNodeCredentials, initActionMenus, initModals, initDeployModal } from './modules/modals.js';
import { initAutoRefresh, initStream, initNodeResult, initBulkStream, initManualRefresh } from './modules/stream.js';
import { initHomeWarnings, initLogDrawer, initDrawer, initServerCards, initServiceWorker, initOrientationLock } from './modules/shell.js';

/* ── Boot: run every page-level init in its established order ──────────── */
function boot() {
  renderIcons();
  [
    initForms, initLinks, initFlash, initSecrets, initCopyButtons, initCopyTargets,
    initNodeCredentials, initProgressBars, initReveal, initAutoRefresh, initStream,
    initHomeWarnings, initNodeResult, initBulkStream, initManualRefresh, initActionMenus,
    initModals, initDeployModal, initLogDrawer, initDrawer, initBackToTop, initShortcuts,
    initCollapsibles, initAdvancedToggle, initServerCards, initServiceWorker, initOrientationLock
  ].forEach(function (init) { init(); });
  renderIcons(); // pick up icons injected by the steps above
}

/* ── v0.6.0: tab-pane rescan bridge ──────────────────────
 * tab-manager.js populates a .tab-pane by DOMParser-extracting another page's
 * markup rather than a real navigation, so none of the boot() inits ever ran
 * against that subtree. rescan() re-runs the idempotent-safe subset, scoped to
 * the given root, so a freshly injected pane gets the same dismiss buttons,
 * secret toggles, copy buttons, modal wiring, collapsibles, server cards and
 * SSE hookups a full page load would have given it — plus a re-render, since
 * DOMParser markup never passed through lucide.
 */
window.NodexiaApp = {
  rescan: function (root) {
    root = root || document;
    renderIcons(root);
    initFlash(root);
    initHomeWarnings(root);
    initSecrets(root);
    initCopyButtons(root);
    initCopyTargets(root);
    initModals(root);
    initDeployModal(root);
    initLogDrawer(root);
    initCollapsibles(root);
    initServerCards(root);
    initStream(root);
    initBulkStream(root);
    initNodeCredentials(root);
    initActionMenus(root);
    initManualRefresh(root);
    initAutoRefresh(root);
    initNodeResult(root);
    initAdvancedToggle(root);
    initProgressBars(root);
    initReveal(root);
    renderIcons(root);
  },
  finishNavigation: function () {
    finishTopBar();
    var overlay = document.getElementById('loading-overlay');
    if (overlay) {
      overlay.hidden = true;
      overlay.classList.remove('is-active');
      overlay.style.display = '';
    }
  }
};

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', boot);
} else {
  boot();
}
