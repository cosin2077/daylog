package notion

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type unavailableSecrets struct {
	calls atomic.Int32
	first chan struct{}
}

func (*unavailableSecrets) Set(string, string) error { return nil }
func (s *unavailableSecrets) Get(string) (string, error) {
	if s.calls.Add(1) == 1 {
		close(s.first)
	}
	return "", errors.New("fixture credential unavailable")
}

func TestMissingCredentialsWaitForNextSchedulerTick(t *testing.T) {
	root := t.TempDir()
	settings, err := NewSettings("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Asia/Shanghai", "2026-10-18")
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = true
	if err = SaveSettings(root, settings); err != nil {
		t.Fatal(err)
	}
	secrets := &unavailableSecrets{first: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); Run(ctx, root, secrets) }()
	defer func() { cancel(); <-done }()
	select {
	case <-secrets.first:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not try initial credential read")
	}
	time.Sleep(40 * time.Millisecond)
	if calls := secrets.calls.Load(); calls != 1 {
		t.Fatalf("missing credential created a busy retry loop: %d calls", calls)
	}
}
