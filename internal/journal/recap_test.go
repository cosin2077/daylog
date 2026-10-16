package journal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecapKeepsInstantActivitiesWithoutInventingTime(t *testing.T) {
	at := time.Date(2025, 10, 15, 0, 0, 0, 0, time.UTC)
	r := Report{SchemaVersion: 2, AppSource: "window_server", BatchID: ID(at), Start: at, End: at.Add(time.Hour), Cards: []Card{
		{Start: at, End: at, Title: "日初单帧", Category: "查资料"},
		{Start: at.Add(time.Minute), End: at.Add(time.Minute), Title: "普通单帧", Category: "写代码"},
		{Start: at.AddDate(0, 0, 1), End: at.AddDate(0, 0, 1), Title: "次日", Category: "写代码"},
	}}
	v := buildPeriod("day", at, []Report{r}, nil, Batch{})
	if len(v.Activities) != 2 || v.EstimatedSeconds != 0 || v.CoverageSeconds != 0 {
		t.Fatalf("instant observations were dropped or timed: count=%d duration=%d", len(v.Activities), v.EstimatedSeconds)
	}
	s := testStore(t)
	dir := filepath.Join(s.Root, "reports", "20251015")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(filepath.Join(dir, r.BatchID+".json"), r); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Reports(at, at.AddDate(0, 0, 1))
	if err != nil || len(loaded) != 1 || len(loaded[0].Cards) != 2 {
		t.Fatal("daily store query disagrees on instantaneous records", err)
	}
}

func TestComparisonAlignsCalendarProgressAndClipsPriorActivities(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, kind, now, start, end string
		partial                     bool
	}{
		{"partial week", "week", "2026-10-14T12:00:00", "2026-10-05T00:00:00", "2026-10-07T12:00:00", true},
		{"partial month", "month", "2026-05-14T12:00:00", "2026-04-01T00:00:00", "2026-04-14T12:00:00", true},
		{"short prior month", "month", "2026-03-31T12:00:00", "2026-02-01T00:00:00", "2026-03-01T00:00:00", true},
		{"DST week", "week", "2026-11-01T12:00:00", "2026-10-19T00:00:00", "2026-10-25T12:00:00", true},
		{"year boundary", "week", "2026-01-01T12:00:00", "2025-12-22T00:00:00", "2025-12-25T12:00:00", true},
		{"day", "day", "2026-10-14T12:00:00", "2026-10-13T00:00:00", "2026-10-13T12:00:00", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parse := func(value string) time.Time {
				v, err := time.ParseInLocation("2006-01-02T15:04:05", value, zone)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			now, start, end := parse(tc.now), parse(tc.start), parse(tc.end)
			before, after := end.Add(-time.Hour), end.Add(time.Hour)
			r := Report{SchemaVersion: 2, AppSource: "window_server", Start: before, End: after, Cards: []Card{{TaskID: "prior", Title: "上期任务", Category: "写代码", Start: before, End: after, Spans: []TimeSpan{{Start: before, End: after}}}}}
			c := buildComparison(tc.kind, now, now, []Report{r})
			if !c.Start.Equal(start) || !c.End.Equal(end) || !c.CurrentEnd.Equal(now) || c.Partial != tc.partial || !c.Available || c.EstimatedSeconds != 3600 || c.Activities != 1 {
				t.Fatalf("incorrect comparison: %+v; want %v..%v", c, start, end)
			}
			if empty := buildComparison(tc.kind, now, now, nil); empty.Available || empty.EstimatedSeconds != 0 {
				t.Fatal("missing history became a numeric baseline")
			}
		})
	}
	anchor := time.Date(2026, 1, 15, 0, 0, 0, 0, zone)
	c := buildComparison("month", anchor, anchor.AddDate(0, 2, 0), nil)
	if c.Partial || c.Start.Format("2006-01-02") != "2025-12-01" || c.End.Format("2006-01-02") != "2026-01-01" || c.CurrentEnd.Format("2006-01-02") != "2026-02-01" {
		t.Fatal("completed month was not compared with the complete prior month", c)
	}
}

func TestRecapHeadlineUsesTimedCategoriesAndKeepsUnknownTasksSeparate(t *testing.T) {
	at := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	r := Report{SchemaVersion: 2, AppSource: "window_server", Start: at, End: at.Add(time.Hour), Cards: []Card{
		{Title: "主要工作", Category: "写代码", Start: at, End: at.Add(10 * time.Minute), Spans: []TimeSpan{{Start: at, End: at.Add(10 * time.Minute)}}},
		{Title: "另一个没有任务编号的工作", Category: "查资料", Start: at.Add(15 * time.Minute), End: at.Add(16 * time.Minute), Spans: []TimeSpan{{Start: at.Add(15 * time.Minute), End: at.Add(16 * time.Minute)}}},
	}}
	for i := 0; i < 10; i++ {
		point := at.Add(time.Duration(20+i) * time.Minute)
		r.Cards = append(r.Cards, Card{Title: "一次消息", Category: "聊天沟通", Start: point, End: point})
	}
	p := buildPeriod("day", at, []Report{r}, nil, Batch{})
	if !strings.Contains(p.Review.Headline, "写代码为主") || strings.Contains(p.Review.Headline, "聊天沟通") || len(p.Tasks) != 0 || p.EstimatedSeconds != 660 {
		t.Fatalf("untimed count overrode duration or unknown task IDs were merged: %+v", p.Review)
	}
}

func TestFragmentSourcesUseObservedAppsAndConserveCounts(t *testing.T) {
	at := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	r := continuityFixture(at)
	for i := range r.TaskObservations {
		if r.TaskObservations[i].TaskID == "b" {
			r.TaskObservations[i].App = "com.tencent.xinWeChat"
		}
	}
	c := buildContinuity(r.Start, r.End, []Report{r})
	if len(c.FragmentSources) != 1 || c.FragmentSources[0].Name != "微信" || c.FragmentSources[0].Count != 1 || c.FragmentSources[0].EstimatedSeconds != 60 {
		t.Fatalf("actual application source was lost: %+v", c.FragmentSources)
	}
	fragments := []TimeFragment{{TaskID: "a", SourceApps: []string{"code.exe", "chrome.exe"}, EstimatedSeconds: 10}, {TaskID: "b", SourceApps: []string{"chrome.exe", "code.exe"}, EstimatedSeconds: 20}, {TaskID: "c"}}
	sources := fragmentSources(fragments)
	if len(sources) != 2 || sources[0].Count != 2 || sources[0].EstimatedSeconds != 30 || sources[1].Name != "来源未记录" {
		t.Fatal("source grouping invented identities or duplicated counts", sources)
	}
}

func TestRecapCancellationIsNotCountedAsFailedPreparation(t *testing.T) {
	at := time.Date(2025, 10, 15, 0, 0, 0, 0, time.UTC)
	pending := []Batch{{Start: at, End: at.Add(time.Hour), LastError: "claude:canceled", Attempts: 2}, {Start: at, End: at.Add(time.Hour), LastError: "hermes:timeout", FailureRounds: 1}}
	v := buildPeriod("day", at, nil, pending, Batch{})
	if v.PendingBatches != 2 || v.FailedBatches != 1 || len(v.Failures) != 1 {
		t.Fatalf("canceled work is mislabelled as failure: %d failed / %d details", v.FailedBatches, len(v.Failures))
	}
}
