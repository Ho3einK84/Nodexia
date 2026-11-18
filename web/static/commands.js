/* Commands workspace — scoped to its .tab-pane instance.
 * Handles preset chip selection, Ctrl/Cmd+Enter run shortcut, semantic
 * history expand/collapse & rerun loading, and interactive command hints.
 */
(function () {
  'use strict';

  var currentScript = document.currentScript;
  var root = (currentScript && currentScript.closest && currentScript.closest('.tab-pane')) || document;
  if (root.dataset && root.dataset.commandsReady === '1') return;
  if (root.dataset) root.dataset.commandsReady = '1';

  function T(key, params) {
    return window.nxT ? window.nxT(key, params) : key;
  }

  var form = root.querySelector('#cmd-form');
  var input = root.querySelector('#cmd-input');
  var runBtn = root.querySelector('#cmd-run-btn');
  var presets = root.querySelectorAll('.preset-chip[data-preset-cmd]');
  var note = root.querySelector('#cmd-terminal-note');
  var termBtn = root.querySelector('#cmd-terminal-btn');

  // Track genuine manual edits so clicking a preset or rerun button does not
  // silently clobber work the operator typed by hand.
  var editorDirty = false;
  var applyingProgrammatic = false;

  function syncPresetStates() {
    if (!input || !presets.length) return;
    var val = input.value.trim();
    for (var i = 0; i < presets.length; i++) {
      var chip = presets[i];
      var match = val !== '' && (chip.getAttribute('data-preset-cmd') || '').trim() === val;
      chip.classList.toggle('is-active', match);
      chip.setAttribute('aria-pressed', match ? 'true' : 'false');
    }
  }

  function applyCommand(cmd) {
    if (!input) return;
    var next = cmd || '';
    if (input.value === next) {
      input.focus();
      return;
    }
    if (editorDirty && input.value.trim() !== '') {
      if (!window.confirm(T('js.commands.confirm_replace'))) {
        input.focus();
        return;
      }
    }
    applyingProgrammatic = true;
    input.value = next;
    editorDirty = false;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    applyingProgrammatic = false;
    syncPresetStates();
    input.focus();
  }

  if (input) {
    var initVal = input.value.trim();
    if (initVal !== '') {
      var matchesPreset = false;
      for (var p = 0; p < presets.length; p++) {
        if ((presets[p].getAttribute('data-preset-cmd') || '').trim() === initVal) {
          matchesPreset = true;
          break;
        }
      }
      editorDirty = !matchesPreset;
    }

    input.addEventListener('input', function () {
      if (!applyingProgrammatic) {
        editorDirty = input.value.trim() !== '';
      }
      syncPresetStates();
    });

    syncPresetStates();
  }

  for (var i = 0; i < presets.length; i++) {
    presets[i].addEventListener('click', function () {
      applyCommand(this.getAttribute('data-preset-cmd'));
    });
  }

  if (input && runBtn) {
    input.addEventListener('keydown', function (e) {
      if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
        e.preventDefault();
        runBtn.click();
      }
    });
  }

  var historyRows = root.querySelectorAll('.history-row');
  for (var h = 0; h < historyRows.length; h++) {
    (function (row) {
      var toggleBtn = row.querySelector('.history-row__toggle');
      var detail = row.querySelector('.history-row__detail');
      if (toggleBtn && detail) {
        toggleBtn.addEventListener('click', function () {
          var expanded = detail.hidden;
          detail.hidden = !expanded;
          row.classList.toggle('is-expanded', expanded);
          toggleBtn.setAttribute('aria-expanded', expanded ? 'true' : 'false');
        });
      }

      var rerunBtn = row.querySelector('.rerun-btn');
      if (rerunBtn) {
        rerunBtn.addEventListener('click', function (e) {
          e.stopPropagation();
          applyCommand(rerunBtn.getAttribute('data-cmd') || '');
        });
      }
    })(historyRows[h]);
  }

  /* Client-side hint for interactive commands. The program list comes from
   * the server (data-interactive-programs), which stays the single source of
   * truth; the server also performs the authoritative redirect on submit. */
  if (form && input && (note || termBtn)) {
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
      for (var t = 0; t < tokens.length; t++) {
        var token = tokens[t];
        if (!token) continue;
        if (/^[A-Za-z_][A-Za-z0-9_]*=/.test(token)) continue;
        if (token.charAt(0) === '-') continue;
        var base = token.replace(/^.*\//, '');
        if (wrappers[base]) continue;
        return base;
      }
      return '';
    }

    function isInteractive(command) {
      var segments = command.split(/\|\||&&|[|;&\n]/);
      for (var s = 0; s < segments.length; s++) {
        var segment = segments[s];
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

    if (Array.isArray(root.__nxCleanups)) {
      root.__nxCleanups.push(function () { clearTimeout(timer); });
    }

    refresh();
  }
})();
