package journal

import (
	"errors"
	"path/filepath"
	"time"
)

type TokenUsage struct {
	PromptTokens     *int64 `json:"prompt_tokens"`
	CompletionTokens *int64 `json:"completion_tokens"`
}
type DailyUsage struct {
	Requests          int   `json:"requests"`
	SubmittedImages   int   `json:"submitted_images"`
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	KnownUsageCalls   int   `json:"known_usage_calls"`
	UnknownUsageCalls int   `json:"unknown_usage_calls"`
}

func (s *Store) RecordTokenUsage(at time.Time, t TokenUsage) error {
	if t.PromptTokens == nil || t.CompletionTokens == nil {
		return nil
	}
	if *t.PromptTokens < 0 || *t.CompletionTokens < 0 || *t.PromptTokens > 1000000000 || *t.CompletionTokens > 1000000000 {
		return errors.New("invalid provider usage")
	}
	return s.locked(func() error {
		path := filepath.Join(s.Root, "usage", at.Local().Format("2006-01-02")+".json")
		var usage DailyUsage
		if err := ReadJSON(path, &usage); err != nil {
			return err
		}
		usage.InputTokens += *t.PromptTokens
		usage.OutputTokens += *t.CompletionTokens
		usage.KnownUsageCalls++
		if usage.UnknownUsageCalls > 0 {
			usage.UnknownUsageCalls--
		}
		return WriteJSON(path, usage)
	})
}
