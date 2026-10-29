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

func TestNativeSignInRedirectsAndSetsSession(t *testing.T) {
	m, _, _, _ := testManager(t)
	auth, _, err := OpenAuth(t.TempDir(), "1234")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(m, auth))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequest("POST", server.URL+"/login", strings.NewReader("username=xray-ui&password=1234"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", server.URL)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 303 || response.Header.Get("Location") != "/" || len(response.Cookies()) != 1 {
		t.Fatal("native form did not redirect with session", response.StatusCode)
	}
	cookie := response.Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe form login cookie")
	}
	state := request(t, client, "GET", server.URL+"/api/state", "", cookie, "", "")
	defer state.Body.Close()
	if state.StatusCode != 200 {
		t.Fatal("native form session unusable")
	}
}
func TestNativeSignInRejectsCrossOriginAndWrongPassword(t *testing.T) {
	server, _ := setupHTTP(t)
	for _, tc := range []struct {
		origin, password string
		want             int
	}{{"https://evil.example", testPassword, 403}, {server.URL, "wrong-password", 401}} {
		req, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader("password="+tc.password))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", tc.origin)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != tc.want || len(response.Cookies()) != 0 || !bytes.Contains(body, []byte("Sign in to Xray")) {
			t.Fatalf("unexpected failed form login: %d", response.StatusCode)
		}
	}
}

// Native form POSTs retain their Origin only with a compatible referrer policy.
func TestSignInPagePreservesSameOriginFormOrigin(t *testing.T) {
	s, _ := setupHTTP(t)
	for _, path := range []string{"/", "/login"} {
		r, err := s.Client().Get(s.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if got := r.Header.Get("Referrer-Policy"); got != "same-origin" {
			t.Fatalf("%s: referrer policy %q makes native sign-in send Origin: null", path, got)
		}
	}
	r := request(t, s.Client(), "POST", s.URL+"/api/login", `{"password":"`+testPassword+`"}`, nil, "", "null")
	defer r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Fatal("opaque origins must remain rejected")
	}
}

func TestProbeSettingsRequireAuthenticationCSRFAndAllowedInterval(t *testing.T) {
	s, m := setupHTTP(t)
	r := request(t, s.Client(), "POST", s.URL+"/api/probe/settings", `{"intervalSeconds":0}`, nil, "", s.URL)
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal("anonymous settings accepted")
	}
	cookie, csrf := login(t, s)
	r = request(t, s.Client(), "POST", s.URL+"/api/probe/settings", `{"intervalSeconds":0}`, cookie, "", s.URL)
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatal("settings accepted without CSRF")
	}
	for _, tc := range []struct {
		body   string
		status int
	}{{`{"intervalSeconds":0}`, 200}, {`{"intervalSeconds":300}`, 200}, {`{"intervalSeconds":1800}`, 200}, {`{"intervalSeconds":600}`, 200}, {`{"intervalSeconds":1}`, 400}, {`{}`, 400}, {`{"intervalSeconds":null}`, 400}, {`{"intervalSeconds":600,"url":"https://evil.example"}`, 400}} {
		r = request(t, s.Client(), "POST", s.URL+"/api/probe/settings", tc.body, cookie, csrf, s.URL)
		r.Body.Close()
		if r.StatusCode != tc.status {
			t.Fatalf("%s: %d", tc.body, r.StatusCode)
		}
	}
	if m.Snapshot().ProbeIntervalSeconds != 600 {
		t.Fatal("invalid settings changed schedule")
	}
}
