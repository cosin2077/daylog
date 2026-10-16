package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"daylog/internal/journal"
	"daylog/internal/platform"
)

func TestEveryCommandHasOwnHelpWithoutInitializingData(t *testing.T) {
	for command := range commandUsage {
		for _, args := range [][]string{{command, "-h"}, {command, "--help"}, {"help", command}} {
			root := filepath.Join(t.TempDir(), "unused")
			out, err := commandOutput(t, append([]string{"--data", root}, args...)...)
			if err != nil || !strings.Contains(string(out), "daylog "+command) {
				t.Fatalf("missing command help for %v: %s (%v)", args, out, err)
			}
			if _, err = os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("%v initialized data", args)
			}
		}
	}
}

func TestNaturalArgumentsAndUnknownOptionSpelling(t *testing.T) {
	s := configStore(t)
	for _, args := range [][]string{{"report", "week", "--json"}, {"report", "--json", "month"}, {"today", "yesterday", "--json"}} {
		out, err := commandOutput(t, append([]string{"--data", s.Root}, args...)...)
		var value map[string]json.RawMessage
		if err != nil || json.Unmarshal(out, &value) != nil {
			t.Fatalf("natural arguments rejected: %v (%v)", args, err)
		}
		if args[0] == "today" {
			want := `"` + time.Now().AddDate(0, 0, -1).Format("2006-01-02") + `"`
			if string(value["date"]) != want {
				t.Fatal("yesterday used the wrong local date")
			}
		}
	}
	for _, args := range [][]string{{"report", "week", "--period", "day"}, {"today", "yesterday", "--date=2026-10-15"}} {
		if _, err := commandOutput(t, append([]string{"--data", s.Root}, args...)...); err == nil || !strings.Contains(err.Error(), "同时使用") {
			t.Fatal("conflicting natural and explicit arguments accepted", err)
		}
	}
	for _, option := range []string{"--bogus", "-bogus"} {
		_, err := commandOutput(t, "--data", s.Root, "status", option+"=fixture-secret")
		if err == nil || !strings.Contains(err.Error(), "未知选项 "+option+"。") || !strings.Contains(err.Error(), "--verbose") || strings.Contains(err.Error(), "fixture-secret") {
			t.Fatal("unknown option lost its spelling or exposed its value", err)
		}
	}
}

