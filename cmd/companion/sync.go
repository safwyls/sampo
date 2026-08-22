package main

// Save-sync custody, from the player's side of the wire
// (docs/save-sync-architecture.md): link installed games' save folders
// to worlds on the save-sync service, check a world out into its folder
// to host it, push mid-session checkpoints, and check it back in when
// the hosting stretch ends. The personal sync token is the whole
// credential, and every answer is sniffed — a 200 that isn't the
// service's own JSON ack is an interceptor, not a success.
//
// Torn-save guard, learned elsewhere in this repo the hard way: any
// packaging waits out a settle window on the folder's mtimes, longer
// for checkpoints so a push lands between autosaves, not during one.
// The companion is game-blind, so there is no process-name check — the
// settle window is the guard, and the service verifies every upload
// again before anything becomes canonical.

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// syncPollEvery paces the status poll against the service when
	// nobody is looking at the page — polite, because custody changes
	// are rare and this runs on someone's machine all day.
	syncPollEvery = time.Minute
	// syncPollWatching is the pace while the companion page is open. A
	// page showing a minute-old custody state is a page that looks
	// broken: someone else checks a world in, and the person staring at
	// the screen sees nothing happen. The page's own poll drives this,
	// so it costs nothing when the page is closed.
	syncPollWatching = 4 * time.Second
	// pageWatchWindow is how long after a page request the app counts as
	// being watched.
	pageWatchWindow = 30 * time.Second
	// checkinSettle / checkpointSettle: how long a world folder must be
	// quiet before packaging. Check-in is short (the game is closed);
	// checkpoints wait longer so a push lands between autosaves.
	checkinSettle    = 10 * time.Second
	checkpointSettle = 60 * time.Second
)

// syncWorldDTO is the service's world status, the subset this side reads.
type syncWorldDTO struct {
	World struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		GameTitle   string `json:"gameTitle"`
		SaveHint    string `json:"saveHint"`
		Checkpoints bool   `json:"checkpoints"`
		SavePath    string `json:"savePath"`
		HeadVersion *int64 `json:"headVersion"`
	} `json:"world"`
	Holder *struct {
		SessionID int64     `json:"sessionId"`
		Username  string    `json:"username"`
		ExpiresAt time.Time `json:"expiresAt"`
		Claimable bool      `json:"claimable"`
		// What the service is waiting for this hold to do, picked up on
		// the next poll (sync.go, answerHandback).
		RequestedKind string `json:"requestedKind,omitempty"`
	} `json:"holder,omitempty"`
	ClaimedBy string `json:"claimedBy,omitempty"`
	Head      *struct {
		ID        int64     `json:"id"`
		Bytes     int64     `json:"bytes"`
		CreatedAt time.Time `json:"createdAt"`
	} `json:"head,omitempty"`
}

// syncState is what the page shows about custody.
type syncState struct {
	Configured bool           `json:"configured"`
	Username   string         `json:"username,omitempty"`
	Worlds     []syncWorldDTO `json:"worlds,omitempty"`
	Busy       bool           `json:"busy"`
	LastError  string         `json:"lastError,omitempty"`
	LastAction string         `json:"lastAction,omitempty"`
	PolledAt   *time.Time     `json:"polledAt,omitempty"`
	// ServerVersion is the service's own build, reported by its status
	// call. Shown beside this app's version so a bug report about a
	// transfer can name both halves rather than one.
	ServerVersion string `json:"serverVersion,omitempty"`
}

func (a *app) syncConfigured() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.configured()
}

func (a *app) syncBase() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return normalizeServerURL(a.cfg.ServerURL) + "/api/public/sync/" + a.cfg.Token
}

func (a *app) setSyncErr(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		a.worldSync.LastError = ""
		return
	}
	a.worldSync.LastError = err.Error()
	log.Printf("sync: %v", err)
}

func (a *app) noteSync(action string) {
	a.mu.Lock()
	a.worldSync.LastAction = action
	a.worldSync.LastError = ""
	a.mu.Unlock()
	log.Printf("sync: %s", action)
}

