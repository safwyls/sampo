package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"

	"github.com/safwyls/artificer/core/agentfiles"
	"github.com/safwyls/artificer/core/api"
	"github.com/safwyls/artificer/core/backup"
	"github.com/safwyls/artificer/core/crypto"
	"github.com/safwyls/artificer/core/db"
	"github.com/safwyls/artificer/core/notify"
	"github.com/safwyls/artificer/core/store"
)

const (
	adminName = "admin"
	adminPass = "adminpass123"
)

func TestMain(m *testing.M) {
	// Silence chi's per-request logger; it writes straight to stdout.
	middleware.DefaultLogger = middleware.RequestLogger(&middleware.DefaultLogFormatter{
		Logger: log.New(io.Discard, "", 0), NoColor: true,
	})
	os.Exit(m.Run())
}

type testApp struct {
	handler http.Handler
	store   *store.Store
	// api allows tests to set post-construction fields (Provisioner).
	api *api.Server
}

func newTestApp(t *testing.T) *testApp {
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
	srv.Provision = testProfile
	// A console embeds its real docs; the fixture proves the endpoint
	// serves whatever FS the wiring provides.
	srv.DocsFS = fstest.MapFS{"advisor.md": &fstest.MapFile{Data: []byte("# The advisor\nHow it works.")}}
	staticFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	return &testApp{handler: srv.Routes(staticFS), store: st, api: srv}
}

// agentToken authenticates test fixtures that stand in for a sidecar
// agent. (It lived in the agent-backed tests deferred to the agent-kit
// extraction; the provisioning fixtures still register rows with it.)
const agentToken = "api-test-agent-token-0123456789"

// newTestAppWithAdmin also bootstraps the initial admin and logs in.
func newTestAppWithAdmin(t *testing.T) (*testApp, []*http.Cookie) {
	t.Helper()
	app := newTestApp(t)
	if err := api.BootstrapAdmin(context.Background(), app.store, adminName, adminPass); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	return app, app.login(t, adminName, adminPass)
}

func (a *testApp) do(t *testing.T, method, path string, body any, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		buf = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, buf)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func (a *testApp) login(t *testing.T, username, password string) []*http.Cookie {
	t.Helper()
	rec := a.do(t, "POST", "/api/login", map[string]string{"username": username, "password": password}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: got %d, want 200 (body %s)", username, rec.Code, rec.Body)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login set no session cookie")
	}
	return cookies
}

// createUser provisions a user through the admin API and returns its id.
func (a *testApp) createUser(t *testing.T, admin []*http.Cookie, username, password, role string, perms []string) int64 {
	t.Helper()
	rec := a.do(t, "POST", "/api/users", map[string]any{
		"username": username, "password": password, "role": role, "permissions": perms,
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user %s: got %d (body %s)", username, rec.Code, rec.Body)
	}
	var dto struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return dto.ID
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body, err)
	}
	return m
}

func TestLoginAndSessionRoundTrip(t *testing.T) {
	app, _ := newTestAppWithAdmin(t)

	if rec := app.do(t, "POST", "/api/login", map[string]string{"username": adminName, "password": "wrong-password"}, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: got %d, want 401", rec.Code)
	}
	if rec := app.do(t, "POST", "/api/login", map[string]string{"username": "nobody", "password": "whatever1"}, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown user: got %d, want 401", rec.Code)
	}
	if rec := app.do(t, "GET", "/api/me", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no cookie: got %d, want 401", rec.Code)
	}
	garbage := []*http.Cookie{{Name: "console_session", Value: "not-a-jwt"}}
	if rec := app.do(t, "GET", "/api/me", nil, garbage); rec.Code != http.StatusUnauthorized {
		t.Errorf("garbage cookie: got %d, want 401", rec.Code)
	}

	cookies := app.login(t, adminName, adminPass)
	rec := app.do(t, "GET", "/api/me", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: got %d, want 200", rec.Code)
	}
	me := decodeMap(t, rec)
	if me["username"] != adminName || me["isAdmin"] != true {
		t.Errorf("me = %v, want username=%s isAdmin=true", me, adminName)
	}
}

