package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"daylog/internal/notion"
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

func TestNotionDefaultDateConfirmationAndNoninteractiveGuard(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  bool
	}{{"y\n", true}, {"YES\n", true}, {"是\n", true}, {"\n", false}, {"n\n", false}, {"", false}} {
		var prompt bytes.Buffer
		got, err := confirmNotionDate(strings.NewReader(tc.input), &prompt, "2026-10-21", "Asia/Shanghai", true)
		if err != nil || got != tc.want || !strings.Contains(prompt.String(), "2026-10-21") || !strings.Contains(prompt.String(), "[y/N]") {
			t.Fatal(tc, got, prompt.String(), err)
		}
	}
	s := configStore(t)
	settings, _ := notion.NewSettings("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Asia/Shanghai", "2026-10-18")
	if err := notion.SaveSettings(s.Root, settings); err != nil {
		t.Fatal(err)
	}
	original := os.Stdin
	r, w, _ := os.Pipe()
	w.Close()
	os.Stdin = r
	defer func() { os.Stdin = original; r.Close() }()
	if _, err := commandOutput(t, "--data", s.Root, "notion", "sync", "--json"); err == nil || !strings.Contains(err.Error(), "--date") || !strings.Contains(err.Error(), "--yes") {
		t.Fatal("noninteractive default bypassed confirmation", err)
	}
	if _, err := commandOutput(t, "--data", s.Root, "notion", "sync", "--yes"); err == nil || !strings.Contains(err.Error(), "凭证") {
		t.Fatal("explicit yes still prompted or rejected today", err)
	}
	if _, err := commandOutput(t, "--data", s.Root, "notion", "sync", "--date", ""); err == nil || !strings.Contains(err.Error(), "日期不能为空") {
		t.Fatal("empty explicit date became today", err)
	}
	if _, err := commandOutput(t, "--data", s.Root, "notion", "sync", "--date", "2099-01-01"); err == nil || !strings.Contains(err.Error(), "未来") {
		t.Fatal("future date accepted", err)
	}
	raw, err := commandOutput(t, "--data", s.Root, "notion", "sync", "--dry-run")
	var preview struct {
		Date    string
		Ongoing bool
	}
	zone, _ := time.LoadLocation("Asia/Shanghai")
	if err != nil || json.Unmarshal(raw, &preview) != nil || preview.Date != time.Now().In(zone).Format("2006-01-02") || !preview.Ongoing {
		t.Fatal("dry-run did not default to today without credentials or confirmation", string(raw), err)
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

func TestNotionProgressKeepsJSONAndFailureOutputClean(t *testing.T) {
	originalStderr := os.Stderr
	defer func() { os.Stderr = originalStderr }()
	s := configStore(t)
	settings, _ := notion.NewSettings("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Asia/Shanghai", "2026-10-18")
	if err := notion.SaveSettings(s.Root, settings); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"sync", "check"} {
		for _, asJSON := range []bool{false, true} {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { r.Close(); w.Close() })
			original := os.Stderr
			os.Stderr = w
			args := []string{"--data", s.Root, "notion", op}
			if op == "sync" {
				args = append(args, "--yes")
			}
			if asJSON {
				args = append(args, "--json")
			}
			stdout, commandErr := commandOutput(t, args...)
			os.Stderr = original
			w.Close()
			stderr, _ := io.ReadAll(r)
			r.Close()
			if commandErr == nil || !strings.Contains(commandErr.Error(), "凭证") || len(stdout) != 0 {
				t.Fatal("missing credential result changed", commandErr, string(stdout))
			}
			if asJSON && len(stderr) != 0 {
				t.Fatal("JSON caller received progress output", string(stderr))
			}
			if !asJSON && (!strings.Contains(string(stderr), "Notion") || strings.Contains(string(stderr), "\x1b")) {
				t.Fatal("non-TTY caller lost progress or received ANSI", string(stderr))
			}
		}
	}
}