// syncDo performs one custody call and decodes the service's ack. Any
// 200 whose body is not the service's own JSON (an Access login page, a
// tunnel interstitial) is a failure with the interceptor named.
// scrubToken keeps the player's sync token out of anything the page
// shows. Transport errors quote the URL they failed on, and the token
// lives in that URL — so a refused connection would print the
// credential onto the screen, and into any screenshot sent to whoever
// runs the vault.
func (a *app) scrubToken(err error) error {
	if err == nil {
		return nil
	}
	a.mu.Lock()
	token := a.cfg.Token
	a.mu.Unlock()
	if token == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), token, "<your sync token>")
	if msg == err.Error() {
		return err
	}
	return errors.New(msg)
}

func (a *app) syncDo(method, path string, body any, out any) error {
	return a.scrubToken(a.syncDoRaw(method, path, body, out))
}

func (a *app) syncDoRaw(method, path string, body any, out any) error {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = strings.NewReader(string(data))
	}
	req, err := http.NewRequest(method, a.syncBase()+path, payload)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var parsed struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &parsed) == nil && parsed.Error != "" {
			return fmt.Errorf("service answered %d: %s", resp.StatusCode, parsed.Error)
		}
		return fmt.Errorf("service answered %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var ack struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil || !ack.Accepted {
		return errors.New(interceptedHint(raw))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// syncRefresh polls the service's custody status.
func (a *app) syncRefresh() error {
	var out struct {
		Username      string         `json:"username"`
		Worlds        []syncWorldDTO `json:"worlds"`
		ServerVersion string         `json:"serverVersion"`
	}
	if err := a.syncDo(http.MethodGet, "", nil, &out); err != nil {
		a.setSyncErr(err)
		return err
	}
	now := time.Now()
	a.mu.Lock()
	a.worldSync.Username = out.Username
	a.worldSync.Worlds = out.Worlds
	a.worldSync.ServerVersion = out.ServerVersion
	a.worldSync.PolledAt = &now
	a.worldSync.LastError = ""
	a.mu.Unlock()
	return nil
}

// refreshIfStale polls the service when the view has aged past the
// current interval. Single-flight: the page asks on every render, and a
// slow service must not stack requests behind each other.
func (a *app) refreshIfStale() {
	a.mu.Lock()
	stale := a.worldSync.PolledAt == nil || time.Since(*a.worldSync.PolledAt) >= a.pollIntervalLocked()
	if !stale || a.refreshing || a.worldSync.Busy || !a.cfg.configured() {
		a.mu.Unlock()
		return
	}
	a.refreshing = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.refreshing = false
		a.mu.Unlock()
	}()
	a.syncRefresh()
}

// pollIntervalLocked is how stale the custody view may get: a few
// seconds while someone has the page open, a minute otherwise. Caller
// holds the lock.
func (a *app) pollIntervalLocked() time.Duration {
	if !a.pageSeen.IsZero() && time.Since(a.pageSeen) < pageWatchWindow {
		return syncPollWatching
	}
	return syncPollEvery
}

// syncTick rides the watch loop: poll if the view has gone stale, then
// act on what it says.
//
// Acting is no longer gated on the poll being due. It used to be — one
// early return covered both — so a handoff, a handback request or a
// checkpoint could only ever be noticed on the same beat the status was
// fetched, and only once a minute at that. Refreshing and reacting are
// different jobs on different clocks.
func (a *app) syncTick() {
	if !a.syncConfigured() {
		return
	}
	a.refreshIfStale()
	a.mu.Lock()
	busy := a.worldSync.Busy
	polled := a.worldSync.PolledAt != nil
	a.mu.Unlock()
	if busy || !polled {
		return
	}
	for _, worldID := range a.linkedWorldIDs() {
		a.adoptHandoff(worldID)
		// Before the automatic checkpoint, because a standing request is
		// somebody waiting: answering it now beats answering it after
		// the settle window decides the folder is quiet enough.
		a.answerHandback(worldID)
		a.autoCheckpoint(worldID)
	}
}

