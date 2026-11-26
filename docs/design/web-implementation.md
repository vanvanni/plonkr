# Web implementation notes

Status: implemented website reference with the selected Pegs identity. Product demonstrations remain proposals. This document records what the Figcraft frames cannot express and how the site in [`plonkr/docs/`](../) builds it. The same material is drawn in the frame [Web implementation notes](exports/identity/Web%20implementation%20notes.png). Treat the HTML, CSS and JS in `plonkr/docs/` as the reference implementation; if the site and this file disagree, fix whichever is wrong.

Nothing here selects a CLI language, command grammar, configuration format, licence or release scope. The terminal text on the site is illustrative.

## What Figcraft can and cannot hold

| Thing | In Figcraft | On the web |
| --- | --- | --- |
| Layout, colour, type, radii, borders | Yes, exact | CSS custom properties in `assets/site.css` |
| Gradients, drop shadows, glows | Yes (radial gradient fills, drop-shadow effects) | `radial-gradient`, `box-shadow` |
| Grain | Noise effect, approximate | Fixed `feTurbulence` SVG data URI, see below |
| Frosted sticky header | Background-blur layer (static) | `position: sticky` + `backdrop-filter` |
| Pegboard dot texture | A few dozen ellipses stand in | One `radial-gradient` tiled at 28 px, masked |
| Pebble drop, terminal reveal, hover, focus | Storyboard frames only | CSS keyframes, a small script |
| Responsive reflow | Separate desktop and mobile frames | Breakpoints at 1100, 820 and 560 px |
| Tabs, menu disclosure, replay | No | Script plus ARIA, see below |
| Real fonts | Inter and Liberation Mono | System stacks (Inter if installed); no font requests |

## Design tokens

All colours are CSS custom properties on `:root` in `assets/site.css`.

| Token | Value | Use |
| --- | --- | --- |
| `--bg` | `#0D0E12` | Page (Graphite) |
| `--bg-raised` | `#12131A` | Terminal, panes, footer |
| `--panel` | `#17181F` | Surface: title bars, docs table header |
| `--violet-deep` | `#252033` | Notices, the "with Plonkr" pane |
| `--line` / `--line-strong` | `#24252F` / `#30313D` | Hairlines / boundaries |
| `--text` | `#F3F2F7` | Primary text (about 17:1 on Graphite) |
| `--secondary` | `#B0AFBF` | Body copy (about 8.9:1) |
| `--muted` | `#8C8B9D` | Labels, captions (about 5.8:1) |
| `--accent` | `#B5A0FF` | Brand, primary action (about 8.7:1) |
| `--ink` | `#171020` | Text on violet |

Violet marks the brand and the primary action. It never means success or failure; ticks in the terminal are violet on purpose.

Type: `--font` is `Inter, ui-sans-serif, system-ui, "Segoe UI", Roboto, …`; `--mono` is `"JetBrains Mono", ui-monospace, "Cascadia Code", "SF Mono", Menlo, Consolas, "Liberation Mono"`. Display headings are weight 650 with `letter-spacing` between −0.04 em and −0.05 em; body is 16 px/1.65. Bundling Inter (and a mono) as self-hosted WOFF2 is a later refinement and the only change needed is an `@font-face` block.

## The mark and wordmark

The current logo is Pegs with a custom outlined wordmark. [mark.mjs](mark.mjs) and [wordmark.mjs](wordmark.mjs) generate one 105 × 32 px SVG lockup for the header/footer, with an 8 px visible gap and a 2 px optical left inset correction. It contains no text/font elements. Use whole-number scaling for the lockup; intrinsic SVG dimensions intentionally need not match the page's 4 px layout grid.

The standalone mark uses a 16-unit viewBox. Three filled discs and one ring share a 3-unit outer radius. The classes are `peg`, `socket`, `demo` (the normally hidden animation peg), `wm` and `wm-o`. The closing mark is 112 px. Palettes and geometry are documented in [branding.md](branding.md).

Favicons (SVG and ICO with 16/32/48 px PNG entries), touch icon, app icon, social card, vector wordmark and 1×/2×/4× lockups are generated into the current identity directories. Earlier block assets are historical.

## Layout and responsive behaviour

Content width is 1160 px including 32 px gutters (20 px below 820 px). Spacing sits on a 4 px grid (see below). Breakpoints:

| Width | Change |
| --- | --- |
| ≤ 1100 px | Hero stacks (copy above the terminal). "How it goes" heading stops being sticky. Docs loses the right-hand "On this page" column. Footer goes to two columns. |
| ≤ 820 px | Header becomes a logo plus **Menu** button; the nav opens as a full-width panel (`aria-expanded`, closes on link click and Escape). Compare panes, is/isn't columns and status rows stack. Step sketches are hidden. Docs sidebar becomes wrapped pill links. |
| ≤ 560 px | Hero buttons go full width. Terminal tabs show the machine name above its kind. Terminal and pane type shrinks one step. Footer is one column. |

