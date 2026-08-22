#!/usr/bin/env node
// Token parity: what a design system claims must be what the app declares.
//
// A design system that has drifted from the running code is worse than none —
// it reads as though someone checked. So every color token in a
// `design-system/<app>/system.mjs` is compared against the declaration in the
// stylesheet that system names as its source, and a difference fails CI.
import { readdir, readFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));
const repo = resolve(root, "..");

/** `--name: value;` pairs from the first :root block, comments stripped. */
function declaredVars(css) {
  const start = css.indexOf(":root");
  if (start < 0) return new Map();
  const open = css.indexOf("{", start);
  const close = css.indexOf("}", open);
  const body = css.slice(open + 1, close).replace(/\/\*[\s\S]*?\*\//g, "");
  const vars = new Map();
  for (const line of body.split(";")) {
    const m = line.match(/^\s*--([a-z0-9-]+)\s*:\s*(.+?)\s*$/i);
    if (m) vars.set(m[1], m[2]);
  }
  return vars;
}

let fail = 0;
const dirs = (await readdir(root, { withFileTypes: true }))
  .filter((e) => e.isDirectory() && e.name !== "lib" && existsSync(join(root, e.name, "system.mjs")))
  .map((e) => e.name)
  .sort();

if (dirs.length === 0) {
  console.error("design-system: no systems found");
  process.exit(1);
}

for (const dir of dirs) {
  const system = (await import(join(root, dir, "system.mjs"))).default;
  const cssPath = join(repo, system.source.css);
  if (!existsSync(cssPath)) {
    console.error(`${dir}: source stylesheet ${system.source.css} does not exist`);
    fail = 1;
    continue;
  }
  const declared = declaredVars(await readFile(cssPath, "utf8"));
  let bad = 0;
  for (const t of system.tokens.colors) {
    const have = declared.get(t.name);
    if (have === undefined) {
      console.error(`${dir}: --${t.name} is in the design system but not in ${system.source.css}`);
      bad++;
    } else if (have !== t.value) {
      console.error(
        `${dir}: --${t.name} is "${t.value}" in the design system, "${have}" in ${system.source.css}`,
      );
      bad++;
    }
  }
  if (bad) fail = 1;
  else console.log(`${dir}: ${system.tokens.colors.length} tokens match ${system.source.css}`);
}

if (fail) {
  console.error("\ndesign-system tokens have drifted: reconcile the system with the app, or the app with the system");
  process.exit(1);
}
