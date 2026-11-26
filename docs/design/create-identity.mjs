// Identity: the Pegs mark, the grid wordmark and the lockup, applied everywhere. Generates the Figcraft command batch for
// the identity, landing (desktop + mobile), docs, social card, app icons and web-implementation sheet.
// Run from the repository root, with the Figcraft app running (control port 7979):
//   node docs/design/create-identity.mjs
//   node docs/design/figcraft-session.mjs docs/design/identity-commands.json
// Put figcraft-cli on PATH, or set FIGCRAFT_CLI to its installed executable.
//   figcraft-cli export docs/design/plonkr-identity.figcraft --out docs/design/exports/identity --format png
//   node docs/design/build-brand-assets.mjs
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import * as M from './mark.mjs';
import { WORDMARK, glyphs, glyphPath } from './wordmark.mjs';

const dir = resolve(import.meta.dirname);
await mkdir(`${dir}/exports/identity`, { recursive: true });
await mkdir(`${dir}/assets/identity`, { recursive: true });

const c = {
  bg: '#0D0E12', raised: '#12131A', panel: '#17181F', violetDeep: '#252033',
  line: '#24252F', lineStrong: '#30313D', text: '#F3F2F7', secondary: '#B0AFBF', muted: '#8C8B9D',
  accent: '#B5A0FF', ink: '#171020',
};
const SEMI = 'Semi Bold', REG = 'Regular';
const commands = [];
let parent, n = 0;
const add = (command, params, as) => commands.push({ command, params, ...(as ? { as } : {}) });
const ref = name => ({ $ref: name });
const nm = p => `${p}${++n}`;

function frame(name, w, h, x, y = 0, fill = c.bg) {
  const as = nm('frame');
  add('frame.create', { name, width: w, height: h, x, y, props: { fill } }, as);
  parent = ref(as);
  return as;
}
function rect(x, y, w, h, fill = c.panel, r = 0, stroke, extra = {}) {
  add('shape.rectangle', { parent, x, y, width: w, height: h, props: { fill, cornerRadius: r, ...(stroke ? { stroke, strokeWeight: 1 } : {}), ...extra } });
}
const hline = (x, y, w, color = c.line) => rect(x, y, w, 1, color);
const vline = (x, y, h, color = c.line) => rect(x, y, 1, h, color);
function dot(x, y, d, fill = c.accent, extra = {}) {
  add('shape.ellipse', { parent, x, y, width: d, height: d, props: { fill, ...extra } });
}
function text(str, x, y, size = 18, color = c.text, width, weight = REG, mono = false, extra = {}) {
  add('text.create', { parent, text: str, x, y, ...(width ? { width } : {}),
    props: { fontFamily: mono ? 'Liberation Mono' : 'Inter', fontStyle: mono && weight === SEMI ? 'Bold' : weight, fontSize: size, fill: color,
      lineHeight: extra.lineHeight || (size >= 30 ? '112%' : '140%'), letterSpacing: extra.letterSpacing ?? (size >= 40 ? '-4%' : 0), ...(extra.align ? { textAlign: extra.align } : {}) } });
}
const mono = (s, x, y, size = 13, color = c.muted, width, weight = REG) => text(s, x, y, size, color, width, weight, true, { lineHeight: '150%' });
function path(d, x, y, w, h, fill = c.accent, name = 'Vector') {
  const as = nm('vector');
  add('vector.create', { parent, name, path: d, x, y, width: w, height: h, props: { fill } }, as);
  return as;
}
function stroke(d, x, y, w, h, color = c.accent, weight = 3, name = 'Stroke') {
  add('vector.create', { parent, name, path: d, x, y, width: w, height: h, props: { fill: null, stroke: color, strokeWeight: weight, strokeCap: 'round', strokeJoin: 'round' } });
}

/* ---- The mark, wordmark and lockup: see mark.mjs and wordmark.mjs ---- */
const PAL = M.palettes;
// Pegs with the top-left of their 14-unit ink box at (x, y); s is pixels per grid unit (s = 1 draws 16 px marks).
function mark(x, y, s, pal = PAL.onDark) {
  for (const l of M.layers) {
    const cx = x + (l.cx - M.bbox.x0) * s, cy = y + (l.cy - M.bbox.y0) * s;
    add('shape.ellipse', { parent, name: l.kind === 'ring' ? 'Mark / socket' : 'Mark / peg', x: cx - l.r * s, y: cy - l.r * s, width: 2 * l.r * s, height: 2 * l.r * s,
      props: l.kind === 'ring' ? { fill: 'none', stroke: pal.ring, strokeWeight: l.stroke * s, strokeAlign: 'inside' } : { fill: pal.peg } });
  }
}
// The grid wordmark as vectors. (x, y) is the top of the ascender box; the baseline is 15 k below it.
function wordmark(x, y, k, pal = PAL.onDark) {
  const text = [];
  for (const { ch, x: gx } of WORDMARK.items) if (!glyphs[ch].pebble) text.push(glyphPath(ch, gx, 15, k));
  add('vector.create', { parent, name: 'Wordmark / letters', path: text.join(''), x, y, width: WORDMARK.width * k, height: 19 * k, props: { fill: pal.text } });
  const o = WORDMARK.items.find(i => i.ch === 'o');
  add('vector.create', { parent, name: 'Wordmark / pebble', path: glyphPath('o', 0, 10, k), x: x + o.x * k, y: y + 5 * k, width: 10 * k, height: 10 * k, props: { fill: pal.pebble } });
}
// The lockup: (x, y) is the top-left of the 105 k x 32 k box. Mark box 32 k, pegs at 2 k inset, wordmark 38 k in.
function lockup(x, y, k = 1, pal = PAL.onDark) {
  mark(x + 2 * k, y + 2 * k, 2 * k, pal);
  wordmark(x + M.LOCKUP.x * k, y + (M.LOCKUP.baseline - 15) * k, k, pal);
}
function pegboard(x, y, cols, rows, gap = 28, filled = null, alpha = '55') {
  for (let i = 0; i < cols; i++) for (let j = 0; j < rows; j++) {
    const isF = filled && filled[0] === i && filled[1] === j;
    if (isF) continue;
    dot(x + i * gap, y + j * gap, 3, `#B0AFBF${alpha}`);
  }
  if (filled) dot(x + filled[0] * gap - 5, y + filled[1] * gap - 5, 13, c.accent);
}
function button(label, x, y, w, h = 44, size = 15) {
  rect(x, y, w, h, c.accent, 8);
  add('text.create', { parent, text: label, x, y: y + (h - size * 1.2) / 2, width: w, props: { fontFamily: 'Inter', fontStyle: SEMI, fontSize: size, fill: c.ink, textAlign: 'center', lineHeight: '120%' } });
}
const scribble = (x, y, w) => stroke('M2 8C34 3 72 10 118 6S196 3 244 7 288 5 298 4', x, y, w, 12, c.accent, 4, 'Hand-drawn underline');
function check(x, y, color = c.accent) { path('M0 6 L4 10 L13 0 L16 3 L4 16 L0 12 Z', x, y, 12, 12, color, 'Tick'); }

