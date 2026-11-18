/* Nodexia terminal mobile/IME input controller.
 *
 * Isolates IME composition and virtual-keyboard input on xterm.js's helper
 * <textarea> in capture phase on `.xterm-helpers` (before xterm's built-in
 * CompositionHelper & _inputEvent run).
 *
 * Fixes:
 *  1. Word duplication on mobile keyboards (e.g. Gboard turning `/root/agy` + `-`
 *     into `/root/agy-agy` or `rebecca-` into `rebecca-rebecca`) caused by:
 *     - xterm.js leaving committed text inside `term.textarea.value`
 *     - xterm.js CompositionHelper double-finalizing when non-229 keydown
 *       precedes compositionend
 *     - Android IME re-composing/re-inserting the previous word on punctuation
 *  2. Exposes live composing text (`onCompositionChange`) and `cancelComposition()`
 *     so intelligent autocomplete works smoothly mid-word on mobile without
 *     dropping characters or corrupting the shell prompt.
 *
 * Hardware desktop keystrokes (`!composing && !e.isComposing && e.keyCode !== 229`)
 * pass through untouched to xterm.js.
 */
(function () {
  'use strict';

  var WORD_PUNCT_RE = /^[-./_:;, ]$/;

  function hardenTextarea(ta) {
    if (!ta) return;
    ta.setAttribute('autocomplete', 'off');
    ta.setAttribute('autocorrect', 'off');
    ta.setAttribute('autocapitalize', 'none');
    ta.setAttribute('spellcheck', 'false');
    ta.setAttribute('writingsuggestions', 'false');
    ta.setAttribute('enterkeyhint', 'enter');
    ta.setAttribute('data-gramm', 'false');
    ta.setAttribute('data-gramm_editor', 'false');
    ta.setAttribute('data-enable-grammarly', 'false');
    ta.setAttribute('inputmode', 'text');
  }

  function stripSingleStepDuplicate(prevText, nextText) {
    if (!prevText || !nextText) return nextText;
    // Detect an instantaneous IME jump from "word" or "word-" to "word-word",
    // OR from "/path/word" / "/path/word-" to "/path/word-word", when not
    // typed key-by-key.
    var base = prevText;
    var sep = '';
    var lastChar = prevText.charAt(prevText.length - 1);
    if (WORD_PUNCT_RE.test(lastChar) && prevText.length >= 3) {
      sep = lastChar;
      base = prevText.slice(0, -1);
    }

    function checkMatch(prefix, word) {
      if (!word || word.length < 2) return null;
      if (sep) {
        if (nextText === prefix + word + sep + word) {
          return prefix + word + sep;
        }
      } else {
        for (var i = 0; i < 6; i++) {
          var p = '-./_: '[i];
          if (nextText === prefix + word + p + word) {
            return prefix + word + p;
          }
        }
      }
      return null;
    }

    var fullHit = checkMatch('', base);
    if (fullHit !== null) return fullHit;

    var tailMatch = base.match(/^(.*?)([A-Za-z0-9_]{2,})$/);
    if (tailMatch && tailMatch[1]) {
      var tailHit = checkMatch(tailMatch[1], tailMatch[2]);
      if (tailHit !== null) return tailHit;
    }

    return nextText;
  }

  function attach(term, container, options) {
    options = options || {};
    var onCommit = typeof options.onCommit === 'function' ? options.onCommit : function () {};
    var onCompositionChange = typeof options.onCompositionChange === 'function' ? options.onCompositionChange : function () {};
    var onPaste = typeof options.onPaste === 'function' ? options.onPaste : null;

    var ta = term && term.textarea;
    if (!ta) {
      return {
        getComposingText: function () { return ''; },
        isComposing: function () { return false; },
        cancelComposition: function () {},
        resetBuffer: function () {},
        dispose: function () {}
      };
    }

    hardenTextarea(ta);

    var captureRoot = ta.parentElement || container || ta;
    var compView = container ? container.querySelector('.composition-view') : null;
    var screenEl = container ? container.querySelector('.xterm-screen') : null;

    var composing = false;
    var compText = '';
    var compUpdateCount = 0;
    var suppressCompUntil = 0;
    var suppressCompWord = '';
    var justEndedCompText = '';
    var justEndedCompAt = 0;
    var lastNonImeKeydownCode = 0;
    var lastNonImeKeydownAt = 0;
    var lastDeleteBeforeInputAt = 0;
    var lastLineBreakBeforeInputAt = 0;
    var pendingXtermData = '';
    var pendingXtermAt = 0;

    // Secondary IPC-race guard: remembers the word committed immediately before
    // a single punctuation char (like `-`) so a machine-emitted duplicate burst
    // in the next ~140ms is dropped cleanly.
    var recentWordBeforePunct = '';
    var recentWordAt = 0;
    var recentPunct = '';
    var recentPunctAt = 0;

    function syncCompView(text) {
      if (!compView) compView = container ? container.querySelector('.composition-view') : null;
      if (!screenEl) screenEl = container ? container.querySelector('.xterm-screen') : null;
      if (!compView) return;

      if (!text) {
        compView.classList.remove('active');
        compView.textContent = '';
        return;
      }

      var rect = screenEl ? screenEl.getBoundingClientRect() : null;
      var cols = (term && term.cols) || 80;
      var rows = (term && term.rows) || 24;
      var cellW = (rect && rect.width) ? (rect.width / cols) : 8;
      var cellH = (rect && rect.height) ? (rect.height / rows) : 16;
      var buf = term && term.buffer && term.buffer.active;
      var cx = buf ? Math.min(buf.cursorX || 0, Math.max(0, cols - 1)) : 0;
      var cy = buf ? Math.max(0, Math.min(buf.cursorY || 0, rows - 1)) : 0;
      var leftPx = Math.round(cx * cellW);
      var topPx = Math.round(cy * cellH);

      compView.style.left = leftPx + 'px';
      compView.style.top = topPx + 'px';
      compView.style.height = Math.round(cellH) + 'px';
      compView.style.lineHeight = Math.round(cellH) + 'px';
      if (term && term.options) {
        if (term.options.fontFamily) compView.style.fontFamily = term.options.fontFamily;
        if (term.options.fontSize) compView.style.fontSize = term.options.fontSize + 'px';
      }
      compView.textContent = text;
      compView.classList.add('active');

      var viewRect = compView.getBoundingClientRect();
      ta.style.left = leftPx + 'px';
      ta.style.top = topPx + 'px';
      ta.style.width = Math.max(Math.round(viewRect.width), Math.round(cellW), 1) + 'px';
      ta.style.height = Math.max(Math.round(viewRect.height), Math.round(cellH), 1) + 'px';
    }

    function clearTextarea() {
      if (ta.value !== '') {
        ta.value = '';
      }
    }

    function notifyComposition(text) {
      try {
        onCompositionChange(text || '');
      } catch (err) { /* ignore listener errors */ }
    }

    function emitCommitted(rawText, updateSteps) {
      if (!rawText) return;
      var text = String(rawText).replace(/\r\n/g, '\r').replace(/\n/g, '\r');
      if (!text) return;

      var now = Date.now();
      // Drop instantaneous Gboard duplicate-word echoes right after punctuation
      // (e.g. "agy" -> "-" -> instant ghost "agy" in < 140ms with <= 1 step).
      if (
        updateSteps <= 1 &&
        recentPunct &&
        (now - recentPunctAt) <= 140 &&
        recentWordBeforePunct.length >= 2 &&
        text === recentWordBeforePunct
      ) {
        recentWordBeforePunct = '';
        recentPunct = '';
        return;
      }

      if (
        WORD_PUNCT_RE.test(text) &&
        recentWordBeforePunct.length >= 2 &&
        (now - recentWordAt) <= 2500
      ) {
        recentPunct = text;
        recentPunctAt = now;
      } else {
        var wordMatch = text.match(/([A-Za-z0-9_]{2,})$/);
        if (wordMatch) {
          recentWordBeforePunct = wordMatch[1];
          recentWordAt = now;
          recentPunct = '';
        } else {
          recentWordBeforePunct = '';
          recentPunct = '';
        }
      }

      try {
        if (typeof term.scrollToBottom === 'function') {
          var b = term.buffer && term.buffer.active;
          if (b && b.baseY !== b.viewportY) {
            term.scrollToBottom();
          }
        }
      } catch (err) { /* ignore */ }

      onCommit(text);
    }

    function onKeyDown(e) {
      if (e.target !== ta) return;
      lastNonImeKeydownCode = 0;

      if (composing || e.isComposing || e.keyCode === 229) {
        // Prevent xterm.js CompositionHelper.keydown from double-finalizing or
        // starting _handleAnyTextareaChanges() while letting the browser IME run.
        e.stopPropagation();
        return;
      }

      pendingXtermData = '';
      lastNonImeKeydownCode = e.keyCode || 0;
      lastNonImeKeydownAt = Date.now();
    }

    function onCompositionStart(e) {
      if (e.target !== ta) return;
      e.stopPropagation();
      composing = true;
      compText = '';
      compUpdateCount = 0;
      syncCompView('');
      notifyComposition('');
    }

    function onCompositionUpdate(e) {
      if (e.target !== ta) return;
      e.stopPropagation();
      var raw = (e && typeof e.data === 'string') ? e.data : ta.value;
      if (raw) {
        if (compUpdateCount > 0 && raw !== compText) {
          raw = stripSingleStepDuplicate(compText, raw);
        } else if (compUpdateCount === 0 && recentWordBeforePunct.length >= 2 && (Date.now() - recentWordAt) <= 2500) {
          var seed = recentWordBeforePunct + (recentPunct || '');
          var strippedFirst = stripSingleStepDuplicate(seed, raw);
          if (strippedFirst !== raw) {
            raw = strippedFirst;
          }
        }
      }
      compText = raw || '';
      compUpdateCount += 1;
      syncCompView(compText);
      notifyComposition(compText);
    }

    function onCompositionEnd(e) {
      if (e.target !== ta) return;
      e.stopPropagation();
      var prev = compText;
      var steps = compUpdateCount;
      var raw = (e && typeof e.data === 'string') ? e.data : ta.value;

      composing = false;
      compText = '';
      compUpdateCount = 0;
      syncCompView('');
      notifyComposition('');
      clearTextarea();

      if (Date.now() < suppressCompUntil && (!suppressCompWord || raw === suppressCompWord)) {
        suppressCompUntil = 0;
        suppressCompWord = '';
        return;
      }

      if (raw) {
        if (prev) {
          raw = stripSingleStepDuplicate(prev, raw);
        } else if (steps <= 1 && recentWordBeforePunct.length >= 2 && (Date.now() - recentWordAt) <= 2500) {
          raw = stripSingleStepDuplicate(recentWordBeforePunct + (recentPunct || ''), raw);
        }
      }

      justEndedCompText = raw || '';
      justEndedCompAt = Date.now();

      if (raw) {
        emitCommitted(raw, steps);
      }
    }

    function onBeforeInput(e) {
      if (e.target !== ta) return;
      if (composing || e.isComposing) return;

      var now = Date.now();
      if (e.inputType === 'deleteContentBackward') {
        if (lastNonImeKeydownCode === 8 && (now - lastNonImeKeydownAt) < 50) {
          return;
        }
        e.preventDefault();
        e.stopPropagation();
        clearTextarea();
        lastDeleteBeforeInputAt = now;
        emitCommitted('\x7f', 1);
      } else if (e.inputType === 'deleteWordBackward') {
        e.preventDefault();
        e.stopPropagation();
        clearTextarea();
        lastDeleteBeforeInputAt = now;
        emitCommitted('\x17', 1);
      } else if (e.inputType === 'insertLineBreak' || e.inputType === 'insertParagraph') {
        if (lastNonImeKeydownCode === 13 && (now - lastNonImeKeydownAt) < 50) {
          return;
        }
        e.preventDefault();
        e.stopPropagation();
        clearTextarea();
        lastLineBreakBeforeInputAt = now;
        emitCommitted('\r', 1);
      }
    }

    function onInput(e) {
      if (e.target !== ta) return;
      e.stopPropagation();

      if (
        composing ||
        e.isComposing ||
        e.inputType === 'insertCompositionText' ||
        e.inputType === 'insertFromComposition'
      ) {
        return;
      }

      var now = Date.now();
      if (
        ta.value === '' &&
        justEndedCompText &&
        e.data === justEndedCompText &&
        (now - justEndedCompAt) < 80
      ) {
        justEndedCompText = '';
        return;
      }

      if (e.inputType === 'insertFromPaste') {
        var pasted = ta.value || (e && e.data) || '';
        clearTextarea();
        if (pasted) {
          if (onPaste) onPaste(pasted);
          else emitCommitted(pasted, 10);
        }
        return;
      }

      if (e.inputType === 'insertLineBreak' || e.inputType === 'insertParagraph') {
        clearTextarea();
        if (
          (now - lastLineBreakBeforeInputAt) >= 50 &&
          !(lastNonImeKeydownCode === 13 && (now - lastNonImeKeydownAt) < 50)
        ) {
          emitCommitted('\r', 1);
        }
        return;
      }

      if (e.inputType === 'deleteContentBackward' || e.inputType === 'deleteWordBackward') {
        clearTextarea();
        if (
          (now - lastDeleteBeforeInputAt) >= 50 &&
          !(lastNonImeKeydownCode === 8 && (now - lastNonImeKeydownAt) < 50)
        ) {
          emitCommitted(e.inputType === 'deleteWordBackward' ? '\x17' : '\x7f', 1);
        }
        return;
      }

      var val = ta.value;
      if (
        !val &&
        e &&
        typeof e.data === 'string' &&
        e.data &&
        !(justEndedCompText && e.data === justEndedCompText && (now - justEndedCompAt) < 120)
      ) {
        val = e.data;
      }
      clearTextarea();
      justEndedCompText = '';

      // Desktop Space, uppercase A-Z, AltGr, and resolved dead keys pass through
      // keydown to xterm's _keyPress (which emits term.onData without calling
      // preventDefault when cancelEvents=false), causing the browser to also fire
      // an input event immediately afterwards. Consume and skip that echo.
      if (val && pendingXtermData && val === pendingXtermData && (now - pendingXtermAt) < 80) {
        pendingXtermData = '';
        return;
      }

      if (val) {
        emitCommitted(val, val.length <= 1 ? 1 : 0);
      }
    }

    function onBlur() {
      composing = false;
      compText = '';
      compUpdateCount = 0;
      syncCompView('');
      notifyComposition('');
      clearTextarea();
    }

    captureRoot.addEventListener('keydown', onKeyDown, true);
    captureRoot.addEventListener('compositionstart', onCompositionStart, true);
    captureRoot.addEventListener('compositionupdate', onCompositionUpdate, true);
    captureRoot.addEventListener('compositionend', onCompositionEnd, true);
    captureRoot.addEventListener('beforeinput', onBeforeInput, true);
    captureRoot.addEventListener('input', onInput, true);
    ta.addEventListener('blur', onBlur);

    return {
      getComposingText: function () { return compText; },
      isComposing: function () { return composing; },
      notifyXtermData: function (data) {
        pendingXtermData = data || '';
        pendingXtermAt = Date.now();
      },
      cancelComposition: function () {
        if (composing || compText || ta.value) {
          suppressCompWord = compText || ta.value || '';
          suppressCompUntil = Date.now() + 200;
        }
        composing = false;
        compText = '';
        compUpdateCount = 0;
        syncCompView('');
        notifyComposition('');
        clearTextarea();
      },
      resetBuffer: function () {
        clearTextarea();
        recentWordBeforePunct = '';
        recentPunct = '';
      },
      dispose: function () {
        captureRoot.removeEventListener('keydown', onKeyDown, true);
        captureRoot.removeEventListener('compositionstart', onCompositionStart, true);
        captureRoot.removeEventListener('compositionupdate', onCompositionUpdate, true);
        captureRoot.removeEventListener('compositionend', onCompositionEnd, true);
        captureRoot.removeEventListener('beforeinput', onBeforeInput, true);
        captureRoot.removeEventListener('input', onInput, true);
        ta.removeEventListener('blur', onBlur);
      }
    };
  }

  window.NodexiaTermInput = {
    attach: attach,
    _stripSingleStepDuplicate: stripSingleStepDuplicate
  };
})();
