(function () {
  'use strict';

  function initPresetChips() {
    var input = document.getElementById('cmd-input');
    if (!input) return;
    document.querySelectorAll('.preset-chip[data-preset-cmd]').forEach(function (chip) {
      chip.addEventListener('click', function () {
        input.value = chip.getAttribute('data-preset-cmd');
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
          input.focus();
        });
      }
    });
  }

  function boot() {
    initPresetChips();
    initCommandSubmit();
    initHistoryRows();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
