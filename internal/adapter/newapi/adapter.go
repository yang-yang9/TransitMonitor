// Package newapi implements adapter.Adapter for QuantumNous/new-api stations.
//
// Fallback chain (mirrors new-api's own controller/ratio_sync.go contract):
//
//	/api/status (public, currency+self-use context)  → always
//	/api/ratio_config (gated by ExposeRatioEnabled)    → richest, 403 → fallback
//	/api/pricing (public by default)                   → per-model []Pricing
//	/api/user/self/groups (PAT)                         → per-user group ratios (override)
//
// Spec: openspec/.../specs/ratio-collection-newapi/spec.md
package newapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"transitmonitor/internal/domain"
	"transitmonitor/internal/normalize"
)

const defaultQuotaPerUnit = 500000.0

// Adapter scrapapes a new-api station.
type Adapter struct {
	StationID string
	BaseURL   string
	PAT       string // system access token (UserAuth/Admin/Root) for /api/user/self/groups
	UserID    string // New-Api-User header value (required by some new-api forks alongside PAT)
	APIKey    string // sk- key for /v1/* + probe (unused by FetchRatios)
	Group     string // group to observe
	Client    *http.Client
	now       func() time.Time
}

// New constructs a new-api adapter. group defaults to "default".
func New(stationID, baseURL, pat, userID, apiKey, group string, client *http.Client) *Adapter {
	if client == nil {
		client = http.DefaultClient
	}
	if group == "" {
		group = "default"
	}
	return &Adapter{
		StationID: stationID, BaseURL: baseURL, PAT: pat, UserID: userID, APIKey: apiKey,
		Group: group, Client: client, now: time.Now,
	}
}

// SetClock injects a clock (for tests).
func (a *Adapter) SetClock(f func() time.Time) { a.now = f }

func (a *Adapter) nowTime() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// --- response shapes (verified against new-api source; see docs/upstream-contract.md) ---

type statusResp struct {
	Success bool `json:"success"`
	Data    struct {
		QuotaPerUnit       float64 `json:"quota_per_unit"`
		SelfUseModeEnabled bool    `json:"self_use_mode_enabled"`
		USDExchangeRate    float64 `json:"usd_exchange_rate"`
	} `json:"data"`
}

type pricingItem struct {
	ModelName        string   `json:"model_name"`
	QuotaType        int      `json:"quota_type"`
	ModelRatio       float64  `json:"model_ratio"`
	ModelPrice       float64  `json:"model_price"`
	CompletionRatio  float64  `json:"completion_ratio"`
	CacheRatio       *float64 `json:"cache_ratio"`
	CreateCacheRatio *float64 `json:"create_cache_ratio"`
	EnableGroup      []string `json:"enable_groups"`
}

type pricingResp struct {
	Success    bool               `json:"success"`
	Data       []pricingItem      `json:"data"`
	GroupRatio map[string]float64 `json:"group_ratio"`
}

type ratioConfigData struct {
	ModelRatio       map[string]float64 `json:"model_ratio"`
	CompletionRatio  map[string]float64 `json:"completion_ratio"`
	CacheRatio       map[string]float64 `json:"cache_ratio"`
	CreateCacheRatio map[string]float64 `json:"create_cache_ratio"`
	ModelPrice       map[string]float64 `json:"model_price"`
	GroupRatio       map[string]float64 `json:"group_ratio"`
}

type ratioConfigResp struct {
	Success bool            `json:"success"`
	Data    ratioConfigData `json:"data"`
}

type optionEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type userSelfResp struct {
	Success bool `json:"success"`
	Data    struct {
		Quota     float64 `json:"quota"`
		UsedQuota float64 `json:"used_quota"`
	} `json:"data"`
}

type optionResp struct {
	Success bool          `json:"success"`
	Data    []optionEntry `json:"data"`
}

type userGroupsResp struct {
	Success bool `json:"success"`
	Data    map[string]struct {
		Ratio float64 `json:"ratio"`
	} `json:"data"`
}

