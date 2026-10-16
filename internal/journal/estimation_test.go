package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func neighborFixture() Report {
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	end := at.Add(90 * time.Second)
	return Report{SchemaVersion: 2, AppSource: "window_server", BatchID: ID(at), Start: at, End: end, UnknownSeconds: 90, UnknownSpans: []TimeSpan{{Start: at, End: end}}, Cards: []Card{
		{TaskID: "a", ActivityID: "first", Title: "编写程序", Category: "写代码", Confidence: "medium", Start: at, End: at},
		{TaskID: "b", ActivityID: "second", Title: "阅读文档", Category: "查资料", Confidence: "high", Start: end, End: end},
	}, TaskObservations: []TaskObservation{{At: at, State: "sampled", App: "editor", TaskID: "a", MaxGapSeconds: 120}, {At: end, State: "sampled", App: "editor", TaskID: "b", MaxGapSeconds: 120}}}
}

func TestNeighborEstimatesRespectAvailabilityAndPreserveUnknown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Report)
		want   int64
	}{
		{"two identified", func(r *Report) {}, 90},
		{"reused picture label outside keyframe bounds", func(r *Report) { r.Cards[1].Start = r.Start; r.Cards[1].End = r.Start }, 90},
		{"unknown screenshot", func(r *Report) { r.TaskObservations[1].TaskID = "" }, 45},
		{"unknown historical endpoint", func(r *Report) { r.TaskObservations[1].TaskID = ""; r.TaskObservations[1].State = "" }, 0},
		{"low confidence", func(r *Report) { r.Cards[1].Confidence = "low" }, 0},
		{"locked", func(r *Report) { r.TaskObservations[1].TaskID = ""; r.TaskObservations[1].State = "screen_locked" }, 0},
		{"missing", func(r *Report) { r.TaskObservations[1].TaskID = ""; r.TaskObservations[1].State = "missing" }, 0},
		{"long interval", func(r *Report) {
			r.TaskObservations[1].At = r.End.Add(5 * time.Minute)
			r.End = r.TaskObservations[1].At
			r.Cards[1].Start = r.End
			r.Cards[1].End = r.End
			r.UnknownSeconds = 390
		}, 0},
		{"legacy app metadata", func(r *Report) { r.AppSource = "" }, 0},
		{"switch without image", func(r *Report) {
			r.TaskObservations = append(r.TaskObservations, TaskObservation{At: r.Start.Add(30 * time.Second), State: "active", App: "browser", MaxGapSeconds: 120})
		}, 0},
		{"conflicting same-time labels", func(r *Report) {
			p := r.TaskObservations[0]
			p.TaskID = "b"
			r.TaskObservations = append(r.TaskObservations, p)
		}, 0},
		{"unknown cross-app picture", func(r *Report) { r.TaskObservations[1].TaskID = ""; r.TaskObservations[1].App = "browser" }, 0},
		{"both cross-app identified", func(r *Report) { r.TaskObservations[1].App = "browser" }, 90},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := neighborFixture()
			tc.change(&r)
			r.UnknownSpans = []TimeSpan{{Start: r.Start, End: r.End}}
			r.UnknownSeconds = int64(r.End.Sub(r.Start) / time.Second)
			before, _ := json.Marshal(r)
			estimated := estimateReport(r)
			var seconds int64
			for _, c := range estimated.Cards {
				seconds += c.InferredSeconds
			}
			if seconds != tc.want {
				t.Fatalf("inferred %d, want %d", seconds, tc.want)
			}
			if tc.want > 0 && seconds+estimated.UnknownSeconds != int64(r.End.Sub(r.Start)/time.Second) {
				t.Fatal("estimated and unknown time do not conserve the interval")
			}
			after, _ := json.Marshal(r)
			if string(before) != string(after) {
				t.Fatal("source report mutated")
			}
			again, _ := json.Marshal(estimateReport(estimated))
			once, _ := json.Marshal(estimated)
			if string(again) != string(once) {
				t.Fatal("inference is not idempotent")
			}
		})
	}
}

func TestRecordedAppActivationLocatesTheTaskBoundary(t *testing.T) {
	r := neighborFixture()
	r.TaskObservations[1].App = "browser"
	r.TaskObservations = append(r.TaskObservations, TaskObservation{At: r.Start.Add(30 * time.Second), State: "active", App: "browser", MaxGapSeconds: 120})
	v := estimateReport(r)
	if v.Cards[0].InferredSeconds != 30 || v.Cards[1].InferredSeconds != 60 || v.UnknownSeconds != 0 {
		t.Fatal("activation was replaced by an arbitrary midpoint", v.Cards)
	}
	r.TaskObservations[1].TaskID = ""
	v = estimateReport(r)
	if v.Cards[0].InferredSeconds != 30 || v.Cards[1].InferredSeconds != 0 || v.UnknownSeconds != 60 {
		t.Fatal("unknown application content was assigned to a task")
	}
}

func TestPeriodNavigationKeepsEdgeWeekDaysAndDistinctMonths(t *testing.T) {
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	o := BuildOverview(at, nil, nil, Batch{})
	days := map[string]bool{}
	for _, d := range o.Calendar {
		days[d.Anchor] = true
	}
	months := map[string]bool{}
	for _, v := range o.Periods {
		if v.Period == "month" {
			months[v.Anchor] = true
		}
		if v.Period == "week" {
			for _, d := range v.Days {
				if !days[d.Date] {
					t.Fatal("week chart lacks the day's summary", d.Date)
				}
			}
		}
	}
	if len(months) != 2 || !months["2026-09-01"] || !months["2026-10-01"] {
		t.Fatal("month navigation includes a partial unrelated month", months)
	}
}

