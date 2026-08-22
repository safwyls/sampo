// Reliquary's design system, as data.
//
// Reliquary is the vault: a game-blind custody service for shared world
// saves. Its look was specified before its code — `docs/reliquary-ui-rebuild.md`
// §"Design language" is normative and this file is that section made
// executable. Every token below is the token the running app declares in
// `web/reliquary/src/index.css`; `scripts/checkdesign.sh` fails CI if the two
// ever disagree.
//
// The game names in the specimens (Dragonwilds, Enshrouded, Palworld) are
// fixture data, the same way `web/reliquary/src/**/*.test.tsx` names one: the
// vault is game-blind and never branches on which game a world is, but a
// world card with no game on it does not show what the card is for.
// `scripts/checkbounds.sh` enforces that rule on `web/reliquary/src`, which
// this file is not part of.
//
// The specimens are written against the `rq-*` kit stylesheet in this file,
// not against the app's Tailwind utilities. That is deliberate: a preview has
// to render with no build step, no CDN and no framework — see
// design-system/README.md. The trade is that the kit restates what Tailwind
// composes, so each card names the source file it was derived from.

const svg = (body, cls = "rq-i") =>
  `<svg class="${cls}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${body}</svg>`;

export const ICON = {
  lock: svg('<rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>'),
  lockOpen: svg('<rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 9.9-1"/>'),
  clock: svg('<circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>'),
  more: svg('<circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/><circle cx="5" cy="12" r="1"/>'),
  globe: svg('<circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/><path d="M2 12h20"/>'),
  laptop: svg('<path d="M20 16V7a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v9"/><path d="M20 16H4l-1.28 2.55a1 1 0 0 0 .9 1.45h16.76a1 1 0 0 0 .9-1.45z"/>'),
  users: svg('<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>'),
  image: svg('<rect width="18" height="18" x="3" y="3" rx="2" ry="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.09-3.09a2 2 0 0 0-2.82 0L6 21"/>'),
  database: svg('<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14a9 3 0 0 0 18 0V5"/><path d="M3 12a9 3 0 0 0 18 0"/>'),
  shield: svg('<path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/><path d="M9 12h6"/><path d="M12 9v6"/>'),
  close: svg('<path d="M18 6 6 18"/><path d="m6 6 12 12"/>'),
};

