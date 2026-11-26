/* Nodexia shell layer (native ES module — no bundler, no npm).
 *
 * Persistent application frame: home fleet warnings, mobile navigation drawer,
 * collapsible server cards, desktop collapsible sidebar rail, and PWA hooks.
 * Live Log Drawer lives in lazy-loaded ./log-drawer.js.
 */
import { I18N, getCSRF, setCSRF, showToast, prefersReducedMotion, tabNavigate } from './core.js';
import { openModal, closeAllMenus, nxConfirm } from './modals.js';

/* ── Home fleet warnings: reveal undismissed, dismiss to localStorage ─────── */
var WARNING_DISMISS_TTL_MS = 7 * 24 * 60 * 60 * 1000;

function readWarningDismissals() {
  try {
    var raw = localStorage.getItem('nodexia_warn_dismissed');
    var map = raw ? JSON.parse(raw) : {};
    var now = Date.now();
    var changed = false;
    Object.keys(map).forEach(function (id) {
      if (typeof map[id] !== 'number' || now - map[id] > WARNING_DISMISS_TTL_MS) {
        delete map[id];
        changed = true;
      }
    });
    if (changed) localStorage.setItem('nodexia_warn_dismissed', JSON.stringify(map));
    return map;
  } catch (err) { return {}; }
}

function initHomeWarnings(root) {
  root = root || document;
  var wraps = root.querySelectorAll ? root.querySelectorAll('[data-home-warnings]') : [];
  if (!wraps.length) return;
  var dismissed = readWarningDismissals();

  wraps.forEach(function (wrap) {
    wrap.querySelectorAll('[data-warning-id]').forEach(function (banner) {
      if (banner.dataset.warnReady === '1') return;
      banner.dataset.warnReady = '1';
      var id = banner.getAttribute('data-warning-id');
      if (!dismissed[id]) banner.hidden = false;

      var close = banner.querySelector('[data-warning-dismiss]');
      if (!close) return;
      close.addEventListener('click', function () {
        try {
          var map = readWarningDismissals();
          map[id] = Date.now();
          localStorage.setItem('nodexia_warn_dismissed', JSON.stringify(map));
        } catch (err) {}
        banner.classList.add('is-dismissing');
        setTimeout(function () { banner.remove(); }, prefersReducedMotion ? 0 : 200);
      });
    });
  });
}

