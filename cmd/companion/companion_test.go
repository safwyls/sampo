package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The config migration chain: wkcompanion-era and first-cut Artificer
// Companion configs both map forward, and a config with no sync side
// maps to empty — its relay credential has nothing to authenticate.
func TestConfigMigration(t *testing.T) {
	legacy := []byte(`{
		"consoleUrl": "https://wilds.example.com",
		"token": "companion-relay-token",
		"saveDir": "C:\\chars",
		"sync": {"token": "personal-sync-token", "worldId": 3, "worldDir": "C:\\worlds\\midgard", "sessionId": 7, "baseVersion": 5}
	}`)
	cfg, err := parseConfig(legacy)
	if err != nil {
		t.Fatalf("parse legacy: %v", err)
	}
	if cfg.ServerURL != "https://wilds.example.com" || cfg.Token != "personal-sync-token" {
		t.Errorf("migrated connection = %q %q", cfg.ServerURL, cfg.Token)
	}
	if len(cfg.Links) != 1 || cfg.Links[0].WorldID != 3 || cfg.Links[0].SessionID != 7 {
		t.Errorf("migrated link = %+v", cfg.Links)
	}

	relayOnly := []byte(`{"consoleUrl": "https://wilds.example.com", "token": "relay-only"}`)
	cfg, err = parseConfig(relayOnly)
	if err != nil {
		t.Fatalf("parse relay-only: %v", err)
	}
	if cfg.configured() {
		t.Errorf("relay-only config should not map to a sync connection: %+v", cfg)
	}

	current := []byte(`{"serverUrl": "https://vault.example.com", "token": "tok", "links": [{"worldId": 1, "dir": "/w"}]}`)
	cfg, err = parseConfig(current)
	if err != nil || cfg.ServerURL != "https://vault.example.com" || len(cfg.Links) != 1 {
		t.Errorf("current shape mangled: %+v (%v)", cfg, err)
	}
}

// Steam discovery: the library list follows libraryfolders.vdf and the
// manifests yield name, app id and install dir.
func TestSteamDiscovery(t *testing.T) {
	root := t.TempDir()
	second := t.TempDir()
	main := filepath.Join(root, "steamapps")
	os.MkdirAll(main, 0o755)
	os.MkdirAll(filepath.Join(second, "steamapps"), 0o755)
	os.WriteFile(filepath.Join(main, "libraryfolders.vdf"), []byte(`
"libraryfolders"
{
	"0" { "path" "`+root+`" }
	"1" { "path" "`+second+`" }
}`), 0o644)
	os.WriteFile(filepath.Join(main, "appmanifest_1374490.acf"), []byte(`
"AppState"
{
	"appid"		"1374490"
	"name"		"RuneScape: Dragonwilds"
	"installdir"		"RSDragonwilds"
}`), 0o644)
	os.WriteFile(filepath.Join(second, "steamapps", "appmanifest_1234.acf"), []byte(`
"AppState"
{
	"appid"		"1234"
	"name"		"Some Other Game"
	"installdir"		"SomeOtherGame"
}`), 0o644)

	t.Setenv("STEAM_ROOT", root)
	found := discoverGames(nil)
	if len(found.Games) != 2 {
		t.Fatalf("found %d games, want 2: %+v", len(found.Games), found.Games)
	}
	byName := map[string]discoveredGame{}
	for _, g := range found.Games {
		byName[g.Name] = g
	}
	if g := byName["RuneScape: Dragonwilds"]; g.AppID != "1374490" || g.InstallDir != "RSDragonwilds" {
		t.Errorf("dragonwilds manifest misread: %+v", g)
	}
	if _, ok := byName["Some Other Game"]; !ok {
		t.Error("second library's manifest not found")
	}
	// The trail records both libraries as resolved hits.
	hits := 0
	for _, p := range found.Probes {
		if p.Resolved != "" && p.Note != "already scanned" {
			hits++
		}
	}
	if hits != 2 {
		t.Errorf("scan trail shows %d resolved libraries, want 2: %+v", hits, found.Probes)
	}
}

// Whatever the player pastes should land on the same library — and when
// it can't, the trail has to say why rather than dropping it silently.
// The quoted case is Windows Explorer's "Copy as path", which is how a
// pasted path most often arrives.
func TestSteamDirSpellings(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "steamapps")
	os.MkdirAll(filepath.Join(main, "common", "Palworld"), 0o755)
	os.WriteFile(filepath.Join(main, "appmanifest_1623730.acf"), []byte(`
"AppState"
{
	"appid"		"1623730"
	"name"		"Palworld"
	"installdir"		"Palworld"
}`), 0o644)
	// An empty auto-detect root, so only the configured folder can find
	// anything.
	t.Setenv("STEAM_ROOT", t.TempDir())
	if found := discoverGames(nil); len(found.Games) != 0 {
		t.Fatalf("empty root found %d games", len(found.Games))
	}

	for _, spelling := range []string{
		root,
		main,
		filepath.Join(main, "common"),
		filepath.Join(main, "common", "Palworld"),
		`"` + root + `"`,
		"  " + root + "  ",
		root + string(filepath.Separator),
	} {
		found := discoverGames([]string{spelling})
		if len(found.Games) != 1 || found.Games[0].Name != "Palworld" {
			t.Errorf("configured %q found %+v, want Palworld", spelling, found.Games)
			continue
		}
		if found.Probes[0].Resolved != main {
			t.Errorf("configured %q resolved to %q, want %q", spelling, found.Probes[0].Resolved, main)
		}
	}

	// A path that leads nowhere is reported, not dropped.
	found := discoverGames([]string{filepath.Join(root, "nope")})
	if len(found.Probes) == 0 || found.Probes[0].Resolved != "" || found.Probes[0].Note == "" {
		t.Errorf("a bad path should be reported with a reason: %+v", found.Probes)
	}
}

