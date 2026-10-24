package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Server struct {
	Name     string `toml:"name"`
	URL      string `toml:"url"`
	Username string `toml:"username"`
}

type Notifications struct {
	Desktop bool `toml:"desktop"`
	Bell    bool `toml:"bell"`
	OSC     bool `toml:"osc"`
}

type UI struct {
	DMLimit int  `toml:"dm_limit"`
	Mouse   bool `toml:"mouse"`
}

type Config struct {
	Servers       []Server      `toml:"server"`
	Notifications Notifications `toml:"notifications"`
	UI            UI            `toml:"ui"`
}

var ErrNotFound = errors.New("config file not found")

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "termost", "config.toml"), nil
}

func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFile(path)
}

func LoadFile(path string) (*Config, error) {
	cfg := Config{
		Notifications: Notifications{Desktop: true},
		UI:            UI{DMLimit: 30, Mouse: true},
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if len(c.Servers) == 0 {
		return errors.New("no [[server]] entries")
	}
	seen := map[string]bool{}
	for i := range c.Servers {
		s := &c.Servers[i]
		if s.Name == "" || s.URL == "" || s.Username == "" {
			return fmt.Errorf("server #%d: name, url and username are required", i+1)
		}
		if seen[s.Name] {
			return fmt.Errorf("duplicate server name %q", s.Name)
		}
		seen[s.Name] = true
		s.URL = strings.TrimRight(s.URL, "/")
	}
	return nil
}

// Select returns the named servers, or all servers when names is empty.
func (c *Config) Select(names []string) ([]Server, error) {
	if len(names) == 0 {
		return c.Servers, nil
	}
	var out []Server
	for _, name := range names {
		found := false
		for _, s := range c.Servers {
			if s.Name == name {
				out = append(out, s)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown server %q", name)
		}
	}
	return out, nil
}
