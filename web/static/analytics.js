/* analytics.js — pure SVG time-series charts and bandwidth forecast UI */
(function () {
  'use strict';

  // ── SVG chart renderer ────────────────────────────────────────────────────

  const CHART_PAD = { top: 12, right: 16, bottom: 28, left: 42 };

  function createSVGEl(tag, attrs) {
    const el = document.createElementNS('http://www.w3.org/2000/svg', tag);
    for (const [k, v] of Object.entries(attrs || {})) el.setAttribute(k, v);
    return el;
  }

  function buildChart(container, data) {
    container.innerHTML = '';
    const w = container.clientWidth || 400;
    const h = container.clientHeight || 180;
    const pw = w - CHART_PAD.left - CHART_PAD.right;
    const ph = h - CHART_PAD.top - CHART_PAD.bottom;

    const svg = createSVGEl('svg', { viewBox: `0 0 ${w} ${h}`, preserveAspectRatio: 'none' });
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
        x: -6, y: y + 4,
        'text-anchor': 'end',
      });
      axisLabel.textContent = formatAxisValue(tick, unit);
      plotG.appendChild(axisLabel);
    }

    // X-axis labels (sparse)
    const xLabelCount = Math.min(6, labels.length);
    const xStep = labels.length > 1 ? (labels.length - 1) / (xLabelCount - 1) : 1;
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

    // Draw each series
    for (const s of series) {
      if (!s.data || s.data.length === 0) continue;
      const pts = s.data.map((v, i) => ({
        x: (i / Math.max(s.data.length - 1, 1)) * pw,
        y: ph - ((v - yMin) / yRange) * ph,
        v,
      }));

      // Area fill
      const areaD = buildAreaPath(pts, pw, ph);
      plotG.appendChild(createSVGEl('path', {
        class: 'chart-area',
        d: areaD,
        fill: s.color || '#3b82f6',
      }));

      // Line
      const lineD = buildLinePath(pts);
      plotG.appendChild(createSVGEl('path', {
        class: 'chart-path',
        d: lineD,
        stroke: s.color || '#3b82f6',
      }));
    }

    // Interaction overlay
    const overlay = buildInteractionOverlay(container, plotG, pw, ph, labels, series, yMin, yRange, unit);
    plotG.appendChild(overlay);

    container.appendChild(svg);
  }

  function buildInteractionOverlay(container, plotG, pw, ph, labels, series, yMin, yRange, unit) {
    // Transparent rect to capture mouse events
    const rect = createSVGEl('rect', {
      x: 0, y: 0, width: pw, height: ph,
      fill: 'transparent', cursor: 'crosshair',
    });

    let tooltipEl = null;
    let vline = null;
    let dots = [];

    function showTooltip(x, clientX, clientY) {
      const idx = Math.round((x / pw) * (labels.length - 1));
      if (idx < 0 || idx >= labels.length) return;

      const xPos = (idx / Math.max(labels.length - 1, 1)) * pw;

      // Vertical line
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

      // Dots
      dots.forEach(d => d.remove());
      dots = [];
      for (const s of series) {
        if (!s.data || idx >= s.data.length) continue;
        const v = s.data[idx];
        const y = ph - ((v - yMin) / yRange) * ph;
        const dot = createSVGEl('circle', {
          class: 'chart-tooltip-dot',
          cx: xPos, cy: y, r: 4,
          fill: s.color || '#3b82f6',
          stroke: '#0f172a', 'stroke-width': 2,
        });
        plotG.appendChild(dot);
        dots.push(dot);
      }

      // Tooltip div
      if (!tooltipEl) {
        tooltipEl = document.createElement('div');
        tooltipEl.className = 'chart-tooltip';
        container.style.position = 'relative';
        container.appendChild(tooltipEl);
      }

      let html = `<div class="chart-tooltip__label">${labels[idx]}</div>`;
      for (const s of series) {
        if (!s.data || idx >= s.data.length) continue;
        const v = s.data[idx];
        const formatted = unit === '%' ? v.toFixed(1) + '%'
          : unit === 'GiB' ? v.toFixed(3) + ' GiB'
          : v.toFixed(2) + (unit ? ' ' + unit : '');
        html += `<div class="chart-tooltip__row">
          <span class="chart-tooltip__dot" style="background:${s.color}"></span>
          <span>${s.label}</span>
          <span class="chart-tooltip__value">${formatted}</span>
        </div>`;
      }
      tooltipEl.innerHTML = html;

      // Position tooltip
      const cRect = container.getBoundingClientRect();
      let tx = clientX - cRect.left + 12;
      let ty = clientY - cRect.top - 12;
      const tWidth = tooltipEl.offsetWidth || 150;
      if (tx + tWidth > cRect.width - 8) tx = clientX - cRect.left - tWidth - 12;
      tooltipEl.style.left = tx + 'px';
      tooltipEl.style.top = ty + 'px';
      tooltipEl.style.opacity = '1';
    }

    function hideTooltip() {
      if (vline) vline.style.display = 'none';
      dots.forEach(d => d.remove());
      dots = [];
      if (tooltipEl) tooltipEl.style.opacity = '0';
    }

    rect.addEventListener('mousemove', (e) => {
      const svgEl = rect.closest('svg');
      const svgRect = svgEl.getBoundingClientRect();
      const scaleX = (svgEl.viewBox.baseVal.width || svgRect.width) / svgRect.width;
      const localX = (e.clientX - svgRect.left) * scaleX - CHART_PAD.left;
      showTooltip(localX, e.clientX, e.clientY);
    });

    rect.addEventListener('mouseleave', hideTooltip);
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
    if (unit === 'GiB') return v.toFixed(1);
    if (v >= 1000) return (v / 1000).toFixed(1) + 'k';
    return v.toFixed(v < 10 ? 1 : 0);
  }

  // ── Chart loading ─────────────────────────────────────────────────────────

  function showSkeleton(metric) {
    const sk = document.getElementById('skeleton-' + metric);
    const ct = document.getElementById('chart-' + metric);
    const em = document.getElementById('empty-' + metric);
    const er = document.getElementById('error-' + metric);
    if (sk) sk.style.display = '';
    if (ct) ct.style.display = 'none';
    if (em) em.style.display = 'none';
    if (er) er.style.display = 'none';
  }

  function showChart(metric) {
    const sk = document.getElementById('skeleton-' + metric);
    const ct = document.getElementById('chart-' + metric);
    if (sk) sk.style.display = 'none';
    if (ct) ct.style.display = '';
  }

  function showEmpty(metric) {
    const sk = document.getElementById('skeleton-' + metric);
    const ct = document.getElementById('chart-' + metric);
    const em = document.getElementById('empty-' + metric);
    if (sk) sk.style.display = 'none';
    if (ct) ct.style.display = 'none';
    if (em) em.style.display = '';
  }

  function showError(metric) {
    const sk = document.getElementById('skeleton-' + metric);
    const ct = document.getElementById('chart-' + metric);
    const er = document.getElementById('error-' + metric);
    if (sk) sk.style.display = 'none';
    if (ct) ct.style.display = 'none';
    if (er) er.style.display = '';
  }

  function updateFooter(metric, data) {
    const footer = document.getElementById('footer-' + metric);
    if (!footer || !data || !data.series || !data.series[0]) return;
    const values = data.series[0].data;
    if (!values || values.length === 0) return;
    const avg = values.reduce((a, b) => a + b, 0) / values.length;
    const max = Math.max(...values);
    const unit = data.unit || '';
    const fmt = (v) => unit === '%' ? v.toFixed(1) + '%' : v.toFixed(2) + (unit ? ' ' + unit : '');
    footer.textContent = `Avg: ${fmt(avg)} · Peak: ${fmt(max)} · ${values.length} points`;
  }

  async function loadChart(serverID, metric, range) {
    showSkeleton(metric);
    try {
      const resp = await fetch(`/servers/${serverID}/analytics/data?metric=${metric}&range=${range}`);
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      const data = await resp.json();
      if (!data.series || data.series.length === 0 || (data.series[0].data && data.series[0].data.length === 0)) {
        showEmpty(metric);
        return;
      }
      showChart(metric);
      const ct = document.getElementById('chart-' + metric);
      if (ct) {
        // Wait for DOM paint to get accurate dimensions
        requestAnimationFrame(() => {
          buildChart(ct, data);
          updateFooter(metric, data);
        });
      }
      // Build legend for traffic
      if (metric === 'traffic') {
        buildLegend('legend-traffic', data.series);
      }
    } catch (e) {
      showError(metric);
    }
  }

  function buildLegend(containerId, series) {
    const legend = document.getElementById(containerId);
    if (!legend) return;
    legend.innerHTML = series.map(s => `
      <div class="legend-item">
        <span class="legend-dot" style="background:${s.color}"></span>
        ${s.label}
      </div>
    `).join('');
  }

  // ── Forecast UI ───────────────────────────────────────────────────────────

  async function loadForecast(serverID) {
    const grid = document.getElementById('forecastGrid');
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
          <p>Forecast unavailable — no traffic data collected yet</p>
        </div>`;
        if (window.lucide) lucide.createIcons();
      }
    }
  }

  function renderForecast(f) {
    const grid = document.getElementById('forecastGrid');
    if (!grid) return;

    const trendHTML = renderTrend(f.trend);
    const confidenceHTML = renderConfidence(f.confidence);
    const risksHTML = renderRisks(f.risks);

    grid.innerHTML = `
      ${renderPeriodCard('Today', f.today, f.today.pct_elapsed)}
      ${renderPeriodCard('This Week', f.this_week, f.this_week.pct_elapsed)}
      ${renderPeriodCard('This Month', f.this_month, f.this_month.pct_elapsed)}
    `;

    // Show metadata below grid
    let meta = document.getElementById('forecastMeta');
    if (!meta) {
      meta = document.createElement('div');
      meta.id = 'forecastMeta';
      meta.style.cssText = 'display:flex;align-items:center;gap:12px;margin-bottom:16px;flex-wrap:wrap;';
      grid.parentNode.insertBefore(meta, grid.nextSibling);
    }
    meta.innerHTML = `
      ${confidenceHTML}
      ${trendHTML}
      ${risksHTML}
    `;

    // Show forecast algo on chart card
    const algoEl = document.getElementById('forecastAlgo');
    if (algoEl) algoEl.textContent = f.algorithm;

    const chartCard = document.getElementById('card-forecast-chart');
    if (chartCard) chartCard.style.display = '';

    if (window.lucide) lucide.createIcons();
  }

  function renderPeriodCard(label, period, pct) {
    const progressClass = pct > 90 ? 'forecast-progress__bar--danger'
      : pct > 70 ? 'forecast-progress__bar--warn'
      : '';
    return `
      <div class="forecast-card">
        <div class="forecast-card__label">${label}</div>
        <div class="forecast-card__current">${period.current_human}</div>
        <div class="forecast-card__predicted">
          Predicted end: <strong>${period.predicted_human}</strong>
        </div>
        <div class="forecast-progress">
          <div class="forecast-progress__bar ${progressClass}" style="width:${Math.min(100, pct)}%"></div>
        </div>
        <div class="forecast-progress__label">${pct}% of period elapsed</div>
      </div>
    `;
  }

  function renderTrend(trend) {
    const map = {
      increasing: { cls: 'trend-indicator--increasing', icon: '↑', label: 'Increasing' },
      decreasing: { cls: 'trend-indicator--decreasing', icon: '↓', label: 'Decreasing' },
      stable:     { cls: 'trend-indicator--stable',     icon: '→', label: 'Stable' },
    };
    const t = map[trend] || map.stable;
    return `<span class="trend-indicator ${t.cls}">${t.icon} ${t.label}</span>`;
  }

  function renderConfidence(confidence) {
    return `
      <span class="forecast-confidence">
        <span class="confidence-dot confidence-dot--${confidence}"></span>
        ${confidence.charAt(0).toUpperCase() + confidence.slice(1)} confidence
      </span>
    `;
  }

  function renderRisks(risks) {
    const items = [];
    if (risks.traffic_spike) {
      items.push(`<span class="risk-badge"><i data-lucide="zap"></i> Traffic spike</span>`);
    }
    if (risks.unusual_growth) {
      items.push(`<span class="risk-badge"><i data-lucide="trending-up"></i> Unusual growth</span>`);
    }
    if (risks.exhaustion) {
      items.push(`<span class="risk-badge"><i data-lucide="alert-triangle"></i> Bandwidth exhaustion risk</span>`);
    }
    return items.length > 0 ? `<div class="risk-badges">${items.join('')}</div>` : '';
  }

  // ── Range tabs ────────────────────────────────────────────────────────────

  let currentRange = '24h';
  let currentServerID = null;

  function initRangeTabs(serverID) {
    const tabs = document.getElementById('globalRangeTabs');
    if (!tabs) return;

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

      currentRange = range;
      loadAllCharts(serverID, range);
    });
  }

  function loadAllCharts(serverID, range) {
    for (const metric of ['cpu', 'ram', 'disk', 'load']) {
      loadChart(serverID, metric, range);
    }
    // Traffic is always 30d (vnstat provides daily totals)
    loadChart(serverID, 'traffic', '30d');
  }

  // ── Init ──────────────────────────────────────────────────────────────────

  function init() {
    // Per-server analytics page
    const firstChart = document.querySelector('[data-metric][data-server]');
    if (!firstChart) return;

    const serverID = firstChart.dataset.server;
    if (!serverID) return;

    currentServerID = serverID;
    initRangeTabs(serverID);
    loadAllCharts(serverID, currentRange);
    loadForecast(serverID);

    // Re-render charts on window resize (debounced)
    let resizeTimer;
    window.addEventListener('resize', () => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        loadAllCharts(serverID, currentRange);
      }, 250);
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
