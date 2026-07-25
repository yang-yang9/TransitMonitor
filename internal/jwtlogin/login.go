// Package jwtlogin implements a client for sub2api's /api/v1/auth/login
// endpoint. It exchanges admin email+password for a JWT and extracts the
// expiry from the token's payload (standard "exp" claim, decoded without
// signature verification since we trust our own sub2api instance).
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

// loginResp covers both sub2api's standard envelope ({code,message,data})
// and flat-shape forks ({token}/{access_token} at top level).
type loginResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Reason  string `json:"reason"`
	Data    struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	} `json:"data"`
	Token       string `json:"token"`        // flat-shape fallback
	AccessToken string `json:"access_token"` // flat-shape fallback
}

func (l loginResp) token() string {
	if l.Data.AccessToken != "" {
		return l.Data.AccessToken
	}
	if l.Token != "" {
		return l.Token
	}
	return l.AccessToken
}

// Login calls POST <baseURL>/api/v1/auth/login and returns the JWT + its expiry.
// Expiry is extracted from the JWT "exp" claim; zero-value if absent.
func Login(ctx context.Context, baseURL, email, password string, client *http.Client) (token string, expiresAt time.Time, err error) {
	if client == nil {
		client = http.DefaultClient
	}
	body, err := json.Marshal(loginReq{Email: email, Password: password})
	if err != nil {
		return "", time.Time{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("登录请求失败: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	var env loginResp
	_ = json.Unmarshal(respBody, &env) // best-effort; checked per-status below

	if resp.StatusCode != http.StatusOK {
		// Prefer the upstream's own message/reason for a clear error.
		hint := strings.TrimSpace(env.Message)
		if hint == "" {
			hint = truncate(string(respBody), 200)
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return "", time.Time{}, fmt.Errorf("登录失败：邮箱或密码错误%s", bodyHint(hint))
		case http.StatusForbidden:
			return "", time.Time{}, fmt.Errorf("登录失败：需要 2FA（暂不支持自动登录）%s", bodyHint(hint))
		case http.StatusTooManyRequests:
			return "", time.Time{}, fmt.Errorf("登录失败：被限流，稍后重试%s", bodyHint(hint))
		default:
			return "", time.Time{}, fmt.Errorf("登录失败：HTTP %d%s", resp.StatusCode, bodyHint(hint))
		}
	}

	tok := env.token()
	if tok == "" {
		return "", time.Time{}, fmt.Errorf("登录响应未包含 token（body: %s）", truncate(string(respBody), 200))
	}
	exp, _ := extractExp(tok)
	return tok, exp, nil
}

// bodyHint prefixes a non-empty hint with " — " for readable error messages.
func bodyHint(s string) string {
	if s == "" {
		return ""
	}
	return " — " + s
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
