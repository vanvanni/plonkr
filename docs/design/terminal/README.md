# Terminal screenshots and promo

Status: **proposal, not yet approved or used on the website.** These are design exports. Nothing in `docs/` links to them until the owner picks which to ship.

They show what the real `plonk` CLI prints, not a mock-up. The recorders in [`ui/screens_test.go`](../../../ui/screens_test.go) and [`cmd/screens_test.go`](../../../cmd/screens_test.go) drive the real runner, terminal UI model, plain printer and run summary with scripted target output. No SSH connection is made and nothing is installed anywhere. The demo profile is [demo-profile.hcl](demo-profile.hcl) and the scripted command output is [demo-output.json](demo-output.json). The commands, paths and git output in them are illustrative. The `User app ready` and `Timezone set to …` messages are the ones the provisioning scripts really print.

## What it makes

Files land in [`../exports/terminal/`](../exports/terminal/). Window chrome uses the site's tokens (`--bg-raised`, `--panel`, `--line-strong`, 12 px radius, three dots).

| File | What it shows |
| --- | --- |
| `plonk-run.svg` | Looping promo, about 26 s and 54 KB: type the command, watch the terminal UI work through four tasks, see the summary, run it again and watch two tasks get bypassed by their locks. |
| `tui-running.svg` / `.png` | Terminal UI mid-run: spinner, completed and pending tasks, live output. |
| `tui-finished.svg` / `.png` | Terminal UI after a first run, every task ticked. |
| `tui-repeat.svg` / `.png` | Terminal UI on a repeat run: `↷ bypassed` rows. |
| `plain-first-run.svg` / `.png` | `--no-tui` output of a first run, with the run summary. |
| `plain-repeat.svg` / `.png` | `--no-tui` output of a repeat run, including `bypassed by lock: …` reasons. |
| `promo-poster.svg` / `.png` | 1600 × 900 card: lockup, "Make every new machine yours.", beta line, and the running terminal. The 2× PNG is 3200 × 1800. |

Stills have a transparent background with a soft shadow, so they sit on any page colour. PNGs are 2×.

## Rebuilding

From the repository root, with Go and Node 24:

```sh
node docs/design/terminal/build.mjs
```

The script runs the two recorders, composes the frames and writes everything above. PNGs need a Chromium-based browser: set `PLONK_BROWSER` to its executable, or let the script look in the usual places (Playwright's Chromium, Chrome, Edge). Without one it still writes the SVGs. To change what is shown, edit the demo profile or the scripted output and rebuild; to change the look, edit [render.mjs](render.mjs).

To add or refresh the Figcraft frames:

```sh
node docs/design/terminal/figcraft.mjs
```

This does not regenerate the document. It opens `plonkr-identity.figcraft` in a headless `figcraft-cli` session (put it on PATH or set `FIGCRAFT_CLI`), replaces only the three frames it made earlier, and saves the file back. The existing frames and any manual edits to them are untouched, and the running Figcraft app is not used. Export the frames afterwards with `figcraft-cli export docs/design/plonkr-identity.figcraft --node "Terminal screens" --out "docs/design/exports/identity/Terminal screens.png"`, and likewise for the other two.

Running `create-identity.mjs` builds a new file and drops these frames, so run `figcraft.mjs` after it. Hand edits made inside the three terminal frames are replaced the next time `figcraft.mjs` runs; copy them somewhere first.

The recorders are behind the `screens` build tag and skip unless `PLONK_SCREENS_OUT` is set, so `go test ./...` never runs them.

## Using them on the website

- Give every image a meaningful `alt`. The SVGs carry an `aria-label`, but an `<img>` needs its own `alt`.
- The animation is plain SMIL: no script, no font requests. Show it as `<img src="plonk-run.svg">`. For visitors who ask for less motion, use a still:

  ```html
  <picture>
    <source media="(prefers-reduced-motion: reduce)" srcset="tui-repeat.png">
    <img src="plonk-run.svg" alt="plonk running a profile, then running it again and bypassing the finished tasks" width="888" height="524">
  </picture>
  ```

- The loop runs for longer than five seconds and cannot be paused. WCAG 2.2 criterion 2.2.2 asks for a way to pause moving content of that kind. Decide whether to accept that, add a control, or use the stills only.
- Copy the files you use into `docs/assets/`; `docs/design/` is excluded from the published build.

## Things to decide or know

- **Colours differ from the landing page's terminal.** The real UI uses green ticks (ANSI 78), a violet spinner and prompt (ANSI 99, `#875FFF`) and orange for bypassed (ANSI 214). The hero terminal on the site uses Violet `#B5A0FF` ticks on purpose, because the identity says violet never means success. Authentic captures and that rule disagree. Either keep these as an honest picture of the CLI, or change the CLI palette; this build does not decide that.
- **The hero terminal says "concept preview"; these do not.** They are real output, but the product is beta and the output may change before stable.
- **Fonts.** The SVGs use the site's mono stack, so they look slightly different per machine. The PNGs use whatever the builder had; the committed ones were made with Cascadia Mono. Every text run is placed at its own column, so a different font cannot misalign the grid.
- **In Figcraft, as an editable copy.** [plonkr-identity.figcraft](../plonkr-identity.figcraft) has three frames added below the existing ones: `Terminal screens` (the five stills as real text layers, the colours of the UI, usage notes), `Promo poster 1600x900` and `Promo storyboard` (six moments of the loop with their times). Their PNG exports are in [../exports/identity/](../exports/identity/). The SVG and PNG files above remain the source for the website; the frames are for layout and review, and can be edited by hand.
- **Verified:** the PNGs and several time offsets of the animation were rendered and inspected in headless Chromium. **Not verified:** Safari, Firefox, real mobile devices, or a page that embeds them.
