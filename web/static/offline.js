/* Offline fallback page behaviour. Kept separate from app.js so the page stays
 * self-contained, lightweight (no lucide bundle), and CSP-clean. */
(function () {
  'use strict';

  function boot() {
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
