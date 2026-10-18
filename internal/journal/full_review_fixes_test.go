package journal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreparationFailuresBackoffWithoutProviderBudget(t *testing.T) {
	for _, damage := range []string{"missing", "digest", "invalid"} {
		t.Run(damage, func(t *testing.T) {
			s := testStore(t)
			at := time.Now().Add(-time.Hour)
			b := auditPending(t, s, at, 2)
			healthy := auditPending(t, s, at.Add(15*time.Minute), 2)
			path, err := s.FramePath(b.Frames[0])
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "missing":
				os.Remove(path)
			case "digest":
				os.WriteFile(path, append(original, byte(1)), 0600)
			case "invalid":
				os.WriteFile(path, []byte("not an image"), 0600)
				b.Frames[0].SHA256 = ""
				WriteJSON(filepath.Join(s.Root, "batches", b.ID+".json"), b)
			}
			c := DefaultConfig()
			c.Diagnostics.FailureNotifications = false
			c.AI.Enabled = true
			c.AI.FallbackAfterAgents = true
			c.AI.Endpoint = "https://fixture.invalid"
			c.AI.Model = "never-called"
			t.Setenv("DAYLOG_AGENT_SEARCH_PATH", t.TempDir())
			err = s.AnalyzeConfigured(context.Background(), b.ID, c, "", "", nil)
			var prep *preparationError
			if !errors.As(err, &prep) {
				t.Fatal("not a preparation failure", err)
			}
			after, _ := s.Batch(b.ID)
			if after.Attempts != 0 || after.FailureRounds != 1 || after.RetryAfter == nil || PreparationState(after) != "failed" {
				t.Fatal("bad preparation state", after)
			}
			pending, _ := s.Pending(0)
			next, ok := nextAnalysis(pending, time.Now())
			if !ok || next.ID != healthy.ID {
				t.Fatal("healthy batch starved")
			}
			if files, _ := os.ReadDir(filepath.Join(s.Root, "usage")); len(files) != 0 {
				t.Fatal("preparation charged provider budget")
			}
			os.WriteFile(path, original, 0600)
			if err = s.RequestRetry(b.ID); err != nil {
				t.Fatal(err)
			}
			after, _ = s.Batch(b.ID)
			if !RetryReady(after, time.Now()) || after.Attempts != 0 || after.FailureRounds != 1 {
				t.Fatal("retry reset counters")
			}
			if _, err = s.analysisImage(context.Background(), after.Frames[0]); err != nil {
				t.Fatal("repaired image cannot be retried", err)
			}
		})
	}
}

