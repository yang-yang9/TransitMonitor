// Package alert evaluates rules against change events / probe results and
// dispatches notifications (DingTalk HMAC-signed markdown + generic webhook).
// Spec: openspec/.../specs/alerting/spec.md.
package alert

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"transitmonitor/internal/domain"
)

// Rule types.
const (
	RuleDeltaPct           = "delta_pct"
	RuleDeltaAbs           = "delta_abs"
	RuleModelAdded         = "model_added"
	RuleModelRemoved       = "model_removed"
	RuleProbeMarkupPct     = "probe_markup_pct"
	RuleEndpointAuthFail   = "endpoint_auth_failed"
	RulePollFailureStreak  = "poll_failure_streak"
	RuleGroupRatioDeltaPct = "group_ratio_delta_pct"
	RuleQuotaBelow         = "quota_below"    // remaining balance (USD) below threshold
	RuleQuotaDropPct       = "quota_drop_pct" // remaining balance dropped ≥ threshold % vs previous poll
)

// Direction values for delta-based rules.
const (
	DirBoth = "both"
	DirUp   = "up"
	DirDown = "down"
)

// ValidRuleTypes enumerates all recognized rule type strings.
var ValidRuleTypes = map[string]bool{
	RuleDeltaPct: true, RuleDeltaAbs: true,
	RuleModelAdded: true, RuleModelRemoved: true,
	RuleProbeMarkupPct: true, RuleEndpointAuthFail: true,
	RulePollFailureStreak: true, RuleGroupRatioDeltaPct: true,
	RuleQuotaBelow: true, RuleQuotaDropPct: true,
}

// ValidDirections enumerates all recognized direction strings (empty = both).
var ValidDirections = map[string]bool{"": true, DirBoth: true, DirUp: true, DirDown: true}

// Rule is an alerting rule.
type Rule struct {
	Name      string  `yaml:"name" json:"name"`
	Type      string  `yaml:"type" json:"type"`
	Threshold float64 `yaml:"threshold" json:"threshold"`
	Direction string  `yaml:"direction" json:"direction"`
	Enabled   bool    `yaml:"enabled" json:"enabled"`
}

func matchDirection(dir string, signedDelta float64) bool {
	switch dir {
	case DirUp:
		return signedDelta > 0
	case DirDown:
		return signedDelta < 0
	default:
		return true
	}
}

// AlertEvent is a fired alert awaiting/after delivery.
type AlertEvent struct {
	Rule      string
	Type      string
	StationID string
	Model     string
	Severity  string
	Payload   map[string]any
	Message   string // fully-formatted body; notifiers use it if non-empty
	CreatedAt time.Time
	Sent      bool
	Error     string
}

