/* Nodexia dialogs & overflow action menus (native ES module — no bundler, no npm).
 *
 * Shared modal dialogs (open/close, visibility-safe focus trap, Escape, scroll
 * lock, pane cleanup) published as window.nxModal for classic page scripts,
 * plus accessible card overflow action menus.
 */
import { I18N, el, renderIcons, prefersReducedMotion, registerCleanup } from './core.js';

/* ── Server / node card overflow action menus ─────────────────────────────── */
var actionMenuDocWired = false;
var allActionMenus = new Set();

function visibleMenuItems(menu) {
  return Array.prototype.slice.call(
    menu.querySelectorAll('[role="menuitem"]:not([disabled])')
  ).filter(function (el) {
    return !el.hidden && !el.closest('[hidden]');
  });
}

function isMobileMenuViewport() {
  return typeof window !== 'undefined' && window.innerWidth <= 767;
}

function positionMenuDirection(menu, toggle) {
  var panel = menu.querySelector('.action-menu__panel');
  if (!panel || !toggle) return;
  var panelH = panel.offsetHeight || 220;
  var rect = toggle.getBoundingClientRect();
  var pane = menu.closest('.tab-pane');
  var bounds = pane ? pane.getBoundingClientRect() : { top: 0, bottom: window.innerHeight };
  var spaceAbove = rect.top - Math.max(bounds.top, 0);
  var spaceBelow = Math.min(bounds.bottom, window.innerHeight) - rect.bottom;
  menu.classList.toggle('action-menu--below', spaceBelow >= (panelH + 12) || spaceBelow >= spaceAbove);
}

function setMenuOpen(menu, open) {
  var toggle = menu.querySelector('.action-menu__toggle');
  if (menu.classList.contains('node-card__menu')) {
    if (open && !isMobileMenuViewport()) positionMenuDirection(menu, toggle);
    else if (!open) menu.classList.add('action-menu--below');
  }
  menu.classList.toggle('is-open', open);
  if (toggle) toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
  var slot = menu.closest('.node-card-slot');
  if (slot) slot.classList.toggle('has-open-menu', open);
  var card = menu.closest('.server-card, .node-card');
  if (card) card.classList.toggle('has-open-menu', open);
  syncBodyScrollLock();
}

function closeAllMenus(except) {
  allActionMenus.forEach(function (menu) {
    if (!document.contains(menu)) { allActionMenus.delete(menu); return; }
    if (menu !== except && menu.classList.contains('is-open')) setMenuOpen(menu, false);
  });
}

