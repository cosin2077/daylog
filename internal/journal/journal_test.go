package journal

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"daylog/internal/native"
)

func imageData(t testing.TB) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for y := 0; y < 180; y++ {
		for x := 0; x < 320; x++ {
			im.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), uint8(x + y), 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, im, &jpeg.Options{Quality: 60}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "private-data"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	return s
}
func testBatch(t *testing.T, s *Store, at time.Time) Batch {
	t.Helper()
	f, err := s.SaveFrame(at, "fixture", imageData(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	b := Batch{ID: ID(at), Start: at, End: at.Add(10 * time.Minute), Frames: []Frame{f}}
	if err = s.SaveDraft(b); err != nil {
		t.Fatal(err)
	}
	if err = s.SealDraft(b, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	return b
}
func testReport(b Batch) Report {
	return Report{BatchID: b.ID, Cards: []Card{{Start: b.Start, End: b.End, Title: "测试任务", Summary: "合成画面中的活动", Category: "工作", Evidence: []string{b.Frames[0].ID}}}}
}

func TestPowerPolicy(t *testing.T) {
	c := DefaultConfig()
	for _, tc := range []struct {
		name    string
		env     native.Environment
		delay   time.Duration
		reason  string
		analyze bool
	}{
		{"AC", native.Environment{}, 30 * time.Second, "", true},
		{"battery", native.Environment{OnBattery: true, BatteryPercent: 80}, 120 * time.Second, "", false},
		{"low_power", native.Environment{LowPower: true}, 180 * time.Second, "", true},
		{"low_battery", native.Environment{OnBattery: true, BatteryPercent: 20}, 120 * time.Second, "low_battery", false},
		{"thermal", native.Environment{Thermal: 2}, time.Minute, "thermal_pressure", false},
		{"locked", native.Environment{Locked: true}, time.Minute, "screen_locked", false},
		{"screen_off", native.Environment{ScreenOff: true}, time.Minute, "screen_asleep", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := PowerPolicy(c, tc.env)
			if p.Delay != tc.delay || p.PauseReason != tc.reason || p.CanAnalyze != tc.analyze {
				t.Fatalf("unexpected power policy: %+v", p)
			}
		})
	}
}

type testSource struct {
	env      native.Environment
	data     []byte
	captures int
	err      error
}

func (s *testSource) Environment(context.Context) (native.Environment, error) { return s.env, nil }
func (s *testSource) Capture(context.Context, native.CaptureOptions) ([]byte, error) {
	s.captures++
	return s.data, s.err
}

func TestRunnerAvoidsCaptureInsideSaveIntervalAndPowerStops(t *testing.T) {
	s := testStore(t)
	source := &testSource{data: imageData(t), env: native.Environment{App: "fixture", BatteryPercent: 100, Permission: true}}
	r := Runner{Store: s, Config: DefaultConfig(), Source: source}
	at := time.Now()
	for i := 0; i <= 6; i++ {
		r.step(context.Background(), at.Add(time.Duration(i)*30*time.Second))
	}
	if source.captures != 3 || r.snapshot().Saved != 1 {
		t.Fatalf("expected 3 captures and 1 saved frame, got captures=%d status=%+v", source.captures, r.snapshot())
	}
	for _, env := range []native.Environment{{Locked: true}, {ScreenOff: true}, {OnBattery: true, BatteryPercent: 10}, {Thermal: 2}, {IdleSeconds: 600}, {App: "blocked"}} {
		source.env = env
		r.Config.BlockedApps = []string{"blocked"}
		r.step(context.Background(), at.Add(time.Hour))
	}
	if source.captures != 3 {
		t.Fatal("suppressed states performed a capture")
	}
}

func TestRunnerReportsBlockedBackgroundWindowWithoutSaving(t *testing.T) {
	s := testStore(t)
	source := &testSource{
		env: native.Environment{App: "allowed.foreground", Permission: true},
		err: native.ErrBlocked,
	}
	c := DefaultConfig()
	c.WindowID = 123
	c.BlockedApps = []string{"blocked.background"}
	r := Runner{Store: s, Config: c, Source: source}
	r.step(context.Background(), time.Now())
	status := r.snapshot()
	if source.captures != 1 || status.SkippedBlocked != 1 || status.Captures != 0 || status.Saved != 0 || status.Error != "" {
		t.Fatalf("blocked target misreported or saved: %+v", status)
	}
	frames, err := os.ReadDir(filepath.Join(s.Root, "frames"))
	if err != nil || len(frames) != 0 {
		t.Fatalf("blocked target wrote a screenshot: %v", err)
	}
}

func TestReportValidationArchiveAndCleanupProtection(t *testing.T) {
	s := testStore(t)
	at := time.Now().AddDate(0, 0, -4)
	b := testBatch(t, s, at)
	path, _ := s.FramePath(b.Frames[0])
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(DefaultConfig(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("pending evidence was deleted")
	}
	r := testReport(b)
	r.Cards[0].Evidence = []string{"unknown"}
	if s.Commit(r, "agent") == nil {
		t.Fatal("unknown evidence accepted")
	}
	r = testReport(b)
	r.Cards[0].End = b.End.Add(time.Second)
	if s.Commit(r, "agent") == nil {
		t.Fatal("out-of-window card accepted")
	}
	if err := s.Commit(testReport(b), "agent"); err != nil {
		t.Fatal(err)
	}
	if s.Commit(testReport(b), "agent") == nil {
		t.Fatal("existing report overwritten")
	}
	pending, err := s.Pending(0)
	if err != nil || len(pending) != 0 {
		t.Fatalf("archive stayed in pending queue: %v", err)
	}
	if _, err = s.Batch(b.ID); err != nil {
		t.Fatal("archived manifest lost")
	}
	if err = s.Cleanup(DefaultConfig(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expired processed raw image retained")
	}
	reports, err := s.Reports(at.Add(-time.Minute), b.End.Add(time.Minute))
	if err != nil || len(reports) != 1 {
		t.Fatalf("report lost after raw cleanup: %v", err)
	}
	bad := b.Frames[0]
	bad.File = "../config.json"
	if _, err = s.FramePath(bad); err == nil {
		t.Fatal("path escape accepted")
	}
}

func TestConcurrentRequestBudget(t *testing.T) {
	s := testStore(t)
	at := time.Now()
	batches := []Batch{}
	for i := 0; i < 8; i++ {
		batches = append(batches, testBatch(t, s, at.Add(time.Duration(i)*time.Second)))
	}
	c := DefaultConfig()
	c.AI.MaxRequestsPerDay = 3
	var success atomic.Int32
	var wg sync.WaitGroup
	for _, b := range batches {
		wg.Add(1)
		go func(b Batch) {
			defer wg.Done()
			if _, err := s.ReserveAttempt(b.ID, c, at); err == nil {
				success.Add(1)
			}
		}(b)
	}
	wg.Wait()
	if success.Load() != 3 {
		t.Fatalf("request budget race: %d calls reserved", success.Load())
	}
}

func TestAPICompleteResponseAndIncompleteRejection(t *testing.T) {
	for _, finish := range []string{"stop", "length"} {
		t.Run(finish, func(t *testing.T) {
			s := testStore(t)
			b := testBatch(t, s, time.Now())
			report, _ := json.Marshal(testReport(b))
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer fixture-only" {
					t.Error("authorization header missing")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid request")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": finish, "message": map[string]string{"content": string(report)}}}})
			}))
			defer server.Close()
			c := DefaultConfig()
			c.AI = AIConfig{Enabled: true, Endpoint: server.URL, Model: "fixture", MaxRequestsPerDay: 48}
			err := s.Analyze(context.Background(), b.ID, c, "fixture-only", nil)
			if (finish == "stop") != (err == nil) {
				t.Fatalf("unexpected completion acceptance: %v", err)
			}
			if calls.Load() != 1 {
				t.Fatal("unexpected retry")
			}
			pending, _ := s.Pending(0)
			if finish == "length" && len(pending) != 1 {
				t.Fatal("partial response was committed")
			}
		})
	}
}

