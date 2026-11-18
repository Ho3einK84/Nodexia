// Nodexia Login: password reveal compartment, submit state, and interactive topology mesh.
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

  function initSubmitState() {
    var form = document.querySelector('[data-auth-form]');
    var btn = document.querySelector('[data-auth-submit]');
    if (!form || !btn) return;

    form.addEventListener('submit', function () {
      if (typeof form.checkValidity === 'function' && !form.checkValidity()) return;
      btn.classList.add('is-submitting');
      btn.setAttribute('aria-busy', 'true');
      window.setTimeout(function () {
        btn.classList.remove('is-submitting');
        btn.removeAttribute('aria-busy');
      }, 6000);
    });
  }

  function initAuthMesh() {
    var canvas = document.getElementById('auth-mesh');
    if (!canvas || typeof canvas.getContext !== 'function') return;
    var ctx = canvas.getContext('2d');
    if (!ctx) return;

    var reducedMotion = window.matchMedia &&
      window.matchMedia('(prefers-reduced-motion: reduce)').matches;

    var width = 0;
    var height = 0;
    var dpr = Math.min(window.devicePixelRatio || 1, 2);
    var nodes = [];
    var pulses = [];
    var packets = [];
    var pointer = { x: -9999, y: -9999, active: false };
    var energyBoost = 1;
    var rafId = 0;

    function createNodes() {
      var count = width < 640 ? 22 : 36;
      nodes = [];
      for (var i = 0; i < count; i++) {
        var speed = (0.14 + Math.random() * 0.24) * (Math.random() < 0.5 ? 1 : -1);
        var angle = Math.random() * Math.PI * 2;
        nodes.push({
          x: Math.random() * width,
          y: Math.random() * height,
          vx: Math.cos(angle) * speed,
          vy: Math.sin(angle) * speed,
          r: 1.5 + Math.random() * 1.6,
          accent: i % 5 === 0 ? 'ok' : 'blue'
        });
      }

      packets = [];
      var packetCount = width < 640 ? 4 : 8;
      for (var p = 0; p < packetCount; p++) {
        packets.push({
          a: p % nodes.length,
          b: (p * 3 + 1) % nodes.length,
          t: Math.random(),
          speed: 0.0045 + Math.random() * 0.004
        });
      }
    }

    function resize() {
      width = canvas.clientWidth || window.innerWidth;
      height = canvas.clientHeight || window.innerHeight;
      dpr = Math.min(window.devicePixelRatio || 1, 2);
      canvas.width = Math.max(1, Math.floor(width * dpr));
      canvas.height = Math.max(1, Math.floor(height * dpr));
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      if (!nodes.length) {
        createNodes();
      }
    }

    function spawnPulse(x, y, okTone) {
      if (reducedMotion) return;
      if (pulses.length > 10) pulses.shift();
      pulses.push({ x: x, y: y, r: 4, max: 54, alpha: 0.45, ok: !!okTone });
    }

    function drawFrame(advance) {
      ctx.clearRect(0, 0, width, height);
      var maxDist = width < 640 ? 125 : 165;

      if (advance) {
        energyBoost += (1 - energyBoost) * 0.03;
        for (var i = 0; i < nodes.length; i++) {
          var n = nodes[i];
          n.x += n.vx * energyBoost;
          n.y += n.vy * energyBoost;

          if (pointer.active) {
            var dxp = pointer.x - n.x;
            var dyp = pointer.y - n.y;
            var distp = Math.sqrt(dxp * dxp + dyp * dyp);
            if (distp > 20 && distp < 180) {
              n.x += (dxp / distp) * 0.12;
              n.y += (dyp / distp) * 0.12;
            }
          }

          if (n.x < -20) n.x = width + 20;
          if (n.x > width + 20) n.x = -20;
          if (n.y < -20) n.y = height + 20;
          if (n.y > height + 20) n.y = -20;
        }
      }

      // Draw topology links
      for (var i = 0; i < nodes.length; i++) {
        var a = nodes[i];
        for (var j = i + 1; j < nodes.length; j++) {
          var b = nodes[j];
          var dx = a.x - b.x;
          var dy = a.y - b.y;
          var d = Math.sqrt(dx * dx + dy * dy);
          if (d < maxDist) {
            var alpha = (1 - d / maxDist) * 0.20;
            ctx.strokeStyle = 'rgba(96, 165, 250, ' + alpha.toFixed(3) + ')';
            ctx.lineWidth = 1;
            ctx.beginPath();
            ctx.moveTo(a.x, a.y);
            ctx.lineTo(b.x, b.y);
            ctx.stroke();
          }
        }

        if (pointer.active) {
          var pdx = a.x - pointer.x;
          var pdy = a.y - pointer.y;
          var pd = Math.sqrt(pdx * pdx + pdy * pdy);
          if (pd < 190) {
            var pAlpha = (1 - pd / 190) * 0.28;
            ctx.strokeStyle = 'rgba(147, 197, 253, ' + pAlpha.toFixed(3) + ')';
            ctx.lineWidth = 1;
            ctx.beginPath();
            ctx.moveTo(a.x, a.y);
            ctx.lineTo(pointer.x, pointer.y);
            ctx.stroke();
          }
        }
      }

      // Draw animated telemetry packets along active edges
      for (var k = 0; k < packets.length; k++) {
        var pkt = packets[k];
        var na = nodes[pkt.a];
        var nb = nodes[pkt.b];
        if (!na || !nb) continue;
        if (advance) {
          pkt.t += pkt.speed * energyBoost;
          if (pkt.t >= 1) {
            pkt.a = pkt.b;
            pkt.b = (pkt.b + 1 + Math.floor(Math.random() * 5)) % nodes.length;
            pkt.t = 0;
            na = nodes[pkt.a];
            nb = nodes[pkt.b];
          }
        }
        var dxk = nb.x - na.x;
        var dyk = nb.y - na.y;
        if (dxk * dxk + dyk * dyk < maxDist * maxDist * 1.45) {
          var px = na.x + dxk * pkt.t;
          var py = na.y + dyk * pkt.t;
          ctx.fillStyle = k % 3 === 0 ? 'rgba(76, 195, 138, 0.85)' : 'rgba(147, 197, 253, 0.88)';
          ctx.beginPath();
          ctx.arc(px, py, 2.1, 0, Math.PI * 2);
          ctx.fill();
        }
      }

      // Draw expanding telemetry pulses
      for (var q = pulses.length - 1; q >= 0; q--) {
        var pls = pulses[q];
        ctx.strokeStyle = (pls.ok ? 'rgba(76, 195, 138, ' : 'rgba(96, 165, 250, ') + pls.alpha.toFixed(3) + ')';
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.arc(pls.x, pls.y, pls.r, 0, Math.PI * 2);
        ctx.stroke();
        if (advance) {
          pls.r += 1.1;
          pls.alpha *= 0.94;
          if (pls.alpha < 0.02 || pls.r > pls.max) {
            pulses.splice(q, 1);
          }
        }
      }

      // Draw network vertices
      for (var v = 0; v < nodes.length; v++) {
        var pt = nodes[v];
        ctx.fillStyle = pt.accent === 'ok'
          ? 'rgba(76, 195, 138, 0.55)'
          : 'rgba(96, 165, 250, 0.45)';
        ctx.beginPath();
        ctx.arc(pt.x, pt.y, pt.r, 0, Math.PI * 2);
        ctx.fill();
      }
    }

    function loop() {
      if (document.hidden) {
        rafId = 0;
        return;
      }
      drawFrame(true);
      rafId = window.requestAnimationFrame(loop);
    }

    function start() {
      resize();
      if (reducedMotion) {
        drawFrame(false);
        return;
      }
      if (!rafId && !document.hidden) {
        rafId = window.requestAnimationFrame(loop);
      }
    }

    window.addEventListener('resize', function () {
      resize();
      if (reducedMotion) drawFrame(false);
    });

    document.addEventListener('visibilitychange', function () {
      if (!document.hidden && !reducedMotion && !rafId) {
        rafId = window.requestAnimationFrame(loop);
      }
    });

    var stage = document.querySelector('[data-auth-stage]');
    if (stage && !reducedMotion) {
      stage.addEventListener('pointermove', function (e) {
        var rect = canvas.getBoundingClientRect();
        pointer.x = e.clientX - rect.left;
        pointer.y = e.clientY - rect.top;
        pointer.active = true;
      });
      stage.addEventListener('pointerleave', function () {
        pointer.active = false;
      });
      stage.addEventListener('focusin', function () {
        energyBoost = 2.0;
        if (nodes.length) {
          var pick = nodes[Math.floor(Math.random() * nodes.length)];
          spawnPulse(pick.x, pick.y, false);
        }
      });
      stage.addEventListener('input', function () {
        energyBoost = 2.4;
        if (nodes.length && pulses.length < 5) {
          var pick = nodes[Math.floor(Math.random() * nodes.length)];
          spawnPulse(pick.x, pick.y, Math.random() < 0.35);
        }
      });
    }

    start();
  }

  function boot() {
    initReveal();
    initSubmitState();
    initAuthMesh();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
