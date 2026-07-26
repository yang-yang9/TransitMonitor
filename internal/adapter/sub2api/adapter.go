// Package sub2api implements adapter.Adapter for Wei-Shaw/sub2api stations.
//
// Fallback chain:
//
//	/v1/sub2api/billing (sk-key)             → per-key effective_rate_multiplier (404 in simple mode)
//	/api/v1/channels/available (user JWT)    → per-(model, group) base prices + each group's rate_multiplier
//
// sub2api decouples price from discount: channels/available carries the RAW
// LiteLLM per-token USD price on supported_models[].pricing and the group's
// rate_multiplier (the discount) as a sibling field on the platform's groups[].
// modelsFromChannels folds each group's rate (× peak, in the station tz) into a
// per-(model, group) row so a discounted group surfaces a discounted USD/1M
// rather than the original LiteLLM price. The sk-key's billing effective (which
// also folds per-user overrides) is only used as a degraded fallback when a
// platform section carries no groups[].
// Spec: openspec/.../specs/ratio-collection-sub2api/spec.md
package sub2api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"transitmonitor/internal/domain"
	"transitmonitor/internal/jwtlogin"
	"transitmonitor/internal/normalize"
)

// Adapter scrapes a sub2api station.
type Adapter struct {
	StationID   string
	BaseURL     string
	APIKey      string // sk- key for /v1/* + billing
	JWT         string // user JWT for /api/v1/channels/available
	AdminAPIKey string // x-api-key for /api/v1/admin/* (reserved; v1 uses channels/available)
	AdminEmail  string // admin email for /api/v1/auth/login (auto JWT refresh)
	AdminPass   string // admin password for /api/v1/auth/login (auto JWT refresh)
	Group       string
	Client      *http.Client
	persistJWT  func(jwt string) // optional: persist a refreshed JWT (store + station list)
	now         func() time.Time
}

// New constructs a sub2api adapter. group defaults to "default".
func New(stationID, baseURL, apiKey, jwt, adminAPIKey, adminEmail, adminPass, group string, client *http.Client) *Adapter {
	if client == nil {
		client = http.DefaultClient
	}
	if group == "" {
		group = "default"
	}
	return &Adapter{
		StationID: stationID, BaseURL: baseURL, APIKey: apiKey, JWT: jwt,
		AdminAPIKey: adminAPIKey, AdminEmail: adminEmail, AdminPass: adminPass,
		Group: group, Client: client, now: time.Now,
	}
}

// SetClock injects a clock (for tests).
func (a *Adapter) SetClock(f func() time.Time) { a.now = f }

// SetJWT updates the JWT used for authenticated endpoints (e.g. after auto-login refresh).
func (a *Adapter) SetJWT(jwt string) { a.JWT = jwt }

// SetJWTPersistFn injects a callback that persists a refreshed JWT (encrypted
// to the store + updates the in-memory station list). Implements adapter.JWPersister.
func (a *Adapter) SetJWTPersistFn(fn func(jwt string)) { a.persistJWT = fn }

// refreshJWT re-logs in via /api/v1/auth/login, updates a.JWT (in-memory), and
// persists via the callback if set. Returns the new JWT and an error.
func (a *Adapter) refreshJWT(ctx context.Context) (string, error) {
	if a.AdminEmail == "" || a.AdminPass == "" {
		return "", fmt.Errorf("admin email/pass not configured for JWT refresh")
	}
	token, _, err := jwtlogin.Login(ctx, a.BaseURL, a.AdminEmail, a.AdminPass, a.Client)
	if err != nil {
		return "", err
	}
	a.JWT = token
	if a.persistJWT != nil {
		a.persistJWT(token)
	}
	return token, nil
}

