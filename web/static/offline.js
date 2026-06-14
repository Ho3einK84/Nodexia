/* Offline fallback page behaviour. Kept separate from app.js so the page stays
 * self-contained and CSP-clean (no inline handlers). */
(function () {
  'use strict';

  function renderIcons() {
    if (typeof lucide !== 'undefined' && lucide.createIcons) {
      try { lucide.createIcons(); } catch (err) { /* ignore */ }
    }
  }

  function boot() {
    renderIcons();
    var retry = document.querySelector('[data-offline-retry]');
    if (retry) {
      retry.addEventListener('click', function () { window.location.reload(); });
    }
    // Auto-recover: when the browser regains connectivity, reload.
    window.addEventListener('online', function () { window.location.reload(); });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
