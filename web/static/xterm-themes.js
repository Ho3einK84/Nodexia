/* Nodexia terminal themes.
 *
 * A small, dependency-free catalog of xterm.js ITheme palettes plus a couple of
 * localStorage helpers. Loaded before terminal.js (script-src 'self' — no CDN),
 * it exposes everything under a single global so terminal.js stays decoupled
 * from the palette definitions and new themes can be added here in one place.
 *
 *   window.NodexiaTermThemes = {
 *     order:   [<id>, …],            // stable display order
 *     names:   { <id>: "Label" },    // human label per theme
 *     themes:  { <id>: ITheme },     // xterm theme objects
 *     get(id), load(), save(id)
 *   }
 *
 * The default ("nodexia") matches the panel's dark surface so the terminal does
 * not visually clash with the surrounding SSR chrome.
 */
(function () {
  'use strict';

  var THEME_KEY = 'nodexia.terminal.theme';
  var DEFAULT = 'nodexia';

  var themes = {
    nodexia: {
      background: '#0b1120', foreground: '#e2e8f0', cursor: '#60a5fa',
      cursorAccent: '#0b1120', selectionBackground: '#1e3a5f',
      black: '#1e293b', red: '#f87171', green: '#4ade80', yellow: '#facc15',
      blue: '#60a5fa', magenta: '#c084fc', cyan: '#22d3ee', white: '#e2e8f0',
      brightBlack: '#334155', brightRed: '#fca5a5', brightGreen: '#86efac',
      brightYellow: '#fde047', brightBlue: '#93c5fd', brightMagenta: '#d8b4fe',
      brightCyan: '#67e8f9', brightWhite: '#f8fafc',
    },
    'solarized-dark': {
      background: '#002b36', foreground: '#839496', cursor: '#93a1a1',
      cursorAccent: '#002b36', selectionBackground: '#073642',
      black: '#073642', red: '#dc322f', green: '#859900', yellow: '#b58900',
      blue: '#268bd2', magenta: '#d33682', cyan: '#2aa198', white: '#eee8d5',
      brightBlack: '#586e75', brightRed: '#cb4b16', brightGreen: '#586e75',
      brightYellow: '#657b83', brightBlue: '#839496', brightMagenta: '#6c71c4',
      brightCyan: '#93a1a1', brightWhite: '#fdf6e3',
    },
    'solarized-light': {
      background: '#fdf6e3', foreground: '#657b83', cursor: '#586e75',
      cursorAccent: '#fdf6e3', selectionBackground: '#eee8d5',
      black: '#073642', red: '#dc322f', green: '#859900', yellow: '#b58900',
      blue: '#268bd2', magenta: '#d33682', cyan: '#2aa198', white: '#eee8d5',
      brightBlack: '#002b36', brightRed: '#cb4b16', brightGreen: '#586e75',
      brightYellow: '#657b83', brightBlue: '#839496', brightMagenta: '#6c71c4',
      brightCyan: '#93a1a1', brightWhite: '#fdf6e3',
    },
    monokai: {
      background: '#272822', foreground: '#f8f8f2', cursor: '#f8f8f0',
      cursorAccent: '#272822', selectionBackground: '#49483e',
      black: '#272822', red: '#f92672', green: '#a6e22e', yellow: '#f4bf75',
      blue: '#66d9ef', magenta: '#ae81ff', cyan: '#a1efe4', white: '#f8f8f2',
      brightBlack: '#75715e', brightRed: '#f92672', brightGreen: '#a6e22e',
      brightYellow: '#f4bf75', brightBlue: '#66d9ef', brightMagenta: '#ae81ff',
      brightCyan: '#a1efe4', brightWhite: '#f9f8f5',
    },
    dracula: {
      background: '#282a36', foreground: '#f8f8f2', cursor: '#bd93f9',
      cursorAccent: '#282a36', selectionBackground: '#44475a',
      black: '#21222c', red: '#ff5555', green: '#50fa7b', yellow: '#f1fa8c',
      blue: '#bd93f9', magenta: '#ff79c6', cyan: '#8be9fd', white: '#f8f8f2',
      brightBlack: '#6272a4', brightRed: '#ff6e6e', brightGreen: '#69ff94',
      brightYellow: '#ffffa5', brightBlue: '#d6acff', brightMagenta: '#ff92df',
      brightCyan: '#a4ffff', brightWhite: '#ffffff',
    },
    'high-contrast': {
      background: '#000000', foreground: '#ffffff', cursor: '#ffff00',
      cursorAccent: '#000000', selectionBackground: '#0000aa',
      black: '#000000', red: '#ff0000', green: '#00ff00', yellow: '#ffff00',
      blue: '#3b78ff', magenta: '#ff00ff', cyan: '#00ffff', white: '#ffffff',
      brightBlack: '#808080', brightRed: '#ff5555', brightGreen: '#55ff55',
      brightYellow: '#ffff55', brightBlue: '#6da3ff', brightMagenta: '#ff55ff',
      brightCyan: '#55ffff', brightWhite: '#ffffff',
    },
  };

  // Display order and labels are kept here (not derived from object key order,
  // which is not guaranteed for non-integer keys across engines).
  var order = ['nodexia', 'solarized-dark', 'solarized-light', 'monokai', 'dracula', 'high-contrast'];
  var names = {
    'nodexia': 'Nodexia Dark',
    'solarized-dark': 'Solarized Dark',
    'solarized-light': 'Solarized Light',
    'monokai': 'Monokai',
    'dracula': 'Dracula',
    'high-contrast': 'High Contrast',
  };

  function get(id) {
    return themes[id] ? id : DEFAULT;
  }

  function load() {
    try {
      var stored = window.localStorage.getItem(THEME_KEY);
      if (stored && themes[stored]) return stored;
    } catch (e) { /* localStorage unavailable (private mode / disabled) */ }
    return DEFAULT;
  }

  function save(id) {
    if (!themes[id]) return;
    try { window.localStorage.setItem(THEME_KEY, id); } catch (e) { /* ignore */ }
  }

  window.NodexiaTermThemes = {
    DEFAULT: DEFAULT,
    order: order,
    names: names,
    themes: themes,
    get: get,
    load: load,
    save: save,
  };
})();
