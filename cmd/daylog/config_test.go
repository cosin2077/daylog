package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"daylog/internal/journal"
)

func TestHelpAliasesDoNotCreateData(t *testing.T) {
	for _, args := range [][]string{nil, {"-h"}, {"--help"}, {"help"}, {"config", "-h"}, {"report", "-h"}} {
		root := filepath.Join(t.TempDir(), "unused")
		old := os.Stdout
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout = w
		err = execute(append([]string{"--data", root}, args...))
		w.Close()
		os.Stdout = old
		out, _ := io.ReadAll(r)
		r.Close()
		if err != nil || !bytes.Contains(out, []byte("Daylog")) {
			t.Fatalf("help failed: %v", err)
		}
		if _, err = os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("help created or migrated data")
		}
	}
}

func configStore(t *testing.T) *journal.Store {
	t.Helper()
	s, err := journal.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLLMConfigPreservesPreferencesAndKeepsKeyOutOfFiles(t *testing.T) {
	s := configStore(t)
	c := journal.DefaultConfig()
	c.Quality = 73
	c.BlockedApps = []string{"private.fixture"}
	if err := journal.WriteJSON(filepath.Join(s.Root, "config.json"), c); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	stored := ""
	err := configure(s, []string{"--endpoint", "https://api.example.com/v1/chat/completions", "--model", "vision-fixture", "--api-key-stdin"}, strings.NewReader("fixture-only-secret\n"), &out, func(key string) error { stored = key; return nil })
	if err != nil {
		t.Fatal(err)
	}
	after, err := journal.ReadConfig(s.Root)
	if err != nil || !after.AI.Enabled || after.AI.Model != "vision-fixture" || after.Quality != 73 || len(after.BlockedApps) != 1 {
		t.Fatalf("preferences or model config lost: %v", err)
	}
	if stored != "fixture-only-secret" {
		t.Fatal("key did not reach credential store")
	}
	raw, _ := os.ReadFile(filepath.Join(s.Root, "config.json"))
	if bytes.Contains(raw, []byte(stored)) || strings.Contains(out.String(), stored) {
		t.Fatal("credential persisted to config or echoed")
	}
	if err = configure(s, []string{"--agent", "auto"}, strings.NewReader(""), &out, func(string) error { t.Fatal("unexpected credential write"); return nil }); err != nil {
		t.Fatal(err)
	}
	after, err = journal.ReadConfig(s.Root)
	if err != nil || after.AI.Enabled || !after.Agent.Enabled || len(after.Agent.Order) != 3 || after.Quality != 73 {
		t.Fatal("switch back to agents did not preserve preferences")
	}
}

func TestInvalidLLMConfigDoesNotWriteConfigurationOrCredential(t *testing.T) {
	s := configStore(t)
	before, _ := os.ReadFile(filepath.Join(s.Root, "config.json"))
	for _, args := range [][]string{
		{"--endpoint", "http://public.example.com/v1/chat/completions", "--model", "fixture", "--api-key-stdin"},
		{"--agent", "auto", "--model", "fixture"},
		{"--file", "-", "--api-key-stdin"},
		{"--agent", "grok"},
	} {
		err := configure(s, args, strings.NewReader("fixture-only-secret"), io.Discard, func(string) error { t.Fatal("invalid config changed credential"); return nil })
		if err == nil {
			t.Fatal("invalid config accepted")
		}
		after, _ := os.ReadFile(filepath.Join(s.Root, "config.json"))
		if !bytes.Equal(before, after) {
			t.Fatal("invalid config changed file")
		}
	}
	for _, key := range []string{"", "line1\nline2", "prefix\x00suffix"} {
		if err := configure(s, []string{"--api-key-stdin"}, strings.NewReader(key), io.Discard, func(string) error { t.Fatal("invalid credential was saved"); return nil }); err == nil {
			t.Fatal("invalid credential accepted")
		}
	}
}
