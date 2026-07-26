// Package domain defines the core, I/O-free domain types shared across
// TransitMonitor packages. Types here carry no behavior — they are the model.
// All behavior (normalization, change-detection, probe reconciliation) lives
// in dedicated packages and is test-driven from the SDD specs.
package domain

import (
	"errors"
	"fmt"
	"time"
)

// ErrAuthFailed is wrapped by an adapter's FetchRatios when a previously-OK
// authenticated endpoint returns 401/403 (bad/expired key). The scheduler
// matches it with errors.Is to emit an endpoint_auth_failed alert. It lives in
// domain (not the adapter package) to avoid an import cycle: adapter imports
// the newapi/sub2api subpackages, which would otherwise have to import adapter.
var ErrAuthFailed = errors.New("endpoint auth failed")

// StationKind identifies the upstream relay-station implementation.
type StationKind string

const (
	KindNewAPI  StationKind = "newapi"
	KindSub2API StationKind = "sub2api"
)

// ChangeEvent.Field values (shared by changedet & alert).
const (
	FieldInput        = "input_usd_per_1m"
	FieldOutput       = "output_usd_per_1m"
	FieldNative       = "native_ratio"
	FieldPresence     = "presence"
	FieldSentinelFlip = "sentinel_flip"
	FieldGroupRatio   = "group_ratio"
)

// Severity values.
const (
	SevInfo     = "info"
	SevWarning  = "warning"
	SevCritical = "critical"
)

// AuthConfig holds the credentials a station needs, by kind.
// All fields are secrets; never log or persist in plaintext (see internal/secrets).
type AuthConfig struct {
	// new-api
	PAT    string `yaml:"pat,omitempty" json:"-"`     // system access token (UserAuth/Admin/Root)
	UserID string `yaml:"user_id,omitempty" json:"-"` // New-Api-User header value (required by some forks)
	APIKey string `yaml:"api_key,omitempty" json:"-"` // sk- key for /v1/* + probe
	// sub2api
	AdminAPIKey string `yaml:"admin_api_key,omitempty" json:"-"` // x-api-key admin mode
	AdminEmail  string `yaml:"admin_email,omitempty" json:"-"`   // JWT admin login (optional)
	AdminPass   string `yaml:"admin_pass,omitempty" json:"-"`    // JWT admin password (optional)
	JWT         string `yaml:"jwt,omitempty" json:"-"`           // pre-issued admin/user JWT (optional)
	// common
	Group string `yaml:"group,omitempty" json:"-"` // group to impersonate/observe
}

// ProbeConfig governs the real-cost probe for a station.
type ProbeConfig struct {
	Enabled            bool     `yaml:"enabled"`
	Model              string   `yaml:"model"`  // single-model shorthand (used if Models empty)
	Models             []string `yaml:"models"` // multi-model probe list (probes each per cycle)
	MaxInputTokens     int      `yaml:"max_input_tokens"`
	MaxOutputTokens    int      `yaml:"max_output_tokens"`
	MaxCostCentsPerRun int      `yaml:"max_cost_cents_per_run"` // refuse probe if declared cost exceeds this
	DryRun             bool     `yaml:"dry_run"`                // compute declared cost, do NOT send
	PromptSeed         string   `yaml:"prompt_seed"`            // randomized per-call to avoid cache hits
	Interval           Duration `yaml:"interval"`               // dedicated probe cadence (independent of poll_interval). 0 = piggyback on poll (backward compat).
}

// TargetModels returns the models to probe: Models if set, else [Model].
func (p ProbeConfig) TargetModels() []string {
	if len(p.Models) > 0 {
		return p.Models
	}
	if p.Model != "" {
		return []string{p.Model}
	}
	return nil
}

