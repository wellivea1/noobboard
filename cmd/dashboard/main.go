package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/wellivea1/noobboard/internal/adapters/docker"
	"github.com/wellivea1/noobboard/internal/adapters/probes"
	"github.com/wellivea1/noobboard/internal/adapters/unifi"
	"github.com/wellivea1/noobboard/internal/adapters/unraid"
	"github.com/wellivea1/noobboard/internal/audit"
	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/db"
	"github.com/wellivea1/noobboard/internal/diagnostics"
	"github.com/wellivea1/noobboard/internal/llm"
	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/notifications"
	"github.com/wellivea1/noobboard/internal/privacy"
	"github.com/wellivea1/noobboard/internal/server"
	"github.com/wellivea1/noobboard/internal/service"
	"github.com/wellivea1/noobboard/internal/users"
)

var version = "0.1.0-dev"

func main() {
	if err := run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 {
		return serve(args)
	}

	switch args[1] {
	case "serve":
		return serve(args[1:])
	case "check-config":
		cfg, err := loadConfig(args[1:])
		if err != nil {
			return err
		}
		return cfg.Validate()
	case "install-service":
		return service.Install(service.OptionsFromVersion(version))
	case "uninstall-service":
		return service.Uninstall(service.OptionsFromVersion(version))
	case "start-service":
		return service.Start(service.OptionsFromVersion(version))
	case "stop-service":
		return service.Stop(service.OptionsFromVersion(version))
	case "run-once":
		return runOnce(args[1:])
	case "version":
		fmt.Printf("noobboard %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[1])
	}
}

func loadConfig(args []string) (config.Config, error) {
	fs := flag.NewFlagSet("noobboard", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to config file")
	if err := fs.Parse(args[1:]); err != nil {
		return config.Config{}, err
	}
	return config.Load(*configPath)
}

func serve(args []string) error {
	cfg, err := loadConfig(append([]string{"serve"}, args[1:]...))
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	// When the process is launched by the Windows Service Control Manager, run as a real
	// service (SCM handshake + stop handling). Otherwise fall through to foreground mode.
	handled, err := service.RunService(service.OptionsFromVersion(version).Name, func(ctx context.Context) error {
		return runServers(ctx, cfg)
	})
	if handled {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runServers(ctx, cfg)
}

// runServers starts the admin and compact HTTP servers and serves until ctx is cancelled
// (by an OS signal in foreground mode, or by the service stop handler under SCM), then
// shuts down gracefully.
func runServers(ctx context.Context, cfg config.Config) error {
	app, err := buildApp(cfg)
	if err != nil {
		return err
	}

	adminSrv := &http.Server{
		Addr:              cfg.Server.Address(),
		Handler:           app.AdminRouter(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	compactSrv := &http.Server{
		Addr:              cfg.Server.CompactAddress(),
		Handler:           app.CompactRouter(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)
	startServer := func(name string, srv *http.Server) {
		go func() {
			slog.Info("dashboard listening", "site", name, "address", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("%s server failed: %w", name, err)
				cancel()
			}
		}()
	}
	startServer("admin", adminSrv)
	startServer("compact", compactSrv)
	go app.RunPoller(ctx, cfg.Polling.Interval)

	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-errCh:
		serveErr = err
		slog.Error("server failed", "error", err)
		cancel()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	app.Flush()

	var shutdownErr error
	for _, srv := range []*http.Server{adminSrv, compactSrv} {
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}
	return errors.Join(serveErr, shutdownErr)
}

func runOnce(args []string) error {
	cfg, err := loadConfig(append([]string{"run-once"}, args[1:]...))
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	app, err := buildApp(cfg)
	if err != nil {
		return err
	}
	defer app.Flush()
	snapshot, err := app.Snapshot(context.Background(), users.AdminRole)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s (%d apps, %d incidents)\n", snapshot.GeneratedAt.Format(time.RFC3339), snapshot.OverallStatus, len(snapshot.Apps), len(snapshot.Incidents))
	return nil
}

func buildApp(cfg config.Config) (*server.App, error) {
	redactor := privacy.NewRedactor(cfg.Privacy)
	store, err := db.OpenFileStore(cfg.Database.Path)
	if err != nil {
		return nil, err
	}
	metricStore, err := db.OpenFileMetricStore(db.MetricsPathForDatabase(cfg.Database.Path))
	if err != nil {
		return nil, err
	}
	historyStore, err := db.OpenFileHistoryStore(db.HistoryPathForDatabase(cfg.Database.Path), cfg.Retention.MaxStatusEventsPerSubject)
	if err != nil {
		return nil, err
	}

	registry := users.NewRegistry(store, cfg.Auth)
	auditor := audit.New(store, redactor)
	notifier := notifications.NewManager(store, notifications.NewMockBackend(), cfg.Notifications, auditor)

	collectors := newCollectors(cfg)
	if cfg.Integrations.Mode == "live" || cfg.Integrations.Mode == "mixed" {
		if cfg.Integrations.UnraidBaseURL != "" && cfg.Integrations.UnraidAPIKey != "" {
			collectors.Unraid = unraid.NewLiveClient(cfg.Integrations.UnraidBaseURL, cfg.Integrations.UnraidAPIKey)
			collectors.Docker = docker.NewUnraidLiveClient(cfg.Integrations.UnraidBaseURL, cfg.Integrations.UnraidAPIKey)
		}
		if cfg.Integrations.UnraidSSHFallback {
			sshDocker := docker.NewSSHClient(docker.SSHOptions{
				Host:    cfg.Integrations.UnraidSSHHost,
				Port:    cfg.Integrations.UnraidSSHPort,
				User:    cfg.Integrations.UnraidSSHUser,
				KeyFile: cfg.Integrations.UnraidSSHKeyFile,
				Command: cfg.Integrations.UnraidSSHCommand,
			})
			if collectors.Docker != nil {
				collectors.Docker = docker.NewLargestListClient(collectors.Docker, sshDocker)
			} else {
				collectors.Docker = sshDocker
			}
		}
		if cfg.Integrations.UniFiBaseURL != "" && cfg.Integrations.UniFiAPIKey != "" {
			collectors.UniFi = unifi.NewLiveClient(cfg.Integrations.UniFiBaseURL, cfg.Integrations.UniFiAPIKey, cfg.Integrations.UniFiSiteID, cfg.Integrations.UniFiInsecureTLS)
		}
	}

	rules := diagnostics.NewRuleEngine()
	llmClient := llm.NewClient(cfg.LLM, redactor)
	return server.New(server.Dependencies{
		Config:        cfg,
		Collectors:    collectors,
		Store:         store,
		History:       historyStore,
		Metrics:       metricStore,
		Users:         registry,
		Audit:         auditor,
		Notifications: notifier,
		Redactor:      redactor,
		Diagnostics:   rules,
		LLM:           llmClient,
		Version:       version,
	})
}

func newCollectors(cfg config.Config) server.Collectors {
	if cfg.Integrations.Mode == "fixture" || cfg.Integrations.Mode == "mixed" {
		return server.Collectors{
			Unraid: unraid.NewFixtureClient(cfg.FixtureDir, cfg.FixtureScenario),
			Docker: docker.NewFixtureClient(cfg.FixtureDir, cfg.FixtureScenario),
			UniFi:  unifi.NewFixtureClient(cfg.FixtureDir, cfg.FixtureScenario),
			Probes: probes.NewFixtureClient(cfg.FixtureDir, cfg.FixtureScenario),
		}
	}
	return server.Collectors{
		Unraid: unavailableUnraidClient("unraid live credentials are not configured"),
		Docker: unavailableDockerClient("unraid docker live credentials are not configured"),
		UniFi:  unavailableUniFiClient("unifi live credentials are not configured"),
		Probes: probes.NewLiveClient(liveProbeConfig(cfg)),
	}
}

func liveProbeConfig(cfg config.Config) probes.LiveConfig {
	return probes.LiveConfig{
		InternetURL:  cfg.Integrations.InternetProbeURL,
		DNSHost:      cfg.Integrations.DNSProbeHost,
		RouterTarget: firstNonEmpty(cfg.Integrations.RouterProbeTarget, cfg.Integrations.UniFiBaseURL),
		NASTarget:    firstNonEmpty(cfg.Integrations.NASProbeTarget, cfg.Integrations.UnraidBaseURL),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type unavailableUnraidClient string

func (c unavailableUnraidClient) Status(context.Context) (models.InfrastructureStatus, []models.LogLine, error) {
	return models.InfrastructureStatus{}, nil, errors.New(string(c))
}

func (c unavailableUnraidClient) StartArray(context.Context) (unraid.ArrayControlResult, error) {
	return unraid.ArrayControlResult{}, errors.New(string(c))
}

type unavailableDockerClient string

func (c unavailableDockerClient) Apps(context.Context) ([]models.AppStatus, error) {
	return nil, errors.New(string(c))
}

func (c unavailableDockerClient) ControlContainer(context.Context, models.AppStatus, docker.ContainerAction) (docker.ControlResult, error) {
	return docker.ControlResult{}, errors.New(string(c))
}

func (c unavailableDockerClient) Logs(context.Context, models.AppStatus, docker.LogOptions) ([]models.LogLine, error) {
	return nil, errors.New(string(c))
}

type unavailableUniFiClient string

func (c unavailableUniFiClient) Status(context.Context) (models.InfrastructureStatus, error) {
	return models.InfrastructureStatus{}, errors.New(string(c))
}

func (c unavailableUniFiClient) RestartableDevices(context.Context) ([]unifi.RestartableDevice, error) {
	return nil, errors.New(string(c))
}

func (c unavailableUniFiClient) RestartDevice(context.Context, string) (unifi.DeviceControlResult, error) {
	return unifi.DeviceControlResult{}, errors.New(string(c))
}

func (c unavailableUniFiClient) DeviceOnline(context.Context, string) (bool, error) {
	return false, errors.New(string(c))
}

type unavailableProbeClient string

func (c unavailableProbeClient) Status(context.Context) (models.InfrastructureStatus, error) {
	return models.InfrastructureStatus{}, errors.New(string(c))
}
