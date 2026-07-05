/* Alert rule form — metric-aware condition builder.
 *
 * Progressive enhancement over the SSR form in content-alert-rule-form:
 * the server renders the correct condition controls for the persisted
 * metric, and this script re-adapts them as the operator changes the
 * picker. Without JavaScript every control stays visible and the server
 * normalises whatever is posted, so nothing here is load-bearing.
 *
 * Metric metadata (kind / unit / hint / default threshold / forecast
 * gates) rides on the <option> elements as data-attributes; the strings
 * the live summary needs are pre-translated data-attributes on the form,
 * so this file contains no user-facing copy of its own.
 */
(function () {
  'use strict';

  var form = document.querySelector('[data-rule-form]');
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

  function selectedMetric() {
    return metricSelect.options[metricSelect.selectedIndex] || null;
  }

  function metricKind(option) {
    return (option && option.getAttribute('data-kind')) || 'threshold';
  }

  function checkedRadio(name) {
    return form.querySelector('input[name="' + name + '"]:checked');
  }

  /* Adapt the condition controls to the selected metric. */
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
      // Switching metrics changes what the number means, so re-seed it with
      // the metric's suggested default instead of keeping a stale value.
      if (resetThreshold) {
        thresholdInput.value = option.getAttribute('data-default') || '';
      }
    }

    updatePreview();
  }

  /* Compose the live summary chips from already-translated fragments. */
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

  // First paint: sync the UI to the server-rendered selection without
  // touching the persisted threshold value.
  applyMetric(false);
})();
