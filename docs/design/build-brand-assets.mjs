// Writes every vector asset and the favicon.ico from mark.mjs and the Figcraft exports, copies the raster icons
// into the site, and patches the inline lockup and mark in the site's HTML. Run from the repository root after
// create-identity, figcraft-session and the PNG export:
//   node docs/design/build-brand-assets.mjs
import { readFile, writeFile, mkdir, copyFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { svg, inline, lockup, lockupFile, palettes } from './mark.mjs';
import { wordmarkPaths, WORDMARK } from './wordmark.mjs';

const here = import.meta.dirname;
const out = resolve(here, 'assets/identity');
const site = resolve(here, '..');
const exportsDir = resolve(here, 'exports/identity');
await mkdir(out, { recursive: true });

// Vector files: the mark on dark, on light and on violet; the lockup at 1x, 2x and 4x; the wordmark alone.
await writeFile(resolve(out, 'plonkr-mark.svg'), svg({ palette: palettes.onDark }));
await writeFile(resolve(out, 'plonkr-mark-on-light.svg'), svg({ palette: palettes.onLight }));
await writeFile(resolve(out, 'plonkr-mark-on-violet.svg'), svg({ palette: palettes.onViolet }));
for (const k of [1, 2, 4]) {
  await writeFile(resolve(out, `plonkr-lockup-${k}x.svg`), lockupFile({ k, palette: palettes.onDark }));
  await writeFile(resolve(out, `plonkr-lockup-${k}x-on-light.svg`), lockupFile({ k, palette: palettes.onLight }));
}
{
  const w = wordmarkPaths(4, 0, 0), W = Math.ceil(WORDMARK.width * 4), top = 15 * 4, H = 19 * 4;
  const wm = pal => `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 ${-top} ${W} ${H}"><path fill="${pal.text}" d="${w.text}"/><path fill="${pal.pebble}" d="${w.pebble}"/></svg>\n`;
  await writeFile(resolve(out, 'plonkr-wordmark.svg'), wm(palettes.onDark));
  await writeFile(resolve(out, 'plonkr-wordmark-on-light.svg'), wm(palettes.onLight));
}
// App icon tile: the mark on graphite, 512 px, pegs 280 px (scale 20).
await writeFile(resolve(out, 'plonkr-app-icon.svg'),
  `<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512" viewBox="0 0 512 512" fill="none"><rect width="512" height="512" rx="112" fill="#0D0E12"/>` +
  `<g transform="translate(96 96) scale(20)">${svg({ palette: palettes.onDark }).replace(/<svg[^>]*>/, '').replace('</svg>', '').trim().replace(/<circle/g, '<circle').trim()}</g></svg>\n`.replace('translate(96 96)', 'translate(96 96)'));

// Site files: the mark as the manifest icon and the SVG favicon (the mark at 16 px).
await writeFile(resolve(site, 'assets/mark.svg'), svg({ palette: palettes.onDark, size: 256 }));
await writeFile(resolve(site, 'assets/favicon.svg'), svg({ palette: palettes.onDark, size: 16 }));

// favicon.ico: PNG-compressed entries at 16, 32 and 48 px, exported from the pixel-exact Figcraft frames.
const sizes = [16, 32, 48];
const pngs = await Promise.all(sizes.map(s => readFile(resolve(exportsDir, `Favicon ${s}.png`))));
const header = Buffer.alloc(6); header.writeUInt16LE(1, 2); header.writeUInt16LE(sizes.length, 4);
let offset = 6 + 16 * sizes.length;
const entries = sizes.map((s, i) => {
  const e = Buffer.alloc(16);
  e.writeUInt8(s, 0); e.writeUInt8(s, 1); e.writeUInt16LE(1, 4); e.writeUInt16LE(32, 6);
  e.writeUInt32LE(pngs[i].length, 8); e.writeUInt32LE(offset, 12); offset += pngs[i].length;
  return e;
});
await writeFile(resolve(site, 'assets/favicon.ico'), Buffer.concat([header, ...entries, ...pngs]));

// Raster icons used by the site.
await copyFile(resolve(exportsDir, 'Social card 1200x630.png'), resolve(site, 'assets/og.png'));
await copyFile(resolve(exportsDir, 'Apple touch icon 180.png'), resolve(site, 'assets/apple-touch-icon.png'));
await copyFile(resolve(exportsDir, 'App icon 512.png'), resolve(site, 'assets/icon-512.png'));

// Inline lockups in the header and footer; the plain mark (with the plonk pebble) in the closing section.
const logo = /(<a class="logo"[^>]*>)[\s\S]*?(<\/a>)/g;
const cta = /(<div class="cta-mark" aria-hidden="true">)[\s\S]*?(<\/div>)/g;
let patched = 0;
for (const file of ['index.html', 'guide/index.html', '404.html']) {
  const path = resolve(site, file);
  const html = await readFile(path, 'utf8');
  const next = html
    .replace(logo, (_, a, b) => { patched++; return a + lockup({ k: 1 }) + b; })
    .replace(cta, (_, a, b) => { patched++; return a + inline({ size: 112 }) + b; });
  if (next !== html) await writeFile(path, next);
}
console.log(`Wrote vector assets, favicon.ico (${sizes.join('/')}), site icons; patched ${patched} inline marks.`);
