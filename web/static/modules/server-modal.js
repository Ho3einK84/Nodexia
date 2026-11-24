/* Nodexia Server Create / Edit Modal & Form UX (lazy-loaded ES module).
 *
 * Loaded on demand by app.js only when `[data-server-form]` exists in the page
 * or active tab pane (/servers, /servers/new, /servers/{id}/edit).
 *
 * Capabilities:
 *   - Smart SSH paste parsing (ssh root@host -p 2222, user@host:port, [v6]:port)
 *   - Live SSH target preview badge (user@host:port)
 *   - Visual credential strategy cards + password reveal + external_ref snippets
 *   - Interactive tag chips with fleet-tag one-click suggestions
 *   - Live SSH connection probe (POST /servers/test-connection, AbortController)
 *   - Instant client validation + smooth AJAX modal create (zero full-page jank)
 *   - Keyboard ergonomics (n on /servers opens modal, Ctrl/Cmd+Enter submits)
 */
import {
  I18N,
  el,
  escapeHTML,
  getCSRF,
  showToast,
  renderIcons,
  registerCleanup,
  tabNavigate,
  tabReload
} from './core.js';
import { openModal, closeModal } from './modals.js';

function fetchFreshCSRF(form) {
  return fetch('/api/csrf-token', { credentials: 'same-origin' })
    .then(function (resp) { return resp.ok ? resp.json() : null; })
    .then(function (data) {
      if (data && data.csrf_token) {
        var input = form && form.querySelector('input[name="_csrf_token"]');
        if (input) input.value = data.csrf_token;
        return data.csrf_token;
      }
      return getCSRF(form);
    })
    .catch(function () { return getCSRF(form); });
}

/* ── Smart SSH target parser ──────────────────────────────────────────────── */
function parseSSHTarget(raw) {
  var text = String(raw || '').trim();
  if (!text) return null;

  var username = '';
  var port = '';
  var host = '';
  var matched = false;

  if (/^ssh\s+/i.test(text)) {
    matched = true;
    var tokens = text.replace(/^ssh\s+/i, '').trim().split(/\s+/);
    var optsWithArg = { '-p': 'port', '-l': 'user', '-i': 'skip', '-o': 'skip', '-J': 'skip', '-F': 'skip', '-b': 'skip', '-c': 'skip', '-m': 'skip', '-W': 'skip', '-L': 'skip', '-R': 'skip', '-D': 'skip' };
    for (var i = 0; i < tokens.length; i++) {
      var tok = tokens[i];
      if (!tok) continue;
      if (optsWithArg[tok]) {
        var kind = optsWithArg[tok];
        var next = tokens[i + 1] || '';
        if (kind === 'port' && /^\d+$/.test(next)) port = next;
        else if (kind === 'user' && next) username = next;
        i++;
        continue;
      }
      if (/^-p\d+$/.test(tok)) { port = tok.slice(2); continue; }
      if (/^-l.+$/.test(tok)) { username = tok.slice(2); continue; }
      if (tok.charAt(0) === '-') continue;
      if (!host) { host = tok; break; }
    }
  } else {
    host = text;
  }

  host = host.replace(/^ssh:\/\//i, '');
  if (host !== text && !matched) matched = true;

  var atIdx = host.lastIndexOf('@');
  if (atIdx > 0) {
    if (!username) username = host.slice(0, atIdx);
    host = host.slice(atIdx + 1);
    matched = true;
  }

  // Bracketed IPv6 with optional :port — e.g. [2001:db8::1]:2222
  var v6 = /^\[([^\]]+)\](?::(\d+))?$/.exec(host);
  if (v6) {
    host = v6[1];
    if (v6[2] && !port) { port = v6[2]; matched = true; }
  } else {
    // Single-colon host:port (do not split bare IPv6 addresses with >=2 colons)
    var firstColon = host.indexOf(':');
    if (firstColon > 0 && firstColon === host.lastIndexOf(':')) {
      var maybePort = host.slice(firstColon + 1);
      if (/^\d{1,5}$/.test(maybePort)) {
        host = host.slice(0, firstColon);
        if (!port) port = maybePort;
        matched = true;
      }
    }
  }

  if (!matched || !host) return null;
  var pNum = port ? parseInt(port, 10) : 0;
  if (port && (isNaN(pNum) || pNum < 1 || pNum > 65535)) port = '';
  return { host: host.trim(), port: port, username: username.trim() };
}

