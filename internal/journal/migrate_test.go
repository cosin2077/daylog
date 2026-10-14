//go:build !windows

package journal

import (
	"daylog/internal/platform"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrationPreservesReportsEvidenceAndRetryBudget(t *testing.T) {
	s := testStore(t)
	b := testBatch(t, s, time.Now())
	if err := s.Commit(testReport(b), "fixture"); err != nil {
		t.Fatal(err)
	}
	pending := testBatch(t, s, time.Now().Add(time.Minute))
	if _, err := s.ReserveAttempt(pending.ID, DefaultConfig(), time.Now()); err != nil {
		t.Fatal(err)
	}
	c := DefaultConfig()
	c.Quality = 72
	if err := WriteJSON(filepath.Join(s.Root, "config.json"), c); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(filepath.Dir(s.Root), ".daylog")
	moved, err := MigrateRoot(s.Root, newRoot)
	if err != nil || !moved {
		t.Fatalf("migration failed: %v", err)
	}
	newStore, err := Open(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadConfig(newRoot)
	if err != nil || got.Quality != 72 {
		t.Fatal("configuration changed")
	}
	after, err := newStore.Batch(pending.ID)
	if err != nil || after.Attempts != 1 {
		t.Fatal("pending attempts lost")
	}
	if _, err = newStore.FramePath(after.Frames[0]); err != nil {
		t.Fatal("relative evidence reference broken", err)
	}
	reports, err := newStore.Reports(b.Start, b.End)
	if err != nil || len(reports) != 1 {
		t.Fatal("report lost")
	}
	if _, err = os.Stat(s.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old data directory retained")
	}
	if moved, err = MigrateRoot(s.Root, newRoot); err != nil || moved {
		t.Fatal("migration not idempotent")
	}
}

func TestMigrationRefusesActiveWriterAndExistingDestination(t *testing.T) {
	s := testStore(t)
	newRoot := filepath.Join(filepath.Dir(s.Root), ".daylog")
	f, err := os.OpenFile(filepath.Join(s.Root, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = platform.TryLock(f); err != nil {
		t.Fatal(err)
	}
	if _, err = MigrateRoot(s.Root, newRoot); !errors.Is(err, ErrMigrationBusy) {
		t.Fatalf("active daemon migrated: %v", err)
	}
	platform.Unlock(f)
	if err = os.Mkdir(newRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = MigrateRoot(s.Root, newRoot); err == nil {
		t.Fatal("existing destination overwritten")
	}
	if _, err = ReadConfig(s.Root); err != nil {
		t.Fatal("original data lost")
	}
}
