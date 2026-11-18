/* Nodexia dialogs & overflow action menus (native ES module — no bundler, no npm).
 *
 * Shared frosted-backdrop modal dialogs (open/close, visibility-safe focus trap,
 * Escape, pane cleanup) and card overflow action menus (keyboard navigation,
 * pane-scoped cleanup, no inline z-index mutations).
 */
import { prefersReducedMotion, registerCleanup } from './core.js';

/* ── Server / node card overflow action menus ────────────── */
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
  return typeof window !== 'undefined' && window.innerWidth <= 640;
}

function positionMenuDirection(menu, toggle) {
  var panel = menu.querySelector('.action-menu__panel');
  if (!panel || !toggle) return;
  var panelH = panel.offsetHeight;
  if (!panelH) {
    panel.style.visibility = 'hidden';
    panel.style.display = 'flex';
    panelH = panel.offsetHeight || 220;
    panel.style.display = '';
    panel.style.visibility = '';
  }
  var rect = toggle.getBoundingClientRect();
  var pane = menu.closest('.tab-pane');
  var bounds = pane ? pane.getBoundingClientRect() : { top: 0, bottom: window.innerHeight };
  var topEdge = Math.max(bounds.top, 0);
  var bottomEdge = Math.min(bounds.bottom, window.innerHeight);
  var spaceAbove = rect.top - topEdge;
  var spaceBelow = bottomEdge - rect.bottom;
  var openBelow = spaceBelow >= (panelH + 12) || spaceBelow >= spaceAbove;
  menu.classList.toggle('action-menu--below', openBelow);
}

function setMenuOpen(menu, open) {
  var toggle = menu.querySelector('.action-menu__toggle');
  if (menu.classList.contains('node-card__menu')) {
    if (open && !isMobileMenuViewport()) {
      positionMenuDirection(menu, toggle);
    } else if (!open) {
      menu.classList.add('action-menu--below');
    }
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
    if (!document.contains(menu)) {
      allActionMenus.delete(menu);
      return;
    }
    if (menu !== except && menu.classList.contains('is-open')) {
      setMenuOpen(menu, false);
    }
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
          if (items.length) {
            items[e.key === 'ArrowUp' ? items.length - 1 : 0].focus();
          }
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

      if (e.key === 'ArrowDown') {
        e.preventDefault();
        items[(idx + 1) % items.length].focus();
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        items[(idx - 1 + items.length) % items.length].focus();
      } else if (e.key === 'Home') {
        e.preventDefault();
        items[0].focus();
      } else if (e.key === 'End') {
        e.preventDefault();
        items[items.length - 1].focus();
      } else if (e.key === 'Escape') {
        e.preventDefault();
        setMenuOpen(menu, false);
        if (toggle) toggle.focus();
      }
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
      if (!document.contains(menu)) {
        allActionMenus.delete(menu);
        return;
      }
      if (!menu.classList.contains('is-open')) return;
      var panel = menu.querySelector('.action-menu__panel');
      var toggle = menu.querySelector('.action-menu__toggle');
      var closeBtn = e.target.closest && e.target.closest('[data-action-menu-close]');
      if (closeBtn && menu.contains(closeBtn)) {
        setMenuOpen(menu, false);
        return;
      }
      if (toggle && (toggle === e.target || toggle.contains(e.target))) {
        return;
      }
      if (panel && panel.contains(e.target)) {
        return;
      }
      setMenuOpen(menu, false);
    });
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') closeAllMenus(null);
  });
  window.addEventListener('resize', function () { closeAllMenus(null); });
}

/* ── Modal dialogs (frosted backdrop + focus trap) ───────── */
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
    document.querySelector('[data-modal].is-open') ||
    document.querySelector('.log-drawer.is-open') ||
    (isMobileMenuViewport() && document.querySelector('.node-card__menu.is-open'))
  );
  document.body.classList.toggle('modal-open', hasOpen);
}

function openModal(modal) {
  if (!modal) return;
  modal._nxLastFocus = document.activeElement;
  modal.hidden = false;
  // Force reflow so entrance transition runs cleanly from hidden state.
  void modal.offsetWidth;
  modal.classList.add('is-open');
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
  if (!modal || !modal.classList.contains('is-open')) return false;
  if (!force) {
    var ok = modal.dispatchEvent(new CustomEvent('modal:beforeclose', {
      cancelable: true,
      bubbles: false
    }));
    if (!ok) return false;
  }

  modal.classList.remove('is-open');
  syncBodyScrollLock();

  var finish = function () {
    if (!modal.classList.contains('is-open')) modal.hidden = true;
  };
  if (prefersReducedMotion) {
    finish();
  } else {
    setTimeout(finish, 240);
  }

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
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
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

export { openModal, closeModal, closeAllMenus, initActionMenus, initModals, syncBodyScrollLock };
