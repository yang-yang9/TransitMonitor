package jwtlogin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func makeJWT(exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`))
	claims := fmt.Sprintf(`{"exp":%d,"sub":"user@test.com"}`, exp)
	payload := base64.RawURLEncoding.EncodeToString([]byte(claims))
	sig := base64.RawURLEncoding.EncodeToString([]byte("fakesig"))
	return header + "." + payload + "." + sig
}

func makeJWTNoExp() string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user@test.com"}`))
	sig := base64.RawURLEncoding.EncodeToString([]byte("fakesig"))
	return header + "." + payload + "." + sig
}

func TestLogin_Success(t *testing.T) {
	exp := time.Now().Add(24 * time.Hour).Unix()
	token := makeJWT(exp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/auth/login" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Email != "admin@test.com" || req.Password != "pass123" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": token})
	}))
	defer srv.Close()

	got, gotExp, err := Login(context.Background(), srv.URL, "admin@test.com", "pass123", srv.Client())
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got != token {
		t.Fatalf("token mismatch: got %q", got)
	}
	if gotExp.Unix() != exp {
		t.Fatalf("exp mismatch: got %v, want %v", gotExp.Unix(), exp)
	}
}

func TestLogin_BadCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()

	_, _, err := Login(context.Background(), srv.URL, "bad@test.com", "wrong", srv.Client())
	if err == nil {
		t.Fatal("expected error for bad credentials")
	}
}

func TestLogin_2FA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer srv.Close()

	_, _, err := Login(context.Background(), srv.URL, "a@b.com", "p", srv.Client())
	if err == nil {
		t.Fatal("expected error for 2FA")
	}
}

func TestLogin_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()

	_, _, err := Login(context.Background(), srv.URL, "a@b.com", "p", srv.Client())
	if err == nil {
		t.Fatal("expected error for rate limit")
	}
}

func TestJWTExpiresAt(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	token := makeJWT(exp)
	got, err := JWTExpiresAt(token)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unix() != exp {
		t.Fatalf("got %v, want %v", got.Unix(), exp)
	}
}

func TestJWTExpiresAt_NoExp(t *testing.T) {
	token := makeJWTNoExp()
	got, err := JWTExpiresAt(token)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsZero() {
		t.Fatalf("expected zero time, got %v", got)
	}
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		token  string
		margin time.Duration
		want   bool
	}{
		{"empty token", "", 5 * time.Minute, true},
		{"expired", makeJWT(now.Add(-time.Hour).Unix()), 5 * time.Minute, true},
		{"within margin", makeJWT(now.Add(3 * time.Minute).Unix()), 5 * time.Minute, true},
		{"fresh", makeJWT(now.Add(time.Hour).Unix()), 5 * time.Minute, false},
		{"no exp claim", makeJWTNoExp(), 5 * time.Minute, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NeedsRefresh(tc.token, tc.margin, now)
			if got != tc.want {
				t.Fatalf("NeedsRefresh = %v, want %v", got, tc.want)
			}
		})
	}
}
