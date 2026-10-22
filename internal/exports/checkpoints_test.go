package exports

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDaytimeCheckpointsUpdateOneDateAndFinalizeAtMidnight(t *testing.T) {
	zone, _ := time.LoadLocation("Asia/Shanghai")
	day := time.Date(2026, 10, 22, 0, 0, 0, 0, zone)
	version := 0
	var sent []Day
	e := Engine{Root: t.TempDir(), Load: func(d time.Time) (Day, error) {
		return Day{Date: d.Format("2006-01-02"), Timezone: zone.String(), AnalyzedBatches: 1, EstimatedSeconds: int64(version)}, nil
	}, Sink: sinkFunc(func(_ context.Context, d Day) (string, error) { sent = append(sent, d); return "same-page", nil })}
	points := []int{12 * 60, 16 * 60, 18 * 60, 20 * 60, 22 * 60}
	for _, hour := range []int{11, 12, 13, 16, 18, 20, 22} {
		if hour != 13 {
			version++
		}
		if err := e.RunDue(context.Background(), day, day.Add(time.Duration(hour)*time.Hour), 90, points...); err != nil {
			t.Fatal(err)
		}
	}
	if len(sent) != 5 {
		t.Fatal("checkpoint fired early, twice, or missed", len(sent))
	}
	for _, d := range sent {
		if d.Date != "2026-10-22" || !d.Ongoing {
			t.Fatal("daytime used a different date or marked final")
		}
	}
	state, _ := ReadState(e.Root)
	if state.Days["2026-10-22"].LastCheckpoint != "22:00" {
		t.Fatal(state.Days)
	}
	if err := e.RunDue(context.Background(), day, day.AddDate(0, 0, 1), 90, points...); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 6 || sent[5].Date != "2026-10-22" || sent[5].Ongoing {
		t.Fatal("midnight did not finalize the same date")
	}
}

func TestMissedDaytimeSlotsCoalesceAndSurviveRestart(t *testing.T) {
	zone, _ := time.LoadLocation("Asia/Shanghai")
	day := time.Date(2026, 10, 22, 0, 0, 0, 0, zone)
	calls := 0
	e := Engine{Root: t.TempDir(), Load: func(d time.Time) (Day, error) { return Day{Date: d.Format("2006-01-02"), AnalyzedBatches: 1}, nil }, Sink: sinkFunc(func(context.Context, Day) (string, error) { calls++; return "url", nil })}
	points := []int{720, 960, 1080, 1200, 1320}
	if err := e.RunDue(context.Background(), day, day.Add(19*time.Hour), 90, points...); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("missed slots caused redundant uploads", calls)
	}
	restarted := e
	if err := restarted.RunDue(context.Background(), day, day.Add(19*time.Hour+time.Minute), 90, points...); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("restart repeated completed slot", calls)
	}
	// An unchanged snapshot completes the new slot without another remote write.
	if err := e.RunDue(context.Background(), day, day.Add(20*time.Hour), 90, points...); err != nil {
		t.Fatal(err)
	}
	state, _ := ReadState(e.Root)
	if calls != 1 || state.Days["2026-10-22"].LastCheckpoint != "20:00" {
		t.Fatal(calls, state.Days)
	}
}

func TestDaytimeRetryRespectsBackoffAndRetryAfter(t *testing.T) {
	day := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	calls := 0
	fail := true
	e := Engine{Root: t.TempDir(), Load: func(d time.Time) (Day, error) { return Day{Date: d.Format("2006-01-02"), AnalyzedBatches: 1}, nil }, Sink: sinkFunc(func(context.Context, Day) (string, error) {
		calls++
		if fail {
			return "", failing{delay: 2 * time.Hour}
		}
		return "url", nil
	})}
	for _, hour := range []int{12, 13} {
		if err := e.RunDue(context.Background(), day, day.Add(time.Duration(hour)*time.Hour), 90, 720, 960); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("Retry-After ignored", calls)
	}
	fail = false
	if err := e.RunDue(context.Background(), day, day.Add(14*time.Hour), 90, 720, 960); err != nil {
		t.Fatal(err)
	}
	state, _ := ReadState(e.Root)
	if calls != 2 || state.Days["2026-10-22"].LastCheckpoint != "12:00" || state.Days["2026-10-22"].LastError != "" {
		t.Fatal("failed slot did not recover", calls, state.Days)
	}
	if err := e.RunDue(context.Background(), day, day.Add(15*time.Hour), 90, -1); err == nil {
		t.Fatal("invalid checkpoint accepted")
	}
}

