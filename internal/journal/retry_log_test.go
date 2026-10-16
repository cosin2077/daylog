package journal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOldExhaustedBatchCanRecoverWithoutResettingBudget(t *testing.T) {
	s := testStore(t)
	at := time.Now()
	b := testBatch(t, s, at)
	old := at.Add(-time.Hour)
	b.Attempts = 3
	b.LastAttempt = &old
	b.LastError = "agent_failed:hermes"
	if err := WriteJSON(filepath.Join(s.Root, "batches", b.ID+".json"), b); err != nil {
		t.Fatal(err)
	}
	next, err := s.ReserveAttempt(b.ID, DefaultConfig(), at)
	if err != nil || next.Attempts != 4 {
		t.Fatalf("legacy batch stuck: %+v %v", next, err)
	}
	if err = s.FinishFailure(b.ID, "claude:timeout"); err != nil {
		t.Fatal(err)
	}
	failed, _ := s.Batch(b.ID)
	if RetryReady(failed, time.Now()) || failed.FailureRounds != 1 {
		t.Fatal("missing backoff")
	}
	if err = s.RequestRetry(b.ID); err != nil {
		t.Fatal(err)
	}
	retry, _ := s.Batch(b.ID)
	if retry.Attempts != 4 || !RetryReady(retry, time.Now()) {
		t.Fatal("manual retry erased counters or did not recover")
	}
	c := DefaultConfig()
	c.AI.MaxRequestsPerDay = 1
	if _, err = s.ReserveAttempt(b.ID, c, time.Now()); !errors.Is(err, ErrDailyBudget) {
		t.Fatal("retry bypassed daily budget")
	}
}

func TestBackoffAndNewDataPriority(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	fresh := Batch{ID: "fresh"}
	failed := Batch{ID: "failed", Attempts: 30, LastAttempt: &old}
	b, ok := nextAnalysis([]Batch{failed, fresh}, now)
	if !ok || b.ID != "fresh" {
		t.Fatal("failed backlog starved fresh data")
	}
	if retryDelay(1) != 15*time.Minute || retryDelay(2) != 30*time.Minute || retryDelay(30) != 6*time.Hour {
		t.Fatal("unbounded or incorrect backoff")
	}
}

func TestAnalysisLogRedactsAndKeepsValidJSON(t *testing.T) {
	s := testStore(t)
	c := DefaultConfig()
	secret := "fixture-provider-secret"
	image := "data:image/jpeg;base64," + strings.Repeat("A", 1000)
	event := AnalysisLog{Event: "response", RequestID: "req", BatchID: "batch", Source: "api", Response: `{"text":"` + secret + `","api_key":"unknown-fixture-key","image":"` + image + `"}`, Stderr: "Authorization: Bearer " + secret, Payload: map[string]any{"authorization": secret, "input": secret, "image": image}}
	if err := s.appendAnalysisLog(c, event, []string{secret}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(s.Root, "logs", "llm", "*.jsonl"))
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "unknown-fixture-key") || strings.Contains(string(raw), strings.Repeat("A", 500)) {
		t.Fatal("log exposed a credential or base64")
	}
	var got AnalysisLog
	if json.Unmarshal(raw, &got) != nil || got.Event != "response" {
		t.Fatal("log is not valid JSONL")
	}
	info, _ := os.Stat(files[0])
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("log is not private")
	}
}

func TestAnalysisLogRotationAndLimits(t *testing.T) {
	s := testStore(t)
	c := DefaultConfig()
	c.Diagnostics.MaxBytes = 1 << 20
	for i := 0; i < 12; i++ {
		if err := s.appendAnalysisLog(c, AnalysisLog{Event: "response", Response: strings.Repeat("测试输出-", 30000)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(s.Root, "logs", "llm", "*.jsonl"))
	var total int64
	for _, p := range files {
		raw, _ := os.ReadFile(p)
		total += int64(len(raw))
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if !json.Valid([]byte(line)) {
				t.Fatal("partial log line")
			}
		}
	}
	if total > c.Diagnostics.MaxBytes {
		t.Fatal("log storage cap exceeded")
	}
}

func TestAgentExitDiagnosticsSurviveCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fixture; Windows process and adapter tests cover this platform")
	}
	s := testStore(t)
	b := testBatch(t, s, time.Now())
	dir := t.TempDir()
	t.Setenv("DAYLOG_AGENT_SEARCH_PATH", dir)
	t.Setenv("FIXTURE_API_KEY", "fixture-private-token")
	os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\necho 'unsupported provider configuration' >&2\necho \"$FIXTURE_API_KEY\" >&2\nexit 42\n"), 0700)
	c := DefaultConfig()
	c.Agent.Order = []string{"claude"}
	c.Agent.InheritLoginEnv = false
	if s.AnalyzeAgent(context.Background(), b.ID, c, "") == nil {
		t.Fatal("failed agent accepted")
	}
	files, _ := filepath.Glob(filepath.Join(s.Root, "logs", "llm", "*.jsonl"))
	raw, _ := os.ReadFile(files[0])
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatal("request/response pair not logged")
	}
	var event AnalysisLog
	json.Unmarshal([]byte(lines[1]), &event)
	if event.ErrorKind != "cli_exit" || event.ExitCode == nil || *event.ExitCode != 42 || !strings.Contains(event.Stderr, "unsupported provider") || strings.Contains(string(raw), "fixture-private-token") {
		t.Fatal("diagnostic details missing or secret leaked")
	}
	after, _ := s.Batch(b.ID)
	if after.LastError != "claude:cli_exit" || after.RetryAfter == nil {
		t.Fatal("failure was flattened or discarded")
	}
	jobs, _ := os.ReadDir(filepath.Join(s.Root, "jobs"))
	if len(jobs) != 0 {
		t.Fatal("temporary image inputs were retained")
	}
}