The page is checked for horizontal overflow at 320, 360, 390, 414, 600, 768, 820, 1024, 1100, 1280, 1440 and 1920 px. Without JavaScript the nav is always visible and the header is not sticky on small screens.

## Spacing grid

All layout lengths in `site.css` (padding, margin, gap, width, height, offsets, radii, grid columns) are multiples of 4 px; hairlines of 1–2 px and 999 px pills are exempt. Radii are 4, 8 or 12. Line heights are whole pixels (20/24 UI, 28 prose, 32 lead). `scripts/build.mjs` enforces this: `npm run build` and `npm run check` fail with the offending declarations unless a declaration is followed by a comment starting `og:` that states the optical reason. The wordmark geometry lives in SVG, outside the page spacing grid. The full table of values is in [branding.md](branding.md).

## Components

### Header and the Docs button

64 px bar, `position: sticky`, background `rgba(13,14,18,.92)` with `backdrop-filter: blur(12px) saturate(1.3)`, 1 px bottom border. The **Docs** button is `btn-sm`: 32 px high, 13 px side padding, 7 px radius, 13.5 px semibold text, vertically centred with the nav links by the flex container (no manual offsets). Hero buttons are 48 px high with 24 px side padding. The nav links are 36 px tall. Hover `#C7B8FF`; arrow glyphs nudge 3 px right.

### Hero

Two columns (5fr / 7fr, 56 px gap), vertically centred. Left: a dictionary-style definition line, the headline, lead, one primary button and one text link. The word "yours." is violet with a hand-drawn underline (below). Right: the terminal window. A pegboard texture sits behind the right side and fades out toward the lower left.

### Pegboard

`background-image: radial-gradient(circle, rgba(176,175,191,.34) 1.1px, transparent 1.6px); background-size: 28px 28px;` on a `::before` pseudo-element, masked with `mask-image: radial-gradient(80% 90% at 85% 25%, #000 0, transparent 70%)` and `opacity: .55`. It is decorative and ignored by assistive technology.

### Terminal preview

Markup is a window with a title bar, a WAI-ARIA `tablist` of three machines (lab-ct · LXC, dev-vm · VM, rack-01 · bare metal), one `tabpanel` per machine, and a footer disclaimer. Every line of output is in the HTML, so the content is complete without JavaScript.

With JavaScript (`assets/site.js`):

1. `<html>` gets the class `js`. Lines marked `.reveal-line` start at `opacity: 0; transform: translateY(3px)` (only when the user has not asked for reduced motion).
2. The first line (the prompt) is always visible. Each later line gets class `in` after `260 ms + 360 ms × index`, which transitions to `opacity: 1; transform: none` over 250 ms.
3. The sequence starts when the window is on screen at load, otherwise when 20% of it enters the viewport (`IntersectionObserver`).
4. Selecting a tab (click, arrow keys, Home, End) swaps panels and replays. The **replay** button replays the current panel.
5. With `prefers-reduced-motion: reduce`, nothing is hidden and the cursor does not blink.

The "still deciding this bit" aside is an `aria-hidden` paragraph with an inline SVG arrow, absolutely positioned at the top right of the output area, `font-style: italic`, rotated −1.5°. It is hidden at 820 px and below.

### Hand-drawn underline

An inline `<svg viewBox="0 0 300 12" preserveAspectRatio="none">` with one wobbly path, `stroke-width: 4`, round caps, `vector-effect: non-scaling-stroke`, positioned `left: -2%; bottom: -.16em; width: 104%`. Do not replace it with `text-decoration`; the irregularity is the point.

### Compare panes, steps, scope, status

- Compare panes are `<pre>` blocks with coloured `<span>`s; the right pane has a violet-deep gradient and a violet border at 38% opacity.
- Steps are an `<ol>` with CSS counters in violet monospace, hairline separators, and a small hand-drawn SVG sketch per step (stroke 1.5 px, slightly crooked lines).
- Scope is two plain lists with ✓ and – markers. Each row is a hairline-separated line, not a card.
- Status is an `<ol>` of rows with a state marker: filled dot (settled), ringed dot with a soft halo (in review), hollow dot (next, later).

### The plonk (logo hover and closing section)

Hovering the logo or revealing the closing section plays a 1.5-second animation once. A temporary fourth peg drops into the empty socket, squashes at impact, remains briefly, then fades out. The socket expands to 1.18× and returns. Three permanent pegs stay still. CSS `--fall` is -16 px in the lockup and -9 units in the standalone mark; each uses its own SVG coordinate space. Disabled under reduced motion.

