package journal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"daylog/internal/native"
)

func workFixture(at time.Time) Report {
	r := Report{SchemaVersion: 2, AppSource: "window_server", BatchID: ID(at), Start: at, End: at.Add(10 * time.Minute), EstimationVersion: estimationVersion, UnknownSeconds: 525,
		Cards: []Card{{TaskID: "fixture-task", ActivityID: "fixture-activity", Title: "合成任务", Confidence: "high", Start: at, End: at.Add(75 * time.Second), EstimatedSeconds: 75, Spans: []TimeSpan{{Start: at, End: at.Add(75 * time.Second)}}}}}
	for i := 0; i <= 20; i++ {
		r.TaskObservations = append(r.TaskObservations, TaskObservation{At: at.Add(time.Duration(i) * 30 * time.Second), State: "sampled", MaxGapSeconds: 60})
	}
	return r
}

func TestRunnerKeepsReadingGraceUntilTwentyMinutes(t *testing.T) {
	s := testStore(t)
	c := DefaultConfig()
	if c.IdleSeconds != 1200 {
		t.Fatal("new installs do not use the twenty-minute rule")
	}
	source := &testSource{data: imageData(t), env: native.Environment{App: "fixture", Permission: true}}
	r := Runner{Store: s, Config: c, Source: source}
	at := time.Now().UTC().Truncate(time.Hour)
	for second := 0; second < 1200; second += 30 {
		source.env.IdleSeconds = float64(second)
		r.step(context.Background(), at.Add(time.Duration(second)*time.Second))
	}
	source.env.IdleSeconds = 1199
	r.step(context.Background(), at.Add(1199*time.Second))
	if r.snapshot().SkippedIdle != 0 {
		t.Fatal("reading grace ended before twenty minutes")
	}
	before := source.captures
	source.env.IdleSeconds = 1200
	r.step(context.Background(), at.Add(1200*time.Second))
	if source.captures != before || r.observedState != "idle" {
		t.Fatal("idle threshold did not stop capture at twenty minutes")
	}
	obs, err := s.ReadObservations(at, at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := newWorkLedger(nil, nil, Batch{}, obs).summary(at, at.Add(time.Hour), nil)
	if w.EstimatedSeconds != 1200 {
		t.Fatalf("first twenty minutes were retroactively removed: %+v", w)
	}
	for _, p := range obs {
		if p.MaxGapSeconds != 180 {
			t.Fatal("effective sampling bound was not persisted", p.MaxGapSeconds)
		}
	}
}

func TestWorkTimeRetainsUnclassifiedSamplesWithoutInventingTasks(t *testing.T) {
	at := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	r := workFixture(at)
	// Duplicate committed input must not count time twice.
	p := BuildOverview(at, []Report{r, r}, nil, Batch{}).Views[0]
	w := p.WorkTime
	if w.EstimatedSeconds != 600 || w.ClassifiedSeconds != 75 || w.UnclassifiedSeconds != 525 || w.MissingSeconds != 0 {
		t.Fatalf("classification gaps dropped computer activity: %+v", w)
	}
	if p.EstimatedSeconds != 75 || len(p.Tasks) != 1 || p.Tasks[0].EstimatedSeconds != 75 || p.Continuity.LongestSeconds > 75 {
		t.Fatalf("unclassified activity was promoted to a task or continuous attention: estimate=%d tasks=%+v longest=%d", p.EstimatedSeconds, p.Tasks, p.Continuity.LongestSeconds)
	}
	if r.UnknownSeconds != 525 || r.Cards[0].EstimatedSeconds != 75 {
		t.Fatal("historical interpretation mutated")
	}
	r.Cards = nil
	w = ReportWorkTime([]Report{r}, at, at.Add(time.Hour))
	if w.EstimatedSeconds != 600 || w.ClassifiedSeconds != 0 || w.UnclassifiedSeconds != 600 {
		t.Fatal("unknown-only analysis lost available work evidence", w)
	}
}

func TestWorkTimeDistinguishesAwayFaultsAndLongGaps(t *testing.T) {
	at := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                string
		seconds             []int
		states              []string
		work, away, missing int64
	}{
		{"lock_and_capture_fault", []int{0, 30, 60, 90, 90, 120, 150, 300, 330}, []string{"sampled", "sampled", "capture_unavailable", "active", "sampled", "sampled", "screen_locked", "active", "sampled"}, 150, 150, 30},
		{"wake_boundary", []int{0, 30, 100, 100, 100, 130}, []string{"sampled", "sampled", "missing", "active", "sampled", "sampled"}, 60, 0, 70},
		{"long_gap", []int{0, 30, 500}, []string{"sampled", "sampled", "sampled"}, 30, 0, 470},
		{"idle_after_grace", []int{0, 30, 60, 120}, []string{"sampled", "sampled", "idle", "active"}, 60, 60, 0},
		{"sleep", []int{0, 30, 60, 120}, []string{"sampled", "sampled", "screen_asleep", "active"}, 60, 60, 0},
		{"pause_is_not_absence", []int{0, 30, 60, 120}, []string{"sampled", "sampled", "paused", "active"}, 60, 0, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var obs []Observation
			for i, second := range tc.seconds {
				obs = append(obs, Observation{At: at.Add(time.Duration(second) * time.Second), State: tc.states[i], MaxGapSeconds: 60})
			}
			w := newWorkLedger(nil, nil, Batch{}, obs).summary(at, at.Add(time.Hour), nil)
			if w.EstimatedSeconds != tc.work || w.AwaySeconds != tc.away || w.MissingSeconds != tc.missing || w.UnclassifiedSeconds != tc.work {
				t.Fatalf("wrong availability interpretation: %+v", w)
			}
		})
	}
}

