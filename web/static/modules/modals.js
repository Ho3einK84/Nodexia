/* Nodexia dialogs, action menus & the deploy wizard (native ES module — no
 * bundler, no npm).
 *
 * The interactive surfaces that sit above a page rather than inside it: the
 * on-demand PasarGuard node credentials panel (live SSH fetch, masked +
 * copyable), the server/node card overflow action menus, the frosted-backdrop
 * modal dialogs (open/close, focus trap, Escape) and the unified deploy-node
 * wizard with its three-step setup → details → review flow. Extracted from
 * app.js; the shared primitives come from ./core.js and the masked-secret /
 * copy widgets from ./ui.js.
 *
 * Every init* function is progressive enhancement and idempotent: absent
 * targets no-op, and each re-run is guarded by a data-*Ready flag, so
 * window.NodexiaApp.rescan() can re-apply them to a freshly injected tab pane.
 */
import { I18N, prefersReducedMotion, renderIcons } from './core.js';
import { initSecrets, initCopyTargets } from './ui.js';

/* ── On-demand PasarGuard node credentials (API key + certificate) ────────
   Fetches the secrets live over SSH and renders masked, copyable blocks that
   reuse initSecrets (masking) and initCopyTargets (copy). Nothing is stored. */
function initNodeCredentials(root) {
  root = root || document;
  root.querySelectorAll('[data-node-credentials]').forEach(function (btn) {
    if (btn.dataset.credReady === '1') return;
    btn.dataset.credReady = '1';
    var out = btn.parentNode.querySelector('.node-credentials__out');
    var showLabel = '<i data-lucide="key-round" class="icon-in-button"></i> ' + I18N.t('js.cred.show');
    btn.addEventListener('click', function () {
      if (out && !out.hidden) {
        out.hidden = true;
        out.innerHTML = '';
        btn.innerHTML = showLabel;
        renderIcons();
        return;
      }
      btn.disabled = true;
      btn.innerHTML = '<i data-lucide="loader"></i> ' + I18N.t('js.loading');
      renderIcons();
      fetch(btn.getAttribute('data-url'), { headers: { Accept: 'application/json' } })
        .then(function (resp) {
          return resp.json().then(function (data) { return { ok: resp.ok, data: data }; });
        })
        .then(function (res) {
          btn.disabled = false;
          if (!res.ok) {
            showCredError(out, btn, showLabel, (res.data && res.data.error) || I18N.t('js.cred.load_error'));
            return;
          }
          renderNodeCredentials(out, btn, res.data);
        })
        .catch(function () {
          btn.disabled = false;
          showCredError(out, btn, showLabel, I18N.t('js.cred.load_error'));
        });
    });
  });
}

function showCredError(out, btn, showLabel, message) {
  if (out) {
    out.hidden = false;
    out.innerHTML = '';
    var p = document.createElement('p');
    p.className = 'node-actions-note';
    p.textContent = message;
    out.appendChild(p);
  }
  btn.innerHTML = showLabel;
  renderIcons();
}

function renderNodeCredentials(out, btn, data) {
  if (!out) return;
  out.innerHTML = '';
  out.hidden = false;
  var name = String(data.node_name || 'node').replace(/[^a-zA-Z0-9._-]/g, '') || 'node';

  if (data.has_api_key) {
    out.appendChild(credentialBlock('key-round', I18N.t('js.cred.api_key'), 'cred-key-' + name, data.api_key, true));
  } else {
    var miss = document.createElement('p');
    miss.className = 'node-actions-note';
    miss.textContent = I18N.t('js.cred.api_key_missing', { name: name });
    out.appendChild(miss);
  }
  if (data.has_cert) {
    out.appendChild(credentialBlock('shield-check', I18N.t('js.cred.ssl_cert'), 'cred-cert-' + name, data.certificate, false));
  }

  // Wire masking + copy on the freshly inserted blocks.
  initSecrets();
  initCopyTargets();
  btn.innerHTML = '<i data-lucide="eye-off" class="icon-in-button"></i> ' + I18N.t('js.cred.hide');
  renderIcons();
}

