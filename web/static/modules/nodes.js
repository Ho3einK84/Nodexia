/* Nodexia Nodes workspace controller (native ES module — no bundler, no npm).
 *
 * Pane-scoped controller for the Nodes feature:
 *   - Live CSRF POST discovery refresh + skeleton shimmer + inline retry + unified toasts
 *   - Client-side instant search, engine/status filtering, sort & grid/list toggle
 *   - Interactive 30-day uptime strip tooltips & on-demand PasarGuard credentials
 *   - Safe per-node action execution -> confirm modal -> live SSE log drawer
 *   - SSH Config Viewer modal (.env keys/redacted secrets)
 *   - Lazy-loads modules/nodes-deploy.js on demand when opening the Deploy Wizard
 */
import { I18N, el, getCSRF, showToast, renderIcons, registerCleanup } from './core.js';
import { initSecrets, initCopyTargets, initCollapsibles } from './ui.js';
import { openModal, closeModal, closeAllMenus, initActionMenus } from './modals.js';

var credIdCounter = 0;

function iconEl(name, className) {
  return el('i', { 'data-lucide': name, class: className || null, 'aria-hidden': 'true' });
}

function readJSON(resp) {
  var ct = ((resp.headers && resp.headers.get('content-type')) || '').toLowerCase();
  if (ct.indexOf('application/json') === -1) {
    return Promise.resolve({ ok: false, status: resp.status, data: null });
  }
  return resp.json().then(function (data) {
    return { ok: resp.ok, status: resp.status, data: data };
  }).catch(function () {
    return { ok: false, status: resp.status, data: null };
  });
}

function healthLabelKey(health) {
  var map = {
    running: 'nodes.health_running',
    degraded: 'nodes.health_degraded',
    restarting: 'nodes.health_restarting',
    activating: 'nodes.health_restarting',
    stopped: 'nodes.health_stopped',
    inactive: 'nodes.health_stopped',
    detected: 'nodes.health_detected',
    not_detected: 'nodes.health_not_detected'
  };
  return map[String(health || '').toLowerCase()] || 'nodes.health_unknown';
}

function isMutatingAction(actionKey) {
  return ['start', 'stop', 'restart', 'update', 'core-update', 'renew-cert', 'reinstall', 'uninstall'].indexOf(String(actionKey || '')) !== -1;
}

function copyWithIconFeedback(btn, getText) {
  var text = typeof getText === 'function' ? getText() : getText;
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text || '');
  }
  btn.textContent = '';
  btn.appendChild(iconEl('check'));
  btn.classList.add('is-copied');
  renderIcons(btn);
  setTimeout(function () {
    btn.textContent = '';
    btn.appendChild(iconEl('copy'));
    btn.classList.remove('is-copied');
    renderIcons(btn);
  }, 1500);
}

/* ── Shared DOM helpers (reused by nodes-deploy.js) ───────────────────────── */
function buildFact(iconName, label, value, codeClass, wide) {
  return el('div', { class: 'node-fact' + (wide ? ' node-fact--wide' : '') }, [
    el('span', { class: 'node-fact__label' }, [iconEl(iconName), ' ' + label]),
    el('span', { class: 'node-fact__value' },
      (codeClass && value !== '-') ? el('code', { class: codeClass, text: value }) : value
    )
  ]);
}

function buildCredBlock(iconName, label, id, value, mask) {
  return el('div', { class: 'node-section node-cred-block' }, [
    el('div', { class: 'node-section__head' }, [
      el('span', { class: 'node-section__label' }, [iconEl(iconName), ' ' + label]),
      el('button', {
        type: 'button',
        class: 'btn btn--ghost btn--sm copy-inline',
        'data-copy-target': '#' + id,
        'aria-label': I18N.t('js.copy.aria_label', { label: label })
      }, [iconEl('copy'), ' ' + I18N.t('js.copy.label')])
    ]),
    el('pre', {
      id: id,
      class: 'output-block node-secret-block' + (mask ? '' : ' scrollable'),
      'data-no-auto-copy': true,
      'data-secret': mask || null,
      text: value || ''
    })
  ]);
}

function buildChipsSection(iconName, label, items, classFn) {
  return el('div', { class: 'node-section' }, [
    el('span', { class: 'node-section__label' }, [iconEl(iconName), ' ' + label]),
    el('div', { class: 'node-chips' }, items.map(function (item) {
      return el('code', { class: classFn(item), text: item });
    }))
  ]);
}

/* ── On-demand PasarGuard node credentials ────────────────────────────────── */
function wireNodeCredentials(root) {
  (root || document).querySelectorAll('[data-node-credentials]').forEach(function (btn) {
    if (btn.dataset.credReady === '1') return;
    btn.dataset.credReady = '1';
    var out = btn.parentNode ? btn.parentNode.querySelector('.node-credentials__out') : null;
    var showLabel = I18N.t('js.cred.show');

    var setButtonState = function (iconName, labelText, disabled) {
      btn.disabled = !!disabled;
      btn.textContent = '';
      btn.appendChild(iconEl(iconName, 'icon-in-button'));
      btn.appendChild(document.createTextNode(' ' + labelText));
      renderIcons(btn);
    };

    var showErr = function (message) {
      if (out) {
        out.hidden = false;
        out.textContent = '';
        out.appendChild(el('p', { class: 'node-actions-note', text: message }));
      }
      setButtonState('key-round', showLabel, false);
    };

    btn.addEventListener('click', function () {
      if (out && !out.hidden) {
        out.hidden = true;
        out.textContent = '';
        setButtonState('key-round', showLabel, false);
        return;
      }
      setButtonState('loader', I18N.t('js.loading'), true);
      fetch(btn.getAttribute('data-url'), { headers: { Accept: 'application/json' } })
        .then(readJSON)
        .then(function (res) {
          btn.disabled = false;
          if (!res.ok || !res.data) {
            showErr((res.data && res.data.error) || I18N.t('js.cred.load_error'));
            return;
          }
          if (!out) return;
          out.textContent = '';
          out.hidden = false;
          var name = String(res.data.node_name || 'node').replace(/[^a-zA-Z0-9._-]/g, '') || 'node';
          var seq = ++credIdCounter;
          if (res.data.has_api_key) {
            out.appendChild(buildCredBlock('key-round', I18N.t('js.cred.api_key'), 'cred-' + seq + '-key-' + name, res.data.api_key, true));
          } else {
            out.appendChild(el('p', { class: 'node-actions-note', text: I18N.t('js.cred.api_key_missing', { name: name }) }));
          }
          if (res.data.has_cert) {
            out.appendChild(buildCredBlock('shield-check', I18N.t('js.cred.ssl_cert'), 'cred-' + seq + '-cert-' + name, res.data.certificate, false));
          }
          initSecrets(out);
          initCopyTargets(out);
          renderIcons(out);
          setButtonState('eye-off', I18N.t('js.cred.hide'), false);
        })
        .catch(function () {
          btn.disabled = false;
          showErr(I18N.t('js.cred.load_error'));
        });
    });
  });
}

