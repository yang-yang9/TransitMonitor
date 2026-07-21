// Package scheduler drives the per-station scrape→normalize→store→diff→alert
// loop, the real-cost probe, and a daily retention/downsample job. It owns no
// domain math; it composes adapter + store + changedet + alert + probe.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"transitmonitor/internal/adapter"
	"transitmonitor/internal/alert"
	"transitmonitor/internal/changedet"
	"transitmonitor/internal/domain"
	"transitmonitor/internal/probe"
	"transitmonitor/internal/store"
)

// Scheduler runs the monitoring loop for a set of stations.
type Scheduler struct {
	Stations []domain.Station
	Adapters map[string]adapter.Adapter
	Store    *store.Store
	Rules    []alert.Rule
	Notifier alert.Notifier
	Prober   *probe.Prober
	DiffCfg  changedet.Config
	Now      func() time.Time
	Logger   *slog.Logger

	snapshotRetentionDays int // default 7
	obsRetentionDays      int // default 30
}

// New constructs a scheduler with defaults.
func New(stations []domain.Station, adapters map[string]adapter.Adapter, st *store.Store, rules []alert.Rule, n alert.Notifier) *Scheduler {
	return &Scheduler{
		Stations: stations, Adapters: adapters, Store: st, Rules: rules, Notifier: n,
		DiffCfg: changedet.DefaultConfig(), Now: time.Now, Logger: slog.Default(),
		snapshotRetentionDays: 7, obsRetentionDays: 30,
	}
}

// SetClock injects a clock (for tests).
func (s *Scheduler) SetClock(f func() time.Time) { s.Now = f }

// SetLogger injects a logger.
func (s *Scheduler) SetLogger(l *slog.Logger) { s.Logger = l }

// logger returns the configured logger or the default (nil-safe, so a Scheduler
// constructed without New() — e.g. in tests — does not panic on logging).
func (s *Scheduler) logger() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}

// SetRetention overrides the retention windows (days).
func (s *Scheduler) SetRetention(snapshotDays, obsDays int) {
	s.snapshotRetentionDays = snapshotDays
	s.obsRetentionDays = obsDays
}

// PollOnce runs one scrape→store→diff→alert cycle for a station.
func (s *Scheduler) PollOnce(ctx context.Context, stationID string) error {
	a, ok := s.Adapters[stationID]
	if !ok {
		return fmt.Errorf("unknown station %s", stationID)
	}
	prev, err := s.Store.LatestRatioObservations(ctx, stationID)
	if err != nil {
		return err
	}
	caps, err := a.ProbeCapabilities(ctx)
	if err != nil {
		return fmt.Errorf("probe capabilities: %w", err)
	}
	snap, obs, err := a.FetchRatios(ctx, caps)
	if err != nil {
		return fmt.Errorf("fetch ratios: %w", err)
	}
	t := s.Now()
	for i := range obs {
		obs[i].StationID = stationID
		obs[i].ObservedAt = t
	}
	snap.ObservedAt = t
	if err := s.Store.InsertSnapshot(ctx, snap); err != nil {
		return err
	}
	if err := s.Store.InsertRatioObservations(ctx, obs); err != nil {
		return err
	}
	events := changedet.Diff(prev, obs, s.DiffCfg)
	if len(events) > 0 {
		if err := s.Store.InsertChangeEvents(ctx, events); err != nil {
			return err
		}
	}
	if s.Notifier != nil {
		for _, ev := range alert.Evaluate(s.Rules, events, nil) {
			_ = s.Notifier.Send(ctx, ev)
		}
	}

	// Real-cost probe (if enabled for this station).
	if s.Prober != nil {
		for _, x := range s.Stations {
			if x.ID != stationID || !x.Probe.Enabled {
				continue
			}
			s.runStationProbe(ctx, x, obs)
			break
		}
	}
	return nil
}

func (s *Scheduler) runStationProbe(ctx context.Context, st domain.Station, obs []domain.RatioObservation) {
	pres, perr := s.Prober.Run(ctx, st, obs)
	if perr != nil {
		s.logger().Error("probe", "station", st.ID, "err", perr)
		return
	}
	if pres.Error == "deduped" {
		return // within the dedupe window — not a real probe
	}
	if err := s.Store.InsertProbeResult(ctx, pres); err != nil {
		s.logger().Error("probe store", "station", st.ID, "err", err)
	}
	_ = s.Store.InsertAuditLog(ctx, "probe", "probe.run", st.ID,
		fmt.Sprintf("model=%s tokens=%d/%d markup=%.2f%% cost_usd=%.6f declared_unavailable=%v error=%s",
			pres.Model, pres.TokensIn, pres.TokensOut, pres.MarkupPct, pres.CostUSD, pres.DeclaredUnavailable, pres.Error))
	if s.Notifier != nil {
		for _, ev := range alert.Evaluate(s.Rules, nil, []domain.ProbeResult{pres}) {
			_ = s.Notifier.Send(ctx, ev)
		}
	}
}

// Run polls each enabled station on its interval and runs a daily retention
// job until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, st := range s.Stations {
		if !st.Enabled {
			continue
		}
		wg.Add(1)
		go func(st domain.Station) {
			defer wg.Done()
			interval := time.Duration(st.PollInterval)
			if interval < 2*time.Minute {
				interval = 2 * time.Minute // respect station-side caches
			}
			s.pollLoop(ctx, st, interval)
		}(st)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.retentionLoop(ctx)
	}()
	wg.Wait()
}

func (s *Scheduler) pollLoop(ctx context.Context, st domain.Station, interval time.Duration) {
	if err := s.PollOnce(ctx, st.ID); err != nil && ctx.Err() == nil {
		s.logger().Error("poll", "station", st.ID, "err", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.PollOnce(ctx, st.ID); err != nil && ctx.Err() == nil {
				s.logger().Error("poll", "station", st.ID, "err", err)
			}
		}
	}
}

func (s *Scheduler) retentionLoop(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	s.runRetention(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runRetention(ctx)
		}
	}
}

func (s *Scheduler) runRetention(ctx context.Context) {
	now := s.Now()
	if err := s.Store.DownsampleAndRetain(ctx, now, s.snapshotRetentionDays, s.obsRetentionDays); err != nil {
		s.logger().Error("retention", "err", err)
		return
	}
	s.logger().Info("retention done", "snap_days", s.snapshotRetentionDays, "obs_days", s.obsRetentionDays)
}