// The kit: preview chrome (`ds-*`) plus Reliquary's own primitives (`rq-*`).
// Both are token-only — no literal color appears outside the `:root` block
// the renderer emits above this, so a token change repaints every card.
const kit = `
*, *::before, *::after { box-sizing: border-box; }

/* ---- preview chrome -------------------------------------------------- */
.ds-page {
  margin: 0;
  background: rgb(var(--ink));
  color: rgb(var(--parchment));
  font-family: Georgia, Gelasio, "Times New Roman", serif;
  font-size: 15px;
  line-height: 1.5;
  -webkit-font-smoothing: antialiased;
}
.ds-head { border-bottom: 1px solid rgb(var(--edge)); padding: 26px 32px 20px; }
.ds-eyebrow {
  margin: 0; font-family: var(--mono); font-size: 11px;
  letter-spacing: 0.12em; text-transform: uppercase; color: rgb(var(--mist));
}
.ds-title { margin: 6px 0 0; font-size: 22px; font-weight: normal; letter-spacing: 0.05em; color: rgb(var(--gold)); }
.ds-sub { margin: 3px 0 0; font-size: 13px; color: rgb(var(--mist)); }
.ds-intent { margin: 14px 0 0; max-width: 70ch; font-size: 14px; color: rgb(var(--parchment) / 0.9); }
.ds-body { display: flex; flex-direction: column; gap: 28px; padding: 24px 32px 44px; }
.ds-caption {
  margin: 0 0 10px; font-family: var(--mono); font-size: 10px;
  letter-spacing: 0.12em; text-transform: uppercase; color: rgb(var(--mist));
}
.ds-stage { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; }
.ds-stage--block { display: block; }
.ds-stage--stack { display: flex; flex-direction: column; align-items: stretch; gap: 12px; }
.ds-stage--well {
  background: rgb(var(--well)); border: 1px solid rgb(var(--edge));
  border-radius: 8px; padding: 18px;
}
.ds-meta { border-top: 1px solid rgb(var(--edge)); padding-top: 16px; }
.ds-meta-h {
  margin: 0 0 8px; font-family: var(--mono); font-size: 10px; font-weight: normal;
  letter-spacing: 0.12em; text-transform: uppercase; color: rgb(var(--mist));
}
.ds-paths, .ds-rules { margin: 0; padding-left: 18px; }
.ds-paths li, .ds-rules li { font-size: 13px; color: rgb(var(--parchment) / 0.85); margin-bottom: 4px; }
.ds-paths li { list-style: none; margin-left: -18px; }
code { font-family: var(--mono); font-size: 12px; color: rgb(var(--goldhi)); }
.ds-idx-group + .ds-idx-group { border-top: 1px solid rgb(var(--edge)); padding-top: 20px; }
.ds-idx { margin: 0; padding: 0; list-style: none; display: grid; gap: 6px; }
.ds-idx li { display: flex; flex-wrap: wrap; align-items: baseline; gap: 10px; }
.ds-idx-link { color: rgb(var(--goldhi)); text-decoration: none; font-size: 15px; }
.ds-idx-link:hover { color: rgb(var(--gold)); text-decoration: underline; }
.ds-idx-sub { font-size: 12px; color: rgb(var(--mist)); }

/* ---- swatches -------------------------------------------------------- */
.rq-swatches { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 12px; }
.rq-swatch { border: 1px solid rgb(var(--edge)); border-radius: 8px; overflow: hidden; background: rgb(var(--panel)); }
.rq-swatch-chip { height: 58px; border-bottom: 1px solid rgb(var(--edge)); }
.rq-swatch-body { padding: 8px 10px 10px; }
.rq-swatch-name { font-family: var(--mono); font-size: 12px; color: rgb(var(--parchment)); }
.rq-swatch-hex { font-family: var(--mono); font-size: 11px; color: rgb(var(--mist)); }
.rq-swatch-use { margin-top: 4px; font-size: 12px; color: rgb(var(--mist)); line-height: 1.35; }

/* ---- Reliquary primitives -------------------------------------------- */
.rq-i { width: 14px; height: 14px; flex: none; }
.rq-i--sm { width: 12px; height: 12px; }
.rq-i--lg { width: 16px; height: 16px; }
.rq-mono { font-family: var(--mono); }

.rq-panel {
  border: 1px solid rgb(var(--edge)); background: rgb(var(--panel));
  border-radius: 8px; padding: 16px 20px;
}
.rq-well {
  border: 1px dashed rgb(var(--edge)); background: rgb(var(--well));
  border-radius: 8px; padding: 14px 20px; font-size: 13px; color: rgb(var(--mist));
}
.rq-h1 { margin: 0; font-size: 22px; font-weight: normal; letter-spacing: 0.05em; color: rgb(var(--gold)); }
.rq-label { display: block; font-size: 11px; text-transform: uppercase; letter-spacing: 0.1em; color: rgb(var(--mist)); }
.rq-input {
  width: 100%; border: 1px solid rgb(var(--edge)); border-radius: 4px;
  background: rgb(var(--ink)); padding: 8px 10px; font-family: inherit;
  font-size: 14px; color: rgb(var(--parchment));
}
.rq-input::placeholder { color: rgb(var(--mist) / 0.6); }
.rq-input:focus-visible, .rq-btn:focus-visible, .rq-navitem:focus-visible {
  outline: none; box-shadow: 0 0 0 2px rgb(var(--ink)), 0 0 0 4px rgb(var(--gold) / 0.6);
}

.rq-btn {
  display: inline-flex; align-items: center; justify-content: center; gap: 8px;
  border: 1px solid transparent; border-radius: 4px; white-space: nowrap;
  font-family: inherit; padding: 6px 16px; font-size: 13px; cursor: pointer;
  background: none; transition: color 0.12s ease, border-color 0.12s ease;
}
.rq-btn--primary {
  border-color: rgb(var(--gold)); background: var(--primary-fill);
  font-weight: 700; letter-spacing: 0.04em; color: rgb(var(--gold));
}
.rq-btn--primary:hover { color: rgb(var(--goldhi)); border-color: rgb(var(--goldhi)); }
.rq-btn--quiet { border-color: rgb(var(--edge)); color: rgb(var(--mist)); }
.rq-btn--quiet:hover { border-color: rgb(var(--gold) / 0.6); color: rgb(var(--parchment)); }
.rq-btn--danger { border-color: rgb(var(--ember) / 0.4); color: rgb(var(--mist)); }
.rq-btn--danger:hover { border-color: rgb(var(--ember)); color: rgb(var(--ember)); }
.rq-btn--sm { padding: 4px 12px; font-size: 12px; }
.rq-btn--lg { padding: 8px 18px; font-size: 14px; }
.rq-btn--icon { padding: 6px 10px; }
.rq-btn[disabled], .rq-btn.is-disabled { opacity: 0.5; pointer-events: none; }
.rq-btn.is-hover.rq-btn--primary { color: rgb(var(--goldhi)); border-color: rgb(var(--goldhi)); }
.rq-btn.is-hover.rq-btn--quiet { border-color: rgb(var(--gold) / 0.6); color: rgb(var(--parchment)); }
.rq-btn.is-hover.rq-btn--danger { border-color: rgb(var(--ember)); color: rgb(var(--ember)); }

.rq-badge {
  display: inline-block; border-radius: 3px; background: rgb(var(--ink));
  padding: 2px 8px; font-family: var(--mono); font-size: 10px; letter-spacing: 0.06em;
}
.rq-badge--head { color: rgb(var(--goldhi)); }
.rq-badge--conflict { color: rgb(var(--ember)); }
.rq-badge--checkpoint { color: rgb(var(--rune)); }
.rq-badge--muted { color: rgb(var(--mist)); }

.rq-chip {
  display: inline-flex; align-items: center; gap: 6px; border: 1px solid;
  border-radius: 999px; padding: 2px 12px; font-size: 12px;
}
.rq-chip--free { border-color: rgb(var(--ok)); background: var(--fill-free); color: rgb(var(--ok)); }
.rq-chip--held { border-color: rgb(var(--gold)); background: var(--fill-held); color: rgb(var(--goldhi)); }
.rq-chip--expired { border-color: rgb(var(--ember)); background: var(--fill-expired); color: rgb(var(--ember)); }

.rq-menu {
  min-width: 13rem; border: 1px solid rgb(var(--edge)); background: rgb(var(--panel));
  border-radius: 4px; padding: 4px 0; font-size: 13px;
  box-shadow: 0 20px 25px -5px rgb(0 0 0 / 0.4), 0 8px 10px -6px rgb(0 0 0 / 0.4);
}
.rq-menuitem { padding: 6px 12px; color: rgb(var(--mist)); cursor: pointer; }
.rq-menuitem.is-highlighted { background: rgb(var(--ink)); color: rgb(var(--parchment)); }
.rq-menuitem--danger.is-highlighted { background: rgb(var(--ember) / 0.1); color: rgb(var(--ember)); }
.rq-menusep { height: 1px; margin: 4px 0; background: rgb(var(--edge)); }

.rq-dialog {
  width: 100%; max-width: 28rem; position: relative;
  border: 1px solid rgb(var(--edge)); background: rgb(var(--panel));
  border-radius: 8px; padding: 24px; color: rgb(var(--parchment));
  box-shadow: 0 25px 50px -12px rgb(0 0 0 / 0.6);
}
.rq-dialog-title { margin: 0; font-size: 13px; font-weight: normal; text-transform: uppercase; letter-spacing: 0.12em; color: rgb(var(--gold)); }
.rq-dialog-body { margin: 12px 0 0; font-size: 14px; color: rgb(var(--parchment) / 0.9); }
.rq-dialog-foot { display: flex; justify-content: flex-end; gap: 8px; margin-top: 24px; }
.rq-dialog-x { position: absolute; right: 16px; top: 16px; color: rgb(var(--mist)); background: none; border: 0; cursor: pointer; padding: 0; }

.rq-toast {
  display: flex; flex-direction: column; gap: 2px; min-width: 18rem;
  border: 1px solid rgb(var(--edge)); background: rgb(var(--panel));
  border-radius: 4px; padding: 12px 14px; font-size: 13px; color: rgb(var(--parchment));
}
.rq-toast--success { border-color: rgb(var(--ok) / 0.5); }
.rq-toast--error { border-color: rgb(var(--ember) / 0.6); }
.rq-toast-desc { color: rgb(var(--mist)); }

.rq-dot { display: inline-block; width: 7px; height: 7px; border-radius: 999px; }
.rq-dot--live { background: rgb(var(--ok)); }
.rq-dot--down { background: rgb(var(--mist)); }

.rq-tableshell { overflow: hidden; border: 1px solid rgb(var(--edge)); background: rgb(var(--panel)); border-radius: 8px; }
.rq-tablehead {
  border-bottom: 1px solid rgb(var(--edge)); padding: 10px 18px;
  font-size: 10px; text-transform: uppercase; letter-spacing: 0.12em; color: rgb(var(--mist));
}
.rq-tablerow { display: flex; align-items: center; gap: 14px; padding: 12px 18px; border-bottom: 1px solid rgb(var(--edge)); }
.rq-tablerow:last-child { border-bottom: 0; }

.rq-cover { flex: none; border: 1px solid rgb(var(--edge)); border-radius: 5px; width: 84px; height: 112px; object-fit: cover; }
.rq-cover--fallback {
  display: flex; align-items: center; justify-content: center; text-align: center;
  background: var(--fill-cover); padding: 6px; font-size: 11px; line-height: 1.2; color: rgb(var(--mist));
}
.rq-cover--detail { width: 96px; height: 128px; }

.rq-card { display: flex; align-items: flex-start; gap: 18px; border: 1px solid rgb(var(--edge)); background: rgb(var(--panel)); border-radius: 8px; padding: 18px 20px; }
.rq-card-main { display: flex; min-width: 0; flex: 1; flex-direction: column; gap: 8px; }
.rq-card-titlerow { display: flex; flex-wrap: wrap; align-items: baseline; gap: 10px; }
.rq-card-name { font-size: 18px; font-weight: 700; color: rgb(var(--parchment)); text-decoration: none; }
.rq-card-game { font-size: 12px; color: rgb(var(--rune)); }
.rq-card-head { margin-left: auto; font-family: var(--mono); font-size: 12px; color: rgb(var(--mist)); }
.rq-card-line { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; font-size: 13px; color: rgb(var(--mist)); }
.rq-card-asked { font-family: var(--mono); font-size: 12px; color: rgb(var(--rune)); }
.rq-card-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }

.rq-versionrow { display: flex; flex-wrap: wrap; align-items: center; gap: 14px; padding: 12px 18px; border-bottom: 1px solid rgb(var(--edge)); }
.rq-versionrow:last-child { border-bottom: 0; }
.rq-vid { width: 42px; font-family: var(--mono); font-size: 13px; color: rgb(var(--parchment)); }
.rq-vmeta { font-size: 13px; color: rgb(var(--mist)); }
.rq-vmeta b { font-weight: normal; color: rgb(var(--parchment)); }
.rq-vactions { margin-left: auto; display: flex; gap: 8px; }

.rq-shell { display: flex; border: 1px solid rgb(var(--edge)); border-radius: 8px; overflow: hidden; min-height: 340px; }
.rq-side { width: 224px; flex: none; display: flex; flex-direction: column; background: rgb(var(--well)); border-right: 1px solid rgb(var(--edge)); padding: 20px 0; }
.rq-side-brand { border-bottom: 1px solid rgb(var(--edge)); padding: 0 20px 18px; }
.rq-side-name { font-size: 21px; letter-spacing: 0.06em; color: rgb(var(--gold)); }
.rq-side-tag { margin-top: 2px; font-size: 12px; color: rgb(var(--mist)); }
.rq-nav { display: flex; flex-direction: column; gap: 2px; padding: 14px 10px; }
.rq-navitem {
  display: flex; align-items: center; gap: 10px; border: 1px solid transparent;
  border-radius: 4px; padding: 8px 12px; font-size: 15px; color: rgb(var(--mist)); text-decoration: none;
}
.rq-navitem:hover { color: rgb(var(--parchment)); }
.rq-navitem.is-active { border-color: rgb(var(--edge)); background: rgb(var(--panel)); color: rgb(var(--goldhi)); }
.rq-navitem-admin { margin-left: auto; font-size: 10px; letter-spacing: 0.08em; color: rgb(var(--rune)); }
.rq-side-foot { margin-top: auto; display: flex; flex-direction: column; gap: 6px; border-top: 1px solid rgb(var(--edge)); padding: 14px 20px 0; }
.rq-avatar {
  display: flex; align-items: center; justify-content: center; width: 26px; height: 26px;
  border: 1px solid rgb(var(--gold)); border-radius: 999px; background: rgb(var(--panel));
  font-size: 12px; color: rgb(var(--gold)); flex: none;
}
.rq-side-who { display: flex; align-items: center; gap: 8px; }
.rq-side-name2 { font-size: 13px; }
.rq-side-role { font-size: 11px; color: rgb(var(--mist)); }
.rq-side-live { display: flex; align-items: center; gap: 6px; font-family: var(--mono); font-size: 11px; color: rgb(var(--mist)); }
.rq-pagehead {
  display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between;
  gap: 10px 16px; border-bottom: 1px solid rgb(var(--edge)); padding: 26px 32px 18px;
}
.rq-pagesub { margin-top: 2px; font-size: 13px; color: rgb(var(--mist)); }
.rq-main { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.rq-mainbody { padding: 20px 32px; display: flex; flex-direction: column; gap: 14px; }

.rq-login-ground { display: flex; align-items: center; justify-content: center; padding: 32px; background: var(--fill-login); border-radius: 8px; }
.rq-login { display: flex; width: 100%; max-width: 360px; flex-direction: column; border: 1px solid rgb(var(--edge)); background: rgb(var(--panel)); border-radius: 8px; padding: 30px 32px 26px; }
.rq-login-mark { display: flex; flex-direction: column; align-items: center; gap: 6px; margin-bottom: 14px; }
.rq-login-mark .rq-i { width: 32px; height: 32px; color: rgb(var(--gold)); stroke-width: 1.2; }
.rq-login-name { font-size: 22px; letter-spacing: 0.06em; color: rgb(var(--gold)); }
.rq-login-tag { font-size: 13px; color: rgb(var(--mist)); }
.rq-login-err { margin-top: 12px; font-family: var(--mono); font-size: 12px; color: rgb(var(--ember)); }
.rq-login-hint { margin-top: 12px; font-size: 12px; font-style: italic; color: rgb(var(--mist)); }
.rq-login-hint b { font-style: normal; font-weight: normal; color: rgb(var(--parchment)); }
.rq-login-build { margin-top: 10px; text-align: center; font-family: var(--mono); font-size: 11px; color: rgb(var(--mist)); }

.rq-type-row { display: flex; align-items: baseline; gap: 18px; border-bottom: 1px solid rgb(var(--edge)); padding: 12px 0; }
.rq-type-row:last-child { border-bottom: 0; }
.rq-type-key { width: 190px; flex: none; font-family: var(--mono); font-size: 11px; color: rgb(var(--mist)); }
.rq-focus-demo { display: inline-flex; }
`;