function credentialBlock(icon, label, id, value, mask) {
  var section = document.createElement('div');
  section.className = 'node-section node-cred-block';

  var head = document.createElement('div');
  head.className = 'node-section__head';
  var lab = document.createElement('span');
  lab.className = 'node-section__label';
  lab.innerHTML = '<i data-lucide="' + icon + '"></i> ' + label;
  var copyBtn = document.createElement('button');
  copyBtn.type = 'button';
  copyBtn.className = 'btn btn--ghost btn--sm copy-inline';
  copyBtn.setAttribute('data-copy-target', '#' + id);
  copyBtn.setAttribute('aria-label', I18N.t('js.copy.aria_label', { label: label }));
  copyBtn.innerHTML = '<i data-lucide="copy"></i> ' + I18N.t('js.copy.label');
  head.appendChild(lab);
  head.appendChild(copyBtn);

  var pre = document.createElement('pre');
  pre.id = id;
  pre.className = 'output-block node-secret-block' + (mask ? '' : ' scrollable');
  pre.setAttribute('data-no-auto-copy', '');
  if (mask) pre.setAttribute('data-secret', '');
  pre.textContent = value; // full raw value — never truncated

  section.appendChild(head);
  section.appendChild(pre);
  return section;
}

/* ── Server card overflow action menu ──────────────────── */
var actionMenuDocWired = false;
var allActionMenus = [];
function initActionMenus(root) {
  root = root || document;
  var menus = root.querySelectorAll('[data-action-menu]');
  if (!menus.length) return;

  function setOpen(menu, open) {
    menu.classList.toggle('is-open', open);
    var toggle = menu.querySelector('.action-menu__toggle');
    if (toggle) toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
    var card = menu.closest('.server-card, .node-card');
    if (card) card.style.zIndex = open ? '30' : '';
  }
  function closeAll(except) {
    allActionMenus.forEach(function (menu) {
      if (menu !== except && menu.classList.contains('is-open')) setOpen(menu, false);
    });
  }

  menus.forEach(function (menu) {
    if (menu.dataset.actionMenuReady === '1') return;
    menu.dataset.actionMenuReady = '1';
    allActionMenus.push(menu);
    var toggle = menu.querySelector('.action-menu__toggle');
    if (!toggle) return;
    toggle.addEventListener('click', function (e) {
      e.preventDefault();
      e.stopPropagation();
      var willOpen = !menu.classList.contains('is-open');
      closeAll(menu);
      setOpen(menu, willOpen);
    });
  });

  if (actionMenuDocWired) return;
  actionMenuDocWired = true;
  document.addEventListener('click', function (e) {
    if (e.target.closest && e.target.closest('[data-action-menu]')) return;
    closeAll(null);
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') closeAll(null);
  });
  window.addEventListener('resize', function () { closeAll(null); });
}

