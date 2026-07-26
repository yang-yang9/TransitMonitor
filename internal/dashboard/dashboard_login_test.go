package dashboard

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"transitmonitor/internal/domain"
	"transitmonitor/internal/store"
)

func TestLoginPage_NoPassword(t *testing.T) {
	srv, _, cleanup := newDash(t, "")
	defer cleanup()
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/login"))
	if r.Code != 302 {
		t.Fatalf("no password: want 302 redirect to /, got %d", r.Code)
	}
	if loc := r.Header().Get("Location"); loc != "/" {
		t.Errorf("redirect location: want / got %s", loc)
	}
}

func newDashWithPassword(t *testing.T, password string) (*Server, func()) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	srv := New([]domain.Station{
		{ID: "s1", Name: "S1", Kind: domain.KindNewAPI, BaseURL: "https://a.example", Enabled: true},
	}, st, "", password)
	return srv, func() { _ = st.Close() }
}

func TestLoginPage_WithPassword(t *testing.T) {
	srv, cleanup := newDashWithPassword(t, "secret123")
	defer cleanup()

	// Unauthenticated → redirected to /login.
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/"))
	if r.Code != 302 {
		t.Fatalf("want 302 got %d", r.Code)
	}
	if loc := r.Header().Get("Location"); loc != "/login" {
		t.Errorf("redirect: want /login got %s", loc)
	}

	// Login page renders.
	r2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r2, localReq(http.MethodGet, "/login"))
	if r2.Code != 200 {
		t.Fatalf("login page: want 200 got %d", r2.Code)
	}
	if !strings.Contains(r2.Body.String(), "TransitMonitor") {
		t.Error("login page should contain TransitMonitor title")
	}
}

func TestLoginAPI_WrongPassword(t *testing.T) {
	srv, cleanup := newDashWithPassword(t, "correct")
	defer cleanup()

	form := url.Values{"password": {"wrong"}}
	req := localReq(http.MethodPost, "/api/login")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Body = nopCloser(strings.NewReader(form.Encode()))
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, req)
	if r.Code != 302 {
		t.Fatalf("wrong password form: want 302 got %d", r.Code)
	}
	if loc := r.Header().Get("Location"); !strings.Contains(loc, "err=1") {
		t.Errorf("redirect should contain err=1, got %s", loc)
	}
}

func TestLoginAPI_CorrectPassword(t *testing.T) {
	srv, cleanup := newDashWithPassword(t, "correct")
	defer cleanup()

	form := url.Values{"password": {"correct"}}
	req := localReq(http.MethodPost, "/api/login")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Body = nopCloser(strings.NewReader(form.Encode()))
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, req)
	if r.Code != 302 {
		t.Fatalf("correct password: want 302 got %d", r.Code)
	}
	if loc := r.Header().Get("Location"); loc != "/" {
		t.Errorf("redirect: want / got %s", loc)
	}

	// Extract session cookie and use it to access a protected page.
	cookies := r.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie set after login")
	}

	req2 := localReq(http.MethodGet, "/")
	req2.AddCookie(sessionCookie)
	r2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r2, req2)
	if r2.Code != 200 {
		t.Errorf("authenticated request: want 200 got %d", r2.Code)
	}
}

func TestLogoutAPI(t *testing.T) {
	srv, cleanup := newDashWithPassword(t, "pass")
	defer cleanup()

	// Login first.
	form := url.Values{"password": {"pass"}}
	req := localReq(http.MethodPost, "/api/login")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Body = nopCloser(strings.NewReader(form.Encode()))
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, req)

	var sessionCookie *http.Cookie
	for _, c := range r.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie")
	}

	// Logout.
	req2 := localReq(http.MethodPost, "/api/logout")
	req2.AddCookie(sessionCookie)
	r2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r2, req2)
	if r2.Code != 302 {
		t.Fatalf("logout: want 302 got %d", r2.Code)
	}

	// Session cookie should be cleared (MaxAge=-1).
	cleared := false
	for _, c := range r2.Result().Cookies() {
		if c.Name == sessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("session cookie should be cleared after logout")
	}
}

func TestAPIUnauthorized_WithPassword(t *testing.T) {
	srv, cleanup := newDashWithPassword(t, "secret")
	defer cleanup()

	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, localReq(http.MethodGet, "/api/stations"))
	if r.Code != 401 {
		t.Errorf("unauthenticated API: want 401 got %d", r.Code)
	}
}

func TestBearerStillWorks_WithPassword(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	srv := New([]domain.Station{
		{ID: "s1", Name: "S1", Kind: domain.KindNewAPI, BaseURL: "https://a.example", Enabled: true},
	}, st, "tok123", "pass123")

	req := localReq(http.MethodGet, "/api/stations")
	req.Header.Set("Authorization", "Bearer tok123")
	r := httptest.NewRecorder()
	srv.Handler().ServeHTTP(r, req)
	if r.Code != 200 {
		t.Errorf("bearer with password mode: want 200 got %d", r.Code)
	}
}

type nopCloserReader struct{ *strings.Reader }

func (nopCloserReader) Close() error { return nil }

func nopCloser(r *strings.Reader) nopCloserReader {
	return nopCloserReader{r}
}
