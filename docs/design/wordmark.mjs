// The Plonkr wordmark, drawn on a pixel grid.
//
// Units are pixels at the 20 px lockup size: x-height 11, ascender 15, descender 4, stems 3 wide, so every
// vertical edge falls on a whole pixel at 1x (and at 2x, 3x ... by integer scale). Letters are filled outlines made
// of rectangles, circular arcs and a few straight diagonals; there is no font, so it renders the same everywhere.
// The o is the pebble: a 10 px disc resting on the baseline.
//
// Baseline is y = 0, up is negative. Each glyph's ink starts at its own x = 0; `layout` places them.

const rect = (x, y, w, h) => `M${x} ${y}H${x + w}V${y + h}H${x}Z`;
const circle = (cx, cy, r, hole = false) => {
  // Two half-arcs. A hole is wound the other way so non-zero fill leaves it empty.
  const s = hole ? 0 : 1;
  return `M${cx - r} ${cy}A${r} ${r} 0 1 ${s} ${cx + r} ${cy}A${r} ${r} 0 1 ${s} ${cx - r} ${cy}Z`;
};

/** Glyph paths, ink box starting at x = 0. `w` is the ink width. */
export const glyphs = {
  p: { w: 11, d: rect(0, -11, 3, 15) + circle(5.5, -5.5, 5.5) + circle(5.5, -5.5, 2.5, true) },
  l: { w: 3, d: rect(0, -15, 3, 15) },
  o: { w: 10, d: circle(5, -5, 5), pebble: true },
  n: { w: 10, d: rect(0, -11, 3, 11) + 'M0 -6A5 5 0 0 1 10 -6V0H7V-6A2 2 0 0 0 3 -6Z' },
  // One outline, clockwise: stem, upper arm (45 degrees), then the leg. The notch opens to the right at (5.26, -6.02).
  k: { w: 10.5, d: 'M0 -15H3V-8L6 -11H10.24L5.26 -6.02L10.5 0H6.3L3 -3.8V0H0Z' },
  r: { w: 8, d: rect(0, -11, 3, 11) + 'M0 -6A5 5 0 0 1 5 -11H8V-8H5A2 2 0 0 0 3 -6Z' },
};

/** Advance gaps between ink boxes, in pixels. */
export const gaps = { 'p-l': 3, 'l-o': 3, 'o-n': 3, 'n-k': 4, 'k-r': 1.5 };
const order = ['p', 'l', 'o', 'n', 'k', 'r'];

/** Positions of each glyph's ink origin. */
export function layout() {
  const out = []; let x = 0;
  order.forEach((ch, i) => {
    out.push({ ch, x });
    x += glyphs[ch].w + (gaps[`${ch}-${order[i + 1]}`] ?? 0);
  });
  return { items: out, width: x };
}

export const WORDMARK = { ...layout(), ascender: 15, descender: 4, xHeight: 11, stem: 3, pebble: 10 };

/** SVG path data for one glyph at ink origin (dx, dy), scaled by s. Only absolute commands. */
export function glyphPath(ch, dx = 0, dy = 0, s = 1) {
  const f = n => +n.toFixed(3);
  const X = v => f((v + dx) * s), Y = v => f((v + dy) * s);
  return glyphs[ch].d.replace(/([MLHVAZ])([^MLHVAZ]*)/g, (_, cmd, args) => {
    if (cmd === "Z") return "Z";
    const n = args.trim().split(/[\s,]+/).filter(Boolean).map(Number);
    switch (cmd) {
      case "M": case "L": return cmd + X(n[0]) + " " + Y(n[1]);
      case "H": return "H" + X(n[0]);
      case "V": return "V" + Y(n[0]);
      case "A": return "A" + f(n[0] * s) + " " + f(n[1] * s) + " " + n[2] + " " + n[3] + " " + n[4] + " " + X(n[5]) + " " + Y(n[6]);
      default: return cmd + args;
    }
  });
}

/** Whole wordmark as one path (letters in the text colour) and the pebble o separately. */
export function wordmarkPaths(s = 1, dx = 0, dy = 0) {
  const text = [], pebble = [];
  for (const { ch, x } of WORDMARK.items) (glyphs[ch].pebble ? pebble : text).push(glyphPath(ch, x + dx, dy, s));
  return { text: text.join(""), pebble: pebble.join("") };
}
