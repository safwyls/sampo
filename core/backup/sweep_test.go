package backup

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/safwyls/artificer/core/agentfiles"
	"github.com/safwyls/artificer/core/crypto"
	"github.com/safwyls/artificer/core/db"
	"github.com/safwyls/artificer/core/notify"
	"github.com/safwyls/artificer/core/store"
)

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

// storeRunner is testRunner with a real store behind it, which the sweep
// needs in order to find servers at all.
func storeRunner(t *testing.T, st *store.Store) *Runner {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(st, notify.New(st, logger, "Test"), logger, t.TempDir(), agentfiles.New(t.TempDir(), logger))
}

// addServer stores a row with a local save path, which is what makes it a
// backup candidate.
func addServer(t *testing.T, st *store.Store, savePath string, intervalHours int, enabled bool) *store.Server {
	t.Helper()
	srv := &store.Server{
		Name: "main", Host: "10.0.0.5", Enabled: enabled,
		SavePath: savePath, RCONPort: 25575, RESTPort: 8212,
	}
	id, err := st.CreateServer(context.Background(), srv)
	if err != nil {
		t.Fatal(err)
	}
	srv.ID = id
	// The interval has its own setter, like the watchdog flag — a
	// server-edit form save must not silently wipe a backup schedule.
	if err := st.SetBackupSettings(context.Background(), id, intervalHours, 3); err != nil {
		t.Fatal(err)
	}
	srv.BackupIntervalHours = intervalHours
	srv.BackupKeep = 3
	return srv
}

func TestSweepBacksUpADueServer(t *testing.T) {
	st := newStore(t)
	r := storeRunner(t, st)
	srv := addServer(t, st, fakeSave(t), 1, true)

	r.sweep(context.Background())

	snaps, err := r.List(srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(snaps))
	}
}

// The sweep's gates: disabled servers, servers with no save configured, and
// servers with scheduling switched off are all left alone.
func TestSweepSkipsServersItShouldNot(t *testing.T) {
	cases := map[string]struct {
		save     string
		interval int
		enabled  bool
	}{
		"disabled":     {"", 1, false},
		"no interval":  {"", 0, true},
		"no save path": {"", 1, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			st := newStore(t)
			r := storeRunner(t, st)
			save := tc.save
			if name != "no save path" {
				save = fakeSave(t)
			}
			srv := addServer(t, st, save, tc.interval, tc.enabled)

			r.sweep(context.Background())

			snaps, _ := r.List(srv.ID)
			if len(snaps) != 0 {
				t.Errorf("%s was backed up anyway: %d snapshots", name, len(snaps))
			}
		})
	}
}

// A second sweep inside the interval doesn't re-archive an unchanged save —
// a server nobody has played doesn't need a second identical zip.
func TestSweepIsIdempotentInsideTheInterval(t *testing.T) {
	st := newStore(t)
	r := storeRunner(t, st)
	srv := addServer(t, st, fakeSave(t), 1, true)

	r.sweep(context.Background())
	r.sweep(context.Background())

	snaps, _ := r.List(srv.ID)
	if len(snaps) != 1 {
		t.Errorf("snapshots = %d, want 1 — the sweep re-archived an unchanged save", len(snaps))
	}
}

func TestSweepWithNoServers(t *testing.T) {
	st := newStore(t)
	// The empty install must not error or panic.
	storeRunner(t, st).sweep(context.Background())
}

// Running is what the UI reads to show a backup in flight; it must be false
// when nothing is happening and must not report one server's work for
// another.
func TestRunningTracksInFlightBackups(t *testing.T) {
	st := newStore(t)
	r := storeRunner(t, st)
	srv := addServer(t, st, fakeSave(t), 1, true)

	if r.Running(srv.ID) {
		t.Error("Running is true with nothing in flight")
	}
	if _, err := r.BackupNow(context.Background(), srv); err != nil {
		t.Fatal(err)
	}
	if r.Running(srv.ID) {
		t.Error("Running is still true after the backup finished")
	}
	if r.Running(9999) {
		t.Error("Running reported work for a server that has none")
	}
}

func TestDeleteRemovesASnapshot(t *testing.T) {
	st := newStore(t)
	r := storeRunner(t, st)
	srv := addServer(t, st, fakeSave(t), 1, true)

	if _, err := r.BackupNow(context.Background(), srv); err != nil {
		t.Fatal(err)
	}
	snaps, _ := r.List(srv.ID)
	if len(snaps) != 1 {
		t.Fatalf("setup: %d snapshots", len(snaps))
	}

	if err := r.Delete(srv.ID, snaps[0].Name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if after, _ := r.List(srv.ID); len(after) != 0 {
		t.Errorf("snapshot survived deletion: %d left", len(after))
	}

	// Deleting one that isn't there is an error, not a silent success.
	if err := r.Delete(srv.ID, snaps[0].Name); err == nil {
		t.Error("deleting a missing snapshot reported success")
	}
}

// Delete shares List's traversal guard: a crafted name must not reach
// outside the server's own backup directory.
func TestDeleteRejectsTraversal(t *testing.T) {
	st := newStore(t)
	r := storeRunner(t, st)
	for _, name := range []string{"../../etc/passwd", "..", "a/b.zip"} {
		if err := r.Delete(7, name); err == nil {
			t.Errorf("Delete(%q) was allowed", name)
		}
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	st := newStore(t)
	r := storeRunner(t, st)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
