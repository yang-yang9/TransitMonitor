// Package transitmonitor is the TransitMonitor binary entrypoint.
//
// Usage:
//
//	transitmonitor -selftest                   # in-process E2E self-test (mock stations)
//	transitmonitor -config config.yaml -once   # one scrape per station, print, exit
//	transitmonitor -config config.yaml         # serve dashboard + poll loop until Ctrl-C
//	transitmonitor -version
//
// Env overrides (win over flags/config):
//
//	TRANSMONITOR_CONFIG            config file path
//	TRANSMONITOR_DB_PATH           sqlite db path
//	TRANSMONITOR_DASHBOARD_ADDR    listen address
//	TRANSMONITOR_DASHBOARD_TOKEN   bearer token for non-localhost dashboard access
//	TRANSMONITOR_ENCRYPTION_KEY    passphrase for at-rest credential encryption
//	TRANSMONITOR_LOG_LEVEL         debug | info | warn | error (default info)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"transitmonitor/internal/adapter"
	"transitmonitor/internal/alert"
	"transitmonitor/internal/config"
	"transitmonitor/internal/dashboard"
	"transitmonitor/internal/e2e"
	"transitmonitor/internal/probe"
	"transitmonitor/internal/scheduler"
	"transitmonitor/internal/secrets"
	"transitmonitor/internal/store"
)

// version is overridable at build time via -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	logger := newLogger()
	var (
		configPath string
		once       bool
		selftest   bool
		addr       string
		showVer    bool
	)
	flag.StringVar(&configPath, "config", envOr("TRANSMONITOR_CONFIG", "config.yaml"), "config file path")
	flag.BoolVar(&once, "once", false, "poll each station once and exit")
	flag.BoolVar(&selftest, "selftest", false, "run in-process end-to-end self-test (mock stations) and exit")
	flag.StringVar(&addr, "addr", "", "dashboard listen address (overrides config/env)")
	flag.BoolVar(&showVer, "version", false, "print version and exit")
	flag.Parse()

	if showVer {
		fmt.Println("transitmonitor", version)
		return
	}

	if selftest {
		if err := e2e.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "self-test FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("self-test PASSED")
		return
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fatal(logger, "load config: %v", err)
	}
	// Apply env overrides.
	if v := os.Getenv("TRANSMONITOR_DB_PATH"); v != "" {
		cfg.DB.Path = v
	}
	if v := os.Getenv("TRANSMONITOR_DASHBOARD_TOKEN"); v != "" {
		cfg.Dashboard.Token = v
	}

	st, err := store.Open(cfg.DB.Path)
	if err != nil {
		fatal(logger, "open store: %v", err)
	}
	defer st.Close()

	adapters := make(map[string]adapter.Adapter, len(cfg.Stations))
	for _, s := range cfg.Stations {
		a, err := adapter.NewAdapter(s, http.DefaultClient)
		if err != nil {
			fatal(logger, "adapter for station %s: %v", s.ID, err)
		}
		adapters[s.ID] = a
	}
	notifier := buildNotifier(cfg)
	sched := scheduler.New(cfg.Stations, adapters, st, cfg.Alerts.Rules, notifier)
	sched.SetLogger(logger)
	sched.Prober = probe.NewProber(http.DefaultClient)

	// Persist credentials at-rest (encrypted) when a passphrase is configured.
	if key := os.Getenv("TRANSMONITOR_ENCRYPTION_KEY"); key != "" {
		encKey := secrets.DeriveKey(key)
		for _, s := range cfg.Stations {
			b, _ := json.Marshal(s.Auth)
			if err := st.SetCredentials(context.Background(), s.ID, encKey, string(b)); err != nil {
				logger.Error("persist credentials", "station", s.ID, "err", err)
			}
		}
		_ = st.InsertAuditLog(context.Background(), "main", "credentials.persisted", "", fmt.Sprintf("stations=%d", len(cfg.Stations)))
		logger.Info("persisted encrypted credentials", "count", len(cfg.Stations))
	}
	_ = st.InsertAuditLog(context.Background(), "main", "startup", "", fmt.Sprintf("version=%s stations=%d", version, len(cfg.Stations)))

	if once {
		for _, s := range cfg.Stations {
			if err := sched.PollOnce(context.Background(), s.ID); err != nil {
				logger.Error("once", "station", s.ID, "err", err)
				continue
			}
			latest, _ := st.LatestRatioObservations(context.Background(), s.ID)
			fmt.Printf("[once] station %s polled OK: %d models\n", s.ID, len(latest))
		}
		return
	}

	dashAddr := cfg.Dashboard.Addr
	if v := os.Getenv("TRANSMONITOR_DASHBOARD_ADDR"); v != "" {
		dashAddr = v
	}
	if addr != "" {
		dashAddr = addr
	}
	dash := dashboard.New(cfg.Stations, st, cfg.Dashboard.Token)
	go func() {
		logger.Info("dashboard", "addr", dashAddr)
		if err := dash.ListenAndServe(dashAddr); err != nil && err != http.ErrServerClosed {
			logger.Error("dashboard", "err", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("TransitMonitor running", "stations", len(cfg.Stations), "version", version)
	sched.Run(ctx) // blocks until signal

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = dash.Shutdown(shutCtx)
	logger.Info("shutdown complete")
}

func buildNotifier(cfg *config.File) alert.Notifier {
	var ns []alert.Notifier
	if cfg.Alerts.DingTalk.Webhook != "" {
		ns = append(ns, alert.NewDingTalk(cfg.Alerts.DingTalk.Webhook, cfg.Alerts.DingTalk.Secret, http.DefaultClient))
	}
	if cfg.Alerts.Webhook.URL != "" {
		ns = append(ns, &alert.WebhookNotifier{URL: cfg.Alerts.Webhook.URL})
	}
	if len(ns) == 0 {
		return nil
	}
	return &alert.Dispatcher{Notifiers: ns}
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	switch os.Getenv("TRANSMONITOR_LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func envOr(key, dflt string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return dflt
}

func fatal(logger *slog.Logger, format string, args ...any) {
	logger.Error("fatal", "err", fmt.Sprintf(format, args...))
	os.Exit(1)
}
