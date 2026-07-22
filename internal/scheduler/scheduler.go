// Package scheduler drives the per-station scrape→normalize→store→diff→alert
// loop, the real-cost probe, a daily retention job, and runtime station
// add/remove (StationManager). It owns no domain math; it composes adapter +
// store + changedet + alert + probe.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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
	stationList []domain.Station
	Adapters    map[string]adapter.Adapter
	Store       *store.Store
	Rules       []alert.Rule
	Notifier    alert.Notifier
	Prober      *probe.Prober
	DiffCfg     changedet.Config
	Now         func() time.Time
	Logger      *slog.Logger
	EncKey      []byte       // for at-rest credential persistence on AddStation
	Client      *http.Client // for building adapters at runtime

	mu                    sync.Mutex
	runCtx                context.Context
	cancels               map[string]context.CancelFunc
	snapshotRetentionDays int
	obsRetentionDays      int
	cooldown              time.Duration
	lastAlert             map[string]time.Time
	alertMu               sync.Mutex
}

// New constructs a scheduler with defaults.
func New(stations []domain.Station, adapters map[string]adapter.Adapter, st *store.Store, rules []alert.Rule, n alert.Notifier) *Scheduler {
	return &Scheduler{
		stationList: stations, Adapters: adapters, Store: st, Rules: rules, Notifier: n,
		DiffCfg: changedet.DefaultConfig(), Now: time.Now, Logger: slog.Default(),
		cancels:               map[string]context.CancelFunc{},
		cooldown:              30 * time.Minute,
		lastAlert:             map[string]time.Time{},
		snapshotRetentionDays: 7, obsRetentionDays: 30,
	}
}

// SetClock injects a clock (for tests).
func (s *Scheduler) SetClock(f func() time.Time) { s.Now = f }

// SetLogger injects a logger.
func (s *Scheduler) SetLogger(l *slog.Logger) { s.Logger = l }

// SetRetention overrides the retention windows (days).
func (s *Scheduler) SetRetention(snapshotDays, obsDays int) {
	s.snapshotRetentionDays = snapshotDays
	s.obsRetentionDays = obsDays
}

// SetEncKey sets the at-rest encryption key (for persisting added stations' creds).
func (s *Scheduler) SetEncKey(k []byte) { s.EncKey = k }

// SetClient sets the HTTP client used to build adapters at runtime.
func (s *Scheduler) SetClient(c *http.Client) { s.Client = c }

// SetCooldown overrides the alert cooldown window (default 30m).
func (s *Scheduler) SetCooldown(d time.Duration) { s.cooldown = d }

// logger returns the configured logger or the default (nil-safe).
func (s *Scheduler) logger() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}

// shouldFire checks if an alert should fire (cooldown dedup).
func (s *Scheduler) shouldFire(ev alert.AlertEvent) bool {
	if s.cooldown <= 0 {
		return true
	}
	key := ev.Rule + "|" + ev.StationID + "|" + ev.Model
	s.alertMu.Lock()
	defer s.alertMu.Unlock()
	if last, ok := s.lastAlert[key]; ok && s.Now().Sub(last) < s.cooldown {
		return false
	}
	s.lastAlert[key] = s.Now()
	return true
}

// Stations returns a snapshot of the current station list (thread-safe).
func (s *Scheduler) Stations() []domain.Station {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Station, len(s.stationList))
	copy(out, s.stationList)
	return out
}

