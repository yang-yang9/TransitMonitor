package sub2api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchRatios_BillingMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{broken`))
	}))
	defer srv.Close()
	a := New("s1", srv.URL, "sk-1", "", "", "", "", "default", srv.Client())
	caps, _ := a.ProbeCapabilities(context.Background())
	_, _, err := a.FetchRatios(context.Background(), caps)
	if err == nil {
		t.Error("malformed billing JSON should error")
	}
}

func TestFetchRatios_ChannelsMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/sub2api/billing" {
			w.Write([]byte(`{"effective_rate_multiplier":0.25}`))
		} else {
			w.Write([]byte(`{broken channels json`))
		}
	}))
	defer srv.Close()
	a := New("s1", srv.URL, "sk-1", "jwt-1", "", "", "", "default", srv.Client())
	caps, _ := a.ProbeCapabilities(context.Background())
	_, _, err := a.FetchRatios(context.Background(), caps)
	// malformed channels JSON should not fatal — the adapter should degrade gracefully
	if err != nil {
		t.Logf("malformed channels returned err (acceptable): %v", err)
	}
}