/* ── Mobile drawer (hamburger menu + swipe-to-close) ──────────────────────── */
function initDrawer() {
  var drawer = document.getElementById('mobile-drawer');
  var backdrop = document.querySelector('[data-drawer-backdrop]');
  if (!drawer || !backdrop) return;
  var openers = document.querySelectorAll('[data-drawer-open]');
  var lastFocus = null;

  function setExpanded(value) {
    openers.forEach(function (o) { o.setAttribute('aria-expanded', value ? 'true' : 'false'); });
  }
  function open() {
    lastFocus = document.activeElement;
    drawer.classList.add('is-open');
    backdrop.classList.add('is-open');
    drawer.setAttribute('aria-hidden', 'false');
    document.body.classList.add('drawer-open');
    setExpanded(true);
    var closeBtn = drawer.querySelector('[data-drawer-close]');
    if (closeBtn) closeBtn.focus();
  }
  function close() {
    if (!drawer.classList.contains('is-open')) return;
    drawer.classList.remove('is-open');
    backdrop.classList.remove('is-open');
    drawer.setAttribute('aria-hidden', 'true');
    document.body.classList.remove('drawer-open');
    drawer.style.transition = '';
    drawer.style.transform = '';
    backdrop.style.transition = '';
    backdrop.style.opacity = '';
    setExpanded(false);
    if (lastFocus && lastFocus.focus && document.contains(lastFocus)) lastFocus.focus();
    lastFocus = null;
  }

  openers.forEach(function (o) {
    o.addEventListener('click', function (e) { e.preventDefault(); open(); });
  });
  backdrop.addEventListener('click', close);
  drawer.querySelectorAll('[data-drawer-close], a[href]').forEach(function (el) {
    el.addEventListener('click', close);
  });
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape') close(); });

  drawer.addEventListener('keydown', function (e) {
    if (e.key !== 'Tab' || !drawer.classList.contains('is-open')) return;
    var focusables = drawer.querySelectorAll(
      'a[href], button:not([disabled]), input, select, textarea, [tabindex]:not([tabindex="-1"])');
    if (!focusables.length) return;
    var first = focusables[0];
    var last = focusables[focusables.length - 1];
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  });

  var resizeRAF = 0;
  window.addEventListener('resize', function () {
    if (resizeRAF) return;
    resizeRAF = requestAnimationFrame(function () {
      resizeRAF = 0;
      if (window.innerWidth >= 768) close();
    });
  });

  function isRTL() { return document.documentElement.getAttribute('dir') === 'rtl'; }
  var touchStartX = 0, touchStartY = 0, dragging = false, closingDx = 0, drawerWidth = 280;

  drawer.addEventListener('touchstart', function (e) {
    if (!drawer.classList.contains('is-open') || e.touches.length !== 1) return;
    touchStartX = e.touches[0].clientX;
    touchStartY = e.touches[0].clientY;
    drawerWidth = Math.max(drawer.offsetWidth || 280, 160);
    dragging = true;
    closingDx = 0;
  }, { passive: true });

  drawer.addEventListener('touchmove', function (e) {
    if (!dragging) return;
    var rtl = isRTL();
    var dx = e.touches[0].clientX - touchStartX;
    var dy = e.touches[0].clientY - touchStartY;
    if (Math.abs(dy) > Math.abs(dx) && closingDx === 0) { dragging = false; return; }
    var candidate = rtl ? -dx : dx;
    closingDx = candidate > 0 ? candidate : 0;
    var ratio = Math.max(0, Math.min(1, 1 - closingDx / drawerWidth));
    drawer.style.transition = 'none';
    drawer.style.transform = closingDx > 0
      ? 'translate3d(' + (rtl ? -closingDx : closingDx) + 'px, 0, 0)'
      : 'translate3d(0, 0, 0)';
    backdrop.style.transition = 'none';
    backdrop.style.opacity = String(ratio);
  }, { passive: true });

  function endTouch() {
    if (!dragging) return;
    dragging = false;
    drawer.style.transition = '';
    drawer.style.transform = '';
    backdrop.style.transition = '';
    backdrop.style.opacity = '';
    if (closingDx > 72) close();
    closingDx = 0;
  }
  drawer.addEventListener('touchend', endTouch);
  drawer.addEventListener('touchcancel', endTouch);
}

/* ── Mobile bottom nav: GPU sliding active pill & icon spring feedback ─────── */
function initBottomNav() {
  var nav = document.querySelector('.bottom-nav');
  if (!nav || nav.dataset.bottomNavReady === '1') return;
  nav.dataset.bottomNavReady = '1';

  var slider = document.createElement('span');
  slider.className = 'bottom-nav__slider';
  slider.setAttribute('aria-hidden', 'true');
  nav.insertBefore(slider, nav.firstChild);

  var activeTarget = null;
  var syncRAF = 0;

  function triggerBounce(item) {
    if (!item || prefersReducedMotion) return;
    item.classList.remove('is-bouncing');
    void item.offsetWidth;
    item.classList.add('is-bouncing');
    var clear = function () { item.classList.remove('is-bouncing'); };
    item.addEventListener('animationend', clear, { once: true });
    setTimeout(clear, 420);
  }

  function placeSlider(item, animate) {
    if (!item) return;
    var navRect = nav.getBoundingClientRect();
    var itemRect = item.getBoundingClientRect();
    if (!navRect.width || !itemRect.width) return;

    var x = Math.round(itemRect.left - navRect.left - (nav.clientLeft || 0));
    var w = Math.round(itemRect.width);
    if (!animate || prefersReducedMotion) {
      slider.classList.remove('is-animated');
    } else if (!slider.classList.contains('is-animated')) {
      slider.classList.add('is-animated');
    }

    nav.classList.add('has-slider');
    slider.style.width = w + 'px';
    slider.style.transform = 'translate3d(' + x + 'px, 0, 0)';
    slider.classList.add('is-ready');

    if (!animate && !prefersReducedMotion) {
      requestAnimationFrame(function () {
        requestAnimationFrame(function () {
          slider.classList.add('is-animated');
        });
      });
    }
  }

  function syncActive(animate, bounce) {
    var active = nav.querySelector('.bottom-nav__item.is-active');
    if (!active) return;
    var changed = activeTarget !== active;
    activeTarget = active;
    placeSlider(active, animate);
    if (bounce && changed) triggerBounce(active);
  }

  function scheduleSync(animate, bounce) {
    if (syncRAF) cancelAnimationFrame(syncRAF);
    syncRAF = requestAnimationFrame(function () {
      syncRAF = 0;
      syncActive(animate, bounce);
    });
  }

  scheduleSync(false, false);

  nav.addEventListener('click', function (e) {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var item = e.target && e.target.closest ? e.target.closest('.bottom-nav__item') : null;
    if (!item) return;
    var changed = activeTarget !== item;
    activeTarget = item;
    placeSlider(item, true);
    if (changed) triggerBounce(item);
  });

  if (typeof MutationObserver !== 'undefined') {
    var mo = new MutationObserver(function (records) {
      var sawItemChange = records.some(function (rec) {
        return rec.target && rec.target.classList && rec.target.classList.contains('bottom-nav__item');
      });
      if (!sawItemChange) return;
      var active = nav.querySelector('.bottom-nav__item.is-active');
      if (active && active !== activeTarget) {
        scheduleSync(true, true);
      }
    });
    mo.observe(nav, { subtree: true, attributes: true, attributeFilter: ['class'] });
  }

  if (typeof ResizeObserver !== 'undefined') {
    var ro = new ResizeObserver(function () {
      scheduleSync(false, false);
    });
    ro.observe(nav);
  }

  window.addEventListener('orientationchange', function () {
    setTimeout(function () { scheduleSync(false, false); }, 120);
  });

  window.addEventListener('pageshow', function () {
    scheduleSync(false, false);
  });
}

