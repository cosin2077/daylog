package journal

import (
	"context"
	"daylog/internal/native"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFullReviewSamplingCadenceAndLockBoundary(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	source := &testSource{data: imageData(t), env: native.Environment{App: "editor", Permission: true}}
	r := Runner{Store: s, Config: DefaultConfig(), Source: source}
	for i := 0; i <= 6; i++ {
		r.step(context.Background(), at.Add(time.Duration(i)*30*time.Second))
	}
	status := r.snapshot()
	t.Logf("0..180 sec, every30sec step: native_captures=%d saved=%d unchanged=%d observations=%d", status.Captures, status.Saved, status.SkippedUnchanged, len(r.draft.Observations))
	if status.Captures != 3 || status.Saved != 1 || status.SkippedUnchanged != 2 {
		t.Fatal("default cadence/dedup differs from declared policy")
	}
	source.env.Locked = true
	r.step(context.Background(), at.Add(210*time.Second))
	after := r.snapshot()
	if after.Captures != 3 || after.Error != "screen_locked" {
		t.Fatal("locked screen must not capture")
	}
}

func TestFullReviewAcceptedFastConfigCannotSeal(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	c := DefaultConfig()
	c.IntervalSeconds = 10
	c.MinSaveSeconds = 10
	c.BatchSeconds = 300
	if err := c.Validate(); err != nil {
		t.Fatal("configuration rejected:", err)
	}
	source := &testSource{data: imageData(t), env: native.Environment{App: "editor", Permission: true}}
	r := Runner{Store: s, Config: c, Source: source}
	r.step(context.Background(), at)
	r.step(context.Background(), at.Add(10*time.Second))
	source.env.Locked = true
	r.step(context.Background(), at.Add(20*time.Second))
	pending, _ := s.Pending(0)
	t.Logf("config_valid=true sample_interval=10 draft_max_gap=%d pending=%d status_error=%q", r.draft.MaxGapSeconds, len(pending), r.snapshot().Error)
	if len(pending) != 1 || r.snapshot().Error != "screen_locked" {
		t.Error("accepted 10s configuration cannot seal and falsely reports full queue")
	}
}

func TestFullReviewInterpolationAndContinuityClaim(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	b := v2Batch(t, s, at)
	b.AppSource = "window_server"
	b.SupportGapSeconds = 300
	b.OmittedFrames = 1
	second := b.Frames[0]
	second.ID = ID(at.Add(180 * time.Second))
	second.At = at.Add(180 * time.Second)
	b.Frames = append(b.Frames, second)
	b.End = second.At
	b.Observations = []Observation{{At: at, App: "editor", State: "sampled", FrameID: b.Frames[0].ID}, {At: at.Add(90 * time.Second), App: "editor", State: "sampled", FrameID: "omitted-new-image"}, {At: second.At, App: "editor", State: "sampled", FrameID: second.ID}}
	in := v2Input(b)
	in.Segments[0].Evidence = append(in.Segments[0].Evidence, second.ID)
	report, err := deriveReport(b, in, "")
	if err != nil {
		t.Fatal(err)
	}
	o := BuildOverview(at, []Report{report}, nil, Batch{})
	v := o.Views[0]
	t.Logf("omitted_new_image=1 estimated=%d interpolated=%d inferred=%d longest_continuous=%d", v.EstimatedSeconds, v.InterpolatedSeconds, v.InferredSeconds, v.Continuity.LongestSeconds)
	if v.InterpolatedSeconds > 0 && v.Continuity.LongestSeconds == v.InterpolatedSeconds {
		t.Error("interpolated unseen image is counted as continuous observation while HTML only labels inferred_seconds")
	}
}

func auditPending(t *testing.T, s *Store, at time.Time, count int) Batch {
	t.Helper()
	c := DefaultConfig()
	b := Batch{ID: ID(at), SchemaVersion: 2, AppSource: "window_server", Start: at, End: at.Add(5 * time.Minute), MaxGapSeconds: 180}
	for i := 0; i < count; i++ {
		f, err := s.SaveFrame(at.Add(time.Duration(i)*90*time.Second), "com.google.Chrome", imageData(t), c)
		if err != nil {
			t.Fatal(err)
		}
		b.Frames = append(b.Frames, f)
		b.Observations = append(b.Observations, Observation{At: f.At, App: f.App, FrameID: f.ID, State: "sampled"})
	}
	if err := s.SaveDraft(b); err != nil {
		t.Fatal(err)
	}
	if err := s.SealDraft(b, c); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFullReviewEvidenceFailureStarvesHealthyBatch(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	broken := auditPending(t, s, at, 2)
	healthy := auditPending(t, s, at.Add(15*time.Minute), 2)
	p, err := s.FramePath(broken.Frames[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DAYLOG_AGENT_SEARCH_PATH", t.TempDir())
	c := DefaultConfig()
	c.Diagnostics.FailureNotifications = false
	for i := 0; i < 2; i++ {
		pending, err := s.Pending(0)
		if err != nil {
			t.Fatal(err)
		}
		chosen, ok := nextAnalysis(pending, time.Now())
		if !ok {
			t.Fatal("no chosen batch")
		}
		if i == 1 && chosen.ID != healthy.ID {
			t.Fatal("healthy batch starved", chosen.ID)
		}
		err = s.AnalyzeConfigured(context.Background(), chosen.ID, c, "", "", nil)
		current, _ := s.Batch(chosen.ID)
		t.Logf("round=%d chosen=%s healthy=%s err=%v attempts=%d failure_rounds=%d last_error=%q retry_after=%v", i, chosen.ID, healthy.ID, err, current.Attempts, current.FailureRounds, current.LastError, current.RetryAfter)
	}
	current, _ := s.Batch(broken.ID)
	if current.LastError == "" || current.RetryAfter == nil {
		t.Error("bad evidence must enter persistent backoff so later healthy data can run")
	}
}

func TestFullReviewPendingReadFreezesContext(t *testing.T) {
	for _, preview := range []bool{false, true} {
		s, err := Open(filepath.Join(t.TempDir(), "data"))
		if err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
		a := auditPending(t, s, at, 2)
		b := auditPending(t, s, at.Add(15*time.Minute), 2)
		if preview {
			if _, err := s.PreviewAnalysis(b.ID); err != nil {
				t.Fatal(err)
			}
		}
		data, _ := json.Marshal(map[string]any{"segments": []map[string]any{{"title": "修复同一登录任务", "summary": "编辑登录处理逻辑。", "category": "写代码", "evidence": []string{a.Frames[0].ID, a.Frames[1].ID}}}})
		if err := s.CommitAnalysis(a.ID, data, "audit", ""); err != nil {
			t.Fatal(err)
		}
		prepared, err := s.PrepareAnalysis(b.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("pending_preview=%v context_ready=%v context_count=%d", preview, prepared.ContextReady, len(prepared.Context))
		if len(prepared.Context) == 0 {
			t.Error("merely reading pending JSON must not permanently remove later available task context")
		}
	}
}

func TestFullReviewAdviceWithoutTimedTasks(t *testing.T) {
	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.Local)
	var reports []Report
	for i := 0; i < 3; i++ {
		p := at.AddDate(0, 0, i)
		reports = append(reports, Report{SchemaVersion: 2, AppSource: "window_server", BatchID: ID(p), Start: p, End: p.Add(time.Second), Cards: []Card{{TaskID: ID(p), Title: "仅有单帧的查阅", Summary: "单帧", Category: "查资料", Confidence: "medium", Start: p, End: p, DurationMethod: "bounded_observation_v1"}}})
	}
	o := BuildOverview(at, reports, nil, Batch{})
	week := o.Views[1]
	t.Logf("active_days=%d estimated=%d tasks=%d headline=%q advice=%q", week.ActiveDays, week.EstimatedSeconds, len(week.Tasks), week.Review.Headline, week.Review.Advice)
	if week.EstimatedSeconds == 0 && week.Review.Advice == "下周为排行中的主要任务预留两段连续时间；周末对照每天的记录，看看哪些时段更容易连续推进。" {
		t.Error("advice points to ranked main tasks even though no timed task exists")
	}
}

func TestFullReviewDailyBudgetCountsRetries(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour)
	b := auditPending(t, s, at, 2)
	c := DefaultConfig()
	c.AI.MaxRequestsPerDay = 1
	if _, err = s.ReserveAttempt(b.ID, c, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.RequestRetry(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveAttempt(b.ID, c, time.Now()); err != ErrDailyBudget {
		t.Fatalf("expected daily bound, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "usage", time.Now().Format("2006-01-02")+".json")); err != nil {
		t.Fatal(err)
	}
}