func TestLoginDisabledAccount(t *testing.T) {
	app, admin := newTestAppWithAdmin(t)
	id := app.createUser(t, admin, "bob", "bobpassword", "user", nil)

	rec := app.do(t, "PUT", fmt.Sprintf("/api/users/%d", id), map[string]any{
		"role": "user", "permissions": []string{}, "disabled": true,
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable bob: got %d (body %s)", rec.Code, rec.Body)
	}
	if rec := app.do(t, "POST", "/api/login", map[string]string{"username": "bob", "password": "bobpassword"}, nil); rec.Code != http.StatusForbidden {
		t.Errorf("disabled login: got %d, want 403", rec.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	app, _ := newTestAppWithAdmin(t)

	// Unknown usernames keep this fast (no bcrypt) while still counting
	// against the per-IP bucket.
	for i := 0; i < 10; i++ {
		rec := app.do(t, "POST", "/api/login", map[string]string{"username": fmt.Sprintf("ghost%d", i), "password": "x"}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d, want 401", i+1, rec.Code)
		}
	}
	if rec := app.do(t, "POST", "/api/login", map[string]string{"username": "ghost11", "password": "x"}, nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th attempt: got %d, want 429", rec.Code)
	}
}

func TestLoginRateLimitResetsOnSuccess(t *testing.T) {
	app, _ := newTestAppWithAdmin(t)

	for i := 0; i < 5; i++ {
		app.do(t, "POST", "/api/login", map[string]string{"username": fmt.Sprintf("ghost%d", i), "password": "x"}, nil)
	}
	app.login(t, adminName, adminPass) // success resets the IP bucket

	// Without the reset, the 6th failure after the successful login would
	// exceed the budget; with it, all ten pass as plain 401s.
	for i := 0; i < 10; i++ {
		rec := app.do(t, "POST", "/api/login", map[string]string{"username": fmt.Sprintf("late%d", i), "password": "x"}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("post-reset attempt %d: got %d, want 401", i+1, rec.Code)
		}
	}
	if rec := app.do(t, "POST", "/api/login", map[string]string{"username": "late11", "password": "x"}, nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("post-reset 11th attempt: got %d, want 429", rec.Code)
	}
}

func TestJWTAlgorithmPinned(t *testing.T) {
	app, _ := newTestAppWithAdmin(t)

	// A token declaring alg=none must be rejected even with valid claims.
	token := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"uid": 1, "username": adminName})
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none-token: %v", err)
	}
	cookies := []*http.Cookie{{Name: "console_session", Value: signed}}
	if rec := app.do(t, "GET", "/api/me", nil, cookies); rec.Code != http.StatusUnauthorized {
		t.Errorf("alg=none token: got %d, want 401", rec.Code)
	}
}

func TestRequireAdmin(t *testing.T) {
	app, admin := newTestAppWithAdmin(t)
	app.createUser(t, admin, "carol", "carolpassword", "user", []string{store.PermBroadcast})
	carol := app.login(t, "carol", "carolpassword")

	if rec := app.do(t, "GET", "/api/users", nil, carol); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin list users: got %d, want 403", rec.Code)
	}
	if rec := app.do(t, "POST", "/api/users", map[string]any{"username": "x", "password": "xxxxxxxx"}, carol); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin create user: got %d, want 403", rec.Code)
	}
	if rec := app.do(t, "POST", "/api/servers", map[string]any{"name": "s"}, carol); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin create server: got %d, want 403", rec.Code)
	}
	if rec := app.do(t, "GET", "/api/users", nil, admin); rec.Code != http.StatusOK {
		t.Errorf("admin list users: got %d, want 200", rec.Code)
	}
}

func TestPermissionGating(t *testing.T) {
	app, admin := newTestAppWithAdmin(t)
	app.createUser(t, admin, "mod", "modpassword", "user", []string{store.PermBroadcast})
	mod := app.login(t, "mod", "modpassword")

	// Granted permission passes the gate and reaches the handler, which
	// then 404s on the nonexistent server — proving the middleware let it
	// through without needing a live game server.
	if rec := app.do(t, "POST", "/api/servers/999/broadcast", map[string]string{"message": "hi"}, mod); rec.Code != http.StatusNotFound {
		t.Errorf("granted broadcast: got %d, want 404 (server not found)", rec.Code)
	}
	if rec := app.do(t, "POST", "/api/servers/999/kick", map[string]string{"playerUid": "1"}, mod); rec.Code != http.StatusForbidden {
		t.Errorf("ungranted kick: got %d, want 403", rec.Code)
	}
	// Admins implicitly hold every permission.
	if rec := app.do(t, "POST", "/api/servers/999/save", nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("admin save: got %d, want 404 (server not found)", rec.Code)
	}
}

