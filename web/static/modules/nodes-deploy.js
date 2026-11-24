/* Nodexia Node Deploy Wizard (lazy-loaded ES module — no bundler, no npm).
 *
 * Loaded on demand by modules/nodes.js when the user opens the Deploy modal
 * (or immediately if the modal renders pre-opened). Handles the 3 setup steps,
 * client-side validation, PEM bundle verification, and Step 3 live SSE install
 * streaming with requestAnimationFrame-batched log rendering.
 */
import { I18N, el, getCSRF, showToast, renderIcons, registerCleanup } from './core.js';
import { initSecrets, initCopyTargets } from './ui.js';
import { closeModal } from './modals.js';
import { iconEl, readJSON, buildFact, buildCredBlock } from './nodes.js';

var NODE_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$/;
var SAN_ENTRY_RE = /^[a-zA-Z0-9.*:-]+$/;
var MAX_INSTALL_LOG_BYTES = 512 * 1024;

function isRebecca(engine) {
  return engine === 'rebecca' || engine === 'rebecca-node';
}

function isValidBundle(val) {
  return val.indexOf('-----BEGIN CERTIFICATE-----') !== -1 && val.indexOf('KEY-----') !== -1;
}

function stageIcon(state) {
  if (state === 'done') return 'check-circle-2';
  if (state === 'failed') return 'x-circle';
  if (state === 'active') return 'loader';
  return 'circle';
}

