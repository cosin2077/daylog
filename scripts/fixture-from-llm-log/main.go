// Command fixture-from-llm-log rebuilds analysed reports from a daylog LLM
// call log so the task-centric HTML report can be reviewed without new model
// calls. It is a developer tool: it is not run by tests and it only reads the
// paths given on the command line.
//
//	go run ./scripts/fixture-from-llm-log -data COPY_OF_DATA_DIR LOG.jsonl...
//
// Each logged request carries the exact batch metadata (frames, window and
// task context) and each successful response the model's segments. The tool
// writes the batch manifest into the COPY's batches/ directory, attaches the
// observations recorded in that window and commits the response through
// Store.CommitAnalysis, so durations are derived by the journal package itself.
// Never point -data at a live data directory.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"daylog/internal/journal"
)

type logEvent struct {
	Event     string          `json:"event"`
	RequestID string          `json:"request_id"`
	BatchID   string          `json:"batch_id"`
	Source    string          `json:"source"`
	Model     string          `json:"model"`
	Error     string          `json:"error"`
	Response  json.RawMessage `json:"response"`
	Payload   *struct {
		Input struct {
			Images []struct {
				ID     string    `json:"id"`
				App    string    `json:"app"`
				At     time.Time `json:"at"`
				File   string    `json:"file"`
				SHA256 string    `json:"sha256"`
			} `json:"images"`
			Metadata struct {
				BatchID string                `json:"batch_id"`
				Start   time.Time             `json:"start"`
				End     time.Time             `json:"end"`
				Context []journal.TaskContext `json:"context"`
			} `json:"metadata"`
		} `json:"input"`
	} `json:"payload"`
}

type call struct {
	request  *logEvent
	response *logEvent
}

func main() {
	data := flag.String("data", "", "daylog data directory to write into (must be a copy)")
	appSource := flag.String("app-source", "window_server", "app_source recorded on rebuilt batches")
	flag.Parse()
	if *data == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/fixture-from-llm-log -data COPY_OF_DATA_DIR LOG.jsonl...")
		os.Exit(2)
	}
	root, err := filepath.Abs(*data)
	check(err)
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(root) == filepath.Join(home, ".daylog") {
		check(errors.New("refusing to write into the live ~/.daylog directory; use a copy"))
	}
	store, err := journal.Open(root)
	check(err)
	calls := map[string]*call{}
	for _, path := range flag.Args() {
		readLog(path, calls)
	}
	// Keep the last successful call per batch.
	chosen := map[string]*call{}
	for _, c := range calls {
		if c.request == nil || c.response == nil || c.response.Error != "" || len(c.response.Response) == 0 {
			continue
		}
		if prev, ok := chosen[c.request.BatchID]; !ok || prev.response.RequestID < c.response.RequestID {
			chosen[c.request.BatchID] = c
		}
	}
	ids := make([]string, 0, len(chosen))
	for id := range chosen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var committed, skipped int
	for _, id := range ids {
		if err := rebuild(store, chosen[id], *appSource); err != nil {
			skipped++
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", id, err)
			continue
		}
		committed++
	}
	fmt.Printf("committed %d analysed batches, skipped %d\n", committed, skipped)
}

func readLog(path string, calls map[string]*call) {
	f, err := os.Open(path)
	check(err)
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		var e logEvent
		if json.Unmarshal(scanner.Bytes(), &e) != nil || e.RequestID == "" {
			continue
		}
		c := calls[e.RequestID]
		if c == nil {
			c = &call{}
			calls[e.RequestID] = c
		}
		switch e.Event {
		case "request":
			if e.Payload != nil {
				ev := e
				c.request = &ev
			}
		case "response":
			ev := e
			c.response = &ev
		}
	}
	check(scanner.Err())
}

func rebuild(store *journal.Store, c *call, appSource string) error {
	in := c.request.Payload.Input
	meta := in.Metadata
	if meta.BatchID != c.request.BatchID || !meta.End.After(meta.Start) || len(in.Images) == 0 || len(in.Images) > 12 {
		return errors.New("request metadata incomplete")
	}
	b := journal.Batch{ID: meta.BatchID, SchemaVersion: 2, AppSource: appSource, Start: meta.Start, End: meta.End, Context: meta.Context, ContextReady: true, SupportGapSeconds: 300}
	for _, im := range in.Images {
		b.Frames = append(b.Frames, journal.Frame{ID: im.ID, At: im.At, App: im.App, File: im.File, SHA256: im.SHA256})
	}
	observations, err := store.ReadObservations(meta.Start.Add(-time.Second), meta.End.Add(time.Second))
	if err != nil {
		return err
	}
	for _, o := range observations {
		if o.At.Before(meta.Start) || o.At.After(meta.End) {
			continue
		}
		if o.MaxGapSeconds > b.MaxGapSeconds {
			b.MaxGapSeconds = o.MaxGapSeconds
		}
		b.Observations = append(b.Observations, o)
	}
	sort.SliceStable(b.Observations, func(i, j int) bool { return b.Observations[i].At.Before(b.Observations[j].At) })
	if len(b.Observations) > 2048 {
		b.Observations = b.Observations[:2048]
	}
	if b.MaxGapSeconds < 20 {
		b.MaxGapSeconds = 60
	}
	path := filepath.Join(store.Root, "batches", b.ID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := journal.WriteJSON(path, b); err != nil {
		return err
	}
	var text string
	if err := json.Unmarshal(c.response.Response, &text); err != nil {
		text = string(c.response.Response)
	}
	model := c.response.Model
	if model == "" {
		model = "fixture:" + c.response.Source
	}
	if err := store.CommitAnalysis(b.ID, []byte(text), c.response.Source, model); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