/* ── Main Nodes Workspace Controller ──────────────────────────────────────── */
function wireNodesWorkspace(workspace) {
  if (!workspace || workspace.dataset.nodesReady === '1') return;
  workspace.dataset.nodesReady = '1';

  var q = function (sel) { return workspace.querySelector(sel); };
  var owningPane = workspace.closest('.tab-pane') || workspace;
  var serverId = workspace.getAttribute('data-server-id') || '';
  var nodesUrl = workspace.getAttribute('data-nodes-url') || ('/servers/' + serverId + '/nodes');
  var hasCreds = workspace.getAttribute('data-has-creds') === '1';

  var gridWrap = q('[data-node-grid]');
  var overviewCard = q('[data-nodes-overview]');
  var toolbarEl = q('[data-nodes-toolbar]');
  var skeletonEl = q('[data-nodes-skeleton]');
  var inlineBanner = q('[data-nodes-inline-banner]');
  var emptyNoneEl = q('[data-nodes-empty="none"]');
  var emptyNosnapEl = q('[data-nodes-empty="nosnap"]');
  var filterEmptyEl = q('[data-nodes-filter-empty]');

  var searchInput = q('[data-nodes-search]');
  var searchClearBtn = q('[data-nodes-search-clear]');
  var engineFilter = q('[data-nodes-filter-engine]');
  var statusFilter = q('[data-nodes-filter-status]');
  var sortSelect = q('[data-nodes-sort]');
  var viewGridBtn = q('[data-nodes-view="grid"]');
  var viewListBtn = q('[data-nodes-view="list"]');
  var filterResetBtn = q('[data-nodes-filter-reset]');

  var logDrawer = q('[data-log-drawer]');
  var configModal = q('[data-nodes-config-modal]');
  var confirmModal = q('[data-nodes-confirm-modal]');
  var deployModal = q('[data-deploy-modal]');

  var activeTimers = new Set();
  var refreshAbort = null;
  var pendingConfirmAction = null;
  var deployWizardPromise = null;

  function trackTimeout(fn, ms) {
    var id = setTimeout(function () { activeTimers.delete(id); fn(); }, ms);
    activeTimers.add(id);
    return id;
  }

  registerCleanup(owningPane, function () {
    activeTimers.forEach(clearTimeout);
    activeTimers.clear();
    if (refreshAbort) {
      try { refreshAbort.abort(); } catch (err) {}
      refreshAbort = null;
    }
    pendingConfirmAction = null;
  });

  function setInlineBanner(kind, message, showRetry) {
    if (!inlineBanner) return;
    if (!message) { inlineBanner.hidden = true; return; }
    inlineBanner.className = 'nodes-inline-banner nodes-inline-banner--' + (kind || 'error');
    var msgEl = inlineBanner.querySelector('[data-nodes-banner-text]');
    var retryBtn = inlineBanner.querySelector('[data-nodes-retry-btn]');
    if (msgEl) msgEl.textContent = message;
    if (retryBtn) retryBtn.hidden = !showRetry;
    inlineBanner.hidden = false;
    renderIcons(inlineBanner);
  }

  /* ── View Mode (Grid / List) + Search / Filter / Sort ── */
  var VIEW_STORAGE_KEY = 'nodexia_nodes_view';
  function applyViewMode(mode) {
    mode = mode === 'list' ? 'list' : 'grid';
    if (gridWrap) gridWrap.classList.toggle('node-grid--list', mode === 'list');
    if (viewGridBtn) viewGridBtn.setAttribute('aria-pressed', mode === 'grid' ? 'true' : 'false');
    if (viewListBtn) viewListBtn.setAttribute('aria-pressed', mode === 'list' ? 'true' : 'false');
    try { localStorage.setItem(VIEW_STORAGE_KEY, mode); } catch (err) {}
  }

  function statusRank(health) {
    var low = String(health || '').toLowerCase();
    if (low === 'running') return 0;
    if (low === 'degraded' || low === 'restarting' || low === 'activating' || low === 'detected') return 1;
    if (low === 'stopped' || low === 'inactive') return 2;
    return 3;
  }

  function matchesStatusFilter(health, filterVal) {
    if (!filterVal || filterVal === 'all') return true;
    var rank = statusRank(health);
    if (filterVal === 'running') return rank === 0;
    if (filterVal === 'degraded') return rank === 1;
    if (filterVal === 'stopped') return rank === 2;
    return rank === 3;
  }

  function applyFilterSortView() {
    if (!gridWrap) return;
    var slots = Array.prototype.slice.call(gridWrap.querySelectorAll('[data-node-slot]'));
    var query = searchInput ? searchInput.value.trim().toLowerCase() : '';
    var eng = engineFilter ? engineFilter.value : 'all';
    var st = statusFilter ? statusFilter.value : 'all';
    var sortBy = sortSelect ? sortSelect.value : 'name';

    if (searchClearBtn) searchClearBtn.hidden = !query;

    slots.sort(function (a, b) {
      if (sortBy === 'status') {
        var sr = statusRank(a.getAttribute('data-node-health')) - statusRank(b.getAttribute('data-node-health'));
        if (sr !== 0) return sr;
      } else if (sortBy === 'uptime') {
        var diffU = parseInt(b.getAttribute('data-node-uptime') || '-1', 10) - parseInt(a.getAttribute('data-node-uptime') || '-1', 10);
        if (diffU !== 0) return diffU;
      } else if (sortBy === 'latency') {
        var la = parseInt(a.getAttribute('data-node-latency') || '0', 10) || 999999;
        var lb = parseInt(b.getAttribute('data-node-latency') || '0', 10) || 999999;
        if (la !== lb) return la - lb;
      }
      return String(a.getAttribute('data-node-name') || '').localeCompare(String(b.getAttribute('data-node-name') || ''));
    });

    var frag = document.createDocumentFragment();
    var visibleCount = 0;
    slots.forEach(function (slot) {
      var ok = (!query || (slot.getAttribute('data-node-search') || '').toLowerCase().indexOf(query) !== -1) &&
        (eng === 'all' || (slot.getAttribute('data-node-type') || '') === eng) &&
        matchesStatusFilter(slot.getAttribute('data-node-health') || '', st);
      slot.hidden = !ok;
      if (ok) visibleCount++;
      frag.appendChild(slot);
    });
    gridWrap.appendChild(frag);

    if (filterEmptyEl) filterEmptyEl.hidden = !(slots.length > 0 && visibleCount === 0);
    gridWrap.hidden = slots.length === 0 || visibleCount === 0;
  }

  var initialView = 'grid';
  try { initialView = localStorage.getItem(VIEW_STORAGE_KEY) || 'grid'; } catch (err) {}
  applyViewMode(initialView);

  if (searchInput) searchInput.addEventListener('input', applyFilterSortView);
  if (searchClearBtn && searchInput) {
    searchClearBtn.addEventListener('click', function () {
      searchInput.value = '';
      applyFilterSortView();
      searchInput.focus();
    });
  }
  [engineFilter, statusFilter, sortSelect].forEach(function (ctrl) {
    if (ctrl) ctrl.addEventListener('change', applyFilterSortView);
  });
  if (viewGridBtn) viewGridBtn.addEventListener('click', function () { applyViewMode('grid'); });
  if (viewListBtn) viewListBtn.addEventListener('click', function () { applyViewMode('list'); });
  if (filterResetBtn) {
    filterResetBtn.addEventListener('click', function () {
      if (searchInput) searchInput.value = '';
      if (engineFilter) engineFilter.value = 'all';
      if (statusFilter) statusFilter.value = 'all';
      if (sortSelect) sortSelect.value = 'name';
      applyFilterSortView();
    });
  }

  /* ── Interactive 30-day Uptime Tooltip Tap / Focus ── */
  function wireUptimeStripInteractions(container) {
    (container || workspace).querySelectorAll('.node-uptime-block').forEach(function (block) {
      if (block.dataset.uptimeStripReady === '1') return;
      block.dataset.uptimeStripReady = '1';
      var activeTip = block.querySelector('[data-uptime-active-tip]');
      var foot = block.querySelector('[data-uptime-foot]') || (activeTip ? activeTip.parentElement : null);
      if (!activeTip) return;
      block.querySelectorAll('.node-uptime-bar').forEach(function (bar) {
        var announce = function () {
          var tip = bar.getAttribute('aria-label') || '';
          if (!tip) return;
          activeTip.textContent = tip;
          activeTip.hidden = false;
          if (foot) foot.hidden = false;
        };
        bar.addEventListener('click', announce);
        bar.addEventListener('focus', announce);
      });
    });
  }
  wireUptimeStripInteractions(workspace);

  /* ── Update KPI Header & Re-render Cards From JSON ── */
  function updateFleetKPI(fleet, collection) {
    if (!fleet) return;
    var setText = function (sel, val) {
      workspace.querySelectorAll(sel).forEach(function (n) { n.textContent = val; });
    };
    var offlineCount = (fleet.stopped || 0) + (fleet.degraded || 0) + (fleet.unknown || 0);
    var deployingCount = fleet.installing_count || 0;

    setText('[data-kpi="total"]', String(fleet.total || 0));
    workspace.querySelectorAll('[data-node-fleet]').forEach(function (f) { f.setAttribute('data-total', String(fleet.total || 0)); });
    setText('[data-kpi="online"]', String(fleet.running || 0));
    setText('[data-kpi="offline_degraded"]', String(offlineCount));
    setText('[data-kpi="pg"]', String(fleet.pasarguard_count || 0));
    setText('[data-kpi="rebecca"]', String(fleet.rebecca_count || 0));
    setText('[data-kpi="deploying"]', String(deployingCount));
    setText('[data-kpi="ports"]', String(fleet.ports || 0));
    setText('[data-kpi="health_pct"]', fleet.health_known ? (fleet.health_pct + '%') : '—');

    workspace.querySelectorAll('[data-kpi-tile="offline"]').forEach(function (t) {
      t.classList.toggle('nodes-kpi--crit', offlineCount > 0);
      t.classList.toggle('nodes-kpi--muted', offlineCount === 0);
    });
    workspace.querySelectorAll('[data-kpi-tile="deploying"]').forEach(function (t) {
      t.classList.toggle('nodes-kpi--info', deployingCount > 0);
      t.classList.toggle('nodes-kpi--muted', deployingCount === 0);
    });
    workspace.querySelectorAll('[data-kpi-progress]').forEach(function (p) {
      p.value = fleet.health_known ? Number(fleet.health_pct || 0) : 0;
    });

    var ports = Array.isArray(fleet.port_list) ? fleet.port_list : [];
    workspace.querySelectorAll('[data-kpi-port-list]').forEach(function (wrap) {
      wrap.textContent = '';
      var frag = document.createDocumentFragment();
      ports.forEach(function (p) { frag.appendChild(el('code', { class: 'nodes-port-badge', text: ':' + p })); });
      wrap.appendChild(frag);
    });
    workspace.querySelectorAll('[data-kpi-ports-bar]').forEach(function (bar) { bar.hidden = ports.length === 0; });
    if (collection && collection.collected_at) setText('[data-last-sweep]', collection.collected_at);
    if (collection && collection.last_sweep_iso) workspace.setAttribute('data-last-sweep-iso', collection.last_sweep_iso);
  }

  function renderSnapshotsFromJSON(snapshots, fleet) {
    if (!gridWrap) return;
    gridWrap.textContent = '';
    var csrfToken = getCSRF(workspace);
    var frag = document.createDocumentFragment();
    (Array.isArray(snapshots) ? snapshots : []).forEach(function (snap) {
      if (snap && snap.node_type !== 'none') {
        frag.appendChild(buildNodeSlotElement(snap, serverId, csrfToken));
      }
    });
    gridWrap.appendChild(frag);

    var hasNodes = gridWrap.childElementCount > 0;
    var emptyState = (fleet && fleet.empty_state) || (hasNodes ? '' : 'none');

    if (overviewCard) overviewCard.hidden = !hasNodes;
    if (toolbarEl) toolbarEl.hidden = !hasNodes;
    if (emptyNoneEl) emptyNoneEl.hidden = !(emptyState === 'none' && !hasNodes);
    if (emptyNosnapEl) emptyNosnapEl.hidden = !(emptyState === 'nosnap' && !hasNodes);

    initCollapsibles(gridWrap);
    initActionMenus(gridWrap);
    wireNodeCredentials(gridWrap);
    wireUptimeStripInteractions(gridWrap);
    applyFilterSortView();
    renderIcons(gridWrap);
  }

  /* ── Refresh Nodes (CSRF POST -> JSON) ── */
  function refreshNodes(opts) {
    opts = opts || {};
    if (refreshAbort) { try { refreshAbort.abort(); } catch (err) {} }
    var controller = typeof AbortController !== 'undefined' ? new AbortController() : null;
    refreshAbort = controller;

    var refreshBtns = workspace.querySelectorAll('[data-nodes-refresh-btn]');
    refreshBtns.forEach(function (b) { b.disabled = true; b.classList.add('is-busy'); });

    if (opts.showSkeleton && skeletonEl) {
      skeletonEl.hidden = false;
      if (gridWrap) gridWrap.hidden = true;
      if (emptyNoneEl) emptyNoneEl.hidden = true;
      if (emptyNosnapEl) emptyNosnapEl.hidden = true;
    }

    var csrf = getCSRF(workspace);
    var body = new URLSearchParams();
    if (csrf) body.set('_csrf_token', csrf);
    if (opts.form) {
      new FormData(opts.form).forEach(function (val, key) {
        if (key !== '_csrf_token') body.set(key, val);
      });
    }

    var fetchOpts = {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'X-Requested-With': 'fetch',
        'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8'
      },
      body: body.toString()
    };
    if (csrf) fetchOpts.headers['X-CSRF-Token'] = csrf;
    if (controller) fetchOpts.signal = controller.signal;

    var finishBusy = function () {
      if (refreshAbort === controller) refreshAbort = null;
      if (skeletonEl) skeletonEl.hidden = true;
      refreshBtns.forEach(function (b) { b.disabled = false; b.classList.remove('is-busy'); });
    };

    return fetch(nodesUrl, fetchOpts)
      .then(readJSON)
      .then(function (res) {
        finishBusy();
        if (!res.data) {
          setInlineBanner('error', I18N.t('nodes.flash_refresh_failed'), true);
          if (!opts.quiet) showToast(I18N.t('nodes.flash_refresh_failed'), 'error');
          applyFilterSortView();
          return;
        }
        if (res.data.fleet) updateFleetKPI(res.data.fleet, res.data.collection);
        if (Array.isArray(res.data.snapshots)) renderSnapshotsFromJSON(res.data.snapshots, res.data.fleet);
        else applyFilterSortView();

        if (!res.ok || !res.data.ok) {
          var errMsg = res.data.flash_message || res.data.error || I18N.t('nodes.flash_refresh_failed');
          setInlineBanner('error', errMsg, true);
          if (!opts.quiet) showToast(errMsg, 'error');
          return;
        }
        if (res.data.partial) {
          var partErr = (res.data.collection && res.data.collection.error) || '';
          setInlineBanner('warning', I18N.t('nodes.partial_warning_banner', { error: partErr }), true);
          if (!opts.quiet) showToast(res.data.flash_message || I18N.t('nodes.flash_refresh_partial'), 'warn');
        } else {
          setInlineBanner('', '', false);
          if (!opts.quiet) showToast(res.data.flash_message || I18N.t('nodes.refresh_done_toast'), 'ok');
        }
      })
      .catch(function (err) {
        if (err && err.name === 'AbortError') return;
        finishBusy();
        setInlineBanner('error', I18N.t('nodes.flash_refresh_failed'), true);
        if (!opts.quiet) showToast(I18N.t('nodes.flash_refresh_failed'), 'error');
        applyFilterSortView();
      });
  }

  workspace.querySelectorAll('[data-nodes-refresh-btn], [data-nodes-retry-btn]').forEach(function (btn) {
    btn.addEventListener('click', function (e) {
      e.preventDefault();
      refreshNodes({ showSkeleton: !gridWrap || gridWrap.childElementCount === 0 });
    });
  });

  workspace.addEventListener('nodes:autorefresh', function (e) {
    e.preventDefault();
    refreshNodes({ quiet: true });
  });

  var nodeForm = q('#node-form, [data-node-discovery-form]');
  if (nodeForm) {
    nodeForm.addEventListener('submit', function (e) {
      e.preventDefault();
      refreshNodes({ form: nodeForm, showSkeleton: true });
    });
  }

  if (workspace.getAttribute('data-empty-state') === 'nosnap' && hasCreds && workspace.dataset.autoDiscovered !== '1') {
    workspace.dataset.autoDiscovered = '1';
    refreshNodes({ showSkeleton: true, quiet: false });
  }

  /* ── Config Viewer Modal ── */
  function openConfigViewer(nodeType, nodeName) {
    if (!configModal) return;
    var cq = function (sel) { return configModal.querySelector(sel); };
    var titleEl = cq('[data-config-modal-title]');
    var pathEl = cq('[data-config-modal-path]');
    var bodyEl = cq('[data-config-modal-body]');
    var cfgSearch = cq('[data-config-search]');
    var countEl = cq('[data-config-count]');
    var copyAllBtn = cq('[data-config-copy-all]');
    var copyAllText = cq('[data-config-copy-all-text]');
    var copyPathBtn = cq('[data-config-copy-path]');

    var configPath = '/opt/' + nodeName + '/.env';
    if (titleEl) titleEl.textContent = I18N.t('nodes.config_modal_title', { name: nodeName });
    if (pathEl) pathEl.textContent = configPath;
    if (cfgSearch) cfgSearch.value = '';
    if (countEl) countEl.textContent = '';

    if (copyPathBtn) {
      copyPathBtn.onclick = function (e) {
        e.preventDefault();
        e.stopPropagation();
        copyWithIconFeedback(copyPathBtn, function () { return (pathEl && pathEl.textContent) || configPath; });
      };
    }

    if (bodyEl) {
      bodyEl.textContent = '';
      bodyEl.appendChild(el('div', { class: 'config-loading' }, [
        el('span', { class: 'spinner spinner--sm' }),
        ' ' + I18N.t('nodes.config_modal_loading')
      ]));
    }
    openModal(configModal);

    fetch(nodesUrl + '/config?type=' + encodeURIComponent(nodeType) + '&name=' + encodeURIComponent(nodeName), {
      headers: { Accept: 'application/json' }
    })
      .then(readJSON)
      .then(function (res) {
        if (!bodyEl) return;
        bodyEl.textContent = '';
        if (!res.ok || !res.data) {
          var errMsg = (res.data && res.data.error) || ('HTTP ' + res.status);
          bodyEl.appendChild(el('p', { class: 'node-actions-note', text: I18N.t('nodes.config_modal_error', { error: errMsg }) }));
          return;
        }
        if (pathEl && res.data.config_path) {
          configPath = res.data.config_path;
          pathEl.textContent = configPath;
        }
        var entries = Array.isArray(res.data.entries) ? res.data.entries : [];
        if (!entries.length) {
          if (countEl) countEl.textContent = I18N.t('nodes.config_count', { count: 0 });
          bodyEl.appendChild(el('p', { class: 'empty-state', text: I18N.t('nodes.config_modal_empty', { name: nodeName }) }));
          return;
        }

        if (copyAllBtn) {
          copyAllBtn.onclick = function (e) {
            e.preventDefault();
            e.stopPropagation();
            var text = entries.map(function (entry) {
              return entry.redacted ? ('# ' + entry.key + '=(redacted secret)') : ((entry.key || '') + '=' + (entry.value || ''));
            }).join('\n');
            if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(text);
            if (copyAllText) copyAllText.textContent = I18N.t('nodes.config_copied');
            copyAllBtn.classList.add('is-copied');
            setTimeout(function () {
              if (copyAllText) copyAllText.textContent = I18N.t('nodes.config_copy_all');
              copyAllBtn.classList.remove('is-copied');
            }, 1800);
          };
        }

        function renderRows(filterQuery) {
          bodyEl.textContent = '';
          var needle = (filterQuery || '').trim().toLowerCase();
          var matched = needle ? entries.filter(function (entry) {
            return (entry.key || '').toLowerCase().indexOf(needle) !== -1 ||
              (entry.value || '').toLowerCase().indexOf(needle) !== -1;
          }) : entries;

          if (countEl) {
            countEl.textContent = needle
              ? I18N.t('nodes.config_count_filtered', { shown: matched.length, total: entries.length })
              : I18N.t('nodes.config_count', { count: entries.length });
          }

          if (!matched.length) {
            var empty = el('div', { class: 'config-empty' }, [
              iconEl('search-x', 'config-empty__icon'),
              el('p', { class: 'config-empty__text', text: I18N.t('common.no_results') || 'No matching properties' })
            ]);
            bodyEl.appendChild(empty);
            renderIcons(empty);
            return;
          }

          var wrap = el('div', { class: 'config-list' });
          var frag = document.createDocumentFragment();
          matched.forEach(function (entry) {
            var valChildren = entry.redacted
              ? [
                  el('span', { class: 'config-secret-pill' }, [iconEl('lock', 'config-secret-pill__icon'), ' ' + I18N.t('nodes.config_secret_badge')]),
                  el('span', { class: 'config-secret-hint', text: I18N.t('nodes.config_secret_hint') })
                ]
              : [
                  el('code', { class: 'config-val-code', text: entry.value || '' }),
                  el('button', {
                    type: 'button',
                    class: 'config-copy-btn',
                    title: I18N.t('nodes.config_copy_value'),
                    'aria-label': I18N.t('nodes.config_copy_value'),
                    on: {
                      click: function (e) {
                        e.stopPropagation();
                        copyWithIconFeedback(e.currentTarget, entry.value || '');
                      }
                    }
                  }, iconEl('copy'))
                ];

            frag.appendChild(el('div', { class: 'config-item' + (entry.redacted ? ' config-item--redacted' : '') }, [
              el('div', { class: 'config-item__key' }, el('code', { class: 'config-key-code', text: entry.key || '' })),
              el('div', { class: 'config-item__val' }, valChildren)
            ]));
          });
          wrap.appendChild(frag);
          bodyEl.appendChild(wrap);
          renderIcons(wrap);
        }

        renderRows('');
        if (cfgSearch) cfgSearch.oninput = function () { renderRows(cfgSearch.value); };
      })
      .catch(function (err) {
        if (!bodyEl) return;
        bodyEl.textContent = '';
        bodyEl.appendChild(el('p', { class: 'node-actions-note', text: I18N.t('nodes.config_modal_error', { error: String(err) }) }));
      });
  }

  /* ── Action Confirm Modal + SSE Drawer Execution ── */
  if (confirmModal) {
    var proceedBtn = confirmModal.querySelector('[data-confirm-proceed]');
    if (proceedBtn) {
      proceedBtn.addEventListener('click', function () {
        var fn = pendingConfirmAction;
        pendingConfirmAction = null;
        closeModal(confirmModal, true);
        if (fn) fn();
      });
    }
    confirmModal.addEventListener('modal:beforeclose', function () { pendingConfirmAction = null; });
  }

  function dispatchNodeAction(nodeType, nodeName, actionKey) {
    var csrf = getCSRF(workspace);
    var body = new URLSearchParams();
    if (csrf) body.set('_csrf_token', csrf);
    body.set('node_type', nodeType);
    body.set('node_name', nodeName);
    body.set('action', actionKey);

    var headers = {
      Accept: 'application/json',
      'X-Requested-With': 'fetch',
      'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8'
    };
    if (csrf) headers['X-CSRF-Token'] = csrf;

    fetch(nodesUrl + '/actions', { method: 'POST', headers: headers, body: body.toString() })
      .then(readJSON)
      .then(function (res) {
        if (!res.ok || !res.data || !res.data.events_url) {
          showToast((res.data && res.data.error) || I18N.t('nodes.action_failed'), 'error');
          return;
        }
        if (logDrawer && typeof logDrawer._openLogDrawer === 'function') {
          logDrawer._openLogDrawer(res.data.events_url, true, res.data.label || (nodeName + ' — ' + actionKey), function (failed) {
            if (!failed && isMutatingAction(actionKey)) {
              trackTimeout(function () { refreshNodes({ quiet: true }); }, 900);
            }
          });
        }
      })
      .catch(function () { showToast(I18N.t('nodes.action_failed'), 'error'); });
  }

  /* ── Lazy Deploy Wizard Loader ── */
  function ensureDeployWizard() {
    if (!deployModal) return Promise.resolve(null);
    if (!deployWizardPromise) {
      deployWizardPromise = import('./nodes-deploy.js').then(function (mod) {
        return mod.wireDeployWizard(deployModal, workspace, owningPane, function () {
          refreshNodes({ quiet: false });
        });
      });
    }
    return deployWizardPromise;
  }

  if (deployModal && (deployModal.classList.contains('is-open') || deployModal.hasAttribute('data-modal-open-initial'))) {
    ensureDeployWizard();
  }

  /* Delegate config/quick-logs & deploy-modal opener clicks across SSR + re-rendered cards */
  workspace.addEventListener('click', function (e) {
    var target = e.target && e.target.closest ? e.target : null;
    if (!target) return;

    var cfgBtn = target.closest('[data-node-config]');
    if (cfgBtn) {
      e.preventDefault();
      closeAllMenus(null);
      openConfigViewer(cfgBtn.getAttribute('data-node-type') || '', cfgBtn.getAttribute('data-node-name') || '');
      return;
    }

    var quickLogsBtn = target.closest('[data-node-quick-logs]');
    if (quickLogsBtn) {
      e.preventDefault();
      dispatchNodeAction(quickLogsBtn.getAttribute('data-node-type') || '', quickLogsBtn.getAttribute('data-node-name') || '', 'logs');
      return;
    }

    var preselectBtn = target.closest('[data-engine-preselect]');
    if (preselectBtn) {
      var eng = preselectBtn.getAttribute('data-engine-preselect');
      ensureDeployWizard().then(function (wiz) { if (wiz) wiz.preselectEngine(eng); });
      return;
    }

    var deployOpenBtn = target.closest('[data-modal-open="deploy-node-modal"]');
    if (deployOpenBtn) {
      ensureDeployWizard().then(function (wiz) { if (wiz) wiz.openDefault(); });
    }
  });

  workspace.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form || !form.classList || !form.classList.contains('action-menu__form')) return;
    e.preventDefault();
    closeAllMenus(null);

    var submitter = e.submitter || document.activeElement;
    var actionKey = (submitter && submitter.value) || '';
    var actionLabel = (submitter && submitter.textContent ? submitter.textContent.trim() : actionKey);
    var nodeType = (form.elements['node_type'] && form.elements['node_type'].value) || '';
    var nodeName = (form.elements['node_name'] && form.elements['node_name'].value) || '';
    if (!actionKey || !nodeType || !nodeName) return;

    var isDanger = !!(submitter && (submitter.classList.contains('action-menu__item--danger') || submitter.hasAttribute('data-confirm')));
    if (isDanger && confirmModal) {
      var msgEl = confirmModal.querySelector('[data-confirm-message]');
      if (msgEl) msgEl.textContent = I18N.t('nodes.confirm_action_danger', { action: actionLabel, name: nodeName });
      pendingConfirmAction = function () { dispatchNodeAction(nodeType, nodeName, actionKey); };
      openModal(confirmModal);
      return;
    }
    dispatchNodeAction(nodeType, nodeName, actionKey);
  });
}