func (a *app) linkedWorldIDs() []int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := make([]int64, 0, len(a.cfg.Links))
	for _, l := range a.cfg.Links {
		ids = append(ids, l.WorldID)
	}
	return ids
}

// world returns the polled status for one world, nil when the service
// doesn't list it.
func (a *app) world(worldID int64) *syncWorldDTO {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.worldSync.Worlds {
		if a.worldSync.Worlds[i].World.ID == worldID {
			w := a.worldSync.Worlds[i]
			return &w
		}
	}
	return nil
}

// adoptHandoff notices that the service handed this player a linked
// world while nobody was looking — a queued claim consumed by someone
// else's check-in — and fetches it, so "your companion will fetch it" is
// a promise this code keeps.
func (a *app) adoptHandoff(worldID int64) {
	world := a.world(worldID)
	a.mu.Lock()
	link := a.cfg.link(worldID)
	me := a.worldSync.Username
	var sessionID int64
	if link != nil {
		sessionID = link.SessionID
	}
	a.mu.Unlock()
	if link == nil || world == nil || world.Holder == nil || world.Holder.Username != me || world.Holder.SessionID == sessionID {
		return
	}
	// The hold is ours but the session is new. Its base is the current
	// head: nothing can move the head under an active hold.
	base := int64(0)
	if world.World.HeadVersion != nil {
		base = *world.World.HeadVersion
	}
	if err := a.installHold(worldID, world.Holder.SessionID, base); err != nil {
		a.setSyncErr(fmt.Errorf("fetching the handed-off world: %w", err))
		return
	}
	a.noteSync(fmt.Sprintf("adopted the handoff of %q — the world is on this machine", world.World.Name))
}

// autoCheckpoint pushes a checkpoint when a held world's folder has
// changed and settled. Failures are recorded, not fatal — the next tick
// retries.
func (a *app) autoCheckpoint(worldID int64) {
	world := a.world(worldID)
	a.mu.Lock()
	link := a.cfg.link(worldID)
	var sessionID int64
	var dir string
	if link != nil {
		sessionID, dir = link.SessionID, link.Dir
	}
	lastPush := a.lastCheckpoint[worldID]
	a.mu.Unlock()
	if link == nil || sessionID == 0 || world == nil || !world.World.Checkpoints {
		return
	}
	newest, err := newestModTime(dir)
	if err != nil || !newest.After(lastPush) || time.Since(newest) < checkpointSettle {
		return
	}
	if err := a.pushBundle(dir, sessionID, "checkpoint"); err != nil {
		a.setSyncErr(fmt.Errorf("checkpoint push (%s): %w", world.World.Name, err))
		return
	}
	a.mu.Lock()
	a.lastCheckpoint[worldID] = time.Now()
	a.mu.Unlock()
	a.noteSync(fmt.Sprintf("checkpoint pushed for %q", world.World.Name))
}

// answerHandback does what the service asked of this hold on its next
// poll: push a checkpoint, or check in and let go.
//
// The service cannot reach a companion — this machine is behind a
// household router — so a request is a flag the poll picks up, and this
// is where it lands. It fires only for a hold this machine actually
// owns: the flag is on the session, and a session belongs to one
// companion.
func (a *app) answerHandback(worldID int64) {
	world := a.world(worldID)
	if world == nil || world.Holder == nil || world.Holder.RequestedKind == "" {
		return
	}
	a.mu.Lock()
	link := a.cfg.link(worldID)
	var sessionID int64
	if link != nil {
		sessionID = link.SessionID
	}
	me := a.worldSync.Username
	a.mu.Unlock()
	// Someone else's hold, or a hold this machine has lost track of.
	if link == nil || sessionID == 0 || sessionID != world.Holder.SessionID || world.Holder.Username != me {
		return
	}

	switch world.Holder.RequestedKind {
	case "checkpoint":
		if err := a.syncCheckpointNow(worldID); err != nil {
			a.setSyncErr(fmt.Errorf("checkpoint asked for by %q: %w", world.World.Name, err))
			return
		}
		a.noteSync(fmt.Sprintf("pushed a checkpoint of %q — the service asked for one", world.World.Name))
	case "checkin":
		if err := a.syncCheckin(worldID); err != nil {
			a.setSyncErr(fmt.Errorf("check-in asked for by %q: %w", world.World.Name, err))
			return
		}
		a.noteSync(fmt.Sprintf("checked %q back in — the service asked for it", world.World.Name))
	}
}

