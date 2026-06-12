/* Bulk-action progressive enhancement for the servers list.
 *
 * Without JS the toolbar is always visible and the form submits normally.
 * With JS we sync the selected-count label and keep the select-all in sync.
 * The page never requires JS to function.
 */
(function () {
  'use strict';

  var form = document.getElementById('bulk-form');
  if (!form) return;

  var selectAll = document.getElementById('bulk-select-all');
  var countEl   = document.getElementById('bulk-count');
  var submitBtn = document.getElementById('bulk-submit');

  function serverCheckboxes() {
    return Array.prototype.slice.call(
      form.querySelectorAll('.bulk-server-checkbox')
    );
  }

  function updateState() {
    var boxes   = serverCheckboxes();
    var checked = boxes.filter(function (cb) { return cb.checked; });
    var n       = checked.length;

    if (countEl) {
      countEl.textContent = n + ' selected';
    }

    if (selectAll) {
      selectAll.checked       = n > 0 && n === boxes.length;
      selectAll.indeterminate = n > 0 && n < boxes.length;
    }

    if (submitBtn) {
      submitBtn.disabled = n === 0;
    }
  }

  if (selectAll) {
    selectAll.addEventListener('change', function () {
      serverCheckboxes().forEach(function (cb) {
        cb.checked = selectAll.checked;
      });
      updateState();
    });
  }

  // Delegate to catch dynamically inserted rows (pagination).
  form.addEventListener('change', function (e) {
    if (e.target && e.target.classList.contains('bulk-server-checkbox')) {
      updateState();
    }
  });

  // Initial state.
  updateState();
})();
