package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"daylog/internal/credentials"
)

type legacyFixture struct {
	value   string
	deleted bool
	gets    int
}

func (f *legacyFixture) Get(string) string   { f.gets++; return f.value }
func (f *legacyFixture) Delete(string) error { f.deleted = true; return nil }

func TestIntegrationSecretsMigrateAndDeleteLegacy(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	legacy := &legacyFixture{value: "fixture-not-real-token"}
	s := integrationSecrets{root: root, legacy: legacy}
	value, err := s.Get("notion.fixture")
	if err != nil || value != legacy.value || !legacy.deleted || legacy.gets != 1 {
		t.Fatal("legacy credential was not migrated and deleted", value, err, legacy)
	}
	legacy.value = "must-not-be-read"
	value, err = s.Get("notion.fixture")
	if err != nil || value != "fixture-not-real-token" || legacy.gets != 1 {
		t.Fatal("file store did not take precedence after migration", value, err, legacy)
	}
	data, err := os.ReadFile(filepath.Join(root, "integrations", "credentials.enc"))
	if err != nil || string(data) == "" {
		t.Fatal("encrypted file missing", err)
	}
	if bytes.Contains(data, []byte("fixture-not-real-token")) {
		t.Fatal("credential stored in plaintext")
	}
}

func TestIntegrationSecretsSetUsesEncryptedFileAndDeletesLegacy(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	legacy := &legacyFixture{value: "old-fixture"}
	s := integrationSecrets{root: root, legacy: legacy}
	if err := s.Set("notion.fixture", "new-fixture-token"); err != nil {
		t.Fatal(err)
	}
	if !legacy.deleted || legacy.gets != 0 {
		t.Fatal("new setup did not remove legacy without reading it", legacy)
	}
	value, err := s.Get("notion.fixture")
	if err != nil || value != "new-fixture-token" {
		t.Fatal("encrypted value unavailable", value, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "integrations", "credentials.enc"))
	if err != nil || bytes.Contains(data, []byte("new-fixture-token")) {
		t.Fatal("new token stored in plaintext")
	}
}

func TestIntegrationSecretsKeepLegacyWhenMigrationFails(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "integrations"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	legacy := &legacyFixture{value: "fixture-not-real-token"}
	s := integrationSecrets{root: root, legacy: legacy}
	if _, err := s.Get("notion.fixture"); err == nil {
		t.Fatal("migration unexpectedly succeeded")
	}
	if legacy.deleted {
		t.Fatal("legacy credential deleted before encrypted storage succeeded")
	}
}

func TestIntegrationSecretsDoNotFallbackFromCorruptFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	store := credentials.Store{Root: root}
	if err := store.Set("notion.fixture", "first-value"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "integrations", "credentials.enc")
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	legacy := &legacyFixture{value: "legacy-value"}
	s := integrationSecrets{root: root, legacy: legacy}
	if _, err := s.Get("notion.fixture"); err == nil || errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("corrupt encrypted store silently fell back to keychain", err)
	}
	if legacy.gets != 0 {
		t.Fatal("keychain was accessed after local corruption")
	}
}
