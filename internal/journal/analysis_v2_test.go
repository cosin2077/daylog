package journal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"daylog/internal/native"
)

func v2Batch(t *testing.T, s *Store, at time.Time) Batch {
	t.Helper()
	c := DefaultConfig()
	f, err := s.SaveFrame(at, "editor", imageData(t), c)
	if err != nil {
		t.Fatal(err)
	}
	b := Batch{SchemaVersion: 2, ID: ID(at), Start: at, End: at.Add(10 * time.Minute), Frames: []Frame{f}, MaxGapSeconds: 180}
	for _, seconds := range []int{0, 90, 180, 450, 540} {
		b.Observations = append(b.Observations, Observation{At: at.Add(time.Duration(seconds) * time.Second), State: "sampled", FrameID: f.ID, App: f.App})
	}
	if err = s.SaveDraft(b); err != nil {
		t.Fatal(err)
	}
	if err = s.SealDraft(b, c); err != nil {
		t.Fatal(err)
	}
	return b
}
func v2Input(b Batch) Interpretation {
	return Interpretation{SchemaVersion: 2, BatchID: b.ID, Segments: []Segment{{Title: "调整报告布局", Summary: "可见正在修改页面布局。", Category: "写代码", Confidence: "medium", Object: "报告页面", Actions: []string{"编辑"}, Evidence: []string{b.Frames[0].ID}, Uncertainties: []string{}}}}
}

