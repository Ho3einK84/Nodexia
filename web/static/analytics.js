/* analytics.js — colorful SVG time-series charts, motion & bandwidth forecast UI */
(function () {
  'use strict';

  var scriptEl = document.currentScript;
  var paneAPI = window.nxPane ? window.nxPane(scriptEl) : null;
  var scriptPane = paneAPI ? paneAPI.pane : (scriptEl && scriptEl.closest ? scriptEl.closest('.tab-pane') : null);
  var rootScope = paneAPI ? paneAPI.root : (scriptPane || document);

  function byId(id) {
    if (paneAPI) return paneAPI.byId(id);
    return rootScope.querySelector ? rootScope.querySelector('#' + id) : document.getElementById(id);
  }

  function onCleanup(fn) {
    if (typeof fn !== 'function') return;
    if (paneAPI) {
      paneAPI.onCleanup(fn);
      return;
    }
    if (scriptPane) {
      if (!scriptPane.__nxCleanups) scriptPane.__nxCleanups = [];
      scriptPane.__nxCleanups.push(fn);
    }
  }

  const prefersReducedMotion = Boolean(
    window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  );

  // Localization helper (see core.js for window.nxT). Falls back to the key.
  function T(key, params) { return window.nxT ? window.nxT(key, params) : key; }
  function Tn(key, count, params) {
    if (window.nxTn) return window.nxTn(key, count, params);
    return T(key, params);
  }

  function escapeHTML(str) {
    if (window.nxEscape) return window.nxEscape(str);
    return String(str == null ? '' : str).replace(/[&<>"']/g, function (m) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[m];
    });
  }

  function renderIcons(scope) {
    if (!scope) return;
    if (window.nxRenderIcons) { window.nxRenderIcons(scope); return; }
    if (window.lucide && window.lucide.createIcons) {
      try { window.lucide.createIcons({ root: scope }); } catch (e) {}
    }
  }

  // Semantic telemetry palette aligned to Nodexia's Dark Graphite design tokens.
  const SERIES_TOKEN_MAP = {
    cpu: '--accent-soft',
    ram: '--ok',
    disk: '--warn',
    swap: '--an-slate-indigo',
    load1: '--accent-soft',
    load5: '--ok',
    load15: '--warn',
    rx: '--accent-soft',
    tx: '--ok',
  };

  const TOKEN_DEFAULTS = {
    '--accent': '#3b82f6',
    '--accent-soft': '#60a5fa',
    '--accent-bright': '#93c5fd',
    '--ok': '#4cc38a',
    '--warn': '#eab308',
    '--crit': '#e5484d',
    '--an-slate-indigo': '#8b9cf7',
    '--an-slate-teal': '#5cb8c4',
    '--text-muted': '#93939a',
    '--text-dim': '#8a8a93',
  };

  const BACKEND_TOKEN_ALIASES = {
    '--accent': '--accent-soft',
  };

  const ALLOWED_TOKENS = new Set(Object.keys(TOKEN_DEFAULTS));
  const COLOR_RE = /^#[0-9a-fA-F]{3,8}$/;

  function tokenHost() {
    return (rootScope.querySelector && rootScope.querySelector('.analytics-page')) || document.documentElement;
  }

  function readCSSToken(tokenName, fallback) {
    try {
      const val = getComputedStyle(tokenHost()).getPropertyValue(tokenName).trim();
      if (val) return val;
    } catch (e) {}
    return fallback;
  }

  function resolveSeriesColor(series, fallbackMetric) {
    const metricKey = (series && series.metric) || fallbackMetric || '';
    let token = SERIES_TOKEN_MAP[metricKey] || '';
    if (!token && series && typeof series.color === 'string') {
      if (ALLOWED_TOKENS.has(series.color)) {
        token = BACKEND_TOKEN_ALIASES[series.color] || series.color;
      } else if (series.color.startsWith('var(')) {
        const inner = series.color.slice(4, -1).trim();
        if (ALLOWED_TOKENS.has(inner)) {
          token = BACKEND_TOKEN_ALIASES[inner] || inner;
        }
      }
    }
    if (token) {
      return readCSSToken(token, TOKEN_DEFAULTS[token] || '#60a5fa');
    }
    if (series && COLOR_RE.test(series.color)) {
      return series.color;
    }
    return readCSSToken('--accent-soft', '#60a5fa');
  }

  // ── Shared GPU Bar & Count-Up Enhancers ───────────────────────────────────

  function revealBarElement(el, targetPct) {
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        el.style.setProperty('--pct', targetPct);
      });
    });
  }

  function initAnimatedBars(scope) {
    const host = scope || rootScope;
    if (!host || !host.querySelectorAll) return;
    const bars = host.querySelectorAll('[data-an-bar]');
    if (!bars.length) return;

    const pending = [];
    bars.forEach((bar) => {
      if (bar.dataset.anBarReady === '1') return;
      bar.dataset.anBarReady = '1';
      const target = bar.style.getPropertyValue('--pct').trim();
      if (target === '') return;
      if (prefersReducedMotion) {
        bar.style.setProperty('--pct', target);
        return;
      }
      bar.dataset.anBarTarget = target;
      bar.style.setProperty('--pct', '0');
      pending.push(bar);
    });

    if (!pending.length) return;

    if (typeof IntersectionObserver === 'undefined') {
      pending.forEach((bar) => revealBarElement(bar, bar.dataset.anBarTarget));
      return;
    }

    const observer = new IntersectionObserver((entries) => {
      entries.forEach((entry) => {
        if (!entry.isIntersecting) return;
        const bar = entry.target;
        const target = bar.dataset.anBarTarget;
        if (target !== undefined) revealBarElement(bar, target);
        observer.unobserve(bar);
      });
    }, { threshold: 0.05, rootMargin: '40px' });

    pending.forEach((bar) => observer.observe(bar));
    onCleanup(() => observer.disconnect());
  }

  function initCountUp(scope) {
    const host = scope || rootScope;
    if (!host || !host.querySelectorAll || prefersReducedMotion) return;
    const nodes = host.querySelectorAll('[data-countup]');
    if (!nodes.length) return;

    const activeRAFs = new Set();
    onCleanup(() => {
      activeRAFs.forEach((id) => cancelAnimationFrame(id));
      activeRAFs.clear();
    });

    nodes.forEach((el) => {
      if (el.dataset.countupDone === '1') return;
      const raw = (el.textContent || '').trim();
      const match = raw.match(/^([^\d.-]*)(-?\d+(?:\.\d+)?)(.*)$/);
      if (!match) return;

      const prefix = match[1] || '';
      const numStr = match[2];
      const suffix = match[3] || '';
      const target = parseFloat(numStr);
      if (!Number.isFinite(target) || target === 0) return;

      el.dataset.countupDone = '1';
      const dotIdx = numStr.indexOf('.');
      const decimals = dotIdx >= 0 ? numStr.length - dotIdx - 1 : 0;
      const duration = 520;
      let startTs = 0;

      function tick(ts) {
        if (!startTs) startTs = ts;
        const elapsed = ts - startTs;
        const progress = Math.min(1, elapsed / duration);
        const eased = 1 - Math.pow(1 - progress, 3);
        const value = target * eased;
        el.textContent = prefix + value.toFixed(decimals) + suffix;
        if (progress < 1) {
          const nextId = requestAnimationFrame(tick);
          activeRAFs.add(nextId);
        } else {
          el.textContent = raw;
        }
      }

      const firstId = requestAnimationFrame(tick);
      activeRAFs.add(firstId);
    });
  }

  // ── SVG Chart Renderer ────────────────────────────────────────────────────

  const CHART_PAD = { top: 14, right: 16, bottom: 28, left: 46 };
  let chartInstanceSeq = 0;

  function createSVGEl(tag, attrs) {
    const el = document.createElementNS('http://www.w3.org/2000/svg', tag);
    for (const [k, v] of Object.entries(attrs || {})) el.setAttribute(k, v);
    return el;
  }

  function buildChart(container, data, opts) {
    if (!container || !data) return;
    const animate = !(opts && opts.animate === false);
    container.__chartData = data;
    if (!container.__hiddenSeries) container.__hiddenSeries = new Set();

    const w = Math.max(120, Math.round(container.clientWidth || 400));
    const h = Math.max(80, Math.round(container.clientHeight || 200));
    container.__lastW = w;
    container.__lastH = h;

    container.innerHTML = '';
    const pw = Math.max(10, w - CHART_PAD.left - CHART_PAD.right);
    const ph = Math.max(10, h - CHART_PAD.top - CHART_PAD.bottom);

    const svg = createSVGEl('svg', {
      viewBox: `0 0 ${w} ${h}`,
      preserveAspectRatio: 'xMidYMid meet',
    });

    const seq = ++chartInstanceSeq;
    const defs = createSVGEl('defs');
    svg.appendChild(defs);

    const plotG = createSVGEl('g', { transform: `translate(${CHART_PAD.left},${CHART_PAD.top})` });
    svg.appendChild(plotG);

    const labels = data.labels || [];
    const series = data.series || [];
    const yMin = data.min ?? 0;
    const yMax = data.max ?? 100;
    const yRange = yMax - yMin || 1;
    const unit = data.unit || '';

    // Y-axis grid lines and labels
    const yTicks = computeYTicks(yMin, yMax, 4);
    for (const tick of yTicks) {
      const y = ph - ((tick - yMin) / yRange) * ph;
      plotG.appendChild(createSVGEl('line', {
        class: 'chart-grid-line',
        x1: 0, y1: y, x2: pw, y2: y,
      }));
      const axisLabel = createSVGEl('text', {
        class: 'chart-axis-label',
        x: -8, y: y + 4,
        'text-anchor': 'end',
      });
      axisLabel.textContent = formatAxisValue(tick, unit);
      plotG.appendChild(axisLabel);
    }

    // X-axis labels (sparse)
    const xLabelCount = Math.min(6, labels.length);
    const xStep = labels.length > 1 ? (labels.length - 1) / Math.max(xLabelCount - 1, 1) : 1;
    for (let i = 0; i < xLabelCount; i++) {
      const idx = Math.round(i * xStep);
      if (idx >= labels.length) continue;
      const x = (idx / Math.max(labels.length - 1, 1)) * pw;
      const axisLabel = createSVGEl('text', {
        class: 'chart-axis-label',
        x: x,
        y: ph + 18,
        'text-anchor': 'middle',
      });
      axisLabel.textContent = labels[idx];
      plotG.appendChild(axisLabel);
    }

    const linePathsToAnimate = [];
    const surfaceBg = readCSSToken('--surface', '#141415');

    series.forEach((s, sIdx) => {
      if (!s.data || s.data.length === 0) return;
      const strokeColor = resolveSeriesColor(s, data.metric);
      const gradId = `an-g-${seq}-${sIdx}`;

      const grad = createSVGEl('linearGradient', {
        id: gradId,
        x1: '0', y1: '0', x2: '0', y2: '1',
      });
      grad.appendChild(createSVGEl('stop', { offset: '0%', 'stop-color': strokeColor, 'stop-opacity': '0.20' }));
      grad.appendChild(createSVGEl('stop', { offset: '62%', 'stop-color': strokeColor, 'stop-opacity': '0.05' }));
      grad.appendChild(createSVGEl('stop', { offset: '100%', 'stop-color': strokeColor, 'stop-opacity': '0' }));
      defs.appendChild(grad);

      const pts = s.data.map((v, i) => ({
        x: (i / Math.max(s.data.length - 1, 1)) * pw,
        y: ph - ((v - yMin) / yRange) * ph,
        v,
      }));

      const seriesG = createSVGEl('g', {
        class: 'chart-series' + (container.__hiddenSeries.has(sIdx) ? ' is-hidden-series' : ''),
        'data-series-idx': String(sIdx),
      });

      // Vertical gradient area fill
      const areaD = buildAreaPath(pts, pw, ph);
      seriesG.appendChild(createSVGEl('path', {
        class: 'chart-area' + (animate && !prefersReducedMotion ? ' is-revealing' : ''),
        d: areaD,
        fill: `url(#${gradId})`,
      }));

      // Luminous soft halo + crisp line path
      const lineD = buildLinePath(pts);
      seriesG.appendChild(createSVGEl('path', {
        class: 'chart-path-glow',
        d: lineD,
        stroke: strokeColor,
      }));

      const linePath = createSVGEl('path', {
        class: 'chart-path',
        d: lineD,
        stroke: strokeColor,
      });
      seriesG.appendChild(linePath);
      if (animate && !prefersReducedMotion) {
        linePathsToAnimate.push(linePath);
      }

      // Highlight peak point on single-series percentage charts
      if (series.length === 1 && pts.length >= 3 && unit === '%') {
        let peakIdx = 0;
        for (let i = 1; i < pts.length; i++) {
          if (pts[i].v > pts[peakIdx].v) peakIdx = i;
        }
        if (peakIdx > 0 && peakIdx < pts.length - 1) {
          seriesG.appendChild(createSVGEl('circle', {
            class: 'chart-peak-dot',
            cx: pts[peakIdx].x.toFixed(2),
            cy: pts[peakIdx].y.toFixed(2),
            r: '3.5',
            fill: surfaceBg,
            stroke: strokeColor,
            'stroke-width': '2',
          }));
        }
      }

      // Pulsing live endpoint beacon
      const lastPt = pts[pts.length - 1];
      if (lastPt) {
        seriesG.appendChild(createSVGEl('circle', {
          class: 'chart-beacon-ring',
          cx: lastPt.x.toFixed(2),
          cy: lastPt.y.toFixed(2),
          r: '4',
          stroke: strokeColor,
        }));
        seriesG.appendChild(createSVGEl('circle', {
          class: 'chart-beacon-dot',
          cx: lastPt.x.toFixed(2),
          cy: lastPt.y.toFixed(2),
          r: '3.5',
          fill: strokeColor,
        }));
      }

      plotG.appendChild(seriesG);
    });

    // Interaction overlay (mouse + touch)
    const overlay = buildInteractionOverlay(container, plotG, pw, ph, labels, series, yMin, yRange, unit, data.metric);
    plotG.appendChild(overlay);

    container.appendChild(svg);

    if (linePathsToAnimate.length && container.clientWidth > 0) {
      linePathsToAnimate.forEach((lp) => {
        try {
          const len = lp.getTotalLength();
          if (len > 0 && Number.isFinite(len)) {
            lp.style.setProperty('--path-len', len.toFixed(1));
            lp.classList.add('is-drawing');
            const finishDraw = () => lp.classList.remove('is-drawing');
            lp.addEventListener('animationend', finishDraw, { once: true });
          }
        } catch (e) {}
      });
    }

    ensureResizeObserver(container);
  }

  function ensureResizeObserver(container) {
    if (container.__ro || typeof ResizeObserver === 'undefined') return;
    let raf = 0;
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const nw = Math.round(entry.contentRect.width);
        const nh = Math.round(entry.contentRect.height);
        if (nw > 0 && nh > 0 && (nw !== container.__lastW || nh !== container.__lastH)) {
          if (!raf && container.__chartData && !container.hidden) {
            raf = requestAnimationFrame(() => {
              raf = 0;
              if (container.__chartData && !container.hidden) {
                buildChart(container, container.__chartData, { animate: false });
              }
            });
          }
        }
      }
    });
    ro.observe(container);
    container.__ro = ro;
    onCleanup(() => {
      if (raf) cancelAnimationFrame(raf);
      ro.disconnect();
    });
  }

  function buildInteractionOverlay(container, plotG, pw, ph, labels, series, yMin, yRange, unit, metricName) {
    const rect = createSVGEl('rect', {
      x: 0, y: 0, width: pw, height: ph,
      fill: 'transparent', cursor: 'crosshair',
    });

    let tooltipEl = null;
    let vline = null;
    let dots = [];
    let touchHideTimer = null;

    onCleanup(() => {
      if (touchHideTimer) {
        clearTimeout(touchHideTimer);
        touchHideTimer = null;
      }
    });

    function showTooltip(x, clientX, clientY) {
      if (!labels.length) return;
      if (touchHideTimer) {
        clearTimeout(touchHideTimer);
        touchHideTimer = null;
      }
      const clampedX = Math.max(0, Math.min(pw, x));
      const idx = Math.round((clampedX / pw) * Math.max(labels.length - 1, 0));
      if (idx < 0 || idx >= labels.length) return;

      const xPos = (idx / Math.max(labels.length - 1, 1)) * pw;
      const hidden = container.__hiddenSeries || new Set();

      if (!vline) {
        vline = createSVGEl('line', {
          class: 'chart-tooltip-line',
          y1: 0, y2: ph,
        });
        plotG.appendChild(vline);
      }
      vline.setAttribute('x1', xPos);
      vline.setAttribute('x2', xPos);
      vline.style.display = '';

      dots.forEach(d => d.remove());
      dots = [];
      let visibleRows = 0;
      series.forEach((s, sIdx) => {
        if (hidden.has(sIdx) || !s.data || idx >= s.data.length) return;
        visibleRows++;
        const v = s.data[idx];
        const y = ph - ((v - yMin) / yRange) * ph;
        const color = resolveSeriesColor(s, metricName);
        const dot = createSVGEl('circle', {
          class: 'chart-tooltip-dot',
          cx: xPos, cy: y, r: 4.5,
          fill: color,
        });
        plotG.appendChild(dot);
        dots.push(dot);
      });

      if (!visibleRows) {
        hideTooltip();
        return;
      }

      if (!tooltipEl) {
        tooltipEl = document.createElement('div');
        tooltipEl.className = 'chart-tooltip';
        container.appendChild(tooltipEl);
      }

      let html = `<div class="chart-tooltip__label">${escapeHTML(labels[idx])}</div>`;
      series.forEach((s, sIdx) => {
        if (hidden.has(sIdx) || !s.data || idx >= s.data.length) return;
        const v = s.data[idx];
        const color = resolveSeriesColor(s, metricName);
        const formatted = unit === '%' ? v.toFixed(1) + '%'
          : unit === 'GB' ? v.toFixed(3) + ' GB'
          : v.toFixed(2) + (unit ? ' ' + unit : '');
        html += `<div class="chart-tooltip__row">
          <span class="chart-tooltip__series">
            <span class="chart-tooltip__dot" style="background:${color}"></span>
            <span>${escapeHTML(s.label)}</span>
          </span>
          <span class="chart-tooltip__value">${formatted}</span>
        </div>`;
      });
      tooltipEl.innerHTML = html;

      // Position tooltip cleanly inside container bounds using GPU transform
      const cRect = container.getBoundingClientRect();
      const pointerX = clientX - cRect.left;
      const pointerY = clientY - cRect.top;
      const tWidth = tooltipEl.offsetWidth || 150;
      const tHeight = tooltipEl.offsetHeight || 56;
      let tx = pointerX + 14;
      if (tx + tWidth > cRect.width - 8) {
        tx = pointerX - tWidth - 14;
      }
      tx = Math.max(8, Math.min(tx, Math.max(8, cRect.width - tWidth - 8)));
      let ty = pointerY - 14;
      ty = Math.max(4, Math.min(ty, Math.max(4, cRect.height - tHeight - 4)));

      tooltipEl.style.transform = `translate3d(${Math.round(tx)}px, ${Math.round(ty)}px, 0)`;
      tooltipEl.style.opacity = '1';
    }

    function hideTooltip() {
      if (vline) vline.style.display = 'none';
      dots.forEach(d => d.remove());
      dots = [];
      if (tooltipEl) tooltipEl.style.opacity = '0';
    }

    function pointerToLocalX(clientX) {
      const svgEl = rect.closest('svg');
      if (!svgEl) return null;
      const svgRect = svgEl.getBoundingClientRect();
      if (!svgRect.width) return null;
      const vbWidth = (svgEl.viewBox && svgEl.viewBox.baseVal && svgEl.viewBox.baseVal.width) || svgRect.width;
      const scaleX = vbWidth / svgRect.width;
      return (clientX - svgRect.left) * scaleX - CHART_PAD.left;
    }

    rect.addEventListener('mousemove', (e) => {
      const localX = pointerToLocalX(e.clientX);
      if (localX !== null) showTooltip(localX, e.clientX, e.clientY);
    });

    rect.addEventListener('mouseleave', hideTooltip);

    rect.addEventListener('touchstart', (e) => {
      if (!e.touches || !e.touches.length) return;
      const t = e.touches[0];
      const localX = pointerToLocalX(t.clientX);
      if (localX !== null) showTooltip(localX, t.clientX, t.clientY);
    }, { passive: true });

    rect.addEventListener('touchmove', (e) => {
      if (!e.touches || !e.touches.length) return;
      const t = e.touches[0];
      const localX = pointerToLocalX(t.clientX);
      if (localX !== null) showTooltip(localX, t.clientX, t.clientY);
    }, { passive: true });

    rect.addEventListener('touchend', () => {
      if (touchHideTimer) clearTimeout(touchHideTimer);
      touchHideTimer = setTimeout(hideTooltip, 1600);
    }, { passive: true });

    return rect;
  }

  function buildLinePath(pts) {
    if (pts.length === 0) return '';
    let d = `M ${pts[0].x.toFixed(2)} ${pts[0].y.toFixed(2)}`;
    for (let i = 1; i < pts.length; i++) {
      const cp1x = (pts[i - 1].x + pts[i].x) / 2;
      d += ` C ${cp1x.toFixed(2)} ${pts[i - 1].y.toFixed(2)}, ${cp1x.toFixed(2)} ${pts[i].y.toFixed(2)}, ${pts[i].x.toFixed(2)} ${pts[i].y.toFixed(2)}`;
    }
    return d;
  }

  function buildAreaPath(pts, pw, ph) {
    if (pts.length === 0) return '';
    let d = `M ${pts[0].x.toFixed(2)} ${ph}`;
    d += ` L ${pts[0].x.toFixed(2)} ${pts[0].y.toFixed(2)}`;
    for (let i = 1; i < pts.length; i++) {
      const cp1x = (pts[i - 1].x + pts[i].x) / 2;
      d += ` C ${cp1x.toFixed(2)} ${pts[i - 1].y.toFixed(2)}, ${cp1x.toFixed(2)} ${pts[i].y.toFixed(2)}, ${pts[i].x.toFixed(2)} ${pts[i].y.toFixed(2)}`;
    }
    d += ` L ${pts[pts.length - 1].x.toFixed(2)} ${ph} Z`;
    return d;
  }

  function computeYTicks(min, max, count) {
    const step = (max - min) / count;
    const ticks = [];
    for (let i = 0; i <= count; i++) {
      ticks.push(+(min + step * i).toFixed(2));
    }
    return ticks;
  }

  function formatAxisValue(v, unit) {
    if (unit === '%') return v.toFixed(0);
    if (unit === 'GB') return v.toFixed(1);
    if (v >= 1000) return (v / 1000).toFixed(1) + 'k';
    return v.toFixed(v < 10 ? 1 : 0);
  }

  // ── Chart loading & interactive series legend ─────────────────────────────

  function setCardState(metric, activeState) {
    const sk = byId('skeleton-' + metric);
    const ct = byId('chart-' + metric);
    const em = byId('empty-' + metric);
    const er = byId('error-' + metric);
    if (sk) sk.hidden = activeState !== 'skeleton';
    if (ct) ct.hidden = activeState !== 'chart';
    if (em) em.hidden = activeState !== 'empty';
    if (er) er.hidden = activeState !== 'error';
    if (activeState !== 'chart') {
      const live = byId('live-' + metric);
      if (live) live.hidden = true;
    }
  }

  function showSkeleton(metric) { setCardState(metric, 'skeleton'); }
  function showChart(metric) { setCardState(metric, 'chart'); }
  function showEmpty(metric) { setCardState(metric, 'empty'); }
  function showError(metric) { setCardState(metric, 'error'); }

  function updateCardLiveBadge(metric, data) {
    const badge = byId('live-' + metric);
    if (!badge || !data || !Array.isArray(data.series)) return;
    const nonEmpty = data.series.filter(s => Array.isArray(s.data) && s.data.length > 0);
    if (!nonEmpty.length) {
      badge.hidden = true;
      return;
    }

    badge.className = 'chart-card__live-val';
    const unit = data.unit || '';
    if (unit === '%' && nonEmpty.length === 1) {
      const vals = nonEmpty[0].data;
      const cur = vals[vals.length - 1];
      const lvl = cur > 80 ? 'crit' : cur > 60 ? 'warn' : 'ok';
      badge.classList.add('chart-card__live-val--' + lvl);
      badge.textContent = cur.toFixed(1) + '%';
      badge.hidden = false;
      return;
    }

    if (metric === 'load') {
      const vals = nonEmpty[0].data;
      const cur = vals[vals.length - 1];
      badge.classList.add('chart-card__live-val--indigo');
      badge.textContent = cur.toFixed(2);
      badge.hidden = false;
      return;
    }

    if (metric === 'traffic' && nonEmpty.length >= 2) {
      const rxVals = nonEmpty[0].data;
      const txVals = nonEmpty[1].data;
      const rx = rxVals[rxVals.length - 1] || 0;
      const tx = txVals[txVals.length - 1] || 0;
      badge.classList.add('chart-card__live-val--sky');
      badge.textContent = `↓ ${rx.toFixed(1)} · ↑ ${tx.toFixed(1)} GB`;
      badge.hidden = false;
      return;
    }

    badge.hidden = true;
  }

  function toggleSeriesVisibility(chartEl, sIdx, toggleBtn) {
    if (!chartEl) return;
    if (!chartEl.__hiddenSeries) chartEl.__hiddenSeries = new Set();
    const hidden = chartEl.__hiddenSeries;
    if (hidden.has(sIdx)) {
      hidden.delete(sIdx);
    } else {
      const totalSeries = (chartEl.__chartData && chartEl.__chartData.series) ? chartEl.__chartData.series.length : 0;
      // Keep at least one series visible so the chart doesn't go completely blank by accident
      if (totalSeries > 1 && hidden.size >= totalSeries - 1) {
        return;
      }
      hidden.add(sIdx);
    }
    const isHidden = hidden.has(sIdx);
    if (toggleBtn) {
      toggleBtn.classList.toggle('is-muted', isHidden);
      toggleBtn.setAttribute('aria-pressed', isHidden ? 'false' : 'true');
    }
    const g = chartEl.querySelector(`.chart-series[data-series-idx="${sIdx}"]`);
    if (g) g.classList.toggle('is-hidden-series', isHidden);
  }

  function updateFooter(metric, data, chartEl) {
    const footer = byId('footer-' + metric);
    if (!footer || !data || !Array.isArray(data.series) || data.series.length === 0) return;

    const unit = data.unit || '';
    const fmt = (v) => unit === '%' ? v.toFixed(1) + '%' : v.toFixed(2) + (unit ? ' ' + unit : '');
    const nonEmpty = data.series.filter(s => Array.isArray(s.data) && s.data.length > 0);
    if (nonEmpty.length === 0) return;

    if (nonEmpty.length === 1) {
      const values = nonEmpty[0].data;
      const avg = values.reduce((a, b) => a + b, 0) / values.length;
      const max = Math.max(...values);
      footer.textContent = window.nxT
        ? window.nxT('js.analytics.chart_footer', { avg: fmt(avg), peak: fmt(max), count: values.length })
        : `Avg: ${fmt(avg)} · Peak: ${fmt(max)} · ${values.length} points`;
      return;
    }

    const hidden = (chartEl && chartEl.__hiddenSeries) || new Set();
    footer.innerHTML = `<div class="chart-footer-series">${data.series.map((s, sIdx) => {
      if (!Array.isArray(s.data) || s.data.length === 0) return '';
      const vals = s.data;
      const avg = vals.reduce((a, b) => a + b, 0) / vals.length;
      const max = Math.max(...vals);
      const cur = vals[vals.length - 1];
      const color = resolveSeriesColor(s, data.metric);
      const muted = hidden.has(sIdx);
      const aria = T('js.analytics.toggle_series', { label: s.label });
      return `<button type="button" class="chart-footer-item chart-footer-item--toggle${muted ? ' is-muted' : ''}" data-series-idx="${sIdx}" aria-pressed="${muted ? 'false' : 'true'}" aria-label="${escapeHTML(aria)}">
        <span class="legend-dot" style="background:${color}"></span>
        <span class="chart-footer-item__label">${escapeHTML(s.label)}</span>
        <span>${fmt(cur)} (${escapeHTML(T('js.analytics.avg'))} ${fmt(avg)} · ${escapeHTML(T('js.analytics.peak'))} ${fmt(max)})</span>
      </button>`;
    }).join('')}</div>`;

    footer.querySelectorAll('.chart-footer-item--toggle').forEach((btn) => {
      btn.addEventListener('click', () => {
        const idx = parseInt(btn.dataset.seriesIdx, 10);
        if (!Number.isNaN(idx)) toggleSeriesVisibility(chartEl, idx, btn);
      });
    });
  }

  async function loadChart(serverID, metric, range) {
    showSkeleton(metric);
    try {
      const resp = await fetch(`/servers/${serverID}/analytics/data?metric=${metric}&range=${range}`);
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      const data = await resp.json();
      if (!data.series || data.series.length === 0 || !data.series.some(s => s.data && s.data.length > 0)) {
        showEmpty(metric);
        return;
      }
      showChart(metric);
      const ct = byId('chart-' + metric);
      if (ct) {
        requestAnimationFrame(() => {
          buildChart(ct, data, { animate: true });
          updateCardLiveBadge(metric, data);
          updateFooter(metric, data, ct);
        });
      }
      if (metric === 'traffic') {
        buildLegend('legend-traffic', data.series, data.metric, ct);
      }
    } catch (e) {
      showError(metric);
    }
  }

  function buildLegend(containerId, series, fallbackMetric, chartEl) {
    const legend = byId(containerId);
    if (!legend) return;
    const hidden = (chartEl && chartEl.__hiddenSeries) || new Set();
    legend.innerHTML = series.map((s, sIdx) => {
      const muted = hidden.has(sIdx);
      const aria = T('js.analytics.toggle_series', { label: s.label });
      return `<button type="button" class="legend-item${muted ? ' is-muted' : ''}" data-series-idx="${sIdx}" aria-pressed="${muted ? 'false' : 'true'}" aria-label="${escapeHTML(aria)}">
        <span class="legend-dot" style="background:${resolveSeriesColor(s, fallbackMetric)}"></span>
        <span>${escapeHTML(s.label)}</span>
      </button>`;
    }).join('');

    legend.querySelectorAll('.legend-item').forEach((btn) => {
      btn.addEventListener('click', () => {
        const idx = parseInt(btn.dataset.seriesIdx, 10);
        if (!Number.isNaN(idx)) toggleSeriesVisibility(chartEl, idx, btn);
      });
    });
  }

  // ── Forecast UI ───────────────────────────────────────────────────────────

  async function loadForecast(serverID) {
    const grid = byId('forecastGrid');
    if (!grid) return;

    try {
      const resp = await fetch(`/servers/${serverID}/analytics/forecast`);
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      const f = await resp.json();
      renderForecast(f);
    } catch (e) {
      if (grid) {
        grid.innerHTML = `<div class="chart-error" style="position:relative;height:80px">
          <i data-lucide="alert-circle"></i>
          <p>${escapeHTML(T('js.analytics.forecast_unavailable'))}</p>
        </div>`;
        renderIcons(grid);
      }
    }
  }

  function renderForecast(f) {
    const grid = byId('forecastGrid');
    if (!grid) return;

    const trendHTML = renderTrend(f.trend);
    const confidenceHTML = renderConfidence(f.confidence);
    const risksHTML = renderRisks(f.risks || {});
    const isExhaustRisk = Boolean(f.risks && f.risks.exhaustion);

    const scope = seriesScopeLabel(f.series);
    const periodNote = anchoredPeriodNote(f.period_start, f.period_end);
    grid.innerHTML = `
      ${renderPeriodCard(T('js.analytics.today'), f.today, f.today.pct_elapsed, scope, '', 'today', 0, false)}
      ${renderPeriodCard(T('js.analytics.this_week'), f.this_week, f.this_week.pct_elapsed, scope, '', 'week', 1, false)}
      ${renderPeriodCard(T('js.analytics.this_month'), f.this_month, f.this_month.pct_elapsed, scope, periodNote, 'month', 2, isExhaustRisk)}
    `;

    let meta = byId('forecastMeta');
    if (!meta) {
      meta = document.createElement('div');
      meta.id = 'forecastMeta';
      meta.className = 'forecast-meta an-enter';
      meta.style.setProperty('--stagger', '2');
      grid.parentNode.insertBefore(meta, grid.nextSibling);
    }
    meta.innerHTML = `
      ${confidenceHTML}
      ${trendHTML}
      ${risksHTML}
    `;

    renderExhaustion(f.exhaustion);

    const exhaustionPanel = byId('forecastExhaustion');
    renderIcons(grid);
    renderIcons(meta);
    renderIcons(exhaustionPanel);
    initAnimatedBars(grid);
    initAnimatedBars(exhaustionPanel);
    initCountUp(grid);
  }

  function seriesScopeLabel(series) {
    if (series === 'tx') return T('js.analytics.upload_scope');
    if (series === 'total') return T('js.analytics.total_scope');
    return T('js.analytics.download_scope');
  }

  function anchoredPeriodNote(start, end) {
    if (!start || !end || start.slice(-2) === '01') return '';
    return T('js.analytics.period_range', { start: start, end: end });
  }

  function renderPeriodCard(label, period, pct, scopeLabel, periodNote, theme, staggerIdx, isRisk) {
    const clampedPct = Math.min(100, Math.max(0, pct || 0));
    const progressClass = clampedPct > 90 ? 'forecast-progress__bar--danger'
      : clampedPct > 70 ? 'forecast-progress__bar--warn'
      : '';
    return `
      <div class="forecast-card forecast-card--${escapeHTML(theme)}${isRisk ? ' forecast-card--risk' : ''} an-enter" style="--stagger:${staggerIdx}">
        <div class="forecast-card__label"><span>${escapeHTML(label)}</span> <span class="forecast-card__scope">${escapeHTML(scopeLabel)}</span></div>
        <div class="forecast-card__current" data-countup>${escapeHTML(period.current_human)}</div>
        <div class="forecast-card__predicted">
          ${escapeHTML(T('js.analytics.predicted_end'))} <strong>${escapeHTML(period.predicted_human)}</strong>
        </div>
        <div class="forecast-progress">
          <div class="forecast-progress__bar ${progressClass}" data-an-bar style="--pct:${clampedPct}"></div>
        </div>
        <div class="forecast-progress__label">${escapeHTML(T('js.analytics.period_elapsed', { pct: clampedPct }))}${periodNote ? ` · ${escapeHTML(periodNote)}` : ''}</div>
      </div>
    `;
  }

  function renderExhaustion(ex) {
    const panel = byId('forecastExhaustion');
    if (!panel) return;
    if (!ex || !ex.has_limit) {
      panel.hidden = true;
      panel.innerHTML = '';
      return;
    }

    let level = 'ok';
    let icon = 'shield-check';
    let message = '';
    let sub = '';

    if (ex.already_over) {
      level = 'danger';
      icon = 'alert-octagon';
      message = T('js.analytics.exhaustion_over', { limit: ex.limit_human, projected: ex.projected_month_human });
    } else if (ex.will_exhaust && ex.days_remaining <= 0) {
      level = 'danger';
      icon = 'alert-octagon';
      message = T('js.analytics.exhaustion_today', { limit: ex.limit_human });
      sub = Tn('js.analytics.exhaustion_margin', ex.days_until_month_end, { count: ex.days_until_month_end });
    } else if (ex.will_exhaust) {
      level = ex.days_remaining <= 3 ? 'danger' : 'warn';
      icon = 'alert-triangle';
      message = Tn('js.analytics.exhaustion_in_days', ex.days_remaining, {
        count: ex.days_remaining, limit: ex.limit_human, date: ex.exhaustion_date,
      });
      sub = Tn('js.analytics.exhaustion_margin', ex.days_until_month_end, { count: ex.days_until_month_end });
    } else {
      level = 'ok';
      icon = 'shield-check';
      message = T('js.analytics.exhaustion_safe', { limit: ex.limit_human, projected: ex.projected_month_human });
    }

    let quotaHTML = '';
    if (ex.limit_bytes > 0 && ex.projected_month_bytes >= 0) {
      const rawPct = Math.round((ex.projected_month_bytes / ex.limit_bytes) * 100);
      const barPct = Math.min(100, Math.max(0, rawPct));
      quotaHTML = `
        <div class="forecast-exhaustion__quota">
          <div class="forecast-exhaustion__meter" aria-hidden="true">
            <span class="forecast-exhaustion__bar" data-an-bar style="--pct:${barPct}"></span>
          </div>
          <div class="forecast-exhaustion__quota-label">${escapeHTML(T('js.analytics.quota_projected', { pct: rawPct }))}</div>
        </div>
      `;
    }

    panel.hidden = false;
    panel.className = 'forecast-exhaustion forecast-exhaustion--' + level + ' an-enter';
    panel.innerHTML = `
      <i data-lucide="${icon}" class="forecast-exhaustion__icon"></i>
      <div class="forecast-exhaustion__body">
        <div class="forecast-exhaustion__title">${escapeHTML(T('js.analytics.exhaustion_title'))}</div>
        <div class="forecast-exhaustion__message">${escapeHTML(message)}</div>
        ${sub ? `<div class="forecast-exhaustion__sub">${escapeHTML(sub)}</div>` : ''}
        ${quotaHTML}
      </div>
    `;
  }

  function renderTrend(trend) {
    const map = {
      increasing: { cls: 'trend-indicator--increasing', icon: 'trending-up', label: T('js.analytics.trend_increasing') },
      decreasing: { cls: 'trend-indicator--decreasing', icon: 'trending-down', label: T('js.analytics.trend_decreasing') },
      stable:     { cls: 'trend-indicator--stable',     icon: 'minus', label: T('js.analytics.trend_stable') },
    };
    const t = map[trend] || map.stable;
    return `<span class="trend-indicator ${t.cls}"><i data-lucide="${t.icon}"></i> ${escapeHTML(t.label)}</span>`;
  }

  function renderConfidence(confidence) {
    const validConfidence = ['low', 'medium', 'high'].includes(confidence) ? confidence : 'low';
    const level = T('js.analytics.confidence_' + validConfidence);
    return `
      <span class="forecast-confidence">
        <span class="confidence-dot confidence-dot--${validConfidence}"></span>
        ${escapeHTML(T('js.analytics.confidence', { level: level }))}
      </span>
    `;
  }

  function renderRisks(risks) {
    const items = [];
    if (risks.traffic_spike) {
      items.push(`<span class="risk-badge"><i data-lucide="zap"></i> ${escapeHTML(T('js.analytics.risk_spike'))}</span>`);
    }
    if (risks.unusual_growth) {
      items.push(`<span class="risk-badge"><i data-lucide="trending-up"></i> ${escapeHTML(T('js.analytics.risk_growth'))}</span>`);
    }
    if (risks.exhaustion) {
      items.push(`<span class="risk-badge"><i data-lucide="alert-triangle"></i> ${escapeHTML(T('js.analytics.risk_exhaustion'))}</span>`);
    }
    return items.length > 0 ? `<div class="risk-badges">${items.join('')}</div>` : '';
  }

  // ── Sliding Range Tabs ────────────────────────────────────────────────────

  let currentRange = '24h';

  function positionRangeIndicator(tabs, indicator, btn) {
    if (!tabs || !indicator || !btn) return;
    const trackRect = tabs.getBoundingClientRect();
    const btnRect = btn.getBoundingClientRect();
    if (!trackRect.width || !btnRect.width) return;
    const x = Math.round(btnRect.left - trackRect.left - (tabs.clientLeft || 0));
    const w = Math.round(btnRect.width);
    tabs.classList.add('has-indicator');
    indicator.style.width = w + 'px';
    indicator.style.transform = `translate3d(${x}px, 0, 0)`;
    indicator.classList.add('is-ready');
  }

  function initRangeTabs(serverID) {
    const tabs = byId('globalRangeTabs');
    if (!tabs) return;
    const indicator = byId('rangeTabIndicator');

    const syncActiveIndicator = () => {
      const activeBtn = tabs.querySelector('.range-tab.active');
      if (activeBtn && indicator) {
        positionRangeIndicator(tabs, indicator, activeBtn);
      }
    };

    requestAnimationFrame(syncActiveIndicator);

    if (typeof ResizeObserver !== 'undefined') {
      const tabRO = new ResizeObserver(() => requestAnimationFrame(syncActiveIndicator));
      tabRO.observe(tabs);
      onCleanup(() => tabRO.disconnect());
    }

    tabs.addEventListener('click', (e) => {
      const btn = e.target.closest('.range-tab');
      if (!btn) return;
      const range = btn.dataset.range;
      if (range === currentRange) return;

      tabs.querySelectorAll('.range-tab').forEach(t => {
        t.classList.remove('active');
        t.setAttribute('aria-selected', 'false');
      });
      btn.classList.add('active');
      btn.setAttribute('aria-selected', 'true');
      positionRangeIndicator(tabs, indicator, btn);

      currentRange = range;
      loadResourceCharts(serverID, range);
    });
  }

  function loadResourceCharts(serverID, range) {
    for (const metric of ['cpu', 'ram', 'disk', 'load']) {
      loadChart(serverID, metric, range);
    }
  }

  function loadAllCharts(serverID, range) {
    loadResourceCharts(serverID, range);
    // Traffic is always 30d (vnstat provides daily totals)
    loadChart(serverID, 'traffic', '30d');
  }

  // ── Init ──────────────────────────────────────────────────────────────────

  function init() {
    initAnimatedBars(rootScope);
    initCountUp(rootScope);

    const firstChart = rootScope.querySelector('[data-metric][data-server]');
    if (!firstChart) return;

    const serverID = firstChart.dataset.server;
    if (!serverID) return;

    initRangeTabs(serverID);
    loadAllCharts(serverID, currentRange);
    loadForecast(serverID);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
