package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"daylog/internal/journal"
	"daylog/internal/platform"
)

func TestReviewStoppedStatusIsQueryableJSON(t *testing.T) {
	s := configStore(t)
	data, err := commandOutput(t, "--data", s.Root, "status", "--json")
	if err != nil {
		t.Fatalf("stopped recorder must remain queryable: %v", err)
	}
	var status map[string]json.RawMessage
	if json.Unmarshal(data, &status) != nil || string(status["running"]) != "false" {
		t.Fatalf("missing structured stopped state: %s", data)
	}
	data, err = commandOutput(t, "--data", s.Root, "status")
	if err != nil || !strings.Contains(string(data), "已停止") {
		t.Fatalf("stopped state should be readable: %s (%v)", data, err)
	}
}

func TestReviewBrokenControlConnectionIsNotReportedAsStopped(t *testing.T) {
	s := configStore(t)
	file, err := os.OpenFile(filepath.Join(s.Root, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = platform.TryLock(file); err != nil {
		t.Fatal(err)
	}
	defer platform.Unlock(file)
	data, err := commandOutput(t, "--data", s.Root, "status", "--json")
	var status map[string]json.RawMessage
	if err == nil || json.Unmarshal(data, &status) != nil || string(status["running"]) != "null" || string(status["state"]) != `"unreachable"` {
		t.Fatalf("IPC failure fabricated a stopped state: %s (%v)", data, err)
	}
}

func TestReviewInvalidArgumentsAreExplicitAndChinese(t *testing.T) {
	s := configStore(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"today_date", []string{"today", "--date", "2026-13-01"}, "日期格式应为 YYYY-MM-DD"},
		{"report_date", []string{"report", "--date", "bad"}, "日期格式应为 YYYY-MM-DD"},
		{"extra_today", []string{"today", "yesterday"}, "不支持的位置参数"},
		{"unknown_command", []string{"statsu"}, "status"},
		{"unknown_flag", []string{"status", "--foo"}, "未知选项"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--data", s.Root}, tc.args...)
			_, err := commandOutput(t, args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("missing actionable validation %q: %v", tc.want, err)
			}
		})
	}
}

func TestReviewTodayDistinguishesUnknownAndSubMinuteDurations(t *testing.T) {
	s := configStore(t)
	start := time.Date(2026, 10, 15, 10, 0, 0, 0, time.Local)
	report := journal.Report{SchemaVersion: 2, BatchID: "20261015T100000Z", Start: start, End: start.Add(time.Hour), CreatedAt: start.Add(time.Hour), Cards: []journal.Card{
		{Title: "单帧记录", Summary: "仅一次观察", Start: start, End: start.Add(time.Second), DurationMethod: "observation", Evidence: []string{"one"}},
		{Title: "短活动", Summary: "有观察区间", Start: start.Add(time.Minute), End: start.Add(90 * time.Second), DurationMethod: "observation", EstimatedSeconds: 30, Evidence: []string{"two", "three"}},
	}}
	dir := filepath.Join(s.Root, "reports", start.Format("20060102"))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(report)
	if err := os.WriteFile(filepath.Join(dir, report.BatchID+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := commandOutput(t, "--data", s.Root, "today", "--date", "2026-10-15")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "约 0 分钟") || !strings.Contains(text, "时长未估") || !strings.Contains(text, "30 秒") {
		t.Fatalf("duration presentation loses evidence: %s", text)
	}
}

func TestReviewPendingExplainsLimitsAndRetryAccounting(t *testing.T) {
	var out bytes.Buffer
	writePending(&out, []journal.Batch{{ID: "fixture", Attempts: 2, LastError: "hermes:canceled"}}, 3)
	for _, want := range []string{"--limit", "调用尝试", "整轮失败", "取消"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q: %s", want, out.String())
		}
	}
}

func TestReviewAgentTimeoutConfigurationPersistsWithoutChangingOrder(t *testing.T) {
	s := configStore(t)
	before, err := journal.ReadConfig(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = configure(s, []string{"--agent-timeout", "240"}, strings.NewReader(""), &out, nil); err != nil {
		t.Fatal(err)
	}
	after, err := journal.ReadConfig(s.Root)
	if err != nil || after.Agent.TimeoutSeconds != 240 || strings.Join(after.Agent.Order, ",") != strings.Join(before.Agent.Order, ",") {
		t.Fatalf("timeout was not persisted independently: %d (%v)", after.Agent.TimeoutSeconds, err)
	}
	for _, value := range []string{"9", "601"} {
		if err = configure(s, []string{"--agent-timeout", value}, strings.NewReader(""), &out, nil); err == nil {
			t.Fatal("invalid timeout accepted")
		}
	}
}

func TestReviewTodaySummarizesAndBoundsHumanOutputOnly(t *testing.T) {
	s := configStore(t)
	start := time.Date(2026, 10, 15, 10, 0, 0, 0, time.Local)
	r := journal.Report{SchemaVersion: 2, BatchID: "20261015T100000Z", Start: start, End: start.Add(2 * time.Hour), CreatedAt: start.Add(2 * time.Hour), UnknownSpans: []journal.TimeSpan{{Start: start.Add(30 * time.Minute), End: start.Add(2 * time.Hour)}}}
	for i := 0; i < 25; i++ {
		at := start.Add(time.Duration(i) * time.Minute)
		r.Cards = append(r.Cards, journal.Card{Title: fmt.Sprintf("记录%02d", i), Summary: "合成活动", Start: at, End: at.Add(30 * time.Second), DurationMethod: "bounded_observation_v1", EstimatedSeconds: 30, TaskID: "fixture-task", Spans: []journal.TimeSpan{{Start: at, End: at.Add(30 * time.Second)}}})
	}
	dir := filepath.Join(s.Root, "reports", start.Format("20060102"))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	if err := os.WriteFile(filepath.Join(dir, r.BatchID+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--data", s.Root, "today", "--date", "2026-10-15"}
	data, err := commandOutput(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"25 个活动片段", "--full", "无法估时", "采样间隔"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing summary %q: %s", want, text)
		}
	}
	if strings.Contains(text, "\n  10:24–") {
		t.Fatal("default human output remained unbounded")
	}
	data, err = commandOutput(t, append(args, "--full")...)
	if err != nil || !strings.Contains(string(data), "\n  10:24–") {
		t.Fatal("full view omitted records", err)
	}
	data, err = commandOutput(t, append(args, "--json")...)
	var export struct {
		Reports []journal.Report `json:"reports"`
	}
	if err != nil || json.Unmarshal(data, &export) != nil || len(export.Reports) != 1 || len(export.Reports[0].Cards) != 25 {
		t.Fatal("human limit truncated the machine export", err)
	}
}

func TestReviewEmptyTodayExplainsStoppedBackendAndRecentDate(t *testing.T) {
	s := configStore(t)
	start := time.Date(2026, 10, 15, 10, 0, 0, 0, time.Local)
	report := journal.Report{BatchID: "20261015T100000Z", CreatedAt: start, Cards: []journal.Card{{Title: "历史合成活动", Start: start, End: start.Add(time.Minute)}}}
	dir := filepath.Join(s.Root, "reports", "20261015")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(report)
	if err := os.WriteFile(filepath.Join(dir, report.BatchID+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := commandOutput(t, "--data", s.Root, "today", "--date", "2026-10-16")
	if err != nil || !strings.Contains(string(data), "后台已停止") || !strings.Contains(string(data), "daylog today --date 2026-10-15") {
		t.Fatalf("empty view lost actionable history: %s (%v)", data, err)
	}
}