// The palette. `value` is the declaration form the app uses — an RGB triple
// rather than a hex string, so Tailwind's opacity modifiers work on it
// (`border-ember/40`, `ring-gold/60`): a `var(--x)` holding "#d4735e" cannot
// be given an alpha, an `rgb(var(--x) / <alpha-value>)` can.
const colors = [
  { name: "ink", value: "16 13 23", hex: "#100d17", use: "the page ground" },
  { name: "well", value: "20 16 29", hex: "#14101d", use: "sidebar and inset strips — a shade under the page, so a panel on it still reads as raised" },
  { name: "panel", value: "26 21 36", hex: "#1a1524", use: "cards, dialogs, menus" },
  { name: "edge", value: "47 39 64", hex: "#2f2740", use: "every border and divider" },
  { name: "parchment", value: "232 224 207", hex: "#e8e0cf", use: "body text" },
  { name: "mist", value: "148 141 163", hex: "#948da3", use: "secondary text, quiet buttons, labels" },
  { name: "gold", value: "201 168 96", hex: "#c9a860", use: "accent, headings, the primary button, the focus ring" },
  { name: "goldhi", value: "227 198 127", hex: "#e3c67f", use: "accent hover, links, the HEAD badge" },
  { name: "ok", value: "127 196 106", hex: "#7fc46a", use: "free custody, the live dot, success" },
  { name: "ember", value: "212 115 94", hex: "#d4735e", use: "danger, conflict, an expired hold" },
  { name: "rune", value: "157 127 196", hex: "#9d7fc4", use: "game tags, info, the CHECKPOINT badge" },
];

