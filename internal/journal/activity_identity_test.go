package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSharedGoalPreservesIntermediateActionDuration(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	b := Batch{ID: ID(at), SchemaVersion: 2, AppSource: "fixture", Start: at, End: at.Add(180 * time.Second), MaxGapSeconds: 120}
	for i := 0; i < 3; i++ {
		point := at.Add(time.Duration(i) * 90 * time.Second)
		id := ID(point)
		b.Frames = append(b.Frames, Frame{ID: id, At: point, App: "browser"})
		b.Observations = append(b.Observations, Observation{At: point, App: "browser", FrameID: id, State: "sampled"})
	}
	code := Segment{Title: "编码", Summary: "编辑缓存边界", Category: "写代码", Confidence: "medium", TaskKey: "cache", TaskTitle: "缓存目标", Actions: []string{}, Uncertainties: []string{}, Evidence: []string{b.Frames[0].ID, b.Frames[2].ID}}
	docs := code
	docs.Title = "查文档"
	docs.Category = "查资料"
	docs.Evidence = []string{b.Frames[1].ID}
	r, err := deriveReport(b, Interpretation{SchemaVersion: 2, BatchID: b.ID, Segments: []Segment{code, docs}}, "")
	if err != nil {
		t.Fatal(err)
	}
	amounts := map[string]int64{}
	for _, c := range r.Cards {
		amounts[c.Category] += c.EstimatedSeconds
	}
	if amounts["写代码"] != 90 || amounts["查资料"] != 90 {
		t.Fatal("middle action swallowed", amounts)
	}
	// Old v3 inferred spans are recalculated from archived evidence, without images.
	s := testStore(t)
	old := r
	old.EstimationVersion = "bounded_neighbors_v3"
	for i := range old.TaskObservations {
		old.TaskObservations[i].ActivityID = ""
	}
	for i := range old.Cards {
		c := &old.Cards[i]
		c.Spans = nil
		c.InferredSeconds = 0
		c.EstimatedSeconds = 0
		if c.Category == "写代码" {
			c.Spans = []TimeSpan{{Start: b.Start, End: b.End, Inferred: true}}
			c.InferredSeconds = 180
			c.EstimatedSeconds = 180
		}
	}
	os.MkdirAll(filepath.Dir(s.archivePath(b.ID)), 0700)
	if err = WriteJSON(s.archivePath(b.ID), b); err != nil {
		t.Fatal(err)
	}
	repaired := estimateReport(s.observationStates(old))
	amounts = map[string]int64{}
	for _, c := range repaired.Cards {
		amounts[c.Category] += c.EstimatedSeconds
	}
	if amounts["查资料"] != 90 || amounts["写代码"] != 90 {
		t.Fatal("archive repair lost ownership", amounts)
	}
	without := estimateReport(old)
	for _, c := range without.Cards {
		if c.EstimatedSeconds != 0 {
			t.Fatal("ambiguous old labels guessed action", c)
		}
	}
}
func TestFutureGoalActionDoesNotRelabelPastFragment(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	r := Report{SchemaVersion: 2, AppSource: "fixture", Start: at, End: at.Add(180 * time.Second), Cards: []Card{
		{ActivityID: "a", TaskID: "a", Title: "主任务", Category: "写代码", Confidence: "medium", Start: at, End: at.Add(300 * time.Second)},
		{ActivityID: "chat", TaskID: "b", Title: "沟通", Category: "聊天沟通", Confidence: "medium", Start: at.Add(time.Minute), End: at.Add(time.Minute)},
	}, TaskObservations: []TaskObservation{{ActivityID: "a", TaskID: "a", At: at, MaxGapSeconds: 120}, {ActivityID: "chat", TaskID: "b", At: at.Add(time.Minute), MaxGapSeconds: 120}, {ActivityID: "a", TaskID: "a", At: at.Add(2 * time.Minute), MaxGapSeconds: 120}}}
	before := buildContinuity(at, r.End, []Report{r})
	if len(before.Fragments) != 1 || before.Fragments[0].Category != "聊天沟通" {
		t.Fatal(before.Fragments)
	}
	r.End = at.Add(5 * time.Minute)
	r.Cards = append(r.Cards, Card{ActivityID: "docs", TaskID: "b", Title: "资料", Category: "查资料", Confidence: "medium", Start: at.Add(3 * time.Minute), End: at.Add(3 * time.Minute)})
	r.TaskObservations = append(r.TaskObservations, TaskObservation{ActivityID: "docs", TaskID: "b", At: at.Add(3 * time.Minute), MaxGapSeconds: 120}, TaskObservation{ActivityID: "a", TaskID: "a", At: at.Add(4 * time.Minute), MaxGapSeconds: 120})
	after := buildContinuity(at, r.End, []Report{r})
	if after.Fragments[0].Category != "聊天沟通" || after.Fragments[2].Category != "查资料" {
		t.Fatal(after.Fragments)
	}
}