/* ── Collapsible server cards, instant search & optimistic delete ─────────── */
function initServerCards(root) {
  root = root || document;
  var cards = Array.prototype.slice.call(root.querySelectorAll('[data-server-card]'));
  if (!cards.length) return;

  var toggleAllBtn = root.querySelector('[data-server-toggle-all], #server-toggle-all');
  var toggleAllLabel = root.querySelector('[data-server-toggle-all-label], #server-toggle-all-label');

  function liveCards() {
    return Array.prototype.slice.call(root.querySelectorAll('[data-server-card]'));
  }

  function updateCard(card, open, persist) {
    var body = card.querySelector('[data-server-body]');
    var toggleBtn = card.querySelector('[data-server-toggle-btn]');
    var serverId = card.getAttribute('data-server-id');

    card.classList.toggle('server-card--expanded', open);
    if (body) body.hidden = !open;
    if (toggleBtn) toggleBtn.setAttribute('aria-expanded', open ? 'true' : 'false');
    if (persist && serverId) {
      try { localStorage.setItem('server_card_' + serverId, open ? 'open' : 'closed'); } catch (e) {}
    }
  }

  function positionActionMenu(menu, toggle) {
    var panel = menu.querySelector('.action-menu__panel');
    if (!panel || !toggle) return;
    var panelH = panel.offsetHeight || 260;
    var rect = toggle.getBoundingClientRect();
    var pane = menu.closest && menu.closest('.tab-pane');
    var bounds = pane ? pane.getBoundingClientRect() : { top: 0, bottom: window.innerHeight };
    var spaceAbove = rect.top - Math.max(bounds.top, 0);
    var spaceBelow = Math.min(bounds.bottom, window.innerHeight) - rect.bottom;
    menu.classList.toggle('action-menu--below', spaceBelow >= (panelH + 12) || spaceBelow >= spaceAbove);
  }

  function postDeleteServer(form, retried) {
    var csrf = getCSRF(form);
    var body = new URLSearchParams();
    if (csrf) body.set('_csrf_token', csrf);
    return fetch(form.getAttribute('action') || form.action, {
      method: 'POST',
      credentials: 'same-origin',
      headers: {
        'Accept': 'application/json',
        'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8',
        'X-CSRF-Token': csrf
      },
      body: body.toString()
    }).then(function (resp) {
      var hdr = resp.headers && resp.headers.get('X-CSRF-Token');
      if (hdr) setCSRF(hdr);
      if (resp.status === 403 && !retried && window.NodexiaTabs && typeof window.NodexiaTabs.refreshCSRFToken === 'function') {
        return window.NodexiaTabs.refreshCSRFToken().then(function () { return postDeleteServer(form, true); });
      }
      return resp.json().catch(function () { return { ok: resp.ok }; }).then(function (data) {
        if (!resp.ok || !data.ok) {
          throw new Error((data && data.message) || I18N.t('js.failed'));
        }
        return data;
      });
    });
  }

  cards.forEach(function (card) {
    if (card.dataset.serverCardReady === '1') return;
    card.dataset.serverCardReady = '1';

    var serverId = card.getAttribute('data-server-id');
    var stored = null;
    if (serverId) {
      try { stored = localStorage.getItem('server_card_' + serverId); } catch (e) {}
    }
    updateCard(card, stored === 'open', false);

    var checkboxWrap = card.querySelector('.server-card__checkbox-wrap');
    if (checkboxWrap) checkboxWrap.addEventListener('click', function (e) { e.stopPropagation(); });

    var menu = card.querySelector('[data-action-menu]');
    var menuToggle = menu && menu.querySelector('.action-menu__toggle');
    if (menu && menuToggle) {
      menuToggle.addEventListener('click', function () {
        if (!menu.classList.contains('is-open')) positionActionMenu(menu, menuToggle);
      }, true);
    }

    var deleteForm = card.querySelector('[data-server-delete-form]');
    if (deleteForm && deleteForm.dataset.deleteAjaxReady !== '1') {
      deleteForm.dataset.deleteAjaxReady = '1';
      deleteForm.addEventListener('submit', function (e) {
        e.preventDefault();
        e.stopPropagation();
        var btn = deleteForm.querySelector('button[type="submit"]');
        var msg = (btn && btn.getAttribute('data-confirm')) || I18N.t('js.confirm_irreversible');
        closeAllMenus(null);
        nxConfirm({ message: msg, danger: true }).then(function (ok) {
          if (!ok || !document.contains(card)) return;
          card.style.opacity = '0.45';
          card.style.pointerEvents = 'none';
          postDeleteServer(deleteForm, false).then(function (data) {
            card.remove();
            var regCount = root.querySelector('[data-servers-registered-count]');
            if (regCount) {
              var cur = parseInt(regCount.textContent, 10);
              if (!isNaN(cur) && cur > 0) regCount.textContent = String(cur - 1);
            }
            root.dispatchEvent(new CustomEvent('nx:servers-changed', { bubbles: false }));
            showToast(data.message || I18N.t('servers.flash.deleted'), 'ok');
            checkAllState();
          }).catch(function (err) {
            card.style.opacity = '';
            card.style.pointerEvents = '';
            showToast((err && err.message) || I18N.t('js.failed'), 'error');
          });
        });
      });
    }

    var header = card.querySelector('[data-server-toggle]');
    if (header) {
      header.addEventListener('click', function (e) {
        if (e.target.closest('a, button, input, select, textarea, .action-menu, .server-card__checkbox-wrap, .server-card__quick-actions')) return;
        updateCard(card, !card.classList.contains('server-card--expanded'), true);
        checkAllState();
      });
    }

    var toggleBtn = card.querySelector('[data-server-toggle-btn]');
    if (toggleBtn) {
      toggleBtn.addEventListener('click', function (e) {
        e.stopPropagation();
        updateCard(card, !card.classList.contains('server-card--expanded'), true);
        checkAllState();
      });
    }
  });

  function checkAllState() {
    if (!toggleAllBtn) return;
    var currentCards = liveCards();
    var allExpanded = currentCards.length > 0;
    currentCards.forEach(function (card) {
      if (!card.classList.contains('server-card--expanded')) allExpanded = false;
    });
    var label = allExpanded
      ? (toggleAllBtn.getAttribute('data-collapse-label') || 'Collapse all')
      : (toggleAllBtn.getAttribute('data-expand-label') || 'Expand all');
    if (toggleAllLabel) toggleAllLabel.textContent = label;
    toggleAllBtn.setAttribute('title', label);
    toggleAllBtn.setAttribute('aria-label', label);
  }

  if (toggleAllBtn && toggleAllBtn.dataset.toggleAllReady !== '1') {
    toggleAllBtn.dataset.toggleAllReady = '1';
    toggleAllBtn.addEventListener('click', function () {
      var currentCards = liveCards();
      var anyClosed = false;
      currentCards.forEach(function (card) { if (!card.classList.contains('server-card--expanded')) anyClosed = true; });
      currentCards.forEach(function (card) { updateCard(card, anyClosed, true); });
      checkAllState();
    });
    checkAllState();
  }

  /* Instant client-side filter when the entire list fits on one page */
  var pageSection = root.querySelector('[data-servers-page]');
  var searchInput = root.querySelector('[data-server-search-input]');
  var emptyFilterEl = root.querySelector('[data-server-filter-empty]');
  var totalPages = pageSection ? parseInt(pageSection.getAttribute('data-total-pages') || '1', 10) : 1;
  if (searchInput && totalPages <= 1 && searchInput.dataset.instantFilterReady !== '1') {
    searchInput.dataset.instantFilterReady = '1';
    var filterTimer = null;
    var applyInstantFilter = function () {
      var q = (searchInput.value || '').trim().toLowerCase();
      var visible = 0;
      liveCards().forEach(function (card) {
        var hay = (card.getAttribute('data-server-search-text') || card.textContent || '').toLowerCase();
        var match = !q || hay.indexOf(q) !== -1;
        card.hidden = !match;
        if (match) visible++;
      });
      if (emptyFilterEl) emptyFilterEl.hidden = !q || visible > 0;
    };
    searchInput.addEventListener('input', function () {
      clearTimeout(filterTimer);
      filterTimer = setTimeout(applyInstantFilter, 40);
    });
  }
}