function flashParsed(input) {
  if (!input) return;
  input.classList.add('is-parsed');
  setTimeout(function () { input.classList.remove('is-parsed'); }, 900);
}

/* ── Interactive tag chips controller ─────────────────────────────────────── */
function initTagsController(form) {
  var wrap = form.querySelector('[data-sf-tags]');
  if (!wrap) return null;

  var source = wrap.querySelector('[data-sf-tags-source]');
  var box = wrap.querySelector('[data-sf-tags-box]');
  var listEl = wrap.querySelector('[data-sf-tags-list]');
  var entry = wrap.querySelector('[data-sf-tags-entry]');
  var suggestBtns = wrap.querySelectorAll('[data-sf-tag-suggest]');
  if (!source || !box || !listEl || !entry) return null;

  source.hidden = true;
  box.hidden = false;

  var tags = [];

  function setError(msg) {
    var errEl = wrap.querySelector('[data-sf-error="tags"]');
    wrap.classList.toggle('has-error', !!msg);
    if (errEl) {
      errEl.textContent = msg || '';
      errEl.hidden = !msg;
    }
  }

  function syncSource() {
    source.value = tags.join(', ');
    suggestBtns.forEach(function (btn) {
      var val = (btn.getAttribute('data-sf-tag-suggest') || '').toLowerCase();
      btn.classList.toggle('is-active', tags.indexOf(val) !== -1);
    });
  }

  function renderChips() {
    var frag = document.createDocumentFragment();
    tags.forEach(function (tag) {
      var rm = el('button', {
        type: 'button',
        class: 'sf-tag-chip__rm',
        'aria-label': I18N.t('js.servers.remove_tag', { tag: tag }),
        on: {
          click: function (e) {
            e.stopPropagation();
            removeTag(tag);
          }
        }
      }, [el('i', { 'data-lucide': 'x', 'aria-hidden': 'true' })]);
      frag.appendChild(el('span', { class: 'sf-tag-chip' }, [tag, rm]));
    });
    listEl.innerHTML = '';
    listEl.appendChild(frag);
    renderIcons(listEl);
    syncSource();
  }

  function addRaw(raw) {
    var parts = String(raw || '').split(',');
    var added = false;
    for (var i = 0; i < parts.length; i++) {
      var clean = parts[i].trim().toLowerCase();
      if (!clean) continue;
      if (clean.length > 40) {
        setError(I18N.t('js.servers.err_tag_length'));
        return false;
      }
      if (tags.indexOf(clean) === -1) {
        tags.push(clean);
        added = true;
      }
    }
    setError('');
    if (added) renderChips();
    return true;
  }

  function removeTag(tag) {
    var idx = tags.indexOf(tag);
    if (idx === -1) return;
    tags.splice(idx, 1);
    setError('');
    renderChips();
  }

  function commitEntry() {
    if (!entry.value.trim()) return true;
    if (addRaw(entry.value)) {
      entry.value = '';
      return true;
    }
    return false;
  }

  box.addEventListener('click', function () { entry.focus(); });

  entry.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' || e.key === ',') {
      if (entry.value.trim()) {
        e.preventDefault();
        commitEntry();
      } else if (e.key === ',') {
        e.preventDefault();
      }
    } else if (e.key === 'Backspace' && !entry.value && tags.length) {
      tags.pop();
      setError('');
      renderChips();
    }
  });

  entry.addEventListener('blur', commitEntry);

  suggestBtns.forEach(function (btn) {
    btn.addEventListener('click', function () {
      var tag = (btn.getAttribute('data-sf-tag-suggest') || '').trim().toLowerCase();
      if (!tag) return;
      if (tags.indexOf(tag) !== -1) removeTag(tag);
      else { tags.push(tag); setError(''); renderChips(); }
    });
  });

  // Initial hydration from server-rendered value
  if (source.value) addRaw(source.value);
  else syncSource();

  return {
    commit: commitEntry,
    reset: function (initialVal) {
      tags = [];
      entry.value = '';
      setError('');
      if (initialVal) addRaw(initialVal);
      else renderChips();
    }
  };
}

