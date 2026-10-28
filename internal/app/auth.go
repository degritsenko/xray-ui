package app

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"
)

const passwordRounds = 600000
const sessionLifetime = 12 * time.Hour

type passwordRecord struct {
	Salt   string `json:"salt"`
	Hash   string `json:"hash"`
	Rounds int    `json:"rounds"`
}
type webSession struct {
	Token   string
	CSRF    string
	Expires time.Time
}
type loginAttempts struct {
	Count int
	Until time.Time
}
type Auth struct {
	record   passwordRecord
	mu       sync.Mutex
	sessions map[string]webSession
	attempts map[string]loginAttempts
}

func OpenAuth(dir, password string) (*Auth, string, error) {
	path := filepath.Join(dir, "auth.json")
	var record passwordRecord
	err := readJSON(path, &record, 4096)
	generated := ""
	if err != nil && !errors.Is(err, os.ErrNotExist) && password == "" {
		return nil, "", errors.New("could not read panel password")
	}
	a := &Auth{record: record, sessions: map[string]webSession{}, attempts: map[string]loginAttempts{}}
	if err == nil {
		salt, e1 := hex.DecodeString(record.Salt)
		hash, e2 := hex.DecodeString(record.Hash)
		if e1 != nil || e2 != nil || len(salt) != 16 || len(hash) != 32 || record.Rounds != passwordRounds {
			if password == "" {
				return nil, "", errors.New("panel password file is corrupted")
			}
			err = errors.New("reset required")
		}
	}
	if password != "" && !validPanelPassword(password) {
		return nil, "", errors.New("PANEL_PASSWORD must contain 4 to 32 characters")
	}
	if err != nil || password != "" && !a.checkPassword(password) {
		if password == "" {
			password = randomID()
			generated = password
		}
		salt, _ := hex.DecodeString(randomID())
		key, keyErr := pbkdf2.Key(sha256.New, password, salt, passwordRounds, 32)
		if keyErr != nil {
			return nil, "", keyErr
		}
		a.record = passwordRecord{Salt: hex.EncodeToString(salt), Hash: hex.EncodeToString(key), Rounds: passwordRounds}
		if atomicJSON(path, a.record) != nil {
			return nil, "", errors.New("could not save panel password")
		}
	}
	return a, generated, nil
}
func (a *Auth) checkPassword(password string) bool {
	// New configured passwords follow the current policy. Existing hashes may
	// belong to passwords accepted by an older version; keep those usable.
	if len(password) > 256 || a.record.Rounds != passwordRounds {
		return false
	}
	salt, err := hex.DecodeString(a.record.Salt)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(a.record.Hash)
	if err != nil || len(expected) != 32 {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordRounds, 32)
	return err == nil && subtle.ConstantTimeCompare(key, expected) == 1
}
func (a *Auth) allowLogin(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for key, value := range a.attempts {
		if now.After(value.Until) {
			delete(a.attempts, key)
		}
	}
	current, exists := a.attempts[ip]
	if !exists {
		if len(a.attempts) >= 1024 {
			return false
		}
		current.Until = now.Add(time.Minute)
	}
	if current.Count >= 5 {
		return false
	}
	current.Count++
	a.attempts[ip] = current
	return true
}
func (a *Auth) newSession() webSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for key, s := range a.sessions {
		if now.After(s.Expires) {
			delete(a.sessions, key)
		}
	}
	if len(a.sessions) >= 64 {
		oldestKey := ""
		var oldest time.Time
		for key, s := range a.sessions {
			if oldestKey == "" || s.Expires.Before(oldest) {
				oldestKey = key
				oldest = s.Expires
			}
		}
		delete(a.sessions, oldestKey)
	}
	session := webSession{Token: randomID() + randomID(), CSRF: randomID() + randomID(), Expires: now.Add(sessionLifetime)}
	a.sessions[session.Token] = session
	return session
}
func (a *Auth) session(token string) (webSession, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[token]
	if ok && time.Now().After(s.Expires) {
		delete(a.sessions, token)
		ok = false
	}
	return s, ok
}
func (a *Auth) logout(token string) { a.mu.Lock(); defer a.mu.Unlock(); delete(a.sessions, token) }

func validPanelPassword(password string) bool {
	if !utf8.ValidString(password) {
		return false
	}
	length := utf8.RuneCountInString(password)
	return length >= 4 && length <= 32
}
