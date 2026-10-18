// Opt-in real provider replay of the four reviewed, synthetic audit JPEGs.
// Never point this at user screenshots. Raw model interpretations and derived
// statistics are retained; the private store and all copied images are removed.
package main

import (
	"bufio"
	"context"
	"daylog/internal/journal"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func run() error {
	if len(os.Args) != 4 {
		return fmt.Errorf("usage: replay-reviewed-images REVIEWED_INPUT_DIR OUTPUT_DIR claude|codex|hermes")
	}
	input, out, agent := os.Args[1], os.Args[2], os.Args[3]
	if agent != "claude" && agent != "codex" && agent != "hermes" {
		return fmt.Errorf("unsupported agent")
	}
	root, err := os.MkdirTemp("", "daylog-semantic-replay-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	s, err := journal.Open(root)
	if err != nil {
		return err
	}
	c := journal.DefaultConfig()
	c.Agent.Order = []string{agent}
	c.Agent.TimeoutSeconds = 180
	c.Diagnostics.FailureNotifications = false
	at := time.Now().UTC().Add(-15 * time.Minute).Truncate(time.Second)
	b := journal.Batch{SchemaVersion: 2, AppSource: "synthetic_review", ID: journal.ID(at), Start: at, End: at.Add(6 * time.Minute), MaxGapSeconds: 180, SupportGapSeconds: 300}
	for i := 0; i < 4; i++ {
		data, e := os.ReadFile(filepath.Join(input, fmt.Sprintf("scene%d-720.jpg", i+1)))
		if e != nil {
			return e
		}
		f, e := s.SaveFrame(at.Add(time.Duration(i)*90*time.Second), "com.google.Chrome", data, c)
		if e != nil {
			return e
		}
		b.Frames = append(b.Frames, f)
		b.Observations = append(b.Observations, journal.Observation{At: f.At, App: f.App, FrameID: f.ID, State: "sampled"})
	}
	if err = s.SaveDraft(b); err != nil {
		return err
	}
	if err = s.SealDraft(b, c); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 195*time.Second)
	defer cancel()
	started := time.Now()
	analysisErr := s.AnalyzeAgent(ctx, b.ID, c, agent)
	reports, err := s.Reports(at, b.End.Add(time.Second))
	if err != nil {
		return err
	}
	var interpretations []string
	err = filepath.WalkDir(filepath.Join(root, "logs"), func(path string, d os.DirEntry, e error) error {
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 2<<20)
		for scanner.Scan() {
			var log journal.AnalysisLog
			if json.Unmarshal(scanner.Bytes(), &log) == nil && log.BatchID == b.ID && log.Response != "" {
				interpretations = append(interpretations, log.Response)
			}
		}
		return scanner.Err()
	})
	if err != nil {
		return err
	}
	overview := journal.BuildOverview(at, reports, nil, journal.Batch{})
	overview.Sample = true
	result := map[string]any{"agent": agent, "elapsed_ms": time.Since(started).Milliseconds(), "scope": "four reviewed synthetic images; constructed 90-second observations; no native capture", "interpretations": interpretations, "reports": reports, "period": overview.Views[0], "error": nil}
	if analysisErr != nil {
		result["error"] = analysisErr.Error()
	}
	if err = os.MkdirAll(out, 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(out, agent+"-replay.json"), raw, 0600); err != nil {
		return err
	}
	html, err := journal.RenderHTML(overview, "day")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(out, agent+"-report.html"), html, 0600); err != nil {
		return err
	}
	fmt.Printf("agent=%s reports=%d interpretations=%d success=%t\n", agent, len(reports), len(interpretations), analysisErr == nil)
	return analysisErr
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
