(function () {
  'use strict';

  var prefersReducedMotion = window.matchMedia &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  function gaugeColor(value) {
    if (value <= 60) return { stroke: '#4ade80', glow: 'rgba(74, 222, 128, 0.45)' };
    if (value <= 80) return { stroke: '#facc15', glow: 'rgba(250, 204, 21, 0.45)' };
    return { stroke: '#f87171', glow: 'rgba(248, 113, 113, 0.5)' };
  }

  function initGauges() {
    document.querySelectorAll('.gauge').forEach(function (gauge) {
      var arc = gauge.querySelector('.gauge__arc');
      if (!arc) return;
      var value = parseFloat(gauge.getAttribute('data-gauge'));
      if (isNaN(value)) value = 0;
      value = Math.max(0, Math.min(100, value));

      var length = arc.getTotalLength();
      arc.style.strokeDasharray = length;
      arc.style.strokeDashoffset = length;

      var color = gaugeColor(value);
      gauge.style.setProperty('--gauge-color', color.stroke);
      gauge.style.setProperty('--gauge-glow', color.glow);

      var filled = length * (1 - value / 100);
      if (prefersReducedMotion) {
        arc.style.strokeDashoffset = filled;
      } else {
        void arc.offsetWidth;
        requestAnimationFrame(function () {
          requestAnimationFrame(function () { arc.style.strokeDashoffset = filled; });
        });
      }
    });
  }

  function boot() {
    initGauges();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
