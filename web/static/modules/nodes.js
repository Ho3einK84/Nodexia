/* Nodexia Nodes workspace controller (native ES module — no bundler, no npm).
 *
 * Pane-scoped controller for the Nodes feature:
 *   - Live CSRF POST discovery refresh + skeleton shimmer + inline retry + toasts
 *   - Automatic first-visit discovery when snapshot is missing & SSH creds exist
 *   - Client-side instant search, engine/status filtering, sort & grid/list toggle
 *   - Interactive 30-day uptime strip with mobile-tap & keyboard tooltips
 *   - Safe per-node action execution -> confirm modal -> live SSE log drawer
 *   - SSH Config Viewer modal (.env keys/redacted secrets) & live credentials
 *   - 3-step Deploy Wizard + Step 3 live stage/log execution, cancel & summary
 */
import { I18N, renderIcons, registerCleanup } from './core.js';
import { initSecrets, initCopyTargets, initCollapsibles } from './ui.js';
import { openModal, closeModal, closeAllMenus, initActionMenus } from './modals.js';

var NODE_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$/;
var SAN_ENTRY_RE = /^[a-zA-Z0-9.*:-]+$/;
var MAX_INSTALL_LOG_BYTES = 512 * 1024;
var credIdCounter = 0;

function el(tag, className, text) {
  var node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = String(text);
  return node;
}

function iconEl(name, className) {
  var ic = document.createElement('i');
  ic.setAttribute('data-lucide', name);
  if (className) ic.className = className;
  ic.setAttribute('aria-hidden', 'true');
  return ic;
}

function getWorkspaceCSRF(workspace) {
  var attr = workspace.getAttribute('data-csrf-token');
  if (attr) return attr;
  var inp = workspace.querySelector('input[name="_csrf_token"]');
  return inp ? inp.value : '';
}

function isJSONResponse(resp) {
  var ct = (resp.headers && resp.headers.get('content-type')) || '';
  return ct.toLowerCase().indexOf('application/json') !== -1;
}

function readJSON(resp) {
  if (!isJSONResponse(resp)) {
    return Promise.resolve({ ok: false, status: resp.status, data: null });
  }
  return resp.json().then(function (data) {
    return { ok: resp.ok, status: resp.status, data: data };
  }).catch(function () {
    return { ok: false, status: resp.status, data: null };
  });
}

function healthLabelKey(health) {
  switch (String(health || '').toLowerCase()) {
    case 'running': return 'nodes.health_running';
    case 'degraded': return 'nodes.health_degraded';
    case 'restarting':
    case 'activating': return 'nodes.health_restarting';
    case 'stopped':
    case 'inactive': return 'nodes.health_stopped';
    case 'detected': return 'nodes.health_detected';
    case 'not_detected': return 'nodes.health_not_detected';
    default: return 'nodes.health_unknown';
  }
}

function isMutatingAction(actionKey) {
  return ['start', 'stop', 'restart', 'update', 'core-update', 'renew-cert', 'reinstall', 'uninstall'].indexOf(String(actionKey || '')) !== -1;
}

/* ── On-demand PasarGuard node credentials ───────────────────────────────── */
function wireNodeCredentials(root) {
  (root || document).querySelectorAll('[data-node-credentials]').forEach(function (btn) {
    if (btn.dataset.credReady === '1') return;
    btn.dataset.credReady = '1';
    var section = btn.parentNode;
    var out = section ? section.querySelector('.node-credentials__out') : null;

    var setButtonState = function (iconName, labelText, disabled) {
      btn.disabled = !!disabled;
      btn.textContent = '';
      btn.appendChild(iconEl(iconName, 'icon-in-button'));
      btn.appendChild(document.createTextNode(' ' + labelText));
      renderIcons(btn);
    };

    var showLabel = I18N.t('js.cred.show');
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
            showCredError(out, setButtonState, showLabel, (res.data && res.data.error) || I18N.t('js.cred.load_error'));
            return;
          }
          renderCredBlocks(out, setButtonState, res.data);
        })
        .catch(function () {
          btn.disabled = false;
          showCredError(out, setButtonState, showLabel, I18N.t('js.cred.load_error'));
        });
    });
  });
}

function showCredError(out, setButtonState, showLabel, message) {
  if (out) {
    out.hidden = false;
    out.textContent = '';
    out.appendChild(el('p', 'node-actions-note', message));
  }
  setButtonState('key-round', showLabel, false);
}

function renderCredBlocks(out, setButtonState, data) {
  if (!out) return;
  out.textContent = '';
  out.hidden = false;
  var name = String(data.node_name || 'node').replace(/[^a-zA-Z0-9._-]/g, '') || 'node';
  var seq = ++credIdCounter;

  if (data.has_api_key) {
    out.appendChild(buildCredBlock('key-round', I18N.t('js.cred.api_key'), 'cred-' + seq + '-key-' + name, data.api_key, true));
  } else {
    out.appendChild(el('p', 'node-actions-note', I18N.t('js.cred.api_key_missing', { name: name })));
  }
  if (data.has_cert) {
    out.appendChild(buildCredBlock('shield-check', I18N.t('js.cred.ssl_cert'), 'cred-' + seq + '-cert-' + name, data.certificate, false));
  }

  initSecrets(out);
  initCopyTargets(out);
  setButtonState('eye-off', I18N.t('js.cred.hide'), false);
}

function buildCredBlock(iconName, label, id, value, mask) {
  var section = el('div', 'node-section node-cred-block');
  var head = el('div', 'node-section__head');

  var lab = el('span', 'node-section__label');
  lab.appendChild(iconEl(iconName));
  lab.appendChild(document.createTextNode(' ' + label));

  var copyBtn = el('button', 'btn btn--ghost btn--sm copy-inline');
  copyBtn.type = 'button';
  copyBtn.setAttribute('data-copy-target', '#' + id);
  copyBtn.setAttribute('aria-label', I18N.t('js.copy.aria_label', { label: label }));
  copyBtn.appendChild(iconEl('copy'));
  copyBtn.appendChild(document.createTextNode(' ' + I18N.t('js.copy.label')));

  head.appendChild(lab);
  head.appendChild(copyBtn);

  var pre = el('pre', 'output-block node-secret-block' + (mask ? '' : ' scrollable'), value || '');
  pre.id = id;
  pre.setAttribute('data-no-auto-copy', '');
  if (mask) pre.setAttribute('data-secret', '');

  section.appendChild(head);
  section.appendChild(pre);
  return section;
}