const derived = {
  mono: "ui-monospace, SFMono-Regular, Menlo, monospace",
  // Fills that are compounds of the palette rather than members of it: each
  // is the accent laid over the page ground at low weight.
  "primary-fill": "linear-gradient(to bottom, #2a2416, #1e1a10)",
  "fill-free": "#14200f",
  "fill-held": "#23180c",
  "fill-expired": "#26130e",
  "fill-cover": "linear-gradient(to bottom right, #221b2e, #14101d)",
  "fill-login": "radial-gradient(ellipse at 50% 30%, #1a1524 0%, #100d17 65%)",
};

const swatches = (names) =>
  `          <div class="rq-swatches">\n` +
  names
    .map((n) => {
      const t = colors.find((c) => c.name === n);
      return `            <div class="rq-swatch">
              <div class="rq-swatch-chip" style="background: rgb(var(--${t.name}))"></div>
              <div class="rq-swatch-body">
                <div class="rq-swatch-name">--${t.name}</div>
                <div class="rq-swatch-hex">${t.hex}</div>
                <div class="rq-swatch-use">${t.use}</div>
              </div>
            </div>`;
    })
    .join("\n") +
  `\n          </div>`;

const btn = (variant, label, extra = "") =>
  `<button class="rq-btn rq-btn--${variant}${extra}">${label}</button>`;

const typeRow = (key, html) =>
  `          <div class="rq-type-row"><div class="rq-type-key">${key}</div><div>${html}</div></div>`;

const worldCard = ({ name, game, head, chip, line, asked, primary, quiet }) => `
          <article class="rq-card">
            <div class="rq-cover rq-cover--fallback">${game}</div>
            <div class="rq-card-main">
              <div class="rq-card-titlerow">
                <span class="rq-card-name">${name}</span>
                <span class="rq-card-game">${game}</span>
                <span class="rq-card-head">${head}</span>
              </div>
              <div class="rq-card-line">${chip}<span>${line}</span></div>
              ${asked ? `<div class="rq-card-asked">${asked}</div>` : ""}
              <div class="rq-card-actions">
                ${primary}
                ${quiet.map((q) => btn("quiet", q)).join("\n                ")}
                <button class="rq-btn rq-btn--quiet rq-btn--icon" aria-label="More actions for ${name}">${ICON.more}</button>
              </div>
            </div>
          </article>`;

const CHIP_FREE = `<span class="rq-chip rq-chip--free">${ICON.lockOpen.replace('class="rq-i"', 'class="rq-i rq-i--sm"')}Free</span>`;
const CHIP_HELD = `<span class="rq-chip rq-chip--held">${ICON.lock.replace('class="rq-i"', 'class="rq-i rq-i--sm"')}Held</span>`;
const CHIP_EXPIRED = `<span class="rq-chip rq-chip--expired">${ICON.clock.replace('class="rq-i"', 'class="rq-i rq-i--sm"')}Hold expired</span>`;

const NAV = [
  { icon: ICON.globe, label: "Worlds", active: true },
  { icon: ICON.laptop, label: "Companion" },
  { icon: ICON.users, label: "Users", admin: true },
  { icon: ICON.image, label: "Cover art", admin: true },
  { icon: ICON.database, label: "Save catalogue", admin: true },
];