// syncCheckout acquires a linked world and installs its head locally.
func (a *app) syncCheckout(worldID int64, takeover bool) error {
	if !a.setBusy(true) {
		return errors.New("a transfer is already running")
	}
	defer a.setBusy(false)
	if a.link(worldID) == nil {
		return errors.New("link this world to a save folder first")
	}
	var out struct {
		Session struct {
			ID          int64  `json:"id"`
			BaseVersion *int64 `json:"baseVersion"`
		} `json:"session"`
		World string `json:"world"`
	}
	if err := a.syncDo(http.MethodPost, fmt.Sprintf("/worlds/%d/checkout", worldID), map[string]bool{"takeover": takeover}, &out); err != nil {
		a.setSyncErr(err)
		return err
	}
	base := int64(0)
	if out.Session.BaseVersion != nil {
		base = *out.Session.BaseVersion
	}
	if err := a.installHold(worldID, out.Session.ID, base); err != nil {
		a.setSyncErr(err)
		return err
	}
	a.noteSync(fmt.Sprintf("checked out %q", out.World))
	a.syncRefresh()
	return nil
}

// checkoutAndPlay is what the page's one button does: take the world,
// put its save in place, then start the game — in that order, always.
// Launching first would have the game load the stale save and write over
// it at its first autosave, which is the failure this whole system
// exists to prevent.
//
// The launch is the softer half. A game that will not start leaves the
// player with the world checked out and the save on disk, which is the
// part that mattered; the reason comes back for the page to show rather
// than failing the checkout that already succeeded. Note that only an
// explicit checkout launches — adopting a queued claim (syncTick) fetches
// the world in the background, possibly while nobody is at the machine,
// and starting a game there would be a surprise, not a convenience.
func (a *app) checkoutAndPlay(worldID int64, takeover bool) (launched bool, launchErr error, err error) {
	if err := a.syncCheckout(worldID, takeover); err != nil {
		return false, nil, err
	}
	a.mu.Lock()
	wanted := a.cfg.launchOnCheckout()
	a.mu.Unlock()
	if !wanted || !launchable(a.link(worldID)) {
		return false, nil, nil
	}
	if err := a.launch(worldID); err != nil {
		return false, err, nil
	}
	return true, nil, nil
}

func (a *app) link(worldID int64) *WorldLink {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.cfg.link(worldID)
	if l == nil {
		return nil
	}
	cp := *l
	return &cp
}

// installHold records the session and places its base version into the
// linked folder. base 0 means a world with no versions yet: nothing to
// download, the folder as it stands is the starting point.
func (a *app) installHold(worldID, sessionID, base int64) error {
	if base != 0 {
		if err := a.installVersion(worldID, base); err != nil {
			return err
		}
	}
	a.mu.Lock()
	if l := a.cfg.link(worldID); l != nil {
		l.SessionID, l.BaseVersion = sessionID, base
	}
	a.mu.Unlock()
	return a.saveCfg()
}

