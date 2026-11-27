#!/usr/bin/env node
// Adds the terminal screens to the Figcraft identity file.
//
//   node docs/design/terminal/figcraft.mjs        (from the repository root)
//
// Unlike create-identity.mjs this does not regenerate the document. It opens
// docs/design/plonkr-identity.figcraft in a headless Figcraft session
// (`figcraft-cli run --in`), adds three frames, and saves the file back. The
// existing frames, and any manual edits to them, are left as they are. Frames
// this script made earlier are replaced, so it can be run again after the
// screens change. The running Figcraft app is not used and is not touched.
//
// Put figcraft-cli on PATH, or set FIGCRAFT_CLI to its executable.
//
//   Terminal screens        the five stills as editable terminal windows, the
//                           colours of the real UI, and notes for use
//   Promo poster 1600x900   the poster card
//   Promo storyboard        six key frames of the looping promo, with timings
//
// The SVG and PNG exports (build.mjs) are the source for the website; these
// frames are the editable copy for layout and review.

import { spawnSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { palette, parseRow } from './ansi.mjs';
import { COLS, fail, promo, record, root, stills } from './scenes.mjs';
import * as M from '../mark.mjs';
import { WORDMARK, glyphs, glyphPath } from '../wordmark.mjs';

const file = join(root, 'docs', 'design', 'plonkr-identity.figcraft');
const FRAMES = ['Terminal screens', 'Promo poster 1600x900', 'Promo storyboard'];

function figcraft() {
  const candidates = [process.env.FIGCRAFT_CLI, 'C:\\Program Files\\ArtCraft\\FigCraft\\figcraft-cli.exe'];
  const found = candidates.find((c) => c && existsSync(c));
  return found ?? 'figcraft-cli';
}
const cli = figcraft();
const run = (...args) => {
  const result = spawnSync(cli, args, { encoding: 'utf8', maxBuffer: 1 << 28 });
  if (result.error) fail(`Could not run ${cli}: ${result.error.message}\nPut figcraft-cli on PATH or set FIGCRAFT_CLI.`);
  if (result.status !== 0) fail(`${cli} ${args.join(' ')} failed:\n${result.stdout}${result.stderr}`);
  return result.stdout;
};

// --- the design vocabulary of create-identity.mjs ---------------------------

const c = {
  bg: '#0D0E12', raised: '#12131A', panel: '#17181F', violetDeep: '#252033',
  line: '#24252F', lineStrong: '#30313D', text: '#F3F2F7', secondary: '#B0AFBF', muted: '#8C8B9D',
  accent: '#B5A0FF', ink: '#171020',
};
const SEMI = 'Semi Bold';
const REG = 'Regular';
const commands = [];
let parent;
let n = 0;
const add = (command, params, as) => commands.push({ command, params, ...(as ? { as } : {}) });
const ref = (name) => ({ $ref: name });
const nm = (p) => `${p}${++n}`;

function frame(name, w, h, x, y, fill = c.bg) {
  const as = nm('frame');
  add('frame.create', { name, width: w, height: h, x, y, props: { fill } }, as);
  parent = ref(as);
  return as;
}
function rect(x, y, w, h, fill = c.panel, r = 0, stroke, extra = {}) {
  add('shape.rectangle', { parent, x, y, width: w, height: h, props: { fill, cornerRadius: r, ...(stroke ? { stroke, strokeWeight: 1 } : {}), ...extra } });
}
const hline = (x, y, w, color = c.line) => rect(x, y, w, 1, color);
function dot(x, y, d, fill = c.accent, extra = {}) {
  add('shape.ellipse', { parent, x, y, width: d, height: d, props: { fill, ...extra } });
}
function text(str, x, y, size = 18, color = c.text, width, weight = REG, mono = false, extra = {}) {
  add('text.create', { parent, text: str, x, y, ...(width ? { width } : {}),
    props: { fontFamily: mono ? 'Liberation Mono' : 'Inter', fontStyle: mono && weight === SEMI ? 'Bold' : weight, fontSize: size, fill: color,
      lineHeight: extra.lineHeight || (size >= 30 ? '112%' : '140%'), letterSpacing: extra.letterSpacing ?? (size >= 40 ? '-4%' : 0), ...(extra.align ? { textAlign: extra.align } : {}) } });
}
const mono = (s, x, y, size = 13, color = c.muted, width, weight = REG) => text(s, x, y, size, color, width, weight, true, { lineHeight: '150%' });

const PAL = M.palettes;
function mark(x, y, s, pal = PAL.onDark) {
  for (const l of M.layers) {
    const cx = x + (l.cx - M.bbox.x0) * s;
    const cy = y + (l.cy - M.bbox.y0) * s;
    add('shape.ellipse', { parent, name: l.kind === 'ring' ? 'Mark / socket' : 'Mark / peg', x: cx - l.r * s, y: cy - l.r * s, width: 2 * l.r * s, height: 2 * l.r * s,
      props: l.kind === 'ring' ? { fill: 'none', stroke: pal.ring, strokeWeight: l.stroke * s, strokeAlign: 'inside' } : { fill: pal.peg } });
  }
}
function wordmark(x, y, k, pal = PAL.onDark) {
  const letters = [];
  for (const { ch, x: gx } of WORDMARK.items) if (!glyphs[ch].pebble) letters.push(glyphPath(ch, gx, 15, k));
  add('vector.create', { parent, name: 'Wordmark / letters', path: letters.join(''), x, y, width: WORDMARK.width * k, height: 19 * k, props: { fill: pal.text } });
  const o = WORDMARK.items.find((i) => i.ch === 'o');
  add('vector.create', { parent, name: 'Wordmark / pebble', path: glyphPath('o', 0, 10, k), x: x + o.x * k, y: y + 5 * k, width: 10 * k, height: 10 * k, props: { fill: pal.pebble } });
}
function lockup(x, y, k = 1, pal = PAL.onDark) {
  mark(x + 2 * k, y + 2 * k, 2 * k, pal);
  wordmark(x + M.LOCKUP.x * k, y + (M.LOCKUP.baseline - 15) * k, k, pal);
}
const SHADOW = [
  { type: 'dropShadow', color: '#000000CC', offset: [0, 30], radius: 60, spread: -28 },
  { type: 'dropShadow', color: '#B5A0FF22', offset: [0, 0], radius: 90, spread: -50 },
];

// --- a terminal window, one text layer per styled run ------------------------

// Same geometry as render.mjs: 14 px mono on a 20 px line, 8.4 px cells.
const CW = 8.4;
const LH = 20;
const PAD_X = 24;
const PAD_TOP = 20;
const PAD_BOTTOM = 24;
const BAR = 40;

const windowSize = (rows, scale = 1) => ({
  w: Math.round((COLS * CW + PAD_X * 2) * scale),
  h: Math.round((BAR + PAD_TOP + rows.length * LH + PAD_BOTTOM) * scale),
});

/** Draws a window with its top-left at (x, y) in the current parent. */
function termWindow(x, y, rows, { name, scale = 1, shadow = false }) {
  const s = scale;
  const { w, h } = windowSize(rows, s);
  const outer = parent;
  const as = nm('window');
  add('frame.create', { parent: outer, name: `Terminal / ${name}`, x, y, width: w, height: h,
    props: { fill: c.raised, cornerRadius: 12 * s, clip: true, stroke: c.lineStrong, strokeWeight: 1, ...(shadow ? { effects: SHADOW } : {}) } }, as);
  parent = ref(as);
  rect(0, 0, w, BAR * s, c.panel); hline(0, BAR * s, w, '#24252F');
  [0, 1, 2].forEach((i) => dot((16 + i * 16) * s, 16 * s, 8 * s, c.lineStrong));
  mono('plonk', 72 * s, 12 * s, 12 * s, c.muted);
  rows.forEach((row, r) => {
    for (const run of parseRow(row)) {
      const trimmed = run.text.trimStart();
      const col = run.col + (run.text.length - trimmed.length);
      const str = trimmed.trimEnd();
      if (str === '') continue;
      add('text.create', { parent, text: str, x: (PAD_X + col * CW) * s, y: (BAR + PAD_TOP + r * LH) * s,
        props: { fontFamily: 'Liberation Mono', fontStyle: run.bold ? 'Bold' : REG, fontSize: 14 * s, fill: run.fg ?? c.text, lineHeight: `${LH * s}px`, letterSpacing: 0 } });
    }
  });
  parent = outer;
  return { w, h };
}

function pegboard(x, y, w, h, fadeFrom, gap = 28) {
  const reach = Math.max(w, h) * 0.8;
  for (let gx = 0; gx * gap < w; gx++) {
    for (let gy = 0; gy * gap < h; gy++) {
      const px = x + gx * gap;
      const py = y + gy * gap;
      const d = Math.hypot(px - fadeFrom[0], py - fadeFrom[1]) / reach;
      const alpha = Math.round(Math.max(0, 1 - d) * 0x55);
      if (alpha < 12) continue;
      dot(px, py, 3, `#B0AFBF${alpha.toString(16).padStart(2, '0')}`);
    }
  }
}

// --- content ------------------------------------------------------------------

const { scenes, plain } = record();
const named = Object.fromEntries(stills(scenes, plain).map(([name, rows, label]) => [name, { rows, label }]));
const { frames: timeline, marks } = promo(scenes, plain);

// Existing copies of our frames, which this run replaces.
const known = run('info', file);
for (const name of FRAMES) {
  for (const match of known.matchAll(new RegExp(`^\\s+(\\d+) FRAME "${name}"`, 'gm'))) {
    add('edit.delete', { ids: [Number(match[1])] });
  }
}

const BASE_Y = 5700;

/* ---- 1. Terminal screens ---- */
const screens = frame('Terminal screens', 1440, 4000, 0, BASE_Y);
const screensIndex = commands.length - 1;
mono('PLONKR / TERMINAL', 64, 46, 13); mono('PROPOSAL / REAL CLI OUTPUT', 1030, 46, 13, c.accent);
text('What the CLI\nactually prints.', 64, 100, 64, c.text, 900, SEMI);
text('These are the real terminal UI and plain output of plonk, recorded with scripted target output. No SSH connection is made. The demo profile, commands and git output are made up; “User app ready” and “Timezone set to …” are the messages the provisioning scripts really print.', 68, 270, 18, c.secondary, 900);
text('Generated from code with docs/design/terminal/build.mjs. This frame is the editable copy for layout and review; the SVG and PNG exports in docs/design/exports/terminal/ are what the website would use. Not approved for the website yet.', 68, 372, 15, c.muted, 900);
hline(64, 452, 1312);

let y = 484;
mono('1 / THE TERMINAL UI', 64, y, 13, c.accent);
text('Four tasks, then the same run again.', 64, y + 34, 30, c.text, 900, SEMI);
text('The first run does everything. The second finds the Go toolchain and the checkout already there, so their locks hold and those tasks are bypassed. The configuration task still runs, because it has no lock.', 64, y + 82, 16, c.secondary, 700);
y += 176;

const SCALE = 0.7;
const caption = (name, label, x, top) => {
  mono(name.toUpperCase(), x, top, 11.5, c.accent); text(label, x, top + 22, 14.5, c.secondary, 600);
};
let rowBottom = y;
[['tui-running', 64], ['tui-finished', 718]].forEach(([name, x]) => {
  const { h } = termWindow(x, y, named[name].rows, { name, scale: SCALE });
  rowBottom = Math.max(rowBottom, y + h);
});
[['tui-running', 64], ['tui-finished', 718]].forEach(([name, x]) => caption(name, named[name].label, x, rowBottom + 16));
y = rowBottom + 90;
const repeat = termWindow(64, y, named['tui-repeat'].rows, { name: 'tui-repeat', scale: SCALE });
caption('tui-repeat', named['tui-repeat'].label, 64, y + repeat.h + 16);
text('Bypassed, not skipped', 718, y + 6, 24, c.text, 620, SEMI);
text('A lock-bypassed task uses the existing bypassed status (↷), as does a bypass set in the profile or on the command line. Skipped stays for work gated off by a flag. The reason is in the plain output and the log: “bypassed by lock: exists and eval unchanged”.', 718, y + 44, 15.5, c.secondary, 620);
y += repeat.h + 110;
hline(64, y, 1312);

y += 32;
mono('2 / PLAIN OUTPUT', 64, y, 13, c.accent);
text('--no-tui, pipes and logs.', 64, y + 34, 30, c.text, 900, SEMI);
text('No colour, one line per step, the same status words as the terminal UI and the saved log, and a summary at the end. The repeat run shows why each task was bypassed.', 64, y + 82, 16, c.secondary, 700);
y += 176;
let plainBottom = y;
[['plain-first-run', 64], ['plain-repeat', 718]].forEach(([name, x]) => {
  const { h } = termWindow(x, y, named[name].rows, { name, scale: SCALE });
  plainBottom = Math.max(plainBottom, y + h);
});
[['plain-first-run', 64], ['plain-repeat', 718]].forEach(([name, x]) => caption(name, named[name].label, x, plainBottom + 16));
y = plainBottom + 100;
hline(64, y, 1312);

y += 32;
mono('3 / COLOURS IN THE REAL UI', 64, y, 13, c.accent);
text('Authentic, and not the landing page’s violet.', 64, y + 34, 30, c.text, 1000, SEMI);
y += 96;
[
  [99, 'plonk heading, spinner, $ prompt'], [78, '✓ completed'], [243, 'muted counts and hints'], [214, '↷ bypassed'],
  [203, '✗ failed'], [220, '⊘ skipped'], [75, '◎ planned'],
].forEach(([index, use], i) => {
  const col = i % 4;
  const row = Math.floor(i / 4);
  const x = 64 + col * 330;
  const top = y + row * 92;
  rect(x, top, 56, 56, palette(index), 8, c.lineStrong);
  mono(`ANSI ${index}`, x + 70, top + 4, 12.5, c.text, 240, SEMI); mono(palette(index).toUpperCase(), x + 70, top + 24, 12, c.muted); text(use, x + 70, top + 44, 13.5, c.secondary, 240);
});
y += 2 * 92 + 12;
text('The identity says violet never means success, and the landing page’s terminal preview uses violet ticks on purpose. The real UI draws completed work green. Either these stay as an honest picture of the CLI, or the CLI palette changes to match the identity. Open decision; nothing here chooses.', 64, y, 16, c.secondary, 980);
y += 100;
hline(64, y, 1312);

y += 32;
mono('4 / USING THEM', 64, y, 13, c.accent);
y += 44;
['Give every image a meaningful alt text. The SVGs carry an aria-label, but an img element needs its own alt.',
  'The promo is plain SMIL, about 26 s, no script and no font requests. Offer a still to visitors who prefer reduced motion (picture with a prefers-reduced-motion source).',
  'It loops for more than five seconds and cannot be paused. WCAG 2.2.2 asks for a pause control for that, so decide: accept, add one, or use the stills only.',
  'The hero terminal on the landing page says “concept preview”. These are real output, but the product is beta and the output may change before a stable release.',
  'Copy only the files you use into docs/assets/. docs/design/ is excluded from the published build.',
  'Fonts: SVGs use the site’s mono stack; the PNGs here were rendered with Cascadia Mono. Every run sits at its own column, so another font cannot misalign the grid.',
].forEach((v, i) => { dot(66, y + i * 46 + 7, 8, c.accent); text(v, 90, y + i * 46, 15.5, c.secondary, 1260); });
y += 6 * 46 + 24;
mono('Proposal. Reproduce: node docs/design/terminal/build.mjs, then node docs/design/terminal/figcraft.mjs', 64, y, 12, c.muted);
y += 60;
commands[screensIndex].params.height = Math.ceil(y);

/* ---- 2. Promo poster ---- */
const POSTER = { w: 1600, h: 900 };
frame('Promo poster 1600x900', POSTER.w, POSTER.h, 1540, BASE_Y);
pegboard(760, 0, 840, 540, [POSTER.w * 0.85, POSTER.h * 0.25]);
lockup(94, 94, 2);
text('Make every\nnew machine', 96, 262, 76, c.text, 640, SEMI, false, { lineHeight: '104%' });
text('yours.', 96, 262 + 2 * 79, 76, c.accent, 640, SEMI, false, { lineHeight: '104%' });
text('Configure existing machines over SSH,\nwith a little less ceremony.', 98, 540, 22, c.secondary, 600);
mono('BETA · NO STABLE RELEASE YET · PLONKR.DEV', 98, POSTER.h - 84, 13, c.muted);
{
  const rows = named['tui-running'].rows;
  const { w, h } = windowSize(rows);
  termWindow(POSTER.w - w - 72, Math.round((POSTER.h - h) / 2) + 24, rows, { name: 'tui-running', shadow: true });
}

/* ---- 3. Promo storyboard ---- */
const BOARD = { w: 1600, h: 1000 };
frame('Promo storyboard', BOARD.w, BOARD.h, 1540, BASE_Y + 1000);
mono('PLONKR / PROMO STORYBOARD', 64, 46, 13); mono('PROPOSAL / PLONK-RUN.SVG', 1230, 46, 13, c.accent);
text('One loop, six moments.', 64, 92, 48, c.text, 1100, SEMI);
const total = timeline.reduce((sum, f) => sum + f.ms, 0);
const startOf = (index) => timeline.slice(0, index).reduce((sum, f) => sum + f.ms, 0);
text(`${(total / 1000).toFixed(1)} s, then it starts again. Type the command, watch the UI, read the summary; run it again and watch two tasks get bypassed.`, 68, 164, 16, c.secondary, 900);
const moments = [
  ['typed-1', 'The command is typed'],
  ['running-1', 'First run: the UI works through the tasks'],
  ['finished-1', 'First run: every task ticked'],
  ['summary-1', 'The UI closes and leaves the summary'],
  ['finished-2', 'Second run: locks bypass two tasks'],
  ['summary-2', 'Both runs, one screen'],
];
moments.forEach(([key, label], i) => {
  const mark_ = marks.find((m) => m.name === key);
  if (!mark_) fail(`promo has no moment named ${key}`);
  const col = i % 3;
  const row = Math.floor(i / 3);
  const x = 64 + col * 500;
  const top = 232 + row * 380;
  const { h } = termWindow(x, top, timeline[mark_.index].rows, { name: key, scale: 0.5 });
  mono(`${(startOf(mark_.index) / 1000).toFixed(1)} s`, x, top + h + 14, 12, c.accent, undefined, SEMI);
  text(label, x + 64, top + h + 12, 14.5, c.secondary, 380);
});
mono('SMIL animation, no script. Each row is animated separately, so unchanged rows are written once.', 64, BOARD.h - 56, 12, c.muted);

/* ---- run it ---- */
const work = mkdtempSync(join(tmpdir(), 'plonkr-figcraft-'));
try {
  const script = join(work, 'terminal-screens.json');
  const result = join(work, 'plonkr-identity.figcraft');
  writeFileSync(script, JSON.stringify(commands));
  run('run', script, '--in', file, '--out', result);
  const after = run('info', result);
  for (const name of FRAMES) if (!after.includes(`FRAME "${name}"`)) fail(`The saved file has no frame named ${name}.`);
  copyFileSync(result, file);
  console.log(`Added ${FRAMES.join(', ')} to ${file} (${commands.length} commands)`);
} finally {
  rmSync(work, { recursive: true, force: true });
}
