/* analytics.js — colorful SVG time-series charts, motion, sorting & bandwidth forecast UI */
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

  function isRTL() {
    const dirNode = (rootScope.closest && rootScope.closest('[dir]')) || document.documentElement;
    return Boolean(dirNode && dirNode.getAttribute('dir') === 'rtl');
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
    '--surface': '#141415',
  };

  const BACKEND_TOKEN_ALIASES = {
    '--accent': '--accent-soft',
  };

  const ALLOWED_TOKENS = new Set(Object.keys(TOKEN_DEFAULTS));
  const COLOR_RE = /^#[0-9a-fA-F]{3,8}$/;
  let cachedTokens = null;

  function tokenHost() {
    return (rootScope.querySelector && rootScope.querySelector('.analytics-page')) || document.documentElement;
  }

  function readCSSToken(tokenName, fallback) {
    if (!cachedTokens) {
      cachedTokens = {};
      try {
        const cs = getComputedStyle(tokenHost());
        for (const k of ALLOWED_TOKENS) {
          const v = cs.getPropertyValue(k).trim();
          if (v) cachedTokens[k] = v;
        }
      } catch (e) {}
    }
    return cachedTokens[tokenName] || fallback;
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

    // Reveal immediately. Gating on IntersectionObserver left bars empty: a bar
    // collapsed to scaleX(0) has a zero-area box and may never report as
    // intersecting, so its fill was never restored.
    pending.forEach((bar) => revealBarElement(bar, bar.dataset.anBarTarget));
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
      const duration = 500;
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

  const CHART_PAD = { top: 14, right: 18, bottom: 28, left: 50 };
  const NICE_STEPS = [0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 25, 50, 100, 200, 500, 1000, 2000, 5000, 10000];
  let chartInstanceSeq = 0;

  function createSVGEl(tag, attrs) {
    const el = document.createElementNS('http://www.w3.org/2000/svg', tag);
    for (const [k, v] of Object.entries(attrs || {})) el.setAttribute(k, v);
    return el;
  }

  function ceilToNiceJS(v) {
    if (!Number.isFinite(v) || v <= 0) return 1;
    for (let i = 0; i < NICE_STEPS.length; i++) {
      if (v <= NICE_STEPS[i]) return NICE_STEPS[i];
    }
    return v * 1.2;
  }

  function effectiveYMax(data, hiddenSet) {
    const unit = data.unit || '';
    if (unit === '%') return data.max ?? 100;
    const series = data.series || [];
    let maxVal = 0;
    let found = false;
    series.forEach((s, idx) => {
      if (hiddenSet && hiddenSet.has(idx)) return;
      if (!Array.isArray(s.data)) return;
      for (let i = 0; i < s.data.length; i++) {
        const v = s.data[i];
        if (Number.isFinite(v) && (!found || v > maxVal)) {
          maxVal = v;
          found = true;
        }
      }
    });
    if (!found) return data.max ?? 1;
    return ceilToNiceJS(maxVal);
  }

  function formatMetricValue(rawV, unit) {
    const v = Number.isFinite(rawV) ? rawV : 0;
    if (unit === '%') return v.toFixed(1) + '%';
    if (unit === 'GB') {
      if (v > 0 && v < 0.95) {
        const mb = v * 1024;
        return mb.toFixed(mb < 10 ? 1 : 0) + ' MB';
      }
      if (v >= 1024) {
        return (v / 1024).toFixed(2) + ' TB';
      }
      return v.toFixed(v < 10 ? 2 : 1) + ' GB';
    }
    return v.toFixed(2) + (unit ? ' ' + unit : '');
  }

  function formatAxisValue(v, unit, yMax) {
    if (unit === '%') return v.toFixed(0);
    if (unit === 'GB') {
      if (v === 0) return '0';
      if (yMax >= 1024) return (v / 1024).toFixed(1) + 'T';
      if (yMax < 0.25) return v.toFixed(2);
      if (yMax < 2) return parseFloat(v.toFixed(2)).toString();
      return v.toFixed(yMax >= 20 ? 0 : 1);
    }
    if (v === 0) return '0';
    if (v >= 1000) return (v / 1000).toFixed(1) + 'k';
    if (yMax < 0.5) return parseFloat(v.toFixed(2)).toString();
    return v.toFixed(yMax < 10 ? 1 : 0);
  }

  function computeYTicks(min, max, count) {
    const step = (max - min) / count;
    const ticks = [];
    for (let i = 0; i <= count; i++) {
      ticks.push(+(min + step * i).toFixed(4));
    }
    return ticks;
  }

  function pointX(idx, len, pw) {
    if (len <= 1) return pw / 2;
    return (idx / (len - 1)) * pw;
  }

  function clampY(y, ph) {
    if (!Number.isFinite(y)) return ph;
    return Math.max(0, Math.min(ph, y));
  }

  function buildChart(container, data, opts) {
    if (!container || !data) return;
    const animate = !(opts && opts.animate === false);
    container.__chartData = data;
    if (!container.__hiddenSeries) container.__hiddenSeries = new Set();

    const rawW = Math.round(container.clientWidth || 0);
    const rawH = Math.round(container.clientHeight || 0);
    // Store actual measured dimensions (0 when hidden) so ResizeObserver always
    // triggers a clean redraw once a background tab becomes visible.
    container.__lastW = rawW;
    container.__lastH = rawH;

    const w = Math.max(160, rawW || 420);
    const h = Math.max(96, rawH || 200);

    container.innerHTML = '';
    const pw = Math.max(20, w - CHART_PAD.left - CHART_PAD.right);
    const ph = Math.max(20, h - CHART_PAD.top - CHART_PAD.bottom);

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
    const yMax = effectiveYMax(data, container.__hiddenSeries);
    const yRange = yMax - yMin || 1;
    const unit = data.unit || '';

    // Y-axis grid lines and deduplicated labels
    const yTicks = computeYTicks(yMin, yMax, 4);
    let prevLabelText = null;
    for (const tick of yTicks) {
      const y = clampY(ph - ((tick - yMin) / yRange) * ph, ph);
      plotG.appendChild(createSVGEl('line', {
        class: 'chart-grid-line',
        x1: 0, y1: y.toFixed(2), x2: pw, y2: y.toFixed(2),
      }));
      const txt = formatAxisValue(tick, unit, yMax);
      if (txt === prevLabelText) continue;
      prevLabelText = txt;
      const axisLabel = createSVGEl('text', {
        class: 'chart-axis-label',
        x: -8, y: (y + 4).toFixed(2),
        'text-anchor': 'end',
      });
      axisLabel.textContent = txt;
      plotG.appendChild(axisLabel);
    }

    // X-axis labels with edge-aware anchoring so first/last timestamps never clip
    if (labels.length === 1) {
      const singleLbl = createSVGEl('text', {
        class: 'chart-axis-label',
        x: (pw / 2).toFixed(2),
        y: ph + 18,
        'text-anchor': 'middle',
      });
      singleLbl.textContent = labels[0];
      plotG.appendChild(singleLbl);
    } else if (labels.length > 1) {
      const maxTicks = pw < 280 ? 4 : pw < 460 ? 5 : 6;
      const xLabelCount = Math.min(maxTicks, labels.length);
      const xStep = (labels.length - 1) / Math.max(xLabelCount - 1, 1);
      let lastDrawnIdx = -1;
      for (let i = 0; i < xLabelCount; i++) {
        const idx = Math.round(i * xStep);
        if (idx >= labels.length || idx === lastDrawnIdx) continue;
        lastDrawnIdx = idx;
        const x = pointX(idx, labels.length, pw);
        const anchor = idx === 0 ? 'start' : (idx === labels.length - 1 ? 'end' : 'middle');
        const axisLabel = createSVGEl('text', {
          class: 'chart-axis-label',
          x: x.toFixed(2),
          y: ph + 18,
          'text-anchor': anchor,
        });
        axisLabel.textContent = labels[idx];
        plotG.appendChild(axisLabel);
      }
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

      const pts = s.data.map((rawV, i) => {
        const v = Number.isFinite(rawV) ? rawV : 0;
        return {
          x: pointX(i, s.data.length, pw),
          y: clampY(ph - ((v - yMin) / yRange) * ph, ph),
          v,
        };
      });

      const seriesG = createSVGEl('g', {
        class: 'chart-series' + (container.__hiddenSeries.has(sIdx) ? ' is-hidden-series' : ''),
        'data-series-idx': String(sIdx),
      });

      if (pts.length === 1) {
        // Single-sample series: draw subtle baseline band + dashed reference line
        // + centered beacon so freshly added servers look intentional, not blank.
        const pt = pts[0];
        const bandD = `M 0 ${ph} L 0 ${pt.y.toFixed(2)} L ${pw.toFixed(2)} ${pt.y.toFixed(2)} L ${pw.toFixed(2)} ${ph} Z`;
        seriesG.appendChild(createSVGEl('path', {
          class: 'chart-area',
          d: bandD,
          fill: `url(#${gradId})`,
        }));
        seriesG.appendChild(createSVGEl('line', {
          class: 'chart-single-ref',
          x1: 0, y1: pt.y.toFixed(2), x2: pw.toFixed(2), y2: pt.y.toFixed(2),
          stroke: strokeColor,
        }));
      } else {
        const areaD = buildAreaPath(pts, ph);
        seriesG.appendChild(createSVGEl('path', {
          class: 'chart-area' + (animate && !prefersReducedMotion ? ' is-revealing' : ''),
          d: areaD,
          fill: `url(#${gradId})`,
        }));

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
        if (animate && !prefersReducedMotion && rawW > 0) {
          linePathsToAnimate.push(linePath);
        }
      }

      // Highlight interior peak point on single-series percentage charts
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

    if (linePathsToAnimate.length && rawW > 0) {
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
    let resizeTimer = null;
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const nw = Math.round(entry.contentRect.width);
        const nh = Math.round(entry.contentRect.height);
        if (nw > 0 && nh > 0 && (nw !== container.__lastW || nh !== container.__lastH)) {
          if (container.__chartData && !container.hidden) {
            if (resizeTimer) clearTimeout(resizeTimer);
            resizeTimer = setTimeout(() => {
              resizeTimer = null;
              if (container.__chartData && !container.hidden) {
                buildChart(container, container.__chartData, { animate: false });
              }
            }, 120);
          }
        }
      }
    });
    ro.observe(container);
    container.__ro = ro;
    onCleanup(() => {
      if (resizeTimer) { clearTimeout(resizeTimer); resizeTimer = null; }
      ro.disconnect();
    });
  }

  function buildInteractionOverlay(container, plotG, pw, ph, labels, series, yMin, yRange, unit, metricName) {
    const rect = createSVGEl('rect', {
      x: 0, y: 0, width: pw, height: ph,
      fill: 'transparent', cursor: 'crosshair',
    });

    const seriesColors = series.map((s) => resolveSeriesColor(s, metricName));
    const persistentDots = series.map((_, sIdx) => {
      const dot = createSVGEl('circle', {
        class: 'chart-tooltip-dot',
        cx: 0, cy: 0, r: 4.5,
        fill: seriesColors[sIdx],
      });
      dot.style.display = 'none';
      plotG.appendChild(dot);
      return dot;
    });

    let tooltipEl = null;
    let vline = null;
    let touchHideTimer = null;
    let cachedSvgRect = null;
    let cachedContainerRect = null;
    let cachedTooltipW = 156;
    let cachedTooltipH = 58;
    let lastIdx = -1;
    let moveRAF = 0;
    let pendingPointer = null;

    onCleanup(() => {
      if (touchHideTimer) { clearTimeout(touchHideTimer); touchHideTimer = null; }
      if (moveRAF) { cancelAnimationFrame(moveRAF); moveRAF = 0; }
    });

    function cacheBounds() {
      const svgEl = rect.closest('svg');
      cachedSvgRect = svgEl ? svgEl.getBoundingClientRect() : null;
      cachedContainerRect = container.getBoundingClientRect();
    }

    function clearBounds() {
      cachedSvgRect = null;
      cachedContainerRect = null;
    }

    function paintAtPointer(clientX, clientY) {
      if (!labels.length) return;
      if (touchHideTimer) {
        clearTimeout(touchHideTimer);
        touchHideTimer = null;
      }
      if (!cachedSvgRect || !cachedContainerRect) cacheBounds();
      if (!cachedSvgRect || !cachedSvgRect.width) return;

      const svgEl = rect.closest('svg');
      const vbWidth = (svgEl && svgEl.viewBox && svgEl.viewBox.baseVal && svgEl.viewBox.baseVal.width) || cachedSvgRect.width;
      const scaleX = vbWidth / cachedSvgRect.width;
      const localX = (clientX - cachedSvgRect.left) * scaleX - CHART_PAD.left;
      const clampedX = Math.max(0, Math.min(pw, localX));
      const idx = labels.length <= 1 ? 0 : Math.round((clampedX / pw) * (labels.length - 1));
      if (idx < 0 || idx >= labels.length) return;

      const hidden = container.__hiddenSeries || new Set();

      if (idx !== lastIdx) {
        lastIdx = idx;
        const xPos = pointX(idx, labels.length, pw);

        if (!vline) {
          vline = createSVGEl('line', {
            class: 'chart-tooltip-line',
            y1: 0, y2: ph,
          });
          plotG.appendChild(vline);
        }
        vline.setAttribute('x1', xPos.toFixed(2));
        vline.setAttribute('x2', xPos.toFixed(2));
        vline.style.display = '';

        let visibleRows = 0;
        for (let sIdx = 0; sIdx < series.length; sIdx++) {
          const s = series[sIdx];
          const dot = persistentDots[sIdx];
          if (hidden.has(sIdx) || !s.data || idx >= s.data.length) {
            dot.style.display = 'none';
            continue;
          }
          visibleRows++;
          const v = Number.isFinite(s.data[idx]) ? s.data[idx] : 0;
          const y = clampY(ph - ((v - yMin) / yRange) * ph, ph);
          dot.setAttribute('cx', xPos.toFixed(2));
          dot.setAttribute('cy', y.toFixed(2));
          dot.style.display = '';
        }

        if (!visibleRows) {
          hideTooltip();
          return;
        }

        if (!tooltipEl) {
          tooltipEl = document.createElement('div');
          tooltipEl.className = 'chart-tooltip';
          if (isRTL()) tooltipEl.setAttribute('dir', 'rtl');
          container.appendChild(tooltipEl);
        }

        let html = `<div class="chart-tooltip__label" dir="ltr">${escapeHTML(labels[idx])}</div>`;
        for (let sIdx = 0; sIdx < series.length; sIdx++) {
          const s = series[sIdx];
          if (hidden.has(sIdx) || !s.data || idx >= s.data.length) continue;
          const formatted = formatMetricValue(s.data[idx], unit);
          html += `<div class="chart-tooltip__row">
            <span class="chart-tooltip__series">
              <span class="chart-tooltip__dot" style="background:${seriesColors[sIdx]}"></span>
              <span>${escapeHTML(s.label)}</span>
            </span>
            <span class="chart-tooltip__value" dir="ltr">${escapeHTML(formatted)}</span>
          </div>`;
        }
        tooltipEl.innerHTML = html;
        cachedTooltipW = tooltipEl.offsetWidth || 156;
        cachedTooltipH = tooltipEl.offsetHeight || 58;
      }

      if (!tooltipEl) return;
      const cRect = cachedContainerRect;
      const pointerX = clientX - cRect.left;
      const pointerY = clientY - cRect.top;
      let tx = pointerX + 14;
      if (tx + cachedTooltipW > cRect.width - 8) {
        tx = pointerX - cachedTooltipW - 14;
      }
      tx = Math.max(8, Math.min(tx, Math.max(8, cRect.width - cachedTooltipW - 8)));
      let ty = pointerY - 14;
      ty = Math.max(4, Math.min(ty, Math.max(4, cRect.height - cachedTooltipH - 4)));

      tooltipEl.style.transform = `translate3d(${Math.round(tx)}px, ${Math.round(ty)}px, 0)`;
      tooltipEl.style.opacity = '1';
    }

    function schedulePointer(clientX, clientY) {
      pendingPointer = { x: clientX, y: clientY };
      if (moveRAF) return;
      moveRAF = requestAnimationFrame(() => {
        moveRAF = 0;
        const p = pendingPointer;
        pendingPointer = null;
        if (p) paintAtPointer(p.x, p.y);
      });
    }

    function hideTooltip() {
      if (moveRAF) { cancelAnimationFrame(moveRAF); moveRAF = 0; }
      pendingPointer = null;
      lastIdx = -1;
      clearBounds();
      if (vline) vline.style.display = 'none';
      for (let i = 0; i < persistentDots.length; i++) {
        persistentDots[i].style.display = 'none';
      }
      if (tooltipEl) tooltipEl.style.opacity = '0';
    }

    rect.addEventListener('mouseenter', cacheBounds);
    rect.addEventListener('mousemove', (e) => {
      schedulePointer(e.clientX, e.clientY);
    });
    rect.addEventListener('mouseleave', hideTooltip);

    rect.addEventListener('touchstart', (e) => {
      if (!e.touches || !e.touches.length) return;
      cacheBounds();
      const t = e.touches[0];
      paintAtPointer(t.clientX, t.clientY);
    }, { passive: true });

    rect.addEventListener('touchmove', (e) => {
      if (!e.touches || !e.touches.length) return;
      const t = e.touches[0];
      schedulePointer(t.clientX, t.clientY);
    }, { passive: true });

    rect.addEventListener('touchend', () => {
      if (touchHideTimer) clearTimeout(touchHideTimer);
      touchHideTimer = setTimeout(hideTooltip, 1600);
    }, { passive: true });

    return rect;
  }

  function buildLinePath(pts) {
    if (pts.length === 0) return '';
    if (pts.length > 120) {
      let d = `M ${pts[0].x.toFixed(1)} ${pts[0].y.toFixed(1)}`;
      for (let i = 1; i < pts.length; i++) {
        d += ` L ${pts[i].x.toFixed(1)} ${pts[i].y.toFixed(1)}`;
      }
      return d;
    }
    let d = `M ${pts[0].x.toFixed(2)} ${pts[0].y.toFixed(2)}`;
    for (let i = 1; i < pts.length; i++) {
      const cp1x = (pts[i - 1].x + pts[i].x) / 2;
      d += ` C ${cp1x.toFixed(2)} ${pts[i - 1].y.toFixed(2)}, ${cp1x.toFixed(2)} ${pts[i].y.toFixed(2)}, ${pts[i].x.toFixed(2)} ${pts[i].y.toFixed(2)}`;
    }
    return d;
  }

  function buildAreaPath(pts, ph) {
    if (pts.length === 0) return '';
    let d = `M ${pts[0].x.toFixed(2)} ${ph}`;
    d += ` L ${pts[0].x.toFixed(2)} ${pts[0].y.toFixed(2)}`;
    if (pts.length > 120) {
      for (let i = 1; i < pts.length; i++) {
        d += ` L ${pts[i].x.toFixed(1)} ${pts[i].y.toFixed(1)}`;
      }
    } else {
      for (let i = 1; i < pts.length; i++) {
        const cp1x = (pts[i - 1].x + pts[i].x) / 2;
        d += ` C ${cp1x.toFixed(2)} ${pts[i - 1].y.toFixed(2)}, ${cp1x.toFixed(2)} ${pts[i].y.toFixed(2)}, ${pts[i].x.toFixed(2)} ${pts[i].y.toFixed(2)}`;
      }
    }
    d += ` L ${pts[pts.length - 1].x.toFixed(2)} ${ph} Z`;
    return d;
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
    if (unit === '%' && (nonEmpty.length === 1 || metric === 'ram')) {
      const vals = nonEmpty[0].data;
      const cur = Number.isFinite(vals[vals.length - 1]) ? vals[vals.length - 1] : 0;
      const lvl = cur > 80 ? 'crit' : cur > 60 ? 'warn' : 'ok';
      badge.classList.add('chart-card__live-val--' + lvl);
      badge.textContent = cur.toFixed(1) + '%';
      badge.hidden = false;
      return;
    }

    if (metric === 'load') {
      const vals = nonEmpty[0].data;
      const cur = Number.isFinite(vals[vals.length - 1]) ? vals[vals.length - 1] : 0;
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
      badge.textContent = `↓ ${formatMetricValue(rx, 'GB')} · ↑ ${formatMetricValue(tx, 'GB')}`;
      badge.hidden = false;
      return;
    }

    badge.hidden = true;
  }

  function syncSeriesToggleButtons(metric, hiddenSet) {
    const legend = byId('legend-' + metric);
    const footer = byId('footer-' + metric);
    [legend, footer].forEach((host) => {
      if (!host) return;
      host.querySelectorAll('[data-series-idx]').forEach((btn) => {
        const idx = parseInt(btn.dataset.seriesIdx, 10);
        if (Number.isNaN(idx)) return;
        const muted = hiddenSet.has(idx);
        btn.classList.toggle('is-muted', muted);
        btn.setAttribute('aria-pressed', muted ? 'false' : 'true');
      });
    });
  }

  function toggleSeriesVisibility(metric, chartEl, sIdx) {
    if (!chartEl || !chartEl.__chartData) return;
    if (!chartEl.__hiddenSeries) chartEl.__hiddenSeries = new Set();
    const hidden = chartEl.__hiddenSeries;
    if (hidden.has(sIdx)) {
      hidden.delete(sIdx);
    } else {
      const totalSeries = (chartEl.__chartData.series || []).filter(s => Array.isArray(s.data) && s.data.length > 0).length;
      if (totalSeries > 1 && hidden.size >= totalSeries - 1) {
        return;
      }
      hidden.add(sIdx);
    }
    syncSeriesToggleButtons(metric, hidden);
    // Rebuild without stroke animation so Y-axis auto-scales cleanly to visible series.
    buildChart(chartEl, chartEl.__chartData, { animate: false });
  }

  function statPillHTML(label, valStr) {
    return `<span class="chart-stat-pill"><span class="chart-stat-pill__key">${escapeHTML(label)}</span> <span class="chart-stat-val" dir="ltr">${escapeHTML(valStr)}</span></span>`;
  }

  function updateFooter(metric, data, chartEl) {
    const footer = byId('footer-' + metric);
    if (!footer) return;
    if (!data || !Array.isArray(data.series) || data.series.length === 0) {
      footer.innerHTML = '';
      return;
    }

    const unit = data.unit || '';
    const nonEmpty = data.series.filter(s => Array.isArray(s.data) && s.data.length > 0);
    if (nonEmpty.length === 0) {
      footer.innerHTML = '';
      return;
    }

    if (metric === 'traffic' && nonEmpty.length >= 2) {
      const rxSum = nonEmpty[0].data.reduce((a, b) => a + (Number.isFinite(b) ? b : 0), 0);
      const txSum = nonEmpty[1].data.reduce((a, b) => a + (Number.isFinite(b) ? b : 0), 0);
      const peakDay = Math.max(...nonEmpty[0].data, ...nonEmpty[1].data);
      footer.innerHTML = `<div class="chart-stat-group">
        ${statPillHTML(T('js.analytics.total_30d'), `↓ ${formatMetricValue(rxSum, 'GB')} · ↑ ${formatMetricValue(txSum, 'GB')}`)}
        ${statPillHTML(T('js.analytics.peak'), formatMetricValue(peakDay, 'GB'))}
      </div>`;
      return;
    }

    if (nonEmpty.length === 1) {
      const values = nonEmpty[0].data.map(v => (Number.isFinite(v) ? v : 0));
      const avg = values.reduce((a, b) => a + b, 0) / values.length;
      const max = Math.max(...values);
      const min = Math.min(...values);
      footer.innerHTML = `<div class="chart-stat-group">
        ${statPillHTML(T('js.analytics.avg'), formatMetricValue(avg, unit))}
        ${statPillHTML(T('js.analytics.peak'), formatMetricValue(max, unit))}
        ${statPillHTML(T('js.analytics.min'), formatMetricValue(min, unit))}
        <span class="chart-stat-samples">${escapeHTML(T('js.analytics.samples', { count: values.length }))}</span>
      </div>`;
      return;
    }

    const hidden = (chartEl && chartEl.__hiddenSeries) || new Set();
    footer.innerHTML = `<div class="chart-footer-series">${data.series.map((s, sIdx) => {
      if (!Array.isArray(s.data) || s.data.length === 0) return '';
      const vals = s.data.map(v => (Number.isFinite(v) ? v : 0));
      const avg = vals.reduce((a, b) => a + b, 0) / vals.length;
      const max = Math.max(...vals);
      const cur = vals[vals.length - 1];
      const color = resolveSeriesColor(s, data.metric);
      const muted = hidden.has(sIdx);
      const aria = T('js.analytics.toggle_series', { label: s.label });
      return `<button type="button" class="chart-footer-item chart-footer-item--toggle${muted ? ' is-muted' : ''}" data-series-idx="${sIdx}" aria-pressed="${muted ? 'false' : 'true'}" aria-label="${escapeHTML(aria)}">
        <span class="legend-dot" style="background:${color}"></span>
        <span class="chart-footer-item__label">${escapeHTML(s.label)}</span>
        <span class="chart-stat-val" dir="ltr">${escapeHTML(formatMetricValue(cur, unit))}</span>
        <span class="chart-footer-item__sub">(${escapeHTML(T('js.analytics.avg'))} <span class="chart-stat-val" dir="ltr">${escapeHTML(formatMetricValue(avg, unit))}</span> · ${escapeHTML(T('js.analytics.peak'))} <span class="chart-stat-val" dir="ltr">${escapeHTML(formatMetricValue(max, unit))}</span>)</span>
      </button>`;
    }).join('')}</div>`;

    footer.querySelectorAll('.chart-footer-item--toggle').forEach((btn) => {
      btn.addEventListener('click', () => {
        const idx = parseInt(btn.dataset.seriesIdx, 10);
        if (!Number.isNaN(idx)) toggleSeriesVisibility(metric, chartEl, idx);
      });
    });
  }

  let batchAbort = null;
  let batchGen = 0;

  onCleanup(() => {
    if (batchAbort) { try { batchAbort.abort(); } catch (e) {} batchAbort = null; }
  });

  function mergeRamWithSwap(ramChart, swapChart) {
    if (!ramChart || !Array.isArray(ramChart.series) || !ramChart.series.length) return ramChart;
    if (!swapChart || !Array.isArray(swapChart.series) || !swapChart.series.length) return ramChart;
    const swapSeries = swapChart.series[0];
    if (!Array.isArray(swapSeries.data) || !swapSeries.data.some(v => Number.isFinite(v) && v > 0.05)) {
      return ramChart;
    }
    return Object.assign({}, ramChart, {
      series: [ramChart.series[0], swapSeries],
    });
  }

  function renderChartPayload(metric, data) {
    if (!data || !data.series || data.series.length === 0 || !data.series.some(s => s.data && s.data.length > 0)) {
      showEmpty(metric);
      const footer = byId('footer-' + metric);
      if (footer) footer.innerHTML = '';
      return;
    }
    showChart(metric);
    const ct = byId('chart-' + metric);
    if (ct) {
      requestAnimationFrame(() => {
        buildChart(ct, data, { animate: true });
        updateCardLiveBadge(metric, data);
        updateFooter(metric, data, ct);
        if (metric === 'traffic') {
          buildLegend('legend-traffic', 'traffic', data.series, data.metric, ct);
        }
      });
    }
  }

  async function loadBatchedCharts(serverID, cardMetrics, range) {
    if (batchAbort) {
      try { batchAbort.abort(); } catch (e) {}
    }
    const ctrl = new AbortController();
    batchAbort = ctrl;
    const gen = ++batchGen;

    const queryMetrics = cardMetrics.slice();
    if (cardMetrics.includes('ram') && !queryMetrics.includes('swap')) {
      queryMetrics.push('swap');
    }

    cardMetrics.forEach((m) => showSkeleton(m));
    try {
      const url = `/servers/${serverID}/analytics/data?metrics=${encodeURIComponent(queryMetrics.join(','))}&range=${encodeURIComponent(range)}`;
      const resp = await fetch(url, { credentials: 'same-origin', signal: ctrl.signal });
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      const payload = await resp.json();
      if (gen !== batchGen) return false;
      const charts = (payload && payload.charts) || {};
      if (charts.ram && charts.swap) {
        charts.ram = mergeRamWithSwap(charts.ram, charts.swap);
      }
      cardMetrics.forEach((m) => renderChartPayload(m, charts[m]));
      return true;
    } catch (e) {
      if (e && e.name === 'AbortError') return false;
      if (gen !== batchGen) return false;
      cardMetrics.forEach((m) => showError(m));
      return false;
    } finally {
      if (batchAbort === ctrl) batchAbort = null;
    }
  }

  function buildLegend(containerId, metric, series, fallbackMetric, chartEl) {
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
        if (!Number.isNaN(idx)) toggleSeriesVisibility(metric, chartEl, idx);
      });
    });
  }

  // ── Forecast UI ───────────────────────────────────────────────────────────

  async function loadForecast(serverID) {
    const grid = byId('forecastGrid');
    if (!grid) return false;

    try {
      const resp = await fetch(`/servers/${serverID}/analytics/forecast`, { credentials: 'same-origin' });
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      const f = await resp.json();
      renderForecast(f);
      return true;
    } catch (e) {
      if (grid) {
        grid.innerHTML = `<div class="forecast-error-card">
          <i data-lucide="alert-circle"></i>
          <p>${escapeHTML(T('js.analytics.forecast_unavailable'))}</p>
          <button type="button" class="btn btn--ghost btn--xs" data-forecast-retry><i data-lucide="refresh-cw" class="icon-in-button"></i> ${escapeHTML(T('analytics.retry'))}</button>
        </div>`;
        renderIcons(grid);
        const retryBtn = grid.querySelector('[data-forecast-retry]');
        if (retryBtn) {
          retryBtn.addEventListener('click', () => {
            grid.innerHTML = `<div class="forecast-skeleton an-shimmer"></div><div class="forecast-skeleton an-shimmer"></div><div class="forecast-skeleton an-shimmer"></div>`;
            loadForecast(serverID);
          });
        }
      }
      return false;
    }
  }

  function syncForecastHeader(series) {
    const badge = byId('forecastScopeBadge');
    const sub = byId('forecastSubtitle');
    const kind = series === 'tx' ? 'tx' : (series === 'total' ? 'total' : 'rx');
    if (badge) {
      const icon = kind === 'tx' ? 'arrow-up' : (kind === 'total' ? 'arrow-up-down' : 'arrow-down');
      const lbl = kind === 'tx' ? T('analytics.upload') : (kind === 'total' ? T('analytics.total') : T('analytics.download'));
      badge.innerHTML = `<i data-lucide="${icon}"></i> <span>${escapeHTML(lbl)}</span>`;
      renderIcons(badge);
    }
    if (sub) {
      const subKey = kind === 'tx' ? 'js.analytics.forecast_sub_tx'
        : (kind === 'total' ? 'js.analytics.forecast_sub_total' : 'js.analytics.forecast_sub_rx');
      sub.textContent = T(subKey);
    }
  }

  function renderForecast(f) {
    const grid = byId('forecastGrid');
    if (!grid) return;

    const firstRender = grid.dataset.loadedOnce !== '1';
    grid.dataset.loadedOnce = '1';

    syncForecastHeader(f.series);

    const trendHTML = renderTrend(f.trend);
    const confidenceHTML = renderConfidence(f.confidence);
    const risksHTML = renderRisks(f.risks || {});
    const isExhaustRisk = Boolean(f.risks && f.risks.exhaustion);

    const scope = seriesScopeLabel(f.series);
    const periodNote = anchoredPeriodNote(f.period_start, f.period_end);
    grid.innerHTML = `
      ${renderPeriodCard(T('js.analytics.today'), f.today, f.today.pct_elapsed, scope, '', 'today', 0, false, firstRender)}
      ${renderPeriodCard(T('js.analytics.this_week'), f.this_week, f.this_week.pct_elapsed, scope, '', 'week', 1, false, firstRender)}
      ${renderPeriodCard(T('js.analytics.this_month'), f.this_month, f.this_month.pct_elapsed, scope, periodNote, 'month', 2, isExhaustRisk, firstRender)}
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

    renderExhaustion(f.exhaustion, f.series);

    const exhaustionPanel = byId('forecastExhaustion');
    renderIcons(grid);
    renderIcons(meta);
    renderIcons(exhaustionPanel);
    initAnimatedBars(grid);
    initAnimatedBars(exhaustionPanel);
    if (firstRender) {
      initCountUp(grid);
    }
  }

  function seriesScopeLabel(series) {
    if (series === 'tx') return T('js.analytics.upload_arrow_scope');
    if (series === 'total') return T('js.analytics.total_arrow_scope');
    return T('js.analytics.download_scope');
  }

  function anchoredPeriodNote(start, end) {
    if (!start || !end || start.slice(-2) === '01') return '';
    return T('js.analytics.period_range', { start: start, end: end });
  }

  function renderPeriodCard(label, period, pct, scopeLabel, periodNote, theme, staggerIdx, isRisk, animateEnter) {
    const clampedPct = Math.min(100, Math.max(0, pct || 0));
    const progressClass = clampedPct > 90 ? 'forecast-progress__bar--danger'
      : clampedPct > 70 ? 'forecast-progress__bar--warn'
      : '';
    const enterCls = animateEnter ? ' an-enter' : '';
    const countupAttr = animateEnter ? ' data-countup' : '';
    return `
      <div class="forecast-card forecast-card--${escapeHTML(theme)}${isRisk ? ' forecast-card--risk' : ''}${enterCls}" style="--stagger:${staggerIdx}">
        <div class="forecast-card__label"><span>${escapeHTML(label)}</span> <span class="forecast-card__scope">${escapeHTML(scopeLabel)}</span></div>
        <div class="forecast-card__current" dir="ltr"${countupAttr}>${escapeHTML(period.current_human)}</div>
        <div class="forecast-card__predicted">
          ${escapeHTML(T('js.analytics.predicted_end'))} <strong dir="ltr">${escapeHTML(period.predicted_human)}</strong>
        </div>
        <div class="forecast-progress">
          <div class="forecast-progress__bar ${progressClass}" data-an-bar style="--pct:${clampedPct}"></div>
        </div>
        <div class="forecast-progress__label">${escapeHTML(T('js.analytics.period_elapsed', { pct: clampedPct }))}${periodNote ? ` · ${escapeHTML(periodNote)}` : ''}</div>
      </div>
    `;
  }

  function renderExhaustion(ex, series) {
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

    const titleKey = series === 'tx' ? 'js.analytics.exhaustion_title_tx'
      : (series === 'total' ? 'js.analytics.exhaustion_title_total' : 'js.analytics.exhaustion_title');

    panel.hidden = false;
    panel.className = 'forecast-exhaustion forecast-exhaustion--' + level + ' an-enter';
    panel.innerHTML = `
      <i data-lucide="${icon}" class="forecast-exhaustion__icon"></i>
      <div class="forecast-exhaustion__body">
        <div class="forecast-exhaustion__title">${escapeHTML(T(titleKey))}</div>
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

  // ── Sliding Range Tabs, URL Sync & Manual Refresh ─────────────────────────

  const VALID_RANGES = new Set(['1h', '6h', '24h', '7d', '30d']);
  let currentRange = '24h';

  function detectInitialRange() {
    try {
      const params = new URLSearchParams(window.location.search || '');
      const r = (params.get('range') || '').toLowerCase();
      if (VALID_RANGES.has(r)) return r;
    } catch (e) {}
    return '24h';
  }

  function syncRangeURL(serverID, range) {
    try {
      const expectedPrefix = `/servers/${serverID}/analytics`;
      if (!window.location.pathname.startsWith(expectedPrefix)) return;
      const u = new URL(window.location.href);
      if (range === '24h') {
        u.searchParams.delete('range');
      } else {
        u.searchParams.set('range', range);
      }
      window.history.replaceState(window.history.state, '', u.pathname + u.search + u.hash);
    } catch (e) {}
  }

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

  function activateRangeTab(tabs, indicator, btn, focusBtn) {
    tabs.querySelectorAll('.range-tab').forEach((t) => {
      const active = t === btn;
      t.classList.toggle('active', active);
      t.setAttribute('aria-selected', active ? 'true' : 'false');
      t.setAttribute('tabindex', active ? '0' : '-1');
    });
    if (focusBtn) btn.focus();
    positionRangeIndicator(tabs, indicator, btn);
  }

  function initRangeTabs(serverID) {
    const tabs = byId('globalRangeTabs');
    if (!tabs) return;
    const indicator = byId('rangeTabIndicator');

    const initialBtn = tabs.querySelector(`.range-tab[data-range="${currentRange}"]`);
    if (initialBtn) {
      activateRangeTab(tabs, indicator, initialBtn, false);
    }

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

    function selectRangeButton(btn, focusBtn) {
      if (!btn) return;
      const range = btn.dataset.range;
      if (!range || range === currentRange) return;
      activateRangeTab(tabs, indicator, btn, focusBtn);
      currentRange = range;
      syncRangeURL(serverID, range);
      loadResourceCharts(serverID, range);
    }

    tabs.addEventListener('click', (e) => {
      const btn = e.target.closest('.range-tab');
      if (btn) selectRangeButton(btn, false);
    });

    tabs.addEventListener('keydown', (e) => {
      const items = Array.from(tabs.querySelectorAll('.range-tab'));
      if (!items.length) return;
      const curIdx = items.indexOf(document.activeElement);
      if (curIdx < 0) return;

      const rtl = isRTL();
      let nextIdx = -1;
      if (e.key === 'ArrowRight') {
        nextIdx = rtl ? (curIdx - 1 + items.length) % items.length : (curIdx + 1) % items.length;
      } else if (e.key === 'ArrowLeft') {
        nextIdx = rtl ? (curIdx + 1) % items.length : (curIdx - 1 + items.length) % items.length;
      } else if (e.key === 'Home') {
        nextIdx = 0;
      } else if (e.key === 'End') {
        nextIdx = items.length - 1;
      }
      if (nextIdx >= 0) {
        e.preventDefault();
        selectRangeButton(items[nextIdx], true);
      }
    });
  }

  function initRefreshAndRetry(serverID) {
    const refreshBtn = byId('analyticsRefreshBtn');
    if (refreshBtn) {
      refreshBtn.addEventListener('click', async () => {
        if (refreshBtn.disabled) return;
        refreshBtn.disabled = true;
        refreshBtn.classList.add('is-spinning');
        try {
          const [chartsOk] = await Promise.all([
            loadAllCharts(serverID, currentRange),
            loadForecast(serverID),
          ]);
          if (chartsOk && typeof window.nxToast === 'function') {
            window.nxToast(T('js.analytics.refreshed'), 'info', 1800);
          }
        } finally {
          refreshBtn.disabled = false;
          refreshBtn.classList.remove('is-spinning');
        }
      });
    }

    rootScope.querySelectorAll('[data-chart-retry]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const m = btn.dataset.chartRetry;
        if (!m) return;
        if (m === 'traffic') {
          loadBatchedCharts(serverID, ['traffic'], currentRange);
        } else {
          loadResourceCharts(serverID, currentRange);
        }
      });
    });
  }

  function loadResourceCharts(serverID, range) {
    return loadBatchedCharts(serverID, ['cpu', 'ram', 'disk', 'load'], range);
  }

  function loadAllCharts(serverID, range) {
    return loadBatchedCharts(serverID, ['cpu', 'ram', 'disk', 'load', 'traffic'], range);
  }

  // ── Form Helpers & Preset Chips ───────────────────────────────────────────

  function postFormJSON(form, retried) {
    const token = (typeof window.nxCSRF === 'function' ? window.nxCSRF(form) : '') ||
      ((form.querySelector('input[name="_csrf_token"]') || {}).value || '');
    const params = new URLSearchParams(new FormData(form));
    if (token) params.set('_csrf_token', token);
    const headers = {
      'Accept': 'application/json',
      'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8',
    };
    if (token) headers['X-CSRF-Token'] = token;

    return fetch(form.getAttribute('action') || form.action, {
      method: 'POST',
      credentials: 'same-origin',
      headers: headers,
      body: params.toString(),
    }).then((resp) => {
      const hdr = resp.headers && resp.headers.get('X-CSRF-Token');
      if (hdr && typeof window.nxSetCSRF === 'function') window.nxSetCSRF(hdr);
      if (resp.status === 403 && !retried && window.NodexiaTabs && typeof window.NodexiaTabs.refreshCSRFToken === 'function') {
        return window.NodexiaTabs.refreshCSRFToken().then(() => postFormJSON(form, true));
      }
      return resp.json().catch(() => ({ ok: false }));
    });
  }

  function syncPresetHighlight(presetBar, form) {
    if (!presetBar || !form) return;
    const valInput = form.querySelector('input[name="limit_value"]');
    const unitSelect = form.querySelector('select[name="limit_unit"]');
    const curVal = valInput ? valInput.value.trim() : '';
    const curUnit = unitSelect ? unitSelect.value : '';
    presetBar.querySelectorAll('.limit-preset-chip').forEach((chip) => {
      const active = curVal !== '' && chip.dataset.presetVal === curVal && chip.dataset.presetUnit === curUnit;
      chip.classList.toggle('is-active', active);
    });
  }

  function initPresetBars() {
    rootScope.querySelectorAll('[data-limit-presets]').forEach((bar) => {
      const card = bar.closest('.analytics-limit');
      const form = card ? card.querySelector('form.limit-form') : null;
      if (!form) return;

      const valInput = form.querySelector('input[name="limit_value"]');
      const unitSelect = form.querySelector('select[name="limit_unit"]');
      syncPresetHighlight(bar, form);

      if (valInput) valInput.addEventListener('input', () => syncPresetHighlight(bar, form));
      if (unitSelect) unitSelect.addEventListener('change', () => syncPresetHighlight(bar, form));

      bar.addEventListener('click', (e) => {
        const chip = e.target.closest('.limit-preset-chip');
        if (!chip) return;
        if (valInput) valInput.value = chip.dataset.presetVal || '';
        if (unitSelect && chip.dataset.presetUnit) unitSelect.value = chip.dataset.presetUnit;
        syncPresetHighlight(bar, form);
        if (valInput) valInput.focus();
      });
    });
  }

  // ── Per-Server Inline Monthly Limit Form ──────────────────────────────────

  function initLimitForms(serverID) {
    const saveForm = rootScope.querySelector('[data-limit-form]');
    const removeForm = rootScope.querySelector('[data-limit-remove-form]');
    const badge = rootScope.querySelector('[data-limit-badge]');
    const errorEl = rootScope.querySelector('[data-limit-error]');
    const quotaBox = rootScope.querySelector('[data-limit-quota]');
    const quotaText = rootScope.querySelector('[data-limit-quota-text]');
    const quotaPct = rootScope.querySelector('[data-limit-quota-pct]');
    const quotaFill = rootScope.querySelector('[data-limit-quota-fill]');
    if (!saveForm && !removeForm) return;

    function applyLimitResponse(data) {
      if (!data || !data.ok) {
        if (errorEl) {
          errorEl.textContent = (data && data.message) || T('js.failed');
          errorEl.hidden = false;
        }
        if (typeof window.nxToast === 'function') window.nxToast((data && data.message) || T('js.failed'), 'error');
        return;
      }
      if (errorEl) {
        errorEl.textContent = '';
        errorEl.hidden = true;
      }
      if (badge && data.badge_text) {
        const variant = ['active', 'inherited', 'none'].includes(data.badge_variant) ? data.badge_variant : 'none';
        badge.className = 'analytics-limit__current analytics-limit__current--' + variant;
        badge.textContent = data.badge_text;
      }
      if (removeForm) removeForm.hidden = !data.has_limit;
      if (saveForm && data.cleared) {
        const valInput = saveForm.querySelector('input[name="limit_value"]');
        if (valInput) valInput.value = '';
        const presetBar = saveForm.closest('.analytics-limit') && saveForm.closest('.analytics-limit').querySelector('[data-limit-presets]');
        syncPresetHighlight(presetBar, saveForm);
      }
      if (quotaBox) {
        if (data.has_quota) {
          const qLvl = ['ok', 'warn', 'crit'].includes(data.quota_level) ? data.quota_level : 'ok';
          quotaBox.className = 'analytics-limit__quota analytics-limit__quota--' + qLvl;
          quotaBox.hidden = false;
          if (quotaText) {
            quotaText.textContent = T('analytics.limit.quota_usage', {
              used: data.quota_used_human || '0 B',
              limit: data.quota_limit_human || '',
              kind: data.quota_kind_label || '',
            });
          }
          if (quotaPct) quotaPct.textContent = (data.quota_pct ?? 0) + '%';
          if (quotaFill) quotaFill.style.setProperty('--pct', String(data.quota_bar_pct ?? 0));
        } else {
          quotaBox.hidden = true;
        }
      }
      if (typeof window.nxToast === 'function' && data.message) {
        window.nxToast(data.message, 'ok');
      }
      loadForecast(serverID);
    }

    if (saveForm && saveForm.dataset.limitAjaxReady !== '1') {
      saveForm.dataset.limitAjaxReady = '1';
      saveForm.addEventListener('submit', (e) => {
        e.preventDefault();
        e.stopPropagation();
        const btn = saveForm.querySelector('button[type="submit"]');
        if (btn) btn.disabled = true;
        postFormJSON(saveForm, false)
          .then(applyLimitResponse)
          .catch(() => {
            if (typeof window.nxToast === 'function') window.nxToast(T('js.failed'), 'error');
          })
          .finally(() => { if (btn) btn.disabled = false; });
      });
    }

    if (removeForm && removeForm.dataset.limitAjaxReady !== '1') {
      removeForm.dataset.limitAjaxReady = '1';
      removeForm.addEventListener('submit', (e) => {
        e.preventDefault();
        e.stopPropagation();
        const btn = removeForm.querySelector('button[type="submit"]');
        const msg = (btn && btn.getAttribute('data-confirm')) || T('js.confirm_irreversible');
        const ask = typeof window.nxConfirm === 'function'
          ? window.nxConfirm({ message: msg, danger: true })
          : Promise.resolve(window.confirm(msg));
        ask.then((ok) => {
          if (!ok || !document.contains(removeForm)) return;
          if (btn) btn.disabled = true;
          postFormJSON(removeForm, false)
            .then(applyLimitResponse)
            .catch(() => {
              if (typeof window.nxToast === 'function') window.nxToast(T('js.failed'), 'error');
            })
            .finally(() => { if (btn) btn.disabled = false; });
        });
      });
    }
  }

  // ── Fleet Traffic Limits Admin (`/analytics/limits`) ──────────────────────

  function initLimitsAdmin() {
    const adminRoot = rootScope.querySelector('[data-limits-admin]');
    if (!adminRoot || adminRoot.dataset.limitsAdminReady === '1') return;
    adminRoot.dataset.limitsAdminReady = '1';

    const globalForm = adminRoot.querySelector('[data-limits-global-form]');
    const globalRemove = adminRoot.querySelector('[data-limits-global-remove]');
    const globalBadge = adminRoot.querySelector('[data-global-limit-badge]');
    const tagForm = adminRoot.querySelector('[data-limits-tag-form]');
    const tagRegion = adminRoot.querySelector('[data-limits-tag-region]');
    const knownWrap = adminRoot.querySelector('[data-known-tags-wrap]');
    const knownList = adminRoot.querySelector('[data-known-tags-list]');
    const knownDatalist = adminRoot.querySelector('[data-known-tags-datalist]');

    function populateTagForm(tag, val, unit) {
      if (!tagForm) return;
      const tagInput = tagForm.querySelector('input[name="tag"]');
      const valInput = tagForm.querySelector('input[name="limit_value"]');
      const unitSelect = tagForm.querySelector('select[name="limit_unit"]');
      if (tagInput) tagInput.value = tag || '';
      if (valInput && val !== undefined) valInput.value = val;
      if (unitSelect && unit) unitSelect.value = unit;
      const presetBar = tagForm.closest('.analytics-limit') && tagForm.closest('.analytics-limit').querySelector('[data-limit-presets]');
      syncPresetHighlight(presetBar, tagForm);
      if (valInput) valInput.focus();
    }

    function renderTagsTable(tags) {
      if (!tagRegion) return;
      const deleteAction = tagRegion.dataset.deleteAction || '/analytics/limits/tags/delete';
      const csrf = (typeof window.nxCSRF === 'function' ? window.nxCSRF(adminRoot) : '') || '';
      if (!Array.isArray(tags) || tags.length === 0) {
        tagRegion.innerHTML = `<div class="empty-state-card empty-state-card--compact" data-limits-tag-empty>
          <span class="empty-state-icon"><i data-lucide="tags"></i></span>
          <p class="empty-state-desc">${escapeHTML(T('analytics.limits.no_tags'))}</p>
        </div>`;
        renderIcons(tagRegion);
        return;
      }
      const rowsHTML = tags.map((row) => {
        const metaHTML = row.server_count > 0
          ? `<span class="an-tag-meta">${escapeHTML(T('analytics.limits.servers_matched', { count: row.server_count }))}</span>`
          : '';
        return `<tr class="an-row" data-tag-row="${escapeHTML(row.tag)}" data-tag-val="${escapeHTML(row.value_input || '')}" data-tag-unit="${escapeHTML(row.unit_input || 'GB')}">
          <td>
            <div class="an-tag-cell">
              <span class="tag-chip an-tag-chip">${escapeHTML(row.tag)}</span>
              ${metaHTML}
            </div>
          </td>
          <td class="text-right"><span class="traffic-badge traffic-badge--dl" dir="ltr">${escapeHTML(row.limit_human)}</span></td>
          <td class="text-right">
            <div class="an-tag-actions">
              <button type="button" class="btn btn--ghost btn--xs" data-edit-tag="${escapeHTML(row.tag)}" data-edit-val="${escapeHTML(row.value_input || '')}" data-edit-unit="${escapeHTML(row.unit_input || 'GB')}"><i data-lucide="pencil" class="icon-in-button"></i> ${escapeHTML(T('analytics.limits.edit_tag'))}</button>
              <form method="post" action="${escapeHTML(deleteAction)}" class="inline-form" data-limits-tag-delete>
                <input type="hidden" name="_csrf_token" value="${escapeHTML(csrf)}">
                <input type="hidden" name="tag" value="${escapeHTML(row.tag)}">
                <button type="submit" class="btn btn--ghost btn--xs btn--danger" data-no-loading data-confirm="${escapeHTML(T('analytics.limits.remove_confirm'))}"><i data-lucide="trash-2" class="icon-in-button"></i> ${escapeHTML(T('analytics.limits.remove'))}</button>
              </form>
            </div>
          </td>
        </tr>`;
      }).join('');

      tagRegion.innerHTML = `<div class="data-table-wrap">
        <table class="data-table an-table an-table--tags">
          <thead>
            <tr>
              <th>${escapeHTML(T('analytics.limits.col_tag'))}</th>
              <th class="text-right">${escapeHTML(T('analytics.limits.col_limit'))}</th>
              <th></th>
            </tr>
          </thead>
          <tbody data-limits-tag-tbody>${rowsHTML}</tbody>
        </table>
      </div>`;
      renderIcons(tagRegion);
    }

    function renderKnownTags(knownTags) {
      if (!knownWrap || !knownList) return;
      if (!Array.isArray(knownTags) || knownTags.length === 0) {
        knownWrap.hidden = true;
        return;
      }
      knownWrap.hidden = false;
      knownList.innerHTML = knownTags.map((kt) => `<button type="button" class="limit-tag-pick${kt.has_cap ? ' is-capped' : ''}" data-pick-tag="${escapeHTML(kt.tag)}">
        <span>${escapeHTML(kt.tag)}</span>
        <span class="limit-tag-pick__count" dir="ltr">${escapeHTML(String(kt.server_count))}</span>
      </button>`).join('');
      if (knownDatalist) {
        knownDatalist.innerHTML = knownTags.map((kt) => `<option value="${escapeHTML(kt.tag)}"></option>`).join('');
      }
    }

    function applyAdminResponse(data, clearTagForm) {
      if (!data || !data.ok) {
        if (typeof window.nxToast === 'function') window.nxToast((data && data.message) || T('js.failed'), 'error');
        return;
      }
      if (globalBadge && data.badge_text) {
        const variant = data.badge_variant === 'active' ? 'active' : 'none';
        globalBadge.className = 'analytics-limit__current analytics-limit__current--' + variant;
        globalBadge.textContent = data.badge_text;
      }
      if (globalRemove) globalRemove.hidden = !data.has_global;
      if (globalForm && !data.has_global) {
        const gVal = globalForm.querySelector('input[name="limit_value"]');
        if (gVal) gVal.value = '';
        const presetBar = globalForm.closest('.analytics-limit') && globalForm.closest('.analytics-limit').querySelector('[data-limit-presets]');
        syncPresetHighlight(presetBar, globalForm);
      }
      if (Array.isArray(data.tags)) renderTagsTable(data.tags);
      if (Array.isArray(data.known_tags)) renderKnownTags(data.known_tags);
      if (clearTagForm && tagForm) {
        const tIn = tagForm.querySelector('input[name="tag"]');
        const vIn = tagForm.querySelector('input[name="limit_value"]');
        if (tIn) tIn.value = '';
        if (vIn) vIn.value = '';
        const presetBar = tagForm.closest('.analytics-limit') && tagForm.closest('.analytics-limit').querySelector('[data-limit-presets]');
        syncPresetHighlight(presetBar, tagForm);
      }
      if (typeof window.nxToast === 'function' && data.message) {
        window.nxToast(data.message, 'ok');
      }
    }

    if (globalForm) {
      globalForm.addEventListener('submit', (e) => {
        e.preventDefault();
        e.stopPropagation();
        const btn = globalForm.querySelector('button[type="submit"]');
        if (btn) btn.disabled = true;
        postFormJSON(globalForm, false)
          .then((d) => applyAdminResponse(d, false))
          .catch(() => { if (typeof window.nxToast === 'function') window.nxToast(T('js.failed'), 'error'); })
          .finally(() => { if (btn) btn.disabled = false; });
      });
    }

    if (globalRemove) {
      globalRemove.addEventListener('submit', (e) => {
        e.preventDefault();
        e.stopPropagation();
        const btn = globalRemove.querySelector('button[type="submit"]');
        const msg = (btn && btn.getAttribute('data-confirm')) || T('js.confirm_irreversible');
        const ask = typeof window.nxConfirm === 'function'
          ? window.nxConfirm({ message: msg, danger: true })
          : Promise.resolve(window.confirm(msg));
        ask.then((ok) => {
          if (!ok || !document.contains(globalRemove)) return;
          if (btn) btn.disabled = true;
          postFormJSON(globalRemove, false)
            .then((d) => applyAdminResponse(d, false))
            .catch(() => { if (typeof window.nxToast === 'function') window.nxToast(T('js.failed'), 'error'); })
            .finally(() => { if (btn) btn.disabled = false; });
        });
      });
    }

    if (tagForm) {
      tagForm.addEventListener('submit', (e) => {
        e.preventDefault();
        e.stopPropagation();
        const btn = tagForm.querySelector('button[type="submit"]');
        if (btn) btn.disabled = true;
        postFormJSON(tagForm, false)
          .then((d) => applyAdminResponse(d, Boolean(d && d.ok)))
          .catch(() => { if (typeof window.nxToast === 'function') window.nxToast(T('js.failed'), 'error'); })
          .finally(() => { if (btn) btn.disabled = false; });
      });
    }

    adminRoot.addEventListener('click', (e) => {
      const editBtn = e.target.closest('[data-edit-tag]');
      if (editBtn) {
        populateTagForm(editBtn.dataset.editTag, editBtn.dataset.editVal || '', editBtn.dataset.editUnit || 'GB');
        return;
      }
      const pickBtn = e.target.closest('[data-pick-tag]');
      if (pickBtn) {
        const tag = pickBtn.dataset.pickTag || '';
        const existingRow = tagRegion && tagRegion.querySelector(`[data-tag-row="${CSS.escape ? CSS.escape(tag) : tag}"]`);
        if (existingRow) {
          populateTagForm(tag, existingRow.dataset.tagVal || '', existingRow.dataset.tagUnit || 'GB');
        } else {
          populateTagForm(tag, undefined, undefined);
        }
      }
    });

    adminRoot.addEventListener('submit', (e) => {
      const delForm = e.target.closest('[data-limits-tag-delete]');
      if (!delForm) return;
      e.preventDefault();
      e.stopPropagation();
      const btn = delForm.querySelector('button[type="submit"]');
      const msg = (btn && btn.getAttribute('data-confirm')) || T('js.confirm_irreversible');
      const ask = typeof window.nxConfirm === 'function'
        ? window.nxConfirm({ message: msg, danger: true })
        : Promise.resolve(window.confirm(msg));
      ask.then((ok) => {
        if (!ok || !document.contains(delForm)) return;
        if (btn) btn.disabled = true;
        postFormJSON(delForm, false)
          .then((d) => applyAdminResponse(d, false))
          .catch(() => { if (typeof window.nxToast === 'function') window.nxToast(T('js.failed'), 'error'); })
          .finally(() => { if (btn) btn.disabled = false; });
      });
    });
  }

  // ── Global Fleet Overview Interactive Sorting & Filtering ─────────────────

  function renumberVisibleRows(tbody) {
    if (!tbody) return 0;
    let visible = 0;
    tbody.querySelectorAll('tr.an-row').forEach((row) => {
      if (row.hidden) return;
      visible++;
      const rankEl = row.querySelector('[data-an-rank]');
      if (rankEl) {
        rankEl.textContent = '#' + visible;
        rankEl.className = 'an-rank' + (visible <= 3 ? ' an-rank--' + visible : '');
      }
    });
    return visible;
  }

  function initGlobalAnalytics() {
    const tables = rootScope.querySelectorAll('table[data-an-sortable]');
    if (!tables.length) return;

    tables.forEach((table) => {
      const tbody = table.querySelector('tbody');
      if (!tbody) return;

      table.querySelectorAll('.an-sort-btn[data-sort-col]').forEach((btn) => {
        btn.addEventListener('click', () => {
          const col = btn.dataset.sortCol;
          if (!col) return;
          const wasActive = btn.classList.contains('is-active');
          const nextDir = wasActive && btn.dataset.sortDir === 'desc' ? 'asc' : 'desc';

          table.querySelectorAll('.an-sort-btn[data-sort-col]').forEach((other) => {
            const active = other === btn;
            other.classList.toggle('is-active', active);
            other.dataset.sortDir = active ? nextDir : '';
            const th = other.closest('th');
            if (th) th.setAttribute('aria-sort', active ? (nextDir === 'desc' ? 'descending' : 'ascending') : 'none');
            const iconHolder = other.querySelector('.an-sort-icon');
            if (iconHolder) {
              const iconName = active ? (nextDir === 'desc' ? 'chevron-down' : 'chevron-up') : 'chevrons-up-down';
              iconHolder.outerHTML = `<i data-lucide="${iconName}" class="an-sort-icon" aria-hidden="true"></i>`;
            }
          });
          renderIcons(table.querySelector('thead'));

          const rows = Array.from(tbody.querySelectorAll('tr.an-row'));
          rows.sort((a, b) => {
            const av = parseFloat(a.getAttribute('data-' + col) || '0') || 0;
            const bv = parseFloat(b.getAttribute('data-' + col) || '0') || 0;
            return nextDir === 'desc' ? bv - av : av - bv;
          });
          const frag = document.createDocumentFragment();
          rows.forEach((r) => {
            r.classList.remove('an-enter');
            frag.appendChild(r);
          });
          tbody.appendChild(frag);
          renumberVisibleRows(tbody);
        });
      });
    });

    const filterInput = rootScope.querySelector('[data-analytics-filter]');
    if (filterInput) {
      filterInput.addEventListener('input', () => {
        const q = (filterInput.value || '').trim().toLowerCase();
        tables.forEach((table) => {
          const tbody = table.querySelector('tbody');
          if (!tbody) return;
          tbody.querySelectorAll('tr.an-row').forEach((row) => {
            const hay = (row.getAttribute('data-search') || '').toLowerCase();
            row.hidden = q !== '' && !hay.includes(q);
          });
          const visibleCount = renumberVisibleRows(tbody);
          const emptyMsg = table.parentElement && table.parentElement.querySelector('[data-an-filter-empty]');
          if (emptyMsg) emptyMsg.hidden = visibleCount > 0;
        });
      });
    }
  }

  // ── Init ──────────────────────────────────────────────────────────────────

  function init() {
    initAnimatedBars(rootScope);
    initCountUp(rootScope);
    initPresetBars();
    initGlobalAnalytics();
    initLimitsAdmin();

    const firstChart = rootScope.querySelector('[data-metric][data-server]');
    if (!firstChart) return;

    const serverID = firstChart.dataset.server;
    if (!serverID) return;

    currentRange = detectInitialRange();
    initRangeTabs(serverID);
    initRefreshAndRetry(serverID);
    initLimitForms(serverID);
    loadAllCharts(serverID, currentRange);
    loadForecast(serverID);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