// jwtGet fetches a JWT-authenticated path. If the JWT is fingerprint-stale
// (401) and admin creds are configured, it re-logs in and retries once.
// Returns (status, body, err).
func (a *Adapter) jwtGet(ctx context.Context, path string) (int, []byte, error) {
	status, body, err := a.doGet(ctx, path, a.JWT)
	if err != nil || status != http.StatusUnauthorized {
		return status, body, err
	}
	// 401: JWT fingerprint-stale (or invalid). Re-login once if we can, then retry.
	if _, lerr := a.refreshJWT(ctx); lerr != nil {
		return status, body, err // keep the original 401 error
	}
	return a.doGet(ctx, path, a.JWT)
}

func (a *Adapter) nowTime() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// --- response shapes (verified against sub2api source; see docs/upstream-contract.md) ---

// billingResp is the direct JSON of GET /v1/sub2api/billing (NOT wrapped).
type billingResp struct {
	Object                  string   `json:"object"`
	GroupRateMultiplier     float64  `json:"group_rate_multiplier"`
	ResolvedRateMultiplier  float64  `json:"resolved_rate_multiplier"`
	PeakRateEnabled         bool     `json:"peak_rate_enabled"`
	PeakStart               *string  `json:"peak_start"`
	PeakEnd                 *string  `json:"peak_end"`
	PeakRateMultiplier      *float64 `json:"peak_rate_multiplier"`
	AppliedPeakMultiplier   *float64 `json:"applied_peak_multiplier"`
	EffectiveRateMultiplier float64  `json:"effective_rate_multiplier"`
	Timezone                *string  `json:"timezone"` // station tz for peak-window math
}

type modelPricing struct {
	InputPrice      *float64 `json:"input_price"`
	OutputPrice     *float64 `json:"output_price"`
	CacheReadPrice  *float64 `json:"cache_read_price"`
	CacheWritePrice *float64 `json:"cache_write_price"`
}

type supportedModel struct {
	Name    string        `json:"name"`
	Pricing *modelPricing `json:"pricing"`
}

type platformSection struct {
	Platform        string              `json:"platform"`
	Groups          []availableGroupRef `json:"groups"`
	SupportedModels []supportedModel    `json:"supported_models"`
}

// availableGroupRef is a per-platform group reference inside channels/available.
// It carries the group's DEFAULT rate_multiplier and peak config (NOT the per-user
// override — that lives on /api/v1/groups/rates, which the monitor does not call).
// Each group's rate_multiplier is a discount off the raw LiteLLM per-token price
// carried on the sibling supported_models[].pricing; it must be folded into the
// model price to surface the discounted USD/1M the user actually pays.
type availableGroupRef struct {
	Name               string  `json:"name"`
	SubscriptionType   string  `json:"subscription_type"`
	RateMultiplier     float64 `json:"rate_multiplier"`
	PeakRateEnabled    bool    `json:"peak_rate_enabled"`
	PeakStart          string  `json:"peak_start"`
	PeakEnd            string  `json:"peak_end"`
	PeakRateMultiplier float64 `json:"peak_rate_multiplier"`
}

type availableChannel struct {
	Name      string            `json:"name"`
	Platforms []platformSection `json:"platforms"`
}

type channelsAvailableResp struct {
	Success bool               `json:"success"`
	Data    []availableChannel `json:"data"`
}

// groupsAvailableResp is GET /api/v1/groups/available (user JWT, no
// available-channels flag, no balance check). Returns every group the user can
// see with its rate_multiplier — the richest per-group ratio source available
// to a non-admin customer.
type groupsAvailableResp struct {
	Code int `json:"code"`
	Data []struct {
		Name               string  `json:"name"`
		RateMultiplier     float64 `json:"rate_multiplier"`
		PeakRateEnabled    bool    `json:"peak_rate_enabled"`
		PeakRateMultiplier float64 `json:"peak_rate_multiplier"`
		Status             string  `json:"status"`
	} `json:"data"`
}

// userProfileResp is GET /api/v1/user/profile (user JWT). Mirrors sub2api's
// response.Response wrapper {code,message,data} (code==0 = success), verified
// against backend/internal/pkg/response/response.go + handler/user_handler.go
// GetProfile (returns dto.User). balance/frozen_balance are USD.
type userProfileResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Balance       float64 `json:"balance"`        // spendable wallet balance (USD)
		FrozenBalance float64 `json:"frozen_balance"` // locked/reserved balance (USD)
	} `json:"data"`
}

