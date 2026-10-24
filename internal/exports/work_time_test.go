package exports

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"daylog/internal/journal"
)

func TestExportWorkTimeUsesCommittedSourcesOnly(t *testing.T) {
	s, err := journal.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	r := journal.Report{SchemaVersion: 2, AppSource: "window_server", EstimationVersion: "bounded_neighbors_v4", BatchID: journal.ID(at), Start: at, End: at.Add(10 * time.Minute), Cards: []journal.Card{{TaskID: "fixture-task", Title: "合成活动", Start: at, End: at.Add(time.Minute), Spans: []journal.TimeSpan{{Start: at, End: at.Add(time.Minute)}}}}}
	for i := 0; i <= 20; i++ {
		r.TaskObservations = append(r.TaskObservations, journal.TaskObservation{At: at.Add(time.Duration(i) * 30 * time.Second), State: "sampled", MaxGapSeconds: 60})
	}
	reportDir := filepath.Join(s.Root, "reports", r.BatchID[:8])
	if err = os.MkdirAll(reportDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = journal.WriteJSON(filepath.Join(reportDir, r.BatchID+".json"), r); err != nil {
		t.Fatal(err)
	}
	draft := journal.Batch{ID: journal.ID(at.Add(time.Hour)), SchemaVersion: 2, Start: at.Add(time.Hour), End: at.Add(time.Hour + time.Minute), MaxGapSeconds: 60, Observations: []journal.Observation{{At: at.Add(time.Hour), State: "sampled"}, {At: at.Add(time.Hour + time.Minute), State: "sampled"}}}
	draft.Frames = []journal.Frame{{ID: draft.ID, At: draft.Start}}
	if err = s.SaveDraft(draft); err != nil {
		t.Fatal(err)
	}
	local, _, err := s.ExportDay(at)
	if err != nil {
		t.Fatal(err)
	}
	day, err := LoadDay(s, at)
	if err != nil {
		t.Fatal(err)
	}
	if local.WorkTime.EstimatedSeconds != 660 || day.WorkTime.EstimatedSeconds != 600 || day.WorkTime.ClassifiedSeconds != 60 || day.WorkTime.UnclassifiedSeconds != 540 {
		t.Fatal("live and durable work sources mixed", local.WorkTime, day.WorkTime)
	}
	if len(day.SourceBatches) != 1 || day.SourceBatches[0] != r.BatchID || len(day.Activities) != 1 || day.EstimatedSeconds != 60 {
		t.Fatal("new total changed task data or manifest", day)
	}
}