func TestWorkTimeDoesNotExtrapolateSingleFrameAndIncludesDraft(t *testing.T) {
	at := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	r := workFixture(at)
	r.Cards = nil
	r.TaskObservations = r.TaskObservations[:1]
	w := ReportWorkTime([]Report{r}, at, at.Add(time.Hour))
	if w.EstimatedSeconds != 0 || w.MissingSeconds != 600 {
		t.Fatal("single point invented duration", w)
	}
	b := Batch{SchemaVersion: 2, Start: at, End: at.Add(time.Minute), MaxGapSeconds: 60, Observations: []Observation{{At: at, State: "sampled"}, {At: at.Add(time.Minute), State: "sampled"}}}
	w = BuildOverview(at, nil, nil, b).Views[0].WorkTime
	if w.EstimatedSeconds != 60 || w.UnclassifiedSeconds != 60 || w.ClassifiedSeconds != 0 {
		t.Fatal("unsealed activity missing from live summary", w)
	}
	if exported := ReportWorkTime(nil, at, at.Add(time.Hour)); exported.EstimatedSeconds != 0 {
		t.Fatal("uncommitted evidence leaked into durable export")
	}
}

func TestWorkTimeClipsMidnightAndFallBackUsingElapsedIntervals(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 24, 0, 0, 0, 0, zone)
	midnight := day.AddDate(0, 0, 1)
	obs := []Observation{{At: midnight.Add(-30 * time.Second), State: "sampled", MaxGapSeconds: 120}, {At: midnight.Add(30 * time.Second), State: "sampled", MaxGapSeconds: 120}}
	l := newWorkLedger(nil, nil, Batch{}, obs)
	if a, b := l.summary(day, midnight, nil), l.summary(midnight, midnight.AddDate(0, 0, 1), nil); a.EstimatedSeconds != 30 || b.EstimatedSeconds != 30 {
		t.Fatal("midnight duplicated or dropped work", a, b)
	}
	a, _ := time.Parse(time.RFC3339, "2026-11-01T05:59:30Z")
	b, _ := time.Parse(time.RFC3339, "2026-11-01T06:00:30Z")
	obs = []Observation{{At: a, State: "sampled", MaxGapSeconds: 120}, {At: b, State: "sampled", MaxGapSeconds: 120}}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, zone)
	if w := newWorkLedger(nil, nil, Batch{}, obs).summary(start, start.AddDate(0, 0, 1), nil); w.EstimatedSeconds != 60 {
		t.Fatal("civil clock rollback changed elapsed duration", w)
	}
}

func TestWorkTimeRetainsSupportedOlderSpansButHonorsExplicitAbsence(t *testing.T) {
	at := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	classified := []span{{at, at.Add(time.Minute)}}
	// Sparse old event metadata is not evidence that its supported card is false.
	obs := []Observation{{At: at.Add(-time.Hour), State: "sampled", MaxGapSeconds: 60}, {At: at.Add(time.Hour), State: "sampled", MaxGapSeconds: 60}}
	w := newWorkLedger(nil, nil, Batch{}, obs).summary(at, at.Add(time.Minute), classified)
	if w.EstimatedSeconds != 60 || w.ClassifiedSeconds != 60 {
		t.Fatal("supported old spans discarded due to absent event metadata", w)
	}
	obs = []Observation{{At: at, State: "screen_locked"}, {At: at.Add(time.Minute), State: "active"}}
	w = newWorkLedger(nil, nil, Batch{}, obs).summary(at, at.Add(time.Minute), classified)
	if w.EstimatedSeconds != 0 || w.ClassifiedSeconds != 0 || w.AwaySeconds != 60 {
		t.Fatal("task inference overrode an explicit lock", w)
	}
}

func TestTimingReaderIgnoresOnlyIncompleteTail(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	dir := filepath.Join(s.Root, "observations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-10-24.jsonl")
	complete := "{\"at\":\"2026-10-24T10:00:00Z\",\"state\":\"sampled\"}\n"
	if err := os.WriteFile(path, []byte(complete+"{\"at\":"), 0600); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.ReadObservations(at, at.Add(time.Hour)); err != nil || len(rows) != 1 {
		t.Fatal("partial append made timing unavailable", len(rows), err)
	}
	if err := os.WriteFile(path, []byte(complete+"broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadObservations(at, at.Add(time.Hour)); err == nil {
		t.Fatal("malformed completed timing record silently accepted")
	}
}
