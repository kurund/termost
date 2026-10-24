package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestFileFallback(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	s := &TokenStore{FilePath: filepath.Join(t.TempDir(), "termost", "tokens.json")}
	key := Key("https://chat.example.com", "kurund")

	if _, err := s.Get(key); !errors.Is(err, ErrNoToken) {
		t.Fatalf("Get before Set: got %v, want ErrNoToken", err)
	}

	fallback, err := s.Set(key, "tok123")
	if err != nil || !fallback {
		t.Fatalf("Set = %v, %v; want fallback", fallback, err)
	}
	info, err := os.Stat(s.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode %o, want 600", perm)
	}

	if tok, err := s.Get(key); err != nil || tok != "tok123" {
		t.Errorf("Get = %q, %v", tok, err)
	}

	if err := s.Delete(key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(key); !errors.Is(err, ErrNoToken) {
		t.Errorf("Get after Delete: got %v", err)
	}
}

func TestKeyringPreferred(t *testing.T) {
	keyring.MockInit()
	s := &TokenStore{FilePath: filepath.Join(t.TempDir(), "tokens.json")}

	fallback, err := s.Set("k", "v")
	if err != nil || fallback {
		t.Fatalf("Set = %v, %v; want keyring", fallback, err)
	}
	if _, err := os.Stat(s.FilePath); !errors.Is(err, os.ErrNotExist) {
		t.Error("token file should not be written when keyring works")
	}
	if tok, err := s.Get("k"); err != nil || tok != "v" {
		t.Errorf("Get = %q, %v", tok, err)
	}
}