func TestCanceledPreparationDoesNotBecomeFailure(t *testing.T) {
	s := testStore(t)
	b := auditPending(t, s, time.Now().Add(-time.Hour), 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := s.AnalyzeConfigured(ctx, b.ID, DefaultConfig(), "", "", nil)
	after, _ := s.Batch(b.ID)
	if !errors.Is(err, context.Canceled) || after.Attempts != 0 || after.FailureRounds != 0 || after.LastError != "" {
		t.Fatal(err, after)
	}
}

func TestFrozenContextSurvivesNewHistoryAndManualCommit(t *testing.T) {
	s := testStore(t)
	at := time.Now().Add(-time.Hour)
	a := auditPending(t, s, at, 2)
	b := auditPending(t, s, at.Add(15*time.Minute), 2)
	raw, _ := json.Marshal(v2Input(a))
	if err := s.CommitAnalysis(a.ID, raw, "fixture", ""); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PrepareAnalysis(b.ID)
	if err != nil || len(prepared.Context) != 1 {
		t.Fatal(err, prepared)
	}
	// A frozen allowlist is still returned unchanged even after its source file vanishes.
	os.Remove(s.reportPath(a.ID))
	preview, err := s.PreviewAnalysis(b.ID)
	if err != nil || !preview.ContextReady || len(preview.Context) != 1 {
		t.Fatal("query changed frozen request", err)
	}
	in := v2Input(b)
	in.Segments[0].TaskID = prepared.Context[0].TaskID
	raw, _ = json.Marshal(in)
	if err = s.CommitAnalysis(b.ID, raw, "manual", ""); err != nil {
		t.Fatal("manual commit lost frozen allowlist", err)
	}
}

func TestSealFailureIsNotQueueFull(t *testing.T) {
	s := testStore(t)
	at := time.Now()
	b := auditPending(t, s, at, 1)
	b.ID = ID(at.Add(time.Minute))
	b.MaxGapSeconds = 1
	r := Runner{Store: s, Config: DefaultConfig(), draft: b}
	err := r.seal()
	if err == nil {
		t.Fatal("invalid bounds accepted")
	}
	r.setSealError(err)
	if r.snapshot().Error != "batch_seal_failed" || len(r.draft.Frames) != 1 {
		t.Fatal("lost draft or wrong classification")
	}
	r.setSealError(ErrQueueFull)
	if r.snapshot().Error != "queue_full" {
		t.Fatal("queue error lost")
	}
}

func TestInterpolationKeepsEstimateButNotContinuity(t *testing.T) {
	for _, kind := range []string{"omitted", "repeat", "neighbor"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			at := time.Now().Add(-time.Hour)
			b := v2Batch(t, s, at)
			b.AppSource = "window_server"
			b.End = at.Add(180 * time.Second)
			b.SupportGapSeconds = 300
			first := b.Frames[0]
			last := first
			last.ID = ID(b.End)
			last.At = b.End
			b.Frames = append(b.Frames, last)
			middle := first.ID
			if kind == "omitted" {
				middle = "omitted-new-frame"
			}
			b.Observations = []Observation{{At: at, State: "sampled", App: "editor", FrameID: first.ID}, {At: at.Add(90 * time.Second), State: "sampled", App: "editor", FrameID: middle}, {At: b.End, State: "sampled", App: "editor", FrameID: last.ID}}
			in := v2Input(b)
			in.Segments[0].Evidence = append(in.Segments[0].Evidence, last.ID)
			if kind == "neighbor" {
				in.Segments[0].Evidence = []string{first.ID}
				other := in.Segments[0]
				other.Title = "另一任务"
				other.Evidence = []string{last.ID}
				in.Segments = append(in.Segments, other)
			}
			r, err := deriveReport(b, in, "")
			if err != nil {
				t.Fatal(err)
			}
			v := BuildOverview(at, []Report{r}, nil, Batch{}).Views[0]
			switch kind {
			case "omitted":
				if v.InterpolatedSeconds != 180 || v.EstimatedSeconds != 180 || v.Continuity.LongestSeconds != 0 {
					t.Fatal(v)
				}
			case "repeat":
				if v.InterpolatedSeconds != 0 || v.EstimatedSeconds != 180 || v.Continuity.LongestSeconds != 180 {
					t.Fatal(v)
				}
			case "neighbor":
				if v.InferredSeconds != 90 || v.InterpolatedSeconds != 0 || v.Continuity.LongestSeconds != 90 {
					t.Fatal(v)
				}
			}
		})
	}
}

func TestReviewAdviceRequiresSupportingFacts(t *testing.T) {
	p := PeriodReport{Period: "week", Start: time.Now(), ActiveDays: 3, AnalyzedBatches: 3, Activities: []Activity{{Title: "single frame"}}, Tasks: []TaskSummary{{TaskID: "a", Title: "task"}}}
	for _, state := range []string{"zero", "mostly_unknown", "pending", "mostly_estimated"} {
		t.Run(state, func(t *testing.T) {
			v := p
			v.Tasks = append([]TaskSummary(nil), p.Tasks...)
			if state != "zero" {
				v.EstimatedSeconds = 60
				v.Tasks[0].EstimatedSeconds = 60
			}
			if state == "mostly_unknown" {
				v.UnknownSeconds = 600
			}
			if state == "mostly_estimated" {
				v.InterpolatedSeconds = 60
			}
			if state == "pending" {
				v.PendingBatches = 10
			}
			r := buildReview(v)
			if strings.Contains(r.Advice, "预留") || strings.Contains(r.Advice, "排行") {
				t.Fatal(r)
			}
		})
	}
}
