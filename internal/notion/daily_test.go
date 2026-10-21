package notion

import (
	"context"
	"strings"
	"testing"
	"time"

	"daylog/internal/exports"
)

func TestManualTodayThenMidnightAndLateUpdateUseOnePage(t *testing.T) {
	s, f := setupFake(t)
	day := sampleDay(3)
	zone, _ := time.LoadLocation(day.Timezone)
	date, _ := time.ParseInLocation("2006-01-02", day.Date, zone)
	now := date.Add(12 * time.Hour)
	engine := exports.Engine{Root: s.Settings.StateRoot(s.Root), Sink: s,
		Now: func() time.Time { return now }, Load: func(time.Time) (exports.Day, error) { return day, nil }}
	if _, err := engine.Sync(context.Background(), date, true); err != nil {
		t.Fatal(err)
	}
	var id string
	for key := range f.pages {
		id = key
	}
	if !strings.Contains(f.markdown[id], "今天尚未结束") || str(obj(obj(obj(f.pages[id]["properties"])["状态"])["select"])["name"]) != "待补齐" {
		t.Fatal("today was presented as complete")
	}
	f.markdown[id] += "\n我手写的备注，不应被删除。"
	now = date.AddDate(0, 0, 1).Add(time.Minute)
	if err := engine.RunDue(context.Background(), date, now, 90); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || strings.Contains(f.markdown[id], "今天尚未结束") || !strings.Contains(f.markdown[id], "我手写的备注") {
		t.Fatal("midnight duplicated page, lost notes, or retained ongoing state")
	}
	if str(obj(obj(obj(f.pages[id]["properties"])["状态"])["select"])["name"]) != "已整理" {
		t.Fatal("ended day did not finalize")
	}
	day.Activities[0].Summary = "后续补齐了更详细的事实描述"
	day.SourceBatches = append(day.SourceBatches, "late-batch")
	if err := engine.RunDue(context.Background(), date, now.Add(time.Minute), 90); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || !strings.Contains(f.markdown[id], "后续补齐了更详细的事实描述") || !strings.Contains(f.markdown[id], "我手写的备注") {
		t.Fatal("late analysis did not replace owned content")
	}
	before := f.markdown[id]
	day.SourceBatches = []string{"batch"}
	if _, err := engine.Sync(context.Background(), date, true); err == nil || f.markdown[id] != before {
		t.Fatal("manual sync shrank history after losing a source batch", err)
	}
}

func TestLostBodyResponseReconcilesWithoutDuplicateOrNotesLoss(t *testing.T) {
	s, f := setupFake(t)
	day := sampleDay(1)
	if _, err := s.Deliver(context.Background(), day); err != nil {
		t.Fatal(err)
	}
	var id string
	for key := range f.pages {
		id = key
	}
	f.markdown[id] += "\n保留的个人备注。"
	f.dropMarkdown = true
	day.Activities[0].Summary = "合成补齐描述"
	if _, err := s.Deliver(context.Background(), day); err == nil {
		t.Fatal("lost update response claimed success")
	}
	f.mu.Lock()
	patches := f.markdownPatches
	f.mu.Unlock()
	if _, err := s.Deliver(context.Background(), day); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || f.markdownPatches != patches || !strings.Contains(f.markdown[id], "保留的个人备注") {
		t.Fatal("committed body update repeated or notes lost")
	}
}

func TestTruncatedOrUnmarkedBodyDoesNotGetOverwritten(t *testing.T) {
	s, f := setupFake(t)
	day := sampleDay(1)
	if _, err := s.Deliver(context.Background(), day); err != nil {
		t.Fatal(err)
	}
	var id string
	for key := range f.pages {
		id = key
	}
	day.Activities[0].Summary = "需要更新的内容"
	f.truncated = true
	before := f.markdown[id]
	if _, err := s.Deliver(context.Background(), day); err == nil || f.markdown[id] != before {
		t.Fatal("truncated content overwritten", err)
	}
	f.truncated = false
	f.markdown[id] = "用户自己写的页面，没有自动记录区。"
	if _, err := s.Deliver(context.Background(), day); err == nil || f.markdown[id] != "用户自己写的页面，没有自动记录区。" {
		t.Fatal("unmarked user body overwritten", err)
	}
}

func TestExplicitExistingDatabaseAndTargetIsolation(t *testing.T) {
	s, f := setupFake(t)
	selected := s.Settings.DailyDatabase
	settings, _ := NewSettings(s.Settings.ParentPage, "Asia/Shanghai", "2026-10-18")
	settings.DailyDatabase = selected
	other := &Service{Root: t.TempDir(), Settings: settings, Client: s.Client}
	if err := other.Setup(context.Background()); err != nil || len(f.databases) != 1 {
		t.Fatal("explicit compatible database was not reused", err)
	}
	before := other.Settings.StateRoot(other.Root)
	other.Settings.DailyDatabase = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if before == other.Settings.StateRoot(other.Root) {
		t.Fatal("different targets share receipts and sync progress")
	}
	f.databases[selected]["parent"] = map[string]any{"page_id": "cccccccccccccccccccccccccccccccc"}
	s.Settings.DailyDatabase = selected
	if err := s.Setup(context.Background()); err == nil {
		t.Fatal("database outside the selected parent was accepted")
	}
}

func TestRendererKeepsEvidenceBoundariesAndEscapesHeadingInjection(t *testing.T) {
	day := sampleDay(3)
	day.Activities[0].Title = "代码 task_id\n## 我的备注\n<page>"
	day.Activities[0].Summary = "参考 [文档](https://example.invalid) 与 C:\\demo；**不是完成证明**"
	day.Activities[1].Start = day.Activities[1].End
	day.Activities[1].EstimatedSeconds = 0
	day.Activities[2].DurationMethod = "legacy_model_range"
	markdown := renderDay(day) + "\n\n" + notesHeading + "\n保留。"
	if _, err := managedContent(markdown); err != nil {
		t.Fatal("activity text escaped the generated section", err)
	}
	if !strings.Contains(markdown, "持续时长未估计") || !strings.Contains(markdown, "旧版范围，未计入") || !strings.Contains(markdown, "空白不等于休息") {
		t.Fatal("estimation evidence boundary missing")
	}
}
