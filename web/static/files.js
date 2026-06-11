(function () {
  'use strict';

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
      // Use --current for the last segment to match the CSS modifier.
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

  function initFileBrowser() {
    var form = document.getElementById('file-form');
    var pathInput = document.getElementById('file-path');
    var downloadBtn = document.getElementById('file-download-btn');
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
      row.addEventListener('click', function () {
        pathInput.value = row.getAttribute('data-nav-path');
        if (crumbContainer) buildBreadcrumb(pathInput.value, crumbContainer);
        submitBrowse(form);
      });
    });

    if (downloadBtn) {
      document.querySelectorAll('.file-row__download[data-download-path]').forEach(function (btn) {
        btn.addEventListener('click', function (e) {
          e.stopPropagation();
          pathInput.value = btn.getAttribute('data-download-path');
          downloadBtn.click();
        });
      });
    }
  }

  function initFileCredPersistence() {
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

    form.addEventListener('submit', function () {
      try {
        sessionStorage.setItem(storageKey, JSON.stringify({
          pwd: pwdInput ? pwdInput.value : '',
          key: keyInput ? keyInput.value : '',
          passphrase: phraseInput ? phraseInput.value : ''
        }));
      } catch (e) {}
    });
  }

  function boot() {
    initFileBrowser();
    initFileCredPersistence();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