/* ── Modal dialogs (frosted backdrop) ───────────────────── */
var modalDocWired = false;
var lastModalFocus = null;
function openModal(modal) {
  if (!modal) return;
  lastModalFocus = document.activeElement;
  modal.hidden = false;
  // Force reflow so the entrance transition runs from the hidden state.
  void modal.offsetWidth;
  modal.classList.add('is-open');
  document.body.classList.add('modal-open');
  var focusTarget = modal.querySelector('[data-modal-autofocus]') ||
    modal.querySelector('input:not([type="hidden"]), select, textarea, button');
  if (focusTarget && focusTarget.focus) {
    try { focusTarget.focus({ preventScroll: true }); } catch (err) { focusTarget.focus(); }
  }
}
function closeModal(modal) {
  if (!modal || !modal.classList.contains('is-open')) return;
  modal.classList.remove('is-open');
  document.body.classList.remove('modal-open');
  setTimeout(function () {
    if (!modal.classList.contains('is-open')) modal.hidden = true;
  }, prefersReducedMotion ? 0 : 240);
  if (lastModalFocus && lastModalFocus.focus) { try { lastModalFocus.focus(); } catch (err) {} }
}
function initModals(root) {
  root = root || document;
  var openers = root.querySelectorAll('[data-modal-open]');
  var modals = root.querySelectorAll('[data-modal]');
  if (!modals.length && !openers.length) return;

  openers.forEach(function (opener) {
    if (opener.dataset.modalOpenerReady === '1') return;
    opener.dataset.modalOpenerReady = '1';
    opener.addEventListener('click', function (e) {
      e.preventDefault();
      var id = opener.getAttribute('data-modal-open');
      var target = (id && root.querySelector ? root.querySelector('#' + id) : null) ||
        (id ? document.getElementById(id) : null);
      openModal(target);
    });
  });

  modals.forEach(function (modal) {
    if (modal.dataset.modalReady === '1') return;
    modal.dataset.modalReady = '1';
    modal.querySelectorAll('[data-modal-close]').forEach(function (btn) {
      btn.addEventListener('click', function (e) {
        e.preventDefault();
        closeModal(modal);
      });
    });
    // Basic focus trap: keep Tab within the dialog while it is open.
    modal.addEventListener('keydown', function (e) {
      if (e.key !== 'Tab' || !modal.classList.contains('is-open')) return;
      var focusables = modal.querySelectorAll(
        'a[href], button:not([disabled]), input:not([type="hidden"]), select, textarea, [tabindex]:not([tabindex="-1"])');
      if (!focusables.length) return;
      var first = focusables[0];
      var last = focusables[focusables.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    });
  });

  if (!modalDocWired) {
    modalDocWired = true;
    document.addEventListener('keydown', function (e) {
      if (e.key !== 'Escape') return;
      document.querySelectorAll('[data-modal].is-open').forEach(closeModal);
    });
  }

  // Re-open a modal the server asked to surface (e.g. install form had errors).
  var initial = root.querySelector('[data-modal][data-modal-open-initial]');
  if (initial) openModal(initial);
}

/* ── Unified Deploy Node Wizard ───────────────────────────── */
function initDeployModal(root) {
  root = root || document;
  root.querySelectorAll('[data-deploy-modal]').forEach(function (modal) {
    wireDeployModal(modal, root);
  });
}

function wireDeployModal(modal, root) {
  if (!modal || modal.dataset.deployWizardReady === '1') return;
  modal.dataset.deployWizardReady = '1';

  var form = modal.querySelector('[data-deploy-form]');
  var steps = modal.querySelectorAll('[data-step-indicator]');
  var panes = modal.querySelectorAll('[data-deploy-pane]');
  var btnBack = modal.querySelector('[data-deploy-back]');
  var btnNext = modal.querySelector('[data-deploy-next]');
  var btnSubmit = modal.querySelector('[data-deploy-submit]');
  var progressBar = modal.querySelector('[data-deploy-progress]');
  var statusEl = modal.querySelector('[data-deploy-status]');
  var bundleInput = modal.querySelector('[data-bundle-input]');
  var bundleState = modal.querySelector('[data-bundle-state]');
  var bundleStateText = modal.querySelector('[data-bundle-state-text]');
  var bundlePasteBtn = modal.querySelector('[data-bundle-paste]');
  var bundleClearBtn = modal.querySelector('[data-bundle-clear]');
  var engineCards = modal.querySelectorAll('[data-engine-card]');
  var currentStep = 0;

  function isRebecca(engine) {
    return engine === 'rebecca' || engine === 'rebecca-node';
  }

  function getSelectedEngine() {
    var checked = modal.querySelector('input[name="node_type"]:checked');
    return checked ? checked.value : '';
  }

  function clearFieldErrors() {
    modal.querySelectorAll('[data-field-error]').forEach(function (el) {
      el.textContent = '';
      el.hidden = true;
    });
    if (statusEl) statusEl.textContent = '';
  }

  function showFieldError(fieldName, msg) {
    var el = modal.querySelector('[data-field-error="' + fieldName + '"]');
    if (el) {
      el.textContent = msg;
      el.hidden = false;
    }
    if (statusEl && !statusEl.textContent) {
      statusEl.textContent = msg;
    }
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
      var isGrpActive = (grp.getAttribute('data-engine-group') === kind);
      grp.hidden = !isGrpActive;
      grp.querySelectorAll('input, textarea, select').forEach(function (el) {
        el.disabled = !isGrpActive;
      });
    });

    var protoRow = modal.querySelector('[data-review-row="protocol"]');
    if (protoRow) protoRow.hidden = rebecca;

    var bundleRow = modal.querySelector('[data-review-row="bundle"]');
    if (bundleRow) bundleRow.hidden = !rebecca;

    modal.querySelectorAll('[data-install-note]').forEach(function (el) {
      el.hidden = (el.getAttribute('data-install-note') !== kind);
    });

    var nameInput = form ? form.elements['node_name'] : null;
    if (nameInput) {
      var selectedCard = modal.querySelector('[data-engine-card="' + engine + '"]');
      var targetDefaultName = (selectedCard && selectedCard.getAttribute('data-default-name')) ||
        (rebecca ? 'rebecca-node' : 'pg-node');
      var prevCard = prevEngine ? modal.querySelector('[data-engine-card="' + prevEngine + '"]') : null;
      var prevDefaultName = (prevCard && prevCard.getAttribute('data-default-name')) ||
        (prevEngine ? (isRebecca(prevEngine) ? 'rebecca-node' : 'pg-node') : (rebecca ? 'pg-node' : 'rebecca-node'));
      var currentVal = nameInput.value.trim();

      if (!currentVal || currentVal === 'node' || currentVal === prevDefaultName || currentVal === (rebecca ? 'pg-node' : 'rebecca-node')) {
        nameInput.value = targetDefaultName;
      }
      nameInput.placeholder = targetDefaultName;
    }

    var apiPortInput = form ? form.elements['api_port'] : null;
    var reqBadge = modal.querySelector('[data-api-port-required]');
    var optBadge = modal.querySelector('[data-api-port-optional]');
    if (apiPortInput) {
      // Rebecca's node registers against the panel over the REST API, so the
      // API port is mandatory. PasarGuard's script can install without the
      // management service (`--no-install-service`), so its API port is
      // optional. Never rewrite a port the user typed: only seed the default
      // when the field is still empty.
      if (rebecca) {
        if (!apiPortInput.value.trim()) {
          apiPortInput.value = '62051';
          apiPortInput.dataset.autoseeded = '1';
        }
        apiPortInput.required = true;
        apiPortInput.placeholder = '62051';
        if (reqBadge) reqBadge.hidden = false;
        if (optBadge) optBadge.hidden = true;
      } else {
        // Drop only the value WE seeded; a port the user typed is kept.
        if (apiPortInput.dataset.autoseeded === '1') {
          apiPortInput.value = '';
          delete apiPortInput.dataset.autoseeded;
        }
        apiPortInput.required = false;
        apiPortInput.placeholder = '62051';
        if (reqBadge) reqBadge.hidden = true;
        if (optBadge) optBadge.hidden = false;
      }
    }

    // The bundle is Rebecca-only and is NOT nested under the API-port guard:
    // PasarGuard must never require it, Rebecca always must.
    if (bundleInput) bundleInput.required = rebecca;

    validateBundle();
  }

  function setBundleIcon(iconName) {
    if (!bundleState || bundleState.dataset.iconState === iconName) return;
    bundleState.dataset.iconState = iconName;
    var cur = bundleState.querySelector('.deploy-status__icon');
    if (cur) {
      var fresh = document.createElement('i');
      fresh.setAttribute('data-lucide', iconName);
      fresh.className = 'deploy-status__icon';
      fresh.setAttribute('aria-hidden', 'true');
      cur.replaceWith(fresh);
      renderIcons(bundleState);
    }
  }

  function validateBundle() {
    if (!bundleInput || !bundleState) return;
    var val = bundleInput.value.trim();
    if (bundleClearBtn) bundleClearBtn.hidden = !val;

    if (!val) {
      bundleState.className = 'deploy-status is-idle';
      setBundleIcon('circle-alert');
      if (bundleStateText) {
        bundleStateText.textContent = (I18N && I18N.t ? I18N.t('nodes.bundle_waiting') : '') || 'Waiting for registration bundle...';
      }
      return;
    }

    var hasCert = val.indexOf('-----BEGIN CERTIFICATE-----') !== -1;
    var hasKey = val.indexOf('KEY-----') !== -1;

    if (hasCert && hasKey) {
      bundleState.className = 'deploy-status is-ok';
      setBundleIcon('check-circle-2');
      if (bundleStateText) {
        bundleStateText.textContent = (I18N && I18N.t ? I18N.t('nodes.bundle_valid') : '') || 'Certificate and private key detected.';
      }
    } else {
      bundleState.className = 'deploy-status is-bad';
      setBundleIcon('alert-triangle');
      if (bundleStateText) {
        bundleStateText.textContent = (I18N && I18N.t ? I18N.t('nodes.bundle_invalid') : '') || 'Paste the certificate and its private key.';
      }
    }
  }

  function updateReview() {
    var engine = getSelectedEngine();
    var rebecca = isRebecca(engine);
    var name = (form && form.elements['node_name'] ? form.elements['node_name'].value : '').trim();
    var sPort = (form && form.elements['service_port'] ? form.elements['service_port'].value : '').trim();
    var aPort = (form && form.elements['api_port'] ? form.elements['api_port'].value : '').trim();
    var channel = modal.querySelector('input[name="channel"]:checked');
    var proto = modal.querySelector('input[name="protocol"]:checked');

    var setReview = function (key, val) {
      var el = modal.querySelector('[data-review="' + key + '"]');
      if (el) el.textContent = val || '—';
    };

    var selectedCard = modal.querySelector('[data-engine-card="' + engine + '"]');
    var engineLabel = selectedCard ? (selectedCard.querySelector('.deploy-engine__label') || {}).textContent : '';
    var engineKind = selectedCard ? (selectedCard.querySelector('.deploy-engine__kind') || {}).textContent : '';

    setReview('engine', engineLabel || (rebecca ? 'Rebecca Node' : 'PasarGuard Node'));
    setReview('engine_kind', engineKind || (rebecca ? 'Binary' : 'Docker'));

    var dockerIcon = modal.querySelector('[data-review-icon="docker"]');
    var binaryIcon = modal.querySelector('[data-review-icon="binary"]');
    if (dockerIcon) dockerIcon.hidden = rebecca;
    if (binaryIcon) binaryIcon.hidden = !rebecca;

    var channelLabel = (channel && channel.closest('label')) ?
      ((channel.closest('label').querySelector('.deploy-channel__title') || {}).textContent || channel.value) :
      (channel ? channel.value : 'Stable');

    setReview('node_name', name || (rebecca ? 'rebecca-node' : 'pg-node'));
    setReview('service_port', sPort ? ':' + sPort : '—');
    setReview('api_port', aPort ? ':' + aPort : '—');
    setReview('channel', (channelLabel || '').trim().split('\n')[0]);
    setReview('protocol', proto ? proto.value.toUpperCase() : 'gRPC');

    var protoRow = modal.querySelector('[data-review-row="protocol"]');
    if (protoRow) protoRow.hidden = rebecca;

    var bundleRow = modal.querySelector('[data-review-row="bundle"]');
    if (bundleRow) {
      bundleRow.hidden = !rebecca;
      var bundleVal = bundleInput ? bundleInput.value.trim() : '';
      var hasCert = bundleVal.indexOf('-----BEGIN CERTIFICATE-----') !== -1;
      var hasKey = bundleVal.indexOf('KEY-----') !== -1;
      setReview('bundle', (hasCert && hasKey) ? ((I18N && I18N.t ? I18N.t('nodes.bundle_valid') : '') || 'Valid Bundle') : '—');
    }

    if (btnSubmit) {
      var tpl = (I18N && I18N.t ? I18N.t('nodes.deploy_confirm') : '') || 'Run the official {engine} installer on this server now?';
      btnSubmit.setAttribute('data-confirm', tpl.replace('{engine}', engineLabel || (rebecca ? 'Rebecca' : 'PasarGuard')));
    }
  }

  function setStep(idx) {
    currentStep = Math.max(0, Math.min(2, idx));
    var modalBody = modal.querySelector('.modal__body');
    if (modalBody) {
      modalBody.scrollTop = 0;
      modalBody.scrollLeft = 0;
    }
    clearFieldErrors();

    steps.forEach(function (s, i) {
      var isActive = (i === currentStep);
      var isDone = (i < currentStep);
      s.classList.toggle('is-active', isActive);
      s.classList.toggle('is-done', isDone);
      if (isActive) {
        s.setAttribute('aria-current', 'step');
      } else {
        s.removeAttribute('aria-current');
      }
      var hit = s.querySelector('.deploy-step__hit');
      if (hit) {
        hit.disabled = (i > currentStep);
      }
    });

    if (progressBar) {
      var pct = currentStep === 0 ? 0 : (currentStep === 1 ? 50 : 100);
      progressBar.style.inlineSize = pct + '%';
    }

    panes.forEach(function (p, i) {
      p.hidden = (i !== currentStep);
      p.classList.toggle('is-active', i === currentStep);
    });

    if (btnBack) btnBack.hidden = (currentStep === 0);
    if (btnNext) btnNext.hidden = (currentStep === 2);
    if (btnSubmit) btnSubmit.hidden = (currentStep !== 2);

    if (currentStep === 1) {
      var focusInput = panes[1] ? panes[1].querySelector('input[name="node_name"]') : null;
      if (focusInput) setTimeout(function () { focusInput.focus(); }, 60);
    } else if (currentStep === 2) {
      updateReview();
      if (btnSubmit) setTimeout(function () { btnSubmit.focus(); }, 60);
    }

    renderIcons(modal);
  }

  steps.forEach(function (stepEl, idx) {
    var hit = stepEl.querySelector('.deploy-step__hit');
    var target = hit || stepEl;
    target.addEventListener('click', function (e) {
      e.preventDefault();
      if (idx < currentStep) {
        setStep(idx);
      }
    });
  });

  engineCards.forEach(function (card) {
    card.addEventListener('click', function () {
      var radio = card.querySelector('input[type="radio"]');
      if (radio) {
        var prevEngine = getSelectedEngine();
        radio.checked = true;
        syncEngineUI(prevEngine);
      }
    });
    var radio = card.querySelector('input[type="radio"]');
    if (radio) {
      radio.addEventListener('change', function () {
        syncEngineUI();
      });
    }
  });

  if (btnNext) {
    btnNext.addEventListener('click', function (e) {
      e.preventDefault();
      clearFieldErrors();

      if (currentStep === 0) {
        var engine = getSelectedEngine();
        if (!engine) {
          showFieldError('engine', (I18N && I18N.t ? I18N.t('nodes.err_engine_required') : '') || 'Please select a node engine.');
          return;
        }
        setStep(1);
        return;
      }

      if (currentStep === 1) {
        var engine = getSelectedEngine();
        var rebecca = isRebecca(engine);

        var nameField = form && form.elements['node_name'];
        if (nameField) {
          var nameVal = nameField.value.trim();
          if (!nameVal) {
            showFieldError('node_name', (I18N && I18N.t ? I18N.t('nodes.err_node_name') : '') || 'Node name is required.');
            nameField.focus();
            if (nameField.reportValidity) nameField.reportValidity();
            return;
          }
          if (!nameField.checkValidity()) {
            nameField.reportValidity();
            return;
          }
        }

        var sPortField = form && form.elements['service_port'];
        if (sPortField) {
          var sVal = parseInt(sPortField.value.trim(), 10);
          if (!sVal || isNaN(sVal) || sVal < 1 || sVal > 65535) {
            showFieldError('service_port', (I18N && I18N.t ? I18N.t('nodes.err_service_port') : '') || 'Enter a valid port (1-65535).');
            sPortField.focus();
            if (sPortField.reportValidity) sPortField.reportValidity();
            return;
          }
        }

        var aPortField = form && form.elements['api_port'];
        if (aPortField) {
          var aRaw = aPortField.value.trim();
          if (rebecca && !aRaw) {
            aPortField.value = '62051';
            aRaw = '62051';
          }
          if (aRaw) {
            var aVal = parseInt(aRaw, 10);
            if (!aVal || isNaN(aVal) || aVal < 1 || aVal > 65535) {
              showFieldError('api_port', (I18N && I18N.t ? I18N.t('nodes.err_api_port') : '') || 'Enter a valid port (1-65535).');
              aPortField.focus();
              if (aPortField.reportValidity) aPortField.reportValidity();
              return;
            }
            if (sPortField && aRaw === sPortField.value.trim()) {
              var errPortsEq = (I18N && I18N.t ? I18N.t('nodes.err_ports_equal') : '') || 'The API port must differ from the service port.';
              showFieldError('api_port', errPortsEq);
              aPortField.setCustomValidity(errPortsEq);
              aPortField.reportValidity();
              return;
            } else {
              aPortField.setCustomValidity('');
            }
          } else {
            aPortField.setCustomValidity('');
          }
        }

        if (rebecca) {
          var bundleVal = (bundleInput ? bundleInput.value : '').trim();
          var hasCert = bundleVal.indexOf('-----BEGIN CERTIFICATE-----') !== -1;
          var hasKey = bundleVal.indexOf('KEY-----') !== -1;
          if (!bundleVal || !hasCert || !hasKey) {
            showFieldError('bundle', (I18N && I18N.t ? I18N.t('nodes.bundle_invalid') : '') || 'Paste the certificate and its private key.');
            validateBundle();
            if (bundleInput) {
              bundleInput.focus();
              if (bundleInput.reportValidity && !bundleVal) bundleInput.reportValidity();
            }
            return;
          }
        }

        setStep(2);
        return;
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

  if (bundleInput) {
    bundleInput.addEventListener('input', validateBundle);
  }

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
            bundleInput.focus();
          }
        }).catch(function () {
          bundleInput.focus();
        });
      } else {
        bundleInput.focus();
      }
    });
  }

  // Pressing Enter in Step 1 inputs advances to Step 2
  panes.forEach(function (pane, paneIdx) {
    if (paneIdx === 1) {
      pane.querySelectorAll('input').forEach(function (input) {
        input.addEventListener('keydown', function (e) {
          if (e.key === 'Enter') {
            e.preventDefault();
            if (btnNext) btnNext.click();
          }
        });
      });
    }
  });

  var btnScope = modal.closest('.tab-pane') || root;
  btnScope.querySelectorAll('[data-engine-preselect]').forEach(function (btn) {
    if (btn.dataset.preselectReady === '1') return;
    btn.dataset.preselectReady = '1';
    btn.addEventListener('click', function () {
      var eng = btn.getAttribute('data-engine-preselect');
      var prevEngine = getSelectedEngine();
      var r = modal.querySelector('input[name="node_type"][value="' + eng + '"]');
      if (r) {
        r.checked = true;
        syncEngineUI(prevEngine);
        setStep(1);
      }
    });
  });

  btnScope.querySelectorAll('[data-modal-open="deploy-node-modal"]').forEach(function (btn) {
    if (!btn.hasAttribute('data-engine-preselect')) {
      if (btn.dataset.deployOpenReady === '1') return;
      btn.dataset.deployOpenReady = '1';
      btn.addEventListener('click', function () {
        setStep(0);
      });
    }
  });

  if (modal.classList.contains('is-open') || modal.hasAttribute('data-modal-open-initial')) {
    syncEngineUI();
    setStep(1);
  } else {
    syncEngineUI();
    setStep(0);
  }
}

export { initNodeCredentials, initActionMenus, initModals, initDeployModal };
