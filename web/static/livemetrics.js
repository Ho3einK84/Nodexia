/* Real-time monitoring stream client.
 *
 * Opens a WebSocket to the per-server live-metrics endpoint and renders CPU
 * (overall + per core), memory, disks, load, network, and uptime as frames
 * arrive (~every few seconds). Progressive enhancement: if the live card is
 * absent (server has no stored credentials) this file quietly no-ops. The
 * scheduled-snapshot UI above is untouched.
 *
 * Reconnect: on any drop we retry with capped exponential backoff and surface a
 * "reconnecting" status without clearing the last good values.
 */
(function () {
  'use strict';

  var card = document.querySelector('[data-live-url]');
  if (!card || typeof WebSocket === 'undefined') return;

  var path = card.getAttribute('data-live-url');
  var wsURL = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + path;

  var statusEl = card.querySelector('[data-live-status]');
  var statusText = card.querySelector('[data-live-status-text]');
  var cpuEl = card.querySelector('[data-live-cpu]');
  var cpuBar = card.querySelector('[data-live-cpu-bar]');
  var coresEl = card.querySelector('[data-live-cores]');
  var memEl = card.querySelector('[data-live-mem]');
  var memBar = card.querySelector('[data-live-mem-bar]');
  var memDetail = card.querySelector('[data-live-mem-detail]');
  var disksEl = card.querySelector('[data-live-disks]');
  var loadEl = card.querySelector('[data-live-load]');
  var netEl = card.querySelector('[data-live-net]');
  var uptimeEl = card.querySelector('[data-live-uptime]');
  var updatedEl = card.querySelector('[data-live-updated]');

  var socket = null;
  var backoff = 1000;
  var reconnectTimer = null;
  var stopped = false;
  var coreCount = -1;
  var diskKey = '';

  function setStatus(kind, text) {
    if (statusEl) statusEl.className = 'live-status live-status--' + kind;
    if (statusText) statusText.textContent = text;
  }

  // Bucketed color matching the snapshot gauges: green / amber / red.
  function levelOf(value) {
    if (value <= 60) return 'ok';
    if (value <= 80) return 'warn';
    return 'crit';
  }

  function setBar(fill, value) {
    if (!fill) return;
    var v = Math.max(0, Math.min(100, value || 0));
    fill.style.width = v.toFixed(1) + '%';
    fill.className = 'live-bar__fill live-bar__fill--' + levelOf(v);
  }

  function humanBytes(bytes) {
    if (!bytes || bytes < 0) bytes = 0;
    var units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
    var size = bytes;
    var i = 0;
    while (size >= 1024 && i < units.length - 1) {
      size /= 1024;
      i++;
    }
    return size.toFixed(size >= 100 || i === 0 ? 0 : 1) + ' ' + units[i];
  }

  function humanUptime(seconds) {
    seconds = Math.max(0, Math.floor(seconds || 0));
    var d = Math.floor(seconds / 86400);
    seconds %= 86400;
    var h = Math.floor(seconds / 3600);
    var m = Math.floor((seconds % 3600) / 60);
    var parts = [];
    if (d > 0) parts.push(d + 'd');
    if (h > 0 || d > 0) parts.push(h + 'h');
    parts.push(m + 'm');
    return parts.join(' ');
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
      var v = Math.max(0, Math.min(100, value || 0));
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
    if (cpuEl) cpuEl.textContent = (m.cpuPercent || 0).toFixed(0) + '%';
    setBar(cpuBar, m.cpuPercent);
    renderCores(m.perCore);

    if (memEl) memEl.textContent = (m.memPercent || 0).toFixed(0) + '%';
    setBar(memBar, m.memPercent);
    if (memDetail) {
      memDetail.textContent = humanBytes(m.memUsedKB * 1024) + ' / ' + humanBytes(m.memTotalKB * 1024) +
        (m.swapPercent > 0 ? '  ·  swap ' + m.swapPercent.toFixed(0) + '%' : '');
    }

    renderDisks(m.disks);

    if (loadEl) loadEl.textContent = fmt2(m.load1) + '  ' + fmt2(m.load5) + '  ' + fmt2(m.load15);
    if (netEl) netEl.textContent = '↓ ' + humanBytes(m.netRxBytes) + '   ↑ ' + humanBytes(m.netTxBytes);
    if (uptimeEl) uptimeEl.textContent = humanUptime(m.uptimeSeconds);
    if (updatedEl) updatedEl.textContent = new Date(m.collectedAt || Date.now()).toLocaleTimeString();
  }

  function fmt2(v) { return (v || 0).toFixed(2); }

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
      // onclose fires next and drives the reconnect; just close defensively.
      try { ws.close(); } catch (err) {}
    };
  }

  window.addEventListener('beforeunload', function () {
    stopped = true;
    if (socket) { try { socket.close(); } catch (err) {} }
  });

  connect();
})();
