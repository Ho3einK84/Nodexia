/* Diagnostics Backup & Restore progressive enhancements (CSP-strict, zero-build).
 *
 * Scoped per [data-diagnostics-root] so direct loads, multi-tab navigation, and
 * post-restore pane reloads operate independently without leaks or global state.
 */
(function () {
  'use strict';

  var MAX_BACKUP_BYTES = 8 * 1024 * 1024;
  var currentScript = document.currentScript;
  var paneScope = (currentScript && currentScript.closest && currentScript.closest('.tab-pane')) || null;

  function t(key, params) {
    if (typeof window.nxT === 'function') {
      return window.nxT(key, params);
    }
    return key;
  }

  function formatBytes(bytes) {
    if (typeof window.nxHumanBytes === 'function') {
      return window.nxHumanBytes(bytes);
    }
    return String(bytes || 0) + ' B';
  }

  function renderIcons(el) {
    if (typeof window.nxRenderIcons === 'function') {
      try { window.nxRenderIcons(el); } catch (err) { /* ignore */ }
    }
  }

  function getFreshCSRFToken(form) {
    if (window.NodexiaTabs && typeof window.NodexiaTabs.refreshCSRFToken === 'function') {
      return window.NodexiaTabs.refreshCSRFToken().then(function (token) {
        if (token) return token;
        var input = form && form.querySelector('input[name="_csrf_token"]');
        return input ? input.value : '';
      });
    }
    return fetch('/api/csrf-token', { credentials: 'same-origin' })
      .then(function (resp) { return resp.ok ? resp.json() : null; })
      .then(function (data) {
        if (data && data.csrf_token) {
          document.querySelectorAll('input[name="_csrf_token"]').forEach(function (inp) {
            inp.value = data.csrf_token;
          });
          return data.csrf_token;
        }
        var input = form && form.querySelector('input[name="_csrf_token"]');
        return input ? input.value : '';
      })
      .catch(function () {
        var input = form && form.querySelector('input[name="_csrf_token"]');
        return input ? input.value : '';
      });
  }

  function initPasswordToggles(container) {
    container.querySelectorAll('[data-toggle-visibility]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var wrap = btn.closest('.backup-input-wrap');
        var input = wrap && wrap.querySelector('input');
        if (!input) return;
        var isSecret = input.type === 'password';
        input.type = isSecret ? 'text' : 'password';
        btn.setAttribute('aria-pressed', isSecret ? 'true' : 'false');
        btn.innerHTML = isSecret ? '<i data-lucide="eye-off" aria-hidden="true"></i>' : '<i data-lucide="eye" aria-hidden="true"></i>';
        renderIcons(btn);
      });
    });
  }

  function initExportCard(container) {
    var form = container.querySelector('[data-backup-export-form]');
    if (!form) return;

    var secretsToggle = form.querySelector('[data-export-secrets]');
    var passInput = form.querySelector('[data-export-passphrase]');
    var confirmWrap = form.querySelector('[data-export-confirm-wrap]');
    var confirmInput = form.querySelector('[data-export-passphrase-confirm]');
    var riskNotice = form.querySelector('[data-secrets-risk-notice]');
    var riskText = form.querySelector('[data-secrets-risk-text]');
    var mismatchNotice = form.querySelector('[data-export-mismatch]');
    var resultNotice = form.querySelector('[data-export-result]');
    var resultText = form.querySelector('[data-export-result-text]');

    function syncRiskAndConfirm() {
      var includeSecrets = Boolean(secretsToggle && secretsToggle.checked);
      var pass = passInput ? passInput.value : '';

      if (confirmWrap) {
        confirmWrap.hidden = pass.length === 0;
      }
      if (confirmInput) {
        confirmInput.required = pass.length > 0;
        if (pass.length === 0) {
          confirmInput.value = '';
          if (mismatchNotice) mismatchNotice.hidden = true;
        }
      }

      if (riskNotice && riskText) {
        if (!includeSecrets) {
          riskNotice.hidden = true;
        } else {
          riskNotice.hidden = false;
          riskNotice.classList.remove('flash-banner--warn', 'flash-banner--ok');
          if (pass.length === 0) {
            riskNotice.classList.add('flash-banner--warn');
            riskText.textContent = t('js.backup.secrets_unencrypted_risk');
          } else {
            riskNotice.classList.add('flash-banner--ok');
            riskText.textContent = t('js.backup.secrets_encrypted_ok');
          }
        }
      }

      if (mismatchNotice && confirmInput) {
        var showMismatch = pass.length > 0 && confirmInput.value.length > 0 && confirmInput.value !== pass;
        mismatchNotice.hidden = !showMismatch;
      }
    }

    if (secretsToggle) secretsToggle.addEventListener('change', syncRiskAndConfirm);
    if (passInput) passInput.addEventListener('input', syncRiskAndConfirm);
    if (confirmInput) confirmInput.addEventListener('input', syncRiskAndConfirm);

    form.addEventListener('submit', function (e) {
      if (resultNotice) resultNotice.hidden = true;
      var pass = passInput ? passInput.value : '';
      if (pass.length > 0 && confirmInput && confirmInput.value !== pass) {
        e.preventDefault();
        e.stopPropagation();
        if (mismatchNotice) {
          mismatchNotice.hidden = false;
        }
        confirmInput.focus();
      }
    });

    container.addEventListener('nodexia:download-complete', function (e) {
      if (!e.detail || (e.detail.form && e.detail.form !== form)) return;
      if (resultNotice && resultText) {
        resultText.textContent = t('js.backup.export_done', {
          filename: e.detail.filename || 'backup.json',
          size: formatBytes(e.detail.size || 0)
        });
        resultNotice.hidden = false;
        renderIcons(resultNotice);
      }
    });

    syncRiskAndConfirm();
  }

  function initRestoreCard(container) {
    var form = container.querySelector('[data-backup-import-form]');
    if (!form) return;

    var dropzone = form.querySelector('[data-backup-dropzone]');
    var fileInput = form.querySelector('[data-backup-file-input]');
    var dropEmpty = form.querySelector('[data-dropzone-empty]');
    var dropSelected = form.querySelector('[data-dropzone-selected]');
    var fileNameEl = form.querySelector('[data-file-name]');
    var fileSizeEl = form.querySelector('[data-file-size]');
    var fileClearBtn = form.querySelector('[data-file-clear]');
    var fileErrorEl = form.querySelector('[data-file-error]');
    var passInput = form.querySelector('[data-restore-passphrase]');
    var passFieldWrap = form.querySelector('[data-restore-passphrase-wrap]');
    var inspectBtn = form.querySelector('[data-backup-inspect-btn]');
    var inspectStatusEl = form.querySelector('[data-inspect-status]');
    var inspectPreviewEl = form.querySelector('[data-inspect-preview]');
    var confirmCheckbox = form.querySelector('[data-confirm-checkbox]');
    var confirmErrorEl = form.querySelector('[data-confirm-error]');
    var submitBtn = form.querySelector('[data-restore-submit]');

    var inspectController = null;
    var fileTooLarge = false;
    var activeFile = null;

    function getSelectedFile() {
      if (fileInput && fileInput.files && fileInput.files[0]) {
        return fileInput.files[0];
      }
      return activeFile;
    }

    function setInspectStatus(kind, message, withSpinner) {
      if (!inspectStatusEl) return;
      if (!message) {
        inspectStatusEl.hidden = true;
        inspectStatusEl.textContent = '';
        inspectStatusEl.className = 'inline-notice backup-inspect-status';
        return;
      }
      inspectStatusEl.hidden = false;
      inspectStatusEl.className = 'inline-notice backup-inspect-status';
      if (kind === 'warn') inspectStatusEl.classList.add('flash-banner--warn');
      else if (kind === 'error') inspectStatusEl.classList.add('flash-banner--error');
      else if (kind === 'ok') inspectStatusEl.classList.add('flash-banner--ok');
      else inspectStatusEl.classList.add('backup-inspect-status--info');

      inspectStatusEl.innerHTML = '';
      var p = document.createElement('p');
      if (withSpinner) {
        var sp = document.createElement('span');
        sp.className = 'spinner spinner--sm';
        sp.setAttribute('aria-hidden', 'true');
        p.appendChild(sp);
        p.appendChild(document.createTextNode(' '));
      }
      p.appendChild(document.createTextNode(message));
      inspectStatusEl.appendChild(p);
    }

    function hidePreview() {
      if (inspectPreviewEl) inspectPreviewEl.hidden = true;
    }

    function renderPreview(data) {
      if (!inspectPreviewEl) return;
      var encBadge = inspectPreviewEl.querySelector('[data-preview-encrypted]');
      var secBadge = inspectPreviewEl.querySelector('[data-preview-secrets-badge]');
      var secWarn = inspectPreviewEl.querySelector('[data-preview-secrets-warning]');
      var versionEl = inspectPreviewEl.querySelector('[data-preview-version]');
      var driverEl = inspectPreviewEl.querySelector('[data-preview-driver]');
      var createdEl = inspectPreviewEl.querySelector('[data-preview-created]');

      if (encBadge) {
        encBadge.textContent = data.encrypted ? t('js.backup.encrypted_badge') : t('js.backup.plain_badge');
        encBadge.className = 'status-pill ' + (data.encrypted ? 'status-pill--ok' : 'status-pill--idle');
      }
      if (secBadge) {
        secBadge.textContent = data.includes_secrets ? t('js.backup.secrets_included_badge') : t('js.backup.secrets_redacted_badge');
        secBadge.className = 'status-pill ' + (data.includes_secrets ? 'status-pill--ok' : 'status-pill--warn');
      }
      if (secWarn) {
        secWarn.hidden = Boolean(data.includes_secrets);
      }
      if (versionEl) versionEl.textContent = data.app_version || '—';
      if (driverEl) driverEl.textContent = data.source_driver || '—';
      if (createdEl) {
        var rawDate = data.created_at || '';
        createdEl.textContent = rawDate ? rawDate.replace('T', ' ').replace('Z', ' UTC') : '—';
      }

      var counts = data.counts || {};
      var setCount = function (key, val) {
        var node = inspectPreviewEl.querySelector('[data-preview-count="' + key + '"]');
        if (node) node.textContent = String(val != null ? val : 0);
      };
      setCount('servers', counts.servers);
      setCount('server_tags', counts.server_tags);
      setCount('limits', (counts.server_traffic_limits || 0) + (counts.traffic_limit_rules || 0));
      setCount('alert_channels', counts.alert_channels);
      setCount('alert_rules', counts.alert_rules);
      setCount('alert_silences', counts.alert_silences);
      setCount('install_metadata', counts.install_metadata);
      setCount('env_keys', counts.env_keys);

      setInspectStatus('ok', t('js.backup.ready_to_restore', { version: data.version || 1 }), false);
      inspectPreviewEl.hidden = false;
      renderIcons(inspectPreviewEl);
    }

    function updateControls() {
      var file = getSelectedFile();
      var hasFile = Boolean(file) && !fileTooLarge;
      if (inspectBtn) {
        inspectBtn.disabled = !hasFile;
      }
      if (submitBtn) {
        submitBtn.disabled = fileTooLarge || !hasFile;
      }
      if (confirmCheckbox && confirmCheckbox.checked && confirmErrorEl) {
        confirmErrorEl.hidden = true;
      }
    }

    function runInspect() {
      var file = getSelectedFile();
      if (!file || fileTooLarge) return;

      if (inspectController) {
        try { inspectController.abort(); } catch (err) { /* ignore */ }
      }
      inspectController = new AbortController();
      if (passFieldWrap) passFieldWrap.classList.remove('is-needs-passphrase');
      setInspectStatus('info', t('js.backup.inspecting'), true);

      var sendInspect = function (token, retried) {
        var fd = new FormData();
        fd.append('backup_file', file);
        fd.append('passphrase', passInput ? passInput.value : '');
        if (token) fd.append('_csrf_token', token);

        var headers = {};
        if (token) headers['X-CSRF-Token'] = token;

        return fetch('/ops/backup/inspect', {
          method: 'POST',
          credentials: 'same-origin',
          headers: headers,
          body: fd,
          signal: inspectController.signal
        }).then(function (resp) {
          var hdrToken = resp.headers && resp.headers.get('X-CSRF-Token');
          if (hdrToken && typeof window.nxSetCSRF === 'function') window.nxSetCSRF(hdrToken);
          if (resp.status === 403 && !retried) {
            return getFreshCSRFToken(form).then(function (fresh) { return sendInspect(fresh, true); });
          }
          return resp.json().catch(function () {
            return { ok: false, error: t('js.backup.inspect_failed') };
          });
        });
      };

      var initialToken = (typeof window.nxCSRF === 'function' ? window.nxCSRF(form) : '') ||
        ((form.querySelector('input[name="_csrf_token"]') || {}).value || '');
      sendInspect(initialToken, false).then(function (data) {
        if (!data || !data.ok) {
          hidePreview();
          var kind = (data && data.code === 'passphrase_required') ? 'warn' : 'error';
          if (passFieldWrap && data && (data.code === 'passphrase_required' || data.code === 'wrong_passphrase')) {
            passFieldWrap.classList.add('is-needs-passphrase');
          }
          setInspectStatus(kind, (data && data.error) || t('js.backup.inspect_failed'), false);
          return;
        }
        renderPreview(data);
      }).catch(function (err) {
        if (err && err.name === 'AbortError') return;
        hidePreview();
        setInspectStatus('error', t('js.backup.inspect_failed'), false);
      });
    }

    function clearSelection() {
      activeFile = null;
      fileTooLarge = false;
      if (fileInput) fileInput.value = '';
      if (dropzone) dropzone.classList.remove('has-file', 'is-error');
      if (dropEmpty) dropEmpty.hidden = false;
      if (dropSelected) dropSelected.hidden = true;
      if (fileErrorEl) fileErrorEl.hidden = true;
      if (passFieldWrap) passFieldWrap.classList.remove('is-needs-passphrase');
      setInspectStatus('', '', false);
      hidePreview();
      updateControls();
    }

    function handleFileChanged() {
      var file = getSelectedFile();
      if (!file) {
        clearSelection();
        return;
      }
      if (fileNameEl) fileNameEl.textContent = file.name;
      if (fileSizeEl) fileSizeEl.textContent = formatBytes(file.size);
      if (dropEmpty) dropEmpty.hidden = true;
      if (dropSelected) dropSelected.hidden = false;
      if (dropzone) dropzone.classList.add('has-file');

      if (file.size > MAX_BACKUP_BYTES) {
        fileTooLarge = true;
        if (dropzone) dropzone.classList.add('is-error');
        if (fileErrorEl) {
          fileErrorEl.textContent = t('js.backup.file_too_large', { size: formatBytes(file.size) });
          fileErrorEl.hidden = false;
        }
        hidePreview();
        setInspectStatus('', '', false);
        updateControls();
        return;
      }

      fileTooLarge = false;
      if (dropzone) dropzone.classList.remove('is-error');
      if (fileErrorEl) fileErrorEl.hidden = true;
      updateControls();
      runInspect();
    }

    if (fileInput) {
      fileInput.addEventListener('change', function () {
        activeFile = (fileInput.files && fileInput.files[0]) || null;
        handleFileChanged();
      });
    }

    if (fileClearBtn) {
      fileClearBtn.addEventListener('click', function (e) {
        e.preventDefault();
        e.stopPropagation();
        clearSelection();
      });
    }

    if (dropzone) {
      ['dragenter', 'dragover'].forEach(function (evName) {
        dropzone.addEventListener(evName, function (e) {
          e.preventDefault();
          dropzone.classList.add('is-dragover');
        });
      });
      ['dragleave', 'drop'].forEach(function (evName) {
        dropzone.addEventListener(evName, function (e) {
          e.preventDefault();
          dropzone.classList.remove('is-dragover');
        });
      });
      dropzone.addEventListener('drop', function (e) {
        var dropped = e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files[0];
        if (!dropped) return;
        activeFile = dropped;
        if (fileInput) {
          try {
            var dt = new DataTransfer();
            dt.items.add(dropped);
            fileInput.files = dt.files;
          } catch (err) { /* Safari/older fallback keeps activeFile */ }
        }
        handleFileChanged();
      });
    }

    if (inspectBtn) {
      inspectBtn.addEventListener('click', function () {
        runInspect();
      });
    }

    if (passInput) {
      passInput.addEventListener('input', function () {
        if (passFieldWrap) passFieldWrap.classList.remove('is-needs-passphrase');
      });
      passInput.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') {
          e.preventDefault();
          runInspect();
        }
      });
    }

    if (confirmCheckbox) {
      confirmCheckbox.addEventListener('change', updateControls);
    }

    form.addEventListener('submit', function (e) {
      var file = getSelectedFile();
      if (!file || fileTooLarge) {
        e.preventDefault();
        e.stopPropagation();
        if (fileInput) fileInput.focus();
        return;
      }
      if (confirmCheckbox && !confirmCheckbox.checked) {
        e.preventDefault();
        e.stopPropagation();
        if (confirmErrorEl) {
          confirmErrorEl.textContent = t('js.backup.confirm_required');
          confirmErrorEl.hidden = false;
        }
        confirmCheckbox.focus();
      }
    });

    updateControls();
  }

  function initSchedulerToggles(container) {
    container.querySelectorAll('[data-scheduler-toggle-form]').forEach(function (form) {
      if (form.dataset.toggleAjaxReady === '1') return;
      form.dataset.toggleAjaxReady = '1';

      form.addEventListener('submit', function (e) {
        e.preventDefault();
        e.stopPropagation();

        var btn = form.querySelector('button[type="submit"]');
        var actionsWrap = form.closest('.dashboard-item__actions');
        var pill = actionsWrap && actionsWrap.querySelector('[data-scheduler-status-pill]');
        if (btn) btn.disabled = true;

        var sendToggle = function (token, retried) {
          var body = new URLSearchParams();
          if (token) body.set('_csrf_token', token);
          var headers = {
            'Accept': 'application/json',
            'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8'
          };
          if (token) headers['X-CSRF-Token'] = token;

          return fetch(form.getAttribute('action') || form.action, {
            method: 'POST',
            credentials: 'same-origin',
            headers: headers,
            body: body.toString()
          }).then(function (resp) {
            var hdr = resp.headers && resp.headers.get('X-CSRF-Token');
            if (hdr && typeof window.nxSetCSRF === 'function') window.nxSetCSRF(hdr);
            if (resp.status === 403 && !retried) {
              return getFreshCSRFToken(form).then(function (fresh) { return sendToggle(fresh, true); });
            }
            return resp.json().catch(function () { return { ok: false }; });
          });
        };

        var initialToken = (typeof window.nxCSRF === 'function' ? window.nxCSRF(form) : '') ||
          ((form.querySelector('input[name="_csrf_token"]') || {}).value || '');

        sendToggle(initialToken, false).then(function (data) {
          if (!data || !data.ok) {
            if (typeof window.nxToast === 'function') window.nxToast((data && data.message) || t('js.failed'), 'error');
            return;
          }
          if (pill) {
            pill.textContent = data.status_label || (data.paused ? 'paused' : 'scheduled');
            pill.className = 'status-pill status-pill--' + (data.status_variant || (data.paused ? 'idle' : 'ok'));
          }
          if (btn) {
            btn.classList.toggle('btn--danger-ghost', !data.paused);
            if (data.button_title) btn.setAttribute('title', data.button_title);
            btn.textContent = '';
            var ic = document.createElement('i');
            ic.setAttribute('data-lucide', data.paused ? 'play' : 'pause');
            ic.className = 'icon-in-button';
            btn.appendChild(ic);
            btn.appendChild(document.createTextNode(' ' + (data.button_label || '')));
            renderIcons(btn);
          }
          if (typeof window.nxToast === 'function' && data.message) {
            window.nxToast(data.message, 'ok');
          }
        }).catch(function () {
          if (typeof window.nxToast === 'function') window.nxToast(t('js.failed'), 'error');
        }).finally(function () {
          if (btn) btn.disabled = false;
        });
      });
    });
  }

  function init() {
    var scope = paneScope || document;
    var container = scope.querySelector('[data-diagnostics-root]');
    if (!container) container = document.querySelector('[data-diagnostics-root]');
    if (!container || container.dataset.diagReady === '1') return;
    container.dataset.diagReady = '1';

    initPasswordToggles(container);
    initExportCard(container);
    initRestoreCard(container);
    initSchedulerToggles(container);
    renderIcons(container);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init, { once: true });
  } else {
    init();
  }
})();
