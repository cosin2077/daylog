package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"daylog/internal/journal"
)

func TestPlaintextKeyFromCLIAndJSON(t *testing.T) {
	s := configStore(t)
	noKeychain := func(string) error { t.Fatal("plaintext config used Keychain"); return nil }
	if err := configure(s, []string{"--endpoint", "https://api.example.com/v1/chat/completions", "--model", "fixture", "--api-key", "fixture-cli-key"}, strings.NewReader(""), io.Discard, noKeychain); err != nil {
		t.Fatal(err)
	}
	c, err := journal.ReadConfig(s.Root)
	if err != nil || c.AI.APIKey != "fixture-cli-key" {
		t.Fatal("CLI did not persist the plaintext key")
	}
	t.Setenv(c.AI.KeyEnv, "fixture-env-key")
	if apiKey(c) != "fixture-cli-key" {
		t.Fatal("explicit config key did not take precedence")
	}
	path := filepath.Join(s.Root, "config.json")
	c.AI.APIKey = "fixture-json-key"
	raw, _ := json.Marshal(c)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	c, err = journal.ReadConfig(s.Root)
	if err != nil || apiKey(c) != "fixture-json-key" {
		t.Fatal("manually edited JSON key was not used")
	}
	if err = configure(s, []string{"--key-env", "DAYLOG_FIXTURE_KEY"}, strings.NewReader(""), io.Discard, noKeychain); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DAYLOG_FIXTURE_KEY", "fixture-selected-env-key")
	c, err = journal.ReadConfig(s.Root)
	if err != nil || c.AI.APIKey != "" || apiKey(c) != "fixture-selected-env-key" {
		t.Fatal("switching to environment did not apply")
	}
}

func TestSwitchPlaintextKeyToOptionalKeychain(t *testing.T) {
	s := configStore(t)
	c := journal.DefaultConfig()
	c.AI.APIKey = "fixture-plain-key"
	if err := journal.WriteJSON(filepath.Join(s.Root, "config.json"), c); err != nil {
		t.Fatal(err)
	}
	stored := ""
	if err := configure(s, []string{"--api-key-stdin"}, strings.NewReader("fixture-keychain-key"), io.Discard, func(value string) error { stored = value; return nil }); err != nil {
		t.Fatal(err)
	}
	c, err := journal.ReadConfig(s.Root)
	if err != nil || stored != "fixture-keychain-key" || c.AI.APIKey != "" || c.AI.KeyEnv != "" {
		t.Fatal("old key source still overrides the requested Keychain value")
	}
}