function initActionMenus(root) {
  root = root || document;
  var menus = root.querySelectorAll('[data-action-menu]');
  if (!menus.length) return;

  menus.forEach(function (menu) {
    if (menu.dataset.actionMenuReady === '1') return;
    menu.dataset.actionMenuReady = '1';
    allActionMenus.add(menu);

    var toggle = menu.querySelector('.action-menu__toggle');
    if (toggle) {
      toggle.addEventListener('click', function (e) {
        e.preventDefault();
        e.stopPropagation();
        var willOpen = !menu.classList.contains('is-open');
        closeAllMenus(menu);
        setMenuOpen(menu, willOpen);
      });

      toggle.addEventListener('keydown', function (e) {
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
          e.preventDefault();
          closeAllMenus(menu);
          setMenuOpen(menu, true);
          var items = visibleMenuItems(menu);
          if (items.length) items[e.key === 'ArrowUp' ? items.length - 1 : 0].focus();
        }
      });
    }

    menu.addEventListener('click', function (e) {
      if (!menu.classList.contains('is-open')) return;
      var closeBtn = e.target.closest && e.target.closest('[data-action-menu-close]');
      if (closeBtn) {
        e.preventDefault();
        e.stopPropagation();
        setMenuOpen(menu, false);
        if (toggle) toggle.focus();
        return;
      }
      var panel = menu.querySelector('.action-menu__panel');
      if (panel && !panel.contains(e.target) && (!toggle || !toggle.contains(e.target))) {
        e.preventDefault();
        e.stopPropagation();
        setMenuOpen(menu, false);
        if (toggle) toggle.focus();
      }
    });

    menu.addEventListener('keydown', function (e) {
      if (!menu.classList.contains('is-open')) return;
      var items = visibleMenuItems(menu);
      if (!items.length) return;
      var idx = items.indexOf(document.activeElement);

      if (e.key === 'ArrowDown') { e.preventDefault(); items[(idx + 1) % items.length].focus(); }
      else if (e.key === 'ArrowUp') { e.preventDefault(); items[(idx - 1 + items.length) % items.length].focus(); }
      else if (e.key === 'Home') { e.preventDefault(); items[0].focus(); }
      else if (e.key === 'End') { e.preventDefault(); items[items.length - 1].focus(); }
      else if (e.key === 'Escape') { e.preventDefault(); setMenuOpen(menu, false); if (toggle) toggle.focus(); }
    });

    var owningPane = menu.closest('.tab-pane');
    if (owningPane) {
      registerCleanup(owningPane, function () {
        setMenuOpen(menu, false);
        allActionMenus.delete(menu);
      });
    }
  });

  if (actionMenuDocWired) return;
  actionMenuDocWired = true;
  document.addEventListener('click', function (e) {
    allActionMenus.forEach(function (menu) {
      if (!document.contains(menu)) { allActionMenus.delete(menu); return; }
      if (!menu.classList.contains('is-open')) return;
      var panel = menu.querySelector('.action-menu__panel');
      var toggle = menu.querySelector('.action-menu__toggle');
      var closeBtn = e.target.closest && e.target.closest('[data-action-menu-close]');
      if (closeBtn && menu.contains(closeBtn)) { setMenuOpen(menu, false); return; }
      if (toggle && (toggle === e.target || toggle.contains(e.target))) return;
      if (panel && panel.contains(e.target)) return;
      setMenuOpen(menu, false);
    });
  });
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape') closeAllMenus(null); });
  window.addEventListener('resize', function () { closeAllMenus(null); });
}

/* ── Modal dialogs (focus trap + scroll lock + Escape) ────────────────────── */
var modalDocWired = false;

function isElementVisible(el) {
  if (!el || el.hidden || el.closest('[hidden]')) return false;
  return !!(el.offsetParent !== null || el.getClientRects().length > 0);
}