// A library whose manifests are missing still has games: the folders
// under common/ are the fallback, so the panel is never empty beside a
// library the player can see in Explorer.
func TestGamesFromCommonWithoutManifests(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "steamapps", "common", "Witchspire"), 0o755)
	os.MkdirAll(filepath.Join(root, "steamapps", "common", "Palworld"), 0o755)
	t.Setenv("STEAM_ROOT", root)

	found := discoverGames(nil)
	if len(found.Games) != 2 {
		t.Fatalf("found %d games from common/, want 2: %+v", len(found.Games), found.Games)
	}
	if found.Games[0].Name != "Palworld" || found.Games[1].Name != "Witchspire" {
		t.Errorf("games = %+v, want them sorted", found.Games)
	}
}

// A save folder round-trips through package + extract: same files, same
// contents, backup folders skipped, traversal refused.
func TestBundleRoundTrip(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "World.sav"), []byte("0123456789abcdef0123456789abcdef"), 0o644)
	os.MkdirAll(filepath.Join(src, "chunks"), 0o755)
	os.WriteFile(filepath.Join(src, "chunks", "0.dat"), []byte("chunkdata"), 0o644)
	os.MkdirAll(filepath.Join(src, "Backups"), 0o755)
	os.WriteFile(filepath.Join(src, "Backups", "old.sav"), []byte("stale"), 0o644)

	var buf bytes.Buffer
	if err := packageWorldDir(src, &buf); err != nil {
		t.Fatalf("package: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "restored")
	if err := extractBundleTo(&buf, dst); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, "World.sav")); string(data) != "0123456789abcdef0123456789abcdef" {
		t.Errorf("world did not round-trip: %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, "chunks", "0.dat")); string(data) != "chunkdata" {
		t.Errorf("nested file did not round-trip: %q", data)
	}
	if _, err := os.Stat(filepath.Join(dst, "Backups")); !os.IsNotExist(err) {
		t.Error("rolling backup folder was packaged")
	}
}

// The save finder against the shapes games actually use: Steam Cloud
// keyed by app id (exact), an Unreal-style folder under LOCALAPPDATA, a
// publisher folder two levels down, and a name that only matches after
// normalization.
func TestSaveCandidates(t *testing.T) {
	steam := t.TempDir()
	home := t.TempDir()
	local := filepath.Join(home, "AppData", "Local")
	lib := filepath.Join(steam, "steamapps")
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Steam Cloud: userdata/<account>/<appid>/remote, non-empty.
	cloud := filepath.Join(steam, "userdata", "1234567", "1623730", "remote")
	os.MkdirAll(cloud, 0o755)
	os.WriteFile(filepath.Join(cloud, "world.sav"), []byte("x"), 0o644)

	// Unreal shape: %LOCALAPPDATA%\<InstallDir>\Saved\SaveGames
	unreal := filepath.Join(local, "Palworld", "Saved", "SaveGames")
	os.MkdirAll(unreal, 0o755)

	got := saveCandidatesFor(discoveredGame{Name: "Palworld", AppID: "1623730", InstallDir: "Palworld"}, []string{lib}, nil)
	if len(got) < 2 {
		t.Fatalf("found %d candidates, want the cloud save and the Unreal folder: %+v", len(got), got)
	}
	if got[0].Path != cloud {
		t.Errorf("strongest candidate = %q, want the Steam Cloud save %q", got[0].Path, cloud)
	}
	if !strings.Contains(got[0].Why, "Steam Cloud") {
		t.Errorf("cloud candidate reason = %q", got[0].Why)
	}
	found := false
	for _, c := range got {
		if c.Path == unreal {
			found = true
		}
	}
	if !found {
		t.Errorf("the Unreal Saved/SaveGames folder was missed: %+v", got)
	}

	// A publisher folder in between, and a name that needs normalizing:
	// "RuneScape: Dragonwilds" living under Jagex\RSDragonwilds.
	pub := filepath.Join(home, "Documents", "My Games", "Jagex", "RSDragonwilds", "Saved")
	os.MkdirAll(pub, 0o755)
	got = saveCandidatesFor(discoveredGame{Name: "RuneScape: Dragonwilds", InstallDir: "RSDragonwilds"}, nil, nil)
	if len(got) == 0 || got[0].Path != pub {
		t.Errorf("publisher-nested save missed: %+v", got)
	}

	// A game with nothing anywhere gets nothing — no false positives.
	if got := saveCandidatesFor(discoveredGame{Name: "Some Game Nobody Installed", InstallDir: "NopeNopeNope"}, nil, nil); len(got) != 0 {
		t.Errorf("invented candidates for an absent game: %+v", got)
	}
}

