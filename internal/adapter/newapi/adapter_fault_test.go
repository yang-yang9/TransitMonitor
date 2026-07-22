package newapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchRatios_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()
	a := New("s1", srv.URL, "", "sk-test", "default", srv.Client())
	a.SetClock(func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) })
	caps, _ := a.ProbeCapabilities(context.Background())
	_, _, err := a.FetchRatios(context.Background(), caps)
	if err == nil {
		t.Error("malformed JSON should error")
	}
}

func TestFetchRatios_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	a := New("s1", srv.URL, "", "sk-test", "default", srv.Client())
	a.SetClock(func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) })
	caps, _ := a.ProbeCapabilities(context.Background())
	_, _, err := a.FetchRatios(context.Background(), caps)
	if err == nil {
		t.Error("500 should error on fetch")
	}
}

func TestProbeCapabilities_ConnectionRefused(t *testing.T) {
	a := New("s1", "http://127.0.0.1:1", "", "sk-test", "default", &http.Client{Timeout: 100 * time.Millisecond})
	caps, err := a.ProbeCapabilities(context.Background())
	// connection refused should not panic, should return caps with all-false
	if err != nil {
		t.Logf("probe returned err (expected for connection refused): %v", err)
	}
	if caps.HasStatus || caps.HasPricing {
		t.Error("connection refused should result in all-false capabilities")
	}
}
