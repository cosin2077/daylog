package journal

import (
	"context"
	"daylog/internal/native"
	"errors"
	"net/http"
	"testing"
	"time"
)

type cancelTransport struct{ cancel context.CancelFunc }

func (x cancelTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	x.cancel()
	return nil, r.Context().Err()
}
func TestAPICancellationRemainsInterrupted(t *testing.T) {
	s := testStore(t)
	b := v2Batch(t, s, time.Now())
	c := DefaultConfig()
	c.AI.Enabled = true
	c.AI.Endpoint = "https://fixture.invalid"
	c.AI.Model = "fixture"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := s.Analyze(ctx, b.ID, c, "", &http.Client{Transport: cancelTransport{cancel}})
	after, e := s.Batch(b.ID)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("isCanceled=%v state=%s reason=%s failureRounds=%d", errors.Is(err, context.Canceled), PreparationState(after), after.LastError, after.FailureRounds)
	if !errors.Is(err, context.Canceled) || PreparationState(after) != "interrupted" {
		t.Fail()
	}
}

type brokenEnvironment struct{}

func (brokenEnvironment) Environment(context.Context) (native.Environment, error) {
	return native.Environment{}, errors.New("fixture environment failure")
}
func (brokenEnvironment) Capture(context.Context, native.CaptureOptions) ([]byte, error) {
	panic("unexpected capture")
}
func TestEnvironmentFailureCancelsActiveAnalysis(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := Runner{Store: s, Config: DefaultConfig(), Source: brokenEnvironment{}, paused: true, canAnalyze: true, analysisCancel: cancel}
	r.step(context.Background(), time.Now())
	t.Logf("activeAnalysisCanceled=%v canAnalyze=%v", ctx.Err() != nil, r.canAnalyze)
	if ctx.Err() == nil || r.canAnalyze {
		t.Fail()
	}
}

func TestProductionLowConfidenceBoundary(t *testing.T) {
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	end := at.Add(90 * time.Second)
	b := Batch{SchemaVersion: 2, AppSource: "window_server", ID: ID(at), Start: at, End: end.Add(time.Second), MaxGapSeconds: 120, Frames: []Frame{{ID: ID(at), At: at, App: "editor"}, {ID: ID(end), At: end, App: "editor"}}, Observations: []Observation{{At: at, State: "sampled", App: "editor", FrameID: ID(at)}, {At: end, State: "sampled", App: "editor", FrameID: ID(end)}}}
	in := Interpretation{SchemaVersion: 2, BatchID: b.ID, Segments: []Segment{{Title: "A", Summary: "A", Category: "写代码", Confidence: "high", Actions: []string{}, Uncertainties: []string{}, Evidence: []string{ID(at)}}, {Title: "B", Summary: "B", Category: "查资料", Confidence: "low", Actions: []string{}, Uncertainties: []string{}, Evidence: []string{ID(end)}}}}
	r, err := deriveReport(b, in, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("inferred=%d unknown=%d lowTaskObservation=%q", r.Cards[0].InferredSeconds, r.UnknownSeconds, r.TaskObservations[1].TaskID)
	if r.Cards[0].InferredSeconds != 0 {
		t.Fail()
	}
}

type switchingSource struct {
	app  string
	data []byte
}

func (s *switchingSource) Environment(context.Context) (native.Environment, error) {
	return native.Environment{App: s.app, Permission: true, BatteryPercent: 100}, nil
}
func (s *switchingSource) Capture(context.Context, native.CaptureOptions) ([]byte, error) {
	s.app = "browser"
	return s.data, nil
}
func TestCaptureMetadataMismatch(t *testing.T) {
	s := testStore(t)
	source := &switchingSource{app: "editor", data: imageData(t)}
	r := Runner{Store: s, Config: DefaultConfig(), Source: source}
	r.step(context.Background(), time.Now())
	if len(r.draft.Frames) == 0 {
		if r.status.Error != "capture_unavailable_check_permission" {
			t.Fatal("capture skipped for unrelated reason", r.status.Error)
		}
		return
	}
	t.Logf("capturedApp=%s recordedApp=%s", source.app, r.draft.Frames[0].App)
	if r.draft.Frames[0].App != source.app {
		t.Fatal("capture app differs from stored metadata")
	}
}

func TestMidMonthSnapshotHasCompleteEdgeWeeks(t *testing.T) {
	at := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	o := BuildOverview(at, nil, nil, Batch{})
	days := map[string]bool{}
	for _, d := range o.Calendar {
		days[d.Anchor] = true
	}
	for _, v := range o.Periods {
		if v.Period != "week" {
			continue
		}
		for _, d := range v.Days {
			if !days[d.Date] {
				t.Errorf("week=%s missing calendar day=%s", v.Anchor, d.Date)
			}
		}
	}
}
