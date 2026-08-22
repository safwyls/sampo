// Package apitest is the api-level test harness for game-module tests:
// a full console app (sqlite store, api.Server, routes) wired the way a
// console main wires it, with the game module's contributions injected
// through Options. It exists so each game's api tests don't rebuild the
// harness core's own tests use — and, like gametest, production code
// must never import it.
package apitest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/safwyls/artificer/core/agentfiles"
	"github.com/safwyls/artificer/core/api"
	"github.com/safwyls/artificer/core/backup"
	"github.com/safwyls/artificer/core/crypto"
	"github.com/safwyls/artificer/core/db"
	"github.com/safwyls/artificer/core/notify"
	"github.com/safwyls/artificer/core/store"
)

const (
	AdminName = "admin"
	AdminPass = "adminpass123"
)

var quietOnce sync.Once

// Options are the game module's contributions to the app under test —
// the same seams a console main wires.
type Options struct {
	// Bans builds the game's offline-config-work queue; nil means none.
	Bans func(st *store.Store, files *agentfiles.Syncer, logger *slog.Logger) api.OfflineConfigWork
	// Provision is the game's provisioning profile; nil disables the wizard.
	Provision *api.ProvisionProfile
	// GameRoutes builds the game's contributed routes (esapi.Mount et al).
	GameRoutes func(*api.Server) func(chi.Router)
	// PublicGameRoutes builds the game's unauthenticated token-gated
	// routes, mounted under /api/public like a console main would.
	PublicGameRoutes func(*api.Server) func(chi.Router)
	// DocsFS backs the advisor docs endpoint; nil serves none.
	DocsFS fs.FS
}

type App struct {
	Handler http.Handler
	Store   *store.Store
	Files   *agentfiles.Syncer
	// API allows tests to set post-construction fields (Provisioner…).
	API *api.Server
}

func New(t *testing.T, opts Options) *App {
	t.Helper()
	quietOnce.Do(func() {
		// Silence chi's per-request logger; it writes straight to stdout.
		middleware.DefaultLogger = middleware.RequestLogger(&middleware.DefaultLogFormatter{
			Logger: log.New(io.Discard, "", 0), NoColor: true,
		})
	})
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
	var bans api.OfflineConfigWork
	if opts.Bans != nil {
		bans = opts.Bans(st, files, logger)
	}
	srv := api.New(st, []byte("test-jwt-secret-0123456789abcdef"), logger, nil, notify.New(st, logger, "Test"),
		backup.New(st, nil, logger, t.TempDir(), files), files, bans)
	srv.Provision = opts.Provision
	srv.DocsFS = opts.DocsFS
	if opts.GameRoutes != nil {
		srv.GameRoutes = opts.GameRoutes(srv)
	}
	if opts.PublicGameRoutes != nil {
		srv.PublicGameRoutes = opts.PublicGameRoutes(srv)
	}
	staticFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	return &App{Handler: srv.Routes(staticFS), Store: st, Files: files, API: srv}
}

// NewWithAdmin also bootstraps the initial admin and logs in.
func NewWithAdmin(t *testing.T, opts Options) (*App, []*http.Cookie) {
	t.Helper()
	app := New(t, opts)
	if err := api.BootstrapAdmin(context.Background(), app.Store, AdminName, AdminPass); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	return app, app.Login(t, AdminName, AdminPass)
}

func (a *App) Do(t *testing.T, method, path string, body any, cookies []*http.Cookie) *httptest.ResponseRecorder {
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
	a.Handler.ServeHTTP(rec, req)
	return rec
}

func (a *App) Login(t *testing.T, username, password string) []*http.Cookie {
	t.Helper()
	rec := a.Do(t, "POST", "/api/login", map[string]string{"username": username, "password": password}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: got %d, want 200 (body %s)", username, rec.Code, rec.Body)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login set no session cookie")
	}
	return cookies
}

// CreateUser provisions a user through the admin API and returns its id.
func (a *App) CreateUser(t *testing.T, admin []*http.Cookie, username, password, role string, perms []string) int64 {
	t.Helper()
	rec := a.Do(t, "POST", "/api/users", map[string]any{
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

func DecodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body, err)
	}
	return m
}