/* ---- shared page chrome (desktop) ---- */
function header(w, active = '') {
  // 64 px bar on a 4 px grid. 32 px mark (the s32 cut, whole pixels), 8 px gap plus the cut's 2 px inset, 20 px wordmark.
  rect(0, 0, w, 64, '#0D0E12EE'); hline(0, 64, w);
  lockup(w / 2 - 548 - 2, 16, 1);
  const items = active === 'docs' ? ['The problem', 'How it goes', 'Status', 'Docs'] : ['The problem', 'How it goes', 'Scope', 'Status'];
  const widths = items.map(v => Math.round(v.length * 7.6 / 4) * 4 + 24); let x = w / 2 + 548 - (active === 'docs' ? 0 : 72) - widths.reduce((a, b) => a + b, 0);
  items.forEach((v, i) => { text(v, x + 12, 22, 14, active === 'docs' && i === 3 ? c.text : c.secondary, undefined, REG, false, { lineHeight: '20px' }); x += widths[i]; });
  if (active !== 'docs') { rect(w / 2 + 548 - 60, 16, 60, 32, c.accent, 8); text('Docs', w / 2 + 548 - 60, 22, 14, c.ink, 60, SEMI, false, { align: 'center', lineHeight: '20px' }); }
}
function footer(w, y) {
  rect(0, y, w, 300, c.raised);
  hline(0, y, w);
  const x0 = w / 2 - 548;
  lockup(x0 - 2, y + 56, 1);
  text('Configure existing machines over SSH.\nStill being built.', x0, y + 100, 14.5, c.muted, 260);
  [['This page', ['The problem', 'How it goes', 'Scope', 'Status']], ['Docs', ['Project guide', 'Installation', 'Command reference', 'Road to v0.1.0']], ['Names', ['Product: Plonkr', 'Command: plonk', 'Home: plonkr.dev']]].forEach(([h, items], i) => {
    const x = x0 + 520 + i * 192;
    mono(h, x, y + 56, 12, c.muted);
    items.forEach((v, k) => text(v, x, y + 90 + k * 28, 14.5, c.secondary));
  });
  hline(x0, y + 230, 1096);
  text('© Plonkr. Work in progress.', x0, y + 250, 13, c.muted);
  text('Everything here is a proposal until the proof of concept has been reviewed.', x0 + 540, y + 250, 13, c.muted, 556, REG, false, { align: 'right' });
}
function terminal(x, y, w) {
  rect(x, y, w, 422, c.raised, 12, c.lineStrong, { effects: [{ type: 'dropShadow', color: '#000000CC', offset: [0, 30], radius: 60, spread: -28 }, { type: 'dropShadow', color: '#B5A0FF22', offset: [0, 0], radius: 90, spread: -50 }] });
  rect(x + 1, y + 1, w - 2, 41, c.panel, 0); hline(x + 1, y + 42, w - 2);
  [0, 1, 2].forEach(i => dot(x + 16 + i * 16, y + 16, 10, c.lineStrong));
  mono('plonk', x + 76, y + 14, 12, c.muted); mono('CONCEPT PREVIEW', x + w - 140, y + 15, 11, c.muted);
  rect(x + 1, y + 43, w - 2, 46, c.bg); hline(x + 1, y + 89, w - 2);
  const tw = (w - 2) / 3;
  [['lab-ct', 'LXC'], ['dev-vm', 'VM'], ['rack-01', 'bare metal']].forEach(([a, b], i) => {
    if (i === 0) { rect(x + 1, y + 43, tw, 46, c.raised); rect(x + 1, y + 87, tw, 2, c.accent); }
    if (i) vline(x + 1 + i * tw, y + 43, 46);
    text(a, x + 1 + i * tw + tw / 2 - 52, y + 57, 13.5, i === 0 ? c.text : c.muted, undefined, SEMI, true);
    text(b, x + 1 + i * tw + tw / 2 + 12, y + 58, 12, c.muted);
  });
  const ox = x + 26, oy = y + 112;
  mono('$', ox, oy, 14, c.accent); mono('plonk', ox + 18, oy, 14, c.text, undefined, SEMI); mono('# syntax pending', ox + 80, oy, 14, c.muted);
  mono('▸ lab-ct', ox, oy + 38, 14, c.text, undefined, SEMI); mono('· LXC container · over SSH', ox + 82, oy + 38, 14, c.muted);
  [['Connected', '1/4', c.secondary], ['System updated', '2/4', c.secondary], ['Git, curl and wget installed', '3/4', c.secondary], ['Working environment ready', '4/4', c.text]].forEach(([v, k, col], i) => {
    mono('✓', ox, oy + 68 + i * 27, 14, c.accent, undefined, SEMI); mono(v, ox + 24, oy + 68 + i * 27, 14, col, undefined, i === 3 ? SEMI : REG); mono(k, x + w - 58, oy + 70 + i * 27, 12, c.muted);
  });
  mono('$', ox, oy + 190, 14, c.accent); rect(ox + 18, oy + 191, 8, 17, c.accent);
  text('still deciding this bit', x + w - 190, y + 108, 12.5, c.muted); stroke('M42 9C32 5 18 4 4 9M4 9L12 3M4 9L12 14', x + w - 238, y + 106, 44, 18, c.muted, 1.5, 'Hand-drawn arrow');
  rect(x + w - 90, y + 340, 66, 24, c.raised, 6, c.lineStrong); mono('↻ replay', x + w - 82, y + 347, 11, c.muted);
  rect(x + 1, y + 380, w - 2, 41, c.panel); hline(x + 1, y + 380, w - 2);
  mono('Made-up hostnames and output. Real commands come with the proof of concept.', x + 18, y + 393, 11, c.muted);
}
function pane(x, y, w, head, tag, hot, lines, foot) {
  rect(x, y, w, 396, hot ? c.violetDeep : c.raised, 10, hot ? '#B5A0FF61' : c.lineStrong);
  mono(head, x + 18, y + 17, 11.5, c.muted); mono(tag, x + w - 18 - tag.length * 7, y + 17, 11.5, hot ? c.accent : c.accent);
  hline(x + 1, y + 46, w - 2);
  lines.forEach(([s, col, dy], i) => mono(s, x + 20, y + 66 + dy * 1, 13.4, col));
  hline(x + 1, y + 350, w - 2); text(foot, x + 18, y + 364, 13.5, c.muted);
}

/* =====================================================================
   1. Identity system
   ===================================================================== */
