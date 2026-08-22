package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	web "github.com/safwyls/artificer/web/companion"
)

// ui is the built React frontend, embedded in web/companion the way the
// consoles and reliquary embed theirs. It must be embedded rather than
// served from disk: the companion is one exe a player downloads, with no
// installer and nothing beside it.
var ui = func() fs.FS {
	dist, err := web.Dist()
	if err != nil {
		// Only reachable if the binary was built without a frontend
		// build, which the Dockerfile and release workflow both do.
		panic("companion frontend missing from build: " + err.Error())
	}
	return dist
}()

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	// The page and its assets. Everything that is not /api is the
	// frontend; there is no router in it, so index.html is the only
	// document — but the hashed JS/CSS beside it must be served too.
	mux.Handle("GET /", http.FileServerFS(ui))
	mux.HandleFunc("GET /api/state", a.handleState)
	mux.HandleFunc("PUT /api/config", a.handleSetConfig)
	mux.HandleFunc("POST /api/discover", a.handleDiscover)
	mux.HandleFunc("GET /api/artwork", a.handleArtwork)
	// Shelf housekeeping: browsing this machine for a save folder, and
	// putting non-game entries away (browse.go, hidden.go).
	mux.HandleFunc("POST /api/sync/refresh", a.handleSyncNow)
	mux.HandleFunc("GET /api/savehints", a.handleSaveHints)
	mux.HandleFunc("GET /api/browse", a.handleBrowse)
	// The two halves of a save folder (savepath.go): where does this
	// folder split, and where does an existing world live under mine.
	mux.HandleFunc("GET /api/savepath/split", a.handleSplitSavePath)
	mux.HandleFunc("POST /api/savepath/resolve", a.handleResolveSavePath)
	mux.HandleFunc("POST /api/hide", a.handleHide)
	// World links and custody. Local-only like everything here; the real
	// authorization is the sync token these calls carry upstream.
	mux.HandleFunc("POST /api/links", a.handleAddLink)
	mux.HandleFunc("POST /api/links/create", a.handleCreateWorld)
	mux.HandleFunc("PUT /api/links/{worldID}", a.handleUpdateLink)
	mux.HandleFunc("POST /api/links/{worldID}/launch", a.linkAction((*app).launch))
	// Keeping this build current (update.go). Local-only like the rest;
	// what it reaches out to is GitHub's public release API.
	mux.HandleFunc("POST /api/update/check", a.handleCheckUpdate)
	mux.HandleFunc("POST /api/update/apply", a.handleApplyUpdate)
	mux.HandleFunc("DELETE /api/links/{worldID}", a.linkAction(func(a *app, id int64) error { return a.unlink(id) }))
	mux.HandleFunc("POST /api/links/{worldID}/checkout", a.handleCheckout)
	mux.HandleFunc("POST /api/links/{worldID}/checkin", a.linkAction((*app).syncCheckin))
	mux.HandleFunc("POST /api/links/{worldID}/checkpoint", a.linkAction((*app).syncCheckpointNow))
	mux.HandleFunc("POST /api/links/{worldID}/renew", a.linkAction((*app).syncRenew))
	mux.HandleFunc("POST /api/links/{worldID}/claim", a.linkAction((*app).syncClaim))
	return mux
}

func (a *app) handleState(w http.ResponseWriter, r *http.Request) {
	// Someone is looking. Note it, and start a poll if the view has gone
	// stale — in the background, because the page asks every few seconds
	// and must not wait on the service to render. The next ask shows the
	// answer, which is what makes an open page feel live without the
	// background loop having to poll this hard all day.
	a.mu.Lock()
	a.pageSeen = time.Now()
	a.mu.Unlock()
	go a.refreshIfStale()

	a.mu.Lock()
	st := a.worldSync
	st.Configured = a.cfg.configured()
	// Empty, not absent: a nil slice marshals to JSON null, and the page
	// reads these as arrays. Getting that wrong cost a whole page —
	// `ST.links.length` on null threw before anything else rendered, so
	// a companion with no links yet showed no games, no scan trail and
	// no version, and looked like three separate bugs.
	links := append([]WorldLink{}, a.cfg.Links...)
	discovered := a.discovered
	// Hidden is resolved here rather than in the scan: the page needs
	// the whole library to offer "show hidden", and unhiding must not
	// cost a filesystem walk.
	games := make([]discoveredGame, 0, len(discovered.Games))
	for _, g := range discovered.Games {
		g.Hidden = a.cfg.isHidden(g)
		g.Key = gameKey(g)
		games = append(games, g)
	}
	discovered.Games = games
	if discovered.Probes == nil {
		discovered.Probes = []probe{}
	}
	out := map[string]any{
		"config": map[string]any{
			"serverUrl":        a.cfg.ServerURL,
			"tokenSet":         a.cfg.Token != "",
			"steamDirs":        append([]string{}, a.cfg.SteamDirs...),
			"launchOnCheckout": a.cfg.launchOnCheckout(),
		},
		"links":      links,
		"discovered": discovered,
		"sync":       st,
		"version":    version,
		"update":     a.update,
	}
	a.mu.Unlock()
	writeJSON(w, out)
}

