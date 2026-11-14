(function () {
  'use strict';

  var prefersReducedMotion = Boolean(window.matchMedia &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches);

  var scriptPane = document.currentScript && document.currentScript.closest('.tab-pane');
  var root = scriptPane || document;

  function resolveGaugeColor(value) {
    var token = '--crit';
    if (value <= 60) {
      token = '--ok';
    } else if (value <= 80) {
      token = '--warn';
    }
    if (typeof window.getComputedStyle === 'function' && document.documentElement) {
      var resolved = window.getComputedStyle(document.documentElement).getPropertyValue(token).trim();
      if (resolved) return resolved;
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
      var length = arc.getTotalLength();
      arc.style.strokeDasharray = length;
      var filled = length * (1 - v / 100);
      gauge.style.setProperty('--gauge-color', resolveGaugeColor(v));
      if (!animate || prefersReducedMotion) {
        arc.style.strokeDashoffset = filled;
      } else {
        arc.style.strokeDashoffset = length;
        void arc.offsetWidth;
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
      applyGauge(gauge, gauge.getAttribute('data-gauge'), true);
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

  function boot() {
    initGauges();
    initInterfaceHints();
    initTrafficDailyToggle();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