// installVersion downloads a version bundle and swaps it into the linked
// folder: extract beside it, keep one .pre-checkout copy of what was
// there, rename into place — a torn download never leaves the folder
// half-new, and the previous local state survives one level of regret.
func (a *app) installVersion(worldID, versionID int64) error {
	link := a.link(worldID)
	if link == nil || link.Dir == "" {
		return errors.New("no save folder linked for this world")
	}
	dir := link.Dir
	resp, err := a.client.Get(a.syncBase() + fmt.Sprintf("/worlds/%d/versions/%d/download", worldID, versionID))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("download answered %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	tmp := dir + ".sync-tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := extractBundleTo(resp.Body, tmp); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("extracting the world: %w", err)
	}
	backup := dir + ".pre-checkout"
	if _, err := os.Stat(dir); err == nil {
		if err := os.RemoveAll(backup); err != nil {
			os.RemoveAll(tmp)
			return err
		}
		if err := os.Rename(dir, backup); err != nil {
			os.RemoveAll(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}

// syncCheckin packages a held world's folder and returns the hold. The
// folder must be settled — committing a torn save as the canonical
// version is the one unforgivable failure here, so close the game and
// let it finish saving first.
func (a *app) syncCheckin(worldID int64) error {
	if !a.setBusy(true) {
		return errors.New("a transfer is already running")
	}
	defer a.setBusy(false)
	link := a.link(worldID)
	if link == nil || link.SessionID == 0 {
		return errors.New("no hold to check in")
	}
	if newest, err := newestModTime(link.Dir); err != nil {
		a.setSyncErr(err)
		return err
	} else if since := time.Since(newest); since < checkinSettle {
		time.Sleep(checkinSettle - since) // written moments ago: wait out the settle window instead of failing
	}
	if err := a.pushBundle(link.Dir, link.SessionID, "checkin"); err != nil {
		a.setSyncErr(err)
		return err
	}
	a.mu.Lock()
	if l := a.cfg.link(worldID); l != nil {
		l.SessionID, l.BaseVersion = 0, 0
	}
	a.mu.Unlock()
	if err := a.saveCfg(); err != nil {
		return err
	}
	a.noteSync("checked in — the world is free")
	a.syncRefresh()
	return nil
}

// syncCheckpointNow is the page's manual checkpoint button.
func (a *app) syncCheckpointNow(worldID int64) error {
	link := a.link(worldID)
	if link == nil || link.SessionID == 0 {
		return errors.New("no hold to checkpoint")
	}
	if err := a.pushBundle(link.Dir, link.SessionID, "checkpoint"); err != nil {
		a.setSyncErr(err)
		return err
	}
	a.mu.Lock()
	a.lastCheckpoint[worldID] = time.Now()
	a.mu.Unlock()
	a.noteSync("checkpoint pushed")
	return nil
}

// pushBundle streams a packaged world folder to the service.
func (a *app) pushBundle(dir string, sessionID int64, verb string) error {
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(packageWorldDir(dir, pw)) }()
	req, err := http.NewRequest(http.MethodPost, a.syncBase()+fmt.Sprintf("/sessions/%d/%s", sessionID, verb), pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	// The default client timeout is sized for JSON, not a world upload.
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var parsed struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &parsed) == nil && parsed.Error != "" {
			return fmt.Errorf("service answered %d: %s", resp.StatusCode, parsed.Error)
		}
		return fmt.Errorf("service answered %d", resp.StatusCode)
	}
	var ack struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil || !ack.Accepted {
		return errors.New(interceptedHint(raw))
	}
	return nil
}

func (a *app) syncRenew(worldID int64) error {
	link := a.link(worldID)
	if link == nil || link.SessionID == 0 {
		return errors.New("no hold to renew")
	}
	if err := a.syncDo(http.MethodPost, fmt.Sprintf("/sessions/%d/renew", link.SessionID), nil, nil); err != nil {
		a.setSyncErr(err)
		return err
	}
	a.noteSync("hold renewed")
	a.syncRefresh()
	return nil
}

func (a *app) syncClaim(worldID int64) error {
	if a.link(worldID) == nil {
		return errors.New("link this world to a save folder first — the handoff needs somewhere to land")
	}
	if err := a.syncDo(http.MethodPost, fmt.Sprintf("/worlds/%d/claim", worldID), nil, nil); err != nil {
		a.setSyncErr(err)
		return err
	}
	a.noteSync("claimed the next hold")
	a.syncRefresh()
	return nil
}

// --- linking installed games to worlds ---

// linkWorld ties an existing world to a local save folder and reports
// the game details to the service (metadata only; the service stores
// what companions report, it never interprets it).
// checkSaveDir is the one gate a link cannot pass without. It is
// separate so createWorld can run it *before* creating anything on the
// service — see the note there.
func checkSaveDir(dir string) error {
	if dir == "" {
		return errors.New("a link needs a save folder")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("save folder: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("save folder: %s is a file, not a folder", dir)
	}
	return nil
}

func (a *app) linkWorld(worldID int64, gameTitle, dir, meta, appID string) error {
	if err := checkSaveDir(dir); err != nil {
		return err
	}
	if gameTitle != "" || meta != "" {
		if err := a.syncDo(http.MethodPut, fmt.Sprintf("/worlds/%d/meta", worldID), map[string]string{
			"gameTitle": gameTitle, "saveHint": dir, "gameMeta": meta,
		}, nil); err != nil {
			a.setSyncErr(err)
			return err
		}
	}
	a.mu.Lock()
	if l := a.cfg.link(worldID); l != nil {
		l.GameTitle, l.Dir = gameTitle, dir
		if appID != "" {
			l.AppID = appID
		}
	} else {
		a.cfg.Links = append(a.cfg.Links, WorldLink{WorldID: worldID, GameTitle: gameTitle, Dir: dir, AppID: appID})
	}
	a.mu.Unlock()
	if err := a.saveCfg(); err != nil {
		return err
	}
	a.noteSync(fmt.Sprintf("linked world %d to %s", worldID, dir))
	a.syncRefresh()
	return nil
}

// renameWorld changes a world's name on the service. Unlike linkWorld's
// meta call this carries none of the game-info fields, so the service
// leaves gameTitle/saveHint/gameMeta exactly as they were.
func (a *app) renameWorld(worldID int64, name string) error {
	if name == "" {
		return errors.New("a world needs a name")
	}
	if err := a.syncDo(http.MethodPut, fmt.Sprintf("/worlds/%d/meta", worldID), map[string]string{"name": name}, nil); err != nil {
		a.setSyncErr(err)
		return err
	}
	a.noteSync(fmt.Sprintf("renamed the world to %q", name))
	a.syncRefresh()
	return nil
}

// createWorld makes a world on the service from a discovered game, links
// it, and optionally seeds it with the folder's current save.
func (a *app) createWorld(name, gameTitle, dir, meta, appID, savePath string, seed bool) error {
	if name == "" {
		return errors.New("a world needs a name")
	}
	// Check the folder before creating anything on the service. The
	// order used to be the other way round, so a link refused for a
	// missing save folder — the one thing discovery genuinely cannot
	// guess — still left a world behind on the service, with nothing
	// linked to it here. An orphan world nobody asked for is worse than
	// a refusal.
	if err := checkSaveDir(dir); err != nil {
		return err
	}
	var out struct {
		Status struct {
			World struct {
				ID int64 `json:"id"`
			} `json:"world"`
		} `json:"status"`
	}
	if err := a.syncDo(http.MethodPost, "/worlds", map[string]string{
		"name": name, "gameTitle": gameTitle, "saveHint": dir, "gameMeta": meta,
		// The folder this world lives in, beneath whatever save folder
		// each player has. Recorded once, by whoever creates the world,
		// so everyone who joins later gets the same folder made for them.
		"savePath": savePath,
	}, &out); err != nil {
		a.setSyncErr(err)
		return err
	}
	worldID := out.Status.World.ID
	if err := a.linkWorld(worldID, gameTitle, dir, meta, appID); err != nil {
		return err
	}
	if seed {
		if err := a.seedWorld(worldID, dir); err != nil {
			a.setSyncErr(fmt.Errorf("world created and linked, but seeding failed: %w", err))
			return err
		}
		a.noteSync(fmt.Sprintf("created %q and seeded it with the current save", name))
	} else {
		a.noteSync(fmt.Sprintf("created %q", name))
	}
	a.syncRefresh()
	return nil
}

// seedWorld imports the folder's current save as the world's first
// version.
func (a *app) seedWorld(worldID int64, dir string) error {
	if !a.setBusy(true) {
		return errors.New("a transfer is already running")
	}
	defer a.setBusy(false)
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(packageWorldDir(dir, pw)) }()
	req, err := http.NewRequest(http.MethodPost, a.syncBase()+fmt.Sprintf("/worlds/%d/import", worldID), pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var ack struct {
		Accepted bool   `json:"accepted"`
		Error    string `json:"error"`
	}
	json.Unmarshal(raw, &ack)
	if resp.StatusCode != http.StatusOK || !ack.Accepted {
		if ack.Error != "" {
			return errors.New(ack.Error)
		}
		return errors.New(interceptedHint(raw))
	}
	return nil
}

func (a *app) unlink(worldID int64) error {
	a.mu.Lock()
	links := a.cfg.Links[:0]
	for _, l := range a.cfg.Links {
		if l.WorldID != worldID {
			links = append(links, l)
		}
	}
	a.cfg.Links = links
	a.mu.Unlock()
	return a.saveCfg()
}

func (a *app) setBusy(busy bool) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if busy && a.worldSync.Busy {
		return false
	}
	a.worldSync.Busy = busy
	return true
}

// packageWorldDir writes a save folder as a save bundle — the same entry
// rules as the agent's (regular files, relative paths, PAX mtimes,
// rolling backup folders skipped; core/agent/files.go listSaveFiles).
func packageWorldDir(dir string, w io.Writer) error {
	if dir == "" {
		return errors.New("no save folder linked")
	}
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.EqualFold(d.Name(), "backup") || strings.EqualFold(d.Name(), "backups") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr := &tar.Header{Name: filepath.ToSlash(rel), Mode: 0o644, Size: info.Size(), ModTime: info.ModTime(), Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.CopyN(tw, f, info.Size())
		f.Close()
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

// extractBundleTo unpacks a bundle, admitting only relative regular
// files that resolve inside dir — the same paranoia as agentctl's
// extractTar, sized for a world save.
func extractBundleTo(r io.Reader, dir string) error {
	const (
		maxFiles = 20_000
		maxBytes = 4 << 30
	)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(r)
	var total int64
	files := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if files++; files > maxFiles {
			return errors.New("bundle exceeds the file-count bound")
		}
		if total += hdr.Size; total > maxBytes {
			return errors.New("bundle exceeds the size bound")
		}
		name := filepath.FromSlash(hdr.Name)
		if filepath.IsAbs(name) || strings.Contains(hdr.Name, "..") {
			return fmt.Errorf("bundle entry %q escapes the destination", hdr.Name)
		}
		dest := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, tr)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		if !hdr.ModTime.IsZero() {
			_ = os.Chtimes(dest, hdr.ModTime, hdr.ModTime)
		}
	}
}

// newestModTime finds the most recent write anywhere under the folder —
// the settle-window input.
func newestModTime(dir string) (time.Time, error) {
	if dir == "" {
		return time.Time{}, errors.New("no save folder linked")
	}
	var newest time.Time
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("reading the save folder: %w", err)
	}
	if newest.IsZero() {
		return time.Time{}, errors.New("the save folder is empty")
	}
	return newest, nil
}

