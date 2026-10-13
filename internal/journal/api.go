package journal

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

//go:embed analysis_prompt.txt
var analysisInstructions string

//go:embed analysis_prompt_v1.txt
var legacyAnalysisInstructions string

func instructionsForBatch(b Batch) string {
	if b.SchemaVersion == 2 {
		return analysisInstructions
	}
	return legacyAnalysisInstructions
}

//go:embed analysis_schema.json
var interpretationSchema string

func schemaForBatch(b Batch) string {
	if b.SchemaVersion == 2 {
		return interpretationSchema
	}
	return reportSchema
}

func AnalysisPrompt() string { return analysisInstructions }

func (s *Store) Analyze(ctx context.Context, id string, c Config, key string, client *http.Client) error {
	if !c.AI.Enabled {
		return errors.New("API analysis is disabled; use the agent skill or configure ai.enabled")
	}
	if err := c.Validate(); err != nil {
		return err
	}
	// Serialize model calls even if a CLI invocation races the daemon.
	lock, err := os.OpenFile(filepath.Join(s.Root, "analysis.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("another analysis is running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	b, err := s.PrepareAnalysis(id)
	if err != nil {
		return err
	}
	parts := []map[string]any{}
	metadata := inputMetadata(b)
	parts = append(parts, map[string]any{"type": "text", "text": string(metadata)})
	var totalBytes int
	for _, f := range b.Frames {
		path, err := s.FramePath(f)
		if err != nil {
			_ = s.Failed(id, "evidence_unavailable")
			return errors.New("batch evidence unavailable")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return errors.New("batch evidence unavailable")
		}
		if err = checkFrameDigest(f, data); err != nil {
			return err
		}
		totalBytes += len(data)
		if totalBytes > 12<<20 {
			return errors.New("batch images exceed 12 MiB")
		}
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)}})
	}
	request := map[string]any{"model": c.AI.Model, "max_tokens": 1800, "messages": []map[string]any{
		{"role": "system", "content": instructionsForBatch(b)}, {"role": "user", "content": parts},
	}}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.AI.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return errors.New("invalid AI endpoint")
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if key != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+key)
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	// Never forward screenshots or keys through redirects.
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("AI redirects are not allowed") }
	attemptTime := time.Now()
	if _, err = s.ReserveAttempt(id, c, attemptTime); err != nil {
		return err
	}
	response, err := safeClient.Do(httpRequest)
	if err != nil {
		s.Failed(id, "network_or_timeout")
		return errors.New("AI request failed or timed out")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		s.Failed(id, "http_error")
		return fmt.Errorf("AI HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		s.Failed(id, "response_size")
		return errors.New("AI response is unreadable or too large")
	}
	var envelope struct {
		Usage   *TokenUsage `json:"usage"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err = json.Unmarshal(body, &envelope); err != nil || len(envelope.Choices) != 1 {
		s.Failed(id, "response_shape")
		return errors.New("AI response has an unsupported shape")
	}
	if envelope.Usage != nil {
		if err = s.RecordTokenUsage(attemptTime, *envelope.Usage); err != nil {
			return err
		}
	}
	if envelope.Choices[0].FinishReason != "stop" {
		s.Failed(id, "incomplete_response")
		return errors.New("AI response did not finish; partial output was rejected")
	}
	if err = s.CommitAnalysis(id, []byte(envelope.Choices[0].Message.Content), "api", c.AI.Model); err != nil {
		s.Failed(id, "invalid_report")
		return errors.New("AI report failed schema or evidence validation")
	}
	return nil
}