export default {
  app: "reliquary",
  title: "Reliquary",
  tagline: "the vault of shared worlds",
  intent: `One deliberate dark look — <b>no light theme and no toggle</b>, so there is exactly
      one palette. Reliquary is game-blind custody: it holds other people's worlds and hands
      them back, and the interface is built so a player can answer "can I take this?" from
      across the room. That is what the custody chip, the one-primary-action rule and the
      badge vocabulary are all for.`,
  source: {
    css: "web/reliquary/src/index.css",
    tailwind: "web/reliquary/tailwind.config.js",
    doc: "docs/reliquary-ui-rebuild.md",
  },
  tokens: { colors, derived, colorScheme: "dark" },
  kit,
  groups: [
    {
      name: "Foundations",
      cards: [
        {
          slug: "colors",
          name: "Palette",
          subtitle: "11 tokens, one theme",
          viewport: { width: 900, height: 900 },
          intent: `Eleven tokens and nothing else. They are declared once in
            <code>index.css</code> and named in <code>tailwind.config.js</code>; no component
            reaches for a literal color except the six compound fills at the bottom, each of
            which is an accent laid over the ground.`,
          specimens: [
            { stage: "block", caption: "Grounds — three depths, darkest on top", html: swatches(["ink", "well", "panel", "edge"]) },
            { stage: "block", caption: "Text", html: swatches(["parchment", "mist"]) },
            { stage: "block", caption: "Accent and state", html: swatches(["gold", "goldhi", "ok", "ember", "rune"]) },
            {
              caption: "Compound fills",
              stage: "block",
              html: `          <div class="rq-swatches">
${Object.entries(derived)
  .filter(([n]) => n !== "mono")
  .map(
    ([n, v]) => `            <div class="rq-swatch">
              <div class="rq-swatch-chip" style="background: var(--${n})"></div>
              <div class="rq-swatch-body">
                <div class="rq-swatch-name">--${n}</div>
                <div class="rq-swatch-hex">${v.length > 34 ? v.slice(0, 32) + "…" : v}</div>
              </div>
            </div>`,
  )
  .join("\n")}
          </div>`,
            },
          ],
          rules: [
            "A new color is a change to this palette, not a one-off hex in a component.",
            "State reads by hue and never by hue alone: free is green <i>and</i> says “Free”, conflict is ember <i>and</i> stamps CONFLICT.",
            "<code>ok</code> / <code>ember</code> / <code>rune</code> are meanings, not decorations — green never means “go”, it means “nobody holds this”.",
          ],
          sources: ["web/reliquary/src/index.css", "web/reliquary/tailwind.config.js"],
        },
        {
          slug: "typography",
          name: "Type",
          subtitle: "Georgia throughout, mono for machine facts",
          viewport: { width: 900, height: 720 },
          intent: `Georgia for everything — the vault's voice — with Gelasio as the
            metric-compatible webfont for machines without it. Monospace is reserved for
            facts a machine produced: ids, byte counts, timestamps, tokens, build strings.
            If it is mono, you can copy it into a bug report and it will still mean something.`,
          specimens: [
            {
              stage: "block",
              caption: "Scale",
              html: [
                typeRow("h1 · 22px / 0.05em / gold", `<span class="rq-h1">Worlds</span>`),
                typeRow("card title · 18px / bold", `<span class="rq-card-name">Ashwood Hollow</span>`),
                typeRow("body · 15px", `Everything the app says in a sentence.`),
                typeRow("secondary · 13px / mist", `<span style="font-size:13px;color:rgb(var(--mist))">held by hazel until 18:40 — claimable</span>`),
                typeRow("field label · 11px / 0.1em / caps", `<span class="rq-label">Retention</span>`),
                typeRow("table head · 10px / 0.12em / caps", `<span class="rq-tablehead" style="border:0;padding:0">User · Role · Custody</span>`),
                typeRow("mono · ids, sizes, times", `<span class="rq-mono" style="font-size:12px;color:rgb(var(--mist))">head v41 · 1.8 GB · 2026-08-21 18:02</span>`),
                typeRow("mono · badges", `<span class="rq-badge rq-badge--head">HEAD</span>`),
              ].join("\n"),
            },
          ],
          rules: [
            "Panel headings are uppercase, letterspaced and gold — one heading treatment, everywhere a panel names itself.",
            "Labels are uppercased by the words the caller writes, not by <code>text-transform</code>, so a test can find a label by what it says.",
            "No emoji anywhere. Icons are lucide, stroke style.",
          ],
          sources: ["web/reliquary/src/index.css", "web/reliquary/src/components/ui/label.tsx"],
        },
        {
          slug: "surfaces",
          name: "Surfaces",
          subtitle: "Panel, well, dashed well, 8px corner",
          viewport: { width: 900, height: 620 },
          intent: `Three grounds stacked darkest-to-lightest: <code>ink</code> is the page,
            <code>well</code> sits under it for sidebars and inset strips, <code>panel</code>
            rises above it for anything you can act on. A dashed <code>edge</code> on a well
            means "this is a form or an aside, not a record".`,
          specimens: [
            {
              stage: "stack",
              caption: "Solid panel — a record you act on",
              html: `          <div class="rq-panel">A world, a version, a user: something the vault holds.</div>`,
            },
            {
              stage: "stack",
              caption: "Dashed well — a form, a hint, an empty state",
              html: `          <div class="rq-well">No worlds yet. Create one, or point the companion at a save you already have.</div>`,
            },
            {
              stage: "stack",
              caption: "Table shell — a panel with a header strip",
              html: `          <div class="rq-tableshell">
            <div class="rq-tablehead">User · Role · Custody</div>
            <div class="rq-tablerow"><span>hazel</span><span class="rq-badge rq-badge--muted">ADMIN</span><span style="color:rgb(var(--mist));font-size:13px">may check worlds out</span></div>
            <div class="rq-tablerow"><span>rook</span><span class="rq-badge rq-badge--muted">USER</span><span style="color:rgb(var(--mist));font-size:13px">read-only</span></div>
          </div>`,
            },
          ],
          rules: [
            "Corner radius is 8px for panels (<code>rounded-panel</code>) and 4px for controls. Chips are fully round; badges are 3px.",
            "Depth is the ground color, not a shadow. Only overlays — dialog, menu, toast — carry a shadow.",
            "A table is drawn as a panel with grid rows, not a <code>&lt;table&gt;</code>: every column is a word or a row of buttons, and grid keeps them aligned without colspan arithmetic.",
          ],
          sources: [
            "web/reliquary/src/components/ui/table.tsx",
            "web/reliquary/tailwind.config.js",
          ],
        },
        {
          slug: "focus",
          name: "Focus",
          subtitle: "One gold ring, offset from the page ground",
          viewport: { width: 900, height: 420 },
          intent: `Focus is visible in exactly one style across the app — a 2px gold ring at
            60% offset from <code>ink</code>, the same accent the primary button carries.
            Declared once on <code>:focus-visible</code> in the base layer, never re-styled
            per component.`,
          specimens: [
            {
              caption: "As it renders (the ring is drawn here, not focused)",
              html: `          <span class="rq-focus-demo" style="box-shadow: 0 0 0 2px rgb(var(--ink)), 0 0 0 4px rgb(var(--gold) / 0.6); border-radius: 4px">${btn("primary", "Check out")}</span>
          <span class="rq-focus-demo" style="box-shadow: 0 0 0 2px rgb(var(--ink)), 0 0 0 4px rgb(var(--gold) / 0.6); border-radius: 4px">${btn("quiet", "Download head")}</span>`,
            },
            {
              caption: "Tab into these to see the real thing",
              html: `          ${btn("primary", "Check out")}\n          ${btn("quiet", "Download head")}\n          <input class="rq-input" style="width:220px" placeholder="world name" />`,
            },
          ],
          rules: [
            "<code>color-scheme: dark</code> is set on <code>:root</code> — without it a dark page still gets light scrollbars and light form widgets.",
            "Never remove the ring without replacing it; the offset exists so it reads on both <code>panel</code> and <code>ink</code>.",
          ],
          sources: ["web/reliquary/src/index.css"],
        },
      ],
    },
    {
      name: "Actions",
      cards: [
        {
          slug: "button",
          name: "Buttons",
          subtitle: "Primary / quiet / danger, four sizes",
          viewport: { width: 900, height: 760 },
          intent: `Three buttons, and only three. <b>Primary</b> is gold outline on a dark gold
            gradient and there is <i>one per custody state</i> — never several competing for
            the same decision. <b>Quiet</b> is mist on an edge border, for everything else.
            <b>Danger</b> is quiet that turns ember when you reach for it, so a destructive
            verb costs nothing to look at and announces itself the moment you aim.`,
          specimens: [
            {
              caption: "Variants · resting",
              html: `          ${btn("primary", "Check out")}\n          ${btn("quiet", "Download head")}\n          ${btn("danger", "Force release")}`,
            },
            {
              caption: "Variants · hover",
              html: `          ${btn("primary", "Check out", " is-hover")}\n          ${btn("quiet", "Download head", " is-hover")}\n          ${btn("danger", "Force release", " is-hover")}`,
            },
            {
              caption: "Sizes · sm 12px · default 13px · lg 14px · icon",
              html: `          ${btn("quiet", "Make head", " rq-btn--sm")}\n          ${btn("quiet", "Download head")}\n          ${btn("primary", "Sign in", " rq-btn--lg")}\n          <button class="rq-btn rq-btn--quiet rq-btn--icon" aria-label="More actions">${ICON.more}</button>`,
            },
            {
              caption: "Disabled",
              html: `          ${btn("primary", "Signing in…", " is-disabled")}\n          ${btn("quiet", "Check in…", " is-disabled")}`,
            },
            {
              caption: "One custody state, one primary — the shape every card takes",
              html: `          ${btn("primary", "Check out")}\n          ${btn("quiet", "Download head")}\n          ${btn("quiet", "History")}\n          <button class="rq-btn rq-btn--quiet rq-btn--icon" aria-label="More actions">${ICON.more}</button>`,
            },
          ],
          rules: [
            "Never two primaries in one action row. If a second verb feels primary, the custody state is being drawn wrong.",
            "A download is a real <code>&lt;a&gt;</code> (the browser must stream it); an in-app destination is a router link. Both wear the button, via <code>asChild</code>.",
            "Danger is never the primary <i>and</i> never hidden: it lives in the quiet row or the overflow menu, and it always confirms.",
            "Labels are verbs in the vault's own words — “Check out”, “Make head”, “Force release” — never “OK”, never “Submit”.",
          ],
          sources: ["web/reliquary/src/components/ui/button.tsx", "web/reliquary/src/components/WorldActions.tsx"],
        },
        {
          slug: "overflow-menu",
          name: "Overflow menu",
          subtitle: "The rare and admin verbs, one click away",
          viewport: { width: 900, height: 560 },
          intent: `Everything a world can do that the custody state is not asking for. The verbs
            stay reachable and stop competing with the one action that matters — which is the
            whole reason the card has room for a single primary.`,
          specimens: [
            {
              stage: "block",
              caption: "Trigger and open menu",
              html: `          <div style="display:flex;gap:16px;align-items:flex-start">
            <button class="rq-btn rq-btn--quiet rq-btn--icon" aria-label="More actions for Ashwood Hollow">${ICON.more}</button>
            <div class="rq-menu">
              <div class="rq-menuitem">Host on the dedicated server</div>
              <div class="rq-menuitem is-highlighted">Take back from the server</div>
              <div class="rq-menuitem">Import a save…</div>
              <div class="rq-menusep"></div>
              <div class="rq-menuitem rq-menuitem--danger">Force release</div>
              <div class="rq-menuitem rq-menuitem--danger is-highlighted">Delete world</div>
            </div>
          </div>`,
            },
          ],
          rules: [
            "Admin-only items are rendered only for admins — an item that answers “403” is worse than no item.",
            "Danger items highlight to ember on a 10% ember wash, not to a filled red row.",
            "A separator precedes the destructive block; nothing follows it.",
          ],
          sources: ["web/reliquary/src/components/ui/menu.tsx", "web/reliquary/src/lib/worldActions.ts"],
        },
      ],
    },
    {
      name: "Forms",
      cards: [
        {
          slug: "fields",
          name: "Fields",
          subtitle: "Label above, input on ink",
          viewport: { width: 900, height: 620 },
          intent: `One field shape. The label is small caps in mist and always sits above its
            input — never beside it, never as a placeholder. The input's ground is
            <code>ink</code>, one shade below the panel it sits on, so a field reads as a hole
            in the surface rather than a raised control.`,
          specimens: [
            {
              stage: "block",
              caption: "A field",
              html: `          <div style="max-width:320px">
            <label class="rq-label" for="w">World name</label>
            <input class="rq-input" id="w" style="margin-top:4px" value="Ashwood Hollow" />
          </div>`,
            },
            {
              stage: "block",
              caption: "An inline admin form — dashed well, fields, one primary",
              html: `          <form class="rq-well" style="display:flex;flex-wrap:wrap;align-items:flex-end;gap:12px">
            <div style="width:180px"><label class="rq-label" for="u">Username</label><input class="rq-input" id="u" style="margin-top:4px" placeholder="rook" /></div>
            <div style="width:140px"><label class="rq-label" for="r">Role</label><input class="rq-input" id="r" style="margin-top:4px" placeholder="user" /></div>
            ${btn("primary", "Add user")}
          </form>`,
            },
            {
              stage: "block",
              caption: "An error — mono, ember, under the field it belongs to",
              html: `          <div style="max-width:320px">
            <label class="rq-label" for="p">Password</label>
            <input class="rq-input" id="p" type="password" style="margin-top:4px" value="wrong" />
            <div class="rq-login-err">401 — that username and password do not match</div>
          </div>`,
            },
          ],
          rules: [
            "Errors quote the server's own words. A message the operator can grep for beats a message that reads nicely.",
            "Placeholders are examples, never labels.",
            "The whole form is a dashed well when it is an aside on a page that is mostly records.",
          ],
          sources: ["web/reliquary/src/components/ui/input.tsx", "web/reliquary/src/components/ui/label.tsx"],
        },
        {
          slug: "dialog",
          name: "Confirm dialog",
          subtitle: "What the action costs, not that it is irreversible",
          viewport: { width: 900, height: 620 },
          intent: `Replaces <code>window.confirm</code> for every verb that used to sit behind
            one. The body says what the action <i>costs</i> — “Anything they have not sent is
            left on their machine” — because “are you sure?” tells a player nothing they did
            not already know.`,
          specimens: [
            {
              stage: "block",
              caption: "Confirming a normal verb",
              html: `          <div class="rq-dialog">
            <button class="rq-dialog-x" aria-label="Close">${ICON.close}</button>
            <h2 class="rq-dialog-title">Make v37 the canonical head?</h2>
            <p class="rq-dialog-body">The next player to check this world out gets this version.</p>
            <div class="rq-dialog-foot">${btn("quiet", "Cancel")}${btn("primary", "Make head")}</div>
          </div>`,
            },
            {
              stage: "block",
              caption: "Confirming a destructive one",
              html: `          <div class="rq-dialog">
            <button class="rq-dialog-x" aria-label="Close">${ICON.close}</button>
            <h2 class="rq-dialog-title">Force release hazel's hold?</h2>
            <p class="rq-dialog-body">Anything they have not sent is left on their machine. Their late check-in will be kept and flagged as a conflict, not lost.</p>
            <div class="rq-dialog-foot">${btn("quiet", "Cancel")}${btn("danger", "Force release")}</div>
          </div>`,
            },
          ],
          rules: [
            "The confirm button repeats the verb (“Force release”), never “OK”.",
            "Cancel is quiet and comes first; the committing button is last and on the right.",
            "The trigger is whatever the caller drew — a quiet button, a menu item — so the dialog never dictates how the verb looks.",
          ],
          sources: ["web/reliquary/src/components/ConfirmDialog.tsx", "web/reliquary/src/components/ui/dialog.tsx"],
        },
      ],
    },
    {
      name: "Status",
      cards: [
        {
          slug: "custody-chip",
          name: "Custody chip",
          subtitle: "Free / Held / Hold expired",
          viewport: { width: 900, height: 640 },
          intent: `The one thing a player needs from across the room: can I take this world?
            The chip and the card's primary action both read <code>custodyOf(status)</code>,
            so the two can never disagree — the chip <i>is</i> the reason the button says what
            it says.`,
          specimens: [
            { caption: "The three states", html: `          ${CHIP_FREE}\n          ${CHIP_HELD}\n          ${CHIP_EXPIRED}` },
            {
              stage: "stack",
              caption: "Each with the sentence beside it, and the action it drives",
              html: `          <div class="rq-card-line">${CHIP_FREE}<span>nobody holds this world · next claim: rook</span>${btn("primary", "Check out")}</div>
          <div class="rq-card-line">${CHIP_HELD}<span>held by hazel until 18:40</span>${btn("quiet", "Ask for it back")}</div>
          <div class="rq-card-line">${CHIP_EXPIRED}<span>held by hazel (on the dedicated server) — claimable</span>${btn("primary", "Claim it")}</div>`,
            },
            {
              stage: "block",
              caption: "A request nobody can see the state of is a request nobody trusts",
              html: `          <div class="rq-card-asked">waiting for hazel's companion to check in and release · asked 18:12 — it answers within a minute of being online; if their machine is asleep the request stands until it wakes.</div>`,
            },
          ],
          rules: [
            "Green is not “go” — it is “nobody holds this”. Gold is not “warning” — it is “someone does”.",
            "Every chip carries both an icon and a word; neither alone.",
            "“Hold expired” is ember but not an error: the world is claimable, and the holder's late check-in is still accepted and flagged.",
          ],
          sources: ["web/reliquary/src/components/CustodyChip.tsx", "web/reliquary/src/lib/types.ts"],
        },
        {
          slug: "badge",
          name: "Version badges",
          subtitle: "HEAD / CONFLICT / CHECKPOINT",
          viewport: { width: 900, height: 520 },
          intent: `A monospace stamp on the page ground, colored by what it <i>means</i> rather
            than by rank. HEAD is what the next player gets. CONFLICT is a check-in from a hold
            that could no longer move the head — accepted and flagged rather than lost, waiting
            for an admin to pick a head. CHECKPOINT is a mid-session snapshot, which never moved
            the head in the first place.`,
          specimens: [
            {
              caption: "The vocabulary",
              html: `          <span class="rq-badge rq-badge--head">HEAD</span>
          <span class="rq-badge rq-badge--conflict">CONFLICT</span>
          <span class="rq-badge rq-badge--checkpoint">CHECKPOINT</span>
          <span class="rq-badge rq-badge--muted">ADMIN</span>`,
            },
          ],
          rules: [
            "Badges never carry an action. They say what a row <i>is</i>; the buttons on the right say what you can do about it.",
            "CONFLICT always appears with the sentence that explains it — the badge alone is a puzzle.",
            "Muted is for classification with no state attached (a user's role), not for a fourth kind of version.",
          ],
          sources: ["web/reliquary/src/components/ui/badge.tsx", "web/reliquary/src/components/VersionRow.tsx"],
        },
        {
          slug: "feedback",
          name: "Toasts and the live dot",
          subtitle: "Every action says what happened, or why it didn't",
          viewport: { width: 900, height: 560 },
          intent: `The old page's one-line status readout, as toasts in the bottom-right. Every
            action says what happened — “hold extended”, “asked — their companion answers within
            a minute” — or why it didn't, in the server's own words. The live dot in the sidebar
            footer says whether the custody stream is still open; a stale page that looks fresh
            is the failure mode this exists to prevent.`,
          specimens: [
            {
              stage: "stack",
              caption: "Toasts",
              html: `          <div class="rq-toast rq-toast--success"><span>Checked out Ashwood Hollow</span><span class="rq-toast-desc">held by you until 21:14 · your companion has the save</span></div>
          <div class="rq-toast rq-toast--error"><span>Could not force release</span><span class="rq-toast-desc">409 — the hold was already released</span></div>
          <div class="rq-toast"><span>Asked hazel to check in</span><span class="rq-toast-desc">their companion answers within a minute of being online</span></div>`,
            },
            {
              caption: "Live dot",
              html: `          <span class="rq-side-live"><span class="rq-dot rq-dot--live"></span>live · reliquary v1.9.2</span>
          <span class="rq-side-live"><span class="rq-dot rq-dot--down"></span>reconnecting… · reliquary v1.9.2</span>`,
            },
          ],
          rules: [
            "A failed action shows the server's status and message verbatim. Do not rewrite a 409 into “something went wrong”.",
            "The build string rides beside the live dot on every screen including login: a bug report about save sync should be able to name the build without anyone opening a container.",
            "One stream for the whole app, opened by the shell — a page that mounts and unmounts must not take the connection with it.",
          ],
          sources: ["web/reliquary/src/components/ui/toaster.tsx", "web/reliquary/src/lib/live.ts", "web/reliquary/src/components/AppShell.tsx"],
        },
      ],
    },
    {
      name: "Patterns",
      cards: [
        {
          slug: "cover-art",
          name: "Cover art",
          subtitle: "The image, and the tile for when there isn't one",
          viewport: { width: 900, height: 480 },
          intent: `A world's cover is the same art the companion's shelf shows, so the two views
            of one world look like one world. With no cover — IGDB unconfigured, a game it does
            not know, a lookup that failed — it falls back to the game's name in a gradient
            tile: never a broken image, never an error.`,
          specimens: [
            {
              caption: "Card size (84×112) and detail size (96×128), both fallbacks",
              html: `          <div class="rq-cover rq-cover--fallback">Dragonwilds</div>
          <div class="rq-cover rq-cover--fallback rq-cover--detail">Enshrouded</div>
          <div class="rq-cover rq-cover--fallback" style="width:64px;height:85px">Palworld</div>`,
            },
          ],
          rules: [
            "The fallback shows the game title, falling back to the world name — never a placeholder glyph.",
            "An <code>onError</code> swaps a broken image for the tile; the player never sees a torn-image icon.",
            "Covers are lazy-loaded and the <code>alt</code> is empty: the world's name is already beside it, so the image is decoration.",
          ],
          sources: ["web/reliquary/src/components/CoverArt.tsx", "web/reliquary/src/lib/art.ts"],
        },
        {
          slug: "world-card",
          name: "World card",
          subtitle: "Cover, custody, and the single action custody calls for",
          viewport: { width: 940, height: 780 },
          intent: `One world on the shelf. The cover names it, the chip says whether you can take
            it, and exactly one primary button does the thing the chip implies. Everything rarer
            is a click away — the world's own page for history and settings, the overflow menu
            for the admin verbs.`,
          specimens: [
            {
              stage: "stack",
              caption: "Free — the primary is “Check out”",
              html: worldCard({
                name: "Ashwood Hollow",
                game: "Dragonwilds",
                head: "head v41 · 1.8 GB · 18:02",
                chip: CHIP_FREE,
                line: "nobody holds this world",
                primary: btn("primary", "Check out"),
                quiet: ["Download head", "History"],
              }),
            },
            {
              stage: "stack",
              caption: "Held by someone else — the primary becomes the ask, and the request is visible",
              html: worldCard({
                name: "Cinderfall",
                game: "Enshrouded",
                head: "head v12 · 640 MB · yesterday",
                chip: CHIP_HELD,
                line: "held by hazel until 18:40 · next claim: rook",
                asked: "waiting for hazel's companion to check in and release · asked 18:12",
                primary: btn("quiet", "Ask for it back"),
                quiet: ["Download head", "History"],
              }),
            },
            {
              stage: "stack",
              caption: "Hold expired — claimable, and the primary says so",
              html: worldCard({
                name: "Verdant Reach",
                game: "Palworld",
                head: "head v7 · 210 MB · 3 days ago",
                chip: CHIP_EXPIRED,
                line: "held by rook (on the dedicated server) — claimable",
                primary: btn("primary", "Claim it"),
                quiet: ["Download head", "History"],
              }),
            },
          ],
          rules: [
            "The chip and the primary are computed from the same <code>custodyOf()</code> call. They cannot disagree.",
            "The head line is mono and right-aligned: version, size, time — the three facts an operator asks for first.",
            "Cover and title both link to the world's page; the card itself is not a link, because it contains buttons.",
          ],
          sources: [
            "web/reliquary/src/components/WorldCard.tsx",
            "web/reliquary/src/lib/worldActions.ts",
            "web/reliquary/src/components/WorldActions.tsx",
          ],
        },
        {
          slug: "version-row",
          name: "Version row",
          subtitle: "One kept version, badges first",
          viewport: { width: 940, height: 520 },
          intent: `The history list. The badges are the whole point of the row — they are how an
            admin finds the head, spots the conflict, and ignores the checkpoints — so they sit
            immediately after the id and before the prose.`,
          specimens: [
            {
              stage: "block",
              caption: "A world's history",
              html: `          <div class="rq-tableshell">
            <div class="rq-versionrow">
              <span class="rq-vid">v41</span>
              <span style="display:flex;gap:6px"><span class="rq-badge rq-badge--head">HEAD</span></span>
              <span class="rq-vmeta">checkin by <b>hazel</b> · 1.8 GB · 2026-08-21 18:02</span>
              <span class="rq-vactions">${btn("quiet", "Download", " rq-btn--sm")}</span>
            </div>
            <div class="rq-versionrow">
              <span class="rq-vid">v40</span>
              <span style="display:flex;gap:6px"><span class="rq-badge rq-badge--conflict">CONFLICT</span></span>
              <span class="rq-vmeta">checkin by <b>rook</b> · 1.8 GB · 2026-08-21 17:55 — from a hold that could no longer move the head</span>
              <span class="rq-vactions">${btn("quiet", "Download", " rq-btn--sm")}${btn("quiet", "Make head", " rq-btn--sm")}</span>
            </div>
            <div class="rq-versionrow">
              <span class="rq-vid">v39</span>
              <span style="display:flex;gap:6px"><span class="rq-badge rq-badge--checkpoint">CHECKPOINT</span></span>
              <span class="rq-vmeta">checkpoint by <b>hazel</b> · 1.7 GB · 2026-08-21 16:30</span>
              <span class="rq-vactions">${btn("quiet", "Download", " rq-btn--sm")}${btn("quiet", "Make head", " rq-btn--sm")}</span>
            </div>
          </div>`,
            },
          ],
          rules: [
            "“Make head” is offered only to a user who may set one, and never on the row that already is the head.",
            "Download is a real link so the browser streams the tar; it wears the quiet button via <code>asChild</code>.",
            "Retention and total storage sit under the list, not in it.",
          ],
          sources: ["web/reliquary/src/components/VersionRow.tsx"],
        },
        {
          slug: "app-shell",
          name: "App shell",
          subtitle: "Sidebar, page header, footer identity",
          viewport: { width: 1000, height: 620 },
          intent: `The shell every signed-in page sits in. The sidebar names what this deployment
            <i>has</i>; the footer names who you are and whether the vault is still talking.
            Admin destinations are rendered only for admins — a nav item that answers
            “Failed to load users” is worse than no item. On mobile the same job is done by two
            slim pinned rows: identity and sign-out, then the nav as one scrollable pill row.`,
          specimens: [
            {
              stage: "block",
              caption: "Desktop shell",
              html: `          <div class="rq-shell">
            <nav class="rq-side">
              <div class="rq-side-brand">
                <div class="rq-side-name">Reliquary</div>
                <div class="rq-side-tag">the vault of shared worlds</div>
              </div>
              <div class="rq-nav">
${NAV.map(
  (n) =>
    `                <a class="rq-navitem${n.active ? " is-active" : ""}" href="#">${n.icon.replace(
      'class="rq-i"',
      'class="rq-i rq-i--lg"',
    )}<span>${n.label}</span>${n.admin ? `<span class="rq-navitem-admin">ADMIN</span>` : ""}</a>`,
).join("\n")}
              </div>
              <div class="rq-side-foot">
                <div class="rq-side-who">
                  <div class="rq-avatar">H</div>
                  <div>
                    <div class="rq-side-name2">hazel</div>
                    <div class="rq-side-role">admin · sign out</div>
                  </div>
                </div>
                <div class="rq-side-live"><span class="rq-dot rq-dot--live"></span>live · reliquary v1.9.2</div>
              </div>
            </nav>
            <div class="rq-main">
              <div class="rq-pagehead">
                <div>
                  <h1 class="rq-h1">Worlds</h1>
                  <div class="rq-pagesub">Every world the vault holds, and who has it right now.</div>
                </div>
                ${btn("primary", "New world")}
              </div>
              <div class="rq-mainbody">
                <div class="rq-well">You have no custody rights on this deployment — you can download heads and read history, but not check a world out.</div>
                <div class="rq-panel" style="color:rgb(var(--mist));font-size:13px">…world cards…</div>
              </div>
            </div>
          </div>`,
            },
          ],
          rules: [
            "The page header is a gold title, a line saying what the screen is for, and room on the right for its one primary action.",
            "The read-only notice is a banner on Worlds, not a disabled button on every card.",
            "The live dot and the build ride in the footer on desktop and beside the wordmark on mobile — both pinned, because both are useless once scrolled away.",
          ],
          sources: ["web/reliquary/src/components/AppShell.tsx"],
        },
        {
          slug: "login",
          name: "Login",
          subtitle: "Password box, and three distinguishable SSO hints",
          viewport: { width: 900, height: 760 },
          intent: `The one screen outside the shell. Its SSO behavior is load-bearing: try
            <code>/me</code>, then <code>/login/cloudflare</code>, and show <i>three
            distinguishable</i> hints — not configured, no assertion, other error — because
            "sign-in failed" sends an operator to the wrong half of the stack.`,
          specimens: [
            {
              stage: "block",
              caption: "Resting",
              html: `          <div class="rq-login-ground">
            <form class="rq-login">
              <div class="rq-login-mark">${ICON.shield}<div class="rq-login-name">Reliquary</div><div class="rq-login-tag">Sign in to the vault.</div></div>
              <label class="rq-label" for="lu" style="margin-top:6px">Username</label>
              <input class="rq-input" id="lu" style="margin-top:4px" />
              <label class="rq-label" for="lp" style="margin-top:10px">Password</label>
              <input class="rq-input" id="lp" type="password" style="margin-top:4px" />
              ${btn("primary", "Sign in", " rq-btn--lg").replace('class="rq-btn', 'style="margin-top:16px" class="rq-btn')}
              <div class="rq-login-build">reliquary v1.9.2</div>
            </form>
          </div>`,
            },
            {
              stage: "block",
              caption: "The three SSO hints",
              html: `          <div class="rq-login-hint">Cloudflare Access sign-in is <b>not configured on this server</b>. Even behind the tunnel, this deployment wants a username and password.</div>
          <div class="rq-login-hint">Cloudflare Access is configured, but <b>this request carried no assertion</b> — you are reaching the server directly rather than through the tunnel.</div>
          <div class="rq-login-err">502 — Cloudflare Access replied, but the vault could not verify the assertion</div>`,
            },
          ],
          rules: [
            "The build string appears here too, before anyone is signed in.",
            "The password error is the server's status and message, in mono, under the field.",
            "The ground is a radial gradient from <code>panel</code> to <code>ink</code> — the only gradient background in the app.",
          ],
          sources: ["web/reliquary/src/pages/Login.tsx"],
        },
      ],
    },
  ],
};