const brand = frame('Identity system', 1440, 3140, 0);
mono('PLONKR / IDENTITY', 64, 46, 13); mono('PEGS / SELECTED DIRECTION', 1030, 46, 13, c.accent);
text('Three in.\nOne to go.', 64, 108, 78, c.text, 860, SEMI);
text('plonk (v.) to put something down firmly, where you want it.', 68, 310, 16, c.muted, 640, REG, true);
text('Three pegs are seated on the pegboard and one socket is still empty: the setup is waiting for the next one to be plonked in. Plain discs and one ring on a 16-unit grid, so there is nothing fine to lose at 16 px.', 68, 372, 21, c.secondary, 760);
rect(1000, 96, 376, 376, c.panel, 22, c.lineStrong);
pegboard(1022, 118, 13, 13, 28, null, '40');
mark(1000 + (376 - 14 * 20) / 2, 96 + (376 - 14 * 20) / 2, 20);
hline(64, 548, 1312);

// Construction
mono('CONSTRUCTION', 64, 580, 13, c.accent);
text('One grid.\nFour circles.\nOne waiting.', 64, 616, 40, c.text, 560, SEMI);
text('Every shape sits on a 16-unit grid: three pegs of radius 3, eight units apart, and a ring of the same outer radius with a 1.5-unit stroke. Scaled by 1, 2, 3 or 6 it lands on whole pixels at 16, 32, 48 and 96 px. Violet for the pegs and a darker violet for the socket, so the empty one reads as waiting rather than missing.', 64, 800, 17, c.secondary, 580);
rect(700, 584, 676, 420, c.panel, 16, c.lineStrong);
{
  const s = 20, ox = 740, oy = 624;
  for (let g = 0; g <= 14; g += 2) { rect(ox + g * s, oy, 1, 14 * s, '#F3F2F714'); rect(ox, oy + g * s, 14 * s, 1, '#F3F2F714'); }
  mark(ox, oy, s);
  const lx = ox + 14 * s + 40;
  [['peg, r = 3', 'three, seated, 8 units apart'], ['socket, a ring', 'outer r = 3, stroke 1.5, #8A74E0'], ['ink box', '14 × 14 units, inset 1 unit in the 16-unit box'], ['sizes', '16, 32, 48 and 96 px (24 px at 1.5×)']].forEach(([a, b], i) => { mono(a, lx, oy + 24 + i * 72, 12.5, c.text, 260, SEMI); mono(b, lx, oy + 44 + i * 72, 11.5, c.muted, 260); });
}
hline(64, 1052, 1312);

// The wordmark, on the pixel grid
mono('THE WORDMARK, ON THE PIXEL GRID', 64, 1084, 13, c.accent);
text('Stems are 3 px, the x-height is 11, ascenders 15 and the descender 4, so every vertical edge falls on a whole pixel at 1× and at any integer scale. The o is the pebble: a 10 px disc resting on the baseline. There is no font in it, only rectangles, circular arcs and one pair of diagonals, so it renders the same on every system.', 64, 1116, 16, c.secondary, 580);
mono('PAIR     GAP     WHY', 64, 1276, 11.5, c.muted);
[['p – l', '3 px', 'round to stem'], ['l – ●', '3 px', 'stem to pebble'], ['● – n', '3 px', 'pebble to stem'], ['n – k', '4 px', 'stem to stem'], ['k – r', '1.5 px', 'open diagonal to stem']].forEach(([a, b, d], i) => {
  hline(64, 1300 + i * 30, 580); mono(a, 64, 1309 + i * 30, 12.5, c.accent, undefined, SEMI); mono(b, 164, 1309 + i * 30, 12.5, c.text); mono(d, 260, 1309 + i * 30, 12.5, c.secondary, 380);
});
hline(64, 1450, 580);
rect(700, 1084, 676, 400, c.panel, 16, c.lineStrong);
{
  const k = 8, X = 770, top = 1124, base = top + 15 * k;
  [[top, 'asc 15'], [base - 11 * k, 'x 11'], [base, 'base'], [base + 4 * k, 'desc 4']].forEach(([gy, lab]) => { rect(716, gy, 640, 1, c.accent + '55'); mono(lab, 716, gy - 14, 9.5, c.muted, 60); });
  wordmark(X, top, k);
  mono('actual size, 1× and 2×, dark and light', 724, 1302, 10.5, c.muted);
  [[1324, c.bg, PAL.onDark, c.lineStrong], [1396, c.text, PAL.onLight, undefined]].forEach(([ty, bg, pal, bd]) => {
    rect(724, ty, 628, 64, bg, 10, bd);
    wordmark(748, ty + 22, 1, pal); wordmark(900, ty + 13, 2, pal); wordmark(1110, ty + 4, 3, pal);
  });
}
hline(64, 1500, 1312);

// App icons, tiles and the lockup
mono('APP ICONS, TILES AND THE LOCKUP', 64, 1532, 13, c.accent);
rect(64, 1572, 128, 128, c.bg, 28, c.lineStrong); mark(64 + (128 - 84) / 2, 1572 + (128 - 84) / 2, 6);
rect(208, 1572, 128, 128, c.accent, 28); mark(208 + (128 - 84) / 2, 1572 + (128 - 84) / 2, 6, PAL.onViolet);
rect(352, 1572, 128, 128, c.text, 28); mark(352 + (128 - 84) / 2, 1572 + (128 - 84) / 2, 6, PAL.onLight);
mono('GRAPHITE', 64, 1710, 11, c.muted); mono('VIOLET', 208, 1710, 11, c.muted); mono('LIGHT', 352, 1710, 11, c.muted);
rect(540, 1572, 420, 56, c.bg, 10, c.lineStrong); lockup(560 - 2, 1584, 1);
rect(540, 1640, 420, 56, c.text, 10); lockup(560 - 2, 1652, 1, PAL.onLight);
mono('lockup, actual size', 540, 1704, 10.5, c.muted);
mono('favicon.svg            the mark at 16 px', 1000, 1580, 12.5, c.secondary, 380);
mono('favicon.ico            16, 32 and 48 px', 1000, 1608, 12.5, c.secondary, 380);
mono('apple-touch-icon.png   180 px on graphite', 1000, 1636, 12.5, c.secondary, 380);
mono('icon-512.png           512 px on graphite', 1000, 1664, 12.5, c.secondary, 380);
mono('lockup-1x/2x/4x.svg    the logo as one SVG', 1000, 1692, 12.5, c.secondary, 380);

// Everything below sits in a group offset by 232 px so the earlier sections could grow.
const lowerAs = nm('lower');
add('frame.create', { parent: ref(brand), name: 'Identity / continued', x: 0, y: 232, width: 1440, height: 2900, props: { fill: 'none', clip: false } }, lowerAs);
parent = ref(lowerAs);
hline(64, 1500, 1312);