func TestAPIRedirectDoesNotForwardScreenshots(t *testing.T) {
	s := testStore(t)
	b := testBatch(t, s, time.Now())
	var redirected atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer trap.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, trap.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	c := DefaultConfig()
	c.AI = AIConfig{Enabled: true, Endpoint: server.URL, Model: "fixture", MaxRequestsPerDay: 48}
	if s.Analyze(context.Background(), b.ID, c, "fixture-only", nil) == nil {
		t.Fatal("redirect accepted")
	}
	if redirected.Load() != 0 {
		t.Fatal("private images forwarded through redirect")
	}
}

func TestEndpointPolicyAndQueueCap(t *testing.T) {
	for _, raw := range []string{"http://example.com/v1/chat/completions", "https://user:password@example.com", "https://example.com?key=fixture"} {
		if validateEndpoint(raw) == nil {
			t.Fatalf("unsafe endpoint accepted")
		}
	}
	if err := validateEndpoint("http://127.0.0.1:8080/v1/chat/completions"); err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	at := time.Now()
	testBatch(t, s, at)
	c := DefaultConfig()
	c.MaxPendingBatches = 1
	f, err := s.SaveFrame(at.Add(time.Hour), "fixture", imageData(t), c)
	if err != nil {
		t.Fatal(err)
	}
	b := Batch{ID: f.ID, Start: f.At, End: f.At.Add(time.Second), Frames: []Frame{f}}
	s.SaveDraft(b)
	if err = s.SealDraft(b, c); err != ErrQueueFull {
		t.Fatalf("queue cap ignored: %v", err)
	}
	if _, err = os.Stat(filepath.Join(s.Root, "draft.json")); err != nil {
		t.Fatal("queue cap discarded live evidence")
	}
}

// Live TextEdit edits changed about 0.9-3% of sampled colour values, while
// static caret/chrome noise was below 0.1%. Keep that gap in the default policy.
func TestSamplerKeepsTextEditsAndSkipsSmallNoise(t *testing.T) {
	c := DefaultConfig()
	now := time.Now()
	last := now.Add(-2 * time.Duration(c.MinSaveSeconds) * time.Second)
	var previous, changed, noise Fingerprint
	for i := range previous {
		previous[i] = 255
		changed[i] = 255
		noise[i] = 255
	}
	for i := 0; i < 15; i++ {
		changed[i] = 0
	}
	noise[0] = 0
	if !ShouldSave(c, now, last, "window:1", "window:1", changed, previous) {
		t.Fatal("small text edit was discarded until heartbeat")
	}
	if ShouldSave(c, now, last, "window:1", "window:1", noise, previous) {
		t.Fatal("static image noise triggered storage")
	}
}

func BenchmarkFingerprintJPEG(b *testing.B) {
	data := imageData(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := FingerprintJPEG(data); err != nil {
			b.Fatal(err)
		}
	}
}
