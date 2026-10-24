// Package exports defines the small, versioned data interface shared by output
// integrations. It contains interpreted metadata, never images or credentials.
package exports

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"daylog/internal/journal"
)

type Activity struct {
	Apps                []string           `json:"apps,omitempty"`
	Source              string             `json:"source"`
	Model               string             `json:"model,omitempty"`
	PromptVersion       string             `json:"prompt_version,omitempty"`
	DurationMethod      string             `json:"duration_method"`
	EvidenceCount       int                `json:"evidence_count"`
	ID                  string             `json:"id"`
	TaskID              string             `json:"task_id"`
	Task                string             `json:"task"`
	Title               string             `json:"title"`
	Summary             string             `json:"summary"`
	Category            string             `json:"category"`
	Start               time.Time          `json:"start"`
	End                 time.Time          `json:"end"`
	EstimatedSeconds    int64              `json:"estimated_seconds"`
	InferredSeconds     int64              `json:"inferred_seconds"`
	InterpolatedSeconds int64              `json:"interpolated_seconds"`
	Confidence          string             `json:"confidence"`
	Actions             []string           `json:"actions,omitempty"`
	Uncertainties       []string           `json:"uncertainties,omitempty"`
	Spans               []journal.TimeSpan `json:"spans,omitempty"`
}

type Day struct {
	WorkTime              journal.WorkTime `json:"work_time"`
	Ongoing               bool             `json:"ongoing"`
	DraftFrames           int              `json:"draft_frames"`
	LegacyCoverageSeconds int64            `json:"legacy_coverage_seconds"`
	SourceBatches         []string         `json:"source_batches"`
	SchemaVersion         int              `json:"schema_version"`
	Date                  string           `json:"date"`
	Timezone              string           `json:"timezone"`
	EstimatedSeconds      int64            `json:"estimated_seconds"`
	InferredSeconds       int64            `json:"inferred_seconds"`
	InterpolatedSeconds   int64            `json:"interpolated_seconds"`
	UnknownSeconds        int64            `json:"unknown_seconds"`
	AnalyzedBatches       int              `json:"analyzed_batches"`
	PendingBatches        int              `json:"pending_batches"`
	FailedBatches         int              `json:"failed_batches"`
	Activities            []Activity       `json:"activities"`
}

func (d Day) HasData() bool {
	return d.AnalyzedBatches > 0 || d.PendingBatches > 0 || len(d.Activities) > 0 || d.UnknownSeconds > 0 || d.DraftFrames > 0
}
func (d Day) Hash() string {
	raw, _ := json.Marshal(d)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Keep the original activity segments instead of the merged display rows, so
// late analysis never changes an existing segment's exported identity.
func LoadDay(s *journal.Store, day time.Time) (Day, error) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	end := start.AddDate(0, 0, 1)
	p, reports, err := s.ExportDay(start)
	if err != nil {
		return Day{}, err
	}
	result := Day{DraftFrames: p.DraftFrames, LegacyCoverageSeconds: p.LegacyCoverageSeconds, SchemaVersion: 1, Date: start.Format("2006-01-02"), Timezone: start.Location().String(), EstimatedSeconds: p.EstimatedSeconds, InferredSeconds: p.InferredSeconds, InterpolatedSeconds: p.InterpolatedSeconds, UnknownSeconds: p.UnknownSeconds, AnalyzedBatches: p.AnalyzedBatches, PendingBatches: p.PendingBatches, FailedBatches: p.FailedBatches, Activities: []Activity{}, SourceBatches: []string{}}
	// Only committed sources contribute exported duration; draft and queue
	// counts stay visible without weakening the durable source manifest.
	result.WorkTime = journal.ReportWorkTime(reports, start, end)
	result.Ongoing = time.Now().In(start.Location()).Before(end)
	for _, r := range reports {
		result.SourceBatches = append(result.SourceBatches, r.BatchID)
		for index, c := range r.Cards {
			from, to := c.Start, c.End
			if from.Before(start) {
				from = start
			}
			if to.After(end) {
				to = end
			}
			if to.Before(from) || !c.Start.Before(end) || c.End.Before(start) {
				continue
			}
			id := c.ActivityID
			if id == "" {
				id = r.BatchID + ":" + stringID(index)
			}
			task := c.TaskTitle
			if task == "" {
				task = c.Title
			}
			a := Activity{DurationMethod: c.DurationMethod, Apps: c.Apps, Source: r.Source, Model: r.Model, PromptVersion: r.PromptVersion, EvidenceCount: len(c.Evidence), ID: id + ":" + result.Date, TaskID: c.TaskID, Task: task, Title: c.Title, Summary: c.Summary, Category: c.Category, Start: from.In(start.Location()), End: to.In(start.Location()), Confidence: c.Confidence, Actions: c.Actions, Uncertainties: c.Uncertainties}
			if r.SchemaVersion != 2 {
				a.DurationMethod = "legacy_model_range"
			}
			var inferred, interpolated []journal.TimeSpan
			for _, span := range c.Spans {
				f, t := span.Start, span.End
				if f.Before(start) {
					f = start
				}
				if t.After(end) {
					t = end
				}
				if !t.After(f) {
					continue
				}
				part := span
				part.Start = f.In(start.Location())
				part.End = t.In(start.Location())
				a.Spans = append(a.Spans, part)
				if part.Inferred {
					inferred = append(inferred, part)
				}
				if part.Interpolated {
					interpolated = append(interpolated, part)
				}
			}
			a.EstimatedSeconds = coverage(a.Spans)
			a.InferredSeconds = coverage(inferred)
			a.InterpolatedSeconds = coverage(interpolated)
			result.Activities = append(result.Activities, a)
		}
	}
	sort.SliceStable(result.Activities, func(i, j int) bool {
		a, b := result.Activities[i], result.Activities[j]
		if a.Start.Equal(b.Start) {
			return a.ID < b.ID
		}
		return a.Start.Before(b.Start)
	})
	sort.Strings(result.SourceBatches)
	return result, nil
}

func coverage(input []journal.TimeSpan) int64 {
	spans := append([]journal.TimeSpan(nil), input...)
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })
	var total time.Duration
	var end time.Time
	for _, s := range spans {
		start := s.Start
		if start.Before(end) {
			start = end
		}
		if s.End.After(start) {
			total += s.End.Sub(start)
		}
		if s.End.After(end) {
			end = s.End
		}
	}
	return int64(total / time.Second)
}
