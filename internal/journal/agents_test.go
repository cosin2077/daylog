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
	c.Agent.InheritLoginEnv = false
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

func TestBackgroundAgentReusesLoginProviderEnvironment(t *testing.T) {
	s := testStore(t)
	b := testBatch(t, s, time.Now())
	dir := t.TempDir()
	report, _ := json.Marshal(testReport(b))
	event, _ := json.Marshal(map[string]any{"type": "result", "result": string(report)})
	t.Setenv("PATH", dir)
	t.Setenv("DAYLOG_AGENT_SEARCH_PATH", dir)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_MODEL", "caller-model")
	t.Setenv("UNRELATED_PROFILE_SECRET", "")
	shell := filepath.Join(dir, "zsh")
	t.Setenv("SHELL", shell)
	// A background process lacks exports from the user's interactive profile.
	profile := "#!/bin/sh\nprintf 'profile banner\\000DAYLOG_ENV_BEGIN\\000ANTHROPIC_AUTH_TOKEN=fixture-token\\000ANTHROPIC_MODEL=profile-model\\000UNRELATED_PROFILE_SECRET=fixture-only\\000'\n"
	if err := os.WriteFile(shell, []byte(profile), 0700); err != nil {
		t.Fatal(err)
	}
	cli := "#!/bin/sh\n[ \"$ANTHROPIC_AUTH_TOKEN\" = fixture-token ] || exit 11\n[ \"$ANTHROPIC_MODEL\" = caller-model ] || exit 12\n[ -z \"$UNRELATED_PROFILE_SECRET\" ] || exit 13\nprintf '%s\\n' '" + string(event) + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(cli), 0700); err != nil {
		t.Fatal(err)
	}
	c := DefaultConfig()
	c.Agent.Order = []string{"claude"}
	if err := s.AnalyzeAgent(context.Background(), b.ID, c, ""); err != nil {
		t.Fatalf("background agent could not reuse existing profile configuration: %v", err)
	}
	other := testBatch(t, s, time.Now().Add(time.Minute))
	c.Agent.InheritLoginEnv = false
	if err := s.AnalyzeAgent(context.Background(), other.ID, c, ""); err == nil {
		t.Fatal("disabled profile inheritance still supplied the credential")
	}
}

func TestLoginEnvironmentCancellationKeepsOriginalEnvironment(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "zsh")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\n/bin/sleep 30 &\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	base := []string{"SHELL=" + shell, "ANTHROPIC_MODEL=caller-model"}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	got := loginAgentEnvironment(ctx, "claude", base)
	if strings.Join(got, "\n") != strings.Join(base, "\n") || time.Since(started) > 2*time.Second {
		t.Fatal("profile cancellation failed to stop children or preserve caller config")
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
	_, err := runAgent(context.Background(), "claude", script, dir, Batch{}, nil, 1, false, "inherit")
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

func TestCodexImportsOnlyDeclaredProviderCredential(t *testing.T) {
	root := t.TempDir()
	shell := filepath.Join(root, "zsh")
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("[model_providers.custom]\nenv_key = 'CUSTOM_PROVIDER_CREDENTIAL'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nprintf '\\000DAYLOG_ENV_BEGIN\\000CUSTOM_PROVIDER_CREDENTIAL=fixture-auth-value\\000UNRELATED_SECRET=do-not-import\\000'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	env := loginAgentEnvironment(context.Background(), "codex", []string{"SHELL=" + shell, "CODEX_HOME=" + root})
	combined := strings.Join(env, "\n")
	if !strings.Contains(combined, "CUSTOM_PROVIDER_CREDENTIAL=fixture-auth-value") || strings.Contains(combined, "UNRELATED_SECRET") {
		t.Fatal("provider credential missing or unrelated secret imported")
	}
	env = loginAgentEnvironment(context.Background(), "codex", []string{"SHELL=" + shell, "CODEX_HOME=" + root, "CUSTOM_PROVIDER_CREDENTIAL=explicit-value"})
	if strings.Contains(strings.Join(env, "\n"), "fixture-auth-value") {
		t.Fatal("profile overrode explicit credential")
	}
}

func TestClaudeStructuredResultIsUsed(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	event := `{"type":"result","is_error":false,"result":"done","structured_output":{"schema_version":2,"batch_id":"fixture","segments":[]}}`
	if err := os.WriteFile(script, []byte("#!/bin/sh\n/bin/cat >/dev/null\nprintf '%s\\n' '"+event+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	trace, err := runAgent(context.Background(), "claude", script, dir, Batch{SchemaVersion: 2}, nil, 10, false, "low")
	if err != nil || !strings.Contains(trace.Response, `"segments":[]`) {
		t.Fatal("structured Claude response ignored")
	}
}