// Evaluate matches rules against change events and probe results.
// (endpoint_auth_failed / poll_failure_streak are emitted directly by the
// scheduler/probe orchestrator, not here.)
func Evaluate(rules []Rule, events []domain.ChangeEvent, probes []domain.ProbeResult) []AlertEvent {
	var out []AlertEvent
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		switch r.Type {
		case RuleDeltaPct:
			for _, e := range events {
				if (e.Field == domain.FieldInput || e.Field == "input_usd_per_1m" ||
					e.Field == domain.FieldOutput || e.Field == "output_usd_per_1m" ||
					e.Field == domain.FieldNative || e.Field == "native_ratio") &&
					abs(e.DeltaPct) >= r.Threshold && matchDirection(r.Direction, e.DeltaPct) {
					out = append(out, fromChange(r, e, map[string]any{"delta_pct": e.DeltaPct, "delta_abs": e.DeltaAbs}))
				}
			}
		case RuleDeltaAbs:
			for _, e := range events {
				if abs(e.DeltaAbs) >= r.Threshold && matchDirection(r.Direction, e.DeltaAbs) {
					out = append(out, fromChange(r, e, map[string]any{"delta_abs": e.DeltaAbs}))
				}
			}
		case RuleGroupRatioDeltaPct:
			for _, e := range events {
				if e.Field == domain.FieldGroupRatio && abs(e.DeltaPct) >= r.Threshold && matchDirection(r.Direction, e.DeltaPct) {
					out = append(out, fromChange(r, e, map[string]any{
						"group": e.Group, "delta_pct": e.DeltaPct, "delta_abs": e.DeltaAbs,
					}))
				}
			}
		case RuleModelAdded:
			for _, e := range events {
				if e.Field == domain.FieldPresence && e.New == "added" {
					out = append(out, fromChange(r, e, map[string]any{"change": "model_added"}))
				}
			}
		case RuleModelRemoved:
			for _, e := range events {
				if e.Field == domain.FieldPresence && e.New == "removed" {
					out = append(out, fromChange(r, e, map[string]any{"change": "model_removed"}))
				}
			}
		case RuleProbeMarkupPct:
			for _, p := range probes {
				if abs(p.MarkupPct) >= r.Threshold && matchDirection(r.Direction, p.MarkupPct) {
					out = append(out, AlertEvent{
						Rule: r.Name, Type: r.Type, StationID: p.StationID, Model: p.Model,
						Severity:  "warning",
						Payload:   map[string]any{"markup_pct": p.MarkupPct, "measured_usd_per_1m": p.MeasuredUSDPer1M, "declared_usd_per_1m": p.DeclaredEffectiveUSDPer1M, "cost_usd": p.CostUSD},
						CreatedAt: p.ObservedAt,
					})
				}
			}
		}
	}
	for i := range out {
		out[i].Message = FormatEvent(out[i])
	}
	return out
}

func fromChange(r Rule, e domain.ChangeEvent, extra map[string]any) AlertEvent {
	payload := map[string]any{
		"station": e.StationID, "model": e.Model, "field": e.Field,
		"old": e.Old, "new": e.New, "delta_pct": e.DeltaPct, "severity": e.Severity,
		"observed_at": e.ObservedAt,
	}
	for k, v := range extra {
		payload[k] = v
	}
	return AlertEvent{
		Rule: r.Name, Type: r.Type, StationID: e.StationID, Model: e.Model,
		Severity: e.Severity, Payload: payload, CreatedAt: e.ObservedAt,
	}
}