// Artwork is asked for once games exist, and not before.
//
// The bug this pins down: the page fetched covers exactly once, at load,
// before the filesystem walk had found anything. artwork() saw an empty
// shelf, had nothing to look up, and returned without calling the
// service — which then reported "0 asked" beside credentials that tested
// fine. Nothing re-triggered it, so covers never appeared at all.
func TestArtworkAsksOnceGamesAreKnown(t *testing.T) {
	var batches [][]artQuery
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Games []artQuery `json:"games"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		batches = append(batches, in.Games)
		json.NewEncoder(w).Encode(map[string]any{
			"accepted": true,
			"art":      map[string]gameArt{"app:111": {Name: "Palworld", Cover: "https://img/co1.jpg"}},
		})
	}))
	defer srv.Close()

	a := newApp(Config{ServerURL: srv.URL, Token: "tok"}, filepath.Join(t.TempDir(), "config.json"))

	// Discovery hasn't run: nothing to ask about, and nothing asked.
	if art := a.artwork(); len(art) != 0 {
		t.Errorf("art = %v before discovery, want none", art)
	}
	if len(batches) != 0 {
		t.Fatalf("the service was asked %d times with no games known", len(batches))
	}

	// Discovery finishes. The next ask reaches the service — the step
	// that never used to happen.
	a.mu.Lock()
	a.discovered = discovery{Games: []discoveredGame{
		{Name: "Palworld", AppID: "111"},
		{Name: "Some Unknown Game", AppID: "222"},
	}}
	a.mu.Unlock()

	art := a.artwork()
	if len(batches) != 1 || len(batches[0]) != 2 {
		t.Fatalf("service batches = %v, want one batch of both games", batches)
	}
	if got := art["app:111"]; got.Name != "Palworld" || got.Cover == "" {
		t.Errorf("art for the known game = %+v, want the service's answer", got)
	}
	if _, ok := art["app:222"]; ok {
		t.Error("a game the service knows nothing about should be absent, not empty")
	}

	// Asking again re-uses the cache, misses included: a rescan must not
	// re-ask for games IGDB has never heard of.
	a.artwork()
	if len(batches) != 1 {
		t.Errorf("service asked %d times, want the second call served from cache", len(batches))
	}
	a.mu.Lock()
	asked, failure := a.artAsked, a.artError
	a.mu.Unlock()
	if asked != 2 || failure != "" {
		t.Errorf("asked = %d, artError = %q; want 2 and no failure", asked, failure)
	}
}

// A service that cannot answer says so, instead of leaving a bare shelf
// with no explanation.
func TestArtworkFailureIsRecorded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "upstream is down")
	}))
	defer srv.Close()

	a := newApp(Config{ServerURL: srv.URL, Token: "tok"}, filepath.Join(t.TempDir(), "config.json"))
	a.mu.Lock()
	a.discovered = discovery{Games: []discoveredGame{{Name: "Palworld", AppID: "111"}}}
	a.mu.Unlock()

	if art := a.artwork(); len(art) != 0 {
		t.Errorf("art = %v from a failing service, want none", art)
	}
	a.mu.Lock()
	failure := a.artError
	a.mu.Unlock()
	if failure == "" {
		t.Error("a failed artwork lookup left no explanation for the page to show")
	}
}

// The state the page reads never carries a JSON null where an array
// belongs.
//
// This is the whole bug, in one assertion. A nil Go slice marshals to
// null; the page did ST.links.length on it and threw — before the shelf,
// the scan trail, the version line or the artwork fetch had run. A fresh
// companion with nothing linked yet therefore showed no games at all,
// and it read as three unrelated faults rather than one.
func TestStateNeverMarshalsNullArrays(t *testing.T) {
	a := newApp(Config{}, filepath.Join(t.TempDir(), "config.json"))
	rec := httptest.NewRecorder()
	a.handleState(rec, httptest.NewRequest("GET", "/api/state", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("state: %d", rec.Code)
	}

	body := rec.Body.String()
	var out struct {
		Links      []WorldLink `json:"links"`
		Discovered struct {
			Games  []discoveredGame `json:"games"`
			Probes []probe          `json:"probes"`
		} `json:"discovered"`
		Config struct {
			SteamDirs []string `json:"steamDirs"`
		} `json:"config"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	// Decoding is not the test — null decodes to a nil slice quite
	// happily. The wire form is.
	for _, null := range []string{`"links":null`, `"games":null`, `"probes":null`, `"steamDirs":null`} {
		if strings.Contains(body, null) {
			t.Errorf("state carries %s; the page reads it as an array", null)
		}
	}
	if out.Links == nil || out.Discovered.Games == nil || out.Discovered.Probes == nil || out.Config.SteamDirs == nil {
		t.Errorf("state decoded a nil slice somewhere: %+v", out)
	}
	if out.Version == "" {
		t.Error("state carries no version; both UIs show it for bug reports")
	}
}

// A link that cannot succeed must not leave a world behind.
//
// createWorld used to POST /worlds first and validate the save folder
// second, so submitting the form without a folder — the one thing
// discovery genuinely cannot guess — created a world on the service and
// then failed locally. The page closed its modal and reported the
// failure off-screen, so the result read as "nothing happened on either
// end" while an orphan world sat on the service.
func TestCreateWorldChecksTheFolderBeforeCreatingAnything(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		json.NewEncoder(w).Encode(map[string]any{
			"accepted": true,
			"status":   map[string]any{"world": map[string]any{"id": 1}},
		})
	}))
	defer srv.Close()

	a := newApp(Config{ServerURL: srv.URL, Token: "tok"}, filepath.Join(t.TempDir(), "config.json"))

	for _, tc := range []struct{ name, dir, want string }{
		{"no folder at all", "", "a link needs a save folder"},
		{"a folder that isn't there", filepath.Join(t.TempDir(), "nope"), "save folder:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits = nil
			err := a.createWorld("midgard", "Palworld", tc.dir, "", "", "", false)
			if err == nil {
				t.Fatal("createWorld succeeded with an unusable save folder")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
			if len(hits) != 0 {
				t.Errorf("the service was called %v; a refused link must create nothing", hits)
			}
			a.mu.Lock()
			links := len(a.cfg.Links)
			a.mu.Unlock()
			if links != 0 {
				t.Errorf("%d links recorded after a refused create", links)
			}
		})
	}

	// A real folder goes through, and only then is a world created.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "world.sav"), []byte("save"), 0o600); err != nil {
		t.Fatalf("write save: %v", err)
	}
	hits = nil
	if err := a.createWorld("midgard", "Palworld", dir, `{"appId":"1623730"}`, "1623730", "", false); err != nil {
		t.Fatalf("createWorld with a real folder: %v", err)
	}
	if len(hits) == 0 || hits[0] != "POST /api/public/sync/tok/worlds" {
		t.Errorf("service calls = %v, want the world create first", hits)
	}
	a.mu.Lock()
	links := append([]WorldLink(nil), a.cfg.Links...)
	a.mu.Unlock()
	if len(links) != 1 || links[0].Dir != dir || links[0].WorldID != 1 {
		t.Errorf("links = %+v, want one link to the created world", links)
	}
	// The app id rides along so the worlds list can show the same cover
	// the shelf does.
	if links[0].AppID != "1623730" {
		t.Errorf("link app id = %q, want the discovered game's", links[0].AppID)
	}
}

