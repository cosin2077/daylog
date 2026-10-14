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
	"sort"
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

// Codex strict schema providers require all declared properties to appear in
// required. Keep that transport constraint out of the permissive input parser.
func codexSchemaForBatch(b Batch) string {
	var schema map[string]any
	if json.Unmarshal([]byte(schemaForBatch(b)), &schema) != nil {
		return schemaForBatch(b)
	}
	var visit func(map[string]any)
	visit = func(node map[string]any) {
		if props, ok := node["properties"].(map[string]any); ok {
			keys := []string{}
			for key, value := range props {
				keys = append(keys, key)
				if child, ok := value.(map[string]any); ok {
					visit(child)
				}
			}
			sort.Strings(keys)
			node["required"] = keys
		}
		if item, ok := node["items"].(map[string]any); ok {
			visit(item)
		}
	}
	visit(schema)
	data, _ := json.Marshal(schema)
	return string(data)
}

func AnalysisPrompt() string { return analysisInstructions }

func (s *Store) analyzeAPI(ctx context.Context, id string, c Config, key string, client *http.Client, withinFallback bool) (err error) {
	if !c.AI.Enabled {
		return errors.New("API analysis is disabled; use the agent skill or configure ai.enabled")
	}
	if err := c.Validate(); err != nil {
		return err
	}
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
	reserved, err := s.reserveAttempt(id, c, attemptTime, withinFallback)
	if err != nil {
		return err
	}
	secrets := append(environmentSecrets(os.Environ()), key)
	event := AnalysisLog{Event: "request", RequestID: ID(attemptTime), BatchID: id, Source: "api", Model: c.AI.Model, Attempt: reserved.Attempts, Payload: map[string]any{"endpoint": c.AI.Endpoint, "model": c.AI.Model, "max_tokens": 1800, "input": logPayload(b)}}
	s.LogAnalysis(c, event, secrets)
	defer func() {
		event.Event = "response"
		event.Payload = nil
		event.ElapsedMS = time.Since(attemptTime).Milliseconds()
		if err != nil {
			if event.Error == "" {
				event.Error = err.Error()
			}
			if event.ErrorKind == "" {
				event.ErrorKind = "request_failed"
			}
			_ = s.Failed(id, event.ErrorKind)
		}
		s.LogAnalysis(c, event, secrets)
	}()

	response, err := safeClient.Do(httpRequest)
	if err != nil {
		event.ErrorKind = "network_or_timeout"
		event.Error = err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			event.ErrorKind = "timeout"
		}
		return errors.New("AI request failed or timed out")
	}
	defer response.Body.Close()
	event.HTTPStatus = response.StatusCode
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	event.Response = string(body)
	if err != nil || len(body) > 1<<20 {
		event.ErrorKind = "response_size"
		return errors.New("AI response is unreadable or too large")
	}
	if response.StatusCode != http.StatusOK {
		event.ErrorKind = "http_error"
		return fmt.Errorf("AI HTTP status %d", response.StatusCode)
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
		event.ErrorKind = "response_shape"
		return errors.New("AI response has an unsupported shape")
	}
	if envelope.Usage != nil {
		if err = s.RecordTokenUsage(attemptTime, *envelope.Usage); err != nil {
			return err
		}
	}
	if envelope.Choices[0].FinishReason != "stop" {
		event.ErrorKind = "incomplete_response"
		return errors.New("AI response did not finish; partial output was rejected")
	}
	if err = s.CommitAnalysis(id, []byte(envelope.Choices[0].Message.Content), "api", c.AI.Model); err != nil {
		event.ErrorKind = "invalid_report"
		return errors.New("AI report failed schema or evidence validation")
	}
	return nil
}
