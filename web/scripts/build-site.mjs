#!/usr/bin/env node
// Copy the static site (index.html, styles, assets) into ./public/ ready for deploy.
// Slides are built separately and land in ./public/slides/.

import { cp, mkdir, rm } from "node:fs/promises";
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");
const out = resolve(root, "public");

if (existsSync(out)) {
  await rm(out, { recursive: true });
}
await mkdir(out, { recursive: true });

await cp(resolve(root, "site"), out, { recursive: true });
await cp(resolve(root, "assets"), resolve(out, "assets"), { recursive: true });

console.log(`Built site -> ${out}`);