/* ── Card DOM Builder (Synced With nodes_card.gohtml) ─────────────────────── */
function buildNodeSlotElement(snap, serverId, csrfToken) {
  var searchText = [snap.name, snap.node_type, snap.type_label, snap.version, snap.service_port, snap.api_port, snap.protocol]
    .concat(snap.active_ports || []).join(' ');

  var engineIcon = snap.node_type === 'pasarguard-node' ? 'container' : (snap.node_type === 'rebecca-node' ? 'package' : 'help-circle');
  var modeIcon = snap.install_mode === 'docker' ? 'container' : (snap.install_mode === 'binary' ? 'package' : 'help-circle');

  var bodyChildren = [];
  if (snap.actions_enabled) {
    bodyChildren.push(el('div', { class: 'node-card__quick-tools' }, [
      el('button', {
        type: 'button', class: 'btn btn--ghost btn--xs',
        'data-node-quick-logs': true, 'data-node-type': snap.node_type, 'data-node-name': snap.name
      }, [iconEl('terminal', 'icon-in-button'), ' ' + I18N.t('nodes.card_quick_logs')]),
      el('button', {
        type: 'button', class: 'btn btn--ghost btn--xs',
        'data-node-config': true, 'data-node-type': snap.node_type, 'data-node-name': snap.name
      }, [iconEl('file-code', 'icon-in-button'), ' ' + I18N.t('nodes.action_view_config')])
    ]));
  }

  bodyChildren.push(el('div', { class: 'node-facts' }, [
    buildFact('tag', I18N.t('common.version'), snap.version || '-', ''),
    buildFact('plug', I18N.t('nodes.service_port'), snap.service_port && snap.service_port !== '-' ? snap.service_port : '-', 'node-port node-port--service'),
    buildFact('webhook', I18N.t('nodes.api_port'), snap.api_port && snap.api_port !== '-' ? snap.api_port : '-', 'node-port node-port--api'),
    buildFact('folder', I18N.t('nodes.data_directory'), snap.data_dir || '-', 'node-path', true)
  ]));

  if (snap.node_type === 'pasarguard-node') {
    bodyChildren.push(el('div', { class: 'node-section node-credentials' }, [
      el('span', { class: 'node-section__label' }, [iconEl('key-round'), ' ' + I18N.t('nodes.credentials')]),
      snap.actions_enabled
        ? [
            el('button', {
              type: 'button', class: 'btn btn--ghost btn--sm node-credentials__btn',
              'data-node-credentials': true,
              'data-url': '/servers/' + serverId + '/nodes/credentials?type=' + encodeURIComponent(snap.node_type) + '&name=' + encodeURIComponent(snap.name)
            }, [iconEl('key-round', 'icon-in-button'), ' ' + I18N.t('nodes.show_api_cert')]),
            el('div', { class: 'node-credentials__out', hidden: true })
          ]
        : el('p', { class: 'node-actions-note' }, [iconEl('key-round', 'icon-in-button'), ' ' + I18N.t('nodes.store_creds_view')])
    ]));
  }

  if (snap.active_ports && snap.active_ports.length) {
    bodyChildren.push(buildChipsSection('network', I18N.t('nodes.active_ports'), snap.active_ports, function (p) {
      return 'node-port' + (p === snap.api_port ? ' node-port--api' : (p === snap.service_port ? ' node-port--service' : ''));
    }));
  }
  if (snap.xray_ports && snap.xray_ports.length) {
    bodyChildren.push(buildChipsSection('arrow-left-right', I18N.t('nodes.xray_ports'), snap.xray_ports, function () { return 'node-port node-port--xray'; }));
  }
  if (snap.dependencies && snap.dependencies.length) {
    bodyChildren.push(buildChipsSection('package-check', I18N.t('nodes.dependencies'), snap.dependencies, function (d) {
      return 'node-dep' + (String(d).slice(-8) === ':missing' ? ' node-dep--missing' : '');
    }));
  }
  if (snap.evidence && snap.evidence.length) {
    bodyChildren.push(el('div', { class: 'node-evidence collapsible' }, [
      el('button', {
        type: 'button', class: 'collapsible__toggle',
        'data-collapse-key': 'evidence_' + snap.node_type + '_' + snap.name,
        'data-collapse-default': 'closed'
      }, [
        iconEl('chevron-right', 'collapsible__icon'),
        el('span', { text: I18N.t('nodes.evidence') }),
        el('span', { class: 'node-evidence__count', text: String(snap.evidence.length) })
      ]),
      el('div', { class: 'collapsible__content' }, el('ul', { class: 'node-evidence__list' },
        snap.evidence.map(function (item) {
          return el('li', null, [iconEl('check', 'node-evidence__check'), el('span', { text: item })]);
        })
      ))
    ]));
  }
  bodyChildren.push(el('div', { class: 'node-card__footer' }, el('span', null, [iconEl('calendar'), ' ' + (snap.collected_at || '')])));

  var uptimeBlock = (Array.isArray(snap.uptime_days) && snap.uptime_days.length) ? el('div', { class: 'node-uptime-block' }, [
    el('div', { class: 'node-uptime-block__head' }, [
      el('span', { class: 'node-uptime-block__title' }, [iconEl('activity'), ' ' + I18N.t('nodes.uptime_30d')]),
      el('span', { class: 'node-uptime-block__meta' }, [
        snap.uptime_detail && el('span', { class: 'node-uptime-block__checks', title: I18N.t('nodes.uptime_checks', { detail: snap.uptime_detail }), text: snap.uptime_detail }),
        el('span', { class: 'node-uptime-block__pct', text: snap.uptime_known ? (snap.uptime_pct + '%') : '—' })
      ])
    ]),
    el('div', { class: 'node-uptime-strip', role: 'group', 'aria-label': I18N.t('nodes.uptime_strip_aria') },
      snap.uptime_days.map(function (day) {
        var label = day.tooltip || day.date;
        return el('button', { type: 'button', class: 'node-uptime-bar node-uptime-bar--' + (day.status || 'empty'), 'aria-label': label }, [
          el('span', { class: 'node-uptime-bar__fill' }),
          el('span', { class: 'node-uptime-bar__tip', 'aria-hidden': 'true' }, el('span', { class: 'node-uptime-bar__tip-date', text: label }))
        ]);
      })
    ),
    el('div', { class: 'node-uptime-block__foot', 'data-uptime-foot': true, hidden: true },
      el('span', { 'data-uptime-active-tip': true, 'aria-live': 'polite', hidden: true })
    )
  ]) : null;

  var card = el('article', { class: 'node-card node-card--' + snap.node_type + ' collapsible' }, [
    el('header', { class: 'node-card__head' }, [
      el('span', { class: 'node-card__icon' }, iconEl(engineIcon)),
      el('div', { class: 'node-card__title' }, [
        el('div', { class: 'node-card__title-row' }, [
          el('span', { class: 'node-card__name', text: snap.name }),
          snap.service_port && snap.service_port !== '-' && el('code', { class: 'node-card__port-tag', text: ':' + snap.service_port })
        ]),
        el('span', { class: 'node-card__type', text: snap.type_label || snap.node_type })
      ]),
      el('div', { class: 'node-card__head-end' }, [
        el('button', {
          type: 'button', class: 'collapsible__toggle node-card__body-toggle',
          'data-collapse-key': 'node_body_' + snap.node_type + '_' + snap.name,
          'data-collapse-default': 'closed',
          'aria-label': I18N.t('nodes.details_aria', { name: snap.name }),
          title: I18N.t('nodes.details_aria', { name: snap.name })
        }, iconEl('chevron-right', 'collapsible__icon')),
        buildActionMenuDOM(snap, serverId, csrfToken)
      ])
    ]),
    el('div', { class: 'node-status-row' }, [
      el('span', { class: 'node-pill node-pill--health-' + snap.health_status }, [el('span', { class: 'node-pill__dot' }), I18N.t(healthLabelKey(snap.health_status))]),
      el('span', { class: 'node-pill node-pill--mode-' + snap.install_mode }, [iconEl(modeIcon), snap.install_mode || '-']),
      snap.protocol && snap.protocol !== '-' && el('span', { class: 'node-pill node-pill--proto-' + snap.protocol }, [iconEl(snap.protocol === 'grpc' ? 'layers' : 'globe'), snap.protocol]),
      el('div', { class: 'node-confidence node-confidence--' + snap.confidence, title: I18N.t('nodes.confidence_title', { level: snap.confidence }) }, [
        el('span', { class: 'node-confidence__meter' }, [el('span', { class: 'node-confidence__seg' }), el('span', { class: 'node-confidence__seg' }), el('span', { class: 'node-confidence__seg' })]),
        el('span', { class: 'node-confidence__label', text: snap.confidence })
      ])
    ]),
    el('div', { class: 'node-telemetry' }, [
      el('span', { class: 'node-telemetry__item', title: I18N.t('nodes.last_seen') }, [iconEl('clock'), el('span', { class: 'node-telemetry__val--ltr', text: snap.last_seen_at || I18N.t('nodes.never_seen') })]),
      snap.latency_ms > 0 && el('span', { class: 'node-telemetry__item node-telemetry__item--latency', title: I18N.t('nodes.latency_label') }, [
        iconEl('gauge'), el('span', { class: 'node-telemetry__val--ltr', text: snap.latency_label || I18N.t('nodes.latency_ms', { ms: snap.latency_ms }) })
      ])
    ]),
    uptimeBlock,
    el('div', { class: 'collapsible__content node-card__body' }, bodyChildren)
  ]);

  return el('div', {
    class: 'node-card-slot',
    'data-node-slot': true,
    'data-node-name': snap.name || '',
    'data-node-type': snap.node_type || '',
    'data-node-health': snap.health_status || 'unknown',
    'data-node-uptime': snap.uptime_known ? String(snap.uptime_pct) : '-1',
    'data-node-latency': String(snap.latency_ms || 0),
    'data-node-search': searchText
  }, card);
}

