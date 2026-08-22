#!/usr/bin/env node
// Renders every sub-app's design system into standalone preview pages.
//
//   node design-system/build.mjs           # write design-system/<app>/previews/
//   node design-system/build.mjs --check   # fail if what is on disk is stale
//
// The generated pages are committed, so pushing a system to Claude Design (or
// opening one in a browser) needs no toolchain. Committed generated files go
// stale silently, which is why --check runs in CI — the same discipline the
// companion's .syso icon is held to.
import { mkdir, readdir, readFile, writeFile, rm } from "node:fs/promises";
import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { renderCard, renderIndex, renderManifest } from "./lib/render.mjs";

const root = dirname(fileURLToPath(import.meta.url));
const check = process.argv.includes("--check");

/** Every directory here holding a system.mjs is a sub-app's design system. */
async function systems() {
  const entries = await readdir(root, { withFileTypes: true });
  const found = [];
  for (const e of entries) {
    if (!e.isDirectory() || e.name === "lib") continue;
    const spec = join(root, e.name, "system.mjs");
    if (existsSync(spec)) found.push({ dir: e.name, spec });
  }
  return found.sort((a, b) => a.dir.localeCompare(b.dir));
}

/** path -> contents for one system's whole preview directory. */
function pages(system) {
  const out = new Map();
  out.set("index.html", renderIndex(system));
  out.set("_ds_manifest.json", renderManifest(system));
  for (const group of system.groups) {
    for (const card of group.cards) {
      out.set(`${card.slug}.html`, renderCard(system, group, card));
    }
  }
  return out;
}

let stale = [];
let written = 0;
for (const { dir, spec } of await systems()) {
  const system = (await import(spec)).default;
  const outDir = join(root, dir, "previews");
  const want = pages(system);

  if (check) {
    const have = existsSync(outDir) ? await readdir(outDir) : [];
    for (const name of have) if (!want.has(name)) stale.push(`${dir}/previews/${name} (orphaned)`);
    for (const [name, body] of want) {
      const p = join(outDir, name);
      const cur = existsSync(p) ? await readFile(p, "utf8") : null;
      if (cur !== body) stale.push(`${dir}/previews/${name} (${cur === null ? "missing" : "differs"})`);
    }
    continue;
  }

  await rm(outDir, { recursive: true, force: true });
  await mkdir(outDir, { recursive: true });
  for (const [name, body] of want) {
    await writeFile(join(outDir, name), body);
    written++;
  }
  console.log(`${dir}: ${want.size} pages`);
}

if (check) {
  if (stale.length) {
    console.error("design-system previews are stale:");
    for (const s of stale) console.error(`  ${s}`);
    console.error("\nrun: node design-system/build.mjs");
    process.exit(1);
  }
  console.log("design-system previews: up to date");
} else {
  console.log(`wrote ${written} files`);
}