// The lockup
mono('THE LOCKUP', 64, 1532, 13, c.accent);
lockup(62, 1584, 4); lockup(520, 1608, 2); lockup(768, 1624, 1);
mono('4×', 64, 1730, 11, c.muted); mono('2×', 520, 1730, 11, c.muted); mono('1×, the header size', 768, 1730, 11, c.muted);
text('The lockup is one SVG on whole pixels: a 32 px mark box, 8 px from the pegs to the wordmark, a 20 px wordmark with its x-height centred on the pegs. Use it at 1×, 2× or any whole multiple and never stretch it. The wordmark can also stand alone in running headers.', 68, 1768, 17, c.secondary, 820);
mono('$ plonk', 990, 1596, 52, c.accent); mono('THE COMMAND', 994, 1672, 12);
rect(990, 1716, 386, 70, c.raised, 10, c.lineStrong); mono('$ plonk # syntax pending', 1012, 1741, 15, c.secondary);
hline(64, 1884, 1312);

// Palette + type
mono('THE COLOURWAY STAYS', 64, 1912, 13, c.accent);
[['Graphite', c.bg], ['Raised', c.raised], ['Surface', c.panel], ['Violet deep', c.violetDeep], ['Boundary', c.lineStrong], ['Text', c.text], ['Secondary', c.secondary], ['Muted', c.muted], ['Violet', c.accent], ['Ink', c.ink]].forEach(([nme, col], i) => {
  const x = 64 + i * 132;
  rect(x, 1956, 116, 66, col, 8, col === c.bg || col === c.raised || col === c.ink ? c.lineStrong : undefined);
  text(nme, x, 2034, 14, c.text); mono(col, x, 2056, 11);
});
text('Muted moved from #7D7C8E to #8C8B9D for contrast (5.8:1 on Graphite). Violet is the brand and the primary action; it never means success or failure. The mark uses two: Violet for the pegs and #8A74E0 for the empty socket.', 64, 2090, 15, c.muted, 1312);
rect(64, 2150, 640, 250, c.panel, 14, c.lineStrong);
text('Aa', 92, 2174, 96, c.text, 300, SEMI); text('Inter', 300, 2200, 28, c.text, undefined, SEMI); text('Semi Bold 650 for display and headings,\nRegular for body. Tight tracking above 40 px.', 300, 2244, 15, c.secondary, 360);
text('System fallbacks only: no font request leaves the page.', 92, 2354, 14, c.muted, 580);
rect(736, 2150, 640, 250, c.panel, 14, c.lineStrong);
mono('$ plonk', 764, 2176, 40, c.accent); mono('Liberation Mono', 764, 2246, 20, c.text); mono('Machine text, terminal output, small labels.\nRendered with JetBrains Mono / Cascadia / Consolas on the web.', 764, 2284, 14, c.secondary, 580);
hline(64, 2450, 1312);

// Motifs + voice
mono('MOTIFS THAT KEEP IT HUMAN', 64, 2482, 13, c.accent);
rect(64, 2526, 420, 260, c.panel, 14, c.lineStrong); pegboard(92, 2556, 12, 7, 30, [7, 4]); text('The pegboard', 92, 2760, 17, c.text, undefined, SEMI);
rect(510, 2526, 420, 260, c.panel, 14, c.lineStrong); text('yours.', 640, 2580, 60, c.accent, undefined, SEMI); scribble(632, 2656, 200); text('The hand-drawn underline', 538, 2760, 17, c.text, undefined, SEMI);
rect(956, 2526, 420, 260, c.panel, 14, c.lineStrong); mono('# syntax pending', 988, 2570, 15, c.muted); stroke('M42 9C32 5 18 4 4 9M4 9L12 3M4 9L12 14', 1020, 2610, 90, 36, c.muted, 1.8, 'Aside arrow'); text('still deciding this bit', 1118, 2616, 15, c.muted); text('The honest aside', 984, 2760, 17, c.text, undefined, SEMI);
mono('Voice: plain, specific, a bit wry. Say what is not decided. No hype words, no claims the proof of concept has not earned.', 64, 2830, 13, c.muted, 1312);

/* =====================================================================
   2. Landing desktop
   ===================================================================== */
const desktop = frame('Landing desktop', 1440, 4380, 1500);
header(1440);
pegboard(900, 90, 18, 22, 28, null, '38');
mono('plonk', 172, 138, 13.4, c.text, undefined, SEMI); mono('v. to set something down firmly,\nwhere you want it.', 228, 138, 13.4, c.muted);
text('Make every\nnew machine', 172, 204, 75, c.text, 480, SEMI, false, { lineHeight: '103%' });
text('yours.', 172, 204 + 2 * 77.25, 75, c.accent, 480, SEMI, false, { lineHeight: '103%' });
scribble(170, 416, 188);
text('You’ve made the container. Now comes the bit where you type what you typed last time. Plonkr is a CLI for configuring machines that already exist, over SSH, so you can skip that part.', 172, 472, 18, c.secondary, 450, REG, false, { lineHeight: '160%' });
button('Read the docs  →', 172, 631, 158); text('How it goes', 352, 643, 15, c.secondary); rect(352, 664, 78, 1, c.lineStrong);
terminal(661, 188, 607);
hline(0, 744, 1440); text('For LXC containers, virtual machines and bare metal: anything you can already reach over SSH.', 172, 768, 16.5, c.secondary, 1000);
[[196, 108], [320, 142], [478, 100]].forEach(([x, w]) => rect(x - 24, 790, w, 1, c.accent + '99'));
hline(0, 820, 1440);

text('Same list. Every machine.', 172, 924, 45, c.text, 900, SEMI);
text('Creating a machine is the interesting part. What comes after is a checklist you already know by heart, and retype anyway.', 172, 996, 17, c.secondary, 560);
pane(172, 1100, 539, 'by hand', 'every time', false, [
  ['$ ssh lab-ct', c.text, 0], ['  update the system', c.secondary, 24], ['  install git, curl, wget', c.secondary, 48], ['  set up the working environment', c.secondary, 72],
  ['$ ssh dev-vm', c.text, 108], ['  update the system', c.secondary, 132], ['  install git, curl, wget', c.secondary, 156], ['  set up the working environment', c.secondary, 180],
  ['$ ssh rack-01', c.text, 216], ['  …and again', c.muted, 240]], 'Easy, and easy to do slightly differently each time.');
pane(729, 1100, 539, 'with plonkr', 'the idea', true, [
  ['$ plonk # syntax pending', c.text, 0], ['▸ lab-ct', c.text, 24], ['  ✓ system updated', c.secondary, 48], ['  ✓ git, curl, wget installed', c.secondary, 72], ['  ✓ working environment ready', c.secondary, 96],
  ['▸ dev-vm', c.text, 132], ['  ✓ same result', c.secondary, 156], ['▸ rack-01', c.text, 192], ['  ✓ same result', c.secondary, 216]], 'One setup, put in place. Illustrative only.');
