package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"daylog/internal/journal"
)

func TestRestartPreservesPauseAndDoesNotReinstallService(t *testing.T) {
	until := time.Now().Add(time.Hour)
	for _, paused := range []bool{false, true} {
		previous := journal.Status{PID: 11, StartedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now(), Paused: paused}
		if paused {
			previous.PauseUntil = &until
		}
		current := previous
		var calls []string
		a := restartActions{managed: true, status: func() (journal.Status, error) { return current, nil },
			savePause: func(p bool, u *time.Time) error {
				calls = append(calls, "save_pause")
				if p != paused || !reflect.DeepEqual(u, previous.PauseUntil) {
					t.Fatal("changed persisted pause")
				}
				return nil
			},
			stop: func() error { calls = append(calls, "stop"); return nil },
			launch: func() error {
				calls = append(calls, "launch")
				current.PID = 12
				current.StartedAt = time.Now()
				return nil
			}}
		got, err := restartWith(context.Background(), a)
		if err != nil || got.PID != 12 || got.Paused != paused || !reflect.DeepEqual(calls, []string{"save_pause", "stop", "launch"}) {
			t.Fatal(got, calls, err)
		}
	}
}

func TestIdleConfigurationChangesOnlyRequestedSetting(t *testing.T) {
	s, err := journal.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	c, err := journal.ReadConfig(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	c.IdleSeconds = 300
	c.AI.APIKey = "fixture-sensitive-value"
	c.AI.KeyEnv = ""
	path := filepath.Join(s.Root, "config.json")
	if err = journal.WriteJSON(path, c); err != nil {
		t.Fatal(err)
	}
	if err = configure(s, []string{"--idle-minutes", "20"}, strings.NewReader(""), io.Discard, func(string) error { t.Fatal("unexpected credential write"); return nil }); err != nil {
		t.Fatal(err)
	}
	after, err := journal.ReadConfig(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	c.IdleSeconds = 1200
	if !reflect.DeepEqual(c, after) {
		t.Fatal("idle setting changed another configuration value")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = configure(s, []string{"--idle-minutes", "-1"}, strings.NewReader(""), io.Discard, nil); err == nil {
		t.Fatal("invalid idle threshold accepted")
	}
	now, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, now) {
		t.Fatal("invalid setting modified configuration", err)
	}
}

func TestRestartFailureNeverClaimsSuccess(t *testing.T) {
	for _, stage := range []string{"stopped", "foreground", "persist", "stop", "launch"} {
		t.Run(stage, func(t *testing.T) {
			calls := 0
			a := restartActions{managed: stage != "foreground", status: func() (journal.Status, error) {
				if stage == "stopped" {
					return journal.Status{}, errors.New("stopped")
				}
				return journal.Status{PID: 10, StartedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now()}, nil
			},
				savePause: func(bool, *time.Time) error {
					if stage == "persist" {
						return errors.New("persist")
					}
					return nil
				}, stop: func() error {
					if stage == "stop" {
						return errors.New("stop")
					}
					return nil
				}, launch: func() error { calls++; return errors.New("launch") }}
			if _, err := restartWith(context.Background(), a); err == nil {
				t.Fatal("failure reported as success")
			}
			if stage != "launch" && calls != 0 {
				t.Fatal("launched after failed precondition")
			}
		})
	}
	previous := journal.Status{PID: 10, StartedAt: time.Now(), UpdatedAt: time.Now()}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := waitRestartReady(ctx, func() (journal.Status, error) { return previous, nil }, previous); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("old process accepted as restarted", err)
	}
}