// --- cover art ---

// gameArt mirrors the service's artwork answer. The companion holds no
// IGDB credentials of its own: the vault looks art up once for everyone
// and this side just renders what comes back, so a service without
// artwork configured simply yields names.
type gameArt struct {
	Name    string `json:"name,omitempty"`
	Cover   string `json:"cover,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type artQuery struct {
	AppID string `json:"appId,omitempty"`
	Name  string `json:"name,omitempty"`
}

func artKey(q artQuery) string {
	if q.AppID != "" {
		return "app:" + q.AppID
	}
	return "name:" + strings.ToLower(strings.TrimSpace(q.Name))
}

// artwork resolves covers for the discovered games, asking the service
// only for what isn't already cached here. Failures are silent by
// design — a shelf without covers is still a shelf.
func (a *app) artwork() map[string]gameArt {
	a.mu.Lock()
	games := append([]discoveredGame(nil), a.discovered.Games...)
	if a.art == nil {
		a.art = map[string]gameArt{}
	}
	out := map[string]gameArt{}
	var need []artQuery
	for _, g := range games {
		q := artQuery{AppID: g.AppID, Name: g.Name}
		key := artKey(q)
		if hit, ok := a.art[key]; ok {
			if hit.Name != "" || hit.Cover != "" {
				out[key] = hit
			}
			continue
		}
		need = append(need, q)
	}
	configured := a.cfg.configured()
	a.mu.Unlock()

	if len(need) == 0 || !configured {
		return out
	}
	a.mu.Lock()
	a.artAsked += len(need)
	a.mu.Unlock()
	var resp struct {
		Art map[string]gameArt `json:"art"`
	}
	if err := a.syncDo(http.MethodPost, "/artwork", map[string]any{"games": need}, &resp); err != nil {
		// Artwork never blocks custody, so this stays out of the sync
		// error line — but it is not silent either: the page shows it
		// under the shelf, where a missing cover is what prompts the
		// question.
		log.Printf("artwork lookup: %v", err)
		a.mu.Lock()
		a.artError = err.Error()
		a.mu.Unlock()
		return out
	}
	a.mu.Lock()
	a.artError = ""
	for _, q := range need {
		key := artKey(q)
		hit := resp.Art[key] // a miss caches as empty, so it isn't re-asked every rescan
		a.art[key] = hit
		if hit.Name != "" || hit.Cover != "" {
			out[key] = hit
		}
	}
	a.mu.Unlock()
	return out
}

// --- save-location hints (expand.go) ---

// saveHints asks the service for the catalogue's save locations for
// every discovered game, expands them here, and folds the results into
// the games' candidate lists.
//
// Same shape as artwork(): the catalogue lives on the service, this side
// asks in one batch and caches, misses included, so a rescan does not
// re-ask about games the manifest has never carried. Unlike artwork this
// changes what the player is offered, so the expansion — and the
// decision about which template applies to this machine — happens here,
// where the placeholders actually mean something.
func (a *app) saveHints() {
	a.mu.Lock()
	games := append([]discoveredGame(nil), a.discovered.Games...)
	libs := append([]string(nil), a.discovered.Libraries...)
	if a.hints == nil {
		a.hints = map[string][]location{}
	}
	var need []savehintQuery
	for _, g := range games {
		key := gameKey(g)
		if _, ok := a.hints[key]; ok {
			continue
		}
		need = append(need, savehintQuery{AppID: g.AppID, Name: g.Name, InstallDir: g.InstallDir})
	}
	configured := a.cfg.configured()
	a.mu.Unlock()

	if len(need) > 0 && configured {
		var resp struct {
			Available bool                  `json:"available"`
			Locations map[string][]location `json:"locations"`
		}
		if err := a.syncDo(http.MethodPost, "/savehints", map[string]any{"games": need}, &resp); err != nil {
			log.Printf("save-location lookup: %v", err)
			a.mu.Lock()
			a.hintsError = err.Error()
			a.mu.Unlock()
			return
		}
		a.mu.Lock()
		a.hintsError = ""
		a.hintsAvailable = resp.Available
		for _, q := range need {
			// A miss caches as an empty list, so it is not re-asked on
			// every rescan.
			a.hints[q.Key()] = resp.Locations[q.Key()]
		}
		a.mu.Unlock()
	}
	a.applyHints(libs)
}

// savehintQuery mirrors core/savedb.Query on the wire.
type savehintQuery struct {
	AppID      string `json:"appId,omitempty"`
	Name       string `json:"name,omitempty"`
	InstallDir string `json:"installDir,omitempty"`
}

func (q savehintQuery) Key() string {
	if q.AppID != "" {
		return "app:" + q.AppID
	}
	return "name:" + strings.ToLower(strings.TrimSpace(q.Name))
}

// applyHints recomputes each game's candidates with whatever the
// catalogue offered. Recomputed rather than appended, so the ordering
// rules in saveCandidatesFor apply to the whole set and a second call
// cannot stack duplicates.
func (a *app) applyHints(libs []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(libs) == 0 {
		libs = a.discovered.Libraries
	}
	for i := range a.discovered.Games {
		g := a.discovered.Games[i]
		locs := a.hints[gameKey(g)]
		if len(locs) == 0 {
			continue
		}
		a.discovered.Games[i].SaveDirs = saveCandidatesFor(g, libs, manifestCandidates(g, locs, libs))
	}
}