hline(0, 1600, 1440);

text('Three steps.\nYou already do\nthe first one.', 172, 1650, 42, c.text, 400, SEMI);
text('Plonkr doesn’t change how you create machines. It starts when you’re done.', 172, 1830, 16.5, c.secondary, 340);
[['Make the machine', 'Container, VM or a box on a shelf. Use whatever you use today.'], ['Reach it over SSH', 'Plonkr connects to the machine that’s already there, the way you would from your own terminal.'], ['Put the setup in place', 'Updates, the usual tools, your working environment. The same result each time, then back to your actual work.']].forEach(([h, b], i) => {
  const y = 1650 + i * 150;
  hline(565, y, 703, c.lineStrong); mono(String(i + 1), 565, y + 28, 30, c.accent); text(h, 627, y + 28, 18.5, c.text, 360, SEMI); text(b, 627, y + 58, 15.5, c.secondary, 360);
  if (i === 0) { stroke('M2 4L86 2L88 54L0 56Z', 1126, y + 36, 90, 56, c.secondary, 1.5, 'Sketch / machine'); rect(1127, y + 52, 86, 1.5, c.secondary); rect(1139, y + 73, 30, 1.5, c.accent); }
  if (i === 1) { stroke('M2 1L42 0L43 31L0 32Z', 1111, y + 54, 43, 34, c.secondary, 1.5, 'Sketch / laptop'); stroke('M0 6C18 0 32 14 46 6', 1159, y + 66, 48, 12, c.accent, 1.5, 'Sketch / link'); stroke('M2 1L42 0L43 31L0 32Z', 1209, y + 54, 43, 34, c.secondary, 1.5, 'Sketch / machine'); }
  if (i === 2) { stroke('M2 4L82 2L84 64L0 66Z', 1126, y + 36, 86, 68, c.secondary, 1.5, 'Sketch / checklist'); check(1142, y + 56); check(1142, y + 80); rect(1166, y + 61, 28, 2, c.secondary); rect(1166, y + 85, 28, 2, c.secondary); }
});
hline(565, 2100, 703, c.lineStrong);
hline(0, 2220, 1440);

text('What it is, and what it isn’t.', 172, 2324, 45, c.text, 900, SEMI);
text('Plonkr is early, so it’s worth being clear about the edges.', 172, 2394, 17, c.secondary, 560);
mono('It is', 172, 2470, 13, c.accent, undefined, SEMI); mono('It isn’t', 729, 2470, 13, c.muted, undefined, SEMI);
[['For machines that already exist.', ' Containers, VMs and bare metal.'], ['Used over SSH.', ' It meets your machines where they already are.'], ['Aimed at the first-hour chores.', ' Updates, the usual tools, your working environment.'], ['One command: plonk.', '']].forEach(([a, b], i) => {
  const y = 2504 + i * 58; hline(172, y, 539); mono('✓', 174, y + 16, 14, c.accent, undefined, SEMI); text(a + b, 202, y + 14, 15.5, c.secondary, 509);
});
[['A way to create machines.', ' Keep using whatever you use now.'], ['Released.', ' There’s nothing to install yet.'], ['Decided.', ' Command syntax, config format and supported platforms wait for the proof of concept.']].forEach(([a, b], i) => {
  const y = 2504 + i * 58; hline(729, y, 539); mono('–', 731, y + 16, 14, c.muted, undefined, SEMI); text(a + b, 759, y + 14, 15.5, c.secondary, 509);
});
hline(172, 2736, 539); hline(729, 2678, 539);
hline(0, 2830, 1440);

text('Where it’s at.', 172, 2934, 45, c.text, 900, SEMI);
text('Branding first, then the proof of concept, then a v0.1.0 pre-release.', 172, 3004, 17, c.secondary, 640);
[['Settled', 'Name and command', 'The project is Plonkr. You type plonk.', 2], ['In review', 'Look and feel', 'The mark, the violet and graphite palette, and this site.', 1], ['Up next', 'Proof of concept', 'Import it, read it properly, decide what the first release includes.', 0], ['Later', 'v0.1.0 pre-release', 'Shipped alongside installation and command docs that have been checked against it.', 0]].forEach(([st, h, b, k], i) => {
  const y = 3074 + i * 90; hline(172, y, 1096, c.lineStrong);
  dot(172, y + 30, 10, k === 2 ? c.accent : 'none', k === 2 ? {} : { stroke: k === 1 ? c.accent : c.muted, strokeWeight: 1.5 });
  mono(st, 194, y + 28, 12.5, k ? c.accent : c.muted); text(h, 350, y + 22, 17.5, c.text, 500, SEMI); text(b, 350, y + 50, 15.5, c.secondary, 700);
});
hline(172, 3434, 1096, c.lineStrong);
hline(0, 3520, 1440);

mark(172, 3648, 8); text('Your next machine starts here.', 380, 3646, 46, c.text, 800, SEMI); text('Follow Plonkr from its first public shape to v0.1.0.', 380, 3714, 17, c.secondary, 640);
button('Read the project guide  →', 380, 3764, 240); text('Road to v0.1.0', 646, 3777, 15, c.secondary); rect(646, 3798, 98, 1, c.lineStrong);
hline(0, 3920, 1440);
footer(1440, 4000);

/* =====================================================================
   3. Landing mobile
   ===================================================================== */
