package api_test

// The vault surface (VaultRoutes): the standalone save-sync service's
// assembly. Auth, users and custody are there; the console furniture is
// not — a vault answering /api/servers would mean the assemblies
// re-merged by accident.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/safwyls/artificer/core/api"
	"github.com/safwyls/artificer/core/crypto"
	"github.com/safwyls/artificer/core/db"
	"github.com/safwyls/artificer/core/igdb"
	"github.com/safwyls/artificer/core/notify"
	"github.com/safwyls/artificer/core/savesync"
	"github.com/safwyls/artificer/core/store"
)

func newVaultApp(t *testing.T) (*testApp, []*http.Cookie) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	box, err := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("crypto: %v", err)
	}
	st := store.New(sqlDB, box)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.New(st, []byte("test-jwt-secret-0123456789abcdef"), logger, nil, notify.New(st, logger, "Test"), nil, nil, nil)
	srv.SaveSync = savesync.New(st, nil, logger, t.TempDir())
	staticFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>vault</html>")}}
	app := &testApp{handler: srv.VaultRoutes(staticFS), store: st, api: srv}
	if err := api.BootstrapAdmin(t.Context(), st, adminName, adminPass); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	return app, app.login(t, adminName, adminPass)
}

func TestVaultSurface(t *testing.T) {
	app, admin := newVaultApp(t)

	// The custody loop works end to end on the vault assembly, including
	// the token tier a companion drives — create with game metadata,
	// seed, check out, check in.
	app.createUser(t, admin, "alice", "alicepassword", "user", []string{store.PermSync})
	alice := app.login(t, "alice", "alicepassword")
	rec := app.do(t, "POST", "/api/me/sync-token", nil, alice)
	if rec.Code != http.StatusOK {
		t.Fatalf("mint token: %d (body %s)", rec.Code, rec.Body)
	}
	token := decodeMap(t, rec)["token"].(string)

	rec = app.do(t, "POST", "/api/public/sync/"+token+"/worlds", map[string]string{
		"name": "midgard", "gameTitle": "RuneScape: Dragonwilds",
		"saveHint": `C:\Users\alice\AppData\Local\RSDragonwilds\Saved\SaveGames`,
		"gameMeta": `{"appId":"1374490"}`,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("companion world create: %d (body %s)", rec.Code, rec.Body)
	}
	if rec := app.doTar(t, "/api/public/sync/"+token+"/worlds/1/import", nil); rec.Code != http.StatusOK {
		t.Fatalf("companion seed: %d (body %s)", rec.Code, rec.Body)
	}
	rec = app.do(t, "GET", "/api/sync/worlds/1", nil, admin)
	world := decodeMap(t, rec)["status"].(map[string]any)["world"].(map[string]any)
	if world["gameTitle"] != "RuneScape: Dragonwilds" || world["headVersion"] == nil {
		t.Errorf("companion-created world = %v, want game metadata and a seeded head", world)
	}
	rec = app.do(t, "POST", "/api/public/sync/"+token+"/worlds/1/checkout", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("token checkout: %d (body %s)", rec.Code, rec.Body)
	}
	sessionID := int64(decodeMap(t, rec)["session"].(map[string]any)["id"].(float64))
	if rec := app.doTar(t, "/api/public/sync/"+token+"/sessions/"+itoa(sessionID)+"/checkin", nil); rec.Code != http.StatusOK {
		t.Fatalf("token checkin: %d (body %s)", rec.Code, rec.Body)
	}

	// The console furniture is absent, not just empty.
	for _, path := range []string{"/api/servers", "/api/host", "/api/servers/1/backups"} {
		if rec := app.do(t, "GET", path, nil, admin); rec.Code != http.StatusNotFound {
			t.Errorf("vault answers %s with %d, want 404", path, rec.Code)
		}
	}

	// The SPA fallback serves the embedded page.
	rec = app.do(t, "GET", "/anything", nil, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "<html>vault</html>" {
		t.Errorf("spa fallback: %d %q", rec.Code, rec.Body.String())
	}
}

// Artwork and live events are additive: with no IGDB credentials the
// lookup answers "not available" rather than failing, and the event
// stream opens and reports itself ready.
func TestVaultArtworkAndEvents(t *testing.T) {
	app, admin := newVaultApp(t)

	rec := app.do(t, "POST", "/api/sync/artwork", map[string]any{
		"games": []map[string]string{{"appId": "1623730", "name": "Palworld"}},
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("artwork: got %d (body %s)", rec.Code, rec.Body)
	}
	out := decodeMap(t, rec)
	if out["available"] != false {
		t.Errorf("artwork available = %v with no credentials, want false", out["available"])
	}
	if _, ok := out["art"]; !ok {
		t.Error("artwork answer carries no art map")
	}

	// The stream opens, announces itself and stays open until the request
	// context ends — which is what the page's EventSource relies on.
	req := httptest.NewRequest("GET", "/api/sync/events", nil)
	for _, c := range admin {
		req.AddCookie(c)
	}
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	streamed := make(chan string, 1)
	go func() {
		w := httptest.NewRecorder()
		app.handler.ServeHTTP(w, req)
		streamed <- w.Body.String()
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case body := <-streamed:
		if !strings.Contains(body, "event: ready") {
			t.Errorf("event stream opened with %q, want a ready frame", body)
		}
	case <-time.After(2 * time.Second):
		t.Error("the event stream did not return after its context ended")
	}
}

// The admin artwork surface: credentials in, diagnostics out. The point
// is that a deployment's owner can tell "no credentials" from "these
// credentials don't work" without reading the service log — the
// distinction the first cut swallowed.
func TestVaultArtworkSettings(t *testing.T) {
	app, admin := newVaultApp(t)

	// A stand-in for Twitch and IGDB, so saving a credential proves
	// itself without the internet.
	var reject bool
	igdbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reject {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"message":"invalid client secret"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/token") {
			io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
			return
		}
		io.WriteString(w, `[{"id":1}]`)
	}))
	defer igdbSrv.Close()
	app.api.Artwork = igdb.New("", "")
	app.api.Artwork.UseEndpoints(igdbSrv.URL+"/token", igdbSrv.URL+"/v4")

	rec := app.do(t, "GET", "/api/sync/artwork/settings", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("artwork settings: %d (body %s)", rec.Code, rec.Body)
	}
	out := decodeMap(t, rec)
	if out["status"].(map[string]any)["configured"] != false || out["stored"] != false {
		t.Errorf("fresh vault reports %v, want unconfigured and nothing stored", out)
	}

	// Half a pair is refused: IGDB authenticates through Twitch, and one
	// half cannot.
	rec = app.do(t, "PUT", "/api/sync/artwork/settings", map[string]string{"clientId": "abc"}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("half a credential pair: %d, want 400", rec.Code)
	}

	rec = app.do(t, "PUT", "/api/sync/artwork/settings", map[string]string{
		"clientId": "abc", "clientSecret": "shh",
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("saving credentials: %d (body %s)", rec.Code, rec.Body)
	}
	out = decodeMap(t, rec)
	if out["test"].(map[string]any)["ok"] != true {
		t.Errorf("save reported test %v, want a proven credential", out["test"])
	}
	if out["stored"] != true || out["status"].(map[string]any)["configured"] != true {
		t.Errorf("after saving: %v, want stored and configured", out)
	}
	if id := out["status"].(map[string]any)["clientId"]; id != "abc" {
		t.Errorf("status client id = %v, want the saved one", id)
	}
	// The secret never comes back out.
	if strings.Contains(rec.Body.String(), "shh") {
		t.Error("the artwork status echoed the client secret")
	}

	// A credential IGDB rejects is a 200 that says so, not an HTTP error:
	// the caller asked whether it works.
	reject = true
	rec = app.do(t, "POST", "/api/sync/artwork/test", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("artwork test: %d (body %s)", rec.Code, rec.Body)
	}
	test := decodeMap(t, rec)["test"].(map[string]any)
	if test["ok"] != false || !strings.Contains(test["error"].(string), "invalid client secret") {
		t.Errorf("test result = %v, want a named failure", test)
	}

	rec = app.do(t, "DELETE", "/api/sync/artwork/settings", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("removing credentials: %d (body %s)", rec.Code, rec.Body)
	}
	if decodeMap(t, rec)["stored"] != false {
		t.Error("credentials still stored after removal")
	}

	// A player with the custody grant is not an admin: shared credentials
	// are the admin's business.
	app.createUser(t, admin, "bob", "bobpassword12", "user", []string{store.PermSync})
	bob := app.login(t, "bob", "bobpassword12")
	if rec := app.do(t, "GET", "/api/sync/artwork/settings", nil, bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin reading artwork settings: %d, want 403", rec.Code)
	}
}

// The companion download is never cached by anything in the path, and
// says which build it is.
//
// The URL is stable for every player forever, so the bytes are the only
// thing that changes between builds — which makes an intermediary's
// cache a real hazard: .exe is in Cloudflare's default-cached extension
// list, and a browser re-serves a same-named download it already has.
// Either one hands out a companion this service stopped shipping.
func TestCompanionDownloadIsNotCacheable(t *testing.T) {
	app, admin := newVaultApp(t)
	app.api.Version = "main-abc123"
	exe := filepath.Join(t.TempDir(), "artificer-companion.exe")
	if err := os.WriteFile(exe, []byte("MZ fake companion"), 0o600); err != nil {
		t.Fatalf("write exe: %v", err)
	}
	app.api.CompanionExe = exe

	app.createUser(t, admin, "carol", "carolpassword", "user", []string{store.PermSync})
	carol := app.login(t, "carol", "carolpassword")
	rec := app.do(t, "POST", "/api/me/sync-token", nil, carol)
	token := decodeMap(t, rec)["token"].(string)

	rec = app.do(t, "GET", "/api/public/sync/"+token+"/companion/download", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("download: %d (body %s)", rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store — a cached exe outlives the build it came from", cc)
	}
	if v := rec.Header().Get("X-Companion-Version"); v != "main-abc123" {
		t.Errorf("X-Companion-Version = %q, want the service's build", v)
	}
	if rec.Body.String() != "MZ fake companion" {
		t.Errorf("download body = %q, want the bundled exe", rec.Body.String())
	}
}

// The custody grant is what separates an account that can hold a world
// from one that can only look at it — and it is grantable after the
// fact, which matters because an account created by signing in through
// Cloudflare Access arrives with no permissions at all by design.
func TestGrantingCustodyAfterTheFact(t *testing.T) {
	app, admin := newVaultApp(t)

	// An account with nothing granted, the shape SSO sign-in creates.
	app.createUser(t, admin, "friend", "friendpassword", "user", nil)
	friend := app.login(t, "friend", "friendpassword")

	// It can read, and that is all.
	if rec := app.do(t, "GET", "/api/sync/worlds", nil, friend); rec.Code != http.StatusOK {
		t.Errorf("reading worlds without custody: %d, want 200", rec.Code)
	}
	if rec := app.do(t, "POST", "/api/me/sync-token", nil, friend); rec.Code != http.StatusForbidden {
		t.Errorf("minting a companion token without custody: %d, want 403", rec.Code)
	}
	rec := app.do(t, "POST", "/api/sync/worlds", map[string]string{"name": "midgard"}, friend)
	if rec.Code != http.StatusForbidden {
		t.Errorf("creating a world without custody: %d, want 403", rec.Code)
	}

	// The admin grants it. This is the whole request the users panel
	// sends: the full record, with one field changed.
	rec = app.do(t, "GET", "/api/users", nil, admin)
	var users []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &users); err != nil {
		t.Fatalf("decode users %q: %v", rec.Body, err)
	}
	var friendID int64
	for _, u := range users {
		if u["username"] == "friend" {
			friendID = int64(u["id"].(float64))
		}
	}
	if friendID == 0 {
		t.Fatal("the friend account is not in the users list")
	}
	rec = app.do(t, "PUT", "/api/users/"+itoa(friendID), map[string]any{
		"role": "user", "permissions": []string{store.PermSync}, "disabled": false,
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("granting custody: %d (body %s)", rec.Code, rec.Body)
	}

	// Now the same account can do the things the grant is for.
	friend = app.login(t, "friend", "friendpassword")
	if rec := app.do(t, "POST", "/api/me/sync-token", nil, friend); rec.Code != http.StatusOK {
		t.Errorf("minting a companion token with custody: %d (body %s)", rec.Code, rec.Body)
	}
	if rec := app.do(t, "POST", "/api/sync/worlds", map[string]string{"name": "midgard"}, friend); rec.Code != http.StatusCreated {
		t.Errorf("creating a world with custody: %d (body %s)", rec.Code, rec.Body)
	}

	// And revoking puts it back — the grant is not one-way.
	rec = app.do(t, "PUT", "/api/users/"+itoa(friendID), map[string]any{
		"role": "user", "permissions": []string{}, "disabled": false,
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoking custody: %d (body %s)", rec.Code, rec.Body)
	}
	friend = app.login(t, "friend", "friendpassword")
	if rec := app.do(t, "POST", "/api/me/sync-token", nil, friend); rec.Code != http.StatusForbidden {
		t.Errorf("minting a token after revocation: %d, want 403", rec.Code)
	}
}

// A world remembers the folder it lives in, and that folder is treated
// as what it is: a path that will be created on someone else's machine.
func TestWorldSavePath(t *testing.T) {
	app, admin := newVaultApp(t)
	app.createUser(t, admin, "alice", "alicepassword", "user", []string{store.PermSync})
	alice := app.login(t, "alice", "alicepassword")
	rec := app.do(t, "POST", "/api/me/sync-token", nil, alice)
	token := decodeMap(t, rec)["token"].(string)

	// The first companion to link records where the world lives.
	rec = app.do(t, "POST", "/api/public/sync/"+token+"/worlds", map[string]string{
		"name": "witchspire", "gameTitle": "Witchspire",
		"saveHint": `C:\Users\alice\AppData\Local\Witchspire\Saved\SaveGames\K2hAc0p_LH74aymwOemkgg`,
		"savePath": "K2hAc0p_LH74aymwOemkgg",
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with a save path: %d (body %s)", rec.Code, rec.Body)
	}
	world := decodeMap(t, rec)["status"].(map[string]any)["world"].(map[string]any)
	if world["savePath"] != "K2hAc0p_LH74aymwOemkgg" {
		t.Fatalf("world = %v, want the save path recorded", world)
	}

	// A second companion reporting its own metadata must not move the
	// world: the first one settled where it lives, and a joiner whose
	// own folder differs would otherwise rewrite it for everyone.
	rec = app.do(t, "PUT", "/api/public/sync/"+token+"/worlds/1/meta", map[string]string{
		"gameTitle": "Witchspire", "saveHint": `D:\Games\Witchspire`, "savePath": "someone-elses-folder",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("second companion meta: %d (body %s)", rec.Code, rec.Body)
	}
	rec = app.do(t, "GET", "/api/sync/worlds/1", nil, admin)
	world = decodeMap(t, rec)["status"].(map[string]any)["world"].(map[string]any)
	if world["savePath"] != "K2hAc0p_LH74aymwOemkgg" {
		t.Errorf("save path = %v after a joiner reported its own, want the original", world["savePath"])
	}

	// An admin can correct it, through the settings form.
	rec = app.do(t, "PUT", "/api/sync/worlds/1", map[string]any{
		"name": "witchspire", "leaseHours": 6, "maxBytes": 1 << 30, "keepVersions": 5,
		"checkpoints": true, "savePath": "corrected/folder",
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin edit: %d (body %s)", rec.Code, rec.Body)
	}
	rec = app.do(t, "GET", "/api/sync/worlds/1", nil, admin)
	world = decodeMap(t, rec)["status"].(map[string]any)["world"].(map[string]any)
	if world["savePath"] != "corrected/folder" {
		t.Errorf("save path = %v after an admin edit", world["savePath"])
	}

	// The save path becomes a real folder on someone else's machine, so
	// anything that could point outside the folder they chose is refused
	// at the door.
	for _, bad := range []string{
		"../escape", "a/../../escape", "/absolute", `windows\style`,
		"C:/drive", "trailing/", "double//slash", "dot/./here",
	} {
		rec := app.do(t, "POST", "/api/public/sync/"+token+"/worlds", map[string]string{
			"name": "bad-" + bad, "savePath": bad,
		}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("save path %q was accepted with %d", bad, rec.Code)
		}
	}
}

// Asking an absent holder to hand the world back.
//
// The companion polls; nothing can reach into it. So the ask is a flag
// on the session that the next poll picks up, and this pins what the
// service promises: the flag is set, it survives until answered, and
// answering clears it.
func TestRequestingAHandback(t *testing.T) {
	app, admin := newVaultApp(t)
	app.createUser(t, admin, "alice", "alicepassword", "user", []string{store.PermSync})
	alice := app.login(t, "alice", "alicepassword")

	rec := app.do(t, "POST", "/api/sync/worlds", map[string]string{"name": "midgard"}, alice)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create world: %d (body %s)", rec.Code, rec.Body)
	}
	if rec := app.doTar(t, "/api/sync/worlds/1/import", alice); rec.Code != http.StatusOK {
		t.Fatalf("seed: %d (body %s)", rec.Code, rec.Body)
	}

	// Nothing to ask of a world nobody holds.
	rec = app.do(t, "POST", "/api/sync/worlds/1/request", map[string]string{"kind": "checkin"}, admin)
	if rec.Code != http.StatusConflict {
		t.Errorf("requesting on a free world: %d, want 409", rec.Code)
	}

	rec = app.do(t, "POST", "/api/sync/worlds/1/checkout", nil, alice)
	if rec.Code != http.StatusOK {
		t.Fatalf("checkout: %d (body %s)", rec.Code, rec.Body)
	}
	sessionID := int64(decodeMap(t, rec)["session"].(map[string]any)["id"].(float64))

	// Only an admin may ask.
	if rec := app.do(t, "POST", "/api/sync/worlds/1/request", map[string]string{"kind": "checkin"}, alice); rec.Code != http.StatusForbidden {
		t.Errorf("a holder asking themselves: %d, want 403", rec.Code)
	}
	// And only for something the companion knows how to do.
	if rec := app.do(t, "POST", "/api/sync/worlds/1/request", map[string]string{"kind": "explode"}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown request kind: %d, want 400", rec.Code)
	}

	rec = app.do(t, "POST", "/api/sync/worlds/1/request", map[string]string{"kind": "checkin"}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("request check-in: %d (body %s)", rec.Code, rec.Body)
	}

	// The holder's own status carries it, because that is the only way
	// their companion will ever hear about it.
	holder := func(c []*http.Cookie) map[string]any {
		t.Helper()
		rec := app.do(t, "GET", "/api/sync/worlds/1", nil, c)
		st := decodeMap(t, rec)["status"].(map[string]any)
		h, _ := st["holder"].(map[string]any)
		return h
	}
	if h := holder(alice); h == nil || h["requestedKind"] != "checkin" {
		t.Fatalf("holder = %v, want a pending check-in request", h)
	}
	if h := holder(alice); h["requestedAt"] == nil {
		t.Error("the request carries no time; the page shows how long it has gone unanswered")
	}

	// It stands across polls — a machine that is asleep must still find
	// it when it wakes.
	if h := holder(alice); h["requestedKind"] != "checkin" {
		t.Error("the request did not survive a second read")
	}

	// Answering clears it, and the check-in does what a check-in does.
	if rec := app.doTar(t, "/api/sync/sessions/"+itoa(sessionID)+"/checkin", alice); rec.Code != http.StatusOK {
		t.Fatalf("checkin: %d (body %s)", rec.Code, rec.Body)
	}
	rec = app.do(t, "GET", "/api/sync/worlds/1", nil, admin)
	st := decodeMap(t, rec)["status"].(map[string]any)
	if st["holder"] != nil {
		t.Errorf("the world is still held after the requested check-in: %v", st["holder"])
	}
	world := st["world"].(map[string]any)
	if world["headVersion"] == nil || world["headVersion"].(float64) < 2 {
		t.Errorf("head = %v, want the requested check-in to have moved it", world["headVersion"])
	}
}

// A checkpoint request clears itself too, without ending the hold — the
// difference that makes check-in the verb for an absent holder.
func TestRequestedCheckpointKeepsTheHold(t *testing.T) {
	app, admin := newVaultApp(t)
	app.createUser(t, admin, "alice", "alicepassword", "user", []string{store.PermSync})
	alice := app.login(t, "alice", "alicepassword")
	app.do(t, "POST", "/api/sync/worlds", map[string]string{"name": "midgard"}, alice)
	app.doTar(t, "/api/sync/worlds/1/import", alice)
	rec := app.do(t, "POST", "/api/sync/worlds/1/checkout", nil, alice)
	sessionID := int64(decodeMap(t, rec)["session"].(map[string]any)["id"].(float64))

	if rec := app.do(t, "POST", "/api/sync/worlds/1/request", map[string]string{"kind": "checkpoint"}, admin); rec.Code != http.StatusOK {
		t.Fatalf("request checkpoint: %d (body %s)", rec.Code, rec.Body)
	}
	if rec := app.doTar(t, "/api/sync/sessions/"+itoa(sessionID)+"/checkpoint", alice); rec.Code != http.StatusOK {
		t.Fatalf("checkpoint: %d (body %s)", rec.Code, rec.Body)
	}
	rec = app.do(t, "GET", "/api/sync/worlds/1", nil, admin)
	st := decodeMap(t, rec)["status"].(map[string]any)
	h, _ := st["holder"].(map[string]any)
	if h == nil {
		t.Fatal("the checkpoint ended the hold; it must not")
	}
	if h["requestedKind"] != nil && h["requestedKind"] != "" {
		t.Errorf("the request survived being answered: %v", h["requestedKind"])
	}
	// A checkpoint deliberately leaves the head where it was, which is
	// why releasing after one would hand the next player a stale save.
	if world := st["world"].(map[string]any); world["headVersion"].(float64) != 1 {
		t.Errorf("head = %v after a checkpoint, want it unmoved", world["headVersion"])
	}

	// Withdrawing is possible too.
	app.do(t, "POST", "/api/sync/worlds/1/request", map[string]string{"kind": "checkpoint"}, admin)
	if rec := app.do(t, "POST", "/api/sync/worlds/1/request", map[string]string{"kind": ""}, admin); rec.Code != http.StatusOK {
		t.Fatalf("withdraw: %d (body %s)", rec.Code, rec.Body)
	}
	rec = app.do(t, "GET", "/api/sync/worlds/1", nil, admin)
	h, _ = decodeMap(t, rec)["status"].(map[string]any)["holder"].(map[string]any)
	if h["requestedKind"] != nil && h["requestedKind"] != "" {
		t.Errorf("the request was not withdrawn: %v", h["requestedKind"])
	}
}

// An automatic checkpoint must not swallow a pending check-in request.
//
// Checkpoints fire on their own schedule while someone plays. If one of
// those cleared a standing check-in ask, the admin's request would
// vanish into an autosave and the world would stay held — with the page
// showing nothing pending, so nobody would know to ask again. Caught in
// testing against a real companion, which checkpointed moments after the
// request was made.
func TestAutomaticCheckpointDoesNotSwallowACheckinRequest(t *testing.T) {
	app, admin := newVaultApp(t)
	app.createUser(t, admin, "alice", "alicepassword", "user", []string{store.PermSync})
	alice := app.login(t, "alice", "alicepassword")
	app.do(t, "POST", "/api/sync/worlds", map[string]string{"name": "midgard"}, alice)
	app.doTar(t, "/api/sync/worlds/1/import", alice)
	rec := app.do(t, "POST", "/api/sync/worlds/1/checkout", nil, alice)
	sessionID := int64(decodeMap(t, rec)["session"].(map[string]any)["id"].(float64))

	if rec := app.do(t, "POST", "/api/sync/worlds/1/request", map[string]string{"kind": "checkin"}, admin); rec.Code != http.StatusOK {
		t.Fatalf("request check-in: %d (body %s)", rec.Code, rec.Body)
	}
	// The companion's ordinary crash-insurance checkpoint lands first.
	if rec := app.doTar(t, "/api/sync/sessions/"+itoa(sessionID)+"/checkpoint", alice); rec.Code != http.StatusOK {
		t.Fatalf("checkpoint: %d (body %s)", rec.Code, rec.Body)
	}

	rec = app.do(t, "GET", "/api/sync/worlds/1", nil, admin)
	st := decodeMap(t, rec)["status"].(map[string]any)
	h, _ := st["holder"].(map[string]any)
	if h == nil {
		t.Fatal("the checkpoint ended the hold")
	}
	if h["requestedKind"] != "checkin" {
		t.Fatalf("requestedKind = %v after an unrelated checkpoint, want the check-in still owed", h["requestedKind"])
	}

	// And it is still answerable.
	if rec := app.doTar(t, "/api/sync/sessions/"+itoa(sessionID)+"/checkin", alice); rec.Code != http.StatusOK {
		t.Fatalf("checkin: %d (body %s)", rec.Code, rec.Body)
	}
	rec = app.do(t, "GET", "/api/sync/worlds/1", nil, admin)
	if decodeMap(t, rec)["status"].(map[string]any)["holder"] != nil {
		t.Error("the world is still held after the requested check-in")
	}
}

// The build is reported without a session: the login page shows it too,
// and a version is not a secret.
func TestVaultVersion(t *testing.T) {
	app, _ := newVaultApp(t)
	rec := app.do(t, "GET", "/api/version", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("version: %d (body %s)", rec.Code, rec.Body)
	}
	if v := decodeMap(t, rec)["version"]; v != "dev" {
		t.Errorf("version = %v on an unstamped build, want \"dev\"", v)
	}
	app.api.Version = "main-abc123"
	rec = app.do(t, "GET", "/api/version", nil, nil)
	if v := decodeMap(t, rec)["version"]; v != "main-abc123" {
		t.Errorf("version = %v, want the stamped build", v)
	}
}
