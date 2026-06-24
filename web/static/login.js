/* Login page enhancements, loaded only on /login via PageScripts.
 *
 * Progressive enhancement only: the form works without this file. It adds a
 * password reveal toggle and a subtle pointer/scroll parallax on the animated
 * background orbs. Strings are resolved through window.nxT (exposed by app.js,
 * which loads first) so they stay localized; lucide.createIcons re-renders any
 * injected <i data-lucide> glyph. CSP-safe: external file under script-src 'self'.
 */
(function () {
  'use strict';

  var prefersReducedMotion = window.matchMedia &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  function t(key, fallback) {
    if (typeof window.nxT === 'function') {
      var v = window.nxT(key);
      if (v && v !== key) return v;
    }
    return fallback;
  }

  function renderIcons() {
    if (typeof lucide !== 'undefined' && lucide.createIcons) {
      try { lucide.createIcons(); } catch (err) { /* never break the page */ }
    }
  }

  /* ── Password reveal toggle ─────────────────────────────── */
  function initReveal() {
    var input = document.querySelector('.auth__field--reveal input[type="password"]');
    if (!input || input.dataset.revealReady === '1') return;
    input.dataset.revealReady = '1';

    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'auth__reveal';
    btn.setAttribute('aria-label', t('js.secret.reveal', 'Show'));
    btn.innerHTML = '<i data-lucide="eye"></i>';

    var shown = false;
    btn.addEventListener('click', function () {
      shown = !shown;
      input.type = shown ? 'text' : 'password';
      btn.setAttribute('aria-label', shown ? t('js.secret.hide', 'Hide') : t('js.secret.reveal', 'Show'));
      btn.innerHTML = shown ? '<i data-lucide="eye-off"></i>' : '<i data-lucide="eye"></i>';
      renderIcons();
      input.focus();
    });

    input.parentNode.appendChild(btn);
    renderIcons();
  }

  /* ── Pointer / scroll parallax on the background orbs ───── */
  function initParallax() {
    if (prefersReducedMotion) return;
    var orbs = document.querySelectorAll('.auth__orb');
    if (!orbs.length) return;

    var depths = [22, -30, 16]; // px of travel per orb, alternating direction
    var raf = 0;

    function apply(nx, ny) {
      if (raf) return;
      raf = requestAnimationFrame(function () {
        raf = 0;
        for (var i = 0; i < orbs.length; i++) {
          var d = depths[i % depths.length];
          orbs[i].style.translate = (nx * d).toFixed(1) + 'px ' + (ny * d).toFixed(1) + 'px';
        }
      });
    }

    window.addEventListener('pointermove', function (e) {
      // Normalise pointer to [-0.5, 0.5] around the viewport centre.
      apply(e.clientX / window.innerWidth - 0.5, e.clientY / window.innerHeight - 0.5);
    }, { passive: true });
  }

  function init() {
    initReveal();
    initParallax();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
