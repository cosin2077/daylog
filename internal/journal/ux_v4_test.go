package journal

import (
	"strings"
	"testing"
	"time"
)

func TestV4HeadlineNamesTheLeadingTaskAndOmitsUnclassified(t *testing.T) {
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	p := PeriodReport{Period: "week", Start: at, Activities: []Activity{{Title: "阅读公开信"}}, EstimatedSeconds: 600, Categories: []CategorySummary{{Name: "未细分", EstimatedSeconds: 400}, {Name: "写代码", EstimatedSeconds: 200}}, Tasks: []TaskSummary{{TaskID: "read", Title: "阅读公开信", EstimatedSeconds: 350}}}
	r := buildReview(p)
	if !strings.Contains(r.Headline, "阅读公开信") || strings.Contains(r.Headline, "未细分") || !strings.Contains(r.Headline, "这一周") {
		t.Fatalf("headline disagrees with ranking or exposes classification fallback: %s", r.Headline)
	}
	if displayApp("ru.keepcoder.Telegram") != "Telegram" {
		t.Fatal("Telegram bundle ID is exposed as a source name")
	}
}

func TestV4BoundedAdjacentObservationsEstimateSwitchTime(t *testing.T) {
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	r := Report{SchemaVersion: 2, AppSource: "window_server", BatchID: "fixture", Start: at, End: at.Add(180 * time.Second), UnknownSpans: []TimeSpan{{Start: at, End: at.Add(90 * time.Second)}}, Cards: []Card{
		{TaskID: "a", Title: "写代码", Category: "写代码", Confidence: "medium", Start: at, End: at},
		{TaskID: "b", Title: "阅读文档", Category: "查资料", Confidence: "medium", Start: at.Add(90 * time.Second), End: at.Add(180 * time.Second), Spans: []TimeSpan{{Start: at.Add(90 * time.Second), End: at.Add(180 * time.Second)}}},
	}, TaskObservations: []TaskObservation{{At: at, App: "editor", TaskID: "a", MaxGapSeconds: 120, State: "sampled"}, {At: at.Add(90 * time.Second), App: "editor", TaskID: "b", MaxGapSeconds: 120, State: "sampled"}, {At: at.Add(180 * time.Second), App: "editor", TaskID: "b", MaxGapSeconds: 120, State: "sampled"}}}
	p := buildPeriod("day", at, []Report{r}, nil, Batch{})
	if p.EstimatedSeconds != 180 || p.UnknownSeconds != 0 || len(p.Activities) != 2 || p.Activities[0].EstimatedSeconds != 45 || p.Activities[1].EstimatedSeconds != 135 {
		t.Fatalf("bounded switch interval was discarded: estimated=%d unknown=%d activities=%+v", p.EstimatedSeconds, p.UnknownSeconds, p.Activities)
	}
	if p.Continuity.LongestSeconds != 90 {
		t.Fatal("inferred boundary was promoted to continuous observation")
	}
	if len(r.Cards[0].Spans) != 0 || len(r.UnknownSpans) != 1 {
		t.Fatal("read-time estimation rewrote source records")
	}
}
