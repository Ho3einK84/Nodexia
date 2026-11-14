// Nodexia Login: progressive enhancement for password visibility toggle.
(function () {
  function initReveal() {
    var field = document.querySelector('.auth__field--reveal');
    if (!field) return;
    var input = field.querySelector('input[type="password"]');
    if (!input) return;
    var wrap = input.closest('.auth__input-wrap') || field;
    if (wrap.querySelector('.auth__reveal')) return;

    var isRTL = document.documentElement.getAttribute('dir') === 'rtl' ||
      document.documentElement.lang === 'fa';

    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'auth__reveal';
    btn.setAttribute('aria-pressed', 'false');
    btn.setAttribute('aria-label', isRTL ? 'نمایش رمز عبور' : 'Show password');
    btn.innerHTML = '<i data-lucide="eye" aria-hidden="true"></i>';

    function renderIcon() {
      if (!window.lucide || typeof window.lucide.createIcons !== 'function') return;
      try {
        window.lucide.createIcons({ root: btn });
      } catch (e) {
        try { window.lucide.createIcons(); } catch (err) { /* ignore */ }
      }
    }

    btn.addEventListener('click', function () {
      var showing = input.type === 'text';
      input.type = showing ? 'password' : 'text';
      btn.setAttribute('aria-pressed', String(!showing));
      btn.setAttribute(
        'aria-label',
        showing
          ? (isRTL ? 'نمایش رمز عبور' : 'Show password')
          : (isRTL ? 'پنهان کردن رمز عبور' : 'Hide password')
      );
      btn.innerHTML = showing
        ? '<i data-lucide="eye" aria-hidden="true"></i>'
        : '<i data-lucide="eye-off" aria-hidden="true"></i>';
      renderIcon();
      input.focus();
    });

    wrap.appendChild(btn);
    renderIcon();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initReveal);
  } else {
    initReveal();
  }
})();
