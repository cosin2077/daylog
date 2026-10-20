package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestNotionStatusAndDryRunDoNotRequireSecrets(t *testing.T) {
	s := configStore(t)
	raw, err := commandOutput(t, "--data", s.Root, "notion", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var status struct{ Configured bool }
	if json.Unmarshal(raw, &status) != nil || status.Configured {
		t.Fatal(string(raw))
	}
	raw, err = commandOutput(t, "--data", s.Root, "notion", "sync", "--date", "2026-10-18", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Date          string
		SchemaVersion int `json:"schema_version"`
	}
	if json.Unmarshal(raw, &data) != nil || data.Date != "2026-10-18" || data.SchemaVersion != 1 {
		t.Fatal(string(raw))
	}
	if _, err = commandOutput(t, "--data", s.Root, "notion", "sync", "--dry-run", "--date", "bad"); err == nil {
		t.Fatal("bad date accepted")
	}
	if _, err = commandOutput(t, "--data", s.Root, "notion", "check"); err == nil || !strings.Contains(err.Error(), "setup") {
		t.Fatal(err)
	}
}
func TestNotionSetupRejectsUnsafeTargetBeforeCredentialWrite(t *testing.T) {
	s := configStore(t)
	original := os.Stdin
	r, w, _ := os.Pipe()
	w.WriteString("synthetic-token-not-real\n")
	w.Close()
	os.Stdin = r
	defer func() { os.Stdin = original; r.Close() }()
	if _, err := commandOutput(t, "--data", s.Root, "notion", "setup", "--token-stdin", "--page", "https://evil.invalid/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("external parent accepted")
	}
}
func TestNotionHelpHasNoDataSideEffects(t *testing.T) {
	for _, args := range [][]string{{"notion", "-h"}, {"notion", "setup", "--help"}, {"help", "notion"}} {
		out, err := commandOutput(t, args...)
		if err != nil || !strings.Contains(string(out), "--token-stdin") || !strings.Contains(string(out), "00:00") {
			t.Fatal(string(out), err)
		}
	}
}
