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

  var currentScript = document.currentScript;
  var root = (currentScript && currentScript.closest && currentScript.closest('.tab-pane')) || document;

  var form = root.querySelector('[data-bulk-form], #bulk-form');
  if (!form || form.dataset.bulkReady === '1') return;
  form.dataset.bulkReady = '1';

  var toolbar   = root.querySelector('[data-bulk-toolbar], #bulk-toolbar');
  var selectAll = root.querySelector('[data-bulk-select-all], #bulk-select-all');
  var countEl   = root.querySelector('[data-bulk-count], #bulk-count');
  var actionEl  = root.querySelector('[data-bulk-action], #bulk-action');
  var submitBtn = root.querySelector('[data-bulk-submit], #bulk-submit');

  if (root !== document) {
    var scopedFormId = 'bulk-form-' + Math.random().toString(36).slice(2, 8);
    form.id = scopedFormId;
    root.querySelectorAll('.bulk-server-checkbox').forEach(function (cb) {
      cb.setAttribute('form', scopedFormId);
    });
    if (selectAll && selectAll.parentElement && selectAll.parentElement.tagName === 'LABEL') {
      selectAll.parentElement.removeAttribute('for');
    }
  }

  // Checkboxes live outside <form>, associated via form="bulk-form", so we
  // query within the owning tab pane (or document on full page load).
  function serverCheckboxes() {
    return Array.prototype.slice.call(
      root.querySelectorAll('.bulk-server-checkbox')
    );
  }

  function confirmMessage(action, n) {
    // The localized noun ("N server(s)") is interpolated into each message so
    // every language can place the count naturally; nxTn handles pluralization.
    var noun = window.nxTn ? window.nxTn('js.bulk.server_count', n) : n + ' servers';
    var t = window.nxT || function (key) { return key; };
    switch (action) {
      case 'delete':
        return t('js.bulk.confirm_delete', { noun: noun });
      case 'reboot':
        return t('js.bulk.confirm_reboot', { noun: noun });
      case 'update':
        return t('js.bulk.confirm_update', { noun: noun });
      case 'node-restart':
        return t('js.bulk.confirm_node_restart', { noun: noun });
      case 'node-update':
        return t('js.bulk.confirm_node_update', { noun: noun });
      default:
        return t('js.bulk.confirm_generic', { noun: noun });
    }
  }

  function updateState() {
    var boxes     = serverCheckboxes();
    var checked   = boxes.filter(function (cb) { return cb.checked; });
    var n         = checked.length;
    var actionVal = actionEl ? actionEl.value : '';

    if (countEl) {
      if (window.nxT) {
        countEl.textContent = window.nxT('js.bulk.selected', { count: n });
      } else if (n > 0 || !countEl.textContent.trim()) {
        countEl.textContent = n + ' selected';
      }
    }

    if (toolbar) {
      toolbar.classList.toggle('is-empty', n === 0);
      toolbar.classList.toggle('is-active', n > 0);
    }

    if (selectAll) {
      selectAll.checked       = n > 0 && n === boxes.length;
      selectAll.indeterminate = n > 0 && n < boxes.length;
    }

    if (submitBtn) {
      submitBtn.disabled = n === 0 || actionVal === '';
      // app.js reads data-confirm off the submitter on submit; keep it current.
      submitBtn.setAttribute(
        'data-confirm',
        confirmMessage(actionVal, n)
      );
    }
  }

  function onSelectAllChange() {
    serverCheckboxes().forEach(function (cb) {
      var card = cb.closest && cb.closest('[data-server-card]');
      if (card && card.hidden) return;
      cb.checked = selectAll.checked;
    });
    updateState();
  }

  function onRootChange(e) {
    if (e.target && e.target.classList &&
        e.target.classList.contains('bulk-server-checkbox')) {
      updateState();
    }
  }

  if (selectAll) {
    selectAll.addEventListener('change', onSelectAllChange);
  }

  if (actionEl) {
    actionEl.addEventListener('change', updateState);
  }

  // Scope delegated change listener to this pane and tear down cleanly when the tab closes/reloads.
  root.addEventListener('change', onRootChange);
  root.addEventListener('nx:servers-changed', updateState);

  if (root !== document) {
    root.__nxCleanups = root.__nxCleanups || [];
    root.__nxCleanups.push(function () {
      root.removeEventListener('change', onRootChange);
      root.removeEventListener('nx:servers-changed', updateState);
      if (selectAll) selectAll.removeEventListener('change', onSelectAllChange);
      if (actionEl) actionEl.removeEventListener('change', updateState);
    });
  }

  // Initial state (and re-sync once deferred ES module core.js exposes window.nxT).
  updateState();
  if (!window.nxT) {
    document.addEventListener('DOMContentLoaded', updateState, { once: true });
  }
})();