const mobile = frame('Landing mobile', 390, 3880, 3100);
rect(0, 0, 390, 64, c.bg); hline(0, 64, 390); lockup(18, 16, 1);
rect(278, 16, 92, 32, c.bg, 8, c.lineStrong); text('Menu', 322, 22, 14, c.text, undefined, SEMI, false, { lineHeight: '20px' }); rect(294, 30, 16, 2, c.text); rect(294, 34, 16, 2, c.text); rect(294, 26, 16, 2, c.text);
pegboard(210, 70, 7, 8, 28, null, '38');
mono('plonk', 20, 100, 12.5, c.text, undefined, SEMI); mono('v. to set something down firmly,\nwhere you want it.', 70, 100, 12.5, c.muted);
text('Make every new\nmachine', 20, 152, 41, c.text, 350, SEMI, false, { lineHeight: '104%' }); text('yours.', 20 + 163, 152 + 42.6, 41, c.accent, 200, SEMI, false, { lineHeight: '104%' }); scribble(181, 234, 108);
text('You’ve made the container. Now comes the bit where you type what you typed last time. Plonkr is a CLI for configuring machines that already exist, over SSH, so you can skip that part.', 20, 262, 17, c.secondary, 350, REG, false, { lineHeight: '158%' });
button('Read the docs  →', 20, 432, 350); text('How it goes', 20, 495, 15, c.secondary); rect(20, 516, 78, 1, c.lineStrong);
rect(20, 568, 350, 412, c.raised, 12, c.lineStrong);
rect(21, 569, 348, 38, c.panel); hline(21, 607, 348); [0, 1, 2].forEach(i => dot(36 + i * 14, 583, 9, c.lineStrong)); mono('plonk', 94, 581, 11, c.muted);
rect(21, 608, 348, 52, c.bg); hline(21, 660, 348); rect(21, 608, 116, 52, c.raised); rect(21, 658, 116, 2, c.accent);
[['lab-ct', 'LXC'], ['dev-vm', 'VM'], ['rack-01', 'bare metal']].forEach(([a, b], i) => { text(a, 21 + i * 116 + 58 - 22, 618, 12.5, i ? c.muted : c.text, undefined, SEMI, true); text(b, 21 + i * 116 + 58 - 20, 637, 11, c.muted); });
mono('$ plonk # syntax pending', 36, 682, 12.5, c.secondary); mono('▸ lab-ct · LXC container · over SSH', 36, 714, 12.5, c.text, undefined, SEMI);
['Connected', 'System updated', 'Git, curl and wget installed', 'Working environment ready'].forEach((v, i) => { mono('✓', 36, 744 + i * 24, 12.5, c.accent, undefined, SEMI); mono(v, 58, 744 + i * 24, 12.5, i === 3 ? c.text : c.secondary); mono(`${i + 1}/4`, 328, 745 + i * 24, 11, c.muted); });
mono('$', 36, 856, 12.5, c.accent); rect(50, 857, 7, 15, c.accent);
hline(21, 920, 348); mono('Made-up hostnames and output.\nReal commands come with the proof of concept.', 36, 928, 10.5, c.muted);
hline(0, 1018, 390); text('For LXC containers, virtual machines and bare metal: anything you can already reach over SSH.', 20, 1044, 15.5, c.secondary, 350); hline(0, 1130, 390);
text('Same list.\nEvery machine.', 20, 1190, 36, c.text, 350, SEMI); text('Creating a machine is the interesting part. What comes after is a checklist you already know by heart, and retype anyway.', 20, 1294, 16, c.secondary, 350);
rect(20, 1410, 350, 250, c.raised, 10, c.lineStrong); mono('by hand', 36, 1426, 11, c.muted); mono('every time', 300, 1426, 11, c.accent); hline(21, 1452, 348);
mono('$ ssh lab-ct\n  update the system\n  install git, curl, wget\n  set up the working env…\n$ ssh dev-vm\n  …the same list\n$ ssh rack-01\n  …and again', 36, 1468, 12, c.secondary);
rect(20, 1684, 350, 250, c.violetDeep, 10, '#B5A0FF61'); mono('with plonkr', 36, 1700, 11, c.muted); mono('the idea', 308, 1700, 11, c.accent); hline(21, 1726, 348);
mono('$ plonk # syntax pending\n▸ lab-ct\n  ✓ system updated\n  ✓ git, curl, wget installed\n  ✓ working environment ready\n▸ dev-vm   ✓ same result\n▸ rack-01  ✓ same result', 36, 1742, 12, c.secondary);
hline(0, 2002, 390);
text('Three steps.\nYou already do\nthe first one.', 20, 2062, 36, c.text, 350, SEMI); text('Plonkr doesn’t change how you create machines. It starts when you’re done.', 20, 2214, 16, c.secondary, 350);
[['Make the machine', 'Container, VM or a box on a shelf. Use whatever you use today.'], ['Reach it over SSH', 'Plonkr connects to the machine that’s already there, the way you would from your own terminal.'], ['Put the setup in place', 'Updates, the usual tools, your working environment. The same result each time.']].forEach(([h, b], i) => {
  const y = 2302 + i * 126; hline(20, y, 350, c.lineStrong); mono(String(i + 1), 20, y + 24, 24, c.accent); text(h, 60, y + 24, 17, c.text, 300, SEMI); text(b, 60, y + 52, 15, c.secondary, 310);
}); hline(20, 2680, 350, c.lineStrong);
hline(0, 2720, 390);
text('What it is, and\nwhat it isn’t.', 20, 2776, 34, c.text, 350, SEMI); text('Plonkr is early, so it’s worth being clear about the edges.', 20, 2862, 16, c.secondary, 340);
mono('It is', 20, 2924, 12, c.accent, undefined, SEMI);
['For machines that already exist.', 'Used over SSH.', 'Aimed at the first-hour chores.', 'One command: plonk.'].forEach((v, i) => { hline(20, 2950 + i * 48, 350); mono('✓', 22, 2965 + i * 48, 12.5, c.accent); text(v, 50, 2963 + i * 48, 15, c.secondary, 320); });
mono('It isn’t', 20, 3160, 12, c.muted, undefined, SEMI);
['A way to create machines.', 'Released.', 'Decided.'].forEach((v, i) => { hline(20, 3186 + i * 48, 350); mono('–', 22, 3201 + i * 48, 12.5, c.muted); text(v, 50, 3199 + i * 48, 15, c.secondary, 320); }); hline(20, 3330, 350);
mark(20, 3400, 4); text('Your next machine\nstarts here.', 20, 3504, 34, c.text, 350, SEMI); button('Read the project guide  →', 20, 3604, 350);
mono('plonkr.dev · work in progress', 20, 3720, 12, c.muted); mono('Everything here is a proposal until the proof of\nconcept has been reviewed.', 20, 3744, 11, c.muted);

/* =====================================================================
   4. Documentation
   ===================================================================== */