func TestHistoricalEstimationUsesArchivedStatesWithoutRewritingFiles(t *testing.T) {
	s := testStore(t)
	r := neighborFixture()
	for i := range r.TaskObservations {
		r.TaskObservations[i].State = ""
	}
	path := s.reportPath(r.BatchID)
	os.MkdirAll(filepath.Dir(path), 0700)
	if err := WriteJSON(path, r); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	read := func() int64 {
		reports, err := s.Reports(r.Start, r.End.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		return BuildOverview(r.Start, reports, nil, Batch{}).Views[0].InferredSeconds
	}
	if read() != 0 {
		t.Fatal("missing archive did not preserve uncertainty")
	}
	b := Batch{ID: r.BatchID, SchemaVersion: 2, AppSource: r.AppSource, Start: r.Start, End: r.End, MaxGapSeconds: 120, Observations: []Observation{{At: r.Start, App: "editor", State: "sampled"}, {At: r.End, App: "editor", State: "sampled"}}}
	archive := s.archivePath(r.BatchID)
	os.MkdirAll(filepath.Dir(archive), 0700)
	if err := WriteJSON(archive, b); err != nil {
		t.Fatal(err)
	}
	if read() != 90 {
		t.Fatal("archived sampled states were not restored")
	}
	// Real captures record activation and the sampled picture at one timestamp.
	// The availability hint must not become a conflicting unidentified sample.
	b.Observations = append([]Observation{{At: r.Start, State: "active", App: "editor"}}, b.Observations...)
	if err := WriteJSON(archive, b); err != nil {
		t.Fatal(err)
	}
	if read() != 90 {
		t.Fatal("same-time activation erased the identified sample")
	}
	b.Observations = b.Observations[1:]
	b.Observations = append(b.Observations[:1], append([]Observation{{At: r.Start.Add(30 * time.Second), State: "active", App: "browser"}}, b.Observations[1:]...)...)
	if err := WriteJSON(archive, b); err != nil {
		t.Fatal(err)
	}
	if read() != 0 {
		t.Fatal("archived app activation was ignored")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("historical report was rewritten")
	}
}

func TestInferredTimeClipsAcrossMidnightWithoutBecomingContinuous(t *testing.T) {
	r := neighborFixture()
	at := time.Date(2026, 10, 15, 23, 59, 30, 0, time.UTC)
	shift := at.Sub(r.Start)
	r.Start = r.Start.Add(shift)
	r.End = r.End.Add(shift)
	for i := range r.Cards {
		r.Cards[i].Start = r.Cards[i].Start.Add(shift)
		r.Cards[i].End = r.Cards[i].End.Add(shift)
	}
	for i := range r.TaskObservations {
		r.TaskObservations[i].At = r.TaskObservations[i].At.Add(shift)
	}
	r.UnknownSpans = []TimeSpan{{Start: r.Start, End: r.End}}
	one := buildPeriod("day", r.Start, []Report{r}, nil, Batch{})
	two := buildPeriod("day", r.End, []Report{r}, nil, Batch{})
	if one.InferredSeconds != 30 || two.InferredSeconds != 60 || one.EstimatedSeconds+two.EstimatedSeconds != 90 || one.Continuity.LongestSeconds != 0 || two.Continuity.LongestSeconds != 0 {
		t.Fatal("cross-midnight inference leaked or claimed continuous attention", one.InferredSeconds, two.InferredSeconds)
	}
}

func TestActivityAndRhythmMergingKeepGapsAndProvenance(t *testing.T) {
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	makeActivity := func(start time.Time, id, task string) Activity {
		return Activity{Start: start, End: start.Add(time.Minute), TaskID: task, ActivityID: id, BatchID: id, Category: "写代码", Summary: id, Spans: []TimeSpan{{Start: start, End: start.Add(time.Minute)}}, EstimatedSeconds: 60}
	}
	a, b := makeActivity(at, "one", "task"), makeActivity(at.Add(time.Minute), "two", "task")
	merged := mergeActivities([]Activity{a, b})
	if len(merged) != 1 || len(merged[0].Parts) != 2 || merged[0].Parts[1].BatchID != "two" || merged[0].EstimatedSeconds != 120 {
		t.Fatal("adjacent task lost provenance", merged)
	}
	b.TaskID = "other"
	if len(mergeActivities([]Activity{a, b})) != 2 {
		t.Fatal("different tasks merged")
	}
	blocks := buildRhythm([]Activity{a, b})
	if len(blocks) != 1 || !blocks[0].End.Equal(b.End) {
		t.Fatal("adjacent same categories remain fragmented")
	}
	b = makeActivity(at.Add(2*time.Minute), "three", "task")
	if len(mergeActivities([]Activity{a, b})) != 2 || len(buildRhythm([]Activity{a, b})) != 2 {
		t.Fatal("a missing minute was painted over")
	}
	a.TaskID = ""
	b.TaskID = ""
	if len(mergeActivities([]Activity{a, b})) != 2 {
		t.Fatal("unknown task IDs merged")
	}
}
