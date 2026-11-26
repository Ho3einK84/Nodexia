/* Nodexia SFTP file manager — client behaviour (CSP-strict, zero-build).
 *
 * Scoped per .tab-pane: AJAX directory navigation with history stack, bookmarks,
 * list/grid views, hidden dotfiles toggle, multi-select batch actions, recursive
 * search, drag-and-drop file & folder uploads with progress/cancel, properties
 * inspector, chmod editor, and code/config/log-tail/image viewer & editor.
 */
(function () {
  'use strict';

  var currentScript = document.currentScript;
  var root = (currentScript && currentScript.closest && currentScript.closest('.tab-pane')) || document;
  var readyHost = root === document ? document.documentElement : root;
  if (readyHost && readyHost.dataset && readyHost.dataset.filesReady === '1') return;
  if (readyHost && readyHost.dataset) readyHost.dataset.filesReady = '1';

  function qs(sel) { return root.querySelector(sel); }
  function qsa(sel) { return root.querySelectorAll(sel); }

  function registerCleanup(fn) {
    if (root === document) return;
    if (!Array.isArray(root.__nxCleanups)) root.__nxCleanups = [];
    root.__nxCleanups.push(fn);
  }
  registerCleanup(function () {
    if (readyHost && readyHost.dataset) delete readyHost.dataset.filesReady;
  });

  function T(key, params) { return window.nxT ? window.nxT(key, params) : key; }
  function formatBytes(n) { return window.nxHumanBytes ? window.nxHumanBytes(n) : (String(n || 0) + ' B'); }
  function el(tag, attrs, children) { return window.nxEl(tag, attrs, children); }
  function iconEl(name, cls) { return el('i', { 'data-lucide': name, class: cls || null, 'aria-hidden': 'true' }); }

  function renderIcons(scope) {
    var target = scope || root;
    if (window.nxRenderIcons) { window.nxRenderIcons(target); return; }
    if (typeof lucide !== 'undefined' && lucide.createIcons) {
      try { lucide.createIcons({ root: target }); } catch (e) {}
    }
  }

  function toast(message, kind) {
    if (window.nxToast) {
      window.nxToast(message, kind || 'info', kind === 'error' ? 5600 : 3400);
    }
  }

  function syncScrollLock() {
    if (window.nxModal && window.nxModal.syncScrollLock) {
      window.nxModal.syncScrollLock();
    } else {
      document.body.classList.toggle('modal-open', Boolean(document.querySelector('.modal.is-open')));
    }
  }

  function setModalVisible(modal, open) {
    if (!modal) return;
    if (open) {
      modal.hidden = false;
      renderIcons(modal);
      requestAnimationFrame(function () {
        if (!modal.hidden) modal.classList.add('is-open');
        syncScrollLock();
      });
    } else {
      modal.classList.remove('is-open');
      modal.hidden = true;
      syncScrollLock();
    }
  }

  function reportBatchResult(data, okKey, okParams) {
    if (data && data.failed && data.failed.length) {
      toast(T('js.files.partial_failures', {
        ok: (data.succeeded || []).length,
        fail: data.failed.length,
        error: data.failed[0].error || ''
      }), 'error');
    } else {
      toast(T(okKey, okParams), 'success');
    }
  }

  /* ── Preferences (localStorage) ─────────────────────────────────────────── */
  var PREFS_KEY = 'nodexia.files.prefs.v1';
  var defaultPrefs = { showHidden: true, viewMode: 'list', sortKey: 'name', sortDir: 'asc', bookmarksOpen: true };
  var prefs = (function () {
    try {
      var raw = localStorage.getItem(PREFS_KEY);
      return Object.assign({}, defaultPrefs, raw ? (JSON.parse(raw) || {}) : {});
    } catch (e) { return Object.assign({}, defaultPrefs); }
  })();
  function savePrefs() { try { localStorage.setItem(PREFS_KEY, JSON.stringify(prefs)); } catch (e) {} }

  /* ── Shared context & state ─────────────────────────────────────────────── */
  var ctx = null;
  var historyStack = [];
  var historyIndex = -1;
  var selectedPaths = new Set();
  var lastCheckedPath = null;
  var clipboard = null; // { mode: 'copy' | 'cut', paths: [] }
  var currentEntries = [];
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
      parentDir: (listing && listing.getAttribute('data-parent-path')) || ''
    };
    return ctx;
  }

  function currentToken(form) {
    return window.nxCSRF ? window.nxCSRF(form || qs('#file-form') || root) : '';
  }

  function refreshCSRFToken() {
    if (window.NodexiaTabs && window.NodexiaTabs.refreshCSRFToken) {
      return window.NodexiaTabs.refreshCSRFToken();
    }
    return fetch('/api/csrf-token', { credentials: 'same-origin' }).then(function (resp) {
      if (!resp.ok) return '';
      return resp.json().then(function (data) {
        if (data && data.csrf_token) {
          document.querySelectorAll('input[name="_csrf_token"]').forEach(function (inp) { inp.value = data.csrf_token; });
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
      var node = qs(map[field]);
      if (node && node.value) out[field] = node.value;
    });
    return out;
  }

  function hasAnyRuntimeCreds() {
    var c = collectCredsObject();
    return Boolean(c.password || c.private_key || c.key_passphrase);
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

  function opsURL(intent) { return context().action + '/ops?intent=' + encodeURIComponent(intent); }

  function postOp(intent, payload, retryAuth) {
    var c = context();
    if (!c) return Promise.reject(new Error('File context missing.'));
    var headers = { 'Content-Type': 'application/json' };
    var csrf = currentToken(c.form);
    if (csrf) headers['X-CSRF-Token'] = csrf;

    return fetch(opsURL(intent), {
      method: 'POST',
      credentials: 'same-origin',
      headers: headers,
      body: JSON.stringify(Object.assign({}, payload || {}, collectCredsObject()))
    }).then(function (res) {
      if (res.status === 403 && !retryAuth) {
        return refreshCSRFToken().then(function () { return postOp(intent, payload, true); });
      }
      return res.json().catch(function () { return {}; }).then(function (data) {
        if (!res.ok || !data.ok) {
          throw new Error((data && data.error) || T('js.files.request_failed', { status: res.status }));
        }
        return data;
      });
    });
  }

  /* ── Spinners & Status Pills ────────────────────────────────────────────── */
  function setRefreshSpinning(spinning) {
    var btn = qs('#file-refresh-btn');
    var icon = btn && btn.querySelector('i, svg, [data-spin-icon]');
    if (icon) icon.classList.toggle('is-spinning', !!spinning);
  }

  function setUploadBusy(busy) {
    var btn = qs('#file-upload-btn');
    if (!btn) return;
    btn.disabled = !!busy;
    if (busy) btn.setAttribute('aria-busy', 'true');
    else btn.removeAttribute('aria-busy');
    var icon = btn.querySelector('i, svg');
    if (icon) icon.classList.toggle('is-spinning', !!busy);
  }

  function stopSpinners() { setRefreshSpinning(false); setUploadBusy(false); }

  function renderDirStats(dirs, files, hiddenCnt, totalSize) {
    var statsEl = qs('#file-dir-stats');
    if (!statsEl) return;
    statsEl.textContent = '';
    statsEl.appendChild(el('span', {
      class: 'file-stat-pill',
      text: T('js.files.summary_counts', { dirs: dirs || 0, files: files || 0, size: totalSize || '0 B' })
    }));
    if ((hiddenCnt || 0) > 0) {
      statsEl.appendChild(el('span', {
        class: 'file-stat-pill',
        text: T('js.files.summary_hidden', { count: hiddenCnt })
      }));
    }
  }

  function renderVfsBar(vfs) {
    var vfsEl = qs('#file-vfs');
    if (!vfsEl) return;
    if (!vfs || !vfs.available) { vfsEl.hidden = true; return; }
    vfsEl.hidden = false;
    var label = qs('#file-vfs-label');
    var fill = qs('#file-vfs-fill');
    if (label) {
      label.textContent = T('js.files.disk_usage', {
        used: vfs.used || '', total: vfs.total || '', pct: vfs.used_percent || 0, free: vfs.free || ''
      });
    }
    if (fill) {
      if (vfs.severity) fill.className = 'file-vfs__fill file-vfs__fill--' + vfs.severity;
      fill.style.width = Math.min(100, Math.max(0, Number(vfs.used_percent) || 0)) + '%';
    }
  }

  /* ── Breadcrumb ─────────────────────────────────────────────────────────── */
  function buildBreadcrumb(pathValue, container) {
    if (!container) return;
    container.textContent = '';
    if (!pathValue) return;
    var filtered = pathValue.replace(/\/+$/, '').split('/').filter(Boolean);
    var frag = document.createDocumentFragment();
    frag.appendChild(el('button', {
      type: 'button',
      class: 'breadcrumb__seg' + ((pathValue === '/' || !filtered.length) ? ' breadcrumb__seg--current' : ''),
      'data-crumb-path': '/',
      text: '/'
    }));
    var acc = '';
    filtered.forEach(function (part, i) {
      acc += '/' + part;
      frag.appendChild(el('span', { class: 'breadcrumb__sep', 'aria-hidden': 'true', text: '›' }));
      frag.appendChild(el('button', {
        type: 'button',
        class: 'breadcrumb__seg' + (i === filtered.length - 1 ? ' breadcrumb__seg--current' : ''),
        'data-crumb-path': acc,
        text: part
      }));
    });
    container.appendChild(frag);
    container.scrollLeft = container.scrollWidth;
  }

  function markPathReady(ready) {
    var nav = qs('#file-nav');
    if (nav) nav.classList.toggle('is-path-ready', !!ready);
  }

  function setPathEditing(editing) {
    var nav = qs('#file-nav');
    if (!nav) return;
    var active = !!editing;
    nav.classList.toggle('is-editing-path', active);
    var editBtn = qs('#file-path-edit-btn');
    if (editBtn) editBtn.setAttribute('aria-expanded', active ? 'true' : 'false');
    var pathInput = qs('#file-path');
    if (active && pathInput && pathInput.focus) {
      pathInput.focus();
      if (pathInput.select) pathInput.select();
    }
  }

  /* ── Entry icon & type helpers ──────────────────────────────────────────── */
  var EXT_ICONS = {
    '.log': 'scroll-text',
    '.conf': 'settings', '.ini': 'settings', '.env': 'settings', '.cfg': 'settings', '.toml': 'settings',
    '.sh': 'terminal', '.bash': 'terminal', '.zsh': 'terminal',
    '.json': 'braces',
    '.yaml': 'file-code', '.yml': 'file-code', '.xml': 'file-code', '.go': 'file-code', '.py': 'file-code',
    '.js': 'file-code', '.ts': 'file-code', '.css': 'file-code', '.html': 'file-code', '.sql': 'file-code',
    '.md': 'file-code', '.txt': 'file-code',
    '.gz': 'archive', '.zip': 'archive', '.tar': 'archive', '.tgz': 'archive', '.xz': 'archive', '.bz2': 'archive',
    '.png': 'image', '.jpg': 'image', '.jpeg': 'image', '.gif': 'image', '.webp': 'image', '.svg': 'image', '.ico': 'image', '.bmp': 'image',
    '.key': 'key-round', '.pem': 'key-round', '.crt': 'key-round', '.pub': 'key-round'
  };

  var BINARY_EXTS = {
    '.zip': 1, '.tar': 1, '.gz': 1, '.tgz': 1, '.xz': 1, '.bz2': 1, '.7z': 1, '.rar': 1,
    '.exe': 1, '.bin': 1, '.so': 1, '.dll': 1, '.dylib': 1, '.deb': 1, '.rpm': 1,
    '.iso': 1, '.dmg': 1, '.sqlite': 1, '.sqlite3': 1, '.db': 1, '.pdf': 1,
    '.mp3': 1, '.mp4': 1, '.mkv': 1, '.avi': 1, '.mov': 1, '.woff': 1, '.woff2': 1
  };

  function iconForEntry(entry) {
    if (entry.kind === 'directory') return 'folder';
    if (entry.is_symlink) return entry.link_is_dir ? 'folder-symlink' : 'file-symlink';
    var ext = (entry.ext || '').toLowerCase();
    if (!ext && entry.name) {
      var dot = entry.name.lastIndexOf('.');
      if (dot > 0) ext = entry.name.slice(dot).toLowerCase();
    }
    return EXT_ICONS[ext] || 'file';
  }

  function isArchiveName(name) {
    var lower = String(name || '').toLowerCase();
    return lower.endsWith('.zip') || lower.endsWith('.tar.gz') || lower.endsWith('.tgz') || lower.endsWith('.tar');
  }

  function isNonPreviewBinaryExt(ext) { return Boolean(BINARY_EXTS[(ext || '').toLowerCase()]); }

  /* ── Selection & Clipboard UI ───────────────────────────────────────────── */
  function entryFromRow(row) {
    var g = function (a) { return row.getAttribute(a) || ''; };
    return {
      name: g('data-entry-name'),
      path: g('data-entry-path'),
      kind: g('data-entry-kind') || 'file',
      size_bytes: parseInt(g('data-entry-size'), 10) || 0,
      size: (row.querySelector('.file-row__size') || {}).textContent || '',
      mod_unix: parseInt(g('data-entry-mtime'), 10) || 0,
      mode: g('data-entry-mode'),
      mode_octal: g('data-entry-octal'),
      uid: parseInt(g('data-entry-uid'), 10) || 0,
      gid: parseInt(g('data-entry-gid'), 10) || 0,
      is_symlink: g('data-entry-symlink') === '1',
      link_target: g('data-entry-link-target'),
      link_is_dir: g('data-entry-link-dir') === '1',
      broken_link: g('data-entry-broken') === '1',
      is_hidden: g('data-entry-hidden') === '1',
      ext: g('data-entry-ext')
    };
  }

  function getEntryByPath(p) {
    for (var i = 0; i < currentEntries.length; i++) {
      if (currentEntries[i].path === p) return currentEntries[i];
    }
    var rows = entryRows();
    for (var j = 0; j < rows.length; j++) {
      if (rows[j].getAttribute('data-entry-path') === p) return entryFromRow(rows[j]);
    }
    return null;
  }

  function syncSelectionUI() {
    var bar = qs('#file-selection-bar');
    var summary = qs('#file-sel-summary');
    var selectAll = qs('#file-select-all');
    var rows = entryRows();
    var visibleCount = 0;
    var visibleSelected = 0;
    var totalBytes = 0;

    rows.forEach(function (row) {
      var vis = row.style.display !== 'none';
      if (vis) visibleCount++;
      var sel = selectedPaths.has(row.getAttribute('data-entry-path'));
      row.classList.toggle('is-selected', sel);
      var cb = row.querySelector('[data-row-check]');
      if (cb) cb.checked = sel;
      if (sel) {
        totalBytes += parseInt(row.getAttribute('data-entry-size'), 10) || 0;
        if (vis) visibleSelected++;
      }
    });

    if (selectAll) {
      selectAll.checked = visibleCount > 0 && visibleSelected === visibleCount;
      selectAll.indeterminate = visibleSelected > 0 && visibleSelected < visibleCount;
    }
    if (bar && summary) {
      bar.hidden = selectedPaths.size === 0;
      if (!bar.hidden) {
        summary.textContent = T('js.files.selected_count', { count: selectedPaths.size, size: formatBytes(totalBytes) });
      }
    }
  }

  function clearSelection() { selectedPaths.clear(); lastCheckedPath = null; syncSelectionUI(); }

  function syncClipboardUI() {
    var pill = qs('#file-clipboard-pill');
    var label = qs('#file-paste-label');
    if (!pill || !label) return;
    pill.hidden = !(clipboard && clipboard.paths.length);
    if (!pill.hidden) {
      label.textContent = T('js.files.clipboard_paste', {
        count: clipboard.paths.length,
        mode: clipboard.mode === 'cut' ? T('js.files.mode_cut') : T('js.files.mode_copy')
      });
    }
  }

  function setClipboard(mode, paths) {
    if (!paths || !paths.length) return;
    clipboard = { mode: mode, paths: paths.slice() };
    syncClipboardUI();
    toast(T(mode === 'cut' ? 'js.files.clipboard_cut' : 'js.files.clipboard_copied', { count: paths.length }), 'info');
  }

  /* ── Bookmarks ──────────────────────────────────────────────────────────── */
  function bookmarksKey() { var c = context(); return 'nodexia.files.bookmarks.' + (c ? c.serverId : '0'); }
  function loadCustomBookmarks() {
    try {
      var parsed = JSON.parse(localStorage.getItem(bookmarksKey()) || '[]');
      return Array.isArray(parsed) ? parsed : [];
    } catch (e) { return []; }
  }
  function saveCustomBookmarks(list) { try { localStorage.setItem(bookmarksKey(), JSON.stringify(list)); } catch (e) {} }

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

    list.textContent = '';
    var seen = {};
    var frag = document.createDocumentFragment();

    function addChip(label, targetPath, removable) {
      if (!targetPath || seen[targetPath]) return;
      seen[targetPath] = true;
      var chip = el('span', {
        class: 'file-bookmark-chip' + (c.currentDir === targetPath ? ' is-active' : ''),
        role: 'button', tabindex: '0', title: targetPath,
        on: {
          click: function (e) { if (!e.target.closest('.file-bookmark-chip__remove')) navigateTo(targetPath); },
          keydown: function (e) { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigateTo(targetPath); } }
        }
      }, [
        el('span', { text: label }),
        removable && el('button', {
          type: 'button', class: 'file-bookmark-chip__remove', 'aria-label': T('common.delete'), text: '×',
          on: {
            click: function (e) {
              e.stopPropagation();
              saveCustomBookmarks(loadCustomBookmarks().filter(function (p) { return p !== targetPath; }));
              renderBookmarks();
              toast(T('js.files.bookmark_removed'), 'info');
            }
          }
        })
      ]);
      frag.appendChild(chip);
    }

    [
      { label: '/', path: '/' },
      { label: '~', path: c.homeDir || '~' },
      { label: '/etc', path: '/etc' },
      { label: '/var/log', path: '/var/log' },
      { label: '/opt', path: '/opt' },
      { label: '/tmp', path: '/tmp' }
    ].forEach(function (b) { addChip(b.label, b.path, false); });
    loadCustomBookmarks().forEach(function (p) { addChip(p, p, true); });
    list.appendChild(frag);
  }

  /* ── History & AJAX Directory Rendering ─────────────────────────────────── */
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
    if (historyIndex >= 0 && historyStack[historyIndex] === dirPath) { syncNavButtons(); return; }
    historyStack = historyStack.slice(0, historyIndex + 1);
    historyStack.push(dirPath);
    if (historyStack.length > 50) historyStack.shift();
    historyIndex = historyStack.length - 1;
    syncNavButtons();
  }

  function setCredToggleLabel(toggleBtn, expanded) {
    if (!toggleBtn) return;
    toggleBtn.setAttribute('aria-expanded', expanded ? 'true' : 'false');
    toggleBtn.textContent = '';
    toggleBtn.appendChild(iconEl(expanded ? 'chevron-up' : 'chevron-down', 'icon-in-button'));
    toggleBtn.appendChild(document.createTextNode(' '));
    toggleBtn.appendChild(el('span', { text: T(expanded ? 'js.files.creds_collapse' : 'js.files.creds_expand') }));
    renderIcons(toggleBtn);
  }

  function buildEntryRow(entry) {
    var isDirLike = entry.kind === 'directory' || !!entry.link_is_dir;
    var rowAttrs = {
      class: 'file-row ' + (isDirLike ? 'file-row--dir' : 'file-row--file') +
        (entry.is_symlink ? ' file-row--symlink' : '') +
        (entry.broken_link ? ' file-row--broken' : '') +
        (entry.is_hidden ? ' file-row--hidden' : ''),
      title: entry.path || '',
      'data-entry-kind': entry.kind || 'file',
      'data-entry-name': entry.name || '',
      'data-entry-path': entry.path || '',
      'data-entry-size': String(entry.size_bytes || 0),
      'data-entry-mtime': String(entry.mod_unix || 0),
      'data-entry-mode': entry.mode || '',
      'data-entry-octal': entry.mode_octal || '',
      'data-entry-uid': String(entry.uid || 0),
      'data-entry-gid': String(entry.gid || 0),
      'data-entry-symlink': entry.is_symlink ? '1' : '0',
      'data-entry-link-target': entry.link_target || '',
      'data-entry-link-dir': entry.link_is_dir ? '1' : '0',
      'data-entry-broken': entry.broken_link ? '1' : '0',
      'data-entry-hidden': entry.is_hidden ? '1' : '0',
      'data-entry-ext': entry.ext || ''
    };
    if (isDirLike) rowAttrs['data-nav-path'] = entry.path;
    else rowAttrs['data-file-path'] = entry.path;

    return el('div', rowAttrs, [
      el('div', { class: 'file-row__check-cell' }, el('input', {
        type: 'checkbox', class: 'file-check file-row__check', 'data-row-check': true, 'aria-label': entry.name || ''
      })),
      el('div', { class: 'file-row__name' }, [
        iconEl(iconForEntry(entry), 'file-row__icon'),
        el('span', { class: 'file-row__name-text', dir: 'ltr', title: entry.name || '', text: entry.name || '' }),
        entry.is_symlink && entry.link_target && el('span', {
          class: 'file-row__symlink-target' + (entry.broken_link ? ' is-broken' : ''),
          dir: 'ltr', title: entry.link_target, text: '→ ' + entry.link_target
        })
      ]),
      el('div', { class: 'file-row__meta' }, [
        el('span', {
          class: 'file-row__mode', dir: 'ltr',
          title: (entry.mode_octal || '') + ' (' + (entry.uid || 0) + ':' + (entry.gid || 0) + ')'
        }, [entry.mode || '', entry.mode_octal && ' ', entry.mode_octal && el('em', { class: 'file-row__octal', text: entry.mode_octal })]),
        el('span', { class: 'file-row__size', dir: 'ltr', text: isDirLike ? '—' : (entry.size || '0 B') }),
        el('span', { class: 'file-row__date', dir: 'ltr', text: entry.modified_at || '—' })
      ]),
      el('div', { class: 'file-row__actions' }, el('button', {
        type: 'button', class: 'file-row__menu-btn', 'data-menu-trigger': true,
        'aria-label': entry.name || '', 'aria-haspopup': 'true', 'aria-expanded': 'false'
      }, iconEl('more-vertical')))
    ]);
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

    renderDirStats(listing.dir_count, listing.file_count, listing.hidden_count, listing.total_size);
    renderVfsBar(listing.vfs);

    var table = qs('#file-table');
    if (table) {
      Array.prototype.slice.call(table.querySelectorAll('.file-row')).forEach(function (r) { r.remove(); });
      var noMatch = qs('#file-no-match');
      var frag = document.createDocumentFragment();

      if (c.parentDir) {
        frag.appendChild(el('div', { class: 'file-row file-row--dir file-row--parent', 'data-nav-path': c.parentDir, title: c.parentDir }, [
          el('div', { class: 'file-row__check-cell' }),
          el('div', { class: 'file-row__name' }, [iconEl('arrow-up', 'file-row__icon'), el('span', { class: 'file-row__name-text', dir: 'ltr', text: '..' })]),
          el('div', { class: 'file-row__meta' }, [el('span', { class: 'file-row__mode' }), el('span', { class: 'file-row__size', text: '—' }), el('span', { class: 'file-row__date', text: '—' })]),
          el('div', { class: 'file-row__actions' })
        ]));
      }

      var entries = Array.isArray(listing.entries) ? listing.entries : [];
      currentEntries = entries;
      entries.forEach(function (entry) { frag.appendChild(buildEntryRow(entry)); });
      table.insertBefore(frag, noMatch);

      var emptyEl = qs('#file-empty-dir');
      if (emptyEl) emptyEl.hidden = entries.length !== 0;
    }

    clearSelection();
    applySort();
    applyFilter();
    renderBookmarks();
    syncNavButtons();
    renderIcons(listingEl || root);
  }

  function navigateTo(dirPath, opts) {
    opts = opts || {};
    var c = context();
    if (!c || !ensureCredsOrPrompt()) return Promise.resolve();
    closeMenu();
    setRefreshSpinning(true);
    return postOp('list', { path: dirPath || c.currentDir || '~' }).then(function (data) {
      setRefreshSpinning(false);
      if (data && data.listing) {
        renderDirectoryListing(data.listing);
        if (opts.pushHistory !== false) pushHistory(data.listing.path);
      }
    }).catch(function (err) {
      setRefreshSpinning(false);
      var hint = qs('#file-connect-hint');
      if (hint && hint.getAttribute('aria-busy') === 'true') {
        hint.removeAttribute('aria-busy');
        hint.textContent = err.message || T('js.files.request_failed', { status: 0 });
      }
      toast(err.message || T('js.files.request_failed', { status: 0 }), 'error');
    });
  }

  function reloadCurrent() {
    var c = context();
    if (!c) { stopSpinners(); return Promise.resolve(); }
    return navigateTo(c.currentDir || '~', { pushHistory: false });
  }

  /* ── Upload (XHR for progress + folder traversal) ───────────────────────── */
  var activeUploadXhr = null;
  var uploadCancelled = false;

  registerCleanup(function () {
    uploadCancelled = true;
    if (activeUploadXhr) { try { activeUploadXhr.abort(); } catch (e) {} activeUploadXhr = null; }
    if (activeFetchController) { try { activeFetchController.abort(); } catch (e) {} activeFetchController = null; }
    stopSpinners();
  });

  function uploadOne(item, onProgress, retryAuth) {
    return new Promise(function (resolve, reject) {
      var c = context();
      if (!c) { reject(new Error(T('js.files.upload_network_error'))); return; }
      var body = new FormData();
      // Credentials and paths must precede the file part so the server streams directly to SFTP.
      body.append('path', c.currentDir || '/');
      if (item.relativePath) body.append('relative_path', item.relativePath);
      var creds = collectCredsObject();
      Object.keys(creds).forEach(function (k) { body.append(k, creds[k]); });
      body.append('file', item.file, item.file.name);

      var xhr = new XMLHttpRequest();
      activeUploadXhr = xhr;
      xhr.open('POST', opsURL('upload'));
      var csrf = currentToken(c.form);
      if (csrf) xhr.setRequestHeader('X-CSRF-Token', csrf);
      if (xhr.upload) {
        xhr.upload.onprogress = function (e) { if (e.lengthComputable && onProgress) onProgress(e.loaded / e.total); };
      }
      var clearActive = function () { if (activeUploadXhr === xhr) activeUploadXhr = null; };
      xhr.onload = function () {
        clearActive();
        if (xhr.status === 403 && !retryAuth) {
          refreshCSRFToken().then(function () { uploadOne(item, onProgress, true).then(resolve, reject); });
          return;
        }
        var data = {};
        try { data = JSON.parse(xhr.responseText || '{}'); } catch (e) {}
        if (xhr.status >= 200 && xhr.status < 300 && data && data.ok) { resolve(data); return; }
        var st = xhr.status || 0;
        reject(new Error((data && data.error) || (st === 0 ? T('js.files.upload_network_error') : T('js.files.upload_failed', { status: st }))));
      };
      xhr.onerror = function () { clearActive(); reject(new Error(T('js.files.upload_network_error'))); };
      xhr.onabort = function () { clearActive(); reject(new Error(T('js.files.upload_aborted'))); };
      try { xhr.send(body); } catch (e) { clearActive(); reject(new Error(T('js.files.upload_network_error'))); }
    });
  }

  function uploadItems(items) {
    if (!items || !items.length || !ensureCredsOrPrompt()) return;
    var panel = qs('#file-uploads');
    var listEl = qs('#file-uploads-list');
    var titleEl = qs('#file-uploads-title');
    var clearBtn = qs('#file-uploads-clear-btn');
    if (!panel || !listEl) return;

    uploadCancelled = false;
    panel.hidden = false;
    listEl.textContent = '';
    if (titleEl) titleEl.textContent = T('js.files.upload_queue_title') + ' (' + items.length + ')';
    if (clearBtn) { clearBtn.textContent = T('js.files.upload_clear'); clearBtn.hidden = true; }

    var rows = [];
    var frag = document.createDocumentFragment();
    items.forEach(function (item) {
      var fill = el('div', { class: 'file-upload__fill' });
      var pct = el('span', { class: 'file-upload__pct', text: '0%' });
      var rec = { row: null, fill: fill, pct: pct, cancelBtn: null, cancelled: false };
      var cancelBtn = el('button', {
        type: 'button', class: 'file-icon-btn file-icon-btn--compact', title: T('js.files.upload_cancel'),
        on: {
          click: function () {
            rec.cancelled = true;
            if (rec.isActive && activeUploadXhr) { try { activeUploadXhr.abort(); } catch (e) {} }
            else { rec.row.classList.add('file-upload--error'); pct.textContent = T('js.files.upload_aborted'); cancelBtn.hidden = true; }
          }
        }
      }, iconEl('x'));
      var label = item.relativePath || item.file.name;
      var row = el('div', { class: 'file-upload' }, [
        el('span', { class: 'file-upload__name', title: label, text: label }),
        el('div', { class: 'file-upload__bar' }, fill),
        pct,
        cancelBtn
      ]);
      rec.row = row;
      rec.cancelBtn = cancelBtn;
      frag.appendChild(row);
      rows.push(rec);
    });
    listEl.appendChild(frag);
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
      var ui = rows[index];
      if (ui.cancelled) { index++; next(); return; }
      ui.isActive = true;
      uploadOne(items[index], function (frac) {
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
        var msg = (err && err.message) ? err.message : T('js.files.upload_network_error');
        if (!ui.cancelled) errors.push(msg);
        ui.row.classList.add('file-upload--error');
        ui.pct.textContent = T('js.failed');
        ui.row.title = msg;
        ui.cancelBtn.hidden = true;
      }).then(function () { index++; next(); });
    }
    next();
  }

  function uploadFileList(fileList, useRelativePath) {
    uploadItems(Array.prototype.map.call(fileList, function (f) {
      return { file: f, relativePath: useRelativePath && f.webkitRelativePath ? f.webkitRelativePath : '' };
    }));
  }

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
        var reader = entry.createReader();
        var all = [];
        var readBatch = function () {
          reader.readEntries(function (batch) {
            if (!batch || !batch.length) {
              var nextPrefix = prefix ? prefix + '/' + entry.name : entry.name;
              Promise.all(all.map(function (child) { return readEntryItems(child, nextPrefix); }))
                .then(function (nested) { resolve([].concat.apply([], nested)); });
              return;
            }
            all = all.concat(Array.prototype.slice.call(batch));
            readBatch();
          }, function () { resolve([]); });
        };
        readBatch();
        return;
      }
      resolve([]);
    });
  }

  function collectDroppedItems(dt) {
    var dtItems = dt && dt.items;
    if (dtItems && dtItems.length && dtItems[0].webkitGetAsEntry) {
      var entries = [];
      for (var i = 0; i < dtItems.length; i++) {
        var entry = dtItems[i].webkitGetAsEntry();
        if (entry) entries.push(entry);
      }
      return Promise.all(entries.map(function (en) { return readEntryItems(en, ''); }))
        .then(function (groups) { return [].concat.apply([], groups); });
    }
    return Promise.resolve(Array.prototype.map.call((dt && dt.files) || [], function (f) {
      return { file: f, relativePath: '' };
    }));
  }

  /* ── CSP-safe Action Dialog ─────────────────────────────────────────────── */
  var dialogActive = null;

  function getDialogNodes() {
    var dialog = qs('#file-dialog');
    if (!dialog) return null;
    var dq = function (s) { return dialog.querySelector(s); };
    return {
      dialog: dialog,
      panel: dq('.modal__dialog'),
      title: dq('[data-file-dialog-title]'),
      desc: dq('[data-file-dialog-desc]'),
      list: dq('[data-file-dialog-list]'),
      field: dq('[data-file-dialog-field]'),
      input: dq('[data-file-dialog-input]'),
      hint: dq('[data-file-dialog-hint]'),
      field2: dq('[data-file-dialog-field2]'),
      input2: dq('[data-file-dialog-input2]'),
      selectWrap: dq('[data-file-dialog-select-wrap]'),
      select: dq('[data-file-dialog-select]'),
      error: dq('[data-file-dialog-error]'),
      confirmBtn: dq('[data-file-dialog-confirm]')
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
      setModalVisible(nodes.dialog, false);
      clearDialogError(nodes);
    }
    if (restoreFocus && active && active.returnFocusTo && active.returnFocusTo.isConnected && active.returnFocusTo.focus) {
      active.returnFocusTo.focus();
    }
  }

  function submitDialog() {
    if (!dialogActive) return;
    var active = dialogActive;
    var nodes = getDialogNodes();
    if (!nodes) { closeDialog(false); return; }
    var v1 = nodes.input ? nodes.input.value : '';
    var v2 = nodes.input2 ? nodes.input2.value : '';
    var sel = nodes.select ? nodes.select.value : '';
    if (active.mode === 'input' && typeof active.validate === 'function') {
      var err = active.validate(v1, v2, sel);
      if (err) {
        if (nodes.error) { nodes.error.textContent = err; nodes.error.hidden = false; }
        if (nodes.input) { nodes.input.setAttribute('aria-invalid', 'true'); nodes.input.focus(); }
        return;
      }
    }
    closeDialog(true);
    if (typeof active.onConfirm === 'function') active.onConfirm(v1, v2, sel);
  }

  function openDialog(opts) {
    var nodes = getDialogNodes();
    if (!nodes) return;
    closeMenu();
    dialogActive = {
      mode: opts.mode || 'confirm',
      validate: opts.validate || null,
      onConfirm: opts.onConfirm || null,
      returnFocusTo: opts.returnFocusTo || ((document.activeElement && root.contains(document.activeElement)) ? document.activeElement : null)
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
      nodes.list.textContent = '';
      if (Array.isArray(opts.items) && opts.items.length) {
        var frag = document.createDocumentFragment();
        opts.items.slice(0, 20).forEach(function (item) { frag.appendChild(el('li', { text: item })); });
        if (opts.items.length > 20) frag.appendChild(el('li', { text: '… +' + (opts.items.length - 20) }));
        nodes.list.appendChild(frag);
        nodes.list.hidden = false;
      } else {
        nodes.list.hidden = true;
      }
    }

    if (nodes.field) nodes.field.hidden = !(dialogActive.mode === 'input' && nodes.input);
    if (nodes.input) {
      nodes.input.value = dialogActive.mode === 'input' ? (opts.initialValue || '') : '';
      nodes.input.placeholder = opts.placeholder || '';
    }
    if (nodes.hint) { nodes.hint.textContent = opts.hint || ''; nodes.hint.hidden = !opts.hint; }
    if (nodes.field2) nodes.field2.hidden = !(opts.secondInput && nodes.input2);
    if (nodes.input2) {
      nodes.input2.value = opts.secondInput ? (opts.secondValue || '') : '';
      nodes.input2.placeholder = opts.secondPlaceholder || '';
    }

    if (nodes.selectWrap && nodes.select) {
      if (Array.isArray(opts.selectOptions) && opts.selectOptions.length) {
        nodes.select.textContent = '';
        opts.selectOptions.forEach(function (opt) {
          var o = el('option', { value: opt.value, text: opt.label });
          if (opt.selected) o.selected = true;
          nodes.select.appendChild(o);
        });
        nodes.selectWrap.hidden = false;
      } else {
        nodes.selectWrap.hidden = true;
      }
    }

    setModalVisible(nodes.dialog, true);
    if (dialogActive.mode === 'input' && nodes.input) {
      nodes.input.focus();
      if (nodes.input.value) nodes.input.select();
    } else if (nodes.confirmBtn) {
      nodes.confirmBtn.focus();
    }
  }

  function initDialog() {
    var nodes = getDialogNodes();
    if (!nodes) return;
    nodes.dialog.querySelectorAll('[data-file-dialog-close]').forEach(function (btn) {
      btn.addEventListener('click', function () { closeDialog(true); });
    });
    if (nodes.confirmBtn) nodes.confirmBtn.addEventListener('click', submitDialog);
    [nodes.input, nodes.input2].forEach(function (inp) {
      if (!inp) return;
      inp.addEventListener('input', function () { clearDialogError(nodes); });
      inp.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); submitDialog(); }
      });
    });
    registerCleanup(function () { closeDialog(false); });
  }

  /* ── File Viewer & Code/Config/Log Editor Modal ─────────────────────────── */
  var editorState = {
    open: false, path: '', name: '', isNew: false, editable: false, dirty: false,
    isTail: false, wrapped: false, originalContent: '', autoTailTimer: null, findIndex: -1
  };

  function stopAutoTail() {
    if (editorState.autoTailTimer !== null) { clearInterval(editorState.autoTailTimer); editorState.autoTailTimer = null; }
    var cb = qs('#file-editor-autotail');
    if (cb) cb.checked = false;
  }

  function updateEditorGutter() {
    var ta = qs('#file-editor-textarea');
    var gutter = qs('#file-editor-gutter');
    if (!ta || !gutter) return;
    if (editorState.wrapped) { gutter.hidden = true; return; }
    gutter.hidden = false;
    var lines = (ta.value.match(/\n/g) || []).length + 1;
    var nums = new Array(lines);
    for (var i = 0; i < lines; i++) nums[i] = i + 1;
    gutter.textContent = nums.join('\n');
    gutter.scrollTop = ta.scrollTop;
  }

  function updateEditorCursorMeta() {
    var ta = qs('#file-editor-textarea');
    var cursorEl = qs('#file-editor-cursor');
    if (!ta || !cursorEl) return;
    var pos = ta.selectionStart || 0;
    var before = ta.value.slice(0, pos);
    cursorEl.textContent = T('js.files.editor_cursor', {
      line: (before.match(/\n/g) || []).length + 1,
      col: pos - before.lastIndexOf('\n'),
      lines: (ta.value.match(/\n/g) || []).length + 1
    });
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
    setModalVisible(qs('#file-editor-modal'), false);
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

    postOp('write', { path: editorState.path, content: ta.value, create_only: false }).then(function (data) {
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
    Object.assign(editorState, {
      open: true, path: filePath, name: opts.name || filePath.split('/').pop() || filePath,
      isNew: !!opts.isNew, editable: false, dirty: false, isTail: !!opts.tail, originalContent: '', findIndex: -1
    });

    var hideIds = ['#file-editor-text-wrap', '#file-editor-image-wrap', '#file-editor-binary-wrap',
      '#file-editor-banner', '#file-editor-find-bar', '#file-editor-autotail-wrap', '#file-editor-dirty'];
    hideIds.forEach(function (id) { var n = qs(id); if (n) n.hidden = true; });
    var titleEl = qs('#file-editor-title'); if (titleEl) titleEl.textContent = editorState.name;
    var pathEl = qs('#file-editor-path'); if (pathEl) pathEl.textContent = filePath;
    var loadingEl = qs('#file-editor-loading'); if (loadingEl) loadingEl.hidden = false;
    var saveBtn = qs('#file-editor-save-btn'); if (saveBtn) saveBtn.disabled = true;
    var metaEl = qs('#file-editor-meta'); if (metaEl) metaEl.textContent = '';
    var cursorEl = qs('#file-editor-cursor'); if (cursorEl) cursorEl.textContent = '';

    setModalVisible(modal, true);

    if (opts.isNew) {
      if (loadingEl) loadingEl.hidden = true;
      var textWrap = qs('#file-editor-text-wrap'); if (textWrap) textWrap.hidden = false;
      var ta = qs('#file-editor-textarea');
      if (ta) {
        ta.value = ''; ta.readOnly = false;
        editorState.editable = true;
        syncEditorDirty(); updateEditorGutter(); updateEditorCursorMeta();
        ta.focus();
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
        if (imgWrap && img) { img.src = f.data_url; img.alt = f.name || ''; imgWrap.hidden = false; }
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
        var atBottom = silentRefresh && (ta.scrollHeight - ta.scrollTop - ta.clientHeight < 40);
        ta.value = f.content || '';
        editorState.originalContent = ta.value;
        editorState.isTail = !!f.is_tail;
        editorState.editable = !!f.editable;
        ta.readOnly = !editorState.editable;
        syncEditorDirty(); updateEditorGutter(); updateEditorCursorMeta();
        if (f.is_tail && (atBottom || !silentRefresh)) ta.scrollTop = ta.scrollHeight;
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
          if (bannerAction) { bannerAction.hidden = false; bannerAction.textContent = T('files.editor_tail'); }
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
    if (!query) { if (countEl) countEl.textContent = ''; return; }
    var hay = ta.value.toLowerCase();
    var needle = query.toLowerCase();
    var positions = [];
    for (var idx = hay.indexOf(needle); idx !== -1; idx = hay.indexOf(needle, idx + needle.length)) {
      positions.push(idx);
    }
    if (countEl) countEl.textContent = T('js.files.editor_find_matches', { count: positions.length });
    if (!positions.length) return;

    var cur = ta.selectionStart || 0;
    var target = forward ? positions[0] : positions[positions.length - 1];
    if (forward) {
      for (var i = 0; i < positions.length; i++) { if (positions[i] > cur) { target = positions[i]; break; } }
    } else {
      for (var j = positions.length - 1; j >= 0; j--) { if (positions[j] < cur) { target = positions[j]; break; } }
    }
    ta.setSelectionRange(target, target + query.length);
    var linesBefore = ta.value.slice(0, target).split('\n').length - 1;
    ta.scrollTop = Math.max(0, linesBefore * (parseFloat(window.getComputedStyle(ta).lineHeight) || 20) - ta.clientHeight * 0.35);
    ta.focus();
    updateEditorCursorMeta();
  }

  function initEditor() {
    var modal = qs('#file-editor-modal');
    if (!modal) return;
    modal.querySelectorAll('[data-editor-close]').forEach(function (btn) { btn.addEventListener('click', closeEditorModal); });

    var ta = qs('#file-editor-textarea');
    var gutter = qs('#file-editor-gutter');
    if (ta) {
      var gutterRaf = 0;
      ta.addEventListener('input', function () { syncEditorDirty(); updateEditorGutter(); updateEditorCursorMeta(); });
      ta.addEventListener('scroll', function () {
        if (!gutter || gutterRaf) return;
        gutterRaf = requestAnimationFrame(function () { gutterRaf = 0; gutter.scrollTop = ta.scrollTop; });
      }, { passive: true });
      ta.addEventListener('click', updateEditorCursorMeta);
      ta.addEventListener('keyup', updateEditorCursorMeta);
      ta.addEventListener('keydown', function (e) {
        if (e.key === 'Tab' && !ta.readOnly) {
          e.preventDefault();
          var start = ta.selectionStart;
          ta.value = ta.value.substring(0, start) + '  ' + ta.value.substring(ta.selectionEnd);
          ta.selectionStart = ta.selectionEnd = start + 2;
          syncEditorDirty(); updateEditorGutter();
          return;
        }
        if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') { e.preventDefault(); saveEditorFile(); return; }
        if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'f') {
          e.preventDefault();
          var fb = qs('#file-editor-find-bar');
          var fi = qs('#file-editor-find-input');
          if (fb) fb.hidden = false;
          if (fi) { fi.focus(); fi.select(); }
        }
      });
    }

    var bind = function (sel, fn) { var n = qs(sel); if (n) n.addEventListener('click', fn); };
    bind('#file-editor-save-btn', saveEditorFile);
    bind('#file-editor-download-btn', function () { if (editorState.path) triggerDownload(editorState.path); });
    bind('#file-editor-binary-dl-btn', function () { if (editorState.path) triggerDownload(editorState.path); });
    bind('#file-editor-force-tail-btn', function () {
      if (!editorState.path) return;
      var bw = qs('#file-editor-binary-wrap'); if (bw) bw.hidden = true;
      loadEditorContent(editorState.path, true, false);
    });
    bind('#file-editor-tail-btn', function () { if (editorState.path) loadEditorContent(editorState.path, true, false); });
    bind('#file-editor-banner-action', function () { if (editorState.path) loadEditorContent(editorState.path, true, false); });

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
        if (editorState.autoTailTimer !== null) { clearInterval(editorState.autoTailTimer); editorState.autoTailTimer = null; }
        if (autoTailCb.checked && editorState.open && editorState.path) {
          editorState.autoTailTimer = setInterval(function () {
            if (editorState.open && editorState.path) loadEditorContent(editorState.path, true, true);
          }, 3000);
        }
      });
    }

    var findBar = qs('#file-editor-find-bar');
    var findInput = qs('#file-editor-find-input');
    bind('#file-editor-find-btn', function () {
      if (!findBar) return;
      findBar.hidden = !findBar.hidden;
      if (!findBar.hidden && findInput) { findInput.focus(); findInput.select(); }
    });
    bind('#file-editor-find-close', function () { if (findBar) findBar.hidden = true; });
    bind('#file-editor-find-next', function () { findInEditor(true); });
    bind('#file-editor-find-prev', function () { findInEditor(false); });
    if (findInput) {
      findInput.addEventListener('input', function () { findInEditor(true); });
      findInput.addEventListener('keydown', function (e) { if (e.key === 'Enter') { e.preventDefault(); findInEditor(!e.shiftKey); } });
    }
    registerCleanup(forceCloseEditor);
  }

  /* ── Chmod (Permissions) Modal ──────────────────────────────────────────── */
  var chmodState = { open: false, paths: [], hasDir: false };

  function modeToSymbolic(val) {
    var triads = [
      { r: 0o400, w: 0o200, x: 0o100, s: 0o4000, sc: 's', Sc: 'S' },
      { r: 0o040, w: 0o020, x: 0o010, s: 0o2000, sc: 's', Sc: 'S' },
      { r: 0o004, w: 0o002, x: 0o001, s: 0o1000, sc: 't', Sc: 'T' }
    ];
    return '-' + triads.map(function (t) {
      return ((val & t.r) ? 'r' : '-') + ((val & t.w) ? 'w' : '-') +
        ((val & t.s) ? ((val & t.x) ? t.sc : t.Sc) : ((val & t.x) ? 'x' : '-'));
    }).join('');
  }

  function syncChmodFromValue(val, skipInput) {
    var modal = qs('#file-chmod-modal');
    if (!modal) return;
    modal.querySelectorAll('[data-chmod-bit]').forEach(function (cb) {
      cb.checked = Boolean(val & parseInt(cb.getAttribute('data-chmod-bit'), 8));
    });
    var octInput = qs('#file-chmod-octal');
    var symEl = qs('#file-chmod-symbolic');
    if (!skipInput && octInput) octInput.value = val.toString(8).padStart(4, '0');
    if (symEl) symEl.textContent = modeToSymbolic(val);
  }

  function closeChmodModal() {
    chmodState.open = false;
    setModalVisible(qs('#file-chmod-modal'), false);
  }

  function openChmodModal(paths, initialOctal, hasDir) {
    var modal = qs('#file-chmod-modal');
    if (!modal || !paths || !paths.length) return;
    closeMenu();
    Object.assign(chmodState, { open: true, paths: paths.slice(), hasDir: !!hasDir });

    var targetEl = qs('#file-chmod-target');
    if (targetEl) targetEl.textContent = paths.length === 1 ? paths[0] : T('js.files.items_count', { count: paths.length });
    var recWrap = qs('#file-chmod-recursive-wrap'); if (recWrap) recWrap.hidden = !hasDir;
    var recCb = qs('#file-chmod-recursive'); if (recCb) recCb.checked = false;
    var errEl = qs('#file-chmod-error'); if (errEl) { errEl.textContent = ''; errEl.hidden = true; }

    var val = parseInt(initialOctal || '0644', 8);
    syncChmodFromValue(isNaN(val) ? 0o644 : val, false);
    setModalVisible(modal, true);
  }

  function initChmodModal() {
    var modal = qs('#file-chmod-modal');
    if (!modal) return;
    modal.querySelectorAll('[data-chmod-close]').forEach(function (btn) { btn.addEventListener('click', closeChmodModal); });

    modal.querySelectorAll('[data-chmod-bit]').forEach(function (cb) {
      cb.addEventListener('change', function () {
        var val = 0;
        modal.querySelectorAll('[data-chmod-bit]').forEach(function (b) {
          if (b.checked) val |= parseInt(b.getAttribute('data-chmod-bit'), 8);
        });
        syncChmodFromValue(val, false);
      });
    });

    var octInput = qs('#file-chmod-octal');
    if (octInput) {
      octInput.addEventListener('input', function () {
        var errEl = qs('#file-chmod-error'); if (errEl) errEl.hidden = true;
        var raw = octInput.value.trim();
        if (/^[0-7]{3,4}$/.test(raw)) syncChmodFromValue(parseInt(raw, 8), true);
      });
    }

    modal.querySelectorAll('[data-chmod-preset]').forEach(function (btn) {
      btn.addEventListener('click', function () { syncChmodFromValue(parseInt(btn.getAttribute('data-chmod-preset'), 8), false); });
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
        applyBtn.disabled = true;
        postOp('chmod', { paths: chmodState.paths, mode: raw, recursive: Boolean(recCb && recCb.checked && chmodState.hasDir) })
          .then(function (data) {
            applyBtn.disabled = false;
            closeChmodModal();
            reportBatchResult(data, 'js.files.chmod_done', { mode: raw.padStart(4, '0') });
            reloadCurrent();
          }).catch(function (err) { applyBtn.disabled = false; toast(err.message, 'error'); });
      });
    }
    registerCleanup(closeChmodModal);
  }

  /* ── Properties Modal ───────────────────────────────────────────────────── */
  var propsState = { open: false, path: '' };

  function closePropsModal() {
    propsState.open = false;
    setModalVisible(qs('#file-props-modal'), false);
  }

  function renderPropsRows(stat) {
    var list = qs('#file-props-list');
    var calcBtn = qs('#file-props-calc-btn');
    if (!list) return;
    list.textContent = '';
    var frag = document.createDocumentFragment();
    var addRow = function (label, value) {
      frag.appendChild(el('div', { class: 'file-props__row' }, [
        el('dt', { text: label }),
        el('dd', { dir: 'ltr', text: value })
      ]));
    };

    var typeStr = stat.is_dir ? T('js.files.props_type_dir')
      : stat.is_symlink ? (T('js.files.props_type_symlink') + (stat.broken_link ? ' (' + T('js.files.props_broken') + ')' : ''))
      : T('js.files.props_type_file');

    addRow(T('common.name'), stat.name || '');
    addRow(T('js.files.props_path'), stat.path || '');
    addRow(T('js.files.props_type'), typeStr);
    if (stat.is_symlink && stat.link_target) addRow(T('js.files.props_target'), stat.link_target);
    if (!stat.is_dir) {
      addRow(T('js.files.props_size'), (stat.size || '0 B') + ' (' + (stat.size_bytes || 0) + ' B)');
    } else if (stat.size_calculated) {
      var val = T('js.files.props_contents_val', { files: stat.dir_files || 0, dirs: stat.dir_dirs || 0, size: stat.dir_total_size || '0 B' });
      if (stat.size_truncated) val += ' ' + T('js.files.props_truncated');
      addRow(T('js.files.props_contents'), val);
    }
    addRow(T('js.files.props_perms'), (stat.mode || '') + ' (' + (stat.mode_octal || '') + ')');
    addRow(T('js.files.props_owner'), (stat.uid || 0) + ':' + (stat.gid || 0));
    addRow(T('js.files.props_modified'), stat.modified_at || '-');
    if (stat.access_time && stat.access_time !== '-') addRow(T('js.files.props_accessed'), stat.access_time);
    list.appendChild(frag);
    if (calcBtn) calcBtn.hidden = !stat.is_dir || !!stat.size_calculated;
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
    list.textContent = '';
    if (calcBtn) calcBtn.hidden = true;
    setModalVisible(modal, true);

    postOp('stat', { path: targetPath, calc_size: false }).then(function (data) {
      if (propsState.open && propsState.path === targetPath) renderPropsRows(data.stat || {});
    }).catch(function (err) { closePropsModal(); toast(err.message, 'error'); });
  }

  function initPropsModal() {
    var modal = qs('#file-props-modal');
    if (!modal) return;
    modal.querySelectorAll('[data-props-close]').forEach(function (btn) { btn.addEventListener('click', closePropsModal); });
    var calcBtn = qs('#file-props-calc-btn');
    if (calcBtn) {
      calcBtn.addEventListener('click', function () {
        if (!propsState.path) return;
        calcBtn.disabled = true;
        postOp('stat', { path: propsState.path, calc_size: true }).then(function (data) {
          calcBtn.disabled = false;
          if (propsState.open) renderPropsRows(data.stat || {});
        }).catch(function (err) { calcBtn.disabled = false; toast(err.message, 'error'); });
      });
    }
    registerCleanup(closePropsModal);
  }

  /* ── Row Actions Menu (kebab) ───────────────────────────────────────────── */
  var menu = null;
  var menuOpenFor = null;

  function ensureMenu() {
    if (menu) return menu;
    menu = el('div', { class: 'file-menu', role: 'menu', hidden: true });
    document.body.appendChild(menu);
    return menu;
  }

  function closeMenu() {
    if (!menu || menu.hidden) return;
    menu.hidden = true;
    if (menuOpenFor) { menuOpenFor.setAttribute('aria-expanded', 'false'); menuOpenFor = null; }
  }

  function menuItem(label, icon, danger, onClick) {
    return el('button', {
      type: 'button',
      class: 'file-menu__item' + (danger ? ' file-menu__item--danger' : ''),
      role: 'menuitem',
      on: { click: function () { var opener = menuOpenFor; closeMenu(); onClick(opener); } }
    }, [iconEl(icon), el('span', { text: label })]);
  }

  function menuDivider() { return el('div', { class: 'file-menu__divider', role: 'separator' }); }

  function copyPath(p) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(p)
        .then(function () { toast(T('js.files.path_copied'), 'info'); })
        .catch(function () { toast(T('js.files.path_copy_failed'), 'error'); });
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
    m.textContent = '';
    var frag = document.createDocumentFragment();
    var add = function (node) { if (node) frag.appendChild(node); };

    if (isDirLike) {
      add(menuItem(T('common.open'), 'folder-open', false, function () { navigateTo(entry.path); }));
      add(menuItem(T('js.files.menu_download_zip'), 'file-archive', false, function () { triggerArchiveDownload([entry.path], 'zip', entry.name); }));
    } else {
      add(menuItem(T('js.files.menu_preview_edit'), 'file-code', false, function () { openEditorModal(entry.path, { name: entry.name }); }));
      add(menuItem(T('js.files.menu_tail'), 'scroll-text', false, function () { openEditorModal(entry.path, { name: entry.name, tail: true }); }));
      add(menuItem(T('js.files.menu_download'), 'download', false, function () { triggerDownload(entry.path); }));
      if (isArchiveName(entry.name)) {
        add(menuItem(T('js.files.menu_extract'), 'package-open', false, function () { extractArchive(entry.path); }));
      }
    }
    add(menuDivider());
    add(menuItem(T('js.files.menu_copy'), 'copy', false, function () { setClipboard('copy', [entry.path]); }));
    add(menuItem(T('js.files.menu_cut'), 'scissors', false, function () { setClipboard('cut', [entry.path]); }));
    add(menuItem(T('js.files.menu_duplicate'), 'copy-plus', false, function () { duplicatePaths([entry.path]); }));
    add(menuItem(T('js.files.menu_rename'), 'pencil', false, function (op) { promptRename(entry.name, entry.path, op); }));
    add(menuItem(T('js.files.menu_permissions'), 'shield', false, function () { openChmodModal([entry.path], entry.mode_octal || '0644', isDirLike); }));
    add(menuItem(T('js.files.menu_compress'), 'archive', false, function (op) { promptCompress([entry.name], op); }));
    add(menuDivider());
    add(menuItem(T('js.files.menu_properties'), 'info', false, function () { openPropsModal(entry.path); }));
    add(menuItem(T('js.files.menu_copy_path'), 'clipboard', false, function () { copyPath(entry.path); }));
    add(menuItem(T('common.delete'), 'trash-2', true, function (op) { confirmDelete(entry.name, entry.path, entry.kind === 'directory', op); }));

    m.appendChild(frag);
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
    var left = isRTL
      ? Math.max(8, Math.min(r.left, window.innerWidth - mw - 8))
      : Math.max(8, Math.min(r.right - mw, window.innerWidth - mw - 8));
    var top = r.bottom + gap;
    if (top + mh > window.innerHeight - 8) {
      top = Math.max(8, r.top - mh - gap);
      if (top + mh > window.innerHeight - 8) top = Math.max(8, window.innerHeight - mh - 8);
    }
    m.style.left = Math.round(left) + 'px';
    m.style.top = Math.round(top) + 'px';
  }

  /* ── File Operations ────────────────────────────────────────────────────── */
  function submitDownloadForm(fields) {
    var c = context();
    if (!c) return;
    refreshCSRFToken().then(function (fresh) {
      var all = Object.assign({ _csrf_token: fresh || currentToken(c.form) }, fields, collectCredsObject());
      var f = el('form', { method: 'post', action: c.action, style: 'display:none' },
        Object.keys(all).map(function (k) { return el('input', { type: 'hidden', name: k, value: all[k] }); })
      );
      document.body.appendChild(f);
      f.submit();
      setTimeout(function () { f.remove(); }, 0);
    });
  }

  function triggerDownload(filePath) {
    if (ensureCredsOrPrompt()) submitDownloadForm({ intent: 'download', path: filePath });
  }

  function triggerArchiveDownload(paths, format, name) {
    if (!ensureCredsOrPrompt() || !paths || !paths.length) return;
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
    if (trimmed === '.' || trimmed === '..' || /[\\/\0]/.test(trimmed)) return T('js.files.err_name_invalid');
    return '';
  }

  function validateMkdirPath(val) {
    var trimmed = val.trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, '');
    if (!trimmed) return T('js.files.err_name_required');
    var segs = trimmed.split('/');
    for (var i = 0; i < segs.length; i++) {
      var seg = segs[i].trim();
      if (!seg || seg === '.' || seg === '..' || seg.indexOf('\0') !== -1) return T('js.files.err_name_invalid');
    }
    return '';
  }

  function promptRename(name, oldPath, opener) {
    openDialog({
      mode: 'input', title: T('js.files.rename_title'), desc: T('js.files.rename_prompt', { name: name }),
      initialValue: name, placeholder: T('js.files.rename_placeholder'), confirmLabel: T('js.files.rename_submit'),
      returnFocusTo: opener, validate: validateEntryName,
      onConfirm: function (raw) {
        var next = raw.trim();
        if (!next || next === name) return;
        postOp('rename', { path: oldPath, name: next })
          .then(function () { toast(T('js.files.renamed', { name: next }), 'success'); reloadCurrent(); })
          .catch(function (err) { toast(err.message, 'error'); });
      }
    });
  }

  function confirmDelete(name, target, isDir, opener) {
    openDialog({
      mode: 'confirm', title: T('js.files.delete_title'),
      desc: T(isDir ? 'js.files.confirm_delete_dir' : 'js.files.confirm_delete_file', { name: name }),
      confirmLabel: T('js.files.delete_submit'), danger: true, returnFocusTo: opener,
      onConfirm: function () {
        postOp('delete', { path: target, recursive: !!isDir })
          .then(function () { toast(T('js.files.deleted', { name: name }), 'success'); reloadCurrent(); })
          .catch(function (err) { toast(err.message, 'error'); });
      }
    });
  }

  function confirmBatchDelete(paths, opener) {
    if (!paths || !paths.length) return;
    if (paths.length === 1) {
      var e = getEntryByPath(paths[0]);
      if (e) { confirmDelete(e.name, e.path, e.kind === 'directory', opener); return; }
    }
    openDialog({
      mode: 'confirm', title: T('js.files.delete_title'),
      desc: T('js.files.confirm_delete_multi', { count: paths.length }),
      items: paths.map(function (p) { return p.split('/').pop() || p; }),
      confirmLabel: T('js.files.delete_submit'), danger: true, returnFocusTo: opener,
      onConfirm: function () {
        postOp('delete', { paths: paths, recursive: true }).then(function (data) {
          reportBatchResult(data, 'js.files.deleted_multi', { count: (data.succeeded || paths).length });
          reloadCurrent();
        }).catch(function (err) { toast(err.message, 'error'); });
      }
    });
  }

  function promptMkdir(e) {
    if (!ensureCredsOrPrompt()) return;
    openDialog({
      mode: 'input', title: T('js.files.mkdir_title'), desc: T('js.files.mkdir_prompt'), hint: T('js.files.mkdir_hint'),
      placeholder: T('js.files.mkdir_placeholder'), confirmLabel: T('js.files.mkdir_submit'),
      returnFocusTo: (e && e.currentTarget) || qs('#file-mkdir-btn'), validate: validateMkdirPath,
      onConfirm: function (raw) {
        var name = raw.trim();
        var c = context();
        if (!name || !c) return;
        postOp('mkdir', { path: c.currentDir, name: name, recursive: name.indexOf('/') !== -1 })
          .then(function () { toast(T('js.files.created', { name: name }), 'success'); reloadCurrent(); })
          .catch(function (err) { toast(err.message, 'error'); });
      }
    });
  }

  function promptNewFile(e) {
    if (!ensureCredsOrPrompt()) return;
    openDialog({
      mode: 'input', title: T('js.files.newfile_title'), desc: T('js.files.newfile_prompt'),
      placeholder: T('js.files.newfile_placeholder'), confirmLabel: T('js.files.newfile_submit'),
      returnFocusTo: (e && e.currentTarget) || qs('#file-newfile-btn'), validate: validateEntryName,
      onConfirm: function (raw) {
        var name = raw.trim();
        var c = context();
        if (!name || !c) return;
        var newPath = (c.currentDir === '/' ? '' : c.currentDir.replace(/\/+$/, '')) + '/' + name;
        postOp('write', { path: newPath, content: '', create_only: true }).then(function () {
          toast(T('js.files.file_created', { name: name }), 'success');
          reloadCurrent();
          openEditorModal(newPath, { name: name });
        }).catch(function (err) { toast(err.message, 'error'); });
      }
    });
  }

  function promptSymlink(e) {
    var c = context();
    if (!c || !ensureCredsOrPrompt()) return;
    openDialog({
      mode: 'input', title: T('js.files.symlink_title'), desc: T('js.files.symlink_prompt', { dir: c.currentDir }),
      placeholder: T('js.files.symlink_name_placeholder'),
      secondInput: true, secondPlaceholder: T('js.files.symlink_target_placeholder'),
      confirmLabel: T('js.files.symlink_submit'), returnFocusTo: (e && e.currentTarget) || qs('#file-symlink-btn'),
      validate: function (n, t) { return validateEntryName(n) || (!String(t || '').trim() ? T('js.files.err_target_required') : ''); },
      onConfirm: function (n, t) {
        postOp('symlink', { path: c.currentDir, name: n.trim(), target: t.trim() })
          .then(function () { toast(T('js.files.symlink_created', { name: n.trim() }), 'success'); reloadCurrent(); })
          .catch(function (err) { toast(err.message, 'error'); });
      }
    });
  }

  function promptCompress(names, opener) {
    var c = context();
    if (!c || !names || !names.length || !ensureCredsOrPrompt()) return;
    openDialog({
      mode: 'input', title: T('js.files.compress_title'), desc: T('js.files.compress_prompt', { count: names.length }),
      initialValue: names.length === 1 ? names[0] : 'archive', placeholder: T('js.files.compress_placeholder'),
      selectOptions: [
        { value: 'tar.gz', label: '.tar.gz (gzip tarball)', selected: true },
        { value: 'zip', label: '.zip (ZIP archive)', selected: false }
      ],
      confirmLabel: T('js.files.compress_submit'), returnFocusTo: opener, validate: validateEntryName,
      onConfirm: function (raw, _v2, format) {
        postOp('compress', { path: c.currentDir, names: names, name: raw.trim(), format: format || 'tar.gz' })
          .then(function (data) { toast(T('js.files.compressed', { name: data.name || raw.trim() }), 'success'); reloadCurrent(); })
          .catch(function (err) { toast(err.message, 'error'); });
      }
    });
  }

  function extractArchive(archivePath) {
    var c = context();
    if (!c || !ensureCredsOrPrompt()) return;
    setRefreshSpinning(true);
    postOp('extract', { path: archivePath, dest: c.currentDir }).then(function (data) {
      setRefreshSpinning(false);
      toast(T('js.files.extracted', { files: data.extracted_files || 0, dirs: data.extracted_dirs || 0 }), 'success');
      reloadCurrent();
    }).catch(function (err) { setRefreshSpinning(false); toast(err.message, 'error'); });
  }

  function duplicatePaths(paths) {
    var c = context();
    if (!c || !paths || !paths.length || !ensureCredsOrPrompt()) return;
    postOp('copy', { paths: paths, dest: c.currentDir, duplicate: true }).then(function (data) {
      reportBatchResult(data, 'js.files.pasted_ok', { count: (data.succeeded || paths).length });
      reloadCurrent();
    }).catch(function (err) { toast(err.message, 'error'); });
  }

  function pasteClipboard() {
    var c = context();
    if (!c || !clipboard || !clipboard.paths.length || !ensureCredsOrPrompt()) return;
    var isCut = clipboard.mode === 'cut';
    postOp(isCut ? 'move' : 'copy', { paths: clipboard.paths, dest: c.currentDir, duplicate: !isCut }).then(function (data) {
      if (isCut) { clipboard = null; syncClipboardUI(); }
      reportBatchResult(data, 'js.files.pasted_ok', { count: (data.succeeded || []).length });
      reloadCurrent();
    }).catch(function (err) { toast(err.message, 'error'); });
  }

  /* ── Deep Search ────────────────────────────────────────────────────────── */
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

    if (metaEl) { metaEl.hidden = false; metaEl.textContent = T('js.files.search_searching'); }
    resultsEl.textContent = '';

    postOp('search', {
      path: c.currentDir, query: query,
      search_content: !!(contentCb && contentCb.checked), include_hidden: prefs.showHidden
    }).then(function (data) {
      var matches = Array.isArray(data.matches) ? data.matches : [];
      if (metaEl) {
        var msg = T('js.files.search_results_count', { count: matches.length, scanned: data.scanned || 0 });
        if (data.truncated) msg += ' ' + T('js.files.search_truncated');
        metaEl.textContent = msg;
      }
      if (!matches.length) {
        resultsEl.appendChild(el('p', { class: 'file-no-match', text: T('js.files.search_none') }));
        return;
      }
      var frag = document.createDocumentFragment();
      matches.forEach(function (m) {
        frag.appendChild(el('div', { class: 'file-deep-match' }, [
          el('div', { class: 'file-deep-match__main' }, [
            el('span', { class: 'file-deep-match__path', text: m.path + (m.line ? ':' + m.line : '') }),
            m.snippet && el('span', { class: 'file-deep-match__snippet', text: m.snippet })
          ]),
          el('div', { class: 'file-deep-match__actions' }, [
            el('button', {
              type: 'button', class: 'btn btn--ghost btn--xs', title: m.dir,
              on: { click: function () { navigateTo(m.is_dir ? m.path : m.dir); } }
            }, iconEl('folder-open', 'icon-in-button')),
            !m.is_dir && el('button', {
              type: 'button', class: 'btn btn--ghost btn--xs', title: T('js.files.menu_preview_edit'),
              on: { click: function () { openEditorModal(m.path, { name: m.name }); } }
            }, iconEl('file-code', 'icon-in-button'))
          ])
        ]));
      });
      resultsEl.appendChild(frag);
      renderIcons(resultsEl);
    }).catch(function (err) {
      if (metaEl) metaEl.textContent = err.message;
      toast(err.message, 'error');
    });
  }

  /* ── Filter, Sort & View Mode ───────────────────────────────────────────── */
  function entryRows() {
    var table = qs('#file-table');
    if (!table) return [];
    return Array.prototype.filter.call(table.querySelectorAll('.file-row'), function (r) {
      return !r.classList.contains('file-row--parent');
    });
  }

  function applyFilter() {
    var input = qs('#file-filter');
    var noMatch = qs('#file-no-match');
    var query = input ? input.value.trim().toLowerCase() : '';
    var rows = entryRows();
    var visible = 0;

    rows.forEach(function (row) {
      var show = (prefs.showHidden || row.getAttribute('data-entry-hidden') !== '1') &&
        (query === '' || (row.getAttribute('data-entry-name') || '').toLowerCase().indexOf(query) !== -1);
      row.style.display = show ? '' : 'none';
      if (show) visible++;
    });
    if (noMatch) noMatch.hidden = !(rows.length > 0 && visible === 0);
    syncSelectionUI();
  }

  function applySort() {
    var table = qs('#file-table');
    if (!table) return;
    var rows = entryRows();
    var dirMul = prefs.sortDir === 'desc' ? -1 : 1;
    var nameCmp = function (a, b) {
      return (a.getAttribute('data-entry-name') || '').localeCompare(
        b.getAttribute('data-entry-name') || '', undefined, { sensitivity: 'base', numeric: true });
    };

    rows.sort(function (a, b) {
      var aDir = a.getAttribute('data-entry-kind') === 'directory' || a.getAttribute('data-entry-link-dir') === '1';
      var bDir = b.getAttribute('data-entry-kind') === 'directory' || b.getAttribute('data-entry-link-dir') === '1';
      if (aDir !== bDir) return aDir ? -1 : 1;
      var cmp = 0;
      if (prefs.sortKey === 'size') cmp = (parseInt(a.getAttribute('data-entry-size'), 10) || 0) - (parseInt(b.getAttribute('data-entry-size'), 10) || 0);
      else if (prefs.sortKey === 'date') cmp = (parseInt(a.getAttribute('data-entry-mtime'), 10) || 0) - (parseInt(b.getAttribute('data-entry-mtime'), 10) || 0);
      else if (prefs.sortKey === 'type') cmp = (a.getAttribute('data-entry-ext') || '').toLowerCase().localeCompare((b.getAttribute('data-entry-ext') || '').toLowerCase()) || nameCmp(a, b);
      else cmp = nameCmp(a, b);
      return cmp * dirMul;
    });

    var frag = document.createDocumentFragment();
    rows.forEach(function (r) { frag.appendChild(r); });
    table.insertBefore(frag, qs('#file-no-match'));
    syncSortIndicators();
  }

  function syncSortIndicators() {
    var select = qs('#file-sort');
    if (select && select.value !== prefs.sortKey) select.value = prefs.sortKey;
    var dirBtn = qs('#file-sort-dir');
    if (dirBtn) {
      dirBtn.setAttribute('data-dir', prefs.sortDir);
      dirBtn.title = T(prefs.sortDir === 'desc' ? 'js.files.sort_desc' : 'js.files.sort_asc');
      dirBtn.textContent = '';
      dirBtn.appendChild(iconEl(prefs.sortDir === 'desc' ? 'arrow-down-wide-narrow' : 'arrow-up-narrow-wide'));
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
    if (listBtn) { listBtn.classList.toggle('is-active', !isGrid); listBtn.setAttribute('aria-pressed', !isGrid ? 'true' : 'false'); }
    if (gridBtn) { gridBtn.classList.toggle('is-active', isGrid); gridBtn.setAttribute('aria-pressed', isGrid ? 'true' : 'false'); }
  }

  function syncHiddenToggleUI() {
    var btn = qs('#file-hidden-btn');
    if (!btn) return;
    btn.setAttribute('aria-pressed', prefs.showHidden ? 'true' : 'false');
    btn.classList.toggle('is-active', prefs.showHidden);
    btn.textContent = '';
    btn.appendChild(iconEl(prefs.showHidden ? 'eye' : 'eye-off'));
    renderIcons(btn);
  }

  /* ── Wiring ─────────────────────────────────────────────────────────────── */
  function initBrowser() {
    var form = qs('#file-form');
    var pathInput = qs('#file-path');
    var crumbContainer = qs('#file-breadcrumb');
    var table = qs('#file-table');
    if (!form || !pathInput) return;

    var c = context();
    if (c && c.currentDir) pushHistory(c.currentDir);

    var statsEl = qs('#file-dir-stats');
    var workspace = qs('#file-workspace');
    if (statsEl && workspace && !workspace.hidden) {
      renderDirStats(
        parseInt(statsEl.getAttribute('data-dirs') || '0', 10),
        parseInt(statsEl.getAttribute('data-files') || '0', 10),
        parseInt(statsEl.getAttribute('data-hidden') || '0', 10),
        statsEl.getAttribute('data-total-size') || '0 B'
      );
    }
    var vfsEl = qs('#file-vfs');
    if (vfsEl && !vfsEl.hidden) {
      renderVfsBar({
        available: true,
        used: vfsEl.getAttribute('data-used'),
        total: vfsEl.getAttribute('data-total'),
        used_percent: parseInt(vfsEl.getAttribute('data-pct') || '0', 10) || 0,
        free: vfsEl.getAttribute('data-free')
      });
    }

    if (crumbContainer) {
      buildBreadcrumb(pathInput.value, crumbContainer);
      crumbContainer.addEventListener('click', function (e) {
        var seg = e.target.closest('.breadcrumb__seg[data-crumb-path]');
        if (seg) { navigateTo(seg.getAttribute('data-crumb-path')); return; }
        if (window.matchMedia && window.matchMedia('(max-width: 767px)').matches) setPathEditing(true);
      });
      pathInput.addEventListener('input', function () { buildBreadcrumb(pathInput.value, crumbContainer); });
    }

    var cancelPathEdit = function () {
      var cur = context();
      if (cur && cur.currentDir) pathInput.value = cur.currentDir;
      buildBreadcrumb(pathInput.value, crumbContainer);
      setPathEditing(false);
    };

    var bind = function (sel, fn) { var n = qs(sel); if (n) n.addEventListener('click', fn); };
    bind('#file-path-edit-btn', function () { setPathEditing(true); });
    bind('#file-path-cancel-btn', cancelPathEdit);
    pathInput.addEventListener('keydown', function (e) {
      var nav = qs('#file-nav');
      if (e.key === 'Escape' && nav && nav.classList.contains('is-editing-path')) {
        e.preventDefault(); e.stopPropagation(); cancelPathEdit();
      }
    });
    form.addEventListener('submit', function (e) { e.preventDefault(); navigateTo(pathInput.value.trim() || '~'); });

    bind('#file-back-btn', function () { if (historyIndex > 0) navigateTo(historyStack[--historyIndex], { pushHistory: false }); });
    bind('#file-forward-btn', function () { if (historyIndex < historyStack.length - 1) navigateTo(historyStack[++historyIndex], { pushHistory: false }); });
    bind('#file-up-btn', function () { var cur = context(); if (cur && cur.parentDir) navigateTo(cur.parentDir); });
    bind('#file-home-btn', function () { var cur = context(); navigateTo((cur && cur.homeDir) || '~'); });
    bind('#file-copy-cwd-btn', function () { var cur = context(); if (cur && cur.currentDir) copyPath(cur.currentDir); });
    bind('#file-bookmarks-btn', function () { prefs.bookmarksOpen = !prefs.bookmarksOpen; savePrefs(); renderBookmarks(); });
    bind('#file-pin-bookmark-btn', function () {
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

    if (table) {
      table.addEventListener('click', function (e) {
        var trigger = e.target.closest('[data-menu-trigger]');
        if (trigger) {
          e.stopPropagation();
          if (menuOpenFor === trigger) { closeMenu(); return; }
          closeMenu(); openMenu(trigger);
          return;
        }
        var row = e.target.closest('.file-row');
        if (!row) return;

        var isParent = row.classList.contains('file-row--parent');
        if (e.target.closest('.file-row__check-cell') && !isParent) {
          var rowPath = row.getAttribute('data-entry-path');
          if (!rowPath) return;
          var cb = row.querySelector('[data-row-check]');
          var desired = e.target === cb ? cb.checked : !selectedPaths.has(rowPath);
          if (e.shiftKey && lastCheckedPath) {
            var vis = entryRows().filter(function (r) { return r.style.display !== 'none'; });
            var i1 = vis.findIndex(function (r) { return r.getAttribute('data-entry-path') === lastCheckedPath; });
            var i2 = vis.findIndex(function (r) { return r.getAttribute('data-entry-path') === rowPath; });
            if (i1 !== -1 && i2 !== -1) {
              for (var k = Math.min(i1, i2); k <= Math.max(i1, i2); k++) {
                var p = vis[k].getAttribute('data-entry-path');
                if (desired) selectedPaths.add(p); else selectedPaths.delete(p);
              }
            }
          } else {
            if (desired) selectedPaths.add(rowPath); else selectedPaths.delete(rowPath);
          }
          lastCheckedPath = rowPath;
          syncSelectionUI();
          return;
        }

        if ((e.ctrlKey || e.metaKey) && !isParent) {
          var cp = row.getAttribute('data-entry-path');
          if (cp) {
            if (selectedPaths.has(cp)) selectedPaths.delete(cp); else selectedPaths.add(cp);
            lastCheckedPath = cp;
            syncSelectionUI();
          }
          return;
        }

        var navPath = row.getAttribute('data-nav-path');
        if (navPath) { navigateTo(navPath); return; }

        var filePath = row.getAttribute('data-file-path');
        if (filePath) {
          var ext = row.getAttribute('data-entry-ext') || '';
          var name = row.getAttribute('data-entry-name') || '';
          var sizeBytes = parseInt(row.getAttribute('data-entry-size'), 10) || 0;
          if (isNonPreviewBinaryExt(ext)) triggerDownload(filePath);
          else openEditorModal(filePath, { name: name, tail: ext === '.log' && sizeBytes > 256 * 1024 });
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

    function isTypingTarget(node) {
      return node && (node.tagName === 'INPUT' || node.tagName === 'TEXTAREA' || node.tagName === 'SELECT' || node.isContentEditable);
    }

    function onDocKeyDown(e) {
      if (root !== document && root.hidden) return;

      if (dialogActive) {
        if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeDialog(true); return; }
        if (e.key === 'Tab') {
          var nodes = getDialogNodes();
          var list = (nodes && nodes.panel) ? Array.prototype.filter.call(
            nodes.panel.querySelectorAll('button:not([disabled]), input:not([disabled]), select:not([disabled])'),
            function (n) { var hp = n.closest('[hidden]'); return !n.hidden && (!hp || hp === document.documentElement); }
          ) : [];
          if (list.length) {
            var idx = list.indexOf(document.activeElement);
            e.preventDefault();
            list[e.shiftKey ? (idx <= 0 ? list.length - 1 : idx - 1) : (idx < 0 || idx >= list.length - 1 ? 0 : idx + 1)].focus();
          }
        }
        return;
      }

      if (editorState.open) {
        if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeEditorModal(); }
        else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') { e.preventDefault(); saveEditorFile(); }
        return;
      }
      if (chmodState.open) { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeChmodModal(); } return; }
      if (propsState.open) { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closePropsModal(); } return; }

      if (e.key === 'Escape') { closeMenu(); if (selectedPaths.size) clearSelection(); return; }
      if (isTypingTarget(document.activeElement)) return;

      var keyLow = e.key.toLowerCase();
      if ((e.ctrlKey || e.metaKey) && keyLow === 'a') {
        var ws = qs('#file-workspace');
        if (ws && !ws.hidden) {
          e.preventDefault();
          entryRows().forEach(function (r) {
            if (r.style.display !== 'none') { var p = r.getAttribute('data-entry-path'); if (p) selectedPaths.add(p); }
          });
          syncSelectionUI();
        }
      } else if ((e.ctrlKey || e.metaKey) && keyLow === 'l') {
        e.preventDefault();
        var pathEl = qs('#file-path'); if (pathEl) { pathEl.focus(); pathEl.select(); }
      } else if (e.key === '/' && !e.ctrlKey && !e.metaKey) {
        var filter = qs('#file-filter'); if (filter) { e.preventDefault(); filter.focus(); }
      } else if (e.key === 'Backspace' || (e.altKey && e.key === 'ArrowUp')) {
        var c = context(); if (c && c.parentDir) { e.preventDefault(); navigateTo(c.parentDir); }
      } else if (e.key === 'F2' && selectedPaths.size === 1) {
        e.preventDefault();
        var entry = getEntryByPath(Array.from(selectedPaths)[0]);
        if (entry) promptRename(entry.name, entry.path, null);
      } else if (e.key === 'Delete' && selectedPaths.size > 0) {
        e.preventDefault();
        confirmBatchDelete(Array.from(selectedPaths), null);
      }
    }

    function onWinViewport() { closeMenu(); }

    document.addEventListener('click', onDocClick);
    document.addEventListener('keydown', onDocKeyDown);
    window.addEventListener('resize', onWinViewport, { passive: true });
    window.addEventListener('scroll', onWinViewport, { capture: true, passive: true });

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
    var hasFiles = function (e) {
      var types = e.dataTransfer && e.dataTransfer.types;
      return Boolean(types && Array.prototype.indexOf.call(types, 'Files') !== -1);
    };

    card.addEventListener('dragenter', function (e) { if (hasFiles(e)) { e.preventDefault(); dragDepth++; dropzone.hidden = false; } });
    card.addEventListener('dragover', function (e) { if (hasFiles(e)) { e.preventDefault(); if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy'; } });
    card.addEventListener('dragleave', function (e) {
      if (hasFiles(e)) { dragDepth = Math.max(0, dragDepth - 1); if (dragDepth === 0) dropzone.hidden = true; }
    });
    card.addEventListener('drop', function (e) {
      if (!hasFiles(e)) return;
      e.preventDefault(); dragDepth = 0; dropzone.hidden = true;
      collectDroppedItems(e.dataTransfer).then(function (items) { if (items.length) uploadItems(items); });
    });
  }

  function initToolbar() {
    var bind = function (sel, fn) { var n = qs(sel); if (n) n.addEventListener('click', fn); };
    bind('#file-refresh-btn', function () { reloadCurrent(); });
    bind('#file-newfile-btn', promptNewFile);
    bind('#file-mkdir-btn', promptMkdir);
    bind('#file-symlink-btn', promptSymlink);

    var wireUploadPicker = function (btnSel, inputSel, useRel) {
      var btn = qs(btnSel);
      var inp = qs(inputSel);
      if (!btn || !inp) return;
      btn.addEventListener('click', function () { inp.click(); });
      inp.addEventListener('change', function () {
        if (inp.files && inp.files.length) uploadFileList(inp.files, useRel);
        inp.value = '';
      });
    };
    wireUploadPicker('#file-upload-btn', '#file-upload-input', false);
    wireUploadPicker('#file-upload-dir-btn', '#file-upload-dir-input', true);

    bind('#file-uploads-clear-btn', function () {
      var listEl = qs('#file-uploads-list'); if (listEl) listEl.textContent = '';
      var panel = qs('#file-uploads'); if (panel) panel.hidden = true;
    });

    var filterInput = qs('#file-filter');
    if (filterInput) filterInput.addEventListener('input', applyFilter);

    var sortSelect = qs('#file-sort');
    if (sortSelect) {
      sortSelect.value = prefs.sortKey;
      sortSelect.addEventListener('change', function () { prefs.sortKey = sortSelect.value; savePrefs(); applySort(); });
    }
    bind('#file-sort-dir', function () { prefs.sortDir = prefs.sortDir === 'asc' ? 'desc' : 'asc'; savePrefs(); applySort(); });

    qsa('.file-th[data-sort-key]').forEach(function (th) {
      th.addEventListener('click', function () {
        var key = th.getAttribute('data-sort-key');
        prefs.sortDir = (prefs.sortKey === key && prefs.sortDir === 'asc') ? 'desc' : 'asc';
        prefs.sortKey = key;
        savePrefs(); applySort();
      });
    });

    bind('#file-view-list', function () { prefs.viewMode = 'list'; savePrefs(); syncViewModeUI(); });
    bind('#file-view-grid', function () { prefs.viewMode = 'grid'; savePrefs(); syncViewModeUI(); });
    bind('#file-hidden-btn', function () { prefs.showHidden = !prefs.showHidden; savePrefs(); syncHiddenToggleUI(); applyFilter(); });
    bind('#file-paste-btn', pasteClipboard);
    bind('#file-paste-clear-btn', function () { clipboard = null; syncClipboardUI(); toast(T('js.files.clipboard_cleared'), 'info'); });

    var deepBtn = qs('#file-deep-search-btn');
    var deepPanel = qs('#file-deep-search-panel');
    var deepQuery = qs('#file-deep-query');
    var setDeepOpen = function (open) {
      if (!deepPanel || !deepBtn) return;
      deepPanel.hidden = !open;
      deepBtn.setAttribute('aria-expanded', open ? 'true' : 'false');
      deepBtn.classList.toggle('is-active', open);
      if (open && deepQuery) deepQuery.focus();
    };
    if (deepBtn && deepPanel) deepBtn.addEventListener('click', function () { setDeepOpen(deepPanel.hidden); });
    bind('#file-deep-close-btn', function () { setDeepOpen(false); });
    bind('#file-deep-run-btn', runDeepSearch);
    if (deepQuery) deepQuery.addEventListener('keydown', function (e) { if (e.key === 'Enter') { e.preventDefault(); runDeepSearch(); } });

    var selectAll = qs('#file-select-all');
    if (selectAll) {
      selectAll.addEventListener('change', function () {
        entryRows().forEach(function (r) {
          if (r.style.display === 'none') return;
          var p = r.getAttribute('data-entry-path');
          if (!p) return;
          if (selectAll.checked) selectedPaths.add(p); else selectedPaths.delete(p);
        });
        syncSelectionUI();
      });
    }

    bind('#file-sel-clear-btn', clearSelection);
    bind('#file-sel-download-btn', function () {
      var list = Array.from(selectedPaths);
      if (list.length === 1) {
        var entry = getEntryByPath(list[0]);
        if (entry && entry.kind !== 'directory' && !entry.link_is_dir) { triggerDownload(entry.path); return; }
      }
      triggerArchiveDownload(list, 'zip');
    });
    bind('#file-sel-copy-btn', function () { setClipboard('copy', Array.from(selectedPaths)); });
    bind('#file-sel-cut-btn', function () { setClipboard('cut', Array.from(selectedPaths)); });
    bind('#file-sel-compress-btn', function (e) {
      promptCompress(Array.from(selectedPaths).map(function (p) { return p.split('/').pop(); }).filter(Boolean), e.currentTarget);
    });
    bind('#file-sel-chmod-btn', function () {
      var paths = Array.from(selectedPaths);
      if (!paths.length) return;
      var hasDir = false;
      var firstOctal = '0644';
      paths.forEach(function (p, i) {
        var entry = getEntryByPath(p);
        if (!entry) return;
        if (i === 0 && entry.mode_octal) firstOctal = entry.mode_octal;
        if (entry.kind === 'directory' || entry.link_is_dir) hasDir = true;
      });
      openChmodModal(paths, firstOctal, hasDir);
    });
    bind('#file-sel-delete-btn', function (e) { confirmBatchDelete(Array.from(selectedPaths), e.currentTarget); });

    syncViewModeUI();
    syncHiddenToggleUI();
    syncClipboardUI();

    if (qs('#file-table')) {
      currentEntries = entryRows().map(entryFromRow);
      applySort();
      applyFilter();
    }
  }

  function initCredPersistence() {
    var form = qs('#file-form');
    if (!form) return false;
    var storageKey = 'nodexia_file_creds' + (form.getAttribute('action') || '').replace(/\//g, '_');
    var pwdInput = qs('#file-password');
    var keyInput = qs('#file-key');
    var phraseInput = qs('#file-passphrase');
    if (!pwdInput && !keyInput && !phraseInput) return false;

    var clearBtn = qs('#file-clear-creds-btn');
    var statusBadge = qs('#file-creds-status');
    var toggleBtn = qs('#file-creds-toggle-btn');
    var credsBody = qs('#file-creds-body');

    var readStored = function () {
      var store = window.__nxRuntimeFileCreds;
      if (!store || typeof store.get !== 'function') return null;
      return store.get(storageKey) || null;
    };
    var writeStored = function (val) {
      if (!window.__nxRuntimeFileCreds || typeof window.__nxRuntimeFileCreds.set !== 'function') {
        window.__nxRuntimeFileCreds = new Map();
      }
      if (!val) window.__nxRuntimeFileCreds.delete(storageKey);
      else window.__nxRuntimeFileCreds.set(storageKey, val);
    };
    var hasAnyInput = function () { return Boolean((pwdInput && pwdInput.value) || (keyInput && keyInput.value) || (phraseInput && phraseInput.value)); };
    var syncCredUI = function () {
      var savedNow = readStored();
      var hasStored = Boolean(savedNow && (savedNow.pwd || savedNow.key || savedNow.passphrase));
      var anyActive = hasStored || hasAnyInput();
      if (statusBadge) statusBadge.hidden = !hasStored;
      if (clearBtn) {
        clearBtn.disabled = !anyActive;
        if (anyActive) clearBtn.removeAttribute('aria-disabled');
        else clearBtn.setAttribute('aria-disabled', 'true');
      }
    };

    // Purge any legacy plaintext credentials previously written to sessionStorage.
    try { sessionStorage.removeItem(storageKey); } catch (e) {}

    var saved = readStored();
    if (saved) {
      if (pwdInput && saved.pwd) pwdInput.value = saved.pwd;
      if (keyInput && saved.key) keyInput.value = saved.key;
      if (phraseInput && saved.passphrase) phraseInput.value = saved.passphrase;
    }

    var persist = function () {
      if (!hasAnyInput()) writeStored(null);
      else writeStored({
        pwd: pwdInput ? pwdInput.value : '',
        key: keyInput ? keyInput.value : '',
        passphrase: phraseInput ? phraseInput.value : ''
      });
      syncCredUI();
    };

    form.addEventListener('submit', persist);
    [pwdInput, keyInput, phraseInput].forEach(function (inp) {
      if (!inp) return;
      inp.addEventListener('change', persist);
      inp.addEventListener('input', persist);
    });

    if (toggleBtn && credsBody) {
      toggleBtn.addEventListener('click', function () {
        credsBody.hidden = !credsBody.hidden;
        setCredToggleLabel(toggleBtn, !credsBody.hidden);
      });
    }
    if (clearBtn) {
      clearBtn.addEventListener('click', function () {
        writeStored(null);
        try { sessionStorage.removeItem(storageKey); } catch (e) {}
        if (pwdInput) pwdInput.value = '';
        if (keyInput) keyInput.value = '';
        if (phraseInput) phraseInput.value = '';
        if (credsBody) credsBody.hidden = false;
        if (toggleBtn) { toggleBtn.hidden = true; toggleBtn.setAttribute('aria-expanded', 'true'); }
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
    if (c && c.card && c.card.getAttribute('data-autoload') === '1' && ws && ws.hidden) {
      c.card.removeAttribute('data-autoload');
      var autoPath = c.card.getAttribute('data-autoload-path') || (qs('#file-path') && qs('#file-path').value) || '~';
      navigateTo(autoPath, { pushHistory: true });
    } else if (hasSavedSession && c && !c.hasStoredCreds && ws && ws.hidden) {
      navigateTo((qs('#file-path') && qs('#file-path').value) || '~');
    }
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot);
  else boot();
})();
