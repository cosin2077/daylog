package journal

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"daylog/internal/platform"
)

func TestControlPauseAndResumeAcknowledgeAppliedState(t *testing.T) {
	parent := ""
	if runtime.GOOS != "windows" {
		parent = "/tmp"
	}
	root, err := os.MkdirTemp(parent, "dlctl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	listener, cleanup, err := platform.ListenControl(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	workCtx, workCancel := context.WithCancel(context.Background())
	defer workCancel()
	r := Runner{Store: &Store{Root: root}, wake: make(chan struct{}, 1), canAnalyze: true, analysisCancel: workCancel}
	go func() { defer close(done); r.serve(ctx, listener, cancel) }()
	t.Cleanup(func() { cancel(); listener.Close(); <-done; cleanup() })
	for _, minutes := range []int{0, -1, 1441} {
		if _, err = SendControl(root, Control{Op: "pause", Minutes: minutes}); err == nil || !strings.Contains(err.Error(), "1–1440") {
			t.Fatal("server accepted an unbounded pause", err)
		}
	}
	before := time.Now()
	status, err := SendControl(root, Control{Op: "pause", Minutes: 30})
	if err != nil || !status.Paused || status.PauseUntil == nil || status.PauseUntil.Before(before.Add(30*time.Minute)) || status.PauseUntil.After(time.Now().Add(30*time.Minute)) {
		t.Fatal("pause acknowledged stale state", status, err)
	}
	if workCtx.Err() != context.Canceled {
		t.Fatal("pause acknowledged before cancellation")
	}
	var saved PauseState
	if err := ReadJSON(filepath.Join(root, "pause.json"), &saved); err != nil || !saved.Paused {
		t.Fatal("pause not persisted", err)
	}
	status, err = SendControl(root, Control{Op: "resume"})
	if err != nil || status.Paused || status.PauseUntil != nil || r.snapshot().Paused {
		t.Fatal("resume acknowledged stale state", status, err)
	}
	if err := os.Remove(filepath.Join(root, "pause.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "pause.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = SendControl(root, Control{Op: "pause", Minutes: 30}); err == nil || !r.snapshot().Paused {
		t.Fatal("pause storage failure resumed recording", err)
	}
	if _, err = SendControl(root, Control{Op: "resume"}); err == nil || !r.snapshot().Paused {
		t.Fatal("resume ignored storage failure", err)
	}

}