// doGet issues a GET and returns status + body. Non-200 is NOT an error here;
// callers decide based on status (e.g. 403 ratio_config → fall back to pricing).
func (a *Adapter) doGet(ctx context.Context, path, bearer string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.BaseURL+path, nil)
	if err != nil {
		return 0, nil, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
		// Some new-api forks require New-Api-User alongside the PAT for
		// dashboard endpoints (/api/pricing, /api/user/self, /api/option).
		if bearer == a.PAT && a.UserID != "" {
			req.Header.Set("New-Api-User", a.UserID)
		}
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

// authStatusErr returns a fetch error for a non-200 ratio endpoint. 401/403 are
// wrapped with domain.ErrAuthFailed so the scheduler can emit
// endpoint_auth_failed alerts; other statuses are plain errors.
func authStatusErr(src string, status int) error {
	if status == 401 || status == 403 {
		return fmt.Errorf("倍率源 %s 返回 HTTP %d（鉴权失败）: %w", src, status, domain.ErrAuthFailed)
	}
	return fmt.Errorf("倍率源 %s 返回 HTTP %d", src, status)
}

// ProbeCapabilities discovers which endpoints/auth succeed.
func (a *Adapter) ProbeCapabilities(ctx context.Context) (domain.CapabilityReport, error) {
	caps := domain.CapabilityReport{
		StationID: a.StationID, Kind: domain.KindNewAPI, QuotaPerUnit: defaultQuotaPerUnit,
	}
	now := a.nowTime()

	// /api/status (public): currency + self-use context.
	if status, body, err := a.doGet(ctx, "/api/status", ""); err == nil {
		caps.HasStatus = status == 200
		caps.Endpoints = append(caps.Endpoints, endpoint("/api/status", status, nil, now))
		if status == 200 {
			var sr statusResp
			if json.Unmarshal(body, &sr) == nil {
				if sr.Data.QuotaPerUnit > 0 {
					caps.QuotaPerUnit = sr.Data.QuotaPerUnit
				}
				caps.SelfUseMode = sr.Data.SelfUseModeEnabled
				caps.USDExchangeRate = sr.Data.USDExchangeRate
			}
		}
	}

	// /api/ratio_config (gated by ExposeRatioEnabled; 403 when off).
	if status, _, err := a.doGet(ctx, "/api/ratio_config", ""); err == nil {
		caps.HasRatioConfig = status == 200
		caps.Endpoints = append(caps.Endpoints, endpoint("/api/ratio_config", status, nil, now))
	}

	// /api/pricing (public by default; some stations set requireAuth=true → retry with PAT).
	if status, _, err := a.doGet(ctx, "/api/pricing", ""); err == nil {
		if status == 401 && a.PAT != "" {
			// pricing requires auth — retry with PAT
			status, _, err = a.doGet(ctx, "/api/pricing", a.PAT)
		}
		caps.HasPricing = status == 200
		caps.Endpoints = append(caps.Endpoints, endpoint("/api/pricing", status, nil, now))
	}

	// /api/user/self (PAT) — quota/balance.
	if a.PAT != "" {
		if status, body, err := a.doGet(ctx, "/api/user/self", a.PAT); err == nil && status == 200 {
			var us userSelfResp
			if json.Unmarshal(body, &us) == nil {
				caps.HasQuota = true
				caps.QuotaTotal = us.Data.Quota
				caps.QuotaRemaining = us.Data.Quota - us.Data.UsedQuota
				caps.QuotaUsed = us.Data.UsedQuota
			}
		}
	}
	// /api/option/ (root PAT — richest source, returns all ratio maps as JSON strings).
	if a.PAT != "" {
		if status, _, err := a.doGet(ctx, "/api/option/", a.PAT); err == nil {
			caps.HasOption = status == 200
			caps.Endpoints = append(caps.Endpoints, endpoint("/api/option/", status, nil, now))
		}
	}
	// /api/user/self/groups (PAT).
	if a.PAT != "" {
		if status, _, err := a.doGet(ctx, "/api/user/self/groups", a.PAT); err == nil {
			caps.HasUserGroups = status == 200
			caps.Endpoints = append(caps.Endpoints, endpoint("/api/user/self/groups", status, nil, now))
		}
	}

	return caps, nil
}

// FetchRatios picks the richest available ratio source, fetches, normalizes.
func (a *Adapter) FetchRatios(ctx context.Context, caps domain.CapabilityReport) (domain.RawSnapshot, []domain.RatioObservation, error) {
	snap := domain.RawSnapshot{
		StationID: a.StationID, ObservedAt: a.nowTime(), Capabilities: caps,
		RawPayloads: map[string][]byte{},
	}
	data := normalize.NewAPIRatioData{QuotaPerUnit: caps.QuotaPerUnit, SelfUseMode: caps.SelfUseMode}

	var src string
	switch {
	case caps.HasPricing:
		// /api/pricing returns only models with enabled channels (what the station actually serves).
		src = "/api/pricing"
		authBearer := ""
		if a.PAT != "" {
			authBearer = a.PAT
		}
		status, body, err := a.doGet(ctx, src, authBearer)
		if err != nil {
			return snap, nil, err
		}
		if status != 200 {
			return snap, nil, authStatusErr("pricing", status)
		}
		snap.RawPayloads[src] = body
		var pr pricingResp
		if err := json.Unmarshal(body, &pr); err != nil {
			return snap, nil, err
		}
		data.TopGroupRatio = pr.GroupRatio
		data.Models = a.modelsFromPricing(pr.Data, caps.SelfUseMode, pr.GroupRatio)
	case caps.HasRatioConfig:
		src = "/api/ratio_config"
		status, body, err := a.doGet(ctx, src, "")
		if err != nil {
			return snap, nil, err
		}
		if status != 200 {
			return snap, nil, authStatusErr("ratio_config", status)
		}
		snap.RawPayloads[src] = body
		var rc ratioConfigResp
		if err := json.Unmarshal(body, &rc); err != nil {
			return snap, nil, err
		}
		data.TopGroupRatio = rc.Data.GroupRatio
		data.Models = a.modelsFromMaps(rc.Data)
	case caps.HasOption:
		src = "/api/option/"
		status, body, err := a.doGet(ctx, src, a.PAT)
		if err != nil {
			return snap, nil, err
		}
		if status != 200 {
			return snap, nil, authStatusErr("option", status)
		}
		snap.RawPayloads[src] = body
		var or optionResp
		if err := json.Unmarshal(body, &or); err != nil {
			return snap, nil, err
		}
		// Parse the JSON-string values into ratio maps (same structure as ratio_config)
		var rc ratioConfigData
		for _, kv := range or.Data {
			switch kv.Key {
			case "ModelRatio":
				_ = json.Unmarshal([]byte(kv.Value), &rc.ModelRatio)
			case "CompletionRatio":
				_ = json.Unmarshal([]byte(kv.Value), &rc.CompletionRatio)
			case "CacheRatio":
				_ = json.Unmarshal([]byte(kv.Value), &rc.CacheRatio)
			case "CreateCacheRatio":
				_ = json.Unmarshal([]byte(kv.Value), &rc.CreateCacheRatio)
			case "ModelPrice":
				_ = json.Unmarshal([]byte(kv.Value), &rc.ModelPrice)
			case "GroupRatio":
				_ = json.Unmarshal([]byte(kv.Value), &rc.GroupRatio)
			}
		}
		data.TopGroupRatio = rc.GroupRatio
		data.Models = a.modelsFromMaps(rc)
	default:
		// No ratio source at all (e.g. pricing auth-gated + ratio_config off).
		return snap, nil, fmt.Errorf("站点 %s 无可用倍率源（/api/pricing 与 /api/ratio_config 均不可用；请检查凭据/网络，或确认站是否开启 ExposeRatioEnabled）", a.StationID)
	}
	snap.EndpointsUsed = []string{src}

	// /api/ratio_config and /api/option return every CONFIGURED model — new-api's
	// full built-in default ratio map (defaultModelRatio, ~2500 entries on a stock
	// install) plus admin overrides — NOT the models the station actually serves.
	// Filter by /v1/models (sk- key) to keep only enabled-channel models. /api/pricing
	// already returns only enabled-channel models, so it needs no filtering.
	enabledFilterApplied := false
	if src != "/api/pricing" && a.APIKey != "" {
		if status, body, err := a.doGet(ctx, "/v1/models", a.APIKey); err == nil && status == 200 {
			type modelsResp struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			var mr modelsResp
			if json.Unmarshal(body, &mr) == nil {
				enabled := make(map[string]bool, len(mr.Data))
				for _, m := range mr.Data {
					enabled[m.ID] = true
				}
				filtered := data.Models[:0]
				for _, m := range data.Models {
					if enabled[m.Name] {
						filtered = append(filtered, m)
					}
				}
				data.Models = filtered
				enabledFilterApplied = true
				snap.EndpointsUsed = append(snap.EndpointsUsed, "/v1/models")
			}
		}
	}

	// Refuse to record when the only ratio source is ratio_config/option AND no
	// enabled-filter applied. Otherwise we would persist new-api's entire built-in
	// default model list (the "2000+ models" that are really just new-api's stock
	// catalog, most of which the station never enabled) as if it were the station's
	// real catalog — flooding the store and firing spurious model_added/removed
	// alerts. The enabled set is only knowable via /api/pricing (needs the pricing
	// module public, or a PAT that satisfies its UserAuth gate) or /v1/models
	// (sk- key). Point the operator at the fix instead of silently ingesting junk.
	if src != "/api/pricing" && !enabledFilterApplied {
		reason := "未配置 api_key"
		switch {
		case a.APIKey != "":
			reason = "当前 api_key 无法访问 /v1/models（key 无效或端点被站限制）"
		case a.PAT == "" && a.APIKey == "":
			// No creds at all — almost always an encKey mismatch (stored creds
			// failed to decrypt and Auth loaded empty), not a station genuinely
			// configured credential-free. Name the real cause so the operator
			// fixes the key instead of re-entering creds that are already there.
			reason = "凭据未加载 — 站点的加密凭据解密失败（TRANSMONITOR_ENCRYPTION_KEY 不匹配）或从未录入；请确认 key 与加站时一致，或重新录入凭据"
		default:
			// A PAT is present but /api/pricing still gated (401) and no api_key:
			// the PAT alone was insufficient. Say so, instead of the generic
			// "no api_key" that implies the operator configured nothing.
			reason = "未配置 api_key（已配置的 PAT 未能解锁 /api/pricing，只有 sk- key 能驱动 /v1/models 过滤）"
		}
		return snap, nil, fmt.Errorf(
			"倍率源 %s 暴露了 %d 个已配置模型（含 new-api 内置默认倍率表 built-in default，多数并非本站实际启用），"+
				"无法确定本站实际启用的模型集（%s）。"+
				"修复（任选其一）：在本站配置填 PAT（new-api 系统访问令牌 — 解锁 /api/pricing，仅返回已启用渠道的模型）；"+
				"或填有效的 api_key（sk- key — 启用 /v1/models 过滤）；"+
				"或在 new-api 站自己的后台设 pricing.requireAuth=false（站点 %s — 让 /api/pricing 公开，无需 PAT）。"+
				"在此之前不记录观测。",
			src, len(data.Models), reason, a.StationID)
	}

	// Per-user group ratios override the top-level group_ratio.
	if a.PAT != "" {
		if status, body, err := a.doGet(ctx, "/api/user/self/groups", a.PAT); err == nil && status == 200 {
			var ug userGroupsResp
			if json.Unmarshal(body, &ug) == nil {
				m := make(map[string]float64, len(ug.Data))
				for g, v := range ug.Data {
					m[g] = v.Ratio
				}
				data.UserGroupRatio = m
				snap.EndpointsUsed = append(snap.EndpointsUsed, "/api/user/self/groups")
			}
		}
	}

	// Persist group_ratios in the snapshot for dashboard display.
	// User group ratios override top-level ones (same priority as normalize).
	if len(data.TopGroupRatio) > 0 || len(data.UserGroupRatio) > 0 {
		gr := make(map[string]float64)
		for k, v := range data.TopGroupRatio {
			gr[k] = v
		}
		for k, v := range data.UserGroupRatio {
			gr[k] = v
		}
		snap.GroupRatios = gr
	}

	obs := normalize.NewAPINormalize(data)
	for i := range obs {
		obs[i].StationID = a.StationID
		obs[i].ObservedAt = snap.ObservedAt
		obs[i].SourceEndpoint = src
	}
	return snap, obs, nil
}

func (a *Adapter) modelsFromPricing(items []pricingItem, selfUse bool, topGroupRatio map[string]float64) []normalize.NewAPIModel {
	out := make([]normalize.NewAPIModel, 0, len(items))
	for _, p := range items {
		// /api/pricing does not expose whether a model's ratio is configured vs
		// the self-use 37.5 fallback. Under self-use mode, treat 37.5 as the
		// sentinel (unconfigured); confirming a real 37.5 would need
		// /api/ratio_config or /api/option (typically unavailable here).
		known := !(selfUse && p.ModelRatio == normalize.SelfUseSentinelRatio)
		base := normalize.NewAPIModel{
			Name: p.ModelName, QuotaType: p.QuotaType,
			ModelRatio: p.ModelRatio, ModelPrice: p.ModelPrice,
			CacheRatio: p.CacheRatio, CreateCacheRatio: p.CreateCacheRatio,
			Group: a.Group, KnownRatio: known,
		}
		if p.CompletionRatio != 0 { // 0 → absent → normalize infers 1.0
			cr := p.CompletionRatio
			base.CompletionRatio = &cr
		}
		// Group=="*" → one observation per group the model is enabled for
		// (intersected with group_ratio), so default/vip prices are captured
		// separately. Any other Group → observe just that group.
		for _, g := range a.groupsFor(p.EnableGroup, topGroupRatio) {
			m := base
			m.Group = g
			out = append(out, m)
		}
	}
	return out
}

// groupsFor returns the groups to emit observations for. With a.Group set (and
// not "*"), it is just that group. With "*", it expands across enable_groups ∩
// group_ratio (or all group_ratio keys if enable_groups contains "all"); falls
// back to "default" when nothing is known.
func (a *Adapter) groupsFor(enable []string, topGroupRatio map[string]float64) []string {
	if a.Group != "" && a.Group != "*" {
		return []string{a.Group}
	}
	hasAll := false
	for _, g := range enable {
		if g == "all" {
			hasAll = true
			break
		}
	}
	if hasAll {
		out := make([]string, 0, len(topGroupRatio))
		for g := range topGroupRatio {
			out = append(out, g)
		}
		if len(out) == 0 {
			return []string{"default"}
		}
		return out
	}
	var hit []string
	for _, g := range enable {
		if _, ok := topGroupRatio[g]; ok {
			hit = append(hit, g)
		}
	}
	if len(hit) > 0 {
		return hit
	}
	if len(enable) > 0 {
		return enable // station-named groups absent from group_ratio → resolve to 1.0
	}
	return []string{"default"}
}

func (a *Adapter) modelsFromMaps(d ratioConfigData) []normalize.NewAPIModel {
	out := make([]normalize.NewAPIModel, 0, len(d.ModelRatio)+len(d.ModelPrice))
	seen := make(map[string]bool, len(d.ModelRatio)+len(d.ModelPrice))
	for name, mr := range d.ModelRatio {
		m := normalize.NewAPIModel{Name: name, QuotaType: 0, ModelRatio: mr, Group: a.Group, KnownRatio: true}
		if cr, ok := d.CompletionRatio[name]; ok && cr != 0 {
			m.CompletionRatio = &cr
		}
		if cv, ok := d.CacheRatio[name]; ok {
			m.CacheRatio = &cv
		}
		if cc, ok := d.CreateCacheRatio[name]; ok {
			m.CreateCacheRatio = &cc
		}
		out = append(out, m)
		seen[name] = true
	}
	for name, price := range d.ModelPrice { // pure fixed-price models live only in model_price
		if seen[name] || price <= 0 {
			continue
		}
		out = append(out, normalize.NewAPIModel{
			Name: name, QuotaType: 1, ModelPrice: price, Group: a.Group, KnownRatio: true,
		})
	}
	return out
}
