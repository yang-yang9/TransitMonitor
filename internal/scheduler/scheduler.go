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
	"strconv"
	"strings"
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
	baseRules       []alert.Rule         // YAML-derived fallback; seed to DB on first run

	mu                    sync.Mutex
	ruleMu                sync.RWMutex // protects Rules
	runCtx                context.Context
	cancels               map[string]context.CancelFunc
	snapshotRetentionDays int
	obsRetentionDays      int
	cooldown              time.Duration
	lastAlert             map[string]time.Time
	alertMu               sync.Mutex

	// Per-station digest: events buffer + last-flush time. digestInterval is the
	// min gap between digest deliveries per station (0 = flush every poll).
	digestInterval time.Duration
	digestMu       sync.Mutex
	pendingAlerts  map[string][]alert.AlertEvent
	lastDigest     map[string]time.Time

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
		pendingAlerts:         map[string][]alert.AlertEvent{},
		lastDigest:            map[string]time.Time{},
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

// snapshotRules returns a copy of the current rules under RLock.
func (s *Scheduler) snapshotRules() []alert.Rule {
	s.ruleMu.RLock()
	defer s.ruleMu.RUnlock()
	out := make([]alert.Rule, len(s.Rules))
	copy(out, s.Rules)
	return out
}

// rulesOfType returns the enabled rules of a given type. endpoint_auth_failed
// and poll_failure_streak rules are emitted directly by the scheduler (not by
// alert.Evaluate), so it looks them up here.
func (s *Scheduler) rulesOfType(typ string) []alert.Rule {
	rules := s.snapshotRules()
	var out []alert.Rule
	for _, r := range rules {
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
				Rule: r.Name, Type: alert.RuleQuotaBelow, StationID: stationID, Severity: domain.SevCritical,
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
	// dropPct > 0 means balance decreased; direction "down" (default/both) fires
	// on decrease, "up" fires on increase.
	for _, r := range s.rulesOfType(alert.RuleQuotaDropPct) {
		if prev.RemainingUSD <= 0 {
			continue // no prior reading or prior was 0 — pct undefined
		}
		dropPct := (prev.RemainingUSD - bal.RemainingUSD) / prev.RemainingUSD * 100
		signedDelta := -dropPct // positive = balance up, negative = balance down
		if abs64(dropPct) >= r.Threshold && matchQuotaDirection(r.Direction, signedDelta) {
			s.dispatchAlert(ctx, alert.AlertEvent{
				Rule: r.Name, Type: alert.RuleQuotaDropPct, StationID: stationID, Severity: domain.SevWarning,
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

func abs64(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func matchQuotaDirection(dir string, signedDelta float64) bool {
	switch dir {
	case alert.DirUp:
		return signedDelta > 0
	case alert.DirDown:
		return signedDelta < 0
	default:
		return true
	}
}

// dispatchAlert sends one AlertEvent through the notifier and records it in the
// store (sent flag + error), honoring the cooldown dedup. Shared by the change
// / probe paths and the direct endpoint_auth_failed / poll_failure_streak path.
// On a failed send the cooldown stamp is rolled back so the next poll that still
// observes the condition retries immediately — otherwise a transient notifier
// failure (5xx / network blip) would suppress re-firing for the full cooldown
// window, leaving the operator blind to a condition that never landed.
// dispatchAlert buffers an event for per-station digest delivery. The event is
// only added if it passes the cooldown dedup (same rule|station|model within
// cooldown is dropped). Actual delivery happens at flushStationAlerts, called
// at the end of each poll/probe cycle.
func (s *Scheduler) dispatchAlert(ctx context.Context, ev alert.AlertEvent) {
	if s.Notifier == nil {
		return
	}
	if !s.shouldFire(ev) {
		return
	}
	s.digestMu.Lock()
	defer s.digestMu.Unlock()
	if s.pendingAlerts == nil { // lazy init (test schedulers may skip New)
		s.pendingAlerts = map[string][]alert.AlertEvent{}
	}
	s.pendingAlerts[ev.StationID] = append(s.pendingAlerts[ev.StationID], ev)
}

// flushStationAlerts delivers one digest message per station if the digest
// interval has elapsed (or interval is 0 → every call). Otherwise the buffer
// is retained until the next eligible flush. Records each event in
// alert_events with the digest's send status; on failure clears the cooldowns
// so events can re-fire next poll.
func (s *Scheduler) flushStationAlerts(ctx context.Context, stationID string) {
	if s.Notifier == nil {
		return
	}
	s.digestMu.Lock()
	buf := s.pendingAlerts[stationID]
	now := s.Now()
	if len(buf) == 0 {
		s.digestMu.Unlock()
		return
	}
	if s.lastDigest == nil { // lazy init (test schedulers may skip New)
		s.lastDigest = map[string]time.Time{}
	}
	if s.digestInterval > 0 {
		if last, ok := s.lastDigest[stationID]; ok && now.Sub(last) < s.digestInterval {
			s.digestMu.Unlock() // not yet → keep buffering
			return
		}
	}
	delete(s.pendingAlerts, stationID)
	s.lastDigest[stationID] = now
	s.digestMu.Unlock()

	// Build a digest AlertEvent joining per-event Messages.
	msgs := make([]string, 0, len(buf))
	maxSev := ""
	for _, e := range buf {
		if e.Message == "" {
			e.Message = alert.FormatEvent(e)
		}
		msgs = append(msgs, e.Message)
		maxSev = severityRank(e.Severity, maxSev)
	}
	body := fmt.Sprintf("🚨 告警汇总 · %s（%d 条）\n─────────────\n%s",
		stationID, len(buf), strings.Join(msgs, "\n─────────────\n"))
	digest := alert.AlertEvent{
		Rule: "告警汇总", Type: "digest", StationID: stationID,
		Severity: maxSev, Message: body, CreatedAt: now,
		Payload: map[string]any{"count": len(buf), "station_id": stationID},
	}
	sendErr := s.Notifier.Send(ctx, digest)

	for _, e := range buf {
		payload, _ := json.Marshal(e.Payload)
		_ = s.Store.InsertAlertEvent(ctx, e.Rule, e.StationID, e.Model, string(payload), sendErr == nil, errStr(sendErr))
	}
	if sendErr != nil {
		for _, e := range buf {
			s.clearAlertCooldown(e)
		}
	}
}

// severityRank returns the more severe of a/b (critical>warning>info).
func severityRank(a, b string) string {
	rank := func(s string) int {
		switch s {
		case "critical":
			return 3
		case "warning":
			return 2
		case "info":
			return 1
		}
		return 0
	}
	if rank(a) >= rank(b) {
		return a
	}
	return b
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
				Rule: r.Name, Type: alert.RuleEndpointAuthFail, StationID: stationID, Severity: "critical",
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
				Rule: r.Name, Type: alert.RulePollFailureStreak, StationID: stationID, Severity: "critical",
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
		st.SortOrder = len(s.stationList)
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

// ReorderStations reorders the in-memory station list + persists sort_order to DB.
func (s *Scheduler) ReorderStations(ids []string) error {
	if s.runCtx == nil {
		return fmt.Errorf("调度器未运行")
	}
	s.mu.Lock()
	idx := make(map[string]int, len(s.stationList))
	for i, st := range s.stationList {
		idx[st.ID] = i
	}
	reordered := make([]domain.Station, 0, len(s.stationList))
	seen := make(map[string]bool, len(ids))
	for order, id := range ids {
		if i, ok := idx[id]; ok {
			s.stationList[i].SortOrder = order
			reordered = append(reordered, s.stationList[i])
			seen[id] = true
		}
	}
	for _, st := range s.stationList {
		if !seen[st.ID] {
			reordered = append(reordered, st)
		}
	}
	s.stationList = reordered
	s.mu.Unlock()
	if s.Store != nil {
		_ = s.Store.ReorderStations(s.runCtx, ids)
	}
	return nil
}

// PollOnce runs one scrape→store→diff→alert cycle for a station.
func (s *Scheduler) PollOnce(ctx context.Context, stationID string) error {
	defer s.flushStationAlerts(ctx, stationID)
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
		for _, ev := range alert.Evaluate(s.snapshotRules(), events, nil) {
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
			for _, ev := range alert.Evaluate(s.snapshotRules(), nil, []domain.ProbeResult{pres}) {
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
	s.flushStationAlerts(ctx, st.ID)
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

const appSettingAlertRules = "alert_rules"

// SetBaseRules stores the YAML-derived rules as fallback/seed (called by main).
func (s *Scheduler) SetBaseRules(rules []alert.Rule) {
	s.baseRules = rules
}

// LoadRules loads alert rules from the store; if none are stored yet, seeds the
// store with baseRules (YAML defaults). Must be called after SetBaseRules.
func (s *Scheduler) LoadRules(ctx context.Context) error {
	raw, ok, err := s.Store.GetAppSetting(ctx, appSettingAlertRules)
	if err != nil {
		return err
	}
	if ok {
		var rules []alert.Rule
		if err := json.Unmarshal([]byte(raw), &rules); err != nil {
			return fmt.Errorf("unmarshal alert_rules: %w", err)
		}
		s.ruleMu.Lock()
		s.Rules = rules
		s.ruleMu.Unlock()
		return nil
	}
	// Seed from baseRules.
	blob, err := json.Marshal(s.baseRules)
	if err != nil {
		return err
	}
	if err := s.Store.SetAppSetting(ctx, appSettingAlertRules, string(blob)); err != nil {
		return err
	}
	s.ruleMu.Lock()
	s.Rules = s.baseRules
	s.ruleMu.Unlock()
	return nil
}

// AlertRules returns a snapshot of the current rules (for the UI).
func (s *Scheduler) AlertRules(_ context.Context) []alert.Rule {
	return s.snapshotRules()
}

// SaveAlertRules validates, persists, and hot-reloads alert rules.
func (s *Scheduler) SaveAlertRules(ctx context.Context, rules []alert.Rule) error {
	for i, r := range rules {
		if r.Name == "" {
			return fmt.Errorf("rule #%d: name is required", i+1)
		}
		if !alert.ValidRuleTypes[r.Type] {
			return fmt.Errorf("rule #%d: unknown type %q", i+1, r.Type)
		}
		if !alert.ValidDirections[r.Direction] {
			return fmt.Errorf("rule #%d: unknown direction %q", i+1, r.Direction)
		}
		if r.Threshold < 0 {
			return fmt.Errorf("rule #%d: threshold must be >= 0", i+1)
		}
	}
	blob, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	if err := s.Store.SetAppSetting(ctx, appSettingAlertRules, string(blob)); err != nil {
		return err
	}
	s.ruleMu.Lock()
	s.Rules = rules
	s.ruleMu.Unlock()
	s.alertMu.Lock()
	s.lastAlert = map[string]time.Time{}
	s.alertMu.Unlock()
	return nil
}

// ResetRules restores the YAML-derived default rules.
func (s *Scheduler) ResetRules(ctx context.Context) error {
	return s.SaveAlertRules(ctx, s.baseRules)
}

const (
	appSettingCooldown       = "alert_cooldown_minutes"
	appSettingDigestInterval = "alert_digest_interval_minutes"
	defaultCooldownMinutes   = 30
	defaultDigestMinutes     = 0 // 0 = flush every poll (per-poll digest)
)

// AlertBehavior returns the current cooldown (min) and digest interval (min).
func (s *Scheduler) AlertBehavior() (cooldownMin, digestMin int) {
	c := int(s.cooldown / time.Minute)
	d := int(s.digestInterval / time.Minute)
	if c < 0 {
		c = 0
	}
	if d < 0 {
		d = 0
	}
	return c, d
}

// LoadBehavior loads cooldown + digest interval from the store, seeding defaults
// on first run. Called at startup.
func (s *Scheduler) LoadBehavior(ctx context.Context) error {
	cd, okCd, err := s.Store.GetAppSetting(ctx, appSettingCooldown)
	if err != nil {
		return err
	}
	di, okDi, err := s.Store.GetAppSetting(ctx, appSettingDigestInterval)
	if err != nil {
		return err
	}
	if !okCd {
		cd = strconv.Itoa(defaultCooldownMinutes)
		if err := s.Store.SetAppSetting(ctx, appSettingCooldown, cd); err != nil {
			return err
		}
	}
	if !okDi {
		di = strconv.Itoa(defaultDigestMinutes)
		if err := s.Store.SetAppSetting(ctx, appSettingDigestInterval, di); err != nil {
			return err
		}
	}
	cMin, _ := strconv.Atoi(cd)
	dMin, _ := strconv.Atoi(di)
	if cMin < 0 {
		cMin = 0
	}
	if dMin < 0 {
		dMin = 0
	}
	s.cooldown = time.Duration(cMin) * time.Minute
	s.digestInterval = time.Duration(dMin) * time.Minute
	return nil
}

// SaveAlertBehavior persists cooldown + digest interval and applies live.
func (s *Scheduler) SaveAlertBehavior(ctx context.Context, cooldownMin, digestMin int) error {
	if cooldownMin < 0 || digestMin < 0 {
		return fmt.Errorf("cooldown 和聚合间隔不能为负")
	}
	if err := s.Store.SetAppSetting(ctx, appSettingCooldown, strconv.Itoa(cooldownMin)); err != nil {
		return err
	}
	if err := s.Store.SetAppSetting(ctx, appSettingDigestInterval, strconv.Itoa(digestMin)); err != nil {
		return err
	}
	s.cooldown = time.Duration(cooldownMin) * time.Minute
	s.digestInterval = time.Duration(digestMin) * time.Minute
	// A behavior change should let stale alerts re-fire.
	s.alertMu.Lock()
	s.lastAlert = map[string]time.Time{}
	s.alertMu.Unlock()
	s.digestMu.Lock()
	s.lastDigest = map[string]time.Time{}
	s.digestMu.Unlock()
	return nil
}