func (a *Adapter) doGet(ctx context.Context, path, bearer string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.BaseURL+path, nil)
	if err != nil {
		return 0, nil, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body, nil
}

func endpoint(path string, status int, err error, at time.Time) domain.EndpointStatus {
	es := domain.EndpointStatus{Path: path, HTTPStatus: status, AttemptedAt: at}
	if err != nil {
		es.Error = err.Error()
	}
	es.OK = err == nil && status >= 200 && status < 300
	return es
}

// noRatioSourceErr returns the "no ratio source" error, wrapped with
// domain.ErrAuthFailed when any probed authenticated endpoint returned 401/403
// (so the scheduler can emit endpoint_auth_failed alerts for sub2api too).
func noRatioSourceErr(stationID string, caps domain.CapabilityReport) error {
	base := fmt.Errorf("站点 %s 无可用倍率源（无 sk-key/JWT，或 billing 不可用）", stationID)
	for _, e := range caps.Endpoints {
		if e.HTTPStatus == 401 || e.HTTPStatus == 403 {
			return fmt.Errorf("%w：端点 %s 返回 %d（鉴权失败）: %w", base, e.Path, e.HTTPStatus, domain.ErrAuthFailed)
		}
	}
	return base
}

// ProbeCapabilities discovers which endpoints/auth succeed.
func (a *Adapter) ProbeCapabilities(ctx context.Context) (domain.CapabilityReport, error) {
	caps := domain.CapabilityReport{StationID: a.StationID, Kind: domain.KindSub2API}
	now := a.nowTime()

	if a.APIKey != "" {
		if status, _, err := a.doGet(ctx, "/v1/sub2api/billing", a.APIKey); err == nil {
			caps.HasBilling = status == 200
			if status == 404 {
				caps.SimpleMode = true
			}
			caps.Endpoints = append(caps.Endpoints, endpoint("/v1/sub2api/billing", status, nil, now))
		}
	}
	if a.AdminAPIKey != "" {
		if status, _, err := a.doGet(ctx, "/api/v1/admin/groups", a.AdminAPIKey); err == nil {
			caps.HasAdminGroups = status == 200
			caps.Endpoints = append(caps.Endpoints, endpoint("/api/v1/admin/groups", status, nil, now))
		}
	}
	if a.JWT != "" {
		if status, _, err := a.jwtGet(ctx, "/api/v1/groups/available"); err == nil {
			caps.HasUserGroups = status == 200
			caps.Endpoints = append(caps.Endpoints, endpoint("/api/v1/groups/available", status, nil, now))
		}
		if status, _, err := a.jwtGet(ctx, "/api/v1/channels/available"); err == nil {
			caps.HasUserChannels = status == 200
			caps.Endpoints = append(caps.Endpoints, endpoint("/api/v1/channels/available", status, nil, now))
		}
		// /api/v1/user/profile (user JWT) — wallet balance. balance is the
		// spendable wallet amount (USD); frozen_balance is reserved/locked credit,
		// NOT consumed usage, so it is NOT mapped into QuotaUsed (the Used KPI
		// would otherwise mislabel reserved funds as consumed spend). /profile
		// exposes no consumed-usage total, so Used stays 0. A wallet has no fixed
		// limit, so Total stays 0. No QuotaPerUnit conversion — balance is USD.
		if status, body, err := a.jwtGet(ctx, "/api/v1/user/profile"); err == nil && status == 200 {
			var pr userProfileResp
			if json.Unmarshal(body, &pr) == nil && pr.Code == 0 {
				caps.HasQuota = true
				caps.QuotaRemaining = pr.Data.Balance
			}
			caps.Endpoints = append(caps.Endpoints, endpoint("/api/v1/user/profile", status, nil, now))
		}
	}
	return caps, nil
}

