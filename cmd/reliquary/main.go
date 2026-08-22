// Command reliquary is the standalone save-sync service: shared-world
// checkout/check-in custody for any game, independent of the per-game
// consoles (docs/save-sync-architecture.md — the option-B shape, chosen
// once the feature outgrew a single console). It holds the canonical
// versioned saves, the custody ledger, the friend group's accounts and
// their companion tokens; the Artificer Companion (cmd/companion) is its
// player-side client, and a dedicated server joins through a world's
// agent link.
//
// Deliberately game-blind: it registers no game module, so save
// verification is structural (well-formed bundle, non-empty, size
// bounds) and game knowledge lives in the metadata companions report.
// It holds no Docker rights and talks to nothing above it — the same
// posture as anvil, one floor up.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/safwyls/artificer/core/api"
	"github.com/safwyls/artificer/core/cfaccess"
	"github.com/safwyls/artificer/core/config"
	"github.com/safwyls/artificer/core/crypto"
	"github.com/safwyls/artificer/core/db"
	"github.com/safwyls/artificer/core/notify"
	"github.com/safwyls/artificer/core/savedb"
	"github.com/safwyls/artificer/core/savesync"
	"github.com/safwyls/artificer/core/store"
	web "github.com/safwyls/artificer/web/reliquary"
)

// version is stamped by the image build (-X main.version=<sha or tag>);
// a plain `go build` leaves it "dev". The vault page shows it, and the
// companion shows it beside its own, so a report about save sync can
// name both halves.
var version = "dev"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	for _, msg := range config.RetiredSettings() {
		logger.Warn(msg)
	}

	sqlDB, err := db.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	box, err := crypto.New(cfg.EncryptionKey)
	if err != nil {
		return err
	}
	st := store.New(sqlDB, box)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := api.BootstrapAdmin(ctx, st, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		return err
	}

	// World webhooks ride the shared notifier; there are no server rows
	// here, so only the sync events ever fire.
	notifier := notify.New(st, logger, "Reliquary")

	saveSync := savesync.New(st, notifier, logger, cfg.DataDir)
	go saveSync.Run(ctx)

	apiServer := api.New(st, cfg.JWTSecret, logger, nil, notifier, nil, nil, nil)
	apiServer.SessionCookie = "reliquary_session"
	apiServer.SaveSync = saveSync
	apiServer.CookieSecure = cfg.CookieSecure
	// The bundled companion the token-gated download hands out; the
	// image build places it beside the binary, and GitHub releases carry
	// the same exe for everyone else.
	companionExe := os.Getenv("COMPANION_EXE")
	if companionExe == "" {
		if _, err := os.Stat("artificer-companion.exe"); err == nil {
			companionExe = "artificer-companion.exe"
		}
	}
	apiServer.CompanionExe = companionExe
	apiServer.Version = version
	// Cover art for the game shelf. Credentials are a Twitch client id
	// and secret (IGDB's auth); without both, artwork is simply absent
	// and every surface degrades to names — never an error. Two sources:
	// the environment here, and a pair saved through the admin panel,
	// which wins. Either can be set without the other existing.
	apiServer.UseEnvArtwork(os.Getenv("IGDB_CLIENT_ID"), os.Getenv("IGDB_CLIENT_SECRET"))
	stored, err := apiServer.LoadStoredArtwork(ctx)
	if err != nil {
		// A pair that will not decrypt (a rotated ENCRYPTION_KEY) costs
		// covers, not the service.
		logger.Error("reading saved igdb credentials", "error", err)
	}
	if apiServer.Artwork.Configured() {
		logger.Info("igdb artwork enabled", "source", map[bool]string{true: "settings", false: "env"}[stored])
	} else {
		logger.Info("igdb artwork not configured; the shelf will show names without covers")
	}
	// Save locations, from the Ludusavi manifest: a community catalogue
	// of where games keep their saves, fetched once here so no player's
	// machine pulls 17MB of YAML. SAVEDB_URL overrides the source;
	// SAVEDB_DISABLED=1 turns it off, and the companion falls back to
	// its own heuristics.
	if os.Getenv("SAVEDB_DISABLED") != "1" {
		saveDB := savedb.New(os.Getenv("SAVEDB_URL"), logger)
		apiServer.SaveDB = saveDB
		// In the background: a 17MB fetch and parse must not hold up
		// listening, and a host with no egress must not fail to start.
		go saveDB.Run(ctx)
	} else {
		logger.Info("save-location catalogue disabled; companions fall back to their own heuristics")
	}
	if cfg.AccessEnabled() {
		verifier, err := cfaccess.New(cfg.AccessTeamDomain, cfg.AccessAUD)
		if err != nil {
			return fmt.Errorf("configuring cloudflare access: %w", err)
		}
		apiServer.Access = verifier
		apiServer.AccessAdminEmails = cfg.AccessAdminEmails
		// Anyone your Access policy lets through is a member of this
		// friend group, so they arrive able to hold worlds rather than
		// waiting for an admin to grant it one account at a time. Set
		// ACCESS_GRANT_CUSTODY=0 to keep the console default, where an
		// SSO identity confers nothing until someone says so.
		if os.Getenv("ACCESS_GRANT_CUSTODY") != "0" {
			apiServer.AccessGrants = []string{store.PermSync}
			logger.Info("cloudflare access sign-ins receive the world-custody grant")
		}
		logger.Info("cloudflare access sign-in enabled", "issuer", verifier.Issuer())
	}

	// The React frontend, built by `npm run build` in web/reliquary and
	// embedded there — the same pattern the three consoles use.
	ui, err := web.Dist()
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           apiServer.VaultRoutes(ui),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
