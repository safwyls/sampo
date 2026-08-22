package watchdog

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/safwyls/artificer/core/crypto"
	"github.com/safwyls/artificer/core/db"
	"github.com/safwyls/artificer/core/dockerctl"
	"github.com/safwyls/artificer/core/notify"
	"github.com/safwyls/artificer/core/store"
)

// fakeDocker answers inspect with a canned container state and records the
// starts the watchdog asks for.
type fakeDocker struct {
	mu       sync.Mutex
	inspect  string
	starts   []string
	inspects int
	failAll  bool
}

func newFakeDocker(t *testing.T, inspectJSON string) (*fakeDocker, *dockerctl.Client) {
	t.Helper()
	f := &fakeDocker{inspect: inspectJSON}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failAll {
			http.Error(w, "docker is down", http.StatusInternalServerError)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/json"):
			f.inspects++
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, f.inspect)
		case strings.HasSuffix(r.URL.Path, "/start"):
			f.starts = append(f.starts, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
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

func (f *fakeDocker) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts)
}

func (f *fakeDocker) inspectCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inspects
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	box, err := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return store.New(sqlDB, box)
}

const crashedContainer = `{"Name":"/flameagent-main","State":{"Status":"exited","Running":false,"ExitCode":137}}`
const healthyContainer = `{"Name":"/flameagent-main","State":{"Status":"running","Running":true,"StartedAt":"2026-07-26T05:00:00Z"}}`

func newWatchdog(t *testing.T, st *store.Store, docker *dockerctl.Client) *Watchdog {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(st, docker, notify.New(st, logger, "Test"), logger)
}

// addServer registers a row; over can switch off the flags the sweep gates on.
func addServer(t *testing.T, st *store.Store, over func(*store.Server)) *store.Server {
	t.Helper()
	srv := &store.Server{
		Name: "main", Host: "10.0.0.5", Enabled: true,
		Watchdog: true, ContainerName: "flameagent-main",
	}
	if over != nil {
		over(srv)
	}
	id, err := st.CreateServer(context.Background(), srv)
	if err != nil {
		t.Fatal(err)
	}
	srv.ID = id
	// Watchdog is deliberately not part of CreateServer/UpdateServer, so a
	// server-edit form save can't silently wipe it — it has its own setter.
	if err := st.SetWatchdog(context.Background(), id, srv.Watchdog); err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestSweepRestartsACrashedContainer(t *testing.T) {
	st := newStore(t)
	fake, docker := newFakeDocker(t, crashedContainer)
	srv := addServer(t, st, nil)
	w := newWatchdog(t, st, docker)

	w.sweep(context.Background())

	if fake.startCount() != 1 {
		t.Fatalf("starts = %d, want 1", fake.startCount())
	}
	// The revival is auditable: "why did my server restart at 4am" needs an
	// answer that isn't the logs.
	entries, err := st.ListAudit(context.Background(), srv.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if e.Action == "watchdog-restart" && strings.Contains(e.Detail, "exit code 137") {
			found = true
		}
	}
	if !found {
		t.Errorf("no watchdog-restart audit row: %+v", entries)
	}
}

func TestSweepLeavesAHealthyContainerAlone(t *testing.T) {
	st := newStore(t)
	fake, docker := newFakeDocker(t, healthyContainer)
	addServer(t, st, nil)

	newWatchdog(t, st, docker).sweep(context.Background())

	if fake.startCount() != 0 {
		t.Errorf("a running container was restarted: %d starts", fake.startCount())
	}
}

// The sweep's gates: a server has to be enabled, opted in, and have a
// container name before the watchdog touches it.
func TestSweepSkipsServersItDoesNotWatch(t *testing.T) {
	cases := map[string]func(*store.Server){
		"disabled":         func(s *store.Server) { s.Enabled = false },
		"watchdog off":     func(s *store.Server) { s.Watchdog = false },
		"no container set": func(s *store.Server) { s.ContainerName = "" },
	}
	for name, over := range cases {
		t.Run(name, func(t *testing.T) {
			st := newStore(t)
			fake, docker := newFakeDocker(t, crashedContainer)
			addServer(t, st, over)

			newWatchdog(t, st, docker).sweep(context.Background())

			if fake.inspectCount() != 0 {
				t.Errorf("docker was consulted for a server we don't watch: %d inspects", fake.inspectCount())
			}
			if fake.startCount() != 0 {
				t.Errorf("an unwatched server was restarted: %d starts", fake.startCount())
			}
		})
	}
}

// An unreachable docker endpoint is not a crashed game server.
func TestSweepSurvivesAnUnreachableDocker(t *testing.T) {
	st := newStore(t)
	fake, docker := newFakeDocker(t, crashedContainer)
	fake.mu.Lock()
	fake.failAll = true
	fake.mu.Unlock()
	addServer(t, st, nil)

	newWatchdog(t, st, docker).sweep(context.Background())

	if fake.startCount() != 0 {
		t.Errorf("a failed inspect still led to a restart: %d starts", fake.startCount())
	}
}

// Three crashes in a row need a human, not a fourth restart — and the
// stand-down is recorded so the silence afterwards is explained.
func TestSweepStandsDownAfterRepeatedCrashes(t *testing.T) {
	st := newStore(t)
	fake, docker := newFakeDocker(t, crashedContainer)
	srv := addServer(t, st, nil)
	w := newWatchdog(t, st, docker)
	ctx := context.Background()

	// Drive the strikes directly: the cooldown between attempts is minutes
	// long, and this test is about what happens once they're spent.
	for i := 0; i < maxStrikes; i++ {
		w.mu.Lock()
		state := w.state[srv.ID]
		if state == nil {
			state = &serverState{}
			w.state[srv.ID] = state
		}
		state.lastAttempt = state.lastAttempt.Add(-cooldown * 2)
		w.mu.Unlock()
		w.sweep(ctx)
	}
	if fake.startCount() != maxStrikes {
		t.Fatalf("starts = %d, want %d", fake.startCount(), maxStrikes)
	}

	// One more sweep past the cooldown gives up rather than restarting.
	w.mu.Lock()
	w.state[srv.ID].lastAttempt = w.state[srv.ID].lastAttempt.Add(-cooldown * 2)
	w.mu.Unlock()
	w.sweep(ctx)

	if fake.startCount() != maxStrikes {
		t.Errorf("the watchdog restarted a fourth time: %d starts", fake.startCount())
	}
	entries, err := st.ListAudit(ctx, srv.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var stoodDown bool
	for _, e := range entries {
		if e.Action == "watchdog-standdown" {
			stoodDown = true
		}
	}
	if !stoodDown {
		t.Errorf("standing down was never recorded: %+v", entries)
	}
}

// Run must return promptly when its context is cancelled, or shutdown hangs.
func TestRunStopsOnContextCancel(t *testing.T) {
	st := newStore(t)
	_, docker := newFakeDocker(t, healthyContainer)
	w := newWatchdog(t, st, docker)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	cancel()
	<-done
}