// FetchRatios picks available sources, fetches, normalizes.
func (a *Adapter) FetchRatios(ctx context.Context, caps domain.CapabilityReport) (domain.RawSnapshot, []domain.RatioObservation, error) {
	snap := domain.RawSnapshot{
		StationID: a.StationID, ObservedAt: a.nowTime(), Capabilities: caps,
		RawPayloads: map[string][]byte{},
	}
	if !caps.HasBilling && !caps.SimpleMode && !caps.HasUserChannels {
		return snap, nil, noRatioSourceErr(a.StationID, caps)
	}

	data := normalize.Sub2APIRatioData{SimpleMode: caps.SimpleMode}
	var effective float64
	var peakInfo string
	var loc *time.Location // station tz for peak-window math (from billing)
	src := "/v1/sub2api/billing"

	if caps.HasBilling || caps.SimpleMode {
		status, body, err := a.doGet(ctx, "/v1/sub2api/billing", a.APIKey)
		if err != nil {
			return snap, nil, err
		}
		if status == 200 {
			snap.RawPayloads["/v1/sub2api/billing"] = body
			var br billingResp
			if err := json.Unmarshal(body, &br); err != nil {
				return snap, nil, err
			}
			effective = br.EffectiveRateMultiplier
			peakInfo = formatPeak(&br)
			if br.Timezone != nil && *br.Timezone != "" {
				if l, lerr := time.LoadLocation(*br.Timezone); lerr == nil {
					loc = l
				}
			}
			// Record the group rate multiplier so the dashboard shows the station's
			// core group ratio even when no per-model source yields models (e.g.
			// available-channels feature flag off + /v1/models gated by balance).
			// Group ratio is the project's most important data point.
			grp := a.Group
			if grp == "" {
				grp = "default"
			}
			if br.GroupRateMultiplier > 0 {
				snap.GroupRatios = map[string]float64{grp: br.GroupRateMultiplier}
			}
		}
	}

	// Per-group ratios from /api/v1/groups/available (user JWT). This is the
	// richest group-ratio source for a non-admin customer: it returns every
	// group the user can see with its rate_multiplier — no available-channels
	// feature flag, no balance check. Overrides the billing-derived single
	// group ratio (a strict superset: it includes the key's group too).
	if caps.HasUserGroups {
		status, body, err := a.jwtGet(ctx, "/api/v1/groups/available")
		if err == nil && status == 200 {
			snap.RawPayloads["/api/v1/groups/available"] = body
			snap.EndpointsUsed = append(snap.EndpointsUsed, "/api/v1/groups/available")
			var gr groupsAvailableResp
			if json.Unmarshal(body, &gr) == nil && len(gr.Data) > 0 {
				out := make(map[string]float64, len(gr.Data))
				for _, g := range gr.Data {
					if g.RateMultiplier > 0 {
						out[g.Name] = g.RateMultiplier
					}
				}
				if len(out) > 0 {
					snap.GroupRatios = out
				}
			}
		}
	}

	// Base prices from channels/available (per-model input/output/cache per-token USD).
	if caps.HasUserChannels {
		status, body, err := a.jwtGet(ctx, "/api/v1/channels/available")
		if err == nil && status == 200 {
			snap.RawPayloads["/api/v1/channels/available"] = body
			snap.EndpointsUsed = append(snap.EndpointsUsed, "/api/v1/channels/available")
			var cr channelsAvailableResp
			if json.Unmarshal(body, &cr) == nil {
				data.Models = a.modelsFromChannels(cr.Data, effective, peakInfo, loc)
			}
		}
	}
	// Fallback: if channels gave no models, use /v1/models + vendored LiteLLM
	// base prices so USD/1M is still derivable (effective multiplier from billing).
	if len(data.Models) == 0 && a.APIKey != "" {
		if status, body, err := a.doGet(ctx, "/v1/models", a.APIKey); err == nil && status == 200 {
			snap.RawPayloads["/v1/models"] = body
			snap.EndpointsUsed = append(snap.EndpointsUsed, "/v1/models")
			var ml modelsListResp
			if json.Unmarshal(body, &ml) == nil {
				data.Models = a.modelsFromLiteLLM(ml.Data, effective, peakInfo)
			}
		}
	}
	if caps.HasBilling || caps.SimpleMode {
		snap.EndpointsUsed = append([]string{src}, snap.EndpointsUsed...)
	}

	// When channels/available carried no per-platform groups[] (feature flag
	// off or older sub2api), all models land in a single group priced at
	// billing effective. If /api/v1/groups/available separately reported
	// multiple groups, expand each model into per-group rows so the dashboard
	// shows the discounted price for each group.
	if len(snap.GroupRatios) > 1 && allSameGroup(data.Models) {
		data.Models = expandByGroupRatios(data.Models, snap.GroupRatios)
	}

	obs := normalize.Sub2APINormalize(data)
	for i := range obs {
		obs[i].StationID = a.StationID
		obs[i].ObservedAt = snap.ObservedAt
		obs[i].SourceEndpoint = "/v1/sub2api/billing"
	}
	return snap, obs, nil
}

