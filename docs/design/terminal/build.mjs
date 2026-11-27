#!/usr/bin/env node
// Builds the terminal screenshots and the looping promo from the real CLI.
//
//   node docs/design/terminal/build.mjs          (from the repository root)
//
// 1. Runs the build-tagged recorders in ui/ and cmd/. They drive the real
//    runner, terminal UI model and plain printer with scripted target output.
//    No SSH connection is made and nothing is installed anywhere.
// 2. Composes the frames into SVG (window chrome from the site's tokens).
// 3. Rasterises PNGs with a Chromium-based browser: set PLONK_BROWSER to its
//    executable, or leave it unset to try common install locations. Without a
//    browser the SVGs are still written and the PNGs are skipped.
//
// Output goes to docs/design/exports/terminal/. These are design exports: copy
// the ones you use into docs/assets/ so they ship with the site.
// The Figcraft copy of these screens is built by figcraft.mjs.

import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { animatedSvg, screenSvg, THEME, windowGroup } from './render.mjs';
import { COLS, ROWS, fail, promo, record, root, stillView, stills } from './scenes.mjs';

const out = join(root, 'docs', 'design', 'exports', 'terminal');

// --- poster ----------------------------------------------------------------

function poster(rows) {
  const W = 1600;
  const H = 900;
  const lockup = readFileSync(join(root, 'docs', 'design', 'assets', 'identity', 'plonkr-lockup-4x.svg'), 'utf8')
    .replace(/^[\s\S]*?<svg[^>]*>/, '').replace(/<\/svg>\s*$/, '');
  const win = windowGroup(rows, { cols: COLS, title: 'plonk', id: 'poster-win' });
  const x = W - win.w - 72;
  const y = Math.round((H - win.h) / 2) + 24;
  const sans = 'Inter,ui-sans-serif,system-ui,"Segoe UI",Roboto,sans-serif';
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" role="img" aria-label="Plonkr: make every new machine yours">
<style>
text{font:14px "JetBrains Mono","Cascadia Mono","SF Mono",Menlo,Consolas,"Liberation Mono",monospace;fill:${THEME.text};white-space:pre;font-variant-ligatures:none}
.title{font-size:12px;fill:${THEME.muted}}
.h{font:650 60px/1 ${sans};letter-spacing:-2.6px}
.p{font:400 22px/1 ${sans};fill:#B0AFBF}
.s{font:400 13px "JetBrains Mono","Cascadia Mono",Consolas,monospace;fill:${THEME.muted};letter-spacing:.4px}
</style>
<defs>
<pattern id="pegs" width="28" height="28" patternUnits="userSpaceOnUse"><circle cx="14" cy="14" r="1.2" fill="#B0AFBF" fill-opacity=".34"/></pattern>
<radialGradient id="fade" cx=".85" cy=".25" r=".8"><stop offset="0" stop-color="#fff"/><stop offset="1" stop-color="#000"/></radialGradient>
<mask id="pegmask"><rect width="${W}" height="${H}" fill="url(#fade)"/></mask>
<filter id="shadow" x="-20%" y="-20%" width="140%" height="160%" color-interpolation-filters="sRGB">
<feDropShadow dx="0" dy="32" stdDeviation="28" flood-color="#000" flood-opacity=".65"/>
<feDropShadow dx="0" dy="0" stdDeviation="44" flood-color="${THEME.accent}" flood-opacity=".12"/>
</filter>
</defs>
<rect width="${W}" height="${H}" fill="#0D0E12"/>
<rect width="${W}" height="${H}" fill="url(#pegs)" mask="url(#pegmask)" opacity=".55"/>
<svg x="96" y="96" width="210" height="64" viewBox="0 0 420 128">${lockup}</svg>
<text class="h" x="96" y="330">Make every</text>
<text class="h" x="96" y="398">new machine</text>
<text class="h" x="96" y="466" style="fill:${THEME.accent}">yours.</text>
<text class="p" x="96" y="540">Configure existing machines over SSH,</text>
<text class="p" x="96" y="572">with a little less ceremony.</text>
<text class="s" x="96" y="${H - 72}">BETA · NO STABLE RELEASE YET · PLONKR.DEV</text>
<g transform="translate(${x} ${y})" filter="url(#shadow)">${win.svg}</g>
</svg>
`;
}

// --- PNG -------------------------------------------------------------------

function findBrowser() {
  const candidates = [process.env.PLONK_BROWSER];
  const local = process.env.LOCALAPPDATA ?? join(homedir(), 'AppData', 'Local');
  for (const base of [join(local, 'ms-playwright'), join(homedir(), '.cache', 'ms-playwright')]) {
    if (!existsSync(base)) continue;
    for (const dir of readdirSync(base).sort().reverse()) {
      candidates.push(
        join(base, dir, 'chrome-headless-shell-win64', 'chrome-headless-shell.exe'),
        join(base, dir, 'chrome-win64', 'chrome.exe'),
        join(base, dir, 'chrome-headless-shell-linux64', 'chrome-headless-shell'),
        join(base, dir, 'chrome-linux64', 'chrome'),
      );
    }
  }
  candidates.push(
    'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
    'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
    '/usr/bin/google-chrome', '/usr/bin/chromium', '/usr/bin/chromium-browser',
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  );
  return candidates.find((c) => c && existsSync(c));
}

function rasterise(browser, svgPath, pngPath, width, height) {
  const run = spawnSync(browser, [
    '--headless', '--disable-gpu', '--hide-scrollbars', '--no-first-run',
    `--screenshot=${pngPath}`, `--window-size=${width},${height}`,
    '--force-device-scale-factor=2', '--default-background-color=00000000',
    pathToFileURL(svgPath).href,
  ], { encoding: 'utf8', timeout: 60_000 });
  if (!existsSync(pngPath)) fail(`browser produced no PNG for ${svgPath}:\n${run.stdout}${run.stderr}`);
}

// --- main ------------------------------------------------------------------

const { scenes, plain } = record();
mkdirSync(out, { recursive: true });

const margin = 48;
const timeline = promo(scenes, plain).frames;
const files = [];
for (const [name, rows, label] of stills(scenes, plain)) {
  const svg = screenSvg(rows, { cols: COLS, margin, label });
  const path = join(out, `${name}.svg`);
  writeFileSync(path, svg);
  const { w, h } = svg.match(/width="(?<w>\d+)" height="(?<h>\d+)"/).groups;
  files.push({ path, png: join(out, `${name}.png`), w: +w, h: +h });
}
writeFileSync(join(out, 'plonk-run.svg'), animatedSvg(timeline, { cols: COLS, rowCount: ROWS }));

const posterPath = join(out, 'promo-poster.svg');
writeFileSync(posterPath, poster(stillView(scenes.run1, 'running')));
files.push({ path: posterPath, png: join(out, 'promo-poster.png'), w: 1600, h: 900 });

const browser = findBrowser();
if (!browser) {
  console.warn('No Chromium-based browser found (set PLONK_BROWSER); wrote SVGs only.');
} else {
  for (const f of files) rasterise(browser, f.path, f.png, f.w, f.h);
}
const seconds = (timeline.reduce((sum, f) => sum + f.ms, 0) / 1000).toFixed(1);
console.log(`Wrote ${files.length} stills, the ${seconds} s loop and the poster to ${out}`);