// handleSetConfig saves whichever settings the request carries — the
// connection panel and the discovery panel post independently, so absent
// fields keep their stored values (pointers make absent distinguishable
// from cleared). A completed connection is proven with a status poll —
// a typo'd token should fail here, not silently every minute forever;
// an empty token keeps the saved one. New Steam folders trigger a
// rescan.
func (a *app) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ServerURL        *string   `json:"serverUrl"`
		Token            string    `json:"token"`
		SteamDirs        *[]string `json:"steamDirs"`
		LaunchOnCheckout *bool     `json:"launchOnCheckout"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "invalid body"})
		return
	}
	a.mu.Lock()
	if in.ServerURL != nil {
		a.cfg.ServerURL = normalizeServerURL(*in.ServerURL)
	}
	if strings.TrimSpace(in.Token) != "" {
		a.cfg.Token = strings.TrimSpace(in.Token)
	}
	if in.SteamDirs != nil {
		dirs := make([]string, 0, len(*in.SteamDirs))
		for _, d := range *in.SteamDirs {
			if d = strings.TrimSpace(d); d != "" {
				dirs = append(dirs, d)
			}
		}
		a.cfg.SteamDirs = dirs
	}
	if in.LaunchOnCheckout != nil {
		v := *in.LaunchOnCheckout
		a.cfg.LaunchOnCheckout = &v
	}
	a.mu.Unlock()
	if err := a.saveCfg(); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "saving config: " + err.Error()})
		return
	}
	if in.SteamDirs != nil {
		a.rescan()
	}
	if in.ServerURL != nil && a.syncConfigured() {
		if err := a.syncRefresh(); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *app) handleDiscover(w http.ResponseWriter, r *http.Request) {
	a.rescan()
	a.mu.Lock()
	found := len(a.discovered.Games)
	a.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "found": found})
}

// handleArtwork answers cover art for the discovered games, resolved
// through the sync service (which holds the IGDB credentials).
func (a *app) handleArtwork(w http.ResponseWriter, r *http.Request) {
	art := a.artwork()
	a.mu.Lock()
	failure, asked := a.artError, a.artAsked
	a.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "art": art, "asked": asked, "error": failure})
}

// handleSyncNow polls the service immediately and answers with what
// happened. The page's own poll keeps things fresh while it is open;
// this is for the moment someone wants to be certain rather than
// patient — and for saying plainly when the service cannot be reached,
// which a silent background poll never does.
func (a *app) handleSyncNow(w http.ResponseWriter, r *http.Request) {
	if !a.syncConfigured() {
		writeJSON(w, map[string]any{"ok": false, "error": "not connected — set the service URL and your token in Settings"})
		return
	}
	if err := a.syncRefresh(); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	a.mu.Lock()
	worlds := len(a.worldSync.Worlds)
	a.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "worlds": worlds})
}

// handleSaveHints asks the service for the catalogue's locations and
// folds them into the discovered games' candidates. Driven by the page
// when the game set changes, like artwork.
func (a *app) handleSaveHints(w http.ResponseWriter, r *http.Request) {
	a.saveHints()
	a.mu.Lock()
	failure, available := a.hintsError, a.hintsAvailable
	known := 0
	for _, locs := range a.hints {
		if len(locs) > 0 {
			known++
		}
	}
	a.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "available": available, "known": known, "error": failure})
}

// handleSplitSavePath reports where a chosen folder divides into the
// part a joining player supplies and the part the world carries with it.
// The page shows the answer before anything is recorded, because a guess
// nobody can see is a guess nobody can correct.
func (a *app) handleSplitSavePath(w http.ResponseWriter, r *http.Request) {
	dir := cleanPastedPath(r.URL.Query().Get("dir"))
	if dir == "" {
		writeJSON(w, map[string]any{"ok": false, "error": "no folder given"})
		return
	}
	a.mu.Lock()
	libs := append([]string(nil), a.discovered.Libraries...)
	var roots []string
	for _, g := range a.discovered.Games {
		if g.AppID == r.URL.Query().Get("appId") || strings.EqualFold(g.Name, r.URL.Query().Get("name")) {
			for _, c := range g.SaveDirs {
				roots = append(roots, c.Path)
			}
			for _, loc := range a.hints[gameKey(g)] {
				if !loc.appliesHere() {
					continue
				}
				roots = append(roots, expandTemplate(loc.Template, g.InstallDir, libs)...)
			}
		}
	}
	a.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "split": splitSaveDir(dir, roots)})
}

// handleResolveSavePath joins a world's own folder under a root the
// player chose, creating it when asked. This is the join flow: the
// second player to take a world cannot type an opaque id they have never
// seen, so they supply the half they know and the companion makes the
// rest.
func (a *app) handleResolveSavePath(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Root   string `json:"root"`
		Leaf   string `json:"leaf"`
		Create bool   `json:"create"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "invalid body"})
		return
	}
	var dir string
	var err error
	if in.Create {
		dir, err = prepareWorldDir(in.Root, in.Leaf)
	} else {
		dir, err = joinSavePath(in.Root, in.Leaf)
	}
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	exists := false
	if info, serr := os.Stat(dir); serr == nil && info.IsDir() {
		exists = true
	}
	writeJSON(w, map[string]any{"ok": true, "dir": dir, "exists": exists})
}

