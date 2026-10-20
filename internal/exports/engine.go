package exports

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"daylog/internal/journal"
	"daylog/internal/platform"
)

func stringID(n int) string { return strconv.Itoa(n) }

// Sink hides provider-specific requests and reconciliation from the scheduler.
type Sink interface {
	Deliver(context.Context, Day) (string, error)
}
type Load func(time.Time) (Day, error)
type Failure interface {
	error
	RetryDelay() time.Duration
	Permanent() bool
}

type Job struct {
	SourceBatches []string  `json:"source_batches,omitempty"`
	Hash          string    `json:"hash,omitempty"`
	URL           string    `json:"url,omitempty"`
	Attempts      int       `json:"attempts"`
	NextAttempt   time.Time `json:"next_attempt,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	Blocked       bool      `json:"blocked,omitempty"`
	LastSuccess   time.Time `json:"last_success,omitempty"`
}
type State struct {
	Blocked    bool           `json:"blocked,omitempty"`
	Error      string         `json:"error,omitempty"`
	Days       map[string]Job `json:"days"`
	PauseUntil time.Time      `json:"pause_until,omitempty"`
}
type Engine struct {
	Root string
	Sink Sink
	Load Load
	Now  func() time.Time
}

func ReadState(root string) (State, error) {
	s := State{Days: map[string]Job{}}
	err := journal.ReadJSON(filepath.Join(root, "sync-state.json"), &s)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if s.Days == nil {
		s.Days = map[string]Job{}
	}
	return s, err
}

func (e Engine) locked(call func() error) error {
	if err := os.MkdirAll(e.Root, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(e.Root, "sync.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = platform.TryLock(f); err != nil {
		return err
	}
	defer platform.Unlock(f)
	return call()
}

// A persisted date is the source date, never reinterpreted as "yesterday" on
// retry. Calendar arithmetic also preserves 23/25-hour days at DST changes.
func (e Engine) RunDue(ctx context.Context, start, now time.Time, retention int) error {
	return e.locked(func() error {
		state, err := ReadState(e.Root)
		if err != nil {
			return err
		}
		if state.Blocked || now.Before(state.PauseUntil) {
			return nil
		}
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		cutoff := today.AddDate(0, 0, -retention)
		if start.Before(cutoff) {
			start = cutoff
		}
		count := 0
		for day := start; day.Before(today); day = day.AddDate(0, 0, 1) {
			if err = ctx.Err(); err != nil {
				return err
			}
			key := day.Format("2006-01-02")
			job := state.Days[key]
			if job.Blocked || now.Before(job.NextAttempt) {
				continue
			}
			snapshot, loadErr := e.Load(day)
			if loadErr != nil {
				return loadErr
			}
			if !snapshot.HasData() || job.Hash == snapshot.Hash() {
				continue
			}
			e.deliver(ctx, &state, snapshot, now, false)
			count++
			if err = journal.WriteJSON(filepath.Join(e.Root, "sync-state.json"), state); err != nil {
				return err
			}
			if state.Blocked || now.Before(state.PauseUntil) || count >= 4 {
				break
			}
		}
		return nil
	})
}

func (e Engine) Sync(ctx context.Context, day time.Time, force bool) (Job, error) {
	var result Job
	err := e.locked(func() error {
		state, err := ReadState(e.Root)
		if err != nil {
			return err
		}
		snapshot, err := e.Load(day)
		if err != nil {
			return err
		}
		if !snapshot.HasData() {
			return errors.New("该日期没有可同步的记录")
		}
		now := time.Now()
		if e.Now != nil {
			now = e.Now()
		}
		e.deliver(ctx, &state, snapshot, now, force)
		result = state.Days[snapshot.Date]
		if err = journal.WriteJSON(filepath.Join(e.Root, "sync-state.json"), state); err != nil {
			return err
		}
		if result.LastError != "" {
			return errors.New(result.LastError)
		}
		return nil
	})
	return result, err
}
func (e Engine) deliver(ctx context.Context, state *State, day Day, now time.Time, force bool) {
	job := state.Days[day.Date]
	if !force {
		present := map[string]bool{}
		for _, id := range day.SourceBatches {
			present[id] = true
		}
		for _, id := range job.SourceBatches {
			if !present[id] {
				job.LastError = "本地历史记录不完整，已保留云端原记录；请先检查本地保留期限"
				job.Blocked = true
				state.Days[day.Date] = job
				return
			}
		}
	}
	if !force && job.Hash == day.Hash() {
		return
	}
	url, err := e.Sink.Deliver(ctx, day)
	if err == nil {
		job.Hash = day.Hash()
		job.SourceBatches = append([]string(nil), day.SourceBatches...)
		state.Blocked = false
		state.Error = ""
		job.URL = url
		job.LastSuccess = now
		job.Attempts = 0
		job.LastError = ""
		job.NextAttempt = time.Time{}
		job.Blocked = false
	} else if ctx.Err() != nil {
		job.LastError = "同步被停止，将在下次运行时继续"
		job.NextAttempt = now.Add(time.Minute)
	} else {
		job.Attempts++
		job.LastError = err.Error()
		delay := time.Minute * time.Duration(1<<min(job.Attempts-1, 8))
		if delay > 6*time.Hour {
			delay = 6 * time.Hour
		}
		var failure Failure
		if errors.As(err, &failure) {
			if failure.RetryDelay() > delay {
				delay = failure.RetryDelay()
			}
			job.Blocked = failure.Permanent()
			if job.Blocked {
				state.Blocked = true
				state.Error = err.Error()
			}
			if failure.RetryDelay() > 0 {
				state.PauseUntil = now.Add(failure.RetryDelay())
			}
		}
		job.NextAttempt = now.Add(delay)
	}
	state.Days[day.Date] = job
}
func ValidateDay(date string, zone *time.Location) (time.Time, error) {
	d, err := time.ParseInLocation("2006-01-02", date, zone)
	if err != nil {
		return d, fmt.Errorf("日期须为 YYYY-MM-DD")
	}
	return d, nil
}

// A successful setup/check can make previously blocked jobs eligible again.
func Resume(root string) error {
	return (Engine{Root: root}).locked(func() error {
		state, err := ReadState(root)
		if err != nil {
			return err
		}
		state.Blocked = false
		state.Error = ""
		state.PauseUntil = time.Time{}
		for key, job := range state.Days {
			job.Blocked = false
			job.NextAttempt = time.Time{}
			state.Days[key] = job
		}
		return journal.WriteJSON(filepath.Join(root, "sync-state.json"), state)
	})
}
