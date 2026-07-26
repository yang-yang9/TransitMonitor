// Package transitmonitor is the TransitMonitor binary entrypoint.
//
// Usage:
//
//	transitmonitor -selftest                   # in-process E2E self-test (mock stations)
//	transitmonitor -config config.yaml -once   # one scrape per station, print, exit
//	transitmonitor -config config.yaml         # serve dashboard + poll loop until Ctrl-C
//	transitmonitor -rotate-key -old-key K -new-key K2  # re-encrypt stored creds to a new key, then exit
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
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // embed the IANA tzdb so time.LoadLocation works in slim containers (Alpine has no zoneinfo)

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
	"transitmonitor/internal/updater"
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
		rotateKey  bool
		oldKey     string
		newKey     string
	)
	flag.StringVar(&configPath, "config", envOr("TRANSMONITOR_CONFIG", "config.yaml"), "config file path")
	flag.BoolVar(&once, "once", false, "poll each station once and exit")
	flag.BoolVar(&selftest, "selftest", false, "run in-process end-to-end self-test (mock stations) and exit")
	flag.StringVar(&addr, "addr", "", "dashboard listen address (overrides config/env)")
	flag.BoolVar(&showVer, "version", false, "print version and exit")
	flag.BoolVar(&rotateKey, "rotate-key", false, "re-encrypt all stored credentials from -old-key to -new-key, then exit")
	flag.StringVar(&oldKey, "old-key", "", "current encryption key (for -rotate-key)")
	flag.StringVar(&newKey, "new-key", "", "new encryption key (for -rotate-key; defaults to TRANSMONITOR_ENCRYPTION_KEY)")
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
	if v := os.Getenv("TRANSMONITOR_DASHBOARD_PASSWORD"); v != "" {
		cfg.Dashboard.Password = v
	}
	applyTimezone(logger, cfg.Timezone)

	st, err := store.Open(cfg.DB.Path)
	if err != nil {
		fatal(logger, "open store: %v", err)
	}
	defer st.Close()

	// Key rotation: re-encrypt every stored credential from oldKey → newKey,
	// then exit. Used when rotating TRANSMONITOR_ENCRYPTION_KEY without losing
	// existing credentials. newKey defaults to TRANSMONITOR_ENCRYPTION_KEY.
	if rotateKey {
		if oldKey == "" {
			fatal(logger, "-rotate-key requires -old-key (the current TRANSMONITOR_ENCRYPTION_KEY)")
		}
		nk := newKey
		if nk == "" {
			nk = os.Getenv("TRANSMONITOR_ENCRYPTION_KEY")
		}
		if nk == "" {
			fatal(logger, "-rotate-key requires -new-key (or set TRANSMONITOR_ENCRYPTION_KEY)")
		}
		res, err := st.RotateKey(context.Background(), secrets.DeriveKey(oldKey), secrets.DeriveKey(nk))
		if err != nil {
			fatal(logger, "rotate key: %v", err)
		}
		logger.Info("key rotation complete",
			"stations_rotated", res.StationsRotated, "notifiers_rotated", res.NotifiersRotated,
			"failed_stations", res.FailedStationIDs, "failed_notifiers", res.FailedNotifierIDs)
		fmt.Printf("rotation complete: %d stations, %d notifiers re-encrypted.\n", res.StationsRotated, res.NotifiersRotated)
		if len(res.FailedStationIDs) > 0 || len(res.FailedNotifierIDs) > 0 {
			fmt.Printf("WARNING: %d station(s) and %d notifier(s) could not be decrypted with -old-key (wrong key or corruption) and were skipped — re-enter those credentials via the web UI.\n",
				len(res.FailedStationIDs), len(res.FailedNotifierIDs))
			fmt.Println("  failed stations:", strings.Join(res.FailedStationIDs, ", "))
			fmt.Println("  failed notifiers:", strings.Join(res.FailedNotifierIDs, ", "))
		}
		fmt.Println("\nRestart TransitMonitor with TRANSMONITOR_ENCRYPTION_KEY=<new key> to load the re-encrypted credentials.")
		return
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	encKey := deriveKeyFromEnv()

	// Stations = YAML (authoritative, creds in-memory) + DB-persisted (web-added).
	stations := cfg.Stations
	decryptFailures := 0
	if encKey != nil {
		dbSt, fails, err := st.ListStationsDB(context.Background(), encKey)
		if err == nil {
			for _, dbs := range dbSt {
				if !containsID(stations, dbs.ID) {
					dbs.Enabled = true // DB-loaded stations default to enabled
					stations = append(stations, dbs)
				}
			}
			// A decrypt failure means the running TRANSMONITOR_ENCRYPTION_KEY does
			// not match the one used when the station was added. The station still
			// loads (empty Auth) so it keeps polling, but surface the real cause
			// loudly — otherwise the operator only sees a misleading "no api_key"
			// refusal downstream and chases the wrong fix.
			for _, f := range fails {
				decryptFailures++
				logger.Error("station credentials could not be decrypted; loading with empty auth",
					"station", f.StationID, "reason", f.Reason, "err", f.Err,
					"fix", "re-enter the station's credentials via the web UI, or restart with the same TRANSMONITOR_ENCRYPTION_KEY used when the station was added")
				_ = st.InsertAuditLog(context.Background(), "main", "creds.decrypt_failed", f.StationID,
					fmt.Sprintf("reason=%s err=%v", f.Reason, f.Err))
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
	sched.SetBaseNotifierConfig(alertNotifierConfig(cfg.Alerts))
	if err := sched.ReloadNotifiers(context.Background()); err != nil {
		logger.Warn("notifier reload", "err", err)
	}
	sched.SetBaseRules(cfg.Alerts.Rules)
	if err := sched.LoadRules(context.Background()); err != nil {
		logger.Warn("load alert rules", "err", err)
	}
	sched.Prober = probe.NewProber(httpClient)

	_ = st.InsertAuditLog(context.Background(), "main", "startup", "",
		fmt.Sprintf("version=%s stations=%d enc=%v decrypt_fails=%d", version, len(stations), encKey != nil, decryptFailures))

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
	dash := dashboard.New(stations, st, cfg.Dashboard.Token, cfg.Dashboard.Password)
	dash.SetManager(sched) // enables web CRUD (add/remove stations at runtime)
	dash.SetEncKey(encKey) // enables /settings notifier-secret persistence
	dash.SetVersion(version)
	// In-panel update / rollback (sub2api-style). Release builds only — a
	// `make build` (version 0.1.0-dev) still checks for updates but should not
	// be self-updated into. The pre-restart hook flushes SQLite WAL and stops
	// the HTTP server before syscall.Exec replaces the process image.
	upd, err := updater.New(version, "", "", os.Getenv("TRANSMONITOR_UPDATE_GITHUB_TOKEN"))
	if err != nil {
		logger.Warn("updater init failed; in-panel update disabled", "err", err)
	} else {
		upd.SetPreRestart(func() error {
			shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = dash.Shutdown(shutCtx)
			return st.Close()
		})
		dash.SetUpdater(upd)
	}
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
				applyTimezone(logger, cfg2.Timezone)
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
	return alert.BuildDispatcher(alertNotifierConfig(cfg.Alerts), http.DefaultClient)
}

// alertNotifierConfig converts the YAML AlertsConfig (anonymous nested structs)
// into the JSON-serializable alert.NotifierConfig used by BuildDispatcher and
// the /settings page.
func alertNotifierConfig(a config.AlertsConfig) alert.NotifierConfig {
	var nc alert.NotifierConfig
	nc.DingTalk.Webhook = a.DingTalk.Webhook
	nc.DingTalk.Secret = a.DingTalk.Secret
	nc.Webhook.URL = a.Webhook.URL
	nc.Lark.Webhook = a.Lark.Webhook
	nc.Lark.Secret = a.Lark.Secret
	nc.Slack.Webhook = a.Slack.Webhook
	nc.QQ.AppID = a.QQ.AppID
	nc.QQ.AppSecret = a.QQ.AppSecret
	nc.QQ.GroupOpenID = a.QQ.GroupOpenID
	return nc
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

// applyTimezone sets the process-wide default location (time.Local) so that all
// time.Now()/Format calls, log timestamps, and JSON-marshalled time fields render
// in the operator's wall-clock (e.g. Beijing time). Falls back to UTC with a
// warning if the name is invalid (time/tzdata is embedded, so common IANA names
// like Asia/Shanghai always resolve, even in slim containers).
func applyTimezone(logger *slog.Logger, name string) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		logger.Error("timezone load failed; falling back to UTC", "timezone", name, "err", err)
		time.Local = time.UTC
		return
	}
	time.Local = loc
	logger.Info("timezone applied", "timezone", name, "offset", time.Now().Format("-07:00"))
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
