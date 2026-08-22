// Command palcon is Palcon: a self-hosted RCON/REST management console
// for Palworld dedicated servers.
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

	"github.com/safwyls/artificer/cmd/palcon/docs"
	"github.com/safwyls/artificer/core/advisor"
	"github.com/safwyls/artificer/core/agentfiles"
	anvil "github.com/safwyls/artificer/core/anvilclient"
	"github.com/safwyls/artificer/core/api"
	"github.com/safwyls/artificer/core/backup"
	"github.com/safwyls/artificer/core/cfaccess"
	"github.com/safwyls/artificer/core/collector"
	"github.com/safwyls/artificer/core/config"
	"github.com/safwyls/artificer/core/crypto"
	"github.com/safwyls/artificer/core/db"
	"github.com/safwyls/artificer/core/dockerctl"
	"github.com/safwyls/artificer/core/game"
	"github.com/safwyls/artificer/core/notify"
	"github.com/safwyls/artificer/core/sched"
	"github.com/safwyls/artificer/core/store"
	"github.com/safwyls/artificer/core/watchdog"
	"github.com/safwyls/artificer/games/palworld"
	"github.com/safwyls/artificer/games/palworld/palapi"
	"github.com/safwyls/artificer/games/palworld/palsave"
	web "github.com/safwyls/artificer/web/palcon"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	// This console registers exactly one game (the palworld package's
	// init ran on import); unlabelled rows resolve to it.
	game.DefaultID = palworld.Definition.ID

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Settings that are set but no longer read. Silence here reads as a
	// feature that vanished for no reason — most often provisioning.
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

	distFS, err := web.Dist()
	if err != nil {
		return err
	}

	// Materializes the embedded save-extractor script into the data dir;
	// actually using it also requires python3 + palworld-save-tools in the
	// runtime environment (both present in the Docker image).
	palReader, err := palsave.NewReader(cfg.DataDir)
	if err != nil {
		return err
	}

	// Discord notifications: the collector reports reachability changes
	// and player joins/leaves through it, the scheduler restart notices.
	notifier := notify.New(st, logger, "Palcon")

	// Samples server health in the background so the dashboard charts have
	// history to draw, rather than only what's happened since page load.
	// Shutdown is awaited below: it closes out open play sessions.
	collectorDone := make(chan struct{})
	go func() {
		defer close(collectorDone)
		collector.New(st, notifier, logger).Run(ctx)
	}()

	// Resolves each server's save/config to a local path — a bind mount,
	// or a cache mirrored from its palagent sidecar (phase 2).
	files := agentfiles.New(cfg.DataDir, logger)

	// Keeps the save-parse cache warm across autosaves (and restarts), so
	// the pals pages open onto a cache hit instead of a multi-second parse.
	// For agent-backed servers this same loop drives the save sync.
	go collector.NewSaveRefresher(st, palReader, files, logger).Run(ctx)

	// Optional: without DOCKER_HOST, power control is simply absent.
	var docker *dockerctl.Client
	if cfg.DockerHost != "" {
		docker, err = dockerctl.New(cfg.DockerHost)
		if err != nil {
			return fmt.Errorf("configuring docker control: %w", err)
		}
		logger.Info("docker control enabled", "endpoint", cfg.DockerHost)
	}

	// Runs scheduled restarts (warnings included) for every server.
	go sched.New(st, notifier, docker, logger, nil).Run(ctx)

	// Crash watchdog: revives watched containers after an unclean exit.
	// Meaningless without docker control, so it only runs alongside it.
	if docker != nil {
		go watchdog.New(st, docker, notifier, logger).Run(ctx)
	}

	// Save backups: zip snapshots of the read-only save mount into the
	// data dataset, on each server's schedule.
	backups := backup.New(st, notifier, logger, cfg.DataDir, files)
	go backups.Run(ctx)

	// Palworld has no offline-config work: bans go through RCON/REST to a
	// live server, and the ini has one writer.
	apiServer := api.New(st, cfg.JWTSecret, logger, docker, notifier, backups, files, nil)
	apiServer.SessionCookie = "palcon_session"
	apiServer.Provision = palworld.ProvisionProfile()
	apiServer.GameRoutes = palapi.Mount(apiServer, palReader)
	apiServer.Roster = &palworld.Roster{Reader: palReader}
	apiServer.AdvisorPrompt = palworld.AdvisorPrompt()
	apiServer.DocsFS = docs.FS
	apiServer.CookieSecure = cfg.CookieSecure
	// Optional: the pal advisor rides whichever model key is set, Anthropic
	// first when both are — a deterministic pick beats erroring on a config
	// most operators set by copying one line from .env.example.
	switch {
	case cfg.AnthropicAPIKey != "":
		apiServer.SetEnvAdvisor(advisor.NewClaude(cfg.AnthropicAPIKey, "", apiServer.AdvisorPrompt))
		logger.Info("pal advisor enabled", "provider", "anthropic", "source", "env")
	case cfg.GeminiAPIKey != "":
		gem, err := advisor.NewGemini(ctx, cfg.GeminiAPIKey, "", apiServer.AdvisorPrompt)
		if err != nil {
			return fmt.Errorf("configuring gemini advisor: %w", err)
		}
		apiServer.SetEnvAdvisor(gem)
		logger.Info("pal advisor enabled", "provider", "gemini", "source", "env")
	}
	// A key saved through the admin UI wins over the environment. Unusable
	// (rotated ENCRYPTION_KEY, say) is a warning, not a startup failure —
	// the admin can paste a fresh key without touching the host.
	if provider, err := apiServer.LoadStoredAdvisor(ctx); err != nil {
		logger.Warn("stored advisor key unusable", "error", err)
	} else if provider != "" {
		logger.Info("pal advisor enabled", "provider", provider, "source", "ui")
	}
	// Optional: single sign-on for a console behind a Cloudflare Tunnel.
	// Unset means the password form is the only way in — see
	// docs/cloudflare-access.md for what the verification does and does
	// not protect.
	if cfg.AccessEnabled() {
		verifier, err := cfaccess.New(cfg.AccessTeamDomain, cfg.AccessAUD)
		if err != nil {
			return fmt.Errorf("configuring cloudflare access: %w", err)
		}
		apiServer.Access = verifier
		apiServer.AccessAdminEmails = cfg.AccessAdminEmails
		logger.Info("cloudflare access sign-in enabled",
			"issuer", verifier.Issuer(), "adminEmails", len(cfg.AccessAdminEmails))
	}
	// Optional one-click provisioning, through Anvil and only Anvil —
	// this console holds no Docker rights of its own beyond power control
	// (the anvil module's README is the contract). Without ANVIL_URL the
	// Raise-a-server wizard is simply absent and servers are registered by
	// hand. The old PROVISIONER_URL agent mode is retired; see
	// docs/palcon-port-verification.md for the migration. A deployment
	// still carrying the retired names is told so at boot, above.
	if cfg.AnvilURL != "" {
		client, err := anvil.New(cfg.AnvilURL, cfg.AnvilToken)
		if err != nil {
			return fmt.Errorf("configuring anvil: %w", err)
		}
		apiServer.Provisioner = api.NewAnvilProvisioner(client, apiServer.Provision)
		logger.Info("provisioner enabled", "endpoint", cfg.AnvilURL, "via", "anvil")
	}
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           apiServer.Routes(distFS),
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
		err := httpServer.Shutdown(shutdownCtx)
		// The collector ends the sessions of whoever is still online on its
		// way out. Exiting without waiting strands those joins, and an
		// unclosed join reads as a session that never ended.
		select {
		case <-collectorDone:
		case <-shutdownCtx.Done():
			logger.Warn("collector did not finish closing open sessions")
		}
		return err
	case err := <-errCh:
		return err
	}
}
