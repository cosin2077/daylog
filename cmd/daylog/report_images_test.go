package main

import (
	"bytes"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"daylog/internal/journal"
)

func reportImagesPayload(t *testing.T, root string) string {
	t.Helper()
	html, err := os.ReadFile(filepath.Join(root, "reports", "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)id="report-data">(.*?)</script>`).FindSubmatch(html)
	if len(m) != 2 {
		t.Fatal("report payload missing")
	}
	return string(m[1])
}

func TestReportEmbedsScreenshotsUnlessNoImages(t *testing.T) {
	s := configStore(t)
	at := time.Date(2026, 10, 15, 10, 0, 0, 0, time.Local)
	saveUXReport(t, s, journal.Report{BatchID: journal.ID(at), Start: at, Cards: []journal.Card{{Title: "合成活动", Start: at, End: at, Evidence: []string{journal.ID(at)}}}})
	var frame bytes.Buffer
	if err := jpeg.Encode(&frame, image.NewRGBA(image.Rect(0, 0, 64, 40)), nil); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Root, "frames", at.Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, journal.ID(at)+".jpg"), frame.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := commandOutput(t, "--data", s.Root, "report", "--date", "2026-10-15", "--no-open"); err != nil {
		t.Fatalf("report failed: %s %v", out, err)
	}
	if payload := reportImagesPayload(t, s.Root); !strings.Contains(payload, `"id":"`+journal.ID(at)+`"`) || !strings.Contains(payload, "data:image/jpeg;base64,") {
		t.Fatal("default report did not embed the day's screenshot")
	}
	if out, err := commandOutput(t, "--data", s.Root, "report", "--date", "2026-10-15", "--no-open", "--no-images"); err != nil {
		t.Fatalf("report --no-images failed: %s %v", out, err)
	}
	if payload := reportImagesPayload(t, s.Root); strings.Contains(payload, "data:image/") || !strings.Contains(payload, `"disabled":true`) {
		t.Fatal("--no-images still embedded screenshots")
	}
}
