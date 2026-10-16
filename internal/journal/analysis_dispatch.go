package journal

import (
	"context"
	"daylog/internal/platform"
	"errors"
	"net/http"
	"os"
	"path/filepath"
)

// One lock and one failure round across every configured provider. Each actual
// request still reserves its own daily call/image budget.
func (s *Store) analysisRound(ctx context.Context, id string, notifications bool, call func() error) error {
	lock, err := os.OpenFile(filepath.Join(s.Root, "analysis.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = platform.TryLock(lock); err != nil {
		return errors.New("another analysis is running")
	}
	defer platform.Unlock(lock)
	err = call()
	if err != nil && ctx.Err() == nil && !errors.Is(err, ErrDailyBudget) && !errors.Is(err, ErrRetryCooling) {
		if b, readErr := s.Batch(id); readErr == nil && b.LastError != "" {
			var notify func() error
			if notifications {
				notify = notifyAnalysisFailure
			}
			if finishErr := s.finishFailure(id, b.LastError, notify); finishErr != nil {
				return finishErr
			}
		}
	}
	return err
}

func (s *Store) Analyze(ctx context.Context, id string, c Config, key string, client *http.Client) error {
	return s.analysisRound(ctx, id, c.Diagnostics.FailureNotifications, func() error { return s.analyzeAPI(ctx, id, c, key, client, false) })
}
func (s *Store) AnalyzeAgent(ctx context.Context, id string, c Config, selected string) error {
	return s.analysisRound(ctx, id, c.Diagnostics.FailureNotifications, func() error { return s.analyzeAgent(ctx, id, c, selected) })
}
func (s *Store) AnalyzeConfigured(ctx context.Context, id string, c Config, key, selected string, client *http.Client) error {
	return s.analysisRound(ctx, id, c.Diagnostics.FailureNotifications, func() error {
		if selected != "" {
			return s.analyzeAgent(ctx, id, c, selected)
		}
		if c.AI.Enabled && !c.AI.FallbackAfterAgents {
			return s.analyzeAPI(ctx, id, c, key, client, false)
		}
		err := s.analyzeAgent(ctx, id, c, "")
		if err == nil || !c.AI.Enabled || !c.AI.FallbackAfterAgents || ctx.Err() != nil || errors.Is(err, ErrDailyBudget) || errors.Is(err, ErrRetryCooling) {
			return err
		}
		models := append([]string{c.AI.Model}, c.AI.FallbackModels...)
		seen := map[string]bool{}
		for _, model := range models {
			if seen[model] {
				continue
			}
			seen[model] = true
			c.AI.Model = model
			err = s.analyzeAPI(ctx, id, c, key, client, true)
			if err == nil || ctx.Err() != nil || errors.Is(err, ErrDailyBudget) {
				return err
			}
		}
		return err
	})
}
