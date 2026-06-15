/* Nodexia SFTP file browser — client behaviour.
 *
 * Progressive enhancement over the SSR listing: directory navigation,
 * client-side filter/sort, a per-row actions menu, and fetch/XHR-driven
 * mutations (upload with progress, mkdir, rename, delete) so the page never
 * reloads to act. Mutations hit POST /servers/{id}/files/ops which returns
 * JSON; on success we re-run the existing Browse post to show fresh server
 * truth. Everything no-ops gracefully when its target elements are absent.
 */
(function () {
  'use strict';

  // Localization helper (see app.js for window.nxT). Falls back to the key.
  function T(key, params) { return window.nxT ? window.nxT(key, params) : key; }

  function renderIcons() {
    if (typeof lucide === 'undefined' || !lucide.createIcons) return;
    try { lucide.createIcons(); } catch (e) { /* never let icons break the page */ }
  }

  /* ── Lightweight toast ─────────────────────────────────── */
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
    }, kind === 'error' ? 6000 : 3200);
  }

  /* ── Breadcrumb ────────────────────────────────────────── */
  function buildBreadcrumb(pathValue, container) {
    container.innerHTML = '';
    if (!pathValue) return;
    var parts = pathValue.replace(/\/+$/, '').split('/');
    var rootSeg = document.createElement('span');
    rootSeg.className = 'breadcrumb__seg';
    rootSeg.textContent = '/';
    rootSeg.setAttribute('data-crumb-path', '/');
    container.appendChild(rootSeg);
    var accumulated = '';
    parts.filter(function (p) { return p !== ''; }).forEach(function (part, i, arr) {
      accumulated += '/' + part;
      var sep = document.createElement('span');
      sep.className = 'breadcrumb__sep';
      sep.textContent = '/';
      container.appendChild(sep);
      var seg = document.createElement('span');
      seg.className = 'breadcrumb__seg' + (i === arr.length - 1 ? ' breadcrumb__seg--current' : '');
      seg.textContent = part;
      seg.setAttribute('data-crumb-path', accumulated);
      container.appendChild(seg);
    });
  }

  function submitBrowse(form) {
    var browseBtn = document.getElementById('file-browse-btn');
    if (browseBtn) { browseBtn.click(); } else { form.submit(); }
  }

  /* ── Shared request context ────────────────────────────── */
  var ctx = null;

  function context() {
    if (ctx) return ctx;
    var form = document.getElementById('file-form');
    if (!form) return null;
    var listing = document.getElementById('file-listing');
    var tokenInput = form.querySelector('input[name="_csrf_token"]');
    ctx = {
      form: form,
      action: form.getAttribute('action') || '',
      token: tokenInput ? tokenInput.value : '',
      currentDir: (listing && listing.getAttribute('data-current-path')) ||
        (document.getElementById('file-path') || {}).value || '/',
    };
    return ctx;
  }

  function collectCreds(body) {
    var map = { password: 'file-password', private_key: 'file-key', key_passphrase: 'file-passphrase' };
    Object.keys(map).forEach(function (field) {
      var el = document.getElementById(map[field]);
      if (el && el.value) body.append(field, el.value);
    });
    return body;
  }

  function opsURL(intent) {
    var c = context();
    return c.action + '/ops?intent=' + encodeURIComponent(intent) +
      '&_csrf_token=' + encodeURIComponent(c.token);
  }

  /* Send a urlencoded mutation; resolves with the parsed JSON or rejects with
     a human-readable message. */
  function postOp(intent, fields) {
    var body = new URLSearchParams();
    Object.keys(fields).forEach(function (k) {
      if (fields[k] !== undefined && fields[k] !== null) body.append(k, fields[k]);
    });
    collectCreds(body);
    return fetch(opsURL(intent), {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString(),
    }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (data) {
        if (!res.ok || !data.ok) {
          throw new Error((data && data.error) || T('js.files.request_failed', { status: res.status }));
        }
        return data;
      });
    });
  }

  /* ── Upload (XHR for progress) ─────────────────────────── */
  function uploadOne(file, onProgress) {
    return new Promise(function (resolve, reject) {
      var c = context();
      var body = new FormData();
      // Credentials and target dir MUST precede the file part: the server reads
      // leading fields, then streams the file straight to SFTP.
      body.append('path', c.currentDir);
      collectCreds(body);
      body.append('file', file, file.name);

      var xhr = new XMLHttpRequest();
      xhr.open('POST', opsURL('upload'));
      if (xhr.upload) {
        xhr.upload.onprogress = function (e) {
          if (e.lengthComputable && onProgress) onProgress(e.loaded / e.total);
        };
      }
      xhr.onload = function () {
        var data = {};
        try { data = JSON.parse(xhr.responseText); } catch (e) {}
        if (xhr.status >= 200 && xhr.status < 300 && data.ok) {
          resolve(data);
        } else {
          reject(new Error((data && data.error) || T('js.files.upload_failed', { status: xhr.status })));
        }
      };
      xhr.onerror = function () { reject(new Error(T('js.files.upload_network_error'))); };
      xhr.send(body);
    });
  }

  function uploadFiles(files) {
    var panel = document.getElementById('file-uploads');
    if (!panel) return;
    panel.hidden = false;
    panel.innerHTML = '';

    var rows = [];
    Array.prototype.forEach.call(files, function (file) {
      var row = document.createElement('div');
      row.className = 'file-upload';
      var name = document.createElement('span');
      name.className = 'file-upload__name';
      name.textContent = file.name;
      var bar = document.createElement('div');
      bar.className = 'file-upload__bar';
      var fill = document.createElement('div');
      fill.className = 'file-upload__fill';
      bar.appendChild(fill);
      var pct = document.createElement('span');
      pct.className = 'file-upload__pct';
      pct.textContent = '0%';
      row.appendChild(name);
      row.appendChild(bar);
      row.appendChild(pct);
      panel.appendChild(row);
      rows.push({ row: row, fill: fill, pct: pct });
    });

    var index = 0;
    var anyFailed = false;
    var anyOk = false;

    function next() {
      if (index >= files.length) {
        if (anyOk && !anyFailed) {
          setTimeout(function () { reloadCurrent(); }, 700);
        } else if (anyFailed) {
          toast(T('js.files.uploads_failed'), 'error');
        }
        return;
      }
      var file = files[index];
      var ui = rows[index];
      uploadOne(file, function (frac) {
        var p = Math.round(frac * 100);
        ui.fill.style.width = p + '%';
        ui.pct.textContent = p + '%';
      }).then(function () {
        anyOk = true;
        ui.row.classList.add('file-upload--ok');
        ui.fill.style.width = '100%';
        ui.pct.textContent = T('js.files.upload_done');
      }).catch(function (err) {
        anyFailed = true;
        ui.row.classList.add('file-upload--error');
        ui.pct.textContent = T('js.failed');
        ui.row.title = err.message;
      }).then(function () {
        index += 1;
        next();
      });
    }
    next();
  }

  function reloadCurrent() {
    var c = context();
    var pathInput = document.getElementById('file-path');
    if (pathInput) pathInput.value = c.currentDir;
    submitBrowse(c.form);
  }

  /* ── Row actions menu (kebab) ──────────────────────────── */
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
    btn.innerHTML = '<i data-lucide="' + icon + '"></i><span>' + label + '</span>';
    btn.addEventListener('click', function () {
      closeMenu();
      onClick();
    });
    return btn;
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
    var kind = row.getAttribute('data-entry-kind');
    var entryPath = row.getAttribute('data-entry-path');
    var entryName = row.getAttribute('data-entry-name');
    if (!entryPath) return;

    var m = ensureMenu();
    m.innerHTML = '';

    if (kind === 'directory') {
      m.appendChild(menuItem(T('common.open'), 'folder-open', false, function () {
        navigateTo(entryPath);
      }));
    } else {
      m.appendChild(menuItem(T('js.files.menu_download'), 'download', false, function () {
        triggerDownload(entryPath);
      }));
    }
    m.appendChild(menuItem(T('js.files.menu_rename'), 'pencil', false, function () {
      promptRename(entryName, entryPath);
    }));
    m.appendChild(menuItem(T('js.files.menu_copy_path'), 'clipboard', false, function () {
      copyPath(entryPath);
    }));
    m.appendChild(menuItem(T('common.delete'), 'trash-2', true, function () {
      confirmDelete(entryName, entryPath, kind === 'directory');
    }));

    m.hidden = false;
    renderIcons();
    positionMenu(m, trigger);
    trigger.setAttribute('aria-expanded', 'true');
    menuOpenFor = trigger;
  }

  function positionMenu(m, trigger) {
    var r = trigger.getBoundingClientRect();
    // Render off-screen first to measure.
    m.style.left = '0px';
    m.style.top = '0px';
    var mw = m.offsetWidth;
    var mh = m.offsetHeight;
    var gap = 6;
    var left = Math.min(r.right - mw, window.innerWidth - mw - 8);
    if (left < 8) left = 8;
    var top = r.bottom + gap;
    if (top + mh > window.innerHeight - 8) {
      top = r.top - mh - gap;          // flip above when it would overflow
      if (top < 8) top = window.innerHeight - mh - 8;
    }
    m.style.left = Math.round(left) + 'px';
    m.style.top = Math.round(top) + 'px';
  }

  /* ── Action implementations ────────────────────────────── */
  function navigateTo(dirPath) {
    var pathInput = document.getElementById('file-path');
    var crumbContainer = document.getElementById('file-breadcrumb');
    if (!pathInput) return;
    pathInput.value = dirPath;
    if (crumbContainer) buildBreadcrumb(dirPath, crumbContainer);
    submitBrowse(context().form);
  }

  function triggerDownload(filePath) {
    var c = context();
    // A throwaway form submitted programmatically: HTMLFormElement.submit()
    // dispatches no 'submit' event, so the global loading overlay / progress
    // bar never fire for an attachment response that doesn't navigate.
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
    add('_csrf_token', c.token);
    add('intent', 'download');
    add('path', filePath);
    var creds = { password: 'file-password', private_key: 'file-key', key_passphrase: 'file-passphrase' };
    Object.keys(creds).forEach(function (field) {
      var el = document.getElementById(creds[field]);
      if (el && el.value) add(field, el.value);
    });
    document.body.appendChild(f);
    f.submit();
    setTimeout(function () { f.remove(); }, 0);
  }

  function promptRename(name, oldPath) {
    var next = window.prompt(T('js.files.rename_prompt', { name: name }), name);
    if (next === null) return;
    next = next.trim();
    if (!next || next === name) return;
    postOp('rename', { path: oldPath, name: next })
      .then(function () { toast(T('js.files.renamed', { name: next }), 'success'); reloadCurrent(); })
      .catch(function (err) { toast(err.message, 'error'); });
  }

  function confirmDelete(name, target, isDir) {
    var msg = isDir
      ? T('js.files.confirm_delete_dir', { name: name })
      : T('js.files.confirm_delete_file', { name: name });
    if (!window.confirm(msg)) return;
    postOp('delete', { path: target, recursive: isDir ? 'true' : 'false' })
      .then(function () { toast(T('js.files.deleted', { name: name }), 'success'); reloadCurrent(); })
      .catch(function (err) { toast(err.message, 'error'); });
  }

  function promptMkdir() {
    var name = window.prompt(T('js.files.mkdir_prompt'), '');
    if (name === null) return;
    name = name.trim();
    if (!name) return;
    postOp('mkdir', { path: context().currentDir, name: name })
      .then(function () { toast(T('js.files.created', { name: name }), 'success'); reloadCurrent(); })
      .catch(function (err) { toast(err.message, 'error'); });
  }

  /* ── Filter + sort ─────────────────────────────────────── */
  var sortState = { key: 'name', dir: 'asc' };

  function entryRows() {
    var table = document.getElementById('file-table');
    if (!table) return [];
    return Array.prototype.filter.call(
      table.querySelectorAll('.file-row'),
      function (row) { return !row.classList.contains('file-row--parent'); }
    );
  }

  function applyFilter() {
    var input = document.getElementById('file-filter');
    var noMatch = document.getElementById('file-no-match');
    var query = input ? input.value.trim().toLowerCase() : '';
    var visible = 0;
    entryRows().forEach(function (row) {
      var name = (row.getAttribute('data-entry-name') || '').toLowerCase();
      var show = query === '' || name.indexOf(query) !== -1;
      row.style.display = show ? '' : 'none';
      if (show) visible += 1;
    });
    if (noMatch) noMatch.hidden = visible !== 0;
  }

  function applySort() {
    var table = document.getElementById('file-table');
    if (!table) return;
    var rows = entryRows();
    var dirMul = sortState.dir === 'desc' ? -1 : 1;

    rows.sort(function (a, b) {
      var aDir = a.getAttribute('data-entry-kind') === 'directory';
      var bDir = b.getAttribute('data-entry-kind') === 'directory';
      if (aDir !== bDir) return aDir ? -1 : 1; // directories always first

      var cmp = 0;
      if (sortState.key === 'size') {
        cmp = (parseInt(a.getAttribute('data-entry-size'), 10) || 0) -
              (parseInt(b.getAttribute('data-entry-size'), 10) || 0);
      } else if (sortState.key === 'date') {
        cmp = (parseInt(a.getAttribute('data-entry-mtime'), 10) || 0) -
              (parseInt(b.getAttribute('data-entry-mtime'), 10) || 0);
      } else {
        cmp = (a.getAttribute('data-entry-name') || '').localeCompare(
          b.getAttribute('data-entry-name') || '', undefined, { sensitivity: 'base', numeric: true });
      }
      return cmp * dirMul;
    });

    var noMatch = document.getElementById('file-no-match');
    rows.forEach(function (row) { table.insertBefore(row, noMatch); });
    syncSortIndicators();
  }

  function syncSortIndicators() {
    var select = document.getElementById('file-sort');
    if (select && select.value !== sortState.key) select.value = sortState.key;
    var dirBtn = document.getElementById('file-sort-dir');
    if (dirBtn) {
      dirBtn.setAttribute('data-dir', sortState.dir);
      dirBtn.title = sortState.dir === 'desc' ? T('js.files.sort_desc') : T('js.files.sort_asc');
      dirBtn.innerHTML = sortState.dir === 'desc'
        ? '<i data-lucide="arrow-down-wide-narrow"></i>'
        : '<i data-lucide="arrow-up-narrow-wide"></i>';
    }
    document.querySelectorAll('.file-th[data-sort-key]').forEach(function (th) {
      var active = th.getAttribute('data-sort-key') === sortState.key;
      th.classList.toggle('file-th--active', active);
      th.setAttribute('data-dir', active ? sortState.dir : '');
    });
    renderIcons();
  }

  function setSort(key) {
    if (sortState.key === key) {
      sortState.dir = sortState.dir === 'asc' ? 'desc' : 'asc';
    } else {
      sortState.key = key;
      sortState.dir = 'asc';
    }
    applySort();
  }

  /* ── Wiring ────────────────────────────────────────────── */
  function initBrowser() {
    var form = document.getElementById('file-form');
    var pathInput = document.getElementById('file-path');
    var crumbContainer = document.getElementById('file-breadcrumb');
    if (!form || !pathInput) return;

    if (crumbContainer) {
      buildBreadcrumb(pathInput.value, crumbContainer);
      crumbContainer.addEventListener('click', function (e) {
        var seg = e.target.closest('.breadcrumb__seg[data-crumb-path]');
        if (!seg) return;
        pathInput.value = seg.getAttribute('data-crumb-path');
        buildBreadcrumb(pathInput.value, crumbContainer);
        submitBrowse(form);
      });
      pathInput.addEventListener('input', function () {
        buildBreadcrumb(pathInput.value, crumbContainer);
      });
    }

    document.querySelectorAll('.file-row--dir[data-nav-path]').forEach(function (row) {
      row.addEventListener('click', function (e) {
        if (e.target.closest('[data-menu-trigger]')) return; // let the menu handle it
        pathInput.value = row.getAttribute('data-nav-path');
        if (crumbContainer) buildBreadcrumb(pathInput.value, crumbContainer);
        submitBrowse(form);
      });
    });
  }

  function initMenus() {
    var table = document.getElementById('file-table');
    if (!table) return;
    table.addEventListener('click', function (e) {
      var trigger = e.target.closest('[data-menu-trigger]');
      if (!trigger) return;
      e.stopPropagation();
      if (menuOpenFor === trigger) { closeMenu(); return; }
      closeMenu();
      openMenu(trigger);
    });

    document.addEventListener('click', function (e) {
      if (!menu || menu.hidden) return;
      if (e.target.closest('.file-menu') || e.target.closest('[data-menu-trigger]')) return;
      closeMenu();
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') closeMenu();
    });
    window.addEventListener('resize', closeMenu);
    window.addEventListener('scroll', closeMenu, true);
  }

  function initToolbar() {
    var mkdirBtn = document.getElementById('file-mkdir-btn');
    if (mkdirBtn) mkdirBtn.addEventListener('click', promptMkdir);

    var uploadBtn = document.getElementById('file-upload-btn');
    var uploadInput = document.getElementById('file-upload-input');
    if (uploadBtn && uploadInput) {
      uploadBtn.addEventListener('click', function () { uploadInput.click(); });
      uploadInput.addEventListener('change', function () {
        if (uploadInput.files && uploadInput.files.length) uploadFiles(uploadInput.files);
        uploadInput.value = '';
      });
    }

    var filterInput = document.getElementById('file-filter');
    if (filterInput) filterInput.addEventListener('input', applyFilter);

    var sortSelect = document.getElementById('file-sort');
    if (sortSelect) sortSelect.addEventListener('change', function () {
      sortState.key = sortSelect.value;
      applySort();
    });

    var dirBtn = document.getElementById('file-sort-dir');
    if (dirBtn) dirBtn.addEventListener('click', function () {
      sortState.dir = sortState.dir === 'asc' ? 'desc' : 'asc';
      applySort();
    });

    document.querySelectorAll('.file-th[data-sort-key]').forEach(function (th) {
      th.addEventListener('click', function () { setSort(th.getAttribute('data-sort-key')); });
    });

    if (document.getElementById('file-table')) {
      applySort();
      applyFilter();
    }
  }

  function initCredPersistence() {
    var form = document.getElementById('file-form');
    if (!form) return;
    var action = form.getAttribute('action') || '';
    var storageKey = 'nodexia_file_creds' + action.replace(/\//g, '_');
    var pwdInput = document.getElementById('file-password');
    var keyInput = document.getElementById('file-key');
    var phraseInput = document.getElementById('file-passphrase');
    if (!pwdInput && !keyInput) return;

    var saved = null;
    try { saved = JSON.parse(sessionStorage.getItem(storageKey)); } catch (e) {}
    if (saved) {
      if (pwdInput && saved.pwd) pwdInput.value = saved.pwd;
      if (keyInput && saved.key) keyInput.value = saved.key;
      if (phraseInput && saved.passphrase) phraseInput.value = saved.passphrase;
    }

    var persist = function () {
      try {
        sessionStorage.setItem(storageKey, JSON.stringify({
          pwd: pwdInput ? pwdInput.value : '',
          key: keyInput ? keyInput.value : '',
          passphrase: phraseInput ? phraseInput.value : ''
        }));
      } catch (e) {}
    };
    // Persist on submit (browse/download) and before any XHR mutation fires.
    form.addEventListener('submit', persist);
    [pwdInput, keyInput, phraseInput].forEach(function (el) {
      if (el) el.addEventListener('change', persist);
    });
  }

  function boot() {
    initBrowser();
    initMenus();
    initToolbar();
    initCredPersistence();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