const docs = frame('Documentation', 1440, 1480, 3600);
header(1440, 'docs');
mono('DOCUMENTATION', 172, 120, 12); ['Project guide', 'Installation', 'Command reference', 'Road to v0.1.0'].forEach((v, i) => { vline(172, 150 + i * 34, 34, i === 0 ? c.accent : c.lineStrong); text(v, 186, 156 + i * 34, 14.4, i === 0 ? c.text : c.secondary); });
mono('docs / project guide', 434, 120, 12.5, c.accent); text('Meet Plonkr.', 434, 150, 51, c.text, 700, SEMI);
text('A CLI project for the familiar work that comes after creating a machine.', 434, 232, 20, c.secondary, 700);
rect(434, 300, 720, 92, c.violetDeep, 9, '#B5A0FF4D'); dot(452, 322, 20, c.accent); mono('i', 458, 325, 12, c.ink, undefined, SEMI);
text('Before you start: Plonkr is in development. There is no installable release yet. This guide describes the project direction; setup instructions will follow the proof-of-concept review.', 488, 316, 15.5, c.text, 640);
text('Plonkr began with a recurring task: create an LXC container, connect to it, update it, and install the everyday tools needed to get working. The intended scope also includes VMs and bare metal.', 434, 424, 16.5, c.secondary, 720);
text('Plonkr is the product name. plonk is the executable name. The project’s home is plonkr.dev.', 434, 508, 16.5, c.secondary, 720);
hline(434, 580, 720); text('The intended workflow', 434, 610, 25, c.text, undefined, SEMI);
['Create a machine using your existing workflow.', 'Use Plonkr to automate repeat setup on that machine.', 'Get on with the work the machine was created for.'].forEach((v, i) => text(`${i + 1}.  ${v}`, 440, 664 + i * 32, 16, c.secondary));
hline(434, 790, 720); text('Where things stand', 434, 820, 25, c.text, undefined, SEMI);
rect(434, 872, 720, 262, 'none', 9, c.lineStrong); rect(435, 873, 718, 44, c.panel); mono('PIECE', 452, 888, 11.5, c.muted); mono('STATUS', 700, 888, 11.5, c.muted);
[['Name and command', 'Plonkr / plonk, confirmed'], ['Visual identity', 'Proposed, awaiting owner review'], ['Website and docs', 'Design in progress'], ['CLI proof of concept', 'Awaiting import'], ['v0.1.0 prerelease', 'Scope pending proof-of-concept review']].forEach(([a, b], i) => { hline(435, 917 + i * 43, 718); text(a, 452, 928 + i * 43, 15, c.text); text(b, 700, 928 + i * 43, 15, c.secondary); });
mono('ON THIS PAGE', 1220, 120, 12); ['Overview', 'The intended workflow', 'Where things stand', 'Installation', 'Command reference', 'Road to v0.1.0'].forEach((v, i) => { vline(1220, 150 + i * 32, 32, i === 0 ? c.accent : c.lineStrong); text(v, 1234, 155 + i * 32, 13.5, c.secondary); });
footer(1440, 1180);

/* =====================================================================
   5. Social card + icons
   ===================================================================== */
const og = frame('Social card 1200x630', 1200, 630, 5200);
pegboard(520, 40, 25, 22, 28, null, '38');
add('shape.ellipse', { parent, x: 700, y: -160, width: 700, height: 700, props: { fills: [{ type: 'radialGradient', stops: ['#B5A0FF2E', '#B5A0FF00'] }] } });
lockup(76, 82, 4);
text('Make every new\nmachine', 84, 262, 76, c.text, 760, SEMI, false, { lineHeight: '104%' }); text('yours.', 398, 262 + 79, 76, c.accent, 400, SEMI, false, { lineHeight: '104%' }); scribble(398, 410, 282);
text('Configure existing machines over SSH.', 86, 466, 26, c.secondary, 760);
mono('plonkr.dev', 86, 560, 20, c.accent); mono('in development', 1000, 564, 15, c.muted);

const touch = frame('Apple touch icon 180', 180, 180, 5200 + 1280, 0, c.bg); mark(41, 41, 7);
const icon512 = frame('App icon 512', 512, 512, 5200 + 1280 + 220, 0, c.bg); mark(116, 116, 20);
const favX = 5200 + 1280 + 220 + 560;
const icon16 = frame('Favicon 16', 16, 16, favX, 0, c.bg); mark(1, 1, 1);
const icon32 = frame('Favicon 32', 32, 32, favX + 40, 0, c.bg); mark(2, 2, 2);
const icon48 = frame('Favicon 48', 48, 48, favX + 100, 0, c.bg); mark(3, 3, 3);

/* =====================================================================
   6. Web implementation sheet
   ===================================================================== */
const web = frame('Web implementation notes', 1440, 2320, 0, 3140 + 60, c.bg);
mono('PLONKR / WEB IMPLEMENTATION', 64, 46, 13); mono('SPEC, NOT ARTWORK', 1160, 46, 13, c.accent);
text('What the static frames can’t show,\nand how to build it.', 64, 100, 56, c.text, 1100, SEMI);
text('Figcraft holds layout, colour, gradients, shadows, blur and noise. Motion, interaction and responsive behaviour are specified here and in docs/design/web-implementation.md. The site in plonkr/docs/ is the reference implementation.', 68, 232, 18, c.secondary, 980);
hline(64, 316, 1312);

// Pebble drop storyboard
mono('1 / THE PLONK', 64, 346, 13, c.accent);
text('Logo hover and the closing section', 64, 380, 30, c.text, 600, SEMI);
text('A pebble drops into the empty socket, squashes, fills it for a moment, then fades so the socket is empty again. The socket pulses as it lands. 1.5 s, once. Reduced motion: none.', 64, 428, 16, c.secondary, 560);
[['0%  empty', 0, 1, 1, 0], ['20%  falling', -4, 1, 1, 1], ['42%  squash', 0, 1.16, 0.84, 1], ['52–80%  filled', 0, 1, 1, 1]].forEach(([lab, dy, sx, sy, on], i) => {
  const x = 720 + i * 170, S = 7;
  rect(x, 346, 150, 200, c.panel, 12, c.lineStrong);
  const mx = x + (150 - 14 * S) / 2, my = 382;
  mark(mx, my, S);
  if (on) add('shape.ellipse', { parent, name: 'Pebble', x: mx + (11 - 3 * sx) * S, y: my + (3 + dy - 3 * sy) * S, width: 6 * sx * S, height: 6 * sy * S, props: { fill: PAL.onDark.peg } });
  mono(lab, x + 12, 508, 10.5, c.muted, 130);
});
hline(64, 596, 1312);

// Terminal reveal
mono('2 / THE TERMINAL REVEAL', 64, 626, 13, c.accent);
text('Lines arrive one by one', 64, 660, 30, c.text, 600, SEMI);
text('First line (the prompt) is visible immediately. Each later line fades in and rises 3 px: opacity 0 → 1, 250 ms, starting at 260 ms and every 360 ms after. Wait until the window is 20% on screen. Tabs and the replay button restart it. No JavaScript, or reduced motion: everything is simply visible.', 64, 708, 16, c.secondary, 560);
[1, 2, 3, 4, 5].forEach((k, i) => {
  const x = 720 + i * 132;
  rect(x, 626, 116, 120, c.raised, 8, c.lineStrong);
  for (let r = 0; r < 5; r++) rect(x + 12, 642 + r * 18, r < k ? (r === 0 ? 70 : 92) : 92, 6, r < k ? (r === 0 ? c.secondary : c.accent) : c.lineStrong, 3, undefined, r < k ? {} : { opacity: .25 });
  mono(['0 ms', '260 ms', '620 ms', '980 ms', '1340 ms'][i], x + 10, 756, 11, c.muted);
});
hline(64, 830, 1312);