func (a *app) handleAddLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		WorldID   int64  `json:"worldId"`
		GameTitle string `json:"gameTitle"`
		Dir       string `json:"dir"`
		Meta      string `json:"meta"`
		AppID     string `json:"appId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.WorldID == 0 {
		writeJSON(w, map[string]any{"ok": false, "error": "invalid body"})
		return
	}
	if err := a.linkWorld(in.WorldID, in.GameTitle, strings.TrimSpace(in.Dir), in.Meta, in.AppID); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *app) handleCreateWorld(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string `json:"name"`
		GameTitle string `json:"gameTitle"`
		Dir       string `json:"dir"`
		Meta      string `json:"meta"`
		AppID     string `json:"appId"`
		SavePath  string `json:"savePath"`
		Seed      bool   `json:"seed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "invalid body"})
		return
	}
	if err := a.createWorld(strings.TrimSpace(in.Name), in.GameTitle, strings.TrimSpace(in.Dir), in.Meta, in.AppID, in.SavePath, in.Seed); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleCheckout takes the world and, when the setting allows and the
// world has something to start, plays it. The answer says which of those
// happened: a save on disk with a game that would not start is a real
// outcome the page has to be able to explain.
func (a *app) handleCheckout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Takeover bool `json:"takeover"`
	}
	json.NewDecoder(r.Body).Decode(&in) // an empty body is a plain checkout
	id, err := strconv.ParseInt(r.PathValue("worldID"), 10, 64)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "invalid world id"})
		return
	}
	if !a.syncConfigured() {
		writeJSON(w, map[string]any{"ok": false, "error": "set the server URL and token first"})
		return
	}
	launched, launchErr, err := a.checkoutAndPlay(id, in.Takeover)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	out := map[string]any{"ok": true, "launched": launched}
	if launchErr != nil {
		out["launchError"] = launchErr.Error()
	}
	writeJSON(w, out)
}

// handleCheckUpdate asks GitHub now rather than waiting for the timer —
// the same "be certain rather than patient" the sync-now button serves.
func (a *app) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	a.checkUpdate(r.Context())
	a.mu.Lock()
	st := a.update
	a.mu.Unlock()
	writeJSON(w, map[string]any{"ok": st.Error == "", "update": st, "error": st.Error})
}

// handleApplyUpdate replaces this binary and restarts into the new one.
// The answer goes out *before* the restart, because the page is served
// by the process that is about to exit — a reply written afterwards
// would never arrive, and the player would see a failed request for an
// update that actually worked.
func (a *app) handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	// Not r.Context(): that is cancelled the moment this response is
	// written, and the download outlives it.
	if err := a.applyUpdate(context.Background()); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "restarting": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	// Hand the page a moment to receive that, then swap processes.
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := restartSelf(); err != nil {
			log.Printf("update: restarting: %v", err)
			return
		}
		exitForRestart()
	}()
}

// handleUpdateLink edits the parts of a link the player owns. Only the
// launch target so far — the folder and the world it points at are
// settled by linking, and changing either is an unlink and a relink.
func (a *app) handleUpdateLink(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("worldID"), 10, 64)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "invalid world id"})
		return
	}
	var in struct {
		LaunchTarget *string `json:"launchTarget"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "invalid body"})
		return
	}
	a.mu.Lock()
	l := a.cfg.link(id)
	if l == nil {
		a.mu.Unlock()
		writeJSON(w, map[string]any{"ok": false, "error": "no such link"})
		return
	}
	if in.LaunchTarget != nil {
		l.LaunchTarget = strings.TrimSpace(*in.LaunchTarget)
	}
	a.mu.Unlock()
	if err := a.saveCfg(); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "saving config: " + err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// linkAction adapts a per-world verb into a local handler.
func (a *app) linkAction(fn func(*app, int64) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("worldID"), 10, 64)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": "invalid world id"})
			return
		}
		if !a.syncConfigured() {
			writeJSON(w, map[string]any{"ok": false, "error": "set the server URL and token first"})
			return
		}
		if err := fn(a, id); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