// Permission and account changes must apply to live sessions immediately,
// not when the week-long token expires.
func TestRevocationAppliesImmediately(t *testing.T) {
	app, admin := newTestAppWithAdmin(t)
	id := app.createUser(t, admin, "mod", "modpassword", "user", []string{store.PermBroadcast})
	mod := app.login(t, "mod", "modpassword")

	if rec := app.do(t, "POST", "/api/servers/999/broadcast", map[string]string{"message": "hi"}, mod); rec.Code != http.StatusNotFound {
		t.Fatalf("granted broadcast: got %d, want 404", rec.Code)
	}

	rec := app.do(t, "PUT", fmt.Sprintf("/api/users/%d", id), map[string]any{
		"role": "user", "permissions": []string{}, "disabled": false,
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke: got %d (body %s)", rec.Code, rec.Body)
	}
	if rec := app.do(t, "POST", "/api/servers/999/broadcast", map[string]string{"message": "hi"}, mod); rec.Code != http.StatusForbidden {
		t.Errorf("revoked broadcast: got %d, want 403", rec.Code)
	}

	rec = app.do(t, "PUT", fmt.Sprintf("/api/users/%d", id), map[string]any{
		"role": "user", "permissions": []string{}, "disabled": true,
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: got %d (body %s)", rec.Code, rec.Body)
	}
	if rec := app.do(t, "GET", "/api/me", nil, mod); rec.Code != http.StatusForbidden {
		t.Errorf("disabled session: got %d, want 403", rec.Code)
	}
}

func TestLastAdminGuards(t *testing.T) {
	app, admin := newTestAppWithAdmin(t)

	// The bootstrap admin has id 1 (first row in a fresh database).
	if rec := app.do(t, "PUT", "/api/users/1", map[string]any{"role": "user", "permissions": []string{}, "disabled": false}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("demote only admin: got %d, want 400", rec.Code)
	}
	if rec := app.do(t, "PUT", "/api/users/1", map[string]any{"role": "admin", "permissions": []string{}, "disabled": true}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("disable only admin: got %d, want 400", rec.Code)
	}
	if rec := app.do(t, "DELETE", "/api/users/1", nil, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("self-delete: got %d, want 400", rec.Code)
	}

	// With a second admin the demotion is allowed.
	id := app.createUser(t, admin, "admin2", "admin2password", "admin", nil)
	if rec := app.do(t, "PUT", fmt.Sprintf("/api/users/%d", id), map[string]any{"role": "user", "permissions": []string{}, "disabled": false}, admin); rec.Code != http.StatusOK {
		t.Errorf("demote second admin: got %d, want 200 (body %s)", rec.Code, rec.Body)
	}
}

func TestCreateUserValidation(t *testing.T) {
	app, admin := newTestAppWithAdmin(t)

	if rec := app.do(t, "POST", "/api/users", map[string]any{"username": "", "password": "longenough"}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("empty username: got %d, want 400", rec.Code)
	}
	if rec := app.do(t, "POST", "/api/users", map[string]any{"username": "dave", "password": "short"}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("short password: got %d, want 400", rec.Code)
	}
	if rec := app.do(t, "POST", "/api/users", map[string]any{"username": adminName, "password": "longenough"}, admin); rec.Code != http.StatusConflict {
		t.Errorf("duplicate username: got %d, want 409", rec.Code)
	}
}

// Regression for T1: a rejected password must reject the whole update, not
// commit the role/permission/disabled changes first.
func TestUpdateUserShortPasswordChangesNothing(t *testing.T) {
	app, admin := newTestAppWithAdmin(t)
	id := app.createUser(t, admin, "carol", "carolpassword", "user", []string{store.PermBroadcast})

	rec := app.do(t, "PUT", fmt.Sprintf("/api/users/%d", id), map[string]any{
		"role": "admin", "permissions": []string{store.PermPower}, "disabled": true, "password": "short",
	}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short password update: got %d, want 400", rec.Code)
	}

	user, err := app.store.GetUser(context.Background(), id)
	if err != nil {
		t.Fatalf("reload carol: %v", err)
	}
	if user.Role != "user" || user.Disabled || len(user.Permissions) != 1 || user.Permissions[0] != store.PermBroadcast {
		t.Errorf("rejected update leaked changes: role=%s disabled=%v perms=%v", user.Role, user.Disabled, user.Permissions)
	}
}

func TestChangeOwnPassword(t *testing.T) {
	app, admin := newTestAppWithAdmin(t)

	if rec := app.do(t, "POST", "/api/me/password", map[string]string{"currentPassword": "wrong", "newPassword": "newpassword1"}, admin); rec.Code != http.StatusForbidden {
		t.Errorf("wrong current password: got %d, want 403", rec.Code)
	}
	if rec := app.do(t, "POST", "/api/me/password", map[string]string{"currentPassword": adminPass, "newPassword": "short"}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("short new password: got %d, want 400", rec.Code)
	}
	if rec := app.do(t, "POST", "/api/me/password", map[string]string{"currentPassword": adminPass, "newPassword": "newpassword1"}, admin); rec.Code != http.StatusNoContent {
		t.Errorf("valid change: got %d, want 204", rec.Code)
	}
	if rec := app.do(t, "POST", "/api/login", map[string]string{"username": adminName, "password": adminPass}, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("old password still works: got %d, want 401", rec.Code)
	}
	app.login(t, adminName, "newpassword1")
}

// Regression for T2: an update that doesn't resend passwords must still
// report the stored ones as present.
func TestUpdateServerReportsStoredPasswords(t *testing.T) {
	app, admin := newTestAppWithAdmin(t)

	rec := app.do(t, "POST", "/api/servers", map[string]any{
		"name": "main", "host": "10.0.0.5", "rconPort": 25575, "rconPassword": "secret123",
		"restPort": 8212, "restPassword": "resty456", "useRest": true, "enabled": true,
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create server: got %d (body %s)", rec.Code, rec.Body)
	}
	created := decodeMap(t, rec)
	id := int64(created["id"].(float64))

	rec = app.do(t, "PUT", fmt.Sprintf("/api/servers/%d", id), map[string]any{
		"name": "renamed", "host": "10.0.0.5", "rconPort": 25575, "rconPassword": "",
		"restPort": 8212, "restPassword": "", "useRest": true, "enabled": true,
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("update server: got %d (body %s)", rec.Code, rec.Body)
	}
	updated := decodeMap(t, rec)
	if updated["name"] != "renamed" {
		t.Errorf("name = %v, want renamed", updated["name"])
	}
	if updated["hasRconPassword"] != true || updated["hasRestPassword"] != true {
		t.Errorf("password flags = rcon:%v rest:%v, want both true", updated["hasRconPassword"], updated["hasRestPassword"])
	}

	srv, err := app.store.GetServer(context.Background(), id)
	if err != nil {
		t.Fatalf("reload server: %v", err)
	}
	if srv.RCONPassword != "secret123" || srv.RESTPassword != "resty456" {
		t.Errorf("stored passwords were clobbered: rcon=%q rest=%q", srv.RCONPassword, srv.RESTPassword)
	}
}

// Regression for T4: bad ids are 400, missing rows 404 — not conflated.
func TestServerLoadErrorMapping(t *testing.T) {
	app, admin := newTestAppWithAdmin(t)

	if rec := app.do(t, "GET", "/api/servers/notanumber/info", nil, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id info: got %d, want 400", rec.Code)
	}
	if rec := app.do(t, "GET", "/api/servers/999/info", nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("missing server info: got %d, want 404", rec.Code)
	}
	if rec := app.do(t, "POST", "/api/servers/notanumber/save", nil, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id save: got %d, want 400", rec.Code)
	}
}

// Unmatched /api paths must return the JSON 404, not the SPA fallback.
func TestUnmatchedAPIRouteIsJSON404(t *testing.T) {
	app := newTestApp(t)

	rec := app.do(t, "GET", "/api/does-not-exist", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	if body := decodeMap(t, rec); body["error"] != "not found" {
		t.Errorf("body = %v, want error=not found", body)
	}
}