// Station is a registered monitoring target.
type Station struct {
	ID           string      `yaml:"id" json:"id"`
	Name         string      `yaml:"name" json:"name"`
	BaseURL      string      `yaml:"base_url" json:"base_url"`
	Kind         StationKind `yaml:"kind" json:"kind"`
	Auth         AuthConfig  `yaml:"auth" json:"auth"`
	PollInterval Duration    `yaml:"poll_interval" json:"poll_interval"`
	Probe        ProbeConfig `yaml:"probe" json:"probe"`
	Tags         []string    `yaml:"tags" json:"tags"`
	Enabled      bool        `yaml:"enabled" json:"enabled"`
	SortOrder    int         `yaml:"sort_order" json:"sort_order"`

	// DecryptFailed marks a station whose encrypted credentials could not be
	// decrypted at load time (TRANSMONITOR_ENCRYPTION_KEY mismatch). Transient:
	// not persisted (json/yaml:"-"), cleared on re-save via the web UI. Surfaced
	// in the dashboard as a red badge so the operator re-enters credentials
	// instead of chasing a misleading downstream "no api_key" error.
	DecryptFailed bool `yaml:"-" json:"-"`
}

// Duration is a time.Duration that (un)marshals from YAML as a Go duration string (e.g. "3m").
type Duration time.Duration

