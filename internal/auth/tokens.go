package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

const service = "termost"

var ErrNoToken = errors.New("no stored token")

// TokenStore keeps session tokens in the Secret Service, falling back to a
// 0600 file when no Secret Service is running.
type TokenStore struct {
	FilePath string
}

func NewTokenStore() (*TokenStore, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return &TokenStore{FilePath: filepath.Join(dir, "termost", "tokens.json")}, nil
}

func Key(serverURL, username string) string {
	return username + "@" + serverURL
}

func (s *TokenStore) Get(key string) (string, error) {
	tok, err := keyring.Get(service, key)
	if err == nil {
		return tok, nil
	}
	tokens, ferr := s.readFile()
	if ferr != nil {
		return "", ferr
	}
	if tok, ok := tokens[key]; ok {
		return tok, nil
	}
	return "", ErrNoToken
}

// Set reports whether it had to use the file fallback.
func (s *TokenStore) Set(key, token string) (bool, error) {
	if err := keyring.Set(service, key, token); err == nil {
		return false, s.deleteFromFile(key)
	}
	tokens, err := s.readFile()
	if err != nil {
		return true, err
	}
	tokens[key] = token
	return true, s.writeFile(tokens)
}

func (s *TokenStore) Delete(key string) error {
	_ = keyring.Delete(service, key)
	return s.deleteFromFile(key)
}

func (s *TokenStore) deleteFromFile(key string) error {
	tokens, err := s.readFile()
	if err != nil {
		return err
	}
	if _, ok := tokens[key]; !ok {
		return nil
	}
	delete(tokens, key)
	return s.writeFile(tokens)
}

func (s *TokenStore) readFile() (map[string]string, error) {
	tokens := map[string]string{}
	data, err := os.ReadFile(s.FilePath)
	if errors.Is(err, os.ErrNotExist) {
		return tokens, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, err
	}
	return tokens, nil
}

func (s *TokenStore) writeFile(tokens map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(s.FilePath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.FilePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.FilePath)
}
