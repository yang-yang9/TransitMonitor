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
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"transitmonitor/internal/domain"
)

// Rule types.
const (
	RuleDeltaPct          = "delta_pct"
	RuleDeltaAbs          = "delta_abs"
	RuleModelAdded        = "model_added"
	RuleModelRemoved      = "model_removed"
	RuleProbeMarkupPct    = "probe_markup_pct"
	RuleEndpointAuthFail  = "endpoint_auth_failed"
	RulePollFailureStreak = "poll_failure_streak"
)

// Rule is an alerting rule.
type Rule struct {
	Name      string  `yaml:"name" json:"name"`
	Type      string  `yaml:"type" json:"type"`
	Threshold float64 `yaml:"threshold" json:"threshold"`
	Enabled   bool    `yaml:"enabled" json:"enabled"`
}

// AlertEvent is a fired alert awaiting/after delivery.
type AlertEvent struct {
	Rule      string
	StationID string
	Model     string
	Severity  string
	Payload   map[string]any
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
					abs(e.DeltaPct) >= r.Threshold {
					out = append(out, fromChange(r.Name, e, map[string]any{"delta_pct": e.DeltaPct, "delta_abs": e.DeltaAbs}))
				}
			}
		case RuleDeltaAbs:
			for _, e := range events {
				if abs(e.DeltaAbs) >= r.Threshold {
					out = append(out, fromChange(r.Name, e, map[string]any{"delta_abs": e.DeltaAbs}))
				}
			}
		case RuleModelAdded:
			for _, e := range events {
				if e.Field == domain.FieldPresence && e.New == "added" {
					out = append(out, fromChange(r.Name, e, map[string]any{"change": "model_added"}))
				}
			}
		case RuleModelRemoved:
			for _, e := range events {
				if e.Field == domain.FieldPresence && e.New == "removed" {
					out = append(out, fromChange(r.Name, e, map[string]any{"change": "model_removed"}))
				}
			}
		case RuleProbeMarkupPct:
			for _, p := range probes {
				if abs(p.MarkupPct) >= r.Threshold {
					out = append(out, AlertEvent{
						Rule: r.Name, StationID: p.StationID, Model: p.Model,
						Severity:  "warning",
						Payload:   map[string]any{"markup_pct": p.MarkupPct, "measured_usd_per_1m": p.MeasuredUSDPer1M, "declared_usd_per_1m": p.DeclaredEffectiveUSDPer1M, "cost_usd": p.CostUSD},
						CreatedAt: p.ObservedAt,
					})
				}
			}
		}
	}
	return out
}

func fromChange(rule string, e domain.ChangeEvent, extra map[string]any) AlertEvent {
	payload := map[string]any{
		"station": e.StationID, "model": e.Model, "field": e.Field,
		"old": e.Old, "new": e.New, "delta_pct": e.DeltaPct, "severity": e.Severity,
		"observed_at": e.ObservedAt,
	}
	for k, v := range extra {
		payload[k] = v
	}
	return AlertEvent{
		Rule: rule, StationID: e.StationID, Model: e.Model,
		Severity: e.Severity, Payload: payload, CreatedAt: e.ObservedAt,
	}
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
	body, err := json.Marshal(ev.Payload)
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
	text := fmt.Sprintf("### %s\n- station: %s\n- model: %s\n- severity: %s\n- payload: `%v`",
		ev.Rule, ev.StationID, ev.Model, ev.Severity, ev.Payload)
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
	text := fmt.Sprintf("[%s] %s\nstation: %s\nmodel: %s\npayload: %v", ev.Severity, ev.Rule, ev.StationID, ev.Model, ev.Payload)
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
	text := fmt.Sprintf("[%s] %s — station: %s, model: %s", ev.Severity, ev.Rule, ev.StationID, ev.Model)
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
