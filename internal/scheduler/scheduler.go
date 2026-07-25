// Package scheduler drives the per-station scrape→normalize→store→diff→alert
// loop, the real-cost probe, a daily retention job, and runtime station
// add/remove (StationManager). It owns no domain math; it composes adapter +
// store + changedet + alert + probe.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"transitmonitor/internal/adapter"
	"transitmonitor/internal/adapter/sub2api"
	"transitmonitor/internal/alert"
	"transitmonitor/internal/changedet"
	"transitmonitor/internal/domain"
	"transitmonitor/internal/jwtlogin"
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

	baseNotifierCfg alert.NotifierConfig // YAML-derived floor; DB overrides merge on top

	mu                    sync.Mutex
	runCtx                context.Context
	cancels               map[string]context.CancelFunc
	snapshotRetentionDays int
	obsRetentionDays      int
	cooldown              time.Duration
	lastAlert             map[string]time.Time
	alertMu               sync.Mutex

	failMu     sync.Mutex
	failStreak map[string]int  // consecutive poll failures per station
	authOK     map[string]bool // last known auth state per station (true = OK)
}

// New constructs a scheduler with defaults.
func New(stations []domain.Station, adapters map[string]adapter.Adapter, st *store.Store, rules []alert.Rule, n alert.Notifier) *Scheduler {
	return &Scheduler{
		stationList: stations, Adapters: adapters, Store: st, Rules: rules, Notifier: n,
		DiffCfg: changedet.DefaultConfig(), Now: time.Now, Logger: slog.Default(),
		cancels:               map[string]context.CancelFunc{},
		cooldown:              30 * time.Minute,
		lastAlert:             map[string]time.Time{},
		failStreak:            map[string]int{},
		authOK:                map[string]bool{},
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

// rulesOfType returns the enabled rules of a given type. endpoint_auth_failed
// and poll_failure_streak rules are emitted directly by the scheduler (not by
// alert.Evaluate), so it looks them up here.
func (s *Scheduler) rulesOfType(typ string) []alert.Rule {
	var out []alert.Rule
	for _, r := range s.Rules {
		if r.Enabled && r.Type == typ {
			out = append(out, r)
		}
	}
	return out
}

// balanceSource returns the endpoint a balance reading came from, for the
// observation's SourceEndpoint field. Mirrors the per-kind probe path.
func (s *Scheduler) balanceSource(caps domain.CapabilityReport) string {
	switch caps.Kind {
	case domain.KindSub2API:
		return "/api/v1/user/profile"
	default:
		return "/api/user/self"
	}
}

// evaluateBalanceRules emits the gauge-based balance alerts (quota_below and
// quota_drop_pct) directly, like endpoint_auth_failed. These evaluate against
// the just-stored reading (and the prior one for the drop %), not change events.
func (s *Scheduler) evaluateBalanceRules(ctx context.Context, stationID string, bal domain.BalanceObservation, prev domain.BalanceObservation, now time.Time) {
	if s.Notifier == nil {
		return
	}
	// quota_below: remaining balance (USD) under threshold. Skip when the
	// station reports an unlimited wallet (no meaningful "low" to fire on).
	for _, r := range s.rulesOfType(alert.RuleQuotaBelow) {
		if bal.Unlimited {
			continue
		}
		if bal.RemainingUSD < r.Threshold {
			s.dispatchAlert(ctx, alert.AlertEvent{
				Rule: r.Name, StationID: stationID, Severity: domain.SevCritical,
				Payload: map[string]any{
					"remaining_usd": bal.RemainingUSD, "used_usd": bal.UsedUSD,
					"total_usd": bal.TotalUSD, "threshold_usd": r.Threshold,
					"currency": bal.Currency, "observed_at": now,
				},
				CreatedAt: now,
			})
		}
	}
	// quota_drop_pct: remaining dropped ≥ threshold % vs the previous reading.
	for _, r := range s.rulesOfType(alert.RuleQuotaDropPct) {
		if prev.RemainingUSD <= 0 {
			continue // no prior reading or prior was 0 — pct undefined
		}
		dropPct := (prev.RemainingUSD - bal.RemainingUSD) / prev.RemainingUSD * 100
		if dropPct >= r.Threshold {
			s.dispatchAlert(ctx, alert.AlertEvent{
				Rule: r.Name, StationID: stationID, Severity: domain.SevWarning,
				Payload: map[string]any{
					"prev_usd": prev.RemainingUSD, "remaining_usd": bal.RemainingUSD,
					"drop_pct": dropPct, "threshold_pct": r.Threshold,
					"observed_at": now,
				},
				CreatedAt: now,
			})
		}
	}
}

// dispatchAlert sends one AlertEvent through the notifier and records it in the
// store (sent flag + error), honoring the cooldown dedup. Shared by the change
// / probe paths and the direct endpoint_auth_failed / poll_failure_streak path.
// On a failed send the cooldown stamp is rolled back so the next poll that still
// observes the condition retries immediately — otherwise a transient notifier
// failure (5xx / network blip) would suppress re-firing for the full cooldown
// window, leaving the operator blind to a condition that never landed.
func (s *Scheduler) dispatchAlert(ctx context.Context, ev alert.AlertEvent) {
	if s.Notifier == nil {
		return
	}
	if !s.shouldFire(ev) {
		return
	}
	sendErr := s.Notifier.Send(ctx, ev)
	if sendErr != nil {
		s.clearAlertCooldown(ev)
	}
	payload, _ := json.Marshal(ev.Payload)
	_ = s.Store.InsertAlertEvent(ctx, ev.Rule, ev.StationID, ev.Model, string(payload), sendErr == nil, errStr(sendErr))
}

// clearAlertCooldown removes the cooldown stamp for an event so a failed send
// can be retried on the next evaluation. No-op if cooldown is disabled.
func (s *Scheduler) clearAlertCooldown(ev alert.AlertEvent) {
	if s.cooldown <= 0 {
		return
	}
	key := ev.Rule + "|" + ev.StationID + "|" + ev.Model
	s.alertMu.Lock()
	delete(s.lastAlert, key)
	s.alertMu.Unlock()
}

// recordPollSuccess resets the consecutive-failure streak and marks the station
// auth-OK. Called after FetchRatios succeeds.
func (s *Scheduler) recordPollSuccess(stationID string) {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	s.failStreak[stationID] = 0
	s.authOK[stationID] = true
}

// recordPollFailure handles a failed scrape: increments the streak, emits
// endpoint_auth_failed (on an OK→failed auth flip) and poll_failure_streak
// (when streak crosses a rule's threshold) alerts. Errors here must not mask
// the returned fetch error.
func (s *Scheduler) recordPollFailure(ctx context.Context, stationID string, err error) {
	now := s.Now()
	isAuth := errors.Is(err, domain.ErrAuthFailed)

	s.failMu.Lock()
	s.failStreak[stationID]++
	streak := s.failStreak[stationID]
	wasOK := s.authOK[stationID]
	if isAuth {
		s.authOK[stationID] = false
	}
	s.failMu.Unlock()

	// endpoint_auth_failed: fire only on the OK→failed transition (not every
	// failed poll), so a persistently-broken station alerts once, not per-poll.
	if isAuth && wasOK {
		for _, r := range s.rulesOfType(alert.RuleEndpointAuthFail) {
			s.dispatchAlert(ctx, alert.AlertEvent{
				Rule: r.Name, StationID: stationID, Severity: "critical",
				Payload:   map[string]any{"status": "auth_failed", "error": err.Error(), "observed_at": now},
				CreatedAt: now,
			})
		}
	}

	// poll_failure_streak: fire when the streak reaches (or first crosses) the
	// threshold. Equal-to check fires exactly once per threshold crossing.
	for _, r := range s.rulesOfType(alert.RulePollFailureStreak) {
		threshold := int(r.Threshold)
		if threshold <= 0 {
			threshold = 1
		}
		if streak == threshold {
			s.dispatchAlert(ctx, alert.AlertEvent{
				Rule: r.Name, StationID: stationID, Severity: "critical",
				Payload:   map[string]any{"streak": streak, "error": err.Error(), "threshold": threshold, "observed_at": now},
				CreatedAt: now,
			})
		}
	}
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
		return fmt.Errorf("调度器未运行")
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
	s.wireJWTPersistLocked(st.ID, a)
	if st.Enabled {
		s.startStationLocked(s.runCtx, st)
	}
	return nil
}

// RemoveStation stops the poller + removes the station + deletes it from the store.
// Implements dashboard.StationManager.
func (s *Scheduler) RemoveStation(id string) error {
	if s.runCtx == nil {
		return fmt.Errorf("调度器未运行")
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
		return fmt.Errorf("未知站点 %s", stationID)
	}
	s.maybeRefreshJWT(ctx, &stn, a)
	prev, err := s.Store.PrevPollObservations(ctx, stationID)
	if err != nil {
		return err
	}
	caps, err := a.ProbeCapabilities(ctx)
	if err != nil {
		s.recordPollFailure(ctx, stationID, err)
		return fmt.Errorf("探测能力失败: %w", err)
	}
	snap, obs, err := a.FetchRatios(ctx, caps)
	if err != nil {
		s.recordPollFailure(ctx, stationID, err)
		return fmt.Errorf("抓取倍率失败: %w", err)
	}
	s.recordPollSuccess(stationID)
	t := s.Now()
	// Fetch previous group ratios BEFORE inserting the new snapshot, so the
	// group-ratio diff compares against the prior state (not the just-written one).
	prevGroupRatios, _ := s.Store.PrevGroupRatios(ctx, stationID, t)
	// Same pattern for balance: fetch the previous reading before inserting the
	// new one, so the quota_drop_pct alert compares against the prior reading.
	prevBalance, _ := s.Store.PrevBalance(ctx, stationID, t)
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
	events := changedet.Diff(prev, obs, prevGroupRatios, snap.GroupRatios, s.DiffCfg)
	// changedet is pure/time-less; stamp station + time on group-ratio events.
	for i := range events {
		if events[i].Field == changedet.FieldGroupRatio {
			events[i].StationID = stationID
			events[i].ObservedAt = t
		}
	}
	if len(events) > 0 {
		if err := s.Store.InsertChangeEvents(ctx, events); err != nil {
			return err
		}
	}
	if s.Notifier != nil {
		for _, ev := range alert.Evaluate(s.Rules, events, nil) {
			s.dispatchAlert(ctx, ev)
		}
	}
	// Persist the balance time series (replaces the audit-log-only write). Only
	// when a balance source succeeded; stations without a balance endpoint stay
	// silent rather than recording zeros.
	if caps.HasQuota {
		bal := domain.NewBalanceFromCaps(caps, t, s.balanceSource(caps))
		if err := s.Store.InsertBalanceObservation(ctx, bal); err != nil {
			s.logger().Error("balance store", "station", stationID, "err", err)
		} else {
			s.evaluateBalanceRules(ctx, stationID, bal, prevBalance, t)
			_ = s.Store.InsertAuditLog(ctx, "adapter", "balance", stationID,
				fmt.Sprintf("remaining_usd=%.4f used_usd=%.4f total_usd=%.4f unlimited=%v src=%s",
					bal.RemainingUSD, bal.UsedUSD, bal.TotalUSD, bal.Unlimited, bal.SourceEndpoint))
		}
	}
	if s.Prober != nil && found && stn.Probe.Enabled && time.Duration(stn.Probe.Interval) == 0 {
		// Piggyback probe (backward compat): no dedicated interval → probe runs
		// every poll. When probe.interval > 0, a dedicated probeLoop handles it.
		s.runStationProbe(ctx, stn, obs)
	}
	return nil
}

func (s *Scheduler) runStationProbe(ctx context.Context, st domain.Station, obs []domain.RatioObservation) {
	for _, model := range st.Probe.TargetModels() {
		pres, perr := s.Prober.Run(ctx, st, model, obs)
		if perr != nil {
			s.logger().Error("probe", "station", st.ID, "model", model, "err", perr)
			continue
		}
		if pres.Error == "deduped" {
			continue
		}
		if err := s.Store.InsertProbeResult(ctx, pres); err != nil {
			s.logger().Error("probe store", "station", st.ID, "model", model, "err", err)
		}
		_ = s.Store.InsertAuditLog(ctx, "probe", "probe.run", st.ID,
			fmt.Sprintf("model=%s tokens=%d/%d markup=%.2f%% cost_usd=%.6f declared_unavailable=%v error=%s",
				pres.Model, pres.TokensIn, pres.TokensOut, pres.MarkupPct, pres.CostUSD, pres.DeclaredUnavailable, pres.Error))
		if s.Notifier != nil {
			for _, ev := range alert.Evaluate(s.Rules, nil, []domain.ProbeResult{pres}) {
				s.dispatchAlert(ctx, ev)
			}
		}
	}
}

// wireJWTPersistLocked injects a persist callback into adapters that implement
// adapter.JWPersister, so a reactive JWT re-login (e.g. sub2api on a 401
// fingerprint-stale token) survives to the store + in-memory station list.
// Caller must hold s.mu.
func (s *Scheduler) wireJWTPersistLocked(stationID string, a adapter.Adapter) {
	if a == nil {
		return
	}
	jp, ok := a.(adapter.JWPersister)
	if !ok {
		return
	}
	jp.SetJWTPersistFn(s.makeJWTPersistFn(stationID))
}

// makeJWTPersistFn returns a callback that updates the station's JWT in the
// in-memory list and re-persists (encrypted) to the store.
func (s *Scheduler) makeJWTPersistFn(stationID string) func(jwt string) {
	return func(jwt string) {
		s.mu.Lock()
		var st domain.Station
		for i, x := range s.stationList {
			if x.ID == stationID {
				s.stationList[i].Auth.JWT = jwt
				st = s.stationList[i]
				break
			}
		}
		s.mu.Unlock()
		if st.ID == "" || s.EncKey == nil || s.Store == nil || s.runCtx == nil {
			return
		}
		_ = s.Store.UpsertStation(s.runCtx, st, s.EncKey)
		s.logger().Info("jwt persisted after reactive refresh", "station", stationID)
	}
}

// Run polls each enabled station and runs a daily retention job until ctx cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	s.mu.Lock()
	s.runCtx = ctx
	for _, st := range s.stationList {
		s.wireJWTPersistLocked(st.ID, s.Adapters[st.ID])
		if !st.Enabled {
			s.logger().Info("skip disabled station", "station", st.ID)
			continue
		}
		s.logger().Info("starting poller", "station", st.ID, "interval", time.Duration(st.PollInterval))
		s.startStationLocked(ctx, st)
	}
	s.mu.Unlock()
	// Refresh the public LiteLLM price table (~3000 models) for sub2api base
	// prices; one shared background refresher for all sub2api stations.
	if s.Client != nil {
		go sub2api.StartLiteLLMRefresher(ctx, s.Client, 24*time.Hour)
	}
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
	// Dedicated probe loop: when probe.interval > 0, the probe runs on its own
	// (low) cadence independent of poll_interval. Shares the station ctx, so it
	// is cancelled with the poller on remove/replace.
	if st.Probe.Enabled && time.Duration(st.Probe.Interval) > 0 {
		go s.probeLoop(ctx, st)
	}
}

// probeLoop runs the real-cost probe on a dedicated cadence (probe.interval),
// decoupled from polling. Each tick it reads the latest scraped observations
// from the store (declared prices to reconcile against) and probes every
// configured target model. The frequency is intentionally low (e.g. 24h) —
// probes cost real money (tiny chat).
func (s *Scheduler) probeLoop(ctx context.Context, st domain.Station) {
	interval := time.Duration(st.Probe.Interval)
	if interval < time.Minute {
		interval = time.Minute // guard against misconfig spin; dedup(10m) floors it anyway
	}
	// First probe shortly after start so a baseline exists without waiting a
	// full interval; subsequent probes tick at interval.
	s.runProbeCycle(ctx, st)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runProbeCycle(ctx, st)
		}
	}
}

