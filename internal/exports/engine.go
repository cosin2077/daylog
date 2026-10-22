package exports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	LastCheckpoint string    `json:"last_checkpoint,omitempty"`
	SourceBatches  []string  `json:"source_batches,omitempty"`
	Hash           string    `json:"hash,omitempty"`
	URL            string    `json:"url,omitempty"`
	Attempts       int       `json:"attempts"`
	NextAttempt    time.Time `json:"next_attempt,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	BlockReason    string    `json:"block_reason,omitempty"`
	Blocked        bool      `json:"blocked,omitempty"`
	LastSuccess    time.Time `json:"last_success,omitempty"`
}
type State struct {
	ArchivedDays  int            `json:"archived_days"`
	ExpiredUnsent int            `json:"expired_unsent"`
	Blocked       bool           `json:"blocked,omitempty"`
	Error         string         `json:"error,omitempty"`
	Days          map[string]Job `json:"days"`
	PauseUntil    time.Time      `json:"pause_until,omitempty"`
}
type Engine struct {
	Root string
	Sink Sink
	Load Load
	Now  func() time.Time
}

func ReadState(root string) (State, error) {
	s := State{Days: map[string]Job{}}
	f, err := os.Open(filepath.Join(root, "sync-state.json"))
	if err == nil {
		defer f.Close()
		var raw []byte
		raw, err = io.ReadAll(io.LimitReader(f, (16<<20)+1))
		if err == nil {
			if len(raw) > 16<<20 {
				return s, errors.New("本地同步状态过大")
			}
			err = json.Unmarshal(raw, &s)
		}
	}
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
func (e Engine) RunDue(ctx context.Context, start, now time.Time, retention int, checkpoints ...int) error {
	latest := -1
	for _, minute := range checkpoints {
		if minute < 0 || minute >= 24*60 {
			return errors.New("同步时间须在 00:00 到 23:59 之间")
		}
		if minute <= now.Hour()*60+now.Minute() && minute > latest {
			latest = minute
		}
	}
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
		pruned := false
		for key, job := range state.Days {
			date, parseErr := time.ParseInLocation("2006-01-02", key, now.Location())
			if parseErr != nil {
				return errors.New("本地同步日期状态无效")
			}
			if date.Before(cutoff) {
				if job.LastSuccess.IsZero() && job.LastError == "" && job.Hash == "" {
					// An empty date checked at a daytime checkpoint had no upload.
				} else if job.LastSuccess.IsZero() || job.LastError != "" {
					state.ExpiredUnsent++
				} else {
					state.ArchivedDays++
				}
				delete(state.Days, key)
				pruned = true
			}
		}
		if pruned {
			if err = journal.WriteJSON(filepath.Join(e.Root, "sync-state.json"), state); err != nil {
				return err
			}
		}
		// A manually pushed older date remains eligible for late updates even
		// before --since, without exporting unrequested intervening history.
		var dates []time.Time
		for day := start; day.Before(today); day = day.AddDate(0, 0, 1) {
			dates = append(dates, day)
		}
		for key := range state.Days {
			day, _ := time.ParseInLocation("2006-01-02", key, now.Location())
			if day.Before(start) && !day.Before(cutoff) && day.Before(today) {
				dates = append(dates, day)
			}
		}
		sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
		count := 0
		checkpoint := ""
		if latest >= 0 && !today.Before(start) {
			checkpoint = fmt.Sprintf("%02d:%02d", latest/60, latest%60)
			job := state.Days[today.Format("2006-01-02")]
			if job.LastCheckpoint < checkpoint || job.LastError != "" {
				// Prioritize today's backup over a historical catch-up backlog.
				dates = append([]time.Time{today}, dates...)
			}
		}
		for _, day := range dates {
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
				job.Attempts++
				job.LastError = "无法读取该日期的本地记录，请检查本地数据完整性；其他日期会继续同步"
				job.NextAttempt = now.Add(syncBackoff(job.Attempts))
				state.Days[key] = job
				if err = journal.WriteJSON(filepath.Join(e.Root, "sync-state.json"), state); err != nil {
					return err
				}
				count++
				if count >= 4 {
					break
				}
				continue
			}
			snapshot.Ongoing = day.Equal(today)
			unchanged := job.Hash == snapshot.Hash() && job.LastError == ""
			if !snapshot.HasData() || unchanged {
				if snapshot.Ongoing && job.LastError == "" && job.LastCheckpoint < checkpoint {
					job.LastCheckpoint = checkpoint
					state.Days[key] = job
					if err = journal.WriteJSON(filepath.Join(e.Root, "sync-state.json"), state); err != nil {
						return err
					}
				}
				continue
			}
			e.deliver(ctx, &state, snapshot, now, false)
			if snapshot.Ongoing && state.Days[key].LastError == "" && state.Days[key].LastCheckpoint < checkpoint {
				job = state.Days[key]
				job.LastCheckpoint = checkpoint
				state.Days[key] = job
			}
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
		snapshot.Ongoing = now.In(day.Location()).Before(day.AddDate(0, 0, 1))
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
	previous := job.SourceBatches
	manifest, manifestErr := e.readManifest(day.Date)
	if manifestErr != nil && !errors.Is(manifestErr, os.ErrNotExist) {
		job.LastError = "无法读取本地同步来源索引，已保留云端原记录"
		job.Blocked = true
		job.BlockReason = "source_incomplete"
		state.Days[day.Date] = job
		return
	}
	if manifestErr == nil {
		previous = manifest
	}
	present := map[string]bool{}
	for _, id := range day.SourceBatches {
		present[id] = true
	}
	for _, id := range previous {
		if !present[id] {
			job.LastError = "本地历史记录不完整，已保留云端原记录；请先检查本地保留期限"
			job.Blocked = true
			job.BlockReason = "source_incomplete"
			state.Days[day.Date] = job
			return
		}
	}

	if !force && job.Hash == day.Hash() && job.LastError == "" {
		return
	}
	// Persist the attempted coverage before sending. A remote commit may
	// outlive a lost response or local process crash; later retries must not
	// replace it with a snapshot missing those sources.
	if saveErr := e.writeManifest(day.Date, day.SourceBatches); saveErr != nil {
		job.LastError = "无法保存本地同步来源索引，未更新云端记录"
		job.NextAttempt = now.Add(time.Minute)
		state.Days[day.Date] = job
		return
	}
	url, err := e.Sink.Deliver(ctx, day)
	if err == nil {
		job.Hash = day.Hash()
		job.SourceBatches = nil
		for key, old := range state.Days {
			if old.Blocked && old.BlockReason == "provider" {
				old.Blocked = false
				old.NextAttempt = time.Time{}
				old.BlockReason = ""
				state.Days[key] = old
			}
		}
		job.BlockReason = ""
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
		delay := syncBackoff(job.Attempts)
		var failure Failure
		if errors.As(err, &failure) {
			if failure.RetryDelay() > delay {
				delay = failure.RetryDelay()
			}
			job.Blocked = failure.Permanent()
			if job.Blocked {
				job.BlockReason = "provider"
			}
			if job.Blocked {
				state.Blocked = true
				state.Error = err.Error()
			}
			if failure.RetryDelay() > 0 {
				state.PauseUntil = now.Add(failure.RetryDelay())
			}
		}
		job.NextAttempt = now.Add(delay)
		var absolute interface{ RetryAt() time.Time }
		if errors.As(err, &absolute) {
			until := absolute.RetryAt()
			if until.After(job.NextAttempt) {
				job.NextAttempt = until
			}
			if until.After(state.PauseUntil) {
				state.PauseUntil = until
			}
		}
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
			if job.BlockReason == "source_incomplete" {
				continue
			}
			job.Blocked = false
			job.BlockReason = ""
			job.NextAttempt = time.Time{}
			state.Days[key] = job
		}
		return journal.WriteJSON(filepath.Join(root, "sync-state.json"), state)
	})
}

func (e Engine) readManifest(date string) ([]string, error) {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return nil, err
	}
	var ids []string
	err := journal.ReadJSON(filepath.Join(e.Root, "manifests", date+".json"), &ids)
	return ids, err
}
func (e Engine) writeManifest(date string, ids []string) error {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return err
	}
	dir := filepath.Join(e.Root, "manifests")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return journal.WriteJSON(filepath.Join(dir, date+".json"), ids)
}

func syncBackoff(attempts int) time.Duration {
	delay := time.Minute * time.Duration(1<<min(max(attempts-1, 0), 8))
	if delay > 6*time.Hour {
		return 6 * time.Hour
	}
	return delay
}