// Header / blur / grain / glow
mono('3 / SURFACES FIGCRAFT CAN ONLY APPROXIMATE', 64, 860, 13, c.accent);
rect(64, 904, 410, 230, c.bg, 12, c.lineStrong);
[0, 1, 2].forEach(i => rect(84, 930 + i * 14, 300 - i * 60, 5, c.lineStrong, 2)); rect(64, 904, 410, 60, '#0D0E12CC', 12); lockup(82, 918, 1);
text('Sticky header', 84, 1000, 17, c.text, undefined, SEMI); text('position: sticky; top: 0; background: rgba(13,14,18,.82); backdrop-filter: blur(12px) saturate(1.3). Figcraft: background blur layer. Border-bottom 1px #24252F.', 84, 1030, 13.5, c.secondary, 360);
rect(498, 904, 410, 230, c.bg, 12, c.lineStrong); pegboard(520, 924, 12, 3, 30, [7, 1], '55');
text('Pegboard', 520, 1010, 17, c.text, undefined, SEMI); text('radial-gradient(circle, rgba(176,175,191,.34) 1.1px, transparent 1.6px) at 28px, masked to fade from the top-right (mask-image radial-gradient). Figcraft: a few dozen ellipses stand in for it.', 520, 1040, 13.5, c.secondary, 366);
rect(932, 904, 444, 230, c.bg, 12, c.lineStrong); add('shape.ellipse', { parent, x: 1010, y: 930, width: 300, height: 180, props: { fills: [{ type: 'radialGradient', stops: ['#B5A0FF40', '#B5A0FF00'] }] } });
text('Glow and grain', 954, 1000, 17, c.text, undefined, SEMI); text('Terminal glow: box-shadow 0 0 90px -50px #B5A0FF. Grain: a fixed 160 px feTurbulence SVG (data URI), 4.5% opacity, blend screen, over the whole page. Figcraft noise effect for reference.', 954, 1030, 13.5, c.secondary, 400);
hline(64, 1190, 1312);

// Hand-drawn parts, hover, responsive
mono('4 / DETAILS THAT MAKE IT FEEL MADE', 64, 1220, 13, c.accent);
rect(64, 1264, 410, 270, c.panel, 12, c.lineStrong); text('yours.', 100, 1292, 58, c.accent, undefined, SEMI); scribble(94, 1362, 160);
text('Hand-drawn underline', 84, 1424, 17, c.text, undefined, SEMI); text('Inline SVG, one wobbly path, stroke 4 px, non-scaling-stroke, preserveAspectRatio none, 104% wide, 0.16 em below the baseline. Never a CSS text-decoration.', 84, 1454, 13.5, c.secondary, 366);
rect(498, 1264, 410, 270, c.panel, 12, c.lineStrong); button('Docs', 530, 1300, 62, 32, 13.5); rect(610, 1300, 62, 32, c.accentHover || '#C7B8FF', 7); text('Docs', 610, 1307, 13.5, c.ink, 62, SEMI, false, { align: 'center', lineHeight: '120%' });
text('Buttons', 518, 1424, 17, c.text, undefined, SEMI); text('Header button 32 px high, 12 px side padding, 8 px radius. Hero buttons 48 px high, 24 px side padding. Hover #C7B8FF; arrows nudge 3 px right. Focus: 2 px violet outline, 3 px offset.', 518, 1454, 13.5, c.secondary, 366);
rect(932, 1264, 444, 270, c.panel, 12, c.lineStrong);
[[954, 150], [1120, 70], [1206, 150]].forEach(([x, w], i) => { rect(x, 1292, w, 98, c.bg, 8, c.lineStrong); rect(x + 8, 1302, w * 0.3, 4, c.accent, 2); rect(x + 8, 1318, w - 16, 5, c.lineStrong, 2); rect(x + 8, 1330, w * 0.7, 5, c.lineStrong, 2); rect(x + 8, 1356, w - 16, i === 1 ? 24 : 18, c.raised, 3, c.lineStrong); });
mono('desktop  tablet  phone', 954, 1396, 10.5, c.muted);
text('Responsive', 954, 1416, 17, c.text, undefined, SEMI); text('Breakpoints 1100 / 820 / 560. Hero stacks, sticky how-it-goes heading unpins, header becomes a Menu disclosure (aria-expanded, Escape closes), terminal tabs stack name over kind, tables scroll inside their wrapper.', 954, 1446, 13.5, c.secondary, 400);
hline(64, 1590, 1312);

mono('5 / PERFORMANCE AND ACCESSIBILITY', 64, 1620, 13, c.accent);
['No third-party requests. System font stacks only (Inter when installed).', 'Total JavaScript under 4 KB, deferred. Content is complete without it.', 'prefers-reduced-motion: no blink, no reveal, no plonk, no smooth scroll.', 'Contrast on Graphite: Text about 17:1, Secondary 8.9:1, Violet 8.7:1, Muted 5.8:1 (all above WCAG AA for body text).', 'Tabs follow the WAI-ARIA tabs pattern with arrow, Home and End keys.', 'Decorative SVG and the pegboard are aria-hidden. The wordmark has an aria-label.'].forEach((v, i) => { dot(66, 1676 + i * 44 + 7, 8, c.accent); text(v, 90, 1670 + i * 44, 16, c.secondary, 1200); });
hline(64, 1990, 1312);
mono('6 / THE LOGO ON THE PIXEL GRID', 64, 2020, 13, c.accent);
['The header and footer logo is the lockup SVG at 1× (105 × 32 px), inlined. The mark has a 2 px inset in its box, so the SVG is pulled left by 2 px and the pegs meet the text column.', 'There is no font in the logo: the wordmark is outlines on the pixel grid (stems 3 px, x-height 11, ascender 15, descender 4, pebble o 10 px), so it renders identically everywhere. Use integer scales only.', 'favicon.svg is the mark at 16 px; favicon.ico carries pixel-exact PNGs at 16, 32 and 48 px.', 'Layout lengths in site.css are multiples of 4 px (1–2 px hairlines excepted). npm run build fails on an off-grid length; there are no exceptions today.', 'Line heights are whole pixels: 20 and 24 for UI, 28 for prose, 32 for the lead.'].forEach((v, i) => { dot(66, 2076 + i * 44 + 7, 8, c.accent); text(v, 90, 2070 + i * 44, 15, c.secondary, 1240); });
mono('Draft / Proposed / Reference implementation lives in plonkr/docs', 64, 2290, 12, c.muted);

add('select.set', { ids: [ref(brand)] }); add('view.zoomSelection', {});
const calls = [
  { name: 'run_command', arguments: { command: 'file.new', params: { name: 'Plonkr — Identity / Pegs' } } },
  { name: 'batch', arguments: { label: 'Identity — identity, website, icons, web notes', commands } },
  { name: 'save_file', arguments: { path: `${dir}/plonkr-identity.figcraft` } },
];
await writeFile(`${dir}/identity-commands.json`, JSON.stringify(calls, null, 2) + '\n');
await writeFile(`${dir}/identity-nodes.json`, JSON.stringify({ brand, desktop, mobile, docs, og, touch, icon512, icon16, icon32, icon48, web }, null, 2) + '\n');
console.log(`Prepared ${commands.length} Figcraft commands`);
