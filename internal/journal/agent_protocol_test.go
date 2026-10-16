package journal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fakeClaude(t *testing.T, events string, ending string) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix fake CLI; Windows adapter/process tests remain separate")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	quoted := "'" + strings.ReplaceAll(events, "'", "'\"'\"'") + "'"
	if err := os.WriteFile(path, []byte("#!/bin/sh\n/bin/cat >/dev/null\nprintf '%s\\n' "+quoted+"\n"+ending+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path, dir
}

func TestClaudeIgnoresUIAndRetryEventsBeforeResult(t *testing.T) {
	event, _ := json.Marshal(map[string]any{"type": "result", "result": `{"schema_version":2,"batch_id":"fixture","segments":[]}`})
	path, dir := fakeClaude(t, "{\"type\":\"system\",\"subtype\":\"ui_invalidate\"}\n{\"type\":\"system\",\"subtype\":\"api_retry\"}\n"+string(event), "exit 0")
	trace, err := runAgent(context.Background(), "claude", path, dir, Batch{SchemaVersion: 2}, nil, 10, false, "low")
	if err != nil || !strings.HasPrefix(trace.Response, `{"schema_version":2`) {
		t.Fatalf("terminal result ignored: %s (%v)", trace.Kind, err)
	}
}

func TestClaudeEmptyTerminalResultIsNotSuccess(t *testing.T) {
	path, dir := fakeClaude(t, `{"type":"result","is_error":false,"result":""}`, "exit 0")
	trace, err := runAgent(context.Background(), "claude", path, dir, Batch{}, nil, 10, false, "inherit")
	if err == nil || trace.Kind != "missing_result" {
		t.Fatalf("empty result accepted: %q (%v)", trace.Kind, err)
	}
}

func TestClaudeTerminalFailureSurvivesNonzeroExit(t *testing.T) {
	path, dir := fakeClaude(t, `{"type":"result","is_error":true,"result":"fixture upstream failure"}`, "exit 1")
	trace, err := runAgent(context.Background(), "claude", path, dir, Batch{}, nil, 10, false, "inherit")
	if err == nil || trace.Kind != "agent_error" || trace.ExitCode == nil || *trace.ExitCode != 1 {
		t.Fatalf("terminal failure misclassified: %q (%v)", trace.Kind, err)
	}
}

func TestClaudeRetryTimeoutIsDistinguished(t *testing.T) {
	path, dir := fakeClaude(t, `{"type":"system","subtype":"api_retry","attempt":1}`, "/bin/sleep 10")
	trace, err := runAgent(context.Background(), "claude", path, dir, Batch{}, nil, 1, false, "inherit")
	if err == nil || trace.Kind != "upstream_timeout" {
		t.Fatalf("upstream retry timeout was hidden: %q (%v)", trace.Kind, err)
	}
}

func TestFailureNotificationContainsOnlySafeReason(t *testing.T) {
	message := failureNotification("claude:upstream_timeout fixture-private-provider-detail")
	if !strings.Contains(message, "超时") || strings.Contains(message, "fixture-private") {
		t.Fatal("notification lost cause or exposed provider details")
	}
}

func TestHermesWarningAndTextDeltasDoNotReplaceTerminalResult(t *testing.T) {
	s := testStore(t)
	batch := testBatch(t, s, time.Now())
	image, err := s.FramePath(batch.Frames[0])
	if err != nil {
		t.Fatal(err)
	}
	report, _ := json.Marshal(testReport(batch))
	terminal, _ := json.Marshal(map[string]any{"type": "result", "exit_code": 0, "text": string(report)})
	path, dir := fakeClaude(t, "Warning: Unknown toolsets: none\n{\"type\":\"text\",\"text\":\"partial\"}\n"+string(terminal), "exit 0")
	trace, err := runAgent(context.Background(), "hermes", path, dir, batch, []string{image}, 10, false, "low")
	if err != nil || trace.Response != string(report) {
		t.Fatalf("Hermes warning/delta broke terminal parsing: %s (%v)", trace.Kind, err)
	}
}
