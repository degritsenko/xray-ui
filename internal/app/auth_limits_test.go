package app

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

func TestPanelPasswordAcceptsFourToThirtyTwoCharacters(t *testing.T) {
	for _, password := range []string{"1234", strings.Repeat("x", 32), strings.Repeat("🔑", 32), "пароль"} {
		auth, generated, err := OpenAuth(t.TempDir(), password)
		if err != nil || generated != "" || !auth.checkPassword(password) {
			t.Fatalf("password length %d rejected: %v", len([]rune(password)), err)
		}
	}
	for _, password := range []string{"123", strings.Repeat("x", 33), "яюэ"} {
		if _, _, err := OpenAuth(t.TempDir(), password); err == nil {
			t.Fatal("out-of-range password accepted")
		}
	}
}

func TestExistingLongPasswordSurvivesPolicyChange(t *testing.T) {
	dir := t.TempDir()
	password := strings.Repeat("x", 40)
	salt := []byte("0123456789abcdef")
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordRounds, 32)
	if err != nil {
		t.Fatal(err)
	}
	record := passwordRecord{Salt: hex.EncodeToString(salt), Hash: hex.EncodeToString(key), Rounds: passwordRounds}
	if err = atomicJSON(filepath.Join(dir, "auth.json"), record); err != nil {
		t.Fatal(err)
	}
	auth, generated, err := OpenAuth(dir, "")
	if err != nil || generated != "" || !auth.checkPassword(password) {
		t.Fatal("upgrade locked out saved password", err)
	}
	if _, _, err = OpenAuth(dir, password); err == nil {
		t.Fatal("new configuration bypassed 32-character policy")
	}
}