// A Steam library is not a list of games, and the shelf says so.
func TestHiddenShelfEntries(t *testing.T) {
	junk := []discoveredGame{
		{Name: "Steamworks Common Redistributables", AppID: "228980"},
		{Name: "Steam Controller Configs", AppID: "241100"},
		{Name: "Proton 9.0", AppID: "2805730"}, // by name: the app id changes every release
		{Name: "Steam Linux Runtime 3.0 (sniper)", AppID: "1628350"},
	}
	game := discoveredGame{Name: "Palworld", AppID: "1623730"}

	var cfg Config
	for _, g := range junk {
		if !cfg.isHidden(g) {
			t.Errorf("%q (%s) shows on a fresh shelf; it is not a game", g.Name, g.AppID)
		}
	}
	if cfg.isHidden(game) {
		t.Errorf("%q is hidden by default; only Steam's own non-games should be", game.Name)
	}

	// The player's choice wins in both directions, and unhiding a
	// default sticks rather than reverting on the next read.
	cfg.setHidden(gameKey(game), true)
	if !cfg.isHidden(game) {
		t.Error("hiding a game did not take")
	}
	cfg.setHidden(gameKey(game), false)
	if cfg.isHidden(game) {
		t.Error("unhiding a game did not take")
	}
	cfg.setHidden(gameKey(junk[0]), false)
	if cfg.isHidden(junk[0]) {
		t.Error("unhiding a default-hidden entry did not stick")
	}
	// Flipping a decision replaces it rather than stacking entries.
	for i := 0; i < 4; i++ {
		cfg.setHidden(gameKey(game), i%2 == 0)
	}
	if len(cfg.Hidden) != 2 {
		t.Errorf("hidden list = %v; a repeated decision should replace, not accumulate", cfg.Hidden)
	}
}

// Browsing lists folders, points at the likely one, and reports an
// unreadable folder rather than dying on it.
func TestBrowse(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"Saved Games", "Config", ".hidden", "Logs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	res := browse(root)
	var names []string
	saveish := map[string]bool{}
	for _, e := range res.Entries {
		names = append(names, e.Name)
		saveish[e.Name] = e.Saveish
	}
	if strings.Join(names, ",") != "Config,Logs,Saved Games" {
		t.Errorf("entries = %v; want the folders, sorted, without dotfiles or files", names)
	}
	if !saveish["Saved Games"] || saveish["Config"] {
		t.Errorf("saveish flags = %v; want only the save-shaped folder marked", saveish)
	}
	if res.Parent == "" || res.Path != filepath.Clean(root) {
		t.Errorf("path/parent = %q/%q", res.Path, res.Parent)
	}
	if len(res.Roots) == 0 {
		t.Error("no shortcuts offered; the browser should open near where saves live")
	}

	// A path to a file browses its folder — pasting the full path to a
	// save file is a natural mistake with an obvious intent.
	if got := browse(filepath.Join(root, "readme.txt")); got.Path != filepath.Clean(root) {
		t.Errorf("browsing a file landed at %q, want its folder", got.Path)
	}
	// A quoted path (Windows "Copy as path") is cleaned, not rejected.
	if got := browse(`"` + root + `"`); got.Path != filepath.Clean(root) {
		t.Errorf("browsing a quoted path landed at %q, want %q", got.Path, root)
	}
	// A folder that isn't there explains itself instead of erroring out.
	missing := browse(filepath.Join(root, "nope"))
	if missing.Error == "" {
		t.Error("browsing a missing folder said nothing")
	}
}

// Expanding a manifest template into folders that actually exist.
func TestExpandTemplate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	local := filepath.Join(home, "AppData", "Local")
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))

	// Two Steam accounts, as a real machine often has.
	for _, id := range []string{"7656119", "7656120"} {
		if err := os.MkdirAll(filepath.Join(local, "Pal", "Saved", "SaveGames", id), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	got := expandTemplate("<winLocalAppData>/Pal/Saved/SaveGames/<storeUserId>", "Palworld", nil)
	if len(got) != 2 {
		t.Fatalf("expanded to %v, want both account folders", got)
	}
	for _, p := range got {
		if !strings.HasPrefix(p, local) {
			t.Errorf("expansion escaped the root: %q", p)
		}
	}

	// A folder the template names but this machine doesn't have yields
	// nothing — a suggestion that doesn't exist is worse than none.
	if got := expandTemplate("<winLocalAppData>/NotInstalled/Saves", "", nil); len(got) != 0 {
		t.Errorf("expanded a missing folder to %v", got)
	}

	// A placeholder this build doesn't know refuses the whole template
	// rather than resolving half of it to a wrong folder.
	if got := expandTemplate("<someFuturePlaceholder>/Saves", "", nil); len(got) != 0 {
		t.Errorf("an unknown placeholder expanded to %v", got)
	}
}

// Only the entries that apply to this machine and this store are used.
func TestManifestCandidatesFilterByOSAndStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	local := filepath.Join(home, "AppData", "Local")
	t.Setenv("LOCALAPPDATA", local)
	steamSave := filepath.Join(local, "Pal", "Saved", "SaveGames", "7656119")
	msSave := filepath.Join(local, "Packages", "PocketpairInc.Palworld", "SystemAppData", "wgs", "7656119")
	for _, d := range []string{steamSave, msSave} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	g := discoveredGame{Name: "Palworld", AppID: "1623730", InstallDir: "Palworld"}
	locs := []location{
		// The Microsoft Store path: real on this disk, wrong for a game
		// installed through Steam. This is the trap the store filter
		// exists for.
		{Template: "<winLocalAppData>/Packages/PocketpairInc.Palworld/SystemAppData/wgs/<storeUserId>", OS: "windows", Store: "microsoft"},
		{Template: "<winLocalAppData>/Pal/Saved/SaveGames/<storeUserId>", OS: "windows"},
		{Template: "<xdgData>/pal", OS: "linux"},
	}

	// hostOS() decides which entries apply, so assert against it rather
	// than against whichever machine runs the suite.
	got := manifestCandidates(g, locs, nil)
	var paths []string
	for _, c := range got {
		paths = append(paths, c.Path)
	}
	for _, p := range paths {
		if strings.Contains(p, "Packages") {
			t.Errorf("a Microsoft Store location was offered for a Steam game: %v", paths)
		}
	}
	if hostOS() == "windows" {
		if len(got) != 1 || got[0].Path != steamSave {
			t.Errorf("candidates = %v, want just the Steam save folder", paths)
		}
		if !strings.Contains(got[0].Why, "Ludusavi") {
			t.Errorf("candidate reason = %q, want it to name the catalogue", got[0].Why)
		}
	} else if len(got) != 0 {
		t.Errorf("windows-only locations were offered on %s: %v", hostOS(), paths)
	}
}

