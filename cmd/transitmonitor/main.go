// Package transitmonitor is the TransitMonitor binary entrypoint.
//
// Usage:
//
//	transitmonitor -selftest                   # in-process E2E self-test (mock stations)
//	transitmonitor -config config.yaml -once   # one scrape per station, print, exit
//	transitmonitor -config config.yaml         # serve dashboard + poll loop until Ctrl-C
//	transitmonitor -version
//
// Stations come from YAML (authoritative, in-memory) + DB-persisted web-added
// stations (when TRANSMONITOR_ENCRYPTION_KEY is set). The web UI can add/remove
// stations at runtime via /stations.
package main

import (
	"context"
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
	"transitmonitor/internal/domain"
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

	httpClient := &http.Client{Timeout: 30 * time.Second}
	encKey := deriveKeyFromEnv()

	// Stations = YAML (authoritative, creds in-memory) + DB-persisted (web-added).
	stations := cfg.Stations
	if encKey != nil {
		if dbSt, err := st.ListStationsDB(context.Background(), encKey); err == nil {
			for _, dbs := range dbSt {
				if !containsID(stations, dbs.ID) {
					stations = append(stations, dbs)
				}
			}
		}
	}

	adapters := make(map[string]adapter.Adapter, len(stations))
	for _, s := range stations {
		a, err := adapter.NewAdapter(s, httpClient)
		if err != nil {
			fatal(logger, "adapter for station %s: %v", s.ID, err)
		}
		adapters[s.ID] = a
	}
	notifier := buildNotifier(cfg)
	sched := scheduler.New(stations, adapters, st, cfg.Alerts.Rules, notifier)
	sched.SetLogger(logger)
	sched.SetEncKey(encKey)
	sched.SetClient(httpClient)
	sched.Prober = probe.NewProber(httpClient)

	_ = st.InsertAuditLog(context.Background(), "main", "startup", "",
		fmt.Sprintf("version=%s stations=%d enc=%v", version, len(stations), encKey != nil))

	if once {
		for _, s := range stations {
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
	dash := dashboard.New(stations, st, cfg.Dashboard.Token)
	dash.SetManager(sched) // enables web CRUD (add/remove stations at runtime)
	go func() {
		logger.Info("dashboard", "addr", dashAddr)
		if err := dash.ListenAndServe(dashAddr); err != nil && err != http.ErrServerClosed {
			logger.Error("dashboard", "err", err)
		}
	}()

	hupCh := make(chan os.Signal, 1)
	signal.Notify(hupCh, syscall.SIGHUP)
	go func() {
		for range hupCh {
			logger.Info("SIGHUP received, reloading config")
			if cfg2, err := config.Load(configPath); err == nil {
				for _, st := range cfg2.Stations {
					_ = sched.AddStation(st)
				}
				logger.Info("config reloaded", "stations", len(cfg2.Stations))
			} else {
				logger.Error("config reload failed", "err", err)
			}
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("TransitMonitor running", "stations", len(stations), "version", version)
	sched.Run(ctx)
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
	if cfg.Alerts.Lark.Webhook != "" {
		ns = append(ns, alert.NewLark(cfg.Alerts.Lark.Webhook, cfg.Alerts.Lark.Secret, http.DefaultClient))
	}
	if cfg.Alerts.Slack.Webhook != "" {
		ns = append(ns, &alert.SlackNotifier{WebhookURL: cfg.Alerts.Slack.Webhook})
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

func deriveKeyFromEnv() []byte {
	if key := os.Getenv("TRANSMONITOR_ENCRYPTION_KEY"); key != "" {
		return secrets.DeriveKey(key)
	}
	return nil
}

func containsID(sts []domain.Station, id string) bool {
	for _, s := range sts {
		if s.ID == id {
			return true
		}
	}
	return false
}

func fatal(logger *slog.Logger, format string, args ...any) {
	logger.Error("fatal", "err", fmt.Sprintf(format, args...))
	os.Exit(1)
}
