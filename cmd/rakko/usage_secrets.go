package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo"

	"rakko/internal/aiusage"
)

const usageKeysFileName = "aiusage-keys.json"

type legacySecretBackend interface {
	Get(key string) ([]byte, error)
	Delete(key string) error
}

type mygoSecretBackend struct{}

func (mygoSecretBackend) Get(key string) ([]byte, error) { return mygo.Secrets.Get(key) }
func (mygoSecretBackend) Delete(key string) error        { return mygo.Secrets.Delete(key) }

// usageKeyStore stores API keys as a mode-0600 JSON file inside Rakko's data
// directory instead of the OS keychain, so ad-hoc signed updates never re-prompt
// for keychain access. Existing keychain entries migrate lazily on first read.
type usageKeyStore struct {
	mu     sync.Mutex
	path   string
	legacy legacySecretBackend
	tried  map[aiusage.Tool]bool
}

func newUsageKeyStore(dataDir string, legacy legacySecretBackend) *usageKeyStore {
	return &usageKeyStore{
		path:   filepath.Join(dataDir, usageKeysFileName),
		legacy: legacy,
		tried:  make(map[aiusage.Tool]bool),
	}
}

func (s *usageKeyStore) secretKey(tool aiusage.Tool) string {
	return "aiusage-" + string(tool)
}

func (s *usageKeyStore) loadFile() map[string]string {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return map[string]string{}
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		// Corrupted or wrong-type JSON reads back empty rather than crashing.
		return map[string]string{}
	}
	return m
}

func (s *usageKeyStore) writeFileAtomic(m map[string]string) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, "."+usageKeysFileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	cleanup = false
	_ = os.Chmod(s.path, 0o600)
	return nil
}

// Get returns the API key for tool. If absent from the key file, it checks the
// legacy keychain at most once per tool per session, saves any non-empty value
// atomically into the key file, and deletes the legacy keychain entry.
func (s *usageKeyStore) Get(tool aiusage.Tool) string {
	if tool == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	keys := s.loadFile()
	if v := strings.TrimSpace(keys[string(tool)]); v != "" {
		return v
	}
	if s.tried[tool] || s.legacy == nil {
		return ""
	}
	s.tried[tool] = true
	raw, err := s.legacy.Get(s.secretKey(tool))
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(raw))
	if v == "" {
		return ""
	}
	keys[string(tool)] = v
	if err := s.writeFileAtomic(keys); err != nil {
		return v
	}
	_ = s.legacy.Delete(s.secretKey(tool))
	return v
}

// Set saves or clears the API key for tool atomically in the mode-0600 file.
func (s *usageKeyStore) Set(tool aiusage.Tool, key string) error {
	if tool == "" {
		return errors.New("usage key tool is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tried[tool] = true
	keys := s.loadFile()
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		delete(keys, string(tool))
		if err := s.writeFileAtomic(keys); err != nil {
			return err
		}
		if s.legacy != nil {
			_ = s.legacy.Delete(s.secretKey(tool))
		}
		return nil
	}
	keys[string(tool)] = trimmed
	return s.writeFileAtomic(keys)
}