func (s *Scheduler) runProbeCycle(ctx context.Context, st domain.Station) {
	obs, err := s.Store.LatestRatioObservations(ctx, st.ID)
	if err != nil {
		s.logger().Error("probe obs load", "station", st.ID, "err", err)
		return
	}
	s.runStationProbe(ctx, st, obs)
}

func (s *Scheduler) pollLoop(ctx context.Context, st domain.Station, interval time.Duration) {
	if err := s.PollOnce(ctx, st.ID); err != nil && ctx.Err() == nil {
		s.logger().Error("poll", "station", st.ID, "err", err)
		_ = s.Store.InsertAuditLog(ctx, "scheduler", "poll.error", st.ID, err.Error())
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
				_ = s.Store.InsertAuditLog(ctx, "scheduler", "poll.error", st.ID, err.Error())
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

const jwtRefreshMargin = 5 * time.Minute

// maybeRefreshJWT auto-refreshes the JWT for sub2api stations that have
// admin_email + admin_pass configured. Skipped if the current JWT is still fresh.
func (s *Scheduler) maybeRefreshJWT(ctx context.Context, stn *domain.Station, a adapter.Adapter) {
	if stn.Kind != domain.KindSub2API || stn.Auth.AdminEmail == "" || stn.Auth.AdminPass == "" {
		return
	}
	if !jwtlogin.NeedsRefresh(stn.Auth.JWT, jwtRefreshMargin, s.Now()) {
		return
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	token, exp, err := jwtlogin.Login(ctx, stn.BaseURL, stn.Auth.AdminEmail, stn.Auth.AdminPass, client)
	if err != nil {
		s.logger().Warn("jwt auto-refresh failed", "station", stn.ID, "err", err)
		return
	}
	s.logger().Info("jwt auto-refreshed", "station", stn.ID, "expires", exp)
	if jr, ok := a.(adapter.JWTRefresher); ok {
		jr.SetJWT(token)
	}
	s.mu.Lock()
	for i, x := range s.stationList {
		if x.ID == stn.ID {
			s.stationList[i].Auth.JWT = token
			*stn = s.stationList[i]
			break
		}
	}
	s.mu.Unlock()
	if s.EncKey != nil && s.Store != nil {
		_ = s.Store.UpsertStation(ctx, *stn, s.EncKey)
	}
}

// PollNow triggers an immediate poll for a station (used by the dashboard "refresh" button).
func (s *Scheduler) PollNow(stationID string) error {
	return s.PollOnce(context.Background(), stationID)
}

// SetBaseNotifierConfig records the YAML-derived notifier config as the floor
// that DB overrides merge on top of. Called once from main at startup.
func (s *Scheduler) SetBaseNotifierConfig(nc alert.NotifierConfig) {
	s.baseNotifierCfg = nc
}

// mergedNotifierConfig returns base ⊕ DB (DB non-empty fields win), with real
// secrets. On missing row / encryption disabled, returns the base config.
func (s *Scheduler) mergedNotifierConfig(ctx context.Context) alert.NotifierConfig {
	nc := s.baseNotifierCfg
	if s.EncKey == nil || s.Store == nil {
		return nc
	}
	blob, err := s.Store.GetNotifierConfig(ctx, store.NotifierConfigID, s.EncKey)
	if err != nil || blob == "" {
		return nc // missing/disabled → base (YAML) is authoritative
	}
	var db alert.NotifierConfig
	if json.Unmarshal([]byte(blob), &db) != nil {
		return nc
	}
	mergeNonEmpty(&nc.DingTalk.Webhook, db.DingTalk.Webhook)
	mergeNonEmpty(&nc.DingTalk.Secret, db.DingTalk.Secret)
	mergeNonEmpty(&nc.Webhook.URL, db.Webhook.URL)
	mergeNonEmpty(&nc.Lark.Webhook, db.Lark.Webhook)
	mergeNonEmpty(&nc.Lark.Secret, db.Lark.Secret)
	mergeNonEmpty(&nc.Slack.Webhook, db.Slack.Webhook)
	mergeNonEmpty(&nc.QQ.AppID, db.QQ.AppID)
	mergeNonEmpty(&nc.QQ.AppSecret, db.QQ.AppSecret)
	mergeNonEmpty(&nc.QQ.GroupOpenID, db.QQ.GroupOpenID)
	return nc
}

func mergeNonEmpty(dst *string, src string) {
	if src != "" {
		*dst = src
	}
}

// ReloadNotifiers rebuilds the live notifier from base ⊕ DB and swaps it in.
// Called at startup and after the /settings page saves.
func (s *Scheduler) ReloadNotifiers(ctx context.Context) error {
	nc := s.mergedNotifierConfig(ctx)
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	s.alertMu.Lock()
	s.Notifier = alert.BuildDispatcher(nc, client)
	// A config change should let stale auth-failure / streak alerts re-fire.
	s.lastAlert = map[string]time.Time{}
	s.alertMu.Unlock()
	return nil
}

// SaveNotifierConfig persists the incoming notifier config (preserving existing
// secrets where the incoming field is blank — keep-blank contract), then
// reloads. Returns ErrEncryptionDisabled when no encryption key is set.
func (s *Scheduler) SaveNotifierConfig(ctx context.Context, incoming alert.NotifierConfig) error {
	if s.EncKey == nil {
		return store.ErrEncryptionDisabled
	}
	cur := s.mergedNotifierConfig(ctx)
	// Merge: incoming non-empty wins; blank secret fields keep current.
	incoming.DingTalk.Secret = orBlank(incoming.DingTalk.Secret, cur.DingTalk.Secret)
	incoming.Lark.Secret = orBlank(incoming.Lark.Secret, cur.Lark.Secret)
	incoming.QQ.AppSecret = orBlank(incoming.QQ.AppSecret, cur.QQ.AppSecret)
	blob, err := json.Marshal(incoming)
	if err != nil {
		return err
	}
	if err := s.Store.SetNotifierConfig(ctx, store.NotifierConfigID, s.EncKey, string(blob)); err != nil {
		return err
	}
	return s.ReloadNotifiers(ctx)
}

func orBlank(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// NotifierConfigs returns the merged config for the /settings form, with
// secrets blanked (keep-blank semantics); URLs / AppID / GroupOpenID shown
// in full. Encryption-disabled status is conveyed by the caller via encKey.
func (s *Scheduler) NotifierConfigs(ctx context.Context) alert.NotifierConfig {
	nc := s.mergedNotifierConfig(ctx)
	nc.DingTalk.Secret = ""
	nc.Lark.Secret = ""
	nc.QQ.AppSecret = ""
	return nc
}

// SendTestAlert fires a sample alert through the current notifier and records
// the result as an alert_event row (so it shows on /alerts). Returns nil on
// successful send, else the send error.
func (s *Scheduler) SendTestAlert(ctx context.Context, kind string) error {
	now := s.Now()
	ev := alert.AlertEvent{
		Rule: "test", StationID: kind, Model: kind, Severity: "info",
		Payload:   map[string]any{"kind": kind, "source": "settings", "observed_at": now},
		CreatedAt: now,
	}
	if s.Notifier == nil {
		return errors.New("no notifier configured")
	}
	sendErr := s.Notifier.Send(ctx, ev)
	payload, _ := json.Marshal(ev.Payload)
	_ = s.Store.InsertAlertEvent(ctx, ev.Rule, ev.StationID, ev.Model, string(payload), sendErr == nil, errStr(sendErr))
	return sendErr
}