// FormatEvent renders a concise, human-readable body for an alert event,
// specialized per rule type. Leads with an emoji + label + station + model,
// followed by the key change. Used as the notification body (and concatenated
// in digest mode).
func FormatEvent(ev AlertEvent) string {
	sval := func(k string) string { // payload value as string
		if v, ok := ev.Payload[k]; ok {
			return fmt.Sprint(v)
		}
		return ""
	}
	num := func(s string) string { // tidy numeric (≤4 decimals)
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return s
		}
		return strconv.FormatFloat(f, 'f', 4, 64)
	}
	pct := func(k string) string { // signed percentage, 1 decimal
		f, err := strconv.ParseFloat(sval(k), 64)
		if err != nil {
			return sval(k)
		}
		sign := ""
		if f >= 0 {
			sign = "+"
		}
		return sign + strconv.FormatFloat(f, 'f', 1, 64)
	}
	fieldLabel := func(f string) string {
		switch f {
		case "input_usd_per_1m":
			return "输入价"
		case "output_usd_per_1m":
			return "输出价"
		case "native_ratio":
			return "原生倍率"
		default:
			return f
		}
	}
	switch ev.Type {
	case RuleDeltaPct, RuleDeltaAbs:
		return fmt.Sprintf("🚨 价格变动 · %s · %s · 规则「%s」\n%s: %s → %s (%s%%)  [%s]",
			ev.StationID, ev.Model, ev.Rule, fieldLabel(sval("field")), num(sval("old")), num(sval("new")), pct("delta_pct"), ev.Severity)
	case RuleGroupRatioDeltaPct:
		return fmt.Sprintf("🚨 分组倍率变动 · %s · 分组 %s · 规则「%s」\n%s → %s (%s%%)  [%s]",
			ev.StationID, sval("group"), ev.Rule, sval("old"), sval("new"), pct("delta_pct"), ev.Severity)
	case RuleModelAdded:
		return fmt.Sprintf("➕ 模型新增 · %s · %s · 规则「%s」  [%s]", ev.StationID, ev.Model, ev.Rule, ev.Severity)
	case RuleModelRemoved:
		return fmt.Sprintf("⚠️ 模型下架 · %s · %s · 规则「%s」  [%s]", ev.StationID, ev.Model, ev.Rule, ev.Severity)
	case RuleProbeMarkupPct:
		return fmt.Sprintf("🚨 暗中加价 · %s · %s · 规则「%s」\n实测 $%s/M vs 声明 $%s/M (markup %s%%)  [%s]",
			ev.StationID, ev.Model, ev.Rule, num(sval("measured_usd_per_1m")), num(sval("declared_usd_per_1m")), pct("markup_pct"), ev.Severity)
	case RuleQuotaBelow:
		return fmt.Sprintf("🚨 余额不足 · %s · 规则「%s」\n剩余 $%s < 阈值 $%s  [%s]",
			ev.StationID, ev.Rule, num(sval("remaining_usd")), num(sval("threshold_usd")), ev.Severity)
	case RuleQuotaDropPct:
		return fmt.Sprintf("🚨 余额下降 · %s · 规则「%s」\n$%s → $%s (↓%s%%)  [%s]",
			ev.StationID, ev.Rule, num(sval("prev_usd")), num(sval("remaining_usd")), num(sval("drop_pct")), ev.Severity)
	case RuleEndpointAuthFail:
		return fmt.Sprintf("🚨 鉴权失败 · %s · 规则「%s」\n%s  [%s]", ev.StationID, ev.Rule, sval("error"), ev.Severity)
	case RulePollFailureStreak:
		return fmt.Sprintf("🚨 连续轮询失败 · %s · 规则「%s」\n连续 %s 次: %s  [%s]",
			ev.StationID, ev.Rule, sval("streak"), sval("error"), ev.Severity)
	default:
		return fmt.Sprintf("[%s] %s · %s · %s  [%s]", ev.Rule, ev.Type, ev.StationID, ev.Model, ev.Severity)
	}
}

// StationAlertOverride holds per-station overrides for alert behavior.
// Nil pointer fields mean "inherit from global".
type StationAlertOverride struct {
	CooldownMinutes       *int           `json:"cooldown_minutes,omitempty"`
	DigestIntervalMinutes *int           `json:"digest_interval_minutes,omitempty"`
	RuleOverrides         []RuleOverride `json:"rule_overrides,omitempty"`
}

// RuleOverride overrides a single global rule's fields for one station.
type RuleOverride struct {
	Name      string   `json:"name"`
	Threshold *float64 `json:"threshold,omitempty"`
	Direction *string  `json:"direction,omitempty"`
	Enabled   *bool    `json:"enabled,omitempty"`
}

