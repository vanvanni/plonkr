import { readdir, readFile, writeFile, stat, mkdir, cp } from 'node:fs/promises';
import { resolve, dirname, relative, sep } from 'node:path';
const root = resolve(import.meta.dirname, '..');
const source = resolve(root, 'docs');
async function walk(dir) { const entries = await readdir(dir,{withFileTypes:true}); return (await Promise.all(entries.map(e=>e.isDirectory()?walk(resolve(dir,e.name)):resolve(dir,e.name)))).flat(); }
// Publish only website inputs; docs/design contains editable sources and private build tooling.
const publicEntries = ['index.html', '404.html', 'guide', 'assets', 'CNAME', '.nojekyll', 'robots.txt', 'sitemap.xml'];
const files = (await Promise.all(publicEntries.map(async name => {
  const file = resolve(source, name);
  return (await stat(file)).isDirectory() ? walk(file) : [file];
}))).flat().filter(f=>!f.endsWith('.md'));
const html = new Map(await Promise.all(files.filter(f=>f.endsWith('.html')).map(async f=>[f,await readFile(f,'utf8')])));
let links=0;
for (const [file,body] of html) {
  if (!/<html lang="en"/.test(body) || !/<title>/.test(body) || !/name="viewport"/.test(body)) throw new Error(`Missing page metadata: ${file}`);
  for (const [,url] of body.matchAll(/(?:href|src)="([^"]+)"/g)) {
    if (/^(?:https?:|mailto:|data:)/.test(url)) continue;
    const [pathname,fragment] = url.split('#');
    let target = pathname ? resolve(pathname.startsWith('/')?source:dirname(file),'.'+(pathname.startsWith('/')?'':sep)+pathname) : file;
    if (relative(source,target).startsWith('..')) throw new Error(`Link escapes site: ${url}`);
    if ((await stat(target)).isDirectory()) target=resolve(target,'index.html');
    await stat(target);
    if (fragment && html.has(target) && !html.get(target).includes(`id="${fragment}"`)) throw new Error(`Missing anchor ${url} in ${file}`);
    links++;
  }
}

// Spacing grid: layout lengths in the stylesheet sit on a 4 px grid (1-2 px hairlines and 999px pills excepted).
// Mark a deliberate optical exception by following the declaration with a comment that starts "og:" (off-grid) and gives the reason.
{
  const css = (await readFile(resolve(source, 'assets/site.css'), 'utf8')).replace(/[^;{}]*;\s*\/\*\s*og:[^*]*\*\//g, '');
  const props = /(?:^|[;{\s])(padding(?:-[a-z]+)?|margin(?:-[a-z]+)?|gap|row-gap|column-gap|top|right|bottom|left|inset|width|height|min-height|border-radius|grid-template-columns):([^;}]*)/g;
  const bad = [];
  for (const [, prop, value] of css.matchAll(props)) {
    for (const [, num] of value.matchAll(/(-?\d+(?:\.\d+)?)px/g)) {
      const n = Math.abs(parseFloat(num));
      if (n > 2 && n !== 999 && n % 4 !== 0) bad.push(`${prop}: ${value.trim().slice(0, 50)} (${num}px)`);
    }
  }
  if (bad.length) throw new Error(`Off-grid spacing in site.css (use multiples of 4 px, or mark an "og:" exception):\n  ${[...new Set(bad)].join('\n  ')}`);
}
if ((await readFile(resolve(source,'CNAME'),'utf8')).trim()!=='plonkr.dev') throw new Error('Unexpected domain');

// HCL highlighting: <pre><code class="language-hcl"> blocks get token spans at build time, so the site needs no JS for it.
const hclTokens = [
  ['c', /#[^\n]*|\/\/[^\n]*/y],                           // comment
  ['s', /"(?:[^"\\\n]|\\.)*"/y],                           // string
  ['n', /\b(?:\d+(?:\.\d+)?|true|false|null)\b/y],         // number, bool, null
  ['f', /[A-Za-z_][\w-]*(?=\()/y],                         // function call
  ['k', /[A-Za-z_][\w-]*(?=\s*=(?!=))/y],                  // attribute key
  ['b', /(?<=^|\n)\s*[A-Za-z_][\w-]*(?=\s+(?:"|\{))/y],    // block type
];
const unescapeHtml = s => s.replace(/&lt;/g,'<').replace(/&gt;/g,'>').replace(/&quot;/g,'"').replace(/&#39;/g,"'").replace(/&amp;/g,'&');
const escapeHtml = s => s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
function highlightHcl(code) {
  let out = '', plain = '', i = 0;
  const flush = () => { out += escapeHtml(plain); plain = ''; };
  scan: while (i < code.length) {
    for (const [cls, re] of hclTokens) {
      re.lastIndex = i;
      const m = re.exec(code);
      if (!m || !m[0]) continue;
      flush();
      const lead = m[0].match(/^\s*/)[0];
      out += escapeHtml(lead) + `<span class="hcl-${cls}">${escapeHtml(m[0].slice(lead.length))}</span>`;
      i += m[0].length;
      continue scan;
    }
    plain += code[i++];
  }
  flush();
  return out;
}
const highlightPage = body => body.replace(/(<pre><code class="language-hcl">)([\s\S]*?)(<\/code><\/pre>)/g, (_, open, code, close) => open + highlightHcl(unescapeHtml(code)) + close);

if (!process.argv.includes('--check')) {
  const output=resolve(root,'dist'); await mkdir(output,{recursive:true});
  for (const file of files) {
    const target=resolve(output,relative(source,file));
    await mkdir(dirname(target),{recursive:true});
    if (html.has(file)) await writeFile(target, highlightPage(html.get(file)));
    else await cp(file,target);
  }
}
console.log(`Validated ${html.size} HTML pages and ${links} local links/assets${process.argv.includes('--check')?'':'; built dist/'}.`);
