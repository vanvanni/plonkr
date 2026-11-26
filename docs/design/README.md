# Design files

Current identity: **Pegs**, selected by the owner, with the final custom vector wordmark. Branding is ready for project work. Product behaviour remains provisional until proof-of-concept review.

| What | Where |
| --- | --- |
| Identity rules and geometry | [branding.md](branding.md), [mark.mjs](mark.mjs), [wordmark.mjs](wordmark.mjs) |
| Editable Figcraft source | [plonkr-identity.figcraft](plonkr-identity.figcraft) |
| PNG exports | [exports/identity/](exports/identity/) |
| Vector marks, wordmark and lockups | [assets/identity/](assets/identity/) |
| Reproducible design and asset build | [create-identity.mjs](create-identity.mjs), [build-brand-assets.mjs](build-brand-assets.mjs) |
| Web behaviour and regeneration | [web-implementation.md](web-implementation.md) |
| Website | [landing page](../index.html), [guide](../guide/index.html) |

This directory contains only the current standard. Superseded explorations, option sheets, and temporary run files have been removed.

Keep the editable Figcraft file, geometry, generators, and current exports together. Edit `mark.mjs` or `wordmark.mjs` for logo geometry; `create-identity.mjs` defines the design frames. The website's deployed copies live in `docs/assets/` (relative to the repository).

For regeneration, put `figcraft-cli` on PATH (or set `FIGCRAFT_CLI` to its installed executable) and follow [the asset workflow](web-implementation.md#regenerating-assets). Generated command batches contain local paths and remain ignored. Preserve manual Figcraft edits before regenerating the document.
