package app

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"html"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

//go:embed web/*
var webFiles embed.FS

type Server struct {
	manager *Manager
	auth    *Auth
	mux     *http.ServeMux
}

func NewServer(manager *Manager, auth *Auth) http.Handler {
	s := &Server{manager: manager, auth: auth, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /api/login", s.login)
	s.mux.HandleFunc("POST /login", s.formLogin)
	s.mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/", http.StatusSeeOther) })
	s.mux.HandleFunc("POST /api/logout", s.protected(s.logout))
	s.mux.HandleFunc("GET /api/state", s.protected(s.state))
	s.mux.HandleFunc("POST /api/subscriptions/{id}/refresh", s.protected(s.refresh))
	s.mux.HandleFunc("POST /api/probe", s.protected(s.probe))
	s.mux.HandleFunc("POST /api/activate", s.protected(s.activate))
	s.mux.HandleFunc("POST /api/xray/{action}", s.protected(s.control))
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { apiError(w, 404, "Request not found") })
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"ok": true}) })
	assets, _ := fs.Sub(webFiles, "web")
	s.mux.Handle("/", http.FileServer(http.FS(assets)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		s.mux.ServeHTTP(w, r)
	})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return u.Scheme == scheme && strings.EqualFold(u.Host, r.Host)
}
func decodeBody(w http.ResponseWriter, r *http.Request, value any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return errors.New("expected a JSON request")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return errors.New("invalid or oversized request")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid JSON request")
	}
	return nil
}
func (s *Server) protected(next func(http.ResponseWriter, *http.Request, webSession)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("xray_session")
		if err != nil {
			apiError(w, 401, "Sign in to the panel")
			return
		}
		session, ok := s.auth.session(cookie.Value)
		if !ok {
			apiError(w, 401, "Session expired; sign in again")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if !sameOrigin(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.CSRF)) != 1 {
				apiError(w, 403, "Request rejected; reload the page")
				return
			}
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.ContentLength > 16384 {
			apiError(w, 413, "Request too large")
			return
		}
		next(w, r, session)
	}
}

// Both native forms and API clients use the same rate limit and session policy.
func (s *Server) loginAllowed(r *http.Request) (int, string) {
	if !sameOrigin(r) {
		return 403, "Request rejected"
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if !s.auth.allowLogin(ip) {
		return 429, "Too many attempts; try again in a minute"
	}
	return 0, ""
}
func (s *Server) startSession(w http.ResponseWriter, r *http.Request) webSession {
	session := s.auth.newSession()
	http.SetCookie(w, &http.Cookie{Name: "xray_session", Value: session.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: int(sessionLifetime.Seconds())})
	return session
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if code, message := s.loginAllowed(r); code != 0 {
		apiError(w, code, message)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if decodeBody(w, r, &body) != nil {
		apiError(w, 400, "Invalid request")
		return
	}
	if !s.auth.checkPassword(body.Password) {
		apiError(w, 401, "Incorrect password")
		return
	}
	session := s.startSession(w, r)
	writeJSON(w, 200, map[string]string{"csrf": session.CSRF})
}
func (s *Server) loginPage(w http.ResponseWriter, code int, message string) {
	page, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "Sign-in page unavailable", 500)
		return
	}
	markup := strings.Replace(string(page), `<p id="login-error" class="form-error" role="alert" hidden></p>`, `<p id="login-error" class="form-error" role="alert">`+html.EscapeString(message)+`</p>`, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, markup)
}
func (s *Server) formLogin(w http.ResponseWriter, r *http.Request) {
	if code, message := s.loginAllowed(r); code != 0 {
		s.loginPage(w, code, message)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" {
		s.loginPage(w, 400, "Invalid request")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if err = r.ParseForm(); err != nil || len(r.PostForm["password"]) != 1 {
		s.loginPage(w, 400, "Invalid request")
		return
	}
	if !s.auth.checkPassword(r.PostForm.Get("password")) {
		s.loginPage(w, 401, "Incorrect password")
		return
	}
	s.startSession(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request, session webSession) {
	var empty struct{}
	if err := decodeBody(w, r, &empty); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	s.auth.logout(session.Token)
	http.SetCookie(w, &http.Cookie{Name: "xray_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) state(w http.ResponseWriter, r *http.Request, session webSession) {
	view := s.manager.Snapshot()
	view.CSRF = session.CSRF
	writeJSON(w, 200, view)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request, _ webSession) {
	var empty struct{}
	if err := decodeBody(w, r, &empty); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if err := s.manager.Refresh(r.Context(), r.PathValue("id")); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) activate(w http.ResponseWriter, r *http.Request, _ webSession) {
	var body struct {
		SubscriptionID string `json:"subscriptionID"`
		NodeID         string `json:"nodeID"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if err := s.manager.Activate(r.Context(), body.SubscriptionID, body.NodeID); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) control(w http.ResponseWriter, r *http.Request, _ webSession) {
	var empty struct{}
	if err := decodeBody(w, r, &empty); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if err := s.manager.Control(r.Context(), r.PathValue("action")); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) probe(w http.ResponseWriter, r *http.Request, _ webSession) {
	var empty struct{}
	if err := decodeBody(w, r, &empty); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if err := s.manager.ProbeAll(r.Context()); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
