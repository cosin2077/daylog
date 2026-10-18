package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"daylog/internal/journal"
)

func TestStartPreparesOnceAndRecoversPermissionOrPause(t *testing.T) {
	for _, tc := range []struct {
		name                                               string
		running, paused, permissionLost, deny, failInstall bool
		want                                               string
	}{
		{name: "first_run", want: "permission,install,clear_pause,launch"},
		{name: "already_running", running: true},
		{name: "resume", running: true, paused: true, want: "resume"},
		{name: "permission_refresh", running: true, permissionLost: true, want: "permission,stop,install,clear_pause,launch"},
		{name: "denied", deny: true, want: "permission"},
		{name: "install_failed", failInstall: true, want: "permission,install"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			running, paused, permissionLost := tc.running, tc.paused, tc.permissionLost
			var calls []string
			a := startActions{
				clearPause: func() error { calls = append(calls, "clear_pause"); paused = false; return nil },
				permission: func(context.Context, string) error {
					calls = append(calls, "permission")
					if tc.deny {
						return errors.New("denied")
					}
					return nil
				},
				install: func() error {
					calls = append(calls, "install")
					if tc.failInstall {
						return errors.New("install failed")
					}
					return nil
				},
				launch: func() error { calls = append(calls, "launch"); running = true; permissionLost = false; return nil },
				resume: func() error { calls = append(calls, "resume"); paused = false; return nil },
				stop:   func() error { calls = append(calls, "stop"); running = false; return nil },
				status: func() (journal.Status, error) {
					if !running {
						return journal.Status{}, errors.New("not running")
					}
					s := journal.Status{UpdatedAt: time.Now(), Paused: paused}
					if permissionLost {
						s.Error = "screen_permission_required"
					}
					return s, nil
				},
			}
			status, err := startWith(context.Background(), "fixture", a)
			if (err != nil) != (tc.deny || tc.failInstall) {
				t.Fatalf("unexpected result: %v", err)
			}
			if got := strings.Join(calls, ","); got != tc.want {
				t.Fatalf("calls %q; want %q", got, tc.want)
			}
			if err == nil && (status.Paused || status.Error != "") {
				t.Fatalf("start did not recover state: %+v", status)
			}
		})
	}
}

func TestTodayInitializesAndExplainsHowToStart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old; r.Close(); w.Close() }()
	err = execute([]string{"--data", root, "today"})
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "daylog start") {
		t.Fatalf("missing first-run guidance: %s", out)
	}
	if _, err = journal.ReadConfig(root); err != nil {
		t.Fatal("query still requires manual init", err)
	}
	if _, err = os.Stat(filepath.Join(root, "run.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("query started recording")
	}
}

func TestRuntimeReadyWaitsForPublishedStatus(t *testing.T) {
	calls := 0
	status, err := waitRuntimeReady(context.Background(), func() (journal.Status, error) {
		calls++
		if calls < 3 {
			return journal.Status{}, nil
		}
		return journal.Status{PID: 42, UpdatedAt: time.Now()}, nil
	}, false)
	if err != nil || calls != 3 || status.PID != 42 {
		t.Fatal("accepted launch acknowledgement before daemon readiness")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = waitRuntimeReady(ctx, func() (journal.Status, error) { return journal.Status{}, errors.New("not started") }, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("failed launch was reported as restarted")
	}
}

func TestRuntimeReadyAcceptsPreservedPause(t *testing.T) {
	status, err := waitRuntimeReady(context.Background(), func() (journal.Status, error) { return journal.Status{Paused: true, UpdatedAt: time.Now()}, nil }, true)
	if err != nil || !status.Paused {
		t.Fatal(status, err)
	}
}