function getFocusableElements(modal) {
  return Array.prototype.slice.call(
    modal.querySelectorAll(
      'a[href], button:not([disabled]), input:not([type="hidden"]):not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )
  ).filter(isElementVisible);
}

function syncBodyScrollLock() {
  var hasOpen = !!(
    document.querySelector('[data-modal].is-open, .modal.is-open') ||
    document.querySelector('.log-drawer.is-open') ||
    (isMobileMenuViewport() && document.querySelector('.node-card__menu.is-open'))
  );
  document.body.classList.toggle('modal-open', hasOpen);
}

function openModal(modal) {
  if (!modal) return;
  modal._nxLastFocus = document.activeElement;
  modal.hidden = false;
  if (prefersReducedMotion) {
    modal.classList.add('is-open');
  } else {
    requestAnimationFrame(function () {
      if (!modal.hidden) modal.classList.add('is-open');
    });
  }
  syncBodyScrollLock();

  var autofocus = modal.querySelector('[data-modal-autofocus]');
  var focusTarget = isElementVisible(autofocus)
    ? autofocus
    : (getFocusableElements(modal)[0] || null);
  if (focusTarget && focusTarget.focus) {
    try { focusTarget.focus({ preventScroll: true }); } catch (err) { focusTarget.focus(); }
  }
}

function closeModal(modal, force) {
  if (!modal || (!modal.classList.contains('is-open') && modal.hidden)) return false;
  if (!force) {
    var ok = modal.dispatchEvent(new CustomEvent('modal:beforeclose', { cancelable: true, bubbles: false }));
    if (!ok) return false;
  }

  modal.classList.remove('is-open');
  syncBodyScrollLock();

  var finish = function () { if (!modal.classList.contains('is-open')) modal.hidden = true; };
  if (prefersReducedMotion || force) finish();
  else setTimeout(finish, 220);

  var prev = modal._nxLastFocus;
  modal._nxLastFocus = null;
  if (prev && prev.focus && document.contains(prev)) {
    var prevPane = prev.closest ? prev.closest('.tab-pane') : null;
    if (!prevPane || !prevPane.hidden) {
      try { prev.focus({ preventScroll: true }); } catch (err) { prev.focus(); }
    }
  }
  return true;
}

window.nxModal = {
  open: openModal,
  close: closeModal,
  syncScrollLock: syncBodyScrollLock
};

/* ── Unified Dark Graphite Confirm Modal (window.nxConfirm) ───────────────── */
var confirmModalEl = null;
var confirmPendingResolve = null;

function resolveConfirm(answer) {
  var fn = confirmPendingResolve;
  confirmPendingResolve = null;
  if (confirmModalEl && (confirmModalEl.classList.contains('is-open') || !confirmModalEl.hidden)) {
    closeModal(confirmModalEl, true);
  }
  if (typeof fn === 'function') fn(!!answer);
}

function ensureConfirmModal() {
  if (confirmModalEl && document.body.contains(confirmModalEl)) return confirmModalEl;

  var backdrop = el('div', { class: 'modal__backdrop', 'data-confirm-cancel': '' });
  var titleIcon = el('i', { 'data-lucide': 'alert-triangle', 'aria-hidden': 'true' });
  var titleText = el('span', { 'data-confirm-title-text': '' });
  var titleEl = el('h3', { class: 'modal__title', id: 'nx-confirm-title', 'data-confirm-title': '' }, [titleIcon, titleText]);
  var closeBtn = el('button', {
    type: 'button',
    class: 'modal__close',
    'data-confirm-cancel': '',
    'aria-label': I18N.t('common.cancel')
  }, [el('i', { 'data-lucide': 'x', 'aria-hidden': 'true' })]);

  var msgEl = el('p', { class: 'modal__confirm-msg', id: 'nx-confirm-msg', 'data-confirm-msg': '' });
  var cancelBtn = el('button', {
    type: 'button',
    class: 'btn btn--secondary',
    'data-confirm-cancel': '',
    text: I18N.t('common.cancel')
  });
  var okBtn = el('button', {
    type: 'button',
    class: 'btn btn--primary',
    'data-confirm-ok': '',
    text: I18N.t('common.confirm')
  });

  var dialog = el('div', { class: 'modal__dialog modal__dialog--confirm' }, [
    el('div', { class: 'modal__header' }, [titleEl, closeBtn]),
    el('div', { class: 'modal__body' }, [msgEl]),
    el('div', { class: 'modal__footer' }, [cancelBtn, okBtn])
  ]);

  var modal = el('div', {
    class: 'modal modal--confirm',
    'data-modal': '',
    'data-confirm-modal': '',
    role: 'alertdialog',
    'aria-modal': 'true',
    'aria-labelledby': 'nx-confirm-title',
    'aria-describedby': 'nx-confirm-msg',
    hidden: true
  }, [backdrop, dialog]);

  modal.addEventListener('click', function (e) {
    if (e.target === modal || (e.target.closest && e.target.closest('[data-confirm-cancel]'))) {
      e.preventDefault();
      resolveConfirm(false);
    } else if (e.target.closest && e.target.closest('[data-confirm-ok]')) {
      e.preventDefault();
      resolveConfirm(true);
    }
  });

  modal.addEventListener('modal:beforeclose', function () {
    if (confirmPendingResolve) {
      var fn = confirmPendingResolve;
      confirmPendingResolve = null;
      fn(false);
    }
  });

  modal.addEventListener('keydown', function (e) {
    if (!modal.classList.contains('is-open')) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      resolveConfirm(false);
      return;
    }
    if (e.key === 'Tab') {
      var focusables = getFocusableElements(modal);
      if (!focusables.length) return;
      var first = focusables[0];
      var last = focusables[focusables.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    }
  });

  document.body.appendChild(modal);
  renderIcons(modal);
  confirmModalEl = modal;
  return modal;
}

function nxConfirm(opts) {
  if (typeof opts === 'string') opts = { message: opts };
  opts = opts || {};
  if (confirmPendingResolve) resolveConfirm(false);

  var modal = ensureConfirmModal();
  var isDanger = opts.danger !== false;
  var titleEl = modal.querySelector('[data-confirm-title]');
  var titleText = modal.querySelector('[data-confirm-title-text]');
  var msgEl = modal.querySelector('[data-confirm-msg]');
  var cancelBtn = modal.querySelector('.modal__footer [data-confirm-cancel]');
  var okBtn = modal.querySelector('[data-confirm-ok]');

  if (titleEl) titleEl.classList.toggle('modal__title--danger', isDanger);
  if (titleText) titleText.textContent = opts.title || I18N.t('common.confirm_title');
  if (msgEl) msgEl.textContent = opts.message || I18N.t('js.confirm_irreversible');
  if (cancelBtn) cancelBtn.textContent = opts.cancelLabel || I18N.t('common.cancel');
  if (okBtn) {
    okBtn.textContent = opts.confirmLabel || I18N.t('common.confirm');
    okBtn.className = 'btn ' + (isDanger ? 'btn--danger' : 'btn--primary');
  }

  return new Promise(function (resolve) {
    confirmPendingResolve = resolve;
    openModal(modal);
    var focusTarget = isDanger ? cancelBtn : okBtn;
    if (focusTarget && focusTarget.focus) {
      try { focusTarget.focus({ preventScroll: true }); } catch (err) { focusTarget.focus(); }
    }
  });
}
window.nxConfirm = nxConfirm;

function findModalForOpener(opener, root) {
  var key = opener.getAttribute('data-modal-open');
  if (!key) return null;
  var scope = opener.closest('[data-nodes-workspace], .tab-pane, section') || root || document;
  return (scope.querySelector && scope.querySelector('[data-modal-id="' + key + '"]')) ||
    (scope.querySelector && scope.querySelector('#' + key)) ||
    (root && root.querySelector ? root.querySelector('[data-modal-id="' + key + '"], #' + key) : null) ||
    document.getElementById(key);
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
      openModal(findModalForOpener(opener, root));
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

    modal.addEventListener('click', function (e) {
      if (e.target === modal || (e.target.hasAttribute && e.target.hasAttribute('data-modal-close'))) {
        e.preventDefault();
        closeModal(modal);
      }
    });

    modal.addEventListener('keydown', function (e) {
      if (e.key !== 'Tab' || !modal.classList.contains('is-open')) return;
      var focusables = getFocusableElements(modal);
      if (!focusables.length) return;
      var first = focusables[0];
      var last = focusables[focusables.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    });

    var owningPane = modal.closest('.tab-pane');
    if (owningPane) {
      registerCleanup(owningPane, function () {
        if (modal.classList.contains('is-open')) closeModal(modal, true);
      });
    }
  });

  if (!modalDocWired) {
    modalDocWired = true;
    document.addEventListener('keydown', function (e) {
      if (e.key !== 'Escape') return;
      var openModals = Array.prototype.slice.call(
        document.querySelectorAll('[data-modal].is-open')
      ).filter(function (m) {
        var pane = m.closest('.tab-pane');
        return !pane || !pane.hidden;
      });
      if (openModals.length) {
        e.stopPropagation();
        closeModal(openModals[openModals.length - 1]);
      }
    });
  }

  var initial = root.querySelector('[data-modal][data-modal-open-initial]');
  if (initial) openModal(initial);
}

export { openModal, closeModal, closeAllMenus, initActionMenus, initModals, syncBodyScrollLock, nxConfirm };
