/* Alerts UI enhancements — rule condition builder & history table filter.
 *
 * Progressive enhancement over SSR templates:
 *  - Scoped to the owning .tab-pane (never singleton document queries) so
 *    multiple open tabs work independently without clobbering each other.
 *  - On the rule builder form, adapts threshold/comparator controls as metrics
 *    change while preserving manual user threshold edits across metric switches.
 *  - On the alerts overview page, filters visible history events by text or state.
 */
(function () {
  'use strict';

  var currentScript = document.currentScript;
  var root = (currentScript && currentScript.closest && currentScript.closest('.tab-pane')) || document;
  if (root.dataset && root.dataset.alertsReady === '1') return;
  if (root.dataset) root.dataset.alertsReady = '1';

  function initRuleForm() {
    var form = root.querySelector('[data-rule-form]');
    if (!form) return;

    var metricSelect = form.querySelector('[data-metric-select]');
    if (!metricSelect) return;

    var hintEl = form.querySelector('[data-metric-hint]');
    var gateLimit = form.querySelector('[data-gate-limit]');
    var gateHistory = form.querySelector('[data-gate-history]');
    var booleanNote = form.querySelector('[data-condition-boolean]');
    var conditionRow = form.querySelector('[data-condition-row]');
    var comparatorWrap = form.querySelector('[data-comparator-wrap]');
    var thresholdLabel = form.querySelector('[data-threshold-label]');
    var thresholdInput = form.querySelector('[data-threshold-input]');
    var thresholdUnit = form.querySelector('[data-threshold-unit]');
    var daysNote = form.querySelector('[data-days-note]');
    var hitsInput = form.querySelector('[data-hits-input]');
    var cooldownSelect = form.querySelector('[data-cooldown-select]');

    var preview = form.querySelector('[data-rule-preview]');
    var previewCondition = form.querySelector('[data-preview-condition]');
    var previewCadence = form.querySelector('[data-preview-cadence]');
    var previewCooldown = form.querySelector('[data-preview-cooldown]');
    var previewSeverity = form.querySelector('[data-preview-severity]');

    var thresholdEdited = false;

    function selectedMetric() {
      return metricSelect.options[metricSelect.selectedIndex] || null;
    }

    function metricKind(option) {
      return (option && option.getAttribute('data-kind')) || 'threshold';
    }

    function checkedRadio(name) {
      return form.querySelector('input[name="' + name + '"]:checked');
    }

    // Preserve manual threshold entries or persisted custom values on edit forms.
    if (thresholdInput) {
      var initOpt = selectedMetric();
      var initVal = thresholdInput.value.trim();
      var initDefault = initOpt ? (initOpt.getAttribute('data-default') || '').trim() : '';
      if (initVal !== '' && initVal !== initDefault) {
        thresholdEdited = true;
      }
      thresholdInput.addEventListener('input', function () {
        thresholdEdited = thresholdInput.value.trim() !== '';
      });
    }

    function applyMetric(resetThreshold) {
      var option = selectedMetric();
      if (!option) return;
      var kind = metricKind(option);
      var unit = option.getAttribute('data-unit') || '';

      if (hintEl) hintEl.textContent = option.getAttribute('data-hint') || '';
      if (gateLimit) gateLimit.hidden = option.getAttribute('data-needs-limit') !== '1';
      if (gateHistory) gateHistory.hidden = option.getAttribute('data-needs-history') !== '1';

      if (booleanNote) booleanNote.hidden = kind !== 'boolean';
      if (conditionRow) conditionRow.hidden = kind === 'boolean';
      if (comparatorWrap) comparatorWrap.hidden = kind !== 'threshold';
      if (daysNote) daysNote.hidden = kind !== 'days';

      if (thresholdLabel) {
        thresholdLabel.textContent = kind === 'days'
          ? (form.getAttribute('data-label-days') || '')
          : (form.getAttribute('data-label-threshold') || '');
      }
      if (thresholdUnit) {
        var unitText = kind === 'days' ? (form.getAttribute('data-unit-days') || '') : unit;
        thresholdUnit.textContent = unitText;
        thresholdUnit.hidden = unitText === '';
      }
      if (thresholdInput) {
        if (kind === 'days') {
          thresholdInput.setAttribute('max', '60');
          thresholdInput.setAttribute('step', '1');
        } else {
          thresholdInput.removeAttribute('max');
          thresholdInput.setAttribute('step', 'any');
        }
        if (resetThreshold && !thresholdEdited) {
          thresholdInput.value = option.getAttribute('data-default') || '';
        }
      }

      updatePreview();
    }

    function updatePreview() {
      if (!preview) return;
      var option = selectedMetric();
      if (!option) return;
      var kind = metricKind(option);
      var metricLabel = (option.textContent || '').trim();

      if (previewCondition) {
        if (kind === 'boolean') {
          previewCondition.textContent = metricLabel + ' — ' + (form.getAttribute('data-detected-label') || '');
        } else {
          var value = thresholdInput ? thresholdInput.value : '';
          var symbol = '≥';
          var unit = option.getAttribute('data-unit') || '';
          if (kind === 'days') {
            symbol = '≤';
            unit = form.getAttribute('data-unit-days') || '';
          } else {
            var comparator = checkedRadio('comparator');
            if (comparator) symbol = comparator.getAttribute('data-symbol') || '≥';
          }
          var suffix = unit === '%' ? '%' : (unit ? ' ' + unit : '');
          previewCondition.textContent = value === ''
            ? metricLabel + ' ' + symbol + ' …'
            : metricLabel + ' ' + symbol + ' ' + value + suffix;
        }
      }

      if (previewCadence) {
        var hits = hitsInput ? parseInt(hitsInput.value, 10) : 1;
        previewCadence.textContent = (!hits || hits <= 1)
          ? (form.getAttribute('data-first-breach') || '')
          : (form.getAttribute('data-after-hits') || '').replace('{count}', String(hits));
      }

      if (previewCooldown && cooldownSelect) {
        var cooldownOption = cooldownSelect.options[cooldownSelect.selectedIndex];
        var cooldownText = cooldownOption ? (cooldownOption.textContent || '').trim() : '';
        previewCooldown.textContent = cooldownSelect.value === '0'
          ? (form.getAttribute('data-no-cooldown') || '')
          : (form.getAttribute('data-with-cooldown') || '').replace('{duration}', cooldownText);
      }

      if (previewSeverity) {
        var severity = checkedRadio('severity');
        previewSeverity.className = 'rule-preview__chip';
        if (severity) {
          var nameEl = severity.parentElement.querySelector('.severity-option__name');
          previewSeverity.textContent = nameEl ? (nameEl.textContent || '').trim() : severity.value;
          previewSeverity.classList.add('rule-preview__chip--sev-' + severity.value);
        }
      }

      preview.hidden = false;
    }

    metricSelect.addEventListener('change', function () { applyMetric(true); });
    form.addEventListener('input', updatePreview);
    form.addEventListener('change', updatePreview);

    applyMetric(false);
  }

  function initHistoryFilter() {
    var historyCard = root.querySelector('#alert-history');
    if (!historyCard) return;

    var queryInput = historyCard.querySelector('[data-history-query]');
    var stateBtns = historyCard.querySelectorAll('[data-history-state]');
    var rows = historyCard.querySelectorAll('tbody tr[data-history-row]');
    var emptyNote = historyCard.querySelector('[data-history-empty]');
    if (!rows.length) return;

    var activeState = 'all';

    function applyFilter() {
      var q = queryInput ? (queryInput.value || '').trim().toLowerCase() : '';
      var visibleCount = 0;

      for (var i = 0; i < rows.length; i++) {
        var row = rows[i];
        var rowState = row.getAttribute('data-state') || '';
        var matchesState = activeState === 'all' || rowState === activeState;
        var matchesQuery = !q || (row.textContent || '').toLowerCase().indexOf(q) !== -1;
        var show = matchesState && matchesQuery;
        row.hidden = !show;
        if (show) visibleCount++;
      }

      if (emptyNote) emptyNote.hidden = visibleCount > 0;
    }

    if (queryInput) {
      queryInput.addEventListener('input', applyFilter);
    }

    for (var i = 0; i < stateBtns.length; i++) {
      stateBtns[i].addEventListener('click', function () {
        activeState = this.getAttribute('data-history-state') || 'all';
        for (var j = 0; j < stateBtns.length; j++) {
          var active = stateBtns[j] === this;
          stateBtns[j].classList.toggle('is-active', active);
          stateBtns[j].setAttribute('aria-pressed', active ? 'true' : 'false');
        }
        applyFilter();
      });
    }
  }

  initRuleForm();
  initHistoryFilter();
})();