// UnmarshalYAML parses a duration string. Uses the v2-compatible signature so
// the domain package need not import yaml.
func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	if s == "" {
		*d = 0
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// EndpointStatus records the outcome of one endpoint probe.
type EndpointStatus struct {
	Path        string    `json:"path"`
	HTTPStatus  int       `json:"http_status"` // 0 = not attempted
	OK          bool      `json:"ok"`
	Error       string    `json:"error,omitempty"`
	AttemptedAt time.Time `json:"attempted_at"`
}

// CapabilityReport summarizes which station endpoints/auth succeeded, so the
// monitor can degrade gracefully (e.g. ratio_config 403 → fall back to pricing).
type CapabilityReport struct {
	StationID        string
	Kind             StationKind
	Endpoints        []EndpointStatus
	HasStatus        bool
	HasRatioConfig   bool // new-api: /api/ratio_config 200
	HasPricing       bool // new-api: /api/pricing 200
	HasOption        bool // new-api: /api/option 200 (RootAuth)
	HasUserGroups    bool // new-api: /api/user/self/groups 200
	HasBilling       bool // sub2api: /v1/sub2api/billing 200 (false in simple mode)
	HasAdminGroups   bool
	HasAdminChannels bool
	HasUserChannels  bool
	SimpleMode       bool
	HasQuota         bool // a balance/quota source succeeded
	QuotaRemaining   float64
	QuotaUsed        float64 // sub2api RunMode=simple
	QuotaTotal       float64 // total quota/limit as reported (0 = unknown or unlimited — see UnlimitedQuota)
	UnlimitedQuota   bool    // true when the upstream reports no limit (sub2api quota=0)
	SelfUseMode      bool    // new-api self_use_mode_enabled
	QuotaPerUnit     float64
	USDExchangeRate  float64
}

// RawSnapshot is a point-in-time capture of a station's raw ratio data.
type RawSnapshot struct {
	StationID        string
	ObservedAt       time.Time
	EndpointsUsed    []string
	EndpointStatuses []EndpointStatus
	RawPayloads      map[string][]byte // path → raw body
	Capabilities     CapabilityReport
	GroupRatios      map[string]float64 // group_name → ratio (from /api/pricing or /api/user/self/groups)
}

// GroupRatioSnapshot is one point in a station's group-ratio time series
// (reconstructed from snapshots.capabilities) — used for trend sparklines.
type GroupRatioSnapshot struct {
	ObservedAt time.Time
	Ratios     map[string]float64
}

// BalanceObservation is one point in a station's balance/quota time series.
// Raw fields carry the upstream's native units (new-api: internal quota units;
// sub2api: USD); the USD fields are the normalized, cross-station-comparable
// values used by the dashboard, alerts, and /metrics.
type BalanceObservation struct {
	StationID       string
	ObservedAt      time.Time
	Remaining       float64 // raw remaining as reported (new-api quota units; sub2api USD)
	Used            float64 // raw used as reported
	Total           float64 // raw total/limit as reported (0 = unknown or unlimited)
	RemainingUSD    float64 // normalized to USD
	UsedUSD         float64
	TotalUSD        float64 // 0 = unknown or unlimited
	Unlimited       bool    // upstream reports no quota limit
	Currency        string  // "quota" (new-api internal units) | "USD" (sub2api)
	QuotaPerUnit    float64 // new-api units-per-USD context (0 = n/a)
	USDExchangeRate float64 // new-api display exchange-rate context
	SourceEndpoint  string
}

// NewBalanceFromCaps builds a BalanceObservation from a capability report,
// normalizing the upstream-native quota fields to USD. new-api quota is in
// internal units (QuotaPerUnit units = $1); sub2api balance is already USD.
// qpu defaults to defaultQuotaPerUnit (500000) when the station didn't expose one.
func NewBalanceFromCaps(caps CapabilityReport, observedAt time.Time, sourceEndpoint string) BalanceObservation {
	const defaultQuotaPerUnit = 500000.0
	qpu := caps.QuotaPerUnit
	if qpu <= 0 {
		qpu = defaultQuotaPerUnit
	}
	ob := BalanceObservation{
		StationID: caps.StationID, ObservedAt: observedAt,
		Remaining: caps.QuotaRemaining, Used: caps.QuotaUsed, Total: caps.QuotaTotal,
		Unlimited: caps.UnlimitedQuota, QuotaPerUnit: qpu, USDExchangeRate: caps.USDExchangeRate,
		SourceEndpoint: sourceEndpoint,
	}
	switch caps.Kind {
	case KindSub2API:
		// sub2api /api/v1/user/profile reports balance in USD directly.
		ob.Currency = "USD"
		ob.RemainingUSD = caps.QuotaRemaining
		ob.UsedUSD = caps.QuotaUsed
		if caps.UnlimitedQuota {
			ob.TotalUSD = 0
		} else {
			ob.TotalUSD = caps.QuotaTotal
		}
	default:
		// new-api: quota units → USD via QuotaPerUnit.
		ob.Currency = "quota"
		ob.RemainingUSD = caps.QuotaRemaining / qpu
		ob.UsedUSD = caps.QuotaUsed / qpu
		if caps.UnlimitedQuota {
			ob.TotalUSD = 0
		} else {
			ob.TotalUSD = caps.QuotaTotal / qpu
		}
	}
	return ob
}

// RatioObservation is the normalized, comparable per-(station,group,model) record.
type RatioObservation struct {
	StationID           string
	GroupName           string
	ModelName           string
	NativeRatio         float64 // raw ratio/multiplier as reported
	NativeRatioKind     string  // "newapi_model_ratio" | "newapi_model_price" | "sub2api_rate_multiplier"
	QuotaType           int     // new-api: 0=per-token,1=fixed; sub2api: -1
	InputUSDPer1M       float64 // effective input USD/1M (normalized)
	OutputUSDPer1M      float64
	CacheReadUSDPer1M   float64
	CacheWriteUSDPer1M  float64
	FixedPriceUSD       float64 // per-call USD (QuotaType=1)
	CompletionRatio     float64
	PeakInfo            string
	Sentinel            string // non-derivable/excluded label, "" if normal
	Note                string // non-blocking annotation (e.g. "completion_ratio=inferred(1.0)")
	DeclaredUnavailable bool   // true if the station declares no multiplier (e.g. sub2api simple mode)
	ObservedAt          time.Time
	SourceEndpoint      string
}

// ChangeEvent records a detected change between two snapshots.
type ChangeEvent struct {
	StationID  string
	Group      string
	Model      string
	Field      string // "input_usd_per_1m" | "output_usd_per_1m" | "native_ratio" | "presence" | "sentinel_flip"
	Old        string
	New        string
	DeltaAbs   float64
	DeltaPct   float64
	ObservedAt time.Time
	Severity   string // "info" | "warning" | "critical"
}

// ProbeResult is the outcome of one real-cost probe.
type ProbeResult struct {
	StationID                 string
	Model                     string
	TokensIn                  int
	TokensOut                 int
	DeclaredNativeRatio       float64
	DeclaredEffectiveUSDPer1M float64
	MeasuredUSDPer1M          float64
	MarkupPct                 float64
	CostUSD                   float64
	DeclaredUnavailable       bool
	ObservedAt                time.Time
	Error                     string
}

// AuditEntry is one audit-log row (probe runs, config changes, startup, …).
type AuditEntry struct {
	ID     int64
	Ts     time.Time
	Actor  string // e.g. "scheduler", "probe", "main"
	Action string // e.g. "probe.run", "startup"
	Target string // e.g. station id / model
	Detail string // redacted of secrets by callers
}
