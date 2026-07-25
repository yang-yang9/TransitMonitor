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

func TestEvaluate_GroupRatioDeltaPct(t *testing.T) {
	rules := []Rule{{Name: "grp", Type: RuleGroupRatioDeltaPct, Threshold: 20, Enabled: true}}
	events := []domain.ChangeEvent{
		{Field: domain.FieldGroupRatio, Group: "vip", Old: "0.05", New: "0.09", DeltaPct: 80, StationID: "s1", Severity: "critical", ObservedAt: alertTime},
		{Field: domain.FieldGroupRatio, Group: "team", DeltaPct: 5, StationID: "s1", Severity: "info", ObservedAt: alertTime}, // below threshold
		{Field: domain.FieldInput, DeltaPct: 90, StationID: "s1", Model: "m", ObservedAt: alertTime},                          // wrong field
	}
	got := Evaluate(rules, events, nil)
	if len(got) != 1 {
		t.Fatalf("want 1 alert (only vip), got %d", len(got))
	}
	if got[0].Payload["group"] != "vip" {
		t.Errorf("alert should carry group=vip, got %v", got[0].Payload["group"])
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

func TestQQNotifier_Send(t *testing.T) {
	tokenHits := 0
	var sendAuth, sendPath, sendCT string
	var sendBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			tokenHits++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"TOK123","expires_in":7200}`))
		default: // "/v2/groups/{id}/messages"
			sendAuth = r.Header.Get("Authorization")
			sendPath = r.URL.Path
			sendCT = r.Header.Get("Content-Type")
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &sendBody)
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()
	qqTokenURL = srv.URL + "/app/getAppAccessToken"
	qqBaseURL = srv.URL

	q := NewQQ("app-1", "secret-1", "group-openid-x", srv.Client())
	q.SetClock(func() time.Time { return alertTime })
	ev := AlertEvent{Rule: "price-up", StationID: "s1", Model: "gpt-4o", Severity: "critical", Payload: map[string]any{"delta_pct": 25}}
	if err := q.Send(context.Background(), ev); err != nil {
		t.Fatalf("send: %v", err)
	}
	if sendAuth != "QQBot TOK123" {
		t.Errorf("Authorization: want %q got %q", "QQBot TOK123", sendAuth)
	}
	if sendCT != "application/json" {
		t.Errorf("content-type: %s", sendCT)
	}
	if !contains(sendPath, "/v2/groups/group-openid-x/messages") {
		t.Errorf("send path: %s", sendPath)
	}
	if mt, _ := sendBody["msg_type"].(float64); mt != 0 {
		t.Errorf("msg_type: want 0 got %v", sendBody["msg_type"])
	}
	if c, _ := sendBody["content"].(string); !contains(c, "price-up") {
		t.Errorf("content missing rule name: %q", c)
	}
	// token cached across two sends within expiry → only one token call.
	if err := q.Send(context.Background(), ev); err != nil {
		t.Fatalf("send2: %v", err)
	}
	if tokenHits != 1 {
		t.Errorf("token cached: want 1 token call got %d", tokenHits)
	}
}

func TestQQNotifier_401Retry(t *testing.T) {
	tokenHits := 0
	sendHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			tokenHits++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"TOK","expires_in":7200}`))
		default:
			sendHits++
			if sendHits == 1 {
				w.WriteHeader(401) // stale token → retry
				return
			}
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()
	qqTokenURL = srv.URL + "/app/getAppAccessToken"
	qqBaseURL = srv.URL

	q := NewQQ("app", "secret", "grp", srv.Client())
	q.SetClock(func() time.Time { return alertTime })
	if err := q.Send(context.Background(), AlertEvent{Rule: "r"}); err != nil {
		t.Fatalf("send after 401 retry: %v", err)
	}
	if tokenHits != 2 {
		t.Errorf("401 should force a token refresh: want 2 token calls got %d", tokenHits)
	}
	if sendHits != 2 {
		t.Errorf("401 should retry send once: want 2 send calls got %d", sendHits)
	}
}

func TestBuildDispatcher(t *testing.T) {
	// No notifiers → nil.
	if got := BuildDispatcher(NotifierConfig{}, http.DefaultClient); got != nil {
		t.Errorf("empty config should yield nil dispatcher, got %T", got)
	}
	var nc NotifierConfig
	nc.DingTalk.Webhook = "https://oapi.dingtalk.com/x"
	nc.QQ.AppID, nc.QQ.AppSecret, nc.QQ.GroupOpenID = "a", "b", "c"
	d := BuildDispatcher(nc, http.DefaultClient)
	dp, ok := d.(*Dispatcher)
	if !ok {
		t.Fatalf("want *Dispatcher got %T", d)
	}
	// 2 notifiers (dingtalk + qq); slack/lark/webhook empty → skipped.
	if len(dp.Notifiers) != 2 {
		t.Errorf("want 2 notifiers got %d", len(dp.Notifiers))
	}
	// Partial QQ config (missing group_openid) → qq skipped → only dingtalk.
	nc.QQ.GroupOpenID = ""
	d2 := BuildDispatcher(nc, http.DefaultClient)
	dp2, _ := d2.(*Dispatcher)
	if len(dp2.Notifiers) != 1 {
		t.Errorf("partial qq should be skipped: want 1 got %d", len(dp2.Notifiers))
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
