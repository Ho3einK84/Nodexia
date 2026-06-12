/* Bulk-action progressive enhancement for the servers list.
 *
 * The toolbar lives in #bulk-form; the per-server checkboxes live in the cards
 * below and associate with the form via the HTML `form="bulk-form"` attribute
 * (so the delete forms inside the cards are never nested inside the bulk form).
 *
 * Without JS the toolbar is always visible and the form submits normally.
 * With JS we sync the selected-count label, the select-all state, the submit
 * button's enabled state, and a per-action confirmation message.
 */
(function () {
  'use strict';

  var form = document.getElementById('bulk-form');
  if (!form) return;

  var selectAll = document.getElementById('bulk-select-all');
  var countEl   = document.getElementById('bulk-count');
  var actionEl  = document.getElementById('bulk-action');
  var submitBtn = document.getElementById('bulk-submit');

  // Checkboxes are outside the <form>, associated via form="bulk-form", so we
  // query the whole document rather than the form subtree.
  function serverCheckboxes() {
    return Array.prototype.slice.call(
      document.querySelectorAll('.bulk-server-checkbox')
    );
  }

  function confirmMessage(action, n) {
    var noun = n === 1 ? '1 server' : n + ' servers';
    switch (action) {
      case 'delete':
        return 'Delete ' + noun + ' from the registry? This cannot be undone.';
      case 'reboot':
        return 'Reboot ' + noun + ' now? Active connections will drop.';
      case 'update':
        return 'Update packages on ' + noun + '? This can take several minutes.';
      default:
        return 'Run this bulk action on ' + noun + '?';
    }
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
      // app.js reads data-confirm off the submitter on submit; keep it current.
      submitBtn.setAttribute(
        'data-confirm',
        confirmMessage(actionEl ? actionEl.value : '', n)
      );
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

  if (actionEl) {
    actionEl.addEventListener('change', updateState);
  }

  // Delegate on the document so dynamically present checkboxes are covered.
  document.addEventListener('change', function (e) {
    if (e.target && e.target.classList &&
        e.target.classList.contains('bulk-server-checkbox')) {
      updateState();
    }
  });

  // Initial state.
  updateState();
})();