// AddStation persists + builds an adapter + starts polling. Upserts by ID.
// Implements dashboard.StationManager.
func (s *Scheduler) AddStation(st domain.Station) error {
	if s.runCtx == nil {
		return fmt.Errorf("scheduler not running")
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	a, err := adapter.NewAdapter(st, client)
	if err != nil {
		return err
	}
	if s.EncKey != nil && s.Store != nil {
		_ = s.Store.UpsertStation(s.runCtx, st, s.EncKey)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.cancels[st.ID]; ok {
		cancel() // stop a previous poller for the same id
	}
	replaced := false
	for i, x := range s.stationList {
		if x.ID == st.ID {
			s.stationList[i] = st
			replaced = true
			break
		}
	}
	if !replaced {
		s.stationList = append(s.stationList, st)
	}
	s.Adapters[st.ID] = a
	if st.Enabled {
		s.startStationLocked(s.runCtx, st)
	}
	return nil
}

// RemoveStation stops the poller + removes the station + deletes it from the store.
// Implements dashboard.StationManager.
func (s *Scheduler) RemoveStation(id string) error {
	if s.runCtx == nil {
		return fmt.Errorf("scheduler not running")
	}
	s.mu.Lock()
	if cancel, ok := s.cancels[id]; ok {
		cancel()
		delete(s.cancels, id)
	}
	for i, x := range s.stationList {
		if x.ID == id {
			s.stationList = append(s.stationList[:i], s.stationList[i+1:]...)
			break
		}
	}
	delete(s.Adapters, id)
	s.mu.Unlock()
	if s.EncKey != nil && s.Store != nil {
		_ = s.Store.DeleteStation(s.runCtx, id)
	}
	return nil
}

// PollOnce runs one scrape→store→diff→alert cycle for a station.
func (s *Scheduler) PollOnce(ctx context.Context, stationID string) error {
	s.mu.Lock()
	a := s.Adapters[stationID]
	var stn domain.Station
	found := false
	for _, x := range s.stationList {
		if x.ID == stationID {
			stn = x
			found = true
			break
		}
	}
	s.mu.Unlock()
	if a == nil {
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
			if !s.shouldFire(ev) {
				continue
			}
			sendErr := s.Notifier.Send(ctx, ev)
			payload, _ := json.Marshal(ev.Payload)
			_ = s.Store.InsertAlertEvent(ctx, ev.Rule, ev.StationID, ev.Model, string(payload), sendErr == nil, errStr(sendErr))
		}
	}
	if s.Prober != nil && found && stn.Probe.Enabled {
		s.runStationProbe(ctx, stn, obs)
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
		return
	}
	if err := s.Store.InsertProbeResult(ctx, pres); err != nil {
		s.logger().Error("probe store", "station", st.ID, "err", err)
	}
	_ = s.Store.InsertAuditLog(ctx, "probe", "probe.run", st.ID,
		fmt.Sprintf("model=%s tokens=%d/%d markup=%.2f%% cost_usd=%.6f declared_unavailable=%v error=%s",
			pres.Model, pres.TokensIn, pres.TokensOut, pres.MarkupPct, pres.CostUSD, pres.DeclaredUnavailable, pres.Error))
	if s.Notifier != nil {
		for _, ev := range alert.Evaluate(s.Rules, nil, []domain.ProbeResult{pres}) {
			if !s.shouldFire(ev) {
				continue
			}
			sendErr := s.Notifier.Send(ctx, ev)
			payload, _ := json.Marshal(ev.Payload)
			_ = s.Store.InsertAlertEvent(ctx, ev.Rule, ev.StationID, ev.Model, string(payload), sendErr == nil, errStr(sendErr))
		}
	}
}

// Run polls each enabled station and runs a daily retention job until ctx cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	s.mu.Lock()
	s.runCtx = ctx
	for _, st := range s.stationList {
		if !st.Enabled {
			continue
		}
		s.startStationLocked(ctx, st)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.retentionLoop(ctx)
	}()
	<-ctx.Done()
	wg.Wait()
	s.logger().Info("scheduler stopped")
}

func (s *Scheduler) startStationLocked(parent context.Context, st domain.Station) {
	ctx, cancel := context.WithCancel(parent)
	s.cancels[st.ID] = cancel
	interval := time.Duration(st.PollInterval)
	if interval < 2*time.Minute {
		interval = 2 * time.Minute
	}
	go s.pollLoop(ctx, st, interval)
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

func errStr(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