func formatPeak(br *billingResp) string {
	if !br.PeakRateEnabled || br.PeakStart == nil || br.PeakEnd == nil {
		return ""
	}
	mult := ""
	if br.PeakRateMultiplier != nil {
		mult = fmt.Sprintf("x%v", *br.PeakRateMultiplier)
	}
	return fmt.Sprintf("peak %s-%s %s", *br.PeakStart, *br.PeakEnd, mult)
}

// modelsFromChannels flattens channel→platforms→supported_models into
// per-(model, group) entries, priced at EACH group's own rate_multiplier × peak.
//
// sub2api decouples price from discount: channels/available carries the RAW
// LiteLLM per-token price on supported_models[].pricing and the group's
// rate_multiplier (the discount) as a sibling field on the platform's groups[].
// Folding the discount in per-group is what makes a discounted group surface a
// discounted USD/1M instead of the original LiteLLM price. First occurrence wins
// per (model, group) on duplicates.
//
// Degraded fallback: a platform section without groups[] (older / non-contract
// payload) falls back to the monitoring sk-key's billing effective for a.Group,
// preserving the legacy single-row behavior.
func (a *Adapter) modelsFromChannels(channels []availableChannel, effective float64, peakInfo string, loc *time.Location) []normalize.Sub2APIModel {
	seen := make(map[string]bool)
	out := []normalize.Sub2APIModel{}
	now := a.nowTime()
	for _, ch := range channels {
		for _, p := range ch.Platforms {
			for _, m := range p.SupportedModels {
				base := channelModelBase(m)
				if len(p.Groups) == 0 {
					grp := a.Group
					if grp == "" {
						grp = "default"
					}
					key := m.Name + "\x00" + grp
					if seen[key] {
						continue
					}
					seen[key] = true
					sm := base
					sm.Name, sm.Group = m.Name, grp
					sm.ResolvedRateMultiplier = effective
					sm.AppliedPeakMultiplier = 1.0
					sm.PeakInfo = peakInfo
					out = append(out, sm)
					continue
				}
				for _, g := range p.Groups {
					if g.RateMultiplier <= 0 {
						continue
					}
					key := m.Name + "\x00" + g.Name
					if seen[key] {
						continue
					}
					seen[key] = true
					peak := peakMultiplierAt(now, g, loc)
					sm := base
					sm.Name, sm.Group = m.Name, g.Name
					sm.ResolvedRateMultiplier = g.RateMultiplier
					sm.AppliedPeakMultiplier = peak
					sm.PeakInfo = formatPeakGroup(g)
					out = append(out, sm)
				}
			}
		}
	}
	return out
}