// A folder with saves in it outranks an empty one of the same kind.
func TestEmptyCandidatesSink(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "empty")
	full := filepath.Join(root, "full")
	for _, d := range []string{empty, full} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(full, "world.sav"), []byte("save"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := saveCandidatesFor(discoveredGame{Name: "X"}, nil, []saveCandidate{
		{Path: empty, Why: "first"}, {Path: full, Why: "second"},
	})
	if len(got) != 2 || got[0].Path != full {
		t.Errorf("candidates = %+v, want the folder with saves in it first", got)
	}
}

// Splitting a save folder into the half a joining player supplies and
// the half the world carries.
func TestSplitSaveDir(t *testing.T) {
	root := t.TempDir()
	saveGames := filepath.Join(root, "Witchspire", "Saved", "SaveGames")
	world := filepath.Join(saveGames, "K2hAc0p_LH74aymwOemkgg")
	if err := os.MkdirAll(world, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// A catalogue root that contains the folder settles it outright.
	got := splitSaveDir(world, []string{saveGames})
	if got.Root != saveGames || got.Leaf != "K2hAc0p_LH74aymwOemkgg" {
		t.Errorf("with a known root: %+v", got)
	}

	// Without one, a parent named like a save container is the giveaway —
	// this is the Unreal shape, and the whole reason the leaf is opaque.
	got = splitSaveDir(world, nil)
	if got.Root != saveGames || got.Leaf != "K2hAc0p_LH74aymwOemkgg" {
		t.Errorf("by SaveGames parent: %+v", got)
	}
	if got.Why == "" {
		t.Error("the split gave no reason; a guess nobody can see is a guess nobody can correct")
	}

	// A game with one save folder and nothing beneath it has no leaf to
	// reproduce, which is the ordinary case and must stay silent.
	plain := filepath.Join(root, "SomeGame")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := splitSaveDir(plain, nil); got.Leaf != "" || got.Root != plain {
		t.Errorf("a plain save folder split anyway: %+v", got)
	}

	// A known root equal to the folder is not a split either.
	if got := splitSaveDir(saveGames, []string{saveGames}); got.Leaf != "" {
		t.Errorf("the root split against itself: %+v", got)
	}

	// Nested leaves survive, slash-separated whatever the platform.
	deep := filepath.Join(saveGames, "profile", "slot1")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := splitSaveDir(deep, []string{saveGames}); got.Leaf != "profile/slot1" {
		t.Errorf("nested leaf = %q, want slash-separated", got.Leaf)
	}
}

// Joining a world creates its folder under the player's own root — and
// refuses anything that would put it somewhere else.
func TestPrepareWorldDir(t *testing.T) {
	root := t.TempDir()

	dir, err := prepareWorldDir(root, "K2hAc0p_LH74aymwOemkgg")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	want := filepath.Join(root, "K2hAc0p_LH74aymwOemkgg")
	if dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("the world's folder was not created: %v", err)
	}

	// Doing it twice is not an error: a player relinking should not have
	// to care whether the folder is already there.
	if _, err := prepareWorldDir(root, "K2hAc0p_LH74aymwOemkgg"); err != nil {
		t.Errorf("second prepare: %v", err)
	}

	// No leaf means the root is the world's folder.
	if got, err := prepareWorldDir(root, ""); err != nil || got != filepath.Clean(root) {
		t.Errorf("empty leaf = %q, %v", got, err)
	}

	// A root that isn't there is refused, rather than conjuring a tree of
	// empty folders somewhere nobody meant.
	missing := filepath.Join(root, "nope")
	if _, err := prepareWorldDir(missing, "world"); err == nil {
		t.Error("a missing root was accepted")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("a refused prepare created folders anyway")
	}

	// Traversal cannot escape the root the player chose.
	for _, leaf := range []string{"../escape", "a/../../escape", "/absolute", `..\windows`} {
		if _, err := joinSavePath(root, leaf); err == nil {
			t.Errorf("joinSavePath accepted %q", leaf)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); err == nil {
		t.Error("a traversal leaf escaped the root")
	}
}

// An open page keeps the custody view current; a closed one lets the
// background clock be polite.
//
// The bug this pins: syncTick gated *everything* — the poll and the
// acting on it — behind a one-minute timer, and the page's own five-
// second poll only ever re-read a cached snapshot. So a world someone
// else checked in took up to a minute to appear, and forcing a sync from
// the tray was the only way to see it, because that called the refresh
// directly.
func TestPagePollDrivesFreshness(t *testing.T) {
	a := newApp(Config{ServerURL: "http://example.invalid", Token: "tok"}, filepath.Join(t.TempDir(), "config.json"))

	a.mu.Lock()
	idle := a.pollIntervalLocked()
	a.mu.Unlock()
	if idle != syncPollEvery {
		t.Errorf("interval with nobody looking = %s, want the polite one (%s)", idle, syncPollEvery)
	}

	// A page request is what "somebody is looking" means.
	rec := httptest.NewRecorder()
	a.handleState(rec, httptest.NewRequest("GET", "/api/state", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("state: %d", rec.Code)
	}
	a.mu.Lock()
	watching := a.pollIntervalLocked()
	seen := a.pageSeen
	a.mu.Unlock()
	if watching != syncPollWatching {
		t.Errorf("interval while watched = %s, want %s", watching, syncPollWatching)
	}
	if seen.IsZero() {
		t.Error("the page request was not noticed")
	}
	if syncPollWatching >= syncPollEvery {
		t.Error("the watched interval is not shorter than the idle one")
	}

	// And it lapses, so a page left open in a closed laptop does not
	// keep the poll running hot forever.
	a.mu.Lock()
	a.pageSeen = time.Now().Add(-pageWatchWindow - time.Second)
	lapsed := a.pollIntervalLocked()
	a.mu.Unlock()
	if lapsed != syncPollEvery {
		t.Errorf("interval after the watch window = %s, want %s", lapsed, syncPollEvery)
	}
}

// The player's sync token never reaches the screen.
//
// Transport errors quote the URL they failed on, and the token lives in
// that URL — so a refused connection printed the credential into the
// page's error line, and into any screenshot sent for help.
func TestSyncErrorsDoNotLeakTheToken(t *testing.T) {
	a := newApp(Config{ServerURL: "http://127.0.0.1:1", Token: "supersecrettoken"}, filepath.Join(t.TempDir(), "config.json"))
	err := a.syncRefresh()
	if err == nil {
		t.Fatal("a refused connection reported success")
	}
	if strings.Contains(err.Error(), "supersecrettoken") {
		t.Errorf("the error carries the sync token: %v", err)
	}
	if !strings.Contains(err.Error(), "<your sync token>") {
		t.Errorf("the error does not say what was redacted: %v", err)
	}
	a.mu.Lock()
	shown := a.worldSync.LastError
	a.mu.Unlock()
	if strings.Contains(shown, "supersecrettoken") {
		t.Errorf("the page's error line carries the sync token: %q", shown)
	}
}

// --- starting the game once the save is in place (launch.go) ---

func TestLaunchTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		link *WorldLink
		want string
	}{
		{"a Steam game launches through Steam", &WorldLink{AppID: "1623730"}, "steam://rungameid/1623730"},
		{"an override wins over the app id", &WorldLink{AppID: "1623730", LaunchTarget: `D:\Games\modded.lnk`}, `D:\Games\modded.lnk`},
		{"an override alone is enough", &WorldLink{LaunchTarget: "com.example.launcher://play"}, "com.example.launcher://play"},
		// A folder linked by hand carries no app id and nothing that says
		// what starts it. The companion must not guess.
		{"a hand-linked folder has nothing to start", &WorldLink{}, ""},
		{"whitespace is not a launch target", &WorldLink{LaunchTarget: "   "}, ""},
		{"no link at all", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := launchTarget(tc.link); got != tc.want {
				t.Errorf("launchTarget = %q, want %q", got, tc.want)
			}
			if launchable(tc.link) != (tc.want != "") {
				t.Errorf("launchable = %v for target %q", launchable(tc.link), tc.want)
			}
		})
	}
}

