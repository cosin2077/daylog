package journal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"daylog/internal/native"
)

type Runner struct {
	Store           *Store
	Config          Config
	Source          native.Source
	Key             func() string
	mu              sync.Mutex
	status          Status
	pauseUntil      time.Time
	paused          bool
	canAnalyze      bool
	analysisCancel  context.CancelFunc
	draft           Batch
	lastFingerprint Fingerprint
	lastApp         string
	lastFrame       string
	observedState   string
	observedApp     string
	lastTick        time.Time
	lastCleanup     time.Time
	lastInventory   time.Time
	wake            chan struct{}
}

type Control struct {
	Op      string `json:"op"`
	Minutes int    `json:"minutes,omitempty"`
}

func (r *Runner) snapshot() Status { r.mu.Lock(); defer r.mu.Unlock(); return r.status }

func (r *Runner) Run(ctx context.Context) error {
	if err := r.Config.Validate(); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(r.Store.Root, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("Daylog is already running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.wake = make(chan struct{}, 1)
	r.status = Status{PID: os.Getpid(), StartedAt: time.Now().UTC()}
	if err = ReadJSON(filepath.Join(r.Store.Root, "draft.json"), &r.draft); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("draft recovery failed")
	}
	if len(r.draft.Frames) > 0 {
		if err = r.seal(); err != nil {
			return err
		}
	}
	path := filepath.Join(r.Store.Root, "run.sock")
	// Holding daemon.lock proves an old socket cannot have a live owner.
	if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(path)
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	var workers sync.WaitGroup
	workers.Add(1)
	go func() { defer workers.Done(); r.serve(ctx, listener, cancel) }()
	workers.Add(1)
	go func() { defer workers.Done(); r.analysisLoop(ctx) }()
	defer func() {
		cancel()
		listener.Close()
		workers.Wait()
		r.noteState(time.Now(), "stopped", "")
		_ = r.seal()
		r.publish()
	}()
	for {
		delay := r.step(ctx, time.Now())
		r.publish()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-r.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (r *Runner) serve(ctx context.Context, listener net.Listener, cancel context.CancelFunc) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		// Bounded, serial local control requests: no arbitrary tool dispatch.
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		var command Control
		err = json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&command)
		if err == nil {
			r.mu.Lock()
			switch command.Op {
			case "status":
			case "pause":
				r.paused = true
				r.pauseUntil = time.Time{}
				if command.Minutes > 0 && command.Minutes <= 1440 {
					r.pauseUntil = time.Now().Add(time.Duration(command.Minutes) * time.Minute)
				}
			case "resume":
				r.paused = false
				r.pauseUntil = time.Time{}
			case "stop":
				cancel()
			default:
				err = errors.New("unsupported control operation")
			}
			r.mu.Unlock()
		}
		if err != nil {
			_ = json.NewEncoder(conn).Encode(map[string]string{"error": "invalid control request"})
		} else {
			_ = json.NewEncoder(conn).Encode(r.snapshot())
		}
		conn.Close()
		if command.Op != "status" {
			select {
			case r.wake <- struct{}{}:
			default:
			}
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func SendControl(root string, c Control) (Status, error) {
	var status Status
	conn, err := net.DialTimeout("unix", filepath.Join(root, "run.sock"), time.Second)
	if err != nil {
		return status, errors.New("Daylog is not running")
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err = json.NewEncoder(conn).Encode(c); err != nil {
		return status, err
	}
	err = json.NewDecoder(conn).Decode(&status)
	return status, err
}

func (r *Runner) setError(reason string) { r.mu.Lock(); r.status.Error = reason; r.mu.Unlock() }

func (r *Runner) step(ctx context.Context, now time.Time) time.Duration {
	env, err := r.Source.Environment(ctx)
	if err != nil {
		r.noteState(now, "environment_unavailable", "")
		r.setError("environment_unavailable")
		return time.Minute
	}
	p := PowerPolicy(r.Config, env)
	r.mu.Lock()
	if r.paused && !r.pauseUntil.IsZero() && !now.Before(r.pauseUntil) {
		r.paused = false
		r.pauseUntil = time.Time{}
	}
	paused := r.paused
	r.status.Paused = paused
	r.status.PauseUntil = nil
	if paused && !r.pauseUntil.IsZero() {
		at := r.pauseUntil
		r.status.PauseUntil = &at
	}
	r.canAnalyze = p.CanAnalyze && !paused
	activeCancel := r.analysisCancel
	r.mu.Unlock()
	if (!p.CanAnalyze || paused) && activeCancel != nil {
		activeCancel()
	}
	if paused || p.PauseReason != "" {
		state := p.PauseReason
		if paused {
			state = "paused"
		}
		r.noteState(now, state, "")
		if err = r.seal(); err != nil {
			r.setError("queue_full")
		} else {
			r.setError(p.PauseReason)
		}
		return p.Delay
	}
	if r.Config.IdleSeconds > 0 && env.IdleSeconds >= float64(r.Config.IdleSeconds) {
		r.noteState(now, "idle", "")
		r.mu.Lock()
		r.status.SkippedIdle++
		r.mu.Unlock()
		if err = r.seal(); err != nil {
			r.setError("queue_full")
		} else {
			r.setError("")
		}
		return p.Delay
	}
	for _, blocked := range r.Config.BlockedApps {
		if blocked == env.App {
			r.noteState(now, "blocked", "")
			r.mu.Lock()
			r.status.SkippedBlocked++
			r.mu.Unlock()
			if err = r.seal(); err != nil {
				r.setError("queue_full")
			} else {
				r.setError("")
			}
			return p.Delay
		}
	}
	if !env.Permission {
		r.noteState(now, "screen_permission_required", "")
		r.setError("screen_permission_required")
		return time.Minute
	}
	if !r.lastTick.IsZero() && now.Sub(r.lastTick) > 3*p.Delay {
		r.noteState(now, "missing", "")
		if err = r.seal(); err != nil {
			r.setError("queue_full")
			return p.Delay
		}
	}
	r.lastTick = now
	r.noteState(now, "active", env.App)
	if len(r.draft.Frames) > 0 && now.Sub(r.draft.Start) >= time.Duration(r.Config.BatchSeconds)*time.Second {
		r.draft.End = r.draft.Start.Add(time.Duration(r.Config.BatchSeconds) * time.Second)
		if err = r.seal(); err != nil {
			r.setError("queue_full")
			return p.Delay
		}
	}
	pending, err := r.Store.Pending(0)
	if err != nil {
		r.setError("storage_read_failed")
		return p.Delay
	}
	if len(pending) >= r.Config.MaxPendingBatches {
		r.noteState(now, "queue_full", "")
		r.setError("queue_full")
		return p.Delay
	}
	if now.Sub(r.lastCleanup) > 10*time.Minute {
		if err = r.Store.Cleanup(r.Config, now); err != nil {
			r.setError("cleanup_failed")
			return p.Delay
		}
		r.lastCleanup = now
	}
	// Polling the power/idle state does not imply taking a screenshot. Avoid
	// capture + JPEG + decode entirely during the minimum retention interval.
	r.mu.Lock()
	previousSave := r.status.LastSaved
	previousCapture := r.status.LastCapture
	r.mu.Unlock()
	captureInterval := time.Duration(r.Config.MinSaveSeconds) * time.Second
	if p.Delay > captureInterval {
		captureInterval = p.Delay
	}
	if (previousSave != nil && now.Sub(*previousSave) < captureInterval) || (previousCapture != nil && now.Sub(*previousCapture) < captureInterval) {
		return p.Delay
	}
	data, err := r.Source.Capture(ctx, native.CaptureOptions{Height: r.Config.Height, Quality: r.Config.Quality, WindowID: r.Config.WindowID, Blocked: r.Config.BlockedApps})
	if err != nil {
		if errors.Is(err, native.ErrBlocked) {
			r.noteState(now, "blocked", "")
			r.mu.Lock()
			r.status.SkippedBlocked++
			r.status.Error = ""
			r.mu.Unlock()
			return p.Delay
		}
		r.noteState(now, "capture_unavailable", "")
		r.setError("capture_unavailable_check_permission")
		return p.Delay
	}
	r.mu.Lock()
	interrupted := r.paused
	r.mu.Unlock()
	if interrupted || ctx.Err() != nil {
		return p.Delay
	}
	r.mu.Lock()
	r.status.Captures++
	at := now.UTC()
	r.status.LastCapture = &at
	r.mu.Unlock()
	fp, err := FingerprintJPEG(data)
	if err != nil {
		r.setError("invalid_capture")
		return p.Delay
	}
	app := env.App
	if r.Config.WindowID != 0 {
		app = fmt.Sprintf("window:%d", r.Config.WindowID)
	}
	if r.lastFrame != "" && app == r.lastApp && !VisualChanged(r.Config, fp, r.lastFingerprint) {
		r.mu.Lock()
		r.status.SkippedUnchanged++
		r.mu.Unlock()
		if err = r.observe(Observation{At: now.UTC(), App: app, FrameID: r.lastFrame, State: "sampled"}); err != nil {
			r.setError("observation_write_failed")
		} else {
			r.setError("")
		}
		return p.Delay
	}
	frame, err := r.Store.SaveFrame(now, app, data, r.Config)
	if err != nil {
		r.setError("raw_storage_full_or_write_failed")
		return p.Delay
	}
	if len(r.draft.Frames) == 0 {
		gap := int(2 * captureInterval / time.Second)
		if gap > r.Config.MaxGapSeconds {
			gap = r.Config.MaxGapSeconds
		}
		r.draft = Batch{SchemaVersion: 2, ID: ID(now), Start: now.UTC(), MaxGapSeconds: gap, SupportGapSeconds: r.Config.MaxGapSeconds}
	}
	frame.Change = Difference(fp, r.lastFingerprint)
	frame.Reason = "change"
	if r.lastFrame == "" {
		frame.Reason = "first"
	} else if app != r.lastApp {
		frame.Reason = "app_change"
	}
	r.draft.Frames = append(r.draft.Frames, frame)
	r.draft.End = now.UTC().Add(time.Second)
	r.lastFrame = frame.ID
	if err = r.observe(Observation{At: now.UTC(), App: app, FrameID: frame.ID, State: "sampled"}); err != nil {
		r.setError("draft_write_failed")
		return p.Delay
	}
	r.lastFingerprint = fp
	r.lastApp = app
	r.mu.Lock()
	r.status.Saved++
	r.status.StorageBytes += int64(len(data))
	at = now.UTC()
	r.status.LastSaved = &at
	r.status.Error = ""
	r.mu.Unlock()
	return p.Delay
}

func (r *Runner) seal() error {
	if len(r.draft.Frames) == 0 {
		return nil
	}
	if err := r.Store.SealDraft(r.draft, r.Config); err != nil {
		return err
	}
	r.draft = Batch{}
	r.lastFrame = ""
	return nil
}

func (r *Runner) publish() {
	pending, err := r.Store.Pending(0)
	if err == nil {
		r.mu.Lock()
		r.status.Pending = len(pending)
		r.mu.Unlock()
	}
	if time.Since(r.lastInventory) >= 10*time.Minute {
		size, err := r.Store.RawBytes()
		if err == nil {
			r.mu.Lock()
			r.status.StorageBytes = size
			r.mu.Unlock()
			r.lastInventory = time.Now()
		}
	}
	r.mu.Lock()
	r.status.UpdatedAt = time.Now().UTC()
	r.mu.Unlock()
}

func (r *Runner) analysisLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !r.Config.AI.Enabled && !r.Config.Agent.Enabled {
			continue
		}
		env, err := r.Source.Environment(ctx)
		if err != nil || !PowerPolicy(r.Config, env).CanAnalyze {
			continue
		}
		r.mu.Lock()
		allowed := r.canAnalyze
		r.mu.Unlock()
		if !allowed {
			continue
		}
		pending, err := r.Store.Pending(0)
		if err != nil {
			continue
		}
		for _, b := range pending {
			if b.Attempts >= 3 || (b.LastAttempt != nil && time.Since(*b.LastAttempt) < 15*time.Minute) {
				continue
			}
			workCtx, cancel := context.WithCancel(ctx)
			r.mu.Lock()
			r.analysisCancel = cancel
			r.mu.Unlock()
			if r.Config.AI.Enabled {
				key := ""
				if r.Key != nil {
					key = r.Key()
				}
				_ = r.Store.Analyze(workCtx, b.ID, r.Config, key, nil)
			} else {
				_ = r.Store.AnalyzeAgent(workCtx, b.ID, r.Config, "")
			}
			cancel()
			r.mu.Lock()
			r.analysisCancel = nil
			r.mu.Unlock()
			break
		}
	}
}