func TestV2DerivesTimeWithoutBridgingGaps(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	b := v2Batch(t, s, at)
	in := v2Input(b)
	r, err := deriveReport(b, in, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if r.Cards[0].EstimatedSeconds != 270 || r.UnknownSeconds != 330 || len(r.Cards[0].Spans) != 2 {
		t.Fatalf("invented duration: %+v", r)
	}
	// Even a short app switch without an accompanying picture breaks continuity.
	b.Observations = append(b.Observations[:1], append([]Observation{{At: at.Add(30 * time.Second), State: "active", App: "browser"}}, b.Observations[1:]...)...)
	r, err = deriveReport(b, in, "")
	if err != nil || r.Cards[0].EstimatedSeconds != 180 {
		t.Fatalf("missed metadata boundary: %+v %v", r, err)
	}
	in.Segments[0].Confidence = "low"
	r, err = deriveReport(b, in, "")
	if err != nil || r.Cards[0].EstimatedSeconds != 0 || r.UnknownSeconds != 600 {
		t.Fatal("low confidence was treated as reliable time")
	}
}

func TestV2RejectsInventedEvidenceAndTaskLinks(t *testing.T) {
	s := testStore(t)
	b := v2Batch(t, s, time.Now())
	in := v2Input(b)
	in.Segments[0].TaskID = "invented"
	if _, err := deriveReport(b, in, ""); err == nil {
		t.Fatal("invented task accepted")
	}
	in = v2Input(b)
	in.Segments[0].Evidence = []string{"invented"}
	if _, err := deriveReport(b, in, ""); err == nil {
		t.Fatal("invented frame accepted")
	}
	in = v2Input(b)
	in.Segments = append(in.Segments, in.Segments[0])
	if _, err := deriveReport(b, in, ""); err == nil {
		t.Fatal("duplicate evidence accepted")
	}
	raw, _ := json.Marshal(v2Input(b))
	raw = []byte(strings.Replace(string(raw), `"confidence":"medium"`, `"confidence":"medium","start":"invented"`, 1))
	if err := s.CommitAnalysis(b.ID, raw, "api", ""); err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := ReadJSON(s.reportPath(b.ID), &report); err != nil || report.Cards[0].EstimatedSeconds != 270 || !report.Cards[0].Start.Equal(b.Start) {
		t.Fatal("ignored model timestamp changed derived time")
	}
	if err := s.Commit(testReport(b), "api"); err == nil {
		t.Fatal("legacy commit bypassed v2 derivation")
	}
}

func TestV2ContextAndUnknownOnlyReports(t *testing.T) {
	s := testStore(t)
	at := time.Now().Add(-time.Hour)
	b := v2Batch(t, s, at)
	raw, _ := json.Marshal(v2Input(b))
	if err := s.CommitAnalysis(b.ID, raw, "api", "fixture"); err != nil {
		t.Fatal(err)
	}
	next := v2Batch(t, s, at.Add(15*time.Minute))
	prepared, err := s.PrepareAnalysis(next.ID)
	if err != nil || len(prepared.Context) != 1 {
		t.Fatalf("missing context: %+v %v", prepared, err)
	}
	in := v2Input(next)
	in.Segments[0].TaskID = prepared.Context[0].TaskID
	raw, _ = json.Marshal(in)
	if err = s.CommitAnalysis(next.ID, raw, "api", "fixture"); err != nil {
		t.Fatal(err)
	}
	var first, second Report
	ReadJSON(s.reportPath(b.ID), &first)
	ReadJSON(s.reportPath(next.ID), &second)
	if first.Cards[0].TaskID != second.Cards[0].TaskID || first.Cards[0].ActivityID == second.Cards[0].ActivityID {
		t.Fatal("unstable task/activity IDs")
	}
	if err = s.CommitAnalysis(next.ID, raw, "api", ""); err == nil {
		t.Fatal("duplicate commit overwritten")
	}
	later := v2Batch(t, s, at.Add(2*time.Hour))
	prepared, err = s.PrepareAnalysis(later.ID)
	if err != nil || len(prepared.Context) != 0 {
		t.Fatal("stale/future context leaked")
	}
	raw, _ = json.Marshal(Interpretation{SchemaVersion: 2, BatchID: later.ID, Segments: []Segment{}})
	if err = s.CommitAnalysis(later.ID, raw, "api", ""); err != nil {
		t.Fatal(err)
	}
	reports, err := s.Reports(later.Start, later.End)
	if err != nil || len(reports) != 1 || len(reports[0].Cards) != 0 {
		t.Fatal("unknown-only report disappeared")
	}
	overview := BuildOverview(later.Start, reports, nil, Batch{})
	if overview.Views[0].UnknownSeconds != 600 || overview.Views[0].AnalyzedBatches != 1 {
		t.Fatal("unknown time lost in overview")
	}
}

func TestRunnerRepeatedObservationsAndPowerInterval(t *testing.T) {
	s := testStore(t)
	source := &testSource{data: imageData(t), env: native.Environment{App: "editor", Permission: true}}
	r := Runner{Store: s, Config: DefaultConfig(), Source: source}
	at := time.Now()
	for i := 0; i < 11; i++ {
		r.step(context.Background(), at.Add(time.Duration(i)*30*time.Second))
	}
	if r.snapshot().Saved != 1 || len(r.draft.Observations) != 4 {
		t.Fatalf("repeats not preserved cheaply: %+v", r.draft)
	}
	var draft Batch
	if err := ReadJSON(filepath.Join(s.Root, "draft.json"), &draft); err != nil || len(draft.Observations) != 4 {
		t.Fatal("observations not crash-persistent")
	}
	source.env.Locked = true
	r.step(context.Background(), at.Add(330*time.Second))
	pending, _ := s.Pending(1)
	if len(pending) != 1 || pending[0].Observations[len(pending[0].Observations)-1].State != "screen_locked" {
		t.Fatal("lock boundary lost")
	}
	source.env = native.Environment{OnBattery: true, BatteryPercent: 80, App: "editor", Permission: true}
	before := source.captures
	r.step(context.Background(), at.Add(360*time.Second))
	r.step(context.Background(), at.Add(390*time.Second))
	if source.captures != before+1 {
		t.Fatal("battery interval bypassed by wake")
	}
}

func TestKeyframesKeepBriefAppAndCaps(t *testing.T) {
	var frames []Frame
	for i := 0; i < 20; i++ {
		app := "editor"
		if i == 3 {
			app = "browser"
		}
		frames = append(frames, Frame{ID: string(rune('a' + i)), App: app, Change: 0.1})
	}
	selected := SelectKeyFrames(frames, 4, 8)
	found := false
	for _, f := range selected {
		if f.App == "browser" {
			found = true
		}
	}
	if !found || len(selected) > 8 {
		t.Fatal("brief app skipped")
	}
	if len(SelectKeyFrames(frames, 4, 1)) != 1 {
		t.Fatal("hard image cap bypassed")
	}
}

func TestKeyframesAddBoundedCoverageForContentChanges(t *testing.T) {
	var stable, changing []Frame
	for i := 0; i < 20; i++ {
		stable = append(stable, Frame{ID: fmt.Sprintf("s%d", i), App: "editor", Change: 0.001})
		change := 0.001
		if i%4 == 2 {
			change = 0.1
		}
		changing = append(changing, Frame{ID: fmt.Sprintf("c%d", i), App: "editor", Change: change})
	}
	steady := SelectKeyFrames(stable, 4, 8)
	dynamic := SelectKeyFrames(changing, 4, 8)
	if len(steady) != 4 || len(dynamic) != 6 {
		t.Fatal("dynamic content did not receive a bounded extra keyframe budget", len(steady), len(dynamic))
	}
	peaks := 0
	for _, f := range dynamic {
		if f.Change >= 0.1 {
			peaks++
		}
	}
	if peaks < 3 {
		t.Fatal("visual content changes were not represented", dynamic)
	}
}

func TestV2APIInputAndUsage(t *testing.T) {
	s := testStore(t)
	b := v2Batch(t, s, time.Now())
	in, _ := json.Marshal(v2Input(b))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(req.Body).Decode(&body)
		raw := string(body.Messages[1].Content)
		if strings.Contains(raw, "frames/") || strings.Contains(raw, "config.json") {
			t.Error("local paths sent")
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": string(in)}}}, "usage": map[string]int{"prompt_tokens": 120, "completion_tokens": 80}})
	}))
	defer server.Close()
	c := DefaultConfig()
	c.AI.Enabled = true
	c.AI.Endpoint = server.URL
	c.AI.Model = "fixture"
	if err := s.Analyze(context.Background(), b.ID, c, "", nil); err != nil {
		t.Fatal(err)
	}
	var usage DailyUsage
	if err := ReadJSON(filepath.Join(s.Root, "usage", time.Now().Format("2006-01-02")+".json"), &usage); err != nil {
		t.Fatal(err)
	}
	if usage.SubmittedImages != 1 || usage.InputTokens != 120 || usage.OutputTokens != 80 || usage.KnownUsageCalls != 1 || usage.UnknownUsageCalls != 0 {
		t.Fatalf("bad usage: %+v", usage)
	}
}