func TestLaunchRefusesWhatItCannotStart(t *testing.T) {
	a := newApp(Config{Links: []WorldLink{{WorldID: 1, Dir: t.TempDir()}}}, filepath.Join(t.TempDir(), "config.json"))
	opened := 0
	restore := stubLaunch(func(string) error { opened++; return nil })
	defer restore()

	if err := a.launch(2); err == nil || !strings.Contains(err.Error(), "link this world") {
		t.Errorf("launching an unlinked world = %v, want it to say the world is not linked", err)
	}
	// The one that is linked but has nothing to start says *why*, and
	// says what would fix it, rather than failing silently.
	err := a.launch(1)
	if err == nil {
		t.Fatal("launch succeeded for a world with no app id and no launch target")
	}
	if !strings.Contains(err.Error(), "no Steam app id") || !strings.Contains(err.Error(), "launch target") {
		t.Errorf("error = %q, want it to name the cause and the fix", err)
	}
	if opened != 0 {
		t.Errorf("the desktop opener ran %d times for a world with nothing to start", opened)
	}
}

// The whole point of the feature, and the one ordering that must never
// invert: the save is in place *before* the game starts. Launching first
// would have the game load the stale save and write over it at its first
// autosave.
func TestCheckoutInstallsTheSaveBeforeStartingTheGame(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "World.sav"), []byte("the world as the service holds it"), 0o600); err != nil {
		t.Fatalf("write source save: %v", err)
	}
	var bundle bytes.Buffer
	if err := packageWorldDir(source, &bundle); err != nil {
		t.Fatalf("package: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			w.Write(bundle.Bytes())
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"accepted": true,
			"world":    "Emberfall",
			"session":  map[string]any{"id": 7, "baseVersion": 41},
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "World.sav"), []byte("stale local save"), 0o600)
	a := newApp(Config{
		ServerURL: srv.URL,
		Token:     "tok",
		Links:     []WorldLink{{WorldID: 1, Dir: dir, AppID: "1623730"}},
	}, filepath.Join(t.TempDir(), "config.json"))

	var launchedWith string
	var sawAtLaunch string
	restore := stubLaunch(func(uri string) error {
		launchedWith = uri
		// What is on disk at the moment the game is told to start.
		data, _ := os.ReadFile(filepath.Join(dir, "World.sav"))
		sawAtLaunch = string(data)
		return nil
	})
	defer restore()

	launched, launchErr, err := a.checkoutAndPlay(1, false)
	if err != nil {
		t.Fatalf("checkoutAndPlay: %v", err)
	}
	if launchErr != nil {
		t.Fatalf("launch error: %v", launchErr)
	}
	if !launched {
		t.Fatal("checkout did not start the game")
	}
	if launchedWith != "steam://rungameid/1623730" {
		t.Errorf("launched %q, want the world's Steam run URI", launchedWith)
	}
	if sawAtLaunch != "the world as the service holds it" {
		t.Errorf("the save on disk when the game started was %q — the checked-out save must be in place first", sawAtLaunch)
	}
}

