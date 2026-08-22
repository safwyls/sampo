package collector

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/safwyls/artificer/core/crypto"
	"github.com/safwyls/artificer/core/db"
	"github.com/safwyls/artificer/core/game/gametest"
	"github.com/safwyls/artificer/core/notify"
	"github.com/safwyls/artificer/core/store"

	_ "github.com/safwyls/artificer/core/game/gametest"
)

// palSpy serves the two endpoints a sample touches: the player list (both
// transports can serve it) and metrics (REST only).
type palSpy struct {
	mu      sync.Mutex
	players []map[string]any
	fps     float64
	down    bool
	hits    int
}

func newPalSpy(t *testing.T) (*palSpy, string) {
	t.Helper()
	spy := &palSpy{fps: 60, players: []map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spy.mu.Lock()
		spy.hits++
		down, players, fps := spy.down, spy.players, spy.fps
		spy.mu.Unlock()

		if down {
			http.Error(w, "unreachable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/api/players":
			json.NewEncoder(w).Encode(map[string]any{"players": players})
		case "/v1/api/metrics":
			json.NewEncoder(w).Encode(map[string]any{
				"serverfps": fps, "currentplayernum": len(players),
				"maxplayernum": 32, "serverframetime": 16.6, "uptime": 100,
			})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	return spy, srv.URL
}

func (p *palSpy) setDown(v bool) {
	p.mu.Lock()
	p.down = v
	p.mu.Unlock()
}

func (p *palSpy) setPlayers(names ...string) {
	p.mu.Lock()
	p.players = nil
	for i, n := range names {
		p.players = append(p.players, map[string]any{
			"name": n, "playerId": "p" + strconv.Itoa(i), "userId": "steam_" + strconv.Itoa(i), "level": 10,
		})
	}
	p.mu.Unlock()
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

func newCollector(t *testing.T, st *store.Store) *Collector {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(st, notify.New(st, logger, "Test"), logger)
}

func addServer(t *testing.T, st *store.Store, rawURL string, tweak func(*store.Server)) *store.Server {
	t.Helper()
	u, _ := url.Parse(rawURL)
	port, _ := strconv.Atoi(u.Port())
	srv := &store.Server{
		Name: "main", Host: u.Hostname(),
		RESTPort: port, RESTPassword: "pw",
		RCONPort: 25575, RCONPassword: "pw",
		UseREST: true, Enabled: true, Game: gametest.ID,
	}
	if tweak != nil {
		tweak(srv)
	}
	id, err := st.CreateServer(context.Background(), srv)
	if err != nil {
		t.Fatal(err)
	}
	srv.ID = id
	return srv
}

func TestSampleStoresAMetricRow(t *testing.T) {
	st := newStore(t)
	spy, addr := newPalSpy(t)
	spy.setPlayers("Ada", "Grace")
	srv := addServer(t, st, addr, nil)

	newCollector(t, st).sampleAll(context.Background())

	samples, err := st.ListMetrics(context.Background(), srv.ID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 {
		t.Fatalf("samples = %d, want 1", len(samples))
	}
	got := samples[0]
	if got.PlayerCount == nil || *got.PlayerCount != 2 {
		t.Errorf("playerCount = %v, want 2", got.PlayerCount)
	}
	if got.ServerFPS == nil || *got.ServerFPS != 60 {
		t.Errorf("serverFPS = %v, want 60", got.ServerFPS)
	}
}

func TestSampleSkipsDisabledServers(t *testing.T) {
	st := newStore(t)
	spy, addr := newPalSpy(t)
	srv := addServer(t, st, addr, func(s *store.Server) { s.Enabled = false })

	newCollector(t, st).sampleAll(context.Background())

	samples, _ := st.ListMetrics(context.Background(), srv.ID, time.Now().Add(-time.Hour))
	if len(samples) != 0 {
		t.Errorf("a disabled server was sampled: %d rows", len(samples))
	}
	spy.mu.Lock()
	hits := spy.hits
	spy.mu.Unlock()
	if hits != 0 {
		t.Errorf("a disabled server was probed %d times", hits)
	}
}

// An unreachable server records nothing rather than a row of zeroes, which
// would read as "the server was up with nobody on it".
func TestUnreachableServerRecordsNothing(t *testing.T) {
	st := newStore(t)
	spy, addr := newPalSpy(t)
	srv := addServer(t, st, addr, nil)
	spy.setDown(true)

	c := newCollector(t, st)
	c.sampleAll(context.Background())
	// A second sweep exercises the "already known unreachable" path, which
	// must stay quiet rather than logging on every tick.
	c.sampleAll(context.Background())

	samples, _ := st.ListMetrics(context.Background(), srv.ID, time.Now().Add(-time.Hour))
	if len(samples) != 0 {
		t.Errorf("an unreachable server produced %d samples", len(samples))
	}

	// Coming back is noticed, and sampling resumes.
	spy.setDown(false)
	c.sampleAll(context.Background())
	samples, _ = st.ListMetrics(context.Background(), srv.ID, time.Now().Add(-time.Hour))
	if len(samples) != 1 {
		t.Errorf("sampling did not resume after recovery: %d samples", len(samples))
	}
}

// Metrics are REST-only; an RCON-only server has nothing to sample and that
// isn't an error worth recording.
func TestRCONOnlyServerSamplesNoMetrics(t *testing.T) {
	st := newStore(t)
	_, addr := newPalSpy(t)
	srv := addServer(t, st, addr, func(s *store.Server) { s.UseREST = false })

	newCollector(t, st).sampleAll(context.Background())

	samples, _ := st.ListMetrics(context.Background(), srv.ID, time.Now().Add(-time.Hour))
	if len(samples) != 0 {
		t.Errorf("an RCON-only server produced metric rows: %d", len(samples))
	}
}

// A row naming a game this build doesn't have is skipped, not fatal — the
// sweep still has other servers to sample.
func TestUnknownGameIsSkipped(t *testing.T) {
	st := newStore(t)
	spy, addr := newPalSpy(t)
	addServer(t, st, addr, func(s *store.Server) { s.Game = "ark" })
	good := addServer(t, st, addr, nil)
	spy.setPlayers("Ada")

	newCollector(t, st).sampleAll(context.Background())

	samples, _ := st.ListMetrics(context.Background(), good.ID, time.Now().Add(-time.Hour))
	if len(samples) != 1 {
		t.Errorf("the healthy server was not sampled alongside the unknown one: %d", len(samples))
	}
}

func TestSampleAllWithNoServers(t *testing.T) {
	// The empty install must not error or panic.
	newCollector(t, newStore(t)).sampleAll(context.Background())
}

func TestPruneDropsExpiredRows(t *testing.T) {
	st := newStore(t)
	_, addr := newPalSpy(t)
	srv := addServer(t, st, addr, nil)
	ctx := context.Background()

	old := time.Now().UTC().Add(-Retention - time.Hour)
	n, fps := 1, 60.0
	if err := st.InsertMetric(ctx, srv.ID, store.MetricSample{
		TS: old, PlayerCount: &n, MaxPlayers: &n, ServerFPS: &fps,
	}); err != nil {
		t.Fatal(err)
	}
	fresh := time.Now().UTC()
	if err := st.InsertMetric(ctx, srv.ID, store.MetricSample{
		TS: fresh, PlayerCount: &n, MaxPlayers: &n, ServerFPS: &fps,
	}); err != nil {
		t.Fatal(err)
	}

	newCollector(t, st).prune(ctx)

	samples, err := st.ListMetrics(ctx, srv.ID, time.Now().Add(-10*Retention))
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 {
		t.Errorf("prune left %d samples, want just the fresh one", len(samples))
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	c := newCollector(t, newStore(t))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
