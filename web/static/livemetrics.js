/* Real-time monitoring stream client.
 *
 * Drives the unified "Resource health" card: the CPU / RAM / Disk arc gauges,
 * per-core bars, mounted-filesystem bars, and the load / network / uptime strip
 * all update in place as frames arrive over a WebSocket (~every few seconds).
 *
 * Progressive enhancement: if the live card is absent (no stored credentials)
 * this file no-ops and the server-rendered snapshot gauges stand on their own.
 * Reconnect uses capped exponential backoff and never clears the last good
 * values, so a blip just flips the status pill to "Reconnecting".
 */
(function () {
  'use strict';

  var card = document.querySelector('[data-live-url]');
  if (!card || typeof WebSocket === 'undefined') return;

  var path = card.getAttribute('data-live-url');
  var wsURL = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + path;

  var statusEl = card.querySelector('[data-live-status]');
  var statusText = card.querySelector('[data-live-status-text]');
  var coresEl = card.querySelector('[data-live-cores]');
  var disksEl = card.querySelector('[data-live-disks]');

  var socket = null;
  var backoff = 1000;
  var reconnectTimer = null;
  var stopped = false;
  var coreCount = -1;
  var diskKey = '';

  function q(sel) { return card.querySelector(sel); }
  function setText(sel, text) {
    var el = q(sel);
    if (el) el.textContent = text;
  }

  function setStatus(kind, text) {
    if (statusEl) statusEl.className = 'live-status live-status--' + kind;
    if (statusText) statusText.textContent = text;
  }

  function clampPct(value) { return Math.max(0, Math.min(100, value || 0)); }

  // Bucketed color matching the gauge palette: green / amber / red.
  function levelOf(value) {
    if (value <= 60) return 'ok';
    if (value <= 80) return 'warn';
    return 'crit';
  }
  function gaugeColor(value) {
    if (value <= 60) return { stroke: '#4ade80', glow: 'rgba(74, 222, 128, 0.45)' };
    if (value <= 80) return { stroke: '#facc15', glow: 'rgba(250, 204, 21, 0.45)' };
    return { stroke: '#f87171', glow: 'rgba(248, 113, 113, 0.5)' };
  }

  // setGauge animates one arc gauge to value and recolors it, matching the
  // server-rendered gauge geometry exactly (same arc path + dash math).
  function setGauge(metric, value) {
    var gauge = card.querySelector('[data-gauge-metric="' + metric + '"]');
    if (!gauge) return;
    var v = clampPct(value);
    var arc = gauge.querySelector('.gauge__arc');
    if (arc) {
      var length = arc.getTotalLength();
      arc.style.strokeDasharray = length;
      arc.style.strokeDashoffset = length * (1 - v / 100);
      var color = gaugeColor(v);
      gauge.style.setProperty('--gauge-color', color.stroke);
      gauge.style.setProperty('--gauge-glow', color.glow);
    }
    var valEl = gauge.querySelector('[data-gauge-value]');
    if (valEl) valEl.textContent = v.toFixed(0) + '%';
    gauge.setAttribute('data-gauge', v.toFixed(2));
  }

  function setBar(fill, value) {
    if (!fill) return;
    var v = clampPct(value);
    fill.style.width = v.toFixed(1) + '%';
    fill.className = 'live-bar__fill live-bar__fill--' + levelOf(v);
  }

  function humanBytes(bytes) {
    if (!bytes || bytes < 0) bytes = 0;
    var units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
    var size = bytes, i = 0;
    while (size >= 1024 && i < units.length - 1) { size /= 1024; i++; }
    return size.toFixed(size >= 100 || i === 0 ? 0 : 1) + ' ' + units[i];
  }

  function humanUptime(seconds) {
    seconds = Math.max(0, Math.floor(seconds || 0));
    var d = Math.floor(seconds / 86400); seconds %= 86400;
    var h = Math.floor(seconds / 3600);
    var m = Math.floor((seconds % 3600) / 60);
    var parts = [];
    if (d > 0) parts.push(d + 'd');
    if (h > 0 || d > 0) parts.push(h + 'h');
    parts.push(m + 'm');
    return parts.join(' ');
  }

  function fmt2(v) { return (v || 0).toFixed(2); }

  // The single Disk gauge tracks the root filesystem; fall back to the busiest
  // mount when "/" is not reported.
  function pickRootDisk(disks) {
    if (!disks || !disks.length) return null;
    for (var i = 0; i < disks.length; i++) {
      if (disks[i].mount === '/') return disks[i];
    }
    var max = disks[0];
    for (var j = 1; j < disks.length; j++) {
      if (disks[j].percent > max.percent) max = disks[j];
    }
    return max;
  }

  function renderCores(perCore) {
    if (!coresEl) return;
    perCore = perCore || [];
    if (perCore.length !== coreCount) {
      coresEl.innerHTML = '';
      perCore.forEach(function () {
        var core = document.createElement('div');
        core.className = 'live-core';
        var fill = document.createElement('div');
        fill.className = 'live-core__fill';
        core.appendChild(fill);
        coresEl.appendChild(core);
      });
      coreCount = perCore.length;
    }
    var fills = coresEl.querySelectorAll('.live-core__fill');
    perCore.forEach(function (value, i) {
      var fill = fills[i];
      if (!fill) return;
      var v = clampPct(value);
      fill.style.height = v.toFixed(0) + '%';
      fill.className = 'live-core__fill live-core__fill--' + levelOf(v);
      fill.parentNode.title = 'core ' + i + ': ' + v.toFixed(0) + '%';
    });
  }

  function renderDisks(disks) {
    if (!disksEl) return;
    disks = disks || [];
    var key = disks.map(function (d) { return d.mount; }).join('|');
    if (key !== diskKey) {
      disksEl.innerHTML = '';
      if (disks.length === 0) {
        var none = document.createElement('p');
        none.className = 'empty-state';
        none.textContent = 'No mounted filesystems reported.';
        disksEl.appendChild(none);
      }
      disks.forEach(function (d) {
        var row = document.createElement('div');
        row.className = 'live-disk';
        row.setAttribute('data-disk-mount', d.mount);
        row.innerHTML =
          '<span class="live-disk__mount"></span>' +
          '<div class="live-bar live-bar--sm"><div class="live-bar__fill" data-disk-fill style="width:0%"></div></div>' +
          '<span class="live-disk__detail" data-disk-detail></span>';
        row.querySelector('.live-disk__mount').textContent = d.mount;
        disksEl.appendChild(row);
      });
      diskKey = key;
    }
    disks.forEach(function (d) {
      var row = disksEl.querySelector('[data-disk-mount="' + cssEscape(d.mount) + '"]');
      if (!row) return;
      setBar(row.querySelector('[data-disk-fill]'), d.percent);
      row.querySelector('[data-disk-detail]').textContent =
        d.percent.toFixed(0) + '% · ' + humanBytes(d.usedKB * 1024) + ' / ' + humanBytes(d.totalKB * 1024);
    });
  }

  // Minimal attribute-selector escaping for mount paths (which contain "/").
  function cssEscape(value) {
    return String(value).replace(/["\\]/g, '\\$&');
  }

  function render(m) {
    setGauge('cpu', m.cpuPercent);
    setText('[data-live-cpu-detail]', (m.perCore ? m.perCore.length : 0) + ' cores');

    setGauge('ram', m.memPercent);
    setText('[data-live-mem-detail]',
      humanBytes(m.memUsedKB * 1024) + ' / ' + humanBytes(m.memTotalKB * 1024) +
      (m.swapPercent > 0 ? '  ·  swap ' + m.swapPercent.toFixed(0) + '%' : ''));

    var root = pickRootDisk(m.disks);
    if (root) {
      setGauge('disk', root.percent);
      setText('[data-live-disk-detail]',
        root.mount + '  ·  ' + humanBytes(root.usedKB * 1024) + ' / ' + humanBytes(root.totalKB * 1024));
    }

    renderCores(m.perCore);
    renderDisks(m.disks);

    setText('[data-live-load1]', fmt2(m.load1));
    setText('[data-live-load5]', fmt2(m.load5));
    setText('[data-live-load15]', fmt2(m.load15));
    setText('[data-live-net]', '↓ ' + humanBytes(m.netRxBytes) + '   ↑ ' + humanBytes(m.netTxBytes));
    setText('[data-live-uptime]', humanUptime(m.uptimeSeconds));
    setText('[data-live-updated]', new Date(m.collectedAt || Date.now()).toLocaleTimeString());
  }

  function scheduleReconnect() {
    if (stopped || reconnectTimer) return;
    setStatus('error', 'Reconnecting…');
    reconnectTimer = setTimeout(function () {
      reconnectTimer = null;
      connect();
    }, backoff);
    backoff = Math.min(backoff * 2, 15000);
  }

  function connect() {
    if (stopped) return;
    setStatus('connecting', 'Connecting…');
    var ws;
    try {
      ws = new WebSocket(wsURL);
    } catch (err) {
      scheduleReconnect();
      return;
    }
    socket = ws;

    ws.onopen = function () {
      backoff = 1000;
      setStatus('live', 'Live');
    };
    ws.onmessage = function (event) {
      var frame;
      try { frame = JSON.parse(event.data); } catch (err) { return; }
      if (frame.type === 'metrics' && frame.data) {
        setStatus('live', 'Live');
        render(frame.data);
      } else if (frame.type === 'error') {
        setStatus('error', 'Reconnecting…');
      }
    };
    ws.onclose = function () {
      socket = null;
      scheduleReconnect();
    };
    ws.onerror = function () {
      try { ws.close(); } catch (err) {}
    };
  }

  window.addEventListener('beforeunload', function () {
    stopped = true;
    if (socket) { try { socket.close(); } catch (err) {} }
  });

  connect();
})();
