/* Nodexia terminal keybindings.
 *
 * A single keyboard-shortcut handler that is wired into xterm.js via
 * term.attachCustomKeyEventHandler. The handler runs BEFORE xterm turns a
 * keystroke into PTY bytes, so it can intercept editor-style shortcuts
 * (copy/paste/search/font/clear) while letting every other key fall through to
 * the shell untouched.
 *
 * Returning `false` from the custom handler tells xterm to skip the key — but
 * xterm then does NOT call preventDefault, so the browser's own shortcut would
 * still fire (Cmd+F find bar, Ctrl+- page zoom, …). We therefore preventDefault
 * ourselves for every shortcut we own; see stop().
 *
 *   window.NodexiaTermKeybindings.attach(term, actions)
 *
 * `actions` is a bag of callbacks supplied by terminal.js:
 *   copySelection()  → boolean   // copy current selection; truthy if one existed
 *   paste()                      // read clipboard → PTY
 *   selectAll()                  // select entire buffer
 *   openSearch()                 // reveal the search bar
 *   clear()                      // xterm.clear()
 *   fontInc() / fontDec() / fontReset()
 *   reconnect()                  // re-establish the session
 *   scrollLines(n)               // scroll viewport by n lines (negative = up)
 *
 * Platform note: on macOS the primary modifier is Cmd (metaKey); elsewhere it is
 * Ctrl. Ctrl+Shift+… is offered on every platform (the Linux terminal
 * convention) so copy/paste work the same way regardless of OS.
 */
(function () {
  'use strict';

  function attach(term, actions) {
    if (!term || typeof term.attachCustomKeyEventHandler !== 'function') return;

    var isMac = /Mac|iPhone|iPad|iPod/.test(
      (navigator.platform || '') + ' ' + (navigator.userAgent || '')
    );

    // Mark a key as handled by us: stop the browser default AND tell xterm to
    // skip it (return false).
    function stop(ev) {
      ev.preventDefault();
      ev.stopPropagation();
      return false;
    }

    term.attachCustomKeyEventHandler(function (ev) {
      // Only act on key-down. xterm also forwards keypress; ignoring it here
      // prevents a shortcut from firing twice.
      if (ev.type !== 'keydown') return true;

      var key = (ev.key || '').toLowerCase();
      var code = ev.code || '';
      var ctrl = ev.ctrlKey, meta = ev.metaKey, shift = ev.shiftKey, alt = ev.altKey;
      var primary = isMac ? meta : ctrl;

      /* ── Font size + search: primary modifier, no shift/alt ─────────── */
      if (primary && !shift && !alt) {
        if (key === '=' || key === '+' || code === 'Equal' || code === 'NumpadAdd') {
          actions.fontInc(); return stop(ev);
        }
        if (key === '-' || key === '_' || code === 'Minus' || code === 'NumpadSubtract') {
          actions.fontDec(); return stop(ev);
        }
        if (key === '0' || code === 'Digit0' || code === 'Numpad0') {
          actions.fontReset(); return stop(ev);
        }
        if (key === 'f') { actions.openSearch(); return stop(ev); }
      }

      /* ── Clear: Cmd+K (macOS). Ctrl+K is intentionally left to the shell
         as readline kill-line — intercepting it would break a very common
         editing key, so "clear" is Cmd-only here. ───────────────────── */
      if (meta && !ctrl && !shift && !alt && key === 'k') {
        actions.clear(); return stop(ev);
      }

      /* ── Ctrl+Shift+… (cross-platform Linux convention) ─────────────── */
      if (ctrl && shift && !alt && !meta) {
        switch (key) {
          case 'c': actions.copySelection(); return stop(ev);
          case 'v': actions.paste();         return stop(ev);
          case 'a': actions.selectAll();     return stop(ev);
          case 't': actions.reconnect();     return stop(ev);
          case 'f': actions.openSearch();    return stop(ev);
          case 'arrowup':   actions.scrollLines(-5); return stop(ev);
          case 'arrowdown': actions.scrollLines(5);  return stop(ev);
        }
      }

      /* ── macOS Cmd+C / Cmd+V / Cmd+A ────────────────────────────────── */
      if (meta && !ctrl && !shift && !alt) {
        if (key === 'c') { if (actions.copySelection()) return stop(ev); }
        if (key === 'v') { actions.paste();     return stop(ev); }
        if (key === 'a') { actions.selectAll(); return stop(ev); }
      }

      /* ── Ctrl+C: copy when there is a selection, otherwise let xterm send
         SIGINT (^C) to the PTY. This is the single most important "feels
         native" behaviour on desktop. ──────────────────────────────── */
      if (ctrl && !meta && !shift && !alt && key === 'c') {
        if (actions.copySelection()) return stop(ev);
        return true; // nothing selected → ^C
      }

      // Everything else (Ctrl+D/Z/U/W/L, arrows, plain typing, …) falls through
      // to xterm, which encodes it for the PTY exactly as before.
      return true;
    });
  }

  window.NodexiaTermKeybindings = { attach: attach };
})();
