// Run after build.mjs. Transform only generated website output for the preview domain.
import { readdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
const output = resolve(import.meta.dirname, '../dist');
async function transform(dir) {
  for (const item of await readdir(dir, { withFileTypes: true })) {
    const file = resolve(dir, item.name);
    if (item.isDirectory()) await transform(file);
    else if (/\.(html|xml|webmanifest)$/.test(item.name)) {
      let body = (await readFile(file, 'utf8')).replaceAll('https://plonkr.dev', 'https://prerelease.plonkr.dev');
      if (item.name.endsWith('.html')) {
        body = body.replace('</head>', '<meta name="robots" content="noindex,nofollow">\n</head>');
        body = body.replace('<body>', '<body>\n<div class="notice" role="note"><p><strong>Prerelease branch preview.</strong> Beta software; no stable release yet. <a href="https://plonkr.dev">Main site</a>.</p></div>');
      }
      await writeFile(file, body);
    }
  }
}
await transform(output);
await writeFile(resolve(output, 'CNAME'), 'prerelease.plonkr.dev\n');
await writeFile(resolve(output, 'robots.txt'), 'User-agent: *\nDisallow: /\n');
console.log('Prepared prerelease.plonkr.dev preview with noindex and its own CNAME.');
