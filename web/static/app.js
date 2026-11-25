/* Nodexia interactive layer — entrypoint and lazy module orchestrator.
 *
 * Zero-build native ES modules (strict CSP, no inline scripts).
 * Heavy domain modules (`nodes.js`, `log-drawer.js`, `stream.js`) are loaded
 * lazily via native dynamic `import()` ONLY when their DOM hooks exist in the
 * page or freshly injected tab pane.
 */
import { renderIcons, finishTopBar, cancelLoadingOverlay } from './modules/core.js';
import {
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
  initPageInfo
} from './modules/ui.js';
import { initActionMenus, initModals } from './modules/modals.js';
import {
  initHomeWarnings,
  initDrawer,
  initBottomNav,
  initServerCards,
  initAddServerTriggers,
  initServiceWorker,
  initOrientationLock,
  initDesktopSidebar
} from './modules/shell.js';

var STREAM_SEL = '[data-stream-sse-url], [data-node-result], [data-bulk-sse-url], #auto-refresh-select, [data-auto-refresh], [data-refresh-now]';

function initDomainModules(root) {
  var scope = root || document;
  if (!scope.querySelector) return;

  if (scope.querySelector(STREAM_SEL)) {
    import('./modules/stream.js').then(function (m) {
      m.initAutoRefresh(scope);
      m.initStream(scope);
      m.initNodeResult(scope);
      m.initBulkStream(scope);
      m.initManualRefresh(scope);
    }).catch(function () {});
  }

  if (scope.querySelector('[data-log-drawer], [data-log-drawer-open]')) {
    import('./modules/log-drawer.js').then(function (m) {
      m.initLogDrawer(scope);
    }).catch(function () {});
  }

  if (scope.querySelector('[data-nodes-workspace]')) {
    import('./modules/nodes.js').then(function (m) {
      m.initNodes(scope);
    }).catch(function () {});
  }

  if (scope.querySelector('[data-server-form]')) {
    import('./modules/server-modal.js').then(function (m) {
      m.initServerModal(scope);
    }).catch(function () {});
  }
}

/* ── Boot: run lightweight shell + widget inits, then one scoped icon pass ── */
function boot() {
  initForms();
  initLinks();
  initFlash();
  initSecrets();
  initCopyButtons();
  initCopyTargets();
  initProgressBars();
  initReveal();
  initHomeWarnings();
  initActionMenus();
  initModals();
  initDrawer();
  initBottomNav();
  initDesktopSidebar();
  initAddServerTriggers();
  initBackToTop();
  initShortcuts();
  initCollapsibles();
  initAdvancedToggle();
  initPageInfo();
  initServerCards();
  initServiceWorker();
  initOrientationLock();
  renderIcons(document);
  initDomainModules(document);
}

/* ── Tab-pane rescan bridge (called by tab-manager.js) ────────────────────── */
window.NodexiaApp = {
  rescan: function (root) {
    root = root || document;
    initFlash(root);
    initHomeWarnings(root);
    initSecrets(root);
    initCopyButtons(root);
    initCopyTargets(root);
    initModals(root);
    initCollapsibles(root);
    initPageInfo(root);
    initServerCards(root);
    initActionMenus(root);
    initAdvancedToggle(root);
    initProgressBars(root);
    initReveal(root);
    renderIcons(root);
    initDomainModules(root);
  },
  finishNavigation: function () {
    finishTopBar();
    cancelLoadingOverlay();
  }
};

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', boot);
} else {
  boot();
}
