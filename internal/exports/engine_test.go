package exports

import (
	"context"
	"daylog/internal/journal"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type sinkFunc func(context.Context, Day) (string, error)

func (f sinkFunc) Deliver(c context.Context, d Day) (string, error) { return f(c, d) }

type failing struct {
	delay   time.Duration
	blocked bool
}

func (f failing) Error() string             { return "fixture unavailable" }
func (f failing) RetryDelay() time.Duration { return f.delay }
func (f failing) Permanent() bool           { return f.blocked }
func TestMidnightCatchupStableDateAndLateUpdate(t *testing.T) {
	zone, _ := time.LoadLocation("Asia/Shanghai")
	start := time.Date(2026, 10, 18, 0, 0, 0, 0, zone)
	now := start.AddDate(0, 0, 3).Add(8 * time.Hour)
	revision := 0
	var dates []string
	fail := true
	engine := Engine{Root: t.TempDir(), Load: func(d time.Time) (Day, error) {
		return Day{SchemaVersion: 1, Date: d.Format("2006-01-02"), Timezone: zone.String(), AnalyzedBatches: 1, PendingBatches: revision}, nil
	}, Sink: sinkFunc(func(_ context.Context, d Day) (string, error) {
		dates = append(dates, d.Date)
		if fail {
			return "", errors.New("offline")
		}
		return "url", nil
	})}
	if err := engine.RunDue(context.Background(), start, now, 90); err != nil {
		t.Fatal(err)
	}
	if len(dates) != 3 {
		t.Fatal(dates)
	}
	state, _ := ReadState(engine.Root)
	if state.Days["2026-10-18"].Attempts != 1 {
		t.Fatal(state)
	}
	fail = false
	dates = nil
	if err := engine.RunDue(context.Background(), start, now.Add(2*time.Minute), 90); err != nil {
		t.Fatal(err)
	}
	if len(dates) != 3 || dates[0] != "2026-10-18" || dates[2] != "2026-10-20" {
		t.Fatal("retry used execution date", dates)
	}
	dates = nil
	engine.RunDue(context.Background(), start, now.Add(3*time.Minute), 90)
	if len(dates) != 0 {
		t.Fatal("unchanged snapshots sent again")
	}
	revision = 1
	engine.RunDue(context.Background(), start, now.Add(4*time.Minute), 90)
	if len(dates) != 3 {
		t.Fatal("late analysis not exported")
	}
}
func TestDSTCalendarAndNoDataAndGlobalQuota(t *testing.T) {
	zone, _ := time.LoadLocation("America/Los_Angeles")
	start := time.Date(2026, 3, 7, 0, 0, 0, 0, zone)
	now := time.Date(2026, 3, 10, 0, 0, 0, 0, zone)
	var dates []string
	e := Engine{Root: t.TempDir(), Load: func(d time.Time) (Day, error) { return Day{Date: d.Format("2006-01-02"), AnalyzedBatches: 1}, nil }, Sink: sinkFunc(func(_ context.Context, d Day) (string, error) { dates = append(dates, d.Date); return "", nil })}
	e.RunDue(context.Background(), start, now, 90)
	if len(dates) != 3 || dates[1] != "2026-03-08" {
		t.Fatal(dates)
	}
	e.Root = t.TempDir()
	calls := 0
	e.Sink = sinkFunc(func(context.Context, Day) (string, error) { calls++; return "", failing{delay: 2 * time.Hour} })
	e.RunDue(context.Background(), start, now, 90)
	state, _ := ReadState(e.Root)
	if calls != 1 || !state.PauseUntil.Equal(now.Add(2*time.Hour)) {
		t.Fatal(calls, state)
	}
	e.RunDue(context.Background(), start, now.Add(time.Hour), 90)
	if calls != 1 {
		t.Fatal("Retry-After ignored")
	}
	e.Root = t.TempDir()
	e.Sink = sinkFunc(func(context.Context, Day) (string, error) { calls++; return "", failing{blocked: true} })
	e.RunDue(context.Background(), start, now, 90)
	n := calls
	e.RunDue(context.Background(), start, now.Add(24*time.Hour), 90)
	if calls != n {
		t.Fatal("permanent failure retried automatically")
	}
}
func TestLoadDayClipsAndPreservesOriginalSegments(t *testing.T) {
	s, err := journal.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	s.Init()
	zone := time.FixedZone("test", 8*3600)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, zone)
	from := day.Add(-time.Minute)
	until := day.Add(time.Minute)
	r := journal.Report{BatchID: journal.ID(from), SchemaVersion: 2, AppSource: "fixture", Start: from, End: until, Cards: []journal.Card{{ActivityID: "first", TaskID: "goal", TaskTitle: "目标", Title: "片段一", Summary: "事实描述", Category: "写代码", Confidence: "medium", Start: from, End: until, Spans: []journal.TimeSpan{{Start: from, End: until, Interpolated: true}}}, {ActivityID: "second", TaskID: "goal", Title: "片段二", Category: "写代码", Confidence: "medium", Start: until, End: until}}}
	dir := filepath.Join(s.Root, "reports", from.UTC().Format("20060102"))
	os.MkdirAll(dir, 0700)
	journal.WriteJSON(filepath.Join(dir, r.BatchID+".json"), r)
	d, err := LoadDay(s, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Activities) != 2 || d.Activities[0].EstimatedSeconds != 60 || d.Activities[0].InterpolatedSeconds != 60 || d.Activities[0].Start.Before(day) {
		t.Fatal(d)
	}
	if d.Hash() != d.Hash() {
		t.Fatal("unstable fingerprint")
	}
	empty, _ := LoadDay(s, day.AddDate(0, 0, 2))
	if empty.HasData() {
		t.Fatal(empty)
	}
}
