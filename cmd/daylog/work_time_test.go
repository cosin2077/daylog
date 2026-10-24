package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"daylog/internal/journal"
)

func TestAvailabilityOnlyEvidenceStillGeneratesReport(t *testing.T) {
	s := configStore(t)
	at := time.Date(2026, 10, 24, 10, 0, 0, 0, time.Local)
	var raw []byte
	for _, p := range []journal.Observation{{At: at, State: "screen_locked"}, {At: at.Add(10 * time.Minute), State: "stopped"}} {
		line, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(append(raw, line...), '\n')
	}
	dir := filepath.Join(s.Root, "observations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, at.Format("2006-01-02")+".jsonl"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	date := at.Format("2006-01-02")
	out, err := commandOutput(t, "--data", s.Root, "report", "--date", date, "--no-open")
	if err != nil || strings.Contains(string(out), "没有记录") {
		t.Fatal("availability evidence disappeared", string(out), err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "reports", "report.html")); err != nil {
		t.Fatal("zero-work report not generated", err)
	}
	out, err = commandOutput(t, "--data", s.Root, "today", "--date", date, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var today struct {
		Work journal.WorkTime `json:"work_time"`
	}
	if err = json.Unmarshal(out, &today); err != nil {
		t.Fatal(err)
	}
	if !today.Work.Available || today.Work.EstimatedSeconds != 0 || today.Work.AwaySeconds != 600 {
		t.Fatal("today and report disagree on zero-work evidence", today)
	}
}

func TestWorkLabelsKeepMissingDistinctFromZero(t *testing.T) {
	if workEstimateLabel(journal.WorkTime{Available: true, MissingSeconds: 600}) != "暂未估时" {
		t.Fatal("missing data presented as zero work")
	}
	if workEstimateLabel(journal.WorkTime{Available: true, AwaySeconds: 600}) != "0 秒" {
		t.Fatal("explicit away time presented as missing")
	}
}