func TestDSTUsesCivilCheckpointsAndTodayHasPriority(t *testing.T) {
	zone, _ := time.LoadLocation("America/New_York")
	day := time.Date(2026, 3, 8, 0, 0, 0, 0, zone)
	var dates []string
	e := Engine{Root: t.TempDir(), Load: func(d time.Time) (Day, error) { return Day{Date: d.Format("2006-01-02"), AnalyzedBatches: 1}, nil }, Sink: sinkFunc(func(_ context.Context, d Day) (string, error) { dates = append(dates, d.Date); return "url", nil })}
	// 02:30 does not exist on this day; it is caught after the civil clock gap.
	e.RunDue(context.Background(), day, time.Date(2026, 3, 8, 1, 45, 0, 0, zone), 90, 150)
	if len(dates) != 0 {
		t.Fatal("nonexistent 02:30 fired early")
	}
	e.RunDue(context.Background(), day, time.Date(2026, 3, 8, 3, 0, 0, 0, zone), 90, 150)
	if len(dates) != 1 {
		t.Fatal("missing DST slot not caught up")
	}
	// A fresh noon checkpoint must not wait behind four old dates.
	dates = nil
	e.Root = t.TempDir()
	e.RunDue(context.Background(), day.AddDate(0, 0, -10), time.Date(2026, 3, 8, 12, 0, 0, 0, zone), 90, 720)
	if len(dates) != 4 || dates[0] != "2026-03-08" {
		t.Fatal("historical backlog starved today", dates)
	}
}

func TestRetryOfUnchangedFailedManualUpdateDoesNotStayStuck(t *testing.T) {
	day := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	fail := false
	calls := 0
	e := Engine{Root: t.TempDir(), Now: func() time.Time { return day.Add(11 * time.Hour) }, Load: func(d time.Time) (Day, error) { return Day{Date: d.Format("2006-01-02"), AnalyzedBatches: 1}, nil }, Sink: sinkFunc(func(context.Context, Day) (string, error) {
		calls++
		if fail {
			return "", errors.New("offline")
		}
		return "url", nil
	})}
	if _, err := e.Sync(context.Background(), day, true); err != nil {
		t.Fatal(err)
	}
	fail = true
	if _, err := e.Sync(context.Background(), day, true); err == nil {
		t.Fatal("failure not returned")
	}
	fail = false
	if err := e.RunDue(context.Background(), day, day.Add(12*time.Hour), 90, 720); err != nil {
		t.Fatal(err)
	}
	state, _ := ReadState(e.Root)
	if calls != 3 || state.Days["2026-10-22"].LastError != "" {
		t.Fatal("unchanged hash hid failed refresh", calls, state.Days)
	}
}

func TestFallBackRetryDoesNotRepeatCompletedCheckpoint(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, zone)
	calls, version := 0, int64(0)
	e := Engine{Root: t.TempDir(), Load: func(d time.Time) (Day, error) {
		return Day{Date: d.Format("2006-01-02"), AnalyzedBatches: 1, EstimatedSeconds: version}, nil
	}, Sink: sinkFunc(func(context.Context, Day) (string, error) {
		calls++
		if calls == 2 {
			return "", errors.New("offline")
		}
		return "url", nil
	})}
	// UTC disambiguates the two occurrences of 01:00–01:59 at fall-back.
	for i, clock := range []string{"05:15", "05:30", "06:10", "06:15"} {
		version++
		now, err := time.Parse(time.RFC3339, "2026-11-01T"+clock+":00Z")
		if err != nil {
			t.Fatal(err)
		}
		if err = e.RunDue(context.Background(), day, now.In(zone), 90, 60, 75, 90); err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			state, err := ReadState(e.Root)
			if err != nil {
				t.Fatal(err)
			}
			if job := state.Days["2026-11-01"]; job.LastError != "" || job.LastCheckpoint != "01:15" {
				t.Fatalf("recovery moved completed checkpoint backwards: %+v", job)
			}
		}
	}
	if calls != 3 {
		t.Fatalf("repeated an already completed civil checkpoint: got %d calls, want 3", calls)
	}
}
