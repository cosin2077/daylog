package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPeriodBoundsUseCalendarWeeksMonthsAndDST(t *testing.T) {
	zone := time.FixedZone("local", 8*3600)
	anchor := time.Date(2026, 1, 1, 12, 0, 0, 0, zone)
	start, end, err := PeriodBounds("week", anchor)
	if err != nil || start.Format("2006-01-02") != "2025-12-29" || end.Format("2006-01-02") != "2026-01-05" {
		t.Fatal("week did not cross year correctly")
	}
	start, end, _ = PeriodBounds("month", time.Date(2024, 2, 15, 0, 0, 0, 0, zone))
	if int(end.Sub(start).Hours()/24) != 29 {
		t.Fatal("leap month lost a day")
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start, end, _ = PeriodBounds("day", time.Date(2026, 3, 8, 12, 0, 0, 0, newYork))
	if end.Sub(start) != 23*time.Hour {
		t.Fatal("DST day treated as fixed 24 hours")
	}
	if _, _, err = PeriodBounds("year", anchor); err == nil {
		t.Fatal("unsupported period accepted")
	}
}

func TestPeriodClipsSplitsAndDeduplicatesCoverage(t *testing.T) {
	zone := time.FixedZone("local", 8*3600)
	anchor := time.Date(2026, 10, 13, 0, 0, 0, 0, zone)
	reports := []Report{
		{BatchID: "one", Source: "agent:claude", Cards: []Card{{Start: anchor.Add(-10 * time.Minute), End: anchor.Add(20 * time.Minute), Title: "跨日片段", Category: "工作", Evidence: []string{"a"}}}},
		{BatchID: "two", Source: "api", Cards: []Card{{Start: anchor.Add(10 * time.Minute), End: anchor.Add(40 * time.Minute), Title: "重叠片段", Category: "沟通", Evidence: []string{"b"}}}},
	}
	pending := []Batch{{Start: anchor.Add(time.Hour), End: anchor.Add(2 * time.Hour), LastError: "fixture"}}
	draft := Batch{Frames: []Frame{{At: anchor.Add(3 * time.Hour)}, {At: anchor.Add(48 * time.Hour)}}}
	o := BuildOverview(anchor, reports, pending, draft)
	day, week, month := o.Views[0], o.Views[1], o.Views[2]
	if day.CoverageSeconds != 40*60 || len(day.Activities) != 2 || day.Activities[0].Start != anchor || day.ActiveDays != 1 {
		t.Fatalf("day clipping/union incorrect: %+v", day)
	}
	if week.CoverageSeconds != 50*60 || len(week.Activities) != 3 || week.ActiveDays != 2 || len(month.Days) != 31 {
		t.Fatal("cross-day fragments or month incorrect")
	}
	if day.PendingBatches != 1 || day.FailedBatches != 1 || day.DraftFrames != 1 || day.EvidenceCount != 2 || day.AnalyzedBatches != 2 {
		t.Fatal("incomplete evidence counts missing")
	}
	if week.Days[0].CoverageSeconds != 10*60 || week.Days[1].CoverageSeconds != 40*60 {
		t.Fatal("local date coverage incorrect")
	}
}

func TestHTMLKeepsUntrustedTextInJSONAndExcludesConfigSecrets(t *testing.T) {
	s := testStore(t)
	c := DefaultConfig()
	c.AI.APIKey = "fixture-secret-must-not-export"
	if err := WriteJSON(filepath.Join(s.Root, "config.json"), c); err != nil {
		t.Fatal(err)
	}
	b := testBatch(t, s, time.Now())
	r := testReport(b)
	r.Cards[0].Title = `</script><img src=x onerror=alert(1)>`
	if err := s.Commit(r, "agent:claude"); err != nil {
		t.Fatal(err)
	}
	o, err := s.Overview(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data, err := RenderHTML(o, "day")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), r.Cards[0].Title) || strings.Contains(string(data), c.AI.APIKey) {
		t.Fatal("raw HTML injection or configuration secret exported")
	}
	match := regexp.MustCompile(`(?s)<script type="application/json" id="report-data">(.*?)</script>`).FindSubmatch(data)
	if len(match) != 2 {
		t.Fatal("report payload missing")
	}
	var decoded Overview
	if err = json.Unmarshal(match[1], &decoded); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if decoded.Views[0].Activities[0].Title != r.Cards[0].Title {
		t.Fatal("escaped report text was lost")
	}
	path, err := s.WriteHTML(o, "day")
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("HTML report is not private")
	}
}

func TestCalendarIncludesClippedObservationAndUnknownSpans(t *testing.T) {
	zone := time.FixedZone("local", 8*3600)
	anchor := time.Date(2026, 10, 14, 0, 0, 0, 0, zone)
	r := Report{SchemaVersion: 2, AppSource: "window_server", BatchID: "fixture", Start: anchor.Add(-time.Minute), End: anchor.Add(4 * time.Minute),
		UnknownSpans: []TimeSpan{{Start: anchor.Add(time.Minute), End: anchor.Add(3 * time.Minute)}, {Start: anchor.Add(2 * time.Minute), End: anchor.Add(4 * time.Minute)}},
		Cards:        []Card{{Start: anchor.Add(-time.Minute), End: anchor.Add(time.Minute), Title: "编辑", Category: "工作", TaskID: "task", DurationMethod: "bounded_observation_v1", Apps: []string{"Editor"}, Spans: []TimeSpan{{Start: anchor.Add(-time.Minute), End: anchor.Add(time.Minute)}}}}}
	o := BuildOverview(anchor, []Report{r}, nil, Batch{})
	byDate := map[string]PeriodReport{}
	for _, d := range o.Calendar {
		byDate[d.Anchor] = d
	}
	if len(o.Calendar) < 61 || byDate["2026-09-01"].Anchor == "" || byDate["2026-10-31"].Anchor == "" {
		t.Fatal("date picker calendar missing")
	}
	day := byDate["2026-10-14"]
	if day.Anchor != "2026-10-14" || day.EstimatedSeconds != 60 || day.UnknownSeconds != 180 || len(day.UnknownSpans) != 1 {
		t.Fatalf("calendar time was double-counted: %+v", day)
	}
	a := day.Activities[0]
	if len(a.Spans) != 1 || !a.Spans[0].Start.Equal(anchor) || len(a.Apps) != 1 || a.Apps[0] != "Editor" {
		t.Fatal("timeline lacks clipped evidence or app search metadata")
	}
	if byDate["2026-10-13"].EstimatedSeconds != 60 || byDate["2026-10-15"].EstimatedSeconds != 0 {
		t.Fatal("calendar navigation leaked time across days")
	}
}
