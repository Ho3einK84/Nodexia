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
function initModals(root) {
  root = root || document;
  var openers = root.querySelectorAll('[data-modal-open]');
  var modals = root.querySelectorAll('[data-modal]');
  if (!modals.length) return;
  var lastFocus = null;

  function openModal(modal) {
    if (!modal) return;
    lastFocus = document.activeElement;
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
    if (lastFocus && lastFocus.focus) { try { lastFocus.focus(); } catch (err) {} }
  }

  openers.forEach(function (opener) {
    opener.addEventListener('click', function (e) {
      e.preventDefault();
      var id = opener.getAttribute('data-modal-open');
      openModal(id ? document.getElementById(id) : null);
    });
  });

  modals.forEach(function (modal) {
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

  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape') return;
    document.querySelectorAll('[data-modal].is-open').forEach(closeModal);
  });

  // Re-open a modal the server asked to surface (e.g. install form had errors).
  var initial = root.querySelector('[data-modal][data-modal-open-initial]');
  if (initial) openModal(initial);
}

/* ── Unified Deploy Node Wizard ───────────────────────────── */
function initDeployModal(root) {
  root = root || document;
  var modal = root.querySelector('[data-deploy-modal]');
  if (!modal || modal.dataset.deployWizardReady === '1') return;
  modal.dataset.deployWizardReady = '1';

  var form = modal.querySelector('[data-deploy-form]');
  var steps = modal.querySelectorAll('[data-step-indicator]');
  var panes = modal.querySelectorAll('[data-deploy-pane]');
  var btnBack = modal.querySelector('[data-deploy-back]');
  var btnNext = modal.querySelector('[data-deploy-next]');
  var btnSubmit = modal.querySelector('[data-deploy-submit]');
  var bundleInput = modal.querySelector('[data-bundle-input]');
  var bundleState = modal.querySelector('[data-bundle-state]');
  var engineCards = modal.querySelectorAll('[data-engine-card]');
  var currentStep = 0;

  function isRebecca(engine) {
    return engine === 'rebecca' || engine === 'rebecca-node';
  }

  function getSelectedEngine() {
    var checked = modal.querySelector('input[name="node_type"]:checked');
    return checked ? checked.value : 'pasarguard-node';
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
    if (protoRow) protoRow.hidden = (kind !== 'docker');

    var bundleRow = modal.querySelector('[data-review-row="bundle"]');
    if (bundleRow) bundleRow.hidden = (kind !== 'binary');

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
    var optBadge = modal.querySelector('[data-api-port-optional]');
    if (apiPortInput) {
      if (rebecca) {
        if (!apiPortInput.value.trim()) apiPortInput.value = '62051';
        apiPortInput.required = true;
        apiPortInput.placeholder = '62051';
        if (optBadge) optBadge.hidden = true;
        if (bundleInput) bundleInput.required = true;
      } else {
        if (apiPortInput.value === '62051') apiPortInput.value = '';
        apiPortInput.required = false;
        apiPortInput.placeholder = '62051';
        if (optBadge) optBadge.hidden = false;
        if (bundleInput) bundleInput.required = false;
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
    var displayEngine = engineLabel ? (engineLabel + ' (' + engineKind + ')') : (rebecca ? 'Rebecca (Native)' : 'PasarGuard (Docker)');

    var channelLabel = (channel && channel.closest('label')) ?
      ((channel.closest('label').querySelector('span') || {}).textContent || channel.value) :
      (channel ? channel.value : 'Stable');

    setReview('engine', displayEngine);
    setReview('node_name', name || (rebecca ? 'rebecca-node' : 'pg-node'));
    setReview('service_port', sPort ? ':' + sPort : '—');
    setReview('api_port', aPort ? ':' + aPort : '—');
    setReview('channel', (channelLabel || '').trim());
    setReview('protocol', proto ? proto.value.toUpperCase() : 'gRPC');

    var protoRow = modal.querySelector('[data-review-row="protocol"]');
    if (protoRow) protoRow.hidden = rebecca;

    var bundleRow = modal.querySelector('[data-review-row="bundle"]');
    if (bundleRow) {
      bundleRow.hidden = !rebecca;
      var hasBundle = bundleInput && bundleInput.value.trim().length > 0;
      setReview('bundle', hasBundle ? '✓ ' + (I18N && I18N.t ? I18N.t('nodes.bundle_valid') || 'Configured' : 'Configured') : '—');
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

    steps.forEach(function (s, i) {
      s.classList.toggle('is-active', i === currentStep);
      s.classList.toggle('is-done', i < currentStep);
    });
    panes.forEach(function (p, i) {
      p.hidden = (i !== currentStep);
      p.classList.toggle('is-active', i === currentStep);
    });
    if (btnBack) btnBack.hidden = (currentStep === 0);
    if (btnNext) btnNext.hidden = (currentStep === 2);
    if (btnSubmit) btnSubmit.hidden = (currentStep !== 2);

    if (currentStep === 1) {
      var focus = panes[1] ? panes[1].querySelector('input[name="node_name"]') : null;
      if (focus) setTimeout(function () { focus.focus(); }, 50);
    } else if (currentStep === 2) {
      updateReview();
    }
  }

  steps.forEach(function (stepEl, idx) {
    stepEl.addEventListener('click', function () {
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
      if (currentStep === 1) {
        var nameField = form && form.elements['node_name'];
        if (nameField && !nameField.checkValidity()) {
          nameField.reportValidity();
          return;
        }
        var sPortField = form && form.elements['service_port'];
        if (sPortField && !sPortField.checkValidity()) {
          sPortField.reportValidity();
          return;
        }
        var aPortField = form && form.elements['api_port'];
        var engine = getSelectedEngine();
        var rebecca = isRebecca(engine);

        if (aPortField) {
          var aVal = aPortField.value.trim();
          if (rebecca && !aVal) {
            aPortField.value = '62051';
            aVal = '62051';
          }
          if (aVal) {
            if (!aPortField.checkValidity()) {
              aPortField.reportValidity();
              return;
            }
            if (sPortField && aVal === sPortField.value.trim()) {
              var errPortsEq = (I18N && I18N.t ? I18N.t('nodes.err_ports_equal') : '') || 'The API port must differ from the service port.';
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
            if (bundleState) {
              bundleState.hidden = false;
              bundleState.className = 'field-hint deploy-bundle-state is-bad';
              bundleState.textContent = '⚠ ' + (I18N && I18N.t ? I18N.t('nodes.bundle_invalid') || 'Paste the certificate and its private key.' : 'Paste the certificate and its private key.');
            }
            if (bundleInput) {
              bundleInput.focus();
              if (bundleInput.reportValidity && !bundleVal) {
                bundleInput.reportValidity();
              }
            }
            return;
          }
        }
      }
      setStep(currentStep + 1);
    });
  }

  if (btnBack) {
    btnBack.addEventListener('click', function (e) {
      e.preventDefault();
      setStep(currentStep - 1);
    });
  }

  if (bundleInput && bundleState) {
    bundleInput.addEventListener('input', function () {
      var val = bundleInput.value.trim();
      if (!val) {
        bundleState.hidden = true;
        return;
      }
      var hasCert = val.indexOf('-----BEGIN CERTIFICATE-----') !== -1;
      var hasKey = val.indexOf('KEY-----') !== -1;
      bundleState.hidden = false;
      if (hasCert && hasKey) {
        bundleState.className = 'field-hint deploy-bundle-state is-ok';
        bundleState.textContent = '✓ ' + (I18N.t('nodes.bundle_valid') || 'Certificate and private key detected.');
      } else {
        bundleState.className = 'field-hint deploy-bundle-state is-bad';
        bundleState.textContent = '⚠ ' + (I18N.t('nodes.bundle_invalid') || 'Paste the certificate and its private key.');
      }
    });
  }

  var pasteBtn = modal.querySelector('[data-bundle-paste]');
  if (pasteBtn && bundleInput) {
    pasteBtn.addEventListener('click', function (e) {
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

  document.querySelectorAll('[data-engine-preselect]').forEach(function (btn) {
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

  document.querySelectorAll('[data-modal-open="deploy-node-modal"]').forEach(function (btn) {
    if (!btn.hasAttribute('data-engine-preselect')) {
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
