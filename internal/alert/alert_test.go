package alert

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"transitmonitor/internal/domain"
)

var alertTime = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

func ce(field string, pct float64) domain.ChangeEvent {
	return domain.ChangeEvent{
		StationID: "s1", Group: "default", Model: "gpt-4o",
		Field: field, DeltaPct: pct, Severity: "critical", ObservedAt: alertTime,
	}
}

func TestEvaluate_DeltaPct(t *testing.T) {
	rules := []Rule{{Name: "r", Type: RuleDeltaPct, Threshold: 10, Enabled: true}}
	events := []domain.ChangeEvent{
		{Field: domain.FieldInput, DeltaPct: 25, StationID: "s1", Model: "m", Severity: "critical", ObservedAt: alertTime},
		{Field: domain.FieldInput, DeltaPct: 3, StationID: "s1", Model: "m2", Severity: "info", ObservedAt: alertTime},
	}
	got := Evaluate(rules, events, nil)
	if len(got) != 1 {
		t.Fatalf("want 1 alert, got %d", len(got))
	}
	if got[0].Model != "m" {
		t.Errorf("matched wrong event: %+v", got[0])
	}
}

func TestEvaluate_ModelAdded(t *testing.T) {
	rules := []Rule{{Name: "r", Type: RuleModelAdded, Enabled: true}}
	events := []domain.ChangeEvent{
		{Field: domain.FieldPresence, New: "added", StationID: "s1", Model: "new-model", ObservedAt: alertTime},
	}
	got := Evaluate(rules, events, nil)
	if len(got) != 1 {
		t.Fatalf("want 1 alert, got %d", len(got))
	}
}

func TestEvaluate_ProbeMarkup(t *testing.T) {
	rules := []Rule{{Name: "r", Type: RuleProbeMarkupPct, Threshold: 5, Enabled: true}}
	probes := []domain.ProbeResult{
		{StationID: "s1", Model: "gpt-4o", MarkupPct: 12, ObservedAt: alertTime},
		{StationID: "s1", Model: "gpt-4o-mini", MarkupPct: 2, ObservedAt: alertTime},
	}
	got := Evaluate(rules, nil, probes)
	if len(got) != 1 {
		t.Fatalf("want 1 alert, got %d", len(got))
	}
	if got[0].Model != "gpt-4o" {
		t.Errorf("matched wrong probe: %+v", got[0])
	}
}

func TestEvaluate_DisabledRule(t *testing.T) {
	rules := []Rule{{Name: "r", Type: RuleDeltaPct, Threshold: 1, Enabled: false}}
	events := []domain.ChangeEvent{ce(domain.FieldInput, 50)}
	if got := Evaluate(rules, events, nil); len(got) != 0 {
		t.Errorf("disabled rule must not fire, got %d", len(got))
	}
}

func TestSignDingTalk(t *testing.T) {
	secret := "SECtest123"
	ts := int64(1234567890000)
	// Independently compute expected.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("1234567890000\nSECtest123"))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	got := SignDingTalk(secret, ts)
	if got != want {
		t.Errorf("sign: want %q got %q", want, got)
	}
}

func TestWebhookNotifier_Send(t *testing.T) {
	var got map[string]any
	var method, ct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, ct = r.Method, r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	w := &WebhookNotifier{URL: srv.URL}
	ev := AlertEvent{Rule: "r", StationID: "s1", Model: "m", Severity: "critical",
		Payload: map[string]any{"station": "s1", "model": "m", "delta_pct": 25.0}}
	if err := w.Send(context.Background(), ev); err != nil {
		t.Fatalf("send: %v", err)
	}
	if method != http.MethodPost {
		t.Errorf("method: want POST got %s", method)
	}
	if ct != "application/json" {
		t.Errorf("content-type: %s", ct)
	}
	if got["model"] != "m" || got["delta_pct"].(float64) != 25 {
		t.Errorf("payload: %+v", got)
	}
}

func TestDingTalkNotifier_Send(t *testing.T) {
	var reqURL, ct string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqURL = r.URL.String()
		ct = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	d := NewDingTalk(srv.URL, "SEC123", srv.Client())
	d.SetClock(func() time.Time { return alertTime })
	ev := AlertEvent{Rule: "r", StationID: "s1", Model: "gpt-4o", Severity: "critical", Payload: map[string]any{"x": 1}}
	if err := d.Send(context.Background(), ev); err != nil {
		t.Fatalf("send: %v", err)
	}
	if ct != "application/json" {
		t.Errorf("content-type: %s", ct)
	}
	if !contains(reqURL, "timestamp=") || !contains(reqURL, "sign=") {
		t.Errorf("URL must contain timestamp & sign: %s", reqURL)
	}
	if body["msgtype"] != "markdown" {
		t.Errorf("msgtype: want markdown got %v", body["msgtype"])
	}
}

func TestSinkNotifier(t *testing.T) {
	s := &SinkNotifier{}
	ev := AlertEvent{Rule: "r"}
	_ = s.Send(context.Background(), ev)
	_ = s.Send(context.Background(), ev)
	if len(s.Sent) != 2 {
		t.Errorf("sink: want 2 sent got %d", len(s.Sent))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
