/* Nodexia Persian (Farsi/Arabic) terminal reshaper and bidi helper.
 *
 * Provides contextual cursive shaping and bidirectional reordering so Persian
 * characters render properly in xterm.js without breaking ANSI escape codes,
 * terminal control sequences, or LTR English text / IP addresses / digits.
 */
(function () {
  'use strict';

  // Unicode Presentation Forms for Arabic/Persian characters
  // Format: [isolated, final, initial, medial]
  var CHAR_MAP = {
    0x0621: ['\uFE80', null, null, null], // Hamza
    0x0622: ['\uFE81', '\uFE82', null, null], // Alef with madda
    0x0623: ['\uFE83', '\uFE84', null, null], // Alef with hamza above
    0x0624: ['\uFE85', '\uFE86', null, null], // Waw with hamza above
    0x0625: ['\uFE87', '\uFE88', null, null], // Alef with hamza below
    0x0626: ['\uFE89', '\uFE8A', '\uFE8B', '\uFE8C'], // Yeh with hamza above
    0x0627: ['\uFE8D', '\uFE8E', null, null], // Alef
    0x0628: ['\uFE8F', '\uFE90', '\uFE91', '\uFE92'], // Beh
    0x0629: ['\uFE93', '\uFE94', null, null], // Teh marbuta
    0x062A: ['\uFE95', '\uFE96', '\uFE97', '\uFE98'], // Teh
    0x062B: ['\uFE99', '\uFE9A', '\uFE9B', '\uFE9C'], // Theh
    0x062C: ['\uFE9D', '\uFE9E', '\uFE9F', '\uFEA0'], // Jeem
    0x062D: ['\uFEA1', '\uFEA2', '\uFEA3', '\uFEA4'], // Hah
    0x062E: ['\uFEA5', '\uFEA6', '\uFEA7', '\uFEA8'], // Khah
    0x062F: ['\uFEA9', '\uFEAA', null, null], // Dal
    0x0630: ['\uFEAB', '\uFEAC', null, null], // Thal
    0x0631: ['\uFEAD', '\uFEAE', null, null], // Reh
    0x0632: ['\uFEAF', '\uFEB0', null, null], // Zain
    0x0633: ['\uFEB1', '\uFEB2', '\uFEB3', '\uFEB4'], // Seen
    0x0634: ['\uFEB5', '\uFEB6', '\uFEB7', '\uFEB8'], // Sheen
    0x0635: ['\uFEB9', '\uFEBA', '\uFEBB', '\uFEBC'], // Sad
    0x0636: ['\uFEBD', '\uFEBE', '\uFEBF', '\uFEC0'], // Dad
    0x0637: ['\uFEC1', '\uFEC2', '\uFEC3', '\uFEC4'], // Tah
    0x0638: ['\uFEC5', '\uFEC6', '\uFEC7', '\uFEC8'], // Zah
    0x0639: ['\uFEC9', '\uFECA', '\uFECB', '\uFECC'], // Ain
    0x063A: ['\uFECD', '\uFECE', '\uFECF', '\uFED0'], // Ghain
    0x0641: ['\uFED1', '\uFED2', '\uFED3', '\uFED4'], // Feh
    0x0642: ['\uFED5', '\uFED6', '\uFED7', '\uFED8'], // Qaf
    0x0643: ['\uFED9', '\uFEDA', '\uFEDB', '\uFEDC'], // Arabic Kaf
    0x0644: ['\uFEDD', '\uFEDE', '\uFEDF', '\uFEE0'], // Lam
    0x0645: ['\uFEE1', '\uFEE2', '\uFEE3', '\uFEE4'], // Meem
    0x0646: ['\uFEE5', '\uFEE6', '\uFEE7', '\uFEE8'], // Noon
    0x0647: ['\uFEE9', '\uFEEA', '\uFEEB', '\uFEEC'], // Heh
    0x0648: ['\uFEED', '\uFEEE', null, null], // Waw
    0x0649: ['\uFEEF', '\uFEF0', null, null], // Alef maksura
    0x064A: ['\uFEF1', '\uFEF2', '\uFEF3', '\uFEF4'], // Arabic Yeh
    // Persian specific characters
    0x067E: ['\uFB56', '\uFB57', '\uFB58', '\uFB59'], // Peh (پ)
    0x0686: ['\uFB7A', '\uFB7B', '\uFB7C', '\uFB7D'], // Tcheh (چ)
    0x0698: ['\uFB8A', '\uFB8B', null, null], // Jeh (ژ)
    0x06A9: ['\uFB8E', '\uFB8F', '\uFB90', '\uFB91'], // Persian Keheh (ک)
    0x06AF: ['\uFB92', '\uFB93', '\uFB94', '\uFB95'], // Gaf (گ)
    0x06CC: ['\uFBFC', '\uFBFD', '\uFBFE', '\uFBFF'], // Persian Yeh (ی)
    0x06C0: ['\uFBA4', '\uFBA5', null, null]  // Heh with yeh above (ۀ)
  };

  // Lam-Alef ligatures: map alef char code -> [isolated, final]
  var LAM_ALEF = {
    0x0622: ['\uFEF5', '\uFEF6'], // Lam + Alef with madda
    0x0623: ['\uFEF7', '\uFEF8'], // Lam + Alef with hamza above
    0x0625: ['\uFEF9', '\uFEFA'], // Lam + Alef with hamza below
    0x0627: ['\uFEFB', '\uFEFC']  // Lam + Alef plain
  };

  var PERSIAN_REGEX = /[\u0600-\u06FF\uFB50-\uFDFF\uFE70-\uFEFF]/;
  var ANSI_SPLIT_REGEX = /(\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[PX^_][^\x1b]*\x1b\\|\x1b.)/g;

  function hasPersian(str) {
    return PERSIAN_REGEX.test(str);
  }

  function isTransparent(code) {
    // Tashkeel / diacritics / tatweel / ZWJ / ZWNJ
    return (code >= 0x064B && code <= 0x065F) || code === 0x0670 || code === 0x0640;
  }

  function connectsLeft(code) {
    var entry = CHAR_MAP[code];
    return entry ? Boolean(entry[2]) : (code === 0x200D); // ZWJ forces connection
  }

  function connectsRight(code) {
    var entry = CHAR_MAP[code];
    return entry ? Boolean(entry[1]) : (code === 0x200D);
  }

  /* ── Shape Arabic/Persian text run ────────────────────────── */
  function shapeArabic(text) {
    if (!text || text.length <= 1) return text || '';
    var len = text.length;
    var out = [];

    for (var i = 0; i < len; i++) {
      var code = text.charCodeAt(i);

      // Handle ZWNJ (\u200C)
      if (code === 0x200C) {
        continue; // ZWNJ prevents connection, handled via neighbor lookup
      }

      // Special Lam-Alef ligature
      if (code === 0x0644 && i + 1 < len) {
        var nextCode = text.charCodeAt(i + 1);
        if (LAM_ALEF[nextCode]) {
          var prevConn = false;
          for (var p = i - 1; p >= 0; p--) {
            var pc = text.charCodeAt(p);
            if (isTransparent(pc)) continue;
            if (pc === 0x200C) break;
            prevConn = connectsLeft(pc);
            break;
          }
          out.push(prevConn ? LAM_ALEF[nextCode][1] : LAM_ALEF[nextCode][0]);
          i++; // skip next alef
          continue;
        }
      }

      var map = CHAR_MAP[code];
      if (!map) {
        out.push(text.charAt(i));
        continue;
      }

      // Check left and right connectivity
      var prevConnects = false;
      for (var p = i - 1; p >= 0; p--) {
        var pc = text.charCodeAt(p);
        if (isTransparent(pc)) continue;
        if (pc === 0x200C) break;
        prevConnects = connectsLeft(pc);
        break;
      }

      var nextConnects = false;
      for (var n = i + 1; n < len; n++) {
        var nc = text.charCodeAt(n);
        if (isTransparent(nc)) continue;
        if (nc === 0x200C) break;
        nextConnects = connectsRight(nc);
        break;
      }

      var shaped;
      if (prevConnects && nextConnects && map[3]) {
        shaped = map[3]; // Medial
      } else if (prevConnects && map[1]) {
        shaped = map[1]; // Final
      } else if (nextConnects && map[2]) {
        shaped = map[2]; // Initial
      } else {
        shaped = map[0]; // Isolated
      }

      out.push(shaped || text.charAt(i));
    }

    return out.join('');
  }

  /* ── Bidirectional reordering for LTR terminal display ──────── */
  function bidiReorder(text) {
    if (!text || text.length <= 1 || !hasPersian(text)) return text || '';

    var tokens = [];
    var i = 0;
    var len = text.length;

    while (i < len) {
      var ch = text.charAt(i);
      var code = text.charCodeAt(i);

      if (PERSIAN_REGEX.test(ch) || code === 0x200C || code === 0x200D) {
        var rtlBuf = '';
        while (i < len) {
          var c = text.charAt(i);
          var cc = text.charCodeAt(i);
          if (PERSIAN_REGEX.test(c) || cc === 0x200C || cc === 0x200D) {
            rtlBuf += c;
            i++;
          } else {
            break;
          }
        }
        tokens.push({ type: 'rtl', text: rtlBuf });
      } else if (/[0-9]/.test(ch)) {
        var numBuf = '';
        while (i < len && /[0-9\.:,\/\-]/.test(text.charAt(i))) {
          numBuf += text.charAt(i);
          i++;
        }
        tokens.push({ type: 'num', text: numBuf });
      } else if (/[\s\(\)\[\]\{\}<>،؛؟]/.test(ch)) {
        var sepBuf = '';
        while (i < len && /[\s\(\)\[\]\{\}<>،؛؟]/.test(text.charAt(i))) {
          sepBuf += text.charAt(i);
          i++;
        }
        tokens.push({ type: 'sep', text: sepBuf });
      } else {
        var ltrBuf = '';
        while (i < len) {
          var lc = text.charAt(i);
          var lcode = text.charCodeAt(i);
          if (PERSIAN_REGEX.test(lc) || lcode === 0x200C || lcode === 0x200D || /[0-9]/.test(lc)) {
            break;
          }
          ltrBuf += lc;
          i++;
        }
        tokens.push({ type: 'ltr', text: ltrBuf });
      }
    }

    var BRACKET_MAP = { '(': ')', ')': '(', '[': ']', ']': '[', '{': '}', '}': '{', '<': '>', '>': '<' };

    var result = [];
    for (var k = 0; k < tokens.length; k++) {
      var tok = tokens[k];
      if (tok.type === 'rtl') {
        var shaped = shapeArabic(tok.text);
        var reversed = shaped.split('').reverse().join('');
        result.push(reversed);
      } else if (tok.type === 'sep') {
        var mirrored = tok.text.split('').map(function (c) {
          return BRACKET_MAP[c] || c;
        }).reverse().join('');
        result.push(mirrored);
      } else {
        result.push(tok.text);
      }
    }

    return result.join('');
  }

  function reshapeTerminalOutput(chunk) {
    if (!chunk || chunk.length <= 1 || !hasPersian(chunk)) return chunk || '';

    var parts = chunk.split(ANSI_SPLIT_REGEX);
    for (var p = 0; p < parts.length; p++) {
      var part = parts[p];
      if (!part || part.charAt(0) === '\x1b') continue;

      if (hasPersian(part)) {
        var lines = part.split('\n');
        for (var l = 0; l < lines.length; l++) {
          if (hasPersian(lines[l])) {
            lines[l] = bidiReorder(lines[l]);
          }
        }
        parts[p] = lines.join('\n');
      }
    }

    return parts.join('');
  }

  window.NodexiaPersian = {
    hasPersian: hasPersian,
    shape: shapeArabic,
    bidi: bidiReorder,
    reshapeOutput: reshapeTerminalOutput
  };
})();