/* ── Wire a single server form (in modal OR standalone page) ──────────────── */
function wireServerForm(form, owningPane) {
  if (!form || form.dataset.sfReady === '1') return;
  form.dataset.sfReady = '1';

  var modal = form.closest('[data-server-add-modal]');
  var isModalForm = form.hasAttribute('data-server-modal-form');

  var nameInput = form.querySelector('[data-sf-name]');
  var hostInput = form.querySelector('[data-sf-host]');
  var portInput = form.querySelector('[data-sf-port]');
  var userInput = form.querySelector('[data-sf-username]');
  var resetDayInput = form.querySelector('[data-sf-reset-day]');
  var previewEl = form.querySelector('[data-sf-preview]');
  var moreEl = form.querySelector('[data-sf-more]');
  var alertBox = form.querySelector('[data-sf-alert]');
  var alertText = form.querySelector('[data-sf-alert-text]');
  var testBtn = form.querySelector('[data-sf-test-btn]');
  var testBanner = form.querySelector('[data-sf-test-result]');
  var testIcon = form.querySelector('[data-sf-test-icon]');
  var testText = form.querySelector('[data-sf-test-text]');
  var submitBtn = form.querySelector('[data-sf-submit]');
  var cancelBtn = form.querySelector('[data-sf-cancel]');

  var strategyRadios = form.querySelectorAll('input[name="credential_strategy"]');
  var strategyCards = form.querySelectorAll('[data-strategy-card]');
  var credSections = {
    stored: form.querySelector('[data-cred-section="stored"]'),
    runtime: form.querySelector('[data-cred-section="runtime"]'),
    external_ref: form.querySelector('[data-cred-section="external_ref"]'),
    agent_ready: form.querySelector('[data-cred-section="agent_ready"]')
  };
  var credInputs = {
    stored: form.querySelector('[data-cred-input="stored"]'),
    runtime: form.querySelector('[data-cred-input="runtime"]'),
    external_ref: form.querySelector('[data-cred-input="external_ref"]')
  };

  var tagsCtrl = initTagsController(form);
  var testAbort = null;
  var isSubmitting = false;

  function activeStrategy() {
    var checked = form.querySelector('input[name="credential_strategy"]:checked');
    return checked ? checked.value : 'stored';
  }

  function syncStrategy() {
    var current = activeStrategy();
    strategyCards.forEach(function (card) {
      card.classList.toggle('is-selected', card.getAttribute('data-strategy-card') === current);
    });
    Object.keys(credSections).forEach(function (key) {
      if (credSections[key]) credSections[key].hidden = key !== current;
    });
    Object.keys(credInputs).forEach(function (key) {
      var ctrl = credInputs[key];
      if (!ctrl) return;
      var active = key === current;
      ctrl.disabled = !active;
      ctrl.name = active ? 'credential_ref' : 'credential_ref_disabled';
    });
  }

  function syncPreview() {
    if (!previewEl) return;
    var host = hostInput ? hostInput.value.trim() : '';
    var user = userInput ? userInput.value.trim() : '';
    var port = portInput ? portInput.value.trim() : '';
    previewEl.textContent = (user || 'root') + '@' + (host || 'host') + ':' + (port || '22');
    previewEl.classList.toggle('is-placeholder', !host);
  }

  function showAlert(msg) {
    if (!alertBox || !alertText) return;
    alertText.textContent = msg || '';
    alertBox.hidden = !msg;
    if (msg) renderIcons(alertBox);
  }

  function clearTestBanner() {
    if (testAbort) {
      try { testAbort.abort(); } catch (err) {}
      testAbort = null;
    }
    if (testBanner) {
      testBanner.hidden = true;
      testBanner.className = 'sf-test-banner';
    }
  }

  function setTestBanner(state, text) {
    if (!testBanner || !testIcon || !testText) return;
    testBanner.hidden = false;
    testBanner.className = 'sf-test-banner is-' + state;
    if (state === 'testing') {
      testIcon.innerHTML = '<span class="spinner spinner--sm" aria-hidden="true"></span>';
    } else {
      var iconName = state === 'ok' ? 'check-circle-2' : 'alert-circle';
      testIcon.innerHTML = '<i data-lucide="' + iconName + '" aria-hidden="true"></i>';
      renderIcons(testIcon);
    }
    testText.textContent = text;
  }

  function clearFieldErrors() {
    showAlert('');
    form.querySelectorAll('[data-sf-server-err]').forEach(function (n) { n.remove(); });
    form.querySelectorAll('[data-sf-error]').forEach(function (elErr) {
      elErr.textContent = '';
      elErr.hidden = true;
    });
    form.querySelectorAll('[data-sf-field].has-error').forEach(function (f) {
      f.classList.remove('has-error');
    });
  }

  function setFieldError(fieldName, msg) {
    var errorKey = fieldName;
    if (fieldName === 'credential_ref') {
      var strat = activeStrategy();
      if (strat === 'external_ref') errorKey = 'credential_ref_ext';
      else if (strat === 'runtime') errorKey = 'credential_ref_runtime';
    }
    var errEl = form.querySelector('[data-sf-error="' + errorKey + '"]') ||
      form.querySelector('[data-sf-error="' + fieldName + '"]');
    var fieldEl = errEl ? errEl.closest('[data-sf-field]') : form.querySelector('[data-sf-field="' + fieldName + '"]');
    if (fieldEl) fieldEl.classList.toggle('has-error', !!msg);
    if (errEl) {
      errEl.textContent = msg || '';
      errEl.hidden = !msg;
    }
    if (msg && moreEl && fieldEl && moreEl.contains(fieldEl)) {
      moreEl.open = true;
    }
    return fieldEl;
  }

  function focusFirstError(firstField) {
    if (!firstField) return;
    var input = firstField.querySelector('input:not([disabled]):not([type="hidden"]), textarea:not([disabled]), select:not([disabled])');
    if (input && input.focus) {
      try { input.focus({ preventScroll: false }); } catch (err) { input.focus(); }
    }
    if (firstField.scrollIntoView) {
      try { firstField.scrollIntoView({ behavior: 'smooth', block: 'nearest' }); } catch (err) {}
    }
  }

  function applyErrors(errorsMap) {
    var firstField = null;
    Object.keys(errorsMap || {}).forEach(function (field) {
      var f = setFieldError(field, errorsMap[field]);
      if (!firstField && f) firstField = f;
    });
    focusFirstError(firstField);
  }

  function validateClient(requireAll) {
    clearFieldErrors();
    var errs = {};

    if (requireAll) {
      var nameVal = nameInput ? nameInput.value.trim() : '';
      if (!nameVal) errs.name = I18N.t('js.servers.err_name');
    }

    var hostVal = hostInput ? hostInput.value.trim() : '';
    if (!hostVal || hostVal.indexOf('://') !== -1 || /[\/\\\s]/.test(hostVal)) {
      errs.host = I18N.t('js.servers.err_host');
    }

    var portRaw = portInput ? portInput.value.trim() : '22';
    var portNum = portRaw === '' ? 22 : Number(portRaw);
    if (!Number.isInteger(portNum) || portNum < 1 || portNum > 65535) {
      errs.port = I18N.t('js.servers.err_port');
    }

    var userVal = userInput ? userInput.value.trim() : '';
    if (!userVal) {
      errs.username = I18N.t('js.servers.err_username');
    }

    if (activeStrategy() === 'external_ref') {
      var extVal = credInputs.external_ref ? credInputs.external_ref.value.trim() : '';
      if (!extVal) errs.credential_ref = I18N.t('js.servers.err_ext_ref');
    }

    if (requireAll && resetDayInput) {
      var dayRaw = resetDayInput.value.trim();
      if (dayRaw !== '') {
        var dayNum = Number(dayRaw);
        if (!Number.isInteger(dayNum) || dayNum < 1 || dayNum > 28) {
          errs.traffic_reset_day = I18N.t('js.servers.err_reset_day');
        }
      }
    }

    if (Object.keys(errs).length > 0) {
      applyErrors(errs);
      return false;
    }
    return true;
  }

  /* ── Smart SSH paste on Host input ──────────────────────────────────────── */
  function tryApplySmartHost(rawText) {
    var parsed = parseSSHTarget(rawText);
    if (!parsed) return false;
    if (hostInput) { hostInput.value = parsed.host; flashParsed(hostInput); }
    if (parsed.port && portInput) { portInput.value = parsed.port; flashParsed(portInput); }
    if (parsed.username && userInput) { userInput.value = parsed.username; flashParsed(userInput); }
    syncPreview();
    clearTestBanner();
    var target = (parsed.username || (userInput && userInput.value.trim()) || 'root') + '@' + parsed.host + ':' + (parsed.port || (portInput && portInput.value.trim()) || '22');
    showToast(I18N.t('js.servers.parsed_ssh', { target: target }), 'ok', 2600);
    return true;
  }

  if (hostInput) {
    hostInput.addEventListener('paste', function (e) {
      var text = e.clipboardData && e.clipboardData.getData('text');
      if (text && parseSSHTarget(text)) {
        e.preventDefault();
        tryApplySmartHost(text);
      }
    });
    hostInput.addEventListener('blur', function () {
      if (hostInput.value && parseSSHTarget(hostInput.value)) {
        tryApplySmartHost(hostInput.value);
      }
    });
  }

  /* ── Strategy, preview, password reveal, snippets, presets ──────────────── */
  strategyRadios.forEach(function (radio) {
    radio.addEventListener('change', function () {
      syncStrategy();
      clearTestBanner();
    });
  });

  [hostInput, portInput, userInput].forEach(function (inp) {
    if (!inp) return;
    inp.addEventListener('input', function () {
      syncPreview();
      clearTestBanner();
    });
  });

  Object.keys(credInputs).forEach(function (k) {
    if (credInputs[k]) credInputs[k].addEventListener('input', clearTestBanner);
  });

  form.querySelectorAll('[data-pw-toggle]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var wrap = btn.closest('.sf-input');
      var input = wrap && wrap.querySelector('input');
      if (!input) return;
      var showing = input.type === 'password';
      input.type = showing ? 'text' : 'password';
      btn.setAttribute('aria-pressed', showing ? 'true' : 'false');
      btn.setAttribute('aria-label', I18N.t(showing ? 'servers.form.hide_password' : 'servers.form.show_password'));
      btn.innerHTML = showing
        ? '<i data-lucide="eye-off" aria-hidden="true"></i>'
        : '<i data-lucide="eye" aria-hidden="true"></i>';
      renderIcons(btn);
    });
  });

  form.querySelectorAll('[data-ext-snippet]').forEach(function (chip) {
    chip.addEventListener('click', function () {
      var ta = credInputs.external_ref;
      if (!ta) return;
      var snippet = chip.getAttribute('data-ext-snippet') || '';
      var cur = ta.value;
      ta.value = cur && !cur.endsWith('\n') ? (cur + '\n' + snippet) : (cur + snippet);
      ta.focus();
      ta.selectionStart = ta.selectionEnd = ta.value.length;
      setFieldError('credential_ref', '');
    });
  });

  form.querySelectorAll('[data-reset-preset]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      if (!resetDayInput) return;
      resetDayInput.value = btn.getAttribute('data-reset-preset') || '1';
      setFieldError('traffic_reset_day', '');
    });
  });

  // Clear inline field error the moment the user edits that field
  form.addEventListener('input', function (e) {
    var field = e.target && e.target.closest ? e.target.closest('[data-sf-field]') : null;
    if (!field || !field.classList.contains('has-error')) return;
    field.classList.remove('has-error');
    field.querySelectorAll('[data-sf-error], [data-sf-server-err]').forEach(function (errNode) {
      errNode.textContent = '';
      errNode.hidden = true;
    });
  });

  /* ── Live SSH Connection Test ───────────────────────────────────────────── */
  function setTestBusy(busy) {
    if (!testBtn) return;
    testBtn.disabled = busy;
    if (busy) {
      if (testBtn.dataset.origHtml === undefined) testBtn.dataset.origHtml = testBtn.innerHTML;
      testBtn.innerHTML = '<span class="spinner spinner--sm" aria-hidden="true"></span> <span>' + escapeHTML(I18N.t('js.servers.testing')) + '</span>';
    } else if (testBtn.dataset.origHtml !== undefined) {
      testBtn.innerHTML = testBtn.dataset.origHtml;
      delete testBtn.dataset.origHtml;
      renderIcons(testBtn);
    }
  }

  function runSSHTest() {
    syncStrategy();
    if (!validateClient(false)) return;

    if (testAbort) {
      try { testAbort.abort(); } catch (err) {}
    }
    var ctrl = new AbortController();
    testAbort = ctrl;

    setTestBusy(true);
    setTestBanner('testing', I18N.t('js.servers.testing'));

    fetchFreshCSRF(form).then(function (token) {
      var body = new URLSearchParams(new FormData(form));
      return fetch('/servers/test-connection', {
        method: 'POST',
        credentials: 'same-origin',
        signal: ctrl.signal,
        headers: {
          'Accept': 'application/json',
          'X-CSRF-Token': token || ''
        },
        body: body
      });
    }).then(function (resp) {
      return resp.json().then(function (data) { return { status: resp.status, data: data }; });
    }).then(function (res) {
      if (testAbort !== ctrl) return;
      var data = res.data || {};
      if (res.status === 422 && data.errors) {
        clearTestBanner();
        applyErrors(data.errors);
        return;
      }
      if (data.ok) {
        setTestBanner('ok', data.message || I18N.t('servers.form.test_ok'));
      } else {
        setTestBanner('error', data.error || data.message || I18N.t('js.servers.test_failed'));
      }
    }).catch(function (err) {
      if (err && err.name === 'AbortError') return;
      setTestBanner('error', I18N.t('js.servers.test_failed'));
    }).finally(function () {
      if (testAbort === ctrl) testAbort = null;
      setTestBusy(false);
    });
  }

  if (testBtn) {
    testBtn.addEventListener('click', runSSHTest);
  }

  /* ── Form reset helper (used after create or explicit cancel) ───────────── */
  function resetFormToDefaults() {
    form.reset();
    if (portInput) portInput.value = '22';
    if (resetDayInput) resetDayInput.value = '1';
    if (moreEl) moreEl.open = false;
    if (tagsCtrl) tagsCtrl.reset('');
    // Restore any revealed password fields back to masked
    form.querySelectorAll('[data-pw-toggle]').forEach(function (btn) {
      var wrap = btn.closest('.sf-input');
      var input = wrap && wrap.querySelector('input');
      if (input && input.type !== 'password') {
        input.type = 'password';
        btn.setAttribute('aria-pressed', 'false');
        btn.setAttribute('aria-label', I18N.t('servers.form.show_password'));
        btn.innerHTML = '<i data-lucide="eye" aria-hidden="true"></i>';
        renderIcons(btn);
      }
    });
    clearFieldErrors();
    clearTestBanner();
    syncStrategy();
    syncPreview();
  }

  if (cancelBtn) {
    cancelBtn.addEventListener('click', function () {
      clearFieldErrors();
      clearTestBanner();
    });
  }

  /* ── Submit handling (AJAX in modal, native/tab-manager on standalone) ──── */
  function setSubmitBusy(busy) {
    if (!submitBtn) return;
    submitBtn.disabled = busy;
    submitBtn.classList.toggle('is-busy', busy);
    if (busy) {
      if (submitBtn.dataset.origHtml === undefined) submitBtn.dataset.origHtml = submitBtn.innerHTML;
      submitBtn.innerHTML = '<span class="spinner spinner--sm" aria-hidden="true"></span> <span>' + escapeHTML(I18N.t('js.servers.creating')) + '</span>';
    } else if (submitBtn.dataset.origHtml !== undefined) {
      submitBtn.innerHTML = submitBtn.dataset.origHtml;
      delete submitBtn.dataset.origHtml;
      renderIcons(submitBtn);
    }
  }

  form.addEventListener('submit', function (e) {
    if (tagsCtrl && !tagsCtrl.commit()) {
      e.preventDefault();
      return;
    }
    syncStrategy();
    if (!validateClient(true)) {
      e.preventDefault();
      return;
    }

    if (!isModalForm) return; // Standalone /servers/new or /edit proceeds cleanly

    e.preventDefault();
    if (isSubmitting) return;
    isSubmitting = true;
    setSubmitBusy(true);

    fetchFreshCSRF(form).then(function (token) {
      var body = new URLSearchParams(new FormData(form));
      return fetch(form.getAttribute('action') || '/servers', {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
          'Accept': 'application/json',
          'X-CSRF-Token': token || ''
        },
        body: body
      });
    }).then(function (resp) {
      return resp.json().then(function (data) { return { status: resp.status, data: data }; });
    }).then(function (res) {
      var data = res.data || {};
      if (res.status === 422 && data.errors) {
        applyErrors(data.errors);
        if (data.message) showAlert(data.message);
        return;
      }
      if (!data.ok) {
        var msg = data.message || I18N.t('js.servers.request_failed');
        showAlert(msg);
        showToast(msg, 'error');
        return;
      }

      // Success! Close modal smoothly, reset, toast, and refresh /servers in-pane
      resetFormToDefaults();
      if (modal) closeModal(modal);
      showToast(data.message || I18N.t('js.servers.created'), 'ok');

      if (window.NodexiaTabs) {
        var active = typeof window.NodexiaTabs.getActive === 'function' ? window.NodexiaTabs.getActive() : null;
        if (active && active.url === '/servers') tabReload();
        else tabNavigate('/servers');
      } else {
        window.location.assign(data.redirect || '/servers');
      }
    }).catch(function () {
      var msg = I18N.t('js.servers.request_failed');
      showAlert(msg);
      showToast(msg, 'error');
    }).finally(function () {
      isSubmitting = false;
      setSubmitBusy(false);
    });
  });

  // Cmd/Ctrl + Enter submits from any field inside the form
  form.addEventListener('keydown', function (e) {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      if (typeof form.requestSubmit === 'function') form.requestSubmit();
      else if (submitBtn) submitBtn.click();
    }
  });

  // Cleanup on modal close & pane teardown
  if (modal) {
    modal.addEventListener('modal:beforeclose', function () {
      if (testAbort) {
        try { testAbort.abort(); } catch (err) {}
        testAbort = null;
      }
    });

    // Smooth mobile bottom-sheet swipe-down on header/handle to close
    var dialog = modal.querySelector('.modal__dialog--server');
    var dragZone = modal.querySelector('.sf-modal__header');
    var handle = modal.querySelector('.sf-modal__handle');
    if (dialog && (dragZone || handle)) {
      var startY = 0, tracking = false, dragDy = 0;
      var onStart = function (e) {
        if (window.innerWidth > 767 || e.touches.length !== 1) return;
        startY = e.touches[0].clientY;
        dragDy = 0;
        tracking = true;
      };
      var onMove = function (e) {
        if (!tracking) return;
        var dy = e.touches[0].clientY - startY;
        if (dy > 0) {
          dragDy = Math.min(dy, 180);
          dialog.style.transition = 'none';
          dialog.style.transform = 'translateY(' + dragDy + 'px)';
        }
      };
      var onEnd = function () {
        if (!tracking) return;
        tracking = false;
        dialog.style.transition = '';
        dialog.style.transform = '';
        if (dragDy > 80) closeModal(modal);
        dragDy = 0;
      };
      [dragZone, handle].forEach(function (zone) {
        if (!zone) return;
        zone.addEventListener('touchstart', onStart, { passive: true });
        zone.addEventListener('touchmove', onMove, { passive: true });
        zone.addEventListener('touchend', onEnd);
        zone.addEventListener('touchcancel', onEnd);
      });
    }
  }

  registerCleanup(owningPane || document, function () {
    if (testAbort) {
      try { testAbort.abort(); } catch (err) {}
      testAbort = null;
    }
  });

  syncStrategy();
  syncPreview();
}

