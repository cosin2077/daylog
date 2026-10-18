package journal

import (
	"context"
	"daylog/internal/platform"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPauseRestoredBeforeCapture(t *testing.T) {
	for _, tc := range []struct {
		name   string
		until  *time.Time
		paused bool
	}{
		{"indefinite", nil, true}, {"future", timePointer(time.Now().Add(time.Hour)), true}, {"expired", timePointer(time.Now().Add(-time.Hour)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			if err := s.SavePause(true, tc.until); err != nil {
				t.Fatal(err)
			}
			r := Runner{Store: s, Config: DefaultConfig(), Source: &switchingSource{app: "editor", data: imageData(t)}}
			if err := r.restorePause(time.Now()); err != nil {
				t.Fatal(err)
			}
			if r.paused != tc.paused {
				t.Fatal("incorrect restart state")
			}
			if tc.paused {
				r.step(context.Background(), time.Now())
				if r.status.Captures != 0 || len(r.draft.Frames) != 0 {
					t.Fatal("captured while restored pause active")
				}
			}
			if err := s.SavePause(false, nil); err != nil {
				t.Fatal(err)
			}
			next := Runner{Store: s}
			if err := next.restorePause(time.Now()); err != nil || next.paused {
				t.Fatal("resume did not persist", err)
			}
		})
	}
	s := testStore(t)
	os.WriteFile(filepath.Join(s.Root, "pause.json"), []byte("broken"), 0600)
	if err := (&Runner{Store: s}).restorePause(time.Now()); err == nil {
		t.Fatal("corrupt pause silently resumed")
	}
}
func timePointer(t time.Time) *time.Time { return &t }

func TestAbandonedJobCleanupHonorsAnalyzerAndStorageCap(t *testing.T) {
	s := testStore(t)
	dir := filepath.Join(s.Root, "jobs", "analysis-crashed")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "image.jpg")
	os.WriteFile(path, make([]byte, 20), 0600)
	if n, err := s.RawBytes(); err != nil || n != 20 {
		t.Fatal(n, err)
	}
	lock, err := os.OpenFile(filepath.Join(s.Root, "analysis.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = platform.TryLock(lock); err != nil {
		t.Fatal(err)
	}
	if err = s.cleanupJobs(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("removed active job", err)
	}
	if err = s.saveJobImage(filepath.Join(dir, "more.jpg"), make([]byte, 12<<20)); !errors.Is(err, ErrStorageFull) {
		t.Fatal("copy escaped cap", err)
	}
	platform.Unlock(lock)
	if err = s.Cleanup(DefaultConfig(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("crashed job retained", err)
	}
}

type cancelBody struct{ cancel context.CancelFunc }

func (b cancelBody) Read([]byte) (int, error) { b.cancel(); return 0, context.Canceled }
func (b cancelBody) Close() error             { return nil }

type cancelBodyTransport struct{ cancel context.CancelFunc }

func (b cancelBodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: cancelBody{b.cancel}}, nil
}
func TestAPICancelWhileReadingBody(t *testing.T) {
	s := testStore(t)
	b := v2Batch(t, s, time.Now())
	c := DefaultConfig()
	c.AI.Enabled = true
	c.AI.Endpoint = "https://fixture.invalid"
	c.AI.Model = "fixture"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := s.Analyze(ctx, b.ID, c, "", &http.Client{Transport: cancelBodyTransport{cancel}})
	after, _ := s.Batch(b.ID)
	if !errors.Is(err, context.Canceled) || PreparationState(after) != "interrupted" || after.FailureRounds != 0 {
		t.Fatal(err, after.LastError, after.FailureRounds)
	}
}

func TestOldInferenceRecomputedWithLowConfidenceEvidence(t *testing.T) {
	s := testStore(t)
	r := neighborFixture()
	r.EstimationVersion = "bounded_neighbors_v2"
	r.Cards[1].Confidence = "low"
	r.TaskObservations[1].TaskID = ""
	r.Cards[0].Evidence = []string{"first"}
	r.Cards[1].Evidence = []string{"second"}
	mid := r.Start.Add(45 * time.Second)
	r.Cards[0].Spans = []TimeSpan{{Start: r.Start, End: mid, Inferred: true}}
	r.Cards[0].EstimatedSeconds = 45
	r.Cards[0].InferredSeconds = 45
	r.Cards[0].End = mid
	b := Batch{ID: r.BatchID, SchemaVersion: 2, AppSource: r.AppSource, Start: r.Start, End: r.End, MaxGapSeconds: 120,
		Frames:       []Frame{{ID: "first", At: r.Start, App: "editor"}, {ID: "second", At: r.End, App: "editor"}},
		Observations: []Observation{{At: r.Start, App: "editor", State: "sampled", FrameID: "first"}, {At: r.End, App: "editor", State: "sampled", FrameID: "second"}}}
	os.MkdirAll(filepath.Dir(s.archivePath(b.ID)), 0700)
	if err := WriteJSON(s.archivePath(b.ID), b); err != nil {
		t.Fatal(err)
	}
	withoutArchive := estimateReport(r)
	if withoutArchive.Cards[0].InferredSeconds != 0 {
		t.Fatal("missing archive treated low confidence as an unidentified image")
	}
	original, _ := json.Marshal(r)
	got := estimateReport(s.observationStates(r))
	if got.Cards[0].InferredSeconds != 0 || got.UnknownSeconds != 90 || got.TaskObservations[1].TaskID == "" {
		t.Fatalf("incorrect historical correction: %+v", got)
	}
	after, _ := json.Marshal(r)
	if string(original) != string(after) {
		t.Fatal("mutated original")
	}
}

func TestReportAxisRespectsDST(t *testing.T) {
	zone, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"2026-03-08", "2026-11-01"} {
		start, _ := time.ParseInLocation("2006-01-02", date, zone)
		noon := time.Date(start.Year(), start.Month(), start.Day(), 12, 0, 0, 0, zone)
		o := Overview{Views: []PeriodReport{{Period: "range", Start: start, End: start.AddDate(0, 0, 1), Activities: []Activity{{Start: noon, End: noon.Add(time.Hour)}}}}}
		html, err := RenderHTML(o, "range")
		if err != nil {
			t.Fatal(err)
		}
		text := string(html)
		begin := strings.Index(text, `"axis_ticks":[`)
		if begin < 0 {
			t.Fatal("missing ticks")
		}
		if !strings.HasPrefix(text[begin:], `"axis_ticks":["`+noon.Format(time.RFC3339)+`"`) {
			t.Fatal("axis uses midnight offset", text[begin:begin+90])
		}
	}
}

