#!/usr/bin/env node
// Compile the browser build of the locmaf tooling (cmd/locmaf-wasm) to
// site/tools/locmaf.wasm and stage Go's wasm_exec.js beside it. The
// site build (build-site.mjs) copies site/ verbatim, so these land in
// public/tools/ automatically. Both outputs are generated artifacts and
// git-ignored (see site/tools/.gitignore).

import { execFileSync } from "node:child_process";
import { cp, mkdir } from "node:fs/promises";
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(here, "..");
const repoRoot = resolve(webRoot, ".."); // the github.com/Eyevinn/locmaf module root
const outDir = resolve(webRoot, "site", "tools");

await mkdir(outDir, { recursive: true });

// Resolve the Go toolchain's wasm_exec.js (Go 1.24+ path).
const goroot = execFileSync("go", ["env", "GOROOT"]).toString().trim();
const wasmExec = join(goroot, "lib", "wasm", "wasm_exec.js");
if (!existsSync(wasmExec)) {
  throw new Error(`wasm_exec.js not found at ${wasmExec}; need Go >= 1.24`);
}

// Build from the module root so go.work / module resolution picks up the
// local locmaf packages.
console.log("Compiling cmd/locmaf-wasm → site/tools/locmaf.wasm …");
execFileSync("go", ["build", "-ldflags=-s -w", "-o", resolve(outDir, "locmaf.wasm"), "./cmd/locmaf-wasm"], {
  cwd: repoRoot,
  env: { ...process.env, GOOS: "js", GOARCH: "wasm" },
  stdio: "inherit",
});

await cp(wasmExec, resolve(outDir, "wasm_exec.js"));
console.log(`Staged wasm_exec.js from ${goroot}`);
console.log(`Built wasm tool -> ${outDir}`);