/* ── Main Nodes Workspace Controller ────────────────────────────────────── */
function wireNodesWorkspace(workspace) {
  if (!workspace || workspace.dataset.nodesReady === '1') return;
  workspace.dataset.nodesReady = '1';

  var owningPane = workspace.closest('.tab-pane') || workspace;
  var serverId = workspace.getAttribute('data-server-id') || '';
  var nodesUrl = workspace.getAttribute('data-nodes-url') || ('/servers/' + serverId + '/nodes');
  var hasCreds = workspace.getAttribute('data-has-creds') === '1';

  var gridWrap = workspace.querySelector('[data-node-grid]');
  var overviewCard = workspace.querySelector('[data-nodes-overview]');
  var toolbarEl = workspace.querySelector('[data-nodes-toolbar]');
  var skeletonEl = workspace.querySelector('[data-nodes-skeleton]');
  var inlineBanner = workspace.querySelector('[data-nodes-inline-banner]');
  var emptyNoneEl = workspace.querySelector('[data-nodes-empty="none"]');
  var emptyNosnapEl = workspace.querySelector('[data-nodes-empty="nosnap"]');
  var filterEmptyEl = workspace.querySelector('[data-nodes-filter-empty]');
  var toastRegion = workspace.querySelector('[data-nodes-toast-region]');

  var searchInput = workspace.querySelector('[data-nodes-search]');
  var searchClearBtn = workspace.querySelector('[data-nodes-search-clear]');
  var engineFilter = workspace.querySelector('[data-nodes-filter-engine]');
  var statusFilter = workspace.querySelector('[data-nodes-filter-status]');
  var sortSelect = workspace.querySelector('[data-nodes-sort]');
  var viewGridBtn = workspace.querySelector('[data-nodes-view="grid"]');
  var viewListBtn = workspace.querySelector('[data-nodes-view="list"]');
  var filterResetBtn = workspace.querySelector('[data-nodes-filter-reset]');

  var logDrawer = workspace.querySelector('[data-log-drawer]');
  var configModal = workspace.querySelector('[data-nodes-config-modal]');
  var confirmModal = workspace.querySelector('[data-nodes-confirm-modal]');
  var deployModal = workspace.querySelector('[data-deploy-modal]');

  var activeTimers = new Set();
  var refreshAbort = null;
  var pendingConfirmAction = null;

  function trackTimeout(fn, ms) {
    var id = setTimeout(function () {
      activeTimers.delete(id);
      fn();
    }, ms);
    activeTimers.add(id);
    return id;
  }

  registerCleanup(owningPane, function () {
    activeTimers.forEach(function (id) { clearTimeout(id); });
    activeTimers.clear();
    if (refreshAbort) {
      try { refreshAbort.abort(); } catch (err) {}
      refreshAbort = null;
    }
    pendingConfirmAction = null;
  });

  function showToast(kind, message) {
    if (!toastRegion || !message) return;
    var toast = el('div', 'nodes-toast nodes-toast--' + (kind || 'success'));
    toast.setAttribute('role', 'status');
    var icName = kind === 'error' ? 'alert-circle' : (kind === 'warning' ? 'alert-triangle' : 'check-circle-2');
    toast.appendChild(iconEl(icName));
    toast.appendChild(el('span', '', message));
    toastRegion.appendChild(toast);
    renderIcons(toast);
    trackTimeout(function () {
      if (toast.parentNode) toast.remove();
    }, 4200);
  }

  function setInlineBanner(kind, message, showRetry) {
    if (!inlineBanner) return;
    if (!message) {
      inlineBanner.hidden = true;
      return;
    }
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
    switch (String(health || '').toLowerCase()) {
      case 'running': return 0;
      case 'degraded':
      case 'restarting':
      case 'activating':
      case 'detected': return 1;
      case 'stopped':
      case 'inactive': return 2;
      default: return 3;
    }
  }

  function matchesStatusFilter(health, filterVal) {
    if (!filterVal || filterVal === 'all') return true;
    var low = String(health || '').toLowerCase();
    if (filterVal === 'running') return low === 'running';
    if (filterVal === 'degraded') return low === 'degraded' || low === 'restarting' || low === 'activating' || low === 'detected';
    if (filterVal === 'stopped') return low === 'stopped' || low === 'inactive';
    return low !== 'running' && low !== 'degraded' && low !== 'restarting' && low !== 'activating' && low !== 'stopped' && low !== 'inactive';
  }

  function applyFilterSortView() {
    if (!gridWrap) return;
    var slots = Array.prototype.slice.call(gridWrap.querySelectorAll('[data-node-slot]'));
    var q = searchInput ? searchInput.value.trim().toLowerCase() : '';
    var eng = engineFilter ? engineFilter.value : 'all';
    var st = statusFilter ? statusFilter.value : 'all';
    var sortBy = sortSelect ? sortSelect.value : 'name';

    if (searchClearBtn) searchClearBtn.hidden = !q;

    slots.sort(function (a, b) {
      if (sortBy === 'status') {
        var sr = statusRank(a.getAttribute('data-node-health')) - statusRank(b.getAttribute('data-node-health'));
        if (sr !== 0) return sr;
      } else if (sortBy === 'uptime') {
        var ua = parseInt(a.getAttribute('data-node-uptime') || '-1', 10);
        var ub = parseInt(b.getAttribute('data-node-uptime') || '-1', 10);
        if (ub !== ua) return ub - ua;
      } else if (sortBy === 'latency') {
        var la = parseInt(a.getAttribute('data-node-latency') || '0', 10);
        var lb = parseInt(b.getAttribute('data-node-latency') || '0', 10);
        var na = la > 0 ? la : 999999;
        var nb = lb > 0 ? lb : 999999;
        if (na !== nb) return na - nb;
      }
      return String(a.getAttribute('data-node-name') || '').localeCompare(String(b.getAttribute('data-node-name') || ''));
    });

    var visibleCount = 0;
    slots.forEach(function (slot) {
      gridWrap.appendChild(slot);
      var typeVal = slot.getAttribute('data-node-type') || '';
      var healthVal = slot.getAttribute('data-node-health') || '';
      var textVal = (slot.getAttribute('data-node-search') || '').toLowerCase();
      var ok = (!q || textVal.indexOf(q) !== -1) &&
        (eng === 'all' || typeVal === eng) &&
        matchesStatusFilter(healthVal, st);
      slot.hidden = !ok;
      if (ok) visibleCount++;
    });

    if (filterEmptyEl) {
      filterEmptyEl.hidden = !(slots.length > 0 && visibleCount === 0);
    }
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
  if (engineFilter) engineFilter.addEventListener('change', applyFilterSortView);
  if (statusFilter) statusFilter.addEventListener('change', applyFilterSortView);
  if (sortSelect) sortSelect.addEventListener('change', applyFilterSortView);
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

  /* ── Interactive 30-day Uptime Tooltip Tap / Focus Helper ── */
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
    workspace.querySelectorAll('[data-node-fleet]').forEach(function (elFleet) {
      elFleet.setAttribute('data-total', String(fleet.total || 0));
    });
    setText('[data-kpi="online"]', String(fleet.running || 0));
    setText('[data-kpi="offline_degraded"]', String(offlineCount));
    setText('[data-kpi="pg"]', String(fleet.pasarguard_count || 0));
    setText('[data-kpi="rebecca"]', String(fleet.rebecca_count || 0));
    setText('[data-kpi="deploying"]', String(deployingCount));
    setText('[data-kpi="ports"]', String(fleet.ports || 0));
    setText('[data-kpi="health_pct"]', fleet.health_known ? (fleet.health_pct + '%') : '—');

    workspace.querySelectorAll('[data-kpi-tile="offline"]').forEach(function (tile) {
      tile.classList.toggle('nodes-kpi--crit', offlineCount > 0);
      tile.classList.toggle('nodes-kpi--muted', offlineCount === 0);
    });
    workspace.querySelectorAll('[data-kpi-tile="deploying"]').forEach(function (tile) {
      tile.classList.toggle('nodes-kpi--info', deployingCount > 0);
      tile.classList.toggle('nodes-kpi--muted', deployingCount === 0);
    });
    workspace.querySelectorAll('[data-kpi-progress]').forEach(function (prog) {
      prog.value = fleet.health_known ? Number(fleet.health_pct || 0) : 0;
    });
    var ports = Array.isArray(fleet.port_list) ? fleet.port_list : [];
    workspace.querySelectorAll('[data-kpi-port-list]').forEach(function (wrap) {
      wrap.textContent = '';
      ports.forEach(function (p) {
        wrap.appendChild(el('code', 'nodes-port-badge', ':' + p));
      });
    });
    workspace.querySelectorAll('[data-kpi-ports-bar]').forEach(function (bar) {
      bar.hidden = ports.length === 0;
    });
    if (collection && collection.collected_at) {
      setText('[data-last-sweep]', collection.collected_at);
    }
    if (collection && collection.last_sweep_iso) {
      workspace.setAttribute('data-last-sweep-iso', collection.last_sweep_iso);
    }
  }

  function renderSnapshotsFromJSON(snapshots, fleet) {
    if (!gridWrap) return;
    gridWrap.textContent = '';
    var list = Array.isArray(snapshots) ? snapshots : [];
    var csrfToken = getWorkspaceCSRF(workspace);

    list.forEach(function (snap) {
      if (!snap || snap.node_type === 'none') return;
      gridWrap.appendChild(buildNodeSlotElement(snap, serverId, csrfToken));
    });

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
    renderIcons(workspace);
  }

  /* ── Refresh Nodes (CSRF POST -> JSON) ── */
  function refreshNodes(opts) {
    opts = opts || {};
    if (refreshAbort) {
      try { refreshAbort.abort(); } catch (err) {}
    }
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

    var body = new URLSearchParams();
    var csrf = getWorkspaceCSRF(workspace);
    if (csrf) body.set('_csrf_token', csrf);
    if (opts.form) {
      var fd = new FormData(opts.form);
      fd.forEach(function (val, key) {
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

    return fetch(nodesUrl, fetchOpts)
      .then(readJSON)
      .then(function (res) {
        if (refreshAbort === controller) refreshAbort = null;
        if (skeletonEl) skeletonEl.hidden = true;
        refreshBtns.forEach(function (b) { b.disabled = false; b.classList.remove('is-busy'); });

        if (!res.data) {
          setInlineBanner('error', I18N.t('nodes.flash_refresh_failed'), true);
          if (!opts.quiet) showToast('error', I18N.t('nodes.flash_refresh_failed'));
          applyFilterSortView();
          return;
        }

        if (res.data.fleet) updateFleetKPI(res.data.fleet, res.data.collection);
        if (Array.isArray(res.data.snapshots)) {
          renderSnapshotsFromJSON(res.data.snapshots, res.data.fleet);
        } else {
          applyFilterSortView();
        }

        if (!res.ok || !res.data.ok) {
          var errMsg = res.data.flash_message || res.data.error || I18N.t('nodes.flash_refresh_failed');
          setInlineBanner('error', errMsg, true);
          if (!opts.quiet) showToast('error', errMsg);
          return;
        }

        if (res.data.partial) {
          var partErr = (res.data.collection && res.data.collection.error) || '';
          setInlineBanner('warning', I18N.t('nodes.partial_warning_banner', { error: partErr }), true);
          if (!opts.quiet) showToast('warning', res.data.flash_message || I18N.t('nodes.flash_refresh_partial'));
        } else {
          setInlineBanner('', '', false);
          if (!opts.quiet) showToast('success', res.data.flash_message || I18N.t('nodes.refresh_done_toast'));
        }
      })
      .catch(function (err) {
        if (err && err.name === 'AbortError') return;
        if (refreshAbort === controller) refreshAbort = null;
        if (skeletonEl) skeletonEl.hidden = true;
        refreshBtns.forEach(function (b) { b.disabled = false; b.classList.remove('is-busy'); });
        setInlineBanner('error', I18N.t('nodes.flash_refresh_failed'), true);
        if (!opts.quiet) showToast('error', I18N.t('nodes.flash_refresh_failed'));
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

  var nodeForm = workspace.querySelector('#node-form, [data-node-discovery-form]');
  if (nodeForm) {
    nodeForm.addEventListener('submit', function (e) {
      e.preventDefault();
      refreshNodes({ form: nodeForm, showSkeleton: true });
    });
  }

  /* ── Auto-Discover First Visit When Empty ("nosnap") ── */
  if (workspace.getAttribute('data-empty-state') === 'nosnap' && hasCreds && workspace.dataset.autoDiscovered !== '1') {
    workspace.dataset.autoDiscovered = '1';
    refreshNodes({ showSkeleton: true, quiet: false });
  }

  /* ── Config Viewer Modal ── */
  function openConfigViewer(nodeType, nodeName) {
    if (!configModal) return;
    var titleEl = configModal.querySelector('[data-config-modal-title]');
    var pathEl = configModal.querySelector('[data-config-modal-path]');
    var bodyEl = configModal.querySelector('[data-config-modal-body]');
    var searchInput = configModal.querySelector('[data-config-search]');
    var countEl = configModal.querySelector('[data-config-count]');
    var copyAllBtn = configModal.querySelector('[data-config-copy-all]');
    var copyAllText = configModal.querySelector('[data-config-copy-all-text]');
    var copyPathBtn = configModal.querySelector('[data-config-copy-path]');

    if (titleEl) titleEl.textContent = I18N.t('nodes.config_modal_title', { name: nodeName });
    var configPath = '/opt/' + nodeName + '/.env';
    if (pathEl) pathEl.textContent = configPath;
    if (searchInput) searchInput.value = '';
    if (countEl) countEl.textContent = '';

    if (copyPathBtn) {
      copyPathBtn.onclick = function (e) {
        e.preventDefault();
        e.stopPropagation();
        var currentPath = (pathEl && pathEl.textContent) || configPath;
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(currentPath);
        }
        copyPathBtn.innerHTML = '';
        copyPathBtn.appendChild(iconEl('check'));
        copyPathBtn.classList.add('is-copied');
        renderIcons(copyPathBtn);
        setTimeout(function () {
          copyPathBtn.innerHTML = '';
          copyPathBtn.appendChild(iconEl('copy'));
          copyPathBtn.classList.remove('is-copied');
          renderIcons(copyPathBtn);
        }, 1500);
      };
    }

    if (bodyEl) {
      bodyEl.textContent = '';
      var loading = el('div', 'config-loading');
      var spinner = el('span', 'spinner spinner--sm');
      loading.appendChild(spinner);
      loading.appendChild(document.createTextNode(' ' + I18N.t('nodes.config_modal_loading')));
      bodyEl.appendChild(loading);
    }
    openModal(configModal);

    var url = nodesUrl + '/config?type=' + encodeURIComponent(nodeType) + '&name=' + encodeURIComponent(nodeName);
    fetch(url, { headers: { Accept: 'application/json' } })
      .then(readJSON)
      .then(function (res) {
        if (!bodyEl) return;
        bodyEl.textContent = '';
        if (!res.ok || !res.data) {
          var errMsg = (res.data && res.data.error) || 'HTTP ' + res.status;
          bodyEl.appendChild(el('p', 'node-actions-note', I18N.t('nodes.config_modal_error', { error: errMsg })));
          return;
        }
        if (pathEl && res.data.config_path) {
          configPath = res.data.config_path;
          pathEl.textContent = configPath;
        }
        var entries = Array.isArray(res.data.entries) ? res.data.entries : [];
        if (!entries.length) {
          if (countEl) countEl.textContent = I18N.t('nodes.config_count', { count: 0 });
          bodyEl.appendChild(el('p', 'empty-state', I18N.t('nodes.config_modal_empty', { name: nodeName })));
          return;
        }

        if (copyAllBtn) {
          copyAllBtn.onclick = function (e) {
            e.preventDefault();
            e.stopPropagation();
            var text = entries.map(function (entry) {
              if (entry.redacted) {
                return '# ' + entry.key + '=(redacted secret)';
              }
              return (entry.key || '') + '=' + (entry.value || '');
            }).join('\n');
            if (navigator.clipboard && navigator.clipboard.writeText) {
              navigator.clipboard.writeText(text);
            }
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
          var q = (filterQuery || '').trim().toLowerCase();
          var matched = entries.filter(function (entry) {
            if (!q) return true;
            var k = (entry.key || '').toLowerCase();
            var v = (entry.value || '').toLowerCase();
            return k.indexOf(q) !== -1 || v.indexOf(q) !== -1;
          });

          if (countEl) {
            if (q) {
              countEl.textContent = I18N.t('nodes.config_count_filtered', { shown: matched.length, total: entries.length });
            } else {
              countEl.textContent = I18N.t('nodes.config_count', { count: entries.length });
            }
          }

          if (!matched.length) {
            var empty = el('div', 'config-empty');
            empty.appendChild(iconEl('search-x', 'config-empty__icon'));
            empty.appendChild(el('p', 'config-empty__text', I18N.t('common.no_results') || 'No matching properties'));
            bodyEl.appendChild(empty);
            renderIcons(empty);
            return;
          }

          var wrap = el('div', 'config-list');
          matched.forEach(function (entry) {
            var row = el('div', 'config-item');
            var keyBox = el('div', 'config-item__key');
            var keyBadge = el('code', 'config-key-code', entry.key || '');
            keyBox.appendChild(keyBadge);
            row.appendChild(keyBox);

            var valBox = el('div', 'config-item__val');
            if (entry.redacted) {
              row.classList.add('config-item--redacted');
              var secretPill = el('span', 'config-secret-pill');
              secretPill.appendChild(iconEl('lock', 'config-secret-pill__icon'));
              secretPill.appendChild(document.createTextNode(' ' + I18N.t('nodes.config_secret_badge')));
              valBox.appendChild(secretPill);
              var hint = el('span', 'config-secret-hint', I18N.t('nodes.config_secret_hint'));
              valBox.appendChild(hint);
            } else {
              var codeVal = el('code', 'config-val-code', entry.value || '');
              valBox.appendChild(codeVal);

              var copyValBtn = el('button', 'config-copy-btn');
              copyValBtn.type = 'button';
              copyValBtn.title = I18N.t('nodes.config_copy_value');
              copyValBtn.setAttribute('aria-label', I18N.t('nodes.config_copy_value'));
              copyValBtn.appendChild(iconEl('copy'));
              copyValBtn.addEventListener('click', function (e) {
                e.stopPropagation();
                if (navigator.clipboard && navigator.clipboard.writeText) {
                  navigator.clipboard.writeText(entry.value || '');
                }
                copyValBtn.innerHTML = '';
                copyValBtn.appendChild(iconEl('check'));
                copyValBtn.classList.add('is-copied');
                renderIcons(copyValBtn);
                setTimeout(function () {
                  copyValBtn.innerHTML = '';
                  copyValBtn.appendChild(iconEl('copy'));
                  copyValBtn.classList.remove('is-copied');
                  renderIcons(copyValBtn);
                }, 1500);
              });
              valBox.appendChild(copyValBtn);
            }
            row.appendChild(valBox);
            wrap.appendChild(row);
          });
          bodyEl.appendChild(wrap);
          renderIcons(wrap);
        }

        renderRows('');
        if (searchInput) {
          searchInput.oninput = function () {
            renderRows(searchInput.value);
          };
        }
      })
      .catch(function (err) {
        if (!bodyEl) return;
        bodyEl.textContent = '';
        bodyEl.appendChild(el('p', 'node-actions-note', I18N.t('nodes.config_modal_error', { error: String(err) })));
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
    confirmModal.addEventListener('modal:beforeclose', function () {
      pendingConfirmAction = null;
    });
  }

  function dispatchNodeAction(nodeType, nodeName, actionKey) {
    var csrf = getWorkspaceCSRF(workspace);
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
          showToast('error', (res.data && res.data.error) || I18N.t('nodes.action_failed'));
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
      .catch(function () {
        showToast('error', I18N.t('nodes.action_failed'));
      });
  }

  /* Delegate action-menu submits & config/quick-logs clicks across SSR + re-rendered cards */
  workspace.addEventListener('click', function (e) {
    var cfgBtn = e.target.closest && e.target.closest('[data-node-config]');
    if (cfgBtn) {
      e.preventDefault();
      closeAllMenus(null);
      openConfigViewer(cfgBtn.getAttribute('data-node-type') || '', cfgBtn.getAttribute('data-node-name') || '');
      return;
    }

    var quickLogsBtn = e.target.closest && e.target.closest('[data-node-quick-logs]');
    if (quickLogsBtn) {
      e.preventDefault();
      dispatchNodeAction(
        quickLogsBtn.getAttribute('data-node-type') || '',
        quickLogsBtn.getAttribute('data-node-name') || '',
        'logs'
      );
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
      if (msgEl) {
        msgEl.textContent = I18N.t('nodes.confirm_action_danger', { action: actionLabel, name: nodeName });
      }
      pendingConfirmAction = function () {
        dispatchNodeAction(nodeType, nodeName, actionKey);
      };
      openModal(confirmModal);
      return;
    }

    dispatchNodeAction(nodeType, nodeName, actionKey);
  });

  /* ── Wire Deploy Wizard Inside Workspace ── */
  if (deployModal) {
    wireDeployWizard(deployModal, workspace, owningPane, function () {
      refreshNodes({ quiet: false });
    }, showToast);
  }
}

/* ── Card DOM Builder (Synced With nodes_card.gohtml) ────────────────────── */
function buildNodeSlotElement(snap, serverId, csrfToken) {
  var searchText = [snap.name, snap.node_type, snap.type_label, snap.version, snap.service_port, snap.api_port, snap.protocol]
    .concat(snap.active_ports || [])
    .join(' ');

  var slot = el('div', 'node-card-slot');
  slot.setAttribute('data-node-slot', '');
  slot.setAttribute('data-node-name', snap.name || '');
  slot.setAttribute('data-node-type', snap.node_type || '');
  slot.setAttribute('data-node-health', snap.health_status || 'unknown');
  slot.setAttribute('data-node-uptime', snap.uptime_known ? String(snap.uptime_pct) : '-1');
  slot.setAttribute('data-node-latency', String(snap.latency_ms || 0));
  slot.setAttribute('data-node-search', searchText);

  var card = el('article', 'node-card node-card--' + snap.node_type + ' collapsible');
  slot.appendChild(card);

  // Header
  var head = el('header', 'node-card__head');
  var iconWrap = el('span', 'node-card__icon');
  iconWrap.appendChild(iconEl(snap.node_type === 'pasarguard-node' ? 'container' : (snap.node_type === 'rebecca-node' ? 'package' : 'help-circle')));
  head.appendChild(iconWrap);

  var titleWrap = el('div', 'node-card__title');
  var titleRow = el('div', 'node-card__title-row');
  titleRow.appendChild(el('span', 'node-card__name', snap.name));
  if (snap.service_port && snap.service_port !== '-') {
    titleRow.appendChild(el('code', 'node-card__port-tag', ':' + snap.service_port));
  }
  titleWrap.appendChild(titleRow);
  titleWrap.appendChild(el('span', 'node-card__type', snap.type_label || snap.node_type));
  head.appendChild(titleWrap);

  var headEnd = el('div', 'node-card__head-end');
  var bodyToggle = el('button', 'collapsible__toggle node-card__body-toggle');
  bodyToggle.type = 'button';
  bodyToggle.setAttribute('data-collapse-key', 'node_body_' + snap.node_type + '_' + snap.name);
  bodyToggle.setAttribute('data-collapse-default', 'closed');
  bodyToggle.setAttribute('aria-label', I18N.t('nodes.details_aria', { name: snap.name }));
  bodyToggle.title = I18N.t('nodes.details_aria', { name: snap.name });
  bodyToggle.appendChild(iconEl('chevron-right', 'collapsible__icon'));
  headEnd.appendChild(bodyToggle);

  headEnd.appendChild(buildActionMenuDOM(snap, serverId, csrfToken));
  head.appendChild(headEnd);
  card.appendChild(head);

  // Status pills + confidence
  var statusRow = el('div', 'node-status-row');
  var healthPill = el('span', 'node-pill node-pill--health-' + snap.health_status);
  healthPill.appendChild(el('span', 'node-pill__dot'));
  healthPill.appendChild(document.createTextNode(I18N.t(healthLabelKey(snap.health_status))));
  statusRow.appendChild(healthPill);

  var modePill = el('span', 'node-pill node-pill--mode-' + snap.install_mode);
  modePill.appendChild(iconEl(snap.install_mode === 'docker' ? 'container' : (snap.install_mode === 'binary' ? 'package' : 'help-circle')));
  modePill.appendChild(document.createTextNode(snap.install_mode || '-'));
  statusRow.appendChild(modePill);

  if (snap.protocol && snap.protocol !== '-') {
    var protoPill = el('span', 'node-pill node-pill--proto-' + snap.protocol);
    protoPill.appendChild(iconEl(snap.protocol === 'grpc' ? 'layers' : 'globe'));
    protoPill.appendChild(document.createTextNode(snap.protocol));
    statusRow.appendChild(protoPill);
  }

  var conf = el('div', 'node-confidence node-confidence--' + snap.confidence);
  conf.title = I18N.t('nodes.confidence_title', { level: snap.confidence });
  var meter = el('span', 'node-confidence__meter');
  meter.appendChild(el('span', 'node-confidence__seg'));
  meter.appendChild(el('span', 'node-confidence__seg'));
  meter.appendChild(el('span', 'node-confidence__seg'));
  conf.appendChild(meter);
  conf.appendChild(el('span', 'node-confidence__label', snap.confidence));
  statusRow.appendChild(conf);
  card.appendChild(statusRow);

  // Telemetry (last-seen + latency)
  var telem = el('div', 'node-telemetry');
  var seenItem = el('span', 'node-telemetry__item');
  seenItem.title = I18N.t('nodes.last_seen');
  seenItem.appendChild(iconEl('clock'));
  seenItem.appendChild(el('span', 'node-telemetry__val--ltr', snap.last_seen_at || I18N.t('nodes.never_seen')));
  telem.appendChild(seenItem);

  if (snap.latency_ms > 0) {
    var latItem = el('span', 'node-telemetry__item node-telemetry__item--latency');
    latItem.title = I18N.t('nodes.latency_label');
    latItem.appendChild(iconEl('gauge'));
    latItem.appendChild(el('span', 'node-telemetry__val--ltr', snap.latency_label || I18N.t('nodes.latency_ms', { ms: snap.latency_ms })));
    telem.appendChild(latItem);
  }
  card.appendChild(telem);

  // 30-day Uptime Strip
  if (Array.isArray(snap.uptime_days) && snap.uptime_days.length) {
    var upBlock = el('div', 'node-uptime-block');
    var upHead = el('div', 'node-uptime-block__head');
    var upTitle = el('span', 'node-uptime-block__title');
    upTitle.appendChild(iconEl('activity'));
    upTitle.appendChild(document.createTextNode(' ' + I18N.t('nodes.uptime_30d')));
    upHead.appendChild(upTitle);
    var upMeta = el('span', 'node-uptime-block__meta');
    if (snap.uptime_detail) {
      var chk = el('span', 'node-uptime-block__checks', snap.uptime_detail);
      chk.title = I18N.t('nodes.uptime_checks', { detail: snap.uptime_detail });
      upMeta.appendChild(chk);
    }
    upMeta.appendChild(el('span', 'node-uptime-block__pct', snap.uptime_known ? (snap.uptime_pct + '%') : '—'));
    upHead.appendChild(upMeta);
    upBlock.appendChild(upHead);

    var strip = el('div', 'node-uptime-strip');
    strip.setAttribute('role', 'group');
    strip.setAttribute('aria-label', I18N.t('nodes.uptime_strip_aria'));
    snap.uptime_days.forEach(function (day) {
      var bar = el('button', 'node-uptime-bar node-uptime-bar--' + (day.status || 'empty'));
      bar.type = 'button';
      bar.setAttribute('aria-label', day.tooltip || day.date);
      bar.appendChild(el('span', 'node-uptime-bar__fill'));
      var tip = el('span', 'node-uptime-bar__tip');
      tip.setAttribute('aria-hidden', 'true');
      tip.appendChild(el('span', 'node-uptime-bar__tip-date', day.tooltip || day.date));
      bar.appendChild(tip);
      strip.appendChild(bar);
    });
    upBlock.appendChild(strip);

    var activeFoot = el('div', 'node-uptime-block__foot');
    activeFoot.setAttribute('data-uptime-foot', '');
    activeFoot.hidden = true;
    var liveTip = el('span', '');
    liveTip.setAttribute('data-uptime-active-tip', '');
    liveTip.setAttribute('aria-live', 'polite');
    liveTip.hidden = true;
    activeFoot.appendChild(liveTip);
    upBlock.appendChild(activeFoot);
    card.appendChild(upBlock);
  }

  // Collapsible Body
  var body = el('div', 'collapsible__content node-card__body');
  if (snap.actions_enabled) {
    var quickTools = el('div', 'node-card__quick-tools');
    var logsBtn = el('button', 'btn btn--ghost btn--xs');
    logsBtn.type = 'button';
    logsBtn.setAttribute('data-node-quick-logs', '');
    logsBtn.setAttribute('data-node-type', snap.node_type);
    logsBtn.setAttribute('data-node-name', snap.name);
    logsBtn.appendChild(iconEl('terminal', 'icon-in-button'));
    logsBtn.appendChild(document.createTextNode(' ' + I18N.t('nodes.card_quick_logs')));
    quickTools.appendChild(logsBtn);

    var cfgBtn = el('button', 'btn btn--ghost btn--xs');
    cfgBtn.type = 'button';
    cfgBtn.setAttribute('data-node-config', '');
    cfgBtn.setAttribute('data-node-type', snap.node_type);
    cfgBtn.setAttribute('data-node-name', snap.name);
    cfgBtn.appendChild(iconEl('file-code', 'icon-in-button'));
    cfgBtn.appendChild(document.createTextNode(' ' + I18N.t('nodes.action_view_config')));
    quickTools.appendChild(cfgBtn);
    body.appendChild(quickTools);
  }

  var facts = el('div', 'node-facts');
  facts.appendChild(buildFact('tag', I18N.t('common.version'), snap.version || '-', ''));
  facts.appendChild(buildFact('plug', I18N.t('nodes.service_port'), snap.service_port && snap.service_port !== '-' ? snap.service_port : '-', 'node-port node-port--service'));
  facts.appendChild(buildFact('webhook', I18N.t('nodes.api_port'), snap.api_port && snap.api_port !== '-' ? snap.api_port : '-', 'node-port node-port--api'));
  facts.appendChild(buildFact('folder', I18N.t('nodes.data_directory'), snap.data_dir || '-', 'node-path', true));
  body.appendChild(facts);

  if (snap.node_type === 'pasarguard-node') {
    var credSec = el('div', 'node-section node-credentials');
    var credLab = el('span', 'node-section__label');
    credLab.appendChild(iconEl('key-round'));
    credLab.appendChild(document.createTextNode(' ' + I18N.t('nodes.credentials')));
    credSec.appendChild(credLab);
    if (snap.actions_enabled) {
      var credBtn = el('button', 'btn btn--ghost btn--sm node-credentials__btn');
      credBtn.type = 'button';
      credBtn.setAttribute('data-node-credentials', '');
      credBtn.setAttribute('data-url', '/servers/' + serverId + '/nodes/credentials?type=' + encodeURIComponent(snap.node_type) + '&name=' + encodeURIComponent(snap.name));
      credBtn.appendChild(iconEl('key-round', 'icon-in-button'));
      credBtn.appendChild(document.createTextNode(' ' + I18N.t('nodes.show_api_cert')));
      credSec.appendChild(credBtn);
      var credOut = el('div', 'node-credentials__out');
      credOut.hidden = true;
      credSec.appendChild(credOut);
    } else {
      var credNote = el('p', 'node-actions-note');
      credNote.appendChild(iconEl('key-round', 'icon-in-button'));
      credNote.appendChild(document.createTextNode(' ' + I18N.t('nodes.store_creds_view')));
      credSec.appendChild(credNote);
    }
    body.appendChild(credSec);
  }

  if (snap.active_ports && snap.active_ports.length) {
    body.appendChild(buildChipsSection('network', I18N.t('nodes.active_ports'), snap.active_ports, function (p) {
      return 'node-port' + (p === snap.api_port ? ' node-port--api' : (p === snap.service_port ? ' node-port--service' : ''));
    }));
  }
  if (snap.xray_ports && snap.xray_ports.length) {
    body.appendChild(buildChipsSection('arrow-left-right', I18N.t('nodes.xray_ports'), snap.xray_ports, function () {
      return 'node-port node-port--xray';
    }));
  }
  if (snap.dependencies && snap.dependencies.length) {
    body.appendChild(buildChipsSection('package-check', I18N.t('nodes.dependencies'), snap.dependencies, function (d) {
      return 'node-dep' + (String(d).slice(-8) === ':missing' ? ' node-dep--missing' : '');
    }));
  }

  if (snap.evidence && snap.evidence.length) {
    var evWrap = el('div', 'node-evidence collapsible');
    var evBtn = el('button', 'collapsible__toggle');
    evBtn.type = 'button';
    evBtn.setAttribute('data-collapse-key', 'evidence_' + snap.node_type + '_' + snap.name);
    evBtn.setAttribute('data-collapse-default', 'closed');
    evBtn.appendChild(iconEl('chevron-right', 'collapsible__icon'));
    evBtn.appendChild(el('span', '', I18N.t('nodes.evidence')));
    evBtn.appendChild(el('span', 'node-evidence__count', String(snap.evidence.length)));
    evWrap.appendChild(evBtn);

    var evContent = el('div', 'collapsible__content');
    var evList = el('ul', 'node-evidence__list');
    snap.evidence.forEach(function (item) {
      var li = el('li');
      li.appendChild(iconEl('check', 'node-evidence__check'));
      li.appendChild(el('span', '', item));
      evList.appendChild(li);
    });
    evContent.appendChild(evList);
    evWrap.appendChild(evContent);
    body.appendChild(evWrap);
  }

  var footer = el('div', 'node-card__footer');
  var calSpan = el('span');
  calSpan.appendChild(iconEl('calendar'));
  calSpan.appendChild(document.createTextNode(' ' + (snap.collected_at || '')));
  footer.appendChild(calSpan);
  body.appendChild(footer);

  card.appendChild(body);
  return slot;
}

function buildFact(iconName, label, value, codeClass, wide) {
  var fact = el('div', 'node-fact' + (wide ? ' node-fact--wide' : ''));
  var lab = el('span', 'node-fact__label');
  lab.appendChild(iconEl(iconName));
  lab.appendChild(document.createTextNode(' ' + label));
  var val = el('span', 'node-fact__value');
  if (codeClass && value !== '-') {
    val.appendChild(el('code', codeClass, value));
  } else {
    val.textContent = value;
  }
  fact.appendChild(lab);
  fact.appendChild(val);
  return fact;
}

function buildChipsSection(iconName, label, items, classFn) {
  var sec = el('div', 'node-section');
  var lab = el('span', 'node-section__label');
  lab.appendChild(iconEl(iconName));
  lab.appendChild(document.createTextNode(' ' + label));
  var chips = el('div', 'node-chips');
  items.forEach(function (item) {
    chips.appendChild(el('code', classFn(item), item));
  });
  sec.appendChild(lab);
  sec.appendChild(chips);
  return sec;
}

function buildActionMenuDOM(snap, serverId, csrfToken) {
  var menu = el('div', 'action-menu action-menu--below node-card__menu');
  menu.setAttribute('data-action-menu', '');
  var toggle = el('button', 'btn btn--ghost btn--sm action-menu__toggle node-card__menu-toggle');
  toggle.type = 'button';
  toggle.setAttribute('aria-haspopup', 'true');
  toggle.setAttribute('aria-expanded', 'false');
  toggle.setAttribute('aria-label', I18N.t('nodes.actions_aria', { name: snap.name }));
  toggle.appendChild(iconEl('ellipsis-vertical'));
  menu.appendChild(toggle);

  var panel = el('div', 'action-menu__panel');
  panel.setAttribute('role', 'menu');

  var handle = el('div', 'action-menu__handle');
  handle.setAttribute('aria-hidden', 'true');
  panel.appendChild(handle);

  var head = el('div', 'action-menu__head');
  var caption = el('span', 'action-menu__caption');
  caption.appendChild(iconEl('wrench'));
  caption.appendChild(document.createTextNode(' ' + I18N.t('nodes.node_actions')));
  head.appendChild(caption);

  var closeBtn = el('button', 'action-menu__close');
  closeBtn.type = 'button';
  closeBtn.setAttribute('data-action-menu-close', '');
  closeBtn.setAttribute('aria-label', I18N.t('nodes.action_menu_close') || 'Close actions menu');
  closeBtn.appendChild(iconEl('x'));
  head.appendChild(closeBtn);

  panel.appendChild(head);

  var div = el('div', 'action-menu__divider');
  div.setAttribute('role', 'separator');
  panel.appendChild(div);

  if (snap.actions_enabled) {
    var form = el('form', 'action-menu__form');
    form.method = 'post';
    form.action = '/servers/' + serverId + '/nodes/actions';
    var appendHidden = function (n, v) {
      var i = document.createElement('input');
      i.type = 'hidden';
      i.name = n;
      i.value = v;
      form.appendChild(i);
    };
    if (csrfToken) appendHidden('_csrf_token', csrfToken);
    appendHidden('node_type', snap.node_type);
    appendHidden('node_name', snap.name);

    (snap.actions || []).forEach(function (act) {
      var item = el('button', 'action-menu__item' + (act.danger ? ' action-menu__item--danger' : ''));
      item.type = 'submit';
      item.name = 'action';
      item.value = act.key;
      item.setAttribute('role', 'menuitem');
      if (act.danger) {
        item.setAttribute('data-confirm', I18N.t('nodes.action_confirm', { action: act.label, name: snap.name }));
      }
      item.appendChild(iconEl(act.icon, 'icon-in-button'));
      item.appendChild(document.createTextNode(' ' + act.label));
      form.appendChild(item);
    });

    var cfgItem = el('button', 'action-menu__item');
    cfgItem.type = 'button';
    cfgItem.setAttribute('role', 'menuitem');
    cfgItem.setAttribute('data-node-config', '');
    cfgItem.setAttribute('data-node-type', snap.node_type);
    cfgItem.setAttribute('data-node-name', snap.name);
    cfgItem.appendChild(iconEl('file-code', 'icon-in-button'));
    cfgItem.appendChild(document.createTextNode(' ' + I18N.t('nodes.action_view_config')));
    form.appendChild(cfgItem);

    panel.appendChild(form);
  } else {
    var note = el('span', 'action-menu__note');
    note.appendChild(iconEl('key-round'));
    note.appendChild(document.createTextNode(' ' + I18N.t('nodes.store_creds_manage')));
    panel.appendChild(note);
  }

  menu.appendChild(panel);
  return menu;
}

/* ── Unified Deploy Wizard (3 Setup Steps + Live Execution Pane) ─────────── */
function wireDeployWizard(modal, workspace, owningPane, onDeployed, showToast) {
  if (!modal || modal.dataset.deployWizardReady === '1') return;
  modal.dataset.deployWizardReady = '1';

  var form = modal.querySelector('[data-deploy-form]');
  var steps = modal.querySelectorAll('[data-step-indicator]');
  var panes = modal.querySelectorAll('[data-deploy-pane]');
  var btnBack = modal.querySelector('[data-deploy-back]');
  var btnNext = modal.querySelector('[data-deploy-next]');
  var btnSubmit = modal.querySelector('[data-deploy-submit]');
  var footerActions = modal.querySelector('[data-deploy-actions]');
  var statusEl = modal.querySelector('[data-deploy-status]');
  var bundleInput = modal.querySelector('[data-bundle-input]');
  var bundleState = modal.querySelector('[data-bundle-state]');
  var bundleStateText = modal.querySelector('[data-bundle-state-text]');
  var bundlePasteBtn = modal.querySelector('[data-bundle-paste]');
  var bundleClearBtn = modal.querySelector('[data-bundle-clear]');
  var overrideInput = modal.querySelector('[data-deploy-override]');
  var engineCards = modal.querySelectorAll('[data-engine-card]');

  // Step 3 live execution nodes
  var liveStagesEl = modal.querySelector('[data-deploy-stages]');
  var liveLogEl = modal.querySelector('[data-deploy-live-log]');
  var liveStatusBadge = modal.querySelector('[data-deploy-live-badge]');
  var liveStatusEl = modal.querySelector('[data-deploy-live-status]');
  var liveScrollBtn = modal.querySelector('[data-deploy-scroll-toggle]');
  var liveCancelBtn = modal.querySelector('[data-deploy-cancel-job]');
  var endOkCard = modal.querySelector('[data-deploy-end="ok"]');
  var endErrCard = modal.querySelector('[data-deploy-end="error"]');
  var endErrText = modal.querySelector('[data-deploy-end-error]');
  var retryBtn = modal.querySelector('[data-deploy-retry-btn]');
  var doneRefreshBtn = modal.querySelector('[data-deploy-done-refresh]');
  var regContainer = modal.querySelector('[data-deploy-reg-info]');

  var currentStep = 0;
  var isSubmitting = false;
  var activeInstallES = null;
  var activeCancelURL = '';
  var activeStatusURL = '';
  var currentStageKey = 'initializing';
  var livePausedScroll = false;
  var liveLogTrack = { received: 0, connOffset: 0, text: '' };

  function stopInstallStream() {
    if (activeInstallES) {
      try { activeInstallES.close(); } catch (err) {}
      activeInstallES = null;
    }
  }

  registerCleanup(owningPane || document, stopInstallStream);

  modal.addEventListener('modal:beforeclose', function () {
    stopInstallStream();
  });

  function isRebecca(engine) {
    return engine === 'rebecca' || engine === 'rebecca-node';
  }

  function getSelectedEngine() {
    var checked = modal.querySelector('input[name="node_type"]:checked');
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
    var errEl = modal.querySelector('[data-field-error="' + fieldName + '"]');
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

    if (!val) {
      bundleState.className = 'deploy-status is-idle';
      setBundleIcon('circle-alert');
      if (bundleStateText) bundleStateText.textContent = I18N.t('nodes.bundle_waiting');
      return;
    }
    var hasCert = val.indexOf('-----BEGIN CERTIFICATE-----') !== -1;
    var hasKey = val.indexOf('KEY-----') !== -1;
    if (hasCert && hasKey) {
      bundleState.className = 'deploy-status is-ok';
      setBundleIcon('check-circle-2');
      if (bundleStateText) bundleStateText.textContent = I18N.t('nodes.bundle_valid');
    } else {
      bundleState.className = 'deploy-status is-bad';
      setBundleIcon('alert-triangle');
      if (bundleStateText) bundleStateText.textContent = I18N.t('nodes.bundle_invalid');
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
      grp.querySelectorAll('input, textarea, select').forEach(function (inp) {
        inp.disabled = !isGrpActive;
      });
    });

    var protoRow = modal.querySelector('[data-review-row="protocol"]');
    if (protoRow) protoRow.hidden = rebecca;
    var bundleRow = modal.querySelector('[data-review-row="bundle"]');
    if (bundleRow) bundleRow.hidden = !rebecca;

    modal.querySelectorAll('[data-install-note]').forEach(function (n) {
      n.hidden = (n.getAttribute('data-install-note') !== kind);
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
    var name = (form && form.elements['node_name'] ? form.elements['node_name'].value : '').trim();
    var sPort = (form && form.elements['service_port'] ? form.elements['service_port'].value : '').trim();
    var aPort = (form && form.elements['api_port'] ? form.elements['api_port'].value : '').trim();
    var channel = modal.querySelector('input[name="channel"]:checked');
    var proto = modal.querySelector('input[name="protocol"]:checked');

    var setReview = function (key, val) {
      var target = modal.querySelector('[data-review="' + key + '"]');
      if (target) target.textContent = val || '—';
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
    setReview('override', (overrideInput && overrideInput.checked)
      ? I18N.t('nodes.deploy_manifest_override_yes')
      : I18N.t('nodes.deploy_manifest_override_no'));

    var bundleVal = bundleInput ? bundleInput.value.trim() : '';
    var hasCert = bundleVal.indexOf('-----BEGIN CERTIFICATE-----') !== -1;
    var hasKey = bundleVal.indexOf('KEY-----') !== -1;
    setReview('bundle', (hasCert && hasKey) ? I18N.t('nodes.bundle_valid') : '—');
  }

  function setStep(idx) {
    currentStep = Math.max(0, Math.min(3, idx));
    modal.setAttribute('data-step', String(currentStep));
    var modalBody = modal.querySelector('.modal__body');
    if (modalBody) {
      modalBody.scrollTop = 0;
      modalBody.scrollLeft = 0;
    }
    clearFieldErrors();

    var isLive = currentStep === 3;
    steps.forEach(function (s, i) {
      var isActive = (i === currentStep) || (isLive && i === 2);
      var isDone = (i < currentStep);
      s.classList.toggle('is-active', isActive && !isLive);
      s.classList.toggle('is-done', isDone || isLive);
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
    var engine = getSelectedEngine();
    var rebecca = isRebecca(engine);
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
    var sRaw = sPortField ? sPortField.value.trim() : '';
    var sVal = parseInt(sRaw, 10);
    if (!sPortField || !sVal || isNaN(sVal) || sVal < 1 || sVal > 65535) {
      showFieldError('service_port', I18N.t('nodes.err_service_port'));
      if (sPortField) sPortField.focus();
      return false;
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

    var chan = modal.querySelector('input[name="channel"]:checked');
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

    if (rebecca) {
      var bVal = (bundleInput ? bundleInput.value : '').trim();
      var hasCert = bVal.indexOf('-----BEGIN CERTIFICATE-----') !== -1;
      var hasKey = bVal.indexOf('KEY-----') !== -1;
      if (!bVal || !hasCert || !hasKey) {
        showFieldError('bundle', I18N.t('nodes.bundle_invalid'));
        validateBundle();
        if (bundleInput) bundleInput.focus();
        return false;
      }
    }

    return true;
  }

  function renderInstallStages(stages, activeStageKey) {
    if (!liveStagesEl || !Array.isArray(stages)) return;
    liveStagesEl.textContent = '';
    stages.forEach(function (st) {
      var state = st.State || st.state || 'pending';
      var key = st.Key || st.key || '';
      if (activeStageKey && key === activeStageKey && state === 'pending') state = 'active';
      var pill = el('div', 'deploy-stage-pill is-' + state);
      pill.setAttribute('data-stage-key', key);
      if (state === 'done') pill.appendChild(iconEl('check-circle-2'));
      else if (state === 'failed') pill.appendChild(iconEl('x-circle'));
      else if (state === 'active') pill.appendChild(iconEl('loader'));
      else pill.appendChild(iconEl('circle'));
      pill.appendChild(el('span', '', st.Label || st.label || key));
      liveStagesEl.appendChild(pill);
    });
    renderIcons(liveStagesEl);
  }

  function setStageStateByKey(activeKey, allDone, failed) {
    if (!liveStagesEl) return;
    var pills = Array.prototype.slice.call(liveStagesEl.querySelectorAll('[data-stage-key]'));
    var seenActive = false;
    pills.forEach(function (pill) {
      var k = pill.getAttribute('data-stage-key');
      pill.classList.remove('is-pending', 'is-active', 'is-done', 'is-failed');
      var nextState = 'pending';
      var nextIcon = 'circle';
      if (allDone) {
        nextState = 'done';
        nextIcon = 'check-circle-2';
      } else if (failed && k === activeKey) {
        nextState = 'failed';
        nextIcon = 'x-circle';
        seenActive = true;
      } else if (k === activeKey) {
        nextState = 'active';
        nextIcon = 'loader';
        seenActive = true;
      } else if (!seenActive) {
        nextState = 'done';
        nextIcon = 'check-circle-2';
      }
      pill.classList.add('is-' + nextState);
      var labelSpan = pill.querySelector('span');
      var labelText = labelSpan ? labelSpan.textContent : k;
      pill.textContent = '';
      pill.appendChild(iconEl(nextIcon));
      pill.appendChild(el('span', '', labelText));
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
    liveLogEl.textContent = liveLogTrack.text;
    if (!livePausedScroll) liveLogEl.scrollTop = liveLogEl.scrollHeight;
  }

  function renderInstallRegistration(info) {
    if (!regContainer) return;
    regContainer.textContent = '';
    if (!info || !info.Available) return;

    var facts = el('div', 'node-facts');
    facts.appendChild(buildFact('box', I18N.t('nodes.node_name'), info.NodeName || '', 'node-port'));
    facts.appendChild(buildFact('globe', I18N.t('nodes.node_ip'), info.NodeIP || '', 'node-port'));
    facts.appendChild(buildFact('plug', I18N.t('nodes.service_port'), info.ServicePort || '', 'node-port node-port--service'));
    facts.appendChild(buildFact('layers', I18N.t('nodes.protocol'), info.Protocol || '', ''));
    regContainer.appendChild(facts);

    if (info.APIKey) {
      regContainer.appendChild(buildCredBlock('key-round', I18N.t('nodes.api_key'), 'deploy-reg-api-key', info.APIKey, true));
    }
    if (info.Certificate) {
      regContainer.appendChild(buildCredBlock('shield-check', I18N.t('nodes.ssl_cert'), 'deploy-reg-cert', info.Certificate, false));
    } else {
      regContainer.appendChild(el('p', 'empty-state', I18N.t('nodes.cert_missing', { name: info.NodeName || '' })));
    }
    initSecrets(regContainer);
    initCopyTargets(regContainer);
    renderIcons(regContainer);
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

    activeInstallES.addEventListener('open', function () {
      liveLogTrack.connOffset = 0;
    });

    activeInstallES.addEventListener('stage', function (e) {
      try {
        var payload = JSON.parse(e.data || '{}');
        if (payload.stage) {
          currentStageKey = payload.stage;
          setStageStateByKey(payload.stage, payload.stage === 'ready', false);
          if (liveStatusEl) {
            liveStatusEl.textContent = I18N.t('nodes.stage_' + payload.stage) || payload.stage;
          }
        }
      } catch (err) {}
    });

    activeInstallES.addEventListener('output', function (e) {
      appendInstallLog(e.data || '');
    });

    activeInstallES.addEventListener('done', function () {
      stopInstallStream();
      setStageStateByKey('ready', true, false);
      if (liveCancelBtn) liveCancelBtn.hidden = true;
      if (liveStatusBadge) {
        liveStatusBadge.classList.add('is-ended', 'is-ok');
        liveStatusBadge.classList.remove('is-error');
      }
      if (liveStatusEl) liveStatusEl.textContent = I18N.t('nodes.deploy_success_title');
      if (endOkCard) endOkCard.hidden = false;
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
        stopInstallStream();
        if (liveCancelBtn) liveCancelBtn.hidden = true;
        var payload = {};
        try { payload = JSON.parse(e.data); } catch (err) {}
        var isCanceled = payload.status === 'canceled';
        var msg = payload.message || (isCanceled ? I18N.t('nodes.deploy_canceled') : I18N.t('nodes.deploy_failed_title'));
        if (liveStatusBadge) {
          liveStatusBadge.classList.add('is-ended', 'is-error');
          liveStatusBadge.classList.remove('is-ok');
        }
        if (liveStatusEl) liveStatusEl.textContent = msg;
        if (endErrText) endErrText.textContent = msg;
        if (endErrCard) endErrCard.hidden = false;
        setStageStateByKey(currentStageKey || 'initializing', false, true);
        return;
      }
      if (activeInstallES && activeInstallES.readyState === EventSource.CLOSED) {
        stopInstallStream();
        if (liveCancelBtn) liveCancelBtn.hidden = true;
        if (liveStatusBadge) {
          liveStatusBadge.classList.add('is-ended', 'is-error');
          liveStatusBadge.classList.remove('is-ok');
        }
        if (endErrText) endErrText.textContent = I18N.t('js.bulk.stream_error');
        if (endErrCard) endErrCard.hidden = false;
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
      var csrf = getWorkspaceCSRF(workspace);
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

  if (retryBtn) {
    retryBtn.addEventListener('click', function () {
      setStep(1);
    });
  }

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
      radio.addEventListener('change', function () { syncEngineUI(); });
    }
  });

  if (btnNext) {
    btnNext.addEventListener('click', function (e) {
      e.preventDefault();
      clearFieldErrors();
      if (currentStep === 0) {
        if (!getSelectedEngine()) {
          showFieldError('engine', I18N.t('nodes.err_engine_required'));
          return;
        }
        setStep(1);
        return;
      }
      if (currentStep === 1 && validateStep1()) {
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
      if (!validateStep1()) {
        setStep(1);
        return;
      }

      isSubmitting = true;
      if (btnSubmit) btnSubmit.disabled = true;
      clearFieldErrors();

      var csrf = getWorkspaceCSRF(workspace);
      var params = new URLSearchParams();
      if (csrf) params.set('_csrf_token', csrf);
      var fd = new FormData(form);
      fd.forEach(function (val, key) {
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
            if (errs.node_type || errs.engine) {
              setStep(0);
            } else {
              setStep(1);
            }
            Object.keys(errs).forEach(function (k) {
              showFieldError(k, errs[k]);
            });
            if (errs.node_name && overrideInput) {
              var ovBox = modal.querySelector('[data-deploy-override-wrap]');
              if (ovBox) ovBox.hidden = false;
            }
            if (!Object.keys(errs).length) {
              var msg = (res.data && res.data.error) || I18N.t('nodes.deploy_failed_title');
              if (statusEl) statusEl.textContent = msg;
              if (showToast) showToast('error', msg);
            }
            return;
          }

          beginLiveInstall(res.data);
        })
        .catch(function () {
          isSubmitting = false;
          if (btnSubmit) btnSubmit.disabled = false;
          if (statusEl) statusEl.textContent = I18N.t('nodes.deploy_failed_title');
          if (showToast) showToast('error', I18N.t('nodes.deploy_failed_title'));
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
        if (e.key === 'Enter') {
          e.preventDefault();
          if (btnNext) btnNext.click();
        }
      });
    });
  }

  workspace.querySelectorAll('[data-engine-preselect]').forEach(function (btn) {
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

  workspace.querySelectorAll('[data-modal-open="deploy-node-modal"]').forEach(function (btn) {
    if (!btn.hasAttribute('data-engine-preselect')) {
      if (btn.dataset.deployOpenReady === '1') return;
      btn.dataset.deployOpenReady = '1';
      btn.addEventListener('click', function () {
        if (currentStep === 3 && !activeInstallES) {
          setStep(0);
        } else if (currentStep !== 3) {
          setStep(0);
        }
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

function initNodes(root) {
  root = root || document;
  wireNodeCredentials(root);
  root.querySelectorAll('[data-nodes-workspace]').forEach(wireNodesWorkspace);
}

export { initNodes };