// The launch is the softer half. A game that will not start still leaves
// the player holding the world with its save on disk, which is the part
// that mattered — so the reason comes back rather than failing a
// checkout that already succeeded.
func TestAFailedLaunchDoesNotFailTheCheckout(t *testing.T) {
	a, dir := appWithCheckoutStub(t, "1623730")
	restore := stubLaunch(func(string) error { return errors.New("steam is not installed") })
	defer restore()

	launched, launchErr, err := a.checkoutAndPlay(1, false)
	if err != nil {
		t.Fatalf("checkoutAndPlay: %v", err)
	}
	if launched {
		t.Error("reported a launch that failed")
	}
	if launchErr == nil || !strings.Contains(launchErr.Error(), "steam is not installed") {
		t.Errorf("launch error = %v, want the opener's own reason", launchErr)
	}
	// The custody half stuck.
	if l := a.link(1); l == nil || l.SessionID != 7 {
		t.Errorf("link after a failed launch = %+v, want the hold recorded", l)
	}
	if _, err := os.Stat(filepath.Join(dir, "World.sav")); err != nil {
		t.Errorf("the save is not on disk after a failed launch: %v", err)
	}
}

func TestLaunchOnCheckoutSetting(t *testing.T) {
	// Absent means on: this is the behaviour people asked for, and a
	// config written before the setting existed should get it.
	if !(Config{}).launchOnCheckout() {
		t.Error("a config with no setting does not launch on checkout")
	}
	off, on := false, true
	if (Config{LaunchOnCheckout: &off}).launchOnCheckout() {
		t.Error("an explicit false still launches")
	}
	if !(Config{LaunchOnCheckout: &on}).launchOnCheckout() {
		t.Error("an explicit true does not launch")
	}

	t.Run("switched off, the save still arrives", func(t *testing.T) {
		a, dir := appWithCheckoutStub(t, "1623730")
		a.mu.Lock()
		a.cfg.LaunchOnCheckout = &off
		a.mu.Unlock()
		opened := 0
		restore := stubLaunch(func(string) error { opened++; return nil })
		defer restore()

		launched, launchErr, err := a.checkoutAndPlay(1, false)
		if err != nil || launchErr != nil {
			t.Fatalf("checkoutAndPlay: %v / %v", err, launchErr)
		}
		if launched || opened != 0 {
			t.Error("the game was started with the setting switched off")
		}
		if _, err := os.Stat(filepath.Join(dir, "World.sav")); err != nil {
			t.Errorf("the save is not on disk: %v", err)
		}
	})

	t.Run("a world with nothing to start is not an error", func(t *testing.T) {
		a, _ := appWithCheckoutStub(t, "")
		restore := stubLaunch(func(string) error { t.Error("started a world with nothing to start"); return nil })
		defer restore()
		launched, launchErr, err := a.checkoutAndPlay(1, false)
		if err != nil {
			t.Fatalf("checkoutAndPlay: %v", err)
		}
		// Not an error to report: nothing was promised. The page shows
		// "Check out & host" for these, not "Check out & play".
		if launched || launchErr != nil {
			t.Errorf("launched = %v, launchErr = %v", launched, launchErr)
		}
	})
}

func TestUpdateLinkStoresTheLaunchTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	a := newApp(Config{Links: []WorldLink{{WorldID: 1, Dir: t.TempDir(), AppID: "1623730"}}}, path)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/links/1", strings.NewReader(`{"launchTarget":"  D:\\Games\\modded.lnk  "}`))
	req.SetPathValue("worldID", "1")
	a.handleUpdateLink(rec, req)
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("update answered %s", rec.Body.String())
	}
	// Trimmed, and it wins over the app id from here on.
	if l := a.link(1); l == nil || l.LaunchTarget != `D:\Games\modded.lnk` {
		t.Fatalf("stored launch target = %+v", l)
	}
	if got := launchTarget(a.link(1)); got != `D:\Games\modded.lnk` {
		t.Errorf("launchTarget = %q, want the override", got)
	}

	// It survives a restart — this is a setting, not a session.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	reloaded, err := parseConfig(raw)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.link(1).LaunchTarget != `D:\Games\modded.lnk` {
		t.Error("the launch target did not survive a reload")
	}

	// Clearing it goes back to Steam.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/links/1", strings.NewReader(`{"launchTarget":""}`))
	req.SetPathValue("worldID", "1")
	a.handleUpdateLink(rec, req)
	if got := launchTarget(a.link(1)); got != "steam://rungameid/1623730" {
		t.Errorf("after clearing, launchTarget = %q, want the Steam URI back", got)
	}
}

// stubLaunch swaps the desktop opener for the duration of a test.
func stubLaunch(fn func(string) error) func() {
	prev := openLaunchURI
	openLaunchURI = fn
	return func() { openLaunchURI = prev }
}

// appWithCheckoutStub is an app pointed at a service that hands out one
// checkout and one (empty) version, linked to a real folder.
func appWithCheckoutStub(t *testing.T, appID string) (*app, string) {
	t.Helper()
	source := t.TempDir()
	os.WriteFile(filepath.Join(source, "World.sav"), []byte("checked out"), 0o600)
	var bundle bytes.Buffer
	if err := packageWorldDir(source, &bundle); err != nil {
		t.Fatalf("package: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			w.Write(bundle.Bytes())
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"accepted": true,
			"world":    "Emberfall",
			"session":  map[string]any{"id": 7, "baseVersion": 41},
		})
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	return newApp(Config{
		ServerURL: srv.URL,
		Token:     "tok",
		Links:     []WorldLink{{WorldID: 1, Dir: dir, AppID: appID}},
	}, filepath.Join(t.TempDir(), "config.json")), dir
}

