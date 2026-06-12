(function () {
  'use strict';

  function initPresetChips() {
    var input = document.getElementById('cmd-input');
    if (!input) return;
    document.querySelectorAll('.preset-chip[data-preset-cmd]').forEach(function (chip) {
      chip.addEventListener('click', function () {
        input.value = chip.getAttribute('data-preset-cmd');
        input.dispatchEvent(new Event('input')); // keep terminal hint in sync
        input.focus();
      });
    });
  }

  function initCommandSubmit() {
    var input = document.getElementById('cmd-input');
    var btn = document.getElementById('cmd-run-btn');
    if (!input || !btn) return;
    input.addEventListener('keydown', function (e) {
      if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
        e.preventDefault();
        btn.click();
      }
    });
  }

  function initHistoryRows() {
    var input = document.getElementById('cmd-input');
    document.querySelectorAll('.history-row').forEach(function (row) {
      row.addEventListener('click', function (e) {
        if (e.target.closest('.rerun-btn')) return;
        row.classList.toggle('is-expanded');
      });
      var rerunBtn = row.querySelector('.rerun-btn');
      if (rerunBtn && input) {
        rerunBtn.addEventListener('click', function (e) {
          e.stopPropagation();
          input.value = rerunBtn.getAttribute('data-cmd') || '';
          input.dispatchEvent(new Event('input')); // keep terminal hint in sync
          input.focus();
        });
      }
    });
  }

  /* Client-side hint for interactive commands. The program list comes from
   * the server (data-interactive-programs), which stays the single source of
   * truth; the server also performs the authoritative redirect on submit.
   * This only toggles a visible note + emphasises the terminal button. */
  function initTerminalDetect() {
    var form = document.getElementById('cmd-form');
    var input = document.getElementById('cmd-input');
    var note = document.getElementById('cmd-terminal-note');
    var termBtn = document.getElementById('cmd-terminal-btn');
    if (!form || !input || (!note && !termBtn)) return;

    var programs = {};
    (form.getAttribute('data-interactive-programs') || '')
      .split(/\s+/)
      .forEach(function (name) { if (name) programs[name] = true; });

    var wrappers = {
      sudo: 1, doas: 1, env: 1, nice: 1, ionice: 1, nohup: 1,
      time: 1, exec: 1, command: 1, stdbuf: 1, setsid: 1, xargs: 1
    };

    function firstProgram(segment) {
      var tokens = segment.trim().split(/\s+/);
      for (var i = 0; i < tokens.length; i++) {
        var token = tokens[i];
        if (!token) continue;
        if (/^[A-Za-z_][A-Za-z0-9_]*=/.test(token)) continue; // env prefix
        if (token.charAt(0) === '-') continue;                // wrapper flag
        var base = token.replace(/^.*\//, '');
        if (wrappers[base]) continue;
        return base;
      }
      return '';
    }

    function isInteractive(command) {
      var segments = command.split(/\|\||&&|[|;&\n]/);
      for (var i = 0; i < segments.length; i++) {
        var segment = segments[i];
        var program = firstProgram(segment);
        if (!program) continue;
        if (programs[program]) return true;
        if ((program === 'tail' || program === 'journalctl' || program === 'kubectl') &&
            /(^|\s)(--follow|-[a-zA-Z]*f[a-zA-Z]*)(\s|$)/.test(segment)) {
          return true;
        }
      }
      return false;
    }

    var timer = null;
    function refresh() {
      var hot = isInteractive(input.value);
      if (note) note.hidden = !hot;
      if (termBtn) termBtn.classList.toggle('cmd-terminal-btn--suggested', hot);
    }

    input.addEventListener('input', function () {
      clearTimeout(timer);
      timer = setTimeout(refresh, 200);
    });
    refresh();
  }

  function boot() {
    initPresetChips();
    initCommandSubmit();
    initHistoryRows();
    initTerminalDetect();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