// channelModelBase extracts the per-token USD base prices from a supported
// model's pricing into a Sub2APIModel shell (Name/Group/multipliers set by the
// caller). BasePriceKnown is true only when an input price was reported, which
// gates whether USD/1M is derivable at all.
func channelModelBase(m supportedModel) normalize.Sub2APIModel {
	sm := normalize.Sub2APIModel{}
	if m.Pricing != nil {
		if m.Pricing.InputPrice != nil {
			sm.InputCostPerToken = *m.Pricing.InputPrice
		}
		if m.Pricing.OutputPrice != nil {
			sm.OutputCostPerToken = *m.Pricing.OutputPrice
		}
		if m.Pricing.CacheReadPrice != nil {
			sm.CacheReadCostPerToken = *m.Pricing.CacheReadPrice
		}
		if m.Pricing.CacheWritePrice != nil {
			sm.CacheWriteCostPerToken = *m.Pricing.CacheWritePrice
		}
		sm.BasePriceKnown = m.Pricing.InputPrice != nil
	}
	return sm
}

// peakMultiplierAt mirrors sub2api Group.PeakMultiplierAt: peak only applies to
// subscription-type groups during the same-day [PeakStart, PeakEnd) window
// (HH:MM, evaluated in the station's timezone). Returns 1.0 outside the window
// or for non-subscription / non-peak groups. (sub2api normalizes peak config so
// only subscription groups can carry an enabled peak; the subscription gate here
// is belt-and-suspenders.)
func peakMultiplierAt(now time.Time, g availableGroupRef, loc *time.Location) float64 {
	if g.SubscriptionType != "subscription" || !g.PeakRateEnabled || g.PeakStart == "" || g.PeakEnd == "" {
		return 1.0
	}
	start, ok1 := parseHHMM(g.PeakStart)
	end, ok2 := parseHHMM(g.PeakEnd)
	if !ok1 || !ok2 || start >= end {
		return 1.0
	}
	if loc == nil {
		loc = time.Local
	}
	t := now.In(loc)
	cur := t.Hour()*60 + t.Minute()
	if cur >= start && cur < end {
		return g.PeakRateMultiplier
	}
	return 1.0
}

// parseHHMM parses "HH:MM" into minutes-of-day. Mirrors sub2api parseMinutes.
func parseHHMM(hhmm string) (int, bool) {
	colon := strings.IndexByte(hhmm, ':')
	if (colon != 1 && colon != 2) || len(hhmm)-colon-1 != 2 {
		return 0, false
	}
	h := 0
	for i := 0; i < colon; i++ {
		d := hhmm[i] - '0'
		if d > 9 {
			return 0, false
		}
		h = h*10 + int(d)
	}
	m1, m2 := hhmm[colon+1]-'0', hhmm[colon+2]-'0'
	if m1 > 9 || m2 > 9 {
		return 0, false
	}
	m := int(m1)*10 + int(m2)
	if h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// formatPeakGroup renders a group ref's peak window for the PeakInfo note.
func formatPeakGroup(g availableGroupRef) string {
	if g.SubscriptionType != "subscription" || !g.PeakRateEnabled || g.PeakStart == "" || g.PeakEnd == "" {
		return ""
	}
	return fmt.Sprintf("peak %s-%s x%v", g.PeakStart, g.PeakEnd, g.PeakRateMultiplier)
}

// allSameGroup returns true when every model sits in one group (the no-groups
// fallback path). Used to decide whether expandByGroupRatios should fire.
func allSameGroup(models []normalize.Sub2APIModel) bool {
	if len(models) == 0 {
		return false
	}
	g := models[0].Group
	for _, m := range models[1:] {
		if m.Group != g {
			return false
		}
	}
	return true
}

// expandByGroupRatios replicates a flat model list (all in one fallback group)
// into per-group rows using group ratios from /api/v1/groups/available. Each
// model gets one row per group, with ResolvedRateMultiplier set to that group's
// rate. This covers the common case where channels/available omits groups[]
// (feature flag off) but groups/available has the full discount map.
func expandByGroupRatios(models []normalize.Sub2APIModel, groupRatios map[string]float64) []normalize.Sub2APIModel {
	out := make([]normalize.Sub2APIModel, 0, len(models)*len(groupRatios))
	for _, m := range models {
		for gName, gRate := range groupRatios {
			row := m
			row.Group = gName
			row.ResolvedRateMultiplier = gRate
			out = append(out, row)
		}
	}
	return out
}
