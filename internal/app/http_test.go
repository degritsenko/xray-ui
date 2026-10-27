package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testPassword = "test-panel-password-123"

func setupHTTP(t *testing.T) (*httptest.Server, *Manager) {
	t.Helper()
	m, _, _, _ := testManager(t)
	auth, _, err := OpenAuth(t.TempDir(), testPassword)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(m, auth))
	t.Cleanup(server.Close)
	return server, m
}
func request(t *testing.T, client *http.Client, method, url, body string, cookie *http.Cookie, csrf, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func login(t *testing.T, s *httptest.Server) (*http.Cookie, string) {
	t.Helper()
	r := request(t, s.Client(), "POST", s.URL+"/api/login", `{"password":"`+testPassword+`"}`, nil, "", s.URL)
	defer r.Body.Close()
	if r.StatusCode != 200 || len(r.Cookies()) == 0 {
		t.Fatal("login failed", r.StatusCode)
	}
	cookie := r.Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	r2 := request(t, s.Client(), "GET", s.URL+"/api/state", "", cookie, "", "")
	defer r2.Body.Close()
	var state Snapshot
	if json.NewDecoder(r2.Body).Decode(&state) != nil || state.CSRF == "" {
		t.Fatal("missing CSRF")
	}
	return cookie, state.CSRF
}
func TestAuthenticationCSRFAndLogout(t *testing.T) {
	s, _ := setupHTTP(t)
	r := request(t, s.Client(), "GET", s.URL+"/api/state", "", nil, "", "")
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal("unauthenticated state exposed")
	}
	cookie, csrf := login(t, s)
	for _, tc := range []struct {
		token, origin string
		code          int
	}{{"", s.URL, 403}, {csrf, "https://evil.example", 403}, {csrf, s.URL, 200}} {
		r = request(t, s.Client(), "POST", s.URL+"/api/logout", `{}`, cookie, tc.token, tc.origin)
		r.Body.Close()
		if r.StatusCode != tc.code {
			t.Fatal("CSRF/logout mismatch", r.StatusCode)
		}
	}
	r = request(t, s.Client(), "GET", s.URL+"/api/state", "", cookie, "", "")
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal("logout did not revoke session")
	}
}
func TestAPIDoesNotExposeConnectionSecrets(t *testing.T) {
	s, m := setupHTTP(t)
	id := m.stateCopy().Subscriptions[0].ID
	_ = m.Refresh(t.Context(), id)
	n := m.stateCopy().Subscriptions[0].Nodes[0]
	_ = m.Activate(t.Context(), id, n.ID)
	m.logs.Add("secret " + n.UUID + " " + n.PublicKey + " https://example.com/sub")
	cookie, _ := login(t, s)
	r := request(t, s.Client(), "GET", s.URL+"/api/state", "", cookie, "", "")
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	for _, secret := range []string{n.UUID, n.PublicKey, "https://example.com/sub", `"config"`, `"password"`} {
		if bytes.Contains(body, []byte(secret)) {
			t.Fatal("secret exposed in API response")
		}
	}
}
func TestSubscriptionSourcesAreReadOnlyInAPI(t *testing.T) {
	s, m := setupHTTP(t)
	cookie, csrf := login(t, s)
	id := m.stateCopy().Subscriptions[0].ID
	for _, tc := range []struct{ method, path string }{{"POST", "/api/subscriptions"}, {"PUT", "/api/subscriptions/" + id}, {"DELETE", "/api/subscriptions/" + id}, {"GET", "/api/subscriptions/" + id}} {
		r := request(t, s.Client(), tc.method, s.URL+tc.path, `{"name":"Injected","url":"https://evil.example/source","intervalSeconds":0}`, cookie, csrf, s.URL)
		r.Body.Close()
		if r.StatusCode != 404 && r.StatusCode != 405 {
			t.Fatalf("subscription source mutable/exposed via %s: %d", tc.method, r.StatusCode)
		}
	}
	if len(m.stateCopy().Subscriptions) != 1 {
		t.Fatal("API changed configured sources")
	}
}
func TestPasswordPersistenceAndReset(t *testing.T) {
	dir := t.TempDir()
	a, generated, err := OpenAuth(dir, "")
	if err != nil || generated == "" || !a.checkPassword(generated) {
		t.Fatal("bootstrap password failed")
	}
	b, again, err := OpenAuth(dir, "")
	if err != nil || again != "" || !b.checkPassword(generated) {
		t.Fatal("password did not persist")
	}
	c, _, err := OpenAuth(dir, testPassword)
	if err != nil || c.checkPassword(generated) || !c.checkPassword(testPassword) {
		t.Fatal("env reset failed")
	}
}
func TestOversizedControlRequestDoesNotMutate(t *testing.T) {
	s, m := setupHTTP(t)
	id := m.stateCopy().Subscriptions[0].ID
	_ = m.Refresh(t.Context(), id)
	n := m.stateCopy().Subscriptions[0].Nodes[0]
	_ = m.Activate(t.Context(), id, n.ID)
	cookie, csrf := login(t, s)
	r := request(t, s.Client(), "POST", s.URL+"/api/xray/stop", strings.Repeat("x", 32768), cookie, csrf, s.URL)
	defer r.Body.Close()
	if r.StatusCode != 400 && r.StatusCode != 413 {
		t.Errorf("oversized control request accepted: %d", r.StatusCode)
	}
	if !m.core.Running() {
		t.Fatal("oversized request mutated core state")
	}
}
