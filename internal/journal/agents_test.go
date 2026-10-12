package journal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentFallbackAndTemporaryCleanup(t *testing.T) {
	s := testStore(t)
	b := testBatch(t, s, time.Now())
	report, _ := json.Marshal(testReport(b))
	dir := t.TempDir()
	fixture := filepath.Join(dir, "report.json")
	os.WriteFile(fixture, report, 0600)
	// Only fixture executables are discoverable; no real agent is called.
	t.Setenv("PATH", dir)
	t.Setenv("DAYLOG_AGENT_SEARCH_PATH", dir)
	t.Setenv("DAYLOG_TEST_REPORT", fixture)
	os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nexit 1\n"), 0700)
	os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nprev=''\nfor arg in \"$@\"; do\n if [ \"$prev\" = '--output-last-message' ]; then /bin/cat \"$DAYLOG_TEST_REPORT\" > \"$arg\"; fi\n prev=$arg\ndone\n"), 0700)
	c := DefaultConfig()
	if err := s.AnalyzeAgent(context.Background(), b.ID, c, ""); err != nil {
		t.Fatal(err)
	}
	archived, err := s.Batch(b.ID)
	if err != nil || archived.Attempts != 2 {
		t.Fatalf("fallback budget not accounted: %+v %v", archived, err)
	}
	reports, err := s.Reports(b.Start, b.End)
	if err != nil || len(reports) != 1 || reports[0].Source != "agent:codex" {
		t.Fatal("fallback report not committed")
	}
	jobs, err := os.ReadDir(filepath.Join(s.Root, "jobs"))
	if err != nil || len(jobs) != 0 {
		t.Fatal("temporary screenshot copies retained")
	}
}

func TestAgentReportStrictParsing(t *testing.T) {
	for _, data := range []string{`{"batch_id":"x","cards":[],"shell":"run"}`, `{"batch_id":"x","cards":[]} trailing`} {
		if _, err := parseAgentReport([]byte(data)); err == nil {
			t.Fatal("untrusted extra content accepted")
		}
	}
}

func TestDraftRecoveryDoesNotResetAttempts(t *testing.T) {
	s := testStore(t)
	b := testBatch(t, s, time.Now())
	if _, err := s.ReserveAttempt(b.ID, DefaultConfig(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDraft(b); err != nil {
		t.Fatal(err)
	}
	if err := s.SealDraft(b, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Batch(b.ID)
	if after.Attempts != 1 {
		t.Fatal("crash recovery reset retry state")
	}
}

func TestAgentTimeoutDoesNotRunPastBudget(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n/bin/sleep 30 &\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := runAgent(context.Background(), "claude", script, dir, Batch{}, nil, 1)
	if err == nil || time.Since(started) > 4*time.Second {
		t.Fatal("agent process timeout did not terminate promptly")
	}
}

func TestUnknownAgentIsDetectedWithoutInvocation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("DAYLOG_AGENT_SEARCH_PATH", dir)
	os.WriteFile(filepath.Join(dir, "grok"), []byte("#!/bin/sh\nexit 99\n"), 0700)
	for _, info := range Agents() {
		if info.Name == "grok" && (!info.Installed || info.Supported || !strings.Contains(info.Reason, "no_verified")) {
			t.Fatal("CLI advertised as a vision adapter")
		}
	}
}