func TestImageBudgetAndObservationRetention(t *testing.T) {
	s := testStore(t)
	at := time.Now()
	c := DefaultConfig()
	c.MaxImagesPerDay = 1
	b := v2Batch(t, s, at)
	next := v2Batch(t, s, at.Add(time.Hour))
	if _, err := s.ReserveAttempt(b.ID, c, at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveAttempt(next.ID, c, at); err == nil {
		t.Fatal("image budget exceeded")
	}
	path := filepath.Join(s.Root, "observations")
	os.MkdirAll(path, 0700)
	expired := filepath.Join(path, at.AddDate(0, 0, -100).Format("2006-01-02")+".jsonl")
	os.WriteFile(expired, []byte("{}\n"), 0600)
	if err := s.Cleanup(c, at); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Fatal("observation retention ignored")
	}
}

func TestV2RangeClipsDurationsAndTasks(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	b := v2Batch(t, s, at)
	raw, _ := json.Marshal(v2Input(b))
	if err := s.CommitAnalysis(b.ID, raw, "api", ""); err != nil {
		t.Fatal(err)
	}
	overview, err := s.RangeOverview(at.Add(60*time.Second), at.Add(480*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	v := overview.Views[0]
	if v.EstimatedSeconds != 150 || v.UnknownSeconds != 270 || v.CoverageSeconds != 150 || len(v.Tasks) != 1 || v.Tasks[0].EstimatedSeconds != 150 {
		t.Fatalf("wrong clipped range: %+v", v)
	}
	if _, err = RenderHTML(overview, "range"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RangeOverview(at, at); err == nil {
		t.Fatal("empty range accepted")
	}
}

func TestProfilesPreserveProviderAndPrivacy(t *testing.T) {
	for _, name := range []string{"balanced", "battery", "detailed"} {
		c := DefaultConfig()
		c.AI.APIKey = "fixture-only"
		c.BlockedApps = []string{"private"}
		c.Power.AnalyzeOnBattery = false
		if err := c.ApplyProfile(name); err != nil {
			t.Fatal(err)
		}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
		if c.AI.APIKey != "fixture-only" || len(c.BlockedApps) != 1 || c.Power.AnalyzeOnBattery {
			t.Fatal("profile replaced unrelated preferences")
		}
	}
}

func TestV2AgentProtocolCommitsDerivedResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fixture; Windows process and adapter tests cover this platform")
	}
	s := testStore(t)
	b := v2Batch(t, s, time.Now())
	raw, _ := json.Marshal(v2Input(b))
	event, _ := json.Marshal(map[string]any{"type": "result", "result": string(raw)})
	dir := t.TempDir()
	t.Setenv("DAYLOG_AGENT_SEARCH_PATH", dir)
	script := "#!/bin/sh\n/bin/cat > /dev/null\nprintf '%s\\n' '" + string(event) + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	c := DefaultConfig()
	c.Agent.Order = []string{"claude"}
	c.Agent.InheritLoginEnv = false
	if err := s.AnalyzeAgent(context.Background(), b.ID, c, ""); err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := ReadJSON(s.reportPath(b.ID), &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 2 || report.Cards[0].EstimatedSeconds != 270 {
		t.Fatal("agent bypassed derived duration")
	}
}

func TestSleepRecoveryDoesNotCreateUnboundedBatch(t *testing.T) {
	s := testStore(t)
	at := time.Now()
	source := &testSource{data: imageData(t), env: native.Environment{App: "editor", Permission: true}}
	r := Runner{Store: s, Config: DefaultConfig(), Source: source}
	r.step(context.Background(), at)
	r.step(context.Background(), at.Add(10*time.Hour))
	pending, err := s.Pending(0)
	if err != nil || len(pending) != 1 {
		t.Fatalf("sleep recovery: %v", err)
	}
	if pending[0].End.Sub(pending[0].Start) > 15*time.Minute {
		t.Fatal("sleep counted inside activity batch")
	}
}

func TestOmittedImagesOnlyInterpolateBetweenMatchingAnchors(t *testing.T) {
	s := testStore(t)
	at := time.Now()
	b := v2Batch(t, s, at)
	b.SupportGapSeconds = 300
	second := b.Frames[0]
	second.ID = ID(at.Add(270 * time.Second))
	second.At = at.Add(270 * time.Second)
	b.Frames = append(b.Frames, second)
	b.End = second.At
	b.Observations = []Observation{{At: at, State: "sampled", App: "editor", FrameID: b.Frames[0].ID}, {At: at.Add(90 * time.Second), State: "sampled", App: "editor", FrameID: "omitted1"}, {At: at.Add(180 * time.Second), State: "sampled", App: "editor", FrameID: "omitted2"}, {At: second.At, State: "sampled", App: "editor", FrameID: second.ID}}
	in := v2Input(b)
	in.Segments[0].Evidence = append(in.Segments[0].Evidence, second.ID)
	r, err := deriveReport(b, in, "")
	if err != nil || r.Cards[0].EstimatedSeconds != 270 || !r.Cards[0].Spans[0].Interpolated {
		t.Fatalf("keyframe interpolation failed: %+v %v", r, err)
	}
	b.Observations[1].App = "browser"
	r, err = deriveReport(b, in, "")
	if err != nil || r.Cards[0].EstimatedSeconds != 0 {
		t.Fatal("app boundary incorrectly bridged")
	}
	b.Observations[1].App = "editor"
	b.SupportGapSeconds = 180
	r, err = deriveReport(b, in, "")
	if err != nil || r.Cards[0].EstimatedSeconds != 0 {
		t.Fatal("distant keyframes incorrectly bridged")
	}
}