/* ── Entrypoint called lazily by app.js ───────────────────────────────────── */
function initServerModal(root) {
  root = root || document;
  var owningPane = (root !== document && root.classList && root.classList.contains('tab-pane')) ? root : null;

  root.querySelectorAll('[data-server-form]').forEach(function (form) {
    wireServerForm(form, owningPane);
  });

  // Clean `?add=1` from URL once the modal auto-opens so browser reload stays clean
  var addModal = root.querySelector('[data-server-add-modal]');
  if (addModal) {
    if (addModal.hasAttribute('data-modal-open-initial')) {
      try {
        var u = new URL(window.location.href);
        if (u.searchParams.has('add')) {
          u.searchParams.delete('add');
          var clean = u.pathname + (u.searchParams.toString() ? '?' + u.searchParams.toString() : '');
          window.history.replaceState(window.history.state, '', clean);
        }
      } catch (err) {}
    }

    // `n` shortcut on /servers opens the Add Server modal smoothly
    if (addModal.dataset.sfShortcutReady !== '1') {
      addModal.dataset.sfShortcutReady = '1';
      var onKeyDown = function (e) {
        if (e.key !== 'n' || e.metaKey || e.ctrlKey || e.altKey) return;
        var active = document.activeElement;
        if (active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA' || active.tagName === 'SELECT' || active.isContentEditable)) return;
        if (document.querySelector('[data-modal].is-open')) return;
        var pane = addModal.closest('.tab-pane');
        if (pane && pane.hidden) return;
        if (!document.contains(addModal)) return;
        e.preventDefault();
        openModal(addModal);
      };
      document.addEventListener('keydown', onKeyDown);
      registerCleanup(owningPane || document, function () {
        document.removeEventListener('keydown', onKeyDown);
      });
    }
  }
}

export { initServerModal };
