// What the terminal assets show: records the real CLI through the build-tagged
// Go recorders, then composes the stills and the promo timeline from the frames.
// Shared by build.mjs (SVG and PNG) and figcraft.mjs (the Figcraft file).

import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', '..');
export const COLS = 100;
export const ROWS = 22;

export const fail = (message) => { console.error(message); process.exit(1); };

/** Runs the recorders and returns { scenes, plain }. No SSH, nothing installed. */
export function record() {
  const data = mkdtempSync(join(tmpdir(), 'plonkr-screens-'));
  try {
    for (const [pkg, test] of [['./ui', 'TestRecordScreens'], ['./cmd', 'TestRecordPlainScreens']]) {
      const run = spawnSync('go', ['test', '-tags', 'screens', '-count=1', '-run', test, pkg], {
        cwd: root, env: { ...process.env, PLONK_SCREENS_OUT: data }, encoding: 'utf8',
      });
      if (run.status !== 0) fail(`go test ${pkg} failed:\n${run.stdout}${run.stderr}`);
    }
    return {
      scenes: JSON.parse(readFileSync(join(data, 'ui-frames.json'), 'utf8')),
      plain: JSON.parse(readFileSync(join(data, 'plain-output.json'), 'utf8')),
    };
  } finally {
    rmSync(data, { recursive: true, force: true });
  }
}

const ACCENT_256 = 147; // closest xterm colour to Violet #B5A0FF
export const prompt = (text = '', cursor = false) =>
  `\x1b[38;5;${ACCENT_256}m$ \x1b[0m${text}` + (cursor ? '\x1b[38;5;250m█\x1b[0m' : '');

export const viewRows = (view) => view.replace(/\n$/, '').split('\n');

export function padTo(rows, count) {
  if (rows.length > count) fail(`screen has ${rows.length} rows, expected at most ${count}`);
  return [...rows, ...Array(count - rows.length).fill('')];
}

/** The summary a real run prints after the terminal UI closes. */
export function summaryOf(plain) {
  const lines = plain.replace(/\n+$/, '').split('\n');
  return lines.slice(lines.findIndex((l) => l.startsWith('plonk · ')));
}

/**
 * A screenshot ends one row below its last line instead of showing the empty
 * rest of a full-height terminal.
 */
export function stillView(scene, name) {
  const index = scene.stills[name];
  if (index === undefined) fail(`scene has no still named ${name}`);
  const rows = viewRows(scene.frames[index].view);
  while (rows.length > 0 && rows.at(-1).trim() === '') rows.pop();
  return [...rows, ''];
}

const plainRows = (text, command) => [prompt(command), ...text.replace(/\n+$/, '').split('\n')];

/** The named stills: [name, rows, accessible description]. */
export function stills(scenes, plain) {
  return [
    ['tui-running', stillView(scenes.run1, 'running'), 'plonk running a profile: two tasks done, one in progress'],
    ['tui-finished', stillView(scenes.run1, 'finished'), 'plonk after a first run: every task completed'],
    ['tui-repeat', stillView(scenes.run2, 'finished'), 'plonk on a repeat run: two tasks bypassed because their lock holds'],
    ['plain-first-run', plainRows(plain.run1, 'plonk run lab-ct web --no-tui'), 'plain output of a first run'],
    ['plain-repeat', plainRows(plain.run2, 'plonk run lab-ct web --no-tui'), 'plain output of a repeat run, with the lock reasons'],
  ];
}

/**
 * The looping promo: type the command, watch the UI, see the summary, then
 * run it again. Returns { frames: [{ ms, rows }], marks: [{ name, index }] };
 * marks point at the frames worth showing in a storyboard.
 */
export function promo(scenes, plain) {
  const frames = [];
  const marks = [];
  const push = (ms, rows) => frames.push({ ms, rows: padTo(rows, ROWS) });
  const mark = (name) => marks.push({ name, index: frames.length - 1 });
  let shell = []; // what the main screen holds once the UI has closed

  const type = (command) => {
    for (let i = 1; i <= command.length; i++) {
      push(i === command.length ? 400 : 45, [...shell, prompt(command.slice(0, i), true)]);
    }
  };
  const run = (n, command, scene, summary) => {
    type(command);
    mark(`typed-${n}`);
    const last = scene.frames.length - 1;
    scene.frames.forEach((frame, i) => {
      push(i === last ? 1200 : frame.ms, viewRows(frame.view));
      if (i === scene.stills.running) mark(`running-${n}`);
      if (i === last) mark(`finished-${n}`);
    });
    // The terminal UI uses the alternate screen; closing it brings the shell
    // back with the summary printed below the command.
    shell = [...shell, prompt(command), '', ...summary];
    push(2000, [...shell, prompt('', true)]);
    mark(`summary-${n}`);
  };

  push(600, [prompt('', true)]);
  mark('start');
  run(1, 'plonk run lab-ct web', scenes.run1, summaryOf(plain.run1));
  run(2, 'plonk run lab-ct web', scenes.run2, summaryOf(plain.run2));
  frames.at(-1).ms += 1800; // linger on the result before the loop restarts
  return { frames, marks };
}
