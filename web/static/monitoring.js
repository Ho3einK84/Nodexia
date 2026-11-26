(function () {
  'use strict';

  var prefersReducedMotion = Boolean(window.matchMedia &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches);

  var scriptPane = document.currentScript && document.currentScript.closest('.tab-pane');
  var root = scriptPane || document;

  var gaugeColorCache = null;

  function resolveGaugeColor(value) {
    var token = '--crit';
    if (value <= 60) {
      token = '--ok';
    } else if (value <= 80) {
      token = '--warn';
    }
    if (!gaugeColorCache && typeof window.getComputedStyle === 'function' && document.documentElement) {
      var cs = window.getComputedStyle(document.documentElement);
      gaugeColorCache = {
        '--ok': cs.getPropertyValue('--ok').trim(),
        '--warn': cs.getPropertyValue('--warn').trim(),
        '--crit': cs.getPropertyValue('--crit').trim()
      };
    }
    if (gaugeColorCache && gaugeColorCache[token]) {
      return gaugeColorCache[token];
    }
    return 'var(' + token + ')';
  }

  function applyGauge(gauge, rawValue, animate) {
    if (!gauge) return;
    var arc = gauge.querySelector('.gauge__arc');
    var v = parseFloat(rawValue);
    if (isNaN(v)) v = 0;
    v = Math.max(0, Math.min(100, v));

    if (arc) {
      var length = arc._totalLen;
      if (!length) {
        length = arc.getTotalLength();
        arc._totalLen = length;
        arc.style.strokeDasharray = length;
      }
      var filled = length * (1 - v / 100);
      gauge.style.setProperty('--gauge-color', resolveGaugeColor(v));
      if (!animate || prefersReducedMotion) {
        arc.style.strokeDashoffset = filled;
      } else {
        arc.style.strokeDashoffset = length;
        requestAnimationFrame(function () {
          requestAnimationFrame(function () { arc.style.strokeDashoffset = filled; });
        });
      }
    }
  }

  window.nxGaugeColor = resolveGaugeColor;
  window.nxApplyGauge = applyGauge;

  function initGauges() {
    root.querySelectorAll('.gauge').forEach(function (gauge) {
      var raw = gauge.getAttribute('data-gauge');
      applyGauge(gauge, raw, true);
      var valEl = gauge.querySelector('[data-gauge-value]');
      if (valEl && raw) {
        var v = parseFloat(raw);
        if (!isNaN(v)) {
          valEl.textContent = Math.round(v) + '%';
        }
      }
    });
  }

  function initInterfaceHints() {
    root.querySelectorAll('[data-iface-fill]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var iface = this.getAttribute('data-iface-fill');
        var input = root.querySelector('input[name="traffic_interface"]');
        if (input && iface) {
          input.value = iface;
          input.focus();
        }
      });
    });
  }

  function initTrafficDailyToggle() {
    var btn = root.querySelector('#traffic-more-daily');
    var rows = root.querySelector('#traffic-daily-rows');
    if (!btn || !rows) return;
    btn.addEventListener('click', function () {
      var isExpanded = rows.classList.toggle('is-expanded');
      var icon = btn.querySelector('i, svg');
      var text = btn.querySelector('.traffic-more-text');
      if (icon) {
        icon.setAttribute('data-lucide', isExpanded ? 'chevron-up' : 'chevron-down');
      }
      if (text) {
        var moreLabel = window.nxT ? window.nxT('monitoring.show_more') : 'Show more';
        var lessLabel = window.nxT ? window.nxT('monitoring.show_less') : 'Show less';
        text.textContent = isExpanded ? lessLabel : moreLabel;
      }
      if (typeof lucide !== 'undefined' && lucide.createIcons) {
        try { lucide.createIcons({ nodes: [btn] }); } catch (e) {}
      }
    });
  }

  function initSilenceForm() {
    root.querySelectorAll('.silence-dropdown-form').forEach(function (form) {
      if (form.dataset.silenceAjaxReady === '1') return;
      form.dataset.silenceAjaxReady = '1';
      form.addEventListener('submit', function (e) {
        e.preventDefault();
        e.stopPropagation();
        var btn = form.querySelector('button[type="submit"]');
        if (btn) btn.disabled = true;

        var sendSilence = function (retried) {
          var token = (typeof window.nxCSRF === 'function' ? window.nxCSRF(form) : '') ||
            ((form.querySelector('input[name="_csrf_token"]') || {}).value || '');
          var params = new URLSearchParams(new FormData(form));
          if (token) params.set('_csrf_token', token);
          var headers = {
            'Accept': 'application/json',
            'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8'
          };
          if (token) headers['X-CSRF-Token'] = token;

          return fetch(form.getAttribute('action') || form.action, {
            method: 'POST',
            credentials: 'same-origin',
            headers: headers,
            body: params.toString()
          }).then(function (resp) {
            var hdr = resp.headers && resp.headers.get('X-CSRF-Token');
            if (hdr && typeof window.nxSetCSRF === 'function') window.nxSetCSRF(hdr);
            if (resp.status === 403 && !retried && window.NodexiaTabs && typeof window.NodexiaTabs.refreshCSRFToken === 'function') {
              return window.NodexiaTabs.refreshCSRFToken().then(function () { return sendSilence(true); });
            }
            return resp.json().catch(function () { return { ok: resp.ok }; });
          });
        };

        sendSilence(false).then(function (data) {
          var menu = form.closest('[data-action-menu]');
          if (menu) {
            menu.classList.remove('is-open');
            var toggle = menu.querySelector('.action-menu__toggle');
            if (toggle) toggle.setAttribute('aria-expanded', 'false');
          }
          if (typeof window.nxToast === 'function') {
            window.nxToast((data && data.message) || (window.nxT ? window.nxT('js.failed') : 'Failed'), data && data.ok ? 'ok' : 'error');
          }
        }).catch(function () {
          if (typeof window.nxToast === 'function') window.nxToast(window.nxT ? window.nxT('js.failed') : 'Failed', 'error');
        }).finally(function () {
          if (btn) btn.disabled = false;
        });
      });
    });
  }

  function boot() {
    initGauges();
    initInterfaceHints();
    initTrafficDailyToggle();
    initSilenceForm();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
