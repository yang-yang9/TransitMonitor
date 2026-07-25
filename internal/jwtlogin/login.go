// Package jwtlogin implements a client for sub2api's /auth/login endpoint.
// It exchanges admin email+password for a JWT and extracts the expiry from
// the token's payload (standard "exp" claim, decoded without signature
// verification since we trust our own sub2api instance).
package jwtlogin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResp struct {
	Token string `json:"token"`
}

// Login calls POST <baseURL>/auth/login and returns the JWT + its expiry.
// Expiry is extracted from the JWT "exp" claim; zero-value if absent.
func Login(ctx context.Context, baseURL, email, password string, client *http.Client) (token string, expiresAt time.Time, err error) {
	if client == nil {
		client = http.DefaultClient
	}
	body, err := json.Marshal(loginReq{Email: email, Password: password})
	if err != nil {
		return "", time.Time{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		// success
	case http.StatusUnauthorized:
		return "", time.Time{}, fmt.Errorf("login failed: invalid email or password")
	case http.StatusForbidden:
		return "", time.Time{}, fmt.Errorf("login failed: 2FA required (not supported)")
	case http.StatusTooManyRequests:
		return "", time.Time{}, fmt.Errorf("login failed: rate limited, retry later")
	default:
		return "", time.Time{}, fmt.Errorf("login failed: HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var lr loginResp
	if err := json.Unmarshal(respBody, &lr); err != nil {
		return "", time.Time{}, fmt.Errorf("login response parse: %w", err)
	}
	if lr.Token == "" {
		return "", time.Time{}, fmt.Errorf("login response: empty token")
	}
	exp, _ := extractExp(lr.Token)
	return lr.Token, exp, nil
}

// JWTExpiresAt decodes the JWT payload and returns the "exp" claim.
// Returns zero-value time if the token is malformed or has no exp.
func JWTExpiresAt(token string) (time.Time, error) {
	return extractExp(token)
}

// NeedsRefresh returns true if the JWT is empty or expires within the given margin.
func NeedsRefresh(token string, margin time.Duration, now time.Time) bool {
	if token == "" {
		return true
	}
	exp, err := extractExp(token)
	if err != nil || exp.IsZero() {
		return true
	}
	return now.Add(margin).After(exp)
}

func extractExp(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("malformed JWT: expected 3 parts, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims struct {
		Exp *float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("parse JWT claims: %w", err)
	}
	if claims.Exp == nil {
		return time.Time{}, nil
	}
	return time.Unix(int64(*claims.Exp), 0), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
