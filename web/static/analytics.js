/* analytics.js — pure SVG time-series charts and bandwidth forecast UI */
(function () {
  'use strict';

  var scriptPane = document.currentScript && document.currentScript.closest('.tab-pane');
  var rootScope = scriptPane || document;

  function byId(id) {
    return rootScope.querySelector ? rootScope.querySelector('#' + id) : document.getElementById(id);
  }

  // Localization helper (see app.js for window.nxT). Falls back to the key.
  function T(key, params) { return window.nxT ? window.nxT(key, params) : key; }

  // Escape HTML-special characters so server-sourced values can safely be
  // interpolated into innerHTML.
  function escapeHTML(str) {
    return String(str).replace(/[&<>"']/g, function (m) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[m];
    });
  }

  // Resolve chart series colors strictly from CSS custom properties on :root
  // (--accent, --ok, --warn, --text-muted), never competing purple/violet hues.
  const SERIES_TOKEN_MAP = {
    cpu: '--accent',
    ram: '--ok',
    disk: '--warn',
    swap: '--text-muted',
    load1: '--accent',
    load5: '--ok',
    load15: '--text-muted',
    rx: '--accent',
    tx: '--ok',
  };
  const ALLOWED_TOKENS = new Set(['--accent', '--ok', '--warn', '--crit', '--text-muted', '--text-dim']);
  const COLOR_RE = /^#[0-9a-fA-F]{3,8}$/;

  function readCSSToken(tokenName, fallback) {
    try {
      const val = getComputedStyle(document.documentElement).getPropertyValue(tokenName).trim();
      if (val) return val;
    } catch (e) {}
    return fallback;
  }

  function resolveSeriesColor(series, fallbackMetric) {
    const metricKey = (series && series.metric) || fallbackMetric || '';
    let token = SERIES_TOKEN_MAP[metricKey] || '';
    if (!token && series && typeof series.color === 'string') {
      if (ALLOWED_TOKENS.has(series.color)) {
        token = series.color;
      } else if (series.color.startsWith('var(')) {
        const inner = series.color.slice(4, -1).trim();
        if (ALLOWED_TOKENS.has(inner)) token = inner;
      }
    }
    if (token) {
      const defaults = {
        '--accent': '#3b82f6',
        '--ok': '#4cc38a',
        '--warn': '#eab308',
        '--crit': '#e5484d',
        '--text-muted': '#93939a',
        '--text-dim': '#66666d',
      };
      return readCSSToken(token, defaults[token] || '#3b82f6');
    }
    if (series && COLOR_RE.test(series.color)) {
      return series.color;
    }
    return readCSSToken('--accent', '#3b82f6');
  }

  // ── SVG chart renderer ────────────────────────────────────────────────────

  const CHART_PAD = { top: 12, right: 16, bottom: 28, left: 44 };

  function createSVGEl(tag, attrs) {
    const el = document.createElementNS('http://www.w3.org/2000/svg', tag);
    for (const [k, v] of Object.entries(attrs || {})) el.setAttribute(k, v);
    return el;
  }

  function buildChart(container, data) {
    if (!container || !data) return;
    container.__chartData = data;
    const w = Math.max(120, Math.round(container.clientWidth || 400));
    const h = Math.max(80, Math.round(container.clientHeight || 200));
    container.__lastW = w;
    container.__lastH = h;

    container.innerHTML = '';
    const pw = Math.max(10, w - CHART_PAD.left - CHART_PAD.right);
    const ph = Math.max(10, h - CHART_PAD.top - CHART_PAD.bottom);

    // Exact container aspect ratio viewBox with uniform preserveAspectRatio
    // so axis labels and text never stretch or distort on resize.
    const svg = createSVGEl('svg', {
      viewBox: `0 0 ${w} ${h}`,
      preserveAspectRatio: 'xMidYMid meet',
    });
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

    // Draw each series
    for (const s of series) {
      if (!s.data || s.data.length === 0) continue;
      const strokeColor = resolveSeriesColor(s, data.metric);
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
        fill: strokeColor,
      }));

      // Line
      const lineD = buildLinePath(pts);
      plotG.appendChild(createSVGEl('path', {
        class: 'chart-path',
        d: lineD,
        stroke: strokeColor,
      }));
    }

    // Interaction overlay
    const overlay = buildInteractionOverlay(container, plotG, pw, ph, labels, series, yMin, yRange, unit, data.metric);
    plotG.appendChild(overlay);

    container.appendChild(svg);
    ensureResizeObserver(container);
  }

  function ensureResizeObserver(container) {
    if (container.__ro || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const nw = Math.round(entry.contentRect.width);
        const nh = Math.round(entry.contentRect.height);
        if (nw > 0 && nh > 0 && (nw !== container.__lastW || nh !== container.__lastH)) {
          if (container.__chartData && !container.hidden) {
            buildChart(container, container.__chartData);
          }
        }
      }
    });
    ro.observe(container);
    container.__ro = ro;
    if (scriptPane) {
      if (!scriptPane.__nxCleanups) scriptPane.__nxCleanups = [];
      scriptPane.__nxCleanups.push(() => ro.disconnect());
    }
  }

  function buildInteractionOverlay(container, plotG, pw, ph, labels, series, yMin, yRange, unit, metricName) {
    const rect = createSVGEl('rect', {
      x: 0, y: 0, width: pw, height: ph,
      fill: 'transparent', cursor: 'crosshair',
    });

    let tooltipEl = null;
    let vline = null;
    let dots = [];

    function showTooltip(x, clientX, clientY) {
      if (!labels.length) return;
      const clampedX = Math.max(0, Math.min(pw, x));
      const idx = Math.round((clampedX / pw) * Math.max(labels.length - 1, 0));
      if (idx < 0 || idx >= labels.length) return;

      const xPos = (idx / Math.max(labels.length - 1, 1)) * pw;

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
      for (const s of series) {
        if (!s.data || idx >= s.data.length) continue;
        const v = s.data[idx];
        const y = ph - ((v - yMin) / yRange) * ph;
        const color = resolveSeriesColor(s, metricName);
        const dot = createSVGEl('circle', {
          class: 'chart-tooltip-dot',
          cx: xPos, cy: y, r: 4,
          fill: color,
        });
        plotG.appendChild(dot);
        dots.push(dot);
      }

      if (!tooltipEl) {
        tooltipEl = document.createElement('div');
        tooltipEl.className = 'chart-tooltip';
        container.appendChild(tooltipEl);
      }

      let html = `<div class="chart-tooltip__label">${escapeHTML(labels[idx])}</div>`;
      for (const s of series) {
        if (!s.data || idx >= s.data.length) continue;
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
      }
      tooltipEl.innerHTML = html;

      // Position tooltip cleanly inside container bounds regardless of document dir (LTR/RTL)
      const cRect = container.getBoundingClientRect();
      const pointerX = clientX - cRect.left;
      const pointerY = clientY - cRect.top;
      const tWidth = tooltipEl.offsetWidth || 150;
      const tHeight = tooltipEl.offsetHeight || 56;
      let tx = pointerX + 12;
      if (tx + tWidth > cRect.width - 8) {
        tx = pointerX - tWidth - 12;
      }
      tx = Math.max(8, Math.min(tx, Math.max(8, cRect.width - tWidth - 8)));
      let ty = pointerY - 12;
      ty = Math.max(4, Math.min(ty, Math.max(4, cRect.height - tHeight - 4)));

      tooltipEl.style.left = tx + 'px';
      tooltipEl.style.right = 'auto';
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
      if (!svgEl) return;
      const svgRect = svgEl.getBoundingClientRect();
      if (!svgRect.width) return;
      const vbWidth = (svgEl.viewBox && svgEl.viewBox.baseVal && svgEl.viewBox.baseVal.width) || svgRect.width;
      const scaleX = vbWidth / svgRect.width;
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
    if (unit === 'GB') return v.toFixed(1);
    if (v >= 1000) return (v / 1000).toFixed(1) + 'k';
    return v.toFixed(v < 10 ? 1 : 0);
  }

  // ── Chart loading (semantic [hidden] toggling) ────────────────────────────

  function setCardState(metric, activeState) {
    const sk = byId('skeleton-' + metric);
    const ct = byId('chart-' + metric);
    const em = byId('empty-' + metric);
    const er = byId('error-' + metric);
    if (sk) sk.hidden = activeState !== 'skeleton';
    if (ct) ct.hidden = activeState !== 'chart';
    if (em) em.hidden = activeState !== 'empty';
    if (er) er.hidden = activeState !== 'error';
  }

  function showSkeleton(metric) { setCardState(metric, 'skeleton'); }
  function showChart(metric) { setCardState(metric, 'chart'); }
  function showEmpty(metric) { setCardState(metric, 'empty'); }
  function showError(metric) { setCardState(metric, 'error'); }

  function updateFooter(metric, data) {
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

    // Multi-series (e.g. Load 1m / 5m / 15m): render per-series Avg / Peak / Current
    footer.innerHTML = `<div class="chart-footer-series">${nonEmpty.map(s => {
      const vals = s.data;
      const avg = vals.reduce((a, b) => a + b, 0) / vals.length;
      const max = Math.max(...vals);
      const cur = vals[vals.length - 1];
      const color = resolveSeriesColor(s, data.metric);
      return `<span class="chart-footer-item">
        <span class="legend-dot" style="background:${color}"></span>
        <span class="chart-footer-item__label">${escapeHTML(s.label)}</span>
        <span>${fmt(cur)} (avg ${fmt(avg)} · peak ${fmt(max)})</span>
      </span>`;
    }).join('')}</div>`;
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
          buildChart(ct, data);
          updateFooter(metric, data);
        });
      }
      if (metric === 'traffic') {
        buildLegend('legend-traffic', data.series, data.metric);
      }
    } catch (e) {
      showError(metric);
    }
  }

  function buildLegend(containerId, series, fallbackMetric) {
    const legend = byId(containerId);
    if (!legend) return;
    legend.innerHTML = series.map(s => `
      <div class="legend-item">
        <span class="legend-dot" style="background:${resolveSeriesColor(s, fallbackMetric)}"></span>
        ${escapeHTML(s.label)}
      </div>
    `).join('');
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
          <p>${window.nxT ? window.nxT('js.analytics.forecast_unavailable') : 'Forecast unavailable — no traffic data collected yet'}</p>
        </div>`;
        if (window.lucide) lucide.createIcons();
      }
    }
  }

  function renderForecast(f) {
    const grid = byId('forecastGrid');
    if (!grid) return;

    const trendHTML = renderTrend(f.trend);
    const confidenceHTML = renderConfidence(f.confidence);
    const risksHTML = renderRisks(f.risks);

    const scope = seriesScopeLabel(f.series);
    const periodNote = anchoredPeriodNote(f.period_start, f.period_end);
    grid.innerHTML = `
      ${renderPeriodCard(T('js.analytics.today'), f.today, f.today.pct_elapsed, scope, '')}
      ${renderPeriodCard(T('js.analytics.this_week'), f.this_week, f.this_week.pct_elapsed, scope, '')}
      ${renderPeriodCard(T('js.analytics.this_month'), f.this_month, f.this_month.pct_elapsed, scope, periodNote)}
    `;

    let meta = byId('forecastMeta');
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

    renderExhaustion(f.exhaustion);

    if (window.lucide) lucide.createIcons();
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

  function renderPeriodCard(label, period, pct, scopeLabel, periodNote) {
    const clampedPct = Math.min(100, Math.max(0, pct || 0));
    const scaleVal = (clampedPct / 100).toFixed(3);
    const progressClass = clampedPct > 90 ? 'forecast-progress__bar--danger'
      : clampedPct > 70 ? 'forecast-progress__bar--warn'
      : '';
    return `
      <div class="forecast-card">
        <div class="forecast-card__label"><span>${escapeHTML(label)}</span> <span class="forecast-card__scope">${escapeHTML(scopeLabel)}</span></div>
        <div class="forecast-card__current">${escapeHTML(period.current_human)}</div>
        <div class="forecast-card__predicted">
          ${escapeHTML(T('js.analytics.predicted_end'))} <strong>${escapeHTML(period.predicted_human)}</strong>
        </div>
        <div class="forecast-progress">
          <div class="forecast-progress__bar ${progressClass}" style="--pct:${scaleVal}"></div>
        </div>
        <div class="forecast-progress__label">${escapeHTML(T('js.analytics.period_elapsed', { pct: clampedPct }))}${periodNote ? ` · ${escapeHTML(periodNote)}` : ''}</div>
      </div>
    `;
  }

  function Tn(key, count, params) {
    if (window.nxTn) return window.nxTn(key, count, params);
    return T(key, params);
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

    panel.hidden = false;
    panel.className = 'forecast-exhaustion forecast-exhaustion--' + level;
    panel.innerHTML = `
      <i data-lucide="${icon}" class="forecast-exhaustion__icon"></i>
      <div class="forecast-exhaustion__body">
        <div class="forecast-exhaustion__title">${escapeHTML(T('js.analytics.exhaustion_title'))}</div>
        <div class="forecast-exhaustion__message">${escapeHTML(message)}</div>
        ${sub ? `<div class="forecast-exhaustion__sub">${escapeHTML(sub)}</div>` : ''}
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
      items.push(`<span class="risk-badge"><i data-lucide="zap"></i> ${T('js.analytics.risk_spike')}</span>`);
    }
    if (risks.unusual_growth) {
      items.push(`<span class="risk-badge"><i data-lucide="trending-up"></i> ${T('js.analytics.risk_growth')}</span>`);
    }
    if (risks.exhaustion) {
      items.push(`<span class="risk-badge"><i data-lucide="alert-triangle"></i> ${T('js.analytics.risk_exhaustion')}</span>`);
    }
    return items.length > 0 ? `<div class="risk-badges">${items.join('')}</div>` : '';
  }

  // ── Range tabs ────────────────────────────────────────────────────────────

  let currentRange = '24h';

  function initRangeTabs(serverID) {
    const tabs = byId('globalRangeTabs');
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