func TestAnalyzeMissingBatchAndPauseZeroFailBeforeEnvironmentAccess(t *testing.T) {
	s := configStore(t)
	for _, id := range []string{"nope", journal.ID(time.Now().Add(-time.Hour))} {
		out, err := commandOutput(t, "--data", s.Root, "analyze", "--batch", id)
		if err == nil || !strings.Contains(err.Error(), "找不到这段记录") || !strings.Contains(err.Error(), "daylog pending") || strings.Contains(string(out), "正在整理") || strings.Contains(err.Error(), "--retry") {
			t.Fatalf("missing ID reached model/environment flow: %s %v", out, err)
		}
	}
	id := journal.ID(time.Now())
	if err := os.WriteFile(filepath.Join(s.Root, "batches", id+".json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := commandOutput(t, "--data", s.Root, "analyze", "--batch", id)
	if err == nil || strings.Contains(err.Error(), "找不到") || !strings.Contains(err.Error(), "无法读取") {
		t.Fatal("corrupt batch was reported as nonexistent", err)
	}
	_, err = commandOutput(t, "--data", s.Root, "pause", "--minutes", "0")
	if err == nil || !strings.Contains(err.Error(), "1–1440") || !strings.Contains(err.Error(), "resume") {
		t.Fatal("unbounded pause reached control connection", err)
	}
}

func saveUXReport(t *testing.T, s *journal.Store, r journal.Report) {
	t.Helper()
	dir := filepath.Join(s.Root, "reports", r.BatchID[:8])
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteJSON(filepath.Join(dir, r.BatchID+".json"), r); err != nil {
		t.Fatal(err)
	}
}

func TestTodayAndReportSharePointAndCrossMidnightActivityCounts(t *testing.T) {
	s := configStore(t)
	at := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	r := journal.Report{SchemaVersion: 2, BatchID: journal.ID(at), Start: at.Add(-time.Minute), End: at.Add(time.Hour), CreatedAt: at.Add(time.Hour), Cards: []journal.Card{
		{Title: "跨日", Start: at.Add(-time.Minute), End: at.Add(time.Minute)},
		{Title: "午夜单帧", Start: at, End: at},
		{Title: "次日单帧", Start: at.AddDate(0, 0, 1), End: at.AddDate(0, 0, 1)},
	}}
	saveUXReport(t, s, r)
	plain, err := commandOutput(t, "--data", s.Root, "today", "--date", "2026-10-15")
	if err != nil || !strings.Contains(string(plain), "2 项活动") {
		t.Fatalf("today omitted point or cross-midnight activity: %s %v", plain, err)
	}
	today, err := commandOutput(t, "--data", s.Root, "today", "--date", "2026-10-15", "--json")
	var diary struct {
		Count      int                `json:"activity_count"`
		Activities []journal.Activity `json:"activities"`
	}
	if err != nil || json.Unmarshal(today, &diary) != nil || diary.Count != 2 || len(diary.Activities) != 2 {
		t.Fatal("today export disagrees with human count", err)
	}
	report, err := commandOutput(t, "--data", s.Root, "report", "--date", "2026-10-15", "--json")
	var period journal.PeriodReport
	if err != nil || json.Unmarshal(report, &period) != nil || len(period.Activities) != diary.Count || period.EstimatedSeconds != 0 {
		t.Fatal("report count differs or point evidence acquired time", err)
	}
}

func TestEmptyReportOffersRecentDateAndPreservesExistingHTML(t *testing.T) {
	s := configStore(t)
	at := time.Date(2026, 10, 15, 10, 0, 0, 0, time.Local)
	saveUXReport(t, s, journal.Report{BatchID: journal.ID(at), Start: at, Cards: []journal.Card{{Title: "合成活动", Start: at, End: at}}})
	path := filepath.Join(s.Root, "reports", "report.html")
	if err := os.WriteFile(path, []byte("existing snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := commandOutput(t, "--data", s.Root, "report", "--date", "2099-01-01", "--no-open")
	current, _ := os.ReadFile(path)
	if err != nil || !strings.Contains(string(out), "2099-01-01 没有记录") || !strings.Contains(string(out), "daylog report --date 2026-10-15") || string(current) != "existing snapshot" {
		t.Fatalf("empty date overwrote useful report: %s (%v)", out, err)
	}
}

func TestRedactedConfigRoundTripPreservesExistingCredential(t *testing.T) {
	s := configStore(t)
	var out bytes.Buffer
	if err := configure(s, []string{"--api-key", "fixture-only-secret"}, strings.NewReader(""), &out, nil); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := configure(s, []string{"--json"}, strings.NewReader(""), &out, nil); err != nil {
		t.Fatal(err)
	}
	if err := configure(s, []string{"--file", "-"}, bytes.NewReader(out.Bytes()), io.Discard, nil); err != nil {
		t.Fatal(err)
	}
	c, err := journal.ReadConfig(s.Root)
	if err != nil || c.AI.APIKey != "fixture-only-secret" {
		t.Fatal("redacted config replaced the credential", err)
	}
	if err := configure(s, []string{"--show-secrets"}, strings.NewReader(""), io.Discard, nil); err == nil {
		t.Fatal("secret export did not require explicit JSON")
	}
	if err := configure(s, []string{"--api-key", "<REDACTED>"}, strings.NewReader(""), io.Discard, nil); err == nil {
		t.Fatal("placeholder became a credential")
	}
}

func TestStatusRefreshesLiveQueueAndSeparatesInterruptedWork(t *testing.T) {
	parent := ""
	if runtime.GOOS != "windows" {
		parent = "/tmp"
	}
	root, err := os.MkdirTemp(parent, "dlstatus-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	s, err := journal.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour)
	for i, reason := range []string{"hermes:canceled", "claude:timeout"} {
		start := at.Add(time.Duration(i) * time.Minute)
		b := journal.Batch{ID: journal.ID(start), Start: start, End: start.Add(time.Minute), LastError: reason, Attempts: 2}
		if err = journal.WriteJSON(filepath.Join(root, "batches", b.ID+".json"), b); err != nil {
			t.Fatal(err)
		}
	}
	check := func(running bool) {
		t.Helper()
		out, err := commandOutput(t, "--data", root, "status", "--json")
		var result statusResult
		if err != nil || json.Unmarshal(out, &result) != nil || result.Running != running || result.Pending != 2 || result.FailedBatches != 1 || result.InterruptedBatches != 1 {
			t.Fatalf("status queue diverged: %s %v", out, err)
		}
		out, err = commandOutput(t, "--data", root, "status")
		if err != nil || len(strings.Split(strings.TrimSpace(string(out)), "\n")) != 6 || strings.Contains(string(out), "PID") || !strings.Contains(string(out), "已中断") {
			t.Fatalf("compact state is incomplete: %s %v", out, err)
		}
	}
	check(false)
	listener, cleanup, err := platform.ListenControl(root)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.SetDeadline(time.Now().Add(time.Second))
			var request journal.Control
			if json.NewDecoder(conn).Decode(&request) == nil {
				json.NewEncoder(conn).Encode(journal.Status{Pending: 99, FailedBatches: 99})
			}
			conn.Close()
		}
	}()
	t.Cleanup(func() { listener.Close(); <-done; cleanup() })
	check(true)
}

func TestPendingDefaultShowsUpToTenSegmentsWithoutIDs(t *testing.T) {
	s := configStore(t)
	at := time.Now().Add(-time.Hour)
	for i := 0; i < 11; i++ {
		start := at.Add(time.Duration(i) * time.Minute)
		b := journal.Batch{ID: journal.ID(start), Start: start, End: start.Add(time.Minute)}
		if err := journal.WriteJSON(filepath.Join(s.Root, "batches", b.ID+".json"), b); err != nil {
			t.Fatal(err)
		}
	}
	out, err := commandOutput(t, "--data", s.Root, "pending")
	if err != nil || strings.Count(string(out), "张截图") != 10 || !strings.Contains(string(out), "待整理 11 段（显示 10 段）") || strings.Contains(string(out), journal.ID(at)) {
		t.Fatalf("pending default lost limits or exposed IDs: %s %v", out, err)
	}
	out, err = commandOutput(t, "--data", s.Root, "pending", "--limit", "11")
	if err != nil || strings.Count(string(out), "张截图") != 11 {
		t.Fatal("explicit pending limit omitted work", err)
	}
}

func TestDoctorUsesHistoricalMetadataAndAlignsLabels(t *testing.T) {
	s := configStore(t)
	t.Setenv("DAYLOG_AGENT_SEARCH_PATH", t.TempDir())
	dir := filepath.Join(s.Root, "logs", "llm")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	log := `{"at":"2026-10-15T10:00:00Z","event":"response","source":"agent:claude","error_kind":"timeout","response":"fixture-secret"}
{"at":"2026-10-15T11:00:00Z","event":"response","source":"agent:hermes"}
{"at":"2026-10-15T12:00:00Z","event":"request","source":"agent:claude"}
`
	if err := os.WriteFile(filepath.Join(dir, "20261015.jsonl"), []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
	c := journal.DefaultConfig()
	c.Agent.Order = []string{"claude", "hermes"}
	models := doctorModels(s.Root, c)
	if len(models) != 2 || models[0].LastErrorKind != "timeout" || models[0].LastAt == nil || models[1].State != "最近整理成功" || models[1].Installed {
		t.Fatal("historical metadata presented as connectivity", models)
	}
	var out bytes.Buffer
	doctorRow(&out, "低电量模式", "关闭")
	doctorRow(&out, "系统热状态", "正常")
	if out.String() != "  低电量模式    关闭\n  系统热状态    正常\n" {
		t.Fatal("doctor columns are misaligned", out.String())
	}
	raw, _ := json.Marshal(models)
	if strings.Contains(string(raw), "fixture-secret") {
		t.Fatal("doctor retained model output")
	}
}

func TestV4AnalyzeAcceptsNaturalIDAndRejectsConflicts(t *testing.T) {
	s := configStore(t)
	for _, args := range [][]string{{"analyze", "nonexist"}, {"analyze", "--json", "nonexist"}} {
		_, err := commandOutput(t, append([]string{"--data", s.Root}, args...)...)
		if err == nil || !strings.Contains(err.Error(), "找不到这段记录（nonexist）") || !strings.Contains(err.Error(), "daylog pending") {
			t.Fatal("natural ID did not reach the actionable missing-record check", err)
		}
	}
	_, err := commandOutput(t, "--data", s.Root, "analyze", "one", "--batch", "two")
	if err == nil || !strings.Contains(err.Error(), "--batch") || !strings.Contains(err.Error(), "同时使用") {
		t.Fatal("conflicting record IDs were silently resolved", err)
	}
}

func TestV4TodayShowsOneTimestampForSubMinuteEntries(t *testing.T) {
	at := time.Date(2026, 10, 15, 13, 14, 0, 0, time.Local)
	for _, end := range []time.Time{at, at.Add(time.Nanosecond), at.Add(20 * time.Second)} {
		if activityTime(at, end) != "13:14" {
			t.Fatal("a point or sub-minute entry looks like a zero-length range")
		}
	}
	if activityTime(at, at.Add(2*time.Minute)) != "13:14–13:16" {
		t.Fatal("a real range lost its end")
	}
}
