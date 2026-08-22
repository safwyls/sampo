# The Artificer Companion — the save-sync client

One Go binary (`cmd/companion`, shipped as `artificer-companion.exe`)
that runs on a player's own machine and moves shared world saves
between it and the save-sync service, reliquary
(docs/save-sync-architecture.md). No installer, no service: a tray
icon, a local page on `127.0.0.1:8377`, a config file under the user's
config directory.

The tray menu shows **state, not narrative** — a transfer running (which
is when quitting is the one thing not to do), an error worth opening the
page for, or what this machine is holding, named. It deliberately does
not repeat the page footer's last-action text: that line is written for
somewhere with room, and it carries whole filesystem paths, which
stretched the menu across the screen. The tray icon and the page's
favicon are one file, `web/companion/public/favicon.ico`, embedded once
with the frontend and read back out of it by the tray — so they cannot
drift apart.

Born `wkcompanion`, the Dragonwilds character relay. That job retired
when the recon corrected itself — the world save carries a connected
player's sheet and wildskeeper reads and remembers those directly — so
the app is now solely the custody client, deliberately game-blind: one
binary for every game Artificer syncs. (The console side of the old
relay still accepts pushes from old wkcompanion builds;
`games/dragonwilds/docs/companion.md` is that contract.)

## What it does

