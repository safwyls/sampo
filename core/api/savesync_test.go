package api_test

// The save-sync HTTP surface: both trust tiers (session cookie and the
// per-player token) drive the same custody engine, and the permission
// boundaries hold — worlds are admin, custody is PermSync, reading is
// any signed-in user.

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/safwyls/artificer/core/agentfiles"
	"github.com/safwyls/artificer/core/api"
	"github.com/safwyls/artificer/core/backup"
	"github.com/safwyls/artificer/core/crypto"
	"github.com/safwyls/artificer/core/db"
	"github.com/safwyls/artificer/core/notify"
	"github.com/safwyls/artificer/core/savesync"
	"github.com/safwyls/artificer/core/store"
)

// newSyncApp is newTestApp plus the save-sync engine — SaveSync has to
// exist before Routes() is called, because the routes are absent without
// it.
func newSyncApp(t *testing.T) (*testApp, []*http.Cookie) {
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
	files := agentfiles.New(t.TempDir(), logger)
	srv := api.New(st, []byte("test-jwt-secret-0123456789abcdef"), logger, nil, notify.New(st, logger, "Test"),
		backup.New(st, nil, logger, t.TempDir(), files), files, nil)
	srv.SaveSync = savesync.New(st, nil, logger, t.TempDir())
	staticFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	app := &testApp{handler: srv.Routes(staticFS), store: st, api: srv}
	if err := api.BootstrapAdmin(t.Context(), st, adminName, adminPass); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	return app, app.login(t, adminName, adminPass)
}

// doTar posts a save bundle as a raw body, the way the companion does.
func (a *testApp) doTar(t *testing.T, path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := []byte("0123456789abcdef0123456789abcdef")
	if err := tw.WriteHeader(&tar.Header{Name: "World.sav", Mode: 0o644, Size: int64(len(content)), ModTime: time.Now(), Format: tar.FormatPAX}); err != nil {
		t.Fatalf("tar: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar: %v", err)
	}
	tw.Close()
	req := httptest.NewRequest("POST", path, &buf)
	req.Header.Set("Content-Type", "application/x-tar")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func TestSyncCustodyOverHTTP(t *testing.T) {
	app, admin := newSyncApp(t)
	app.createUser(t, admin, "alice", "alicepassword", "user", []string{store.PermSync})
	alice := app.login(t, "alice", "alicepassword")

	// World creation rides the custody grant — the companion's
	// link-a-game flow creates worlds, so alice can.
	rec := app.do(t, "POST", "/api/sync/worlds", map[string]any{"name": "midgard", "gameTitle": "Dragonwilds"}, alice)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create world: got %d (body %s)", rec.Code, rec.Body)
	}

	// Custody: checkout, upload, and the head moves.
	rec = app.do(t, "POST", "/api/sync/worlds/1/checkout", nil, alice)
	if rec.Code != http.StatusOK {
		t.Fatalf("checkout: got %d (body %s)", rec.Code, rec.Body)
	}
	out := decodeMap(t, rec)
	session := out["session"].(map[string]any)
	sessionID := int64(session["id"].(float64))

	// A second checkout answers 409 with the custody facts.
	app.createUser(t, admin, "bob", "bobpassword12", "user", []string{store.PermSync})
	bob := app.login(t, "bob", "bobpassword12")
	rec = app.do(t, "POST", "/api/sync/worlds/1/checkout", nil, bob)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second checkout: got %d, want 409", rec.Code)
	}
	held := decodeMap(t, rec)
	if held["holder"] != "alice" || held["claimable"] != false {
		t.Errorf("409 payload = %v, want holder alice, unclaimable", held)
	}

	// Bob queues, alice checks in, the handoff gives bob the world.
	if rec := app.do(t, "POST", "/api/sync/worlds/1/claim", nil, bob); rec.Code != http.StatusOK {
		t.Fatalf("claim: got %d (body %s)", rec.Code, rec.Body)
	}
	rec = app.doTar(t, "/api/sync/sessions/"+itoa(sessionID)+"/checkin", alice)
	if rec.Code != http.StatusOK {
		t.Fatalf("checkin: got %d (body %s)", rec.Code, rec.Body)
	}
	rec = app.do(t, "GET", "/api/sync/worlds/1", nil, alice)
	detail := decodeMap(t, rec)
	status := detail["status"].(map[string]any)
	if status["holder"] == nil {
		t.Fatal("claim-next handoff did not run")
	}
	if holder := status["holder"].(map[string]any); holder["username"] != "bob" {
		t.Errorf("holder = %v, want bob", holder["username"])
	}
	if status["head"] == nil {
		t.Error("checkin did not set a head")
	}

	// The archive comes back down.
	rec = app.do(t, "GET", "/api/sync/worlds/1/versions/1/download", nil, bob)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-tar" {
		t.Errorf("download: got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestSyncPermissionBoundary(t *testing.T) {
	app, admin := newSyncApp(t)
	if rec := app.do(t, "POST", "/api/sync/worlds", map[string]string{"name": "midgard"}, admin); rec.Code != http.StatusCreated {
		t.Fatalf("create world: %d", rec.Code)
	}
	app.createUser(t, admin, "carol", "carolpassword", "user", nil) // no grants
	carol := app.login(t, "carol", "carolpassword")

	// Reading custody state is open to any signed-in user; holding is not.
	if rec := app.do(t, "GET", "/api/sync/worlds", nil, carol); rec.Code != http.StatusOK {
		t.Errorf("list without grant: got %d, want 200", rec.Code)
	}
	if rec := app.do(t, "POST", "/api/sync/worlds/1/checkout", nil, carol); rec.Code != http.StatusForbidden {
		t.Errorf("checkout without grant: got %d, want 403", rec.Code)
	}
	if rec := app.do(t, "POST", "/api/me/sync-token", nil, carol); rec.Code != http.StatusForbidden {
		t.Errorf("token mint without grant: got %d, want 403", rec.Code)
	}
}

