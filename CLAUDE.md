# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

The information site for **LOCMAF** (Low Overhead CMAF for MOQ) — published at <https://locmaf.dev>. LOCMAF itself is a wire format defined and implemented elsewhere (in `Eyevinn/moqlivemock`); this repo only contains the marketing/explanatory site plus a MARP slide deck.

The primary surface is the static landing page (`site/index.html`). The slide deck (`slides/locmaf.md`) is a secondary artifact published at `/slides/`.

## Commands

```sh
npm install              # one-time
npm run preview          # build + serve public/ on http://localhost:8000  (use PORT=9000 to override)
npm run build            # site → public/, slides → public/slides/index.html
npm run dev              # MARP live-preview server for slides only (file index of slides/)
npm run build:slides:pdf # also: :pptx
npm run clean            # rm -rf public
```

Important: `build:slides:html` does **not** rebuild assets. After changing any SVG or anything under `assets/`, run the full `npm run build` (or `npm run preview`) — otherwise stale assets remain in `public/`. Browsers also cache SVGs aggressively; use a cache-busting query string or a fresh port when verifying changes locally.

## Architecture

Sources live in three parallel trees; a build script stitches them into `public/`:

```
site/      → copied verbatim to public/
slides/    → rendered by MARP using themes/locmaf.css → public/slides/index.html
assets/    → copied to public/assets/, referenced by both site/ and slides/
themes/    → MARP-only; consumed by .marprc.yml
scripts/   → build-site.mjs and preview.mjs (Node, no deps)
```

The two halves intentionally **share** `assets/` (logos and `assets/diagrams/*.svg`). Edit a diagram once and both surfaces update.

### The MARP theme is a derivative of `../ev-marp`

`themes/locmaf.css` is forked from the upstream Eyevinn MARP theme at `../moq-workspace/ev-marp/themes/eyevinn.css` (same brand colors, LevelOne font, slide layout). The fork swaps the bottom-left logo for the LOCMAF logomark and adds the Eyevinn logo to the footer (right of center, left of the page number). When the upstream theme adds a feature (e.g. table styling), copy the change over rather than reinventing.

### Color convention (load-bearing)

Every diagram and the site CSS follow this convention — keep it consistent when adding or editing visuals:

- **Orange (#FC9900)** — CMAF / source side (moof, segment outlines, CMAF totals)
- **Cyan (#61B5E5)** — anything on the LOCMAF wire (full moof box, delta moof, header_id, properties_length, MoQ groups/objects)
- **Gray (#646464)** — raw mdat / sample data (unchanged by LOCMAF)
- **Green (#33aa55)** — "omitted" in `moof-anatomy.svg` only (matches trex / implicit)

The LOCMAF logo itself is an hourglass: big orange CMAF chunks → compress wedge → tiny cyan delta blocks → decompress wedge → big orange CMAF chunks again. There is a regular variant (`logo.svg`, `logomark.svg`) and a higher-contrast **dark-bg variant** (`logo-dark.svg`, `logomark-dark.svg`); the site and slides use the dark-bg variants because both contexts are dark.

### SVG diagram conventions

All diagrams under `assets/diagrams/` are hand-written SVG (no build step), designed for both web embedding and inline use in MARP slides. They use the same color palette and an inline `<style>` block with these typical classes: `.title` (LevelOne), `.field`/`.lbl` (monospace), `.note`/`.small` (muted monospace). Keep text inside box bounds — slide rendering at the embedded size makes overflow conspicuous.

### Spec source of truth

The full LOCMAF specification lives at `Eyevinn/moqlivemock:docs/LOCMAF.md` (not in this repo). The site copy and slide deck are summarized from it. When the spec changes, the byte counts / object IDs / field tables here must follow.
