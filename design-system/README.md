# Design systems

One design system per sub-app, written as data and rendered to standalone
preview pages. `reliquary/` is the first; the same shape takes the other
four (`companion`, `palcon`, `wildskeeper`, `flametender`).

```
design-system/
  build.mjs              # renders every system to <app>/previews/
  check.mjs              # token parity against each app's own stylesheet
  lib/render.mjs         # the page renderer (cards, index, manifest)
  <app>/system.mjs       # THE system: tokens, kit CSS, cards — source of truth
  <app>/previews/        # generated, committed
```

```sh
node design-system/build.mjs          # rebuild the previews
node design-system/build.mjs --check  # fail if they are stale
./scripts/checkdesign.sh              # what CI runs: parity + freshness
```

## What a system is

`<app>/system.mjs` holds four things:

- **tokens** — the palette, in the exact declaration form the app's
  `index.css` uses (an RGB or HSL triple, not a hex string, so Tailwind's
  opacity modifiers work on it), plus the compound fills that are accents
  laid over the ground rather than members of the palette.
- **kit** — a token-only stylesheet the specimens are written against.
  Plain class names, not the app's Tailwind utilities.
- **cards** — grouped specimens. Each card carries the *intent* (why the
  component is shaped this way), the *rules* that are easy to break, and the
  source files it was derived from.
- **source** — the stylesheet, Tailwind config and design doc it answers to.

## Why the previews are standalone

Every generated page inlines its own tokens and kit and links to nothing.
That is what makes one file render identically in three places that share
no infrastructure: a browser opening it off disk, a Claude Design project
card (served under a strict CSP — no CDN, no sibling `<link>`), and a diff
someone is reading as text.

The cost is that the kit restates in plain CSS what the app composes in
Tailwind, so the two can drift. Two guards, both in
`scripts/checkdesign.sh` and both in CI:

- `check.mjs` compares every token against the stylesheet the system names,
  because a stale token is a lie that reads like a record.
- `build.mjs --check` rebuilds and diffs, because committed generated files
  go stale silently — the trap `cmd/companion/rsrc_windows_amd64.syso` is
  already held to in the same workflow.

Nothing guards the kit's *class bodies* against the Tailwind they mirror.
That is why every card names its `Implemented by` paths: when you change
`ui/button.tsx`, `button.html` is the page to re-read.

## Pushing a system to Claude Design

The first line of every generated page is the `@dsCard` marker the Design
System pane indexes by, and `previews/_ds_manifest.json` is the same index
as JSON. To push one:

1. From an interactive terminal (`claude` in a real TTY — the design
   authorization flow needs one), run `/design-login`, then `/design-sync`.
2. Point it at `design-system/<app>/previews` as the local directory, and
   at a project of type `PROJECT_TYPE_DESIGN_SYSTEM` — that type is fixed at
   creation, so pushing to a regular project never makes it a design system.
3. Sync incrementally, one card at a time. Never wholesale-replace.

A Claude Code Web session cannot do step 1: `/design-login` has no TTY
there, so `DesignSync` refuses. Building and reviewing the bundle works
anywhere; only the push needs the terminal.

## Adding the next app

1. `mkdir design-system/<app>` and write `system.mjs`, copying
   `reliquary/system.mjs`'s shape.
2. Read the real tokens out of `web/<app>/src/index.css`. The three consoles
   declare shadcn-style HSL triples rather than reliquary's RGB triples;
   `check.mjs` compares declaration strings, so either form works as long as
   the system copies it verbatim.
3. Derive each card from a named component file, and list it under
   `sources`. A card with no source is a drawing, not a design system.
4. `node design-system/build.mjs && ./scripts/checkdesign.sh`.
