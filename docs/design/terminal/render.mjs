// Draws terminal screens as SVG in the Plonkr window style
// (see docs/assets/site.css: .window, .window-bar, .dots).

import { parseRow } from './ansi.mjs';

export const THEME = {
  bg: '#12131A', // --bg-raised
  panel: '#17181F', // --panel
  line: '#30313D', // --line-strong
  text: '#F3F2F7', // --text
  muted: '#8C8B9D', // --muted
  accent: '#B5A0FF', // --accent
};

export const FONT = '"JetBrains Mono","Cascadia Mono","SF Mono",Menlo,Consolas,"Liberation Mono",monospace';

// Cell metrics for 14 px monospace. Every run starts at its own column, so a
// font with a slightly different advance cannot make columns drift.
const CW = 8.4;
const LH = 20;
const FS = 14;
const PAD_X = 24;
const PAD_TOP = 20;
const PAD_BOTTOM = 24;
const BAR = 40;

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
const num = (n) => (Math.round(n * 100) / 100).toString();

/** Outer size of the window for a grid of cols x rows. */
export function windowSize(cols, rows) {
  return { w: Math.round(cols * CW + PAD_X * 2), h: BAR + PAD_TOP + rows * LH + PAD_BOTTOM };
}

function rowText(row, index) {
  const spans = [];
  for (const run of parseRow(row)) {
    const trimmed = run.text.trimStart();
    const col = run.col + (run.text.length - trimmed.length);
    const text = trimmed.trimEnd();
    if (text === '') continue;
    const attrs = (run.fg ? ` fill="${run.fg}"` : '') + (run.bold ? ' font-weight="700"' : '');
    spans.push(`<tspan x="${num(PAD_X + col * CW)}"${attrs}>${esc(text)}</tspan>`);
  }
  if (spans.length === 0) return '';
  const y = BAR + PAD_TOP + index * LH + 15;
  return `<text y="${y}" xml:space="preserve">${spans.join('')}</text>`;
}

function chrome(id, { w, h, title }) {
  return `<clipPath id="${id}"><rect width="${w}" height="${h}" rx="12"/></clipPath>
<rect width="${w}" height="${h}" rx="12" fill="${THEME.bg}"/>
<g clip-path="url(#${id})"><rect width="${w}" height="${BAR}" fill="${THEME.panel}"/><rect y="${BAR}" width="${w}" height="1" fill="#24252F"/></g>
<rect x=".5" y=".5" width="${w - 1}" height="${h - 1}" rx="11.5" fill="none" stroke="${THEME.line}"/>
<circle cx="20" cy="20" r="4" fill="${THEME.line}"/><circle cx="36" cy="20" r="4" fill="${THEME.line}"/><circle cx="52" cy="20" r="4" fill="${THEME.line}"/>
<text x="76" y="24" class="title">${esc(title)}</text>`;
}

const STYLE = `<style>
text{font:${FS}px ${FONT};fill:${THEME.text};white-space:pre;font-variant-ligatures:none}
.title{font-size:12px;fill:${THEME.muted}}
</style>`;

/**
 * A window drawn at the origin, without an <svg> wrapper, for embedding.
 * rows is an array of ANSI-styled strings, one per terminal row.
 */
export function windowGroup(rows, { cols, title = 'plonk', id = 'win' }) {
  const size = windowSize(cols, Math.max(rows.length, 1));
  return { ...size, svg: `${chrome(id, { ...size, title })}\n${rows.map(rowText).join('\n')}` };
}

function shadow(id) {
  return `<filter id="${id}" x="-20%" y="-20%" width="140%" height="160%" color-interpolation-filters="sRGB">
<feDropShadow dx="0" dy="28" stdDeviation="26" flood-color="#000" flood-opacity=".6"/>
<feDropShadow dx="0" dy="0" stdDeviation="40" flood-color="${THEME.accent}" flood-opacity=".1"/>
</filter>`;
}

/** One screen as a standalone SVG. A margin adds room for the shadow. */
export function screenSvg(rows, { cols, title = 'plonk', margin = 0, label }) {
  const win = windowGroup(rows, { cols, title });
  const w = win.w + margin * 2;
  const h = win.h + margin * 2;
  const frame = margin > 0
    ? `<g transform="translate(${margin} ${margin})" filter="url(#shadow)">${win.svg}</g>`
    : win.svg;
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" role="img" aria-label="${esc(label ?? 'Terminal output from plonk')}">
${STYLE}
<defs>${margin > 0 ? shadow('shadow') : ''}</defs>
${frame}
</svg>
`;
}

/**
 * A looping animation. frames is [{ ms, rows }]; rows is an array of
 * ANSI-styled strings, padded with '' to the same length in every frame.
 * Each row is animated on its own, and a row's content that never changes is
 * written once without animation, so the file stays small.
 */
export function animatedSvg(frames, { cols, rowCount, title = 'plonk', label }) {
  const total = frames.reduce((sum, f) => sum + f.ms, 0);
  const size = windowSize(cols, rowCount);

  const rowLayers = [];
  for (let r = 0; r < rowCount; r++) {
    /** content -> list of [start, end) in ms */
    const spans = new Map();
    let t = 0;
    for (const frame of frames) {
      const content = frame.rows[r] ?? '';
      if (content !== '') {
        const list = spans.get(content) ?? [];
        const last = list.at(-1);
        if (last && last[1] === t) last[1] = t + frame.ms;
        else list.push([t, t + frame.ms]);
        spans.set(content, list);
      }
      t += frame.ms;
    }
    for (const [content, list] of spans) {
      const markup = rowText(content, r);
      if (markup === '') continue;
      if (list.length === 1 && list[0][0] === 0 && list[0][1] === total) {
        rowLayers.push(markup);
        continue;
      }
      // The base value is the one at time 0, so a renderer without SMIL support
      // still shows the first screen instead of an empty window.
      const base = list[0][0] === 0 ? '' : ' opacity="0"';
      rowLayers.push(`<g${base}>${animation(list, total)}${markup}</g>`);
    }
  }

  return `<svg xmlns="http://www.w3.org/2000/svg" width="${size.w}" height="${size.h}" viewBox="0 0 ${size.w} ${size.h}" role="img" aria-label="${esc(label ?? 'Animation of plonk running a profile on a machine, then running it again')}">
${STYLE}
${chrome('win', { ...size, title })}
${rowLayers.join('\n')}
</svg>
`;
}

/** SMIL opacity timeline that is 1 inside the given [start, end) intervals. */
function animation(intervals, total) {
  const times = [0];
  const values = [intervals[0][0] === 0 ? 1 : 0];
  for (const [start, end] of intervals) {
    if (start > 0) { times.push(start); values.push(1); }
    if (end < total) { times.push(end); values.push(0); }
  }
  times.push(total);
  values.push(values.at(-1));
  const keyTimes = times.map((t) => (t === 0 ? '0' : t === total ? '1' : (t / total).toFixed(5)));
  return `<animate attributeName="opacity" dur="${total}ms" repeatCount="indefinite" calcMode="discrete" keyTimes="${keyTimes.join(';')}" values="${values.join(';')}"/>`;
}
