// Minimal SGR parser for the output of the real plonk terminal UI.
// Handles reset, bold, the 16 standard colours and 256-colour foregrounds,
// which is everything the CLI emits. Other sequences are ignored.

const STANDARD = [
  '#000000', '#cd0000', '#00cd00', '#cdcd00', '#0000ee', '#cd00cd', '#00cdcd', '#e5e5e5',
  '#7f7f7f', '#ff0000', '#00ff00', '#ffff00', '#5c5cff', '#ff00ff', '#00ffff', '#ffffff',
];
const LEVELS = [0, 95, 135, 175, 215, 255];

const hex = (n) => n.toString(16).padStart(2, '0');

/** xterm 256-colour palette entry as #rrggbb. */
export function palette(n) {
  if (n < 16) return STANDARD[n];
  if (n < 232) {
    const i = n - 16;
    return '#' + [Math.floor(i / 36), Math.floor(i / 6) % 6, i % 6].map((l) => hex(LEVELS[l])).join('');
  }
  const gray = 8 + 10 * (n - 232);
  return '#' + hex(gray).repeat(3);
}

/**
 * Splits one terminal row into styled runs.
 * Each run is { col, text, fg, bold }; col counts code points from the left edge.
 * fg is null for the terminal's default foreground.
 */
export function parseRow(row) {
  const runs = [];
  let fg = null;
  let bold = false;
  let col = 0;
  const pattern = /\x1b\[([0-9;]*)m|\x1b\[[0-9;?]*[A-Za-z]|([^\x1b]+)/g;
  for (const match of row.matchAll(pattern)) {
    if (match[2] !== undefined) {
      const text = match[2];
      const last = runs.at(-1);
      if (last && last.fg === fg && last.bold === bold && last.col + [...last.text].length === col) {
        last.text += text;
      } else {
        runs.push({ col, text, fg, bold });
      }
      col += [...text].length;
    } else if (match[1] !== undefined) {
      const params = match[1] === '' ? [0] : match[1].split(';').map(Number);
      for (let i = 0; i < params.length; i++) {
        const p = params[i];
        if (p === 0) { fg = null; bold = false; }
        else if (p === 1) bold = true;
        else if (p === 22) bold = false;
        else if (p === 39) fg = null;
        else if (p >= 30 && p <= 37) fg = palette(p - 30);
        else if (p >= 90 && p <= 97) fg = palette(p - 90 + 8);
        else if (p === 38 && params[i + 1] === 5) { fg = palette(params[i + 2]); i += 2; }
      }
    }
  }
  return runs;
}

/** Display width of a row in terminal cells (all glyphs the CLI prints are one cell wide). */
export function rowWidth(row) {
  return parseRow(row).reduce((max, run) => Math.max(max, run.col + [...run.text].length), 0);
}
