package sched

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/safwyls/artificer/core/dockerctl"
	"github.com/safwyls/artificer/core/game/gametest"
	"github.com/safwyls/artificer/core/notify"
	"github.com/safwyls/artificer/core/store"
)

// agentSpy is a flameagent that reports whichever mode a test asks for and
// records the power verbs it receives.
type agentSpy struct {
	mu    sync.Mutex
	calls []string
	mode  string
	down  bool
}

func newAgentSpy(t *testing.T, mode string) (*agentSpy, string) {
	t.Helper()
	spy := &agentSpy{mode: mode}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spy.mu.Lock()
		spy.calls = append(spy.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		mode, down := spy.mode, spy.down
		spy.mu.Unlock()

		if down {
			http.Error(w, "agent is unreachable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/health":
			body := map[string]any{"agent": "flameagent", "mode": mode, "apiVersion": 1}
			// Only a supervisor reports a game; a companion's is null, which
			// is half of what marks it as not owning the process.
			if mode == "supervisor" {
				body["game"] = map[string]any{"state": "running"}
			}
			json.NewEncoder(w).Encode(body)
		case strings.HasPrefix(r.URL.Path, "/v1/power/"):
			json.NewEncoder(w).Encode(map[string]any{"game": map[string]any{"state": "running"}})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	return spy, srv.URL
}

func (a *agentSpy) saw(fragment string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Contains(strings.Join(a.calls, " | "), fragment)
}

func (a *agentSpy) setDown(v bool) {
	a.mu.Lock()
	a.down = v
	a.mu.Unlock()
}

// dockerSpy records container actions so a test can prove docker was — or
// wasn't — the thing that restarted the server.
type dockerSpy struct {
	mu      sync.Mutex
	actions []string
}

func newDockerSpy(t *testing.T) (*dockerSpy, *dockerctl.Client) {
	t.Helper()
	spy := &dockerSpy{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spy.mu.Lock()
		spy.actions = append(spy.actions, r.URL.Path)
		spy.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	client, err := dockerctl.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return spy, client
}

func (d *dockerSpy) restarted() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Contains(strings.Join(d.actions, " "), "/restart")
}

// addAgentServer registers a row wired to both a game endpoint and an
// agent, with a container name — the shape a provisioned server now has.
func addAgentServer(t *testing.T, st *store.Store, gameURL, agentURL, container string) *store.Server {
	t.Helper()
	u, _ := url.Parse(gameURL)
	port, _ := strconv.Atoi(u.Port())
	srv := &store.Server{
		Name: "main", Host: u.Hostname(),
		RESTPort: port, RESTPassword: "pw",
		RCONPort: 25575, RCONPassword: "pw",
		UseREST: true, Enabled: true, Game: gametest.ID,
		AgentURL: agentURL, AgentToken: "agent-token-0123456789abcdef",
		ContainerName: container,
	}
	id, err := st.CreateServer(context.Background(), srv)
	if err != nil {
		t.Fatal(err)
	}
	srv.ID = id
	return srv
}

func schedulerWithDocker(t *testing.T, st *store.Store, docker *dockerctl.Client) *Scheduler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(st, notify.New(st, logger, "Test"), docker, logger, nil)
}

// The regression this guards: a provisioned server carries both an agent
// and a container name, and flametender's docker proxy may be pointed at a
// different daemon than the one running it. The agent is the only half
// guaranteed to be looking at the right machine, so it wins — exactly as
// the manual Restart button decides it.
func TestSupervisorAgentRestartsInsteadOfDocker(t *testing.T) {
	st := newStore(t)
	game, gameURL := newGameSpy(t)
	agent, agentURL := newAgentSpy(t, "supervisor")
	docker, dockerClient := newDockerSpy(t)
	srv := addAgentServer(t, st, gameURL, agentURL, "flameagent-main")
	sc := addSchedule(t, st, srv.ID, time.Now().Truncate(time.Minute), nil, true)

	schedulerWithDocker(t, st, dockerClient).restart(context.Background(), srv, sc)

	if !agent.saw("POST /v1/power/restart") {
		t.Errorf("the agent was not asked to restart: %v", agent.calls)
	}
	if docker.restarted() {
		t.Errorf("docker restarted a server the agent owns: %v", docker.actions)
	}
	// The in-game courtesy still happens either way.
	if !game.saw("/v1/api/save") || !game.saw("/v1/api/shutdown") {
		t.Errorf("the save/shutdown courtesy was skipped: %v", game.calls)
	}
}

// An accepted in-game shutdown means the game is already on its way out;
// signalling on top of it lands mid-save. The agent is told to wait.
func TestAgentRestartWaitsOutAnAcceptedShutdown(t *testing.T) {
	st := newStore(t)
	_, gameURL := newGameSpy(t)
	agent, agentURL := newAgentSpy(t, "supervisor")
	srv := addAgentServer(t, st, gameURL, agentURL, "")
	sc := addSchedule(t, st, srv.ID, time.Now().Truncate(time.Minute), nil, true)

	newScheduler(t, st).restart(context.Background(), srv, sc)

	if !agent.saw("graceful=") {
		t.Errorf("the agent was not given a self-exit window: %v", agent.calls)
	}
}

// A companion agent doesn't own the game process, so it must not be
// mistaken for one that does — docker stays in charge.
func TestCompanionAgentFallsBackToDocker(t *testing.T) {
	st := newStore(t)
	_, gameURL := newGameSpy(t)
	agent, agentURL := newAgentSpy(t, "companion")
	docker, dockerClient := newDockerSpy(t)
	srv := addAgentServer(t, st, gameURL, agentURL, "flameagent-main")
	sc := addSchedule(t, st, srv.ID, time.Now().Truncate(time.Minute), nil, true)

	schedulerWithDocker(t, st, dockerClient).restart(context.Background(), srv, sc)

	if agent.saw("/v1/power/") {
		t.Errorf("a companion agent was asked to restart the game: %v", agent.calls)
	}
	if !docker.restarted() {
		t.Errorf("docker did not restart the container: %v", docker.actions)
	}
}

// An unreachable agent is not a reason to skip the restart — fall back to
// docker rather than leaving the server down.
func TestUnreachableAgentFallsBackToDocker(t *testing.T) {
	st := newStore(t)
	_, gameURL := newGameSpy(t)
	agent, agentURL := newAgentSpy(t, "supervisor")
	agent.setDown(true)
	docker, dockerClient := newDockerSpy(t)
	srv := addAgentServer(t, st, gameURL, agentURL, "flameagent-main")
	sc := addSchedule(t, st, srv.ID, time.Now().Truncate(time.Minute), nil, true)

	schedulerWithDocker(t, st, dockerClient).restart(context.Background(), srv, sc)

	if !docker.restarted() {
		t.Errorf("an unreachable agent left the server unrestarted: %v", docker.actions)
	}
}

// The player countdown tracks whether anything is standing by to bring the
// server back: one second when something will, ten when the in-game
// shutdown is itself the restart.
func TestCountdownMatchesWhoIsRestarting(t *testing.T) {
	waitFor := func(t *testing.T, agentMode string, withDocker bool, container string) float64 {
		t.Helper()
		st := newStore(t)
		game, gameURL := newGameSpy(t)
		agentURL := ""
		if agentMode != "" {
			_, agentURL = newAgentSpy(t, agentMode)
		}
		var dockerClient *dockerctl.Client
		if withDocker {
			_, dockerClient = newDockerSpy(t)
		}
		srv := addAgentServer(t, st, gameURL, agentURL, container)
		sc := addSchedule(t, st, srv.ID, time.Now().Truncate(time.Minute), nil, true)

		schedulerWithDocker(t, st, dockerClient).restart(context.Background(), srv, sc)

		body := game.body("/v1/api/shutdown")
		if body == nil {
			t.Fatal("no in-game shutdown was sent")
		}
		wait, _ := body["waittime"].(float64)
		return wait
	}

	if got := waitFor(t, "supervisor", true, "flameagent-main"); got != 1 {
		t.Errorf("agent-restarted countdown = %v, want 1", got)
	}
	if got := waitFor(t, "", true, "flameagent-main"); got != 1 {
		t.Errorf("docker-restarted countdown = %v, want 1", got)
	}
	// Nothing configured to restart it: the shutdown is the restart, so
	// players get a real warning.
	if got := waitFor(t, "", false, ""); got != 10 {
		t.Errorf("unassisted countdown = %v, want 10", got)
	}
}