// --- the tray's status line (app.go) ---
//
// The tray shows state, not narrative. It used to paste worldSync's last
// action in beside the hold count, and that text is written for the
// page's footer where there is room — it carries whole filesystem paths,
// which stretched the menu across the screen.
func TestTrayStatusLine(t *testing.T) {
	longPath := `linked world 3 to C:\Users\safwyl\AppData\Local\RSDragonwilds\Saved\SaveGames\K2hAc0p_LH74aymwOemkgg`

	t.Run("unconfigured says where to go", func(t *testing.T) {
		a := newApp(Config{}, filepath.Join(t.TempDir(), "config.json"))
		if got := a.statusLine(); got != "Not connected — open the page to set up" {
			t.Errorf("statusLine = %q", got)
		}
	})

	t.Run("a held world is named, and no path comes with it", func(t *testing.T) {
		a := newApp(Config{
			ServerURL: "https://vault.example.test",
			Token:     "tok",
			Links:     []WorldLink{{WorldID: 3, Dir: `C:\saves`, SessionID: 9}},
		}, filepath.Join(t.TempDir(), "config.json"))
		a.mu.Lock()
		a.worldSync.LastAction = longPath
		a.worldSync.Worlds = []syncWorldDTO{makeDTO(3, "Emberfall")}
		a.mu.Unlock()

		got := a.statusLine()
		if got != "Holding Emberfall" {
			t.Errorf("statusLine = %q, want the world named", got)
		}
		if strings.Contains(got, `C:\`) || strings.Contains(got, "linked world") {
			t.Errorf("statusLine leaked the last action: %q", got)
		}
	})

	t.Run("a world the poll has not named yet still counts", func(t *testing.T) {
		a := newApp(Config{
			ServerURL: "https://vault.example.test",
			Token:     "tok",
			Links:     []WorldLink{{WorldID: 3, SessionID: 9}},
		}, filepath.Join(t.TempDir(), "config.json"))
		if got := a.statusLine(); got != "Holding 1 world" {
			t.Errorf("statusLine = %q, want a count when there is no name", got)
		}
	})

	t.Run("several holds are counted", func(t *testing.T) {
		a := newApp(Config{
			ServerURL: "https://vault.example.test",
			Token:     "tok",
			Links: []WorldLink{
				{WorldID: 1, SessionID: 1},
				{WorldID: 2, SessionID: 2},
				{WorldID: 3},
			},
		}, filepath.Join(t.TempDir(), "config.json"))
		if got := a.statusLine(); got != "Holding 2 worlds" {
			t.Errorf("statusLine = %q, want only the held ones counted", got)
		}
	})

	// The one thing the player can do from this menu is quit, and during
	// a transfer it is the one thing they must not do.
	t.Run("a running transfer outranks everything", func(t *testing.T) {
		a := newApp(Config{
			ServerURL: "https://vault.example.test",
			Token:     "tok",
			Links:     []WorldLink{{WorldID: 3, SessionID: 9}},
		}, filepath.Join(t.TempDir(), "config.json"))
		a.mu.Lock()
		a.worldSync.Busy = true
		a.worldSync.LastError = "something older"
		a.worldSync.Worlds = []syncWorldDTO{makeDTO(3, "Emberfall")}
		a.mu.Unlock()
		if got := a.statusLine(); got != "Transferring a save — don't quit yet" {
			t.Errorf("statusLine = %q, want the transfer warning", got)
		}
	})

	t.Run("an error points at the page rather than repeating itself", func(t *testing.T) {
		a := newApp(Config{ServerURL: "https://vault.example.test", Token: "tok"}, filepath.Join(t.TempDir(), "config.json"))
		a.mu.Lock()
		a.worldSync.LastError = "service answered 502: " + strings.Repeat("blah ", 40)
		a.mu.Unlock()
		got := a.statusLine()
		if got != "Sync error — open the page for details" {
			t.Errorf("statusLine = %q", got)
		}
	})

	t.Run("connected and idle", func(t *testing.T) {
		a := newApp(Config{ServerURL: "https://vault.example.test", Token: "tok"}, filepath.Join(t.TempDir(), "config.json"))
		a.mu.Lock()
		a.worldSync.LastAction = longPath
		a.mu.Unlock()
		if got := a.statusLine(); got != "Connected — no worlds held" {
			t.Errorf("statusLine = %q", got)
		}
	})

	// A world's name comes from whoever created it, so it is the one
	// variable-length thing here and has to be bounded.
	t.Run("an absurd world name is cut short", func(t *testing.T) {
		a := newApp(Config{
			ServerURL: "https://vault.example.test",
			Token:     "tok",
			Links:     []WorldLink{{WorldID: 3, SessionID: 9}},
		}, filepath.Join(t.TempDir(), "config.json"))
		a.mu.Lock()
		a.worldSync.Worlds = []syncWorldDTO{makeDTO(3, strings.Repeat("long ", 40))}
		a.mu.Unlock()
		got := a.statusLine()
		if len([]rune(got)) > len("Holding ")+trayNameMax {
			t.Errorf("statusLine is %d runes: %q", len([]rune(got)), got)
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("a cut name should say it was cut: %q", got)
		}
	})
}

func TestEllipsize(t *testing.T) {
	if got := ellipsize("short", 32); got != "short" {
		t.Errorf("ellipsize(short) = %q", got)
	}
	if got := ellipsize("exactly ten", 11); got != "exactly ten" {
		t.Errorf("a name at the limit was cut: %q", got)
	}
	// Rune-aware: a name in any script must not be cut mid-character.
	got := ellipsize("日本語のワールドの名前です", 5)
	if r := []rune(got); len(r) != 5 || r[4] != '…' {
		t.Errorf("ellipsize cut badly: %q (%d runes)", got, len(r))
	}
}

// makeDTO is a world as the custody poll reports it, named.
func makeDTO(id int64, name string) syncWorldDTO {
	var w syncWorldDTO
	w.World.ID = id
	w.World.Name = name
	return w
}