1. **Finds installed games**, and shows them as a shelf of covers (art
   from IGDB, resolved by the service — see below). Steam's own metadata
   is the ground truth for what is installed (`libraryfolders.vdf` →
   `appmanifest_*.acf`, with `steamapps/common` folder names as the
   fallback when manifests are missing). Steam itself is found through
   the configured folder, then the Windows registry, then `STEAM_ROOT`,
   then the default install paths; the page shows the whole scan trail,
   so "no games found" names its own cause.

   **Save folders** come from four sources, strongest first:

   1. **The save-location catalogue** — the [Ludusavi
      manifest](https://github.com/mtkennerly/ludusavi-manifest) (MIT),
      ~13,000 games with known save paths, largely from PCGamingWiki.
      Curated per-game knowledge beats any shape that usually works, so
      it leads. The service holds it (`core/savedb`) and answers
      `/savehints` in batches; what travels is a path *template* in the
      manifest's placeholder vocabulary, because only the player's
      machine can resolve `<winLocalAppData>` or `<storeUserId>`.
      Expansion happens in the companion (`expand.go`): placeholders
      become paths, `<storeUserId>` becomes a wildcard (a machine can
      hold several Steam accounts and the manifest does not say which),
      and only folders that actually exist are offered. A placeholder
      this build does not know refuses the whole template rather than
      resolving half of it — half a path is a wrong path. Entries for
      another OS or another store are dropped, which is the Palworld
      trap: its Microsoft Store path is real on disk and wrong for a
      Steam install.
   2. `<steam>/userdata/<account>/<appid>/remote` (Steam Cloud — keyed by
      the app id from the game's own manifest, so not a guess at all,
      though plenty of games never write a save into it).
   3. A small built-in catalog of verified locations.
   4. A name search across `Saved Games`, `%LOCALAPPDATA%`,
      `%LOCALAPPDATA%Low`, `%APPDATA%` and `Documents\My Games`
      (OneDrive-redirected Documents included), one and two levels deep,
      preferring a `Saved`/`SaveGames`-style subfolder inside a match.

   Within that order, a folder holding files outranks an empty one — a
   game that has never been played leaves its save folder empty, and a
   game that has been played leaves the *wrong* one empty. Every
   candidate says where it came from, and the player confirms it —
   nothing syncs a guessed path unseen.

   The catalogue is optional in both directions: a deployment that
   cannot reach it (or sets `SAVEDB_DISABLED=1`) falls back to sources
   2–4, exactly as the companion worked before. `SAVEDB_URL` overrides
   the source. The vault's **Save-location catalogue** panel shows what
   loaded, when, and what failed, with a refresh button.
### Joining a world whose folder has an opaque name

A save folder has two halves. The **root** is machine-specific and
discoverable — `%LOCALAPPDATA%\Witchspire\Saved\SaveGames`. The **leaf**
is the world's own identity inside it, and Unreal games routinely make
it an id generated once and never renameable:
`K2hAc0p_LH74aymwOemkgg`. Everyone playing that world shares the leaf,
nobody can retype it, and the game will not create it until it has saved
there. Before this, the second player to join either had to be told the
exact string and make the folder by hand, or link their whole
`SaveGames` and sync every save they own.

Save bundles carry paths **relative to the linked folder**, so the leaf
never travels inside an archive. That is what makes the fix small:

- The player who creates a world has their chosen folder **split**. A
  catalogue or discovery root that contains it wins; failing that, a
  parent named like a save container (`SaveGames`, `Saved`, `Saves`)
  means the folder inside it is one save among several; failing both,
  the folder is the root and there is no leaf — which is right for every
  game that keeps a single save folder. The split is shown in the form
  before anything is recorded.
- The world stores the leaf (`sync_worlds.save_path`, migration 0027).
- A joining player picks only **their own save root**. The companion
  joins the world's leaf beneath it, **creates the folder**, and links
  that. The root must already exist, so a typo is refused rather than
  producing empty folders somewhere nobody meant.

The leaf is validated as what it is — a path that will be created on
someone else's machine — at the service and again in the companion:
relative, no traversal, no drive letters, no separators but `/`.

The first companion to record a leaf settles it; a later joiner
reporting its own metadata cannot overwrite it. An admin can correct it
in the vault's world settings.

**Where this can still go wrong**: if a game's leaf identifies the
*player* rather than the world, recreating the first player's folder
name on a second machine may produce a folder that game ignores. The
world's leaf is a default, not a mandate — link by hand to a different
folder if a game turns out to work that way, and tell us, because the
split rule is where that knowledge would live.

2. **Links games to worlds.** Click a tile: unlinked games open a link
   form in a modal over the shelf, linked ones (shown in colour, against
   the greyed-out rest) open what they point at. (The form was inline
   under its tile first; a full-width row wedged into the grid reflowed
   the shelf around it and, on a wide window, landed nowhere near the
   tile that opened it.) Linking tells
   the service which game a world belongs to and where its save lives
   here (`game_title`, `save_hint`, and a free-form JSON blob with the
   Steam app id); it can create the world and seed it with the folder's
   current save in the same step. Any number of worlds can be linked.
   Any folder at all can be linked by hand, discovery or no discovery.

   **The save folder is the one thing the form cannot guess**, so a game
   discovery found no candidate for is a question, not a failure: the
   form says so, refuses to submit without one, and reports whatever
   went wrong inside the modal rather than on the status line behind it.
   The check runs before anything reaches the service, too — creating
   the world first and validating second left an orphan world on the
   service whenever a link was refused (2026-08-21).
3. **Moves the saves.** Checkout installs a world's head into its
   folder (tmp-extract-and-swap, one `.pre-checkout` copy kept);
   check-in packages the folder and returns the hold; mid-session
   checkpoints push automatically as crash insurance; a queued claim's
   handoff is fetched without the player doing anything. An admin can
   also **ask** a hold to check in or checkpoint — the companion picks
   the request up on its next poll and answers it, which is how a world
   comes back from someone who went to bed still holding it. Packaging
   waits out a settle window on the folder's mtimes — the app is
   game-blind, so the settle window is the torn-save guard, and the
   service verifies every upload again.

4. **Starts the game** (2026-08-22, `launch.go`). Checking a world out
   is two halves of one intention — fetch the save, then play — so the
   button does both and reads *Check out & play*. **The order is not
   negotiable**: the save is installed first and the game started
   after. Launching first would have the game read the stale save and
   write over it at its first autosave, which is the failure this whole
   system exists to prevent.

   What gets opened is `steam://rungameid/<appId>` from the app id
   discovery recorded, or a **launch target** the player set on the
   link, which wins. That target is a path or a URI the desktop's own
   opener takes — an `.exe`, a `.lnk`, another launcher's URI scheme —
   deliberately **not** a command line: quoting a Windows path with
   spaces into arguments is a bug generator, and a shortcut carries its
   arguments already. A world with neither an app id nor a target (a
   folder linked by hand) is one the companion will not pretend it can
   start: the button goes back to *Check out & host* and says so in the
   link's own panel.

   The launch is the softer half. A game that will not start still
   leaves the player holding the world with its save on disk — the part
   that mattered — so the reason comes back for the page to show rather
   than failing a checkout that already succeeded. **Play** on a world
   already held here starts it again without touching custody.

   Only an *explicit* checkout launches. A queued claim's handoff is
   fetched in the background, possibly while nobody is at the machine,
   and starting a game there would be a surprise rather than a
   convenience. Switch the whole thing off in **Settings** ("Start the
   game when I check a world out") to take custody without opening
   anything; the setting defaults on, and a config written before it
   existed gets that default.

The credential is the player's personal sync token from the service's
page. Nothing leaves the machine until a service URL and token are set.
It never reaches the screen either: transport errors quote the URL they
failed on, and the token lives in that URL, so errors are scrubbed
before the page or the log sees them.

**How current the page is.** Custody is shared state — the whole point is
that someone else checks a world in — so the page polls the service
every few seconds while it is open and once a minute when it is not, its
own render being what tells the app somebody is looking. It says how
long ago it last heard from the service, and **Sync now** asks
immediately and reports plainly if the service cannot be reached. (Until
2026-08-22 the poll and the acting on it shared one one-minute gate, so
a world someone else released took up to a minute to appear and forcing
a sync from the tray icon was the only cure.)

**Cover art** is resolved by the service, not here: reliquary holds the
IGDB credentials (a Twitch app's client id and secret, from the vault's
admin panel or `IGDB_CLIENT_ID`/`IGDB_CLIENT_SECRET`) and answers
`/artwork` for a whole batch, so one deployment looks each game up once
for everyone and no player's machine ever holds a credential. A service
without artwork configured yields names, which costs nothing but the
pictures. Covers appear everywhere a game is named — the companion's
shelf tile, its "Your worlds" row, and the vault's own world panels — so
the same world looks like the same world from either side. The vault
reads the Steam app id out of the `game_meta` blob the companion
reported, which is the one place anything interprets that field; the
service otherwise stores it without looking inside. Each page asks for
covers only when its set of games or worlds changes, never on its
refresh timer.

Covers are fetched whenever the discovered game set changes, not once at
page load. The boot-time-only fetch was a real defect, found on
2026-08-21: discovery is a filesystem walk that finishes *after* the
first render, so the single call always saw an empty shelf, asked for
nothing, and never ran again. The service's own counter read "0 asked"
while its credentials tested fine — no cover ever appeared, and nothing
anywhere said why.

Artwork degrades quietly, but it does not fail invisibly — a distinction
the first cut missed. Every IGDB error was swallowed, so a wrong secret
and a game IGDB has never heard of produced the same blank tile. The
vault's **Cover art** panel now reports the credential's source, the last
error in IGDB's own words, the hit/miss counts, and a **Test** button
that makes one real call; the companion shows its own last lookup error
under the shelf. Between them, "0 asked" on the service and a bare shelf
on the client are no longer the same picture. Two failures the panel
exists to name:

- **The Steam-id filter.** IGDB has spelled "this external id is a Steam
  app id" as both `category = 1` and `external_game_source = 1`. A
  rejected filter is a 400, not an empty result, and the first cut read
  it as "no such game" — for every game at once. The client now tries
  both and remembers which one answered (`status.filter` shows it).
- **A game with no Steam record.** IGDB carries plenty of games it has no
  `external_games` row for. Those now fall back to a name search under
  the same key, so the tile still gets its cover.

**Versions.** Both binaries are stamped at link time (`-X main.version`)
and show it: the companion's footer reads `companion <build> · service
<build>`, taking the service's from its own status call, and the vault's
page footer and login box show reliquary's. A report about a transfer
that names one half names nothing.

## Getting it

- **GitHub releases**: every push to main rebuilds the exe onto the
  rolling `companion-latest` release
  (`.github/workflows/release-companion.yml`); version tags attach it
  to their own releases.

Both routes keep the **same file name at the same URL** for every build,
which is what makes the download link worth handing to a player once —
and also what makes a stale copy easy to end up with. The service's
download sends `Cache-Control: no-store` and an `X-Companion-Version`
header for that reason (`.exe` is in Cloudflare's default-cached
extension list, and browsers re-serve same-named downloads). GitHub's
release assets are outside that control: if `companion-latest` looks a
build behind, hard-reload the release page, and check what you actually
got — the companion's footer names its own build, and the vault's
companion panel names the one it ships.
- **The service hands it out**: the reliquary image bundles the exe and
  serves it behind each player's token
  (`/api/public/sync/{token}/companion/download`).
- **By hand**: `GOOS=windows GOARCH=amd64 go build -ldflags="-H windowsgui" ./cmd/companion`
  (the flag suppresses the console window; without it the window
  flashes once and closes — see `console_windows.go`).

To start it with Windows, drop a shortcut in `shell:startup` —
deliberately manual; an app that installs itself into startup is not
this repo's style.

## Config, and the migration chain

`%AppData%`-equivalent (`os.UserConfigDir()`) `artificer-companion/config.json`,
mode 0600: the service URL, the token, the linked worlds with their hold
state and per-link launch target, and whether checking out starts the
game (`launchOnCheckout`, absent = on). Older configs are read as fallbacks: the first Artificer
Companion cut's nested sync block maps forward, and a wkcompanion-era
file maps to its sync side only — a relay-only config maps to empty,
because its credential has nothing to authenticate any more. Logs go to
`companion.log` beside the config (a windowed build has no console).

## Reaching the service through an auth layer

The token-in-path endpoints are unauthenticated-with-token by design,
so anything that forces its own login in front of the service breaks
them: Cloudflare Access answers with its login page — HTTP 200, HTML.
Hit for real on 2026-08-19; the companion never counts such a 200 as
delivered and names the interceptor instead. Fixes, either of: a
bypass/service-auth policy for `/api/public/*`, or a direct/LAN
address.