function wireDeployWizard(modal, workspace, owningPane, onDeployed) {
  if (!modal) return null;
  if (modal._nxDeployCtrl) return modal._nxDeployCtrl;
  modal.dataset.deployWizardReady = '1';

  var q = function (sel) { return modal.querySelector(sel); };
  var form = q('[data-deploy-form]');
  var steps = modal.querySelectorAll('[data-step-indicator]');
  var panes = modal.querySelectorAll('[data-deploy-pane]');
  var btnBack = q('[data-deploy-back]');
  var btnNext = q('[data-deploy-next]');
  var btnSubmit = q('[data-deploy-submit]');
  var footerActions = q('[data-deploy-actions]');
  var statusEl = q('[data-deploy-status]');
  var bundleInput = q('[data-bundle-input]');
  var bundleState = q('[data-bundle-state]');
  var bundleStateText = q('[data-bundle-state-text]');
  var bundlePasteBtn = q('[data-bundle-paste]');
  var bundleClearBtn = q('[data-bundle-clear]');
  var overrideInput = q('[data-deploy-override]');
  var engineCards = modal.querySelectorAll('[data-engine-card]');

  var liveStagesEl = q('[data-deploy-stages]');
  var liveLogEl = q('[data-deploy-live-log]');
  var liveStatusBadge = q('[data-deploy-live-badge]');
  var liveStatusEl = q('[data-deploy-live-status]');
  var liveScrollBtn = q('[data-deploy-scroll-toggle]');
  var liveCancelBtn = q('[data-deploy-cancel-job]');
  var endOkCard = q('[data-deploy-end="ok"]');
  var endErrCard = q('[data-deploy-end="error"]');
  var endErrText = q('[data-deploy-end-error]');
  var retryBtn = q('[data-deploy-retry-btn]');
  var doneRefreshBtn = q('[data-deploy-done-refresh]');
  var regContainer = q('[data-deploy-reg-info]');

  var currentStep = 0;
  var isSubmitting = false;
  var activeInstallES = null;
  var activeCancelURL = '';
  var activeStatusURL = '';
  var currentStageKey = 'initializing';
  var livePausedScroll = false;
  var liveLogTrack = { received: 0, connOffset: 0, text: '' };
  var logFlushRaf = 0;

  function flushInstallLog() {
    logFlushRaf = 0;
    if (!liveLogEl) return;
    liveLogEl.textContent = liveLogTrack.text;
    if (!livePausedScroll) liveLogEl.scrollTop = liveLogEl.scrollHeight;
  }

  function stopInstallStream() {
    if (logFlushRaf) {
      cancelAnimationFrame(logFlushRaf);
      flushInstallLog();
    }
    if (activeInstallES) {
      try { activeInstallES.close(); } catch (err) {}
      activeInstallES = null;
    }
  }

  registerCleanup(owningPane || document, stopInstallStream);
  modal.addEventListener('modal:beforeclose', stopInstallStream);

  function getSelectedEngine() {
    var checked = q('input[name="node_type"]:checked');
    return checked ? checked.value : '';
  }

  function clearFieldErrors() {
    modal.querySelectorAll('[data-field-error]').forEach(function (errEl) {
      errEl.textContent = '';
      errEl.hidden = true;
    });
    if (statusEl) statusEl.textContent = '';
  }

  function showFieldError(fieldName, msg) {
    var errEl = q('[data-field-error="' + fieldName + '"]');
    if (errEl) {
      errEl.textContent = msg;
      errEl.hidden = false;
    }
    if (statusEl && !statusEl.textContent) statusEl.textContent = msg;
  }

  function setBundleIcon(iconName) {
    if (!bundleState || bundleState.dataset.iconState === iconName) return;
    bundleState.dataset.iconState = iconName;
    var cur = bundleState.querySelector('.deploy-status__icon');
    if (cur) {
      cur.replaceWith(iconEl(iconName, 'deploy-status__icon'));
      renderIcons(bundleState);
    }
  }

  function validateBundle() {
    if (!bundleInput || !bundleState) return;
    var val = bundleInput.value.trim();
    if (bundleClearBtn) bundleClearBtn.hidden = !val;
    var state = !val ? 'idle' : (isValidBundle(val) ? 'ok' : 'bad');
    var icons = { idle: 'circle-alert', ok: 'check-circle-2', bad: 'alert-triangle' };
    var keys = { idle: 'nodes.bundle_waiting', ok: 'nodes.bundle_valid', bad: 'nodes.bundle_invalid' };
    bundleState.className = 'deploy-status is-' + state;
    setBundleIcon(icons[state]);
    if (bundleStateText) bundleStateText.textContent = I18N.t(keys[state]);
  }

  function syncEngineUI(prevEngine) {
    var engine = getSelectedEngine();
    var rebecca = isRebecca(engine);
    var kind = rebecca ? 'binary' : 'docker';
    modal.setAttribute('data-selected-engine', engine);

    engineCards.forEach(function (card) {
      var radio = card.querySelector('input[type="radio"]');
      card.classList.toggle('is-selected', !!(radio && radio.checked));
    });

    modal.querySelectorAll('[data-engine-group]').forEach(function (grp) {
      var active = grp.getAttribute('data-engine-group') === kind;
      grp.hidden = !active;
      grp.querySelectorAll('input, textarea, select').forEach(function (inp) { inp.disabled = !active; });
    });

    var protoRow = q('[data-review-row="protocol"]');
    if (protoRow) protoRow.hidden = rebecca;
    var bundleRow = q('[data-review-row="bundle"]');
    if (bundleRow) bundleRow.hidden = !rebecca;

    modal.querySelectorAll('[data-install-note]').forEach(function (n) {
      n.hidden = n.getAttribute('data-install-note') !== kind;
    });

    var nameInput = form ? form.elements['node_name'] : null;
    if (nameInput) {
      var selCard = q('[data-engine-card="' + engine + '"]');
      var targetDef = (selCard && selCard.getAttribute('data-default-name')) || (rebecca ? 'rebecca-node' : 'pg-node');
      var prevCard = prevEngine ? q('[data-engine-card="' + prevEngine + '"]') : null;
      var prevDef = (prevCard && prevCard.getAttribute('data-default-name')) ||
        (prevEngine ? (isRebecca(prevEngine) ? 'rebecca-node' : 'pg-node') : (rebecca ? 'pg-node' : 'rebecca-node'));
      var cur = nameInput.value.trim();
      if (!cur || cur === 'node' || cur === prevDef || cur === (rebecca ? 'pg-node' : 'rebecca-node')) {
        nameInput.value = targetDef;
      }
      nameInput.placeholder = targetDef;
    }

    var apiPortInput = form ? form.elements['api_port'] : null;
    var reqBadge = q('[data-api-port-required]');
    var optBadge = q('[data-api-port-optional]');
    if (apiPortInput) {
      if (rebecca) {
        if (!apiPortInput.value.trim()) {
          apiPortInput.value = '62051';
          apiPortInput.dataset.autoseeded = '1';
        }
        apiPortInput.required = true;
      } else {
        if (apiPortInput.dataset.autoseeded === '1') {
          apiPortInput.value = '';
          delete apiPortInput.dataset.autoseeded;
        }
        apiPortInput.required = false;
      }
      apiPortInput.placeholder = '62051';
      if (reqBadge) reqBadge.hidden = !rebecca;
      if (optBadge) optBadge.hidden = rebecca;
    }
    if (bundleInput) bundleInput.required = rebecca;
    validateBundle();
  }

  function updateReview() {
    var engine = getSelectedEngine();
    var rebecca = isRebecca(engine);
    var fieldVal = function (n) { return (form && form.elements[n] ? form.elements[n].value : '').trim(); };
    var setReview = function (key, val) {
      var target = q('[data-review="' + key + '"]');
      if (target) target.textContent = val || '—';
    };

    var selCard = q('[data-engine-card="' + engine + '"]');
    var engineLabel = selCard ? (selCard.querySelector('.deploy-engine__label') || {}).textContent : '';
    var engineKind = selCard ? (selCard.querySelector('.deploy-engine__kind') || {}).textContent : '';
    var channel = q('input[name="channel"]:checked');
    var proto = q('input[name="protocol"]:checked');
    var chanLabel = (channel && channel.closest('label'))
      ? ((channel.closest('label').querySelector('.deploy-channel__title') || {}).textContent || channel.value)
      : (channel ? channel.value : 'Stable');

    setReview('engine', engineLabel || (rebecca ? 'Rebecca Node' : 'PasarGuard Node'));
    setReview('engine_kind', engineKind || (rebecca ? 'Binary' : 'Docker'));

    var dockerIcon = q('[data-review-icon="docker"]');
    var binaryIcon = q('[data-review-icon="binary"]');
    if (dockerIcon) dockerIcon.hidden = rebecca;
    if (binaryIcon) binaryIcon.hidden = !rebecca;

    var sPort = fieldVal('service_port');
    var aPort = fieldVal('api_port');
    setReview('node_name', fieldVal('node_name') || (rebecca ? 'rebecca-node' : 'pg-node'));
    setReview('service_port', sPort ? ':' + sPort : '—');
    setReview('api_port', aPort ? ':' + aPort : '—');
    setReview('channel', (chanLabel || '').trim().split('\n')[0]);
    setReview('protocol', proto ? proto.value.toUpperCase() : 'gRPC');
    setReview('override', (overrideInput && overrideInput.checked)
      ? I18N.t('nodes.deploy_manifest_override_yes')
      : I18N.t('nodes.deploy_manifest_override_no'));
    setReview('bundle', isValidBundle(bundleInput ? bundleInput.value.trim() : '') ? I18N.t('nodes.bundle_valid') : '—');
  }

  function setStep(idx) {
    currentStep = Math.max(0, Math.min(3, idx));
    modal.setAttribute('data-step', String(currentStep));
    var modalBody = q('.modal__body');
    if (modalBody) { modalBody.scrollTop = 0; modalBody.scrollLeft = 0; }
    clearFieldErrors();

    var isLive = currentStep === 3;
    steps.forEach(function (s, i) {
      var isActive = (i === currentStep) || (isLive && i === 2);
      s.classList.toggle('is-active', isActive && !isLive);
      s.classList.toggle('is-done', i < currentStep || isLive);
      if (isActive && !isLive) s.setAttribute('aria-current', 'step');
      else s.removeAttribute('aria-current');
      var hit = s.querySelector('.deploy-step__hit');
      if (hit) hit.disabled = isLive || (i > currentStep);
    });

    panes.forEach(function (p, i) {
      p.hidden = (i !== currentStep);
      p.classList.toggle('is-active', i === currentStep);
    });

    if (footerActions) footerActions.hidden = isLive;
    if (btnBack) btnBack.hidden = (currentStep === 0 || isLive);
    if (btnNext) btnNext.hidden = (currentStep !== 0 && currentStep !== 1);
    if (btnSubmit) btnSubmit.hidden = (currentStep !== 2);

    if (currentStep === 1 && panes[1]) {
      var focusInput = panes[1].querySelector('input[name="node_name"]');
      if (focusInput) setTimeout(function () { focusInput.focus(); }, 40);
    } else if (currentStep === 2) {
      updateReview();
      if (btnSubmit) setTimeout(function () { btnSubmit.focus(); }, 40);
    }
    renderIcons(modal);
  }

  function validateStep1() {
    var rebecca = isRebecca(getSelectedEngine());
    var nameField = form && form.elements['node_name'];
    if (nameField) {
      var nameVal = nameField.value.trim();
      if (!nameVal || !NODE_NAME_RE.test(nameVal)) {
        showFieldError('node_name', I18N.t('nodes.err_node_name'));
        nameField.focus();
        return false;
      }
    }

    var sPortField = form && form.elements['service_port'];
    var sVal = parseInt(sPortField ? sPortField.value.trim() : '', 10);
    if (!sPortField || !sVal || isNaN(sVal) || sVal < 1 || sVal > 65535) {
      showFieldError('service_port', I18N.t('nodes.err_service_port'));
      if (sPortField) sPortField.focus();
      return false;
    }

    var aPortField = form && form.elements['api_port'];
    if (aPortField) {
      var aRaw = aPortField.value.trim();
      if (rebecca && !aRaw) { aPortField.value = '62051'; aRaw = '62051'; }
      if (aRaw) {
        var aVal = parseInt(aRaw, 10);
        if (!aVal || isNaN(aVal) || aVal < 1 || aVal > 65535) {
          showFieldError('api_port', I18N.t('nodes.err_api_port'));
          aPortField.focus();
          return false;
        }
        if (aVal === sVal) {
          showFieldError('api_port', I18N.t('nodes.err_ports_equal'));
          aPortField.focus();
          return false;
        }
      }
    }

    var chan = q('input[name="channel"]:checked');
    if (chan && chan.disabled) {
      showFieldError('channel', I18N.t('nodes.err_channel_disabled'));
      return false;
    }

    if (!rebecca && form && form.elements['san_entries']) {
      var sanRaw = form.elements['san_entries'].value.trim();
      if (sanRaw) {
        var parts = sanRaw.split(',');
        for (var i = 0; i < parts.length; i++) {
          var item = parts[i].trim();
          if (!item || !SAN_ENTRY_RE.test(item)) {
            showFieldError('san_entries', I18N.t('nodes.err_san_entries'));
            form.elements['san_entries'].focus();
            return false;
          }
        }
      }
    }

    if (rebecca && !isValidBundle((bundleInput ? bundleInput.value : '').trim())) {
      showFieldError('bundle', I18N.t('nodes.bundle_invalid'));
      validateBundle();
      if (bundleInput) bundleInput.focus();
      return false;
    }
    return true;
  }

  function renderInstallStages(stages, activeStageKey) {
    if (!liveStagesEl || !Array.isArray(stages)) return;
    liveStagesEl.textContent = '';
    var frag = document.createDocumentFragment();
    stages.forEach(function (st) {
      var state = st.State || st.state || 'pending';
      var key = st.Key || st.key || '';
      if (activeStageKey && key === activeStageKey && state === 'pending') state = 'active';
      frag.appendChild(el('div', { class: 'deploy-stage-pill is-' + state, 'data-stage-key': key }, [
        iconEl(stageIcon(state)),
        el('span', { text: st.Label || st.label || key })
      ]));
    });
    liveStagesEl.appendChild(frag);
    renderIcons(liveStagesEl);
  }

  function setStageStateByKey(activeKey, allDone, failed) {
    if (!liveStagesEl) return;
    var seenActive = false;
    liveStagesEl.querySelectorAll('[data-stage-key]').forEach(function (pill) {
      var k = pill.getAttribute('data-stage-key');
      var nextState = 'pending';
      if (allDone) nextState = 'done';
      else if (failed && k === activeKey) { nextState = 'failed'; seenActive = true; }
      else if (k === activeKey) { nextState = 'active'; seenActive = true; }
      else if (!seenActive) nextState = 'done';

      pill.className = 'deploy-stage-pill is-' + nextState;
      var labelSpan = pill.querySelector('span');
      var labelText = labelSpan ? labelSpan.textContent : k;
      pill.textContent = '';
      pill.appendChild(iconEl(stageIcon(nextState)));
      pill.appendChild(el('span', { text: labelText }));
    });
    renderIcons(liveStagesEl);
  }

  function appendInstallLog(chunk) {
    if (!liveLogEl || !chunk) return;
    var start = liveLogTrack.connOffset;
    liveLogTrack.connOffset += chunk.length;
    if (liveLogTrack.connOffset <= liveLogTrack.received) return;
    var unseen = start < liveLogTrack.received ? chunk.slice(liveLogTrack.received - start) : chunk;
    if (!unseen) return;
    liveLogTrack.received += unseen.length;
    liveLogTrack.text += unseen;
    if (liveLogTrack.text.length > MAX_INSTALL_LOG_BYTES) {
      liveLogTrack.text = liveLogTrack.text.slice(liveLogTrack.text.length - MAX_INSTALL_LOG_BYTES);
    }
    if (!logFlushRaf) logFlushRaf = requestAnimationFrame(flushInstallLog);
  }

  function renderInstallRegistration(info) {
    if (!regContainer) return;
    regContainer.textContent = '';
    if (!info || !info.Available) return;

    regContainer.appendChild(el('div', { class: 'node-facts' }, [
      buildFact('box', I18N.t('nodes.node_name'), info.NodeName || '', 'node-port'),
      buildFact('globe', I18N.t('nodes.node_ip'), info.NodeIP || '', 'node-port'),
      buildFact('plug', I18N.t('nodes.service_port'), info.ServicePort || '', 'node-port node-port--service'),
      buildFact('layers', I18N.t('nodes.protocol'), info.Protocol || '', '')
    ]));

    if (info.APIKey) {
      regContainer.appendChild(buildCredBlock('key-round', I18N.t('nodes.api_key'), 'deploy-reg-api-key', info.APIKey, true));
    }
    if (info.Certificate) {
      regContainer.appendChild(buildCredBlock('shield-check', I18N.t('nodes.ssl_cert'), 'deploy-reg-cert', info.Certificate, false));
    } else {
      regContainer.appendChild(el('p', { class: 'empty-state', text: I18N.t('nodes.cert_missing', { name: info.NodeName || '' }) }));
    }
    initSecrets(regContainer);
    initCopyTargets(regContainer);
    renderIcons(regContainer);
  }

  function setLiveEndState(ok, message) {
    stopInstallStream();
    if (liveCancelBtn) liveCancelBtn.hidden = true;
    if (liveStatusBadge) {
      liveStatusBadge.classList.add('is-ended', ok ? 'is-ok' : 'is-error');
      liveStatusBadge.classList.remove(ok ? 'is-error' : 'is-ok');
    }
    if (liveStatusEl && message) liveStatusEl.textContent = message;
    if (ok) {
      if (endOkCard) endOkCard.hidden = false;
    } else {
      if (endErrText && message) endErrText.textContent = message;
      if (endErrCard) endErrCard.hidden = false;
    }
  }

  function beginLiveInstall(data) {
    stopInstallStream();
    activeCancelURL = data.cancel_url || '';
    activeStatusURL = data.status_url || '';
    currentStageKey = 'initializing';
    livePausedScroll = false;
    liveLogTrack = { received: 0, connOffset: 0, text: '' };
    if (liveLogEl) liveLogEl.textContent = '';
    if (endOkCard) endOkCard.hidden = true;
    if (endErrCard) endErrCard.hidden = true;
    if (regContainer) regContainer.textContent = '';
    if (liveCancelBtn) {
      liveCancelBtn.hidden = false;
      liveCancelBtn.disabled = false;
      liveCancelBtn.textContent = I18N.t('nodes.deploy_cancel_job');
    }
    if (liveScrollBtn) {
      liveScrollBtn.textContent = I18N.t('nodes.deploy_pause_scroll');
      liveScrollBtn.setAttribute('aria-pressed', 'false');
    }
    if (liveStatusBadge) liveStatusBadge.classList.remove('is-ended', 'is-ok', 'is-error');
    if (liveStatusEl) liveStatusEl.textContent = I18N.t('nodes.deploy_starting');

    renderInstallStages(data.stages || [], 'initializing');
    setStep(3);

    activeInstallES = new EventSource(data.events_url);
    activeInstallES.addEventListener('open', function () { liveLogTrack.connOffset = 0; });
    activeInstallES.addEventListener('output', function (e) { appendInstallLog(e.data || ''); });

    activeInstallES.addEventListener('stage', function (e) {
      try {
        var payload = JSON.parse(e.data || '{}');
        if (payload.stage) {
          currentStageKey = payload.stage;
          setStageStateByKey(payload.stage, payload.stage === 'ready', false);
          if (liveStatusEl) liveStatusEl.textContent = I18N.t('nodes.stage_' + payload.stage) || payload.stage;
        }
      } catch (err) {}
    });

    activeInstallES.addEventListener('done', function () {
      setLiveEndState(true, I18N.t('nodes.deploy_success_title'));
      setStageStateByKey('ready', true, false);
      if (activeStatusURL) {
        fetch(activeStatusURL, { headers: { Accept: 'application/json' } })
          .then(readJSON)
          .then(function (res) {
            if (res.ok && res.data && res.data.install && res.data.install.Info) {
              renderInstallRegistration(res.data.install.Info);
            }
          }).catch(function () {});
      }
      if (typeof onDeployed === 'function') onDeployed();
    });

    activeInstallES.addEventListener('error', function (e) {
      if (e && e.data) {
        var payload = {};
        try { payload = JSON.parse(e.data); } catch (err) {}
        var isCanceled = payload.status === 'canceled';
        var msg = payload.message || (isCanceled ? I18N.t('nodes.deploy_canceled') : I18N.t('nodes.deploy_failed_title'));
        setLiveEndState(false, msg);
        setStageStateByKey(currentStageKey || 'initializing', false, true);
        return;
      }
      if (activeInstallES && activeInstallES.readyState === EventSource.CLOSED) {
        setLiveEndState(false, I18N.t('js.bulk.stream_error'));
      }
    });
  }

  if (liveScrollBtn) {
    liveScrollBtn.addEventListener('click', function () {
      livePausedScroll = !livePausedScroll;
      liveScrollBtn.setAttribute('aria-pressed', livePausedScroll ? 'true' : 'false');
      liveScrollBtn.textContent = livePausedScroll
        ? I18N.t('nodes.deploy_resume_scroll')
        : I18N.t('nodes.deploy_pause_scroll');
      if (!livePausedScroll && liveLogEl) liveLogEl.scrollTop = liveLogEl.scrollHeight;
    });
  }

  if (liveCancelBtn) {
    liveCancelBtn.addEventListener('click', function () {
      if (!activeCancelURL || liveCancelBtn.disabled) return;
      liveCancelBtn.disabled = true;
      liveCancelBtn.textContent = I18N.t('nodes.deploy_canceling');
      var csrf = getCSRF(workspace);
      var headers = { Accept: 'application/json', 'X-Requested-With': 'fetch' };
      if (csrf) headers['X-CSRF-Token'] = csrf;
      fetch(activeCancelURL, { method: 'POST', headers: headers })
        .then(readJSON)
        .then(function (res) {
          if (!res.ok && liveStatusEl) {
            liveStatusEl.textContent = (res.data && res.data.error) || I18N.t('nodes.action_failed');
          }
        })
        .catch(function () {
          liveCancelBtn.disabled = false;
          liveCancelBtn.textContent = I18N.t('nodes.deploy_cancel_job');
        });
    });
  }

  if (retryBtn) retryBtn.addEventListener('click', function () { setStep(1); });
  if (doneRefreshBtn) {
    doneRefreshBtn.addEventListener('click', function () {
      closeModal(modal, true);
      if (typeof onDeployed === 'function') onDeployed();
    });
  }

  steps.forEach(function (stepEl, idx) {
    var hit = stepEl.querySelector('.deploy-step__hit');
    (hit || stepEl).addEventListener('click', function (e) {
      e.preventDefault();
      if (currentStep < 3 && idx < currentStep) setStep(idx);
    });
  });

  engineCards.forEach(function (card) {
    var radio = card.querySelector('input[type="radio"]');
    card.addEventListener('click', function () {
      if (!radio) return;
      var prev = getSelectedEngine();
      radio.checked = true;
      syncEngineUI(prev);
    });
    if (radio) radio.addEventListener('change', function () { syncEngineUI(); });
  });

  if (btnNext) {
    btnNext.addEventListener('click', function (e) {
      e.preventDefault();
      clearFieldErrors();
      if (currentStep === 0) {
        if (!getSelectedEngine()) { showFieldError('engine', I18N.t('nodes.err_engine_required')); return; }
        setStep(1);
      } else if (currentStep === 1 && validateStep1()) {
        setStep(2);
      }
    });
  }

  if (btnBack) {
    btnBack.addEventListener('click', function (e) {
      e.preventDefault();
      clearFieldErrors();
      setStep(currentStep - 1);
    });
  }

  if (form) {
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      if (isSubmitting) return;
      if (!validateStep1()) { setStep(1); return; }

      isSubmitting = true;
      if (btnSubmit) btnSubmit.disabled = true;
      clearFieldErrors();

      var csrf = getCSRF(workspace);
      var params = new URLSearchParams();
      if (csrf) params.set('_csrf_token', csrf);
      new FormData(form).forEach(function (val, key) {
        if (key !== '_csrf_token') params.set(key, val);
      });

      var headers = {
        Accept: 'application/json',
        'X-Requested-With': 'fetch',
        'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8'
      };
      if (csrf) headers['X-CSRF-Token'] = csrf;

      fetch(form.action, { method: 'POST', headers: headers, body: params.toString() })
        .then(readJSON)
        .then(function (res) {
          isSubmitting = false;
          if (btnSubmit) btnSubmit.disabled = false;

          if (!res.ok || !res.data || !res.data.ok) {
            var errs = (res.data && res.data.errors) || {};
            setStep(errs.node_type || errs.engine ? 0 : 1);
            Object.keys(errs).forEach(function (k) { showFieldError(k, errs[k]); });
            if (errs.node_name && overrideInput) {
              var ovBox = q('[data-deploy-override-wrap]');
              if (ovBox) ovBox.hidden = false;
            }
            if (!Object.keys(errs).length) {
              var msg = (res.data && res.data.error) || I18N.t('nodes.deploy_failed_title');
              if (statusEl) statusEl.textContent = msg;
              showToast(msg, 'error');
            }
            return;
          }
          beginLiveInstall(res.data);
        })
        .catch(function () {
          isSubmitting = false;
          if (btnSubmit) btnSubmit.disabled = false;
          var msg = I18N.t('nodes.deploy_failed_title');
          if (statusEl) statusEl.textContent = msg;
          showToast(msg, 'error');
        });
    });
  }

  if (bundleInput) bundleInput.addEventListener('input', validateBundle);
  if (bundleClearBtn && bundleInput) {
    bundleClearBtn.addEventListener('click', function (e) {
      e.preventDefault();
      bundleInput.value = '';
      bundleInput.dispatchEvent(new Event('input', { bubbles: true }));
      bundleInput.focus();
    });
  }
  if (bundlePasteBtn && bundleInput) {
    bundlePasteBtn.addEventListener('click', function (e) {
      e.preventDefault();
      if (navigator.clipboard && navigator.clipboard.readText) {
        navigator.clipboard.readText().then(function (text) {
          if (text) {
            bundleInput.value = text;
            bundleInput.dispatchEvent(new Event('input', { bubbles: true }));
          }
          bundleInput.focus();
        }).catch(function () { bundleInput.focus(); });
      } else {
        bundleInput.focus();
      }
    });
  }

  if (panes[1]) {
    panes[1].querySelectorAll('input').forEach(function (input) {
      input.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') { e.preventDefault(); if (btnNext) btnNext.click(); }
      });
    });
  }

  syncEngineUI();
  setStep((modal.classList.contains('is-open') || modal.hasAttribute('data-modal-open-initial')) ? 1 : 0);

  modal._nxDeployCtrl = {
    openDefault: function () {
      if (currentStep !== 3 || !activeInstallES) setStep(0);
    },
    preselectEngine: function (eng) {
      var prev = getSelectedEngine();
      var r = q('input[name="node_type"][value="' + eng + '"]');
      if (r) {
        r.checked = true;
        syncEngineUI(prev);
        setStep(1);
      }
    }
  };
  return modal._nxDeployCtrl;
}

export { wireDeployWizard };
