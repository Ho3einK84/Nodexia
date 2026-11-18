/* Nodexia SFTP file manager — client behaviour.
 *
 * Progressive enhancement over the SSR listing: scoped strictly per .tab-pane,
 * AJAX directory navigation with history stack, bookmarks, list/grid views,
 * hidden dotfiles toggle, multi-select batch actions (copy/cut/paste, ZIP
 * download, compress, extract, chmod, delete), recursive search, drag-and-drop
 * file & folder uploads with progress/cancel, properties inspector, and an
 * in-browser code/config/log-tail/image viewer & editor.
 */
(function () {
  'use strict';

  var currentScript = document.currentScript;
  var root = (currentScript && currentScript.closest && currentScript.closest('.tab-pane')) || document;
  var readyHost = root === document ? document.documentElement : root;
  if (readyHost && readyHost.dataset && readyHost.dataset.filesReady === '1') return;
  if (readyHost && readyHost.dataset) readyHost.dataset.filesReady = '1';

  function qs(selector) { return root.querySelector(selector); }
  function qsa(selector) { return root.querySelectorAll(selector); }

  function registerCleanup(fn) {
    if (root === document) return;
    if (!Array.isArray(root.__nxCleanups)) root.__nxCleanups = [];
    root.__nxCleanups.push(fn);
  }

  registerCleanup(function () {
    if (readyHost && readyHost.dataset) delete readyHost.dataset.filesReady;
  });

  function T(key, params) { return window.nxT ? window.nxT(key, params) : key; }
  function formatBytes(n) {
    if (window.nxHumanBytes) return window.nxHumanBytes(n);
    return String(n || 0) + ' B';
  }

  function renderIcons(scope) {
    if (window.nxRenderIcons) {
      window.nxRenderIcons(scope || root);
      return;
    }
    if (typeof lucide === 'undefined' || !lucide.createIcons) return;
    try {
      if (scope) lucide.createIcons({ root: scope });
      else lucide.createIcons();
    } catch (e) {}
  }

  /* ── Toast host ─────────────────────────────────────────── */
  function toast(message, kind) {
    var host = document.getElementById('file-toast-host');
    if (!host) {
      host = document.createElement('div');
      host.id = 'file-toast-host';
      host.className = 'file-toast-host';
      document.body.appendChild(host);
    }
    var el = document.createElement('div');
    el.className = 'file-toast file-toast--' + (kind || 'info');
    el.textContent = message;
    host.appendChild(el);
    requestAnimationFrame(function () { el.classList.add('is-visible'); });
    setTimeout(function () {
      el.classList.remove('is-visible');
      setTimeout(function () { el.remove(); }, 250);
    }, kind === 'error' ? 6000 : 3400);
  }

  /* ── Preferences (persisted in localStorage) ─────────────── */
  var PREFS_KEY = 'nodexia.files.prefs.v1';
  var defaultPrefs = {
    showHidden: true,
    viewMode: 'list',
    sortKey: 'name',
    sortDir: 'asc',
    bookmarksOpen: true
  };

  function loadPrefs() {
    try {
      var raw = localStorage.getItem(PREFS_KEY);
      if (!raw) return Object.assign({}, defaultPrefs);
      return Object.assign({}, defaultPrefs, JSON.parse(raw) || {});
    } catch (e) {
      return Object.assign({}, defaultPrefs);
    }
  }

  var prefs = loadPrefs();
  function savePrefs() {
    try { localStorage.setItem(PREFS_KEY, JSON.stringify(prefs)); } catch (e) {}
  }

  /* ── Shared context & state ──────────────────────────────── */
  var ctx = null;
  var historyStack = [];
  var historyIndex = -1;
  var selectedPaths = new Set();
  var lastCheckedPath = null;
  var clipboard = null; // { mode: 'copy' | 'cut', paths: [] }
  var currentEntries = []; // mirrors rendered entries for quick lookup
  var activeFetchController = null;

  function context() {
    if (ctx) return ctx;
    var form = qs('#file-form');
    var card = qs('#file-browser-card');
    if (!form || !card) return null;
    var listing = qs('#file-listing');
    var pathEl = qs('#file-path');
    ctx = {
      form: form,
      card: card,
      serverId: card.getAttribute('data-server-id') || '0',
      hasStoredCreds: card.getAttribute('data-stored-creds') === '1',
      homeDir: card.getAttribute('data-home-dir') || '',
      action: form.getAttribute('action') || '',
      currentDir: (listing && listing.getAttribute('data-current-path')) || (pathEl && pathEl.value) || '',
      parentDir: (listing && listing.getAttribute('data-parent-path')) || '',
    };
    return ctx;
  }

  function currentToken(form) {
    var f = form || qs('#file-form');
    var tokenInput = f ? f.querySelector('input[name="_csrf_token"]') : null;
    return tokenInput ? tokenInput.value : '';
  }

  function refreshCSRFToken() {
    return fetch('/api/csrf-token', { credentials: 'same-origin' }).then(function (resp) {
      if (!resp.ok) return '';
      return resp.json().then(function (data) {
        if (data && data.csrf_token) {
          document.querySelectorAll('input[name="_csrf_token"]').forEach(function (input) {
            input.value = data.csrf_token;
          });
          return data.csrf_token;
        }
        return '';
      });
    }).catch(function () { return ''; });
  }

  function collectCredsObject() {
    var out = {};
    var map = { password: '#file-password', private_key: '#file-key', key_passphrase: '#file-passphrase' };
    Object.keys(map).forEach(function (field) {
      var el = qs(map[field]);
      if (el && el.value) out[field] = el.value;
    });
    return out;
  }

  function collectCredsFormData(body) {
    var creds = collectCredsObject();
    Object.keys(creds).forEach(function (k) { body.append(k, creds[k]); });
    return body;
  }

  function hasAnyRuntimeCreds() {
    var creds = collectCredsObject();
    return Boolean(creds.password || creds.private_key || creds.key_passphrase);
  }

  function ensureCredsOrPrompt() {
    var c = context();
    if (!c || c.hasStoredCreds || hasAnyRuntimeCreds()) return true;
    var credsBody = qs('#file-creds-body');
    var toggleBtn = qs('#file-creds-toggle-btn');
    if (credsBody && credsBody.hidden) {
      credsBody.hidden = false;
      if (toggleBtn) toggleBtn.setAttribute('aria-expanded', 'true');
    }
    var pwd = qs('#file-password');
    if (pwd && pwd.focus) pwd.focus();
    toast(T('js.files.creds_required'), 'error');
    return false;
  }

  function opsURL(intent) {
    var c = context();
    return c.action + '/ops?intent=' + encodeURIComponent(intent);
  }

  function postOp(intent, payload, retryAuth) {
    var c = context();
    if (!c) return Promise.reject(new Error('File context missing.'));
    var body = Object.assign({}, payload || {}, collectCredsObject());
    var headers = { 'Content-Type': 'application/json' };
    var csrf = currentToken(c.form);
    if (csrf) headers['X-CSRF-Token'] = csrf;

    return fetch(opsURL(intent), {
      method: 'POST',
      credentials: 'same-origin',
      headers: headers,
      body: JSON.stringify(body),
    }).then(function (res) {
      if (res.status === 403 && !retryAuth) {
        return refreshCSRFToken().then(function () {
          return postOp(intent, payload, true);
        });
      }
      return res.json().catch(function () { return {}; }).then(function (data) {
        if (!res.ok || !data.ok) {
          throw new Error((data && data.error) || T('js.files.request_failed', { status: res.status }));
        }
        return data;
      });
    });
  }

  /* ── Spinners & busy indicators ─────────────────────────── */
  function setRefreshSpinning(spinning) {
    var refreshBtn = qs('#file-refresh-btn');
    if (!refreshBtn) return;
    var icon = refreshBtn.querySelector('i, svg, [data-spin-icon]');
    if (icon) icon.classList.toggle('is-spinning', !!spinning);
  }

  function setUploadBusy(busy) {
    var uploadBtn = qs('#file-upload-btn');
    if (!uploadBtn) return;
    uploadBtn.disabled = !!busy;
    if (busy) uploadBtn.setAttribute('aria-busy', 'true');
    else uploadBtn.removeAttribute('aria-busy');
    var icon = uploadBtn.querySelector('i, svg');
    if (icon) icon.classList.toggle('is-spinning', !!busy);
  }

  function stopSpinners() {
    setRefreshSpinning(false);
    setUploadBusy(false);
  }

  /* ── Breadcrumb ─────────────────────────────────────────── */
  function buildBreadcrumb(pathValue, container) {
    if (!container) return;
    container.innerHTML = '';
    if (!pathValue) return;
    var parts = pathValue.replace(/\/+$/, '').split('/');
    var rootSeg = document.createElement('button');
    rootSeg.type = 'button';
    rootSeg.className = 'breadcrumb__seg';
    rootSeg.textContent = '/';
    rootSeg.setAttribute('data-crumb-path', '/');
    container.appendChild(rootSeg);
    var filtered = parts.filter(function (p) { return p !== ''; });
    if (pathValue === '/' || filtered.length === 0) {
      rootSeg.classList.add('breadcrumb__seg--current');
      return;
    }
    var accumulated = '';
    filtered.forEach(function (part, i, arr) {
      accumulated += '/' + part;
      var sep = document.createElement('span');
      sep.className = 'breadcrumb__sep';
      sep.setAttribute('aria-hidden', 'true');
      sep.textContent = '›';
      container.appendChild(sep);
      var seg = document.createElement('button');
      seg.type = 'button';
      seg.className = 'breadcrumb__seg' + (i === arr.length - 1 ? ' breadcrumb__seg--current' : '');
      seg.textContent = part;
      seg.setAttribute('data-crumb-path', accumulated);
      container.appendChild(seg);
    });
    container.scrollLeft = container.scrollWidth;
  }

  function markPathReady(ready) {
    var nav = qs('#file-nav');
    if (nav) nav.classList.toggle('is-path-ready', !!ready);
  }

  function setPathEditing(editing) {
    var nav = qs('#file-nav');
    var editBtn = qs('#file-path-edit-btn');
    var pathInput = qs('#file-path');
    if (!nav) return;
    var active = !!editing;
    nav.classList.toggle('is-editing-path', active);
    if (editBtn) editBtn.setAttribute('aria-expanded', active ? 'true' : 'false');
    if (active && pathInput && pathInput.focus) {
      pathInput.focus();
      if (pathInput.select) pathInput.select();
    }
  }

  /* ── Entry icon resolver ────────────────────────────────── */
  function iconForEntry(entry) {
    if (entry.kind === 'directory') return 'folder';
    if (entry.is_symlink) return entry.link_is_dir ? 'folder-symlink' : 'file-symlink';
    var ext = (entry.ext || '').toLowerCase();
    if (!ext && entry.name) {
      var dot = entry.name.lastIndexOf('.');
      if (dot > 0) ext = entry.name.slice(dot).toLowerCase();
    }
    if (ext === '.log') return 'scroll-text';
    if (ext === '.conf' || ext === '.ini' || ext === '.env' || ext === '.cfg' || ext === '.toml') return 'settings';
    if (ext === '.sh' || ext === '.bash' || ext === '.zsh') return 'terminal';
    if (ext === '.json') return 'braces';
    if (ext === '.yaml' || ext === '.yml' || ext === '.xml' || ext === '.go' || ext === '.py' ||
        ext === '.js' || ext === '.ts' || ext === '.css' || ext === '.html' || ext === '.sql' ||
        ext === '.md' || ext === '.txt') return 'file-code';
    if (ext === '.gz' || ext === '.zip' || ext === '.tar' || ext === '.tgz' || ext === '.xz' || ext === '.bz2') return 'archive';
    if (ext === '.png' || ext === '.jpg' || ext === '.jpeg' || ext === '.gif' || ext === '.webp' || ext === '.svg' || ext === '.ico' || ext === '.bmp') return 'image';
    if (ext === '.key' || ext === '.pem' || ext === '.crt' || ext === '.pub') return 'key-round';
    return 'file';
  }

  function isArchiveName(name) {
    var lower = String(name || '').toLowerCase();
    return lower.endsWith('.zip') || lower.endsWith('.tar.gz') || lower.endsWith('.tgz') || lower.endsWith('.tar');
  }

  function isNonPreviewBinaryExt(ext) {
    switch ((ext || '').toLowerCase()) {
      case '.zip': case '.tar': case '.gz': case '.tgz': case '.xz': case '.bz2': case '.7z': case '.rar':
      case '.exe': case '.bin': case '.so': case '.dll': case '.dylib': case '.deb': case '.rpm':
      case '.iso': case '.dmg': case '.sqlite': case '.sqlite3': case '.db': case '.pdf':
      case '.mp3': case '.mp4': case '.mkv': case '.avi': case '.mov': case '.woff': case '.woff2':
        return true;
      default:
        return false;
    }
  }

  /* ── Selection & Clipboard UI ───────────────────────────── */
  function getEntryByPath(p) {
    for (var i = 0; i < currentEntries.length; i++) {
      if (currentEntries[i].path === p) return currentEntries[i];
    }
    var row = findRowByPath(p);
    if (!row) return null;
    return entryFromRow(row);
  }

  function findRowByPath(p) {
    var rows = entryRows();
    for (var i = 0; i < rows.length; i++) {
      if (rows[i].getAttribute('data-entry-path') === p) return rows[i];
    }
    return null;
  }

  function entryFromRow(row) {
    return {
      name: row.getAttribute('data-entry-name') || '',
      path: row.getAttribute('data-entry-path') || '',
      kind: row.getAttribute('data-entry-kind') || 'file',
      size_bytes: parseInt(row.getAttribute('data-entry-size'), 10) || 0,
      size: (row.querySelector('.file-row__size') || {}).textContent || '',
      mod_unix: parseInt(row.getAttribute('data-entry-mtime'), 10) || 0,
      mode: row.getAttribute('data-entry-mode') || '',
      mode_octal: row.getAttribute('data-entry-octal') || '',
      uid: parseInt(row.getAttribute('data-entry-uid'), 10) || 0,
      gid: parseInt(row.getAttribute('data-entry-gid'), 10) || 0,
      is_symlink: row.getAttribute('data-entry-symlink') === '1',
      link_target: row.getAttribute('data-entry-link-target') || '',
      link_is_dir: row.getAttribute('data-entry-link-dir') === '1',
      broken_link: row.getAttribute('data-entry-broken') === '1',
      is_hidden: row.getAttribute('data-entry-hidden') === '1',
      ext: row.getAttribute('data-entry-ext') || ''
    };
  }

  function seedEntriesFromDOM() {
    currentEntries = entryRows().map(entryFromRow);
  }

  function syncSelectionUI() {
    var bar = qs('#file-selection-bar');
    var summary = qs('#file-sel-summary');
    var selectAll = qs('#file-select-all');
    var rows = entryRows();
    var visibleRows = rows.filter(function (r) { return r.style.display !== 'none'; });

    var totalBytes = 0;
    var visibleSelected = 0;
    rows.forEach(function (row) {
      var p = row.getAttribute('data-entry-path');
      var sel = selectedPaths.has(p);
      row.classList.toggle('is-selected', sel);
      var cb = row.querySelector('[data-row-check]');
      if (cb) cb.checked = sel;
      if (sel) {
        totalBytes += parseInt(row.getAttribute('data-entry-size'), 10) || 0;
        if (row.style.display !== 'none') visibleSelected++;
      }
    });

    if (selectAll) {
      selectAll.checked = visibleRows.length > 0 && visibleSelected === visibleRows.length;
      selectAll.indeterminate = visibleSelected > 0 && visibleSelected < visibleRows.length;
    }

    if (bar && summary) {
      if (selectedPaths.size === 0) {
        bar.hidden = true;
      } else {
        bar.hidden = false;
        summary.textContent = T('js.files.selected_count', {
          count: selectedPaths.size,
          size: formatBytes(totalBytes)
        });
      }
    }
  }

  function clearSelection() {
    selectedPaths.clear();
    lastCheckedPath = null;
    syncSelectionUI();
  }

  function syncClipboardUI() {
    var pill = qs('#file-clipboard-pill');
    var label = qs('#file-paste-label');
    if (!pill || !label) return;
    if (!clipboard || !clipboard.paths.length) {
      pill.hidden = true;
      return;
    }
    pill.hidden = false;
    label.textContent = T('js.files.clipboard_paste', {
      count: clipboard.paths.length,
      mode: clipboard.mode === 'cut' ? T('js.files.mode_cut') : T('js.files.mode_copy')
    });
  }

  function setClipboard(mode, paths) {
    if (!paths || !paths.length) return;
    clipboard = { mode: mode, paths: paths.slice() };
    syncClipboardUI();
    toast(T(mode === 'cut' ? 'js.files.clipboard_cut' : 'js.files.clipboard_copied', { count: paths.length }), 'info');
  }

  /* ── Bookmarks ──────────────────────────────────────────── */
  function bookmarksKey() {
    var c = context();
    return 'nodexia.files.bookmarks.' + (c ? c.serverId : '0');
  }

  function loadCustomBookmarks() {
    try {
      var raw = localStorage.getItem(bookmarksKey());
      var parsed = raw ? JSON.parse(raw) : [];
      return Array.isArray(parsed) ? parsed : [];
    } catch (e) { return []; }
  }

  function saveCustomBookmarks(list) {
    try { localStorage.setItem(bookmarksKey(), JSON.stringify(list)); } catch (e) {}
  }

  function renderBookmarks() {
    var bar = qs('#file-bookmarks-bar');
    var list = qs('#file-bookmarks-list');
    var toggleBtn = qs('#file-bookmarks-btn');
    var c = context();
    if (!bar || !list || !c) return;

    bar.hidden = !prefs.bookmarksOpen;
    if (toggleBtn) {
      toggleBtn.setAttribute('aria-expanded', prefs.bookmarksOpen ? 'true' : 'false');
      toggleBtn.classList.toggle('is-active', prefs.bookmarksOpen);
    }

    list.innerHTML = '';
    var builtins = [
      { label: '/', path: '/' },
      { label: '~', path: c.homeDir || '~' },
      { label: '/etc', path: '/etc' },
      { label: '/var/log', path: '/var/log' },
      { label: '/opt', path: '/opt' },
      { label: '/tmp', path: '/tmp' }
    ];
    var custom = loadCustomBookmarks();
    var seen = {};

    function addChip(label, targetPath, removable) {
      if (!targetPath || seen[targetPath]) return;
      seen[targetPath] = true;
      var chip = document.createElement('span');
      chip.className = 'file-bookmark-chip' + (c.currentDir === targetPath ? ' is-active' : '');
      chip.setAttribute('role', 'button');
      chip.setAttribute('tabindex', '0');
      chip.setAttribute('title', targetPath);
      var text = document.createElement('span');
      text.textContent = label;
      chip.appendChild(text);

      chip.addEventListener('click', function (e) {
        if (e.target.closest('.file-bookmark-chip__remove')) return;
        navigateTo(targetPath);
      });
      chip.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigateTo(targetPath); }
      });

      if (removable) {
        var rm = document.createElement('button');
        rm.type = 'button';
        rm.className = 'file-bookmark-chip__remove';
        rm.textContent = '×';
        rm.setAttribute('aria-label', T('common.delete'));
        rm.addEventListener('click', function (e) {
          e.stopPropagation();
          var next = loadCustomBookmarks().filter(function (p) { return p !== targetPath; });
          saveCustomBookmarks(next);
          renderBookmarks();
          toast(T('js.files.bookmark_removed'), 'info');
        });
        chip.appendChild(rm);
      }
      list.appendChild(chip);
    }

    builtins.forEach(function (b) { addChip(b.label, b.path, false); });
    custom.forEach(function (p) { addChip(p, p, true); });
  }

  /* ── History & AJAX directory rendering ─────────────────── */
  function syncNavButtons() {
    var c = context();
    var backBtn = qs('#file-back-btn');
    var fwdBtn = qs('#file-forward-btn');
    var upBtn = qs('#file-up-btn');
    if (backBtn) backBtn.disabled = historyIndex <= 0;
    if (fwdBtn) fwdBtn.disabled = historyIndex < 0 || historyIndex >= historyStack.length - 1;
    if (upBtn) upBtn.disabled = !(c && c.parentDir);
  }

  function pushHistory(dirPath) {
    if (!dirPath) return;
    if (historyIndex >= 0 && historyStack[historyIndex] === dirPath) {
      syncNavButtons();
      return;
    }
    historyStack = historyStack.slice(0, historyIndex + 1);
    historyStack.push(dirPath);
    if (historyStack.length > 50) historyStack.shift();
    historyIndex = historyStack.length - 1;
    syncNavButtons();
  }

  function setCredToggleLabel(toggleBtn, expanded) {
    if (!toggleBtn) return;
    toggleBtn.setAttribute('aria-expanded', expanded ? 'true' : 'false');
    toggleBtn.innerHTML = '';
    var icon = document.createElement('i');
    icon.setAttribute('data-lucide', expanded ? 'chevron-up' : 'chevron-down');
    icon.className = 'icon-in-button';
    var span = document.createElement('span');
    span.textContent = T(expanded ? 'js.files.creds_collapse' : 'js.files.creds_expand');
    toggleBtn.appendChild(icon);
    toggleBtn.appendChild(document.createTextNode(' '));
    toggleBtn.appendChild(span);
    renderIcons(toggleBtn);
  }

  function renderDirectoryListing(listing) {
    var c = context();
    if (!c) return;
    c.currentDir = listing.path || '/';
    c.parentDir = listing.parent || '';
    if (listing.home_dir) c.homeDir = listing.home_dir;

    var listingEl = qs('#file-listing');
    if (listingEl) {
      listingEl.setAttribute('data-current-path', c.currentDir);
      listingEl.setAttribute('data-parent-path', c.parentDir);
    }
    var workspace = qs('#file-workspace');
    if (workspace) workspace.hidden = false;
    var hint = qs('#file-connect-hint');
    if (hint) hint.hidden = true;
    var ssrRes = qs('#file-ssr-result');
    if (ssrRes) ssrRes.hidden = true;

    var pathInput = qs('#file-path');
    if (pathInput) pathInput.value = c.currentDir;
    buildBreadcrumb(c.currentDir, qs('#file-breadcrumb'));
    markPathReady(true);
    setPathEditing(false);

    // Mark session active and auto-collapse runtime creds on first successful connect
    if (!c.hasStoredCreds && hasAnyRuntimeCreds()) {
      var badge = qs('#file-creds-status');
      if (badge) badge.hidden = false;
      var toggleBtn = qs('#file-creds-toggle-btn');
      var credsBody = qs('#file-creds-body');
      if (toggleBtn && toggleBtn.hidden) {
        toggleBtn.hidden = false;
        if (credsBody) credsBody.hidden = true;
        setCredToggleLabel(toggleBtn, false);
      }
    }

    // Status bar counts + VFS disk stats
    var statsEl = qs('#file-dir-stats');
    if (statsEl) {
      statsEl.innerHTML = '';
      var pill = document.createElement('span');
      pill.className = 'file-stat-pill';
      pill.textContent = T('js.files.summary_counts', {
        dirs: listing.dir_count || 0,
        files: listing.file_count || 0,
        size: listing.total_size || '0 B'
      });
      statsEl.appendChild(pill);
      if ((listing.hidden_count || 0) > 0) {
        var hPill = document.createElement('span');
        hPill.className = 'file-stat-pill';
        hPill.textContent = T('js.files.summary_hidden', { count: listing.hidden_count });
        statsEl.appendChild(hPill);
      }
    }

    var vfsEl = qs('#file-vfs');
    var vfsLabel = qs('#file-vfs-label');
    var vfsFill = qs('#file-vfs-fill');
    if (vfsEl && listing.vfs && listing.vfs.available) {
      vfsEl.hidden = false;
      if (vfsLabel) {
        vfsLabel.textContent = T('js.files.disk_usage', {
          used: listing.vfs.used,
          total: listing.vfs.total,
          pct: listing.vfs.used_percent,
          free: listing.vfs.free
        });
      }
      if (vfsFill) {
        vfsFill.className = 'file-vfs__fill file-vfs__fill--' + (listing.vfs.severity || 'ok');
        vfsFill.style.width = Math.min(100, Math.max(0, listing.vfs.used_percent || 0)) + '%';
      }
    } else if (vfsEl) {
      vfsEl.hidden = true;
    }

    // Rebuild table rows cleanly
    var table = qs('#file-table');
    if (table) {
      Array.prototype.slice.call(table.querySelectorAll('.file-row')).forEach(function (r) { r.remove(); });
      var noMatch = qs('#file-no-match');

      if (c.parentDir) {
        var upRow = document.createElement('div');
        upRow.className = 'file-row file-row--dir file-row--parent';
        upRow.setAttribute('data-nav-path', c.parentDir);
        upRow.title = c.parentDir;

        var upChk = document.createElement('div');
        upChk.className = 'file-row__check-cell';
        upRow.appendChild(upChk);

        var upName = document.createElement('div');
        upName.className = 'file-row__name';
        var upIcon = document.createElement('i');
        upIcon.setAttribute('data-lucide', 'arrow-up');
        upIcon.className = 'file-row__icon';
        var upTxt = document.createElement('span');
        upTxt.className = 'file-row__name-text';
        upTxt.setAttribute('dir', 'ltr');
        upTxt.textContent = '..';
        upName.appendChild(upIcon);
        upName.appendChild(upTxt);
        upRow.appendChild(upName);

        var upMeta = document.createElement('div');
        upMeta.className = 'file-row__meta';
        upMeta.innerHTML = '<span class="file-row__mode"></span><span class="file-row__size">—</span><span class="file-row__date">—</span>';
        upRow.appendChild(upMeta);

        var upAct = document.createElement('div');
        upAct.className = 'file-row__actions';
        upRow.appendChild(upAct);
        table.insertBefore(upRow, noMatch);
      }

      var entries = Array.isArray(listing.entries) ? listing.entries : [];
      currentEntries = entries;

      entries.forEach(function (entry) {
        var isDirLike = entry.kind === 'directory' || !!entry.link_is_dir;
        var row = document.createElement('div');
        row.className = 'file-row ' + (isDirLike ? 'file-row--dir' : 'file-row--file') +
          (entry.is_symlink ? ' file-row--symlink' : '') +
          (entry.broken_link ? ' file-row--broken' : '') +
          (entry.is_hidden ? ' file-row--hidden' : '');

        if (isDirLike) row.setAttribute('data-nav-path', entry.path);
        else row.setAttribute('data-file-path', entry.path);

        row.setAttribute('data-entry-kind', entry.kind || 'file');
        row.setAttribute('data-entry-name', entry.name || '');
        row.setAttribute('data-entry-path', entry.path || '');
        row.setAttribute('data-entry-size', String(entry.size_bytes || 0));
        row.setAttribute('data-entry-mtime', String(entry.mod_unix || 0));
        row.setAttribute('data-entry-mode', entry.mode || '');
        row.setAttribute('data-entry-octal', entry.mode_octal || '');
        row.setAttribute('data-entry-uid', String(entry.uid || 0));
        row.setAttribute('data-entry-gid', String(entry.gid || 0));
        row.setAttribute('data-entry-symlink', entry.is_symlink ? '1' : '0');
        row.setAttribute('data-entry-link-target', entry.link_target || '');
        row.setAttribute('data-entry-link-dir', entry.link_is_dir ? '1' : '0');
        row.setAttribute('data-entry-broken', entry.broken_link ? '1' : '0');
        row.setAttribute('data-entry-hidden', entry.is_hidden ? '1' : '0');
        row.setAttribute('data-entry-ext', entry.ext || '');
        row.title = entry.path || '';

        var chkCell = document.createElement('div');
        chkCell.className = 'file-row__check-cell';
        var chk = document.createElement('input');
        chk.type = 'checkbox';
        chk.className = 'file-check file-row__check';
        chk.setAttribute('data-row-check', '');
        chk.setAttribute('aria-label', entry.name || '');
        chkCell.appendChild(chk);
        row.appendChild(chkCell);

        var nameCell = document.createElement('div');
        nameCell.className = 'file-row__name';
        var icon = document.createElement('i');
        icon.setAttribute('data-lucide', iconForEntry(entry));
        icon.className = 'file-row__icon';
        var nameSpan = document.createElement('span');
        nameSpan.className = 'file-row__name-text';
        nameSpan.setAttribute('dir', 'ltr');
        nameSpan.title = entry.name || '';
        nameSpan.textContent = entry.name || '';
        nameCell.appendChild(icon);
        nameCell.appendChild(nameSpan);
        if (entry.is_symlink && entry.link_target) {
          var symSpan = document.createElement('span');
          symSpan.className = 'file-row__symlink-target' + (entry.broken_link ? ' is-broken' : '');
          symSpan.setAttribute('dir', 'ltr');
          symSpan.title = entry.link_target;
          symSpan.textContent = '→ ' + entry.link_target;
          nameCell.appendChild(symSpan);
        }
        row.appendChild(nameCell);

        var metaCell = document.createElement('div');
        metaCell.className = 'file-row__meta';
        var modeSpan = document.createElement('span');
        modeSpan.className = 'file-row__mode';
        modeSpan.setAttribute('dir', 'ltr');
        modeSpan.title = (entry.mode_octal || '') + ' (' + (entry.uid || 0) + ':' + (entry.gid || 0) + ')';
        modeSpan.textContent = entry.mode || '';
        if (entry.mode_octal) {
          var oct = document.createElement('em');
          oct.className = 'file-row__octal';
          oct.textContent = entry.mode_octal;
          modeSpan.appendChild(document.createTextNode(' '));
          modeSpan.appendChild(oct);
        }
        var sizeSpan = document.createElement('span');
        sizeSpan.className = 'file-row__size';
        sizeSpan.setAttribute('dir', 'ltr');
        sizeSpan.textContent = isDirLike ? '—' : (entry.size || '0 B');
        var dateSpan = document.createElement('span');
        dateSpan.className = 'file-row__date';
        dateSpan.setAttribute('dir', 'ltr');
        dateSpan.textContent = entry.modified_at || '—';
        metaCell.appendChild(modeSpan);
        metaCell.appendChild(sizeSpan);
        metaCell.appendChild(dateSpan);
        row.appendChild(metaCell);

        var actCell = document.createElement('div');
        actCell.className = 'file-row__actions';
        var menuBtn = document.createElement('button');
        menuBtn.type = 'button';
        menuBtn.className = 'file-row__menu-btn';
        menuBtn.setAttribute('data-menu-trigger', '');
        menuBtn.setAttribute('aria-label', entry.name || '');
        menuBtn.setAttribute('aria-haspopup', 'true');
        menuBtn.setAttribute('aria-expanded', 'false');
        var menuIcon = document.createElement('i');
        menuIcon.setAttribute('data-lucide', 'more-vertical');
        menuBtn.appendChild(menuIcon);
        actCell.appendChild(menuBtn);
        row.appendChild(actCell);

        table.insertBefore(row, noMatch);
      });

      var emptyEl = qs('#file-empty-dir');
      if (emptyEl) emptyEl.hidden = entries.length !== 0;
    }

    clearSelection();
    applySort();
    applyFilter();
    renderBookmarks();
    syncNavButtons();
    renderIcons(qs('#file-listing'));
  }

  function navigateTo(dirPath, opts) {
    opts = opts || {};
    var c = context();
    if (!c) return Promise.resolve();
    if (!ensureCredsOrPrompt()) return Promise.resolve();

    closeMenu();
    setRefreshSpinning(true);
    var target = dirPath || c.currentDir || '~';

    return postOp('list', { path: target }).then(function (data) {
      setRefreshSpinning(false);
      if (data && data.listing) {
        renderDirectoryListing(data.listing);
        if (opts.pushHistory !== false) pushHistory(data.listing.path);
      }
    }).catch(function (err) {
      setRefreshSpinning(false);
      toast(err.message || T('js.files.request_failed', { status: 0 }), 'error');
    });
  }

  function reloadCurrent() {
    var c = context();
    if (!c) { stopSpinners(); return Promise.resolve(); }
    return navigateTo(c.currentDir || '~', { pushHistory: false });
  }

  /* ── Upload (XHR for progress + folder traversal) ───────── */
  var activeUploadXhr = null;
  var uploadCancelled = false;

  registerCleanup(function () {
    uploadCancelled = true;
    if (activeUploadXhr) {
      try { activeUploadXhr.abort(); } catch (e) {}
      activeUploadXhr = null;
    }
    if (activeFetchController) {
      try { activeFetchController.abort(); } catch (e) {}
      activeFetchController = null;
    }
    stopSpinners();
  });

  function uploadOne(item, onProgress, retryAuth) {
    return new Promise(function (resolve, reject) {
      var c = context();
      if (!c) {
        reject(new Error(T('js.files.upload_network_error')));
        return;
      }
      var body = new FormData();
      // Credentials and target paths MUST precede the file part: the server reads
      // leading fields, then streams the file straight to SFTP.
      body.append('path', c.currentDir || '/');
      if (item.relativePath) body.append('relative_path', item.relativePath);
      collectCredsFormData(body);
      body.append('file', item.file, item.file.name);

      var xhr = new XMLHttpRequest();
      activeUploadXhr = xhr;
      xhr.open('POST', opsURL('upload'));
      var csrf = currentToken(c.form);
      if (csrf) xhr.setRequestHeader('X-CSRF-Token', csrf);
      if (xhr.upload) {
        xhr.upload.onprogress = function (e) {
          if (e.lengthComputable && onProgress) onProgress(e.loaded / e.total);
        };
      }
      xhr.onload = function () {
        if (activeUploadXhr === xhr) activeUploadXhr = null;
        if (xhr.status === 403 && !retryAuth) {
          refreshCSRFToken().then(function () {
            uploadOne(item, onProgress, true).then(resolve, reject);
          });
          return;
        }
        var data = {};
        try { data = JSON.parse(xhr.responseText || '{}'); } catch (e) {}
        if (xhr.status >= 200 && xhr.status < 300 && data && data.ok) {
          resolve(data);
        } else {
          var statusCode = xhr.status || 0;
          var msg = (data && data.error) ||
            (statusCode === 0 ? T('js.files.upload_network_error') : T('js.files.upload_failed', { status: statusCode }));
          reject(new Error(msg));
        }
      };
      xhr.onerror = function () {
        if (activeUploadXhr === xhr) activeUploadXhr = null;
        reject(new Error(T('js.files.upload_network_error')));
      };
      xhr.onabort = function () {
        if (activeUploadXhr === xhr) activeUploadXhr = null;
        reject(new Error(T('js.files.upload_aborted')));
      };
      try {
        xhr.send(body);
      } catch (e) {
        if (activeUploadXhr === xhr) activeUploadXhr = null;
        reject(new Error(T('js.files.upload_network_error')));
      }
    });
  }

  function uploadItems(items) {
    if (!items || !items.length) return;
    if (!ensureCredsOrPrompt()) return;

    var panel = qs('#file-uploads');
    var listEl = qs('#file-uploads-list');
    var titleEl = qs('#file-uploads-title');
    var clearBtn = qs('#file-uploads-clear-btn');
    if (!panel || !listEl) return;

    uploadCancelled = false;
    panel.hidden = false;
    listEl.innerHTML = '';
    if (titleEl) titleEl.textContent = T('js.files.upload_queue_title') + ' (' + items.length + ')';
    if (clearBtn) {
      clearBtn.textContent = T('js.files.upload_clear');
      clearBtn.hidden = true;
    }

    var rows = [];
    items.forEach(function (item) {
      var row = document.createElement('div');
      row.className = 'file-upload';
      var name = document.createElement('span');
      name.className = 'file-upload__name';
      name.textContent = item.relativePath || item.file.name;
      name.title = item.relativePath || item.file.name;

      var bar = document.createElement('div');
      bar.className = 'file-upload__bar';
      var fill = document.createElement('div');
      fill.className = 'file-upload__fill';
      bar.appendChild(fill);

      var pct = document.createElement('span');
      pct.className = 'file-upload__pct';
      pct.textContent = '0%';

      var cancelBtn = document.createElement('button');
      cancelBtn.type = 'button';
      cancelBtn.className = 'file-icon-btn file-icon-btn--compact';
      cancelBtn.title = T('js.files.upload_cancel');
      cancelBtn.innerHTML = '<i data-lucide="x"></i>';

      var rec = { row: row, fill: fill, pct: pct, cancelBtn: cancelBtn, cancelled: false };
      cancelBtn.addEventListener('click', function () {
        rec.cancelled = true;
        if (rec.isActive && activeUploadXhr) {
          try { activeUploadXhr.abort(); } catch (e) {}
        } else {
          row.classList.add('file-upload--error');
          pct.textContent = T('js.files.upload_aborted');
          cancelBtn.hidden = true;
        }
      });

      row.appendChild(name);
      row.appendChild(bar);
      row.appendChild(pct);
      row.appendChild(cancelBtn);
      listEl.appendChild(row);
      rows.push(rec);
    });
    renderIcons(listEl);

    setUploadBusy(true);

    var index = 0;
    var errors = [];
    var anyOk = false;

    function next() {
      if (uploadCancelled || index >= items.length) {
        stopSpinners();
        if (clearBtn) clearBtn.hidden = false;
        if (anyOk) reloadCurrent();
        if (errors.length === 1) toast(errors[0], 'error');
        else if (errors.length > 1) toast(T('js.files.uploads_failed') + ' (' + errors[0] + ')', 'error');
        return;
      }

      var item = items[index];
      var ui = rows[index];
      if (ui.cancelled) {
        index += 1;
        next();
        return;
      }

      ui.isActive = true;
      uploadOne(item, function (frac) {
        var p = Math.round(frac * 100);
        ui.fill.style.width = p + '%';
        ui.pct.textContent = p + '%';
      }).then(function () {
        anyOk = true;
        ui.isActive = false;
        ui.row.classList.add('file-upload--ok');
        ui.fill.style.width = '100%';
        ui.pct.textContent = T('js.files.upload_done');
        ui.cancelBtn.hidden = true;
      }).catch(function (err) {
        ui.isActive = false;
        var errMsg = (err && err.message) ? err.message : T('js.files.upload_network_error');
        if (!ui.cancelled) errors.push(errMsg);
        ui.row.classList.add('file-upload--error');
        ui.pct.textContent = T('js.failed');
        ui.row.title = errMsg;
        ui.cancelBtn.hidden = true;
      }).then(function () {
        index += 1;
        next();
      });
    }
    next();
  }

  function uploadFileList(fileList, useRelativePath) {
    var items = Array.prototype.map.call(fileList, function (f) {
      return {
        file: f,
        relativePath: useRelativePath && f.webkitRelativePath ? f.webkitRelativePath : ''
      };
    });
    uploadItems(items);
  }

  /* ── Recursive drag-and-drop directory reader ───────────── */
  function readEntryItems(entry, prefix) {
    return new Promise(function (resolve) {
      if (!entry) { resolve([]); return; }
      if (entry.isFile) {
        entry.file(function (file) {
          resolve([{ file: file, relativePath: prefix ? prefix + '/' + file.name : '' }]);
        }, function () { resolve([]); });
        return;
      }
      if (entry.isDirectory) {
        var dirReader = entry.createReader();
        var allEntries = [];
        var readBatch = function () {
          dirReader.readEntries(function (batch) {
            if (!batch || !batch.length) {
              var nextPrefix = prefix ? prefix + '/' + entry.name : entry.name;
              Promise.all(allEntries.map(function (child) {
                return readEntryItems(child, nextPrefix);
              })).then(function (nested) {
                var flat = [];
                nested.forEach(function (arr) { flat = flat.concat(arr); });
                resolve(flat);
              });
              return;
            }
            allEntries = allEntries.concat(Array.prototype.slice.call(batch));
            readBatch();
          }, function () { resolve([]); });
        };
        readBatch();
        return;
      }
      resolve([]);
    });
  }

  function collectDroppedItems(dataTransfer) {
    var dtItems = dataTransfer && dataTransfer.items;
    if (dtItems && dtItems.length && dtItems[0].webkitGetAsEntry) {
      var entries = [];
      for (var i = 0; i < dtItems.length; i++) {
        var entry = dtItems[i].webkitGetAsEntry();
        if (entry) entries.push(entry);
      }
      return Promise.all(entries.map(function (en) { return readEntryItems(en, ''); })).then(function (groups) {
        var flat = [];
        groups.forEach(function (g) { flat = flat.concat(g); });
        return flat;
      });
    }
    var files = (dataTransfer && dataTransfer.files) || [];
    return Promise.resolve(Array.prototype.map.call(files, function (f) {
      return { file: f, relativePath: '' };
    }));
  }

  /* ── CSP-safe Action Dialog ─────────────────────────────── */
  var dialogActive = null;

  function getDialogNodes() {
    var dialog = qs('#file-dialog');
    if (!dialog) return null;
    return {
      dialog: dialog,
      panel: dialog.querySelector('.modal__dialog'),
      title: dialog.querySelector('[data-file-dialog-title]'),
      desc: dialog.querySelector('[data-file-dialog-desc]'),
      list: dialog.querySelector('[data-file-dialog-list]'),
      field: dialog.querySelector('[data-file-dialog-field]'),
      input: dialog.querySelector('[data-file-dialog-input]'),
      hint: dialog.querySelector('[data-file-dialog-hint]'),
      field2: dialog.querySelector('[data-file-dialog-field2]'),
      input2: dialog.querySelector('[data-file-dialog-input2]'),
      selectWrap: dialog.querySelector('[data-file-dialog-select-wrap]'),
      select: dialog.querySelector('[data-file-dialog-select]'),
      error: dialog.querySelector('[data-file-dialog-error]'),
      confirmBtn: dialog.querySelector('[data-file-dialog-confirm]'),
    };
  }

  function clearDialogError(nodes) {
    if (!nodes) return;
    if (nodes.error) { nodes.error.textContent = ''; nodes.error.hidden = true; }
    if (nodes.input) nodes.input.removeAttribute('aria-invalid');
    if (nodes.input2) nodes.input2.removeAttribute('aria-invalid');
  }

  function closeDialog(restoreFocus) {
    var active = dialogActive;
    dialogActive = null;
    var nodes = getDialogNodes();
    if (nodes) {
      nodes.dialog.classList.remove('is-open');
      nodes.dialog.hidden = true;
      clearDialogError(nodes);
    }
    if (restoreFocus && active && active.returnFocusTo &&
        active.returnFocusTo.isConnected && typeof active.returnFocusTo.focus === 'function') {
      active.returnFocusTo.focus();
    }
  }

  function submitDialog() {
    if (!dialogActive) return;
    var active = dialogActive;
    var nodes = getDialogNodes();
    if (!nodes) { closeDialog(false); return; }

    var value = nodes.input ? nodes.input.value : '';
    var value2 = nodes.input2 ? nodes.input2.value : '';
    var selectVal = nodes.select ? nodes.select.value : '';
    if (active.mode === 'input' && typeof active.validate === 'function') {
      var errMsg = active.validate(value, value2, selectVal);
      if (errMsg) {
        if (nodes.error) { nodes.error.textContent = errMsg; nodes.error.hidden = false; }
        if (nodes.input) { nodes.input.setAttribute('aria-invalid', 'true'); nodes.input.focus(); }
        return;
      }
    }

    closeDialog(true);
    if (typeof active.onConfirm === 'function') {
      active.onConfirm(value, value2, selectVal);
    }
  }

  function openDialog(opts) {
    var nodes = getDialogNodes();
    if (!nodes) return;
    closeMenu();

    var fallbackFocus = (document.activeElement && root.contains(document.activeElement))
      ? document.activeElement
      : null;

    dialogActive = {
      mode: opts.mode || 'confirm',
      validate: opts.validate || null,
      onConfirm: opts.onConfirm || null,
      returnFocusTo: opts.returnFocusTo || fallbackFocus,
    };

    if (nodes.title) nodes.title.textContent = opts.title || '';
    if (nodes.desc) nodes.desc.textContent = opts.desc || '';
    if (nodes.confirmBtn) {
      nodes.confirmBtn.textContent = opts.confirmLabel || '';
      nodes.confirmBtn.classList.toggle('btn--danger', !!opts.danger);
      nodes.confirmBtn.classList.toggle('btn--primary', !opts.danger);
    }
    clearDialogError(nodes);

    if (nodes.list) {
      if (Array.isArray(opts.items) && opts.items.length) {
        nodes.list.innerHTML = '';
        opts.items.slice(0, 20).forEach(function (item) {
          var li = document.createElement('li');
          li.textContent = item;
          nodes.list.appendChild(li);
        });
        if (opts.items.length > 20) {
          var more = document.createElement('li');
          more.textContent = '… +' + (opts.items.length - 20);
          nodes.list.appendChild(more);
        }
        nodes.list.hidden = false;
      } else {
        nodes.list.hidden = true;
      }
    }

    if (dialogActive.mode === 'input' && nodes.field && nodes.input) {
      nodes.field.hidden = false;
      nodes.input.value = opts.initialValue || '';
      nodes.input.placeholder = opts.placeholder || '';
    } else if (nodes.field) {
      nodes.field.hidden = true;
      if (nodes.input) nodes.input.value = '';
    }

    if (nodes.hint) {
      if (opts.hint) { nodes.hint.textContent = opts.hint; nodes.hint.hidden = false; }
      else { nodes.hint.textContent = ''; nodes.hint.hidden = true; }
    }

    if (opts.secondInput && nodes.field2 && nodes.input2) {
      nodes.field2.hidden = false;
      nodes.input2.value = opts.secondValue || '';
      nodes.input2.placeholder = opts.secondPlaceholder || '';
    } else if (nodes.field2) {
      nodes.field2.hidden = true;
      if (nodes.input2) nodes.input2.value = '';
    }

    if (Array.isArray(opts.selectOptions) && opts.selectOptions.length && nodes.selectWrap && nodes.select) {
      nodes.select.innerHTML = '';
      opts.selectOptions.forEach(function (opt) {
        var o = document.createElement('option');
        o.value = opt.value;
        o.textContent = opt.label;
        if (opt.selected) o.selected = true;
        nodes.select.appendChild(o);
      });
      nodes.selectWrap.hidden = false;
    } else if (nodes.selectWrap) {
      nodes.selectWrap.hidden = true;
    }

    nodes.dialog.hidden = false;
    renderIcons(nodes.dialog);
    requestAnimationFrame(function () {
      if (dialogActive) nodes.dialog.classList.add('is-open');
    });

    if (dialogActive.mode === 'input' && nodes.input) {
      nodes.input.focus();
      if (nodes.input.value) nodes.input.select();
    } else if (nodes.confirmBtn) {
      nodes.confirmBtn.focus();
    }
  }

  function focusablesInDialog(nodes) {
    if (!nodes || !nodes.panel) return [];
    return Array.prototype.filter.call(
      nodes.panel.querySelectorAll('button:not([disabled]), input:not([disabled]), select:not([disabled])'),
      function (el) {
        if (el.hidden) return false;
        var hiddenParent = el.closest('[hidden]');
        return !hiddenParent || hiddenParent === document.documentElement;
      }
    );
  }

  function initDialog() {
    var nodes = getDialogNodes();
    if (!nodes) return;
    nodes.dialog.querySelectorAll('[data-file-dialog-close]').forEach(function (el) {
      el.addEventListener('click', function () { closeDialog(true); });
    });
    if (nodes.confirmBtn) nodes.confirmBtn.addEventListener('click', submitDialog);
    [nodes.input, nodes.input2].forEach(function (inp) {
      if (!inp) return;
      inp.addEventListener('input', function () { clearDialogError(nodes); });
      inp.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') {
          e.preventDefault();
          e.stopPropagation();
          submitDialog();
        }
      });
    });
    registerCleanup(function () { closeDialog(false); });
  }

  /* ── File Viewer & Code/Config/Log Editor Modal ─────────── */
  var editorState = {
    open: false,
    path: '',
    name: '',
    isNew: false,
    editable: false,
    dirty: false,
    isTail: false,
    wrapped: false,
    originalContent: '',
    autoTailTimer: null,
    findIndex: -1
  };

  function stopAutoTail() {
    if (editorState.autoTailTimer !== null) {
      clearInterval(editorState.autoTailTimer);
      editorState.autoTailTimer = null;
    }
    var cb = qs('#file-editor-autotail');
    if (cb) cb.checked = false;
  }

  function updateEditorGutter() {
    var ta = qs('#file-editor-textarea');
    var gutter = qs('#file-editor-gutter');
    if (!ta || !gutter) return;
    if (editorState.wrapped) {
      gutter.hidden = true;
      return;
    }
    gutter.hidden = false;
    var lines = (ta.value.match(/\n/g) || []).length + 1;
    var nums = [];
    for (var i = 1; i <= lines; i++) nums.push(i);
    gutter.textContent = nums.join('\n');
    gutter.scrollTop = ta.scrollTop;
  }

  function updateEditorCursorMeta() {
    var ta = qs('#file-editor-textarea');
    var cursorEl = qs('#file-editor-cursor');
    if (!ta || !cursorEl) return;
    var pos = ta.selectionStart || 0;
    var before = ta.value.slice(0, pos);
    var line = (before.match(/\n/g) || []).length + 1;
    var lastNL = before.lastIndexOf('\n');
    var col = pos - lastNL;
    var totalLines = (ta.value.match(/\n/g) || []).length + 1;
    cursorEl.textContent = T('js.files.editor_cursor', { line: line, col: col, lines: totalLines });
  }

  function syncEditorDirty() {
    var ta = qs('#file-editor-textarea');
    var dirtyEl = qs('#file-editor-dirty');
    var saveBtn = qs('#file-editor-save-btn');
    if (!ta) return;
    editorState.dirty = editorState.editable && (ta.value !== editorState.originalContent || editorState.isNew);
    if (dirtyEl) dirtyEl.hidden = !editorState.dirty;
    if (saveBtn) saveBtn.disabled = !editorState.editable || !editorState.dirty;
  }

  function forceCloseEditor() {
    stopAutoTail();
    editorState.open = false;
    editorState.dirty = false;
    var modal = qs('#file-editor-modal');
    if (modal) {
      modal.classList.remove('is-open');
      modal.hidden = true;
    }
    document.body.classList.remove('modal-open');
  }

  function closeEditorModal() {
    if (!editorState.open) return;
    if (editorState.dirty) {
      openDialog({
        mode: 'confirm',
        title: T('js.files.editor_unsaved_title'),
        desc: T('js.files.editor_unsaved_desc', { name: editorState.name }),
        confirmLabel: T('js.files.editor_discard'),
        danger: true,
        onConfirm: forceCloseEditor
      });
      return;
    }
    forceCloseEditor();
  }

  function saveEditorFile() {
    if (!editorState.open || !editorState.editable) return;
    var ta = qs('#file-editor-textarea');
    var saveBtn = qs('#file-editor-save-btn');
    if (!ta) return;
    if (saveBtn) saveBtn.disabled = true;

    postOp('write', {
      path: editorState.path,
      content: ta.value,
      create_only: false
    }).then(function (data) {
      editorState.originalContent = ta.value;
      editorState.isNew = false;
      syncEditorDirty();
      var metaEl = qs('#file-editor-meta');
      if (metaEl && data.file) {
        metaEl.textContent = (data.file.size || '') + ' · ' + (data.file.mode_octal || '') + ' · ' + (data.file.modified_at || '');
      }
      toast(T('js.files.editor_saved', { name: editorState.name }), 'success');
      reloadCurrent();
    }).catch(function (err) {
      if (saveBtn) saveBtn.disabled = false;
      toast(err.message, 'error');
    });
  }

  function openEditorModal(filePath, opts) {
    opts = opts || {};
    if (!ensureCredsOrPrompt()) return;
    closeMenu();

    var modal = qs('#file-editor-modal');
    if (!modal) return;

    stopAutoTail();
    editorState.open = true;
    editorState.path = filePath;
    editorState.name = opts.name || filePath.split('/').pop() || filePath;
    editorState.isNew = !!opts.isNew;
    editorState.editable = false;
    editorState.dirty = false;
    editorState.isTail = !!opts.tail;
    editorState.originalContent = '';
    editorState.findIndex = -1;

    var titleEl = qs('#file-editor-title');
    var pathEl = qs('#file-editor-path');
    var loadingEl = qs('#file-editor-loading');
    var textWrap = qs('#file-editor-text-wrap');
    var imgWrap = qs('#file-editor-image-wrap');
    var binWrap = qs('#file-editor-binary-wrap');
    var bannerEl = qs('#file-editor-banner');
    var findBar = qs('#file-editor-find-bar');
    var autoTailWrap = qs('#file-editor-autotail-wrap');
    var metaEl = qs('#file-editor-meta');
    var cursorEl = qs('#file-editor-cursor');
    var saveBtn = qs('#file-editor-save-btn');
    var dirtyEl = qs('#file-editor-dirty');

    if (titleEl) titleEl.textContent = editorState.name;
    if (pathEl) pathEl.textContent = filePath;
    if (loadingEl) loadingEl.hidden = false;
    if (textWrap) textWrap.hidden = true;
    if (imgWrap) imgWrap.hidden = true;
    if (binWrap) binWrap.hidden = true;
    if (bannerEl) bannerEl.hidden = true;
    if (findBar) findBar.hidden = true;
    if (autoTailWrap) autoTailWrap.hidden = true;
    if (dirtyEl) dirtyEl.hidden = true;
    if (saveBtn) saveBtn.disabled = true;
    if (metaEl) metaEl.textContent = '';
    if (cursorEl) cursorEl.textContent = '';

    modal.hidden = false;
    document.body.classList.add('modal-open');
    renderIcons(modal);
    requestAnimationFrame(function () { modal.classList.add('is-open'); });

    if (opts.isNew) {
      if (loadingEl) loadingEl.hidden = true;
      if (textWrap) textWrap.hidden = false;
      var taNew = qs('#file-editor-textarea');
      if (taNew) {
        taNew.value = '';
        taNew.readOnly = false;
        editorState.editable = true;
        syncEditorDirty();
        updateEditorGutter();
        updateEditorCursorMeta();
        taNew.focus();
      }
      return;
    }

    loadEditorContent(filePath, !!opts.tail, false);
  }

  function loadEditorContent(filePath, tailMode, silentRefresh) {
    var loadingEl = qs('#file-editor-loading');
    var textWrap = qs('#file-editor-text-wrap');
    var imgWrap = qs('#file-editor-image-wrap');
    var binWrap = qs('#file-editor-binary-wrap');
    var bannerEl = qs('#file-editor-banner');
    var bannerText = qs('#file-editor-banner-text');
    var bannerAction = qs('#file-editor-banner-action');
    var autoTailWrap = qs('#file-editor-autotail-wrap');
    var metaEl = qs('#file-editor-meta');
    var ta = qs('#file-editor-textarea');
    var img = qs('#file-editor-image');

    if (!silentRefresh && loadingEl) loadingEl.hidden = false;

    postOp('read', { path: filePath, tail: tailMode }).then(function (data) {
      if (!editorState.open || editorState.path !== filePath) return;
      if (loadingEl) loadingEl.hidden = true;
      var f = data.file || {};
      if (metaEl) {
        metaEl.textContent = (f.size || '') + ' · ' + (f.mode_octal || '') + ' (' + (f.uid || 0) + ':' + (f.gid || 0) + ') · ' + (f.modified_at || '');
      }

      if (f.is_image && f.data_url) {
        if (imgWrap && img) {
          img.src = f.data_url;
          img.alt = f.name || '';
          imgWrap.hidden = false;
        }
        editorState.editable = false;
        syncEditorDirty();
        return;
      }

      if (f.is_binary) {
        if (binWrap) {
          binWrap.hidden = false;
          var binMsg = qs('#file-editor-binary-msg');
          var forceTailBtn = qs('#file-editor-force-tail-btn');
          var binDlBtn = qs('#file-editor-binary-dl-btn');
          if (binMsg) binMsg.textContent = T('js.files.editor_binary_banner', { size: f.size || '' });
          if (forceTailBtn) forceTailBtn.textContent = T('js.files.editor_force_tail');
          if (binDlBtn) binDlBtn.textContent = T('js.files.menu_download');
        }
        editorState.editable = false;
        syncEditorDirty();
        return;
      }

      if (textWrap && ta) {
        textWrap.hidden = false;
        var prevScrollBottom = silentRefresh && (ta.scrollHeight - ta.scrollTop - ta.clientHeight < 40);
        ta.value = f.content || '';
        editorState.originalContent = ta.value;
        editorState.isTail = !!f.is_tail;
        editorState.editable = !!f.editable;
        ta.readOnly = !editorState.editable;
        syncEditorDirty();
        updateEditorGutter();
        updateEditorCursorMeta();
        if (f.is_tail && (prevScrollBottom || !silentRefresh)) {
          ta.scrollTop = ta.scrollHeight;
        }
      }

      if (bannerEl && bannerText) {
        if (f.is_tail) {
          bannerEl.hidden = false;
          bannerText.textContent = T('js.files.editor_tail_banner', { size: f.size || '' });
          if (bannerAction) bannerAction.hidden = true;
          if (autoTailWrap) autoTailWrap.hidden = false;
        } else if (f.truncated) {
          bannerEl.hidden = false;
          bannerText.textContent = T('js.files.editor_truncated_banner', { size: f.size || '' });
          if (bannerAction) {
            bannerAction.hidden = false;
            bannerAction.textContent = T('files.editor_tail');
          }
          if (autoTailWrap) autoTailWrap.hidden = true;
        } else {
          bannerEl.hidden = true;
          if (autoTailWrap) autoTailWrap.hidden = true;
        }
      }
    }).catch(function (err) {
      if (loadingEl) loadingEl.hidden = true;
      toast(err.message, 'error');
      forceCloseEditor();
    });
  }

  function findInEditor(forward) {
    var ta = qs('#file-editor-textarea');
    var input = qs('#file-editor-find-input');
    var countEl = qs('#file-editor-find-count');
    if (!ta || !input) return;
    var query = input.value;
    if (!query) {
      if (countEl) countEl.textContent = '';
      return;
    }
    var hay = ta.value.toLowerCase();
    var needle = query.toLowerCase();
    var positions = [];
    var idx = hay.indexOf(needle);
    while (idx !== -1) {
      positions.push(idx);
      idx = hay.indexOf(needle, idx + needle.length);
    }
    if (countEl) countEl.textContent = T('js.files.editor_find_matches', { count: positions.length });
    if (!positions.length) return;

    var cur = ta.selectionStart || 0;
    var target = positions[0];
    if (forward) {
      for (var i = 0; i < positions.length; i++) {
        if (positions[i] > cur) { target = positions[i]; break; }
      }
    } else {
      target = positions[positions.length - 1];
      for (var j = positions.length - 1; j >= 0; j--) {
        if (positions[j] < cur) { target = positions[j]; break; }
      }
    }
    ta.setSelectionRange(target, target + query.length);
    var linesBefore = ta.value.slice(0, target).split('\n').length - 1;
    var lineHeight = parseFloat(window.getComputedStyle(ta).lineHeight) || 20;
    ta.scrollTop = Math.max(0, linesBefore * lineHeight - ta.clientHeight * 0.35);
    ta.focus();
    updateEditorCursorMeta();
  }

  function initEditor() {
    var modal = qs('#file-editor-modal');
    if (!modal) return;

    modal.querySelectorAll('[data-editor-close]').forEach(function (el) {
      el.addEventListener('click', closeEditorModal);
    });

    var ta = qs('#file-editor-textarea');
    var gutter = qs('#file-editor-gutter');
    if (ta) {
      ta.addEventListener('input', function () {
        syncEditorDirty();
        updateEditorGutter();
        updateEditorCursorMeta();
      });
      ta.addEventListener('scroll', function () {
        if (gutter) gutter.scrollTop = ta.scrollTop;
      });
      ta.addEventListener('click', updateEditorCursorMeta);
      ta.addEventListener('keyup', updateEditorCursorMeta);
      ta.addEventListener('keydown', function (e) {
        if (e.key === 'Tab' && !ta.readOnly) {
          e.preventDefault();
          var start = ta.selectionStart;
          var end = ta.selectionEnd;
          ta.value = ta.value.substring(0, start) + '  ' + ta.value.substring(end);
          ta.selectionStart = ta.selectionEnd = start + 2;
          syncEditorDirty();
          updateEditorGutter();
          return;
        }
        if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
          e.preventDefault();
          saveEditorFile();
          return;
        }
        if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'f') {
          e.preventDefault();
          var fb = qs('#file-editor-find-bar');
          var fi = qs('#file-editor-find-input');
          if (fb) fb.hidden = false;
          if (fi) { fi.focus(); fi.select(); }
        }
      });
    }

    var saveBtn = qs('#file-editor-save-btn');
    if (saveBtn) saveBtn.addEventListener('click', saveEditorFile);

    var dlBtn = qs('#file-editor-download-btn');
    if (dlBtn) dlBtn.addEventListener('click', function () {
      if (editorState.path) triggerDownload(editorState.path);
    });

    var binDlBtn = qs('#file-editor-binary-dl-btn');
    if (binDlBtn) binDlBtn.addEventListener('click', function () {
      if (editorState.path) triggerDownload(editorState.path);
    });

    var forceTailBtn = qs('#file-editor-force-tail-btn');
    if (forceTailBtn) forceTailBtn.addEventListener('click', function () {
      if (editorState.path) {
        var binWrap = qs('#file-editor-binary-wrap');
        if (binWrap) binWrap.hidden = true;
        loadEditorContent(editorState.path, true, false);
      }
    });

    var tailBtn = qs('#file-editor-tail-btn');
    if (tailBtn) tailBtn.addEventListener('click', function () {
      if (editorState.path) loadEditorContent(editorState.path, true, false);
    });

    var bannerAction = qs('#file-editor-banner-action');
    if (bannerAction) bannerAction.addEventListener('click', function () {
      if (editorState.path) loadEditorContent(editorState.path, true, false);
    });

    var wrapBtn = qs('#file-editor-wrap-btn');
    if (wrapBtn && ta) {
      wrapBtn.addEventListener('click', function () {
        editorState.wrapped = !editorState.wrapped;
        ta.wrap = editorState.wrapped ? 'soft' : 'off';
        wrapBtn.setAttribute('aria-pressed', editorState.wrapped ? 'true' : 'false');
        wrapBtn.classList.toggle('is-active', editorState.wrapped);
        updateEditorGutter();
      });
    }

    var autoTailCb = qs('#file-editor-autotail');
    if (autoTailCb) {
      autoTailCb.addEventListener('change', function () {
        if (editorState.autoTailTimer !== null) {
          clearInterval(editorState.autoTailTimer);
          editorState.autoTailTimer = null;
        }
        if (autoTailCb.checked && editorState.open && editorState.path) {
          editorState.autoTailTimer = setInterval(function () {
            if (editorState.open && editorState.path) loadEditorContent(editorState.path, true, true);
          }, 3000);
        }
      });
    }

    var findBtn = qs('#file-editor-find-btn');
    var findBar = qs('#file-editor-find-bar');
    var findInput = qs('#file-editor-find-input');
    var findPrev = qs('#file-editor-find-prev');
    var findNext = qs('#file-editor-find-next');
    var findClose = qs('#file-editor-find-close');
    if (findBtn && findBar) {
      findBtn.addEventListener('click', function () {
        findBar.hidden = !findBar.hidden;
        if (!findBar.hidden && findInput) { findInput.focus(); findInput.select(); }
      });
    }
    if (findClose && findBar) findClose.addEventListener('click', function () { findBar.hidden = true; });
    if (findNext) findNext.addEventListener('click', function () { findInEditor(true); });
    if (findPrev) findPrev.addEventListener('click', function () { findInEditor(false); });
    if (findInput) {
      findInput.addEventListener('input', function () { findInEditor(true); });
      findInput.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') { e.preventDefault(); findInEditor(!e.shiftKey); }
      });
    }

    registerCleanup(forceCloseEditor);
  }

  /* ── Chmod (Permissions) Modal ──────────────────────────── */
  var chmodState = { open: false, paths: [], hasDir: false };

  function modeToSymbolic(val) {
    var chars = ['-'];
    var triads = [
      { r: 0o400, w: 0o200, x: 0o100, s: 0o4000, sc: 's', Sc: 'S' },
      { r: 0o040, w: 0o020, x: 0o010, s: 0o2000, sc: 's', Sc: 'S' },
      { r: 0o004, w: 0o002, x: 0o001, s: 0o1000, sc: 't', Sc: 'T' }
    ];
    triads.forEach(function (t) {
      chars.push((val & t.r) ? 'r' : '-');
      chars.push((val & t.w) ? 'w' : '-');
      if (val & t.s) chars.push((val & t.x) ? t.sc : t.Sc);
      else chars.push((val & t.x) ? 'x' : '-');
    });
    return chars.join('');
  }

  function syncChmodFromValue(val) {
    var modal = qs('#file-chmod-modal');
    if (!modal) return;
    modal.querySelectorAll('[data-chmod-bit]').forEach(function (cb) {
      var bit = parseInt(cb.getAttribute('data-chmod-bit'), 8);
      cb.checked = Boolean(val & bit);
    });
    var octInput = qs('#file-chmod-octal');
    var symEl = qs('#file-chmod-symbolic');
    if (octInput) octInput.value = val.toString(8).padStart(4, '0');
    if (symEl) symEl.textContent = modeToSymbolic(val);
  }

  function readChmodCheckboxes() {
    var modal = qs('#file-chmod-modal');
    var val = 0;
    if (!modal) return val;
    modal.querySelectorAll('[data-chmod-bit]').forEach(function (cb) {
      if (cb.checked) val |= parseInt(cb.getAttribute('data-chmod-bit'), 8);
    });
    return val;
  }

  function closeChmodModal() {
    chmodState.open = false;
    var modal = qs('#file-chmod-modal');
    if (modal) {
      modal.classList.remove('is-open');
      modal.hidden = true;
    }
    document.body.classList.remove('modal-open');
  }

  function openChmodModal(paths, initialOctal, hasDir) {
    var modal = qs('#file-chmod-modal');
    if (!modal || !paths || !paths.length) return;
    closeMenu();
    chmodState.open = true;
    chmodState.paths = paths.slice();
    chmodState.hasDir = !!hasDir;

    var targetEl = qs('#file-chmod-target');
    if (targetEl) {
      targetEl.textContent = paths.length === 1 ? paths[0] : T('js.files.items_count', { count: paths.length });
    }
    var recWrap = qs('#file-chmod-recursive-wrap');
    var recCb = qs('#file-chmod-recursive');
    if (recWrap) recWrap.hidden = !hasDir;
    if (recCb) recCb.checked = false;

    var errEl = qs('#file-chmod-error');
    if (errEl) { errEl.textContent = ''; errEl.hidden = true; }

    var val = parseInt(initialOctal || '0644', 8);
    if (isNaN(val)) val = 0o644;
    syncChmodFromValue(val);

    modal.hidden = false;
    document.body.classList.add('modal-open');
    renderIcons(modal);
    requestAnimationFrame(function () { modal.classList.add('is-open'); });
  }

  function initChmodModal() {
    var modal = qs('#file-chmod-modal');
    if (!modal) return;

    modal.querySelectorAll('[data-chmod-close]').forEach(function (el) {
      el.addEventListener('click', closeChmodModal);
    });

    modal.querySelectorAll('[data-chmod-bit]').forEach(function (cb) {
      cb.addEventListener('change', function () {
        var val = readChmodCheckboxes();
        var octInput = qs('#file-chmod-octal');
        var symEl = qs('#file-chmod-symbolic');
        if (octInput) octInput.value = val.toString(8).padStart(4, '0');
        if (symEl) symEl.textContent = modeToSymbolic(val);
      });
    });

    var octInput = qs('#file-chmod-octal');
    if (octInput) {
      octInput.addEventListener('input', function () {
        var errEl = qs('#file-chmod-error');
        if (errEl) errEl.hidden = true;
        var raw = octInput.value.trim();
        if (/^[0-7]{3,4}$/.test(raw)) {
          var v = parseInt(raw, 8);
          modal.querySelectorAll('[data-chmod-bit]').forEach(function (cb) {
            cb.checked = Boolean(v & parseInt(cb.getAttribute('data-chmod-bit'), 8));
          });
          var symEl = qs('#file-chmod-symbolic');
          if (symEl) symEl.textContent = modeToSymbolic(v);
        }
      });
    }

    modal.querySelectorAll('[data-chmod-preset]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var v = parseInt(btn.getAttribute('data-chmod-preset'), 8);
        syncChmodFromValue(v);
      });
    });

    var applyBtn = qs('#file-chmod-apply');
    if (applyBtn) {
      applyBtn.addEventListener('click', function () {
        var raw = octInput ? octInput.value.trim() : '';
        var errEl = qs('#file-chmod-error');
        if (!/^[0-7]{3,4}$/.test(raw)) {
          if (errEl) { errEl.textContent = T('js.files.chmod_err_invalid'); errEl.hidden = false; }
          return;
        }
        var recCb = qs('#file-chmod-recursive');
        var recursive = Boolean(recCb && recCb.checked && chmodState.hasDir);
        applyBtn.disabled = true;
        postOp('chmod', {
          paths: chmodState.paths,
          mode: raw,
          recursive: recursive
        }).then(function (data) {
          applyBtn.disabled = false;
          closeChmodModal();
          if (data.failed && data.failed.length) {
            toast(T('js.files.partial_failures', {
              ok: (data.succeeded || []).length,
              fail: data.failed.length,
              error: data.failed[0].error || ''
            }), 'error');
          } else {
            toast(T('js.files.chmod_done', { mode: raw.padStart(4, '0') }), 'success');
          }
          reloadCurrent();
        }).catch(function (err) {
          applyBtn.disabled = false;
          toast(err.message, 'error');
        });
      });
    }
    registerCleanup(closeChmodModal);
  }

  /* ── Properties Modal ───────────────────────────────────── */
  var propsState = { open: false, path: '' };

  function closePropsModal() {
    propsState.open = false;
    var modal = qs('#file-props-modal');
    if (modal) {
      modal.classList.remove('is-open');
      modal.hidden = true;
    }
    document.body.classList.remove('modal-open');
  }

  function renderPropsRows(stat) {
    var list = qs('#file-props-list');
    var calcBtn = qs('#file-props-calc-btn');
    if (!list) return;
    list.innerHTML = '';

    function addRow(label, value) {
      var row = document.createElement('div');
      row.className = 'file-props__row';
      var dt = document.createElement('dt');
      dt.textContent = label;
      var dd = document.createElement('dd');
      dd.setAttribute('dir', 'ltr');
      dd.textContent = value;
      row.appendChild(dt);
      row.appendChild(dd);
      list.appendChild(row);
    }

    var typeStr = T('js.files.props_type_file');
    if (stat.is_dir) typeStr = T('js.files.props_type_dir');
    else if (stat.is_symlink) typeStr = T('js.files.props_type_symlink') + (stat.broken_link ? ' (' + T('js.files.props_broken') + ')' : '');

    addRow(T('common.name'), stat.name || '');
    addRow(T('js.files.props_path'), stat.path || '');
    addRow(T('js.files.props_type'), typeStr);
    if (stat.is_symlink && stat.link_target) {
      addRow(T('js.files.props_target'), stat.link_target);
    }
    if (!stat.is_dir) {
      addRow(T('js.files.props_size'), (stat.size || '0 B') + ' (' + (stat.size_bytes || 0) + ' B)');
    } else if (stat.size_calculated) {
      var val = T('js.files.props_contents_val', {
        files: stat.dir_files || 0,
        dirs: stat.dir_dirs || 0,
        size: stat.dir_total_size || '0 B'
      });
      if (stat.size_truncated) val += ' ' + T('js.files.props_truncated');
      addRow(T('js.files.props_contents'), val);
    }
    addRow(T('js.files.props_perms'), (stat.mode || '') + ' (' + (stat.mode_octal || '') + ')');
    addRow(T('js.files.props_owner'), (stat.uid || 0) + ':' + (stat.gid || 0));
    addRow(T('js.files.props_modified'), stat.modified_at || '-');
    if (stat.access_time && stat.access_time !== '-') {
      addRow(T('js.files.props_accessed'), stat.access_time);
    }

    if (calcBtn) {
      calcBtn.hidden = !stat.is_dir || !!stat.size_calculated;
    }
  }

  function openPropsModal(targetPath) {
    if (!ensureCredsOrPrompt()) return;
    closeMenu();
    var modal = qs('#file-props-modal');
    var list = qs('#file-props-list');
    var calcBtn = qs('#file-props-calc-btn');
    if (!modal || !list) return;

    propsState.open = true;
    propsState.path = targetPath;
    list.innerHTML = '';
    if (calcBtn) calcBtn.hidden = true;

    modal.hidden = false;
    document.body.classList.add('modal-open');
    renderIcons(modal);
    requestAnimationFrame(function () { modal.classList.add('is-open'); });

    postOp('stat', { path: targetPath, calc_size: false }).then(function (data) {
      if (!propsState.open || propsState.path !== targetPath) return;
      renderPropsRows(data.stat || {});
    }).catch(function (err) {
      closePropsModal();
      toast(err.message, 'error');
    });
  }

  function initPropsModal() {
    var modal = qs('#file-props-modal');
    if (!modal) return;
    modal.querySelectorAll('[data-props-close]').forEach(function (el) {
      el.addEventListener('click', closePropsModal);
    });
    var calcBtn = qs('#file-props-calc-btn');
    if (calcBtn) {
      calcBtn.addEventListener('click', function () {
        if (!propsState.path) return;
        calcBtn.disabled = true;
        postOp('stat', { path: propsState.path, calc_size: true }).then(function (data) {
          calcBtn.disabled = false;
          if (propsState.open) renderPropsRows(data.stat || {});
        }).catch(function (err) {
          calcBtn.disabled = false;
          toast(err.message, 'error');
        });
      });
    }
    registerCleanup(closePropsModal);
  }

  /* ── Row actions menu (kebab) ───────────────────────────── */
  var menu = null;
  var menuOpenFor = null;

  function ensureMenu() {
    if (menu) return menu;
    menu = document.createElement('div');
    menu.className = 'file-menu';
    menu.setAttribute('role', 'menu');
    menu.hidden = true;
    document.body.appendChild(menu);
    return menu;
  }

  function closeMenu() {
    if (!menu || menu.hidden) return;
    menu.hidden = true;
    if (menuOpenFor) {
      menuOpenFor.setAttribute('aria-expanded', 'false');
      menuOpenFor = null;
    }
  }

  function menuItem(label, icon, danger, onClick) {
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'file-menu__item' + (danger ? ' file-menu__item--danger' : '');
    btn.setAttribute('role', 'menuitem');
    var iconEl = document.createElement('i');
    iconEl.setAttribute('data-lucide', icon);
    var spanEl = document.createElement('span');
    spanEl.textContent = label;
    btn.appendChild(iconEl);
    btn.appendChild(spanEl);
    btn.addEventListener('click', function () {
      var opener = menuOpenFor;
      closeMenu();
      onClick(opener);
    });
    return btn;
  }

  function menuDivider() {
    var div = document.createElement('div');
    div.className = 'file-menu__divider';
    div.setAttribute('role', 'separator');
    return div;
  }

  function copyPath(p) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(p).then(function () {
        toast(T('js.files.path_copied'), 'info');
      }).catch(function () { toast(T('js.files.path_copy_failed'), 'error'); });
    } else {
      toast(T('js.files.clipboard_unavailable'), 'error');
    }
  }

  function openMenu(trigger) {
    var row = trigger.closest('.file-row');
    if (!row) return;
    var entry = entryFromRow(row);
    if (!entry.path) return;
    var isDirLike = entry.kind === 'directory' || entry.link_is_dir;

    var m = ensureMenu();
    m.innerHTML = '';

    if (isDirLike) {
      m.appendChild(menuItem(T('common.open'), 'folder-open', false, function () {
        navigateTo(entry.path);
      }));
      m.appendChild(menuItem(T('js.files.menu_download_zip'), 'file-archive', false, function () {
        triggerArchiveDownload([entry.path], 'zip', entry.name);
      }));
    } else {
      m.appendChild(menuItem(T('js.files.menu_preview_edit'), 'file-code', false, function () {
        openEditorModal(entry.path, { name: entry.name });
      }));
      m.appendChild(menuItem(T('js.files.menu_tail'), 'scroll-text', false, function () {
        openEditorModal(entry.path, { name: entry.name, tail: true });
      }));
      m.appendChild(menuItem(T('js.files.menu_download'), 'download', false, function () {
        triggerDownload(entry.path);
      }));
      if (isArchiveName(entry.name)) {
        m.appendChild(menuItem(T('js.files.menu_extract'), 'package-open', false, function () {
          extractArchive(entry.path);
        }));
      }
    }

    m.appendChild(menuDivider());
    m.appendChild(menuItem(T('js.files.menu_copy'), 'copy', false, function () {
      setClipboard('copy', [entry.path]);
    }));
    m.appendChild(menuItem(T('js.files.menu_cut'), 'scissors', false, function () {
      setClipboard('cut', [entry.path]);
    }));
    m.appendChild(menuItem(T('js.files.menu_duplicate'), 'copy-plus', false, function () {
      duplicatePaths([entry.path]);
    }));
    m.appendChild(menuItem(T('js.files.menu_rename'), 'pencil', false, function (opener) {
      promptRename(entry.name, entry.path, opener);
    }));
    m.appendChild(menuItem(T('js.files.menu_permissions'), 'shield', false, function () {
      openChmodModal([entry.path], entry.mode_octal || '0644', isDirLike);
    }));
    m.appendChild(menuItem(T('js.files.menu_compress'), 'archive', false, function (opener) {
      promptCompress([entry.name], opener);
    }));

    m.appendChild(menuDivider());
    m.appendChild(menuItem(T('js.files.menu_properties'), 'info', false, function () {
      openPropsModal(entry.path);
    }));
    m.appendChild(menuItem(T('js.files.menu_copy_path'), 'clipboard', false, function () {
      copyPath(entry.path);
    }));
    m.appendChild(menuItem(T('common.delete'), 'trash-2', true, function (opener) {
      confirmDelete(entry.name, entry.path, entry.kind === 'directory', opener);
    }));

    m.hidden = false;
    renderIcons(m);
    positionMenu(m, trigger);
    trigger.setAttribute('aria-expanded', 'true');
    menuOpenFor = trigger;
  }

  function positionMenu(m, trigger) {
    var r = trigger.getBoundingClientRect();
    var isRTL = document.documentElement.dir === 'rtl' || document.body.dir === 'rtl';
    m.style.left = '0px';
    m.style.top = '0px';
    var mw = m.offsetWidth;
    var mh = m.offsetHeight;
    var gap = 6;
    var left;
    if (isRTL) {
      left = Math.max(8, Math.min(r.left, window.innerWidth - mw - 8));
    } else {
      left = Math.min(r.right - mw, window.innerWidth - mw - 8);
      if (left < 8) left = 8;
    }
    var top = r.bottom + gap;
    if (top + mh > window.innerHeight - 8) {
      top = r.top - mh - gap;
      if (top < 8) top = Math.max(8, window.innerHeight - mh - 8);
    }
    m.style.left = Math.round(left) + 'px';
    m.style.top = Math.round(top) + 'px';
  }

  /* ── Action implementations ─────────────────────────────── */
  function submitDownloadForm(fields) {
    var c = context();
    if (!c) return;
    refreshCSRFToken().then(function (fresh) {
      var f = document.createElement('form');
      f.method = 'post';
      f.action = c.action;
      f.style.display = 'none';
      var add = function (name, value) {
        var input = document.createElement('input');
        input.type = 'hidden';
        input.name = name;
        input.value = value;
        f.appendChild(input);
      };
      add('_csrf_token', fresh || currentToken(c.form));
      Object.keys(fields).forEach(function (k) { add(k, fields[k]); });
      var creds = collectCredsObject();
      Object.keys(creds).forEach(function (k) { add(k, creds[k]); });
      document.body.appendChild(f);
      f.submit();
      setTimeout(function () { f.remove(); }, 0);
    });
  }

  function triggerDownload(filePath) {
    if (!ensureCredsOrPrompt()) return;
    submitDownloadForm({ intent: 'download', path: filePath });
  }

  function triggerArchiveDownload(paths, format, name) {
    if (!ensureCredsOrPrompt()) return;
    if (!paths || !paths.length) return;
    submitDownloadForm({
      intent: 'archive_download',
      format: format || 'zip',
      name: name || (paths.length === 1 ? paths[0].split('/').pop() : 'files'),
      paths: JSON.stringify(paths)
    });
  }

  function validateEntryName(val) {
    var trimmed = val.trim();
    if (!trimmed) return T('js.files.err_name_required');
    if (trimmed === '.' || trimmed === '..' ||
        trimmed.indexOf('/') !== -1 || trimmed.indexOf('\\') !== -1 || trimmed.indexOf('\0') !== -1) {
      return T('js.files.err_name_invalid');
    }
    return '';
  }

  function validateMkdirPath(val) {
    var trimmed = val.trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, '');
    if (!trimmed) return T('js.files.err_name_required');
    var segs = trimmed.split('/');
    for (var i = 0; i < segs.length; i++) {
      var seg = segs[i].trim();
      if (!seg || seg === '.' || seg === '..' || seg.indexOf('\0') !== -1) {
        return T('js.files.err_name_invalid');
      }
    }
    return '';
  }

  function promptRename(name, oldPath, opener) {
    openDialog({
      mode: 'input',
      title: T('js.files.rename_title'),
      desc: T('js.files.rename_prompt', { name: name }),
      initialValue: name,
      placeholder: T('js.files.rename_placeholder'),
      confirmLabel: T('js.files.rename_submit'),
      danger: false,
      returnFocusTo: opener,
      validate: validateEntryName,
      onConfirm: function (rawValue) {
        var next = rawValue.trim();
        if (!next || next === name) return;
        postOp('rename', { path: oldPath, name: next })
          .then(function () { toast(T('js.files.renamed', { name: next }), 'success'); reloadCurrent(); })
          .catch(function (err) { toast(err.message, 'error'); });
      },
    });
  }

  function confirmDelete(name, target, isDir, opener) {
    var msg = isDir
      ? T('js.files.confirm_delete_dir', { name: name })
      : T('js.files.confirm_delete_file', { name: name });
    openDialog({
      mode: 'confirm',
      title: T('js.files.delete_title'),
      desc: msg,
      confirmLabel: T('js.files.delete_submit'),
      danger: true,
      returnFocusTo: opener,
      onConfirm: function () {
        postOp('delete', { path: target, recursive: !!isDir })
          .then(function () { toast(T('js.files.deleted', { name: name }), 'success'); reloadCurrent(); })
          .catch(function (err) { toast(err.message, 'error'); });
      },
    });
  }

  function confirmBatchDelete(paths, opener) {
    if (!paths || !paths.length) return;
    if (paths.length === 1) {
      var e = getEntryByPath(paths[0]);
      if (e) { confirmDelete(e.name, e.path, e.kind === 'directory', opener); return; }
    }
    var names = paths.map(function (p) { return p.split('/').pop() || p; });
    openDialog({
      mode: 'confirm',
      title: T('js.files.delete_title'),
      desc: T('js.files.confirm_delete_multi', { count: paths.length }),
      items: names,
      confirmLabel: T('js.files.delete_submit'),
      danger: true,
      returnFocusTo: opener,
      onConfirm: function () {
        postOp('delete', { paths: paths, recursive: true })
          .then(function (data) {
            if (data.failed && data.failed.length) {
              toast(T('js.files.partial_failures', {
                ok: (data.succeeded || []).length,
                fail: data.failed.length,
                error: data.failed[0].error || ''
              }), 'error');
            } else {
              toast(T('js.files.deleted_multi', { count: (data.succeeded || paths).length }), 'success');
            }
            reloadCurrent();
          })
          .catch(function (err) { toast(err.message, 'error'); });
      }
    });
  }

  function promptMkdir(e) {
    if (!ensureCredsOrPrompt()) return;
    var opener = (e && e.currentTarget) || qs('#file-mkdir-btn');
    openDialog({
      mode: 'input',
      title: T('js.files.mkdir_title'),
      desc: T('js.files.mkdir_prompt'),
      hint: T('js.files.mkdir_hint'),
      initialValue: '',
      placeholder: T('js.files.mkdir_placeholder'),
      confirmLabel: T('js.files.mkdir_submit'),
      danger: false,
      returnFocusTo: opener,
      validate: validateMkdirPath,
      onConfirm: function (rawValue) {
        var name = rawValue.trim();
        var c = context();
        if (!name || !c) return;
        postOp('mkdir', { path: c.currentDir, name: name, recursive: name.indexOf('/') !== -1 })
          .then(function () { toast(T('js.files.created', { name: name }), 'success'); reloadCurrent(); })
          .catch(function (err) { toast(err.message, 'error'); });
      },
    });
  }

  function promptNewFile(e) {
    if (!ensureCredsOrPrompt()) return;
    var opener = (e && e.currentTarget) || qs('#file-newfile-btn');
    openDialog({
      mode: 'input',
      title: T('js.files.newfile_title'),
      desc: T('js.files.newfile_prompt'),
      initialValue: '',
      placeholder: T('js.files.newfile_placeholder'),
      confirmLabel: T('js.files.newfile_submit'),
      danger: false,
      returnFocusTo: opener,
      validate: validateEntryName,
      onConfirm: function (rawValue) {
        var name = rawValue.trim();
        var c = context();
        if (!name || !c) return;
        var base = c.currentDir === '/' ? '' : c.currentDir.replace(/\/+$/, '');
        var newPath = base + '/' + name;
        postOp('write', { path: newPath, content: '', create_only: true })
          .then(function () {
            toast(T('js.files.file_created', { name: name }), 'success');
            reloadCurrent();
            openEditorModal(newPath, { name: name });
          })
          .catch(function (err) { toast(err.message, 'error'); });
      }
    });
  }

  function promptSymlink(e) {
    if (!ensureCredsOrPrompt()) return;
    var c = context();
    if (!c) return;
    var opener = (e && e.currentTarget) || qs('#file-symlink-btn');
    openDialog({
      mode: 'input',
      title: T('js.files.symlink_title'),
      desc: T('js.files.symlink_prompt', { dir: c.currentDir }),
      initialValue: '',
      placeholder: T('js.files.symlink_name_placeholder'),
      secondInput: true,
      secondValue: '',
      secondPlaceholder: T('js.files.symlink_target_placeholder'),
      confirmLabel: T('js.files.symlink_submit'),
      returnFocusTo: opener,
      validate: function (nameVal, targetVal) {
        var ne = validateEntryName(nameVal);
        if (ne) return ne;
        if (!String(targetVal || '').trim()) return T('js.files.err_target_required');
        return '';
      },
      onConfirm: function (nameVal, targetVal) {
        var linkName = nameVal.trim();
        var target = targetVal.trim();
        postOp('symlink', { path: c.currentDir, name: linkName, target: target })
          .then(function () { toast(T('js.files.symlink_created', { name: linkName }), 'success'); reloadCurrent(); })
          .catch(function (err) { toast(err.message, 'error'); });
      }
    });
  }

  function promptCompress(names, opener) {
    if (!ensureCredsOrPrompt()) return;
    var c = context();
    if (!c || !names || !names.length) return;
    var defaultName = names.length === 1 ? names[0] : 'archive';
    openDialog({
      mode: 'input',
      title: T('js.files.compress_title'),
      desc: T('js.files.compress_prompt', { count: names.length }),
      initialValue: defaultName,
      placeholder: T('js.files.compress_placeholder'),
      selectOptions: [
        { value: 'tar.gz', label: '.tar.gz (gzip tarball)', selected: true },
        { value: 'zip', label: '.zip (ZIP archive)', selected: false }
      ],
      confirmLabel: T('js.files.compress_submit'),
      returnFocusTo: opener,
      validate: validateEntryName,
      onConfirm: function (rawName, _v2, format) {
        postOp('compress', {
          path: c.currentDir,
          names: names,
          name: rawName.trim(),
          format: format || 'tar.gz'
        }).then(function (data) {
          toast(T('js.files.compressed', { name: data.name || rawName.trim() }), 'success');
          reloadCurrent();
        }).catch(function (err) { toast(err.message, 'error'); });
      }
    });
  }

  function extractArchive(archivePath) {
    if (!ensureCredsOrPrompt()) return;
    var c = context();
    if (!c) return;
    setRefreshSpinning(true);
    postOp('extract', { path: archivePath, dest: c.currentDir })
      .then(function (data) {
        setRefreshSpinning(false);
        toast(T('js.files.extracted', {
          files: data.extracted_files || 0,
          dirs: data.extracted_dirs || 0
        }), 'success');
        reloadCurrent();
      })
      .catch(function (err) {
        setRefreshSpinning(false);
        toast(err.message, 'error');
      });
  }

  function duplicatePaths(paths) {
    if (!ensureCredsOrPrompt()) return;
    var c = context();
    if (!c || !paths || !paths.length) return;
    postOp('copy', { paths: paths, dest: c.currentDir, duplicate: true })
      .then(function (data) {
        if (data.failed && data.failed.length) {
          toast(T('js.files.partial_failures', {
            ok: (data.succeeded || []).length,
            fail: data.failed.length,
            error: data.failed[0].error || ''
          }), 'error');
        } else {
          toast(T('js.files.pasted_ok', { count: (data.succeeded || paths).length }), 'success');
        }
        reloadCurrent();
      })
      .catch(function (err) { toast(err.message, 'error'); });
  }

  function pasteClipboard() {
    if (!clipboard || !clipboard.paths.length) return;
    if (!ensureCredsOrPrompt()) return;
    var c = context();
    if (!c) return;
    var isCut = clipboard.mode === 'cut';
    var intent = isCut ? 'move' : 'copy';
    var payload = {
      paths: clipboard.paths,
      dest: c.currentDir,
      duplicate: !isCut
    };
    postOp(intent, payload).then(function (data) {
      if (isCut) {
        clipboard = null;
        syncClipboardUI();
      }
      if (data.failed && data.failed.length) {
        toast(T('js.files.partial_failures', {
          ok: (data.succeeded || []).length,
          fail: data.failed.length,
          error: data.failed[0].error || ''
        }), 'error');
      } else {
        toast(T('js.files.pasted_ok', { count: (data.succeeded || []).length }), 'success');
      }
      reloadCurrent();
    }).catch(function (err) { toast(err.message, 'error'); });
  }

  /* ── Deep Search ────────────────────────────────────────── */
  function runDeepSearch() {
    var c = context();
    var queryInput = qs('#file-deep-query');
    var contentCb = qs('#file-deep-content');
    var metaEl = qs('#file-deep-meta');
    var resultsEl = qs('#file-deep-results');
    if (!c || !queryInput || !resultsEl) return;
    var query = queryInput.value.trim();
    if (!query) { queryInput.focus(); return; }
    if (!ensureCredsOrPrompt()) return;

    if (metaEl) {
      metaEl.hidden = false;
      metaEl.textContent = T('js.files.search_searching');
    }
    resultsEl.innerHTML = '';

    postOp('search', {
      path: c.currentDir,
      query: query,
      search_content: !!(contentCb && contentCb.checked),
      include_hidden: prefs.showHidden
    }).then(function (data) {
      var matches = Array.isArray(data.matches) ? data.matches : [];
      if (metaEl) {
        var msg = T('js.files.search_results_count', { count: matches.length, scanned: data.scanned || 0 });
        if (data.truncated) msg += ' ' + T('js.files.search_truncated');
        metaEl.textContent = msg;
      }
      if (!matches.length) {
        var none = document.createElement('p');
        none.className = 'file-no-match';
        none.textContent = T('js.files.search_none');
        resultsEl.appendChild(none);
        return;
      }
      matches.forEach(function (m) {
        var row = document.createElement('div');
        row.className = 'file-deep-match';

        var main = document.createElement('div');
        main.className = 'file-deep-match__main';
        var p = document.createElement('span');
        p.className = 'file-deep-match__path';
        p.textContent = m.path + (m.line ? ':' + m.line : '');
        main.appendChild(p);
        if (m.snippet) {
          var snip = document.createElement('span');
          snip.className = 'file-deep-match__snippet';
          snip.textContent = m.snippet;
          main.appendChild(snip);
        }
        row.appendChild(main);

        var acts = document.createElement('div');
        acts.className = 'file-deep-match__actions';
        var openFolderBtn = document.createElement('button');
        openFolderBtn.type = 'button';
        openFolderBtn.className = 'btn btn--ghost btn--xs';
        openFolderBtn.innerHTML = '<i data-lucide="folder-open" class="icon-in-button"></i>';
        openFolderBtn.title = m.dir;
        openFolderBtn.addEventListener('click', function () {
          navigateTo(m.is_dir ? m.path : m.dir);
        });
        acts.appendChild(openFolderBtn);

        if (!m.is_dir) {
          var viewBtn = document.createElement('button');
          viewBtn.type = 'button';
          viewBtn.className = 'btn btn--ghost btn--xs';
          viewBtn.innerHTML = '<i data-lucide="file-code" class="icon-in-button"></i>';
          viewBtn.title = T('js.files.menu_preview_edit');
          viewBtn.addEventListener('click', function () {
            openEditorModal(m.path, { name: m.name });
          });
          acts.appendChild(viewBtn);
        }
        row.appendChild(acts);
        resultsEl.appendChild(row);
      });
      renderIcons(resultsEl);
    }).catch(function (err) {
      if (metaEl) metaEl.textContent = err.message;
      toast(err.message, 'error');
    });
  }

  /* ── Filter + sort + view mode ──────────────────────────── */
  function entryRows() {
    var table = qs('#file-table');
    if (!table) return [];
    return Array.prototype.filter.call(
      table.querySelectorAll('.file-row'),
      function (row) { return !row.classList.contains('file-row--parent'); }
    );
  }

  function applyFilter() {
    var input = qs('#file-filter');
    var noMatch = qs('#file-no-match');
    var query = input ? input.value.trim().toLowerCase() : '';
    var rows = entryRows();
    var visible = 0;

    rows.forEach(function (row) {
      var name = (row.getAttribute('data-entry-name') || '').toLowerCase();
      var isHidden = row.getAttribute('data-entry-hidden') === '1';
      var matchesHidden = prefs.showHidden || !isHidden;
      var matchesQuery = query === '' || name.indexOf(query) !== -1;
      var show = matchesHidden && matchesQuery;
      row.style.display = show ? '' : 'none';
      if (show) visible += 1;
    });
    if (noMatch) {
      noMatch.hidden = !(rows.length > 0 && visible === 0);
    }
    syncSelectionUI();
  }

  function applySort() {
    var table = qs('#file-table');
    if (!table) return;
    var rows = entryRows();
    var dirMul = prefs.sortDir === 'desc' ? -1 : 1;

    rows.sort(function (a, b) {
      var aDir = a.getAttribute('data-entry-kind') === 'directory' || a.getAttribute('data-entry-link-dir') === '1';
      var bDir = b.getAttribute('data-entry-kind') === 'directory' || b.getAttribute('data-entry-link-dir') === '1';
      if (aDir !== bDir) return aDir ? -1 : 1;

      var cmp = 0;
      if (prefs.sortKey === 'size') {
        cmp = (parseInt(a.getAttribute('data-entry-size'), 10) || 0) -
              (parseInt(b.getAttribute('data-entry-size'), 10) || 0);
      } else if (prefs.sortKey === 'date') {
        cmp = (parseInt(a.getAttribute('data-entry-mtime'), 10) || 0) -
              (parseInt(b.getAttribute('data-entry-mtime'), 10) || 0);
      } else if (prefs.sortKey === 'type') {
        var ae = (a.getAttribute('data-entry-ext') || '').toLowerCase();
        var be = (b.getAttribute('data-entry-ext') || '').toLowerCase();
        cmp = ae.localeCompare(be);
        if (cmp === 0) {
          cmp = (a.getAttribute('data-entry-name') || '').localeCompare(
            b.getAttribute('data-entry-name') || '', undefined, { sensitivity: 'base', numeric: true });
        }
      } else {
        cmp = (a.getAttribute('data-entry-name') || '').localeCompare(
          b.getAttribute('data-entry-name') || '', undefined, { sensitivity: 'base', numeric: true });
      }
      return cmp * dirMul;
    });

    var noMatch = qs('#file-no-match');
    rows.forEach(function (row) { table.insertBefore(row, noMatch); });
    syncSortIndicators();
  }

  function syncSortIndicators() {
    var select = qs('#file-sort');
    if (select && select.value !== prefs.sortKey) select.value = prefs.sortKey;
    var dirBtn = qs('#file-sort-dir');
    if (dirBtn) {
      dirBtn.setAttribute('data-dir', prefs.sortDir);
      dirBtn.title = prefs.sortDir === 'desc' ? T('js.files.sort_desc') : T('js.files.sort_asc');
      dirBtn.innerHTML = '';
      var sortIcon = document.createElement('i');
      sortIcon.setAttribute('data-lucide', prefs.sortDir === 'desc' ? 'arrow-down-wide-narrow' : 'arrow-up-narrow-wide');
      dirBtn.appendChild(sortIcon);
      renderIcons(dirBtn);
    }
    qsa('.file-th[data-sort-key]').forEach(function (th) {
      var active = th.getAttribute('data-sort-key') === prefs.sortKey;
      th.classList.toggle('file-th--active', active);
      th.setAttribute('data-dir', active ? prefs.sortDir : '');
    });
  }

  function syncViewModeUI() {
    var table = qs('#file-table');
    var listBtn = qs('#file-view-list');
    var gridBtn = qs('#file-view-grid');
    var isGrid = prefs.viewMode === 'grid';
    if (table) table.classList.toggle('is-grid', isGrid);
    if (listBtn) {
      listBtn.classList.toggle('is-active', !isGrid);
      listBtn.setAttribute('aria-pressed', !isGrid ? 'true' : 'false');
    }
    if (gridBtn) {
      gridBtn.classList.toggle('is-active', isGrid);
      gridBtn.setAttribute('aria-pressed', isGrid ? 'true' : 'false');
    }
  }

  function syncHiddenToggleUI() {
    var btn = qs('#file-hidden-btn');
    if (!btn) return;
    btn.setAttribute('aria-pressed', prefs.showHidden ? 'true' : 'false');
    btn.classList.toggle('is-active', prefs.showHidden);
    btn.innerHTML = '';
    var icon = document.createElement('i');
    icon.setAttribute('data-lucide', prefs.showHidden ? 'eye' : 'eye-off');
    btn.appendChild(icon);
    renderIcons(btn);
  }

  /* ── Wiring ─────────────────────────────────────────────── */
  function initBrowser() {
    var form = qs('#file-form');
    var pathInput = qs('#file-path');
    var crumbContainer = qs('#file-breadcrumb');
    var table = qs('#file-table');
    if (!form || !pathInput) return;

    var c = context();
    if (c && c.currentDir) {
      pushHistory(c.currentDir);
    }

    // Apply initial VFS width and localized status bar labels from SSR data attributes
    var statsEl = qs('#file-dir-stats');
    var workspace = qs('#file-workspace');
    if (statsEl && workspace && !workspace.hidden) {
      var dirs = parseInt(statsEl.getAttribute('data-dirs') || '0', 10) || 0;
      var files = parseInt(statsEl.getAttribute('data-files') || '0', 10) || 0;
      var hiddenCnt = parseInt(statsEl.getAttribute('data-hidden') || '0', 10) || 0;
      var totalSize = statsEl.getAttribute('data-total-size') || '0 B';
      statsEl.innerHTML = '';
      var pill = document.createElement('span');
      pill.className = 'file-stat-pill';
      pill.textContent = T('js.files.summary_counts', { dirs: dirs, files: files, size: totalSize });
      statsEl.appendChild(pill);
      if (hiddenCnt > 0) {
        var hPill = document.createElement('span');
        hPill.className = 'file-stat-pill';
        hPill.textContent = T('js.files.summary_hidden', { count: hiddenCnt });
        statsEl.appendChild(hPill);
      }
    }
    var vfsEl = qs('#file-vfs');
    var vfsLabel = qs('#file-vfs-label');
    var vfsFill = qs('#file-vfs-fill');
    if (vfsEl && !vfsEl.hidden && vfsLabel) {
      vfsLabel.textContent = T('js.files.disk_usage', {
        used: vfsEl.getAttribute('data-used') || '',
        total: vfsEl.getAttribute('data-total') || '',
        pct: vfsEl.getAttribute('data-pct') || '0',
        free: vfsEl.getAttribute('data-free') || ''
      });
    }
    if (vfsFill) {
      var pct = parseInt(vfsFill.getAttribute('data-pct') || '0', 10);
      if (!isNaN(pct) && pct > 0) vfsFill.style.width = Math.min(100, pct) + '%';
    }

    if (crumbContainer) {
      buildBreadcrumb(pathInput.value, crumbContainer);
      crumbContainer.addEventListener('click', function (e) {
        var seg = e.target.closest('.breadcrumb__seg[data-crumb-path]');
        if (seg) {
          navigateTo(seg.getAttribute('data-crumb-path'));
          return;
        }
        if (window.matchMedia && window.matchMedia('(max-width: 700px)').matches) {
          setPathEditing(true);
        }
      });
      pathInput.addEventListener('input', function () {
        buildBreadcrumb(pathInput.value, crumbContainer);
      });
    }

    var pathEditBtn = qs('#file-path-edit-btn');
    if (pathEditBtn) {
      pathEditBtn.addEventListener('click', function () {
        setPathEditing(true);
      });
    }

    var pathCancelBtn = qs('#file-path-cancel-btn');
    if (pathCancelBtn) {
      pathCancelBtn.addEventListener('click', function () {
        var cur = context();
        if (cur && cur.currentDir) pathInput.value = cur.currentDir;
        buildBreadcrumb(pathInput.value, crumbContainer);
        setPathEditing(false);
      });
    }

    pathInput.addEventListener('keydown', function (e) {
      var nav = qs('#file-nav');
      if (e.key === 'Escape' && nav && nav.classList.contains('is-editing-path')) {
        e.preventDefault();
        e.stopPropagation();
        var cur = context();
        if (cur && cur.currentDir) pathInput.value = cur.currentDir;
        buildBreadcrumb(pathInput.value, crumbContainer);
        setPathEditing(false);
      }
    });

    form.addEventListener('submit', function (e) {
      e.preventDefault();
      navigateTo(pathInput.value.trim() || '~');
    });

    var backBtn = qs('#file-back-btn');
    if (backBtn) backBtn.addEventListener('click', function () {
      if (historyIndex > 0) {
        historyIndex -= 1;
        navigateTo(historyStack[historyIndex], { pushHistory: false });
      }
    });

    var fwdBtn = qs('#file-forward-btn');
    if (fwdBtn) fwdBtn.addEventListener('click', function () {
      if (historyIndex < historyStack.length - 1) {
        historyIndex += 1;
        navigateTo(historyStack[historyIndex], { pushHistory: false });
      }
    });

    var upBtn = qs('#file-up-btn');
    if (upBtn) upBtn.addEventListener('click', function () {
      var cur = context();
      if (cur && cur.parentDir) navigateTo(cur.parentDir);
    });

    var homeBtn = qs('#file-home-btn');
    if (homeBtn) homeBtn.addEventListener('click', function () {
      var cur = context();
      navigateTo((cur && cur.homeDir) || '~');
    });

    var copyCwdBtn = qs('#file-copy-cwd-btn');
    if (copyCwdBtn) copyCwdBtn.addEventListener('click', function () {
      var cur = context();
      if (cur && cur.currentDir) copyPath(cur.currentDir);
    });

    var bookmarksBtn = qs('#file-bookmarks-btn');
    if (bookmarksBtn) bookmarksBtn.addEventListener('click', function () {
      prefs.bookmarksOpen = !prefs.bookmarksOpen;
      savePrefs();
      renderBookmarks();
    });

    var pinBtn = qs('#file-pin-bookmark-btn');
    if (pinBtn) pinBtn.addEventListener('click', function () {
      var cur = context();
      if (!cur || !cur.currentDir) return;
      var list = loadCustomBookmarks();
      if (list.indexOf(cur.currentDir) === -1) {
        list.push(cur.currentDir);
        saveCustomBookmarks(list);
        renderBookmarks();
        toast(T('js.files.bookmark_added', { path: cur.currentDir }), 'success');
      }
    });

    // Event delegation on #file-table for clicks, checkboxes, and range selection
    if (table) {
      table.addEventListener('click', function (e) {
        var trigger = e.target.closest('[data-menu-trigger]');
        if (trigger) {
          e.stopPropagation();
          if (menuOpenFor === trigger) { closeMenu(); return; }
          closeMenu();
          openMenu(trigger);
          return;
        }

        var checkCell = e.target.closest('.file-row__check-cell');
        var row = e.target.closest('.file-row');
        if (!row) return;

        if (checkCell && !row.classList.contains('file-row--parent')) {
          var rowPath = row.getAttribute('data-entry-path');
          if (!rowPath) return;
          var cb = row.querySelector('[data-row-check]');
          var desired = e.target === cb ? cb.checked : !selectedPaths.has(rowPath);

          if (e.shiftKey && lastCheckedPath) {
            var visibleRows = entryRows().filter(function (r) { return r.style.display !== 'none'; });
            var i1 = visibleRows.findIndex(function (r) { return r.getAttribute('data-entry-path') === lastCheckedPath; });
            var i2 = visibleRows.findIndex(function (r) { return r.getAttribute('data-entry-path') === rowPath; });
            if (i1 !== -1 && i2 !== -1) {
              var lo = Math.min(i1, i2);
              var hi = Math.max(i1, i2);
              for (var k = lo; k <= hi; k++) {
                var p = visibleRows[k].getAttribute('data-entry-path');
                if (desired) selectedPaths.add(p);
                else selectedPaths.delete(p);
              }
            }
          } else {
            if (desired) selectedPaths.add(rowPath);
            else selectedPaths.delete(rowPath);
          }
          lastCheckedPath = rowPath;
          syncSelectionUI();
          return;
        }

        // Ctrl/Cmd+Click toggles selection on desktop
        if ((e.ctrlKey || e.metaKey) && !row.classList.contains('file-row--parent')) {
          var cp = row.getAttribute('data-entry-path');
          if (cp) {
            if (selectedPaths.has(cp)) selectedPaths.delete(cp);
            else selectedPaths.add(cp);
            lastCheckedPath = cp;
            syncSelectionUI();
          }
          return;
        }

        var navPath = row.getAttribute('data-nav-path');
        if (navPath) {
          navigateTo(navPath);
          return;
        }

        var filePath = row.getAttribute('data-file-path');
        if (filePath) {
          var ext = row.getAttribute('data-entry-ext') || '';
          var name = row.getAttribute('data-entry-name') || '';
          var sizeBytes = parseInt(row.getAttribute('data-entry-size'), 10) || 0;
          if (isNonPreviewBinaryExt(ext)) {
            triggerDownload(filePath);
          } else if (ext === '.log' && sizeBytes > 256 * 1024) {
            openEditorModal(filePath, { name: name, tail: true });
          } else {
            openEditorModal(filePath, { name: name });
          }
        }
      });
    }

    renderBookmarks();
    syncNavButtons();
  }

  function initMenusAndGlobalKeys() {
    function onDocClick(e) {
      if (!menu || menu.hidden) return;
      if (e.target.closest('.file-menu') || e.target.closest('[data-menu-trigger]')) return;
      closeMenu();
    }

    function isTypingTarget(el) {
      return el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' ||
        el.tagName === 'SELECT' || el.isContentEditable);
    }

    function onDocKeyDown(e) {
      if (root !== document && root.hidden) return;

      if (dialogActive) {
        if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeDialog(true); return; }
        if (e.key === 'Tab') {
          var nodes = getDialogNodes();
          var list = focusablesInDialog(nodes);
          if (list.length > 0) {
            var idx = list.indexOf(document.activeElement);
            e.preventDefault();
            if (e.shiftKey) list[idx <= 0 ? list.length - 1 : idx - 1].focus();
            else list[idx < 0 || idx >= list.length - 1 ? 0 : idx + 1].focus();
          }
          return;
        }
        return;
      }

      if (editorState.open) {
        if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeEditorModal(); return; }
        if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
          e.preventDefault();
          saveEditorFile();
        }
        return;
      }

      if (chmodState.open) {
        if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeChmodModal(); }
        return;
      }

      if (propsState.open) {
        if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closePropsModal(); }
        return;
      }

      if (e.key === 'Escape') {
        closeMenu();
        if (selectedPaths.size > 0) clearSelection();
        return;
      }

      if (isTypingTarget(document.activeElement)) return;

      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'a') {
        var ws = qs('#file-workspace');
        if (ws && !ws.hidden) {
          e.preventDefault();
          entryRows().forEach(function (r) {
            if (r.style.display !== 'none') {
              var p = r.getAttribute('data-entry-path');
              if (p) selectedPaths.add(p);
            }
          });
          syncSelectionUI();
        }
        return;
      }

      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'l') {
        e.preventDefault();
        var pathEl = qs('#file-path');
        if (pathEl) { pathEl.focus(); pathEl.select(); }
        return;
      }

      if (e.key === '/' && !e.ctrlKey && !e.metaKey) {
        var filter = qs('#file-filter');
        if (filter) { e.preventDefault(); filter.focus(); }
        return;
      }

      if ((e.key === 'Backspace' || (e.altKey && e.key === 'ArrowUp'))) {
        var c = context();
        if (c && c.parentDir) { e.preventDefault(); navigateTo(c.parentDir); }
        return;
      }

      if (e.key === 'F2' && selectedPaths.size === 1) {
        e.preventDefault();
        var onlyPath = Array.from(selectedPaths)[0];
        var entry = getEntryByPath(onlyPath);
        if (entry) promptRename(entry.name, entry.path, null);
        return;
      }

      if (e.key === 'Delete' && selectedPaths.size > 0) {
        e.preventDefault();
        confirmBatchDelete(Array.from(selectedPaths), null);
      }
    }

    function onWinViewport() { closeMenu(); }

    document.addEventListener('click', onDocClick);
    document.addEventListener('keydown', onDocKeyDown);
    window.addEventListener('resize', onWinViewport);
    window.addEventListener('scroll', onWinViewport, true);

    registerCleanup(function () {
      document.removeEventListener('click', onDocClick);
      document.removeEventListener('keydown', onDocKeyDown);
      window.removeEventListener('resize', onWinViewport);
      window.removeEventListener('scroll', onWinViewport, true);
      closeMenu();
      if (menu && menu.parentNode) menu.parentNode.removeChild(menu);
      menu = null;
      menuOpenFor = null;
    });
  }

  function initDragAndDrop() {
    var card = qs('#file-browser-card');
    var dropzone = qs('#file-dropzone');
    if (!card || !dropzone) return;

    var dragDepth = 0;

    function hasFilePayload(e) {
      var types = e.dataTransfer && e.dataTransfer.types;
      if (!types) return false;
      return Array.prototype.indexOf.call(types, 'Files') !== -1;
    }

    card.addEventListener('dragenter', function (e) {
      if (!hasFilePayload(e)) return;
      e.preventDefault();
      dragDepth += 1;
      dropzone.hidden = false;
    });
    card.addEventListener('dragover', function (e) {
      if (!hasFilePayload(e)) return;
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
    });
    card.addEventListener('dragleave', function (e) {
      if (!hasFilePayload(e)) return;
      dragDepth = Math.max(0, dragDepth - 1);
      if (dragDepth === 0) dropzone.hidden = true;
    });
    card.addEventListener('drop', function (e) {
      if (!hasFilePayload(e)) return;
      e.preventDefault();
      dragDepth = 0;
      dropzone.hidden = true;
      collectDroppedItems(e.dataTransfer).then(function (items) {
        if (items.length) uploadItems(items);
      });
    });
  }

  function initToolbar() {
    var refreshBtn = qs('#file-refresh-btn');
    if (refreshBtn) refreshBtn.addEventListener('click', function () { reloadCurrent(); });

    var newfileBtn = qs('#file-newfile-btn');
    if (newfileBtn) newfileBtn.addEventListener('click', promptNewFile);

    var mkdirBtn = qs('#file-mkdir-btn');
    if (mkdirBtn) mkdirBtn.addEventListener('click', promptMkdir);

    var symlinkBtn = qs('#file-symlink-btn');
    if (symlinkBtn) symlinkBtn.addEventListener('click', promptSymlink);

    var uploadBtn = qs('#file-upload-btn');
    var uploadInput = qs('#file-upload-input');
    if (uploadBtn && uploadInput) {
      uploadBtn.addEventListener('click', function () { uploadInput.click(); });
      uploadInput.addEventListener('change', function () {
        if (uploadInput.files && uploadInput.files.length) uploadFileList(uploadInput.files, false);
        uploadInput.value = '';
      });
    }

    var uploadDirBtn = qs('#file-upload-dir-btn');
    var uploadDirInput = qs('#file-upload-dir-input');
    if (uploadDirBtn && uploadDirInput) {
      uploadDirBtn.addEventListener('click', function () { uploadDirInput.click(); });
      uploadDirInput.addEventListener('change', function () {
        if (uploadDirInput.files && uploadDirInput.files.length) uploadFileList(uploadDirInput.files, true);
        uploadDirInput.value = '';
      });
    }

    var uploadsClearBtn = qs('#file-uploads-clear-btn');
    if (uploadsClearBtn) uploadsClearBtn.addEventListener('click', function () {
      var panel = qs('#file-uploads');
      var listEl = qs('#file-uploads-list');
      if (listEl) listEl.innerHTML = '';
      if (panel) panel.hidden = true;
    });

    var filterInput = qs('#file-filter');
    if (filterInput) filterInput.addEventListener('input', applyFilter);

    var sortSelect = qs('#file-sort');
    if (sortSelect) {
      sortSelect.value = prefs.sortKey;
      sortSelect.addEventListener('change', function () {
        prefs.sortKey = sortSelect.value;
        savePrefs();
        applySort();
      });
    }

    var dirBtn = qs('#file-sort-dir');
    if (dirBtn) dirBtn.addEventListener('click', function () {
      prefs.sortDir = prefs.sortDir === 'asc' ? 'desc' : 'asc';
      savePrefs();
      applySort();
    });

    qsa('.file-th[data-sort-key]').forEach(function (th) {
      th.addEventListener('click', function () {
        var key = th.getAttribute('data-sort-key');
        if (prefs.sortKey === key) {
          prefs.sortDir = prefs.sortDir === 'asc' ? 'desc' : 'asc';
        } else {
          prefs.sortKey = key;
          prefs.sortDir = 'asc';
        }
        savePrefs();
        applySort();
      });
    });

    var listBtn = qs('#file-view-list');
    var gridBtn = qs('#file-view-grid');
    if (listBtn) listBtn.addEventListener('click', function () {
      prefs.viewMode = 'list';
      savePrefs();
      syncViewModeUI();
    });
    if (gridBtn) gridBtn.addEventListener('click', function () {
      prefs.viewMode = 'grid';
      savePrefs();
      syncViewModeUI();
    });

    var hiddenBtn = qs('#file-hidden-btn');
    if (hiddenBtn) hiddenBtn.addEventListener('click', function () {
      prefs.showHidden = !prefs.showHidden;
      savePrefs();
      syncHiddenToggleUI();
      applyFilter();
    });

    var pasteBtn = qs('#file-paste-btn');
    if (pasteBtn) pasteBtn.addEventListener('click', pasteClipboard);
    var pasteClearBtn = qs('#file-paste-clear-btn');
    if (pasteClearBtn) pasteClearBtn.addEventListener('click', function () {
      clipboard = null;
      syncClipboardUI();
      toast(T('js.files.clipboard_cleared'), 'info');
    });

    // Deep search panel
    var deepBtn = qs('#file-deep-search-btn');
    var deepPanel = qs('#file-deep-search-panel');
    var deepQuery = qs('#file-deep-query');
    var deepRun = qs('#file-deep-run-btn');
    var deepClose = qs('#file-deep-close-btn');
    if (deepBtn && deepPanel) {
      deepBtn.addEventListener('click', function () {
        deepPanel.hidden = !deepPanel.hidden;
        deepBtn.setAttribute('aria-expanded', deepPanel.hidden ? 'false' : 'true');
        deepBtn.classList.toggle('is-active', !deepPanel.hidden);
        if (!deepPanel.hidden && deepQuery) deepQuery.focus();
      });
    }
    if (deepClose && deepPanel && deepBtn) {
      deepClose.addEventListener('click', function () {
        deepPanel.hidden = true;
        deepBtn.setAttribute('aria-expanded', 'false');
        deepBtn.classList.remove('is-active');
      });
    }
    if (deepRun) deepRun.addEventListener('click', runDeepSearch);
    if (deepQuery) deepQuery.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') { e.preventDefault(); runDeepSearch(); }
    });

    // Batch selection bar actions
    var selectAll = qs('#file-select-all');
    if (selectAll) {
      selectAll.addEventListener('change', function () {
        var visibleRows = entryRows().filter(function (r) { return r.style.display !== 'none'; });
        if (selectAll.checked) {
          visibleRows.forEach(function (r) {
            var p = r.getAttribute('data-entry-path');
            if (p) selectedPaths.add(p);
          });
        } else {
          visibleRows.forEach(function (r) {
            var p = r.getAttribute('data-entry-path');
            if (p) selectedPaths.delete(p);
          });
        }
        syncSelectionUI();
      });
    }

    var selClear = qs('#file-sel-clear-btn');
    if (selClear) selClear.addEventListener('click', clearSelection);

    var selDownload = qs('#file-sel-download-btn');
    if (selDownload) selDownload.addEventListener('click', function () {
      var list = Array.from(selectedPaths);
      if (list.length === 1) {
        var entry = getEntryByPath(list[0]);
        if (entry && entry.kind !== 'directory' && !entry.link_is_dir) {
          triggerDownload(entry.path);
          return;
        }
      }
      triggerArchiveDownload(list, 'zip');
    });

    var selCopy = qs('#file-sel-copy-btn');
    if (selCopy) selCopy.addEventListener('click', function () {
      setClipboard('copy', Array.from(selectedPaths));
    });

    var selCut = qs('#file-sel-cut-btn');
    if (selCut) selCut.addEventListener('click', function () {
      setClipboard('cut', Array.from(selectedPaths));
    });

    var selCompress = qs('#file-sel-compress-btn');
    if (selCompress) selCompress.addEventListener('click', function (e) {
      var names = Array.from(selectedPaths).map(function (p) { return p.split('/').pop(); }).filter(Boolean);
      promptCompress(names, e.currentTarget);
    });

    var selChmod = qs('#file-sel-chmod-btn');
    if (selChmod) selChmod.addEventListener('click', function () {
      var paths = Array.from(selectedPaths);
      if (!paths.length) return;
      var hasDir = false;
      var firstOctal = '0644';
      paths.forEach(function (p, i) {
        var e = getEntryByPath(p);
        if (e) {
          if (i === 0 && e.mode_octal) firstOctal = e.mode_octal;
          if (e.kind === 'directory' || e.link_is_dir) hasDir = true;
        }
      });
      openChmodModal(paths, firstOctal, hasDir);
    });

    var selDelete = qs('#file-sel-delete-btn');
    if (selDelete) selDelete.addEventListener('click', function (e) {
      confirmBatchDelete(Array.from(selectedPaths), e.currentTarget);
    });

    syncViewModeUI();
    syncHiddenToggleUI();
    syncClipboardUI();

    if (qs('#file-table')) {
      seedEntriesFromDOM();
      applySort();
      applyFilter();
    }
  }

  function initCredPersistence() {
    var form = qs('#file-form');
    if (!form) return;
    var action = form.getAttribute('action') || '';
    var storageKey = 'nodexia_file_creds' + action.replace(/\//g, '_');
    var pwdInput = qs('#file-password');
    var keyInput = qs('#file-key');
    var phraseInput = qs('#file-passphrase');
    if (!pwdInput && !keyInput && !phraseInput) return;

    var clearBtn = qs('#file-clear-creds-btn');
    var statusBadge = qs('#file-creds-status');
    var toggleBtn = qs('#file-creds-toggle-btn');
    var credsBody = qs('#file-creds-body');

    function readStored() {
      try {
        var raw = sessionStorage.getItem(storageKey);
        return raw ? JSON.parse(raw) : null;
      } catch (e) { return null; }
    }

    function hasAnyInput() {
      return Boolean(
        (pwdInput && pwdInput.value) ||
        (keyInput && keyInput.value) ||
        (phraseInput && phraseInput.value)
      );
    }

    function syncCredUI() {
      var savedNow = readStored();
      var hasStored = Boolean(savedNow && (savedNow.pwd || savedNow.key || savedNow.passphrase));
      var anyActive = hasStored || hasAnyInput();
      if (statusBadge) statusBadge.hidden = !hasStored;
      if (clearBtn) {
        clearBtn.disabled = !anyActive;
        if (anyActive) clearBtn.removeAttribute('aria-disabled');
        else clearBtn.setAttribute('aria-disabled', 'true');
      }
    }

    var saved = readStored();
    if (saved) {
      if (pwdInput && saved.pwd) pwdInput.value = saved.pwd;
      if (keyInput && saved.key) keyInput.value = saved.key;
      if (phraseInput && saved.passphrase) phraseInput.value = saved.passphrase;
    }

    function persist() {
      try {
        if (!hasAnyInput()) {
          sessionStorage.removeItem(storageKey);
        } else {
          sessionStorage.setItem(storageKey, JSON.stringify({
            pwd: pwdInput ? pwdInput.value : '',
            key: keyInput ? keyInput.value : '',
            passphrase: phraseInput ? phraseInput.value : ''
          }));
        }
      } catch (e) {}
      syncCredUI();
    }

    form.addEventListener('submit', persist);
    [pwdInput, keyInput, phraseInput].forEach(function (el) {
      if (!el) return;
      el.addEventListener('change', persist);
      el.addEventListener('input', function () {
        persist();
      });
    });

    if (toggleBtn && credsBody) {
      toggleBtn.addEventListener('click', function () {
        credsBody.hidden = !credsBody.hidden;
        setCredToggleLabel(toggleBtn, !credsBody.hidden);
      });
    }

    if (clearBtn) {
      clearBtn.addEventListener('click', function () {
        try { sessionStorage.removeItem(storageKey); } catch (e) {}
        if (pwdInput) pwdInput.value = '';
        if (keyInput) keyInput.value = '';
        if (phraseInput) phraseInput.value = '';
        if (credsBody) credsBody.hidden = false;
        if (toggleBtn) {
          toggleBtn.hidden = true;
          toggleBtn.setAttribute('aria-expanded', 'true');
        }
        syncCredUI();
        toast(T('js.files.creds_cleared'), 'info');
      });
    }

    syncCredUI();
    return Boolean(saved && (saved.pwd || saved.key || saved.passphrase));
  }

  function boot() {
    initBrowser();
    initDialog();
    initEditor();
    initChmodModal();
    initPropsModal();
    initMenusAndGlobalKeys();
    initDragAndDrop();
    initToolbar();
    var hasSavedSession = initCredPersistence();
    var c = context();
    var ws = qs('#file-workspace');
    if (hasSavedSession && c && !c.hasStoredCreds && ws && ws.hidden) {
      navigateTo((qs('#file-path') && qs('#file-path').value) || '~');
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