func TestSyncTokenTier(t *testing.T) {
	app, admin := newSyncApp(t)
	if rec := app.do(t, "POST", "/api/sync/worlds", map[string]string{"name": "midgard"}, admin); rec.Code != http.StatusCreated {
		t.Fatalf("create world: %d", rec.Code)
	}
	app.createUser(t, admin, "alice", "alicepassword", "user", []string{store.PermSync})
	alice := app.login(t, "alice", "alicepassword")

	rec := app.do(t, "POST", "/api/me/sync-token", nil, alice)
	if rec.Code != http.StatusOK {
		t.Fatalf("mint token: got %d (body %s)", rec.Code, rec.Body)
	}
	token := decodeMap(t, rec)["token"].(string)

	// A wrong token is a 404 with no detail, like the other token tiers.
	if rec := app.do(t, "GET", "/api/public/sync/wrong-token", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("bad token: got %d, want 404", rec.Code)
	}

	// The whole custody loop, cookie-less: status, checkout, checkin.
	rec = app.do(t, "GET", "/api/public/sync/"+token, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d (body %s)", rec.Code, rec.Body)
	}
	status := decodeMap(t, rec)
	if status["accepted"] != true || status["username"] != "alice" {
		t.Errorf("status = %v, want accepted for alice", status)
	}
	rec = app.do(t, "POST", "/api/public/sync/"+token+"/worlds/1/checkout", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("token checkout: got %d (body %s)", rec.Code, rec.Body)
	}
	sessionID := int64(decodeMap(t, rec)["session"].(map[string]any)["id"].(float64))
	rec = app.doTar(t, "/api/public/sync/"+token+"/sessions/"+itoa(sessionID)+"/checkin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("token checkin: got %d (body %s)", rec.Code, rec.Body)
	}
	rec = app.do(t, "GET", "/api/public/sync/"+token+"/worlds/1/versions/1/download", nil, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("token download: got %d", rec.Code)
	}

	// Revoking the grant kills the token even though it still exists.
	rec = app.do(t, "GET", "/api/users", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list users: %d", rec.Code)
	}
	var aliceID int64
	var users []struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &users); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	for _, u := range users {
		if u.Username == "alice" {
			aliceID = u.ID
		}
	}
	if rec := app.do(t, "PUT", "/api/users/"+itoa(aliceID), map[string]any{"role": "user", "permissions": []string{}, "disabled": false}, admin); rec.Code != http.StatusOK {
		t.Fatalf("revoke grant: %d (body %s)", rec.Code, rec.Body)
	}
	if rec := app.do(t, "GET", "/api/public/sync/"+token, nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("token after grant revoked: got %d, want 404", rec.Code)
	}
}
