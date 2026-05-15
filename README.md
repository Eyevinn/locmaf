# locmaf.dev

Information site and MARP deck for [LOCMAF](https://github.com/Eyevinn/moqlivemock/blob/main/docs/LOCMAF.md) — *Low Overhead CMAF for MOQ.*

The primary surface is a single-page static site (`site/index.html`).
A MARP-rendered slide deck (`slides/locmaf.md`) is published as a secondary artifact at `/slides/`.

## Layout

```
locmaf/
├── site/                # Source for the static landing page
│   ├── index.html
│   └── styles.css
├── slides/
│   └── locmaf.md        # MARP source for the deck
├── themes/
│   └── locmaf.css       # MARP theme derived from Eyevinn brand
├── assets/
│   ├── logo.svg         # Wordmark logo (hourglass + LOCMAF)
│   ├── logomark.svg     # Logomark only (hourglass)
│   ├── favicon.svg
│   ├── LevelOne.otf     # Eyevinn brand font
│   ├── eyevinn-logo.png
│   └── diagrams/        # SVG diagrams reused in site + slides
├── scripts/
│   └── build-site.mjs   # Copy site/ + assets/ into public/
├── .marprc.yml
├── Makefile
└── package.json
```

## Building

```
npm install
npm run build          # site → public/, slides → public/slides/
```

Outputs land in `public/` ready for deploy.

## Previewing locally

```
npm run preview        # build, then serve public/ at http://localhost:8000
```

Opens both the landing page (`/`) and the slide deck (`/slides/`).
Set `PORT=9000` to pick a different port. Ctrl-C to stop.

### Other targets

| command                       | what it does                                  |
| ----------------------------- | --------------------------------------------- |
| `npm run dev`                 | MARP live-preview server for the deck only    |
| `npm run preview`             | Build everything, serve `public/` locally     |
| `npm run build:site`          | Static site only                              |
| `npm run build:slides:html`   | Slide deck → `public/slides/index.html`       |
| `npm run build:slides:pdf`    | Slide deck → `public/slides/locmaf.pdf`       |
| `npm run build:slides:pptx`   | Slide deck → `public/slides/locmaf.pptx`      |

## Style

Theme follows the Eyevinn Technology brand (see `../ev-marp/` for the
upstream PowerPoint-aligned MARP theme):

- LevelOne brand font, dark gradient background
- Orange `#FC9900` for primary accent (CMAF chunks, headings)
- Cyan `#61B5E5` for secondary accent (LOCMAF deltas, links)
- White on dark for body text

The LOCMAF logo visualises the format itself: a sequence of full CMAF
chunks, compressed into tiny delta blocks for the wire, then
reconstructed back into identical CMAF chunks at the receiver.
