# Plonkr identity

Status: **Pegs is the selected direction**, accepted for now by the owner on 2026-11-26. The requested final wordmark polish is complete. Branding is ready for project work; product behaviour and v0.1.0 scope still depend on proof-of-concept review.

## Mark: Pegs

Three pegs are seated on a pegboard; the fourth socket is empty, ready for the next plonk. The symbol represents putting setup in place on an existing machine. The geometry lives in [mark.mjs](mark.mjs).

- A 16-unit square contains four circle centres at (4,4), (4,12), (12,12), (12,4).
- The three solid discs have radius 3. The empty socket has outer radius 3 and a 1.5-unit ring.
- Visible bounds are 14 × 14 units, inset one unit from the square.
- Use 16, 32, 48 or 96 px for integer scaling. Curves are normally antialiased; pixel alignment does not mean jagged circles.
- On dark: Violet #B5A0FF pegs, #8A74E0 socket. On light: #8A74E0 pegs, #6A55C2 socket. On Violet: #4B3B96 pegs, Ink #171020 socket.
- Keep one peg diameter of clear space where possible. Do not stretch the mark, rearrange the circles, or use them as application status indicators.

## Wordmark and lockup

The wordmark is custom vector lettering with a solid violet pebble replacing the o. It contains no font dependency. [wordmark.mjs](wordmark.mjs) is its source of truth; Figcraft and the website use the same paths.

At the base size: 3 px stems, 11 px x-height, 15 px ascender, 4 px descender, and a 10 px pebble resting on the baseline. Pair gaps are p–l 3 px, l–o 3 px, o–n 3 px, n–k 4 px, k–r 1.5 px. The tighter final pair compensates for the open diagonal of the k. The letter origins and vertical stems land on whole pixels; curves and diagonals retain normal antialiasing.

The full lockup is **105 × 32 px**: a 32 px mark box, 8 px visible gap to the lettering, and baseline y=22. The wordmark begins at x=38 and is 67 px wide. The website pulls the SVG left by 2 px so the pegs align with the content column. Use the supplied 1×, 2× or 4× SVGs, or another whole-number scale. Do not retype the wordmark or adjust individual glyphs in the HTML.

## Voice

Plonkr is the product; plonk is the executable. Headline: **Make every new machine yours.** Be plain, specific, and a little wry. Do not invent command syntax, configuration, timings or compatibility. Terminal previews are illustrative; the command placeholder remains `plonk # syntax pending` until the proof of concept is reviewed.

## Spacing and grid

Everything sits on a **4 px grid**, with an **8 px rhythm** for larger steps.

- Scale: 4, 8, 12, 16, 20, 24, 32, 40, 48, 64, 80, 96, 112. Hairlines (1–2 px) and pill radii are exempt.
- Radii: 4 (inline code), 8 (buttons, controls, notices), 12 (panels, panes, terminal). Pills use 999 px.
- Header: 64 px high, content width 1160 px with 32 px gutters (20 px under 820 px). Docs button 32 px high with 12 px side padding; hero buttons 48 px high with 24 px side padding. Nav links are 36 px tall (20 px line plus 8 px padding), 12 px side padding.
- Line heights are whole pixels: 20 and 24 for UI and small text, 28 for prose and sub-headings, 32 for the lead. Fluid display headings are the one place line height is a ratio.
- Terminal: 40 px title bar, 48 px tabs, 24 px padding, 28 px mono lines, 304 px output area.
- Section padding is 104 px (68 px under 820 px). Hero padding 80 / 96 px.

The site enforces this: `npm run build` fails on any layout length in `site.css` that is not a multiple of 4 px unless the declaration is followed by a comment starting `og:` that gives the optical reason.

## Colour

| Token | Value | Role |
| --- | --- | --- |
| Graphite | `#0D0E12` | Page |
| Raised | `#12131A` | Terminal, panes, footer |
| Surface | `#17181F` | Title bars, table headers |
| Violet deep | `#252033` | Notices, emphasised panes |
| Hairline | `#24252F` | Quiet rules |
| Boundary | `#30313D` | Borders |
| Text | `#F3F2F7` | Primary text |
| Secondary | `#B0AFBF` | Body copy |
| Muted | `#8C8B9D` | Labels and captions (was `#7D7C8E`; raised for contrast) |
| Violet | `#B5A0FF` | Brand and action |
| Ink | `#171020` | Text on violet |

Contrast on Graphite: Text about 17:1, Secondary 8.9:1, Violet 8.7:1, Muted 5.8:1. Violet is the brand and the primary action; it never means success or failure. Pegs use Violet; the empty socket uses #8A74E0. The logo has its own outlined lettering; these text-font rules apply to page content.

## Type

Inter is the design font and Liberation Mono is used for machine text in Figcraft. The website uses system stacks (Inter, JetBrains Mono, Cascadia and Consolas when installed) with no third-party font requests. Display headings are Semi Bold with tight tracking above 40 px; body text is 16 px; metadata is 12–14 px.


## Motion and applications

The plonk drops a temporary fourth peg into the socket, squashes it briefly, then fades it out. The socket expands slightly and returns to its original size. It runs once for 1.5 seconds on logo hover or when the closing section enters view, and is disabled for reduced motion. The other three pegs stay still.

The identity is applied to the website header and footer, closing illustration, SVG/ICO favicon, 180 px touch icon, 512 px app icon and 1200 × 630 social card. All are derived from the same geometry and [Figcraft file](plonkr-identity.figcraft). [Web implementation notes](web-implementation.md) describe the remaining interactions and layout.

## Sources and verification

- [Editable identity](plonkr-identity.figcraft) and [PNG exports](exports/identity/).
- [Vector assets](assets/identity/), including mark variants, wordmark and lockups.
- [Design generator](create-identity.mjs), [asset builder](build-brand-assets.mjs).
- Static build validates three pages, local links/assets and the CSS spacing grid.
- The preceding implementation pass checked Chromium layouts at 320–1920 px, mobile menu and terminal preview. Other browsers and real devices remain unchecked.
- Final identity export visually inspected; outlined wordmark and applied website assets verified. No deployment or DNS changes have been made.

The [design index](README.md) lists the current source files and assets. Superseded explorations have been removed; Pegs is the standard for future work.
