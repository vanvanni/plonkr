// The Plonkr mark, "Pegs": three pegs seated on the pegboard and one socket still empty, waiting for the next
// plonk. One 16-unit grid; scale it by 1, 2, 3 or 6 for 16, 32, 48 and 96 px and every disc edge sits on a whole
// pixel. Plain discs and one ring, so there is no detail to lose at small sizes.
//
// Also the lockup: the mark at 2x (32 px) and the grid wordmark (wordmark.mjs) in one SVG, on whole pixels.
// Single source of truth for the site's inline logo, favicon.svg, favicon.ico, the touch and app icons, the social
// card and the Figcraft frames.

import { WORDMARK, wordmarkPaths } from './wordmark.mjs';

export const GRID = 16;

/** kind 'disc' = filled circle; 'ring' = stroked circle. */
export const layers = [
  { kind: 'disc', tone: 'peg', cx: 4, cy: 4, r: 3 },
  { kind: 'disc', tone: 'peg', cx: 4, cy: 12, r: 3 },
  { kind: 'disc', tone: 'peg', cx: 12, cy: 12, r: 3 },
  { kind: 'ring', tone: 'ring', cx: 12, cy: 4, r: 3, stroke: 1.5 }, // outer radius 3, so it is the same size as a peg
];
/** The pebble that fills the socket in the plonk animation. */
export const demo = { cx: 12, cy: 4, r: 3 };

export const palettes = {
  onDark: { peg: '#B5A0FF', ring: '#8A74E0', text: '#F3F2F7', pebble: '#B5A0FF' },
  onLight: { peg: '#8A74E0', ring: '#6A55C2', text: '#171020', pebble: '#8A74E0' },
  onViolet: { peg: '#4B3B96', ring: '#171020', text: '#171020', pebble: '#4B3B96' },
};

/** Ink bounding box in grid units: 14 x 14, inset 1 unit from the 16-unit box. */
export const bbox = { x0: 1, y0: 1, x1: 15, y1: 15, w: 14, h: 14 };

/** The lockup in pixels at 1x: 32 px mark box, 8 px gap from the pegs to the wordmark, 20 px wordmark. */
export const LOCKUP = { markScale: 2, gap: 8, baseline: 22, x: 30 + 8, w: 38 + WORDMARK.width, h: 32 };

const f = n => +n.toFixed(3);
const el = (l, s, dx, dy, cls, pal, extra = '') => {
  const c = cls ? ` class="${cls}"` : '';
  if (l.kind === 'ring') return `<circle${c} cx="${f(dx + l.cx * s)}" cy="${f(dy + l.cy * s)}" r="${f((l.r - l.stroke / 2) * s)}" fill="none"${pal ? ` stroke="${pal.ring}"` : ''} stroke-width="${f(l.stroke * s)}"${extra}/>`;
  return `<circle${c} cx="${f(dx + l.cx * s)}" cy="${f(dy + l.cy * s)}" r="${f(l.r * s)}"${pal ? ` fill="${pal.peg}"` : ''}${extra}/>`;
};

/** Standalone mark SVG on the 16-unit viewBox. */
export function svg({ size = 256, palette = palettes.onDark } = {}) {
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 ${GRID} ${GRID}" fill="none">${layers.map(l => el(l, 1, 0, 0, '', palette)).join('')}</svg>\n`;
}

/** Inline mark for the site: classes the stylesheet colours and animates, plus the hidden plonk pebble. */
export function inline({ size = 112 } = {}) {
  const body = layers.map(l => el(l, 1, 0, 0, l.kind === 'ring' ? 'socket' : 'peg', null)).join('');
  return `<svg class="mark" width="${size}" height="${size}" viewBox="0 0 ${GRID} ${GRID}" aria-hidden="true">${body}<circle class="demo" cx="${demo.cx}" cy="${demo.cy}" r="${demo.r}"/></svg>`;
}

/** Lockup SVG at integer scale k (1 = the 20 px header lockup, 105 x 32 px). */
export function lockup({ k = 1, palette = null, inlineClasses = true, label = 'Plonkr' } = {}) {
  const s = LOCKUP.markScale * k;
  const w = LOCKUP.w * k, h = LOCKUP.h * k;
  const wm = wordmarkPaths(k, LOCKUP.x, LOCKUP.baseline);
  const mark = layers.map(l => el(l, s, 0, 0, inlineClasses ? (l.kind === 'ring' ? 'socket' : 'peg') : '', palette)).join('');
  const demoEl = `<circle class="demo" cx="${f(demo.cx * s)}" cy="${f(demo.cy * s)}" r="${f(demo.r * s)}"/>`;
  const text = `<path${inlineClasses ? ' class="wm"' : ''}${palette ? ` fill="${palette.text}"` : ''} d="${wm.text}"/>`;
  const pebble = `<path${inlineClasses ? ' class="wm-o"' : ''}${palette ? ` fill="${palette.pebble}"` : ''} d="${wm.pebble}"/>`;
  return `<svg class="lockup" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" role="img" aria-label="${label}">${mark}${demoEl}${text}${pebble}</svg>`;
}

/** Standalone lockup file with explicit colours (no animation pebble). */
export function lockupFile({ k = 4, palette = palettes.onDark } = {}) {
  return lockup({ k, palette, inlineClasses: false }).replace('<svg ', '<svg xmlns="http://www.w3.org/2000/svg" ').replace(/<circle class="demo"[^>]*\/>/, '') + '\n';
}
