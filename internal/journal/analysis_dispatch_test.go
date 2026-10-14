package journal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMinimalInterpretationStillRequiresEvidence(t *testing.T) {
	s := testStore(t)
	b := v2Batch(t, s, time.Now())
	minimal := `{"segments":[{"title":"编辑","summary":"修改页面。","evidence":["` + b.Frames[0].ID + `","` + b.Frames[0].ID + `"],"extra":"ignored","duration_seconds":999999}]}`
	in, err := decodeInterpretation(b, []byte("```JSON\r\n"+minimal+"\r\n```"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := deriveReport(b, in, "")
	if err != nil || r.Cards[0].EstimatedSeconds != 270 || r.Cards[0].Category != "其他" || len(r.Cards[0].Evidence) != 1 {
		t.Fatalf("normalization changed evidence/time: %+v %v", r, err)
	}
	for _, raw := range []string{`{}`, `{"segments":null}`, `{"batch_id":"wrong","segments":[]}`, `{"schema_version":1,"segments":[]}`, minimal + `{}`, `{"segments":[{"title":"x","summary":"y"}]}`, strings.Replace(minimal, b.Frames[0].ID, "unknown", -1)} {
		parsed, e := decodeInterpretation(b, []byte(raw))
		if e == nil {
			_, e = deriveReport(b, parsed, "")
		}
		if e == nil {
			t.Fatalf("invalid output accepted: %s", raw)
		}
	}
}

func TestConfiguredFallbackCountsOneRoundAndSharesBudget(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "failed_round"}[success], func(t *testing.T) {
			s := testStore(t)
			b := v2Batch(t, s, time.Now())
			dir := t.TempDir()
			t.Setenv("DAYLOG_AGENT_SEARCH_PATH", dir)
			for _, name := range []string{"claude", "hermes"} {
				os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 1\n"), 0700)
			}
			// If dispatch ignores the allowlist, this must never execute.
			os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nexit 99\n"), 0700)
			var models []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body struct {
					Model string `json:"model"`
				}
				json.NewDecoder(req.Body).Decode(&body)
				models = append(models, body.Model)
				if body.Model == "second" && success {
					raw, _ := json.Marshal(v2Input(b))
					json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": string(raw)}}}})
				} else {
					w.WriteHeader(503)
				}
			}))
			defer server.Close()
			c := DefaultConfig()
			c.Agent.Order = []string{"claude", "hermes"}
			c.Agent.InheritLoginEnv = false
			c.AI.Enabled = true
			c.AI.FallbackAfterAgents = true
			c.AI.Endpoint = server.URL
			c.AI.Model = "first"
			c.AI.FallbackModels = []string{"first", "second"}
			err := s.AnalyzeConfigured(context.Background(), b.ID, c, "fixture-key", "", nil)
			if (err == nil) != success || !reflect.DeepEqual(models, []string{"first", "second"}) {
				t.Fatalf("wrong fallback: %v %v", models, err)
			}
			after, _ := s.Batch(b.ID)
			if after.Attempts != 4 {
				t.Fatalf("requests not counted: %d", after.Attempts)
			}
			if success {
				if after.FailureRounds != 0 {
					t.Fatal("failed candidates counted as rounds")
				}
			} else if after.FailureRounds != 1 || after.RetryAfter == nil {
				t.Fatal("failed round not deferred")
			}
			var usage DailyUsage
			ReadJSON(filepath.Join(s.Root, "usage", time.Now().Format("2006-01-02")+".json"), &usage)
			if usage.Requests != 4 || usage.SubmittedImages != 4 {
				t.Fatal("fallback bypassed budgets")
			}
		})
	}
}

func TestFailureNotificationThresholdAndPersistence(t *testing.T) {
	s := testStore(t)
	b := testBatch(t, s, time.Now())
	sent := 0
	notify := func() error { sent++; return nil }
	for i := 1; i <= 8; i++ {
		if err := s.finishFailure(b.ID, "timeout", notify); err != nil {
			t.Fatal(err)
		}
		if i <= 6 && sent != 0 {
			t.Fatal("notified before six failed retries")
		}
	}
	if sent != 1 {
		t.Fatal("repeated notification")
	}
	after, _ := s.Batch(b.ID)
	if !after.FailureNotified || after.FailureRounds != 8 {
		t.Fatal("notification marker lost")
	}
	// Failed notification submissions may retry on the next failed round.
	other := testBatch(t, s, time.Now().Add(time.Hour))
	other.FailureRounds = 6
	WriteJSON(filepath.Join(s.Root, "batches", other.ID+".json"), other)
	s.finishFailure(other.ID, "timeout", func() error { return errors.New("disabled") })
	after, _ = s.Batch(other.ID)
	if after.FailureNotified {
		t.Fatal("failed submission marked delivered")
	}
}

func TestCanceledAndBudgetDoNotCountAsFailedRound(t *testing.T) {
	s := testStore(t)
	b := testBatch(t, s, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.analysisRound(ctx, b.ID, true, func() error { s.Failed(b.ID, "canceled"); return context.Canceled })
	s.analysisRound(context.Background(), b.ID, true, func() error { return ErrDailyBudget })
	after, _ := s.Batch(b.ID)
	if after.FailureRounds != 0 {
		t.Fatal("cancellation or budget counted as model failure")
	}
}

func TestTimeDistributionUsesSpansNotCardBounds(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 10, 14, 8, 58, 0, 0, time.UTC)
	b := v2Batch(t, s, at)
	r, err := deriveReport(b, v2Input(b), "")
	if err != nil {
		t.Fatal(err)
	}
	p := BuildOverview(at, []Report{r}, []Batch{{ID: "failed", Start: at, End: b.End, LastError: "timeout", FailureRounds: 7}}, Batch{}).Views[0]
	var total int64
	for _, bucket := range p.Distribution {
		total += bucket.EstimatedSeconds
	}
	if total != 270 || p.Categories[0].EstimatedSeconds != 270 || len(p.Failures) != 1 || !p.Failures[0].NeedsAttention {
		t.Fatalf("wrong chart/failure summary: %d %+v", total, p.Failures)
	}
	if p.Distribution[8].EstimatedSeconds != 120 || p.Distribution[9].EstimatedSeconds != 150 {
		t.Fatal("hourly gaps incorrectly filled")
	}
}

func TestDisabledFailureNotificationPreservesRetryAndAlertEligibility(t *testing.T) {
	s := testStore(t)
	b := testBatch(t, s, time.Now())
	b.FailureRounds = 6
	WriteJSON(filepath.Join(s.Root, "batches", b.ID+".json"), b)
	if err := s.finishFailure(b.ID, "timeout", nil); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Batch(b.ID)
	if after.FailureNotified || after.FailureRounds != 7 || after.RetryAfter == nil {
		t.Fatal("disabled banner changed retry state")
	}
	sent := 0
	s.finishFailure(b.ID, "timeout", func() error { sent++; return nil })
	if sent != 1 {
		t.Fatal("re-enabled notification suppressed")
	}
}