/* ── PWA: service worker & portrait orientation lock ──────────────────────── */
function initServiceWorker() {
  if (!('serviceWorker' in navigator)) return;
  window.addEventListener('load', function () {
    navigator.serviceWorker.register('/sw.js', { scope: '/' }).then(function (reg) {
      if (reg && typeof reg.update === 'function') reg.update().catch(function () {});
    }).catch(function () {});
  });
}

function initOrientationLock() {
  try {
    var so = window.screen && window.screen.orientation;
    if (!so || typeof so.lock !== 'function') return;
    var p = so.lock('portrait');
    if (p && typeof p.catch === 'function') p.catch(function () {});
  } catch (err) {}
}

/* ── Desktop Collapsible Architectural Sidebar ────────────────────────────── */
function initDesktopSidebar() {
  var shell = document.getElementById('shell');
  var sidebar = document.getElementById('shell-sidebar');
  var toggle = document.querySelector('[data-sidebar-toggle]');
  if (!shell || !sidebar || !toggle || toggle.dataset.sidebarToggleReady === '1') return;
  toggle.dataset.sidebarToggleReady = '1';

  var STORAGE_KEY = 'nodexia.sidebar.collapsed';
  var DESKTOP_MIN_PX = 768;
  var resizeTimer = null;
  var resizeRAF = 0;

  var tooltip = document.createElement('div');
  tooltip.className = 'shell-rail-tooltip';
  tooltip.setAttribute('role', 'tooltip');
  tooltip.hidden = true;
  document.body.appendChild(tooltip);
  var activeTipTarget = null;

  function hideTooltip() {
    activeTipTarget = null;
    tooltip.classList.remove('is-visible');
    tooltip.hidden = true;
  }

  function showTooltip(target) {
    if (window.innerWidth < DESKTOP_MIN_PX || !shell.classList.contains('is-sidebar-collapsed')) {
      hideTooltip();
      return;
    }
    var text = target && target.getAttribute('data-rail-tooltip');
    if (!text) { hideTooltip(); return; }
    activeTipTarget = target;
    tooltip.textContent = text;
    tooltip.hidden = false;
    var rect = target.getBoundingClientRect();
    var sidebarRect = sidebar.getBoundingClientRect();
    var rtl = document.documentElement.getAttribute('dir') === 'rtl';
    tooltip.style.top = Math.round(rect.top + rect.height / 2) + 'px';
    if (rtl) {
      tooltip.style.left = 'auto';
      tooltip.style.right = Math.round(window.innerWidth - sidebarRect.left + 10) + 'px';
    } else {
      tooltip.style.right = 'auto';
      tooltip.style.left = Math.round(sidebarRect.right + 10) + 'px';
    }
    requestAnimationFrame(function () {
      if (activeTipTarget === target) tooltip.classList.add('is-visible');
    });
  }

  function readStored() {
    try { return localStorage.getItem(STORAGE_KEY) === '1'; } catch (err) { return false; }
  }

  function applyState(collapsed, persist, dispatchResize) {
    var effective = !!collapsed && window.innerWidth >= DESKTOP_MIN_PX;
    shell.classList.toggle('is-sidebar-collapsed', effective);
    toggle.setAttribute('aria-expanded', effective ? 'false' : 'true');
    var label = effective
      ? (I18N.t('shell.sidebar.expand') || 'Expand sidebar')
      : (I18N.t('shell.sidebar.collapse') || 'Collapse sidebar');
    if (label === 'shell.sidebar.expand') label = 'Expand sidebar';
    if (label === 'shell.sidebar.collapse') label = 'Collapse sidebar';
    toggle.setAttribute('aria-label', label);
    toggle.setAttribute('title', label + ' ([)');
    if (!effective) hideTooltip();
    if (persist) {
      try { localStorage.setItem(STORAGE_KEY, collapsed ? '1' : '0'); } catch (err) {}
    }
    if (dispatchResize) {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(function () {
        window.dispatchEvent(new Event('resize'));
      }, prefersReducedMotion ? 10 : 230);
    }
  }

  applyState(readStored(), false, false);
  requestAnimationFrame(function () {
    requestAnimationFrame(function () { shell.classList.add('is-animated'); });
  });

  toggle.addEventListener('click', function () {
    applyState(!shell.classList.contains('is-sidebar-collapsed'), true, true);
  });

  sidebar.addEventListener('mouseover', function (e) {
    var target = e.target && e.target.closest ? e.target.closest('[data-rail-tooltip]') : null;
    if (!target || !sidebar.contains(target)) return;
    if (target !== activeTipTarget) showTooltip(target);
  });
  sidebar.addEventListener('mouseout', function (e) {
    if (!activeTipTarget) return;
    if (!e.relatedTarget || !activeTipTarget.contains(e.relatedTarget)) hideTooltip();
  });
  sidebar.addEventListener('focusin', function (e) {
    var target = e.target && e.target.closest ? e.target.closest('[data-rail-tooltip]') : null;
    if (target) showTooltip(target);
  });
  sidebar.addEventListener('focusout', hideTooltip);
  sidebar.addEventListener('click', hideTooltip);

  var bodyEl = sidebar.querySelector('.shell-sidebar__body');
  if (bodyEl) bodyEl.addEventListener('scroll', hideTooltip, { passive: true });

  window.addEventListener('resize', function () {
    if (resizeRAF) return;
    resizeRAF = requestAnimationFrame(function () {
      resizeRAF = 0;
      hideTooltip();
      applyState(readStored(), false, false);
    });
  });

  document.addEventListener('keydown', function (e) {
    if (window.innerWidth < DESKTOP_MIN_PX) return;
    if (e.metaKey || e.ctrlKey || e.altKey || e.key !== '[') return;
    var el = document.activeElement;
    if (el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT' || el.isContentEditable)) return;
    if (el && el.closest && el.closest('.xterm, [data-modal].is-open')) return;
    if (document.body.classList.contains('drawer-open') || document.body.classList.contains('tab-switcher-open')) return;
    e.preventDefault();
    applyState(!shell.classList.contains('is-sidebar-collapsed'), true, true);
  });
}

/* ── Sidebar & Mobile Drawer "Add Server" trigger → in-page modal or /servers?add=1 */
function initAddServerTriggers() {
  document.addEventListener('click', function (e) {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var trigger = e.target && e.target.closest ? e.target.closest('[data-add-server-trigger]') : null;
    if (!trigger) return;
    e.preventDefault();

    var activePane = document.querySelector('.tab-pane:not([hidden])') || document;
    var modal = activePane.querySelector('[data-server-add-modal]');
    if (modal) {
      openModal(modal);
      return;
    }
    tabNavigate('/servers?add=1');
  });
}

export { initHomeWarnings, initDrawer, initBottomNav, initServerCards, initAddServerTriggers, initServiceWorker, initOrientationLock, initDesktopSidebar };
