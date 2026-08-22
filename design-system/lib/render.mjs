// Turns a sub-app's design-system spec into standalone preview pages.
//
// Standalone is the whole trick. Each page carries its own tokens and its
// own kit stylesheet inline, so it renders identically in three places that
// share nothing: a browser opening the file off disk, a Claude Design
// project card (which serves the file under a strict CSP — no CDN, no
// sibling <link>), and a PR diff someone is reading as text. Nothing here
// resolves a relative path or fetches anything.
//
// The first line of every page is the `@dsCard` marker Claude Design's
// Design System pane indexes by; see design-system/README.md for the sync.

/** The color tokens as a `:root` block, in the app's own declaration form. */
export function tokenBlock(system) {
  const lines = system.tokens.colors.map(
    (t) => `  --${t.name}: ${t.value}; /* ${t.hex} — ${t.use} */`,
  );
  for (const [name, value] of Object.entries(system.tokens.derived ?? {})) {
    lines.push(`  --${name}: ${value};`);
  }
  return `:root {\n${lines.join("\n")}\n  color-scheme: ${system.tokens.colorScheme};\n}`;
}

const esc = (s) =>
  String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

/** One specimen strip: a caption naming what varies, then the markup. */
function specimen(s) {
  const caption = s.caption ? `<p class="ds-caption">${esc(s.caption)}</p>` : "";
  return `      <section class="ds-specimen">\n${caption}\n        <div class="ds-stage${
    s.stage ? ` ds-stage--${s.stage}` : ""
  }">\n${s.html.trimEnd()}\n        </div>\n      </section>`;
}

/** The "where this lives" table — every card names its source of truth. */
function sourceTable(card) {
  if (!card.sources?.length) return "";
  const rows = card.sources
    .map((p) => `            <li><code>${esc(p)}</code></li>`)
    .join("\n");
  return `      <section class="ds-meta">\n        <h2 class="ds-meta-h">Implemented by</h2>\n        <ul class="ds-paths">\n${rows}\n        </ul>\n      </section>`;
}

function rules(card) {
  if (!card.rules?.length) return "";
  const items = card.rules.map((r) => `            <li>${r}</li>`).join("\n");
  return `      <section class="ds-meta">\n        <h2 class="ds-meta-h">Rules</h2>\n        <ul class="ds-rules">\n${items}\n        </ul>\n      </section>`;
}

/**
 * Render one card to a complete HTML document.
 *
 * `system.kit` is the token-driven stylesheet the specimens are written
 * against — plain CSS class names, not the app's Tailwind utilities, so a
 * preview needs no build step and no framework to look like the app.
 */
export function renderCard(system, group, card) {
  const w = card.viewport?.width ?? 880;
  const h = card.viewport?.height ?? 560;
  const marker = `<!-- @dsCard group="${group.name}" name="${card.name}" subtitle="${
    card.subtitle ?? ""
  }" width="${w}" height="${h}" -->`;
  const body = card.specimens.map(specimen).join("\n");
  return `${marker}
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>${esc(system.title)} · ${esc(card.name)}</title>
    <style>
${tokenBlock(system)
  .split("\n")
  .map((l) => `      ${l}`)
  .join("\n")}

${system.kit
  .trim()
  .split("\n")
  .map((l) => `      ${l}`)
  .join("\n")}
    </style>
  </head>
  <body class="ds-page">
    <header class="ds-head">
      <p class="ds-eyebrow">${esc(system.title)} · ${esc(group.name)}</p>
      <h1 class="ds-title">${esc(card.name)}</h1>
      ${card.subtitle ? `<p class="ds-sub">${esc(card.subtitle)}</p>` : ""}
      ${card.intent ? `<p class="ds-intent">${card.intent}</p>` : ""}
    </header>
    <main class="ds-body">
${body}
${rules(card)}
${sourceTable(card)}
    </main>
  </body>
</html>
`;
}

/** The index page: every card in the system, grouped, with its intent. */
export function renderIndex(system) {
  const groups = system.groups
    .map((g) => {
      const items = g.cards
        .map(
          (c) =>
            `          <li>\n            <a class="ds-idx-link" href="${c.slug}.html">${esc(
              c.name,
            )}</a>\n            <span class="ds-idx-sub">${esc(c.subtitle ?? "")}</span>\n          </li>`,
        )
        .join("\n");
      return `      <section class="ds-idx-group">\n        <h2 class="ds-meta-h">${esc(
        g.name,
      )}</h2>\n        <ul class="ds-idx">\n${items}\n        </ul>\n      </section>`;
    })
    .join("\n");
  return `<!-- @dsCard group="Brand" name="${esc(system.title)} design system" subtitle="Index of every card" width="880" height="900" -->
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>${esc(system.title)} design system</title>
    <style>
${tokenBlock(system)
  .split("\n")
  .map((l) => `      ${l}`)
  .join("\n")}

${system.kit
  .trim()
  .split("\n")
  .map((l) => `      ${l}`)
  .join("\n")}
    </style>
  </head>
  <body class="ds-page">
    <header class="ds-head">
      <p class="ds-eyebrow">Artificer design systems</p>
      <h1 class="ds-title">${esc(system.title)}</h1>
      <p class="ds-sub">${esc(system.tagline)}</p>
      <p class="ds-intent">${system.intent}</p>
    </header>
    <main class="ds-body">
${groups}
    </main>
  </body>
</html>
`;
}

/** The manifest Claude Design's pane compiles from the `@dsCard` markers. */
export function renderManifest(system) {
  const cards = [];
  for (const g of system.groups) {
    for (const c of g.cards) {
      cards.push({
        name: c.name,
        path: `${c.slug}.html`,
        subtitle: c.subtitle ?? "",
        group: g.name,
        viewport: { width: c.viewport?.width ?? 880, height: c.viewport?.height ?? 560 },
      });
    }
  }
  return `${JSON.stringify({ app: system.app, title: system.title, cards }, null, 2)}\n`;
}
