package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFile(t *testing.T) {
	path := writeConfig(t, `
[[server]]
name = "work"
url = "https://chat.example.com/"
username = "kurund"

[[server]]
name = "oss"
url = "https://community.example.org"
username = "kurund"
`)
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("got %d servers, want 2", len(cfg.Servers))
	}
	if got := cfg.Servers[0].URL; got != "https://chat.example.com" {
		t.Errorf("trailing slash not trimmed: %q", got)
	}

	if !cfg.UI.Mouse {
		t.Error("mouse should default to on")
	}
	if cfg.UI.DMLimit != 30 {
		t.Errorf("dm_limit default = %d", cfg.UI.DMLimit)
	}
	if !cfg.Notifications.Desktop || cfg.Notifications.Bell {
		t.Errorf("notification defaults = %+v", cfg.Notifications)
	}

	sel, err := cfg.Select([]string{"oss"})
	if err != nil || len(sel) != 1 || sel[0].Name != "oss" {
		t.Errorf("Select(oss) = %v, %v", sel, err)
	}
	if _, err := cfg.Select([]string{"nope"}); err == nil {
		t.Error("Select(nope) should fail")
	}
}

func TestLoadFileErrors(t *testing.T) {
	cases := map[string]string{
		"empty":      ``,
		"missing":    "[[server]]\nname = \"a\"\nurl = \"https://x\"\n",
		"duplicate":  "[[server]]\nname = \"a\"\nurl = \"https://x\"\nusername = \"u\"\n[[server]]\nname = \"a\"\nurl = \"https://y\"\nusername = \"u\"\n",
		"plain http": "[[server]]\nname = \"a\"\nurl = \"http://chat.example.com\"\nusername = \"u\"\n",
		"no scheme":  "[[server]]\nname = \"a\"\nurl = \"chat.example.com\"\nusername = \"u\"\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadFile(writeConfig(t, body)); err == nil {
				t.Error("expected error")
			}
		})
	}

	_, err := LoadFile(filepath.Join(t.TempDir(), "absent.toml"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestNotificationsOverride(t *testing.T) {
	cfg, err := LoadFile(writeConfig(t, "[notifications]\ndesktop = false\nosc = true\n[[server]]\nname = \"a\"\nurl = \"https://x\"\nusername = \"u\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Notifications.Desktop || !cfg.Notifications.OSC {
		t.Errorf("got %+v", cfg.Notifications)
	}
}

func TestCheckURL(t *testing.T) {
	for _, ok := range []string{"https://chat.example.com", "http://localhost:8065", "http://127.0.0.1:8065", "http://[::1]:8065"} {
		if err := checkURL(ok); err != nil {
			t.Errorf("checkURL(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"http://chat.example.com", "http://localhost.evil.com", "ftp://x", "https://", "chat.example.com"} {
		if err := checkURL(bad); err == nil {
			t.Errorf("checkURL(%q) should fail", bad)
		}
	}
}
