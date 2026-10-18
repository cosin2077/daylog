package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"daylog/internal/journal"
)

func TestFullReviewUnrecognizedImagesAreRecords(t *testing.T) {
	s := configStore(t)
	at := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	c := journal.DefaultConfig()
	f, err := s.SaveFrame(at, "com.google.Chrome", []byte("fixture only"), c)
	if err != nil {
		t.Fatal(err)
	}
	b := journal.Batch{SchemaVersion: 2, AppSource: "window_server", ID: journal.ID(at), Start: at, End: at.Add(5 * time.Minute), MaxGapSeconds: 180, Frames: []journal.Frame{f}, Observations: []journal.Observation{{At: at, State: "sampled", FrameID: f.ID, App: f.App}}}
	if err = s.SaveDraft(b); err != nil {
		t.Fatal(err)
	}
	if err = s.SealDraft(b, c); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitAnalysis(b.ID, []byte(`{"segments":[]}`), "audit", ""); err != nil {
		t.Fatal(err)
	}
	jsonOut, err := commandOutput(t, "--data", s.Root, "report", "--date", at.In(time.Local).Format("2006-01-02"), "--json")
	if err != nil {
		t.Fatal(err)
	}
	var p journal.PeriodReport
	if err = json.Unmarshal(jsonOut, &p); err != nil {
		t.Fatal(err)
	}
	out, err := commandOutput(t, "--data", s.Root, "report", "--date", at.In(time.Local).Format("2006-01-02"), "--no-open")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("analyzed_batches=%d unknown_seconds=%d activities=%d output=%q expected_html=%s", p.AnalyzedBatches, p.UnknownSeconds, len(p.Activities), string(out), filepath.Join(s.Root, "reports", "report.html"))
	if p.AnalyzedBatches == 1 && strings.Contains(string(out), "没有记录") {
		t.Error("analyzed but unrecognized screenshots must explain unknown evidence instead of claiming no records")
	}
}

func TestPendingJSONCompleteAndReadOnly(t *testing.T) {
	for _, count := range []int{12, 256} {
		t.Run(fmt.Sprint(count), func(t *testing.T) { testPendingQueue(t, count) })
	}
}
func testPendingQueue(t *testing.T, count int) {
	s := configStore(t)
	at := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	c := journal.DefaultConfig()
	c.MaxPendingBatches = 256
	for i := 0; i < count; i++ {
		point := at.Add(time.Duration(i) * 10 * time.Minute)
		f, err := s.SaveFrame(point, "fixture", []byte("metadata fixture"), c)
		if err != nil {
			t.Fatal(err)
		}
		b := journal.Batch{SchemaVersion: 2, AppSource: "window_server", ID: journal.ID(point), Start: point, End: point.Add(time.Minute), MaxGapSeconds: 120, Frames: []journal.Frame{f}, Observations: []journal.Observation{{At: point, State: "sampled", FrameID: f.ID, App: f.App}}}
		if err = s.SaveDraft(b); err != nil {
			t.Fatal(err)
		}
		if err = s.SealDraft(b, c); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{0, 1, 12, 256} {
		args := []string{"--data", s.Root, "pending", "--json"}
		if limit > 0 {
			args = append(args, "--limit", fmt.Sprint(limit))
		}
		raw, err := commandOutput(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		var rows []struct {
			Batch   journal.Batch `json:"batch"`
			Total   int           `json:"queue_total"`
			More    bool          `json:"queue_has_more"`
			Preview bool          `json:"context_preview"`
		}
		if err = json.Unmarshal(raw, &rows); err != nil {
			t.Fatal(err)
		}
		expected := count
		if limit > 0 && limit < expected {
			expected = limit
		}
		if len(rows) != expected {
			t.Fatal("truncated default", len(rows))
		}
		for _, r := range rows {
			if r.Total != count || r.More != (expected < count) || !r.Preview {
				t.Fatal(r)
			}
			stored, _ := s.Batch(r.Batch.ID)
			if stored.ContextReady {
				t.Fatal("query froze context")
			}
		}
	}
	raw, err := commandOutput(t, "--data", s.Root, "pending", "--json", "--prepare", "--limit", "1")
	var prepared []struct {
		Batch   journal.Batch `json:"batch"`
		Preview bool          `json:"context_preview"`
	}
	decodeErr := json.Unmarshal(raw, &prepared)
	if err != nil || decodeErr != nil || len(prepared) != 1 || prepared[0].Preview || !prepared[0].Batch.ContextReady {
		t.Fatal(err, string(raw))
	}
	pending, _ := s.Pending(0)
	path, err := s.FramePath(pending[0].Frames[0])
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	raw, err = commandOutput(t, "--data", s.Root, "pending", "--json")
	var damaged []struct {
		Available bool     `json:"evidence_available"`
		Paths     []string `json:"image_paths"`
	}
	if err != nil || json.Unmarshal(raw, &damaged) != nil || len(damaged) != count || damaged[0].Available || len(damaged[0].Paths) != 0 || !damaged[1].Available {
		t.Fatal("broken evidence hid queue or misaligned paths", err)
	}
	empty := configStore(t)
	raw, err = commandOutput(t, "--data", empty.Root, "pending", "--json")
	if err != nil || strings.TrimSpace(string(raw)) != "[]" {
		t.Fatal("empty contract", err, string(raw))
	}
}
