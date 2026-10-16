package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type AnalysisLog struct {
	At              time.Time `json:"at"`
	Event           string    `json:"event"`
	RequestID       string    `json:"request_id"`
	BatchID         string    `json:"batch_id"`
	Source          string    `json:"source"`
	Model           string    `json:"model,omitempty"`
	Attempt         int       `json:"attempt"`
	ElapsedMS       int64     `json:"elapsed_ms,omitempty"`
	Payload         any       `json:"payload,omitempty"`
	Response        string    `json:"response,omitempty"`
	Stderr          string    `json:"stderr,omitempty"`
	ErrorKind       string    `json:"error_kind,omitempty"`
	Error           string    `json:"error,omitempty"`
	HTTPStatus      int       `json:"http_status,omitempty"`
	ExitCode        *int      `json:"exit_code,omitempty"`
	UpstreamRetries int       `json:"upstream_retries,omitempty"`
	Truncated       bool      `json:"truncated,omitempty"`
}

var credentialText = regexp.MustCompile(`(?i)(authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|secret|password|token)["']?\s*[:=]\s*["']?[^\s,"'}]+`)
var urlCredentials = regexp.MustCompile(`(https?://)[^\s/@]+:[^\s/@]+@`)
var bearerText = regexp.MustCompile(`(?i)bearer\s+[^\s,"'}]+`)
var prefixedKey = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{8,}|ghp_[A-Za-z0-9_]+|github_pat_[A-Za-z0-9_]+)`)
var imageDataPattern = regexp.MustCompile(`data:image/[^;\s]+;base64,[A-Za-z0-9+/=]+`)
var largeBase64 = regexp.MustCompile(`[A-Za-z0-9+/]{512,}={0,2}`)

func redact(text string, secrets []string) string {
	for _, secret := range secrets {
		if len(secret) >= 6 {
			text = strings.ReplaceAll(text, secret, "<REDACTED>")
		}
	}
	text = urlCredentials.ReplaceAllString(text, "${1}<REDACTED>@")
	text = imageDataPattern.ReplaceAllString(text, "<image omitted>")
	text = largeBase64.ReplaceAllString(text, "<binary omitted>")
	text = bearerText.ReplaceAllString(text, "Bearer <REDACTED>")
	text = credentialText.ReplaceAllString(text, "$1=<REDACTED>")
	return prefixedKey.ReplaceAllString(text, "<REDACTED>")
}
func environmentSecrets(env []string) []string {
	var secrets []string
	for _, pair := range env {
		name, value, ok := strings.Cut(pair, "=")
		name = strings.ToUpper(name)
		if ok && (strings.Contains(name, "KEY") || strings.Contains(name, "TOKEN") || strings.Contains(name, "SECRET") || strings.Contains(name, "PASSWORD")) && len(value) >= 6 {
			secrets = append(secrets, value)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return secrets
}
func boundedLogText(text string, secrets []string, limit int) (string, bool) {
	text = redact(text, secrets)
	if len(text) > limit {
		return strings.ToValidUTF8(text[:limit], "�"), true
	}
	return text, false
}

func logPayload(b Batch) any {
	frames := make([]map[string]any, 0, len(b.Frames))
	for _, f := range b.Frames {
		frames = append(frames, map[string]any{"id": f.ID, "at": f.At, "app": f.App, "file": f.File, "sha256": f.SHA256})
	}
	return map[string]any{"instructions": instructionsForBatch(b), "output_schema": json.RawMessage(schemaForBatch(b)), "metadata": json.RawMessage(inputMetadata(b)), "images": frames, "image_content": "local references only; base64 omitted"}
}

func (s *Store) LogAnalysis(c Config, event AnalysisLog, secrets []string) {
	if !c.Diagnostics.Enabled {
		return
	}
	if err := s.appendAnalysisLog(c, event, secrets); err != nil {
		fmt.Fprintln(os.Stderr, "daylog: analysis_log_write_failed")
	}
}
func (s *Store) appendAnalysisLog(c Config, event AnalysisLog, secrets []string) error {
	event.At = time.Now().UTC()
	var cut bool
	event.Response, cut = boundedLogText(event.Response, secrets, 128<<10)
	event.Truncated = event.Truncated || cut
	event.Stderr, cut = boundedLogText(event.Stderr, secrets, 8<<10)
	event.Truncated = event.Truncated || cut
	event.Error, _ = boundedLogText(event.Error, secrets, 4096)
	// Sanitize nested string fields without corrupting the outer JSONL encoding.
	if event.Payload != nil {
		raw, err := json.Marshal(event.Payload)
		if err != nil {
			return err
		}
		var value any
		if err = json.Unmarshal(raw, &value); err != nil {
			return err
		}
		event.Payload = sanitizeValue(value, secrets)
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if int64(len(data)) > c.Diagnostics.MaxBytes {
		return fmt.Errorf("analysis log entry exceeds budget")
	}
	return s.locked(func() error {
		dir := filepath.Join(s.Root, "logs", "llm")
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		day := event.At.Local().Format("2006-01-02")
		path := filepath.Join(dir, day+".jsonl")
		if st, e := os.Stat(path); e == nil && st.Size()+int64(len(data)) > 1<<20 {
			// Unique rotation names preserve completed lines and keep writes bounded.
			if err = os.Rename(path, filepath.Join(dir, day+"-"+event.At.Format("150405.000000000")+".jsonl")); err != nil {
				return err
			}
		}
		if err = s.pruneAnalysisLogs(c, dir, int64(len(data))); err != nil {
			return err
		}
		f, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(data)
		closeErr := f.Close()
		if e != nil {
			return e
		}
		return closeErr
	})
}
func sanitizeValue(v any, secrets []string) any {
	switch x := v.(type) {
	case string:
		return redact(x, secrets)
	case []any:
		for i := range x {
			x[i] = sanitizeValue(x[i], secrets)
		}
		return x
	case map[string]any:
		for k, item := range x {
			switch strings.ToLower(k) {
			case "authorization", "api_key", "apikey", "access_token", "refresh_token", "password", "secret":
				x[k] = "<REDACTED>"
			default:
				x[k] = sanitizeValue(item, secrets)
			}
		}
		return x
	}
	return v
}
func (s *Store) pruneAnalysisLogs(c Config, dir string, reserve int64) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type item struct {
		path string
		size int64
		at   time.Time
	}
	var keep []item
	var total int64
	cutoff := time.Now().AddDate(0, 0, -c.Diagnostics.RetentionDays)
	for _, f := range files {
		if !f.Type().IsRegular() || !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		info, err := f.Info()
		if err != nil {
			return err
		}
		path := filepath.Join(dir, f.Name())
		if info.ModTime().Before(cutoff) {
			if err = os.Remove(path); err != nil {
				return err
			}
			continue
		}
		keep = append(keep, item{path, info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].at.Before(keep[j].at) })
	for _, f := range keep {
		if total+reserve <= c.Diagnostics.MaxBytes {
			break
		}
		if err = os.Remove(f.path); err != nil {
			return err
		}
		total -= f.size
	}
	return nil
}
