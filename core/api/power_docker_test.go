package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/safwyls/artificer/core/agentfiles"
	"github.com/safwyls/artificer/core/api"
	"github.com/safwyls/artificer/core/backup"
	"github.com/safwyls/artificer/core/crypto"
	"github.com/safwyls/artificer/core/db"
	"github.com/safwyls/artificer/core/dockerctl"
	"github.com/safwyls/artificer/core/notify"
	"github.com/safwyls/artificer/core/store"
)

// dockerFake answers the handful of endpoints the power handlers use and
// records the actions asked of it.
type dockerFake struct {
	mu      sync.Mutex
	actions []string
	state   string
	fail    bool
}

func newDockerFake(t *testing.T) (*dockerFake, *dockerctl.Client) {
	t.Helper()
	f := &dockerFake{state: `{"Name":"/flameagent-main","State":{"Status":"running","Running":true,"StartedAt":"2026-08-04T10:00:00Z"}}`}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.actions = append(f.actions, r.Method+" "+r.URL.Path)
		state, fail := f.state, f.fail
		f.mu.Unlock()

		if fail {
			http.Error(w, "docker is unwell", http.StatusInternalServerError)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, state)
		case strings.HasSuffix(r.URL.Path, "/logs"):
			io.WriteString(w, "line one\nline two\n")
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	client, err := dockerctl.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return f, client
}

func (d *dockerFake) saw(fragment string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Contains(strings.Join(d.actions, " | "), fragment)
}

func (d *dockerFake) setFail(v bool) {
	d.mu.Lock()
	d.fail = v
	d.mu.Unlock()
}

// newTestAppWithDocker is newTestApp with container control configured,
// which the default app deliberately leaves off.
func newTestAppWithDocker(t *testing.T, docker *dockerctl.Client) (*testApp, []*http.Cookie) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	box, err := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(sqlDB, box)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	files := agentfiles.New(t.TempDir(), logger)
	srv := api.New(st, []byte("test-jwt-secret-0123456789abcdef"), logger, docker,
		notify.New(st, logger, "Test"), backup.New(st, nil, logger, t.TempDir(), files), files, nil)
	staticFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	app := &testApp{handler: srv.Routes(staticFS), store: st, api: srv}

	if err := api.BootstrapAdmin(context.Background(), st, adminName, adminPass); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	return app, app.login(t, adminName, adminPass)
}

func addContainerServer(t *testing.T, app *testApp, container string) int64 {
	t.Helper()
	id, err := app.store.CreateServer(context.Background(), &store.Server{
		Name: "main", Host: "10.0.0.5", Enabled: true,
		RCONPort: 25575, RESTPort: 8212, ContainerName: container,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestContainerStatus(t *testing.T) {
	_, docker := newDockerFake(t)
	app, admin := newTestAppWithDocker(t, docker)
	base := "/api/servers/" + itoa(addContainerServer(t, app, "flameagent-main"))

	rec := app.do(t, "GET", base+"/container", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("container status: %d (body %s)", rec.Code, rec.Body)
	}
	var state struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Running bool   `json:"running"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Name != "flameagent-main" || !state.Running || state.Status != "running" {
		t.Errorf("state = %+v", state)
	}
}

func TestContainerStatusWithNoContainerNameSet(t *testing.T) {
	_, docker := newDockerFake(t)
	app, admin := newTestAppWithDocker(t, docker)
	base := "/api/servers/" + itoa(addContainerServer(t, app, ""))

	rec := app.do(t, "GET", base+"/container", nil, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status with no container name: %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "container name") {
		t.Errorf("the error should name the missing half: %s", rec.Body)
	}
}

func TestContainerStatusWhenDockerIsUnwell(t *testing.T) {
	fake, docker := newDockerFake(t)
	app, admin := newTestAppWithDocker(t, docker)
	base := "/api/servers/" + itoa(addContainerServer(t, app, "flameagent-main"))
	fake.setFail(true)

	if rec := app.do(t, "GET", base+"/container", nil, admin); rec.Code != http.StatusBadGateway {
		t.Errorf("status with a failing docker: %d, want 502", rec.Code)
	}
}

func TestContainerActions(t *testing.T) {
	fake, docker := newDockerFake(t)
	app, admin := newTestAppWithDocker(t, docker)
	id := addContainerServer(t, app, "flameagent-main")
	base := "/api/servers/" + itoa(id)

	for _, action := range []string{"start", "stop", "restart"} {
		rec := app.do(t, "POST", base+"/container/"+action, nil, admin)
		if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
			t.Fatalf("%s: %d (body %s)", action, rec.Code, rec.Body)
		}
		if !fake.saw("/" + action) {
			t.Errorf("%s never reached docker: %v", action, fake.actions)
		}
	}

	// Bouncing a server other people are playing on is never anonymous.
	rec := app.do(t, "GET", base+"/audit", nil, admin)
	trail := rec.Body.String()
	for _, action := range []string{"power-start", "power-stop", "power-restart"} {
		if !strings.Contains(trail, action) {
			t.Errorf("audit trail is missing %q: %s", action, trail)
		}
	}
	if !strings.Contains(trail, adminName) {
		t.Errorf("the audit trail does not name the actor: %s", trail)
	}
}

func TestContainerActionRejectsAnUnknownVerb(t *testing.T) {
	fake, docker := newDockerFake(t)
	app, admin := newTestAppWithDocker(t, docker)
	base := "/api/servers/" + itoa(addContainerServer(t, app, "flameagent-main"))

	if rec := app.do(t, "POST", base+"/container/explode", nil, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown action: %d, want 400", rec.Code)
	}
	if fake.saw("explode") {
		t.Error("an unknown action was forwarded to docker")
	}
}

func TestContainerActionsNeedThePowerPermission(t *testing.T) {
	fake, docker := newDockerFake(t)
	app, admin := newTestAppWithDocker(t, docker)
	base := "/api/servers/" + itoa(addContainerServer(t, app, "flameagent-main"))
	app.createUser(t, admin, "peon", "peonpass12345", "user", nil)
	peon := app.login(t, "peon", "peonpass12345")

	for _, action := range []string{"start", "stop", "restart"} {
		if rec := app.do(t, "POST", base+"/container/"+action, nil, peon); rec.Code != http.StatusForbidden {
			t.Errorf("%s without the power permission: %d, want 403", action, rec.Code)
		}
	}
	if fake.saw("/start") || fake.saw("/stop") {
		t.Error("a refused action still reached docker")
	}
}

func TestContainerLogs(t *testing.T) {
	_, docker := newDockerFake(t)
	app, admin := newTestAppWithDocker(t, docker)
	base := "/api/servers/" + itoa(addContainerServer(t, app, "flameagent-main"))

	rec := app.do(t, "GET", base+"/container/logs", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("logs: %d (body %s)", rec.Code, rec.Body)
	}
	var res struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Lines) != 2 || res.Lines[0] != "line one" {
		t.Errorf("lines = %v", res.Lines)
	}
}