func TestDefaultUpdateFeed(t *testing.T) {
	if got := DefaultConfig().Update.Source; got != "https://github.com/cosin2077/daylog/releases/latest/download/release.json" {
		t.Fatal(got)
	}
}

func TestFullRawStoreStillAllowsBoundedAnalysisWorkspace(t *testing.T) {
	s := testStore(t)
	data := imageData(t)
	data = append(data, make([]byte, (2<<20)-len(data))...)
	c := DefaultConfig()
	c.MaxStorageBytes = 8 << 20
	for i := 0; i < 4; i++ {
		if _, err := s.SaveFrame(time.Now().Add(time.Duration(i)*time.Second), "fixture", data, c); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(s.Root, "jobs", "analysis-active")
	os.MkdirAll(dir, 0700)
	if err := s.saveJobImage(filepath.Join(dir, "frame.jpg"), data); err != nil {
		t.Fatal("full capture store blocked analysis", err)
	}
	if n, err := s.RawBytes(); err != nil || n != 10<<20 {
		t.Fatal("temporary evidence missing from storage accounting", n, err)
	}
}

func TestStoreOverviewLoadsCompleteMonthEdgeWeek(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	b := v2Batch(t, s, at)
	r, err := deriveReport(b, v2Input(b), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	path := s.reportPath(b.ID)
	os.MkdirAll(filepath.Dir(path), 0700)
	if err = WriteJSON(path, r); err != nil {
		t.Fatal(err)
	}
	o, err := s.Overview(time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range o.Periods {
		if v.Period == "week" && v.Anchor == "2026-07-27" {
			if len(v.Activities) != 1 {
				t.Fatal("cross-month activity omitted", len(v.Activities))
			}
			return
		}
	}
	t.Fatal("missing edge week")
}
