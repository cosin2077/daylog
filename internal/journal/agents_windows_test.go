//go:build windows

package journal

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Run this test executable as a native CLI fixture, without cmd.exe or a Unix
// shell. Real agent authentication and vision remain separate live tests.
func TestMain(m *testing.M) {
	if os.Getenv("DAYLOG_WINDOWS_AGENT_FIXTURE") == "1" {
		io.Copy(io.Discard, os.Stdin)
		if strings.EqualFold(filepath.Base(os.Args[0]), "claude.exe") {
			os.Exit(42)
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "result", "text": os.Getenv("DAYLOG_WINDOWS_REPORT_FIXTURE")})
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestWindowsAgentFallbackCommitsAndCleansTemporaryInput(t *testing.T) {
	s := testStore(t)
	b := v2Batch(t, s, time.Now())
	raw, _ := json.Marshal(v2Input(b))
	dir := t.TempDir()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	binary, e := os.ReadFile(exe)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"claude.exe", "hermes.exe"} {
		if e = os.WriteFile(filepath.Join(dir, name), binary, 0700); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("DAYLOG_AGENT_SEARCH_PATH", dir)
	t.Setenv("DAYLOG_WINDOWS_AGENT_FIXTURE", "1")
	t.Setenv("DAYLOG_WINDOWS_REPORT_FIXTURE", string(raw))
	c := DefaultConfig()
	c.Agent.Order = []string{"claude", "hermes"}
	c.Agent.InheritLoginEnv = false
	if e = s.AnalyzeAgent(context.Background(), b.ID, c, ""); e != nil {
		t.Fatal(e)
	}
	var report Report
	if e = ReadJSON(s.reportPath(b.ID), &report); e != nil {
		t.Fatal(e)
	}
	if report.Source != "agent:hermes" || report.Cards[0].EstimatedSeconds != 270 {
		t.Fatal("native fallback lost the validated result")
	}
	jobs, e := os.ReadDir(filepath.Join(s.Root, "jobs"))
	if e != nil || len(jobs) != 0 {
		t.Fatal("temporary image inputs survived")
	}
}