### Grain

A `body::before` layer, `position: fixed; inset: 0; opacity: .045; mix-blend-mode: screen; pointer-events: none`, painting a 160 px `feTurbulence` SVG (baseFrequency .9, two octaves) from a data URI. It is invisible at a glance and takes the digital sheen off large dark areas. Remove it if it ever costs paint performance on low-end devices.

### Docs page

Three columns on wide screens: sidebar (210 px), article (max 720 px), "On this page" (180 px), both side columns sticky below the header. Article headings carry `scroll-margin-top` so anchors clear the sticky header. Tables scroll horizontally inside `.table-wrap` instead of breaking the page. Notices use a violet-deep fill and a small "i" disc.

## Keeping it from looking generated

These decisions are deliberate; keep them when extending the site.

- Left-aligned, asymmetric hero rather than a centred headline over a grid with a glow.
- Section headings written as sentences, not labelled with an eyebrow, number or chip on every block. Monospace labels are used sparingly.
- No identical three-card grids. Steps, scope and status use ruled rows; the compare section uses two unequal-looking panes with real content.
- A few imperfect, hand-made marks (underline, arrow aside, sketches) in a system that is otherwise exact.
- Plain, specific, a bit wry copy that says what is not decided. No hype words, no lists of three adjectives, no claims the proof of concept has not earned.
- Texture is physical (pegboard slots, paper grain), not a gradient mesh.

## Performance and accessibility

- No third-party requests; system font stacks only; one CSS file (about 22 KB) and one script (under 4 KB), deferred.
- Content is readable and navigable with JavaScript off.
- Contrast on Graphite: Text 17:1, Secondary 8.9:1, Violet 8.7:1, Muted 5.8:1. Violet on Ink buttons is above 8:1.
- Visible focus: 2 px violet outline, 3 px offset. Skip link on every page.
- Decorative SVG, the pegboard, the grain and the aside are `aria-hidden` or pseudo-elements.
- `prefers-reduced-motion: reduce`: no blink, no reveal, no pebble drop, no smooth scroll, no transitions.

## Regenerating assets

1. Edit the mark geometry in `mark.mjs` (repository `docs/design/`) or the layout in `create-identity.mjs`, run the generator from the repository root, then run `figcraft-session.mjs` with the generated commands. Use the generated identity-commands.json with figcraft-session.mjs.
2. Export the frames with `figcraft-cli export docs/design/plonkr-identity.figcraft --out docs/design/exports/identity --format png`.
3. Run `node docs/design/build-brand-assets.mjs`. It writes the vector marks, builds `favicon.ico` from the 16, 32 and 48 px Figcraft exports, copies `og.png`, `apple-touch-icon.png` and `icon-512.png` into `docs/assets/` (relative to the repository), writes `mark.svg` and `favicon.svg`, and patches the inline logo SVG in every page.
4. The 16, 32 and 48 px favicon frames in Figcraft must stay exactly that size with the Pegs mark at its native grid position, or the ICO is not pixel-exact.
5. From the repository root, run `npm run build` to validate links, anchors and assets.

## Verified and not verified

### Public guide expansion, 2026-11-26

The guide now has six static pages: overview, profile locations, configuration
and tasks, repository SSH keys, execution/results and examples. They reuse the
existing header lockup, navigation, sidebar, table-of-contents and footer styling.
Documentation code blocks use the existing palette, mono font and spacing grid,
with horizontal scrolling for long lines. No identity assets, landing-page content
or terminal behavior were changed.

The guide distinguishes agreed requirements from proposed HCL syntax and existing
prototype behavior. Windows profile storage is selected as `%ProgramData%\plonkr\`;
the Linux location remains proposed. No claim of an installable release or verified
new-format CLI is made. Design sources remain excluded from the site build.

Validation: `npm run check` and `npm run build` passed (8 HTML pages, 185 local
links/assets). New guide pages match their generated output; `dist/design/` and
`dist/AGENTS.md` are absent. The new pages were checked for workspace-path links.
Prior browser layout verification does not extend to the expanded guide; its responsive layout
and assistive-technology behavior have not been newly verified in a browser.

Verified: static build validation (links, anchors, assets, 4 px grid); the preceding implementation pass checked headless Chromium layouts at 320–1920 px with no horizontal overflow, mobile menu and terminal reveal. Current Pegs identity and wordmark were exported from Figcraft and inspected. The current lockup is outlined SVG and does not depend on fonts.

Not verified: Safari, Firefox and real mobile devices; screen-reader behaviour of the tabs and menu; Lighthouse and Core Web Vitals; behaviour with Inter and JetBrains Mono actually installed; deployment to GitHub Pages and DNS for plonkr.dev.
