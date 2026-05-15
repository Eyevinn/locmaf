#!/usr/bin/env node
// Build the site and slides, then serve ./public on PORT (default 8000).
// Quit with Ctrl-C.

import { spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { readFile, stat } from "node:fs/promises";
import { extname, join, normalize, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const here = fileURLToPath(new URL(".", import.meta.url));
const root = resolve(here, "..");
const publicDir = resolve(root, "public");
const port = Number(process.env.PORT ?? 8000);

console.log("→ Building site and slides…");
const build = spawnSync("npm", ["run", "build"], {
  cwd: root,
  stdio: "inherit",
});
if (build.status !== 0) {
  console.error("Build failed.");
  process.exit(build.status ?? 1);
}

const mime = {
  ".html": "text/html; charset=utf-8",
  ".css":  "text/css; charset=utf-8",
  ".js":   "text/javascript; charset=utf-8",
  ".mjs":  "text/javascript; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg":  "image/svg+xml",
  ".png":  "image/png",
  ".jpg":  "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif":  "image/gif",
  ".webp": "image/webp",
  ".ico":  "image/x-icon",
  ".woff":  "font/woff",
  ".woff2": "font/woff2",
  ".otf":   "font/otf",
  ".ttf":   "font/ttf",
  ".pdf":   "application/pdf",
  ".txt":   "text/plain; charset=utf-8",
  ".md":    "text/markdown; charset=utf-8",
};

const server = createServer(async (req, res) => {
  try {
    const urlPath = decodeURIComponent((req.url ?? "/").split("?")[0]);
    let p = normalize(join(publicDir, urlPath));
    if (!p.startsWith(publicDir + sep) && p !== publicDir) {
      res.writeHead(403); res.end("Forbidden"); return;
    }
    let s;
    try { s = await stat(p); } catch { res.writeHead(404); res.end("Not found"); return; }
    if (s.isDirectory()) {
      p = join(p, "index.html");
      try { s = await stat(p); } catch { res.writeHead(404); res.end("Not found"); return; }
    }
    const body = await readFile(p);
    res.writeHead(200, {
      "content-type": mime[extname(p).toLowerCase()] ?? "application/octet-stream",
      "cache-control": "no-store",
    });
    res.end(body);
  } catch (err) {
    res.writeHead(500); res.end(String(err));
  }
});

server.listen(port, () => {
  console.log();
  console.log(`  Site   → http://localhost:${port}/`);
  console.log(`  Slides → http://localhost:${port}/slides/`);
  console.log();
  console.log("  Ctrl-C to stop.");
});