// MergeRules returns a copy of global with per-station overrides applied.
// Overrides match by Name; unmatched overrides are ignored, unmatched globals
// pass through unchanged.
func MergeRules(global []Rule, overrides []RuleOverride) []Rule {
	if len(overrides) == 0 {
		out := make([]Rule, len(global))
		copy(out, global)
		return out
	}
	idx := make(map[string]RuleOverride, len(overrides))
	for _, o := range overrides {
		idx[o.Name] = o
	}
	out := make([]Rule, len(global))
	for i, r := range global {
		out[i] = r
		if o, ok := idx[r.Name]; ok {
			if o.Threshold != nil {
				out[i].Threshold = *o.Threshold
			}
			if o.Direction != nil {
				out[i].Direction = *o.Direction
			}
			if o.Enabled != nil {
				out[i].Enabled = *o.Enabled
			}
		}
	}
	return out
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// Notifier delivers AlertEvents.
type Notifier interface {
	Send(ctx context.Context, ev AlertEvent) error
}

// Dispatcher fans an AlertEvent out to all notifiers, recording per-notifier
// success. It does NOT retry here (retry is the store's AlertEvent queue job).
type Dispatcher struct{ Notifiers []Notifier }

func (d *Dispatcher) Send(ctx context.Context, ev AlertEvent) error {
	var firstErr error
	for _, n := range d.Notifiers {
		if err := n.Send(ctx, ev); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SinkNotifier collects sent events in memory (for tests).
type SinkNotifier struct {
	Sent []AlertEvent
}

func (s *SinkNotifier) Send(_ context.Context, ev AlertEvent) error {
	s.Sent = append(s.Sent, ev)
	return nil
}

// WebhookNotifier POSTs a JSON payload to a generic webhook.
type WebhookNotifier struct {
	URL    string
	Client *http.Client
}

func (w *WebhookNotifier) Send(ctx context.Context, ev AlertEvent) error {
	client := w.Client
	if client == nil {
		client = http.DefaultClient
	}
	// Augment the payload with a rendered message so webhook consumers get the
	// same human-readable body as the chat notifiers.
	payload := ev.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	if ev.Message != "" {
		payload["message"] = ev.Message
	}
	payload["rule"] = ev.Rule
	payload["station_id"] = ev.StationID
	payload["severity"] = ev.Severity
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("webhook: status %d", resp.StatusCode)
	}
	return nil
}

// SignDingTalk computes the DingTalk webhook signature for a (secret,timestamp).
// stringToSign = timestamp + "\n" + secret; sign = base64(HMAC-SHA256(key=secret, msg)).
func SignDingTalk(secret string, timestamp int64) string {
	stringToSign := fmt.Sprintf("%d\n%s", timestamp, secret)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// DingTalkNotifier sends a markdown message to a DingTalk webhook (signed).
type DingTalkNotifier struct {
	WebhookURL string // base URL (without query)
	Secret     string
	Client     *http.Client
	now        func() time.Time
}

// NewDingTalk constructs a DingTalk notifier.
func NewDingTalk(webhookURL, secret string, client *http.Client) *DingTalkNotifier {
	return &DingTalkNotifier{WebhookURL: webhookURL, Secret: secret, Client: client, now: time.Now}
}

// SetClock injects a clock (tests).
func (d *DingTalkNotifier) SetClock(f func() time.Time) { d.now = f }

func (d *DingTalkNotifier) Send(ctx context.Context, ev AlertEvent) error {
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	ts := d.now().UnixMilli()
	sign := SignDingTalk(d.Secret, ts)
	u, err := url.Parse(d.WebhookURL)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("timestamp", strconv.FormatInt(ts, 10))
	q.Set("sign", sign)
	u.RawQuery = q.Encode()

	title := fmt.Sprintf("TransitMonitor %s: %s", ev.Severity, ev.Rule)
	text := ev.Message
	if text == "" {
		text = fmt.Sprintf("### %s\n- station: %s\n- model: %s\n- severity: %s\n- payload: `%v`",
			ev.Rule, ev.StationID, ev.Model, ev.Severity, ev.Payload)
	}
	body, _ := json.Marshal(map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"title": title, "text": text},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("dingtalk: status %d", resp.StatusCode)
	}
	return nil
}

// SignLark computes the Lark/飞书 webhook signature.
// Lark signing: hmac_sha256(key=timestamp+"\n"+secret, msg="") → base64.
func SignLark(secret string, timestamp int64) string {
	mac := hmac.New(sha256.New, []byte(fmt.Sprintf("%d\n%s", timestamp, secret)))
	mac.Write([]byte{})
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// LarkNotifier sends a text message to a Lark/飞书 custom bot webhook (signed).
type LarkNotifier struct {
	WebhookURL string
	Secret     string
	Client     *http.Client
	now        func() time.Time
}

func NewLark(webhookURL, secret string, client *http.Client) *LarkNotifier {
	if client == nil {
		client = http.DefaultClient
	}
	return &LarkNotifier{WebhookURL: webhookURL, Secret: secret, Client: client, now: time.Now}
}

func (l *LarkNotifier) SetClock(f func() time.Time) { l.now = f }

func (l *LarkNotifier) Send(ctx context.Context, ev AlertEvent) error {
	ts := l.now().Unix()
	text := ev.Message
	if text == "" {
		text = fmt.Sprintf("[%s] %s\nstation: %s\nmodel: %s\npayload: %v", ev.Severity, ev.Rule, ev.StationID, ev.Model, ev.Payload)
	}
	body := map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
	if l.Secret != "" {
		body["timestamp"] = strconv.FormatInt(ts, 10)
		body["sign"] = SignLark(l.Secret, ts)
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, l.WebhookURL, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("lark: status %d", resp.StatusCode)
	}
	return nil
}

// SlackNotifier sends a message to a Slack incoming webhook.
type SlackNotifier struct {
	WebhookURL string
	Client     *http.Client
}

func (s *SlackNotifier) Send(ctx context.Context, ev AlertEvent) error {
	c := s.Client
	if c == nil {
		c = http.DefaultClient
	}
	text := ev.Message
	if text == "" {
		text = fmt.Sprintf("[%s] %s — station: %s, model: %s", ev.Severity, ev.Rule, ev.StationID, ev.Model)
	}
	b, _ := json.Marshal(map[string]string{"text": text})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.WebhookURL, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("slack: status %d", resp.StatusCode)
	}
	return nil
}

// NotifierConfig is the JSON-serializable configuration for all notifier kinds.
// It mirrors config.AlertsConfig's notifier blocks so it can round-trip through
// the encrypted store row and the /settings form. Empty fields → notifier skipped.
type NotifierConfig struct {
	DingTalk struct {
		Webhook string `json:"webhook"`
		Secret  string `json:"secret"`
	} `json:"dingtalk"`
	Webhook struct {
		URL string `json:"url"`
	} `json:"webhook"`
	Lark struct {
		Webhook string `json:"webhook"`
		Secret  string `json:"secret"`
	} `json:"lark"`
	Slack struct {
		Webhook string `json:"webhook"`
	} `json:"slack"`
	QQ struct {
		AppID       string `json:"app_id"`
		AppSecret   string `json:"app_secret"`
		GroupOpenID string `json:"group_openid"`
	} `json:"qq"`
}

// BuildDispatcher assembles a *Dispatcher (or nil if no notifier is configured)
// from a NotifierConfig. client may be nil → http.DefaultClient. Used by main at
// startup and by the scheduler when reloading notifiers from the /settings page.
func BuildDispatcher(nc NotifierConfig, client *http.Client) Notifier {
	if client == nil {
		client = http.DefaultClient
	}
	var ns []Notifier
	if nc.DingTalk.Webhook != "" {
		ns = append(ns, NewDingTalk(nc.DingTalk.Webhook, nc.DingTalk.Secret, client))
	}
	if nc.Webhook.URL != "" {
		ns = append(ns, &WebhookNotifier{URL: nc.Webhook.URL, Client: client})
	}
	if nc.Lark.Webhook != "" {
		ns = append(ns, NewLark(nc.Lark.Webhook, nc.Lark.Secret, client))
	}
	if nc.Slack.Webhook != "" {
		ns = append(ns, &SlackNotifier{WebhookURL: nc.Slack.Webhook, Client: client})
	}
	if nc.QQ.AppID != "" && nc.QQ.AppSecret != "" && nc.QQ.GroupOpenID != "" {
		ns = append(ns, NewQQ(nc.QQ.AppID, nc.QQ.AppSecret, nc.QQ.GroupOpenID, client))
	}
	if len(ns) == 0 {
		return nil
	}
	return &Dispatcher{Notifiers: ns}
}

// qqBaseURL / qqTokenURL are vars so tests can point them at an httptest server.
var (
	qqTokenURL = "https://bots.qq.com/app/getAppAccessToken"
	qqBaseURL  = "https://api.sgroup.qq.com"
)

// QQNotifier sends a message to a QQ group via the official Bot OpenAPI v2.
// Auth uses AppID+AppSecret → access_token (cached, refreshed 60s before expiry),
// then POST /v2/groups/{group_openid}/messages with Authorization: QQBot {token}.
//
// NOTE: proactive group pushes may require the bot to have 主动消息 permission on
// the QQ open platform; the call is issued as documented regardless.
type QQNotifier struct {
	AppID, AppSecret, GroupOpenID string
	Client                        *http.Client
	now                           func() time.Time

	mu       sync.Mutex
	token    string
	expireAt time.Time
}

// NewQQ constructs a QQ bot notifier.
func NewQQ(appID, appSecret, groupOpenID string, client *http.Client) *QQNotifier {
	return &QQNotifier{AppID: appID, AppSecret: appSecret, GroupOpenID: groupOpenID, Client: client, now: time.Now}
}

// SetClock injects a clock (tests).
func (q *QQNotifier) SetClock(f func() time.Time) { q.now = f }

type qqTokenResp struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// accessToken returns a valid cached token, refreshing via getAppAccessToken
// when empty or within 60s of expiry.
func (q *QQNotifier) accessToken(ctx context.Context) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.token != "" && q.now().Before(q.expireAt.Add(-60*time.Second)) {
		return q.token, nil
	}
	c := q.Client
	if c == nil {
		c = http.DefaultClient
	}
	body, _ := json.Marshal(map[string]string{"appId": q.AppID, "clientSecret": q.AppSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, qqTokenURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("qq access_token: status %d: %s", resp.StatusCode, string(rb))
	}
	var tr qqTokenResp
	if err := json.Unmarshal(rb, &tr); err != nil {
		return "", fmt.Errorf("qq access_token: decode: %w", err)
	}
	if tr.AccessToken == "" {
		return "", errors.New("qq access_token: empty token in response")
	}
	exp := time.Duration(tr.ExpiresIn) * time.Second
	if exp <= 0 {
		exp = 7200 * time.Second
	}
	q.token = tr.AccessToken
	q.expireAt = q.now().Add(exp)
	return q.token, nil
}

func (q *QQNotifier) invalidateToken() {
	q.mu.Lock()
	q.token = ""
	q.expireAt = time.Time{}
	q.mu.Unlock()
}

func (q *QQNotifier) Send(ctx context.Context, ev AlertEvent) error {
	c := q.Client
	if c == nil {
		c = http.DefaultClient
	}
	text := ev.Message
	if text == "" {
		text = fmt.Sprintf("[%s] %s\nstation: %s\nmodel: %s\npayload: %v", ev.Severity, ev.Rule, ev.StationID, ev.Model, ev.Payload)
	}
	return q.postMessage(ctx, c, text, true)
}

func (q *QQNotifier) postMessage(ctx context.Context, c *http.Client, text string, allowRetry bool) error {
	tok, err := q.accessToken(ctx)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"content": text, "msg_type": 0})
	u := qqBaseURL + "/v2/groups/" + url.PathEscape(q.GroupOpenID) + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "QQBot "+tok)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	// 401 → token stale; invalidate, refetch, retry once.
	if resp.StatusCode == 401 && allowRetry {
		q.invalidateToken()
		return q.postMessage(ctx, c, text, false)
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("qq send: status %d: %s", resp.StatusCode, truncateErr(rb))
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("qq send: status %d: %s", resp.StatusCode, truncateErr(rb))
	}
	return nil
}

func truncateErr(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