function buildActionMenuDOM(snap, serverId, csrfToken) {
  var hiddenField = function (n, v) { return el('input', { type: 'hidden', name: n, value: v }); };
  var panelBody = snap.actions_enabled
    ? el('form', { class: 'action-menu__form', method: 'post', action: '/servers/' + serverId + '/nodes/actions' }, [
        csrfToken && hiddenField('_csrf_token', csrfToken),
        hiddenField('node_type', snap.node_type),
        hiddenField('node_name', snap.name),
        (snap.actions || []).map(function (act) {
          return el('button', {
            type: 'submit',
            class: 'action-menu__item' + (act.danger ? ' action-menu__item--danger' : ''),
            name: 'action',
            value: act.key,
            role: 'menuitem',
            'data-confirm': act.danger ? I18N.t('nodes.action_confirm', { action: act.label, name: snap.name }) : null
          }, [iconEl(act.icon, 'icon-in-button'), ' ' + act.label]);
        }),
        el('button', {
          type: 'button', class: 'action-menu__item', role: 'menuitem',
          'data-node-config': true, 'data-node-type': snap.node_type, 'data-node-name': snap.name
        }, [iconEl('file-code', 'icon-in-button'), ' ' + I18N.t('nodes.action_view_config')])
      ])
    : el('span', { class: 'action-menu__note' }, [iconEl('key-round'), ' ' + I18N.t('nodes.store_creds_manage')]);

  return el('div', { class: 'action-menu action-menu--below node-card__menu', 'data-action-menu': true }, [
    el('button', {
      type: 'button',
      class: 'btn btn--ghost btn--sm action-menu__toggle node-card__menu-toggle',
      'aria-haspopup': 'true', 'aria-expanded': 'false',
      'aria-label': I18N.t('nodes.actions_aria', { name: snap.name })
    }, iconEl('ellipsis-vertical')),
    el('div', { class: 'action-menu__panel', role: 'menu' }, [
      el('div', { class: 'action-menu__handle', 'aria-hidden': 'true' }),
      el('div', { class: 'action-menu__head' }, [
        el('span', { class: 'action-menu__caption' }, [iconEl('wrench'), ' ' + I18N.t('nodes.node_actions')]),
        el('button', {
          type: 'button', class: 'action-menu__close', 'data-action-menu-close': true,
          'aria-label': I18N.t('nodes.action_menu_close') || 'Close actions menu'
        }, iconEl('x'))
      ]),
      el('div', { class: 'action-menu__divider', role: 'separator' }),
      panelBody
    ])
  ]);
}

function initNodes(root) {
  root = root || document;
  wireNodeCredentials(root);
  root.querySelectorAll('[data-nodes-workspace]').forEach(wireNodesWorkspace);
}

export { initNodes, iconEl, readJSON, buildFact, buildCredBlock };
