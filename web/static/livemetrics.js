/* Real-time monitoring stream client.
 *
 * Drives the unified "Resource health" card: the CPU / RAM / Disk arc gauges
 * and the load / bandwidth / uptime strip update in place as frames arrive over
 * a WebSocket (~every few seconds).
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

  var socket = null;
  var backoff = 1000;
  var reconnectTimer = null;
  var stopped = false;
  // Previous network counters, for deriving current bandwidth (rate) from the
  // delta between consecutive frames. Reset on every (re)connect so a gap never
  // shows up as a misleading averaged-over-the-gap spike.
  var prevNet = null;

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

    setText('[data-live-load1]', fmt2(m.load1));
    setText('[data-live-load5]', fmt2(m.load5));
    setText('[data-live-load15]', fmt2(m.load15));

    // Current bandwidth = byte-counter delta over the elapsed time between
    // frames. The counters are cumulative since boot, so a negative delta means
    // the host rebooted and the counter reset — skip that sample.
    var nowMs = new Date(m.collectedAt || Date.now()).getTime();
    if (prevNet && nowMs > prevNet.t) {
      var dtSec = (nowMs - prevNet.t) / 1000;
      var drx = m.netRxBytes - prevNet.rx;
      var dtx = m.netTxBytes - prevNet.tx;
      if (dtSec > 0 && drx >= 0 && dtx >= 0) {
        var rxMbps = (drx * 8) / 1e6 / dtSec;
        var txMbps = (dtx * 8) / 1e6 / dtSec;
        var digits = Math.max(rxMbps, txMbps) >= 100 ? 0 : (Math.max(rxMbps, txMbps) >= 10 ? 1 : 2);
        setText('[data-live-bandwidth]', '↓ ' + rxMbps.toFixed(digits) + '  ↑ ' + txMbps.toFixed(digits) + ' Mbps');
      }
    }
    prevNet = { rx: m.netRxBytes, tx: m.netTxBytes, t: nowMs };

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
      prevNet = null; // re-seed the bandwidth baseline after any (re)connect
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
